package alert

import (
	"context"
	"encoding/json"
	"log"
	"sync"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// AlertActionRegistry 从 DB 加载告警→动作编排规则到内存，供 ChainDiagnoser 匹配。
// 支持热重载（Reload）。
type AlertActionRegistry struct {
	store  store.AlertActionRuleStore
	mu     sync.RWMutex
	actions []AlertAction
}

// NewAlertActionRegistry 创建告警动作规则注册表。
func NewAlertActionRegistry(s store.AlertActionRuleStore) *AlertActionRegistry {
	return &AlertActionRegistry{store: s}
}

// Load 从 DB 加载所有启用的规则到内存。
func (r *AlertActionRegistry) Load(ctx context.Context) error {
	records, err := r.store.List(ctx)
	if err != nil {
		return err
	}

	var actions []AlertAction
	for _, rec := range records {
		action, err := deserializeRule(rec)
		if err != nil {
			log.Printf("[alert-action-registry] skip rule %q: %v", rec.Name, err)
			continue
		}
		actions = append(actions, action)
	}

	r.mu.Lock()
	r.actions = actions
	r.mu.Unlock()

	log.Printf("[alert-action-registry] loaded %d rules", len(actions))
	return nil
}

// Reload 重新从 DB 加载（热重载）。
func (r *AlertActionRegistry) Reload(ctx context.Context) error {
	return r.Load(ctx)
}

// Match 对一条告警返回所有匹配的规则。
func (r *AlertActionRegistry) Match(alert Alert) []AlertAction {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return MatchActions(alert, r.actions)
}

// List 返回所有规则（供 API 使用）。
func (r *AlertActionRegistry) List() []AlertAction {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]AlertAction, len(r.actions))
	copy(out, r.actions)
	return out
}

// deserializeRule 把 DB 记录反序列化为 AlertAction。
func deserializeRule(rec store.AlertActionRuleRecord) (AlertAction, error) {
	var action AlertAction
	action.Name = rec.Name
	action.Description = rec.Description
	action.ExecuteLastStep = rec.ExecuteLastStep

	if err := json.Unmarshal(rec.AlertMatch, &action.AlertMatch); err != nil {
		return AlertAction{}, err
	}
	if err := json.Unmarshal(rec.ToolSequence, &action.ToolSequence); err != nil {
		return AlertAction{}, err
	}
	return action, nil
}
