package eval

import (
	"fmt"
	"strings"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
)

// Report aggregates evaluation outcomes.
type Report struct {
	Total      int
	Passed     int
	Failed     int
	ByCategory map[Category]CategoryStat
	Failures   []Failure
	Duration   time.Duration
}

// CategoryStat holds per-category totals and pass rate.
type CategoryStat struct {
	Total  int
	Passed int
	Failed int
	Rate   float64
}

// Failure records one failing case with the expected vs actual outcome.
type Failure struct {
	CaseName  string
	Category  Category
	Reason    string
	Expected  ExpectedIntent
	Actual    assistant.Intent
	ActualErr error
}

// Markdown returns a human-readable markdown report.
//
// Layout:
//   - Header with timestamp, duration, totals
//   - Per-category table with threshold markers
//   - Failures section with one block per failure
func (r Report) Markdown() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# EinoPlanner Evaluation Report\n\n")
	fmt.Fprintf(&sb, "Generated: %s\n", time.Now().Format("2006-01-02 15:04"))
	fmt.Fprintf(&sb, "Duration: %s\n", r.Duration.Truncate(time.Millisecond))
	fmt.Fprintf(&sb, "Total: %d | Pass: %d | Fail: %d\n\n", r.Total, r.Passed, r.Failed)

	// Category table.
	sb.WriteString("## By Category\n\n")
	sb.WriteString("| Category       | Total | Pass | Fail | Rate   | Threshold | Status |\n")
	sb.WriteString("|----------------|-------|------|------|--------|-----------|--------|\n")
	r.writeCategoryRow(&sb, CategoryTool, 1.0)
	r.writeCategoryRow(&sb, CategoryClarification, 0.9)
	r.writeCategoryRow(&sb, CategoryDiagnostic, 0.9)
	r.writeCategoryRow(&sb, CategoryHistory, 0.9)
	sb.WriteString("\n")

	// Failures.
	if len(r.Failures) == 0 {
		sb.WriteString("## Failures\n\n_No failures._\n")
	} else {
		sb.WriteString("## Failures\n\n")
		for _, f := range r.Failures {
			fmt.Fprintf(&sb, "### %s\n\n", f.CaseName)
			fmt.Fprintf(&sb, "- **Category**: %s\n", f.Category)
			fmt.Fprintf(&sb, "- **Reason**: %s\n", f.Reason)
			fmt.Fprintf(&sb, "- **Expected**: %s\n", describeExpected(f.Expected))
			fmt.Fprintf(&sb, "- **Actual**: %s\n", describeActual(f.Actual, f.ActualErr))
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// writeCategoryRow emits one table row. Threshold is the minimum required
// rate; status is ❌ when below threshold.
func (r Report) writeCategoryRow(sb *strings.Builder, cat Category, threshold float64) {
	stat := r.ByCategory[cat]
	if stat.Total == 0 {
		return
	}
	status := "✅"
	if stat.Rate < threshold {
		status = "❌"
	}
	fmt.Fprintf(sb, "| %-14s | %5d | %4d | %4d | %5.1f%% | %7d%% | %s |\n",
		cat, stat.Total, stat.Passed, stat.Failed, stat.Rate*100, int(threshold*100), status)
}

func describeExpected(e ExpectedIntent) string {
	if e.Clarification {
		return "clarification needed"
	}
	if e.Diagnostic {
		return "diagnostic intent"
	}
	parts := []string{fmt.Sprintf("tool=%q", e.ToolName)}
	if len(e.Input) > 0 {
		parts = append(parts, fmt.Sprintf("input=%v", e.Input))
	}
	return strings.Join(parts, " ")
}

func describeActual(intent assistant.Intent, err error) string {
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if intent.Diagnostic != nil {
		return fmt.Sprintf("diagnostic intent (domain=%s)", intent.Diagnostic.Domain)
	}
	parts := []string{fmt.Sprintf("tool=%q", intent.ToolName)}
	if len(intent.Input) > 0 {
		parts = append(parts, fmt.Sprintf("input=%v", intent.Input))
	}
	return strings.Join(parts, " ")
}
