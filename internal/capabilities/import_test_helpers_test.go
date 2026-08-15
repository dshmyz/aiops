package capabilities_test

import (
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// ensureImporterTestDomains 幂等地把 importer 用例依赖的测试域注册为动态工具。
// 域清单现派生自工具注册表（KnownDomains），不再硬编码，因此从 OpenAPI 文本
// 推断 minio/kafka/glusterfs 域的用例必须先注册这些域（模拟"系统已支持该系统"）。
// 包内其他测试（runtime/loader）会按各自清理逻辑重置注册表，故此处采用
// 幂等注册：已存在则跳过，并发重复注册容忍 "already registered" 错误。
func ensureImporterTestDomains(t *testing.T) {
	t.Helper()
	for _, d := range []struct {
		domain string
		kind   string
	}{
		{"kafka", "consumer_group"},
		{"minio", "bucket"},
		{"glusterfs", "volume"},
	} {
		name := d.domain + ".importer.test.read"
		if _, ok := tools.Lookup(name); ok {
			continue
		}
		err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
			Tool: tools.Tool{Name: name, Operation: tools.Read, Risk: tools.Low, Domain: d.domain, ResourceType: d.kind},
			InputSchema: map[string]tools.DynamicInputField{
				"environment": {Type: "string", Required: true},
			},
		}})
		if err != nil && !strings.Contains(err.Error(), "already registered") {
			t.Fatalf("register importer test domain %q: %v", d.domain, err)
		}
	}
}