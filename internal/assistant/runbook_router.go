package assistant

import (
	"context"
	"strings"
)

// RunbookSummary 是 Runbook 路由所需的精简视图（借鉴-5）。
type RunbookSummary struct {
	Slug          string
	IntentPattern []string
	ToolSequence  []string
	RiskLevel     string
	IsEnabled     bool
}

// RunbookLookup 提供已启用的 Runbook 列表。store.RunbookStore 经 httpapi
// adapter 适配到本接口，使 assistant 包不直接依赖 store 包。
type RunbookLookup interface {
	ListEnabledRunbooks(ctx context.Context) ([]RunbookSummary, error)
}

// RunbookRouter 把用户消息 + 目标工具名匹配到已启用的 Runbook 模板。
// 匹配规则：IntentPattern 最长关键词命中（复用 longestKeywordHit），且
// ToolSequence 包含目标工具名（工具门，避免无关消息触发）。未命中返回
// (zero, false)，向后兼容。
type RunbookRouter struct {
	lookup RunbookLookup
}

// NewRunbookRouter 创建一个 Runbook 路由。lookup 为 nil 时 Match 返回 false。
func NewRunbookRouter(lookup RunbookLookup) *RunbookRouter {
	return &RunbookRouter{lookup: lookup}
}

// Match 在已启用 Runbook 中匹配用户消息 + 工具名。返回最佳命中（最长关键词
// 优先；平手按列表顺序）。工具门：Runbook.ToolSequence 必须包含 toolName。
func (r *RunbookRouter) Match(ctx context.Context, message, toolName string) (RunbookSummary, bool) {
	if r == nil || r.lookup == nil {
		return RunbookSummary{}, false
	}
	runbooks, err := r.lookup.ListEnabledRunbooks(ctx)
	if err != nil || len(runbooks) == 0 {
		return RunbookSummary{}, false
	}
	text := strings.ToLower(strings.TrimSpace(message))
	best := RunbookSummary{}
	bestHit := 0
	found := false
	for _, rb := range runbooks {
		if !rb.IsEnabled || len(rb.ToolSequence) == 0 {
			continue
		}
		if !containsString(rb.ToolSequence, toolName) {
			continue
		}
		hit := longestKeywordHit(text, rb.IntentPattern)
		if hit == 0 {
			continue
		}
		if hit > bestHit {
			bestHit = hit
			best = rb
			found = true
		}
	}
	return best, found
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

// SequenceForMessage resolves the declared tool_sequence of the best-matching
// enabled runbook for a message, ignoring the per-tool gate that Match applies.
// It is the agent loop's source of a product-declared evidence-collection order
// (② chain skeleton): for e.g. "告警根因" it returns alert-root-cause-sequence's
// [alert.query, event.query]. Returns nil when no runbook with a sequence
// matches.
func (r *RunbookRouter) SequenceForMessage(ctx context.Context, message string) []string {
	if r == nil || r.lookup == nil {
		return nil
	}
	runbooks, err := r.lookup.ListEnabledRunbooks(ctx)
	if err != nil || len(runbooks) == 0 {
		return nil
	}
	text := strings.ToLower(strings.TrimSpace(message))
	best := RunbookSummary{}
	bestHit := 0
	found := false
	for _, rb := range runbooks {
		if !rb.IsEnabled || len(rb.ToolSequence) == 0 {
			continue
		}
		hit := longestKeywordHit(text, rb.IntentPattern)
		if hit == 0 {
			continue
		}
		if hit > bestHit {
			bestHit = hit
			best = rb
			found = true
		}
	}
	if !found {
		return nil
	}
	return append([]string{}, best.ToolSequence...)
}
