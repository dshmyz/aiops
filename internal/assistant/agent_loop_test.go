package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

// --- Fake planner for AgentLoop tests ---

// fakePlanner returns a sequence of intents, one per Plan() call, then errors.
type fakePlanner struct {
	intents []Intent
	errs    []error
	call    int
}

func (p *fakePlanner) Plan(_ context.Context, _ identity.CurrentUser, _ string, _ []Turn, _ PageContext) (Intent, error) {
	i := p.call
	p.call++
	if i < len(p.errs) && p.errs[i] != nil {
		return Intent{}, p.errs[i]
	}
	if i < len(p.intents) {
		return p.intents[i], nil
	}
	// Default: return Done with empty answer.
	return Intent{Done: true, Answer: "done"}, nil
}

// --- Fake executor ---

// fakeExecutor records calls and returns pre-configured outcomes.
type fakeExecutor struct {
	outcomes []StepOutcome
	errs     []error
	call     int
}

func (e *fakeExecutor) execute(_ Intent, _ int) (StepOutcome, error) {
	i := e.call
	e.call++
	if i < len(e.errs) && e.errs[i] != nil {
		return StepOutcome{}, e.errs[i]
	}
	if i < len(e.outcomes) {
		return e.outcomes[i], nil
	}
	return StepOutcome{Tool: "default.tool", Summary: "ok"}, nil
}

func newFakeLoop(planner *fakePlanner, executor *fakeExecutor, maxExec, maxControl int) *AgentLoop {
	execFn := func(intent Intent, step int) (StepOutcome, error) {
		return executor.execute(intent, step)
	}
	return NewAgentLoop(planner, execFn, maxExec).
		WithMaxControlSteps(maxControl)
}

// --- Dual-budget tests ---

// TestControlStepsDoNotEatExecBudget verifies that steering turns
// (sequence gate) consume control budget, not execution budget.
func TestControlStepsDoNotEatExecBudget(t *testing.T) {
	t.Parallel()
	// Planner: first call returns Done (triggering sequence steer), then
	// returns 2 normal intents, then Done again (no pending sequence).
	// With old code, the steer would eat one of the 2 exec steps.
	// With dual budgets, 2 exec steps should still be available after steers.
	steps := []Intent{
		{Done: true, Answer: "premature"}, // triggers steer
		{ToolName: "tool.a", Input: map[string]any{"k": "1"}},
		{Done: true, Answer: "finally"},
	}
	// Need 2 steer attempts before sequence is "done".
	// Fake sequence: ["tool.a", "tool.b"] — tool.a gets touched, tool.b stays pending.
	// But we only have tool.a in steps, so let's use a simpler approach:
	// sequence = ["tool.a"] — after tool.a executes, sequence is complete,
	// so the second Done is honored.
	//
	// Actually: first Done triggers steer (tool.a pending), then intent[1]
	// executes tool.a (sequence touched), then intent[2] Done — sequence
	// satisfied, final answer honored.
	p := &fakePlanner{intents: steps}
	e := &fakeExecutor{outcomes: []StepOutcome{
		{Tool: "tool.a", Summary: "result a"},
	}}
	loop := newFakeLoop(p, e, 2, 6).WithRunbookSequence([]string{"tool.a"})

	run := loop.Run(context.Background(), identity.CurrentUser{}, "test", nil, PageContext{})
	if run.Reason != TerminalDone {
		t.Fatalf("Reason = %v, want TerminalDone", run.Reason)
	}
	if run.FinalAnswer != "finally" {
		t.Fatalf("FinalAnswer = %q, want finally", run.FinalAnswer)
	}
	// 1 exec step (tool.a) was executed — budget exec should be 1.
	if len(run.Steps) != 1 {
		t.Fatalf("Steps = %d, want 1", len(run.Steps))
	}
	// 1 control step consumed (sequence steer).
	if p.call != 3 {
		t.Fatalf("planner calls = %d, want 3 (steer + exec + final)", p.call)
	}
}

// TestControlExhaustedStopsRun verifies that exhausting the control budget
// terminates the run with TerminalControlExhausted.
func TestControlExhaustedStopsRun(t *testing.T) {
	t.Parallel()
	// Planner always returns Done but sequence always has pending members.
	// This burns through control budget without executing anything.
	intents := make([]Intent, 10)
	for i := range intents {
		intents[i] = Intent{Done: true, Answer: "premature"}
	}
	p := &fakePlanner{intents: intents}
	e := &fakeExecutor{}
	loop := newFakeLoop(p, e, 8, 3).WithRunbookSequence([]string{"tool.missing"})

	run := loop.Run(context.Background(), identity.CurrentUser{}, "test", nil, PageContext{})
	if run.Reason != TerminalControlExhausted {
		t.Fatalf("Reason = %v, want TerminalControlExhausted", run.Reason)
	}
	if !run.Fallback {
		t.Fatal("Fallback should be true on control exhaustion")
	}
	// 0 exec steps — control exhaustion happened before any tool ran.
	if len(run.Steps) != 0 {
		t.Fatalf("Steps = %d, want 0 (no exec budget spent)", len(run.Steps))
	}
}

// TestFailureFeedbackUsesControlBudget verifies that tool execution failures
// consume control budget (failure feedback), not execution budget.
func TestFailureFeedbackUsesControlBudget(t *testing.T) {
	t.Parallel()
	// Planner: 3 normal intents, but executors[0] and [1] fail.
	// After 2 control-step failures, budget.controlLimit=2 should exhaust.
	intents := []Intent{
		{ToolName: "tool.a", Input: map[string]any{"k": "1"}},
		{ToolName: "tool.b", Input: map[string]any{"k": "2"}},
		{ToolName: "tool.c", Input: map[string]any{"k": "3"}},
	}
	p := &fakePlanner{intents: intents}
	e := &fakeExecutor{
		errs: []error{
			errors.New("fail 1"),
			errors.New("fail 2"),
		},
		outcomes: []StepOutcome{
			{Tool: "tool.c", Summary: "ok c"},
		},
	}
	loop := newFakeLoop(p, e, 8, 2)

	run := loop.Run(context.Background(), identity.CurrentUser{}, "test", nil, PageContext{})
	// 2 failures consumed 2 control steps → control exhausted before
	// the 3rd intent gets a chance.
	if run.Reason != TerminalControlExhausted {
		t.Fatalf("Reason = %v, want TerminalControlExhausted (2 failures used 2 control steps)", run.Reason)
	}
	// 失败的尝试也被记录为 step（tool.a、tool.b），tool.c 未执行即因控制预算耗尽终止。
	if len(run.Steps) != 2 {
		t.Fatalf("Steps = %d, want 2 (failed attempts recorded)", len(run.Steps))
	}
	for _, step := range run.Steps {
		if step.Err == "" {
			t.Fatalf("failed step %q has empty Err", step.Tool)
		}
	}
}

// TestFailedStepRecordedAndRetryAllowed 验证失败步进 run.Steps（带 Err）但不标
// seen——同一工具失败后可被再次调用并成功。
func TestFailedStepRecordedAndRetryAllowed(t *testing.T) {
	t.Parallel()
	p := &fakePlanner{intents: []Intent{
		{ToolName: "tool.a", Input: map[string]any{"k": "1"}},
		{ToolName: "tool.a", Input: map[string]any{"k": "1"}},
	}}
	e := &fakeExecutor{
		errs:     []error{errors.New("transient failure")},
		outcomes: []StepOutcome{{Tool: "tool.a", Summary: "ok"}},
	}
	loop := newFakeLoop(p, e, 8, 6)

	run := loop.Run(context.Background(), identity.CurrentUser{}, "test", nil, PageContext{})
	if len(run.Steps) != 2 {
		t.Fatalf("Steps = %d, want 2 (failed attempt + retry success)", len(run.Steps))
	}
	if run.Steps[0].Err == "" {
		t.Fatal("first step Err empty, want the transient failure")
	}
	if run.Steps[0].Kind != StepAdvisory || run.Steps[0].StepIndex != 0 {
		t.Fatalf("failed step = %+v, want advisory at step_index 0", run.Steps[0])
	}
	if run.Steps[1].Err != "" {
		t.Fatalf("second step Err = %q, want success after retry", run.Steps[1].Err)
	}
	if run.Steps[1].Summary != "ok" {
		t.Fatalf("second step Summary = %q, want ok", run.Steps[1].Summary)
	}
}

// TestExecBudgetStillWorks verifies the basic exec budget is respected.
func TestExecBudgetStillWorks(t *testing.T) {
	t.Parallel()
	intents := make([]Intent, 5)
	for i := range intents {
		intents[i] = Intent{ToolName: "tool.x", Input: map[string]any{"i": i}}
	}
	p := &fakePlanner{intents: intents}
	e := &fakeExecutor{
		outcomes: make([]StepOutcome, 5),
	}
	for i := range e.outcomes {
		e.outcomes[i] = StepOutcome{Tool: "tool.x", Summary: "ok"}
	}
	loop := newFakeLoop(p, e, 3, 6)

	run := loop.Run(context.Background(), identity.CurrentUser{}, "test", nil, PageContext{})
	if run.Reason != TerminalMaxSteps {
		t.Fatalf("Reason = %v, want TerminalMaxSteps", run.Reason)
	}
	if len(run.Steps) != 3 {
		t.Fatalf("Steps = %d, want 3 (exec budget=3)", len(run.Steps))
	}
}

// TestClassifyReturnsSteerOnPendingSequence verifies classify returns StateSteer
// when intent.Done but sequence has pending members.
func TestClassifyReturnsSteerOnPendingSequence(t *testing.T) {
	t.Parallel()
	p := &fakePlanner{intents: []Intent{{Done: true, Answer: "early"}}}
	e := &fakeExecutor{}
	loop := newFakeLoop(p, e, 8, 6).WithRunbookSequence([]string{"missing.tool"})

	run := loop.Run(context.Background(), identity.CurrentUser{}, "test", nil, PageContext{})
	if run.Reason != TerminalControlExhausted {
		t.Fatalf("Reason = %v, want TerminalControlExhausted (steer loop exhausted)", run.Reason)
	}
	// Verify steer turn was appended to history.
	found := false
	for _, h := range run.History {
		if strings.Contains(h.Content, "尚未收集齐") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("history missing sequence steer turn")
	}
}

// TestClassifyReturnsClarifyOnClarificationError verifies classify returns
// StateClarify when planOnce returns a ClarificationError.
func TestClassifyReturnsClarifyOnClarificationError(t *testing.T) {
	t.Parallel()
	p := &fakePlanner{
		errs: []error{ClarificationError{Message: "需要指定环境"}},
	}
	e := &fakeExecutor{}
	loop := newFakeLoop(p, e, 8, 6)

	run := loop.Run(context.Background(), identity.CurrentUser{}, "test", nil, PageContext{})
	if run.Reason != TerminalClarification {
		t.Fatalf("Reason = %v, want TerminalClarification", run.Reason)
	}
	if !strings.Contains(run.Clarified, "需要指定环境") {
		t.Fatalf("Clarified = %q, want clarification message", run.Clarified)
	}
}

// TestWithMaxControlStepsDefault verifies default is applied when value < 1.
func TestWithMaxControlStepsDefault(t *testing.T) {
	t.Parallel()
	p := &fakePlanner{}
	e := &fakeExecutor{}
	loop := newFakeLoop(p, e, 8, 0)
	if loop.maxControlSteps != defaultMaxControlSteps {
		t.Fatalf("maxControlSteps = %d, want defaultMaxControlSteps=%d", loop.maxControlSteps, defaultMaxControlSteps)
	}
}

// TestMaxAgentFailuresStillTerminates verifies the consecutive failure
// terminator still works (3 failures → TerminalMaxSteps) when control
// budget is not exhausted.
func TestMaxAgentFailuresStillTerminates(t *testing.T) {
	t.Parallel()
	intents := []Intent{
		{ToolName: "tool.a", Input: map[string]any{"k": "1"}},
		{ToolName: "tool.b", Input: map[string]any{"k": "2"}},
		{ToolName: "tool.c", Input: map[string]any{"k": "3"}},
	}
	p := &fakePlanner{intents: intents}
	e := &fakeExecutor{
		errs: []error{
			errors.New("fail 1"),
			errors.New("fail 2"),
			errors.New("fail 3"),
		},
	}
	// Control budget high enough (10) to not exhaust before maxAgentFailures.
	loop := newFakeLoop(p, e, 8, 10)

	run := loop.Run(context.Background(), identity.CurrentUser{}, "test", nil, PageContext{})
	if run.Reason != TerminalMaxSteps {
		t.Fatalf("Reason = %v, want TerminalMaxSteps (maxAgentFailures=3)", run.Reason)
	}
	if run.Err == nil {
		t.Fatal("run.Err should be set on maxAgentFailures termination")
	}
}
