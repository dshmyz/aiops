package diagnostics

import (
	"reflect"
	"testing"
)

// 新增诊断模板只需向注册表注册一处：校验与维度生成都自动识别，
// 无需再改 validRunbook 的枚举或 genericCheckpoints 的 switch。
func TestRegisterRunbookSingleExtensionPoint(t *testing.T) {
	t.Cleanup(func() { delete(runbookTemplates, "network") })
	registerRunbook("network", RunbookTemplate{
		Description: "网络连通性巡检",
		Checkpoints: []string{"连通性", "丢包率", "带宽利用率"},
	})

	if !validRunbook("network") {
		t.Fatalf("registered runbook should pass validation")
	}
	if validRunbook("archived") {
		t.Fatalf("unregistered runbook must fail validation")
	}

	want := []string{"连通性", "丢包率", "带宽利用率"}
	got := genericCheckpoints("network", "kafka")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("genericCheckpoints(network,kafka) = %v, want %v", got, want)
	}

	// 不存在的 runbook 兜底为 health，不返回空维度
	fallback := genericCheckpoints("does_not_exist", "kafka")
	if len(fallback) == 0 {
		t.Fatalf("unknown runbook should fall back to health checkpoints")
	}

	if !validRunbook("") {
		t.Fatalf("empty runbook must remain valid (backward compat)")
	}
}