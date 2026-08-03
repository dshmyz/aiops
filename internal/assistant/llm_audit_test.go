package assistant_test

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// usageFakeChatModel 返回带 ResponseMeta.Usage 的响应，验证 llm_invoked
// 审计记录 token 用量。
type usageFakeChatModel struct {
	content string
}

func (m usageFakeChatModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	msg := schema.AssistantMessage(m.content, nil)
	msg.ResponseMeta = &schema.ResponseMeta{
		Usage: &schema.TokenUsage{
			PromptTokens:     120,
			CompletionTokens: 40,
			TotalTokens:      160,
		},
	}
	return msg, nil
}

func (m usageFakeChatModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

func TestEinoPlannerRecordsLLMInvocationAudit(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryActionPlanStore()
	auditService := audit.NewService(repository)
	chat := usageFakeChatModel{content: `{"tool_name":"cluster.status.read","input":{"environment":"prod"},"confidence":0.91,"explanation":"read cluster status"}`}
	planner := assistant.NewEinoPlanner(chat)
	planner.WithLLMAudit(auditService, "gpt-test")

	intent, err := planner.Plan(context.Background(), user(), "查看集群状态", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if intent.ToolName != "cluster.status.read" {
		t.Fatalf("tool = %q", intent.ToolName)
	}

	events := repository.AuditEvents()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Action != audit.ActionLLMInvoked {
		t.Errorf("action = %q, want %q", ev.Action, audit.ActionLLMInvoked)
	}
	if ev.Metadata["model"] != "gpt-test" {
		t.Errorf("metadata model = %v, want gpt-test", ev.Metadata["model"])
	}
	if ev.Metadata["component"] != "planner" {
		t.Errorf("metadata component = %v, want planner", ev.Metadata["component"])
	}
	if got, _ := ev.Metadata["prompt_tokens"].(int); got != 120 {
		t.Errorf("prompt_tokens = %v, want 120", ev.Metadata["prompt_tokens"])
	}
	if got, _ := ev.Metadata["completion_tokens"].(int); got != 40 {
		t.Errorf("completion_tokens = %v, want 40", ev.Metadata["completion_tokens"])
	}
	if _, ok := ev.Metadata["latency_ms"]; !ok {
		t.Error("latency_ms missing from metadata")
	}
}

func TestEinoPlannerNoAuditWhenNotWired(t *testing.T) {
	t.Parallel()
	chat := usageFakeChatModel{content: `{"tool_name":"cluster.status.read","input":{"environment":"prod"},"confidence":0.91,"explanation":"read cluster status"}`}
	planner := assistant.NewEinoPlanner(chat)
	// 不注入 audit → Plan 正常执行，无 panic
	if _, err := planner.Plan(context.Background(), user(), "查看集群状态", nil, assistant.PageContext{}); err != nil {
		t.Fatalf("Plan: %v", err)
	}
}

func TestLLMFormatterRecordsLLMInvocationAudit(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryActionPlanStore()
	auditService := audit.NewService(repository)
	chat := usageFakeChatModel{content: `{"summary":"集群健康","blocks":[]}`}
	formatter := assistant.NewLLMFormatter(chat)
	formatter.WithLLMAudit(auditService, "gpt-test")

	req := assistant.FormatRequest{
		UserMessage: "查看集群状态",
		Tool:        "cluster.status.read",
		Answer:      map[string]any{"status": "ok"},
	}
	result, err := formatter.Format(context.Background(), req)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if result.Summary != "集群健康" {
		t.Fatalf("summary = %q", result.Summary)
	}

	events := repository.AuditEvents()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	if events[0].Action != audit.ActionLLMInvoked {
		t.Errorf("action = %q, want llm_invoked", events[0].Action)
	}
	if events[0].Metadata["component"] != "formatter" {
		t.Errorf("component = %v, want formatter", events[0].Metadata["component"])
	}
}
