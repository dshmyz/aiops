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

