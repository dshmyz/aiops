package audit_test

import (
	"context"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

func TestRecordAcceptsAlertIngestActions(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryActionPlanStore()
	service := audit.NewService(repository)

	for _, action := range []string{audit.ActionAlertIngested, audit.ActionAlertRejected, audit.ActionLLMInvoked} {
		err := service.Record(context.Background(), audit.Event{
			ID:       "audit-alert-" + action,
			Subject:  "grafana",
			Action:   action,
			Decision: audit.DecisionPermitted,
		})
		if err != nil {
			t.Fatalf("Record(%s): %v", action, err)
		}
	}
}
