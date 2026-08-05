package assistant_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

// scriptedPlanner returns intents from a queue in order, then keeps returning
// the last one. It records each history slice it was called with so tests can
// assert the loop fed tool results back into subsequent plans.
type scriptedPlanner struct {
	intents  []assistant.Intent
	errs     []error
	historys [][]assistant.Turn
}

func (p *scriptedPlanner) Plan(_ context.Context, _ identity.CurrentUser, _ string, history []assistant.Turn, _ assistant.PageContext) (assistant.Intent, error) {
	// Deep copy so the test can inspect what the loop fed back.
	cp := make([]assistant.Turn, len(history))
	copy(cp, history)
	p.historys = append(p.historys, cp)
	i := len(p.historys) - 1
	if i < len(p.errs) {
		if err := p.errs[i]; err != nil {
			return assistant.Intent{}, err
		}
	}
	if i < len(p.intents) {
		return p.intents[i], nil
	}
	return assistant.Intent{}, errors.New("scriptedPlanner: out of script")
}

func readIntent() assistant.Intent {
	return assistant.Intent{ToolName: "cluster.status.read", Input: map[string]any{"environment": "prod"}}
}

func readIntentOn(tool string, input map[string]any) assistant.Intent {
	return assistant.Intent{ToolName: tool, Input: input}
}

func writeIntent() assistant.Intent {
	// The executive type is declared explicitly: topic.retention.set is now a
	// runtime-registered capability, so ClassifyIntent cannot infer write from a
	// static tools.Lookup in a bare unit test. Marking the type keeps the loop's
	// write-boundary detection deterministic here.
	return assistant.Intent{ToolName: "topic.retention.set", Type: assistant.IntentExecutive, Input: map[string]any{"topic": "x", "retention_hours": 24}}
}

func doneIntent(summary string) assistant.Intent {
	return assistant.Intent{Done: true, Answer: summary}
}

// recorderExecute is a scriptable executor that returns the given outcome/err
// counters, recording how many advisory steps actually ran.
type recorderExecute struct {
	outcomes []assistant.StepOutcome
	errs     []error
	ran      int
	calls    int
}

func (e *recorderExecute) fn() assistant.AgentExecute {
	return func(intent assistant.Intent, stepIndex int) (assistant.StepOutcome, error) {
		e.calls++
		i := e.ran
		e.ran++
		if i < len(e.errs) && e.errs[i] != nil {
			return assistant.StepOutcome{}, e.errs[i]
		}
		if i < len(e.outcomes) {
			o := e.outcomes[i]
			o.Tool = intent.ToolName
			return o, nil
		}
		return assistant.StepOutcome{Tool: intent.ToolName, Summary: "ran " + intent.ToolName}, nil
	}
}

// TestAgentLoopStopsOnFinalAnswer: planner returns one advisory read then a
// final_answer. The loop must run one step, feed it back, and stop as Done.
func TestAgentLoopStopsOnFinalAnswer(t *testing.T) {
	t.Parallel()
	planner := &scriptedPlanner{intents: []assistant.Intent{readIntent(), doneIntent("检查完成，集群健康")}}
	exec := &recorderExecute{}
	loop := assistant.NewAgentLoop(planner, exec.fn(), 8)

	run := loop.Run(context.Background(), user(), "检查 prod 集群", nil, assistant.PageContext{})
	if run.Reason != assistant.TerminalDone {
		t.Fatalf("reason = %v, want TerminalDone", run.Reason)
	}
	if run.FinalAnswer != "检查完成，集群健康" {
		t.Fatalf("FinalAnswer = %q", run.FinalAnswer)
	}
	if len(run.Steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(run.Steps))
	}
	if len(planner.historys) != 2 {
		t.Fatalf("planner calls = %d, want 2", len(planner.historys))
	}
	// The second plan must have received the first step's feedback turn
	// carrying ResponseType tool_step.
	feedback := planner.historys[1]
	if len(feedback) != 1 || feedback[0].ResponseType != "tool_step" {
		t.Fatalf("feedback history = %+v, want one tool_step turn", feedback)
	}
}

// TestAgentLoopStopsOnClarification: planner asks for clarification. The loop
// must surface it and make no tool calls.
func TestAgentLoopStopsOnClarification(t *testing.T) {
	t.Parallel()
	planner := &scriptedPlanner{errs: []error{assistant.ErrClarificationNeeded}}
	exec := &recorderExecute{}
	loop := assistant.NewAgentLoop(planner, exec.fn(), 8)

	run := loop.Run(context.Background(), user(), "帮我看看", nil, assistant.PageContext{})
	if run.Reason != assistant.TerminalClarification {
		t.Fatalf("reason = %v, want TerminalClarification", run.Reason)
	}
	if run.Clarified == "" {
		t.Fatal("clarification message is empty")
	}
	if exec.ran != 0 {
		t.Fatalf("executed %d steps, want 0", exec.ran)
	}
}

// TestAgentLoopStopsOnWriteHandoff: planner returns a write intent after a
// read. The loop must NOT execute the write; it hands it off as a pending plan
// and stops.
func TestAgentLoopStopsOnWriteHandoff(t *testing.T) {
	t.Parallel()
	w := writeIntent()
	planner := &scriptedPlanner{intents: []assistant.Intent{readIntent(), w}}
	exec := &recorderExecute{outcomes: []assistant.StepOutcome{
		{Summary: "集群健康"},                     // advisory read
		{PlanID: "plan-1", Status: "pending"}, // write handoff
	}}
	loop := assistant.NewAgentLoop(planner, exec.fn(), 8)

	run := loop.Run(context.Background(), user(), "查完再改 retention", nil, assistant.PageContext{})
	if run.Reason != assistant.TerminalHandoff {
		t.Fatalf("reason = %v, want TerminalHandoff", run.Reason)
	}
	if run.Handoff == nil {
		t.Fatal("handoff is nil")
	}
	if run.Handoff.Kind != assistant.StepExecutive {
		t.Fatalf("handoff kind = %v, want StepExecutive", run.Handoff.Kind)
	}
	if run.Handoff.PlanID != "plan-1" {
		t.Fatalf("plan id = %q", run.Handoff.PlanID)
	}
	// One advisory ran, then the write was handed off (never "executed").
	if len(run.Steps) != 2 {
		t.Fatalf("steps = %d, want 2 (read + write handoff)", len(run.Steps))
	}
}

// TestAgentLoopBoundedByMaxSteps: planner never concludes. The loop must stop
// at maxSteps with the accumulated steps, not spin forever.
func TestAgentLoopBoundedByMaxSteps(t *testing.T) {
	t.Parallel()
	// Distinct reads so the convergence backstop (which collapses repeated
	// identical reads at step 0) does not fire; this isolates the maxSteps
	// budget as the terminator.
	planner := &scriptedPlanner{intents: []assistant.Intent{
		readIntentOn("cluster.status.read", map[string]any{"environment": "prod", "cluster": "a"}),
		readIntentOn("cluster.status.read", map[string]any{"environment": "prod", "cluster": "b"}),
		readIntentOn("cluster.status.read", map[string]any{"environment": "prod", "cluster": "c"}),
	}}
	exec := &recorderExecute{}
	loop := assistant.NewAgentLoop(planner, exec.fn(), 2)

	run := loop.Run(context.Background(), user(), "无限排查", nil, assistant.PageContext{})
	if run.Reason != assistant.TerminalMaxSteps {
		t.Fatalf("reason = %v, want TerminalMaxSteps", run.Reason)
	}
	if len(run.Steps) != 2 {
		t.Fatalf("steps = %d, want 2 (maxSteps)", len(run.Steps))
	}
	if exec.ran != 2 {
		t.Fatalf("executed %d, want 2", exec.ran)
	}
}

// TestAgentLoopFailureFeedsBackAndFallsBack: a read fails, the loop feeds the
// error back so the planner can pick a fallback tool, which then succeeds and
// the loop concludes.
func TestAgentLoopFailureFeedsBackAndFallsBack(t *testing.T) {
	t.Parallel()
	planner := &scriptedPlanner{intents: []assistant.Intent{
		assistant.Intent{ToolName: "bogus.tool"}, // first fails at execute
		readIntent(),                             // fallback
		doneIntent("fallback 成功"),
	}}
	exec := &recorderExecute{errs: []error{errors.New("tool_not_registered")}}
	loop := assistant.NewAgentLoop(planner, exec.fn(), 8)

	run := loop.Run(context.Background(), user(), "查一下", nil, assistant.PageContext{})
	if run.Reason != assistant.TerminalDone {
		t.Fatalf("reason = %v, want TerminalDone (fallback succeeded)", run.Reason)
	}
	// The second plan call must have seen the failure feedback turn.
	if len(planner.historys) < 2 {
		t.Fatalf("planner calls = %d, want >= 2", len(planner.historys))
	}
	fb := planner.historys[1]
	if len(fb) != 1 || !strings.Contains(fb[0].Content, "工具执行失败") {
		t.Fatalf("feedback = %+v, want a failure-feedback turn", fb)
	}
}

// TestAgentLoopGivesUpAfterRepeatedFailures: a tool keeps failing; the loop
// gives up after maxAgentFailures instead of looping forever.
func TestAgentLoopGivesUpAfterRepeatedFailures(t *testing.T) {
	t.Parallel()
	planner := &scriptedPlanner{intents: []assistant.Intent{
		assistant.Intent{ToolName: "a"}, assistant.Intent{ToolName: "b"}, assistant.Intent{ToolName: "c"},
		assistant.Intent{ToolName: "d"},
	}}
	exec := &recorderExecute{errs: []error{errors.New("boom"), errors.New("boom"), errors.New("boom")}}
	loop := assistant.NewAgentLoop(planner, exec.fn(), 8)

	run := loop.Run(context.Background(), user(), "查", nil, assistant.PageContext{})
	if run.Reason != assistant.TerminalMaxSteps {
		t.Fatalf("reason = %v, want TerminalMaxSteps", run.Reason)
	}
	if run.Err == nil {
		t.Fatal("expected the last failure to surface as run.Err")
	}
	if exec.ran != 3 {
		t.Fatalf("executed %d, want 3 (failure budget)", exec.ran)
	}
}

// TestAgentLoopConvergesOnRepeatedRead: even if the planner keeps re-emitting
// the same read on the same resource (as a weak reasoning model can), the loop's
// deterministic convergence backstop concludes after the first execution instead
// of burning maxSteps on identical reads.
func TestAgentLoopConvergesOnRepeatedRead(t *testing.T) {
	t.Parallel()
	planner := &scriptedPlanner{intents: []assistant.Intent{
		readIntent(), // executes once
		readIntent(), // identical tool+input: must be collapsed, not re-run
	}}
	exec := &recorderExecute{}
	loop := assistant.NewAgentLoop(planner, exec.fn(), 8)

	run := loop.Run(context.Background(), user(), "检查 prod 集群", nil, assistant.PageContext{})
	if run.Reason != assistant.TerminalDone {
		t.Fatalf("reason = %v, want TerminalDone (converged on repeated read)", run.Reason)
	}
	if exec.ran != 1 {
		t.Fatalf("executed %d, want 1 (duplicate read collapsed)", exec.ran)
	}
	if len(run.Steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(run.Steps))
	}
	if run.FinalAnswer == "" {
		t.Fatal("expected a fallback answer summarizing the executed step")
	}
}

// TestAgentLoopDoesNotCollapseDistinctReads: the convergence backstop must NOT
// collapse reads that target different resources/tools, so legitimate multi-step
// diagnosis still runs to completion.
func TestAgentLoopDoesNotCollapseDistinctReads(t *testing.T) {
	t.Parallel()
	planner := &scriptedPlanner{intents: []assistant.Intent{
		readIntentOn("cluster.status.read", map[string]any{"environment": "prod", "cluster": "a"}),
		readIntentOn("cluster.status.read", map[string]any{"environment": "prod", "cluster": "b"}),
		doneIntent("两个集群都检查完"),
	}}
	exec := &recorderExecute{}
	loop := assistant.NewAgentLoop(planner, exec.fn(), 8)

	run := loop.Run(context.Background(), user(), "查两个集群", nil, assistant.PageContext{})
	if run.Reason != assistant.TerminalDone {
		t.Fatalf("reason = %v, want TerminalDone", run.Reason)
	}
	if exec.ran != 2 {
		t.Fatalf("executed %d, want 2 (distinct reads both run)", exec.ran)
	}
	if run.FinalAnswer != "两个集群都检查完" {
		t.Fatalf("FinalAnswer = %q, want the planner's answer", run.FinalAnswer)
	}
}

// --- ① single-turn diagnostic short-circuit ---

func singleDiagIntent(domain string) assistant.Intent {
	return assistant.Intent{Diagnostic: &diagnostics.Request{Domain: domain, Environment: "prod"}}
}

func diagOutcome(tool, summary string) assistant.StepOutcome {
	return assistant.StepOutcome{
		Tool:    tool,
		Summary: summary,
		Output:  map[string]any{"summary": summary, "severity": "ok", "domains": []string{}},
	}
}

// TestAgentLoopShortCircuitsSingleDiagnostic: a single-domain diagnostic that
// ran successfully is structurally conclusive — the loop must conclude after ONE
// step instead of looping back to re-plan (letting the model self-enforce a
// final_answer). This is the execution-layer fix for "single-turn simple
// requests burning the full multi-step loop".
func TestAgentLoopShortCircuitsSingleDiagnostic(t *testing.T) {
	t.Parallel()
	planner := &scriptedPlanner{intents: []assistant.Intent{
		singleDiagIntent("kafka"),
		doneIntent("should never be reached"),
	}}
	exec := &recorderExecute{outcomes: []assistant.StepOutcome{
		diagOutcome("kafka", "诊断完成：kafka consumer_group lag 正常"),
	}}
	loop := assistant.NewAgentLoop(planner, exec.fn(), 8)

	run := loop.Run(context.Background(), user(), "检查 kafka 健康吗", nil, assistant.PageContext{})
	if run.Reason != assistant.TerminalDone {
		t.Fatalf("reason = %v, want TerminalDone (single-domain short-circuit)", run.Reason)
	}
	if run.Fallback {
		t.Fatalf("fallback = true, want false — single-domain diagnostic is a genuine answer, not a synthesized fallback")
	}
	if len(run.Steps) != 1 {
		t.Fatalf("steps = %d, want 1 — the single diagnostic must not re-plan", len(run.Steps))
	}
	if exec.ran != 1 {
		t.Fatalf("executed %d steps, want 1", exec.ran)
	}
	if !strings.Contains(run.FinalAnswer, "kafka") {
		t.Fatalf("FinalAnswer = %q, want to carry the diagnostic summary", run.FinalAnswer)
	}
}

// TestAgentLoopDoesNotShortCircuitContinuation: a single-domain diagnostic asked
// with an explicit continuation intent ("再查 kafka") must NOT be short-circuited
// — the model should be allowed to keep planning.
func TestAgentLoopDoesNotShortCircuitContinuation(t *testing.T) {
	t.Parallel()
	planner := &scriptedPlanner{intents: []assistant.Intent{
		singleDiagIntent("kafka"),
		doneIntent("继续分析后收尾"),
	}}
	exec := &recorderExecute{outcomes: []assistant.StepOutcome{
		diagOutcome("kafka", "诊断完成：kafka lag 正常"),
	}}
	loop := assistant.NewAgentLoop(planner, exec.fn(), 8)

	run := loop.Run(context.Background(), user(), "检查 kafka，再顺便对比下历史", nil, assistant.PageContext{})
	if run.Reason != assistant.TerminalDone {
		t.Fatalf("reason = %v, want TerminalDone", run.Reason)
	}
	// The continuation cue (再/顺便) must allow the loop to re-plan and honor the
	// model's final_answer on the second plan.
	if len(run.Steps) != 1 || run.FinalAnswer != "继续分析后收尾" {
		t.Fatalf("steps=%d FinalAnswer=%q, want the model's final_answer honored (no short-circuit) on continuation", len(run.Steps), run.FinalAnswer)
	}
}

// --- ② runbook-sequence conclusion gate ---

// TestAgentLoopSequenceGatesFinalAnswer: with a declared runbook sequence
// [alert.query, event.query], a model final_answer emitted BEFORE those members
// are touched must NOT conclude — the loop steers the planner back; only after
// the sequence is satisfied does the final_answer conclude.
func TestAgentLoopSequenceGatesFinalAnswer(t *testing.T) {
	t.Parallel()
	planner := &scriptedPlanner{intents: []assistant.Intent{
		doneIntent("已回答但序列未齐"), // final_answer too early
		readIntentOn("alert.query", map[string]any{"environment": "prod"}),
		readIntentOn("event.query", map[string]any{"environment": "prod"}),
		doneIntent("序列齐了，收尾"),
	}}
	exec := &recorderExecute{}
	loop := assistant.NewAgentLoop(planner, exec.fn(), 8).
		WithRunbookSequence([]string{"alert.query", "event.query"})

	run := loop.Run(context.Background(), user(), "告警根因分析", nil, assistant.PageContext{})
	if run.Reason != assistant.TerminalDone {
		t.Fatalf("reason = %v, want TerminalDone", run.Reason)
	}
	if run.FinalAnswer != "序列齐了，收尾" {
		t.Fatalf("FinalAnswer = %q, want the concluding answer AFTER the sequence is satisfied", run.FinalAnswer)
	}
	// Both sequence members must have been executed as advisory steps.
	var tools []string
	for _, s := range run.Steps {
		if s.Kind == assistant.StepAdvisory {
			tools = append(tools, s.Tool)
		}
	}
	for _, want := range []string{"alert.query", "event.query"} {
		if !slices.Contains(tools, want) {
			t.Fatalf("advisory tools = %v, want to include sequence member %q", tools, want)
		}
	}
	// The early final_answer must have been steered: a steering feedback turn
	// naming the pending sequence must have reached the planner history.
	steered := false
	for _, h := range run.History {
		if strings.Contains(h.Content, "告警根因序列尚未收集齐") && strings.Contains(h.Content, "alert.query") {
			steered = true
			break
		}
	}
	if !steered {
		t.Fatalf("history has no sequence steering turn before conclusion — the early final_answer was not gated")
	}
}

// TestAgentLoopSequenceCompleteConcludesDirectly: when the sequence is already
// satisfied before the first plan, a final_answer concludes immediately (no
// steering).
func TestAgentLoopSequenceCompleteConcludesDirectly(t *testing.T) {
	t.Parallel()
	planner := &scriptedPlanner{intents: []assistant.Intent{
		readIntentOn("alert.query", map[string]any{"environment": "prod"}),
		doneIntent("收尾"),
	}}
	exec := &recorderExecute{}
	loop := assistant.NewAgentLoop(planner, exec.fn(), 8).
		WithRunbookSequence([]string{"alert.query"})

	run := loop.Run(context.Background(), user(), "告警根因分析", nil, assistant.PageContext{})
	if run.Reason != assistant.TerminalDone {
		t.Fatalf("reason = %v, want TerminalDone", run.Reason)
	}
	if run.FinalAnswer != "收尾" {
		t.Fatalf("FinalAnswer = %q", run.FinalAnswer)
	}
}

// --- ⑤ clarification carries prior steps ---

// TestAgentLoopClarificationCarriesPriorSteps: when a clarification is raised
// AFTER some advisory steps already ran, the clarification message must surface
// what was already checked so the operator has context.
func TestAgentLoopClarificationCarriesPriorSteps(t *testing.T) {
	planner := &scriptedPlanner{intents: []assistant.Intent{
		readIntent(),
	}}
	planner.errs = []error{nil, assistant.ErrClarificationNeeded}
	exec := &recorderExecute{outcomes: []assistant.StepOutcome{{Summary: "cluster green"}}}
	loop := assistant.NewAgentLoop(planner, exec.fn(), 8)

	run := loop.Run(context.Background(), user(), "先查集群再看告警", nil, assistant.PageContext{})
	if run.Reason != assistant.TerminalClarification {
		t.Fatalf("reason = %v, want TerminalClarification", run.Reason)
	}
	if !strings.Contains(run.Clarified, "cluster green") {
		t.Fatalf("clarification = %q, want it to carry the prior step summary", run.Clarified)
	}
}
