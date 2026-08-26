package assistant_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// TestEinoPlannerRealHTTPPath verifies the full HTTP round trip through the
// Eino OpenAI chat model. An httptest server mocks /v1/chat/completions and
// asserts that the planner sends a valid JSON request with the expected
// Authorization header and model name.
func TestEinoPlannerRealHTTPPath(t *testing.T) {
	t.Parallel()
	intentJSON := `{"tool_name":"cluster.status.read","input":{},"confidence":0.95,"explanation":"check cluster status"}`
	var requestCount int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want 'Bearer test-key'", auth)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal request body: %v", err)
		}
		if req.Model != "test-model" {
			t.Fatalf("model = %q, want test-model", req.Model)
		}
		if len(req.Messages) == 0 {
			t.Fatal("expected non-empty messages")
		}

		resp := map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": 1234567890,
			"model":   req.Model,
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": intentJSON,
					},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	planner, _, _, _, err := assistant.NewPlannerFromEnv(context.Background(), map[string]string{
		"COPILOT_ASSISTANT_PROVIDER": "eino-openai",
		"COPILOT_OPENAI_API_KEY":     "test-key",
		"COPILOT_OPENAI_MODEL":       "test-model",
		"COPILOT_OPENAI_BASE_URL":    server.URL,
	})
	if err != nil {
		t.Fatalf("new planner: %v", err)
	}

	intent, err := planner.Plan(context.Background(), user(), "查看 集群状态", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan returned %v", err)
	}
	if intent.ToolName != tools.ClusterStatusRead {
		t.Fatalf("intent = %+v, want cluster.status.read", intent)
	}
	if requestCount == 0 {
		t.Fatal("mock server did not receive any request")
	}
}

// TestEinoPlannerRealHTTPPathStream exercises PlanStream over a real HTTP
// connection. The mock server returns SSE frames; the planner should forward
// deltas and parse the final intent.
func TestEinoPlannerRealHTTPPathStream(t *testing.T) {
	t.Parallel()
	intentJSON := `{"tool_name":"cluster.status.read","input":{},"confidence":0.95,"explanation":"check cluster status"}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Stream the intent in chunks.
		chunks := []string{intentJSON[:15], intentJSON[15:50], intentJSON[50:]}
		for _, chunk := range chunks {
			data, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{
					{
						"delta": map[string]any{"content": chunk},
					},
				},
			})
			_, _ = w.Write([]byte("data: " + string(data) + "\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		// Finish marker.
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer server.Close()

	planner, _, _, _, err := assistant.NewPlannerFromEnv(context.Background(), map[string]string{
		"COPILOT_ASSISTANT_PROVIDER": "eino-openai",
		"COPILOT_OPENAI_API_KEY":     "test-key",
		"COPILOT_OPENAI_MODEL":       "test-model",
		"COPILOT_OPENAI_BASE_URL":    server.URL,
	})
	if err != nil {
		t.Fatalf("new planner: %v", err)
	}
	ep, ok := planner.(*assistant.EinoPlanner)
	if !ok {
		t.Fatalf("planner type = %T, want *EinoPlanner", planner)
	}

	events, err := ep.PlanStream(context.Background(), user(), "查看 集群状态", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("PlanStream returned %v", err)
	}
	var deltas []string
	var done bool
	var finalIntent *assistant.Intent
	for ev := range events {
		if ev.Delta != "" {
			deltas = append(deltas, ev.Delta)
		}
		if ev.Done {
			done = true
			finalIntent = ev.Intent
		}
	}
	if !done {
		t.Fatal("no terminal event from streaming planner")
	}
	if len(deltas) == 0 {
		t.Fatal("no deltas forwarded from stream")
	}
	if finalIntent == nil || finalIntent.ToolName != tools.ClusterStatusRead {
		t.Fatalf("finalIntent = %+v, want cluster.status.read", finalIntent)
	}
	if strings.Join(deltas, "") != intentJSON {
		t.Fatalf("concat deltas = %q, want %q", strings.Join(deltas, ""), intentJSON)
	}
}

// TestLLMCompactorRealHTTPPath verifies the full HTTP round trip through the
// LLMCompactor. An httptest server mocks /chat/completions and asserts that
// the compactor sends the compaction system prompt plus conversation turns,
// and returns the LLM-produced summary.
func TestLLMCompactorRealHTTPPath(t *testing.T) {
	t.Parallel()
	expectedSummary := "User checked prod Kafka consumer lag (orders group = 1234). Then queried MinIO bucket status."
	var receivedMessages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		receivedMessages = req.Messages

		resp := map[string]any{
			"id":      "chatcmpl-compact",
			"object":  "chat.completion",
			"created": 1234567890,
			"model":   "test-model",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": expectedSummary,
					},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	_, compactor, _, _, err := assistant.NewPlannerFromEnv(context.Background(), map[string]string{
		"COPILOT_ASSISTANT_PROVIDER": "eino-openai",
		"COPILOT_OPENAI_API_KEY":     "test-key",
		"COPILOT_OPENAI_MODEL":       "test-model",
		"COPILOT_OPENAI_BASE_URL":    server.URL,
	})
	if err != nil {
		t.Fatalf("new planner: %v", err)
	}
	if compactor == nil {
		t.Fatal("compactor is nil for eino-openai provider")
	}

	turns := []assistant.Turn{
		{Role: "system_summary", Content: "Previous: user checked cluster status."},
		{Role: "user", Content: "查 kafka orders group 的 lag"},
		{Role: "assistant", Content: "lag = 1234"},
		{Role: "user", Content: "再查 MinIO bucket"},
		{Role: "assistant", Content: "MinIO healthy"},
	}
	summary, err := compactor.Compact(context.Background(), turns)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if summary != expectedSummary {
		t.Fatalf("summary = %q, want %q", summary, expectedSummary)
	}

	// Verify the LLM received the compaction system prompt + all turns.
	if len(receivedMessages) < 4 {
		t.Fatalf("LLM received %d messages, want >= 4 (system + turns)", len(receivedMessages))
	}
	// First message must be the compaction system prompt.
	if receivedMessages[0].Role != "system" {
		t.Fatalf("first message role = %q, want system", receivedMessages[0].Role)
	}
	if !strings.Contains(receivedMessages[0].Content, "summarizing") {
		t.Fatalf("first message not compaction prompt: %q", receivedMessages[0].Content)
	}
	// Second message should be the previous summary injected as system.
	if receivedMessages[1].Role != "system" || !strings.Contains(receivedMessages[1].Content, "Previous") {
		t.Fatalf("second message = %+v, want previous summary as system", receivedMessages[1])
	}
}
