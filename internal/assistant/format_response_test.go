package assistant

import (
	"context"
	"strings"
	"testing"
)

// fakeStreamFormatter 是同时实现 ResponseFormatter + StreamingResponseFormatter
// 的最小替身，记录 FormatStream 被调用次数与转发出的 delta。
type fakeStreamFormatter struct {
	streamCalls int
}

func (f *fakeStreamFormatter) Format(_ context.Context, _ FormatRequest) (FormatResult, error) {
	return FormatResult{Summary: "plain-summary"}, nil
}

func (f *fakeStreamFormatter) FormatStream(_ context.Context, _ FormatRequest, onDelta func(string)) (FormatResult, error) {
	f.streamCalls++
	onDelta("诊")
	onDelta("断")
	return FormatResult{
		Summary: "streamed-summary",
		Blocks:  []Block{{Type: BlockEvidenceTimeline, Title: "证据时间线"}},
	}, nil
}

// plainOnlyFormatter 只实现 ResponseFormatter（非流式，如 CodeFallbackFormatter）。
type plainOnlyFormatter struct{}

func (f plainOnlyFormatter) Format(_ context.Context, _ FormatRequest) (FormatResult, error) {
	return FormatResult{Summary: "plain-only-summary"}, nil
}

// TestFormatResponseStreamsWithDelta 验证 formatDelta 非 nil 且 formatter 支持流式时
// 走 FormatStream：摘要叙述逐段转发 delta，Blocks/Summary 仍并入 response。消除
// 诊断/读路径整形期的空窗等待是本轮目标，此测试钉住"流式被真正使用"。
func TestFormatResponseStreamsWithDelta(t *testing.T) {
	s := &Service{formatter: &fakeStreamFormatter{}}
	var deltas []string
	resp := s.formatResponse(context.Background(), Response{Type: "answer"}, Intent{}, func(d string) { deltas = append(deltas, d) })
	if len(deltas) != 2 || strings.Join(deltas, "") != "诊断" {
		t.Fatalf("deltas = %v, want two chunks joined %q", deltas, "诊断")
	}
	if resp.Summary != "streamed-summary" {
		t.Fatalf("Summary = %q, want streamed-summary (FormatStream result)", resp.Summary)
	}
	if len(resp.Blocks) != 1 || resp.Blocks[0].Type != BlockEvidenceTimeline {
		t.Fatalf("Blocks = %+v, want evidence_timeline block from FormatStream", resp.Blocks)
	}
}

// TestFormatResponseNilDeltaNonStream 验证 formatDelta 为 nil（一次性调用方）时仍走
// 非流式 Format，行为与旧路径一致（流式 formatter 也不触发 FormatStream）。
func TestFormatResponseNilDeltaNonStream(t *testing.T) {
	sf := &fakeStreamFormatter{}
	s := &Service{formatter: sf}
	resp := s.formatResponse(context.Background(), Response{Type: "answer"}, Intent{}, nil)
	if sf.streamCalls != 0 {
		t.Fatalf("FormatStream called %d times, want 0 when formatDelta is nil", sf.streamCalls)
	}
	if resp.Summary != "plain-summary" {
		t.Fatalf("Summary = %q, want plain-summary (non-stream Format)", resp.Summary)
	}
}

// TestFormatResponseDeltaFallsBackForPlainFormatter 验证 formatter 不支持流式时，
// 即使 formatDelta 非 nil 也回退非流式 Format（类型断言 fallback），不 panic。
func TestFormatResponseDeltaFallsBackForPlainFormatter(t *testing.T) {
	s := &Service{formatter: plainOnlyFormatter{}}
	resp := s.formatResponse(context.Background(), Response{Type: "answer"}, Intent{}, func(string) { t.Fatal("non-streaming formatter must not emit deltas") })
	if resp.Summary != "plain-only-summary" {
		t.Fatalf("Summary = %q, want plain-only-summary via Format fallback", resp.Summary)
	}
}

// TestExecuteFromIntentReadStreamsFormatDelta 钉住流式接线缝：流式调用方传的
// formatDelta 从 executeFromIntent（读工具分支）一路穿到 formatResponse，最终
// 摘要叙述以 delta 逐段转发——这是"诊断/读路径整形空窗"在 service 层的落点。
func TestExecuteFromIntentReadStreamsFormatDelta(t *testing.T) {
	s := enrichTestService(t)
	s.formatter = &fakeStreamFormatter{}
	var deltas []string
	resp, err := s.executeFromIntent(context.Background(), adminUser(),
		"查一下 demo volume 健康",
		Intent{ToolName: "demo.health.read", Input: map[string]any{"name": "data"}},
		nil,
		func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("executeFromIntent error: %v", err)
	}
	if strings.Join(deltas, "") != "诊断" {
		t.Fatalf("deltas = %q, want streaming formatter narrative delivered via formatDelta", strings.Join(deltas, ""))
	}
	if resp.Summary != "streamed-summary" {
		t.Fatalf("Summary = %q, want streaming formatter result", resp.Summary)
	}
}