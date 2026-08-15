package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/autonomy"
	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// responseTypeToolStep marks a persisted assistant turn that records one
// agent-loop tool invocation. The turn's payload carries a ToolFact
// (tool/input/result) plus step_index so the conversation can be replayed and
// audited without re-running the underlying capability.
const responseTypeToolStep = "tool_step"

// responseTypeFallbackAnswer marks a terminal answer that was synthesized by the
// loop's fallback (maxSteps exhaustion or repeated-read convergence) rather than
// authored as a model final_answer. Persisted turns and the streamed Response use
// it so the frontend can badge it distinctly: the operator must not mistake it for
// completed multi-step reasoning.
const responseTypeFallbackAnswer = "answer_converged"

// agentPlanner is the subset of Planner the loop's streaming wiring needs. A
// planner that implements PlanStream (e.g. *EinoPlanner) supports the
// multi-step agent loop; the deterministic planner does not and keeps the
// single-plan path.
type agentPlanner interface {
	Planner
	PlanStream(context.Context, identity.CurrentUser, string, []Turn, PageContext) (<-chan StreamEvent, error)
}

// agentCapable reports whether s.planner can drive the autonomous agent loop,
// and only when the loop is explicitly enabled via WithAgentLoop. Only LLM
// planners with PlanStream support result-feedback replanning; the deterministic
// rule-based planner ignores history, so running it in a loop would just replay
// the same intent. The loop is opt-in so existing single-plan streaming
// semantics (and their tests) are preserved unless the caller enables it.
func (s *Service) agentEnabled() bool {
	return s.agentLoopEnabled && agentPlannerCapable(s.planner)
}

// plannerUnwrapper lets a wrapper planner expose the planner it delegates to,
// so capability probing can look through the wrapper chain at the innermost
// planner.
type plannerUnwrapper interface{ UnwrapPlanner() Planner }

func agentPlannerCapable(planner Planner) bool {
	for {
		if _, ok := planner.(agentPlanner); ok {
			return true
		}
		u, ok := planner.(plannerUnwrapper)
		if !ok || u.UnwrapPlanner() == nil {
			return false
		}
		planner = u.UnwrapPlanner()
	}
}

// executeAgentStep is the loop's execution callback. It resolves one planner
// intent through the existing policy / diagnostics / read / write pipeline and
// returns a StepOutcome. It deliberately does NOT run the two-stage LLM
// formatter per step — each step's Summary is a lightweight human summary of
// the raw result, and only the final aggregated answer is formatted. This keeps
// a multi-step loop from burning an LLM formatting call per step.
//
// Write intents short-circuit through agentWriteStep: a low-risk write admitted
// by the Low-Risk Admission Controller auto-executes as an advisory step; all
// other writes queue a pending plan as a handoff StepOutcome and stop the loop.
func (s *Service) executeAgentStep(ctx context.Context, user identity.CurrentUser, message string, intent Intent, stepIndex int) (StepOutcome, error) {
	out := StepOutcome{
		Intent:    intent,
		StepIndex: stepIndex,
	}
	if isWriteIntent(intent) {
		return s.agentWriteStep(ctx, user, message, intent)
	}
	if intent.Diagnostic != nil {
		return s.agentDiagnosticStep(ctx, user, intent, out)
	}
	// Read tool.
	if intent.ToolName == "" {
		return StepOutcome{}, ErrClarificationNeeded
	}
	tool, ok := tools.Lookup(intent.ToolName)
	if !ok {
		return StepOutcome{}, fmt.Errorf("%w: %s", ErrPolicyDenied, policy.ToolNotRegistered)
	}
	decision := policy.Evaluate(user, tool, intent.Input)
	if !decision.Allowed {
		return StepOutcome{}, fmt.Errorf("%w: %s", ErrPolicyDenied, decision.Reason)
	}
	if s.reads == nil {
		return StepOutcome{}, errors.New("read service is required")
	}
	out.Tool = tool.Name
	out.Input = intent.Input
	// Attribute this read to its loop step for audit.
	answer, err := s.reads.ExecuteRead(ctx, user, tool.Name, intent.Input)
	if err != nil {
		return StepOutcome{}, err
	}
	out.Kind = StepAdvisory
	out.Output = answer
	out.Summary = stepReadSummary(tool.Name, answer)
	return out, nil
}

// resolveDiagnosticToolName resolves a diagnostic request to a stable,
// displayable read tool name for progress events. It prefers a capability-backed
// resolver when the configured diagnostics runner exposes one (e.g. the
// orchestrator delegating to a *diagnostics.Service with a
// DiagnosticCapabilityResolver), falls back to the runner's own resolution, and
// finally to the domain itself. It deliberately never fails: the name only drives
// progress/timeline display, and treating a resolution miss here as an error
// would wrongly block a diagnostic that can otherwise execute.
func resolveDiagnosticToolName(d DiagnosticRunner, request diagnostics.Request) string {
	if r, ok := d.(interface {
		ResolveReadTool(diagnostics.Request) (string, error)
	}); ok {
		if name, err := r.ResolveReadTool(request); err == nil && name != "" {
			return name
		}
	}
	if domain := strings.TrimSpace(request.Domain); domain != "" {
		return domain
	}
	return "diagnostic"
}

// agentDiagnosticStep executes a diagnostic intent in the loop, capturing the
// package as the step output.
func (s *Service) agentDiagnosticStep(ctx context.Context, user identity.CurrentUser, intent Intent, out StepOutcome) (StepOutcome, error) {
	if s.diagnostics == nil {
		return StepOutcome{}, errors.New("diagnostic service is required")
	}
	toolName := resolveDiagnosticToolName(s.diagnostics, *intent.Diagnostic)
	pkg, err := s.diagnostics.Run(ctx, user, *intent.Diagnostic)
	if err != nil {
		return StepOutcome{}, err
	}
	out.Tool = toolName
	out.Input = map[string]any{"domain": intent.Diagnostic.Domain, "environment": intent.Diagnostic.Environment}
	out.Output = map[string]any{
		"summary":         diagnosticStepSummary(pkg),
		"environment":     pkg.Environment,
		"domains":         pkg.Domains,
		"severity":        packageSeverity(pkg),
		"observations":    observationSummaries(pkg),
		"findings":        packageFindings(pkg),
		"recommendations": len(pkg.Recommendations),
	}
	out.Summary = stepReadSummary(toolName, out.Output)
	return out, nil
}

// agentWriteStep executes a write intent in the loop. Default behavior: queue a
// pending plan and return the handoff StepOutcome (the loop stops and hands to
// the human). E2: for a low-risk write admitted by the Low-Risk Admission
// Controller, it auto-executes instead and returns an advisory-step outcome so
// the loop continues. It mirrors executeFromIntent's write path: policy is
// applied, a plan is created, dry-run preview attached.
func (s *Service) agentWriteStep(ctx context.Context, user identity.CurrentUser, message string, intent Intent) (StepOutcome, error) {
	if intent.ToolName == "" {
		return StepOutcome{}, ErrClarificationNeeded
	}
	if s.plans == nil {
		return StepOutcome{}, errors.New("plan service is required")
	}
	tool, ok := tools.Lookup(intent.ToolName)
	if !ok {
		return StepOutcome{}, fmt.Errorf("%w: %s", ErrPolicyDenied, policy.ToolNotRegistered)
	}
	decision := policy.Evaluate(user, tool, intent.Input)
	if !decision.Allowed {
		return StepOutcome{}, fmt.Errorf("%w: %s", ErrPolicyDenied, decision.Reason)
	}
	// E2: 低风险写若通过准入门则自动执行（已确认 plan → 执行 → advisory 结果），
	// 否则保持 loop 的硬禁止默认，退回人工确认。agent loop 无 runbook 模板评审单元，
	// 走裸写严格门（Admit：工具自身必须 low），模板宽松门（AdmitRunbook）不适用。
	if s.admitAutoExec(ctx, user, tool, decision, autonomy.SourceAgentLoop, nil) {
		return s.agentWriteAutoExec(ctx, user, tool, intent, decision)
	}
	return s.agentWriteHandoff(ctx, user, tool, intent, decision)
}

// agentWriteAutoExec 执行一次被准入门放行的低风险写：创建已确认 plan → 立即执行 →
// 以 advisory 结果返回（loop 继续），与 executeFromIntent 的直接低风险 runbook 路径
// 语义一致。
func (s *Service) agentWriteAutoExec(ctx context.Context, user identity.CurrentUser, tool tools.Tool, intent Intent, decision policy.Decision) (StepOutcome, error) {
	if s.execution == nil {
		// 无执行器时回落普通 pending plan（不放行自动执行）。
		return s.agentWriteHandoff(ctx, user, tool, intent, decision)
	}
	plan, err := s.plans.CreateRunbookPlan(ctx, user, decision, intent.Input, "", "low")
	if err != nil {
		return StepOutcome{}, err
	}
	executionResult, execErr := s.execution.ExecuteConfirmedStoredPlan(ctx, plan.ID)
	if execErr != nil {
		return StepOutcome{}, execErr
	}
	s.recordAutoExec(ctx, user)
	out := StepOutcome{
		Intent:  intent,
		Kind:    StepAdvisory,
		Tool:    tool.Name,
		Input:   intent.Input,
		Output: map[string]any{
			"execution_id": executionResult.ID,
			"status":       executionResult.Status,
			"reused":       executionResult.Reused,
			"plan_id":      plan.ID,
		},
		Summary: fmt.Sprintf("已自动执行 %s（准入放行）：执行 %s 状态 %s", tool.Name, executionResult.ID, executionResult.Status),
	}
	if s.dryRun != nil {
		if block, _, ok := s.previewWritePlan(ctx, tool.Name, intent.Input); ok {
			out.Blocks = append(out.Blocks, block)
		}
	}
	return out, nil
}

// agentWriteHandoff 在自动执行不可用时退回 pending plan 交接（与历史行为一致的
// 硬禁止默认）。
func (s *Service) agentWriteHandoff(ctx context.Context, user identity.CurrentUser, tool tools.Tool, intent Intent, decision policy.Decision) (StepOutcome, error) {
	plan, err := s.plans.CreatePlan(ctx, user, decision, intent.Input)
	if err != nil {
		return StepOutcome{}, err
	}
	out := StepOutcome{
		Intent:            intent,
		Kind:              StepExecutive,
		Tool:              intent.ToolName,
		Input:             intent.Input,
		PlanID:            plan.ID,
		Status:            string(plan.Status),
		Version:           plan.Version,
		ExpiresAt:         plan.ExpiresAt,
		ConfirmationToken: plan.ConfirmationToken,
		Trace:             buildAssistantTrace(intent, intent.ToolName, intent.Input, nil),
		Summary:           summarizePlan(intent.ToolName, intent.Input),
	}
	if s.dryRun != nil {
		if block, result, ok := s.previewWritePlan(ctx, intent.ToolName, intent.Input); ok {
			out.Blocks = append(out.Blocks, block)
			if encoded, err := json.Marshal(result); err == nil {
				_ = s.plans.AttachDryRun(ctx, plan.ID, encoded)
			}
		}
	}
	return out, nil
}

// stepReadSummary produces a lightweight human summary of a read step's raw
// result for the feedback turn, without an LLM formatting call.
func stepReadSummary(toolName string, answer map[string]any) string {
	if summary, ok := answer["summary"].(string); ok && summary != "" {
		return toolName + "：" + summary
	}
	for _, key := range []string{"message", "status", "text", "result", "heading"} {
		if v, ok := answer[key].(string); ok && v != "" {
			return toolName + "：" + v
		}
	}
	return toolName + "：已执行"
}

// diagnosticStepSummary builds a one-line, data-bearing summary of a diagnostic
// package so the tool_step feedback lets the planner see what it actually found
// (severity + resource + observation) instead of a generic count. This matters
// for loop convergence: a single-domain diagnostic that already answered the
// question must read as conclusive to the planner.
//
// For a multi-domain package (an orchestrator-merged sweep across glusterfs /
// minio / kafka), it enumerates each domain's result so the operator-facing
// conclusion reflects all domains, not just the first.
func diagnosticStepSummary(pkg diagnostics.Package) string {
	sev := packageSeverity(pkg)
	if len(pkg.Domains) > 1 {
		parts := make([]string, 0, len(pkg.Observations))
		for _, obs := range pkg.Observations {
			parts = append(parts, obs.Summary)
		}
		joined := strings.Join(parts, "；")
		if joined != "" {
			return fmt.Sprintf("诊断完成（%d 个域：%s）：%s", len(pkg.Domains), strings.Join(pkg.Domains, ","), joined)
		}
		return fmt.Sprintf("诊断完成（%d 个域：%s）：综合状态为 %s", len(pkg.Domains), strings.Join(pkg.Domains, ","), sev)
	}
	var resource string
	if len(pkg.Resources) > 0 {
		r := pkg.Resources[0]
		if r.Name != "" {
			resource = fmt.Sprintf("%s %s %s", r.Domain, r.Type, r.Name)
		}
	}
	if resource == "" {
		resource = strings.Join(pkg.Domains, ",")
	}
	base := fmt.Sprintf("诊断完成：%s 状态为 %s", resource, sev)
	if len(pkg.Observations) > 0 && strings.TrimSpace(pkg.Observations[0].Summary) != "" {
		base += "；" + pkg.Observations[0].Summary
	}
	return base
}

// packageSeverity returns the worst severity across observations, defaulting to ok.
func packageSeverity(pkg diagnostics.Package) diagnostics.Severity {
	worst := diagnostics.SeverityOK
	for _, obs := range pkg.Observations {
		if severityRank(obs.Severity) > severityRank(worst) {
			worst = obs.Severity
		}
	}
	return worst
}

func severityRank(sev diagnostics.Severity) int {
	switch sev {
	case diagnostics.SeverityCritical:
		return 4
	case diagnostics.SeverityWarning:
		return 3
	case diagnostics.SeverityInfo:
		return 2
	default:
		return 1
	}
}

// observationSummaries collects the per-observation human summaries, so the
// planner's feedback includes the concrete diagnostic data it can draw on.
func observationSummaries(pkg diagnostics.Package) []string {
	if len(pkg.Observations) == 0 {
		return nil
	}
	summary := make([]string, 0, len(pkg.Observations))
	for _, obs := range pkg.Observations {
		line := strings.TrimSpace(obs.Summary)
		if line != "" {
			summary = append(summary, line)
		}
	}
	return summary
}

// packageFindings collects the finding summaries, latest of the diagnostic
// conclusion.
func packageFindings(pkg diagnostics.Package) []string {
	if len(pkg.Findings) == 0 {
		return nil
	}
	findings := make([]string, 0, len(pkg.Findings))
	for _, f := range pkg.Findings {
		line := strings.TrimSpace(f.Summary)
		if line != "" {
			findings = append(findings, line)
		}
	}
	return findings
}

// envAgentMaxSteps returns the configured step budget for agent loops,
// defaulting to defaultAgentMaxSteps. Values below 1 (or unparsable) fall back
// to the default.
const envAgentMaxSteps = "COPILOT_ASSISTANT_MAX_STEPS"

func agentMaxSteps() int {
	v := strings.TrimSpace(os.Getenv(envAgentMaxSteps))
	if v == "" {
		return defaultAgentMaxSteps
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return defaultAgentMaxSteps
	}
	return n
}

// agentMaxControlSteps reads the control step budget from
// COPILOT_AGENT_MAX_CONTROL_STEPS, defaulting to defaultMaxControlSteps.
// Values below 1 (or unparsable) fall back to the default.
const envAgentMaxControlSteps = "COPILOT_AGENT_MAX_CONTROL_STEPS"

func agentMaxControlSteps() int {
	v := strings.TrimSpace(os.Getenv(envAgentMaxControlSteps))
	if v == "" {
		return defaultMaxControlSteps
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return defaultMaxControlSteps
	}
	return n
}

// stepEventFromOutcome renders an executed advisory step as a StreamEvent for
// the frontend step timeline.
func stepEventFromOutcome(out StepOutcome) StreamEvent {
	return StreamEvent{Step: &StepEvent{
		Tool:      out.Tool,
		StepIndex: out.StepIndex,
		Status:    "done",
		Summary:   out.Summary,
		Input:     out.Input,
		Output:    out.Output,
	}}
}

// handoffResponse renders an executive StepOutcome as a confirmation_required
// Response so the loop's write boundary emits the same shape as the single-plan
// write path.
func handoffResponse(out *StepOutcome) Response {
	if out == nil {
		return Response{Type: "answer", Message: "无法创建待确认计划"}
	}
	return Response{
		Type:              "confirmation_required",
		Tool:              out.Tool,
		PlanID:            out.PlanID,
		Status:            out.Status,
		Version:           out.Version,
		ExpiresAt:         out.ExpiresAt,
		Summary:           out.Summary,
		ConfirmationToken: out.ConfirmationToken,
		Blocks:            out.Blocks,
		Trace:             out.Trace,
	}
}

// stepsAnswer builds a fallback terminal answer from the accumulated advisory
// steps (used when the loop exhausts maxSteps without a final_answer, or to
// enrich a Done answer). Each step's summary is listed so the operator can see
// what was checked. The wording is deliberately honest: this is a synthesized
// summary of what was executed, NOT a model-authored final conclusion, and the
// operator should treat it as provisional (repeated-read / maxSteps convergence
// often means the diagnosis did not reach a definitive verdict).
func stepsAnswer(run *AgentRun) string {
	if run.FinalAnswer != "" {
		return run.FinalAnswer
	}
	var b strings.Builder
	if len(run.Steps) == 0 {
		return "未能给出明确结论：本轮未执行到任何检查。请补充信息或换个问法重试。"
	}

	// 兜底是"未完整推理"的诚实收尾（与 answer_converged 标识一致），不能让
	// operator 误以为是权威结论。若已执行步骤带出明确严重级别，将其提到开头，
	// 作为对真实证据的最低限度综合——但仍明确标注未达成明确结论。
	advisory := make([]StepOutcome, 0, len(run.Steps))
	for _, out := range run.Steps {
		if out.Kind == StepAdvisory {
			advisory = append(advisory, out)
		}
	}
	if sev, ok := worstAdvisorySeverity(advisory); ok && sev != "" {
		fmt.Fprintf(&b, "综合严重级别为 %s，未达到明确的最终结论。", sev)
	} else {
		b.WriteString("未达到明确的最终结论。")
	}
	b.WriteString("已执行以下检查，结论仅供参考（可继续追问）：")
	for i, out := range advisory {
		if i > 0 {
			b.WriteString("；")
		}
		b.WriteString(out.Summary)
	}

	// 诚实列出未达成/失败的环节，不让"部分失败"被透传成"已给出完整结论"。
	failed := runFailedTools(run)
	if len(failed) > 0 {
		b.WriteString("（以下环节未完成或失败：")
		for i, f := range failed {
			if i > 0 {
				b.WriteString("、")
			}
			b.WriteString(f)
		}
		b.WriteString("）")
	}
	return b.String()
}

// worstAdvisorySeverity returns the worst severity among the advisory steps'
// output packages (severity is carried in step output by agentDiagnosticStep).
func worstAdvisorySeverity(steps []StepOutcome) (string, bool) {
	worst := diagnostics.SeverityOK
	found := false
	for _, out := range steps {
		if out.Kind != StepAdvisory {
			continue
		}
		sev, ok := out.Output["severity"].(string)
		if !ok || sev == "" {
			continue
		}
		found = true
		if severityRank(diagnostics.Severity(sev)) > severityRank(worst) {
			worst = diagnostics.Severity(sev)
		}
	}
	if !found {
		return "", false
	}
	return string(worst), true
}

// runFailedTools enumerates the non-advisory / error outcomes so a fallback
// answer does not silently claim every check succeeded. Advisory steps are
// only recorded in run.Steps once they execute; a terminal error (run.Err) or a
// handoff (run.Handoff) marks work that did not produce a clean advisory result
// and is surfaced here for honesty.
func runFailedTools(run *AgentRun) []string {
	var failed []string
	seen := map[string]bool{}
	note := func(tool string) {
		if tool == "" || seen[tool] {
			return
		}
		seen[tool] = true
		failed = append(failed, tool)
	}
	if run.Handoff != nil {
		// A handoff queued a plan for approval — not a failure, but the loop
		// reached the fallback path, so flag the tool being handed off.
		note(run.Handoff.Tool)
	}
	if run.Err != nil {
		// The terminal error is not bound to a single tool; surface a generic
		// marker rather than fabricate a tool name.
		note("诊断链条中断")
	}
	return failed
}

// persistAgentRun records a completed agent-loop iteration into the
// conversation, preserving the full step-level audit trail. The turn chain is:
//
//	user message -> step1 (tool_step) -> step2 (tool_step) -> ... -> terminal response
//
// Each tool step is stored as an assistant turn with ResponseType=tool_step and
// a ToolFact payload (tool/input/result) plus step_index, chained via
// parent_turn_id so the causal order is reconstructable. The terminal response
// turn is chained to the last step (or to the user turn when no step ran).
//
// It returns the terminal assistant turn ID so the caller can populate
// Response.TurnID. Waits are nudged by 1ms intervals so strict created-at
// ordering stays stable in the store (mirroring persistTurns).
func (s *Service) persistAgentRun(ctx context.Context, convID, userMessage string, run *AgentRun, response Response) (string, error) {
	now := s.now()
	base := now.Add(1 * time.Millisecond) // terminal turn lands strictly after all steps

	// latest is the turn ID the next turn chains under; starts as the user turn.
	userTurn, err := s.conversations.AppendTurn(ctx, store.Turn{
		ConversationID: convID,
		Role:           store.ConversationRoleUser,
		Content:        userMessage,
		CreatedAt:      now,
	})
	if err != nil {
		return "", err
	}
	latest := userTurn.ID

	// Persist each executed step as a tool_step assistant turn, chained in order.
	for i, out := range run.Steps {
		if out.Kind != StepAdvisory {
			continue // executive/clarification steps carry no tool result to persist
		}
		stepTurn, err := s.conversations.AppendTurn(ctx, store.Turn{
			ConversationID:  convID,
			ParentTurnID:    latest,
			Role:            store.ConversationRoleAssistant,
			Content:         out.Summary,
			ResponseType:    responseTypeToolStep,
			ResponsePayload: stepPayload(out),
			CreatedAt:       base.Add(time.Duration(i+1) * time.Millisecond),
		})
		if err != nil {
			return "", err
		}
		latest = stepTurn.ID
	}

	// Persist the terminal response turn, chained to the last persisted turn.
	terminalTurn, err := s.conversations.AppendTurn(ctx, store.Turn{
		ConversationID:  convID,
		ParentTurnID:    latest,
		Role:            store.ConversationRoleAssistant,
		Content:         assistantTurnContent(response),
		ResponseType:    response.Type,
		ResponsePayload: responsePayload(response),
		CreatedAt:       base.Add(time.Duration(len(run.Steps)+1) * time.Millisecond),
	})
	if err != nil {
		return "", err
	}
	return terminalTurn.ID, nil
}

// stepPayload serializes an executed advisory step as a persisted ToolFact
// payload: the tool name, its input, the raw read result, and a step-level
// summary so the conversation replay carries the full step audit.
func stepPayload(out StepOutcome) map[string]any {
	payload := map[string]any{
		"tool":       out.Tool,
		"step_index": out.StepIndex,
	}
	if out.Input != nil {
		payload["input"] = out.Input
	}
	if out.Output != nil {
		payload["result"] = out.Output
	}
	if out.Summary != "" {
		payload["summary"] = out.Summary
	}
	return payload
}
