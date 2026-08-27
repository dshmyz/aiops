package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
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

// TestBuildProducerIndex 钉住参数生产者索引的匹配规则：已发布读工具的
// Output.Fields 键 ↔ 其它能力必填入参同名才算；未发布/非读/非必填一律不进。
func TestBuildProducerIndex(t *testing.T) {
	caps := []capabilities.Capability{
		{
			Name: "kafka.topic.list", Status: capabilities.StatusPublished, Operation: tools.Read,
			Output: capabilities.OutputSpec{Fields: map[string]string{"topic": "$.data[*].name", "broker": "$.data[0].broker"}},
		},
		{
			// 未发布：不作为产出方，但它自身的必填缺失提示仍可引用别的产出方（不影响）
			Name: "kafka.topic.draft", Status: "draft", Operation: tools.Read,
			Output: capabilities.OutputSpec{Fields: map[string]string{"topic": "$.name"}},
		},
		{
			// 已发布但写操作：不作为产出方
			Name: "kafka.topic.update", Status: capabilities.StatusPublished, Operation: tools.Write,
			Output: capabilities.OutputSpec{Fields: map[string]string{"topic": "$.name"}},
		},
		{
			Name: "kafka.topic.update", Status: capabilities.StatusPublished, Operation: tools.Write,
			InputSchema: map[string]capabilities.InputField{
				"topic": {Type: "string", Required: true},
				"note":  {Type: "string"}, // 非必填不索引
			},
		},
	}
	idx := buildProducerIndex(caps)
	if got := idx["topic"]; len(got) != 1 || got[0] != "kafka.topic.list" {
		t.Fatalf("index[topic] = %v, want [kafka.topic.list] (draft/write producers excluded)", got)
	}
	if _, ok := idx["note"]; ok {
		t.Fatal("optional field must not be indexed")
	}
	if len(idx) != 1 {
		t.Fatalf("index size = %d, want 1 (only topic)", len(idx))
	}
	if buildProducerIndex(nil) != nil {
		t.Fatal("nil caps must yield nil index")
	}
}

// TestMissingParamHint 钉住缺参引导文案的三种形态：有产出方→指路工具；
// 无产出方→要求结合上下文勿猜；非缺参错误→空串。
func TestMissingParamHint(t *testing.T) {
	exec := &AgentExecutor{paramProducers: producerIndex{
		"topic": {"kafka.topic.list"},
	}}
	hint := exec.missingParamHint("kafka.topic.update", `execute kafka.topic.update: missing required input "topic"`)
	if !strings.Contains(hint, "缺失必填参数「topic」") {
		t.Fatalf("hint = %q, want missing-param framing", hint)
	}
	if !strings.Contains(hint, "kafka.topic.list") {
		t.Fatalf("hint = %q, want producer tool named", hint)
	}

	noProducer := exec.missingParamHint("x.y.read", `missing required input "unknown_param"`)
	if !strings.Contains(noProducer, "不要猜测") {
		t.Fatalf("no-producer hint = %q, want anti-guess guidance", noProducer)
	}

	plain := exec.missingParamHint("x.y.read", "connection refused")
	if plain != "" {
		t.Fatalf("non-missing-param error hint = %q, want empty", plain)
	}
}
