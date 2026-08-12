package alert

import (
	"context"
	"fmt"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// PlanCreator 为告警自动创建 action plan（PendingConfirmation 状态）。
type PlanCreator struct {
	planSvc *plans.Service
	alertSvc *Service
}

// NewPlanCreator 创建自动建 plan 器。
func NewPlanCreator(planSvc *plans.Service, alertSvc *Service) *PlanCreator {
	return &PlanCreator{planSvc: planSvc, alertSvc: alertSvc}
}

// CreatePlansForAlert 为一条告警匹配所有 action 规则，为每条命中的规则创建 plan。
func (c *PlanCreator) CreatePlansForAlert(ctx context.Context, alert Alert, actions []AlertAction) {
	if c.planSvc == nil {
		return
	}
	if alert.Status != StatusFiring {
		return
	}

	matched := MatchActions(alert, actions)
	if len(matched) == 0 {
		return
	}

	user := identity.CurrentUser{
		Subject:             "alert-auto-plan",
		Roles:               []string{"admin"},
		AllowedEnvironments: []string{"prod", "staging", "dev"},
	}

	for _, action := range matched {
		tool, ok := tools.Lookup(action.Tool)
		if !ok {
			continue
		}

		input := action.RenderInput(alert)

		decision := policy.Evaluate(user, tool, input)
		if !decision.Allowed {
			continue
		}

		// 强制 RequiresConfirmation=true（告警触发的 plan 必须人工确认）
		decision.RequiresConfirmation = true

		plan, err := c.planSvc.CreatePlan(ctx, user, decision, input)
		if err != nil {
			continue
		}

		_ = c.alertSvc.UpdateDescription(ctx, alert.ID,
			fmt.Sprintf("已自动创建待审批计划 (plan: %s, 工具: %s, 描述: %s)",
				plan.ID, action.Tool, action.Description))
	}
}
