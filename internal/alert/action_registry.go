package alert

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// AlertActionRegistry 从 DB 加载告警→动作编排规则到内存，供 ChainDiagnoser 匹配。
// 支持热重载（Reload）。
type AlertActionRegistry struct {
	store    store.AlertActionRuleStore
	runStore store.AlertActionRunStore
	mu       sync.RWMutex
	actions  []AlertAction
}

// NewAlertActionRegistry 创建告警动作规则注册表。
func NewAlertActionRegistry(s store.AlertActionRuleStore) *AlertActionRegistry {
	return &AlertActionRegistry{store: s}
}

// WithRunStore 注入执行历史 store，供管理后台查询触发历史与统计。
func (r *AlertActionRegistry) WithRunStore(s store.AlertActionRunStore) *AlertActionRegistry {
	r.runStore = s
	return r
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

// Match 对一条告警返回所有匹配的启用规则。停用的规则不参与匹配（启停开关是
// 管理后台的第一道控制；规则保存即生效的旧行为在列表/编辑中已改为显式开关）。
func (r *AlertActionRegistry) Match(alert Alert) []AlertAction {
	r.mu.RLock()
	defer r.mu.RUnlock()
	enabled := make([]AlertAction, 0, len(r.actions))
	for _, a := range r.actions {
		if a.Enabled == nil || *a.Enabled {
			enabled = append(enabled, a)
		}
	}
	return MatchActions(alert, enabled)
}

// List 返回所有规则（供 API 使用）。
func (r *AlertActionRegistry) List() []AlertAction {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]AlertAction, len(r.actions))
	copy(out, r.actions)
	return out
}

// Upsert 保存（新增/更新）一条规则并热重载，让管理后台的"新建/编辑规则"
// 真实落库并立即生效（修复前 POST 是 TODO，只返回假成功）。
func (r *AlertActionRegistry) Upsert(ctx context.Context, action AlertAction) error {
	if r.store == nil {
		return errors.New("alert action store not configured")
	}
	// Enabled 未显式设置（nil）：新建默认生效；编辑已有规则保留 DB 现有状态，
	// 避免一次 Upsert 把操作者的停用状态静默抹成启用（修复前 serializeRule
	// 恒写 Enabled:true，停用的规则一编辑就复活）。
	if action.Enabled == nil {
		existing, err := r.store.Get(ctx, action.Name)
		switch {
		case errors.Is(err, store.ErrNotFound):
			enabled := true
			action.Enabled = &enabled
		case err != nil:
			return err
		default:
			action.Enabled = &existing.Enabled
		}
	}
	rec, err := serializeRule(action)
	if err != nil {
		return err
	}
	if err := r.store.Upsert(ctx, rec); err != nil {
		return err
	}
	return r.Reload(ctx)
}

// Delete 删除一条规则并热重载。
func (r *AlertActionRegistry) Delete(ctx context.Context, name string) error {
	if r.store == nil {
		return errors.New("alert action store not configured")
	}
	if err := r.store.Delete(ctx, name); err != nil {
		return err
	}
	return r.Reload(ctx)
}

// SetEnabled 启停一条规则（不存在的规则返回 store.ErrNotFound），并热重载。
// 管理后台的启停开关走这里，避免用整条 Upsert 把其他字段一并覆写。
func (r *AlertActionRegistry) SetEnabled(ctx context.Context, name string, enabled bool) error {
	if r.store == nil {
		return errors.New("alert action store not configured")
	}
	rec, err := r.store.Get(ctx, name)
	if err != nil {
		return err
	}
	rec.Enabled = enabled
	if err := r.store.Upsert(ctx, rec); err != nil {
		return err
	}
	return r.Reload(ctx)
}

// RuleRunOverview 是一条规则的触发历史 + 统计（管理后台卡片展示用）。
type RuleRunOverview struct {
	Recent []store.AlertActionRunRecord `json:"recent"`
	Stats  store.AlertActionRunStats    `json:"stats"`
}

// Runs 返回一条规则的触发历史与统计。未配置 runStore 时返回空视图（stats 全 0）。
func (r *AlertActionRegistry) Runs(ctx context.Context, name string, limit int) (RuleRunOverview, error) {
	if r.runStore == nil {
		return RuleRunOverview{}, nil
	}
	recent, err := r.runStore.RecentByRule(ctx, name, limit)
	if err != nil {
		return RuleRunOverview{}, err
	}
	stats, err := r.runStore.RuleStats(ctx, name)
	if err != nil {
		return RuleRunOverview{}, err
	}
	return RuleRunOverview{Recent: recent, Stats: stats}, nil
}

// serializeRule 把 AlertAction 序列化为 DB 记录（deserializeRule 的反向）。
func serializeRule(action AlertAction) (store.AlertActionRuleRecord, error) {
	alertMatch, err := json.Marshal(action.AlertMatch)
	if err != nil {
		return store.AlertActionRuleRecord{}, err
	}
	toolSequence, err := json.Marshal(action.ToolSequence)
	if err != nil {
		return store.AlertActionRuleRecord{}, err
	}
	enabled := true
	if action.Enabled != nil {
		enabled = *action.Enabled
	}
	return store.AlertActionRuleRecord{
		Name:            action.Name,
		AlertMatch:      alertMatch,
		ToolSequence:    toolSequence,
		ExecuteLastStep: action.ExecuteLastStep,
		Description:     action.Description,
		Enabled:         enabled,
	}, nil
}

// deserializeRule 把 DB 记录反序列化为 AlertAction。
func deserializeRule(rec store.AlertActionRuleRecord) (AlertAction, error) {
	var action AlertAction
	action.Name = rec.Name
	action.Description = rec.Description
	action.ExecuteLastStep = rec.ExecuteLastStep
	enabled := rec.Enabled
	action.Enabled = &enabled

	if err := json.Unmarshal(rec.AlertMatch, &action.AlertMatch); err != nil {
		return AlertAction{}, err
	}
	if err := json.Unmarshal(rec.ToolSequence, &action.ToolSequence); err != nil {
		return AlertAction{}, err
	}
	return action, nil
}
