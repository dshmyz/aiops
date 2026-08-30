package capabilities

import (
	"sort"
	"strings"
)

const (
	// maxSmartFields 限制智能提取的扁平字段数量，避免响应过大
	maxSmartFields = 20
	// maxOverviewItems 数组概览(_overview)最多保留的元素个数，防超大列表失控。
	// 概览按"元素"计而非按字段计，故不占 maxSmartFields 预算。
	maxOverviewItems = 200
	// maxOverviewFieldsPerItem 每个概览元素最多保留的短标量字段数（按 key 字典序确定），
	// 防止单个元素标量字段过多拖爆体积。完整结构由 _sample 承担。
	maxOverviewFieldsPerItem = 6
	// maxOverviewValueLen 概览元素标量值的长度上限，超出截断（完整值在 _sample 里）。
	maxOverviewValueLen = 64
	// maxArrayItems 用户显式声明字段路径时，数组标量元素最多保留个数
	// （http_adapter 显式 path 提取使用；智能提取走 flattenArray 不受此限）。
	maxArrayItems = 3
)

// smartExtractFields 从原始响应中智能提取字段：
// 1. 递归展开嵌套对象，扁平化为 a.b.c 路径
// 2. 数组取前 maxArrayItems 个元素 + 总数统计
// 3. 只保留标量值（string/number/bool）
// 4. 过滤敏感字段
// 5. 最多保留 maxSmartFields 个字段
func smartExtractFields(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	flat := make(map[string]any)
	flattenObject("", raw, flat)
	return truncateFields(flat, maxSmartFields)
}

// flattenObject 递归展开嵌套对象为扁平路径。
// prefix 是当前路径前缀（空字符串表示根），结果写入 out。
func flattenObject(prefix string, obj map[string]any, out map[string]any) {
	for key, value := range obj {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}
		// 敏感字段跳过整个子树
		if isSensitive(key) {
			continue
		}
		switch v := value.(type) {
		case map[string]any:
			// 嵌套对象：递归展开
			if len(v) == 0 {
				continue
			}
			flattenObject(fullKey, v, out)
		case []any:
			// 数组：A+B 策略（count + 首元素完整样例 + 全量判别字段概览），
			// 避免"只有 count、没有列表具体元素内容"。
			flattenArray(fullKey, v, out)
		default:
			// 标量值：直接保留
			if s, ok := scalarString(value); ok {
				out[fullKey] = s
			}
		}
	}
}

// flattenArray 处理数组字段（A+B 策略）：
//  1. _count：数组元素总数
//  2. _sample：首个元素的完整对象样例（保结构，任意层级过滤敏感字段）
//  3. _overview：全量元素的关键判别字段（覆盖"有哪些、各自什么状态"）
//
// 它们各自占扁平结果的一个 key，因此不会像之前那样因深层路径在 20 字段截断里被优先丢弃。
func flattenArray(fullKey string, items []any, out map[string]any) {
	if len(items) == 0 {
		return
	}
	out[fullKey+"_count"] = len(items)

	// A：保结构——首元素完整样例。
	if first, ok := items[0].(map[string]any); ok {
		if sample, ok := redactValue(first).(map[string]any); ok && len(sample) > 0 {
			out[fullKey+"_sample"] = sample
		}
	}

	// B：保覆盖——全量元素的关键判别字段概览。
	overview := make([]any, 0, min(len(items), maxOverviewItems))
	for i, item := range items {
		if i >= maxOverviewItems {
			out[fullKey+"_overview_truncated"] = true
			break
		}
		overview = append(overview, keyFieldsOf(item))
	}
	out[fullKey+"_overview"] = overview
}

// keyFieldsOf 保留对象元素的"短标量"字段；标量元素原样保留。
// 它是数据驱动的：不依赖写死的判别字段词表，接口元素带什么短标量字段就展示什么，
// 从而适配各接口字段命名不一致（instance/mode/healthy/pod...）的情况。
// 仅取标量（string/number/bool），嵌套对象/数组不下展（其完整结构保在 _sample 里）。
func keyFieldsOf(item any) any {
	m, ok := item.(map[string]any)
	if !ok {
		return item
	}
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if isSensitive(k) {
			continue
		}
		if _, ok := scalarString(v); ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	pick := make(map[string]any, min(len(keys), maxOverviewFieldsPerItem))
	for _, k := range keys {
		if len(pick) >= maxOverviewFieldsPerItem {
			break
		}
		pick[k] = truncateOverviewValue(m[k])
	}
	return pick
}

// applyStatusMapping 把接口原始状态值按能力自配置的 status_mapping 归一为标准严重级。
// 未命中映射时原样返回（向后兼容：保留 SeverityPath 原始值或智能推断结果）。
// 这是针对具体接口"值→层级"的可配逃生舱，替代仅靠全局 classifySeverity 猜各接口取值。
func applyStatusMapping(severity string, mapping map[string]string) string {
	if len(mapping) == 0 || strings.TrimSpace(severity) == "" {
		return severity
	}
	norm := normalizeStatusKey(severity)
	for k, v := range mapping {
		if normalizeStatusKey(k) == norm {
			return v
		}
	}
	return severity
}

// normalizeStatusKey 归一化状态值用于映射键查找（大小写不敏感、去空格）。
func normalizeStatusKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// truncateOverviewValue 截断概览元素标量值，避免长值撑爆展示。
func truncateOverviewValue(v any) any {
	s, ok := scalarString(v)
	if !ok {
		return v
	}
	if len(s) > maxOverviewValueLen {
		return s[:maxOverviewValueLen] + "…"
	}
	return s
}

// 重要性排序：含 status/result/code/health/name 等关键词的 > 路径短的 > 其他
func truncateFields(fields map[string]any, maxCount int) map[string]any {
	if len(fields) <= maxCount {
		return fields
	}
	// 按重要性排序
	type scored struct {
		key   string
		score int
	}
	scoredFields := make([]scored, 0, len(fields))
	for key := range fields {
		scoredFields = append(scoredFields, scored{key: key, score: fieldImportance(key)})
	}
	// 冒泡排序（简单起见，因为 maxCount 不大）
	for i := 0; i < len(scoredFields)-1; i++ {
		for j := i + 1; j < len(scoredFields); j++ {
			if scoredFields[j].score > scoredFields[i].score {
				scoredFields[i], scoredFields[j] = scoredFields[j], scoredFields[i]
			}
		}
	}
	result := make(map[string]any, maxCount)
	for i := 0; i < maxCount && i < len(scoredFields); i++ {
		key := scoredFields[i].key
		result[key] = fields[key]
	}
	return result
}

// fieldImportance 计算字段的重要性分数。
// 含高价值关键词的字段得分更高，路径更短的得分更高。
func fieldImportance(key string) int {
	score := 0
	lower := strings.ToLower(key)
	// 高价值关键词（+10分）
	highValue := []string{
		"status", "state", "health", "code", "result", "success",
		"name", "id", "count", "total", "size", "usage",
		"error", "message", "severity", "level",
	}
	for _, kw := range highValue {
		if strings.Contains(lower, kw) {
			score += 10
			break
		}
	}
	// 中价值关键词（+5分）
	midValue := []string{
		"cpu", "memory", "disk", "latency", "time", "rate",
		"bytes", "mb", "gb", "kb", "percent", "pct",
	}
	for _, kw := range midValue {
		if strings.Contains(lower, kw) {
			score += 5
			break
		}
	}
	// 路径越短越重要（+1/每级，越深越减分）
	depth := strings.Count(key, ".") + strings.Count(key, "[")
	score -= depth * 2
	// 避免负数
	if score < -10 {
		score = -10
	}
	return score
}

// inferSeverityFromData 从返回数据中自动推断严重级别。
// 查找常见的状态字段（status/health/state/code），根据值判断严重级别。
func inferSeverityFromData(data map[string]any) string {
	if len(data) == 0 {
		return "info"
	}
	// 常见的状态字段名
	statusKeys := []string{
		"severity", "status", "health", "state", "level",
		"result.status", "data.status", "status.code",
	}
	for _, key := range statusKeys {
		if val, ok := data[key]; ok {
			if s, ok := scalarString(val); ok {
				if sev := classifySeverity(s); sev != "" {
					return sev
				}
			}
		}
	}
	// 遍历所有字段，找匹配的
	for key, val := range data {
		lowerKey := strings.ToLower(key)
		if !strings.Contains(lowerKey, "status") &&
			!strings.Contains(lowerKey, "state") &&
			!strings.Contains(lowerKey, "health") &&
			!strings.Contains(lowerKey, "severity") {
			continue
		}
		if s, ok := scalarString(val); ok {
			if sev := classifySeverity(s); sev != "" {
				return sev
			}
		}
	}
	return "info"
}

// classifySeverity 根据状态值判断严重级别。
// 返回空字符串表示无法识别。
func classifySeverity(value string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.Contains(lower, "critical") ||
		strings.Contains(lower, "fatal") ||
		strings.Contains(lower, "down") ||
		strings.Contains(lower, "error") && !strings.Contains(lower, "no error"):
		return "critical"
	case strings.Contains(lower, "warn") ||
		strings.Contains(lower, "degraded") ||
		strings.Contains(lower, "warning") ||
		strings.Contains(lower, "unhealthy"):
		return "warning"
	case strings.Contains(lower, "ok") ||
		strings.Contains(lower, "healthy") ||
		strings.Contains(lower, "normal") ||
		strings.Contains(lower, "success") ||
		strings.Contains(lower, "running") ||
		strings.Contains(lower, "active") ||
		lower == "up" || lower == "200":
		return "ok"
	case strings.Contains(lower, "info") ||
		strings.Contains(lower, "unknown"):
		return "info"
	}
	return ""
}

// inferSummaryFromData 从返回数据中自动生成摘要。
// 当 summary_template 为空时使用，让零配置能力也有可读摘要。
func inferSummaryFromData(data map[string]any, resourceType string) string {
	if len(data) == 0 {
		return "查询完成，未返回数据"
	}
	// 优先找 name/status 组合
	nameVal := ""
	statusVal := ""
	for key, val := range data {
		lowerKey := strings.ToLower(key)
		if s, ok := scalarString(val); ok {
			if (lowerKey == "name" || strings.HasSuffix(lowerKey, ".name")) && nameVal == "" {
				nameVal = s
			}
			if (lowerKey == "status" || lowerKey == "state" || lowerKey == "health") && statusVal == "" {
				statusVal = s
			}
		}
	}
	if statusVal != "" {
		if nameVal != "" {
			return resourceType + " " + nameVal + " 状态为 " + statusVal
		}
		return resourceType + " 状态为 " + statusVal
	}
	if nameVal != "" {
		return resourceType + " " + nameVal + " 查询完成"
	}
	// 找第一个高价值字段作为摘要
	highValueKeys := []string{
		"total", "count", "size", "usage", "result", "message",
	}
	for _, key := range highValueKeys {
		if val, ok := data[key]; ok {
			if s, ok := scalarString(val); ok {
				return key + ": " + s
			}
		}
	}
	return resourceType + " 查询完成，返回 " + string(rune('0'+len(data))) + " 个字段"
}
