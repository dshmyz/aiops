package alert

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/notification"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// PlanCreator 为告警自动创建 action plan（PendingConfirmation 状态）。
// 同时实现 PlanExecutor 接口（供 ChainDiagnoser 调用）和
// RecommendationPlanCreator 接口（供 auto_diagnose 处置闭环调用）。
type PlanCreator struct {
	planSvc  *plans.Service
	alertSvc *Service
	notifier notification.Notifier // 可 nil：建 plan 后把确认 token 推给审批人
}

// NewPlanCreator 创建自动建 plan 器。
func NewPlanCreator(planSvc *plans.Service, alertSvc *Service) *PlanCreator {
	return &PlanCreator{planSvc: planSvc, alertSvc: alertSvc}
}

// WithNotifier 注入确认通知通道。自动建 plan 的确认 token 只随创建响应存在，
// 不通知就没人能确认——闭环会停在待确认态。
func (c *PlanCreator) WithNotifier(n notification.Notifier) *PlanCreator {
	c.notifier = n
	return c
}

// notifyConfirmation 把确认请求推给审批人（飞书/日志）。通知失败不影响建 plan。
func (c *PlanCreator) notifyConfirmation(ctx context.Context, plan plans.Plan, user identity.CurrentUser, input map[string]any) {
	if c.notifier == nil || plan.ConfirmationToken == "" {
		return
	}
	req := notification.ConfirmationRequest{
		PlanID:            plan.ID,
		ConfirmationToken: plan.ConfirmationToken,
		ToolName:          plan.ToolName,
		Risk:              plan.RiskLevel,
		Subject:           user.Subject,
		Input:             input,
	}
	if !plan.ExpiresAt.IsZero() {
		req.ExpiresAt = plan.ExpiresAt.Format(time.RFC3339)
	}
	if err := c.notifier.NotifyConfirmation(ctx, req); err != nil {
		log.Printf("[alert-auto-plan] notify confirmation failed: %v", err)
	}
}

// CreateRecommendationPlan 把诊断产出的可执行推荐转成待确认的 action plan
// （RecommendationPlanCreator 接口）。告警触发的 plan 必须人工确认，不自动执行写操作。
func (c *PlanCreator) CreateRecommendationPlan(ctx context.Context, user identity.CurrentUser, rec diagnostics.Recommendation) (string, error) {
	if c.planSvc == nil {
		return "", fmt.Errorf("plan service not configured")
	}
	toolName := rec.ToolName
	if toolName == "" {
		return "", fmt.Errorf("recommendation has no tool_name")
	}
	tool, ok := tools.Lookup(toolName)
	if !ok {
		return "", fmt.Errorf("tool %q not registered", toolName)
	}

	decision := policy.Evaluate(user, tool, rec.CandidateInput)
	if !decision.Allowed {
		return "", fmt.Errorf("policy denies %q: %s", toolName, decision.Reason)
	}
	// 告警触发的 plan 必须人工确认
	decision.RequiresConfirmation = true

	plan, err := c.planSvc.CreatePlan(ctx, user, decision, rec.CandidateInput)
	if err != nil {
		return "", fmt.Errorf("create plan: %w", err)
	}

	log.Printf("[alert-auto-plan] created plan %s from recommendation (tool=%s)", plan.ID[:8], toolName)
	c.notifyConfirmation(ctx, plan, user, rec.CandidateInput)
	return plan.ID, nil
}

// CreatePlanForStep 为序列中某一步创建 plan（PlanExecutor 接口）。
func (c *PlanCreator) CreatePlanForStep(ctx context.Context, alert Alert, action AlertAction, stepIdx int, prevResults []StepResult) error {
	if c.planSvc == nil {
		return fmt.Errorf("plan service not configured")
	}
	if stepIdx < 0 || stepIdx >= len(action.ToolSequence) {
		return fmt.Errorf("step index %d out of range", stepIdx)
	}

	step := action.ToolSequence[stepIdx]
	tool, ok := tools.Lookup(step.Tool)
	if !ok {
		return fmt.Errorf("tool %q not registered", step.Tool)
	}

	input := step.RenderInput(alert)

	user := identity.CurrentUser{
		Subject: "alert-auto-plan",
		Roles:   []string{"admin"},
	}

	decision := policy.Evaluate(user, tool, input)
	if !decision.Allowed {
		return fmt.Errorf("policy denies %q: %s", step.Tool, decision.Reason)
	}

	// 告警触发的 plan 必须人工确认
	decision.RequiresConfirmation = true

	plan, err := c.planSvc.CreatePlan(ctx, user, decision, input)
	if err != nil {
		return fmt.Errorf("create plan: %w", err)
	}

	log.Printf("[alert-auto-plan] created plan %s for step %d (tool=%s)", plan.ID[:8], stepIdx+1, step.Tool)
	c.notifyConfirmation(ctx, plan, user, input)
	return nil
}
