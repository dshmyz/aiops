package plans_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestConfirmPlanUsesExpectedVersionAndSingleTransition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryActionPlanStore()
	service := plans.NewService(repository, fixedClock())
	plan := createWritePlan(t, ctx, service)

	if _, err := service.ConfirmPlan(ctx, plan.ID, plan.Version+1, plan.ConfirmationToken, user()); err == nil {
		t.Fatal("confirmation with a stale version succeeded")
	}

	confirmed, err := service.ConfirmPlan(ctx, plan.ID, plan.Version, plan.ConfirmationToken, user())
	if err != nil {
		t.Fatalf("confirm plan: %v", err)
	}
	if confirmed.Status != plans.Confirmed {
		t.Fatalf("status = %q, want %q", confirmed.Status, plans.Confirmed)
	}
	if confirmed.Version != plan.Version+1 {
		t.Fatalf("version = %d, want %d", confirmed.Version, plan.Version+1)
	}

	if _, err := service.ConfirmPlan(ctx, plan.ID, confirmed.Version, plan.ConfirmationToken, user()); err == nil {
		t.Fatal("second confirmation succeeded")
	}
}

func TestCreatePlanStoresCanonicalInputSnapshotAndExpiresInTenMinutes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := fixedClock()
	service := plans.NewService(store.NewMemoryActionPlanStore(), clock)

	plan := createWritePlan(t, ctx, service)
	if plan.Status != plans.PendingConfirmation {
		t.Fatalf("status = %q, want %q", plan.Status, plans.PendingConfirmation)
	}
	if plan.ExpiresAt != clock.Now().Add(10*time.Minute) {
		t.Fatalf("expires at = %s, want %s", plan.ExpiresAt, clock.Now().Add(10*time.Minute))
	}
	if plan.InputHash == "" || plan.ConfirmationToken == "" {
		t.Fatal("plan did not expose a hash and one-time confirmation token")
	}
}

func TestCreatePlanReevaluatesCallerSuppliedDecision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service := plans.NewService(store.NewMemoryActionPlanStore(), fixedClock())

	t.Run("write cannot disable confirmation", func(t *testing.T) {
		forged := policy.Decision{Allowed: true, RequiresConfirmation: false, Tool: registeredTool(t, tools.TopicRetentionSet)}
		plan, err := service.CreatePlan(ctx, user(), forged, retentionInput())
		if err != nil {
			t.Fatalf("create plan: %v", err)
		}
		if plan.Status != plans.PendingConfirmation || plan.ConfirmationToken == "" {
			t.Fatalf("forged write decision created plan %+v, want confirmation required", plan)
		}
	})

	t.Run("unknown tool is rejected", func(t *testing.T) {
		_, err := service.CreatePlan(ctx, user(), policy.Decision{Allowed: true, Tool: tools.Tool{Name: "unknown.write", Operation: tools.Write}}, retentionInput())
		if !errors.Is(err, plans.ErrPlanNotPermitted) {
			t.Fatalf("unknown tool error = %v, want ErrPlanNotPermitted", err)
		}
	})

	t.Run("forged allow cannot bypass role policy", func(t *testing.T) {
		viewer := user()
		viewer.Roles = []string{"viewer"}
		_, err := service.CreatePlan(ctx, viewer, policy.Decision{Allowed: true, Tool: registeredTool(t, tools.TopicRetentionSet)}, retentionInput())
		if !errors.Is(err, plans.ErrPlanNotPermitted) {
			t.Fatalf("forged allowed decision error = %v, want ErrPlanNotPermitted", err)
		}
	})
}

func TestConfirmPlanAuditsEmptyTokenWithConfirmingActor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryActionPlanStore()
	service := plans.NewService(repository, fixedClock())
	plan := createWritePlan(t, ctx, service)
	confirmer := user()
	confirmer.Subject = "operator-2"
	confirmer.RequestID = "confirmation-request"

	_, err := service.ConfirmPlan(ctx, plan.ID, plan.Version, "", confirmer)
	if !errors.Is(err, plans.ErrConfirmationDenied) {
		t.Fatalf("empty token error = %v, want ErrConfirmationDenied", err)
	}
	events := repository.AuditEvents()
	last := events[len(events)-1]
	if last.Action != "plan_confirmation_rejected" || last.Subject != confirmer.Subject || last.RequestID != confirmer.RequestID {
		t.Fatalf("rejection audit = %+v, want confirmer attribution", last)
	}
}

func TestConfirmPlanAuditsSuccessfulConfirmationWithConfirmingActor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryActionPlanStore()
	service := plans.NewService(repository, fixedClock())
	plan := createWritePlan(t, ctx, service)
	confirmer := user()
	confirmer.Subject = "operator-2"
	confirmer.RequestID = "confirmation-request"

	if _, err := service.ConfirmPlan(ctx, plan.ID, plan.Version, plan.ConfirmationToken, confirmer); err != nil {
		t.Fatalf("confirm plan: %v", err)
	}
	events := repository.AuditEvents()
	last := events[len(events)-1]
	if last.Action != "plan_confirmed" || last.Subject != confirmer.Subject || last.RequestID != confirmer.RequestID {
		t.Fatalf("confirmation audit = %+v, want confirmer attribution", last)
	}
}

func TestConfirmPlanReturnsRejectionAuditFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryActionPlanStore()
	service := plans.NewService(rejectAuditFailureStore{ActionPlanStore: repository}, fixedClock())
	plan := createWritePlan(t, ctx, plans.NewService(repository, fixedClock()))

	_, err := service.ConfirmPlan(ctx, plan.ID, plan.Version, "", user())
	if !errors.Is(err, errAuditUnavailable) {
		t.Fatalf("confirmation rejection error = %v, want audit failure", err)
	}
}

func createWritePlan(t *testing.T, ctx context.Context, service *plans.Service) plans.Plan {
	t.Helper()
	ensureMiddlewareWriteTool(t)
	decision := policy.Evaluate(user(), registeredTool(t, tools.TopicRetentionSet), retentionInput())
	if !decision.Allowed || !decision.RequiresConfirmation {
		t.Fatalf("test policy decision = %+v", decision)
	}
	plan, err := service.CreatePlan(ctx, user(), decision, retentionInput())
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	return plan
}

func fixedClock() plans.Clock {
	return plans.ClockFunc(func() time.Time {
		return time.Date(2026, time.July, 21, 8, 0, 0, 0, time.UTC)
	})
}

func user() identity.CurrentUser {
	return identity.CurrentUser{
		Subject:             "operator-1",
		Roles:               []string{"admin"},
		AllowedEnvironments: []string{"prod"},
		RequestID:           "req-task-3",
	}
}

func registeredTool(t *testing.T, name string) tools.Tool {
	t.Helper()
	tool, ok := tools.Lookup(name)
	if !ok {
		t.Fatalf("registered tool %q not found", name)
	}
	return tool
}

// ensureMiddlewareWriteTool loads topic.retention.set into the dynamic registry
// with operator/admin role permissions, mirroring the published YAML capability.
// Idempotent and mutex-guarded so parallel plan tests share it safely.
var ensureMiddlewareWriteMu sync.Mutex

func ensureMiddlewareWriteTool(t *testing.T) {
	t.Helper()
	ensureMiddlewareWriteMu.Lock()
	defer ensureMiddlewareWriteMu.Unlock()
	if _, ok := tools.Lookup(tools.TopicRetentionSet); !ok {
		if err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
			Tool: tools.Tool{
				Name:                tools.TopicRetentionSet,
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
			t.Fatalf("register middleware write tool: %v", err)
		}
	}
	policy.RegisterDynamicRolePermissions(map[string][]string{
		tools.TopicRetentionSet: {"operator", "admin"},
	})
}

func retentionInput() map[string]any {
	return map[string]any{
		"environment":     "prod",
		"topic":           "orders",
		"retention_hours": 72,
	}
}

var errAuditUnavailable = errors.New("audit unavailable")

type rejectAuditFailureStore struct{ store.ActionPlanStore }

func (rejectAuditFailureStore) AppendAudit(context.Context, store.AuditEvent) error {
	return errAuditUnavailable
}
