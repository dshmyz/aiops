package capabilities

import "testing"

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
// summary 中应被正确识别，gluster 别名应展开为 glusterfs。
func TestInferDomainMatchesCanonicalAndAlias(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		text string
		want string
	}{
		{"/api/minio/clusters/m1/buckets", "minio"},
		{"tags: [kafka] summary: topic retention", "kafka"},
		{"summary: check gluster volume health", "glusterfs"},
		{"/api/glusterfs/volumes/data", "glusterfs"},
	} {
		if domain := inferDomain(test.text); domain != test.want {
			t.Fatalf("inferDomain(%q) = %q, want %q", test.text, domain, test.want)
		}
	}
}
