package eval

import (
	"context"
	"errors"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

// planByMessage routes the call based on the user message, which the runner
// forwards verbatim. This indirection keeps the stub testable without ctx wiring.
type planByMessage struct {
	intents map[string]assistant.Intent
	errors  map[string]error
}

func (p *planByMessage) Plan(_ context.Context, _ identity.CurrentUser, message string, _ []assistant.Turn, _ assistant.PageContext) (assistant.Intent, error) {
	if err, ok := p.errors[message]; ok {
		return assistant.Intent{}, err
	}
	if intent, ok := p.intents[message]; ok {
		return intent, nil
	}
	return assistant.Intent{}, errors.New("fake planner: no scripted outcome for message")
}

// TestRunnerPassesWhenIntentMatches asserts the happy path: three cases
// whose scripted Intents match the ExpectedIntent all pass.
func TestRunnerPassesWhenIntentMatches(t *testing.T) {
	t.Parallel()
	planner := &planByMessage{
		intents: map[string]assistant.Intent{
			"check prod cluster":     {ToolName: "cluster.status.read", Input: map[string]any{"environment": "prod"}},
			"check staging cluster":  {ToolName: "cluster.status.read", Input: map[string]any{"environment": "staging"}},
			"check kafka orders lag": {ToolName: "kafka.consumer_group.lag.read", Input: map[string]any{"environment": "prod", "group": "orders"}},
		},
	}
	cases := []Case{
		{Name: "tool/a", Category: CategoryTool, UserMessage: "check prod cluster", ExpectedIntent: ExpectedIntent{ToolName: "cluster.status.read", Input: map[string]any{"environment": "prod"}}},
		{Name: "tool/b", Category: CategoryTool, UserMessage: "check staging cluster", ExpectedIntent: ExpectedIntent{ToolName: "cluster.status.read", Input: map[string]any{"environment": "staging"}}},
		{Name: "tool/c", Category: CategoryTool, UserMessage: "check kafka orders lag", ExpectedIntent: ExpectedIntent{ToolName: "kafka.consumer_group.lag.read", Input: map[string]any{"environment": "prod", "group": "orders"}}},
	}

	report := Run(context.Background(), planner, cases)

	if report.Total != 3 || report.Passed != 3 || report.Failed != 0 {
		t.Fatalf("report = %+v, want 3/3 pass", report)
	}
	if len(report.Failures) != 0 {
		t.Fatalf("failures = %v, want none", report.Failures)
	}
}

// TestRunnerFailsWhenToolNameMismatch asserts that a wrong ToolName produces
// a Failure with a reason mentioning the mismatch.
func TestRunnerFailsWhenToolNameMismatch(t *testing.T) {
	t.Parallel()
	planner := &planByMessage{
		intents: map[string]assistant.Intent{
			"msg": {ToolName: "wrong.tool", Input: map[string]any{}},
		},
	}
	cases := []Case{{
		Name:        "tool/mismatch",
		Category:    CategoryTool,
		UserMessage: "msg",
		ExpectedIntent: ExpectedIntent{
			ToolName: "cluster.status.read",
		},
	}}

	report := Run(context.Background(), planner, cases)

	if report.Passed != 0 || report.Failed != 1 {
		t.Fatalf("report = %+v, want 0 pass / 1 fail", report)
	}
	if len(report.Failures) != 1 {
		t.Fatalf("failures len = %d, want 1", len(report.Failures))
	}
	f := report.Failures[0]
	if f.CaseName != "tool/mismatch" {
		t.Fatalf("failure case = %q, want tool/mismatch", f.CaseName)
	}
	if f.Reason == "" {
		t.Fatalf("failure reason is empty")
	}
}

// TestRunnerPartialInputMatchIgnoresExtraKeys asserts that extra keys in
// the actual Intent.Input do not cause a failure. Only keys declared in
// ExpectedIntent.Input are checked.
func TestRunnerPartialInputMatchIgnoresExtraKeys(t *testing.T) {
	t.Parallel()
	planner := &planByMessage{
		intents: map[string]assistant.Intent{
			"msg": {
				ToolName: "cluster.status.read",
				Input:    map[string]any{"environment": "prod", "cluster": "extra", "unused": 42},
			},
		},
	}
	cases := []Case{{
		Name:        "tool/extra_keys",
		Category:    CategoryTool,
		UserMessage: "msg",
		ExpectedIntent: ExpectedIntent{
			ToolName: "cluster.status.read",
			Input:    map[string]any{"environment": "prod"},
		},
	}}

	report := Run(context.Background(), planner, cases)

	if report.Passed != 1 || report.Failed != 0 {
		t.Fatalf("report = %+v, want 1 pass / 0 fail (extra keys should be ignored)", report)
	}
}

// TestRunnerPartialInputMatchFailsOnMissingKey asserts that a missing key in
// the actual Intent.Input causes a failure.
func TestRunnerPartialInputMatchFailsOnMissingKey(t *testing.T) {
	t.Parallel()
	planner := &planByMessage{
		intents: map[string]assistant.Intent{
			"msg": {
				ToolName: "cluster.status.read",
				Input:    map[string]any{"environment": "prod"}, // missing "cluster"
			},
		},
	}
	cases := []Case{{
		Name:        "tool/missing_key",
		Category:    CategoryTool,
		UserMessage: "msg",
		ExpectedIntent: ExpectedIntent{
			ToolName: "cluster.status.read",
			Input:    map[string]any{"environment": "prod", "cluster": "main"},
		},
	}}

	report := Run(context.Background(), planner, cases)

	if report.Passed != 0 || report.Failed != 1 {
		t.Fatalf("report = %+v, want 0 pass / 1 fail (missing key should fail)", report)
	}
}

// TestRunnerPartialInputMatchFailsOnWrongValue asserts that a wrong value for
// a declared key causes a failure.
func TestRunnerPartialInputMatchFailsOnWrongValue(t *testing.T) {
	t.Parallel()
	planner := &planByMessage{
		intents: map[string]assistant.Intent{
			"msg": {
				ToolName: "cluster.status.read",
				Input:    map[string]any{"environment": "staging"}, // wrong: expected prod
			},
		},
	}
	cases := []Case{{
		Name:        "tool/wrong_value",
		Category:    CategoryTool,
		UserMessage: "msg",
		ExpectedIntent: ExpectedIntent{
			ToolName: "cluster.status.read",
			Input:    map[string]any{"environment": "prod"},
		},
	}}

	report := Run(context.Background(), planner, cases)

	if report.Passed != 0 || report.Failed != 1 {
		t.Fatalf("report = %+v, want 0 pass / 1 fail (wrong value should fail)", report)
	}
}

// TestRunnerClarificationPath asserts that when ExpectedIntent.Clarification
// is true, the runner checks that planner returned ErrClarificationNeeded. A
// non-error Intent with a ToolName should fail.
func TestRunnerClarificationPath(t *testing.T) {
	t.Parallel()
	// Sub-case A: planner returns ErrClarificationNeeded → pass.
	plannerPass := &planByMessage{
		errors: map[string]error{"need env": assistant.ErrClarificationNeeded},
	}
	casesPass := []Case{{
		Name:           "clarification/pass",
		Category:       CategoryClarification,
		UserMessage:    "need env",
		ExpectedIntent: ExpectedIntent{Clarification: true},
	}}
	reportPass := Run(context.Background(), plannerPass, casesPass)
	if reportPass.Passed != 1 {
		t.Fatalf("sub-case A report = %+v, want 1 pass", reportPass)
	}

	// Sub-case B: planner returns a concrete Intent → fail.
	plannerFail := &planByMessage{
		intents: map[string]assistant.Intent{
			"need env": {ToolName: "cluster.status.read", Input: map[string]any{}},
		},
	}
	casesFail := []Case{{
		Name:           "clarification/fail",
		Category:       CategoryClarification,
		UserMessage:    "need env",
		ExpectedIntent: ExpectedIntent{Clarification: true},
	}}
	reportFail := Run(context.Background(), plannerFail, casesFail)
	if reportFail.Failed != 1 {
		t.Fatalf("sub-case B report = %+v, want 1 fail", reportFail)
	}
}

// TestRunnerDiagnosticPath asserts that when ExpectedIntent.Diagnostic is true,
// the runner checks that intent.Diagnostic is non-nil. A tool Intent should
// fail.
func TestRunnerDiagnosticPath(t *testing.T) {
	t.Parallel()
	// Sub-case A: planner returns a diagnostic Intent → pass.
	plannerPass := &planByMessage{
		intents: map[string]assistant.Intent{
			"check health": {Diagnostic: &diagnostics.Request{Domain: "glusterfs", Environment: "prod"}},
		},
	}
	casesPass := []Case{{
		Name:           "diagnostic/pass",
		Category:       CategoryDiagnostic,
		UserMessage:    "check health",
		ExpectedIntent: ExpectedIntent{Diagnostic: true},
	}}
	reportPass := Run(context.Background(), plannerPass, casesPass)
	if reportPass.Passed != 1 {
		t.Fatalf("sub-case A report = %+v, want 1 pass", reportPass)
	}

	// Sub-case B: planner returns a tool Intent → fail.
	plannerFail := &planByMessage{
		intents: map[string]assistant.Intent{
			"check health": {ToolName: "cluster.status.read", Input: map[string]any{}},
		},
	}
	casesFail := []Case{{
		Name:           "diagnostic/fail",
		Category:       CategoryDiagnostic,
		UserMessage:    "check health",
		ExpectedIntent: ExpectedIntent{Diagnostic: true},
	}}
	reportFail := Run(context.Background(), plannerFail, casesFail)
	if reportFail.Failed != 1 {
		t.Fatalf("sub-case B report = %+v, want 1 fail", reportFail)
	}
}

// TestRunnerPlannerErrorFails asserts that an unexpected planner error (not
// ErrClarificationNeeded) fails the case with a reason referencing the error.
func TestRunnerPlannerErrorFails(t *testing.T) {
	t.Parallel()
	planner := &planByMessage{
		errors: map[string]error{"boom": errors.New("internal explosion")},
	}
	cases := []Case{{
		Name:        "tool/error",
		Category:    CategoryTool,
		UserMessage: "boom",
		ExpectedIntent: ExpectedIntent{
			ToolName: "cluster.status.read",
		},
	}}

	report := Run(context.Background(), planner, cases)

	if report.Failed != 1 {
		t.Fatalf("report = %+v, want 1 fail", report)
	}
	if report.Failures[0].Reason == "" {
		t.Fatalf("failure reason is empty")
	}
}

// TestRunnerCategoryStats asserts that ByCategory is populated correctly:
// each category has Total/Passed/Failed/Rate computed.
func TestRunnerCategoryStats(t *testing.T) {
	t.Parallel()
	planner := &planByMessage{
		intents: map[string]assistant.Intent{
			"pass1": {ToolName: "t1", Input: map[string]any{}},
			"pass2": {ToolName: "t2", Input: map[string]any{}},
			"fail1": {ToolName: "wrong", Input: map[string]any{}},
		},
	}
	cases := []Case{
		{Name: "tool/pass1", Category: CategoryTool, UserMessage: "pass1", ExpectedIntent: ExpectedIntent{ToolName: "t1"}},
		{Name: "tool/pass2", Category: CategoryTool, UserMessage: "pass2", ExpectedIntent: ExpectedIntent{ToolName: "t2"}},
		{Name: "tool/fail1", Category: CategoryTool, UserMessage: "fail1", ExpectedIntent: ExpectedIntent{ToolName: "expected"}},
		{Name: "clar/pass", Category: CategoryClarification, UserMessage: "need env", ExpectedIntent: ExpectedIntent{Clarification: true}},
	}
	// Add the error for clar/pass
	planner.errors = map[string]error{"need env": assistant.ErrClarificationNeeded}

	report := Run(context.Background(), planner, cases)

	toolStat := report.ByCategory[CategoryTool]
	if toolStat.Total != 3 || toolStat.Passed != 2 || toolStat.Failed != 1 {
		t.Fatalf("tool stat = %+v, want 3/2/1", toolStat)
	}
	if toolStat.Rate != 2.0/3.0 {
		t.Fatalf("tool rate = %v, want %v", toolStat.Rate, 2.0/3.0)
	}
	clarStat := report.ByCategory[CategoryClarification]
	if clarStat.Total != 1 || clarStat.Passed != 1 || clarStat.Failed != 0 {
		t.Fatalf("clarification stat = %+v, want 1/1/0", clarStat)
	}
}
