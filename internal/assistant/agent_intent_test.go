package assistant

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// namedTool 是最小工具替身：只实现 Info（toolsForDomain 只用 Info）。
type namedTool struct{ name string }

func (t namedTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name}, nil
}

// TestToolsForDomain 验证按意图规划的域裁剪工具：
//   - 只给该域的能力工具（工具集 = 意图投影）
//   - 知识型（域为空）→ 空工具集
func TestToolsForDomain(t *testing.T) {
	exec := &AgentExecutor{tools: []tool.BaseTool{
		namedTool{name: "kafka.consumer_lag.read"},
		namedTool{name: "kafka.topic.retention.read"},
		namedTool{name: "minio.bucket.health.read"},
	}}

	cases := []struct {
		domain string
		want   []string
	}{
		{domain: "", want: nil},
		{domain: "kafka", want: []string{"kafka.consumer_lag.read", "kafka.topic.retention.read"}},
		{domain: "minio", want: []string{"minio.bucket.health.read"}},
		{domain: "rocketmq", want: nil}, // 未注册域 → 空
	}

	for _, c := range cases {
		got := exec.toolsForDomain(c.domain)
		gotNames := make([]string, 0, len(got))
		for _, g := range got {
			info, _ := g.Info(context.Background())
			gotNames = append(gotNames, info.Name)
		}
		if len(gotNames) != len(c.want) {
			t.Errorf("toolsForDomain(%q) = %v, want %v", c.domain, gotNames, c.want)
			continue
		}
		for i := range c.want {
			if gotNames[i] != c.want[i] {
				t.Errorf("toolsForDomain(%q)[%d] = %q, want %q", c.domain, i, gotNames[i], c.want[i])
			}
		}
	}
}

// scriptedChat 按调用次序返回预设响应，用于测 planIntent。
type scriptedChat struct{ responses []*schema.Message }

func (m *scriptedChat) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if len(m.responses) == 0 {
		return &schema.Message{Content: `{"intent":"knowledge","domain":null}`}, nil
	}
	msg := m.responses[0]
	m.responses = m.responses[1:]
	return msg, nil
}

func (m *scriptedChat) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

// TestPlanIntent 验证意图规划：LLM 语义理解意图，不依赖任何关键词/分隔符规则。
// "看看rocketmq的命令"（粘连中文）和"查一下 kafka 的 consumer lag"都能正确分类，
// 用户怎么问都行。
func TestPlanIntent(t *testing.T) {
	cases := []struct {
		name     string
		chatResp string
		want     intentPlan
	}{
		{
			name:     "knowledge",
			chatResp: `{"intent":"knowledge","domain":null}`,
			want:     intentPlan{Intent: "knowledge", Domain: ""},
		},
		{
			name:     "tool_call with domain",
			chatResp: `{"intent":"tool_call","domain":"kafka"}`,
			want:     intentPlan{Intent: "tool_call", Domain: "kafka"},
		},
		{
			name:     "unknown intent falls back to tool_call",
			chatResp: `{"intent":"whatever","domain":"kafka"}`,
			want:     intentPlan{Intent: "tool_call", Domain: "kafka"},
		},
		{
			name:     "invalid json falls back to tool_call",
			chatResp: `not json`,
			want:     intentPlan{Intent: "tool_call", Domain: ""},
		},
		{
			name:     "markdown-wrapped json parsed",
			chatResp: "```json\n{\"intent\":\"knowledge\",\"domain\":null}\n```",
			want:     intentPlan{Intent: "knowledge", Domain: ""},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			exec := &AgentExecutor{chat: &scriptedChat{responses: []*schema.Message{{Content: c.chatResp}}}}
			got := exec.planIntent(context.Background(), "随便什么消息")
			if got.Intent != c.want.Intent || got.Domain != c.want.Domain {
				t.Fatalf("planIntent = %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestPlanIntentNilChat 验证无 chat 时回退 tool_call（不 panic）。
func TestPlanIntentNilChat(t *testing.T) {
	exec := &AgentExecutor{}
	got := exec.planIntent(context.Background(), "hi")
	if got.Intent != "tool_call" {
		t.Fatalf("planIntent = %+v, want tool_call fallback", got)
	}
}

// recordingInvokableTool 是可被 agent loop 执行的最小工具替身。
type recordingInvokableTool struct {
	name  string
	calls int
}

func (t *recordingInvokableTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name}, nil
}

func (t *recordingInvokableTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	t.calls++
	return `{"status":"ok"}`, nil
}

// intentFallbackChat 按调用次序返回预设响应：第 1 次意图规划、第 2 次 ReAct
// 发工具调用、之后最终回复。
type intentFallbackChat struct {
	planIntentResp string
	step2Resp      *schema.Message
	finalResp      *schema.Message
	calls          int
}

func (m *intentFallbackChat) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.calls++
	switch m.calls {
	case 1:
		return &schema.Message{Content: m.planIntentResp}, nil
	case 2:
		return m.step2Resp, nil
	default:
		return m.finalResp, nil
	}
}

func (m *intentFallbackChat) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

// TestAgentExecutorFallsBackToAllToolsWhenIntentFails 验证意图规划失败（LLM 调用/
// JSON 解析失败，回退 tool_call + 空域）时，ReAct 循环回退全量工具集，实时数据
// 类请求仍能取到数据。
//
// 修复回归：planIntent 失败时 toolsForDomain("") 返回 nil，ReAct 带 0 工具执行，
// "查 kafka 消费延迟"这类请求被 LLM 纯文字作答（编造数据或回"无法获取"）。
func TestAgentExecutorFallsBackToAllToolsWhenIntentFails(t *testing.T) {
	rec := &recordingInvokableTool{name: "kafka.consumer_lag.read"}
	chat := &intentFallbackChat{
		planIntentResp: `not json`, // 意图规划解析失败 → 回退 tool_call + 空域
		step2Resp: &schema.Message{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: schema.FunctionCall{
					Name:      "kafka.consumer_lag.read",
					Arguments: `{"environment":"prod"}`,
				},
			}},
		},
		finalResp: &schema.Message{Content: "查到了"},
	}
	exec := &AgentExecutor{
		chat:     chat,
		tools:    []tool.BaseTool{rec},
		toolMap:  map[string]tool.BaseTool{"kafka.consumer_lag.read": rec},
		maxSteps: 5,
	}

	result := exec.Run(context.Background(), "查一下 kafka 消费延迟", nil)
	if result.Error != nil {
		t.Fatalf("Run error: %v", result.Error)
	}
	if rec.calls != 1 {
		t.Fatalf("tool calls = %d, want 1 (tool set should fall back to all tools when intent planning fails)", rec.calls)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Tool != "kafka.consumer_lag.read" {
		t.Fatalf("ToolCalls = %+v, want the fallback tool to be executed", result.ToolCalls)
	}
}

