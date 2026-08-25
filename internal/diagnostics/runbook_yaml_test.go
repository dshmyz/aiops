package diagnostics

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runbooks.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp yaml: %v", err)
	}
	return path
}

func restoreRunbookTemplates(t *testing.T) {
	t.Helper()
	snapshot := map[string]RunbookTemplate{}
	for k, v := range runbookTemplates {
		snapshot[k] = v
	}
	t.Cleanup(func() {
		runbookTemplates = snapshot
	})
}

// yaml 增量注册：同名覆盖内建项、新名追加，未提到的内建项保持不变，
// 校验与维度生成对新增模板自动生效（单一扩展点 registerRunbook）。
func TestRegisterRunbooksFromYAMLAddsAndOverrides(t *testing.T) {
	restoreRunbookTemplates(t)
	path := writeTempYAML(t, `
runbooks:
  health:
    description: "自定义健康巡检（覆盖内建）"
    checkpoints:
      - 自定义检查项 A
      - 自定义检查项 B
  network:
    description: "网络连通性巡检"
    checkpoints:
      - 连通性
      - 丢包率
`)
	names, err := RegisterRunbooksFromYAML(path)
	if err != nil {
		t.Fatalf("RegisterRunbooksFromYAML: %v", err)
	}
	if !reflect.DeepEqual(names, []string{"health", "network"}) {
		t.Fatalf("registered names = %v, want [health network]", names)
	}

	health, ok := lookupRunbook("health")
	if !ok || health.Description != "自定义健康巡检（覆盖内建）" {
		t.Fatalf("health should be overridden by yaml, got %+v", health)
	}
	if got := genericCheckpoints("network", "kafka"); !reflect.DeepEqual(got, []string{"连通性", "丢包率"}) {
		t.Fatalf("genericCheckpoints(network) = %v", got)
	}
	if !validRunbook("network") {
		t.Fatal("yaml-registered runbook should pass validation")
	}
	if _, ok := lookupRunbook("capacity"); !ok {
		t.Fatal("built-in capacity must remain untouched")
	}
}

// 显式指定的配置文件出错必须返回错误（读不到/解析失败/空模板），
// 不允许静默吞掉——与 LoadModelRegistry 的显式失败原则一致。
func TestRegisterRunbooksFromYAMLErrors(t *testing.T) {
	restoreRunbookTemplates(t)

	if _, err := RegisterRunbooksFromYAML(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("missing file must error")
	}

	bad := writeTempYAML(t, "runbooks: [not, a, map]")
	if _, err := RegisterRunbooksFromYAML(bad); err == nil {
		t.Fatal("malformed yaml must error")
	}

	emptyCheckpoints := writeTempYAML(t, `
runbooks:
  hollow:
    description: "没有检查维度"
    checkpoints: []
`)
	_, err := RegisterRunbooksFromYAML(emptyCheckpoints)
	if err == nil || !strings.Contains(err.Error(), "hollow") {
		t.Fatalf("empty checkpoints must error naming the runbook, got %v", err)
	}
	if _, ok := lookupRunbook("hollow"); ok {
		t.Fatal("invalid entry must not be registered")
	}
}

// 文件存在但未声明 runbooks 键：合法的空配置，不注册任何项、不报错，
// 内建模板保持原样。
func TestRegisterRunbooksFromYAMLEmptyConfigIsNoop(t *testing.T) {
	restoreRunbookTemplates(t)
	path := writeTempYAML(t, "# 空配置\n")

	names, err := RegisterRunbooksFromYAML(path)
	if err != nil {
		t.Fatalf("empty config should be a no-op, got %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("names = %v, want none", names)
	}
	if _, ok := lookupRunbook("health"); !ok {
		t.Fatal("built-ins must remain intact")
	}
}
