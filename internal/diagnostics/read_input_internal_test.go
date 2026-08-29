package diagnostics

import "testing"

// TestBuildReadInputLabelPriority 回归：告警标签按字段名填充读工具入参，
// 资源名只落到标签未覆盖的第一个字段。修复前多字段能力（cluster+group）
// 只收到 {cluster: 资源名}，group 必填缺失 → invalid_input，研判全挂。
func TestBuildReadInputLabelPriority(t *testing.T) {
	schema := map[string]any{
		"cluster": map[string]any{"type": "string", "required": true},
		"group":   map[string]any{"type": "string", "required": true},
	}
	// 标签齐全：两字段都来自标签。
	got := buildReadInput(schema, "payments", map[string]string{"cluster": "c1", "group": "payments"})
	if got["cluster"] != "c1" || got["group"] != "payments" {
		t.Fatalf("label fill failed: %v", got)
	}
	// 标签只覆盖 cluster：资源名落到剩余字段 group。
	got = buildReadInput(schema, "payments", map[string]string{"cluster": "c1"})
	if got["cluster"] != "c1" || got["group"] != "payments" {
		t.Fatalf("fallback fill failed: %v", got)
	}
	// 无标签：维持旧行为（资源名 → 第一个字段）。
	got = buildReadInput(schema, "payments", nil)
	if got["cluster"] != "payments" {
		t.Fatalf("legacy behavior broken: %v", got)
	}
	// name 字段优先语义不变。
	schemaName := map[string]any{"name": map[string]any{"type": "string", "required": true}}
	got = buildReadInput(schemaName, "x", map[string]string{"name": "y"})
	if got["name"] != "x" {
		t.Fatalf("name-field preference broken: %v", got)
	}
}
