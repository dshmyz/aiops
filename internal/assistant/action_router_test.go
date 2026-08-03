package assistant_test

import (
	"context"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
)

// stubSkillLookup 是测试用的 SkillLookup 桩，按 action 预设 SkillSummary 列表。
type stubSkillLookup struct {
	byAction map[string][]assistant.SkillSummary
	fail     bool
}

func newStubSkillLookup(skillsByAction map[string][]assistant.SkillSummary) *stubSkillLookup {
	return &stubSkillLookup{byAction: skillsByAction}
}

func (s *stubSkillLookup) ListSkillsByAction(_ context.Context, actionCode string) ([]assistant.SkillSummary, error) {
	if s.fail {
		return nil, errLookupFail
	}
	return s.byAction[actionCode], nil
}

var errLookupFail = errLookupFailed{}

type errLookupFailed struct{}

func (errLookupFailed) Error() string { return "skill lookup failed" }

func TestActionRouterAugmentsMiddlewareDiagnose(t *testing.T) {
	t.Parallel()

	skills := newStubSkillLookup(map[string][]assistant.SkillSummary{
		"middleware.diagnose": {
			{Slug: "middleware-evidence-checklist", Content: "必须输出：结论、证据、影响范围、下一步动作。"},
		},
	})
	router := assistant.NewActionRouter(skills)

	augment, err := router.Route(context.Background(), "查看 prod kafka 健康状态", assistant.PageContext{})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if augment.ActionCode != "middleware.diagnose" {
		t.Fatalf("ActionCode = %q, want middleware.diagnose", augment.ActionCode)
	}
	if augment.PromptHint == "" {
		t.Fatal("PromptHint is empty")
	}
	if len(augment.SkillContents) != 1 {
		t.Fatalf("SkillContents = %d, want 1", len(augment.SkillContents))
	}
	if augment.SkillContents[0] != "必须输出：结论、证据、影响范围、下一步动作。" {
		t.Fatalf("SkillContents[0] = %q", augment.SkillContents[0])
	}
}

func TestActionRouterNoMatchReturnsEmpty(t *testing.T) {
	t.Parallel()

	router := assistant.NewActionRouter(newStubSkillLookup(nil))

	augment, err := router.Route(context.Background(), "今天天气怎么样", assistant.PageContext{})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if augment.ActionCode != "" {
		t.Fatalf("ActionCode = %q, want empty for no match", augment.ActionCode)
	}
	if len(augment.SkillContents) != 0 {
		t.Fatalf("SkillContents = %d, want 0 for no match", len(augment.SkillContents))
	}
}

func TestActionRouterLoadsMultipleSkills(t *testing.T) {
	t.Parallel()

	skills := newStubSkillLookup(map[string][]assistant.SkillSummary{
		"alert.root_cause": {
			{Slug: "alert-evidence-checklist", Content: "告警证据清单 SOP"},
			{Slug: "answer-formatter", Content: "回答整形规范"},
		},
	})
	router := assistant.NewActionRouter(skills)

	augment, err := router.Route(context.Background(), "这条告警为什么触发，帮我做根因分析", assistant.PageContext{})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if augment.ActionCode != "alert.root_cause" {
		t.Fatalf("ActionCode = %q, want alert.root_cause", augment.ActionCode)
	}
	if len(augment.SkillContents) != 2 {
		t.Fatalf("SkillContents = %d, want 2", len(augment.SkillContents))
	}
}

func TestActionRouterSkillStoreError(t *testing.T) {
	t.Parallel()

	router := assistant.NewActionRouter(&stubSkillLookup{fail: true})

	_, err := router.Route(context.Background(), "查看 prod kafka 健康状态", assistant.PageContext{})
	if err == nil {
		t.Fatal("Route with failing store: expected error, got nil")
	}
}

// TestActionRouterPageContextDomainFallback 验证消息未命中关键词时，
// 用 pageContext.Domain 兜底路由：在 minio 页面发"健康状态如何"应命中
// middleware.diagnose（复用 LookupAction 对 domain="minio" 的关键词匹配）。
func TestActionRouterPageContextDomainFallback(t *testing.T) {
	t.Parallel()

	skills := newStubSkillLookup(map[string][]assistant.SkillSummary{
		"middleware.diagnose": {
			{Slug: "middleware-evidence-checklist", Content: "中间件诊断 SOP"},
		},
	})
	router := assistant.NewActionRouter(skills)

	// 消息"健康状态如何"不含 minio/kafka/glusterfs 等关键词，
	// 但 pageContext.Domain=minio 应兜底命中 middleware.diagnose。
	augment, err := router.Route(context.Background(), "健康状态如何", assistant.PageContext{Domain: "minio"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if augment.ActionCode != "middleware.diagnose" {
		t.Fatalf("ActionCode = %q, want middleware.diagnose (pageContext domain fallback)", augment.ActionCode)
	}
	if len(augment.SkillContents) != 1 {
		t.Fatalf("SkillContents = %d, want 1", len(augment.SkillContents))
	}
}

// TestActionRouterMessagePrecedenceOverPageContext 验证消息命中的 Action
// 优先于 pageContext.Domain：消息命中 alert.root_cause 时，即使
// pageContext.Domain=minio 也应走 alert.root_cause（message tokens always
// take precedence over PageContext）。
func TestActionRouterMessagePrecedenceOverPageContext(t *testing.T) {
	t.Parallel()

	skills := newStubSkillLookup(map[string][]assistant.SkillSummary{
		"alert.root_cause": {
			{Slug: "alert-evidence-checklist", Content: "告警 SOP"},
		},
		"middleware.diagnose": {
			{Slug: "middleware-evidence-checklist", Content: "中间件 SOP"},
		},
	})
	router := assistant.NewActionRouter(skills)

	// 消息含"告警"命中 alert.root_cause，pageContext.Domain=minio 本会
	// 兜底到 middleware.diagnose，但 message 优先，应走 alert.root_cause。
	augment, err := router.Route(context.Background(), "这条告警怎么回事", assistant.PageContext{Domain: "minio"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if augment.ActionCode != "alert.root_cause" {
		t.Fatalf("ActionCode = %q, want alert.root_cause (message precedence)", augment.ActionCode)
	}
}

// TestActionRouterPageContextUnknownDomainNoMatch 验证 pageContext.Domain
// 为业务服务（非中间件）时不会强行匹配，仍返回空（向后兼容）。
func TestActionRouterPageContextUnknownDomainNoMatch(t *testing.T) {
	t.Parallel()

	router := assistant.NewActionRouter(newStubSkillLookup(nil))

	// "怎么样"不含任何 Action 关键词，"order" 是业务服务名也不在关键词中，不应命中。
	augment, err := router.Route(context.Background(), "怎么样", assistant.PageContext{Domain: "order"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if augment.ActionCode != "" {
		t.Fatalf("ActionCode = %q, want empty for unknown domain", augment.ActionCode)
	}
}

func TestPromptAugmentBuildHint(t *testing.T) {
	t.Parallel()

	augment := assistant.PromptAugment{
		ActionCode:    "middleware.diagnose",
		PromptHint:    "识别 domain 和 environment",
		SkillContents: []string{"SOP 内容1", "SOP 内容2"},
		SkillSlugs:    []string{"sop1", "sop2"},
	}
	hint := augment.BuildHint()
	if hint == "" {
		t.Fatal("BuildHint returned empty")
	}
	for _, want := range []string{"识别 domain 和 environment", "SOP 内容1", "SOP 内容2"} {
		if !strings.Contains(hint, want) {
			t.Errorf("BuildHint() missing %q in:\n%s", want, hint)
		}
	}
}

func TestPromptAugmentBuildHintEmpty(t *testing.T) {
	t.Parallel()

	augment := assistant.PromptAugment{}
	if hint := augment.BuildHint(); hint != "" {
		t.Fatalf("empty PromptAugment BuildHint = %q, want empty", hint)
	}
}
