package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// defaultAgentMaxSteps is the default number of tool invocations per agent
// loop request. Overridable via COPILOT_ASSISTANT_MAX_STEPS.
const defaultAgentMaxSteps = 8

// maxAgentFailures bounds how many consecutive read failures the loop tolerates
// while feeding errors back for fallback replanning, so a planner that keeps
// selecting a broken tool cannot spin forever.
const maxAgentFailures = 3

// AgentStepKind classifies what the agent loop did with one planner intent.
type AgentStepKind int

const (
	// StepAdvisory: a read / diagnostic step the loop executed inline and fed
	// back to the planner for the next iteration.
	StepAdvisory AgentStepKind = iota
	// StepExecutive: a write step. The loop stops here and hands a pending plan
	// to the human for approval — it never auto-executes a write.
	StepExecutive
	// StepClarification: the planner could not resolve an intent (missing
	// fields / low confidence). The loop stops and surfaces the clarification.
	StepClarification
)

// StepOutcome records one executed agent step. It is the per-step unit the loop
// returns: which tool ran (advisory) or which plan was queued (executive), plus
// the human-facing summary the feedback turn is built from. Output is the raw
// read result carried forward into the final formatter's fact set.
type StepOutcome struct {
	Intent Intent
	Kind   AgentStepKind

	// Advisory fields.
	Tool   string         `json:"tool,omitempty"`
	Input  map[string]any `json:"input,omitempty"`
	Output map[string]any `json:"output,omitempty"`
	// Summary is the human-facing description of what this step did; it becomes
	// the assistant Turn fed back to the LLM so it can decide the next step.
	Summary string `json:"summary,omitempty"`

	// Executive (handoff) fields — mirrors a confirmation_required Response so
	// the wiring can emit it directly to the operator.
	PlanID            string          `json:"plan_id,omitempty"`
	Status            string          `json:"status,omitempty"`
	Version           uint            `json:"version,omitempty"`
	ExpiresAt         time.Time       `json:"expires_at,omitempty"`
	Blocks            []Block         `json:"blocks,omitempty"`
	ConfirmationToken string          `json:"-"`
	Trace             *AssistantTrace `json:"trace,omitempty"`

	// StepIndex is the zero-based position of this step within the run, used to
	// disambiguate repeated tool calls in the frontend timeline.
	StepIndex int `json:"step_index"`
}

// TerminalReason explains why the agent loop stopped, so the wiring can pick the
// right terminal Response shape.
type TerminalReason int

const (
	// TerminalDone: the planner emitted final_answer with a summary.
	TerminalDone TerminalReason = iota
	// TerminalMaxSteps: the loop reached maxSteps without the planner
	// concluding — emit the accumulated steps and a fallback answer.
	TerminalMaxSteps
	// TerminalHandoff: an executive step queued a plan for human approval.
	TerminalHandoff
	// TerminalClarification: the planner asked for missing parameters.
	TerminalClarification
)

// AgentRun is the outcome of a full agent-loop iteration.
type AgentRun struct {
	Steps       []StepOutcome
	Reason      TerminalReason
	FinalAnswer string // planner's final_answer summary (TerminalDone)
	Clarified   string // clarification message (TerminalClarification)
	Handoff     *StepOutcome

	// History is the full agent turn slice (seeded history + per-step feedback
	// turns) so the wiring can persist tool steps to the conversation.
	History []Turn
	Err     error
}

// AgentExecute is the execution callback the loop calls for each resolved
// intent. It turns an advisory intent into a StepOutcome (running the
// read/diagnostic) or an executive intent into a handoff StepOutcome (queuing a
// plan). The loop stays agnostic to execution internals so it is unit-testable
// with a fake planner + fake executor.
type AgentExecute func(intent Intent, stepIndex int) (StepOutcome, error)

// AgentLoop drives multi-step, read-only autonomous execution: it plans, runs
// advisory tools, feeds each result back into the planner's history, and
// replans until the planner concludes (final_answer), asks for clarification,
// queues a write for approval, or exhausts maxSteps. Writes are never executed
// by the loop — reaching one stops the loop and hands back to the human.
//
// When the wiring supplies a planStream callback (from a PlanStream-capable
// planner) plus an onEvent forwarder, each planning iteration streams — the
// loop consumes the LLM deltas/thinking and forwards them live to the client
// while still feeding results back between iterations. Without them the loop
// falls back to one-shot Planner.Plan per iteration.
type AgentLoop struct {
	planner    Planner
	execute    AgentExecute
	maxSteps   int
	planStream func(context.Context, identity.CurrentUser, string, []Turn, PageContext) (<-chan StreamEvent, error)
	onEvent    func(StreamEvent)
}

// NewAgentLoop builds an agent loop. execute must be non-nil. maxSteps bounds
// the number of tool invocations per request (>=1); values below 1 fall back to
// defaultAgentMaxSteps.
func NewAgentLoop(planner Planner, execute AgentExecute, maxSteps int) *AgentLoop {
	if maxSteps < 1 {
		maxSteps = defaultAgentMaxSteps
	}
	return &AgentLoop{planner: planner, execute: execute, maxSteps: maxSteps}
}

// WithStreaming wires a PlanStream-capable planner path so each planning
// iteration streams LLM deltas/thinking to the client via onEvent. When
// planStream is nil the loop uses one-shot Planner.Plan.
func (l *AgentLoop) WithStreaming(planStream func(context.Context, identity.CurrentUser, string, []Turn, PageContext) (<-chan StreamEvent, error), onEvent func(StreamEvent)) *AgentLoop {
	l.planStream = planStream
	l.onEvent = onEvent
	return l
}

// Run executes the loop. history is the conversation's prior turns (may be
// nil) that seed the agent history. The caller routes the returned AgentRun:
// each Advisory step is emitted to the UI and accumulated; Handoff / Clarified
// / Done dictate the terminal response.
func (l *AgentLoop) Run(ctx context.Context, user identity.CurrentUser, message string, history []Turn, pageContext PageContext) *AgentRun {
	run := &AgentRun{History: append([]Turn{}, history...)}
	consecutiveFailures := 0
	// seen records every advisory (read/diagnostic) execution in this run, keyed
	// by the tool plus the concrete resource it targeted. It is the
	// deterministic convergence backstop: if the planner repeats a read on a
	// resource it already executed this run, the loop concludes instead of
	// burning another step on the same data. Prompt feedback (feedbackText)
	// nudges well-behaved models to conclude on their own; this guard guarantees
	// it even when the model re-emits the same tool.
	seen := map[string]struct{}{}

	for step := 0; step < l.maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			run.Reason = TerminalMaxSteps
			run.Err = err
			return run
		}
		intent, err := l.planOnce(ctx, user, message, run.History, pageContext)
		if err != nil {
			var clarification ClarificationError
			if errors.As(err, &clarification) {
				run.Reason = TerminalClarification
				msg := strings.TrimSpace(clarification.Message)
				if msg == "" {
					msg = clarification.Error()
				}
				run.Clarified = msg
				return run
			}
			if errors.Is(err, ErrClarificationNeeded) {
				run.Reason = TerminalClarification
				run.Clarified = clarificationMessage
				return run
			}
			run.Reason = TerminalMaxSteps
			run.Err = err
			return run
		}
		// Terminal: planner concluded with a human-facing answer.
		if intent.Done {
			run.Reason = TerminalDone
			run.FinalAnswer = intent.Answer
			return run
		}
		// Write intent: stop and hand the pending plan to the human. The loop
		// never auto-executes a write.
		if isWriteIntent(intent) {
			out, execErr := l.execute(intent, step)
			if execErr != nil {
				run.Reason = TerminalMaxSteps
				run.Err = execErr
				return run
			}
			out.Kind = StepExecutive
			out.StepIndex = step
			run.Handoff = &out
			run.Steps = append(run.Steps, out)
			run.Reason = TerminalHandoff
			return run
		}
		// Convergence backstop: a read intent that replays a tool on a resource
		// already executed this run cannot move the diagnosis forward. Conclude
		// with the accumulated steps instead of re-running it.
		if key, ok := intentAdvisoryKey(intent); ok {
			if _, dup := seen[key]; dup && !isWriteIntent(intent) {
				run.Reason = TerminalDone
				run.FinalAnswer = stepsAnswer(run)
				return run
			}
		}
		// Advisory: run the read/diagnostic step.
		out, execErr := l.execute(intent, step)
		if execErr != nil {
			// Feed the error back so the planner can fall back to another tool,
			// but bound the retries to avoid an infinite failure loop.
			consecutiveFailures++
			if consecutiveFailures >= maxAgentFailures {
				run.Reason = TerminalMaxSteps
				run.Err = execErr
				return run
			}
			run.History = append(run.History, failureTurn(intent, execErr))
			continue
		}
		consecutiveFailures = 0
		out.Kind = StepAdvisory
		out.StepIndex = step
		run.Steps = append(run.Steps, out)
		if key, ok := intentAdvisoryKey(intent); ok {
			seen[key] = struct{}{}
		}
		// Feed the result back into the agent history so the next Plan() call
		// sees it (read-only chaining / fallback reasoning).
		run.History = append(run.History, feedbackTurn(intent, out))
	}
	run.Reason = TerminalMaxSteps
	return run
}

// intentAdvisoryKey canonicalizes an advisory (read/diagnostic) intent into a
// stable key identifying "which tool, on which resource". Diagnostics key on
// the resolved resource; read tools key on the tool name plus a
// deterministically-ordered JSON of their input so identical calls compare
// equal regardless of map key order. It returns ok=false for write/terminal
// intents that should never be de-duplicated away.
func intentAdvisoryKey(intent Intent) (string, bool) {
	if intent.Diagnostic != nil {
		d := intent.Diagnostic
		return "diag:" + strings.Join([]string{
			d.Domain, d.Environment, d.ResourceType, d.ResourceName,
		}, "\x00"), true
	}
	if intent.ToolName == "" {
		return "", false
	}
	// Terminal non-executable intents and write tools must not be collapsed.
	if intent.Done || isWriteIntent(intent) {
		return "", false
	}
	ordered, err := json.Marshal(intent.Input)
	if err != nil {
		return "", false
	}
	return "tool:" + intent.ToolName + "\x00" + string(ordered), true
}

// isWriteIntent reports whether an intent targets a write tool. It is the
// loop's own classifier (advisory-safe even when Intent.Type is unset), falling
// back to the tool registry's Operation.
func isWriteIntent(intent Intent) bool {
	if intent.ToolName == "" {
		return false
	}
	if ClassifyIntent(intent) == IntentExecutive {
		return true
	}
	tool, ok := tools.Lookup(intent.ToolName)
	if !ok {
		return false
	}
	return tool.Operation == tools.Write
}

// planOnce resolves one planning iteration. When streaming is wired, it
// consumes the plan stream — forwarding deltas/thinking to the client via
// onEvent — and returns the terminal intent/error. Otherwise it calls the
// planner's one-shot Plan directly.
func (l *AgentLoop) planOnce(ctx context.Context, user identity.CurrentUser, message string, history []Turn, pageContext PageContext) (Intent, error) {
	if l.planStream == nil {
		return l.planner.Plan(ctx, user, message, history, pageContext)
	}
	events, err := l.planStream(ctx, user, message, history, pageContext)
	if err != nil {
		return l.planner.Plan(ctx, user, message, history, pageContext)
	}
	var intent Intent
	for ev := range events {
		// Forward non-terminal deltas/thinking to the client while planning.
		if !ev.Done && l.onEvent != nil {
			if ev.Delta != "" || ev.Thinking != "" {
				l.onEvent(ev)
			}
		}
		if ev.Done {
			if ev.Err != nil {
				return Intent{}, ev.Err
			}
			if ev.Intent != nil {
				intent = *ev.Intent
			}
		}
	}
	return intent, nil
}

// feedbackTurn wraps an executed advisory step as an assistant Turn carrying the
// tool result, so the next Plan() sees what the previous tool returned. The
// content is explicit about the tool already having run and returned a definite
// value, and instructs the planner to conclude (final_answer) when that result
// answers the question — a single-domain diagnostic that already returned data is
// conclusive, not a reason to re-run the same tool.
func feedbackTurn(intent Intent, out StepOutcome) Turn {
	return Turn{
		Role:         "assistant",
		ResponseType: "tool_step",
		Content:      feedbackText(out),
		Intent:       &intent,
	}
}

// feedbackText renders the human-visible feedback for an executed step. When
// present, it includes the step summary so the replanning LLM can see the actual
// result. The trailing instruction tells the planner to emit final_answer when
// the returned data already answers the user's question, instead of repeating the
// same tool.
func feedbackText(out StepOutcome) string {
	if out.Tool == "" || out.Summary == "" {
		return "工具已执行。若结果已回答用户问题，请直接 final_answer: true 总结收尾，不要重复执行已执行过的工具。"
	}
	return out.Tool + " 已执行并返回结果：" + out.Summary +
		"。若此结果已回答用户问题，请直接 final_answer: true 并给出面向用户的中文 summary 收尾；不要重复执行同域/同资源的相同工具。"
}

// failureTurn wraps a failed advisory step as an assistant Turn signalling the
// error, letting the planner pick a fallback tool within its step budget.
func failureTurn(intent Intent, err error) Turn {
	return Turn{
		Role:         "assistant",
		ResponseType: "tool_step",
		Content:      "工具执行失败：" + err.Error() + "。请选择其他候选工具继续排查，或直接给出最终答复。",
		Intent:       &intent,
	}
}
