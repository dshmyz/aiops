package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/autonomy"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// 本文件驱动真实 RunbookAutoExecutor 端到端全链：
//
//	runbook 模板 → tools.Lookup → policy.Evaluate → autonomy.Admit(SourceScheduler)
//	  → plans.CreateRunbookPlan → execution.ExecuteConfirmedStoredPlan → autonomy.Record
//
// 与 runbook_executor_test.go 的 fakeRunbookExecutor 编排用例互补：那里测的是调度器
// 分支把错误转成 failed run；这里测的是真实执行器是否正确地放行/拒绝并真正执行写。

// registerLowRiskWriteTool 把 topic.retention.set 注册为**低风险**写工具
// （覆盖生产 Medium 语义，便于本测试走通准入成功路径）。admin 角色 + prod 环境放行。
func registerLowRiskWriteTool(t *testing.T) {
	t.Helper()
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	if err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
		Tool: tools.Tool{
			Name:                "topic.retention.set",
			Operation:           tools.Write,
			Risk:                tools.Low,
			RollbackDescription: "reset_to_previous",
			Domain:              "kafka",
			ResourceType:        "topic",
			SupportsDryRun:      true,
		},
		InputSchema: map[string]tools.DynamicInputField{
			"environment":     {Type: "string", Required: true},
			"topic":           {Type: "string", Required: true},
			"retention_hours": {Type: "integer", Required: true},
		},
	}}); err != nil {
		t.Fatalf("register low-risk write tool: %v", err)
	}
	policy.ResetDynamicRolePermissionsForTest()
	policy.RegisterDynamicRolePermissions(map[string][]string{
		"topic.retention.set": {"admin"},
	})
}

// registerMediumWriteTool 把 topic.retention.set 注册为**Medium**写工具——与生产
// capability 声明一致（验证器强制写能力 Medium+）。配合按模板风险准入
// （AdmitRunbook），低风险模板 + Medium 工具应能自动执行。
func registerMediumWriteTool(t *testing.T) {
	t.Helper()
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	if err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
		Tool: tools.Tool{
			Name:                "topic.retention.set",
			Operation:           tools.Write,
			Risk:                tools.Medium,
			RollbackDescription: "reset_to_previous",
			Domain:              "kafka",
			ResourceType:        "topic",
			SupportsDryRun:      true,
		},
		InputSchema: map[string]tools.DynamicInputField{
			"environment":     {Type: "string", Required: true},
			"topic":           {Type: "string", Required: true},
			"retention_hours": {Type: "integer", Required: true},
		},
	}}); err != nil {
		t.Fatalf("register medium write tool: %v", err)
	}
	policy.ResetDynamicRolePermissionsForTest()
	policy.RegisterDynamicRolePermissions(map[string][]string{
		"topic.retention.set": {"admin"},
	})
}

// recordingExecutor 是 execution.Service 的注入 Executor，记录是否真正执行了写。
type recordingExecutor struct {
	mu    sync.Mutex
	calls []string // tool names executed
}

func (e *recordingExecutor) Execute(_ context.Context, tool string, _ map[string]any) (map[string]any, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, tool)
	return map[string]any{"status": "ok"}, nil
}

func (e *recordingExecutor) executed() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := append([]string{}, e.calls...)
	return out
}

// e2eFixture 组装真实执行器所需的全部依赖，模拟 main.go 的装配形状。
type e2eFixture struct {
	rbStore  store.RunbookStore
	planRepo *store.MemoryActionPlanStore
	planSvc  *plans.Service
	execSvc  *execution.Service
	exec     *recordingExecutor
	admCtl   *autonomy.Controller
	executor *RunbookAutoExecutor
	now      time.Time
}

func newE2EFixture(t *testing.T, cfg autonomy.Config, limiter autonomy.DailyLimiter) *e2eFixture {
	return newE2EFixtureWithTool(t, cfg, limiter, registerLowRiskWriteTool)
}

// newE2EFixtureWithTool 用自定义工具注册回调组装 fixture（低/中风险写工具）。
func newE2EFixtureWithTool(t *testing.T, cfg autonomy.Config, limiter autonomy.DailyLimiter, register func(*testing.T)) *e2eFixture {
	t.Helper()
	register(t)

	now := time.Date(2026, time.August, 7, 10, 0, 0, 0, time.UTC)
	rbStore := store.NewMemoryRunbookStore()
	planRepo := store.NewMemoryActionPlanStore()
	planSvc := plans.NewService(planRepo, plans.ClockFunc(func() time.Time { return now }))
	exec := &recordingExecutor{}
	execSvc := execution.NewServiceWithClock(planRepo, exec, func() time.Time { return now })
	admCtl := autonomy.NewController(cfg, limiter).WithClock(func() time.Time { return now })

	return &e2eFixture{
		rbStore:  rbStore,
		planRepo: planRepo,
		planSvc:  planSvc,
		execSvc:  execSvc,
		exec:     exec,
		admCtl:   admCtl,
		executor: NewRunbookAutoExecutor(rbStore, planSvc, execSvc, admCtl),
		now:      now,
	}
}

// seedRunbook 把低风险单写 runbook 写入 store。
func (f *e2eFixture) seedRunbook(t *testing.T, slug string, sequence []string) {
	t.Helper()
	if _, err := f.rbStore.CreateRunbook(context.Background(), store.Runbook{
		ID:            "rb-1",
		Slug:          slug,
		Name:          "低风险写 runbook",
		IntentPattern: []string{"test"},
		ToolSequence:  sequence,
		RiskLevel:     "low",
		IsEnabled:     true,
	}); err != nil {
		t.Fatalf("create runbook: %v", err)
	}
}

func (f *e2eFixture) runbookTask(slug string) store.ScheduledTask {
	return store.ScheduledTask{
		ID:          "task-1",
		Subject:     "admin-1",
		RunKind:     store.RunKindRunbook,
		RunbookSlug: slug,
		Input:       map[string]any{"environment": "prod", "topic": "orders", "retention_hours": 72},
	}
}

func enabledCfg() autonomy.Config {
	return autonomy.Config{
		Enabled:      true,
		DailyLimit:   0, // 不设上限，聚焦准入成功路径
		LowRiskTools: map[string]bool{"topic.retention.set": true},
	}
}

// TestRunbookExecutorE2EAdmittedWriteExecutes 验证：低风险写 runbook 过 E2 准入门后
// 真实执行——plan 已确认、写工具被真执行、审计 permitted、返回结果含 steps。
func TestRunbookExecutorE2EAdmittedWriteExecutes(t *testing.T) {
	f := newE2EFixture(t, enabledCfg(), nil)
	f.seedRunbook(t, "minio-retention-low-risk", []string{"topic.retention.set"})

	result, err := f.executor.Execute(context.Background(), f.runbookTask("minio-retention-low-risk"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := f.exec.executed(); len(got) != 1 || got[0] != "topic.retention.set" {
		t.Fatalf("executed tools = %v, want [%s]", got, "topic.retention.set")
	}
	if result["runbook"] != "minio-retention-low-risk" {
		t.Errorf("runbook = %v", result["runbook"])
	}
	steps, ok := result["steps"].([]stepResult)
	if !ok || len(steps) != 1 {
		t.Fatalf("steps = %#v, want 1 stepResult", result["steps"])
	}
	if steps[0].Tool != "topic.retention.set" || steps[0].Status != "succeeded" {
		t.Errorf("step = %+v, want tool=%s status=succeeded", steps[0], "topic.retention.set")
	}
	if steps[0].PlanID == "" || steps[0].ExecutionID == "" {
		t.Errorf("step missing plan/execution id: %+v", steps[0])
	}

	// plan 应为已确认（低风险 runbook 自动确认）。
	plan, err := f.planRepo.GetPlan(context.Background(), steps[0].PlanID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if plan.Status != store.PlanConfirmed {
		t.Errorf("plan status = %q, want confirmed", plan.Status)
	}

	// 审计链应含 permitted 决策（plan_created + execution_started 均写 permitted）。
	var sawPermitted, sawRunbook bool
	for _, ev := range f.planRepo.AuditEvents() {
		if ev.Decision == "permitted" {
			sawPermitted = true
		}
		if ev.Metadata != nil {
			if runbook, ok := ev.Metadata["runbook"].(string); ok && runbook == "minio-retention-low-risk" {
				sawRunbook = true
			}
		}
	}
	if !sawPermitted {
		t.Errorf("expected a permitted audit event; got %d events", len(f.planRepo.AuditEvents()))
	}
	if !sawRunbook {
		t.Errorf("expected audit metadata to carry runbook slug")
	}
}

// TestRunbookExecutorE2EDeniedWithoutController 验证 fail-closed：未装配准入控制器
// （nil）时拒绝，即便 runbook 是低风险写。
func TestRunbookExecutorE2EDeniedWithoutController(t *testing.T) {
	registerLowRiskWriteTool(t)
	now := time.Date(2026, time.August, 7, 10, 0, 0, 0, time.UTC)
	rbStore := store.NewMemoryRunbookStore()
	planRepo := store.NewMemoryActionPlanStore()
	planSvc := plans.NewService(planRepo, plans.ClockFunc(func() time.Time { return now }))
	exec := &recordingExecutor{}
	execSvc := execution.NewServiceWithClock(planRepo, exec, func() time.Time { return now })
	// 无准入门：直接为 nil controller。
	ex := NewRunbookAutoExecutor(rbStore, planSvc, execSvc, nil)

	if _, err := rbStore.CreateRunbook(context.Background(), store.Runbook{
		ID: "rb-1", Slug: "minio-retention-low-risk", Name: "x",
		IntentPattern: []string{"test"}, ToolSequence: []string{"topic.retention.set"},
		RiskLevel: "low", IsEnabled: true,
	}); err != nil {
		t.Fatalf("create runbook: %v", err)
	}

	task := store.ScheduledTask{ID: "t1", Subject: "admin-1", RunKind: store.RunKindRunbook,
		RunbookSlug: "minio-retention-low-risk",
		Input:       map[string]any{"environment": "prod", "topic": "orders", "retention_hours": 72}}
	if _, err := ex.Execute(context.Background(), task); !errors.Is(err, ErrRunbookDenied) {
		t.Fatalf("Execute = %v, want ErrRunbookDenied (nil controller fail-closed)", err)
	}
	if got := exec.executed(); len(got) != 0 {
		t.Fatalf("executed tools = %v, want none (denied before execution)", got)
	}
}

// TestRunbookExecutorE2ESkipsReadSteps Keeps Write 验证多步序列：只读工具被跳过，
// 写工具照常通过准入执行。
func TestRunbookExecutorE2ESkipsReadStepsKeepsWrite(t *testing.T) {
	f := newE2EFixture(t, enabledCfg(), nil)
	// 序列：read 打头 + 写。只读工具（cluster.status.read 静态注册）应被跳过，
	// 写工具应被执行。
	sequence := []string{tools.ClusterStatusRead, "topic.retention.set"}
	f.seedRunbook(t, "mixed-read-write", sequence)

	result, err := f.executor.Execute(context.Background(), f.runbookTask("mixed-read-write"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// 只读工具不产生执行；仅写工具被真执行。
	if got := f.exec.executed(); len(got) != 1 || got[0] != "topic.retention.set" {
		t.Fatalf("executed tools = %v, want only [%s] (read skipped)", got, "topic.retention.set")
	}
	steps, ok := result["steps"].([]stepResult)
	if !ok || len(steps) != 1 {
		t.Fatalf("steps = %#v, want 1 write stepResult", result["steps"])
	}
	if steps[0].Tool != "topic.retention.set" {
		t.Errorf("step tool = %q, want %s", steps[0].Tool, "topic.retention.set")
	}
}

// TestRunbookExecutorE2EDeniedNoWriteTool 验证 fail-closed：序列中没有写工具
// （纯只读 runbook）时拒绝，避免调度写执行器静默跑空。
func TestRunbookExecutorE2EDeniedNoWriteTool(t *testing.T) {
	f := newE2EFixture(t, enabledCfg(), nil)
	f.seedRunbook(t, "read-only-runbook", []string{tools.ClusterStatusRead})

	if _, err := f.executor.Execute(context.Background(), f.runbookTask("read-only-runbook")); !errors.Is(err, ErrRunbookDenied) {
		t.Fatalf("Execute = %v, want ErrRunbookDenied (no write tool)", err)
	}
	if got := f.exec.executed(); len(got) != 0 {
		t.Fatalf("executed tools = %v, want none", got)
	}
}

// TestRunbookExecutorE2EDeniedToolNotInWhitelist 验证：工具不在 E2 白名单时拒绝。
func TestRunbookExecutorE2EDeniedToolNotInWhitelist(t *testing.T) {
	// 白名单为空且 Enabled=true → 任何写工具都不在名单 → 拒绝。
	cfg := autonomy.Config{Enabled: true, DailyLimit: 0, LowRiskTools: map[string]bool{}}
	f := newE2EFixture(t, cfg, nil)
	f.seedRunbook(t, "minio-retention-low-risk", []string{"topic.retention.set"})

	if _, err := f.executor.Execute(context.Background(), f.runbookTask("minio-retention-low-risk")); !errors.Is(err, ErrRunbookDenied) {
		t.Fatalf("Execute = %v, want ErrRunbookDenied (not whitelisted)", err)
	}
	if got := f.exec.executed(); len(got) != 0 {
		t.Fatalf("executed tools = %v, want none", got)
	}
}

// TestRunbookExecutorE2EMediumToolLowTemplateNowExecutes 验证生产可达性回归：能力验证器
// 强制写能力必须 Medium+（validation.go），因此真实注册的写工具是 Medium；而 E2 准入门
// 对工具自身要求 low → 出现「写死成 low 者无法注册、写成 Medium 者被准入拒绝」的零可达
// 空档。模板评审准入（AdmitRunbook）以 runbook 模板风险为授权单元：Medium 工具 + low 模板
// 应放行并真执行——证明该空档被补上。
func TestRunbookExecutorE2EMediumToolLowTemplateNowExecutes(t *testing.T) {
	cfg := autonomy.Config{Enabled: true, DailyLimit: 0, LowRiskTools: map[string]bool{"topic.retention.set": true}}
	f := newE2EFixtureWithTool(t, cfg, nil, registerMediumWriteTool)
	f.seedRunbook(t, "minio-retention-low-risk", []string{"topic.retention.set"})

	result, err := f.executor.Execute(context.Background(), f.runbookTask("minio-retention-low-risk"))
	if err != nil {
		t.Fatalf("Execute = %v, want success (medium tool + low template archives)", err)
	}
	if got := f.exec.executed(); len(got) != 1 || got[0] != "topic.retention.set" {
		t.Fatalf("executed tools = %v, want [%s]", got, "topic.retention.set")
	}
	steps, ok := result["steps"].([]stepResult)
	if !ok || len(steps) != 1 || steps[0].Status != "succeeded" {
		t.Fatalf("steps = %#v, want 1 succeeded step", result["steps"])
	}
	plan, err := f.planRepo.GetPlan(context.Background(), steps[0].PlanID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if plan.Status != store.PlanConfirmed {
		t.Errorf("plan status = %q, want confirmed", plan.Status)
	}
}
