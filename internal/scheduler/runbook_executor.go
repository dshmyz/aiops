package scheduler

import (
	"context"
	"fmt"

	"github.com/gracegaoya/ai-operations-copilot/internal/autonomy"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// ErrRunbookDenied 表示一次定时 runbook 自动执行被 E2 准入门拒绝。调用方应记为
// denied 审计 + failed run（非静默，见设计 §5.1⑥），而不是当作普通执行错误。
var ErrRunbookDenied = fmt.Errorf("scheduled runbook auto-execution denied by admission controller")

// RunbookExecutor 执行一次被准入门放行的低风险 runbook 写（E2 Phase 3）。
// 由 main 装配一个具体实现（复用 runbook store / plans / execution / autonomy）。
type RunbookExecutor interface {
	// Execute 执行低风险 runbook。task.RunKind 必须为 'runbook'。
	// 返回运行结果（写进 run.ResultData）或错误：准入门拒绝返回 ErrRunbookDenied
	// （包装底层原因），其余为执行错误。
	Execute(ctx context.Context, task store.ScheduledTask) (map[string]any, error)
}

// RunbookAutoExecutor 是生产 RunbookExecutor：解析 runbook 模板 → 取首个工具 →
// policy 判定 → E2 准入门（SourceScheduler）→ 创建已确认 plan → 执行 → 记每日上限。
//
// 安全边界（设计 §5.3）：定时任务只允许触发**预先评审过的低风险 runbook 模板**，
// 工具名来自模板（固定，不可由任务任意指定），参数由 admin 创建任务时提供。
// 绝不接受「定时执行任意 tool + input」。
type RunbookAutoExecutor struct {
	runbooks store.RunbookStore
	plans    *plans.Service
	exec     *execution.Service
	admit    *autonomy.Controller
}

// NewRunbookAutoExecutor 创建 runbook 自动执行器。
func NewRunbookAutoExecutor(runbooks store.RunbookStore, planService *plans.Service, execService *execution.Service, controller *autonomy.Controller) *RunbookAutoExecutor {
	return &RunbookAutoExecutor{runbooks: runbooks, plans: planService, exec: execService, admit: controller}
}

// Execute implements RunbookExecutor.
func (e *RunbookAutoExecutor) Execute(ctx context.Context, task store.ScheduledTask) (map[string]any, error) {
	if task.RunKind != store.RunKindRunbook {
		return nil, fmt.Errorf("task run_kind is %q, want %q", task.RunKind, store.RunKindRunbook)
	}
	if task.RunbookSlug == "" {
		return nil, fmt.Errorf("runbook task missing runbook_slug")
	}
	rb, err := e.runbooks.GetRunbook(ctx, task.RunbookSlug)
	if err != nil {
		return nil, fmt.Errorf("resolve runbook %q: %w", task.RunbookSlug, err)
	}
	if !rb.IsEnabled {
		return nil, fmt.Errorf("runbook %q is disabled", task.RunbookSlug)
	}
	if len(rb.ToolSequence) == 0 {
		return nil, fmt.Errorf("runbook %q has empty tool_sequence", task.RunbookSlug)
	}
	// 低风险预检：模板本身须声明 low。工具风险与白名单由准入门最终判定。
	if rb.RiskLevel != "low" {
		return nil, fmt.Errorf("runbook %q risk_level is %q, want low (autonomous scheduled runbook must be low risk)", task.RunbookSlug, rb.RiskLevel)
	}

	toolName := rb.ToolSequence[0]
	tool, ok := tools.Lookup(toolName)
	if !ok {
		return nil, fmt.Errorf("runbook %q first tool %q not registered", task.RunbookSlug, toolName)
	}
	// 定时任务由创建它的 admin 预先授权；调度器以其身份执行，但环境限定为
	// 任务输入所声明的 environment（不放开「全环境」），写操作仍过 policy + 准入门。
	user := scheduledAdminIdentity(task)

	decision := policy.Evaluate(user, tool, task.Input)
	if !decision.Allowed {
		return nil, fmt.Errorf("policy denies tool %q: %s", toolName, decision.Reason)
	}
	if e.admit == nil {
		return nil, fmt.Errorf("%w: admission controller not configured (fail-closed)", ErrRunbookDenied)
	}
	if err := e.admit.Admit(ctx, user, tool, decision); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRunbookDenied, err)
	}

	plan, err := e.plans.CreateRunbookPlan(ctx, user, decision, task.Input, task.RunbookSlug, rb.RiskLevel)
	if err != nil {
		return nil, fmt.Errorf("create runbook plan: %w", err)
	}
	execResult, execErr := e.exec.ExecuteConfirmedStoredPlan(ctx, plan.ID)
	if execErr != nil {
		return nil, execErr
	}
	e.admit.Record(ctx, user)

	return map[string]any{
		"status":       execResult.Status,
		"execution_id": execResult.ID,
		"plan_id":      plan.ID,
		"runbook":      task.RunbookSlug,
		"tool":         toolName,
		"reused":       execResult.Reused,
	}, nil
}

// scheduledAdminIdentity 构造定时 runbook 执行所用的身份：Subject 用创建任务的
// admin，角色为 admin（其创建时已具备该 runbook 的授权），环境限定为任务输入所
// 声明的 environment（单个，避免「全环境」通配）。
func scheduledAdminIdentity(task store.ScheduledTask) identity.CurrentUser {
	env := ""
	if raw, ok := task.Input["environment"].(string); ok {
		env = raw
	}
	return identity.CurrentUser{
		Subject:             task.Subject,
		Roles:               []string{"admin"},
		AllowedEnvironments: []string{env},
		RequestID:           "scheduler:" + task.ID,
	}
}
