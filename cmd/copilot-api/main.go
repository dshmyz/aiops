package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/gracegaoya/ai-operations-copilot/internal/alert"
	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/autonomy"
	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/knowledge"
	"github.com/gracegaoya/ai-operations-copilot/internal/marketplace"
	"github.com/gracegaoya/ai-operations-copilot/internal/mcp"
	"github.com/gracegaoya/ai-operations-copilot/internal/notification"
	"github.com/gracegaoya/ai-operations-copilot/internal/observability"
	"github.com/gracegaoya/ai-operations-copilot/internal/orchestrator"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/prompt"
	"github.com/gracegaoya/ai-operations-copilot/internal/scheduler"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
	"github.com/gracegaoya/ai-operations-copilot/internal/webui"
	"go.uber.org/zap"
)

// compositeCapabilityRuntime fans hot-published capabilities out to both the
// read and write runners. Each runner filters by operation internally, so a
// single dispatch keeps the manager wiring simple while preserving the
// "read-only via read runner, writes via write runner" boundary.
type compositeCapabilityRuntime struct {
	read  *capabilities.CapabilityReadRunner
	write *capabilities.CapabilityWriteRunner
}

func (r *compositeCapabilityRuntime) AddPublishedCapability(capability capabilities.Capability) error {
	if err := r.read.AddPublishedCapability(capability); err != nil {
		return err
	}
	return r.write.AddPublishedCapability(capability)
}

// buildCapabilityRuntimes wires the read runner, write executor (which also
// implements execution.Verifier), and hot-publish runtime. When capabilities
// are not configured the function returns the static stubs so the rest of the
// server keeps working without a capability directory.
func buildCapabilityRuntimes(loaded []capabilities.Capability, adapter *capabilities.HTTPAdapter, capabilitiesConfigured bool, fallbackWriter execution.Executor) (execution.ReadRunner, execution.Executor, execution.Verifier, capabilities.PublishedCapabilityRuntime) {
	if !capabilitiesConfigured {
		return staticReadRunner{}, fallbackWriter, nil, nil
	}
	read := capabilities.NewCapabilityReadRunner(staticReadRunner{}, loaded, adapter)
	write := capabilities.NewCapabilityWriteRunnerWithVerifier(fallbackWriter, loaded, adapter, read)
	return read, write, write, &compositeCapabilityRuntime{read: read, write: write}
}

func main() {
	// Initialize structured logger first so all subsequent log calls are JSON.
	logger := observability.InitLogger(os.Getenv("COPILOT_LOG_LEVEL"))
	defer func() { _ = logger.Sync() }()

	// 启动时优先加载 .env 文件（如存在），已存在的真实环境变量优先级更高
	if err := loadDotEnv(".env"); err != nil {
		logger.Warn("load .env failed", zap.Error(err))
	}

	driver := os.Getenv("COPILOT_DATABASE_DRIVER")
	dsn := os.Getenv("COPILOT_DATABASE_DSN")
	db, err := store.OpenWithDriver(driver, dsn)
	if err != nil {
		logger.Fatal("open database", zap.Error(err))
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		logger.Fatal("connect to database", zap.Error(err))
	}
	if err := store.ApplyMigrationsForDriver(driver, db); err != nil {
		logger.Fatal("apply database migrations", zap.Error(err))
	}

	logger.Info("storage connection is ready", zap.String("driver", databaseDriverLabel(driver)))

	serviceContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTracer, err := observability.InitTracer(serviceContext, observability.Config{
		ServiceName:   "copilot-api",
		Exporter:      os.Getenv("COPILOT_OTEL_EXPORTER"),
		OTLPEndpoint:  os.Getenv("COPILOT_OTEL_OTLP_ENDPOINT"),
		SamplingRatio: 1.0,
	})
	if err != nil {
		logger.Fatal("init tracer", zap.Error(err))
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTracer(shutdownCtx)
	}()

	listener, err := net.Listen("tcp", httpAddress())
	if err != nil {
		logger.Fatal("listen for HTTP", zap.Error(err))
	}
	repository := store.NewMySQLActionPlanStore(db)
	auditService := audit.NewService(repository)
	// R6 事件准 - 防丢失：DB 写入失败时重试 + 本地 JSON 落盘兜底 + 后台重放，
	// DB 恢复后自动补齐，进程崩溃也不丢（启动时扫描 fallback 目录重放积压）。
	// 默认开启；COPILOT_AUDIT_FALLBACK_ENABLED=0 可关闭；COPILOT_AUDIT_FALLBACK_DIR 可覆盖目录。
	if os.Getenv("COPILOT_AUDIT_FALLBACK_ENABLED") != "0" {
		cfg := audit.DefaultFallbackConfig()
		if dir := os.Getenv("COPILOT_AUDIT_FALLBACK_DIR"); dir != "" {
			cfg.Dir = dir
		}
		auditService = auditService.WithFallback(cfg)
		logger.Info("audit fallback enabled", zap.String("dir", cfg.Dir))
	}
	loadedCapabilities, err := publishedCapabilitiesFromEnv()
	if err != nil {
		logger.Fatal("load published capabilities", zap.Error(err))
	}
	if len(loadedCapabilities) > 0 {
		logger.Info("loaded published capabilities", zap.Int("count", len(loadedCapabilities)))
	}
	// 缺口-2 + 借鉴-6: 外部 MCP 接入层 + 健康检查。启动时加载
	// COPILOT_MCP_SERVERS 配置，连接外部 MCP 服务器发现工具并注册到统一
	// 工具表；注册后执行启动时健康检查，不健康的服务器通过审计回调记录事件。
	// Best-effort：连接或注册失败只记录警告，不阻塞主服务启动。
	if count := registerMCPToolsFromEnv(serviceContext, logger, auditService); count > 0 {
		logger.Info("registered external MCP tools", zap.Int("count", count))
	}
	// MCP 热配置：DB 持久化 MCP 服务器配置，运行时通过 /v1/mcp/servers CRUD 管理，
	// POST /v1/mcp/servers/reload 触发增量注册/注销工具（无需重启服务）。
	// 启动时从 DB 加载已启用配置；环境变量（COPILOT_MCP_SERVERS）与 DB 配置
	// 共存时需保证服务器名不冲突，否则 Reload 记录警告但不阻塞启动。
	mcpServerStore := store.NewSQLMCPServerStore(db)
	mcpManager := mcp.NewManager(mcpServerStore, mcp.NewStdioLister()).WithEventEmitter(func(event mcp.MCPEvent) {
		// 收口3: 用 mcpEventToAuditEvent 统一转换，覆盖 unhealthy / degraded /
		// tools_changed 三类事件。原先 degraded 被静默丢弃（死常量）。
		auditEvt := mcpEventToAuditEvent(event, time.Now().UTC())
		if recordErr := auditService.Record(serviceContext, auditEvt); recordErr != nil {
			logger.Warn("record MCP reload audit event", zap.Error(recordErr), zap.String("server", event.ServerName))
		}
	})
	// best-effort：初始 Reload 失败只记录警告，不阻塞主服务启动。
	if err := mcpManager.Reload(serviceContext); err != nil {
		logger.Warn("initial MCP reload from DB", zap.Error(err))
	}
	var readRunner execution.ReadRunner = staticReadRunner{}
	var writeExecutor execution.Executor = staticWriteExecutor{}
	var verifier execution.Verifier
	var capabilityRuntime capabilities.PublishedCapabilityRuntime
	capabilitiesConfigured := os.Getenv("COPILOT_CAPABILITIES_DIR") != ""
	capabilityAdapter := capabilities.NewHTTPAdapterWithConfig(nil, capabilities.AdapterConfig{
		MaxRetries:       3,
		InitialBackoff:   200 * time.Millisecond,
		MaxBackoff:       2 * time.Second,
		FailureThreshold: 5,
		ResetTimeout:     30 * time.Second,
		// COPILOT_OPENAPI_INSECURE_SKIP_VERIFY=1 时，能力执行与抓取外部 OpenAPI/Swagger
		// 文档都跳过 TLS 证书校验（对接自签/内网 HTTPS 后端）。默认关闭（校验证书）。
		OpenAPIInsecureSkipVerify: os.Getenv("COPILOT_OPENAPI_INSECURE_SKIP_VERIFY") == "1",
	})
	if capabilitiesConfigured {
		readRunner, writeExecutor, verifier, capabilityRuntime = buildCapabilityRuntimes(loadedCapabilities, capabilityAdapter, true, writeExecutor)
	}
	readService := execution.NewReadOnlyService(readRunner, auditService)
	// 告警数据源接入层（告警准专项）：webhook 接入 + alert.query 查询工具。
	alertStore := store.NewSQLAlertStore(db)
	alertSvc := alert.NewService(alertStore)
	alertRunner := alertReadRunner{svc: alertSvc}
	scheduledTaskStore := store.NewSQLScheduledTaskStore(db)
	inspectionReportStore := store.NewSQLInspectionReportStore(db)
	scheduledTaskService := scheduler.NewService(scheduledTaskStore, readService, auditService, nil)
	// Runbook 存储提前构造：incident.view 需要在此并入 readRunner 链，runbook 是
	// 其证据来源之一（§借鉴-5 复用）。runbookLookup 适配器留在原处。
	runbookStore := store.NewSQLRunbookStore(db)
	if err := store.SeedBuiltinRunbooks(serviceContext, runbookStore); err != nil {
		logger.Warn("seed builtin runbooks", zap.Error(err))
	}
	// §4 工具生态扩展：把数据源直连工具（alert.query / event.query / task.query）
	// 并入 readRunner 链。event.query 走 audit 数据源，task.query 走定时任务数据源，
	// 均经 ReadOnlyService 治理边界（Lookup→policy→runner→audit）。
	incidentRunner := incidentViewReadRunner{
		alerts:       alertSvc,
		audit:        auditService,
		plans:        repository,
		schedules:    scheduledTaskService,
		runbooks:     runbookStore,
		capabilities: loadedCapabilities,
	}
	readRunner = compositeReadRunner{
		inner: readRunner,
		byName: map[string]execution.ReadRunner{
			tools.AlertQuery:   alertRunner,
			tools.EventQuery:   eventReadRunner{svc: auditService},
			tools.TaskQuery:    taskReadRunner{svc: scheduledTaskService},
			tools.IncidentView: incidentRunner,
		},
	}
	readService = execution.NewReadOnlyService(readRunner, auditService)
	// 重建 scheduledTaskService：readService 升级后需重新注入（定时任务执行走
	// ExecuteTrustedRead，使用最新 readService 的超时/审计行为）。
	scheduledTaskService = scheduler.NewService(scheduledTaskStore, readService, auditService, nil)
	planService := plans.NewService(repository, nil)
	executionService := execution.NewServiceWithVerifier(repository, writeExecutor, verifier)
	var knowledgeManager *knowledge.Manager
	if env := os.Getenv("COPILOT_KNOWLEDGE_EMBEDDER_BASE_URL"); env != "" {
		var embedder knowledge.Embedder
		if key := os.Getenv("COPILOT_KNOWLEDGE_EMBEDDER_API_KEY"); key != "" {
			embedder = knowledge.NewOpenAIEmbedder(key, env, os.Getenv("COPILOT_KNOWLEDGE_EMBEDDER_MODEL"))
		}
		knowledgeStore := knowledge.NewSQLStore(db)
		knowledgeManager = knowledge.NewManager(knowledgeStore, embedder, 3)
		// Wire execution → knowledge base ingestion: every completed execution
		// is automatically recorded as a RAG document for future retrieval.
		executionService.WithObservers(knowledge.NewExecutionIngester(knowledgeStore, embedder))
	}

	// Wire AIOps Skill store → Action Router. Skills are loaded by action code
	// so the ActionAwarePlanner can inject domain SOPs (evidence checklists,
	// query guides, etc.) into the planner prompt before capability matching.
	skillStore := store.NewSQLSkillStore(db)
	if err := store.SeedBuiltinSkills(serviceContext, skillStore); err != nil {
		logger.Warn("seed builtin skills", zap.Error(err))
	}
	skillLookup := httpapi.NewSkillLookupAdapter(skillStore)

	// runbook 存储已在 readRunner 链构建处提前构造；此处仅需复用其 lookup 适配器。
	runbookLookup := httpapi.NewRunbookLookupAdapter(runbookStore)

	planner, compactor, formatter, promptRegistry, plannerMode, err := assistantPlannerFromEnv(serviceContext, assistant.EnvMapFromLookup(os.Getenv), knowledgeManager, skillLookup, auditService)
	if err != nil {
		logger.Fatal("configure assistant planner", zap.Error(err))
	}
	logger.Info("assistant planner mode", zap.String("mode", plannerMode))
	conversationStore := store.NewSQLAssistantConversationStore(db)
	assistantService := assistant.NewServiceWithCompactor(planner, readService, planService, conversationStore, compactor)
	// 启用自治 agent 循环（多步链式执行 + 结果反馈重规划）。只有 LLM planner
	// （eino-openai）支持；确定性 planner 忽略历史，不启用。循环内只读工具自主
	// 链式执行，写工具在建 plan/审批处停下交还给人，绝不自动执行写。
	if strings.HasPrefix(plannerMode, "eino-openai") {
		assistantService = assistantService.WithAgentLoop(true)
	}
	// Wire second-stage response formatter. eino-openai 模式下 formatter 是
	// ChainedFormatter[LLM, Code]（LLM 复用 planner 的 chat model，失败回退代码兜底）；
	// deterministic 模式下 formatter 为 nil，退化为纯 CodeFallbackFormatter。
	if formatter != nil {
		assistantService.WithFormatter(formatter)
	} else {
		assistantService.WithFormatter(assistant.NewCodeFallbackFormatter())
	}
	// Wire dry-run preview: every pending write plan is auto-previewed and the
	// result is attached as a risk_notice block so operators see the impact
	// (affected resources, commands, warnings) before confirming. Handlers are
	// registered per write tool; unsupported tools are silently skipped.
	dryRunService := execution.NewDryRunService()
	dryRunService.Register(tools.TopicRetentionSet, execution.TopicRetentionDryRunHandler)
	assistantService.WithDryRunRunner(dryRunService)
	// 借鉴-5: 低风险 Runbook 自动执行。命中低风险 Runbook 的写操作创建已确认
	// plan 并立即执行，跳过人工确认。
	assistantService.WithRunbookRouter(assistant.NewRunbookRouter(runbookLookup))
	assistantService.WithExecutionRunner(executionService)
	// E2: Low-Risk Admission Controller。所有自动执行源（direct runbook / agent
	// loop 低风险写）共用同一准入门。默认 fail-closed：仅当显式开启
	// COPILOT_AUTONOMY_ENABLED=1 且工具在 COPILOT_AUTONOMY_LOW_RISK_TOOLS
	// 白名单时才自动执行；生产必须保持默认（关闭）。
	autonomyCfg, autonomyErr := autonomy.ConfigFromEnv(os.Getenv)
	if autonomyErr != nil {
		logger.Warn("autonomy config invalid; autonomy stays disabled (fail-closed)", zap.Error(autonomyErr))
	}
	// 所有自动执行源（direct runbook / agent loop / scheduler）共用同一 Controller，
	// 使每日上限与白名单按主体统一计数。每日上限用持久化 SQL limiter（autonomy_daily_limit
	// 表，按自然日滚动），多实例部署下并发自增不丢计数；db 为 nil 时退化为 NopLimiter
	// （每日上限不生效，但总开关 fail-closed 仍在）。
	var autonomyLimiter autonomy.DailyLimiter
	if db != nil {
		autonomyLimiter = autonomy.NewSQLDailyLimiter(db)
	}
	autonomyController := autonomy.NewController(autonomyCfg, autonomyLimiter).WithClock(time.Now)
	assistantService.WithAutonomy(autonomyController)
	if autonomyCfg.Enabled {
		logger.Warn("autonomous write execution ENABLED (COPILOT_AUTONOMY_ENABLED=1); verify this is intentional for the deployment")
	} else {
		logger.Info("autonomous write execution disabled (fail-closed default)")
	}
	// E2 Phase 3：定时 runbook 自动执行器。与 agent loop / 直接对话共用同一准入
	// Controller（fail-closed，见 autonomy/admission.go）。未开启时 runbook 定时任务
	// 执行会被准入门拒绝（决策 denied + run failed），不会静默放行。
	runbookExec := scheduler.NewRunbookAutoExecutor(runbookStore, planService, executionService, autonomyController)
	scheduledTaskService = scheduledTaskService.WithRunbookExecutor(runbookExec)
	// Wrap the diagnostics service with the orchestrator so multi-domain
	// requests (e.g. "kafka 和 minio 健康状态") are automatically split into
	// concurrent sub-diagnostics and merged into a single package. The
	// assistant service injects the user message into the diagnostic context;
	// the orchestrator reads it to decide whether to orchestrate or delegate.
	diagService := diagnostics.NewService(readService, nil).WithCapabilityResolver(diagnostics.NewCapabilityResolver(loadedCapabilities))
	assistantService = assistantService.WithDiagnostics(orchestrator.New(diagService, 3, nil))
	notifier := buildNotifier()
	// 构造能力存储：DB 可用时用 SQLCapabilityStore（多节点一致），否则退化为
	// FileCapabilityStore（单机文件模式，现有行为）。SeedFromYAML 仅在 DB 模式
	// 首次启动时执行（幂等，跳过已存在）。
	var capStore capabilities.CapabilityStore
	capDir := os.Getenv("COPILOT_CAPABILITIES_DIR")
	if db != nil {
		sqlCapStore := capabilities.NewSQLCapabilityStore(db)
		capStore = sqlCapStore
		if capDir != "" {
			if count, err := sqlCapStore.SeedFromYAML(serviceContext, capDir+"/published"); err != nil {
				logger.Warn("seed capabilities from YAML to DB", zap.Error(err))
			} else if count > 0 {
				logger.Info("seeded capabilities from YAML to DB", zap.Int("count", count))
			}
		}
	} else if capDir != "" {
		capStore = capabilities.NewFileCapabilityStore(capDir)
	}
	options := routerOptions(repository, assistantService, planService, executionService, capabilityManagerFromEnv(capStore, capabilityAdapter, capabilityRuntime, importEnricher(planner)), auditService)
	options = append(options, httpapi.WithConversations(assistantService))
	options = append(options, httpapi.WithScheduledTasks(scheduledTaskService))
	options = append(options, httpapi.WithInspectionReports(inspectionReportStore))
	options = append(options, httpapi.WithNotifier(notifier))
	feedbackStore := store.NewSQLFeedbackStore(db)
	options = append(options, httpapi.WithFeedback(feedbackStore))
	// Runbook 意图进化：反馈 → 可确认启用的 runbook 草稿。activate 时经
	// runbookStore 落 SQL，RunbookRouter 即时命中。
	options = append(options, httpapi.WithRunbookDrafts(httpapi.NewRunbookDraftService(runbookStore)))
	// 可调度的低风险 runbook 列表（E2 Phase 3：定时任务表单 run_kind=runbook 下拉）。
	options = append(options, httpapi.WithRunbooks(runbookStore))
	options = append(options, httpapi.WithMCPService(httpapi.NewMCPServerService(mcpServerStore, mcpManager)))
	// 告警 webhook：可选自动研判 + 自动建 plan（COPILOT_ALERT_AUTO_DIAGNOSE /
	// COPILOT_ALERT_AUTO_PLAN / COPILOT_ALERT_ACTIONS_JSON）。
	alertWebhook := httpapi.NewAlertWebhookService(alertSvc, auditService)
	if os.Getenv("COPILOT_ALERT_AUTO_DIAGNOSE") == "1" {
		// 优先用多步链式研判（alert.query → event.query → domain.read），
		// 回退到单步研判（仅调 domain.read）。
		chainDiag := alert.NewChainDiagnoser(diagService, alertSvc, readService.ExecuteRead)
		alertWebhook = alertWebhook.WithChainDiagnoser(chainDiag)
	}
	if os.Getenv("COPILOT_ALERT_AUTO_PLAN") == "1" {
		alertActions, loadErr := alert.LoadAlertActionsFromEnv()
		if loadErr != nil {
			logger.Warn("load alert actions", zap.Error(loadErr))
		} else if len(alertActions) > 0 {
			alertWebhook = alertWebhook.WithPlanCreator(alert.NewPlanCreator(planService, alertSvc), alertActions)
		}
	}
	options = append(options, httpapi.WithAlertWebhook(alertWebhook))
	options = append(options, httpapi.WithAlertWebhookSecret(os.Getenv("COPILOT_ALERT_WEBHOOK_SECRET")))
	// 告警查询：供 /v1/overview 统计活动告警数（*alert.Service 满足 AlertQueryService）。
	options = append(options, httpapi.WithAlertQuery(alertSvc))
	// Capability marketplace: a registry of shared, versioned capabilities. The
	// service probes the dialect so ratings use the right upsert on MySQL vs
	// SQLite; both engines get their tables from migrations/015 (MySQL) and
	// internal/store/db.go (SQLite).
	marketplaceSvc := marketplace.NewService(db)
	if knowledgeManager != nil {
		// 知识库可用时给能力市场开启语义检索：能力发布时按 AI 描述建索引，
		// 支持"我想重启 Kafka"这类自然语言查询。embedder 可能为 nil（此时
		// SemanticSearch 退化为子串检索，离线也可用）。
		marketplaceSvc.EnableSemantic(knowledgeManager.Store(), knowledgeManager.Embedder())
	}
	options = append(options, httpapi.WithMarketplace(marketplaceSvc))
	if promptRegistry != nil {
		options = append(options, httpapi.WithPromptRegistry(promptRegistry))
	}
	if knowledgeManager != nil {
		options = append(options, httpapi.WithKnowledge(httpapi.NewAdminKnowledgeService(knowledgeManager.Store(), knowledgeManager.Embedder())))
	}
	if os.Getenv("COPILOT_DEV_EXPOSE_CONFIRMATION_TOKEN") == "1" {
		logger.Warn("development confirmation tokens are exposed in assistant responses")
		options = append(options, httpapi.WithDevelopmentConfirmationTokens())
	}
	if os.Getenv("COPILOT_DEV_INJECT_ADMIN") == "1" {
		logger.Warn("development admin identity injection enabled: unauthenticated /v1 requests become admin (dev only, never enable in production)")
		options = append(options, httpapi.WithDevelopmentAdminIdentity())
	}
	authenticator := httpapi.NewHMACAuthenticator([]byte(os.Getenv("COPILOT_JWT_HMAC_SECRET")))

	// Wire environment alias expander: expands canonical environment identifiers
	// with their aliases during authentication so policy checks accept any name
	// the user might type (e.g. "生产" → "prod").
	aliasStore := store.NewSQLEnvironmentAliasStore(db)
	aliasExpander := httpapi.NewCachedAliasExpander(aliasStore, 5*time.Minute)
	authenticator.WithAliasExpander(aliasExpander)

	// Multi-mode authentication: jwt (default) / cas / both.
	authMode := httpapi.AuthMode(strings.ToLower(strings.TrimSpace(os.Getenv("COPILOT_AUTH_MODE"))))
	if authMode == "" {
		authMode = httpapi.AuthModeJWT
	}
	var casAuth *httpapi.CASAuthenticator
	if authMode == httpapi.AuthModeCAS || authMode == httpapi.AuthModeBoth {
		casCfg := httpapi.CASConfig{
			ServerURL:           os.Getenv("COPILOT_CAS_SERVER_URL"),
			ServiceURL:          os.Getenv("COPILOT_CAS_SERVICE_URL"),
			SessionSecret:       []byte(os.Getenv("COPILOT_JWT_HMAC_SECRET")),
			SessionTTL:          casSessionTTL(logger),
			DefaultRoles:        casJSONList("COPILOT_CAS_DEFAULT_ROLES", logger),
			DefaultEnvironments: casJSONList("COPILOT_CAS_DEFAULT_ENVIRONMENTS", logger),
		}
		var err error
		casAuth, err = httpapi.NewCASAuthenticator(casCfg)
		if err != nil {
			logger.Fatal("configure CAS authenticator", zap.Error(err))
		}
		casAuth.WithAliasExpander(aliasExpander)
		logger.Info("CAS authentication enabled",
			zap.String("mode", string(authMode)),
			zap.String("cas_server", casCfg.ServerURL),
			zap.Duration("session_ttl", casCfg.SessionTTL),
			zap.Strings("default_roles", casCfg.DefaultRoles),
			zap.Strings("default_environments", casCfg.DefaultEnvironments),
		)
	}
	multiAuth := httpapi.NewMultiAuthenticator(authMode, authenticator, casAuth)

	handler := httpapi.NewRouter(
		multiAuth,
		readService,
		options...,
	)
	handler = observability.RequestTracing(handler)

	// Rate limiting: per-subject (30 req/min) for authenticated requests,
	// per-IP (60 req/min) for unauthenticated fallback. Configurable via
	// COPILOT_RATE_LIMIT_SUBJECT and COPILOT_RATE_LIMIT_IP env vars.
	rateLimitCfg := httpapi.DefaultRateLimiterConfig()
	if v := os.Getenv("COPILOT_RATE_LIMIT_SUBJECT"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			rateLimitCfg.SubjectCapacity = n
			rateLimitCfg.SubjectRefillPS = n / 60.0
		}
	}
	if v := os.Getenv("COPILOT_RATE_LIMIT_IP"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			rateLimitCfg.IPCapacity = n
			rateLimitCfg.IPRefillPS = n / 60.0
		}
	}
	rateLimiter := httpapi.NewRateLimiter(rateLimitCfg)
	defer rateLimiter.Stop()
	handler = httpapi.RateLimitMiddleware(rateLimiter, multiAuth, handler)
	metrics := observability.NewMetrics()
	accessLog := observability.NewAccessLog()
	schedulerInstance := scheduler.New(scheduledTaskStore, readService, auditService, 60*time.Second, nil)
	schedulerInstance.WithRunbookExecutor(runbookExec).WithReportGeneration(inspectionReportStore, scheduler.NewReporter(scheduledTaskStore, nil, nil))
	go schedulerInstance.Start(serviceContext)
	// MCP Server：把已发布的能力作为 MCP 工具对外暴露，供外部 AI 客户端调用。
	// 启用条件：COPILOT_MCP_SERVER_ENABLED=1；与主 API 共享端口（挂载在 /mcp）。
	var mcpHandler http.Handler
	if mcpCfg := mcp.MCPServerEnvConfigFromEnv(); mcpCfg.Enabled {
		mcpSrv := mcp.NewMCPServer(capStore, readRunner, auditService)
		if initErr := mcpSrv.Init(serviceContext); initErr != nil {
			logger.Warn("mcp server init", zap.Error(initErr))
		}
		mcpHandler = mcpSrv.Handler()
		logger.Info("mcp server enabled", zap.String("path", "/mcp"))
	}
	if err := serveHTTP(serviceContext, listener, handler, db, metrics, accessLog, mcpHandler); err != nil {
		logger.Fatal("serve HTTP", zap.Error(err))
	}
	// 优雅退出：先停 audit 重放 goroutine（它依赖 db），再让 deferred db.Close 执行。
	// 顺序错误会导致重放 goroutine 在 db 关闭后仍访问 db，引发竞态。
	if err := auditService.Close(); err != nil {
		logger.Warn("close audit service", zap.Error(err))
	}
}

func publishedCapabilitiesFromEnv() ([]capabilities.Capability, error) {
	dir := os.Getenv("COPILOT_CAPABILITIES_DIR")
	if dir == "" {
		return []capabilities.Capability{}, nil
	}
	return capabilities.RegisterPublished(dir)
}

// casSessionTTL returns the CAS session-cookie TTL from
// COPILOT_CAS_SESSION_TTL (a Go duration like "8h", "30m"). Invalid or empty
// values fall back to the authenticator default (8h) with a warning — CAS SSO
// must never be blocked by a misconfigured optional knob.
func casSessionTTL(logger *zap.Logger) time.Duration {
	raw := strings.TrimSpace(os.Getenv("COPILOT_CAS_SESSION_TTL"))
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		logger.Warn("invalid COPILOT_CAS_SESSION_TTL, using default",
			zap.String("value", raw), zap.Error(err))
		return 0
	}
	return d
}

// casJSONList reads a JSON array string list from an env var (e.g. roles,
// environments). Empty / invalid values return nil so the authenticator's
// defaults [operator] / [prod,staging,dev] apply. Best-effort on purpose.
func casJSONList(env string, logger *zap.Logger) []string {
	raw := strings.TrimSpace(os.Getenv(env))
	if raw == "" {
		return nil
	}
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		logger.Warn("invalid CAS list env var, using default",
			zap.String("env", env), zap.String("value", raw), zap.Error(err))
		return nil
	}
	return list
}

// registerMCPToolsFromEnv 加载 COPILOT_MCP_SERVERS 环境变量（JSON 数组），
// 连接外部 MCP 服务器发现工具，并注册到统一工具表（缺口-2: 外部 MCP 接入层）。
// 注册后执行启动时健康检查，不健康/degraded 的服务器通过审计回调记录事件
// （借鉴-6: 外部 MCP 健康检查）。
//
// Best-effort：配置缺失/无效、连接失败、注册冲突时只记录警告并返回 0，
// 不阻塞主服务启动。返回成功注册的工具数量。
func registerMCPToolsFromEnv(ctx context.Context, logger *zap.Logger, auditService *audit.Service) int {
	raw := os.Getenv("COPILOT_MCP_SERVERS")
	if strings.TrimSpace(raw) == "" {
		return 0
	}
	configs, err := mcp.LoadConfigs(raw)
	if err != nil {
		logger.Warn("load MCP server configs", zap.Error(err))
		return 0
	}
	if len(configs) == 0 {
		return 0
	}
	lister := mcp.NewStdioLister()
	defs, err := mcp.Discover(ctx, lister, configs)
	if err != nil {
		logger.Warn("discover MCP tools", zap.Error(err))
		return 0
	}
	if len(defs) == 0 {
		return 0
	}
	if err := tools.RegisterDynamicTools(defs); err != nil {
		logger.Warn("register MCP tools", zap.Error(err))
		return 0
	}
	// 借鉴-6: 注册成功后执行启动时健康检查，把不健康/degraded 事件记入审计。
	// 审计回调把 mcp.MCPEvent 转成 audit.Event：unhealthy → mcp_health_unhealthy，
	// degraded → mcp_health_degraded。healthy 不记审计（减少噪声）。
	checker := mcp.NewHealthChecker(lister).WithEventEmitter(func(event mcp.MCPEvent) {
		action := audit.ActionMCPHealthDegraded
		decision := audit.DecisionPermitted
		if event.Type == mcp.EventTypeHealthUnhealthy {
			action = audit.ActionMCPHealthUnhealthy
			decision = audit.DecisionDenied
		}
		recordErr := auditService.Record(ctx, audit.Event{
			ID:        uuid.NewString(),
			Action:    action,
			Decision:  decision,
			ToolName:  event.ServerName,
			Metadata:  event.Metadata,
			CreatedAt: time.Now().UTC(),
		})
		if recordErr != nil {
			logger.Warn("record MCP health audit event", zap.Error(recordErr), zap.String("server", event.ServerName))
		}
	})
	reports := checker.HealthCheckAll(ctx, configs)
	for _, report := range reports {
		logger.Info("MCP health check",
			zap.String("server", report.ServerName),
			zap.String("status", string(report.Status)),
			zap.Int("tools", report.ToolCount),
			zap.Duration("latency", report.Latency),
			zap.String("error", report.Error),
		)
	}
	return len(defs)
}

// mcpEventToAuditEvent 把 mcp.MCPEvent 转成 audit.Event（收口3）。
// 覆盖三类事件：
//   - EventTypeHealthUnhealthy → ActionMCPHealthUnhealthy + DecisionDenied（连接失败）
//   - EventTypeHealthDegraded  → ActionMCPHealthDegraded + DecisionPermitted（连接成功但无工具，降级非失败）
//   - EventTypeToolsChanged    → ActionMCPToolsChanged + DecisionPermitted（正常运维变更）
//
// 抽成纯函数便于单元测试覆盖全部事件类型，避免退化成"死常量"。
func mcpEventToAuditEvent(event mcp.MCPEvent, now time.Time) audit.Event {
	action := audit.ActionMCPToolsChanged
	decision := audit.DecisionPermitted
	switch event.Type {
	case mcp.EventTypeHealthUnhealthy:
		action = audit.ActionMCPHealthUnhealthy
		decision = audit.DecisionDenied
	case mcp.EventTypeHealthDegraded:
		action = audit.ActionMCPHealthDegraded
		// degraded 是"连接成功但未暴露工具"，属降级而非失败，用 permitted。
		decision = audit.DecisionPermitted
	case mcp.EventTypeToolsChanged:
		// 默认值已设，显式列出便于完整性
	}
	return audit.Event{
		ID:        uuid.NewString(),
		Action:    action,
		Decision:  decision,
		ToolName:  event.ServerName,
		Metadata:  event.Metadata,
		CreatedAt: now,
	}
}

func assistantPlannerFromEnv(ctx context.Context, env map[string]string, aug assistant.KnowledgeAugmenter, skillLookup assistant.SkillLookup, auditService *audit.Service) (assistant.Planner, assistant.Compactor, assistant.ResponseFormatter, *prompt.Registry, string, error) {
	planner, compactor, formatter, registry, mode, err := assistant.NewPlannerFromEnvWithPrompts(ctx, env)
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	if ep, ok := planner.(*assistant.EinoPlanner); ok {
		if aug != nil {
			ep.WithKnowledge(aug)
		}
		// 缺口-5 / R1：LLM 调用审计。model 名从环境捕获（planner/formatter/
		// compactor 共享同一 chat model）。
		if modelName := strings.TrimSpace(env["COPILOT_OPENAI_MODEL"]); modelName != "" && auditService != nil {
			ep.WithLLMAudit(auditService, modelName)
			if c, ok := compactor.(*assistant.LLMCompactor); ok {
				c.WithLLMAudit(auditService, modelName)
			}
			if f, ok := formatter.(*assistant.ChainedFormatter); ok {
				f.WithLLMAudit(auditService, modelName)
			}
		}
	}
	// 包装顺序（由内到外）：EinoPlanner → CapabilityAwarePlanner → ActionAwarePlanner
	// Action 路由在最外层：先识别任务入口并注入 Skill SOP，再做能力匹配和执行。
	var capabilityPlanner assistant.Planner
	if ep, ok := planner.(*assistant.EinoPlanner); ok && ep != nil {
		// 使用 AI 参数提取器，当规则提取失败时用 LLM 提取
		extractor := assistant.NewLLMParamExtractor(ep.ChatModel())
		capabilityPlanner = assistant.NewCapabilityAwarePlannerWithExtractor(planner, extractor)
	} else {
		capabilityPlanner = assistant.NewCapabilityAwarePlanner(planner)
	}
	router := assistant.NewActionRouter(skillLookup)
	return assistant.NewActionAwarePlanner(capabilityPlanner, router), compactor, formatter, registry, mode + "+capabilities+actions", nil
}

// importEnricher 构造能力导入的 LLM 富化器。仅当 planner 是 eino（有 chat model）
// 时启用；否则返回 nil（导入走纯规则，不富化）。富化全程容错，不阻塞导入。
func importEnricher(planner assistant.Planner) capabilities.ImportEnricher {
	ep, ok := planner.(*assistant.EinoPlanner)
	if !ok || ep == nil || ep.ChatModel() == nil {
		return nil
	}
	return capabilities.NewLLMImportEnricher(assistant.NewChatCompleter(ep.ChatModel()))
}

// capabilityManagerFromEnv 构造能力管理 Manager。传入的 store 决定持久化后端：
//   - SQLCapabilityStore（db != nil）→ 多节点一致的运行时事实源
//   - FileCapabilityStore（db == nil）→ 单机文件模式（现有行为）
//
// 种子逻辑（SeedFromYAML）由 main() 在此处之外调用，因为需要 logger/serviceContext。
func capabilityManagerFromEnv(store capabilities.CapabilityStore, adapter *capabilities.HTTPAdapter, runtime capabilities.PublishedCapabilityRuntime, enricher capabilities.ImportEnricher) httpapi.CapabilityManagementService {
	if store == nil {
		return nil
	}
	manager := capabilities.NewManagerWithStore(store, adapter, runtime)
	if enricher != nil {
		manager = manager.WithEnricher(enricher)
	}
	return manager
}

func routerOptions(repository httpapi.ActionPlanQueryService, assistantService httpapi.AssistantService, planService httpapi.PlanConfirmationService, executionService httpapi.ExecutionService, capabilityService httpapi.CapabilityManagementService, auditService httpapi.AuditService) []httpapi.Option {
	options := []httpapi.Option{
		httpapi.WithAssistant(assistantService),
		httpapi.WithActionPlans(repository),
		httpapi.WithActionPlanConfirmation(planService, executionService),
		httpapi.WithAuditEvents(auditService),
	}
	if capabilityService != nil {
		options = append(options, httpapi.WithCapabilities(capabilityService))
	}
	return options
}

func databaseDriverLabel(driver string) string {
	if driver == "" {
		return "mysql"
	}
	return driver
}

func httpAddress() string {
	if address := os.Getenv("COPILOT_HTTP_ADDR"); address != "" {
		return address
	}
	return ":18080"
}

func healthHandler(api http.Handler, db *sql.DB, metrics *observability.Metrics, accessLog *observability.AccessLog, mcpHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(writer http.ResponseWriter, _ *http.Request) {
		if db == nil {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"status":"ready"}`))
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"status":"not ready","error":"database unreachable"}`))
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ready"}`))
	})
	if metrics != nil {
		mux.Handle("GET /metrics", metrics.Handler())
	}
	if mcpHandler != nil {
		mux.Handle("/mcp", mcpHandler)
	}
	mux.Handle("/v1/", api)
	// Fallback: serve the embedded SPA at the root. /v1/ and the health/metrics
	// routes win via ServeMux longest-prefix matching, so API 404s stay JSON.
	mux.Handle("/", webui.WebHandler())

	var handler http.Handler = mux
	if accessLog != nil {
		handler = accessLog.Middleware(handler)
	}
	handler = securityHeaders(handler)
	handler = cors(handler)
	return handler
}

// cors adds permissive CORS headers for browser access. In production the
// allowed origins should be restricted via COPILOT_CORS_ALLOWED_ORIGINS.
func cors(next http.Handler) http.Handler {
	allowedOrigins := os.Getenv("COPILOT_CORS_ALLOWED_ORIGINS")
	if allowedOrigins == "" {
		allowedOrigins = "*"
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Access-Control-Allow-Origin", allowedOrigins)
		writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
		writer.Header().Set("Access-Control-Max-Age", "3600")
		if request.Method == http.MethodOptions {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

// securityHeaders adds basic browser security headers to every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("X-XSS-Protection", "1; mode=block")
		writer.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(writer, request)
	})
}

func serveHTTP(ctx context.Context, listener net.Listener, api http.Handler, db *sql.DB, metrics *observability.Metrics, accessLog *observability.AccessLog, mcpHandler http.Handler) error {
	server := &http.Server{
		Handler:           healthHandler(api, db, metrics, accessLog, mcpHandler),
		ReadHeaderTimeout: 5 * time.Second,
	}

	shutdownStarted := make(chan struct{})
	shutdownResult := make(chan error, 1)
	serverStopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			close(shutdownStarted)
			shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			shutdownResult <- server.Shutdown(shutdownContext)
		case <-serverStopped:
		}
	}()

	err := server.Serve(listener)
	close(serverStopped)
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	select {
	case <-shutdownStarted:
		return <-shutdownResult
	default:
		return nil
	}
}

type staticReadRunner struct{}

func (staticReadRunner) Read(_ context.Context, tool tools.Tool, input map[string]any) (map[string]any, error) {
	switch tool.Name {
	case tools.QuerySystemPosture:
		// 系统态势聚合视图（借鉴-1）：返回多域摘要，让 operator 一眼看到整体状态。
		// 静态 stub 返回固定数据；生产环境由 configurableReadRunner 聚合各域 API。
		return map[string]any{
			"tool":           tool.Name,
			"environment":    input["environment"],
			"overall_status": "degraded",
			"domains": []map[string]any{
				{"domain": "kafka", "status": "warning", "summary": "1 consumer group lag超阈值"},
				{"domain": "glusterfs", "status": "warning", "summary": "1 volume 容量 82.5%"},
				{"domain": "minio", "status": "ok", "summary": "所有 bucket 健康"},
			},
		}, nil
	case tools.GlusterVolumeHealthRead, tools.MinIOBucketHealthRead, tools.KafkaConsumerLagRead:
		// 中间件读工具已外置为 YAML published 能力（examples/capabilities/published），
		// 经 CapabilityReadRunner + HTTPAdapter 执行，不落静态 stub。
		return map[string]any{"tool": tool.Name, "environment": input["environment"], "status": "unavailable"}, nil
	default:
		return map[string]any{"tool": tool.Name, "environment": input["environment"], "status": "available"}, nil
	}
}

// alertReadRunner 把 alert.query 解析到真实 AlertStore 查询（非 stub）。
// 让 AI 助手能回答"当前有哪些告警"。经 ReadOnlyService 治理边界执行，
// 自动获得 policy 鉴权 + 审计记录。
type alertReadRunner struct {
	svc *alert.Service
}

func (r alertReadRunner) Read(ctx context.Context, tool tools.Tool, input map[string]any) (map[string]any, error) {
	if tool.Name != tools.AlertQuery {
		return nil, fmt.Errorf("unsupported tool %q for alert read runner", tool.Name)
	}
	environment, _ := input["environment"].(string)
	alerts, err := r.svc.ListActive(ctx, environment, 50)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"tool":        tool.Name,
		"environment": environment,
		"alerts":      alerts,
		"count":       len(alerts),
	}, nil
}

// incidentViewReadRunner 是 incident.view 元工具的只读 runner（Phase 1）：给定
// 一个告警/资源身份，把告警本体、相关审计事件、相关定时巡检 run、可跑只读能力
// 与匹配 runbook 串成一张可回链的 incident 全景。alert 与其余证据无 FK，全部按
// (domain, resource_type, resource_name, environment, 时间窗) 软匹配。
type incidentViewReadRunner struct {
	alerts    *alert.Service
	audit     *audit.Service
	plans     store.ActionPlanStore // 反解 action_plan.input_json 确认资源
	schedules *scheduler.Service
	runbooks  interface {
		ListEnabledRunbooks(context.Context) ([]store.Runbook, error)
	}
	capabilities []capabilities.Capability
}

func (r incidentViewReadRunner) Read(ctx context.Context, tool tools.Tool, input map[string]any) (map[string]any, error) {
	if tool.Name != tools.IncidentView {
		return nil, fmt.Errorf("unsupported tool %q for incident view runner", tool.Name)
	}
	pivot, anchor, err := r.resolvePivot(ctx, input)
	if err != nil {
		return nil, err
	}

	// 时间窗：默认 [最早告警 fired_at-1h, 现在]，可被 since/until 覆盖。
	now := time.Now().UTC()
	since := now.Add(-1 * time.Hour)
	if !anchor.FiredAt.IsZero() && anchor.FiredAt.Before(since) {
		since = anchor.FiredAt.Add(-1 * time.Hour)
	}
	if s, ok := input["since"].(string); ok {
		if t, perr := time.Parse(time.RFC3339, s); perr == nil {
			since = t
		}
	}
	until := now
	if s, ok := input["until"].(string); ok {
		if t, perr := time.Parse(time.RFC3339, s); perr == nil {
			until = t
		}
	}

	// Leg B: 同窗同域审计事件（tool_name 前缀匹配，先取窗再 Go 内过滤）。
	page, err := r.audit.List(ctx, store.AuditFilter{
		CreatedAfter:  since,
		CreatedBefore: until,
		Limit:         200,
	})
	if err != nil {
		return nil, err
	}
	domain := pivot.Domain
	matchTool := func(name, action string) bool {
		if name == "" {
			return false
		}
		if action == audit.ActionAlertIngested {
			return false
		}
		return domain == "" || strings.HasPrefix(name, domain+".")
	}

	relatedAudit := make([]map[string]any, 0, len(page.Events))
	writeEvents := make([]map[string]any, 0, 8)
	matchedAuditIDs := make([]string, 0, len(page.Events))
	for _, e := range page.Events {
		if !matchTool(e.ToolName, e.Action) {
			continue
		}
		// 资源确认：命中 inspect 的可疑写/读工具需与 pivot.resource_name 对上，
		// 否则视为"同域异资源"排除。读工具名已知为读（操作 read），写入走 input 反解。
		resName := pivot.ResourceName
		if resName != "" && e.PlanID != "" {
			if ok := r.planHitsResource(ctx, e.PlanID, resName); !ok {
				continue
			}
		}
		event := map[string]any{
			"id":                e.ID,
			"tool_name":         e.ToolName,
			"action":            e.Action,
			"decision":          e.Decision,
			"subject":           e.Subject,
			"created_at":        e.CreatedAt,
			"action_plan_id":    e.PlanID,
			"tool_execution_id": e.ExecutionID,
			"trace_id":          e.TraceID,
		}
		relatedAudit = append(relatedAudit, event)
		if !r.isReadTool(e.ToolName) {
			writeEvents = append(writeEvents, event)
		}
		matchedAuditIDs = append(matchedAuditIDs, e.ID)
	}

	// Leg C: 相关定时巡检 run（audit_event_id 桥接，run 表唯一 FK-like 键）。
	scheduledRuns := r.relatedScheduledRuns(ctx, matchedAuditIDs)

	// Leg D: 可跑只读探测建议（只列不执行）。
	probes := r.availableProbes(pivot, input)

	// Leg E: runbook 匹配（intent_pattern 命中，按具体度给 confidence）。
	runbooks := r.matchRunbooks(ctx, pivot)

	return map[string]any{
		"tool":           tool.Name,
		"incident_id":    anchor.ID,
		"pivot":          map[string]any{"domain": pivot.Domain, "resource_type": pivot.ResourceType, "resource_name": pivot.ResourceName},
		"alert":          anchor,
		"timeline":       relatedAudit,
		"scheduled_runs": scheduledRuns,
		"probes":         probes,
		"runbooks":       runbooks,
		"recent_writes":  map[string]any{"count": len(writeEvents), "events": writeEvents},
		"counts":         map[string]any{"audit": len(relatedAudit), "scheduled_runs": len(scheduledRuns), "probes": len(probes), "runbooks": len(runbooks), "recent_writes": len(writeEvents)},
	}, nil
}

// resolvePivot 解析入参到 (domain, resource_type, resource_name) 与一个告警锚点。
// 支持 incident_id 直达，或按 domain/resource_name/environment 定位最近告警。
func (r incidentViewReadRunner) resolvePivot(ctx context.Context, input map[string]any) (struct {
	Domain       string
	ResourceType string
	ResourceName string
}, alert.Alert, error) {
	var pivot struct {
		Domain       string
		ResourceType string
		ResourceName string
	}
	environment, _ := input["environment"].(string)
	domain, _ := input["domain"].(string)
	resType, _ := input["resource_type"].(string)
	resName, _ := input["resource_name"].(string)

	var anchor alert.Alert
	alerts, aerr := r.alerts.Query(ctx, store.AlertFilter{Domain: domain, Environment: environment, Limit: 50})
	if aerr != nil {
		return pivot, alert.Alert{}, aerr
	}
	// 定位锚点：优先精确命中 resource_name 的告警；否则取该域最近一条作为锚点，
	// 让 pivot 的 domain/resource_type/resource_name 跟随告警补全。
	for _, a := range alerts {
		if resName == "" || a.ResourceName == resName {
			anchor = a
			break
		}
	}
	if anchor.ID == "" && len(alerts) > 0 {
		anchor = alerts[0]
	}
	if domain == "" && anchor.Domain != "" {
		domain = anchor.Domain
	}
	if resType == "" {
		resType = anchor.ResourceType
	}
	if resName == "" {
		resName = anchor.ResourceName
	}
	pivot.Domain = domain
	pivot.ResourceType = resType
	pivot.ResourceName = resName
	return pivot, anchor, nil
}

// planHitsResource 通过 action_plan.input_json 反解资源字段，确认计划确在
// pivot.resource_name 上操作。input 里任一字段值 == resource_name 即判定命中。
func (r incidentViewReadRunner) planHitsResource(ctx context.Context, planID, resourceName string) bool {
	plan, err := r.plans.GetPlan(ctx, planID)
	if err != nil || len(plan.InputJSON) == 0 {
		return true // 无 plan 可反解时不排除（保守放行）
	}
	var in map[string]any
	if err := json.Unmarshal(plan.InputJSON, &in); err != nil {
		return true
	}
	for _, v := range in {
		if s, ok := v.(string); ok && s == resourceName {
			return true
		}
	}
	return false
}

// isReadTool 依据已加载能力目录判断某工具名是否为只读读取（operation==read）。
// 目录外的工具（如元工具）不算写，避免误报。
func (r incidentViewReadRunner) isReadTool(name string) bool {
	for _, c := range r.capabilities {
		if c.Name == name {
			return c.Operation == tools.Read
		}
	}
	// 非能力目录工具：静态只读元工具也算读，其余按写保守算（Phase 1）。
	switch name {
	case tools.QuerySystemPosture, tools.AlertQuery, tools.EventQuery, tools.TaskQuery, tools.IncidentView:
		return true
	}
	return false
}

// relatedScheduledRuns 找到 audit_event_id 命中 matchedAuditIDs 的定时巡检 run。
// 逐任务 ListRuns 收集（任务数量级小，Phase 1 足够）。
func (r incidentViewReadRunner) relatedScheduledRuns(ctx context.Context, matchedAuditIDs []string) []map[string]any {
	if len(matchedAuditIDs) == 0 {
		return nil
	}
	want := make(map[string]bool, len(matchedAuditIDs))
	for _, id := range matchedAuditIDs {
		want[id] = true
	}
	tasks, _ := r.schedules.List(ctx, incidentQueryUser(), store.ScheduledTaskFilter{Limit: 200})
	out := make([]map[string]any, 0, 8)
	for _, task := range tasks {
		runs, err := r.schedules.ListRuns(ctx, incidentQueryUser(), task.ID, 50)
		if err != nil {
			continue
		}
		for _, run := range runs {
			if want[run.AuditEventID] {
				out = append(out, map[string]any{
					"id":             run.ID,
					"task_id":        run.TaskID,
					"status":         run.Status,
					"started_at":     run.StartedAt,
					"finished_at":    run.FinishedAt,
					"audit_event_id": run.AuditEventID,
				})
			}
		}
	}
	return out
}

// availableProbes 从能力目录筛出该 pivot 上可跑的只读探测，resource_name 映射到
// input_schema 占位。只列建议不执行（守写边界）。
func (r incidentViewReadRunner) availableProbes(pivot struct {
	Domain       string
	ResourceType string
	ResourceName string
}, input map[string]any) []map[string]any {
	if pivot.Domain == "" || pivot.ResourceType == "" {
		return nil
	}
	environment, _ := input["environment"].(string)
	out := make([]map[string]any, 0, 4)
	for _, c := range r.capabilities {
		if c.Domain != pivot.Domain || c.ResourceType != pivot.ResourceType || c.Operation != tools.Read {
			continue
		}
		probe := map[string]any{"tool_name": c.Name, "operation": "read"}
		fields := make(map[string]any)
		if environment != "" {
			fields["environment"] = environment
		}
		if pivot.ResourceName != "" {
			// 把 resource_name 填到最可能的 identity 字段（非 environment 的必填字符串）
			for k, f := range c.InputSchema {
				if k == "environment" || f.Type != "string" {
					continue
				}
				fields[k] = pivot.ResourceName
				break
			}
		}
		probe["input"] = fields
		out = append(out, probe)
	}
	return out
}

// matchRunbooks 用 intent_pattern（[]string 关键词）命中 pivot 的 domain/资源类型，
// 命中越具体 confidence 越高（resource_type 命中 > 仅 domain 命中）。
func (r incidentViewReadRunner) matchRunbooks(ctx context.Context, pivot struct {
	Domain       string
	ResourceType string
	ResourceName string
}) []map[string]any {
	runbooks, err := r.runbooks.ListEnabledRunbooks(ctx)
	if err != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(runbooks))
	for _, rb := range runbooks {
		if len(rb.IntentPattern) == 0 {
			continue
		}
		hitRes := false
		hitDomain := false
		for _, kw := range rb.IntentPattern {
			if pivot.ResourceType != "" && strings.Contains(kw, pivot.ResourceType) {
				hitRes = true
			}
			if pivot.Domain != "" && strings.Contains(kw, pivot.Domain) {
				hitDomain = true
			}
		}
		if !hitRes && !hitDomain {
			continue
		}
		conf := 0.6
		if hitRes {
			conf = 0.9
		}
		out = append(out, map[string]any{
			"slug":          rb.Slug,
			"name":          rb.Name,
			"risk_level":    rb.RiskLevel,
			"confidence":    conf,
			"tool_sequence": rb.ToolSequence,
		})
	}
	return out
}

// incidentQueryUser 是 incident.view 查询数据源时使用的身份（跨用户只读）。
func incidentQueryUser() identity.CurrentUser {
	return identity.CurrentUser{Subject: "system:incident-view", Roles: []string{"viewer"}}
}

// compositeReadRunner 按工具名把数据源直连工具（alert.query / event.query /
// task.query / incident.view）路由到专用 runner，其余工具委托给 inner runner。
// 保持非数据源工具行为不变，避免改动 staticReadRunner。
type compositeReadRunner struct {
	inner  execution.ReadRunner
	byName map[string]execution.ReadRunner // toolName -> specialized runner
}

func (c compositeReadRunner) Read(ctx context.Context, tool tools.Tool, input map[string]any) (map[string]any, error) {
	if r, ok := c.byName[tool.Name]; ok {
		return r.Read(ctx, tool, input)
	}
	return c.inner.Read(ctx, tool, input)
}

// taskQueryUser 是 task.query 工具内部查询定时任务时使用的身份。scheduler.Service
// 的 List/ListRuns 忽略 user 参数（运维跨用户只读查看是特性），这里传一个
// 最小身份即可。
func taskQueryUser() identity.CurrentUser {
	return identity.CurrentUser{Subject: "system:task-query", Roles: []string{"viewer"}}
}

// eventReadRunner 把 event.query 解析到 audit 事件数据源（事件中心工具）。
// 复用 audit.ParseSearchQuery 支持自然语言查询（如"上周谁拒绝了 plan"）。
type eventReadRunner struct {
	svc *audit.Service
}

func (r eventReadRunner) Read(ctx context.Context, tool tools.Tool, input map[string]any) (map[string]any, error) {
	if tool.Name != tools.EventQuery {
		return nil, fmt.Errorf("unsupported tool %q for event read runner", tool.Name)
	}
	environment, _ := input["environment"].(string)
	query, _ := input["query"].(string)
	filter := audit.ParseSearchQuery(query, time.Now().UTC())
	// 审计事件单条含 metadata，50 条会超过 read 响应 10KB 上限。降到 10 条。
	filter.Limit = 10
	page, err := r.svc.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	// 将 AuditEvent 转换为小写字段名的 map，匹配前端 ToolAnswerView 期望的格式
	events := make([]map[string]any, 0, len(page.Events))
	for _, e := range page.Events {
		events = append(events, map[string]any{
			"id":         e.ID,
			"tool_name":  e.ToolName,
			"action":     e.Action,
			"decision":   e.Decision,
			"subject":    e.Subject,
			"created_at": e.CreatedAt,
		})
	}
	return map[string]any{
		"tool":        tool.Name,
		"environment": environment,
		"query":       query,
		"events":      events,
		"count":       len(events),
	}, nil
}

// taskReadRunner 把 task.query 解析到定时任务数据源（任务中心工具）。
type taskReadRunner struct {
	svc *scheduler.Service
}

func (r taskReadRunner) Read(ctx context.Context, tool tools.Tool, input map[string]any) (map[string]any, error) {
	if tool.Name != tools.TaskQuery {
		return nil, fmt.Errorf("unsupported tool %q for task read runner", tool.Name)
	}
	environment, _ := input["environment"].(string)
	limit := 20
	if v, ok := input["limit"].(float64); ok {
		limit = int(v)
		if limit < 1 {
			limit = 1
		}
		if limit > 100 {
			limit = 100
		}
	}
	var enabled *bool
	if s, ok := input["status"].(string); ok {
		b := s == "enabled"
		enabled = &b
	}
	tasks, err := r.svc.List(ctx, taskQueryUser(), store.ScheduledTaskFilter{Enabled: enabled, Limit: limit})
	if err != nil {
		return nil, err
	}
	type taskView struct {
		ID         string                   `json:"id"`
		Name       string                   `json:"name"`
		Capability string                   `json:"capability"`
		Enabled    bool                     `json:"enabled"`
		NextRunAt  string                   `json:"next_run_at,omitempty"`
		LastStatus string                   `json:"last_status,omitempty"`
		Runs       []store.ScheduledTaskRun `json:"runs,omitempty"`
	}
	views := make([]taskView, 0, len(tasks))
	for _, task := range tasks {
		runs, _ := r.svc.ListRuns(ctx, taskQueryUser(), task.ID, 5)
		views = append(views, taskView{
			ID:         task.ID,
			Name:       task.Name,
			Capability: task.CapabilityName,
			Enabled:    task.Enabled,
			NextRunAt:  task.NextRunAt.Format(time.RFC3339),
			LastStatus: task.LastStatus,
			Runs:       runs,
		})
	}
	return map[string]any{
		"tool":        tool.Name,
		"environment": environment,
		"tasks":       views,
		"count":       len(views),
	}, nil
}

// buildNotifier constructs the notification chain from environment configuration.
// When COPILOT_FEISHU_WEBHOOK_URL is set, a FeishuNotifier is layered on top of
// the LogNotifier so confirmation requests reach a real IM channel. Otherwise
// the LogNotifier is used alone (local development default).
func buildNotifier() notification.Notifier {
	webhookURL := strings.TrimSpace(os.Getenv("COPILOT_FEISHU_WEBHOOK_URL"))
	if webhookURL == "" {
		return notification.NewLogNotifier()
	}
	return notification.NewMultiNotifier(
		notification.NewFeishuNotifier(webhookURL),
		notification.NewLogNotifier(),
	)
}

type staticWriteExecutor struct{}

func (staticWriteExecutor) Execute(_ context.Context, toolName string, input map[string]any) (map[string]any, error) {
	return map[string]any{
		"tool":        toolName,
		"environment": input["environment"],
		"topic":       input["topic"],
		"status":      "applied",
	}, nil
}

// loadDotEnv 解析 KEY=VALUE 格式的 .env 文件，把未在真实环境变量中设置的项
// 灌入 os.Setenv。已存在的环境变量优先级更高，文件不会覆盖。
// 文件不存在时静默返回 nil（开发环境可选）。
// 支持：# 注释、空行、值的单/双引号包裹（引号会被剥离）。
// 格式错误的行（无 =、key 为空）会被跳过，不报错。
func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		value = strings.TrimSpace(value)
		// 剥离包裹引号："..." 或 '...'
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		// 已存在且非空的环境变量优先级更高，不覆盖
		// （空字符串视为未设置，允许 .env 文件填充）
		if current := os.Getenv(key); current != "" {
			continue
		}
		os.Setenv(key, value)
	}
	return scanner.Err()
}
