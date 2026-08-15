// Package tools 提供工具注册表和输入校验。本文件抽取域名清单，供多处复用。
package tools

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// KnownDomains 返回系统当前支持的域清单，派生自工具注册表：
// 静态工具 + 动态工具（已发布能力 / MCP）的 Domain 字段去重、按字母序。
// 这是域名的唯一来源——其他包不应重复维护此列表。
//
// 调用方：
//   - orchestrator.SplitMessage（多域检测）
//   - capabilities.inferDomain（OpenAPI 导入推断）
//   - assistant action/eino prompt（域枚举）
//
// 域清单不再手写维护：只有实际注册了工具（或发布了能力）的域才会被
// 识别，测试域不会自动成为正式域。inferDomain 对未注册的新系统返回
// "unknown"，由管理员在导入评审时显式登记，避免"导入即认"。
func KnownDomains() []string {
	seen := make(map[string]bool)
	for _, tool := range registeredTools {
		if domain := strings.TrimSpace(tool.Domain); domain != "" {
			seen[domain] = true
		}
	}
	dynamicMu.RLock()
	for _, tool := range dynamicTools {
		if domain := strings.TrimSpace(tool.Domain); domain != "" {
			seen[domain] = true
		}
	}
	dynamicMu.RUnlock()

	domains := make([]string, 0, len(seen))
	for domain := range seen {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	return domains
}

// FindDomainReadTool 返回某域已注册的读工具，未注册该域时 ok=false。
func FindDomainReadTool(domain string) (Tool, bool) {
	if strings.TrimSpace(domain) == "" {
		return Tool{}, false
	}
	for _, tool := range All() {
		if tool.Domain == domain && tool.Operation == Read {
			return tool, true
		}
	}
	return Tool{}, false
}

// FindDomainWriteTool 返回某域已注册的写工具，未注册该域时 ok=false。
// 用于推荐逻辑：把一个域的读诊断结果映射为可执行的修复工具。
func FindDomainWriteTool(domain string) (Tool, bool) {
	if strings.TrimSpace(domain) == "" {
		return Tool{}, false
	}
	for _, tool := range All() {
		if tool.Domain == domain && tool.Operation == Write {
			return tool, true
		}
	}
	return Tool{}, false
}

// ResourceTypeForDomain 返回某域已注册工具的资源类型。同一域可能注册多个
// 工具，取第一个读工具的资源类型；未注册该域时返回空字符串。
func ResourceTypeForDomain(domain string) string {
	if tool, ok := FindDomainReadTool(domain); ok {
		return tool.ResourceType
	}
	return ""
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
//
// 返回值：(domain, ok)。domain 是规范名（别名已展开），ok=false 表示未找到。
//
// 原实现：internal/assistant/planner.go 的 extractDomain + boundedBySeparator。
// 现提升为工具包导出函数，供 orchestrator 和 capabilities.importer 复用，
// 消除裸 strings.Contains 无边界检测的误匹配（"kafkax" 误命中 "kafka"）。
func MatchDomainBounded(text string) (string, bool) {
	const separators = " \t\r\n,/()[]（），、。：:；;"
	text = strings.ToLower(text)
	candidates := append([]string{}, KnownDomains()...)

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
			// 取文本位置最早出现的域名。
			if idx < bestIdx {
				bestIdx = idx
				bestDomain = candidate
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
