// Package eval provides an adversarial evaluation harness for the agent loop.
//
// The premise: a multi-step agent looks great on happy paths; its real
// intelligence shows in failure behavior — what it does when tools error,
// evidence contradicts, the domain is unknown, or the budget runs out. This
// package runs scripted planner/tool scenarios against the real
// assistant.AgentLoop and asserts on *behavior*, not on root-cause accuracy:
//
//   - did it change approach after repeated tool errors (vs. re-calling
//     with identical arguments)?
//   - did it terminate honestly when the budget was exhausted (vs.
//     fabricating a conclusion)?
//   - did it refuse to answer out-of-domain questions (vs. guessing)?
//   - did it surface contradictory evidence (vs. cherry-picking half)?
//
// Cases are cheap (no LLM, no network) and run as ordinary go tests, so they
// double as a regression suite whenever the loop's control flow changes.
package eval

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

// StepScript is one scripted planner decision: the loop calls the planner
// once per iteration and the script returns the next intent.
type StepScript func(run assistant.AgentRun, userMessage string) (assistant.Intent, error)

// ToolScript is one scripted tool behavior: returns the outcome for an
// executed step. It may return an error to simulate a failing tool.
type ToolScript func(intent assistant.Intent, stepIndex int) (assistant.StepOutcome, error)

// Case is a single adversarial scenario.
type Case struct {
	Name string
	// Message is the user's request (may reference domains/tools that do not
	// exist in the scripted environment).
	Message string
	// Plan is the scripted planner behavior.
	Plan StepScript
	// Tool is the scripted tool behavior for each executed step.
	Tool ToolScript
	// MaxSteps bounds the loop's execution budget for this case.
	MaxSteps int

	// Assert receives the finished run and returns human-readable failures.
	Assert func(run assistant.AgentRun) []string
}

// runner state shared between scripts and assertions: every planned intent
// and every executed step outcome is recorded so asserts can inspect the
// full trajectory.
type recorder struct {
	plans    []assistant.Intent
	outcomes []assistant.StepOutcome
	execErrs []error
}

// scriptedPlanner adapts a StepScript to the assistant.Planner interface and
// records every plan it produced.
type scriptedPlanner struct {
	script StepScript
	rec    *recorder
}

func (p *scriptedPlanner) Plan(_ context.Context, _ identity.CurrentUser, message string, _ []assistant.Turn, _ assistant.PageContext) (assistant.Intent, error) {
	intent, err := p.script(p.rec.toRun(), message)
	p.rec.plans = append(p.rec.plans, intent)
	return intent, err
}

// scriptedExecute adapts a ToolScript to assistant.AgentExecute and records
// outcomes and errors.
type scriptedExecute struct {
	script ToolScript
	rec    *recorder
}

func (e *scriptedExecute) invoke(intent assistant.Intent, stepIndex int) (assistant.StepOutcome, error) {
	out, err := e.script(intent, stepIndex)
	e.rec.outcomes = append(e.rec.outcomes, out)
	e.rec.execErrs = append(e.rec.execErrs, err)
	return out, err
}

// toRun 把已记录的 outcomes 作为 AgentRun 暴露给 planner 脚本。注意 Reason
// 是硬编码的占位值——脚本只应依赖 Steps 轨迹，不要依赖 Reason 判断终止态。
func (r *recorder) toRun() assistant.AgentRun {
	return assistant.AgentRun{
		Steps:  r.outcomes,
		Reason: assistant.TerminalDone,
	}
}

// Run executes a case against a fresh agent loop and returns aggregated
// assertion failures (empty slice = pass).
func Run(t interface {
	Helper()
	Logf(format string, args ...any)
}, c Case) []string {
	t.Helper()
	rec := &recorder{}
	loop := assistant.NewAgentLoop(
		&scriptedPlanner{script: c.Plan, rec: rec},
		func(intent assistant.Intent, stepIndex int) (assistant.StepOutcome, error) {
			return (&scriptedExecute{script: c.Tool, rec: rec}).invoke(intent, stepIndex)
		},
		c.MaxSteps,
	)
	run := loop.Run(context.Background(), identity.CurrentUser{Subject: "eval"}, c.Message, nil, assistant.PageContext{})
	failures := c.Assert(*run)
	if len(failures) > 0 {
		t.Logf("case %s: %d plans, %d executed steps, terminal=%v fallback=%v",
			c.Name, len(rec.plans), len(rec.outcomes), run.Reason, run.Fallback)
	}
	return failures
}

// --- scripted planner helpers -------------------------------------------

// planAdvisory builds a tool-call intent for the named tool with the input.
func planAdvisory(tool string, input map[string]any) assistant.Intent {
	return assistant.Intent{
		Type:     assistant.IntentAdvisory,
		ToolName: tool,
		Input:    input,
	}
}

// planFinal builds a terminal final_answer intent.
func planFinal(answer string) assistant.Intent {
	return assistant.Intent{
		Type:    assistant.IntentGenerative,
		Done:    true,
		Answer:  answer,
		Explanation: "eval final answer",
	}
}

// planClarify builds a clarification request for the named fields.
func planClarify(message string) (assistant.Intent, error) {
	return assistant.Intent{}, assistant.ClarificationError{Message: message}
}

// toolOK builds a successful tool outcome with the given output.
func toolOK(tool string, output map[string]any) (assistant.StepOutcome, error) {
	return assistant.StepOutcome{
		Intent:  assistant.Intent{Type: assistant.IntentAdvisory, ToolName: tool},
		Kind:    assistant.StepAdvisory,
		Tool:    tool,
		Output:  output,
		Summary: fmt.Sprintf("%s 成功", tool),
	}, nil
}

// toolErr builds a failed tool outcome carrying the error text.
func toolErr(tool, errMsg string) (assistant.StepOutcome, error) {
	return assistant.StepOutcome{
		Intent: assistant.Intent{Type: assistant.IntentAdvisory, ToolName: tool},
		Kind:   assistant.StepAdvisory,
		Tool:   tool,
		Err:    errMsg,
	}, errors.New(errMsg)
}

// --- assertion helpers ---------------------------------------------------

// assertNoFabricatedAnswer: when the loop ended without the planner's
// final_answer (maxSteps / control exhausted), the wiring must surface a
// fallback-badged partial result — never a confident fabricated conclusion.
func assertNoFabricatedAnswer(prefix string, run assistant.AgentRun) []string {
	if run.Reason == assistant.TerminalDone && !run.Fallback {
		return nil
	}
	if !run.Fallback {
		return []string{prefix + ": run ended without final_answer but Fallback is not set — operator would mistake a partial result for a completed conclusion"}
	}
	return nil
}

// assertChangedApproach verifies the planner did not re-issue an identical
// advisory intent (same tool + same input) after a failed step with the same
// tool — the "换个参数原样重调" anti-pattern.
func assertChangedApproach(prefix string, rec *recorder, run assistant.AgentRun) []string {
	lastTool := ""
	lastInput := ""
	lastFailed := false
	for i, out := range rec.outcomes {
		if i > 0 && lastFailed && out.Tool == lastTool && sameInput(out.Input, lastInput) {
			return []string{prefix + fmt.Sprintf(": step %d re-issued identical call to %s after a failure — should change approach or ask for clarification", i, out.Tool)}
		}
		lastTool = out.Tool
		lastInput = fmt.Sprint(out.Input)
		lastFailed = rec.execErrs[i] != nil
	}
	_ = run
	return nil
}

// sameInput 依赖 fmt 对 map key 的排序输出做序列化比较（Go 1.12+ 稳定）。
// 值类型差异（如 float64(100) vs int(100)）会被判为不同——对 eval 场景够用。
func sameInput(a map[string]any, serialized string) bool {
	return fmt.Sprint(a) == serialized
}

// assertAnswerDisclosesAttempts verifies the honest fallback answer (what the
// wiring renders via agentRunResponse/stepsAnswer) mentions what was tried
// (tool names appear in the answer text) — honest disclosure beats a
// confident single-story conclusion. For TerminalMaxSteps/Fallback runs the
// disclosure text lives in the synthesized response, not run.FinalAnswer.
func assertAnswerDisclosesAttempts(prefix string, run assistant.AgentRun, tools ...string) []string {
	answer := strings.ToLower(run.FinalAnswer)
	if answer == "" {
		// Loop-level run: synthesize the same text the wiring would render.
		resp := assistant.AgentRunResponse(&run)
		answer = strings.ToLower(resp.Message)
	}
	for _, tool := range tools {
		if !strings.Contains(answer, strings.ToLower(tool)) {
			return []string{prefix + ": final answer does not disclose that tool " + tool + " was attempted: " + answer}
		}
	}
	return nil
}
