package assistant

import "context"

// SkillSummary 是 AgentExecutor 需要的 Skill 精简视图。
// 通过接口而非直接依赖 store.Skill，保持 assistant 包的分层独立性。
type SkillSummary struct {
	Slug      string
	Content   string
	IsBuiltin bool
}

// SkillLookup 是 AgentExecutor 查询 Skills 的依赖接口。
// store.SkillStore 实现此接口（ListSkillsByAction 返回 store.Skill，
// 通过 adapter 转换为 SkillSummary）。这样 assistant 包不直接依赖 store 包。
type SkillLookup interface {
	ListSkillsByAction(ctx context.Context, actionCode string) ([]SkillSummary, error)
}
