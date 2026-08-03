package httpapi

import (
	"context"
	"fmt"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// runbookStoreAdapter 把 store.RunbookStore 适配成 assistant.RunbookLookup。
// 放在 httpapi 层做组装，避免 assistant 包直接依赖 store 包。
type runbookStoreAdapter struct {
	store store.RunbookStore
}

// NewRunbookLookupAdapter 用 store.RunbookStore 创建一个 assistant.RunbookLookup。
// 传入 nil 时返回 nil（RunbookRouter 会当作无 Runbook 匹配处理）。
func NewRunbookLookupAdapter(s store.RunbookStore) assistant.RunbookLookup {
	if s == nil {
		return nil
	}
	return &runbookStoreAdapter{store: s}
}

func (a *runbookStoreAdapter) ListEnabledRunbooks(ctx context.Context) ([]assistant.RunbookSummary, error) {
	runbooks, err := a.store.ListEnabledRunbooks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list enabled runbooks: %w", err)
	}
	out := make([]assistant.RunbookSummary, 0, len(runbooks))
	for _, rb := range runbooks {
		out = append(out, assistant.RunbookSummary{
			Slug:          rb.Slug,
			IntentPattern: rb.IntentPattern,
			ToolSequence:  rb.ToolSequence,
			RiskLevel:     rb.RiskLevel,
			IsEnabled:     rb.IsEnabled,
		})
	}
	return out, nil
}
