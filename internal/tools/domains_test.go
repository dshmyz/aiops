package tools_test

import (
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestKnownDomainsReturnsAlphabeticallySortedList(t *testing.T) {
	domains := tools.KnownDomains()
	if len(domains) != 3 {
		t.Fatalf("KnownDomains count = %d, want 3", len(domains))
	}
	if domains[0] != "glusterfs" || domains[1] != "kafka" || domains[2] != "minio" {
		t.Fatalf("KnownDomains = %v, want [glusterfs kafka minio] in alpha order", domains)
	}
}

func TestDomainAliasesReturnsGlusterMapping(t *testing.T) {
	aliases := tools.DomainAliases()
	if len(aliases) != 1 {
		t.Fatalf("DomainAliases count = %d, want 1", len(aliases))
	}
	if aliases["gluster"] != "glusterfs" {
		t.Fatalf("aliases[gluster] = %q, want glusterfs", aliases["gluster"])
	}
}

// TestMatchDomainBoundedFindsCanonicalDomain 覆盖基础的边界完整匹配：
// 中英文空格、中英文标点、字符串起止，以及 gluster 别名展开。
func TestMatchDomainBoundedFindsCanonicalDomain(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		text string
		want string
	}{
		{"查看 prod kafka 状态", "kafka"},
		{"检查（minio）容量", "minio"},                      // 中文全角括号
		{"glusterfs 卷健康", "glusterfs"},                 // 字符串起始
		{"状态：kafka。", "kafka"},                         // 中文冒号、句号
		{"查看 gluster 卷", "glusterfs"},                  // 别名展开
		{"kafka", "kafka"},                              // 单词即全文
		{"kafka、minio、glusterfs", "kafka"},             // 顿号分隔，返回首个
		{"prod/kafka/health", "kafka"},                  // 斜线分隔
		{"检查 minio，kafka 延迟", "minio"},                // 中文逗号，返回首个
		{"KAFKA", "kafka"},                              // 大写转小写
		{"Kafka Status", "kafka"},                       // 首字母大写
		{"check gluster volume", "glusterfs"},           // 英文空格 + 别名
		{"(kafka)", "kafka"},                            // 英文括号
		{"kafka/minio", "kafka"},                        // 返回第一个
	} {
		t.Run(test.text, func(t *testing.T) {
			domain, ok := tools.MatchDomainBounded(test.text)
			if !ok {
				t.Fatalf("MatchDomainBounded(%q) = (\"\", false), want (%q, true)", test.text, test.want)
			}
			if domain != test.want {
				t.Fatalf("MatchDomainBounded(%q) = (%q, true), want (%q, true)", test.text, domain, test.want)
			}
		})
	}
}

// TestMatchDomainBoundedRejectsBareSubstring 是回归测试：修复前
// orchestrator.go:101 和 importer.go:285 用裸 strings.Contains，
// "kafkax" 误命中 "kafka"。现要求边界完整。
func TestMatchDomainBoundedRejectsBareSubstring(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"kafkax",              // 域名内嵌于其他单词
		"查看 prod kafkax 状态", // 中文语境，域名后接字母
		"minioadmin",          // minio 内嵌
		"glusterfsx",          // glusterfs 后接字母
		"xkafka",              // 域名前接字母
		"检查xminio健康",         // 中文字 + 域名 + 中文字，无分隔符
		"prod环境kafka集群",      // 中文字紧邻，无分隔符前缀
		"",                    // 空字符串
		"prod staging dev",    // 仅环境关键词
		"健康状态",               // 仅中文，无域名
	} {
		t.Run(text, func(t *testing.T) {
			if domain, ok := tools.MatchDomainBounded(text); ok {
				t.Fatalf("MatchDomainBounded(%q) = (%q, true), want (\"\", false) — bare substring should not match", text, domain)
			}
		})
	}
}

// TestMatchDomainBoundedHandlesMultibyteCorrectly 验证 UTF-8 rune 解码：
// 中文全角标点、emoji 等多字节字符作为分隔符时能正确识别边界。
func TestMatchDomainBoundedHandlesMultibyteCorrectly(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		text string
		want string
	}{
		{"查看（kafka）状态", "kafka"},      // 中文全角括号
		{"检查、minio、容量", "minio"},      // 中文顿号
		{"kafka。健康", "kafka"},          // 中文句号
		{"状态：gluster：卷", "glusterfs"}, // 中文冒号 + 别名
	} {
		t.Run(test.text, func(t *testing.T) {
			domain, ok := tools.MatchDomainBounded(test.text)
			if !ok || domain != test.want {
				t.Fatalf("MatchDomainBounded(%q) = (%q, %v), want (%q, true)", test.text, domain, ok, test.want)
			}
		})
	}
}
