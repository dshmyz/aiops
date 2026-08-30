package audit_test

import (
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
)

func TestParseSearchQueryEmptyReturnsEmptyFilter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	filter := audit.ParseSearchQuery("", now)
	if filter.ToolName != "" || filter.Action != "" || filter.Decision != "" || filter.Subject != "" {
		t.Fatalf("filter = %+v, want empty", filter)
	}
}

func TestParseSearchQueryMapsRejectedToDeniedDecision(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)

	cases := map[string]string{
		"上周谁拒绝了 plan":           "denied",
		"who rejected the plan": "denied",
		"拒绝":                    "denied",
		"denied":                "denied",
	}
	for query, wantDecision := range cases {
		query, wantDecision := query, wantDecision
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			filter := audit.ParseSearchQuery(query, now)
			if filter.Decision != wantDecision {
				t.Fatalf("decision = %q, want %q", filter.Decision, wantDecision)
			}
		})
	}
}

func TestParseSearchQueryMapsPermittedToPermittedDecision(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)

	cases := map[string]string{
		"上周谁允许了 plan":            "permitted",
		"who permitted the plan": "permitted",
		"允许":                     "permitted",
		"permitted":              "permitted",
	}
	for query, wantDecision := range cases {
		query, wantDecision := query, wantDecision
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			filter := audit.ParseSearchQuery(query, now)
			if filter.Decision != wantDecision {
				t.Fatalf("decision = %q, want %q", filter.Decision, wantDecision)
			}
		})
	}
}

func TestParseSearchQueryExtractsSubject(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)

	cases := map[string]string{
		"admin-1 上周拒绝了 plan": "admin-1",
		"admin-1 rejected":   "admin-1",
	}
	for query, wantSubject := range cases {
		query, wantSubject := query, wantSubject
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			filter := audit.ParseSearchQuery(query, now)
			if filter.Subject != wantSubject {
				t.Fatalf("subject = %q, want %q", filter.Subject, wantSubject)
			}
		})
	}
}

func TestParseSearchQueryMapsTimeWordsToCreatedAfter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)

	y, m, d := now.Date()
	todayMidnight := time.Date(y, m, d, 0, 0, 0, 0, now.Location())

	cases := map[string]time.Time{
		"上周谁拒绝了 plan":        now.Add(-7 * 24 * time.Hour),
		"last week rejected": now.Add(-7 * 24 * time.Hour),
		"昨天":                 now.Add(-24 * time.Hour),
		"yesterday":          now.Add(-24 * time.Hour),
		"今天":                 todayMidnight,
		"today":              todayMidnight,
	}
	for query, want := range cases {
		query, want := query, want
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			filter := audit.ParseSearchQuery(query, now)
			if !filter.CreatedAfter.Equal(want) {
				t.Fatalf("created_after = %v, want %v", filter.CreatedAfter, want)
			}
		})
	}
}

func TestParseSearchQuerySetsDefaultPageSize(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)

	filter := audit.ParseSearchQuery("上周谁拒绝了 plan", now)
	if filter.Limit <= 0 {
		t.Fatalf("limit = %d, want a positive default page size", filter.Limit)
	}
}

// TestParseSearchQueryFinalResultKeyword 验证借鉴-4: 自然语言搜索"最终结果"/
// "final result" 触发 FinalResultOnly，隐藏驳回/未执行事件。
func TestParseSearchQueryFinalResultKeyword(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)

	cases := map[string]bool{
		"最终结果":               true,
		"只看最终结果":             true,
		"final result":       true,
		"final results only": true,
		"上周的执行记录":            false, // 不含关键词，不触发
		"rejected":           false, // 仅 decision 过滤，不触发 final result
	}
	for query, want := range cases {
		query, want := query, want
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			filter := audit.ParseSearchQuery(query, now)
			if filter.FinalResultOnly != want {
				t.Fatalf("FinalResultOnly = %v, want %v for query %q", filter.FinalResultOnly, want, query)
			}
		})
	}
}
