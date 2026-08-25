package capabilities

import (
	"strings"
	"testing"
)

// 数组字段在 A+B 策略下应产出：_count（总数）+ _sample（首元素完整样例）+ _overview（全量判别字段）。
func TestSmartExtractArraySampleAndOverview(t *testing.T) {
	raw := map[string]any{
		"total": 50,
		"topics": []any{
			map[string]any{"name": "orders", "partitions": 12, "lag": 500, "status": "critical", "owner": "orders-owner"},
			map[string]any{"name": "payments", "partitions": 6, "lag": 20, "status": "ok", "owner": "payments-owner"},
			map[string]any{"name": "inventory", "partitions": 4, "lag": 1200, "status": "critical", "owner": "inventory-owner"},
		},
	}
	out := smartExtractFields(raw)
	if got, ok := out["topics_count"]; !ok || got != 3 {
		t.Fatalf("topics_count = %v (ok=%v), want 3", got, ok)
	}
	sample, ok := out["topics_sample"].(map[string]any)
	if !ok {
		t.Fatalf("topics_sample missing or not an object")
	}
	// _sample 必须是首元素的完整样例（含 partitions/owner 等非判别字段）
	for _, k := range []string{"name", "partitions", "lag", "status", "owner"} {
		if _, ok := sample[k]; !ok {
			t.Fatalf("topics_sample missing field %q; sample=%v", k, sample)
		}
	}
	overview, ok := out["topics_overview"].([]any)
	if !ok {
		t.Fatalf("topics_overview missing or not an array")
	}
	// _overview 覆盖全部元素，且每个元素保留判别字段
	if len(overview) != 3 {
		t.Fatalf("topics_overview len = %d, want 3", len(overview))
	}
	first, ok := overview[0].(map[string]any)
	if !ok {
		t.Fatalf("topics_overview[0] not an object")
	}
	if first["name"] != "orders" || first["status"] != "critical" {
		t.Fatalf("topics_overview[0] = %v, want {name:orders status:critical}", first)
	}
}

func TestSmartExtractArrayOverviewCoversAllItems(t *testing.T) {
	raw := map[string]any{
		"topics": []any{
			map[string]any{"name": "t0", "status": "ok"},
			map[string]any{"name": "t1", "status": "ok"},
			map[string]any{"name": "t2", "status": "ok"},
			map[string]any{"name": "t3", "status": "ok"},
			map[string]any{"name": "t4", "status": "ok"},
		},
	}
	out := smartExtractFields(raw)
	overview := out["topics_overview"].([]any)
	if len(overview) != 5 {
		t.Fatalf("topics_overview len = %d, want 5（全量覆盖）", len(overview))
	}
}

func TestSmartExtractSampleFiltersSensitive(t *testing.T) {
	raw := map[string]any{
		"topics": []any{
			map[string]any{"name": "orders", "status": "ok", "secret_token": "hide-me"},
		},
	}
	out := smartExtractFields(raw)
	sample := out["topics_sample"].(map[string]any)
	if _, ok := sample["secret_token"]; ok {
		t.Fatalf("topics_sample must filter sensitive field, got %v", sample)
	}
	if sample["name"] != "orders" {
		t.Fatalf("topics_sample.name = %v, want orders", sample["name"])
	}
}

func TestSmartExtractOverviewFiltersSensitiveKey(t *testing.T) {
	raw := map[string]any{
		"topics": []any{
			map[string]any{"name": "orders", "status": "ok", "secret_token": "hide-me"},
		},
	}
	out := smartExtractFields(raw)
	overview := out["topics_overview"].([]any)
	item := overview[0].(map[string]any)
	if _, ok := item["secret_token"]; ok {
		t.Fatalf("topics_overview item must filter sensitive key, got %v", item)
	}
	if item["name"] != "orders" {
		t.Fatalf("topics_overview[0].name = %v, want orders", item["name"])
	}
}

// 标量元素（非对象）数组直接原样进 overview，且不丢明细。
func TestSmartExtractScalarArrayElements(t *testing.T) {
	raw := map[string]any{"ports": []any{8080, 9092, 9300}}
	out := smartExtractFields(raw)
	overview := out["ports_overview"].([]any)
	if len(overview) != 3 {
		t.Fatalf("ports_overview len = %d, want 3", len(overview))
	}
	if overview[1] != 9092 {
		t.Fatalf("ports_overview[1] = %v, want 9092", overview[1])
	}
}

// 现有标量/嵌套行为不得回归。
func TestSmartExtractScalarBehaviorKept(t *testing.T) {
	raw := map[string]any{"usage_pct": 86, "secret_token": "hide", "node": map[string]any{"zone": "a"}}
	out := smartExtractFields(raw)
	if out["usage_pct"] != "86" {
		t.Fatalf("usage_pct = %v (%T), want \"86\"", out["usage_pct"], out["usage_pct"])
	}
	if _, ok := out["secret_token"]; ok {
		t.Fatalf("scalar sensitive field must be filtered")
	}
	if out["node.zone"] != "a" {
		t.Fatalf("node.zone = %v, want a", out["node.zone"])
	}
}

// 数据驱动：_overview 保留元素的所有短标量字段（接口带什么字段就展示什么），
// 不再依赖写死的判别字段词表；长值截断、嵌套对象不进概览、完整结构保在 _sample。
func TestSmartExtractOverviewDataDriven(t *testing.T) {
	raw := map[string]any{
		"topics": []any{
			map[string]any{"name": "orders", "partitions": 12, "lag": 500, "status": "critical", "config": map[string]any{"retention": "7d"}},
		},
	}
	out := smartExtractFields(raw)
	overview := out["topics_overview"].([]any)
	item := overview[0].(map[string]any)
	for _, k := range []string{"name", "partitions", "lag", "status"} {
		if _, ok := item[k]; !ok {
			t.Fatalf("overview item must keep short scalar field %q, got %v", k, item)
		}
	}
	if _, ok := item["config"]; ok {
		t.Fatalf("overview item must not keep nested object, got %v", item)
	}
	sample := out["topics_sample"].(map[string]any)
	if _, ok := sample["config"]; !ok {
		t.Fatalf("topics_sample must keep nested structure, got %v", sample)
	}
}

func TestSmartExtractOverviewTruncatesLongValue(t *testing.T) {
	raw := map[string]any{"topics": []any{map[string]any{"name": "orders", "desc": strings.Repeat("x", 500)}}}
	out := smartExtractFields(raw)
	overview := out["topics_overview"].([]any)
	item := overview[0].(map[string]any)
	if got := item["desc"]; got != strings.Repeat("x", maxOverviewValueLen)+"…" {
		t.Fatalf("overview long value = %q, want %d x + ellipsis", got, maxOverviewValueLen)
	}
	sample := out["topics_sample"].(map[string]any)
	if got := sample["desc"].(string); len(got) != 500 {
		t.Fatalf("_sample should keep full untruncated value, got len=%d", len(got))
	}
}

func TestApplyStatusMapping(t *testing.T) {
	mapping := map[string]string{"RED": "critical", "YELLOW": "warning", "running": "ok"}
	cases := []struct {
		name     string
		severity string
		want     string
	}{
		{name: "命中映射", severity: "RED", want: "critical"},
		{name: "大小写不敏感", severity: "running", want: "ok"},
		{name: "大小写不敏感-大写", severity: "RUNNING", want: "ok"},
		{name: "去空格", severity: " red ", want: "critical"},
		{name: "未命中保持", severity: "warning", want: "warning"},
		{name: "空值保持", severity: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := applyStatusMapping(tc.severity, mapping); got != tc.want {
				t.Fatalf("applyStatusMapping(%q) = %q, want %q", tc.severity, got, tc.want)
			}
		})
	}
	// 空映射无副作用
	if got := applyStatusMapping("RED", nil); got != "RED" {
		t.Fatalf("applyStatusMapping with nil mapping = %q, want RED", got)
	}
}

// _sample（首元素完整样例）必须对任意层级的敏感字段做过滤，嵌套 secret 不得经 _sample 泄漏给 LLM。
func TestSmartExtractSampleFiltersNestedSensitive(t *testing.T) {
	raw := map[string]any{
		"topics": []any{
			map[string]any{
				"name": "orders",
				"lag":  5,
				"creds": map[string]any{"secret_token": "x", "zone": "a"},
			},
		},
	}
	fields := smartExtractFields(raw)
	sample, ok := fields["topics_sample"].(map[string]any)
	if !ok || sample == nil {
		t.Fatalf("topics_sample missing: %+v", fields)
	}
	if _, leaked := sample["creds"].(map[string]any)["secret_token"]; leaked {
		t.Fatalf("_sample leaked nested secret_token: %+v", sample["creds"])
	}
	creds, _ := sample["creds"].(map[string]any)
	if creds["zone"] != "a" {
		t.Fatalf("_sample should keep non-sensitive nested field zone, got %+v", sample["creds"])
	}
}

func TestSmartExtractEmptyArrayNoCrash(t *testing.T) {
	out := smartExtractFields(map[string]any{"topics": []any{}})
	if _, ok := out["topics_count"]; ok {
		t.Fatalf("empty array should not emit count")
	}
	if len(out) != 0 {
		t.Fatalf("empty array should leave no fields, got %v", out)
	}
}

func TestSmartExtractVeryLongValueStillContained(t *testing.T) {
	raw := map[string]any{"big": strings.Repeat("x", 20000)}
	out := smartExtractFields(raw)
	if got, ok := out["big"].(string); !ok || len(got) != 20000 {
		t.Fatalf("big value should be preserved, len=%d ok=%v", len(got), ok)
	}
}