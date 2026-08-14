package assistant

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
)

// TestFormatAssistantTurnNilIntent asserts that a nil Intent returns the
// original content unchanged. This is the backward-compat path: existing
// callers that do not populate Turn.Intent keep working.
func TestFormatAssistantTurnNilIntent(t *testing.T) {
	t.Parallel()
	got := formatAssistantTurn("hello world", nil)
	if got != "hello world" {
		t.Fatalf("got %q, want %q", got, "hello world")
	}
}

// TestFormatAssistantTurnToolIntent asserts that a tool Intent produces a
// [Last Intent] block with tool_name and input JSON.
func TestFormatAssistantTurnToolIntent(t *testing.T) {
	t.Parallel()
	intent := &Intent{
		ToolName: "kafka.consumer_lag.read",
		Input:    map[string]any{"environment": "prod", "group": "orders"},
	}
	got := formatAssistantTurn("lag = 1234", intent)

	if !contains(got, "[Last Intent]") {
		t.Fatalf("missing [Last Intent] block: %q", got)
	}
	if !contains(got, "tool_name: kafka.consumer_lag.read") {
		t.Fatalf("missing tool_name: %q", got)
	}
	if !contains(got, `"environment":"prod"`) {
		t.Fatalf("missing environment in input JSON: %q", got)
	}
	if !contains(got, `"group":"orders"`) {
		t.Fatalf("missing group in input JSON: %q", got)
	}
}

// TestFormatAssistantTurnDiagnosticIntent asserts that a diagnostic Intent
// produces a [Last Intent] block with the diagnostic fields (no tool_name).
func TestFormatAssistantTurnDiagnosticIntent(t *testing.T) {
	t.Parallel()
	intent := &Intent{
		Diagnostic: &diagnostics.Request{
			Domain:       "glusterfs",
			Environment:  "prod",
			ResourceType: "volume",
			ResourceName: "data",
		},
	}
	got := formatAssistantTurn("volume is healthy", intent)

	if !contains(got, "[Last Intent]") {
		t.Fatalf("missing [Last Intent] block: %q", got)
	}
	if !contains(got, "diagnostic:") {
		t.Fatalf("missing diagnostic: prefix: %q", got)
	}
	if !contains(got, "domain=glusterfs") {
		t.Fatalf("missing domain: %q", got)
	}
	if !contains(got, "environment=prod") {
		t.Fatalf("missing environment: %q", got)
	}
	if !contains(got, "resource_type=volume") {
		t.Fatalf("missing resource_type: %q", got)
	}
	if !contains(got, "resource_name=data") {
		t.Fatalf("missing resource_name: %q", got)
	}
	// Diagnostic path must not include tool_name.
	if contains(got, "tool_name:") {
		t.Fatalf("diagnostic block should not contain tool_name: %q", got)
	}
}

// TestFormatAssistantTurnEmptyToolName asserts that an Intent with neither
// ToolName nor Diagnostic returns the original content. This covers the case
// where Intent is non-nil but represents a clarification (no concrete intent).
func TestFormatAssistantTurnEmptyToolName(t *testing.T) {
	t.Parallel()
	intent := &Intent{} // zero value: no ToolName, no Diagnostic
	got := formatAssistantTurn("clarification needed", intent)
	if got != "clarification needed" {
		t.Fatalf("got %q, want original content (no intent block)", got)
	}
}

// TestFormatAssistantTurnMarshalingFailure asserts that when Input cannot be
// JSON-marshaled (e.g. contains a channel), the function falls back to the
// original content instead of panicking.
func TestFormatAssistantTurnMarshalingFailure(t *testing.T) {
	t.Parallel()
	intent := &Intent{
		ToolName: "some.tool",
		Input:    map[string]any{"bad": make(chan int)}, // channels cannot be JSON-marshaled
	}
	got := formatAssistantTurn("fallback content", intent)
	if got != "fallback content" {
		t.Fatalf("marshaling failure should fall back to original content; got %q", got)
	}
}

// TestFormatAssistantTurnContentPreserved asserts the original content is
// always at the start of the output, before the [Last Intent] block.
func TestFormatAssistantTurnContentPreserved(t *testing.T) {
	t.Parallel()
	intent := &Intent{
		ToolName: "cluster.status.read",
		Input:    map[string]any{"environment": "prod"},
	}
	got := formatAssistantTurn("status OK", intent)
	if !startsWith(got, "status OK") {
		t.Fatalf("content not at start: %q", got)
	}
}

// contains is a tiny strings.Contains replacement for test readability.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || indexOfString(s, substr) >= 0)
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func indexOfString(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestEstimateTurnCharsNoIntent asserts that a turn without Intent estimates
// to the length of its content.
func TestEstimateTurnCharsNoIntent(t *testing.T) {
	t.Parallel()
	turn := Turn{Role: "assistant", Content: "hello world"}
	got := estimateTurnChars(turn)
	if got != len("hello world") {
		t.Fatalf("got %d, want %d", got, len("hello world"))
	}
}

// TestEstimateTurnCharsWithToolIntent asserts that a turn with a tool Intent
// estimates larger than content alone, including space for the [Last Intent]
// block (template + tool_name + input JSON).
func TestEstimateTurnCharsWithToolIntent(t *testing.T) {
	t.Parallel()
	turn := Turn{
		Role:    "assistant",
		Content: "lag = 1234",
		Intent: &Intent{
			ToolName: "kafka.consumer_lag.read",
			Input:    map[string]any{"environment": "prod", "group": "orders"},
		},
	}
	got := estimateTurnChars(turn)
	if got <= len(turn.Content) {
		t.Fatalf("got %d, want > %d (content length)", got, len(turn.Content))
	}
	// sanity check that input marshals
	_ = contains(jsonMust(t, map[string]any{"environment": "prod", "group": "orders"}), "")
}

// TestEstimateTurnCharsWithDiagnosticIntent asserts that a diagnostic turn
// estimates larger than content alone, including the fixed diagnostic block
// size (150 chars).
func TestEstimateTurnCharsWithDiagnosticIntent(t *testing.T) {
	t.Parallel()
	turn := Turn{
		Role:    "assistant",
		Content: "healthy",
		Intent: &Intent{
			Diagnostic: &diagnostics.Request{Domain: "kafka", Environment: "prod"},
		},
	}
	got := estimateTurnChars(turn)
	if got <= len(turn.Content) {
		t.Fatalf("got %d, want > %d (content length)", got, len(turn.Content))
	}
}

func jsonMust(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// TestHistoryMessagesRespectsMaxTurns asserts that only the most recent
// maxHistoryTurns turns are kept when history exceeds the limit.
func TestHistoryMessagesRespectsMaxTurns(t *testing.T) {
	t.Parallel()
	history := make([]Turn, 20)
	for i := range history {
		history[i] = Turn{Role: "user", Content: "msg " + itoa(i)}
	}
	msgs := historyMessages(history)
	if len(msgs) != maxHistoryTurns {
		t.Fatalf("len(msgs) = %d, want %d", len(msgs), maxHistoryTurns)
	}
	// First kept message should be the 11th (index 10).
	if got := msgs[0].Content; got != "msg 10" {
		t.Fatalf("first msg = %q, want %q", got, "msg 10")
	}
}

// TestHistoryMessagesRespectsTokenBudget asserts that large turns trigger
// char-budget truncation before reaching maxHistoryTurns.
func TestHistoryMessagesRespectsTokenBudget(t *testing.T) {
	t.Parallel()
	// Each turn is 5000 chars; 4 turns = 20000 > maxHistoryChars (16000).
	// Expect at most 3 turns kept (15000 chars ≤ 16000).
	bigContent := strings.Repeat("a", 5000)
	history := []Turn{
		{Role: "user", Content: bigContent + "1"},
		{Role: "user", Content: bigContent + "2"},
		{Role: "user", Content: bigContent + "3"},
		{Role: "user", Content: bigContent + "4"},
	}
	msgs := historyMessages(history)
	if len(msgs) > 3 {
		t.Fatalf("len(msgs) = %d, want ≤ 3 (token budget)", len(msgs))
	}
	// Most recent turn must be kept.
	if len(msgs) == 0 {
		t.Fatalf("expected at least one message, got 0")
	}
}

// TestHistoryMessagesKeepsAtLeastOneTurn asserts that a single oversized
// turn is still kept (the "at least one" rule).
func TestHistoryMessagesKeepsAtLeastOneTurn(t *testing.T) {
	t.Parallel()
	hugeContent := strings.Repeat("x", 50_000) // far exceeds maxHistoryChars
	history := []Turn{{Role: "user", Content: hugeContent}}
	msgs := historyMessages(history)
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1 (at least one rule)", len(msgs))
	}
}

// TestHistoryMessagesEmptyHistory asserts the empty-input path.
func TestHistoryMessagesEmptyHistory(t *testing.T) {
	t.Parallel()
	msgs := historyMessages(nil)
	if len(msgs) != 0 {
		t.Fatalf("len(msgs) = %d, want 0", len(msgs))
	}
}

// TestHistoryMessagesSkipsEmptyContent asserts that turns with empty content
// are dropped (existing behavior).
func TestHistoryMessagesSkipsEmptyContent(t *testing.T) {
	t.Parallel()
	history := []Turn{
		{Role: "user", Content: ""},
		{Role: "assistant", Content: "   "},
		{Role: "user", Content: "real msg"},
	}
	msgs := historyMessages(history)
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1 (only non-empty)", len(msgs))
	}
	if msgs[0].Content != "real msg" {
		t.Fatalf("content = %q, want %q", msgs[0].Content, "real msg")
	}
}

// TestHistoryMessagesPreservesStructuredIntent asserts that an assistant turn
// with Intent produces a message containing the [Last Intent] block.
func TestHistoryMessagesPreservesStructuredIntent(t *testing.T) {
	t.Parallel()
	history := []Turn{
		{Role: "user", Content: "查 prod kafka orders group 的 lag"},
		{
			Role:    "assistant",
			Content: "lag = 1234",
			Intent: &Intent{
				ToolName: "kafka.consumer_lag.read",
				Input:    map[string]any{"environment": "prod", "group": "orders"},
			},
		},
		{Role: "user", Content: "同 environment 再查一个 payments group"},
	}
	msgs := historyMessages(history)
	if len(msgs) != 3 {
		t.Fatalf("len(msgs) = %d, want 3", len(msgs))
	}
	// Assistant message (index 1) must contain [Last Intent].
	assistantContent := msgs[1].Content
	if !contains(assistantContent, "[Last Intent]") {
		t.Fatalf("assistant msg missing [Last Intent] block: %q", assistantContent)
	}
	if !contains(assistantContent, "tool_name: kafka.consumer_lag.read") {
		t.Fatalf("missing tool_name in [Last Intent]: %q", assistantContent)
	}
	if !contains(assistantContent, `"environment":"prod"`) {
		t.Fatalf("missing environment in [Last Intent]: %q", assistantContent)
	}
}

// TestHistoryMessagesNilIntentBackwardCompat asserts that an assistant turn
// with nil Intent falls back to pure content (backward compat path).
func TestHistoryMessagesNilIntentBackwardCompat(t *testing.T) {
	t.Parallel()
	history := []Turn{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there", Intent: nil},
	}
	msgs := historyMessages(history)
	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2", len(msgs))
	}
	if msgs[1].Content != "hi there" {
		t.Fatalf("assistant content = %q, want %q (no intent block)", msgs[1].Content, "hi there")
	}
}

// TestHistoryMessagesIncludesSystemSummary asserts that a system_summary turn
// is injected as a system message prefixed with [对话摘要], so the LLM retains
// early-turn context after rolling compaction.
func TestHistoryMessagesIncludesSystemSummary(t *testing.T) {
	t.Parallel()
	history := []Turn{
		{Role: "system_summary", Content: "User previously checked prod Kafka lag."},
		{Role: "user", Content: "同 environment 查 MinIO"},
		{Role: "assistant", Content: "MinIO healthy"},
	}
	msgs := historyMessages(history)
	if len(msgs) != 3 {
		t.Fatalf("len(msgs) = %d, want 3 (summary + user + assistant)", len(msgs))
	}
	// First message must be the summary as a system message.
	if msgs[0].Role != "system" {
		t.Fatalf("msgs[0].Role = %q, want system", msgs[0].Role)
	}
	if !contains(msgs[0].Content, "[对话摘要]") {
		t.Fatalf("msgs[0] missing [对话摘要] prefix: %q", msgs[0].Content)
	}
	if !contains(msgs[0].Content, "prod Kafka lag") {
		t.Fatalf("msgs[0] missing summary content: %q", msgs[0].Content)
	}
}

// itoa is a tiny strconv.Itoa replacement to avoid importing strconv.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
