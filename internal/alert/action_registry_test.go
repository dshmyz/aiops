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
		if r.Enabled {
			out = append(out, r)
		}
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
