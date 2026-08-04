package assistant_test

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// agentFakePlanner is an agent-capable planner (implements both Plan and
// PlanStream). It scripts intents from a shared queue so the streaming path
// (used by the loop) returns the same intent as the one-shot path.
type agentFakePlanner struct {
	mu      sync.Mutex
	intents []assistant.Intent
	calls   int
}

func (p *agentFakePlanner) next() (assistant.Intent, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls >= len(p.intents) {
		return assistant.Intent{}, nil
	}
	it := p.intents[p.calls]
	p.calls++
	return it, nil
}

func (p *agentFakePlanner) Plan(_ context.Context, _ identity.CurrentUser, _ string, _ []assistant.Turn, _ assistant.PageContext) (assistant.Intent, error) {
	return p.next()
}

func (p *agentFakePlanner) PlanStream(_ context.Context, _ identity.CurrentUser, _ string, _ []assistant.Turn, _ assistant.PageContext) (<-chan assistant.StreamEvent, error) {
	events := make(chan assistant.StreamEvent, 1)
	intent, err := p.next()
	if err != nil {
		events <- assistant.StreamEvent{Err: err, Done: true}
		close(events)
		return events, nil
	}
	ic := intent
	events <- assistant.StreamEvent{Intent: &ic, Done: true}
	close(events)
	return events, nil
}

// TestServiceHandleMessageStreamRunsAgentLoop: with the agent loop enabled, a
// multi-step chain (read → read → final_answer) emits one StepEvent per advisory
// step and concludes with the planner's final answer.
func TestServiceHandleMessageStreamRunsAgentLoop(t *testing.T) {
	planner := &agentFakePlanner{intents: []assistant.Intent{
		readIntent(),
		readIntentOn("alert.query", map[string]any{"environment": "prod"}),
		doneIntent("prod 集群健康，无异常"),
	}}
	service, _ := newAssistant(t, planner)
	service.WithAgentLoop(true)

	events, err := service.HandleMessageStream(context.Background(), viewer(), "检查 prod 集群", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("HandleMessageStream start: %v", err)
	}
	var steps []assistant.StepEvent
	var resp *assistant.Response
	done := false
	for ev := range events {
		if ev.Step != nil {
			steps = append(steps, *ev.Step)
		}
		if ev.Done {
			done = true
			resp = ev.Response
		}
	}
	if !done {
		t.Fatal("no terminal event received")
	}
	if len(steps) != 2 {
		t.Fatalf("step events = %d, want 2 (two advisory reads)", len(steps))
	}
	for _, st := range steps {
		if st.Status != "done" {
			t.Fatalf("step %d status = %q, want done", st.StepIndex, st.Status)
		}
	}
	if resp == nil || resp.Type != "answer" || resp.Message != "prod 集群健康，无异常" {
		t.Fatalf("response = %+v, want final_answer answer", resp)
	}
}

// TestServiceHandleMessageStreamConvergenceEmitsFallbackMarker drives a
// repeated-read convergence backstop: the planner sends the same advisory read
// twice, the loop collapses the duplicate and concludes. Because the terminal
// answer is synthesized (not a model final_answer), the response must be tagged
// answer_converged so the operator never mistakes it for completed multi-step
// reasoning.
func TestServiceHandleMessageStreamConvergenceEmitsFallbackMarker(t *testing.T) {
	planner := &agentFakePlanner{intents: []assistant.Intent{
		readIntent(),
		readIntent(), // identical read -> convergence backstop
	}}
	service, _ := newAssistant(t, planner)
	service.WithAgentLoop(true)

	events, err := service.HandleMessageStream(context.Background(), viewer(), "查一下 prod 集群", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("HandleMessageStream start: %v", err)
	}
	var resp *assistant.Response
	done := false
	for ev := range events {
		if ev.Done {
			done = true
			resp = ev.Response
		}
	}
	if !done || resp == nil {
		t.Fatalf("done=%v resp=%+v, want a terminal answer_converged", done, resp)
	}
	if resp.Type != "answer_converged" {
		t.Fatalf("response type = %q, want answer_converged (converged fallback, not a model final_answer)", resp.Type)
	}
	if !strings.Contains(resp.Message, "未达到明确的最终结论") {
		t.Fatalf("message = %q, want honest provisional wording", resp.Message)
	}
}

// TestServiceHandleMessageStreamMaxStepsEmitsFallbackMarker drives a maxSteps
// exhaustion: with a 2-step budget the loop runs out before the planner ever
// emits a final_answer, so the synthesized summary must surface as
// answer_converged.
func TestServiceHandleMessageStreamMaxStepsEmitsFallbackMarker(t *testing.T) {
	t.Setenv("COPILOT_ASSISTANT_MAX_STEPS", "2")
	planner := &agentFakePlanner{intents: []assistant.Intent{
		readIntent(),
		readIntentOn("alert.query", map[string]any{"environment": "prod"}),
		readIntentOn("event.query", map[string]any{"environment": "prod"}),
	}}
	service, _ := newAssistant(t, planner)
	service.WithAgentLoop(true)

	events, err := service.HandleMessageStream(context.Background(), viewer(), "给我全面排查", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("HandleMessageStream start: %v", err)
	}
	var resp *assistant.Response
	done := false
	for ev := range events {
		if ev.Done {
			done = true
			resp = ev.Response
		}
	}
	if !done || resp == nil {
		t.Fatalf("done=%v resp=%+v, want a terminal answer_converged", done, resp)
	}
	if resp.Type != "answer_converged" {
		t.Fatalf("response type = %q, want answer_converged (maxSteps exhaustion)", resp.Type)
	}
	if !strings.Contains(resp.Message, "未达到明确的最终结论") {
		t.Fatalf("message = %q, want honest provisional wording", resp.Message)
	}
}

// TestServiceHandleMessageStreamAgentLoopStopsOnWrite: the loop must stop at a
// write intent and hand back a confirmation_required response, never executing
// the write.
func TestServiceHandleMessageStreamAgentLoopStopsOnWrite(t *testing.T) {
	planner := &agentFakePlanner{intents: []assistant.Intent{
		readIntent(),
		assistant.Intent{ToolName: tools.TopicRetentionSet, Input: retentionInput()},
	}}
	service, _ := newAssistant(t, planner)
	service.WithAgentLoop(true)

	events, err := service.HandleMessageStream(context.Background(), admin(), "查完把 topic 保留时间改 72 小时", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	var resp *assistant.Response
	done := false
	for ev := range events {
		if ev.Done {
			done = true
			resp = ev.Response
		}
	}
	if !done || resp == nil {
		t.Fatalf("done=%v resp=%+v, want confirmation_required", done, resp)
	}
	if resp.Type != "confirmation_required" {
		t.Fatalf("response type = %q, want confirmation_required (write stops for human approval)", resp.Type)
	}
	if resp.PlanID == "" {
		t.Fatalf("plan id empty; write should queue a pending plan, not execute")
	}
}

// TestAgentLoopPersistsToolSteps: with a conversation store, an agent-loop run
// must persist each advisory step as a chained tool_step turn (ToolFact payload)
// between the user turn and the terminal answer, and the read audits must carry
// agent_step + conversation_turn_id step identity.
func TestAgentLoopPersistsToolSteps(t *testing.T) {
	planner := &agentFakePlanner{intents: []assistant.Intent{
		readIntent(),
		readIntentOn("alert.query", map[string]any{"environment": "prod"}),
		doneIntent("prod 集群健康，无异常"),
	}}
	conversations := store.NewMemoryAssistantConversationStore()
	service, repository := newAssistantWithStore(t, planner, conversations)
	service.WithAgentLoop(true)

	events, err := service.HandleMessageStream(context.Background(), viewer(), "检查 prod 集群", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("HandleMessageStream start: %v", err)
	}
	var resp *assistant.Response
	for ev := range events {
		if ev.Done {
			resp = ev.Response
		}
	}
	if resp == nil || resp.ConversationID == "" {
		t.Fatalf("terminal response missing conversation id: %+v", resp)
	}

	// ListTurns returns newest first; reverse to inspect causal order.
	page, err := conversations.ListTurns(context.Background(), resp.ConversationID, 0, "")
	if err != nil {
		t.Fatalf("ListTurns: %v", err)
	}
	if len(page.Turns) != 4 {
		t.Fatalf("turns = %d, want 4 (user + 2 tool_step + terminal)", len(page.Turns))
	}
	// Newest-first: [terminal, step2, step1, user].
	terminal := page.Turns[0]
	step2 := page.Turns[1]
	step1 := page.Turns[2]
	user := page.Turns[3]

	if user.Role != store.ConversationRoleUser || user.Content != "检查 prod 集群" {
		t.Fatalf("user turn = %+v, want the original message", user)
	}
	// Two distinct advisory reads (cluster status then alerts) so the step chain
	// exercises multi-step execution without tripping the convergence backstop
	// (which collapses repeated identical reads).
	for i, step := range []store.Turn{step1, step2} {
		wantTool := tools.ClusterStatusRead
		if i == 1 {
			wantTool = tools.AlertQuery
		}
		if step.Role != store.ConversationRoleAssistant {
			t.Fatalf("step %d role = %q, want assistant", i, step.Role)
		}
		if step.ResponseType != "tool_step" {
			t.Fatalf("step %d response_type = %q, want tool_step", i, step.ResponseType)
		}
		if step.ParentTurnID == "" {
			t.Fatalf("step %d parent_turn_id empty, want chaining", i)
		}
		if step.ResponsePayload == nil {
			t.Fatalf("step %d payload nil, want ToolFact", i)
		}
		if step.ResponsePayload["tool"] != wantTool {
			t.Fatalf("step %d payload tool = %v, want %s", i, step.ResponsePayload["tool"], wantTool)
		}
		if step.ResponsePayload["step_index"] == nil {
			t.Fatalf("step %d payload step_index missing", i)
		}
		if step.ResponsePayload["result"] == nil {
			t.Fatalf("step %d payload result missing (ToolFact result)", i)
		}
	}
	// Chain: user -> step1 -> step2 -> terminal.
	if step1.ParentTurnID != user.ID {
		t.Fatalf("step1 parent = %q, want user %q", step1.ParentTurnID, user.ID)
	}
	if step2.ParentTurnID != step1.ID {
		t.Fatalf("step2 parent = %q, want step1 %q", step2.ParentTurnID, step1.ID)
	}
	if terminal.ResponseType != "answer" || terminal.ParentTurnID != step2.ID {
		t.Fatalf("terminal = %+v, want answer chained to step2", terminal)
	}

	// Audit step identity: each read recorded agent_step + conversation_turn_id.
	conv := resp.ConversationID
	stepIndexes := map[string]bool{}
	for _, ev := range repository.AuditEvents() {
		if ev.Metadata == nil {
			continue
		}
		if _, ok := ev.Metadata["conversation_turn_id"]; !ok {
			continue
		}
		if got := ev.Metadata["conversation_turn_id"]; got != conv {
			t.Fatalf("audit conversation_turn_id = %v, want %s", got, conv)
		}
		switch idx := ev.Metadata["agent_step"].(type) {
		case float64: // JSON-decoded metadata
			stepIndexes[strconv.Itoa(int(idx))] = true
		case int: // in-memory metadata
			stepIndexes[strconv.Itoa(idx)] = true
		}
	}
	if !stepIndexes["0"] || !stepIndexes["1"] {
		t.Fatalf("audit agent_step indexes = %v, want both 0 and 1", stepIndexes)
	}
}

// TestAgentLoopNeverAutoExecutesLowRiskRunbook: even when a low-risk runbook
// matches a write intent, the agent loop must NOT auto-execute it. Writes in the
// loop always stop at confirmation_required and hand the decision back to the
// human; the execution runner must never be called.
func TestAgentLoopNeverAutoExecutesLowRiskRunbook(t *testing.T) {
	planner := &agentFakePlanner{intents: []assistant.Intent{
		assistant.Intent{ToolName: tools.TopicRetentionSet, Input: retentionInput()},
	}}
	service, _ := newAssistant(t, planner)
	execRunner := &fakeExecutionRunner{execResult: execution.Execution{ID: "exec-1", Status: "succeeded"}}
	service.WithRunbookRouter(assistant.NewRunbookRouter(fakeRunbookLookupAlways{}))
	service.WithExecutionRunner(execRunner)
	service.WithDryRunRunner(&fakeDryRunRunner{result: execution.DryRunResult{Summary: "保留 72h"}})
	service.WithAgentLoop(true)

	events, err := service.HandleMessageStream(context.Background(), admin(), "把 prod orders 的保留调成 72 小时", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	var resp *assistant.Response
	for ev := range events {
		if ev.Done {
			resp = ev.Response
		}
	}
	if resp == nil || resp.Type != "confirmation_required" {
		t.Fatalf("response type = %v, want confirmation_required (loop never auto-executes a write)", resp)
	}
	if resp.PlanID == "" {
		t.Fatalf("plan id empty; write should queue a pending plan for approval")
	}
	if execRunner.called {
		t.Fatal("execution runner called: low-risk runbook must NOT auto-execute inside the agent loop")
	}
}
