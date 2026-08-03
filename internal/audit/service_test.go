package audit_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

func TestRecordPersistsCorrelatedAuditEvent(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryActionPlanStore()
	service := audit.NewService(repository)

	err := service.Record(context.Background(), audit.Event{
		ID:          "audit-1",
		RequestID:   "request-1",
		Subject:     "subject-1",
		PlanID:      "plan-1",
		ExecutionID: "execution-1",
		ToolName:    "topic.retention.set",
		Action:      audit.ActionExecutionStarted,
		Decision:    audit.DecisionPermitted,
		Metadata:    map[string]any{"idempotency_key": "plan:plan-1:hash"},
	})
	if err != nil {
		t.Fatalf("record audit event: %v", err)
	}

	events := repository.AuditEvents()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	if events[0].RequestID != "request-1" || events[0].Subject != "subject-1" || events[0].PlanID != "plan-1" || events[0].ExecutionID != "execution-1" || events[0].Decision != audit.DecisionPermitted {
		t.Fatalf("stored event = %+v, want fully correlated event", events[0])
	}
}

func TestRecordRejectsUnknownActionEnum(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryActionPlanStore()
	service := audit.NewService(repository)

	err := service.Record(context.Background(), audit.Event{
		ID:       "audit-bad-action",
		PlanID:   "plan-1",
		ToolName: "topic.retention.set",
		Action:   "totally_made_up_action",
		Decision: audit.DecisionPermitted,
	})
	if err == nil {
		t.Fatal("Record accepted unknown action enum")
	}
	if !strings.Contains(err.Error(), "action") {
		t.Fatalf("error = %v, want it to mention action", err)
	}
	if len(repository.AuditEvents()) != 0 {
		t.Fatalf("repository should not persist event with invalid action, got %+v", repository.AuditEvents())
	}
}

func TestRecordRejectsUnknownDecisionEnum(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryActionPlanStore()
	service := audit.NewService(repository)

	err := service.Record(context.Background(), audit.Event{
		ID:       "audit-bad-decision",
		PlanID:   "plan-1",
		ToolName: "topic.retention.set",
		Action:   audit.ActionPlanCreated,
		Decision: "totally_made_up_decision",
	})
	if err == nil {
		t.Fatal("Record accepted unknown decision enum")
	}
	if !strings.Contains(err.Error(), "decision") {
		t.Fatalf("error = %v, want it to mention decision", err)
	}
	if len(repository.AuditEvents()) != 0 {
		t.Fatalf("repository should not persist event with invalid decision, got %+v", repository.AuditEvents())
	}
}

func TestRecordDefaultsCreatedAtWhenMissing(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryActionPlanStore()
	service := audit.NewService(repository)

	before := time.Now().Add(-time.Second)
	err := service.Record(context.Background(), audit.Event{
		ID:       "audit-no-time",
		PlanID:   "plan-1",
		ToolName: "topic.retention.set",
		Action:   audit.ActionPlanCreated,
		Decision: audit.DecisionPermitted,
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	after := time.Now().Add(time.Second)

	events := repository.AuditEvents()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].CreatedAt.Before(before) || events[0].CreatedAt.After(after) {
		t.Fatalf("CreatedAt = %v, want between %v and %v", events[0].CreatedAt, before, after)
	}
}
