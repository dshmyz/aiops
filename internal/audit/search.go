package audit

import (
	"regexp"
	"strings"
	"time"
)

// defaultSearchPageSize is the page size used when no explicit limit is
// derived from the query. Audit search results are interactive — callers want
// to scan the page quickly, not scroll through thousands of rows.
const defaultSearchPageSize = 50

var (
	// subjectPattern matches tokens that look like operator/admin identities:
	// alnum, dash, dot, underscore, with at least one dash or dot so we don't
	// accidentally grab common English words.
	subjectPattern = regexp.MustCompile(`\b[a-zA-Z0-9._-]+\.[a-zA-Z0-9._-]+\b|\b[a-zA-Z0-9]+-[a-zA-Z0-9-]+\b`)
)

// ParseSearchQuery translates a natural-language audit search query into an
// AuditFilter. It understands a small, deliberately-tiny set of keywords:
//   - decision: 拒绝/rejected/denied, 允许/permitted
//   - time window: 上周/last week, 昨天/yesterday, 今天/today
//   - subject: any token matching the operator/admin identity shape
//
// Unknown tokens are ignored rather than producing a wrong filter. The
// returned filter always carries a default page size so the route handler
// doesn't need to special-case empty limits.
func ParseSearchQuery(query string, now time.Time) Filter {
	query = strings.ToLower(strings.TrimSpace(query))
	filter := Filter{Limit: defaultSearchPageSize}
	if query == "" {
		return filter
	}

	// Decision. "拒绝" and "rejected"/"denied" map to denied; "允许" and
	// "permitted"/"allowed" map to permitted. We check denied first because
	// "denied" is the more common negative lookup and a substring check on
	// "permitted" would not collide with it.
	switch {
	case containsAny(query, "拒绝", "rejected", "denied", "reject"):
		filter.Decision = DecisionDenied
	case containsAny(query, "允许", "permitted", "allowed", "allow"):
		filter.Decision = DecisionPermitted
	}

	// Time window. "上周/last week" → last 7 days; "昨天/yesterday" → last
	// 24h; "今天/today" → since midnight today. Keep this minimal — anything
	// more precise should go through the explicit after/before params.
	switch {
	case containsAny(query, "上周", "last week", "past week", "过去一周"):
		filter.CreatedAfter = now.Add(-7 * 24 * time.Hour)
	case containsAny(query, "昨天", "yesterday"):
		filter.CreatedAfter = now.Add(-24 * time.Hour)
	case containsAny(query, "今天", "today"):
		y, m, d := now.Date()
		filter.CreatedAfter = time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	}

	// Subject. Scan for identity-shaped tokens; take the first match.
	if match := subjectPattern.FindString(query); match != "" {
		filter.Subject = match
	}

	// 借鉴-4: "最终结果"/"final result" 触发 FinalResultOnly，隐藏驳回/未执行
	// 事件，让事件中心复盘聚焦在真正发生的结果上。
	if containsAny(query, "最终结果", "final result", "final results") {
		filter.FinalResultOnly = true
	}

	return filter
}

// containsAny reports whether s contains any of the substrings. Matching is
// case-insensitive because the query was already lowercased by ParseSearchQuery
// and the candidates here are pre-lowercased.
func containsAny(s string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(s, candidate) {
			return true
		}
	}
	return false
}
