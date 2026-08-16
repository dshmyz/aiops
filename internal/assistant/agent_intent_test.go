package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// queuedChat 按调用次序返回预设响应，Generate 与 Stream 共用同一 FIFO。
// calls 记录 Generate 被调用的次数（用于断言缓存命中零调用）。
type queuedChat struct {
	responses []*schema.Message
	calls     int
}

func (m *queuedChat) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.calls++
	if len(m.responses) == 0 {
		return &schema.Message{Content: "（无预设响应）"}, nil
	}
	msg := m.responses[0]
	m.responses = m.responses[1:]
	return msg, nil
}

func (m *queuedChat) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
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

// chunkedChat 把一个预设答案拆成多个 content chunk 流式返回，用于断言终轮
// content 按原 chunk 逐段 flush 为 delta（而非整段一波）。
type chunkedChat struct {
	chunks []*schema.Message
}

func (m *chunkedChat) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	var sb strings.Builder
	for _, c := range m.chunks {
		sb.WriteString(c.Content)
	}
	return &schema.Message{Content: sb.String()}, nil
}

func (m *chunkedChat) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray(m.chunks), nil
}

// TestAgentExecutorStreamsKnowledgeFinalAnswer 验证知识型问题（无工具注册、单轮即
// 终轮）在 onDelta 存在时流式生成最终答案：token 增量转发，且结果与一次性 Generate
// 一致。覆盖"最终答案流式"在知识型问题的落地。
func TestAgentExecutorStreamsKnowledgeFinalAnswer(t *testing.T) {
	const answer = "Kafka 是分布式消息中间件，用于高吞吐消息投递。"
	chat := &queuedChat{responses: []*schema.Message{{Content: answer}}}
	exec := &AgentExecutor{chat: chat, maxSteps: 3}
	var deltas []string
	result := exec.RunWithRoleCallbackStream(context.Background(), RoleSupervisor, "kafka 是什么", nil, nil, func(d string) { deltas = append(deltas, d) }, nil)
	if result.Error != nil {
		t.Fatalf("Run error: %v", result.Error)
	}
	if joined := strings.Join(deltas, ""); joined != answer {
		t.Fatalf("streamed deltas = %q, want answer streamed token-by-token", joined)
	}
	if result.Answer != answer {
		t.Fatalf("Answer = %q", result.Answer)
	}
	if len(result.ToolCalls) != 0 {
		t.Fatalf("ToolCalls = %+v, want none (knowledge path)", result.ToolCalls)
	}
}

// TestAgentExecutorKnowledgeStreamsPerChunk 钉住终轮 content 按原 chunk 逐段 flush
// 为 delta（每段增量渲染），而非整段一波。回归护栏：streamRound 若不按 chunk 转发，
// 知识/终轮答案会退化为单次大 delta，前端失去逐 token 手感。
func TestAgentExecutorKnowledgeStreamsPerChunk(t *testing.T) {
	ans1 := "Kafka 是分布式消息中间件。"
	ans2 := "它负责把数据从生产者"
	ans3 := "高效送到消费者，吞吐高、可水平扩展。"
	chat := &chunkedChat{chunks: []*schema.Message{{Content: ans1}, {Content: ans2}, {Content: ans3}}}
	exec := &AgentExecutor{chat: chat, maxSteps: 3}
	var deltas []string
	result := exec.RunWithRoleCallbackStream(context.Background(), RoleSupervisor, "kafka 是什么", nil, nil, func(d string) { deltas = append(deltas, d) }, nil)
	if result.Error != nil {
		t.Fatalf("Run error: %v", result.Error)
	}
	if len(deltas) != 3 {
		t.Fatalf("delta count = %d, want 3 (one per content chunk, not one wave)", len(deltas))
	}
	if deltas[0] != ans1 || deltas[1] != ans2 || deltas[2] != ans3 {
		t.Fatalf("per-chunk deltas = %q, want %q / %q / %q", deltas, ans1, ans2, ans3)
	}
}

// TestAgentExecutorKnowledgeNoStreamSameAsBefore 验证知识路径不带 onDelta 时
// 走原有一次性 Generate（回归护栏：非流式调用方行为不变）。
func TestAgentExecutorKnowledgeNoStreamSameAsBefore(t *testing.T) {
	const answer = "MinIO 是 S3 兼容的对象存储。"
	chat := &queuedChat{responses: []*schema.Message{{Content: answer}}}
	exec := &AgentExecutor{chat: chat, maxSteps: 3}
	result := exec.Run(context.Background(), "minio 是什么", nil)
	if result.Error != nil {
		t.Fatalf("Run error: %v", result.Error)
	}
	if result.Answer != answer {
		t.Fatalf("Answer = %q, want %q", result.Answer, answer)
	}
}

// TestAgentExecutorStreamsToolIntentPureAnswer 验证工具已注册、但 LLM 判断该问题
// 直接文字回答（知识型）时，最终答案以流式转发，且不会误调用工具。修复回归：
// 此前流式仅覆盖无工具的轮次；工具常驻后（planIntent 移除）知识型问题可能被
// 带偏去调工具，本测试钉住"有工具也不强制调用、答案照常流式"。
func TestAgentExecutorStreamsToolIntentPureAnswer(t *testing.T) {
	const answer = "Kafka 是一个分布式消息中间件。"
	chat := &queuedChat{responses: []*schema.Message{{Content: answer}}}
	rec := &recordingInvokableTool{name: "kafka.consumer_lag.read"}
	exec := &AgentExecutor{
		chat:     chat,
		tools:    []tool.BaseTool{rec},
		toolMap:  map[string]tool.BaseTool{"kafka.consumer_lag.read": rec},
		maxSteps: 5,
	}
	var deltas []string
	result := exec.RunWithRoleCallbackStream(context.Background(), RoleSupervisor, "minio 是什么", nil, nil, func(d string) { deltas = append(deltas, d) }, nil)
	if result.Error != nil {
		t.Fatalf("Run error: %v", result.Error)
	}
	if joined := strings.Join(deltas, ""); joined != answer {
		t.Fatalf("streamed deltas = %q, want generated answer %q", joined, answer)
	}
	if result.Answer != answer {
		t.Fatalf("Answer = %q, want %q", result.Answer, answer)
	}
	if rec.calls != 0 {
		t.Fatalf("tool calls = %d, want 0 (LLM answered directly despite tools offered)", rec.calls)
	}
}

// TestAgentExecutorStreamsNoToolsRegistered 验证执行器未注册任何工具时，流式请求
// 首轮即终轮流式生成（0 工具可用的轮次即终轮）。
func TestAgentExecutorStreamsNoToolsRegistered(t *testing.T) {
	const answer = "RocketMQ 是阿里巴巴开源的分布式消息中间件。"
	chat := &queuedChat{responses: []*schema.Message{{Content: answer}}}
	exec := &AgentExecutor{chat: chat, maxSteps: 3}
	var deltas []string
	result := exec.RunWithRoleCallbackStream(context.Background(), RoleSupervisor, "rocketmq 是什么", nil, nil, func(d string) { deltas = append(deltas, d) }, nil)
	if result.Error != nil {
		t.Fatalf("Run error: %v", result.Error)
	}
	if joined := strings.Join(deltas, ""); joined != answer {
		t.Fatalf("streamed deltas = %q, want %q", joined, answer)
	}
	if result.Answer != answer {
		t.Fatalf("Answer = %q", result.Answer)
	}
}

// TestAgentExecutorStreamsToolPathFinalAnswer 验证工具路径（实际调用过工具）的
// 终轮也流式转发最终答案：流式请求每轮都走 streamRound，终轮（无工具调用）把
// 累积 content 整体 flush 为 delta。修复回归：移除 LLM formatter 的二阶段整形后，
// 工具路径的流式由 executor 承担；此前已调工具（allToolCalls>0）时不再二次生成。
func TestAgentExecutorStreamsToolPathFinalAnswer(t *testing.T) {
	const answer = "Kafka 消费组 lag 正常，无堆积。"
	chat := &queuedChat{responses: []*schema.Message{
		{
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
		{Content: answer},
	}}
	rec := &recordingInvokableTool{name: "kafka.consumer_lag.read"}
	exec := &AgentExecutor{
		chat:     chat,
		tools:    []tool.BaseTool{rec},
		toolMap:  map[string]tool.BaseTool{"kafka.consumer_lag.read": rec},
		maxSteps: 5,
	}
	var deltas []string
	result := exec.RunWithRoleCallbackStream(context.Background(), RoleSupervisor, "查 kafka 消费延迟", nil, nil, func(d string) { deltas = append(deltas, d) }, nil)
	if result.Error != nil {
		t.Fatalf("Run error: %v", result.Error)
	}
	if rec.calls != 1 {
		t.Fatalf("tool calls = %d, want 1 (tool path must execute the tool)", rec.calls)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Tool != "kafka.consumer_lag.read" {
		t.Fatalf("ToolCalls = %+v, want the executed tool recorded", result.ToolCalls)
	}
	if joined := strings.Join(deltas, ""); joined != answer {
		t.Fatalf("streamed deltas = %q, want tool-path final answer streamed %q", joined, answer)
	}
	if result.Answer != answer {
		t.Fatalf("Answer = %q, want %q", result.Answer, answer)
	}
}

// TestAgentExecutorStreamingForwardsThinking 验证流式请求把模型推理 token 实时转发
// 给 onThinking（独立通道），终轮答案继续走 onDelta——工具轮的先导叙述（content
// 在无工具调用时才是答案）不会混入 delta。
func TestAgentExecutorStreamingForwardsThinking(t *testing.T) {
	const answer = "Kafka 消费组 lag 正常，无堆积。"
	chat := &queuedChat{responses: []*schema.Message{
		{Content: answer, ReasoningContent: "先查消费延迟，再给结论。"},
	}}
	exec := &AgentExecutor{chat: chat, maxSteps: 3}
	var thinking []string
	var deltas []string
	result := exec.RunWithRoleCallbackStream(context.Background(), RoleSupervisor, "查 kafka 消费延迟", nil, nil, func(d string) { deltas = append(deltas, d) }, func(t string) { thinking = append(thinking, t) })
	if result.Error != nil {
		t.Fatalf("Run error: %v", result.Error)
	}
	if joined := strings.Join(thinking, ""); joined != "先查消费延迟，再给结论。" {
		t.Fatalf("thinking = %q, want reasoning forwarded to onThinking", joined)
	}
	if joined := strings.Join(deltas, ""); joined != answer {
		t.Fatalf("streamed deltas = %q, want answer flushed %q", joined, answer)
	}
}

// TestAgentExecutorStreamingBypassesCache 验证流式请求跳过缓存读：缓存命中会绕过
// 流式生成、整段"一波"返回（修复前重复提问同一消息时前端不增量渲染）。非流式请求
// 仍命中缓存（零 LLM 调用），流式请求必须重新调 LLM 产出逐 token delta。
func TestAgentExecutorStreamingBypassesCache(t *testing.T) {
	const answer = "Kafka 是分布式消息中间件。"
	ctx := context.Background()
	chat := &queuedChat{
		responses: []*schema.Message{{Content: answer}},
	}
	exec := &AgentExecutor{chat: chat, cache: NewResponseCache(10, time.Minute), maxSteps: 3}

	// 预填充缓存：模拟该问题此前已请求过一次（非流式），结果已入缓存。
	cached := &AgentRunResult{Answer: "旧缓存答案"}
	exec.cache.Set(ctx, "kafka 是什么", cached)

	// 非流式：命中缓存，零 LLM 调用，直接返回缓存答案。
	before := chat.calls
	nonStreaming := exec.RunWithRoleCallback(ctx, RoleSupervisor, "kafka 是什么", nil, nil)
	if nonStreaming.Error != nil {
		t.Fatalf("non-streaming Run error: %v", nonStreaming.Error)
	}
	if nonStreaming.Answer != "旧缓存答案" {
		t.Fatalf("non-streaming Answer = %q, want cached answer", nonStreaming.Answer)
	}
	if chat.calls != before {
		t.Fatalf("non-streaming made %d LLM calls, want 0 (cache hit)", chat.calls-before)
	}

	// 流式：必须绕过缓存重新调 LLM，并产出 delta。
	before = chat.calls
	var deltas []string
	streamed := exec.RunWithRoleCallbackStream(ctx, RoleSupervisor, "kafka 是什么", nil, nil, func(d string) { deltas = append(deltas, d) }, nil)
	if streamed.Error != nil {
		t.Fatalf("streaming Run error: %v", streamed.Error)
	}
	if chat.calls == before {
		t.Fatal("streaming request should bypass cache and re-call LLM")
	}
	if joined := strings.Join(deltas, ""); joined != answer {
		t.Fatalf("streamed deltas = %q, want fresh answer %q (cached one-wave result must not be served)", joined, answer)
	}
	if streamed.Answer != answer {
		t.Fatalf("streaming Answer = %q, want fresh %q", streamed.Answer, answer)
	}
}