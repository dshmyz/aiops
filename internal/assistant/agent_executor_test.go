package assistant

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

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

// TestRunWithCallbackStepEventCarriesInputOutput 钉住前端"输入/输出展开面板"的
// 数据契约：onStep 事件必须携带 input（LLM 入参）与 output（工具原始返回），
// 失败时必须带 error。修复前 AgentStepEvent 只有 tool/status/summary，转换到
// StepEvent 时 Input/Output 恒为 nil，AssistantSteps 的面板永远空白——用户无法
// 判断"到底干没干、干了什么"。
func TestRunWithCallbackStepEventCarriesInputOutput(t *testing.T) {
	ctx := context.Background()
	rec := &payloadTool{name: "kafka.consumer_lag.read", payload: `{"status":"ok","summary":"lag=0","severity":"info"}`}
	exec := &AgentExecutor{
		chat: &queuedChat{responses: []*schema.Message{
			{ToolCalls: []schema.ToolCall{{ID: "c1", Type: "function", Function: schema.FunctionCall{Name: "kafka.consumer_lag.read", Arguments: `{"group":"g1"}`}}}},
			{Content: "done"},
		}},
		reasoningChat: blankReasoner{},
		toolMap:       map[string]tool.BaseTool{"kafka.consumer_lag.read": rec},
		maxSteps:      3,
	}
	var events []AgentStepEvent
	result := exec.RunWithCallback(ctx, "查看消费组", nil, func(e AgentStepEvent) {
		events = append(events, e)
	})
	if result.Error != nil {
		t.Fatalf("Run error: %v", result.Error)
	}
	if len(events) != 1 {
		t.Fatalf("step events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Input == nil || ev.Input["group"] != "g1" {
		t.Fatalf("event.Input = %+v, want parsed LLM arguments with group=g1 (frontend input panel depends on it)", ev.Input)
	}
	if ev.Output == nil || ev.Output["summary"] != "lag=0" {
		t.Fatalf("event.Output = %+v, want tool output with summary (frontend output panel depends on it)", ev.Output)
	}
	if ev.Summary != "lag=0" {
		t.Fatalf("Summary = %q, want lag=0 (kept from output.summary)", ev.Summary)
	}
	if ev.Error != "" {
		t.Fatalf("Error = %q, want empty on success", ev.Error)
	}
}

// payloadTool 返回预设 JSON 的只读工具替身（带 summary 输出）。
type payloadTool struct {
	name    string
	payload string
}

func (t *payloadTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name}, nil
}
func (t *payloadTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	return t.payload, nil
}

// failureInvokableTool 总是返回错误的工具替身。
type failureInvokableTool struct{}

func (failureInvokableTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "kafka.broker.read"}, nil
}
func (failureInvokableTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	return "", errors.New("connection refused")
}

func TestRunWithCallbackStepEventCarriesError(t *testing.T) {
	exec := &AgentExecutor{
		chat: &queuedChat{responses: []*schema.Message{
			{ToolCalls: []schema.ToolCall{{ID: "c1", Type: "function", Function: schema.FunctionCall{Name: "kafka.broker.read", Arguments: `{}`}}}},
			{Content: "failed"},
		}},
		reasoningChat: blankReasoner{},
		toolMap:       map[string]tool.BaseTool{"kafka.broker.read": failureInvokableTool{}},
		maxSteps:      3,
	}
	var events []AgentStepEvent
	result := exec.RunWithCallback(context.Background(), "查看 broker", nil, func(e AgentStepEvent) {
		events = append(events, e)
	})
	if result.Error != nil {
		t.Fatalf("Run error: %v", result.Error)
	}
	if len(events) != 1 {
		t.Fatalf("step events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Status != "error" {
		t.Fatalf("Status = %q, want error", ev.Status)
	}
	if ev.Error != "connection refused" {
		t.Fatalf("Error = %q, want connection refused surfaced to frontend", ev.Error)
	}
}
