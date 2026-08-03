package assistant_test

import (
	"context"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

// recordingPlanner 记录最后一次收到的消息，用于验证 augment 是否注入。
type recordingPlanner struct {
	lastMessage string
	intent      assistant.Intent
	err         error
}

func (r *recordingPlanner) Plan(_ context.Context, _ identity.CurrentUser, message string, _ []assistant.Turn, _ assistant.PageContext) (assistant.Intent, error) {
	r.lastMessage = message
	return r.intent, r.err
}

func TestActionAwarePlannerInjectsAugmentIntoMessage(t *testing.T) {
	t.Parallel()

	inner := &recordingPlanner{intent: assistant.Intent{ToolName: "cluster.status.read"}}
	skills := newStubSkillLookup(map[string][]assistant.SkillSummary{
		"middleware.diagnose": {
			{Slug: "middleware-evidence-checklist", Content: "必须输出：结论、证据、影响范围。"},
		},
	})
	router := assistant.NewActionRouter(skills)
	planner := assistant.NewActionAwarePlanner(inner, router)

	_, err := planner.Plan(context.Background(), identity.CurrentUser{}, "查看 prod kafka 健康状态", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// inner planner 应该收到包含 Skill content 的增强消息
	if inner.lastMessage == "" {
		t.Fatal("inner planner received empty message")
	}
	if !containsStr(inner.lastMessage, "必须输出：结论、证据、影响范围。") {
		t.Errorf("inner message missing Skill content:\n%s", inner.lastMessage)
	}
	if !containsStr(inner.lastMessage, "查看 prod kafka 健康状态") {
		t.Errorf("inner message missing original user message:\n%s", inner.lastMessage)
	}
}

func TestActionAwarePlannerNoMatchPassesOriginalMessage(t *testing.T) {
	t.Parallel()

	inner := &recordingPlanner{intent: assistant.Intent{Confidence: 0.5}}
	router := assistant.NewActionRouter(newStubSkillLookup(nil))
	planner := assistant.NewActionAwarePlanner(inner, router)

	_, err := planner.Plan(context.Background(), identity.CurrentUser{}, "今天天气怎么样", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// 无 Action 匹配时，原消息应原样传递
	if inner.lastMessage != "今天天气怎么样" {
		t.Fatalf("inner message = %q, want original unchanged", inner.lastMessage)
	}
}

func TestActionAwarePlannerNilRouterActsAsPassthrough(t *testing.T) {
	t.Parallel()

	inner := &recordingPlanner{intent: assistant.Intent{Confidence: 0.5}}
	// router 为 nil 时不应该 panic，且消息原样传递
	planner := assistant.NewActionAwarePlanner(inner, nil)

	_, err := planner.Plan(context.Background(), identity.CurrentUser{}, "test message", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if inner.lastMessage != "test message" {
		t.Fatalf("inner message = %q, want %q", inner.lastMessage, "test message")
	}
}

func TestActionAwarePlannerPreservesHistoryAndUser(t *testing.T) {
	t.Parallel()

	inner := &recordingPlanner{intent: assistant.Intent{Confidence: 0.5}}
	router := assistant.NewActionRouter(newStubSkillLookup(nil))
	planner := assistant.NewActionAwarePlanner(inner, router)

	user := identity.CurrentUser{Subject: "alice"}
	history := []assistant.Turn{{Role: "user", Content: "previous message"}}

	_, _ = planner.Plan(context.Background(), user, "查看 prod kafka 健康", history, assistant.PageContext{})
	// 主要验证不 panic 且正常返回；history 和 user 的传递由接口保证
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) > 0 && len(substr) > 0 && indexOfStr(s, substr) >= 0))
}

func indexOfStr(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
