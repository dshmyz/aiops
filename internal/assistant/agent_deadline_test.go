package assistant

// 时间预算感知收尾的回归测试：长 agent 循环耗尽请求 deadline 时，已采集的
// 工具证据不能作废——要么无工具收尾、要么确定性摘要，绝不 502。

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// TestAgentExecutorWrapsUpWhenTimeBudgetTight：第一轮工具执行后剩余时间低于
// wrapupLLMMinTime → 不再开新工具轮，无 tools 收尾，答案不丢、证据保留。
func TestAgentExecutorWrapsUpWhenTimeBudgetTight(t *testing.T) {
	probe := &recordingInvokableTool{name: "kafka.lag.read"}
	chat := &queuedChat{responses: []*schema.Message{
		{ToolCalls: []schema.ToolCall{{ID: "c1", Type: "function", Function: schema.FunctionCall{Name: "kafka.lag.read", Arguments: `{}`}}}},
		{Content: "基于已采集数据的结论：消费正常。"},
	}}
	exec := &AgentExecutor{
		chat:     chat,
		tools:    []tool.BaseTool{probe},
		toolMap:  map[string]tool.BaseTool{"kafka.lag.read": probe},
		maxSteps: 8,
	}
	// 10s 剩余 < wrapupLLMMinTime(25s)：首轮工具执行后应立即收尾，
	// 而不是继续开第二轮工具轮直到把时间烧光。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result := exec.Run(ctx, "kafka 消费有积压吗", nil)
	if result.Error != nil {
		t.Fatalf("want graceful wrap-up, got error: %v", result.Error)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1 (evidence must survive)", len(result.ToolCalls))
	}
	if result.Answer == "" {
		t.Fatalf("wrap-up answer is empty")
	}
}

// ctxCancellingChat 第一次 Generate 时取消 ctx 并返回一个工具调用：
// 模拟"deadline 在循环中途到期"。
type ctxCancellingChat struct {
	cancel context.CancelFunc
	called bool
}

func (m *ctxCancellingChat) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if !m.called {
		m.called = true
		m.cancel()
		return &schema.Message{ToolCalls: []schema.ToolCall{{
			ID:   "c1",
			Type: "function",
			Function: schema.FunctionCall{
				Name:      "kafka.lag.read",
				Arguments: `{}`,
			},
		}}}, nil
	}
	return &schema.Message{Content: "不应到达这里：deadline 已到期"}, nil
}

func (m *ctxCancellingChat) Stream(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

// TestAgentExecutorDigestsWhenDeadlineExpiredMidLoop：deadline 在工具执行后、
// 下一轮之前到期 → 降级确定性摘要（含工具证据），Error 必须为 nil。
func TestAgentExecutorDigestsWhenDeadlineExpiredMidLoop(t *testing.T) {
	probe := &recordingInvokableTool{name: "kafka.lag.read"}
	ctx, cancel := context.WithCancel(context.Background())
	chat := &ctxCancellingChat{cancel: cancel}
	exec := &AgentExecutor{
		chat:     chat,
		tools:    []tool.BaseTool{probe},
		toolMap:  map[string]tool.BaseTool{"kafka.lag.read": probe},
		maxSteps: 8,
	}
	defer cancel()

	result := exec.Run(ctx, "kafka 消费有积压吗", nil)
	if result.Error != nil {
		t.Fatalf("want digest fallback, got error: %v", result.Error)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(result.ToolCalls))
	}
	if !strings.Contains(result.Answer, "kafka.lag.read") {
		t.Fatalf("digest answer missing tool evidence: %q", result.Answer)
	}
}

// TestPersistTurnsSurvivesExpiredRequestDeadline：请求 deadline 已死时，
// 已算出的回答仍能落库（WithoutCancel 解耦）——此前记账失败会把答案整单丢弃。
func TestPersistTurnsSurvivesExpiredRequestDeadline(t *testing.T) {
	convs := store.NewMemoryAssistantConversationStore()
	svc := &Service{conversations: convs}

	ctx, cancel := context.WithCancel(context.Background())
	conv, err := convs.CreateConversation(ctx, "admin-1", "标题", "预览", time.Now().UTC())
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	cancel() // 请求 deadline 到期（ctx 已死）

	turnID, err := svc.persistTurns(ctx, conv.ID, "kafka 有积压吗", Response{Type: "answer", Message: "lag=1288，建议扩容消费者"})
	if err != nil {
		t.Fatalf("persistTurns with dead ctx: %v (已算出的回答不能因记账失败丢弃)", err)
	}
	if turnID == "" {
		t.Fatalf("assistant turn id is empty")
	}
	turns, err := convs.ListTurns(context.Background(), conv.ID, 10, "")
	if err != nil {
		t.Fatalf("list turns: %v", err)
	}
	if len(turns.Turns) != 2 { // user turn + assistant turn
		t.Fatalf("turns = %d, want 2", len(turns.Turns))
	}
}
