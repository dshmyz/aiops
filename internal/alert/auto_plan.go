package alert

import (
	"context"
	"fmt"
	"log"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// PlanCreator 为告警自动创建 action plan（PendingConfirmation 状态）。
// 同时实现 PlanExecutor 接口，供 ChainDiagnoser 调用。
type PlanCreator struct {
	planSvc  *plans.Service
	alertSvc *Service
}

// NewPlanCreator 创建自动建 plan 器。
func NewPlanCreator(planSvc *plans.Service, alertSvc *Service) *PlanCreator {
	return &PlanCreator{planSvc: planSvc, alertSvc: alertSvc}
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
		Subject:             "alert-auto-plan",
		Roles:               []string{"admin"},
		AllowedEnvironments: []string{"prod", "staging", "dev"},
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
	return nil
}
