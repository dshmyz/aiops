package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// defaultAgentMaxSteps is the default number of tool invocations per agent
// loop request. Overridable via COPILOT_ASSISTANT_MAX_STEPS.
const defaultAgentMaxSteps = 8

// defaultMaxControlSteps bounds how many steering / failure-feedback turns are
// tolerated within a single run before giving up, regardless of remaining
// execution steps. Overridable via COPILOT_AGENT_MAX_CONTROL_STEPS.
const defaultMaxControlSteps = 6

// maxAgentStepsHardCap bounds intent-driven exec-budget raises: no matter how
// many steps the planner suggests, the loop never runs more tool calls than
// this. Product-declared runbook sequences are exempt (their floor is an
// explicit contract, not a model estimate).
const maxAgentStepsHardCap = 16

// runbookSequenceStepMargin is added on top of a declared runbook sequence's
// length when computing the exec-budget floor: the model legitimately inserts
// extra steps (re-reads, related resource checks) beyond the declared order.
const runbookSequenceStepMargin = 3

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

	// Err holds the raw failure text when the step was denied or failed. Empty
	// for successful steps. Persisted into the tool_step payload so denied and
	// failed tool calls stay visible in the frontend timeline and audit.
	Err string `json:"error,omitempty"`

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
	// TerminalControlExhausted: steering or failure-feedback turns consumed
	// the control step budget before the execution budget was spent — the
	// loop could not make progress because replanning itself was too costly.
	TerminalControlExhausted
)

// AgentRun is the outcome of a full agent-loop iteration.
type AgentRun struct {
	Steps       []StepOutcome
	Reason      TerminalReason
	FinalAnswer string // planner's final_answer summary (TerminalDone)
	Clarified   string // clarification message (TerminalClarification)
	// ClarifiedFields 是缺参澄清的结构化表单字段（TerminalClarification），
	// 由 planner 的 ClarificationError.Fields 透传，终态映射据此渲染
	// approval_form block；为空时仅有自然语言消息。
	ClarifiedFields []PreflightField
	Handoff     *StepOutcome
	// Fallback is set when the loop concluded WITHOUT the planner emitting a
	// final_answer — it ran out of steps (TerminalMaxSteps) or hit a repeated-read
	// convergence backstop. In both cases FinalAnswer is a synthesized summary of
	// the executed steps, not a model-authored conclusion; the wiring surfaces this
	// distinctly so the operator does not mistake it for completed multi-step
	// reasoning.
	Fallback bool

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

// StepState drives the loop's next iteration.
type StepState int

const (
	// StateSteer: a control step (sequence steering or failure feedback) was
	// appended; the loop continues with another planOnce.
	StateSteer StepState = iota
	// StateExec: the intent should be executed.
	StateExec
	// StateDone: the run has reached a terminal outcome (written to run.Reason).
	StateDone
	// StateHandoff: the intent is a write that requires human approval.
	StateHandoff
	// StateClarify: the planner needs more information.
	StateClarify
)

// stepBudget tracks execution vs control step consumption independently. Exec
// steps are real tool invocations; control steps are replanning overhead
// (sequence steers, failure retries) that must not eat the execution budget.
type stepBudget struct {
	exec          int
	execLimit     int
	control       int
	controlLimit  int
}

func (b *stepBudget) canExec() bool    { return b.exec < b.execLimit }
func (b *stepBudget) canControl() bool { return b.control < b.controlLimit }
func (b *stepBudget) consumeExec()     { b.exec++ }
func (b *stepBudget) consumeControl()  { b.control++ }

// AgentLoop drives multi-step, read-only autonomous execution: it plans, runs
// advisory tools, feeds each result back into the planner's history, and
// replans until the planner concludes (final_answer), asks for clarification,
// queues a write for approval, or exhausts the execution budget. Writes are
// never executed by the loop — reaching one stops the loop and hands back to
// the human.
//
// Execution steps (real tool invocations) and control steps (steering /
// failure feedback) are budgeted independently, so replanning overhead cannot
// starve tool execution.
type AgentLoop struct {
	planner    Planner
	execute    AgentExecute
	maxSteps   int
	// maxControlSteps bounds how many steering / failure-feedback turns the loop
	// tolerates within a single run before giving up. Control turns are
	// replanning overhead (sequence steers, error retries) that should not eat
	// the execution-step budget.
	maxControlSteps int
	planStream      func(context.Context, identity.CurrentUser, string, []Turn, PageContext) (<-chan StreamEvent, error)
	onEvent         func(StreamEvent)
	sequence        []string
}

// NewAgentLoop builds an agent loop. execute must be non-nil. maxSteps bounds
// the number of tool invocations per request (>=1); values below 1 fall back to
// defaultAgentMaxSteps.
func NewAgentLoop(planner Planner, execute AgentExecute, maxSteps int) *AgentLoop {
	if maxSteps < 1 {
		maxSteps = defaultAgentMaxSteps
	}
	return &AgentLoop{planner: planner, execute: execute, maxSteps: maxSteps, maxControlSteps: defaultMaxControlSteps}
}

// WithMaxControlSteps overrides the default control step budget. Control steps
// are steering and failure-feedback turns that should not eat the execution
// budget; this limit is a safety backstop against replanning spin.
func (l *AgentLoop) WithMaxControlSteps(n int) *AgentLoop {
	if n < 1 {
		n = defaultMaxControlSteps
	}
	l.maxControlSteps = n
	return l
}

// WithStreaming wires a PlanStream-capable planner path so each planning
// iteration streams LLM deltas/thinking to the client via onEvent. When
// planStream is nil the loop uses one-shot Planner.Plan.
func (l *AgentLoop) WithStreaming(planStream func(context.Context, identity.CurrentUser, string, []Turn, PageContext) (<-chan StreamEvent, error), onEvent func(StreamEvent)) *AgentLoop {
	l.planStream = planStream
	l.onEvent = onEvent
	return l
}

// WithRunbookSequence declares a product-level evidence-collection order that the
// loop must respect before honoring a model final_answer. Members are matched
// against advisory tool names (a diagnostic step's resolved tool name counts).
// It turns "alert root cause collects a few checks" from model improvisation
// into a declared plan order while still letting the model insert extra steps.
func (l *AgentLoop) WithRunbookSequence(sequence []string) *AgentLoop {
	l.sequence = append([]string{}, sequence...)
	return l
}

// initialExecLimit computes the exec budget at loop start. A declared runbook
// sequence is a structural floor: its members MUST be touched before a
// final_answer is honored, so the budget must at least cover the sequence plus
// a margin for steps the model inserts beyond the declared order.
func (l *AgentLoop) initialExecLimit() int {
	limit := l.maxSteps
	if n := len(l.sequence); n > 0 {
		if floor := n + runbookSequenceStepMargin; floor > limit {
			limit = floor
		}
	}
	return limit
}

// applyIntentBudget lets the planner's first execution intent raise the exec
// budget: the model self-assesses SuggestedSteps tool calls for the question.
// Raise-only — a lower self-assessment never shrinks the budget (convergence
// already ends simple questions early; underestimating must not truncate a
// deep investigation). Applied exactly once, before the first exec step is
// consumed, and clamped to maxAgentStepsHardCap.
func (l *AgentLoop) applyIntentBudget(budget *stepBudget, intent Intent) {
	if budget.exec != 0 || intent.SuggestedSteps <= 0 {
		return
	}
	s := intent.SuggestedSteps
	if s > maxAgentStepsHardCap {
		s = maxAgentStepsHardCap
	}
	if s > budget.execLimit {
		budget.execLimit = s
	}
}

// Run executes the loop. history is the conversation's prior turns (may be
// nil) that seed the agent history. The caller routes the returned AgentRun:
// each Advisory step is emitted to the UI and accumulated; Handoff / Clarified
// / Done dictate the terminal response.
func (l *AgentLoop) Run(ctx context.Context, user identity.CurrentUser, message string, history []Turn, pageContext PageContext) *AgentRun {
	run := &AgentRun{History: append([]Turn{}, history...)}
	consecutiveFailures := 0
	seen := map[string]struct{}{}
	sequenceTouched := map[string]bool{}
	budget := stepBudget{execLimit: l.initialExecLimit(), controlLimit: l.maxControlSteps}

	_ = consecutiveFailures // used in executeAndEvaluate
	for budget.canExec() || budget.canControl() {
		if err := ctx.Err(); err != nil {
			run.Reason = TerminalMaxSteps
			run.Err = err
			return run
		}
		intent, planErr := l.planOnce(ctx, user, message, run.History, pageContext)
		state := l.classify(intent, planErr, seen, sequenceTouched, run)
		if state == StateExec {
			l.applyIntentBudget(&budget, intent)
		}
		if state != StateExec {
			if l.handleNonExec(run, state, intent, planErr, &budget, sequenceTouched) {
				return run
			}
			// StateSteer consumed a control step; re-check.
			if !budget.canExec() && !budget.canControl() {
				run.Reason = TerminalControlExhausted
				run.Fallback = true
				return run
			}
			continue
		}
		if l.executeAndEvaluate(run, intent, &budget, seen, sequenceTouched, message, &consecutiveFailures) {
			return run
		}
		// After a successful exec step, check if we can still steer. When
		// control budget is exhausted, the next classify may return StateDone
		// (planner concludes) even though the sequence wasn't fully satisfied —
		// the user got a partial result, not a controlled completion.
		if !budget.canExec() {
			run.Reason = TerminalMaxSteps
			run.Fallback = true
			return run
		}
		if !budget.canControl() && len(run.Steps) > 0 {
			// Control budget spent but we have executed steps. The planner
			// may still conclude (StateDone), but we mark the run so the
			// wiring can badge it as a controlled partial result.
			run.Reason = TerminalControlExhausted
			run.Fallback = true
			// Don't return — let the next planOnce run; classify will set
			// its own reason which takes precedence if it's non-zero.
		}
	}
	// Budget exhausted. Determine whether execution or control ran out first.
	if !budget.canExec() {
		run.Reason = TerminalMaxSteps
		run.Fallback = true
	} else {
		run.Reason = TerminalControlExhausted
		run.Fallback = true
	}
	return run
}

// classify inspects the planner output (intent + error) and returns the
// appropriate next state. It does not mutate any loop state.
func (l *AgentLoop) classify(intent Intent, planErr error, seen map[string]struct{}, sequenceTouched map[string]bool, run *AgentRun) StepState {
	if planErr != nil {
		var clarification ClarificationError
		if errors.As(planErr, &clarification) {
			msg := strings.TrimSpace(clarification.Message)
			if msg == "" {
				msg = clarification.Error()
			}
			run.Clarified = clarifyWithCheckedSteps(run, msg)
			run.ClarifiedFields = clarification.Fields
			return StateClarify
		}
		if errors.Is(planErr, ErrClarificationNeeded) {
			run.Clarified = clarifyWithCheckedSteps(run, clarificationMessage)
			return StateClarify
		}
		run.Err = planErr
		run.Reason = TerminalMaxSteps
		return StateDone
	}
	// Planner concluded with a human-facing answer.
	if intent.Done {
		if pending := l.pendingSequenceMembers(sequenceTouched); len(pending) > 0 {
			return StateSteer
		}
		// Only set TerminalDone if no more specific reason was already set
		// (e.g. TerminalControlExhausted from the previous iteration).
		if run.Reason == 0 {
			run.Reason = TerminalDone
		}
		run.FinalAnswer = intent.Answer
		return StateDone
	}
	// Write intent: execute it. An admitted low-risk write returns an advisory
	// StepOutcome; anything else hands off for human approval.
	if isWriteIntent(intent) {
		return StateExec
	}
	// Convergence backstop: a read on a resource already executed this run
	// cannot move the diagnosis forward.
	if key, ok := intentAdvisoryKey(intent); ok {
		if _, dup := seen[key]; dup {
			run.Reason = TerminalDone
			run.Fallback = true
			run.FinalAnswer = stepsAnswer(run)
			return StateDone
		}
	}
	return StateExec
}

// handleNonExec processes a non-execution state (steer, done, clarify) and
// returns true when the caller should return the run. Returns false only for
// StateSteer (loop continues after appending the steering turn).
func (l *AgentLoop) handleNonExec(run *AgentRun, state StepState, intent Intent, planErr error, budget *stepBudget, sequenceTouched map[string]bool) bool {
	switch state {
	case StateSteer:
		if !budget.canControl() {
			run.Reason = TerminalControlExhausted
			run.Fallback = true
			return true
		}
		budget.consumeControl()
		if pending := l.pendingSequenceMembers(sequenceTouched); len(pending) > 0 {
			run.History = append(run.History, sequenceSteerTurn(pending))
		}
		return false
	case StateClarify:
		run.Reason = TerminalClarification
		return true
	case StateDone:
		// classify always sets run.Reason for StateDone; no override needed.
		return true
	case StateHandoff:
		run.Reason = TerminalHandoff
		return true
	default:
		return true
	}
}

// executeAndEvaluate runs the intent as an advisory step, feeds the result
// (or error) back, and returns true when the caller should return the run.
func (l *AgentLoop) executeAndEvaluate(run *AgentRun, intent Intent, budget *stepBudget, seen map[string]struct{}, sequenceTouched map[string]bool, message string, consecutiveFailures *int) bool {
	// When control budget is exhausted, no further replanning is possible.
	// The run terminates here — either as a controlled partial result (if we
	// have steps) or as a control-exhausted failure (if we don't).
	if !budget.canControl() {
		run.Reason = TerminalControlExhausted
		run.Fallback = true
		return true
	}
	step := budget.exec
	out, execErr := l.execute(intent, step)
	if execErr != nil {
		// Persist the failed attempt as an advisory step so denied/failed tool
		// calls stay visible in the timeline; it carries the raw error and input.
		// It does not consume the exec budget, mark `seen`, or feed feedbackTurn
		// — retry and dedup semantics are unchanged from before.
		failed := StepOutcome{
			Intent:    intent,
			Kind:      StepAdvisory,
			StepIndex: step,
			Tool:      intent.ToolName,
			Input:     intent.Input,
			Err:       execErr.Error(),
			Summary:   "工具执行失败：" + execErr.Error(),
		}
		if intent.Diagnostic != nil {
			failed.Tool = intent.Diagnostic.Domain
		}
		run.Steps = append(run.Steps, failed)
		(*consecutiveFailures)++
		if *consecutiveFailures >= maxAgentFailures {
			run.Reason = TerminalMaxSteps
			run.Err = execErr
			return true
		}
		if budget.canControl() {
			budget.consumeControl()
		}
		run.History = append(run.History, failureTurn(intent, execErr))
		return false
	}
	(*consecutiveFailures) = 0
	budget.consumeExec()
	out.Kind = StepAdvisory
	out.StepIndex = step
	run.Steps = append(run.Steps, out)
	if key, ok := intentAdvisoryKey(intent); ok {
		seen[key] = struct{}{}
	}
	sequenceTrackTouched(sequenceTouched, l.sequence, out.Tool)
	if shouldShortCircuitSingleDiagnostic(message, intent, out) {
		run.Reason = TerminalDone
		run.FinalAnswer = singleDiagnosticAnswer(out)
		return true
	}
	run.History = append(run.History, feedbackTurn(intent, out))
	return false
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
// result. When the step is a diagnostic (the step output carries severity,
// per-observation and per-finding structure), those concrete findings are
// appended line by line so the replanning LLM — and any later step that must
// converge into the SOP's "候选根因表/最可能根因" (alert-evidence-checklist) —
// actually sees the evidence the previous diagnosis surfaced, rather than a
// one-line summary. The trailing instruction tells the planner to emit
// final_answer when the returned data already answers the user's question,
// instead of repeating the same tool.
func feedbackText(out StepOutcome) string {
	conclude := "，不要重复执行同域/同资源的相同工具"
	if out.Tool == "" || out.Summary == "" {
		return "工具已执行。若结果已回答用户问题，请直接 final_answer: true 总结收尾，不要重复执行已执行过的工具。"
	}
	text := out.Tool + " 已执行并返回结果：" + out.Summary +
		"。若此结果已回答用户问题，请直接 final_answer: true 并给出面向用户的中文 summary 收尾" + conclude + "。"
	if extra := diagnosticFeedback(out.Output); extra != "" {
		text += "\n本次诊断的取证结果如下，供后续步骤引用（涉及候选根因时需据实给出证据链，不得编造）：\n" + extra
	}
	return text
}

// diagnosticFeedback renders the structured findings of a diagnostic step
// (severity, per-observation and per-finding lines carried in the step output)
// as a compact evidence block. It returns "" for non-diagnostic steps so the
// feedback for a plain read stays one line.
func diagnosticFeedback(output map[string]any) string {
	if len(output) == 0 {
		return ""
	}
	severity, hasSev := output["severity"].(string)
	obs, hasObs := output["observations"].([]string)
	findings, hasFindings := output["findings"].([]string)
	domains, hasDomains := output["domains"].([]string)
	if !hasSev && !hasObs && !hasFindings {
		return ""
	}
	var b strings.Builder
	if hasDomains && len(domains) > 0 {
		fmt.Fprintf(&b, "- 诊断域：%s\n", strings.Join(domains, ", "))
	}
	if hasSev {
		fmt.Fprintf(&b, "- 综合严重级别：%s\n", severity)
	}
	for _, line := range obs {
		if strings.TrimSpace(line) != "" {
			fmt.Fprintf(&b, "- 观察：%s\n", line)
		}
	}
	for _, line := range findings {
		if strings.TrimSpace(line) != "" {
			fmt.Fprintf(&b, "- 结论：%s\n", line)
		}
	}
	return strings.TrimRight(b.String(), "\n")
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

// continuationCues are explicit markers that the user wants an investigation
// chain rather than a one-shot single-domain answer. When present, a single
// diagnostic is NOT structurally conclusive — the model should be allowed to
// keep planning follow-up steps.
var continuationCues = []string{"继续", "然后", "再", "对比", "比较", "延伸", "顺便", "以及", "连带", "看下", "看看", "接着", "之后", "逐个", "都查"}

// shouldShortCircuitSingleDiagnostic reports whether a successfully executed
// single-domain diagnostic step is structurally conclusive for this request, so
// the loop can conclude instead of re-planning (avoiding a full extra LLM turn
// for what is, semantically, a single question). It is conservative: it only
// fires for diagnostic intents, requires the message to name exactly one domain,
// and exempts messages that express an explicit continuation/investigation
// intent. Multi-domain requests always keep the loop (the Orchestrator already
// fanned out inside the single diagnostic step).
func shouldShortCircuitSingleDiagnostic(message string, intent Intent, out StepOutcome) bool {
	if intent.Diagnostic == nil {
		return false
	}
	// Only conclude when the step actually produced a package (already satisfied
	// by the success path, but guard against an empty/broken output).
	if len(out.Output) == 0 {
		return false
	}
	if isMultiDomainDiagnostic(message) {
		return false
	}
	text := strings.ToLower(message)
	for _, cue := range continuationCues {
		if strings.Contains(text, cue) {
			return false
		}
	}
	return true
}

// singleDiagnosticAnswer builds the operator-facing conclusion for a
// short-circuited single-domain diagnostic. It prefers the executed step's
// structured summary (severity + resource + observation), falling back to the
// tool name so the answer is never empty.
func singleDiagnosticAnswer(out StepOutcome) string {
	if s, ok := out.Output["summary"].(string); ok && s != "" {
		return s
	}
	if out.Summary != "" {
		return out.Summary
	}
	if out.Tool != "" {
		return out.Tool + " 已诊断完成。"
	}
	return "诊断完成。"
}

// pendingSequenceMembers returns the declared runbook-sequence members that have
// not yet been satisfied by an executed advisory step this run. An empty slice
// means the sequence (if any) is complete.
func (l *AgentLoop) pendingSequenceMembers(touched map[string]bool) []string {
	if len(l.sequence) == 0 {
		return nil
	}
	var pending []string
	for _, m := range l.sequence {
		if m == "" {
			continue
		}
		if !touched[m] {
			pending = append(pending, m)
		}
	}
	return pending
}

// sequenceTrackTouched advances the sequence-touched set: an advisory step whose
// resolved tool name matches a declared member (exact match, else substring so a
// domain diagnostic satisfies a member naming that domain) marks that member
// satisfied. Sequence members that can't be matched (unregistered tools) simply
// stay pending and fail safe to the honest fallback.
func sequenceTrackTouched(touched map[string]bool, sequence []string, toolName string) {
	if len(sequence) == 0 || toolName == "" {
		return
	}
	for _, m := range sequence {
		if m == "" {
			continue
		}
		if toolName == m || strings.Contains(toolName, m) || strings.Contains(m, toolName) {
			touched[m] = true
		}
	}
}

// sequenceSteerTurn is the feedback turn sent when the model emits final_answer
// before a declared runbook sequence is complete. It explicitly names the
// remaining declared steps so the planner collects them rather than concluding
// prematurely.
func sequenceSteerTurn(pending []string) Turn {
	return Turn{
		Role:         "assistant",
		ResponseType: "tool_step",
		Content: "告警根因序列尚未收集齐声明的证据步骤，请先执行下列步骤完成取证：" + strings.Join(pending, "、") +
			"。全部执行完后再 final_answer: true 总结收尾。",
	}
}

// clarifyWithCheckedSteps appends the advisory steps already run before the
// clarification, so the operator sees what diagnostics already completed rather
// than treating the clarification as a cold prompt. When no step has run yet the
// message is returned unchanged (the fresh-prompt case).
func clarifyWithCheckedSteps(run *AgentRun, msg string) string {
	var ran []string
	for _, out := range run.Steps {
		if out.Kind != StepAdvisory {
			continue
		}
		if s := strings.TrimSpace(out.Summary); s != "" {
			ran = append(ran, s)
		}
	}
	if len(ran) == 0 || msg == "" {
		return msg
	}
	return msg + "（此前已检查：" + strings.Join(ran, "；") + "）"
}
