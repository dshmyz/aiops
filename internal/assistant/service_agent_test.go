package assistant

import (
	"context"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

func testClock() func() time.Time {
	return func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
}

// TestPersistAgentRunRecordsFailedStep 验证被拒/失败的 step 被持久化为 tool_step
// turn，payload 带 status=failed 与非空 error，回放后前端可显示被拒原因。
func TestPersistAgentRunRecordsFailedStep(t *testing.T) {
	conversations := store.NewMemoryAssistantConversationStore()
	conv, err := conversations.CreateConversation(context.Background(), "admin-1", "t", "p", time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{conversations: conversations, clock: testClock()}
	run := &AgentRun{
		Steps: []StepOutcome{
			{
				Kind:      StepAdvisory,
				Tool:      "minio.bucket.health.read",
				Input:     map[string]any{"environment": "prod", "bucket": "archive"},
				Err:       "policy denied: environment_denied",
				Summary:   "工具执行失败：policy denied: environment_denied",
				StepIndex: 0,
			},
		},
	}
	if _, err := s.persistAgentRun(context.Background(), conv.ID, "minio bucket archive 现在存了多少", run, Response{Type: "answer", Message: "failed"}); err != nil {
		t.Fatal(err)
	}

	page, err := conversations.ListTurns(context.Background(), conv.ID, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	var found *store.Turn
	for i := range page.Turns {
		if page.Turns[i].ResponseType == responseTypeToolStep {
			found = &page.Turns[i]
		}
	}
	if found == nil {
		t.Fatal("no tool_step turn persisted")
	}
	if found.ResponsePayload["status"] != "failed" {
		t.Fatalf("payload status = %v, want failed", found.ResponsePayload["status"])
	}
	if got, _ := found.ResponsePayload["error"].(string); got != "policy denied: environment_denied" {
		t.Fatalf("payload error = %v, want policy denied: environment_denied", found.ResponsePayload["error"])
	}
	if found.ResponsePayload["tool"] != "minio.bucket.health.read" {
		t.Fatalf("payload tool = %v, want minio.bucket.health.read", found.ResponsePayload["tool"])
	}
}

// TestRecordDeniedWritesAuditEvent 验证 agent 循环策略拒绝点写审计事件，携带
// agent_step / conversation_turn_id 归属与策略拒绝原因。
func TestRecordDeniedWritesAuditEvent(t *testing.T) {
	repo := store.NewMemoryActionPlanStore()
	s := &Service{audit: audit.NewService(repo), clock: testClock()}
	user := identity.CurrentUser{
		Subject:             "admin-1",
		RequestID:           "req-1",
		Roles:               []string{"admin"},
		AllowedEnvironments: []string{"prod", "staging", "dev"},
	}
	ctx := execution.WithAgentStep(context.Background(), execution.AgentStep{StepIndex: 3, Conversation: "conv-9"})
	s.recordDenied(ctx, user, "minio.bucket.health.read", policy.EnvironmentDenied)

	page, err := repo.ListAudit(context.Background(), store.AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(page.Events))
	}
	event := page.Events[0]
	if event.ToolName != "minio.bucket.health.read" {
		t.Fatalf("tool_name = %q", event.ToolName)
	}
	if event.Action != audit.ActionReadonlyToolRejected {
		t.Fatalf("action = %q, want %q", event.Action, audit.ActionReadonlyToolRejected)
	}
	if event.Decision != string(policy.EnvironmentDenied) {
		t.Fatalf("decision = %q, want environment_denied", event.Decision)
	}
	if event.Subject != "admin-1" {
		t.Fatalf("subject = %q", event.Subject)
	}
	if got, _ := event.Metadata["agent_step"].(int); got != 3 {
		t.Fatalf("metadata.agent_step = %v, want 3", event.Metadata["agent_step"])
	}
	if got, _ := event.Metadata["conversation_turn_id"].(string); got != "conv-9" {
		t.Fatalf("metadata.conversation_turn_id = %v, want conv-9", event.Metadata["conversation_turn_id"])
	}
}

// TestRecordDeniedNilAuditNoop 验证 audit 未配置时 recordDenied 是 no-op 不 panic。
func TestRecordDeniedNilAuditNoop(t *testing.T) {
	s := &Service{}
	s.recordDenied(context.Background(), identity.CurrentUser{Subject: "u"}, "tool.a", policy.EnvironmentDenied)
}
