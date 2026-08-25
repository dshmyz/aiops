package diagnostics

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type stubChatModel struct {
	content string
	err     error
}

func (s *stubChatModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if s.err != nil {
		return nil, s.err
	}
	return schema.AssistantMessage(s.content, nil), nil
}

func (s *stubChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("stream not supported in tests")
}

// 测试使用 testmain_test.go 里 TestMain 注册的 kafka 域工具
// （kafka.consumer_group.lag.read / topic.retention.set），不改动全局注册表。

// LLM 生成成功：summary/rationale 来自模型输出；候选工具与输入仍由确定性模板
// 派生（LLM 只负责说清楚建议，不负责挑工具），保证建议可执行。
func TestLLMRecommendationGeneratorParsesModelOutput(t *testing.T) {
	chat := &stubChatModel{content: `{"summary":"尽快扩容","rationale":"磁盘水位超过 85%"}`}
	gen := NewLLMRecommendationGenerator(chat)

	result, err := gen.Generate(context.Background(), "kafka", "vol-1", "prod", SeverityWarning,
		map[string]any{"capacity_pct": 88.5, "retention_hours": 24})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Summary != "尽快扩容" || result.Rationale != "磁盘水位超过 85%" {
		t.Fatalf("summary/rationale = %q / %q, want LLM output", result.Summary, result.Rationale)
	}
	if result.ToolName != "topic.retention.set" {
		t.Fatalf("ToolName = %q, want topic.retention.set from template action", result.ToolName)
	}
	if result.CandidateInput["retention_hours"] != 24 {
		t.Fatalf("CandidateInput = %#v, want retention_hours from observation data", result.CandidateInput)
	}
}

// 未配置模型即构造（chat 为 nil）时报明确错误，不静默退化为模板。
func TestLLMRecommendationGeneratorRequiresModel(t *testing.T) {
	gen := NewLLMRecommendationGenerator(nil)
	if _, err := gen.Generate(context.Background(), "demo", "vol-1", "prod", SeverityWarning, nil); err == nil || !strings.Contains(err.Error(), "LLM") {
		t.Fatalf("want explicit no-model error, got %v", err)
	}
}

// 模型返回空 summary / 非 JSON 输出：报错而不是产出空建议（由 Hybrid 兜底）。
func TestLLMRecommendationGeneratorRejectsBadOutput(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"empty summary", `{"summary":"  ","rationale":"x"}`},
		{"not json", `抱歉，我无法生成建议。`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gen := NewLLMRecommendationGenerator(&stubChatModel{content: tc.content})
			if _, err := gen.Generate(context.Background(), "demo", "vol-1", "prod", SeverityWarning, nil); err == nil {
				t.Fatalf("want error for %q", tc.content)
			}
		})
	}
}

// Hybrid：LLM 可用时优先用 LLM 的 summary。
func TestHybridRecommendationGeneratorPrefersLLM(t *testing.T) {
	gen := NewHybridRecommendationGenerator(&stubChatModel{content: `{"summary":"LLM 建议","rationale":"r"}`})

	result, err := gen.Generate(context.Background(), "kafka", "vol-1", "prod", SeverityWarning, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Summary != "LLM 建议" {
		t.Fatalf("Summary = %q, want LLM summary", result.Summary)
	}
}

// Hybrid：LLM 失败（报错/产出无效）时回退模板生成，建议链路不因模型抖动而断。
func TestHybridRecommendationGeneratorFallsBackToTemplate(t *testing.T) {
	cases := []struct {
		name string
		chat *stubChatModel
	}{
		{"model error", &stubChatModel{err: errors.New("rate limited")}},
		{"invalid output", &stubChatModel{content: "not json"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gen := NewHybridRecommendationGenerator(tc.chat)
			result, err := gen.Generate(context.Background(), "kafka", "vol-1", "prod", SeverityWarning, nil)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if result.Summary == "" {
				t.Fatal("fallback template returned empty summary")
			}
			if result.ToolName != "topic.retention.set" {
				t.Fatalf("ToolName = %q, want template action tool", result.ToolName)
			}
		})
	}
}
