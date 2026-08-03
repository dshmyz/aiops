package notification

import (
	"context"
	"encoding/json"
	"testing"
)

func TestLogNotifierNotifyConfirmationNoError(t *testing.T) {
	n := NewLogNotifier()
	req := ConfirmationRequest{
		PlanID:            "plan-001",
		ConfirmationToken: "token-abc",
		ToolName:          "kafka.topic.retention.write",
		Environment:       "prod",
		Risk:              "medium",
		Subject:           "Set retention for orders topic to 72h",
		Input:             map[string]any{"retention_hours": 72},
		ExpiresAt:         "2025-12-31T23:59:59Z",
	}
	if err := n.NotifyConfirmation(context.Background(), req); err != nil {
		t.Fatalf("NotifyConfirmation returned error: %v", err)
	}
}

func TestLogNotifierNotifyConfirmationEmptyFieldsNoPanic(t *testing.T) {
	n := NewLogNotifier()
	req := ConfirmationRequest{}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("NotifyConfirmation panicked on empty fields: %v", r)
		}
	}()
	if err := n.NotifyConfirmation(context.Background(), req); err != nil {
		t.Fatalf("NotifyConfirmation returned error on empty fields: %v", err)
	}
}

func TestConfirmationRequestJSONFieldNames(t *testing.T) {
	req := ConfirmationRequest{
		PlanID:            "plan-002",
		ConfirmationToken: "token-xyz",
		ToolName:          "minio.bucket.create.write",
		Environment:       "staging",
		Risk:              "high",
		Subject:           "Create bucket data-prod",
		Input:             map[string]any{"bucket": "data-prod"},
		ExpiresAt:         "2025-12-31T23:59:59Z",
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	want := map[string]string{
		"plan_id":            "plan-002",
		"confirmation_token": "token-xyz",
		"tool_name":          "minio.bucket.create.write",
		"environment":         "staging",
		"risk":                "high",
		"subject":             "Create bucket data-prod",
		"expires_at":          "2025-12-31T23:59:59Z",
	}
	for jsonKey, expected := range want {
		got, ok := m[jsonKey]
		if !ok {
			t.Fatalf("expected JSON key %q not found in marshaled output", jsonKey)
		}
		if str, ok := got.(string); !ok || str != expected {
			t.Fatalf("for key %q got %v, want %q", jsonKey, got, expected)
		}
	}
	if input, ok := m["input"].(map[string]any); !ok {
		t.Fatalf("expected JSON key \"input\" to be an object")
	} else if input["bucket"] != "data-prod" {
		t.Fatalf("input.bucket = %v, want \"data-prod\"", input["bucket"])
	}
}
