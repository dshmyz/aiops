package alert

import (
	"testing"
	"time"
)

func TestValidateMissingRequiredFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		payload WebhookPayload
	}{
		{name: "missing external_id", payload: WebhookPayload{Source: "grafana", Title: "t", Severity: "critical", Status: "firing", Environment: "prod"}},
		{name: "missing source", payload: WebhookPayload{ExternalID: "a1", Title: "t", Severity: "critical", Status: "firing", Environment: "prod"}},
		{name: "missing title", payload: WebhookPayload{ExternalID: "a1", Source: "grafana", Severity: "critical", Status: "firing", Environment: "prod"}},
		{name: "missing severity", payload: WebhookPayload{ExternalID: "a1", Source: "grafana", Title: "t", Status: "firing", Environment: "prod"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.payload.Validate(); err == nil {
				t.Errorf("Validate() = nil, want error for %s", tc.name)
			}
		})
	}
}

func TestValidateRejectsUnknownStatus(t *testing.T) {
	t.Parallel()
	p := WebhookPayload{ExternalID: "a1", Source: "grafana", Title: "t", Severity: "critical", Status: "acknowledged", Environment: "prod"}
	if err := p.Validate(); err == nil {
		t.Error("Validate() = nil, want error for unknown status")
	}
}

func TestValidateAcceptsValidPayload(t *testing.T) {
	t.Parallel()
	p := WebhookPayload{ExternalID: "a1", Source: "grafana", Title: "t", Severity: "critical", Status: "firing", Environment: "prod"}
	if err := p.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestNormalizeDefaults(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	p := WebhookPayload{
		ExternalID:  "a1",
		Source:      "GRAFANA",
		Title:       "CPU 高",
		Severity:    "unknown-severity", // 未知 severity → warning
		Environment: "prod",
	}
	a, err := Normalize(p, now)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if a.Source != "grafana" {
		t.Errorf("Source = %q, want lowercase grafana", a.Source)
	}
	if a.Status != StatusFiring {
		t.Errorf("Status = %q, want firing", a.Status)
	}
	if a.Severity != SeverityWarning {
		t.Errorf("Severity = %q, want warning (unknown normalized)", a.Severity)
	}
	if !a.FiredAt.Equal(now) {
		t.Errorf("FiredAt = %v, want now %v", a.FiredAt, now)
	}
	if a.ID == "" {
		t.Error("ID should be generated")
	}
	if a.ReceivedAt.IsZero() {
		t.Error("ReceivedAt should be set")
	}
}

func TestNormalizeDefaultEnvironment(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	p := WebhookPayload{ExternalID: "a1", Source: "grafana", Title: "t", Severity: "warning", Status: "firing"}
	a, err := Normalize(p, now)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if a.Environment != "prod" {
		t.Errorf("Environment = %q, want default prod", a.Environment)
	}
}

func TestNormalizeResolvedSetsResolvedAt(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	p := WebhookPayload{ExternalID: "a1", Source: "grafana", Title: "t", Severity: "warning", Status: "resolved", Environment: "prod"}
	a, err := Normalize(p, now)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if a.Status != StatusResolved {
		t.Errorf("Status = %q, want resolved", a.Status)
	}
	if a.ResolvedAt == nil || !a.ResolvedAt.Equal(now) {
		t.Errorf("ResolvedAt = %v, want now", a.ResolvedAt)
	}
}

func TestNormalizeRejectsUnknownStatus(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	p := WebhookPayload{ExternalID: "a1", Source: "grafana", Title: "t", Severity: "warning", Status: "acknowledged", Environment: "prod"}
	if _, err := Normalize(p, now); err == nil {
		t.Error("Normalize() = nil err, want error for unknown status")
	}
}
