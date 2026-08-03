package assistant_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// fakeCompactor records the turns it received and returns a fixed summary.
// Used to verify loadHistory behavior without depending on an LLM.
type fakeCompactor struct {
	receivedTurns []assistant.Turn
	summary       string
	calls         int
	err           error
}

func (f *fakeCompactor) Compact(_ context.Context, turns []assistant.Turn) (string, error) {
	f.calls++
	f.receivedTurns = append([]assistant.Turn(nil), turns...)
	if f.err != nil {
		return "", f.err
	}
	return f.summary, nil
}

// newAssistantWithCompactor wires a service with both a conversation store
// and a compactor so loadHistory exercises the rolling summarization path.
func newAssistantWithCompactor(t *testing.T, planner assistant.Planner, conversations store.AssistantConversationStore, compactor assistant.Compactor) (*assistant.Service, *store.MemoryActionPlanStore) {
	t.Helper()
	repository := store.NewMemoryActionPlanStore()
	readService := execution.NewReadOnlyService(readRunner{}, audit.NewService(repository))
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time {
		return time.Date(2026, time.July, 21, 10, 0, 0, 0, time.UTC)
	}))
	return assistant.NewServiceWithCompactor(planner, readService, planService, conversations, compactor), repository
}

// seedConversationTurns appends n user+assistant turn pairs to the conversation
// starting at pair index `start` (used for timestamp offset so successive
// calls produce strictly increasing timestamps). Returns the IDs of all
// appended turns in chronological order (oldest first).
func seedConversationTurns(t *testing.T, conversations store.AssistantConversationStore, conversationID string, start, pairs int) []string {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, time.July, 21, 10, 0, 0, 0, time.UTC)
	var ids []string
	for i := 0; i < pairs; i++ {
		idx := start + i
		ts := base.Add(time.Duration(idx) * time.Second)
		userTurn, err := conversations.AppendTurn(ctx, store.Turn{
			ConversationID: conversationID,
			Role:           store.ConversationRoleUser,
			Content:        "user message " + string(rune('a'+idx)),
			CreatedAt:      ts,
		})
		if err != nil {
			t.Fatalf("append user turn %d: %v", idx, err)
		}
		ids = append(ids, userTurn.ID)
		assistantTurn, err := conversations.AppendTurn(ctx, store.Turn{
			ConversationID: conversationID,
			Role:           store.ConversationRoleAssistant,
			Content:        "assistant reply " + string(rune('a'+idx)),
			CreatedAt:      ts.Add(1 * time.Millisecond),
		})
		if err != nil {
			t.Fatalf("append assistant turn %d: %v", idx, err)
		}
		ids = append(ids, assistantTurn.ID)
	}
	return ids
}

// TestLoadHistoryCompactsWhenTurnsExceedThreshold verifies that when a
// conversation has more than compactThreshold unsummarized turns, the oldest
// turns are replaced by a summary. The planner should receive [summary,
// ...keepRecentTurns verbatim] in chronological order.
func TestLoadHistoryCompactsWhenTurnsExceedThreshold(t *testing.T) {
	conversations := store.NewMemoryAssistantConversationStore()
	compactor := &fakeCompactor{summary: "SUMMARY_OF_OLD_TURNS"}
	planner := &historyCapturingPlanner{intent: assistant.Intent{
		ToolName: tools.ClusterStatusRead,
		Input:    map[string]any{"environment": "prod"},
	}}
	service, _ := newAssistantWithCompactor(t, planner, conversations, compactor)

	ctx := context.Background()
	conv, err := conversations.CreateConversation(ctx, viewer().Subject, "test", "preview", time.Now())
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	// 7 pairs = 14 turns. compactThreshold=12, keepRecent=8, so 4 oldest
	// turns (2 pairs) should be compacted, leaving 8 verbatim + 1 summary.
	seedConversationTurns(t, conversations, conv.ID, 0, 7)

	_, err = service.HandleMessage(ctx, viewer(), "查看 prod 集群状态", conv.ID, assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}

	if compactor.calls != 1 {
		t.Fatalf("compactor calls = %d, want 1", compactor.calls)
	}
	// Compactor should receive the 4 oldest turns (no existing summary yet).
	if len(compactor.receivedTurns) != 4 {
		t.Fatalf("compactor received %d turns, want 4", len(compactor.receivedTurns))
	}
	// Planner should receive: [summary, ...8 recent turns] = 9 turns.
	if len(planner.history) != 9 {
		t.Fatalf("history len = %d, want 9 (1 summary + 8 recent)", len(planner.history))
	}
	if planner.history[0].Role != "system_summary" || planner.history[0].Content != "SUMMARY_OF_OLD_TURNS" {
		t.Fatalf("history[0] = %+v, want summary turn", planner.history[0])
	}
	// Verify the most recent verbatim turn is the last assistant reply.
	last := planner.history[len(planner.history)-1]
	if last.Role != "assistant" || !strings.Contains(last.Content, "assistant reply g") {
		t.Fatalf("last history turn = %+v, want assistant reply g", last)
	}

	// The summary should be persisted so it can be reused on the next turn.
	stored, err := conversations.GetSummary(ctx, conv.ID)
	if err != nil {
		t.Fatalf("GetSummary after compaction: %v", err)
	}
	if stored.Content != "SUMMARY_OF_OLD_TURNS" {
		t.Fatalf("stored summary = %q, want SUMMARY_OF_OLD_TURNS", stored.Content)
	}
	if stored.ParentTurnID == "" {
		t.Fatal("stored summary ParentTurnID is empty, want the last compacted turn ID")
	}
}

// TestLoadHistoryDoesNotCompactBelowThreshold verifies that when a conversation
// has fewer than compactThreshold turns, the compactor is never called and
// the planner receives all turns verbatim in chronological order.
func TestLoadHistoryDoesNotCompactBelowThreshold(t *testing.T) {
	conversations := store.NewMemoryAssistantConversationStore()
	compactor := &fakeCompactor{summary: "SHOULD_NOT_BE_USED"}
	planner := &historyCapturingPlanner{intent: assistant.Intent{
		ToolName: tools.ClusterStatusRead,
		Input:    map[string]any{"environment": "prod"},
	}}
	service, _ := newAssistantWithCompactor(t, planner, conversations, compactor)

	ctx := context.Background()
	conv, err := conversations.CreateConversation(ctx, viewer().Subject, "test", "preview", time.Now())
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	// 3 pairs = 6 turns. Below compactThreshold=12, no compaction.
	seedConversationTurns(t, conversations, conv.ID, 0, 3)

	_, err = service.HandleMessage(ctx, viewer(), "查看 prod 集群状态", conv.ID, assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}

	if compactor.calls != 0 {
		t.Fatalf("compactor calls = %d, want 0 below threshold", compactor.calls)
	}
	// Planner should receive all 6 turns verbatim.
	if len(planner.history) != 6 {
		t.Fatalf("history len = %d, want 6 verbatim turns", len(planner.history))
	}
	for _, turn := range planner.history {
		if turn.Role == "system_summary" {
			t.Fatalf("unexpected summary turn in history below threshold: %+v", turn)
		}
	}
}

// TestLoadHistoryReusesSummaryUntilNewTurnsExceedThreshold verifies that
// after an initial compaction, subsequent HandleMessage calls reuse the
// stored summary without re-compacting until enough new turns accumulate.
func TestLoadHistoryReusesSummaryUntilNewTurnsExceedThreshold(t *testing.T) {
	conversations := store.NewMemoryAssistantConversationStore()
	compactor := &fakeCompactor{summary: "EXISTING_SUMMARY"}
	planner := &historyCapturingPlanner{intent: assistant.Intent{
		ToolName: tools.ClusterStatusRead,
		Input:    map[string]any{"environment": "prod"},
	}}
	service, _ := newAssistantWithCompactor(t, planner, conversations, compactor)

	ctx := context.Background()
	conv, err := conversations.CreateConversation(ctx, viewer().Subject, "test", "preview", time.Now())
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	// Seed 7 pairs (14 turns) → first HandleMessage compacts 4 oldest.
	seedConversationTurns(t, conversations, conv.ID, 0, 7)

	_, err = service.HandleMessage(ctx, viewer(), "查看 prod 集群状态", conv.ID, assistant.PageContext{})
	if err != nil {
		t.Fatalf("first handle message: %v", err)
	}
	if compactor.calls != 1 {
		t.Fatalf("compactor calls after first = %d, want 1", compactor.calls)
	}

	// Second HandleMessage: only 2 new turns (1 pair) added since last compaction.
	// Unsummarized count = 8 (kept) + 2 (new) = 10 < 12 → no re-compaction.
	planner.history = nil
	compactor.calls = 0
	compactor.receivedTurns = nil
	_, err = service.HandleMessage(ctx, viewer(), "查看 staging 集群状态", conv.ID, assistant.PageContext{})
	if err != nil {
		t.Fatalf("second handle message: %v", err)
	}
	if compactor.calls != 0 {
		t.Fatalf("compactor calls after second = %d, want 0 (below threshold)", compactor.calls)
	}
	// History should include: [summary, ...8 recent verbatim] = 9 turns.
	if len(planner.history) != 9 {
		t.Fatalf("history len = %d, want 9 (1 summary + 8 recent)", len(planner.history))
	}
	if planner.history[0].Role != "system_summary" {
		t.Fatalf("history[0] role = %q, want system_summary", planner.history[0].Role)
	}
}

// TestLoadHistoryRecompactsWhenNewTurnsExceedThreshold verifies that after
// enough new turns accumulate post-summary, a second compaction merges the
// old summary with the next batch of oldest turns.
func TestLoadHistoryRecompactsWhenNewTurnsExceedThreshold(t *testing.T) {
	conversations := store.NewMemoryAssistantConversationStore()
	compactor := &fakeCompactor{summary: "ROLLING_SUMMARY"}
	planner := &historyCapturingPlanner{intent: assistant.Intent{
		ToolName: tools.ClusterStatusRead,
		Input:    map[string]any{"environment": "prod"},
	}}
	service, _ := newAssistantWithCompactor(t, planner, conversations, compactor)

	ctx := context.Background()
	conv, err := conversations.CreateConversation(ctx, viewer().Subject, "test", "preview", time.Now())
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	// Seed 7 pairs (14 turns) → first compaction.
	seedConversationTurns(t, conversations, conv.ID, 0, 7)
	_, err = service.HandleMessage(ctx, viewer(), "查看 prod 集群状态", conv.ID, assistant.PageContext{})
	if err != nil {
		t.Fatalf("first handle message: %v", err)
	}
	if compactor.calls != 1 {
		t.Fatalf("compactor calls after first = %d, want 1", compactor.calls)
	}

	// Add 4 more pairs (8 new turns). Unsummarized = 8 + 8 = 16 > 12 → re-compact.
	seedConversationTurns(t, conversations, conv.ID, 7, 4)

	planner.history = nil
	compactor.calls = 0
	compactor.receivedTurns = nil
	_, err = service.HandleMessage(ctx, viewer(), "查看 dev 集群状态", conv.ID, assistant.PageContext{})
	if err != nil {
		t.Fatalf("second handle message: %v", err)
	}
	if compactor.calls != 1 {
		t.Fatalf("compactor calls after second = %d, want 1 (re-compaction)", compactor.calls)
	}
	// Compactor should receive: [old summary, ...4 oldest unsummarized turns] = 5.
	if len(compactor.receivedTurns) != 5 {
		t.Fatalf("compactor received %d turns, want 5 (1 old summary + 4 new)", len(compactor.receivedTurns))
	}
	if compactor.receivedTurns[0].Role != "system_summary" {
		t.Fatalf("compactor input[0] role = %q, want system_summary (old summary)", compactor.receivedTurns[0].Role)
	}
	// History should be: [new summary, ...8 recent verbatim] = 9 turns.
	if len(planner.history) != 9 {
		t.Fatalf("history len = %d, want 9 after re-compaction", len(planner.history))
	}
}

// TestLoadHistoryWithoutCompactorPreservesBackwardCompatibility verifies
// that when compactor is nil, loadHistory behaves exactly like before: fetch
// historyTurnLimit turns, no summary, no compaction.
func TestLoadHistoryWithoutCompactorPreservesBackwardCompatibility(t *testing.T) {
	conversations := store.NewMemoryAssistantConversationStore()
	planner := &historyCapturingPlanner{intent: assistant.Intent{
		ToolName: tools.ClusterStatusRead,
		Input:    map[string]any{"environment": "prod"},
	}}
	// newAssistantWithStore constructs a service WITHOUT a compactor.
	service, _ := newAssistantWithStore(t, planner, conversations)

	ctx := context.Background()
	conv, err := conversations.CreateConversation(ctx, viewer().Subject, "test", "preview", time.Now())
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	// 10 pairs = 20 turns. Without compactor, only the 10 newest are loaded
	// (historyTurnLimit=10) and the 10 oldest are silently dropped.
	seedConversationTurns(t, conversations, conv.ID, 0, 10)

	_, err = service.HandleMessage(ctx, viewer(), "查看 prod 集群状态", conv.ID, assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if len(planner.history) != 10 {
		t.Fatalf("history len = %d, want 10 (historyTurnLimit)", len(planner.history))
	}
}

// TestLLMCompactorReturnsEmptyWhenChatIsNil verifies graceful degradation
// when no LLM is configured (e.g. deterministic planner mode). Returns empty
// string rather than erroring so the service falls back to no-compaction.
func TestLLMCompactorReturnsEmptyWhenChatIsNil(t *testing.T) {
	compactor := assistant.NewLLMCompactor(nil)
	summary, err := compactor.Compact(context.Background(), []assistant.Turn{
		{Role: "user", Content: "hello"},
	})
	if err != nil {
		t.Fatalf("Compact with nil chat: %v", err)
	}
	if summary != "" {
		t.Fatalf("summary = %q, want empty when chat is nil", summary)
	}
}
