package assistant_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
)

// mockChatModel 是 LLMFormatter 测试用的 model.BaseChatModel 桩。
// content 决定 Generate 返回的 AssistantMessage 内容；genErr 决定是否返回错误。
type mockChatModel struct {
	content string
	genErr  error
}

func (m *mockChatModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if m.genErr != nil {
		return nil, m.genErr
	}
	return schema.AssistantMessage(m.content, nil), nil
}

func (m *mockChatModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage(m.content, nil)}), nil
}

func TestLLMFormatterParsesValidJSON(t *testing.T) {
	t.Parallel()
	chat := &mockChatModel{content: `{"summary":"Kafka 集群健康，3 节点在线","blocks":[{"type":"incident_card","title":"诊断结果","content":"healthy","payload":{"status":"healthy"}}]}`}
	f := assistant.NewLLMFormatter(chat)
	req := assistant.FormatRequest{
		UserMessage: "查看 kafka 健康",
		Tool:        "cluster.status.read",
		Answer:      map[string]any{"status": "healthy", "nodes": 3},
	}
	result, err := f.Format(context.Background(), req)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if result.Summary != "Kafka 集群健康，3 节点在线" {
		t.Fatalf("Summary = %q", result.Summary)
	}
	if len(result.Blocks) != 1 || result.Blocks[0].Type != assistant.BlockIncidentCard {
		t.Fatalf("Blocks = %+v", result.Blocks)
	}
}

func TestLLMFormatterRejectsInvalidJSON(t *testing.T) {
	t.Parallel()
	chat := &mockChatModel{content: "not a json"}
	f := assistant.NewLLMFormatter(chat)
	_, err := f.Format(context.Background(), assistant.FormatRequest{Tool: "t", Answer: map[string]any{}})
	if err == nil {
		t.Fatal("expected error for non-JSON LLM response")
	}
}

func TestLLMFormatterEmptySummaryReturnsError(t *testing.T) {
	t.Parallel()
	chat := &mockChatModel{content: `{"summary":"","blocks":[]}`}
	f := assistant.NewLLMFormatter(chat)
	_, err := f.Format(context.Background(), assistant.FormatRequest{Tool: "t", Answer: map[string]any{}})
	if err == nil {
		t.Fatal("expected error for empty summary")
	}
}

func TestLLMFormatterGenerateError(t *testing.T) {
	t.Parallel()
	chat := &mockChatModel{genErr: errors.New("upstream unavailable")}
	f := assistant.NewLLMFormatter(chat)
	_, err := f.Format(context.Background(), assistant.FormatRequest{Tool: "t", Answer: map[string]any{}})
	if err == nil {
		t.Fatal("expected error when chat.Generate fails")
	}
}

func TestLLMFormatterNilChatReturnsError(t *testing.T) {
	t.Parallel()
	f := assistant.NewLLMFormatter(nil)
	_, err := f.Format(context.Background(), assistant.FormatRequest{Tool: "t", Answer: map[string]any{}})
	if err == nil {
		t.Fatal("expected error when chat model is nil")
	}
}

func TestLLMFormatterFiltersInvalidBlockTypes(t *testing.T) {
	t.Parallel()
	chat := &mockChatModel{content: `{"summary":"ok","blocks":[{"type":"incident_card","title":"a"},{"type":"unknown_type","title":"b"},{"type":"tool_trace","title":"c"}]}`}
	f := assistant.NewLLMFormatter(chat)
	result, err := f.Format(context.Background(), assistant.FormatRequest{Tool: "t", Answer: map[string]any{}})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if len(result.Blocks) != 2 {
		t.Fatalf("Blocks len = %d, want 2 (invalid type filtered)", len(result.Blocks))
	}
	for _, b := range result.Blocks {
		if b.Type == "unknown_type" {
			t.Fatal("invalid block type was not filtered")
		}
	}
}

// TestChainedFormatterLLMFallsBackToCode 验证 LLM 失败时 ChainedFormatter 回退到 CodeFallbackFormatter。
func TestChainedFormatterLLMFallsBackToCode(t *testing.T) {
	t.Parallel()
	llm := assistant.NewLLMFormatter(&mockChatModel{genErr: errors.New("down")})
	chained := assistant.NewChainedFormatter(llm, assistant.NewCodeFallbackFormatter())
	req := assistant.FormatRequest{Tool: "cluster.status.read", Answer: map[string]any{"status": "healthy"}}
	result, err := chained.Format(context.Background(), req)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if result.Summary == "" {
		t.Fatal("fallback Summary is empty")
	}
	hasToolTrace := false
	for _, b := range result.Blocks {
		if b.Type == assistant.BlockToolTrace {
			hasToolTrace = true
		}
	}
	if !hasToolTrace {
		t.Error("fallback missing tool_trace block")
	}
}

// TestChainedFormatterLLMSuccessNoFallback 验证 LLM 成功时不走兜底。
func TestChainedFormatterLLMSuccessNoFallback(t *testing.T) {
	t.Parallel()
	llm := assistant.NewLLMFormatter(&mockChatModel{content: `{"summary":"LLM 摘要","blocks":[{"type":"incident_card"}]}`})
	chained := assistant.NewChainedFormatter(llm, assistant.NewCodeFallbackFormatter())
	result, err := chained.Format(context.Background(), assistant.FormatRequest{Tool: "t", Answer: map[string]any{}})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if result.Summary != "LLM 摘要" {
		t.Fatalf("Summary = %q, want LLM 摘要", result.Summary)
	}
}
