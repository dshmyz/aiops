package assistant_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
)

// mockChatModel 是 LLMFormatter 测试用的 model.BaseChatModel 桩。
// content 决定 Generate 返回的 AssistantMessage 内容；streamChunks 非空时
// Stream 按块返回（模拟 token 流），否则退回 content 单块；genErr 决定是否返回错误；
// emptyFirstStreams 使前 N 次 Stream 返回空流（模拟 provider 偶发空响应，测试重试）。
type mockChatModel struct {
	content           string
	streamChunks      []string
	genErr            error
	emptyFirstStreams int
}

func (m *mockChatModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if m.genErr != nil {
		return nil, m.genErr
	}
	return schema.AssistantMessage(m.content, nil), nil
}

func (m *mockChatModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if m.genErr != nil {
		return nil, m.genErr
	}
	if m.emptyFirstStreams > 0 {
		m.emptyFirstStreams--
		return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("", nil)}), nil
	}
	chunks := m.streamChunks
	if len(chunks) == 0 {
		chunks = []string{m.content}
	}
	msgs := make([]*schema.Message, 0, len(chunks))
	for _, c := range chunks {
		msgs = append(msgs, schema.AssistantMessage(c, nil))
	}
	return schema.StreamReaderFromArray(msgs), nil
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

func TestLLMFormatterProseBecomesSummary(t *testing.T) {
	t.Parallel()
	// LLM 未按分隔符/JSON 格式输出、直接写散文时，把全文当作 summary 保住最终答复
	//（不再无谓回退 code 兜底）。
	chat := &mockChatModel{content: "Kafka 集群健康，3 节点在线"}
	f := assistant.NewLLMFormatter(chat)
	result, err := f.Format(context.Background(), assistant.FormatRequest{Tool: "t", Answer: map[string]any{}})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if result.Summary != "Kafka 集群健康，3 节点在线" {
		t.Fatalf("Summary = %q, want prose as summary", result.Summary)
	}
	if len(result.Blocks) != 0 {
		t.Fatalf("Blocks = %+v, want none for prose output", result.Blocks)
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

func TestLLMFormatterParsesDelimitedFormat(t *testing.T) {
	t.Parallel()
	chat := &mockChatModel{content: "[[SUMMARY_START]]Kafka 集群健康，3 节点在线[[SUMMARY_END]]\n[[BLOCKS_START]][{\"type\":\"incident_card\",\"title\":\"诊断结果\",\"content\":\"healthy\",\"payload\":{\"status\":\"healthy\"}}][[BLOCKS_END]]"}
	f := assistant.NewLLMFormatter(chat)
	result, err := f.Format(context.Background(), assistant.FormatRequest{Tool: "t", Answer: map[string]any{}})
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

// TestLLMFormatterStreamForwardsSummaryDelta 验证 FormatStream：SUMMARY 区间的
// token 被实时转发（含分隔符被 chunk 切分的边界），BLOCKS 区间不泄漏到 delta，
// 流结束后返回完整 Summary + Blocks。
func TestLLMFormatterStreamForwardsSummaryDelta(t *testing.T) {
	t.Parallel()
	chat := &mockChatModel{streamChunks: []string{
		"前置说明 [[SUMMARY_START]]Kafka 集",                 // 标记与 SUMMARY 前缀同块
		"群健康，3 节点在线[[SUMMARY_END]]",                  // SUMMARY 结尾与结束标记同块
		"[[BLOCKS_START]][{\"type\":\"incident_card\",\"title\":\"诊断\"}][[BLOCKS_END]]",
	}}
	f := assistant.NewLLMFormatter(chat)
	var deltas []string
	result, err := f.FormatStream(context.Background(), assistant.FormatRequest{Tool: "t", Answer: map[string]any{}}, func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("FormatStream: %v", err)
	}
	joined := strings.Join(deltas, "")
	if joined != "Kafka 集群健康，3 节点在线" {
		t.Fatalf("streamed deltas = %q, want summary text only (no markers/blocks)", joined)
	}
	if result.Summary != "Kafka 集群健康，3 节点在线" {
		t.Fatalf("Summary = %q", result.Summary)
	}
	if len(result.Blocks) != 1 || result.Blocks[0].Type != assistant.BlockIncidentCard {
		t.Fatalf("Blocks = %+v", result.Blocks)
	}
}

func TestLLMFormatterStreamGenerateError(t *testing.T) {
	t.Parallel()
	chat := &mockChatModel{genErr: errors.New("upstream unavailable")}
	f := assistant.NewLLMFormatter(chat)
	_, err := f.FormatStream(context.Background(), assistant.FormatRequest{Tool: "t", Answer: map[string]any{}}, nil)
	if err == nil {
		t.Fatal("expected error when chat.Stream fails")
	}
}

// TestLLMFormatterStreamForwardsProseDelta 验证 LLM 未按分隔符格式、直接输出散文时，
// FormatStream 把全文逐 token 转发（纯文本模式），结果 summary 与流式文本一致。
// 修复：此前散文输出解析失败 → ChainedFormatter 回退 code 兜底，工具路径"一波"输出。
func TestLLMFormatterStreamForwardsProseDelta(t *testing.T) {
	t.Parallel()
	chat := &mockChatModel{streamChunks: []string{"Kafka 集群健康，", "3 节点在线，无离线节点", "。"}}
	f := assistant.NewLLMFormatter(chat)
	var deltas []string
	result, err := f.FormatStream(context.Background(), assistant.FormatRequest{Tool: "t", Answer: map[string]any{}}, func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("FormatStream: %v", err)
	}
	joined := strings.Join(deltas, "")
	if joined != "Kafka 集群健康，3 节点在线，无离线节点。" {
		t.Fatalf("streamed deltas = %q, want prose streamed whole", joined)
	}
	if result.Summary != joined {
		t.Fatalf("Summary = %q, want %q", result.Summary, joined)
	}
}

// TestLLMFormatterStreamTrimsPartialMarkerTail 验证纯文本模式下 chunk 切分停在半截
// 标记前缀处时，不把半截标记（如 "[[SUMMARY_EN"）泄漏给前端。
func TestLLMFormatterStreamTrimsPartialMarkerTail(t *testing.T) {
	t.Parallel()
	chat := &mockChatModel{streamChunks: []string{"增长趋势。", "[[SUMMARY_EN"}}
	f := assistant.NewLLMFormatter(chat)
	var deltas []string
	result, err := f.FormatStream(context.Background(), assistant.FormatRequest{Tool: "t", Answer: map[string]any{}}, func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("FormatStream: %v", err)
	}
	if joined := strings.Join(deltas, ""); joined != "增长趋势。" {
		t.Fatalf("deltas = %q, want %q (partial marker must not leak)", joined, "增长趋势。")
	}
	if result.Summary != "增长趋势。[[SUMMARY_EN" {
		t.Fatalf("Summary = %q, want full prose (final response is authoritative)", result.Summary)
	}
}

// TestLLMFormatterStreamTrimsPartialEndMarker 验证分隔符模式下 SUMMARY 尾部出现
// 半截 [[SUMMARY_END]] 前缀时也不泄漏给前端（修复：此前只在纯文本模式截半截标记，
// 分隔符模式的 rest 直接转发，chunk 恰好停在 "[[SUM" 时把半截标记发给前端）。
func TestLLMFormatterStreamTrimsPartialEndMarker(t *testing.T) {
	t.Parallel()
	chat := &mockChatModel{streamChunks: []string{
		"[[SUMMARY_START]]Kafka 集群健康，3 节点在线[[SUM", // SUMMARY 与 END 标记半截同块
		"MARY_END]]\n[[BLOCKS_START]][{\"type\":\"incident_card\",\"title\":\"诊断\"}][[BLOCKS_END]]",
	}}
	f := assistant.NewLLMFormatter(chat)
	var deltas []string
	result, err := f.FormatStream(context.Background(), assistant.FormatRequest{Tool: "t", Answer: map[string]any{}}, func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("FormatStream: %v", err)
	}
	if joined := strings.Join(deltas, ""); joined != "Kafka 集群健康，3 节点在线" {
		t.Fatalf("streamed deltas = %q, want summary text only (no partial end marker)", joined)
	}
	if result.Summary != "Kafka 集群健康，3 节点在线" {
		t.Fatalf("Summary = %q", result.Summary)
	}
	if len(result.Blocks) != 1 || result.Blocks[0].Type != assistant.BlockIncidentCard {
		t.Fatalf("Blocks = %+v", result.Blocks)
	}
}

// TestLLMFormatterStreamRetriesOnEmpty 验证空流时重试一次（provider 偶发返回空流），
// 第二次拿到真实输出后正常返回并转发 delta。
func TestLLMFormatterStreamRetriesOnEmpty(t *testing.T) {
	t.Parallel()
	chat := &mockChatModel{
		emptyFirstStreams: 1,
		content:           "[[SUMMARY_START]]重试成功[[SUMMARY_END]]\n[[BLOCKS_START]][{\"type\":\"incident_card\",\"title\":\"诊断\"}][[BLOCKS_END]]",
	}
	f := assistant.NewLLMFormatter(chat)
	var deltas []string
	result, err := f.FormatStream(context.Background(), assistant.FormatRequest{Tool: "t", Answer: map[string]any{}}, func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("FormatStream: %v", err)
	}
	if result.Summary != "重试成功" {
		t.Fatalf("Summary = %q, want retry to recover", result.Summary)
	}
	if joined := strings.Join(deltas, ""); joined != "重试成功" {
		t.Fatalf("deltas = %q, want summary streamed after retry", joined)
	}
	if len(result.Blocks) != 1 || result.Blocks[0].Type != assistant.BlockIncidentCard {
		t.Fatalf("Blocks = %+v", result.Blocks)
	}
}

// TestLLMFormatterStreamEmptyContentReturnsError 验证空响应仍报错（不当作空 summary
// 通过；重试后仍为空时错误返回，由 ChainedFormatter 回退到 code 兜底）。
func TestLLMFormatterStreamEmptyContentReturnsError(t *testing.T) {
	t.Parallel()
	chat := &mockChatModel{streamChunks: []string{""}}
	f := assistant.NewLLMFormatter(chat)
	_, err := f.FormatStream(context.Background(), assistant.FormatRequest{Tool: "t", Answer: map[string]any{}}, nil)
	if err == nil {
		t.Fatal("expected error for empty stream content")
	}
}

// TestChainedFormatterStreamLLMSuccessNoFallback 验证 ChainedFormatter.FormatStream
// 成功时透传 delta 且不走兜底。
func TestChainedFormatterStreamLLMSuccessNoFallback(t *testing.T) {
	t.Parallel()
	llm := assistant.NewLLMFormatter(&mockChatModel{content: "[[SUMMARY_START]]LLM 摘要[[SUMMARY_END]]\n[[BLOCKS_START]][{\"type\":\"incident_card\"}][[BLOCKS_END]]"})
	chained := assistant.NewChainedFormatter(llm, assistant.NewCodeFallbackFormatter())
	var deltas []string
	result, err := chained.FormatStream(context.Background(), assistant.FormatRequest{Tool: "t", Answer: map[string]any{}}, func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("FormatStream: %v", err)
	}
	if result.Summary != "LLM 摘要" {
		t.Fatalf("Summary = %q, want LLM 摘要", result.Summary)
	}
	if joined := strings.Join(deltas, ""); joined != "LLM 摘要" {
		t.Fatalf("deltas = %q, want summary streamed", joined)
	}
}

// TestChainedFormatterStreamLLMFailsBackToCode 验证 LLM 流式失败时 ChainedFormatter
// 回退到 CodeFallbackFormatter（无 delta，但最终结果完整）。
func TestChainedFormatterStreamLLMFailsBackToCode(t *testing.T) {
	t.Parallel()
	llm := assistant.NewLLMFormatter(&mockChatModel{genErr: errors.New("down")})
	chained := assistant.NewChainedFormatter(llm, assistant.NewCodeFallbackFormatter())
	req := assistant.FormatRequest{Tool: "cluster.status.read", Answer: map[string]any{"status": "healthy"}}
	result, err := chained.FormatStream(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("FormatStream: %v", err)
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
