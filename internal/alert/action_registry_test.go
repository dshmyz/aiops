package alert_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/alert"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// fakeRuleStore 是 AlertActionRuleStore 的内存实现，验证 Upsert/Delete 落库。
type fakeRuleStore struct {
	records map[string]store.AlertActionRuleRecord
}

func (f *fakeRuleStore) List(_ context.Context) ([]store.AlertActionRuleRecord, error) {
	out := make([]store.AlertActionRuleRecord, 0, len(f.records))
	for _, r := range f.records {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeRuleStore) Get(_ context.Context, name string) (store.AlertActionRuleRecord, error) {
	r, ok := f.records[name]
	if !ok {
		return store.AlertActionRuleRecord{}, store.ErrNotFound
	}
	return r, nil
}

func (f *fakeRuleStore) Upsert(_ context.Context, rule store.AlertActionRuleRecord) error {
	if f.records == nil {
		f.records = map[string]store.AlertActionRuleRecord{}
	}
	f.records[rule.Name] = rule
	return nil
}

func (f *fakeRuleStore) Delete(_ context.Context, name string) error {
	delete(f.records, name)
	return nil
}

func sampleAlertAction(name string) alert.AlertAction {
	return alert.AlertAction{
		Name:            name,
		Description:     "容量告警处置",
		ExecuteLastStep: false,
		AlertMatch:      alert.AlertMatch{Severity: "critical", Domain: "demo"},
		ToolSequence: []alert.AlertActionStep{
			{Tool: "alert.query"},
			{Tool: "demo.retention.set", Input: map[string]string{"name": "{resource_name}"}},
		},
	}
}

// TestAlertActionRegistryUpsertPersistsAndReloads 验证管理后台的"新建规则"真实落库
// 并热重载（修复前 POST 是 TODO，只返回假成功）。
func TestAlertActionRegistryUpsertPersistsAndReloads(t *testing.T) {
	fs := &fakeRuleStore{}
	registry := alert.NewAlertActionRegistry(fs)
	ctx := context.Background()

	action := sampleAlertAction("capacity-critical-handler")
	if err := registry.Upsert(ctx, action); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// 落库
	if _, err := fs.Get(ctx, action.Name); err != nil {
		t.Fatalf("rule not persisted in store: %v", err)
	}
	// 热重载后 registry 能匹配到该规则
	if len(registry.List()) != 1 {
		t.Fatalf("registry.List() = %d rules, want 1 after reload", len(registry.List()))
	}
}

// TestAlertActionRegistryDeleteRemovesAndReloads 验证删除真实删库并热重载。
func TestAlertActionRegistryDeleteRemovesAndReloads(t *testing.T) {
	fs := &fakeRuleStore{}
	registry := alert.NewAlertActionRegistry(fs)
	ctx := context.Background()

	if err := registry.Upsert(ctx, sampleAlertAction("to-delete")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := registry.Delete(ctx, "to-delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := fs.Get(ctx, "to-delete"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("rule still in store after delete, want ErrNotFound")
	}
	if len(registry.List()) != 0 {
		t.Fatalf("registry.List() = %d rules, want 0 after delete", len(registry.List()))
	}
}

// TestAlertActionRegistryUpsertPreservesDisabledState 验证 Enabled 三态语义：
//   - 新建规则（Enabled 未显式设置）默认启用（Enabled=true 落库）
//   - 显式停用（Enabled=&false）持久化到 DB，且不参与 List/Match
//   - 编辑停用中的规则（不带 Enabled 字段）保留禁用状态，不会静默复活
//
// 修复回归：serializeRule 恒写 Enabled:true，操作者停用的规则一编辑就复活。
// 用 fs.Get 校验 DB 记录，而非 registry.List()——List 只返回启用规则，无法
// 区分"未落库"和"已停用"。
func TestAlertActionRegistryUpsertPreservesDisabledState(t *testing.T) {
	fs := &fakeRuleStore{}
	registry := alert.NewAlertActionRegistry(fs)
	ctx := context.Background()

	// 1. 新建规则（不带 Enabled）→ 默认启用
	if err := registry.Upsert(ctx, sampleAlertAction("capacity-critical-handler")); err != nil {
		t.Fatalf("Upsert new rule: %v", err)
	}
	rec, err := fs.Get(ctx, "capacity-critical-handler")
	if err != nil {
		t.Fatalf("Get after new upsert: %v", err)
	}
	if !rec.Enabled {
		t.Fatal("new rule should default to enabled in DB")
	}

	// 2. 显式停用 → 持久化，且不再被匹配
	disabled := false
	action := sampleAlertAction("capacity-critical-handler")
	action.Enabled = &disabled
	if err := registry.Upsert(ctx, action); err != nil {
		t.Fatalf("Upsert disabled rule: %v", err)
	}
	rec, err = fs.Get(ctx, "capacity-critical-handler")
	if err != nil {
		t.Fatalf("Get after disable: %v", err)
	}
	if rec.Enabled {
		t.Fatal("explicitly disabled rule should persist Enabled=false in DB")
	}
	// List() 现在返回全部规则（含停用，供管理界面展示），但 Match 不再命中停用规则。
	if len(registry.List()) != 1 {
		t.Fatalf("List() = %d after disable, want 1 (all rules, disabled included for admin view)", len(registry.List()))
	}
	if matched := registry.Match(alert.Alert{Severity: "critical", Domain: "demo"}); len(matched) != 0 {
		t.Fatalf("Match() = %d after disable, want 0 (disabled rules excluded from matching)", len(matched))
	}

	// 3. 编辑停用中的规则，不带 Enabled 字段 → 保留禁用状态
	if err := registry.Upsert(ctx, sampleAlertAction("capacity-critical-handler")); err != nil {
		t.Fatalf("Upsert without enabled field: %v", err)
	}
	rec, err = fs.Get(ctx, "capacity-critical-handler")
	if err != nil {
		t.Fatalf("Get after edit: %v", err)
	}
	if rec.Enabled {
		t.Fatal("editing a disabled rule without an enabled field must preserve disabled state (regression: previously resurrected to enabled)")
	}
	if len(registry.List()) != 1 {
		t.Fatalf("List() = %d after editing disabled rule, want 1", len(registry.List()))
	}
}

// TestAlertActionRegistrySetEnabled 验证启停开关：SetEnabled 只改启停位并热重载，
// 停用后 Match 不再命中、启用后恢复命中，且不影响其他字段。
func TestAlertActionRegistrySetEnabled(t *testing.T) {
	fs := &fakeRuleStore{}
	registry := alert.NewAlertActionRegistry(fs)
	ctx := context.Background()

	if err := registry.Upsert(ctx, sampleAlertAction("toggle-me")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// 停用
	if err := registry.SetEnabled(ctx, "toggle-me", false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	rec, err := fs.Get(ctx, "toggle-me")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rec.Enabled {
		t.Fatal("SetEnabled(false) should persist Enabled=false")
	}
	if matched := registry.Match(alert.Alert{Severity: "critical", Domain: "demo"}); len(matched) != 0 {
		t.Fatalf("Match after disable = %d, want 0", len(matched))
	}

	// 重新启用
	if err := registry.SetEnabled(ctx, "toggle-me", true); err != nil {
		t.Fatalf("SetEnabled(true): %v", err)
	}
	rec, err = fs.Get(ctx, "toggle-me")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !rec.Enabled {
		t.Fatal("SetEnabled(true) should persist Enabled=true")
	}
	if matched := registry.Match(alert.Alert{Severity: "critical", Domain: "demo"}); len(matched) != 1 {
		t.Fatalf("Match after enable = %d, want 1", len(matched))
	}
	// 其他字段不受影响（DB 记录里 JSON 字段仍是原字节，非空即未被覆写）
	if rec.Description != "容量告警处置" || len(rec.ToolSequence) == 0 || len(rec.AlertMatch) == 0 {
		t.Fatalf("SetEnabled changed unrelated fields: %+v", rec)
	}
}

// TestAlertActionRegistrySetEnabledNotFound 验证对不存在规则 SetEnabled 返回 ErrNotFound。
func TestAlertActionRegistrySetEnabledNotFound(t *testing.T) {
	registry := alert.NewAlertActionRegistry(&fakeRuleStore{})
	err := registry.SetEnabled(context.Background(), "missing", true)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SetEnabled(missing) err = %v, want ErrNotFound", err)
	}
}

// TestAlertActionSerializeRoundTrip 验证 serializeRule → deserializeRule 往返一致，
// 保证 DB 存储的 alert_match / tool_sequence JSON 能正确还原为可匹配的规则。
func TestAlertActionSerializeRoundTrip(t *testing.T) {
	registry := alert.NewAlertActionRegistry(&fakeRuleStore{})
	// 通过 Upsert（内部 serializeRule + 热重载）再反序列化验证。
	ctx := context.Background()
	action := sampleAlertAction("roundtrip")
	if err := registry.Upsert(ctx, action); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got := registry.List()
	if len(got) != 1 {
		t.Fatalf("List = %d, want 1", len(got))
	}
	if got[0].Name != action.Name || got[0].AlertMatch.Severity != "critical" {
		t.Fatalf("roundtrip mismatch: %+v", got[0])
	}
	if len(got[0].ToolSequence) != 2 || got[0].ToolSequence[1].Tool != "demo.retention.set" {
		t.Fatalf("tool sequence roundtrip mismatch: %+v", got[0].ToolSequence)
	}
	// 规则应能被同一告警匹配。
	matched := registry.Match(alert.Alert{
		Severity: "critical", Domain: "demo",
		Labels: map[string]string{"resource_name": "data"},
	})
	if len(matched) != 1 {
		t.Fatalf("Match = %d rules, want 1", len(matched))
	}
}

// TestAlertActionRegistryImplementsMatcher 验证 DB 注册表实现了 ActionMatcher 接口，
// 可直接注入 webhook 服务驱动链式研判（后台配置真正生效的接线契约）。
func TestAlertActionRegistryImplementsMatcher(t *testing.T) {
	ctx := context.Background()
	store := &fakeRuleStore{}
	registry := alert.NewAlertActionRegistry(store)
	if err := registry.Upsert(ctx, alert.AlertAction{
		Name:            "m1",
		Description:     "kafka lag",
		ExecuteLastStep: false,
		AlertMatch:      alert.AlertMatch{AlertName: "HighLag"},
		ToolSequence:    []alert.AlertActionStep{{Tool: "kafka.consumer_group.lag.read"}},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	var matcher alert.ActionMatcher = registry // 编译期断言：registry 实现 ActionMatcher
	if got := matcher.Match(alert.Alert{Labels: map[string]string{"alertname": "HighLag"}}); len(got) != 1 {
		t.Fatalf("Match via interface = %d, want 1", len(got))
	}
	// 停用后接口层面不再命中。
	if err := registry.SetEnabled(ctx, "m1", false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if got := matcher.Match(alert.Alert{Labels: map[string]string{"alertname": "HighLag"}}); len(got) != 0 {
		t.Fatalf("Match after disable = %d, want 0", len(got))
	}
	// 大小写不敏感放宽：接口层面也应命中。
	if err := registry.SetEnabled(ctx, "m1", true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if got := matcher.Match(alert.Alert{Labels: map[string]string{"alertname": "highlag"}}); len(got) != 1 {
		t.Fatalf("Match case-insensitive = %d, want 1", len(got))
	}
}
