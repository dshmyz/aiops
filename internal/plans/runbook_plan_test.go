package plans_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestCreateRunbookPlanLowRiskAutoConfirms(t *testing.T) {
	t.Parallel()
	ensureMiddlewareWriteTool(t)
	ctx := context.Background()
	repository := store.NewMemoryActionPlanStore()
	service := plans.NewService(repository, fixedClock())
	decision := policy.Evaluate(user(), registeredTool(t, tools.TopicRetentionSet), retentionInput())

	plan, err := service.CreateRunbookPlan(ctx, user(), decision, retentionInput(), "kafka-retention-low-risk", "low")
	if err != nil {
		t.Fatalf("CreateRunbookPlan: %v", err)
	}
	if plan.Status != plans.Confirmed {
		t.Fatalf("status = %q, want confirmed (low-risk auto-confirm)", plan.Status)
	}
	if plan.ConfirmationToken != "" {
		t.Fatalf("confirmation token = %q, want empty for auto-confirmed plan", plan.ConfirmationToken)
	}

	// audit 应记录 confirmation_required=false + runbook slug
	events := repository.AuditEvents()
	var found bool
	for _, ev := range events {
		if ev.Action == "plan_created" {
			found = true
			if ev.Metadata["confirmation_required"] != false {
				t.Errorf("metadata confirmation_required = %v, want false", ev.Metadata["confirmation_required"])
			}
			if ev.Metadata["runbook"] != "kafka-retention-low-risk" {
				t.Errorf("metadata runbook = %v, want kafka-retention-low-risk", ev.Metadata["runbook"])
			}
		}
	}
	if !found {
		t.Fatalf("no plan_created audit event; events = %+v", events)
	}
}

func TestCreateRunbookPlanMediumRiskRequiresConfirmation(t *testing.T) {
	t.Parallel()
	ensureMiddlewareWriteTool(t)
	ctx := context.Background()
	repository := store.NewMemoryActionPlanStore()
	service := plans.NewService(repository, fixedClock())
	decision := policy.Evaluate(user(), registeredTool(t, tools.TopicRetentionSet), retentionInput())

	plan, err := service.CreateRunbookPlan(ctx, user(), decision, retentionInput(), "alert-root-cause-sequence", "medium")
	if err != nil {
		t.Fatalf("CreateRunbookPlan: %v", err)
	}
	if plan.Status != plans.PendingConfirmation {
		t.Fatalf("status = %q, want pending_confirmation for medium risk", plan.Status)
	}
	if plan.ConfirmationToken == "" {
		t.Fatal("confirmation token = empty, want token for medium risk")
	}
}

func TestCreateRunbookPlanRejectsNotPermittedDecision(t *testing.T) {
	t.Parallel()
	ensureMiddlewareWriteTool(t)
	ctx := context.Background()
	repository := store.NewMemoryActionPlanStore()
	service := plans.NewService(repository, fixedClock())
	// 用一个 viewer 无法访问的写工具决策（viewer 无 TopicRetentionSet 权限）
	decision := policy.Evaluate(viewerUser(), registeredTool(t, tools.TopicRetentionSet), retentionInput())

	if _, err := service.CreateRunbookPlan(ctx, viewerUser(), decision, retentionInput(), "kafka-retention-low-risk", "low"); !errors.Is(err, plans.ErrPlanNotPermitted) {
		t.Fatalf("CreateRunbookPlan = %v, want ErrPlanNotPermitted", err)
	}
}

func viewerUser() identity.CurrentUser {
	return identity.CurrentUser{Subject: "viewer-1", Roles: []string{"viewer"}, AllowedEnvironments: []string{"prod"}, RequestID: "req-viewer"}
}
