package assistant

import (
	"context"
	"fmt"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// mockChatModelForTest 是测试用的 LLM mock
type mockChatModelForTest struct {
	responses []*schema.Message
	callIdx   int
}

func (m *mockChatModelForTest) Generate(_ context.Context, _ []*schema.Message, _ ...interface{}) (*schema.Message, error) {
	if m.callIdx >= len(m.responses) {
		return nil, fmt.Errorf("no more responses")
	}
	resp := m.responses[m.callIdx]
	m.callIdx++
	return resp, nil
}

func (m *mockChatModelForTest) Stream(ctx context.Context, messages []*schema.Message, opts ...interface{}) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, messages)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func TestAgentExecutor_CompressToolOutput(t *testing.T) {
	tests := []struct {
		name   string
		input  map[string]any
		expect string
	}{
		{
			name:   "has summary",
			input:  map[string]any{"summary": "缓存命中率 94%", "status": "ok"},
			expect: "缓存命中率 94%",
		},
		{
			name:   "has status error",
			input:  map[string]any{"status": "error", "error": "connection refused"},
			expect: "状态: error, 错误: connection refused",
		},
		{
			name:   "has severity",
			input:  map[string]any{"severity": "critical"},
			expect: "严重级别: critical",
		},
		{
			name:   "nil output",
			input:  nil,
			expect: "(无结果)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := compressToolOutput(tt.input)
			if result != tt.expect {
				t.Errorf("compressToolOutput() = %q, want %q", result, tt.expect)
			}
		})
	}
}

func TestAgentExecutor_SaveKnowledge_NilStore(t *testing.T) {
	executor := &AgentExecutor{knowledge: nil}
	// 不应 panic
	executor.saveKnowledge(context.Background(), "test query", nil)
	executor.saveKnowledge(context.Background(), "test query", []ToolCallLog{
		{Tool: "http.probe", Input: `{"url":"http://example.com"}`},
	})
}

func TestAgentStepEvent(t *testing.T) {
	event := AgentStepEvent{
		Step:    0,
		Tool:    "http.probe",
		Status:  "done",
		Summary: "probe completed",
	}
	if event.Tool != "http.probe" {
		t.Errorf("Tool = %q, want http.probe", event.Tool)
	}
	if event.Status != "done" {
		t.Errorf("Status = %q, want done", event.Status)
	}
}
