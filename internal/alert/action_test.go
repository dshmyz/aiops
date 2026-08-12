package alert_test

import (
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/alert"
)

func TestAlertActionMatchAlertName(t *testing.T) {
	action := alert.AlertAction{
		AlertMatch: alert.AlertMatch{AlertName: "HighCPU"},
	}
	if !action.Match(alert.Alert{Labels: map[string]string{"alertname": "HighCPU"}}) {
		t.Fatal("should match alertname")
	}
	if action.Match(alert.Alert{Labels: map[string]string{"alertname": "HighMemory"}}) {
		t.Fatal("should not match wrong alertname")
	}
}

func TestAlertActionMatchSeverity(t *testing.T) {
	action := alert.AlertAction{
		AlertMatch: alert.AlertMatch{Severity: "critical"},
	}
	if !action.Match(alert.Alert{Severity: alert.SeverityCritical}) {
		t.Fatal("should match critical")
	}
	if action.Match(alert.Alert{Severity: alert.SeverityWarning}) {
		t.Fatal("should not match warning")
	}
}

func TestAlertActionMatchANDLogic(t *testing.T) {
	action := alert.AlertAction{
		AlertMatch: alert.AlertMatch{AlertName: "HighCPU", Severity: "critical"},
	}
	// Both match
	if !action.Match(alert.Alert{
		Severity: alert.SeverityCritical,
		Labels:   map[string]string{"alertname": "HighCPU"},
	}) {
		t.Fatal("should match both")
	}
	// Only alertname matches
	if action.Match(alert.Alert{
		Severity: alert.SeverityWarning,
		Labels:   map[string]string{"alertname": "HighCPU"},
	}) {
		t.Fatal("should not match when severity differs")
	}
}

func TestAlertActionRenderInput(t *testing.T) {
	action := alert.AlertAction{
		Tool: "cluster.status.read",
		Input: map[string]string{
			"environment": "{environment}",
			"cluster":     "{labels.cluster}",
		},
	}
	a := alert.Alert{
		Environment: "prod",
		Labels:      map[string]string{"cluster": "m1"},
	}
	input := action.RenderInput(a)
	if input["environment"] != "prod" {
		t.Fatalf("environment = %q", input["environment"])
	}
	if input["cluster"] != "m1" {
		t.Fatalf("cluster = %q", input["cluster"])
	}
}

func TestMatchActionsMultiple(t *testing.T) {
	actions := []alert.AlertAction{
		{AlertMatch: alert.AlertMatch{AlertName: "HighCPU"}},
		{AlertMatch: alert.AlertMatch{AlertName: "HighMemory"}},
	}
	a := alert.Alert{Labels: map[string]string{"alertname": "HighCPU"}}
	matched := alert.MatchActions(a, actions)
	if len(matched) != 1 || matched[0].AlertMatch.AlertName != "HighCPU" {
		t.Fatalf("matched = %v, want 1 HighCPU", matched)
	}
}

func TestLoadAlertActionsEmpty(t *testing.T) {
	actions, err := alert.LoadAlertActions("")
	if err != nil || len(actions) != 0 {
		t.Fatalf("empty string should return nil, got %v err=%v", actions, err)
	}
}

func TestLoadAlertActionsInvalid(t *testing.T) {
	_, err := alert.LoadAlertActions("not json")
	if err == nil {
		t.Fatal("should error on invalid JSON")
	}
}
