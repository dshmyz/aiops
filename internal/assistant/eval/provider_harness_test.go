package eval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

// openAIStub is a minimal OpenAI-compatible /chat/completions server whose
// responses are scripted per call. It drives NewPlannerFromEnv through the
// REAL provider construction (eino-openai client, JSON mode, HTTP round
// trip) — the layer between "model said something" and "planner parsed it".
type openAIStub struct {
	server    *httptest.Server
	responses []string // raw assistant content per call; last repeats
	statuses  []int    // optional per-call HTTP status (0 => 200)
	calls     atomic.Int64
	bodies    chan string
}

func newOpenAIStub(t *testing.T, responses []string, statuses []int) *openAIStub {
	t.Helper()
	stub := &openAIStub{responses: responses, statuses: statuses, bodies: make(chan string, 16)}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		call := int(stub.calls.Add(1)) - 1
		_ = r.Body.Close()
		status := http.StatusOK
		if call < len(statuses) && statuses[call] != 0 {
			status = statuses[call]
		}
		content := responses[len(responses)-1]
		if call < len(responses) {
			content = responses[call]
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != http.StatusOK {
			_, _ = w.Write([]byte(`{"error":{"message":"stub error"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"stub","object":"chat.completion","created":1,"model":"stub-model","choices":[{"index":0,"message":{"role":"assistant","content":` + jsonString(content) + `},"finish_reason":"stop"}],"usage":{"total_tokens":10}}`))
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func jsonString(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func stubEnv(url string) map[string]string {
	return map[string]string{
		"COPILOT_ASSISTANT_PROVIDER": "eino-openai",
		"COPILOT_OPENAI_API_KEY":     "stub-key",
		"COPILOT_OPENAI_MODEL":       "stub-model",
		"COPILOT_OPENAI_BASE_URL":    url,
	}
}

// runViaProvider builds the planner through NewPlannerFromEnv against the
// stub and runs the loop with the given tool script, returning run + rendered.
func runViaProvider(t *testing.T, stub *openAIStub, maxSteps int, tool ToolScript) (assistant.AgentRun, assistant.Response) {
	t.Helper()
	planner, _, _, _, err := assistant.NewPlannerFromEnv(context.Background(), stubEnv(stub.server.URL))
	if err != nil {
		t.Fatalf("NewPlannerFromEnv: %v", err)
	}
	rec := &recorder{}
	loop := assistant.NewAgentLoop(
		planner,
		func(intent assistant.Intent, stepIndex int) (assistant.StepOutcome, error) {
			return (&scriptedExecute{script: tool, rec: rec}).invoke(intent, stepIndex)
		},
		maxSteps,
	)
	runPtr := loop.Run(context.Background(), identity.CurrentUser{Subject: "eval-provider"}, "排查 kafka 消费延迟", nil, assistant.PageContext{})
	return *runPtr, assistant.AgentRunResponse(runPtr)
}

// Provider boundary 1: the model returns broken JSON. The planner must fail
// gracefully (planning error path), the loop must terminate without executing
// tools, and the rendered answer must not look like a confident result.
func TestProviderBrokenJSONDegradesGracefully(t *testing.T) {
	stub := newOpenAIStub(t, []string{`{not valid json at all`}, nil)
	run, rendered := runViaProvider(t, stub, 3, func(intent assistant.Intent, _ int) (assistant.StepOutcome, error) {
		t.Errorf("no tool may execute when the planner cannot parse the model output, got %s", intent.ToolName)
		return toolOK(intent.ToolName, nil)
	})
	var fails []string
	if len(run.Steps) != 0 {
		fails = append(fails, "broken JSON must not produce executed steps")
	}
	if run.Reason == assistant.TerminalDone && !run.Fallback {
		fails = append(fails, "broken JSON must not end as a clean authoritative done")
	}
	if strings.TrimSpace(rendered.Message) == "" && run.Err == nil {
		fails = append(fails, "neither rendered answer nor run error — operator gets silence")
	}
	report(t, "provider-broken-json", fails)
}

// Provider boundary 2: the provider returns HTTP 500. Same invariants as
// broken JSON: no tool execution, honest termination.
func TestProviderHTTP500DegradesGracefully(t *testing.T) {
	stub := newOpenAIStub(t, []string{""}, []int{http.StatusInternalServerError})
	run, rendered := runViaProvider(t, stub, 3, func(intent assistant.Intent, _ int) (assistant.StepOutcome, error) {
		t.Errorf("no tool may execute when the provider errors, got %s", intent.ToolName)
		return toolOK(intent.ToolName, nil)
	})
	var fails []string
	if len(run.Steps) != 0 {
		fails = append(fails, "provider 500 must not produce executed steps")
	}
	if run.Reason == assistant.TerminalDone && !run.Fallback {
		fails = append(fails, "provider 500 must not end as a clean authoritative done")
	}
	if strings.TrimSpace(rendered.Message) == "" && run.Err == nil {
		fails = append(fails, "neither rendered answer nor run error — operator gets silence")
	}
	report(t, "provider-http-500", fails)
}

// Provider boundary 3: the full happy-ish path through the real HTTP shape —
// valid intent JSON over the wire executes the tool and the loop reaches a
// clean done. This guards against the stub layers drifting from the real
// provider contract (field names, response envelope).
func TestProviderValidIntentEndToEnd(t *testing.T) {
	stub := newOpenAIStub(t, []string{
		`{"tool_name":"kafka.metrics.read","input":{"cluster":"m1"},"diagnostic":null,"confidence":0.9,"explanation":"查指标","suggested_steps":1,"final_answer":false,"summary":null}`,
		finalJSON("指标正常，无消费积压。"),
	}, nil)
	run, rendered := runViaProvider(t, stub, 3, func(intent assistant.Intent, _ int) (assistant.StepOutcome, error) {
		if intent.ToolName != "kafka.metrics.read" {
			return toolErr(intent.ToolName, "unexpected tool")
		}
		return toolOK(intent.ToolName, map[string]any{"lag": 0})
	})
	var fails []string
	if run.Reason != assistant.TerminalDone || run.Fallback {
		fails = append(fails, "valid intent + successful tool should end as clean TerminalDone")
	}
	if rendered.Message != "指标正常，无消费积压。" {
		fails = append(fails, "rendered answer should carry the model summary verbatim when no step failed, got: "+rendered.Message)
	}
	report(t, "provider-valid-intent-e2e", fails)
}
