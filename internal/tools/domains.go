// Package tools 提供工具注册表和输入校验。本文件抽取域名清单，供多处复用。
package tools

import (
	"strings"
	"unicode/utf8"
)

// KnownDomains 返回系统支持的中间件域清单。顺序固定：按字母序排列。
// 这是域名的唯一来源——其他包不应重复维护此列表。
//
// 调用方：
//   - orchestrator.SplitMessage（多域检测）
//   - capabilities.inferDomain（OpenAPI 导入推断）
//   - assistant action/eino prompt（域枚举）
//
// 原先各处各自维护 []string{"kafka", "minio", "glusterfs"}，现统一从此派生。
func KnownDomains() []string {
	return []string{"glusterfs", "kafka", "minio"}
}

// DomainAliases 返回域名的别名映射：alias → canonical。
// 当前仅 "gluster" → "glusterfs"，供推断场景使用（用户消息、OpenAPI tags）。
func DomainAliases() map[string]string {
	return map[string]string{
		"gluster": "glusterfs",
	}
}

// MatchDomainBounded 在文本中查找最早出现的、边界完整匹配的已知域名。
//
// 边界定义：域名两侧必须是分隔符或字符串起止，而非字母/数字/下划线。
// 用 rune 解码以正确处理多字节标点（中文全角括号、中文逗号等）。
//
// "最早出现"按文本位置排序，而非按候选域名排序：对 "kafka、minio、glusterfs"
// 返回 "kafka"（位置 0），而非 "glusterfs"（位置 8）。这让多域扫描能按
// 用户提及的顺序逐个取出。
//
// 例子：
//   - "查看 prod kafka 状态" → ("kafka", true)
//   - "检查（minio）容量" → ("minio", true)   // 中文全角括号
//   - "kafkax 健康状态" → ("", false)         // 无边界，不匹配
//   - "查看 gluster 卷" → ("glusterfs", true) // 别名展开
//
// 返回值：(domain, ok)。domain 是规范名（别名已展开），ok=false 表示未找到。
//
// 原实现：internal/assistant/planner.go 的 extractDomain + boundedBySeparator。
// 现提升为工具包导出函数，供 orchestrator 和 capabilities.importer 复用，
// 消除裸 strings.Contains 无边界检测的误匹配（"kafkax" 误命中 "kafka"）。
func MatchDomainBounded(text string) (string, bool) {
	const separators = " \t\r\n,/()[]（），、。：:；;"
	text = strings.ToLower(text)
	aliases := DomainAliases()
	candidates := append([]string{}, KnownDomains()...)
	for alias := range aliases {
		candidates = append(candidates, alias)
	}

	bestIdx := len(text) + 1
	bestDomain := ""
	found := false

	for _, candidate := range candidates {
		for offset := 0; ; {
			rel := strings.Index(text[offset:], candidate)
			if rel < 0 {
				break
			}
			idx := offset + rel
			offset = idx + len(candidate)
			if !boundedBySeparator(text, idx, offset, separators) {
				continue
			}
			// 取文本位置最早出现的域名（相同位置时保留第一个找到的候选）。
			if idx < bestIdx {
				bestIdx = idx
				domain := candidate
				if canonical, isAlias := aliases[candidate]; isAlias {
					domain = canonical
				}
				bestDomain = domain
				found = true
			}
			break
		}
	}
	return bestDomain, found
}

// boundedBySeparator 判断 text[start:end] 两侧是否为分隔符或字符串边界。
// 按 rune 解码以正确处理多字节标点（中文全角标点、emoji 等）。
func boundedBySeparator(text string, start, end int, separators string) bool {
	if start > 0 {
		prev, _ := utf8.DecodeLastRuneInString(text[:start])
		if !strings.ContainsRune(separators, prev) {
			return false
		}
	}
	if end < len(text) {
		next, _ := utf8.DecodeRuneInString(text[end:])
		if !strings.ContainsRune(separators, next) {
			return false
		}
	}
	return true
}
