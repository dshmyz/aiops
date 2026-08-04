package assistant

import (
	"context"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

func TestAssistantTurnContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response Response
		want     string
	}{
		{
			name:     "prefers summary",
			response: Response{Type: "answer", Summary: "集群状态正常"},
			want:     "集群状态正常",
		},
		{
			name:     "falls back to message",
			response: Response{Type: "clarification_needed", Message: "缺少 cluster 参数"},
			want:     "缺少 cluster 参数",
		},
		{
			name:     "uses answer summary",
			response: Response{Type: "answer", Answer: map[string]any{"summary": "容量充足"}},
			want:     "容量充足",
		},
		{
			name:     "uses answer status",
			response: Response{Type: "answer", Answer: map[string]any{"status": "green"}},
			want:     "green",
		},
		{
			name:     "falls back to answer json when no readable fields",
			response: Response{Type: "answer", Answer: map[string]any{"environment": "prod", "value": 42}},
			want:     `{"environment":"prod","value":42}`,
		},
		{
			name:     "uses top-level status when answer is empty",
			response: Response{Type: "execution_result", Status: "succeeded"},
			want:     "succeeded",
		},
		{
			name:     "answer with empty content shows placeholder instead of type",
			response: Response{Type: "answer"},
			want:     "未返回具体内容",
		},
		{
			name:     "non-answer type with empty content falls back to type",
			response: Response{Type: "clarification_needed"},
			want:     "clarification_needed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := assistantTurnContent(tt.response)
			if got != tt.want {
				t.Fatalf("assistantTurnContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

// fakeStreamingPlanner implements the agentPlanner interface (Planner +
// PlanStream) as a stand-in for *EinoPlanner at the bottom of the wrapper chain.
type fakeStreamingPlanner struct{}

func (*fakeStreamingPlanner) Plan(context.Context, identity.CurrentUser, string, []Turn, PageContext) (Intent, error) {
	return Intent{}, nil
}

func (*fakeStreamingPlanner) PlanStream(context.Context, identity.CurrentUser, string, []Turn, PageContext) (<-chan StreamEvent, error) {
	events := make(chan StreamEvent, 1)
	events <- StreamEvent{Done: true}
	close(events)
	return events, nil
}

// TestAgentPlannerCapableUnwrapsChain verifies agentPlannerCapable looks through
// the production wrapper chain (ActionAwarePlanner -> CapabilityAwarePlanner ->
// *EinoPlanner) to the innermost PlanStream-capable planner, and stays false for
// planners that never reach a streaming planner.
func TestAgentPlannerCapableUnwrapsChain(t *testing.T) {
	t.Parallel()

	// A minimal PlanStream-capable stand-in (agentPlanner) at the bottom of the
	// chain, mirroring *EinoPlanner's role in production wiring.
	streaming := &fakeStreamingPlanner{}

	// Production chain: ActionAwarePlanner wraps CapabilityAwarePlanner wraps the
	// streaming planner. The inner planners do not implement PlanStream
	// themselves, so the check must unwrap to find it.
	capability := NewCapabilityAwarePlanner(streaming)
	action := NewActionAwarePlanner(capability, nil)
	if !agentPlannerCapable(action) {
		t.Fatal("agentPlannerCapable(production wrapper chain) = false, want true")
	}
	// Capability-only chain (no Action wrapper) unwraps too.
	if !agentPlannerCapable(capability) {
		t.Fatal("agentPlannerCapable(capability planner) = false, want true")
	}
	// Bare streaming planner is directly capable.
	if !agentPlannerCapable(streaming) {
		t.Fatal("agentPlannerCapable(raw streaming planner) = false, want true")
	}
	// Deterministic planner never unwraps to a streaming planner.
	var det Planner = DeterministicPlanner{}
	if agentPlannerCapable(det) {
		t.Fatal("agentPlannerCapable(deterministic planner) = true, want false")
	}
	// Wrapper around a non-streaming planner stays incapable.
	if agentPlannerCapable(NewActionAwarePlanner(DeterministicPlanner{}, nil)) {
		t.Fatal("agentPlannerCapable(wrapper around deterministic planner) = true, want false")
	}
}
