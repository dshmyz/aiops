//go:build eval

// This file is compiled only when the `eval` build tag is set:
//
//	go test -tags=eval ./internal/assistant/eval/...
//
// It runs the full evaluation suite against EinoPlanner + ScriptedProvider
// and asserts the per-category thresholds:
//   - tool:          100%
//   - clarification: ≥90%
//   - diagnostic:    ≥90%
//   - history:       ≥90%
//
// After the suite finishes, TestMain writes report.md to the package
// directory so the user can inspect detailed outcomes.
package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
)

// TestMain runs the eval test suite and then writes report.md to the package
// directory. The report is regenerated on every `go test -tags=eval` run.
func TestMain(m *testing.M) {
	code := m.Run()
	writeReportMarkdown()
	os.Exit(code)
}

// writeReportMarkdown runs all cases once more (cheap: ~100 planner calls
// with a scripted provider, no network) and writes the markdown report to
// report.md in the package directory.
func writeReportMarkdown() {
	provider := NewScriptedProvider(Cases)
	planner := assistant.NewEinoPlanner(provider)
	report := Run(context.Background(), planner, Cases)

	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "eval: getwd:", err)
		return
	}
	path := filepath.Join(dir, "report.md")
	if err := os.WriteFile(path, []byte(report.Markdown()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "eval: write report.md:", err)
		return
	}
	fmt.Println("eval: report written to", path)
}

// TestEvalSuiteRunAllCases runs all 100 cases through EinoPlanner with the
// ScriptedProvider and asserts no case silently failed to wire (no
// "no response registered" errors from the provider).
//
// This is a smoke test that the suite plumbing works; the threshold checks
// are in the per-category tests below.
func TestEvalSuiteRunAllCases(t *testing.T) {
	t.Parallel()
	report := runEvalSuite(t)

	if report.Total != len(Cases) {
		t.Fatalf("report Total = %d, want %d (some cases were not executed)", report.Total, len(Cases))
	}

	// Failures whose reason mentions "no response registered" indicate a
	// wiring bug: the case name was not propagated to ScriptedProvider.
	for _, f := range report.Failures {
		if contains(f.Reason, "no response registered") {
			t.Errorf("wiring bug for case %q: %s", f.CaseName, f.Reason)
		}
	}
}

// TestEvalSuiteCoreTool100Percent asserts the tool category achieves 100%
// pass rate. Tool intent resolution is the planner's primary job; any failure
// here is a regression.
func TestEvalSuiteCoreTool100Percent(t *testing.T) {
	t.Parallel()
	report := runEvalSuite(t)
	stat := report.ByCategory[CategoryTool]
	if stat.Rate != 1.0 {
		t.Fatalf("tool category rate = %.1f%%, want 100%%. Failures:\n%s",
			stat.Rate*100, formatFailuresForCategory(report.Failures, CategoryTool))
	}
}

// TestEvalSuiteClarificationAtLeast90Percent asserts the clarification
// category achieves at least 90% pass rate. Clarification detection is
// secondary but should rarely fail.
func TestEvalSuiteClarificationAtLeast90Percent(t *testing.T) {
	t.Parallel()
	report := runEvalSuite(t)
	stat := report.ByCategory[CategoryClarification]
	const threshold = 0.9
	if stat.Rate < threshold {
		t.Fatalf("clarification category rate = %.1f%%, want ≥%.0f%%. Failures:\n%s",
			stat.Rate*100, threshold*100, formatFailuresForCategory(report.Failures, CategoryClarification))
	}
}

// TestEvalSuiteDiagnosticAtLeast90Percent asserts the diagnostic category
// achieves at least 90% pass rate. Diagnostic routing is secondary.
func TestEvalSuiteDiagnosticAtLeast90Percent(t *testing.T) {
	t.Parallel()
	report := runEvalSuite(t)
	stat := report.ByCategory[CategoryDiagnostic]
	const threshold = 0.9
	if stat.Rate < threshold {
		t.Fatalf("diagnostic category rate = %.1f%%, want ≥%.0f%%. Failures:\n%s",
			stat.Rate*100, threshold*100, formatFailuresForCategory(report.Failures, CategoryDiagnostic))
	}
}

// TestEvalSuiteHistoryAtLeast90Percent asserts the history category achieves
// at least 90% pass rate. Multi-turn reference resolution is secondary and
// depends on the structured [Last Intent] block being populated correctly.
func TestEvalSuiteHistoryAtLeast90Percent(t *testing.T) {
	t.Parallel()
	report := runEvalSuite(t)
	stat := report.ByCategory[CategoryHistory]
	const threshold = 0.9
	if stat.Rate < threshold {
		t.Fatalf("history category rate = %.1f%%, want ≥%.0f%%. Failures:\n%s",
			stat.Rate*100, threshold*100, formatFailuresForCategory(report.Failures, CategoryHistory))
	}
}

// TestEvalSuiteWritesReportMarkdown asserts that the markdown report is
// written to report.md in the package directory, so the user can inspect
// detailed outcomes after a run.
func TestEvalSuiteWritesReportMarkdown(t *testing.T) {
	t.Parallel()
	report := runEvalSuite(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "report.md")
	if err := os.WriteFile(path, []byte(report.Markdown()), 0o644); err != nil {
		t.Fatalf("write report.md: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat report.md: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("report.md is empty")
	}
}

// runEvalSuite builds a ScriptedProvider + EinoPlanner and runs all cases.
// The build is cached per-test to keep the suite fast.
func runEvalSuite(t *testing.T) Report {
	t.Helper()
	provider := NewScriptedProvider(Cases)
	planner := assistant.NewEinoPlanner(provider)
	return Run(context.Background(), planner, Cases)
}

// formatFailuresForCategory renders the failures in a given category as a
// readable list, used in test failure messages.
func formatFailuresForCategory(failures []Failure, cat Category) string {
	var sb []byte
	for _, f := range failures {
		if f.Category != cat {
			continue
		}
		sb = append(sb, "  - "...)
		sb = append(sb, f.CaseName...)
		sb = append(sb, ": "...)
		sb = append(sb, f.Reason...)
		sb = append(sb, '\n')
	}
	if len(sb) == 0 {
		return "  (none)"
	}
	return string(sb)
}

// contains is a tiny strings.Contains replacement to avoid importing strings
// in this build-tagged file (keeps imports minimal).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || indexOf(s, substr) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
