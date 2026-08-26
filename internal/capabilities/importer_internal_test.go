package capabilities

import (
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// registerInternalTestDomains 幂等地注册本包内 inferDomain 用例依赖的测试域。
// 域清单派生自工具注册表，不再硬编码，因此匹配用例需先注册对应域。
func registerInternalTestDomains(t *testing.T) {
	t.Helper()
	for _, d := range []struct {
		domain string
		kind   string
	}{
		{"kafka", "consumer_group"},
		{"minio", "bucket"},
		{"glusterfs", "volume"},
	} {
		name := d.domain + ".internal.test.read"
		if _, ok := tools.Lookup(name); ok {
			continue
		}
		_ = tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
			Tool: tools.Tool{Name: name, Operation: tools.Read, Risk: tools.Low, Domain: d.domain, ResourceType: d.kind},
			InputSchema: map[string]tools.DynamicInputField{
			},
		}})
	}
}

// TestInferDomainRejectsBareSubstring 是回归测试：修复前 inferDomain 用裸
// strings.Contains，"kafkax" 误命中 "kafka"。现委托 tools.MatchDomainBounded，
// 要求词边界完整，裸子串不再匹配。
func TestInferDomainRejectsBareSubstring(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"/api/kafkax/status",
		"summary: minioadmin tool",
		"tags: [glusterfsx]",
	} {
		if domain := inferDomain(text); domain != "unknown" {
			t.Fatalf("inferDomain(%q) = %q, want \"unknown\" — bare substring must not match", text, domain)
		}
	}
}

// TestInferDomainMatchesCanonicalAndAlias 覆盖正常路径：域名出现在 path/tags/
// summary 中应被正确识别。别名机制已移除（测试域不再有别名），只匹配注册域名。
func TestInferDomainMatchesCanonicalAndAlias(t *testing.T) {
	t.Parallel()
	registerInternalTestDomains(t)

	for _, test := range []struct {
		text string
		want string
	}{
		{"/api/minio/clusters/m1/buckets", "minio"},
		{"tags: [kafka] summary: topic retention", "kafka"},
		{"summary: check glusterfs volume health", "glusterfs"},
		{"/api/glusterfs/volumes/data", "glusterfs"},
	} {
		if domain := inferDomain(test.text); domain != test.want {
			t.Fatalf("inferDomain(%q) = %q, want %q", test.text, domain, test.want)
		}
	}
}
