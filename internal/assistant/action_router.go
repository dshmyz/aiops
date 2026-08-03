package assistant

import (
	"context"
	"fmt"
	"strings"
)

// SkillSummary 是 ActionRouter 需要的 Skill 精简视图。
// 通过接口而非直接依赖 store.Skill，保持 assistant 包的分层独立性。
type SkillSummary struct {
	Slug      string
	Content   string
	IsBuiltin bool
}

// SkillLookup 是 ActionRouter 查询 Skills 的依赖接口。
// store.SkillStore 实现此接口（ListSkillsByAction 返回 store.Skill，
// 通过 adapter 转换为 SkillSummary）。这样 assistant 包不直接依赖 store 包。
type SkillLookup interface {
	ListSkillsByAction(ctx context.Context, actionCode string) ([]SkillSummary, error)
}

// PromptAugment 是 Action Router 产出的 prompt 增量，包含 Action 上下文
// 引导和关联 Skill 的 SOP 内容，供 planner 注入到 LLM 提示词。
type PromptAugment struct {
	ActionCode        string
	ActionDisplayName string
	PromptHint        string
	AgentMode         AgentMode
	RiskLevel         ActionRisk
	SkillContents     []string
	SkillSlugs        []string
}

// BuildHint 把 Action 的 PromptHint 和所有 Skill 的 Content 组装成
// 可注入 planner 提示词的文本块。无任何内容时返回空字符串。
func (a PromptAugment) BuildHint() string {
	var parts []string
	if strings.TrimSpace(a.PromptHint) != "" {
		parts = append(parts, a.PromptHint)
	}
	for _, content := range a.SkillContents {
		if strings.TrimSpace(content) != "" {
			parts = append(parts, content)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

// ActionRouter 把用户消息路由到一个 Action，并加载该 Action 关联的
// Skills，组装成 PromptAugment 供 planner 使用。
//
// 设计参考 SxDevOps 的 select_action_by_handler：先按关键词匹配 Action，
// 再加载该 Action 的 Skills。无匹配时返回空 PromptAugment，调用方回退
// 到现有 planner（向后兼容）。
type ActionRouter struct {
	skills SkillLookup
}

// NewActionRouter 创建一个 ActionRouter。skills 可以为 nil（此时只做
// Action 匹配，不加载 Skill 内容）。
func NewActionRouter(skills SkillLookup) *ActionRouter {
	return &ActionRouter{skills: skills}
}

// Route 把消息路由到 Action 并组装 PromptAugment。
// 无匹配 Action 时返回零值 PromptAugment 和 nil error（向后兼容）。
//
// 缺口-3 增强：消息未命中关键词时，用 pageContext.Domain 兜底路由。
// 复用 LookupAction 的关键词匹配——minio/glusterfs/kafka 等中间件 domain
// 会命中 middleware.diagnose；业务 domain 不命中，保持 ok=false（向后兼容）。
// message 始终优先，符合 PageContext 文档"Message tokens always take precedence"。
func (r *ActionRouter) Route(ctx context.Context, message string, pageContext PageContext) (PromptAugment, error) {
	action, ok := LookupAction(message)
	if !ok && pageContext.Domain != "" {
		action, ok = LookupAction(pageContext.Domain)
	}
	if !ok {
		return PromptAugment{}, nil
	}

	augment := PromptAugment{
		ActionCode:        action.Code,
		ActionDisplayName: action.DisplayName,
		PromptHint:        action.PromptHint,
		AgentMode:         action.AgentMode,
		RiskLevel:         action.RiskLevel,
	}

	if r.skills == nil {
		return augment, nil
	}

	skills, err := r.skills.ListSkillsByAction(ctx, action.Code)
	if err != nil {
		return PromptAugment{}, fmt.Errorf("load skills for action %s: %w", action.Code, err)
	}

	for _, sk := range skills {
		augment.SkillContents = append(augment.SkillContents, sk.Content)
		augment.SkillSlugs = append(augment.SkillSlugs, sk.Slug)
	}
	return augment, nil
}
