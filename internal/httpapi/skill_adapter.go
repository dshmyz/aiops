package httpapi

import (
	"context"
	"fmt"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// skillStoreAdapter 把 store.SkillStore 适配成 assistant.SkillLookup。
// 放在 httpapi 层做组装，避免 assistant 包直接依赖 store 包。
type skillStoreAdapter struct {
	store store.SkillStore
}

// NewSkillLookupAdapter 用 store.SkillStore 创建一个 assistant.SkillLookup。
// 传入 nil 时返回 nil（调用方视作无 Skill 查询）。
func NewSkillLookupAdapter(s store.SkillStore) assistant.SkillLookup {
	if s == nil {
		return nil
	}
	return &skillStoreAdapter{store: s}
}

func (a *skillStoreAdapter) ListSkillsByAction(ctx context.Context, actionCode string) ([]assistant.SkillSummary, error) {
	skills, err := a.store.ListSkillsByAction(ctx, actionCode)
	if err != nil {
		return nil, fmt.Errorf("list skills by action: %w", err)
	}
	out := make([]assistant.SkillSummary, 0, len(skills))
	for _, sk := range skills {
		out = append(out, assistant.SkillSummary{
			Slug:      sk.Slug,
			Content:   sk.Content,
			IsBuiltin: sk.IsBuiltin,
		})
	}
	return out, nil
}
