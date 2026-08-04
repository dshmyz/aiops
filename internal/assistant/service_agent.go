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

// plannerUnwrapper lets a wrapper planner (CapabilityAwarePlanner,
// ActionAwarePlanner) expose the planner it delegates to, so capability probing
// can look through the wrapper chain at the innermost planner.
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
// Write intents short-circuit to a handoff StepOutcome (a queued plan awaiting
// human approval); the loop stops and never auto-executes the write.
func (s *Service) executeAgentStep(ctx context.Context, user identity.CurrentUser, message string, intent Intent, stepIndex int) (StepOutcome, error) {
	out := StepOutcome{
		Intent:    intent,
		StepIndex: stepIndex,
	}
	if isWriteIntent(intent) {
		handoff, err := s.agentWriteStep(ctx, user, message, intent)
		if err != nil {
			return StepOutcome{}, err
		}
		out.Kind = StepExecutive
		out.Tool = intent.ToolName
		return handoff, nil
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

// agentDiagnosticStep executes a diagnostic intent in the loop, capturing the
// package as the step output.
func (s *Service) agentDiagnosticStep(ctx context.Context, user identity.CurrentUser, intent Intent, out StepOutcome) (StepOutcome, error) {
	if s.diagnostics == nil {
		return StepOutcome{}, errors.New("diagnostic service is required")
	}
	toolName, err := diagnostics.ResolveReadTool(*intent.Diagnostic)
	if err != nil {
		return StepOutcome{}, err
	}
	pkg, err := s.diagnostics.Run(ctx, user, *intent.Diagnostic)
	if err != nil {
		return StepOutcome{}, err
	}
	out.Tool = toolName
	out.Input = map[string]any{"domain": intent.Diagnostic.Domain, "environment": intent.Diagnostic.Environment}
	out.Output = map[string]any{
		"summary":         fmt.Sprintf("诊断完成：%d 个观察，%d 个发现，%d 个建议", len(pkg.Observations), len(pkg.Findings), len(pkg.Recommendations)),
		"environment":     pkg.Environment,
		"domains":         pkg.Domains,
		"recommendations": len(pkg.Recommendations),
	}
	out.Summary = stepReadSummary(toolName, out.Output)
	return out, nil
}

// agentWriteStep queues a plan for a write intent (confirmation_required) and
// returns the handoff StepOutcome. It mirrors executeFromIntent's write path:
// policy is applied, a plan is created, dry-run preview attached. Low-risk
// runbook auto-execution is intentionally NOT performed from inside the loop —
// writes in the loop always stop and hand back to the human.
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
// what was checked.
func stepsAnswer(run *AgentRun) string {
	if run.FinalAnswer != "" {
		return run.FinalAnswer
	}
	var b strings.Builder
	if len(run.Steps) == 0 {
		return "已执行若干检查，但未能给出明确结论。"
	}
	b.WriteString("已完成以下检查：")
	for i, out := range run.Steps {
		if out.Kind != StepAdvisory {
			continue
		}
		if i > 0 {
			b.WriteString("；")
		}
		b.WriteString(out.Summary)
	}
	return b.String()
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
