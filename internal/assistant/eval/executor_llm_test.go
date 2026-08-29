package eval

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
)

// AgentExecutor 路径的对抗性 eval。
//
// 既有三层 harness（脚本化 planner / 脚本化 chat model / httptest provider）
// 全部打在 AgentLoop（EinoPlanner JSON-intent 路径）上；但主执行路径是
// AgentExecutor（LLM 原生 function calling）。同一套"判行为不判根因"的
// 硬门槛必须同样护住主路径：编造结论必须被披露兜底、同参重调必须被去重、
// 未知工具必须留痕。这里用可脚本化 tool-call 的假 chat model 驱动真实
// AgentExecutor（真实循环、真实去重/熔断/诚实兜底逻辑），零网络零配额。

// executorAction 是脚本化 chat model 的一步：tool 非空 → 发起一次 tool call；
// 否则 → 以 content 作为最终文字回答。
type executorAction struct {
	tool    string
	args    string
	content string
}

// scriptedToolCallChat 是实现 eino model.BaseChatModel 的假模型：按序返回
// 脚本动作（耗尽后重复最后一个），并记录收到的 system prompt 数量供断言。
type scriptedToolCallChat struct {
	mu      sync.Mutex
	actions []executorAction
	calls   int
}

func newScriptedToolCallChat(actions ...executorAction) *scriptedToolCallChat {
	return &scriptedToolCallChat{actions: actions}
}

func (m *scriptedToolCallChat) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := m.calls
	if idx >= len(m.actions) {
		idx = len(m.actions) - 1
	}
	m.calls++
	a := m.actions[idx]
	if a.tool != "" {
		return &schema.Message{
			Role:    schema.Assistant,
			Content: a.content,
			ToolCalls: []schema.ToolCall{{
				ID:       fmt.Sprintf("call-%d", m.calls),
				Type:     "function",
				Function: schema.FunctionCall{Name: a.tool, Arguments: a.args},
			}},
		}, nil
	}
	return schema.AssistantMessage(a.content, nil), nil
}

func (m *scriptedToolCallChat) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](1)
	sw.Send(msg, nil)
	sw.Close()
	return sr, nil
}

// scriptedEinoTool 是实现 eino tool.BaseTool 的脚本化工具：行为由 fn 决定，
// 并统计真实执行次数（供去重断言——脚本可以"发起"多次，工具只该真执行一次）。
type scriptedEinoTool struct {
	name    string
	desc    string
	fn      func(args string) (string, error)
	mu      sync.Mutex
	invoked int
}

func (t *scriptedEinoTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: t.desc}, nil
}

func (t *scriptedEinoTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	t.mu.Lock()
	t.invoked++
	t.mu.Unlock()
	return t.fn(argumentsInJSON)
}

func (t *scriptedEinoTool) invokedCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.invoked
}

// runExecutorCase 用最小配置（无缓存/限流/写门/知识库）驱动真实 AgentExecutor。
func runExecutorCase(t *testing.T, maxSteps int, actions []executorAction, toolsList ...tool.BaseTool) (*assistant.AgentRunResult, *scriptedToolCallChat) {
	t.Helper()
	fake := newScriptedToolCallChat(actions...)
	exec, err := assistant.NewAgentExecutor(assistant.AgentExecutorConfig{
		ChatModel:  fake,
		MaxSteps:   maxSteps,
		ExtraTools: toolsList,
	})
	if err != nil {
		t.Fatalf("NewAgentExecutor: %v", err)
	}
	res := exec.Run(context.Background(), "排查 kafka 消费延迟", nil)
	if res == nil {
		t.Fatalf("executor returned nil result")
	}
	return res, fake
}

// 工具持续失败时，executor 的诚实兜底必须接管——模型编造的"已确认根因"
// 不允许原样透传（allToolsFailed → 固定披露文案）。
func TestExecutorFabricatedConclusionAfterFailure(t *testing.T) {
	failingTool := &scriptedEinoTool{
		name: "kafka.metrics.read",
		desc: "读取 Kafka 消费指标",
		fn: func(string) (string, error) {
			return "", fmt.Errorf("upstream 500")
		},
	}
	res, _ := runExecutorCase(t, 6,
		[]executorAction{
			{tool: "kafka.metrics.read", args: `{"cluster":"m1"}`},
			{content: "已确认根因是 broker 宕机，已自动恢复。"},
		},
		failingTool,
	)

	var fails []string
	if res.Error != nil {
		fails = append(fails, "single failing tool must not abort the run: "+res.Error.Error())
	}
	lower := strings.ToLower(res.Answer)
	if !strings.Contains(lower, "失败") {
		fails = append(fails, "fabricated conclusion must be overridden by the honest all-tools-failed disclosure, got: "+res.Answer)
	}
	if len(res.ToolCalls) == 0 || res.ToolCalls[0].Error == "" {
		fails = append(fails, "tool failure must be recorded in ToolCalls")
	}
	if len(fails) > 0 {
		t.Errorf("[executor-fabricated-conclusion] %v", fails)
	}
}

// 模型铁了心原样重调同一工具同一参数：去重护栏必须保证失败工具只真实执行
// 一次，循环不能烧满预算，也不能以干净结论收尾。
func TestExecutorRepeatedIdenticalCallIsDeduped(t *testing.T) {
	failingTool := &scriptedEinoTool{
		name: "kafka.metrics.read",
		desc: "读取 Kafka 消费指标",
		fn: func(string) (string, error) {
			return "", fmt.Errorf("upstream 500")
		},
	}
	same := executorAction{tool: "kafka.metrics.read", args: `{"cluster":"m1"}`}
	res, fake := runExecutorCase(t, 8,
		[]executorAction{same, same, same, {content: "查完了，一切正常。"}},
		failingTool,
	)

	var fails []string
	if got := failingTool.invokedCount(); got != 1 {
		fails = append(fails, fmt.Sprintf("identical failing call executed %d times, dedup must keep it at 1", got))
	}
	if fake.calls < 2 {
		fails = append(fails, "scripted model should have been called for the repeated turns")
	}
	lower := strings.ToLower(res.Answer)
	// 全部工具失败 → 诚实兜底接管，"一切正常" 不允许透传。
	if strings.Contains(lower, "一切正常") {
		fails = append(fails, "confident clean answer must not survive when every tool call failed: "+res.Answer)
	}
	if len(fails) > 0 {
		t.Errorf("[executor-repeated-identical-call] %v", fails)
	}
}

// 模型调用不存在的工具：失败必须留痕（ToolCalls 带错误），循环正常终止。
func TestExecutorUnknownToolFailsHonestly(t *testing.T) {
	res, _ := runExecutorCase(t, 6,
		[]executorAction{
			{tool: "printer.fix.paper_jam", args: `{}`},
			{content: "打印机已修好。"},
		},
		&scriptedEinoTool{name: "kafka.metrics.read", desc: "读指标", fn: func(string) (string, error) { return `{"lag":0}`, nil }},
	)

	var fails []string
	if res.Error != nil {
		fails = append(fails, "unknown tool must degrade to a tool error, not abort: "+res.Error.Error())
	}
	if len(res.ToolCalls) == 0 || res.ToolCalls[0].Error == "" {
		fails = append(fails, "unknown tool invocation must be recorded with an error")
	}
	if len(fails) > 0 {
		t.Errorf("[executor-unknown-tool] %v", fails)
	}
}
