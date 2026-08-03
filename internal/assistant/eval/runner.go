package eval

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

// Run executes cases against planner and returns a Report.
//
// For each case, Run injects the case name into ctx so ScriptedProvider can
// look up the scripted LLM response. The planner is called with the case's
// UserMessage and History, and the resulting Intent is compared against
// ExpectedIntent via judge().
//
// Run does not execute tools, create action plans, or write audit records.
// The governance loop is untouched: evaluation stops at the Intent layer.
func Run(ctx context.Context, planner assistant.Planner, cases []Case) Report {
	start := time.Now()
	r := newReport()
	for _, c := range cases {
		caseCtx := withCaseName(ctx, c.Name)
		intent, err := planner.Plan(caseCtx, evalUser(), c.UserMessage, c.History, assistant.PageContext{})
		r.record(c, intent, err)
	}
	r.Duration = time.Since(start)
	return r.finalize()
}

// evalUser returns the identity used for evaluation. The planner does not
// consult identity for routing decisions today; the field is required only
// to satisfy the Planner interface signature.
func evalUser() identity.CurrentUser {
	return identity.CurrentUser{
		Subject:             "eval-user",
		Roles:               []string{"admin"},
		AllowedEnvironments: []string{"prod", "staging", "dev"},
		RequestID:           "eval-request",
	}
}

// judge compares the actual (intent, err) against the expected outcome and
// returns ("", true) on pass or (reason, false) on fail.
//
// Judgment order (short-circuit on first failure):
//  1. Clarification path: expect errors.Is(err, ErrClarificationNeeded)
//  2. Diagnostic path: expect err == nil && intent.Diagnostic != nil
//  3. Tool path: expect err == nil && intent.Diagnostic == nil && ToolName matches
//  4. Input partial match (only when ExpectedIntent.Input is non-empty)
//  5. Selection check (only when ExpectedIntent.Selection is non-nil)
func judge(c Case, intent assistant.Intent, err error) (string, bool) {
	expected := c.ExpectedIntent

	// 1. Clarification path.
	if expected.Clarification {
		if !errors.Is(err, assistant.ErrClarificationNeeded) {
			return fmt.Sprintf("expected clarification error, got err=%v intent=%+v", err, intent), false
		}
		return "", true
	}

	// 2. Planner error is unexpected for non-clarification paths.
	if err != nil {
		return fmt.Sprintf("planner error: %v", err), false
	}

	// 3. Diagnostic path.
	if expected.Diagnostic {
		if intent.Diagnostic == nil {
			return fmt.Sprintf("expected diagnostic intent, got ToolName=%q", intent.ToolName), false
		}
		return "", true
	}

	// 4. Tool path.
	if intent.Diagnostic != nil {
		return fmt.Sprintf("expected tool %q, got diagnostic intent", expected.ToolName), false
	}
	if intent.ToolName != expected.ToolName {
		return fmt.Sprintf("tool name: got %q, want %q", intent.ToolName, expected.ToolName), false
	}

	// 5. Input partial match.
	if reason, ok := mismatchInput(expected.Input, intent.Input); !ok {
		return reason, false
	}

	// 6. Selection check (reserved for future CapabilityAwarePlanner eval;
	// EinoPlanner does not populate Selection today).
	if expected.Selection != nil && intent.Selection != nil {
		if reason, ok := mismatchSelection(expected.Selection, intent.Selection); !ok {
			return reason, false
		}
	}

	return "", true
}

// mismatchInput returns ("", true) when actual contains every key in expected
// with a matching value. Extra keys in actual are ignored. A declared key
// missing or with a different value yields (reason, false).
func mismatchInput(expected, actual map[string]any) (string, bool) {
	for k, want := range expected {
		got, ok := actual[k]
		if !ok {
			return fmt.Sprintf("input[%q]: missing", k), false
		}
		if !equalValue(want, got) {
			return fmt.Sprintf("input[%q]: got %v, want %v", k, got, want), false
		}
	}
	return "", true
}

// equalValue compares expected and actual values with light type coercion:
// numbers are compared by value (int vs float64), everything else by
// reflect.DeepEqual semantics via fmt.Sprintf to keep it simple.
func equalValue(want, got any) bool {
	// Numeric coercion: JSON unmarshals numbers to float64, but case data may
	// use int. Compare as float64 when both are numeric.
	if wn, wok := numericFloat(want); wok {
		if gn, gok := numericFloat(got); gok {
			return wn == gn
		}
		return false
	}
	// String and bool: direct comparison via fmt to avoid reflection overhead
	// for the common cases.
	return fmt.Sprintf("%v", want) == fmt.Sprintf("%v", got)
}

// numericFloat returns the value as float64 and true when it is an integer or
// float. Used to bridge JSON float64 and Go int case data.
func numericFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// mismatchSelection checks the subset of CapabilitySelection fields declared
// in ExpectedSelection. Empty expected fields are skipped (partial match).
func mismatchSelection(expected *ExpectedSelection, actual *assistant.CapabilitySelection) (string, bool) {
	_ = expected
	_ = actual
	// Reserved for future CapabilityAwarePlanner evaluation. EinoPlanner does
	// not populate Selection, so this is a no-op today. When a future planner
	// fills Extracted/Missing/Candidates, expand this function to check them.
	return "", true
}

// newReport returns a Report builder with empty buckets.
func newReport() Report {
	return Report{
		ByCategory: make(map[Category]CategoryStat),
	}
}

// record appends the case outcome to the report.
func (r *Report) record(c Case, intent assistant.Intent, err error) {
	stat := r.ByCategory[c.Category]
	stat.Total++

	reason, ok := judge(c, intent, err)
	if ok {
		stat.Passed++
		r.Passed++
	} else {
		stat.Failed++
		r.Failures = append(r.Failures, Failure{
			CaseName:  c.Name,
			Category:  c.Category,
			Reason:    reason,
			Expected:  c.ExpectedIntent,
			Actual:    intent,
			ActualErr: err,
		})
		r.Failed++
	}
	r.ByCategory[c.Category] = stat
}

// finalize computes rates and total, returning the report.
func (r Report) finalize() Report {
	r.Total = r.Passed + r.Failed
	for cat, stat := range r.ByCategory {
		if stat.Total > 0 {
			stat.Rate = float64(stat.Passed) / float64(stat.Total)
		}
		r.ByCategory[cat] = stat
	}
	return r
}
