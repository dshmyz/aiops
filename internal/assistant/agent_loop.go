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
	// sequence is an optional product-declared evidence-collection order (from a
	// matched runbook's tool_sequence, e.g. alert-root-cause-sequence →
	// [alert.query, event.query]). When set, the loop treats it as a conclusion
	// gate: a model final_answer is not honored while sequence members remain
	// untouched — the loop steers the planner back to collect them. Declared
	// sequence steps that cannot resolve are best-effort skipped rather than
	// stalling the loop.
	sequence []string
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

// WithRunbookSequence declares a product-level evidence-collection order that the
// loop must respect before honoring a model final_answer. Members are matched
// against advisory tool names (a diagnostic step's resolved tool name counts).
// It turns "alert root cause collects a few checks" from model improvisation
// into a declared plan order while still letting the model insert extra steps.
func (l *AgentLoop) WithRunbookSequence(sequence []string) *AgentLoop {
	l.sequence = append([]string{}, sequence...)
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
	// sequenceTouched records which declared runbook-sequence members have been
	// executed this run, advanced by advisory steps whose (resolved) tool name
	// matches a member.
	sequenceTouched := map[string]bool{}

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
				run.Clarified = clarifyWithCheckedSteps(run, msg)
				return run
			}
			if errors.Is(err, ErrClarificationNeeded) {
				run.Reason = TerminalClarification
				run.Clarified = clarifyWithCheckedSteps(run, clarificationMessage)
				return run
			}
			run.Reason = TerminalMaxSteps
			run.Err = err
			return run
		}
		// Terminal: planner concluded with a human-facing answer.
		if intent.Done {
			// A declared runbook sequence (② chain skeleton) is a product-level
			// conclusion gate: when the model calls final_answer BEFORE the
			// declared evidence members have all been collected, the loop does not
			// end — it steers the planner back to collect the remaining declared
			// steps, turning "alert root cause collects a few checks" into a
			// declared order instead of letting the model conclude on a whim. Only
			// when the sequence is satisfied (or never declared) is the final_answer
			// honored.
			if pending := l.pendingSequenceMembers(sequenceTouched); len(pending) > 0 {
				run.History = append(run.History, sequenceSteerTurn(pending))
				continue
			}
			run.Reason = TerminalDone
			run.FinalAnswer = intent.Answer
			return run
		}
		// Write intent: execute it. A non-admitted write returns an executive
		// (handoff) StepOutcome — stop and hand the pending plan to the human.
		// A low-risk write admitted by the Low-Risk Admission Controller returns
		// an advisory StepOutcome and the loop continues.
		if isWriteIntent(intent) {
			out, execErr := l.execute(intent, step)
			if execErr != nil {
				run.Reason = TerminalMaxSteps
				run.Err = execErr
				return run
			}
			out.StepIndex = step
			if out.Kind != StepAdvisory {
				// 硬禁止默认：写操作不自动执行，交出待确认 plan。
				out.Kind = StepExecutive
				run.Handoff = &out
				run.Steps = append(run.Steps, out)
				run.Reason = TerminalHandoff
				return run
			}
			// 准入放行的低风险写：作为 advisory 步骤继续循环。
			run.Steps = append(run.Steps, out)
			if key, ok := intentAdvisoryKey(intent); ok {
				seen[key] = struct{}{}
			}
			sequenceTrackTouched(sequenceTouched, l.sequence, out.Tool)
			continue
		}
		// Convergence backstop: a read intent that replays a tool on a resource
		// already executed this run cannot move the diagnosis forward. Conclude
		// with the accumulated steps instead of re-running it.
		if key, ok := intentAdvisoryKey(intent); ok {
			if _, dup := seen[key]; dup && !isWriteIntent(intent) {
				run.Reason = TerminalDone
				run.Fallback = true // synthesized summary, not a model final_answer
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
		sequenceTrackTouched(sequenceTouched, l.sequence, out.Tool)
		// Execution-layer short-circuit for single-turn diagnostics: a
		// single-domain diagnostic (e.g. "检查 kafka 健康吗") that ran
		// successfully is structurally conclusive — it already answered the
		// question, so looping back to re-plan (letting the model self-enforce a
		// final_answer) only burns extra LLM turns. This moves "single-turn
		// question → done" from model discipline to a deterministic check.
		// Multi-domain messages and explicit continuation cues ("然后/再/对比/
		// 延伸") are exempt so genuine investigation chains still run.
		if shouldShortCircuitSingleDiagnostic(message, intent, out) {
			run.Reason = TerminalDone
			run.FinalAnswer = singleDiagnosticAnswer(out)
			return run
		}
		// Feed the result back into the agent history so the next Plan() call
		// sees it (read-only chaining / fallback reasoning).
		run.History = append(run.History, feedbackTurn(intent, out))
	}
	run.Reason = TerminalMaxSteps
	run.Fallback = true // ran out of steps without a final_answer; summary is synthesized
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
