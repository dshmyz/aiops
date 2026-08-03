// Package eval provides a planner evaluation framework for EinoPlanner.
//
// The framework runs scripted LLM responses through EinoPlanner and asserts
// that the resulting Intent matches expected outcomes. It evaluates only the
// planner layer (Intent construction); it does not execute tools, create action
// plans, or write audit records. The governance loop (plan → policy →
// execution → audit) remains untouched.
package eval

import (
	"context"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
)

// Category partitions cases into groups that share a threshold policy.
type Category string

const (
	// CategoryTool covers cases where the planner should resolve a concrete
	// tool intent (ToolName + Input).
	CategoryTool Category = "tool"
	// CategoryClarification covers cases where the planner should signal that
	// the user message is missing required information.
	CategoryClarification Category = "clarification"
	// CategoryDiagnostic covers cases where the planner should route to the
	// diagnostic package instead of a concrete tool.
	CategoryDiagnostic Category = "diagnostic"
	// CategoryHistory covers multi-turn cases where the planner should resolve
	// references to previous turns (e.g. "同 environment 再查一个") by reading
	// the structured [Last Intent] block appended to assistant turns.
	CategoryHistory Category = "history"
)

// Case is one evaluation scenario.
//
// ScriptedLLM is the canned JSON the ScriptedProvider will return when the
// planner asks the LLM for this case. It must conform to EinoPlanner's
// einoIntent JSON schema:
//
//	{"tool_name":"...","input":{},"diagnostic":{...}|null,"confidence":0.0,"explanation":"..."}
//
// History is optional and reserved for future multi-turn evaluation.
//
// ExpectedIntent declares the expected outcome. Only one of the path flags
// (Clarification / Diagnostic / ToolName) should be set per case; the runner
// checks them in that order.
type Case struct {
	Name           string
	Category       Category
	UserMessage    string
	History        []assistant.Turn
	ScriptedLLM    string
	ExpectedIntent ExpectedIntent
	Notes          string
}

// ExpectedIntent declares what the planner should produce for a case.
//
// Input uses partial matching: only the keys listed here are checked. Extra
// keys in the actual Intent.Input are ignored, so LLM-supplied fields that the
// case does not care about do not cause failures.
//
// Selection is optional and only checked when non-nil. Note: EinoPlanner does
// not populate Selection today, so this field is reserved for future
// CapabilityAwarePlanner evaluation.
type ExpectedIntent struct {
	ToolName      string
	Input         map[string]any
	Selection     *ExpectedSelection
	Clarification bool
	Diagnostic    bool
}

// ExpectedSelection is the subset of CapabilitySelection fields to check.
type ExpectedSelection struct {
	Environment string
	Cluster     string
	Domain      string
}

// caseNameKey is the context key type used by Run to propagate the current
// case name to ScriptedProvider.Generate. Using a private key type prevents
// collisions with other context values in the call chain.
type caseNameKey struct{}

// withCaseName returns a ctx carrying the case name for ScriptedProvider.
func withCaseName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, caseNameKey{}, name)
}

// caseNameFromContext extracts the case name injected by Run, returning "" if
// absent. ScriptedProvider uses this to look up its scripted response.
func caseNameFromContext(ctx context.Context) string {
	v := ctx.Value(caseNameKey{})
	name, _ := v.(string)
	return name
}

// errNoScriptedResponse is returned by ScriptedProvider when no scripted
// response matches the current case. It signals a framework wiring error
// (the case name was not propagated through ctx), not a planner bug.
var errNoScriptedResponse = errString("scripted provider: no response registered for case")

type errString string

func (e errString) Error() string { return string(e) }
