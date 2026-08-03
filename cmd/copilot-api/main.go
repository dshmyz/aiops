package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	mockMiddlewareURL := os.Getenv("COPILOT_MOCK_MIDDLEWARE_URL")
	baseRunner := newConfigurableReadRunner(mockMiddlewareURL)
	var readRunner execution.ReadRunner = baseRunner
	var writeExecutor execution.Executor = newConfigurableWriteExecutor(mockMiddlewareURL)
	var verifier execution.Verifier
	var capabilityRuntime capabilities.PublishedCapabilityRuntime
	capabilitiesConfigured := os.Getenv("COPILOT_CAPABILITIES_DIR") != ""
	capabilityAdapter := capabilities.NewHTTPAdapterWithConfig(nil, capabilities.AdapterConfig{
		MaxRetries:       3,
		InitialBackoff:   200 * time.Millisecond,
		MaxBackoff:       2 * time.Second,
		FailureThreshold: 5,
		ResetTimeout:     30 * time.Second,
	})
	if mockMiddlewareURL != "" {
		logger.Info("static read runner pointing to mock middleware", zap.String("url", mockMiddlewareURL))
	}
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
	// §4 工具生态扩展：把数据源直连工具（alert.query / event.query / task.query）
	// 并入 readRunner 链。event.query 走 audit 数据源，task.query 走定时任务数据源，
	// 均经 ReadOnlyService 治理边界（Lookup→policy→runner→audit）。
	readRunner = compositeReadRunner{
		inner: readRunner,
		byName: map[string]execution.ReadRunner{
			tools.AlertQuery: alertRunner,
			tools.EventQuery: eventReadRunner{svc: auditService},
			tools.TaskQuery:  taskReadRunner{svc: scheduledTaskService},
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

	// 借鉴-5: Runbook / 命令模板复用。播种内置 Runbook，供 assistant 低风险
	// 自动执行（命中 IntentPattern 的写操作跳过人工确认）。
	runbookStore := store.NewSQLRunbookStore(db)
	if err := store.SeedBuiltinRunbooks(serviceContext, runbookStore); err != nil {
		logger.Warn("seed builtin runbooks", zap.Error(err))
	}
	runbookLookup := httpapi.NewRunbookLookupAdapter(runbookStore)

	planner, compactor, formatter, promptRegistry, plannerMode, err := assistantPlannerFromEnv(serviceContext, assistant.EnvMapFromLookup(os.Getenv), knowledgeManager, skillLookup, auditService)
	if err != nil {
		logger.Fatal("configure assistant planner", zap.Error(err))
	}
	logger.Info("assistant planner mode", zap.String("mode", plannerMode))
	conversationStore := store.NewSQLAssistantConversationStore(db)
	assistantService := assistant.NewServiceWithCompactor(planner, readService, planService, conversationStore, compactor)
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
	// Wrap the diagnostics service with the orchestrator so multi-domain
	// requests (e.g. "kafka 和 minio 健康状态") are automatically split into
	// concurrent sub-diagnostics and merged into a single package. The
	// assistant service injects the user message into the diagnostic context;
	// the orchestrator reads it to decide whether to orchestrate or delegate.
	diagService := diagnostics.NewService(readService, nil)
	assistantService = assistantService.WithDiagnostics(orchestrator.New(diagService, 3, nil))
	notifier := buildNotifier()
	options := routerOptions(repository, assistantService, planService, executionService, capabilityManagerFromEnv(capabilityRuntime), auditService)
	options = append(options, httpapi.WithConversations(assistantService))
	options = append(options, httpapi.WithScheduledTasks(scheduledTaskService))
	options = append(options, httpapi.WithInspectionReports(inspectionReportStore))
	options = append(options, httpapi.WithNotifier(notifier))
	feedbackStore := store.NewSQLFeedbackStore(db)
	options = append(options, httpapi.WithFeedback(feedbackStore))
	options = append(options, httpapi.WithMCPService(httpapi.NewMCPServerService(mcpServerStore, mcpManager)))
	options = append(options, httpapi.WithAlertWebhook(httpapi.NewAlertWebhookService(alertSvc, auditService)))
	options = append(options, httpapi.WithAlertWebhookSecret(os.Getenv("COPILOT_ALERT_WEBHOOK_SECRET")))
	// Capability marketplace: a registry of shared, versioned capabilities. The
	// service probes the dialect so ratings use the right upsert on MySQL vs
	// SQLite; both engines get their tables from migrations/015 (MySQL) and
	// internal/store/db.go (SQLite).
	options = append(options, httpapi.WithMarketplace(marketplace.NewService(db)))
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
			ServerURL:     os.Getenv("COPILOT_CAS_SERVER_URL"),
			ServiceURL:    os.Getenv("COPILOT_CAS_SERVICE_URL"),
			SessionSecret: []byte(os.Getenv("COPILOT_JWT_HMAC_SECRET")),
		}
		var err error
		casAuth, err = httpapi.NewCASAuthenticator(casCfg)
		if err != nil {
			logger.Fatal("configure CAS authenticator", zap.Error(err))
		}
		casAuth.WithAliasExpander(aliasExpander)
		logger.Info("CAS authentication enabled", zap.String("mode", string(authMode)), zap.String("cas_server", casCfg.ServerURL))
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
	schedulerInstance.WithReportGeneration(inspectionReportStore, scheduler.NewReporter(scheduledTaskStore, nil, nil))
	go schedulerInstance.Start(serviceContext)
	if err := serveHTTP(serviceContext, listener, handler, db, metrics, accessLog); err != nil {
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

func capabilityManagerFromEnv(runtime capabilities.PublishedCapabilityRuntime) httpapi.CapabilityManagementService {
	dir := os.Getenv("COPILOT_CAPABILITIES_DIR")
	if dir == "" {
		return nil
	}
	return capabilities.NewManagerWithRuntime(dir, capabilities.NewHTTPAdapter(http.DefaultClient), runtime)
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

func healthHandler(api http.Handler, db *sql.DB, metrics *observability.Metrics, accessLog *observability.AccessLog) http.Handler {
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
	mux.Handle("/v1/", api)

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

func serveHTTP(ctx context.Context, listener net.Listener, api http.Handler, db *sql.DB, metrics *observability.Metrics, accessLog *observability.AccessLog) error {
	server := &http.Server{
		Handler:           healthHandler(api, db, metrics, accessLog),
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
	case tools.GlusterVolumeHealthRead:
		return map[string]any{"tool": tool.Name, "environment": input["environment"], "name": input["name"], "status": "warning", "capacity_pct": 82.5, "heal_pending": 12}, nil
	case tools.MinIOBucketHealthRead:
		return map[string]any{"tool": tool.Name, "environment": input["environment"], "name": input["name"], "status": "ok", "objects": 42000, "quota_pct": 61.2}, nil
	case tools.KafkaConsumerLagRead:
		return map[string]any{"tool": tool.Name, "environment": input["environment"], "name": input["name"], "status": "warning", "lag": 1840}, nil
	default:
		return map[string]any{"tool": tool.Name, "environment": input["environment"], "status": "available"}, nil
	}
}

// configurableReadRunner wraps the static stub but can proxy read calls to a
// real or mock middleware HTTP API when COPILOT_MOCK_MIDDLEWARE_URL is set.
// When the base URL is empty it falls back to the hardcoded stub data.
type configurableReadRunner struct {
	baseURL string
	client  *http.Client
}

func newConfigurableReadRunner(baseURL string) configurableReadRunner {
	return configurableReadRunner{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

// pathFor maps a static tool name to the middleware API path. The cluster
// segment is hardcoded to "default" because the static read runner's input
// schema only carries environment and name.
func (r configurableReadRunner) pathFor(toolName, name string) string {
	switch toolName {
	case tools.GlusterVolumeHealthRead:
		return fmt.Sprintf("/api/glusterfs/default/volumes/%s/status", name)
	case tools.MinIOBucketHealthRead:
		return fmt.Sprintf("/api/minio/default/buckets/%s/capacity", name)
	case tools.KafkaConsumerLagRead:
		return fmt.Sprintf("/api/kafka/default/consumer-groups/%s/lag", name)
	default:
		return ""
	}
}

func (r configurableReadRunner) Read(ctx context.Context, tool tools.Tool, input map[string]any) (map[string]any, error) {
	if r.baseURL == "" {
		return staticReadRunner{}.Read(ctx, tool, input)
	}
	name, _ := input["name"].(string)
	if name == "" {
		name, _ = input["name"].(string)
	}
	path := r.pathFor(tool.Name, name)
	if path == "" {
		return staticReadRunner{}.Read(ctx, tool, input)
	}
	url := r.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		runLogger := observability.LoggerFromContext(ctx)
		runLogger.Warn("configurable read runner HTTP call failed", zap.Error(err))
		return nil, fmt.Errorf("read call to middleware failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		runLogger := observability.LoggerFromContext(ctx)
		runLogger.Warn("configurable read runner non-200", zap.Int("status", resp.StatusCode))
		return nil, fmt.Errorf("read call to middleware returned status %d", resp.StatusCode)
	}
	var result map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10*1024)).Decode(&result); err != nil {
		runLogger := observability.LoggerFromContext(ctx)
		runLogger.Warn("configurable read runner decode failed", zap.Error(err))
		return nil, fmt.Errorf("decode read response: %w", err)
	}
	result["tool"] = tool.Name
	if _, ok := result["environment"]; !ok {
		result["environment"] = input["environment"]
	}
	return result, nil
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

// compositeReadRunner 按工具名把数据源直连工具（alert.query / event.query /
// task.query）路由到专用 runner，其余工具委托给 inner runner。保持非数据源
// 工具行为不变，避免改动 staticReadRunner。
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
			"id":          e.ID,
			"tool_name":   e.ToolName,
			"action":      e.Action,
			"decision":    e.Decision,
			"subject":     e.Subject,
			"created_at":  e.CreatedAt,
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

// configurableWriteExecutor wraps the static stub but can proxy write calls to
// a real or mock middleware HTTP API when COPILOT_MOCK_MIDDLEWARE_URL is set.
// When the base URL is empty it falls back to the hardcoded stub response.
type configurableWriteExecutor struct {
	baseURL string
	client  *http.Client
}

func newConfigurableWriteExecutor(baseURL string) configurableWriteExecutor {
	return configurableWriteExecutor{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// writePathFor maps a static tool name to the middleware API write path.
func (e configurableWriteExecutor) writePathFor(toolName string, input map[string]any) string {
	topic, _ := input["topic"].(string)
	switch toolName {
	case "kafka.topic.retention.set":
		return fmt.Sprintf("/api/kafka/default/topics/%s/retention", topic)
	default:
		return ""
	}
}

func (e configurableWriteExecutor) Execute(ctx context.Context, toolName string, input map[string]any) (map[string]any, error) {
	if e.baseURL == "" {
		return staticWriteExecutor{}.Execute(ctx, toolName, input)
	}
	path := e.writePathFor(toolName, input)
	if path == "" {
		return staticWriteExecutor{}.Execute(ctx, toolName, input)
	}
	url := e.baseURL + path
	bodyJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal write input: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("build write request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		runLogger := observability.LoggerFromContext(ctx)
		runLogger.Warn("configurable write executor HTTP call failed", zap.Error(err))
		return nil, fmt.Errorf("write call to middleware failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		runLogger := observability.LoggerFromContext(ctx)
		runLogger.Warn("configurable write executor non-200", zap.Int("status", resp.StatusCode))
		return nil, fmt.Errorf("write call to middleware returned status %d", resp.StatusCode)
	}
	var result map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10*1024)).Decode(&result); err != nil {
		runLogger := observability.LoggerFromContext(ctx)
		runLogger.Warn("configurable write executor decode failed", zap.Error(err))
		return nil, fmt.Errorf("decode write response: %w", err)
	}
	result["tool"] = toolName
	if _, ok := result["status"]; !ok {
		result["status"] = "applied"
	}
	return result, nil
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
