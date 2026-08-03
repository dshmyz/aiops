package httpapi_test

import (
	"context"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

func TestNewRunbookLookupAdapterConvertsStoreRunbooks(t *testing.T) {
	t.Parallel()
	runbookStore := store.NewMemoryRunbookStore()
	ctx := context.Background()

	if _, err := runbookStore.CreateRunbook(ctx, store.Runbook{
		Slug:          "kafka-retention-low-risk",
		Name:          "保留调整",
		IntentPattern: []string{"保留"},
		ToolSequence:  []string{"topic.retention.set"},
		RiskLevel:     "low",
		IsEnabled:     true,
	}); err != nil {
		t.Fatalf("create runbook: %v", err)
	}

	lookup := httpapi.NewRunbookLookupAdapter(runbookStore)
	runbooks, err := lookup.ListEnabledRunbooks(ctx)
	if err != nil {
		t.Fatalf("ListEnabledRunbooks: %v", err)
	}
	if len(runbooks) != 1 {
		t.Fatalf("runbooks = %d, want 1", len(runbooks))
	}
	rb := runbooks[0]
	if rb.Slug != "kafka-retention-low-risk" || rb.RiskLevel != "low" {
		t.Fatalf("runbook = %+v, want slug/risk preserved", rb)
	}
	if len(rb.ToolSequence) != 1 || rb.ToolSequence[0] != "topic.retention.set" {
		t.Fatalf("tool sequence = %v", rb.ToolSequence)
	}
}

func TestNewRunbookLookupAdapterNilReturnsNil(t *testing.T) {
	t.Parallel()
	if lookup := httpapi.NewRunbookLookupAdapter(nil); lookup != nil {
		t.Fatal("nil store should yield nil lookup")
	}
}
