package alert

import (
	"context"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

func fixedNow() time.Time {
	return time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
}

func newTestService() (*Service, *store.MemoryAlertStore) {
	alertStore := store.NewMemoryAlertStore().WithClock(fixedNow)
	return NewService(alertStore), alertStore
}

func validPayload() WebhookPayload {
	return WebhookPayload{
		ExternalID:  "a1",
		Source:      "grafana",
		Title:       "CPU 高",
		Severity:    "critical",
		Status:      "firing",
		Environment: "prod",
		Domain:      "kafka",
	}
}

func TestIngestCreatesAlert(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService()
	result, err := svc.Ingest(context.Background(), validPayload())
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if !result.Created {
		t.Error("Created = false, want true for first ingest")
	}
	if result.Alert.Status != StatusFiring {
		t.Errorf("Status = %q, want firing", result.Alert.Status)
	}
	if result.Alert.ID == "" {
		t.Error("Alert.ID should be set")
	}
}

func TestIngestDedupReturnsCreatedFalse(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService()
	if _, err := svc.Ingest(context.Background(), validPayload()); err != nil {
		t.Fatalf("first Ingest: %v", err)
	}
	result, err := svc.Ingest(context.Background(), validPayload())
	if err != nil {
		t.Fatalf("second Ingest: %v", err)
	}
	if result.Created {
		t.Error("Created = true, want false for duplicate identity")
	}
}

func TestIngestResolvedFlipsStatus(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService()
	if _, err := svc.Ingest(context.Background(), validPayload()); err != nil {
		t.Fatalf("firing Ingest: %v", err)
	}
	resolved := validPayload()
	resolved.Status = "resolved"
	result, err := svc.Ingest(context.Background(), resolved)
	if err != nil {
		t.Fatalf("resolved Ingest: %v", err)
	}
	if result.Alert.Status != StatusResolved {
		t.Errorf("Status = %q, want resolved", result.Alert.Status)
	}
	if result.Alert.ResolvedAt == nil {
		t.Error("ResolvedAt should be set after resolution")
	}
}

func TestIngestRejectsInvalidPayload(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService()
	bad := validPayload()
	bad.Severity = ""
	if _, err := svc.Ingest(context.Background(), bad); err == nil {
		t.Error("Ingest() = nil err, want error for invalid payload")
	}
}

func TestQueryFilters(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService()
	critical := validPayload()
	critical.ExternalID = "a1"
	critical.Severity = "critical"
	critical.Status = "firing"
	info := validPayload()
	info.ExternalID = "a2"
	info.Severity = "info"
	info.Status = "resolved"
	for _, p := range []WebhookPayload{critical, info} {
		if _, err := svc.Ingest(context.Background(), p); err != nil {
			t.Fatalf("Ingest: %v", err)
		}
	}

	firing, err := svc.Query(context.Background(), store.AlertFilter{Status: "firing", Environment: "prod"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(firing) != 1 || firing[0].ExternalID != "a1" {
		t.Errorf("firing alerts = %d (want 1 a1), got %+v", len(firing), firing)
	}

	criticalOnly, err := svc.Query(context.Background(), store.AlertFilter{Severity: "critical", Environment: "prod"})
	if err != nil {
		t.Fatalf("Query critical: %v", err)
	}
	if len(criticalOnly) != 1 || criticalOnly[0].ExternalID != "a1" {
		t.Errorf("critical alerts = %d (want 1 a1)", len(criticalOnly))
	}
}

func TestListActive(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService()
	firing := validPayload()
	firing.ExternalID = "a1"
	resolved := validPayload()
	resolved.ExternalID = "a2"
	resolved.Status = "resolved"
	for _, p := range []WebhookPayload{firing, resolved} {
		if _, err := svc.Ingest(context.Background(), p); err != nil {
			t.Fatalf("Ingest: %v", err)
		}
	}
	active, err := svc.ListActive(context.Background(), "prod", 50)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(active) != 1 || active[0].ExternalID != "a1" {
		t.Errorf("active alerts = %d (want 1 a1)", len(active))
	}
}

func TestResolveAlert(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService()
	if _, err := svc.Ingest(context.Background(), validPayload()); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	resolved, err := svc.Resolve(context.Background(), "a1", "grafana")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Status != StatusResolved {
		t.Errorf("Status = %q, want resolved", resolved.Status)
	}
	if _, err := svc.Resolve(context.Background(), "missing", "grafana"); err == nil {
		t.Error("Resolve() = nil err, want ErrNotFound for missing alert")
	}
}
