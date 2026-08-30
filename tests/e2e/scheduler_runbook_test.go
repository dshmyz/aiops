package e2e_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/autonomy"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/scheduler"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// schedulerRunbookWriteTool 是一个低风险写工具（非内置），用于验证定时 runbook
// 自动执行的 permitted 链路。真实项目里这类工具来自已评审发布的中低风险模板。
const schedulerRunbookWriteTool = "minio.bucket.retention.set"

// schedulerRunbookWriteSlug 是使用上述低风险写工具的自定义 runbook 模板。
const schedulerRunbookWriteSlug = "minio-retention-low-risk"

// e2eSchedulerRunbookExecutor 是 execution.Executor 的替身：不产生真实副作用，
// 只把工具名回显进结果以证明「执行服务真的调用了 executor」。
type e2eSchedulerRunbookExecutor struct{}

func (e2eSchedulerRunbookExecutor) Execute(_ context.Context, toolName string, _ map[string]any) (map[string]any, error) {
	return map[string]any{"status": "succeeded", "tool": toolName}, nil
}

// TestSchedulerRunbookEndToEndPermitted 验证「创建 run_kind=runbook 定时任务 →
// 手动触发 → 真 RunbookAutoExecutor（真实 runbook store + autonomy + plans +
// execution）→ 执行成功 + 审计 permitted」的完整闭环（任务清单 #85）。
func TestSchedulerRunbookEndToEndPermitted(t *testing.T) {
	// 非并行：本测试动态注册低风险写工具并在结束时重置。
	// 与既有 capability/assistant e2e 测试共享工具注册表，顺序阶段避免竞态。
	registerSchedulerLowRiskWriteTool(t)
	db := openAssistantSQLite(t)
	repository := store.NewSQLActionPlanStore(db)
	auditService := audit.NewService(repository)
	runbookStore := store.NewSQLRunbookStore(db)
	if err := store.SeedBuiltinRunbooks(context.Background(), runbookStore); err != nil {
		t.Fatalf("seed builtin runbooks: %v", err)
	}
	// 加一个真正低风险写工具驱动的 runbook 模板（permitted 链路用）。
	if _, err := runbookStore.CreateRunbook(context.Background(), store.Runbook{
		Slug:         schedulerRunbookWriteSlug,
		Name:         "MinIO 保留期设置（低风险）",
		RiskLevel:    "low",
		IsBuiltin:    true,
		IsEnabled:    true,
		ToolSequence: []string{schedulerRunbookWriteTool},
	}); err != nil {
		t.Fatalf("create low-risk runbook: %v", err)
	}

	readService := execution.NewReadOnlyService(e2eReadRunner{}, auditService)
	// 计划创建与执行使用同一时钟（fake），保证新建计划在校验时未过期。
	fakeNow := func() time.Time {
		return time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	}
	executionService := execution.NewServiceWithClock(repository, e2eSchedulerRunbookExecutor{}, fakeNow)
	planService := plans.NewService(repository, plans.ClockFunc(fakeNow))
	controller := autonomy.NewController(autonomy.Config{
		Enabled:      true,
		DailyLimit:   100,
		LowRiskTools: map[string]bool{schedulerRunbookWriteTool: true},
	}, nil)
	runbookExec := scheduler.NewRunbookAutoExecutor(runbookStore, planService, executionService, controller)
	taskService := scheduler.NewService(store.NewSQLScheduledTaskStore(db), readService, auditService, nil).
		WithRunbookExecutor(runbookExec)

	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		readService,
		httpapi.WithActionPlans(repository),
		httpapi.WithScheduledTasks(taskService),
		httpapi.WithRunbooks(runbookStore),
	)

	// 1) 创建 run_kind=runbook 定时任务
	createBody := `{"name":"minio 保留期定时设置","run_kind":"runbook","runbook_slug":"` + schedulerRunbookWriteSlug + `","input":{"bucket":"archive","retention_days":90},"schedule_kind":"preset","preset":"daily","timezone":"Asia/Shanghai","enabled":true}`
	createReq := schedulerRunbookReq(t, http.MethodPost, "/v1/scheduled-tasks", createBody)
	createRes := httptest.NewRecorder()
	router.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusOK {
		t.Fatalf("create status = %d body = %s, want 200", createRes.Code, createRes.Body.String())
	}
	if !strings.Contains(createRes.Body.String(), `"run_kind":"runbook"`) ||
		!strings.Contains(createRes.Body.String(), `"runbook_slug":"`+schedulerRunbookWriteSlug+`"`) {
		t.Fatalf("create body = %s, want runbook task echoed", createRes.Body.String())
	}
	taskID := extractJSONString(createRes.Body.String(), `"id":"`)
	if taskID == "" {
		t.Fatalf("create body = %s, want non-empty task id", createRes.Body.String())
	}

	// 2) GET /v1/runbooks 只暴露 enabled + low（应包含我们的低风险模板，不含 medium 的告警序列）
	listReq := schedulerRunbookReq(t, http.MethodGet, "/v1/runbooks", "")
	listRes := httptest.NewRecorder()
	router.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list runbooks status = %d body = %s, want 200", listRes.Code, listRes.Body.String())
	}
	if !strings.Contains(listRes.Body.String(), `"configured":true`) {
		t.Fatalf("list runbooks body = %s, want configured:true", listRes.Body.String())
	}
	if !strings.Contains(listRes.Body.String(), schedulerRunbookWriteSlug) {
		t.Fatalf("list runbooks body = %s, want low-risk runbook listed", listRes.Body.String())
	}
	if strings.Contains(listRes.Body.String(), "alert-root-cause-sequence") {
		t.Fatalf("list runbooks body = %s, medium runbook must NOT be listed", listRes.Body.String())
	}

	// 3) 触发执行 → 真 RunbookAutoExecutor 走准入门 → 计划 + 执行 + 审计 permitted
	triggerReq := schedulerRunbookReq(t, http.MethodPost, "/v1/scheduled-tasks/"+taskID+"/run", "")
	triggerRes := httptest.NewRecorder()
	router.ServeHTTP(triggerRes, triggerReq)
	if triggerRes.Code != http.StatusOK {
		t.Fatalf("trigger status = %d body = %s, want 200", triggerRes.Code, triggerRes.Body.String())
	}
	if !strings.Contains(triggerRes.Body.String(), `"status":"succeeded"`) {
		t.Fatalf("trigger body = %s, want run status succeeded", triggerRes.Body.String())
	}
	if !strings.Contains(triggerRes.Body.String(), schedulerRunbookWriteTool) {
		t.Fatalf("trigger body = %s, want run result referencing tool %s", triggerRes.Body.String(), schedulerRunbookWriteTool)
	}

	// 4) 审计里有 permitted 的定时任务执行记录（computed 于 scheduler.recordAudit）
	permitted, err := auditService.List(context.Background(), store.AuditFilter{Decision: audit.DecisionPermitted})
	if err != nil {
		t.Fatalf("list permitted audit: %v", err)
	}
	foundScheduledSuccess := false
	for _, e := range permitted.Events {
		if e.Action == audit.ActionScheduledTaskSucceeded {
			foundScheduledSuccess = true
		}
	}
	if !foundScheduledSuccess {
		t.Fatalf("no scheduled_task_succeeded permitted audit, got events = %+v", permitted.Events)
	}
}

// TestSchedulerRunbookEndToEndDeniedFailClosed 验证 fail-closed：runbook 模板为 low，
// 但其写工具不在 E2 白名单 → 准入门（AdmitRunbook 步 3）拒绝 → run 记为 failed +
// 审计 denied（非静默退回人工确认）。
func TestSchedulerRunbookEndToEndDenied(t *testing.T) {
	registerSchedulerLowRiskWriteTool(t)
	db := openAssistantSQLite(t)
	repository := store.NewSQLActionPlanStore(db)
	auditService := audit.NewService(repository)
	runbookStore := store.NewSQLRunbookStore(db)
	if err := store.SeedBuiltinRunbooks(context.Background(), runbookStore); err != nil {
		t.Fatalf("seed builtin runbooks: %v", err)
	}
	// low 风险模板指向写工具，但白名单为空 → 工具不在名单 → 准入拒绝。
	if _, err := runbookStore.CreateRunbook(context.Background(), store.Runbook{
		Slug:         "minio-retention-low-template",
		Name:         "MinIO 保留期设置（模板 low）",
		RiskLevel:    "low",
		IsBuiltin:    true,
		IsEnabled:    true,
		ToolSequence: []string{schedulerRunbookWriteTool},
	}); err != nil {
		t.Fatalf("create low-risk runbook: %v", err)
	}

	readService := execution.NewReadOnlyService(e2eReadRunner{}, auditService)
	// 计划创建与执行使用同一时钟，避免「计划已过期」：新建计划基于 fake clock
	// 计算 ExpiresAt，执行服务必须以同一时刻校验，否则真实墙钟会使计划立即过期。
	fakeNow := func() time.Time {
		return time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	}
	executionService := execution.NewServiceWithClock(repository, e2eSchedulerRunbookExecutor{}, fakeNow)
	planService := plans.NewService(repository, plans.ClockFunc(fakeNow))
	// 模板 low 但白名单为空 → AdmitRunbook 步 3（工具不在名单）拒绝。
	controller := autonomy.NewController(autonomy.Config{
		Enabled:      true,
		DailyLimit:   100,
		LowRiskTools: map[string]bool{},
	}, nil)
	runbookExec := scheduler.NewRunbookAutoExecutor(runbookStore, planService, executionService, controller)
	taskService := scheduler.NewService(store.NewSQLScheduledTaskStore(db), readService, auditService, nil).
		WithRunbookExecutor(runbookExec)

	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		readService,
		httpapi.WithActionPlans(repository),
		httpapi.WithScheduledTasks(taskService),
		httpapi.WithRunbooks(runbookStore),
	)

	createBody := `{"name":"minio 保留期定时","run_kind":"runbook","runbook_slug":"minio-retention-low-template","input":{"bucket":"archive","retention_days":90},"schedule_kind":"preset","preset":"daily","timezone":"Asia/Shanghai","enabled":true}`
	createReq := schedulerRunbookReq(t, http.MethodPost, "/v1/scheduled-tasks", createBody)
	createRes := httptest.NewRecorder()
	router.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusOK {
		t.Fatalf("create status = %d body = %s, want 200", createRes.Code, createRes.Body.String())
	}
	taskID := extractJSONString(createRes.Body.String(), `"id":"`)
	if taskID == "" {
		t.Fatalf("create body = %s, want task id", createRes.Body.String())
	}

	triggerReq := schedulerRunbookReq(t, http.MethodPost, "/v1/scheduled-tasks/"+taskID+"/run", "")
	triggerRes := httptest.NewRecorder()
	router.ServeHTTP(triggerRes, triggerReq)
	if triggerRes.Code != http.StatusOK {
		t.Fatalf("trigger status = %d body = %s, want 200", triggerRes.Code, triggerRes.Body.String())
	}
	// fail-closed：run 记为 failed 且带 denied 原因，而非 succeeded。
	if !strings.Contains(triggerRes.Body.String(), `"status":"failed"`) {
		t.Fatalf("trigger body = %s, want run status failed (fail-closed)", triggerRes.Body.String())
	}
	if !strings.Contains(triggerRes.Body.String(), "denied") {
		t.Fatalf("trigger body = %s, want denied reason in run error", triggerRes.Body.String())
	}

	denied, err := auditService.List(context.Background(), store.AuditFilter{Decision: audit.DecisionDenied})
	if err != nil {
		t.Fatalf("list denied audit: %v", err)
	}
	if len(denied.Events) == 0 {
		t.Fatalf("no denied audit events, want scheduled_task_failed denied, got %+v", denied.Events)
	}
}

// registerSchedulerLowRiskWriteTool 动态注册一个低风险写工具并授予 admin 权限，
// 用于 permitted 链路；结束时重置整个注册表/权限表（与 capabilities_test 同模式，
// 保持顺序阶段互不干扰）。
func registerSchedulerLowRiskWriteTool(t *testing.T) {
	t.Helper()
	if err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{
		{
			Tool: tools.Tool{
				Name:                schedulerRunbookWriteTool,
				Operation:           tools.Write,
				Risk:                tools.Low,
				RollbackDescription: "reset_retention_to_previous",
				Domain:              "minio",
				ResourceType:        "bucket",
			},
			InputSchema: map[string]tools.DynamicInputField{

				"bucket": {Type: "string", Required: true},
				"retention_days": {Type: "integer", Required: true,
					Min: e2eMinBound(1), Max: e2eMaxBound(3650)},
			},
		},
	}); err != nil {
		t.Fatalf("register low-risk write tool: %v", err)
	}
	policy.RegisterDynamicRolePermissions(map[string][]string{
		schedulerRunbookWriteTool: {"admin"},
	})
	t.Cleanup(func() {
		tools.ResetDynamicToolsForTest()
		policy.ResetDynamicRolePermissionsForTest()
	})
	// 让 RegisterDynamicRolePermissions 之后的权限对 policy.Evaluate 生效：复用上层
	// 注册的中间件工具权限（幂等），确保 admin 不致因重置而缺权限。
	ensureMiddlewareTools(t)
}

// schedulerRunbookReq 构造一个 admin 签名的 http 请求（Bearer JWT + 请求 ID），
// 与 assistant e2e 测试的鉴权方式一致。
func schedulerRunbookReq(t *testing.T, method, path, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+signedAdminJWT(t))
	req.Header.Set("X-Request-ID", "scheduler-runbook-e2e")
	return req
}

// extractJSONString 从 JSON 里提取 "key":"value" 的 value（仅顶层字符串，测试用）。
func extractJSONString(body, prefix string) string {
	idx := strings.Index(body, prefix)
	if idx < 0 {
		return ""
	}
	rest := body[idx+len(prefix):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}
