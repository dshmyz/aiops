package eval

import (
	"errors"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
)

// TestReportMarkdownStructure asserts the report contains the expected
// sections: header, category table, and failures section.
func TestReportMarkdownStructure(t *testing.T) {
	t.Parallel()
	report := Report{
		Total:  2,
		Passed: 1,
		Failed: 1,
		ByCategory: map[Category]CategoryStat{
			CategoryTool:          {Total: 2, Passed: 1, Failed: 1, Rate: 0.5},
			CategoryClarification: {Total: 0},
			CategoryDiagnostic:    {Total: 0},
		},
		Failures: []Failure{
			{
				CaseName:  "tool/sample",
				Category:  CategoryTool,
				Reason:    "tool name: got \"wrong\", want \"right\"",
				Expected:  ExpectedIntent{ToolName: "right"},
				Actual:    assistant.Intent{ToolName: "wrong"},
				ActualErr: nil,
			},
		},
	}

	md := report.Markdown()

	// Header sections.
	if !strings.Contains(md, "# EinoPlanner Evaluation Report") {
		t.Errorf("markdown missing title header")
	}
	if !strings.Contains(md, "Total: 2 | Pass: 1 | Fail: 1") {
		t.Errorf("markdown missing totals: %s", md)
	}

	// Category table.
	if !strings.Contains(md, "## By Category") {
		t.Errorf("markdown missing category section")
	}
	if !strings.Contains(md, "| tool ") || !strings.Contains(md, "100%") {
		t.Errorf("markdown missing tool category threshold (100%%): %s", md)
	}
	if !strings.Contains(md, "❌") {
		t.Errorf("markdown missing failure marker ❌ (tool rate 50%% < 100%%)")
	}

	// Failures section.
	if !strings.Contains(md, "## Failures") {
		t.Errorf("markdown missing failures section")
	}
	if !strings.Contains(md, "### tool/sample") {
		t.Errorf("markdown missing failure case name")
	}
	if !strings.Contains(md, "tool name: got") {
		t.Errorf("markdown missing failure reason")
	}
}

// TestReportMarkdownNoFailures asserts the markdown renders an empty
// failures section when there are no failures.
func TestReportMarkdownNoFailures(t *testing.T) {
	t.Parallel()
	report := Report{
		Total:  3,
		Passed: 3,
		Failed: 0,
		ByCategory: map[Category]CategoryStat{
			CategoryTool: {Total: 3, Passed: 3, Failed: 0, Rate: 1.0},
		},
	}

	md := report.Markdown()

	if !strings.Contains(md, "_No failures._") {
		t.Errorf("markdown missing empty-failures placeholder: %s", md)
	}
	if strings.Contains(md, "❌") {
		t.Errorf("markdown should not contain failure marker when all pass")
	}
}

// TestReportMarkdownClarificationFailure verifies the failure block format for
// a clarification-path mismatch.
func TestReportMarkdownClarificationFailure(t *testing.T) {
	t.Parallel()
	report := Report{
		Total:  1,
		Passed: 0,
		Failed: 1,
		ByCategory: map[Category]CategoryStat{
			CategoryClarification: {Total: 1, Passed: 0, Failed: 1, Rate: 0.0},
		},
		Failures: []Failure{
			{
				CaseName:  "clarification/sample",
				Category:  CategoryClarification,
				Reason:    "expected clarification error, got ToolName=cluster.status.read",
				Expected:  ExpectedIntent{Clarification: true},
				Actual:    assistant.Intent{ToolName: "cluster.status.read"},
				ActualErr: nil,
			},
		},
	}

	md := report.Markdown()

	if !strings.Contains(md, "clarification needed") {
		t.Errorf("markdown missing expected description: %s", md)
	}
	if !strings.Contains(md, "expected clarification error") {
		t.Errorf("markdown missing reason: %s", md)
	}
}

// TestReportMarkdownDiagnosticFailure verifies the failure block format for
// a diagnostic-path mismatch.
func TestReportMarkdownDiagnosticFailure(t *testing.T) {
	t.Parallel()
	report := Report{
		Total:  1,
		Passed: 0,
		Failed: 1,
		ByCategory: map[Category]CategoryStat{
			CategoryDiagnostic: {Total: 1, Passed: 0, Failed: 1, Rate: 0.0},
		},
		Failures: []Failure{
			{
				CaseName:  "diagnostic/sample",
				Category:  CategoryDiagnostic,
				Reason:    "expected diagnostic intent, got ToolName=\"cluster.status.read\"",
				Expected:  ExpectedIntent{Diagnostic: true},
				Actual:    assistant.Intent{ToolName: "cluster.status.read"},
				ActualErr: nil,
			},
		},
	}

	md := report.Markdown()

	if !strings.Contains(md, "diagnostic intent") {
		t.Errorf("markdown missing expected description: %s", md)
	}
}

// TestReportMarkdownPlannerError verifies the failure block format for
// unexpected planner errors.
func TestReportMarkdownPlannerError(t *testing.T) {
	t.Parallel()
	report := Report{
		Total:  1,
		Passed: 0,
		Failed: 1,
		ByCategory: map[Category]CategoryStat{
			CategoryTool: {Total: 1, Passed: 0, Failed: 1, Rate: 0.0},
		},
		Failures: []Failure{
			{
				CaseName:  "tool/sample",
				Category:  CategoryTool,
				Reason:    "planner error: internal explosion",
				Expected:  ExpectedIntent{ToolName: "cluster.status.read"},
				Actual:    assistant.Intent{},
				ActualErr: errors.New("internal explosion"),
			},
		},
	}

	md := report.Markdown()

	if !strings.Contains(md, "error: internal explosion") {
		t.Errorf("markdown missing error description: %s", md)
	}
}

// TestReportMarkdownDiagnosticActual verifies the actual description for a
// diagnostic intent.
func TestReportMarkdownDiagnosticActual(t *testing.T) {
	t.Parallel()
	report := Report{
		Failures: []Failure{
			{
				CaseName: "diagnostic/sample",
				Category: CategoryTool,
				Reason:   "tool name mismatch",
				Expected: ExpectedIntent{ToolName: "expected.tool"},
				Actual: assistant.Intent{
					Diagnostic: &diagnostics.Request{Domain: "kafka", Environment: "prod"},
				},
			},
		},
	}

	md := report.Markdown()

	if !strings.Contains(md, "diagnostic intent (domain=kafka)") {
		t.Errorf("markdown missing actual diagnostic description: %s", md)
	}
}
