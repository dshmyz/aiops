package alert

import (
	"testing"
	"time"
)

func TestMapAlertmanagerProducesValidPayloads(t *testing.T) {
	t.Parallel()
	am := AlertmanagerPayload{
		Version:  "4",
		GroupKey: "{}/{namespace=prod}:{alertname=HighCPU}",
		Status:   "firing",
		Alerts: []AlertmanagerAlert{
			{
				Status:      "firing",
				Fingerprint: "fp-abc-123",
				Labels:      map[string]string{"alertname": "HighCPU", "namespace": "prod", "severity": "critical", "environment": "prod"},
				Annotations: map[string]string{"summary": "CPU 超过 90%", "description": "节点 ns1 的 CPU 使用率持续过高"},
				StartsAt:    "2026-08-02T10:00:00Z",
			},
		},
	}
	payloads := MapAlertmanager(am)
	if len(payloads) != 1 {
		t.Fatalf("len(payloads) = %d, want 1", len(payloads))
	}
	p := payloads[0]
	if err := p.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if p.ExternalID != "fp:fp-abc-123" {
		t.Errorf("ExternalID = %q, want fp:fp-abc-123", p.ExternalID)
	}
	if p.Source != SourceAlertmanager {
		t.Errorf("Source = %q, want alertmanager", p.Source)
	}
	if p.Title != "CPU 超过 90%" {
		t.Errorf("Title = %q, want annotation summary", p.Title)
	}
	if p.Description != "节点 ns1 的 CPU 使用率持续过高" {
		t.Errorf("Description = %q, want annotation description", p.Description)
	}
	if p.Severity != "critical" {
		t.Errorf("Severity = %q, want critical", p.Severity)
	}
	if p.Status != "firing" {
		t.Errorf("Status = %q, want firing", p.Status)
	}
	if p.Domain != "prod" {
		t.Errorf("Domain = %q, want prod (from namespace)", p.Domain)
	}
	if p.Labels["alertname"] != "HighCPU" {
		t.Errorf("labels missing alertname: %v", p.Labels)
	}
	if p.Labels["am_summary"] != "CPU 超过 90%" {
		t.Errorf("labels missing am_summary: %v", p.Labels)
	}
	if p.FiredAt == nil || !p.FiredAt.Equal(time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("FiredAt = %v, want parsed startsAt", p.FiredAt)
	}
	if p.Raw == nil {
		t.Error("Raw should capture the original payload")
	}
}

func TestMapAlertmanagerMissingFingerprintUsesLabelComposite(t *testing.T) {
	t.Parallel()
	// Same label set, different map construction/order and different status.
	// Without a fingerprint the external ID must be a stable sorted composite,
	// so firing→resolved on the same alert keeps the same identity.
	a1 := AlertmanagerAlert{Status: "firing", Labels: map[string]string{"alertname": "HighMem", "severity": "warning", "job": "nodes"}}
	a2 := AlertmanagerAlert{Status: "resolved", Labels: map[string]string{"job": "nodes", "severity": "warning", "alertname": "HighMem"}}
	id1 := alertmanagerExternalID(a1)
	id2 := alertmanagerExternalID(a2)
	if id1 == "" {
		t.Fatal("external ID should not be empty")
	}
	if id1 != id2 {
		t.Errorf("composite IDs differ for same label set:\n %q\n %q", id1, id2)
	}
}

func TestMapAlertmanagerDefaultStatusAndSeverity(t *testing.T) {
	t.Parallel()
	// No explicit status and no labels → defaults firing + warning.
	am := AlertmanagerPayload{Alerts: []AlertmanagerAlert{{Labels: map[string]string{"alertname": "DiskFull"}}}}
	p := MapAlertmanager(am)
	if len(p) != 1 {
		t.Fatalf("len(payloads) = %d, want 1", len(p))
	}
	if p[0].Status != "firing" {
		t.Errorf("Status = %q, want firing default", p[0].Status)
	}
	if p[0].Severity != "warning" {
		t.Errorf("Severity = %q, want warning default", p[0].Severity)
	}
	if p[0].Environment != "" {
		t.Errorf("Environment = %q, want empty (no environment label)", p[0].Environment)
	}
}

func TestMapAlertmanagerResolvedSetsResolvedAt(t *testing.T) {
	t.Parallel()
	am := AlertmanagerPayload{Alerts: []AlertmanagerAlert{{
		Status:      "resolved",
		Labels:      map[string]string{"alertname": "HighCPU"},
		StartsAt:    "2026-08-02T10:00:00Z",
		EndsAt:      "2026-08-02T11:30:00Z",
		Fingerprint: "fp-resolved",
	}}}
	p := MapAlertmanager(am)[0]
	if p.Status != "resolved" {
		t.Errorf("Status = %q, want resolved", p.Status)
	}
	if p.ResolvedAt == nil || !p.ResolvedAt.Equal(time.Date(2026, 8, 2, 11, 30, 0, 0, time.UTC)) {
		t.Errorf("ResolvedAt = %v, want parsed endsAt", p.ResolvedAt)
	}
	// Resolved state still normalizes to a valid Alert.
	a, err := Normalize(p, time.Now())
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if a.Status != StatusResolved {
		t.Errorf("Normalized Status = %q, want resolved", a.Status)
	}
}

func TestMapAlertmanagerMultipleAlerts(t *testing.T) {
	t.Parallel()
	am := AlertmanagerPayload{Alerts: []AlertmanagerAlert{
		{Status: "firing", Fingerprint: "fp-1", Labels: map[string]string{"alertname": "A"}},
		{Status: "firing", Fingerprint: "fp-2", Labels: map[string]string{"alertname": "B"}},
	}}
	payloads := MapAlertmanager(am)
	if len(payloads) != 2 {
		t.Fatalf("len(payloads) = %d, want 2", len(payloads))
	}
	if payloads[0].ExternalID != "fp:fp-1" || payloads[1].ExternalID != "fp:fp-2" {
		t.Errorf("external IDs not per-alert: %q, %q", payloads[0].ExternalID, payloads[1].ExternalID)
	}
}
