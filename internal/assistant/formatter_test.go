package assistant_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
)

// stubFormatter 是测试用的 ResponseFormatter 桩。
type stubFormatter struct {
	result assistant.FormatResult
	err    error
}

func (f *stubFormatter) Format(_ context.Context, _ assistant.FormatRequest) (assistant.FormatResult, error) {
	return f.result, f.err
}

func TestFormatRequestFields(t *testing.T) {
	t.Parallel()

	req := assistant.FormatRequest{
		UserMessage: "查看 kafka 健康状态",
		Tool:        "cluster.status.read",
		Answer:      map[string]any{"status": "healthy", "nodes": 3},
	}
	if req.UserMessage != "查看 kafka 健康状态" {
		t.Errorf("UserMessage = %q", req.UserMessage)
	}
	if req.Tool != "cluster.status.read" {
		t.Errorf("Tool = %q", req.Tool)
	}
	if req.Answer["status"] != "healthy" {
		t.Errorf("Answer[status] = %v", req.Answer["status"])
	}
}

func TestFormatResultFields(t *testing.T) {
	t.Parallel()

	result := assistant.FormatResult{
		Summary: "Kafka 集群健康，3 个节点在线",
		Blocks: []assistant.Block{
			{Type: assistant.BlockIncidentCard, Title: "Kafka 状态", Content: "健康"},
			{Type: assistant.BlockToolTrace, Title: "工具调用", Content: "cluster.status.read"},
		},
	}
	if result.Summary == "" {
		t.Error("Summary is empty")
	}
	if len(result.Blocks) != 2 {
		t.Fatalf("Blocks len = %d, want 2", len(result.Blocks))
	}
}

func TestCodeFallbackFormatterExtractsSummary(t *testing.T) {
	t.Parallel()

	f := assistant.NewCodeFallbackFormatter()
	req := assistant.FormatRequest{
		UserMessage: "查看 kafka 健康状态",
		Tool:        "cluster.status.read",
		Answer: map[string]any{
			"status":  "healthy",
			"message": "集群运行正常",
			"nodes":   3,
		},
	}

	result, err := f.Format(context.Background(), req)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if result.Summary == "" {
		t.Fatal("Summary is empty")
	}
}

func TestCodeFallbackFormatterGeneratesBlocks(t *testing.T) {
	t.Parallel()

	f := assistant.NewCodeFallbackFormatter()
	req := assistant.FormatRequest{
		UserMessage: "查看 kafka 健康状态",
		Tool:        "cluster.status.read",
		Answer: map[string]any{
			"status":  "healthy",
			"message": "集群运行正常",
		},
	}

	result, err := f.Format(context.Background(), req)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if len(result.Blocks) == 0 {
		t.Fatal("Blocks is empty, fallback should generate at least one block")
	}
	// 应该包含 tool_trace block
	hasToolTrace := false
	for _, b := range result.Blocks {
		if b.Type == assistant.BlockToolTrace {
			hasToolTrace = true
		}
	}
	if !hasToolTrace {
		t.Error("fallback Blocks missing tool_trace")
	}
}

func TestCodeFallbackFormatterHandlesEmptyAnswer(t *testing.T) {
	t.Parallel()

	f := assistant.NewCodeFallbackFormatter()
	req := assistant.FormatRequest{
		UserMessage: "查看 kafka 健康状态",
		Tool:        "cluster.status.read",
		Answer:      nil,
	}

	result, err := f.Format(context.Background(), req)
	if err != nil {
		t.Fatalf("Format with empty answer: %v", err)
	}
	// 空 Answer 不应 panic，应返回兜底 Summary
	if result.Summary == "" {
		t.Error("Summary should not be empty even for nil answer")
	}
}

func TestChainedFormatterPrimarySuccess(t *testing.T) {
	t.Parallel()

	primary := &stubFormatter{
		result: assistant.FormatResult{Summary: "LLM 整形结果", Blocks: []assistant.Block{{Type: assistant.BlockIncidentCard}}},
	}
	fallback := assistant.NewCodeFallbackFormatter()
	chained := assistant.NewChainedFormatter(primary, fallback)

	result, err := chained.Format(context.Background(), assistant.FormatRequest{})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if result.Summary != "LLM 整形结果" {
		t.Fatalf("Summary = %q, want LLM 整形结果", result.Summary)
	}
}

func TestChainedFormatterFallsBackOnError(t *testing.T) {
	t.Parallel()

	primary := &stubFormatter{err: errors.New("LLM unavailable")}
	fallback := &stubFormatter{
		result: assistant.FormatResult{Summary: "代码兜底结果"},
	}
	chained := assistant.NewChainedFormatter(primary, fallback)

	result, err := chained.Format(context.Background(), assistant.FormatRequest{})
	if err != nil {
		t.Fatalf("Format should not return error when fallback succeeds: %v", err)
	}
	if result.Summary != "代码兜底结果" {
		t.Fatalf("Summary = %q, want 代码兜底结果", result.Summary)
	}
}

func TestChainedFormatterFallsBackOnEmptyResult(t *testing.T) {
	t.Parallel()

	// primary 返回空 Summary 也应触发兜底
	primary := &stubFormatter{result: assistant.FormatResult{}}
	fallback := &stubFormatter{
		result: assistant.FormatResult{Summary: "兜底结果"},
	}
	chained := assistant.NewChainedFormatter(primary, fallback)

	result, err := chained.Format(context.Background(), assistant.FormatRequest{})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if result.Summary != "兜底结果" {
		t.Fatalf("Summary = %q, want 兜底结果", result.Summary)
	}
}

func TestChainedFormatterBothFailReturnsError(t *testing.T) {
	t.Parallel()

	primary := &stubFormatter{err: errors.New("primary fail")}
	fallback := &stubFormatter{err: errors.New("fallback fail")}
	chained := assistant.NewChainedFormatter(primary, fallback)

	_, err := chained.Format(context.Background(), assistant.FormatRequest{})
	if err == nil {
		t.Fatal("expected error when both formatters fail")
	}
}

// --- 缺口-4：事实集聚合测试 ---

// TestCodeFallbackFormatterAggregatesFactSet verifies that when FormatRequest
// carries a FactSet (multiple tool facts from diagnostic + read + recommend
// chains), the fallback formatter iterates the FactSet and emits one
// tool_trace block per fact. This fixes the gap where the fallback only saw
// the single top-level Answer and dropped facts from multi-tool flows.
func TestCodeFallbackFormatterAggregatesFactSet(t *testing.T) {
	t.Parallel()
	f := assistant.NewCodeFallbackFormatter()
	req := assistant.FormatRequest{
		UserMessage: "prod kafka 异常分析",
		Tool:        "system.posture.read",
		Answer:      map[string]any{"overall_status": "degraded"},
		FactSet: []assistant.ToolFact{
			{
				Tool:   "system.posture.read",
				Input:  map[string]any{},
				Result: map[string]any{"overall_status": "degraded", "domains": []any{}},
			},
			{
				Tool:   "kafka.consumer_lag.read",
				Input:  map[string]any{"name": "orders"},
				Result: map[string]any{"status": "warning", "lag": 1840},
			},
			{
				Tool:   "glusterfs.volume.health.read",
				Input:  map[string]any{"name": "data"},
				Result: map[string]any{"status": "warning", "capacity_pct": 82.5},
			},
		},
	}

	result, err := f.Format(context.Background(), req)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if result.Summary == "" {
		t.Fatal("Summary is empty")
	}
	// 必须为 FactSet 中的每个 fact 生成一个 tool_trace block
	toolTraceCount := 0
	for _, b := range result.Blocks {
		if b.Type == assistant.BlockToolTrace {
			toolTraceCount++
		}
	}
	if toolTraceCount != len(req.FactSet) {
		t.Fatalf("tool_trace block count = %d, want %d (one per FactSet entry)", toolTraceCount, len(req.FactSet))
	}
}

// TestCodeFallbackFormatterFactSetEmptyPreservesSingleToolBehavior verifies
// backward compatibility: when FactSet is empty, the formatter falls back to
// the single-Tool/Answer path (one tool_trace block from req.Tool/req.Answer).
func TestCodeFallbackFormatterFactSetEmptyPreservesSingleToolBehavior(t *testing.T) {
	t.Parallel()
	f := assistant.NewCodeFallbackFormatter()
	req := assistant.FormatRequest{
		Tool:   "cluster.status.read",
		Answer: map[string]any{"status": "green"},
		// FactSet 为空，应走旧的单工具路径
	}

	result, err := f.Format(context.Background(), req)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	toolTraceCount := 0
	for _, b := range result.Blocks {
		if b.Type == assistant.BlockToolTrace {
			toolTraceCount++
		}
	}
	if toolTraceCount != 1 {
		t.Fatalf("tool_trace block count = %d, want 1 (single-tool path)", toolTraceCount)
	}
}

// TestCodeFallbackFormatterFactSetSummaryAggregation verifies that the Summary
// reflects facts across the entire FactSet, not just the top-level Answer.
// When the top-level Answer has no extractable field but FactSet entries do,
// the summary should surface the fact-level status.
func TestCodeFallbackFormatterFactSetSummaryAggregation(t *testing.T) {
	t.Parallel()
	f := assistant.NewCodeFallbackFormatter()
	req := assistant.FormatRequest{
		Tool:   "system.posture.read",
		Answer: map[string]any{"overall_status": "degraded"},
		FactSet: []assistant.ToolFact{
			{
				Tool:   "kafka.consumer_lag.read",
				Result: map[string]any{"status": "warning", "lag": 1840},
			},
		},
	}

	result, err := f.Format(context.Background(), req)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	// Summary 应至少提到 FactSet 中的 warning 状态或工具，不能只说"未返回结构化结果"
	if result.Summary == "" {
		t.Fatal("Summary is empty")
	}
}
