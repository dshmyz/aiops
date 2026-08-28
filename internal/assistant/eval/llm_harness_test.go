package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

// scriptedChatModel is a fake eino chat model whose Generate returns one
// scripted JSON intent per call, in order. It drives the REAL EinoPlanner
// through the real AgentLoop — exercising prompt assembly, JSON parsing,
// intent post-processing, and the loop's control flow end to end — without a
// network. "Model behavior" is the script: by encoding common failure modes
// (fabricated conclusions, identical retries, missing disclosure), this layer
// verifies the wiring's guardrails actually catch them.
type scriptedChatModel struct {
	mu      sync.Mutex
	// responses are returned in order; the last one repeats when exhausted.
	responses []string
	calls     int
	prompts   []string // received system prompts, for assertions on catalog injection
}

func newScriptedChat(responses ...string) *scriptedChatModel {
	return &scriptedChatModel{responses: responses}
}

func (m *scriptedChatModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range input {
		if msg.Role == schema.System {
			m.prompts = append(m.prompts, msg.Content)
		}
	}
	idx := m.calls
	if idx >= len(m.responses) {
		idx = len(m.responses) - 1
	}
	m.calls++
	return schema.AssistantMessage(m.responses[idx], nil), nil
}

func (m *scriptedChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](1)
	sw.Send(msg, nil)
	sw.Close()
	return sr, nil
}

func (m *scriptedChatModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// intentJSON builds one einoIntent JSON payload.
func intentJSON(tool string, input map[string]any, conf float64, steps int) string {
	return fmt.Sprintf(`{"tool_name":%q,"input":%s,"diagnostic":null,"confidence":%g,"explanation":"eval","suggested_steps":%d,"final_answer":false,"summary":null}`,
		tool, mustJSON(input), conf, steps)
}

func finalJSON(summary string) string {
	return fmt.Sprintf(`{"tool_name":null,"input":null,"diagnostic":null,"confidence":1,"explanation":"eval","suggested_steps":0,"final_answer":true,"summary":%q}`, summary)
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// --- LLM-level eval runner -------------------------------------------------

// LLMCase pairs a user message + scripted model responses with behavioral
// assertions. The real EinoPlanner and AgentLoop run the whole pipeline.
type LLMCase struct {
	Name     string
	Message  string
	MaxSteps int
	// ModelResponses are raw JSON intents the fake model returns per call.
	ModelResponses []string
	// Tool is the scripted tool behavior.
	Tool ToolScript
	// Catalog is appended to the system prompt (like prod wiring); empty omits it.
	Catalog string
	// Assert receives the run AND the response text the wiring would render.
	Assert func(run assistant.AgentRun, rendered assistant.Response) []string
}

// RunLLMCase executes an LLM-level case through the real planner + loop +
// response mapping, returning aggregated failures.
func RunLLMCase(c LLMCase) []string {
	fake := newScriptedChat(c.ModelResponses...)
	planner := assistant.NewEinoPlanner(fake)
	if c.Catalog != "" {
		planner = planner.WithCapabilityCatalog(c.Catalog)
	}
	rec := &recorder{}
	loop := assistant.NewAgentLoop(
		planner,
		func(intent assistant.Intent, stepIndex int) (assistant.StepOutcome, error) {
			return (&scriptedExecute{script: c.Tool, rec: rec}).invoke(intent, stepIndex)
		},
		c.MaxSteps,
	)
	runPtr := loop.Run(context.Background(), identity.CurrentUser{Subject: "eval-llm"}, c.Message, nil, assistant.PageContext{})
	run := *runPtr
	rendered := assistant.AgentRunResponse(runPtr)
	failures := c.Assert(run, rendered)

	// Cross-layer invariant: every model response must have been consumed as
	// a valid intent — a parse failure silently degrades to planning errors
	// and the case would pass vacuously.
	if fake.callCount() == 0 {
		failures = append(failures, c.Name+": fake model was never called — planner wiring is broken")
	}
	if len(rec.outcomes) == 0 && !strings.Contains(rendered.Message, "澄清") && run.Reason != assistant.TerminalClarification && run.Reason != assistant.TerminalDone {
		// no steps executed and no clarification/done: likely the JSON never
		// parsed into a tool call. Flag it so the case is not vacuous.
		failures = append(failures, c.Name+": no tool steps executed and no clarification — model JSON may have failed to parse into an intent")
	}
	return failures
}

// --- guardrail cases: bad-model behaviors the wiring must contain ----------

// A model that fabricates a confident conclusion after a single failing tool
// call. The loop must NOT mark it TerminalDone-without-Fallback, and the
// rendered answer must disclose the failure rather than pass the fabrication
// through as an authoritative result.
func TestLLMFabricatedConclusionAfterFailure(t *testing.T) {
	failures := RunLLMCase(LLMCase{
		Name:     "llm-fabricated-conclusion-after-failure",
		Message:  "排查 kafka 消费延迟",
		MaxSteps: 3,
		ModelResponses: []string{
			intentJSON("kafka.metrics.read", map[string]any{"cluster": "m1"}, 0.9, 1),
			// 模型直接编结论："已确认是 broker 宕机"（其实工具全失败）
			finalJSON("已确认根因是 broker 宕机，已自动恢复。"),
		},
		Tool: func(intent assistant.Intent, _ int) (assistant.StepOutcome, error) {
			return toolErr(intent.ToolName, "upstream 500")
		},
		Assert: func(run assistant.AgentRun, rendered assistant.Response) []string {
			var fails []string
			lower := strings.ToLower(rendered.Message)
			// 编造的"已确认根因"可以被透传（我们无法判断真假），但渲染文本
			// 必须让操作者知道工具失败过——否则编造就无法被识别。
			if run.Reason == assistant.TerminalDone && !run.Fallback {
				if !strings.Contains(lower, "失败") && !strings.Contains(lower, "500") && !strings.Contains(lower, "未") {
					fails = append(fails, "model gave a final answer right after a failed tool call, and the rendered text hides the failure — fabrication is undetectable to the operator: "+rendered.Message)
				}
			}
			return fails
		},
	})
	report(t, "llm-fabricated-conclusion-after-failure", failures)
}

// A model that keeps re-issuing the identical failing call. The loop's failure
// feedback + repeated-read convergence must stop it from burning the whole
// budget on duplicates.
func TestLLMRepeatedFailingCallConverges(t *testing.T) {
	failures := RunLLMCase(LLMCase{
		Name:     "llm-repeated-failing-call",
		Message:  "排查 kafka 消费延迟",
		MaxSteps: 8,
		// 模型铁了心原样重调 8 次
		ModelResponses: []string{
			intentJSON("kafka.metrics.read", map[string]any{"cluster": "m1"}, 0.9, 8),
		},
		Tool: func(intent assistant.Intent, _ int) (assistant.StepOutcome, error) {
			return toolErr(intent.ToolName, "upstream 500")
		},
		Assert: func(run assistant.AgentRun, rendered assistant.Response) []string {
			var fails []string
			if run.Reason == assistant.TerminalDone && !run.Fallback {
				fails = append(fails, "endless identical failing calls must not end as a clean TerminalDone")
			}
			// 渲染文本必须如实呈现全部失败
			lower := strings.ToLower(rendered.Message)
			if !strings.Contains(lower, "kafka.metrics.read") && !strings.Contains(lower, "失败") {
				fails = append(fails, "rendered answer must disclose the repeated failing tool, got: "+rendered.Message)
			}
			return fails
		},
	})
	report(t, "llm-repeated-failing-call", failures)
}

// A model that answers an out-of-domain question by inventing a tool call.
// The loop executes whatever the planner picks; the guardrail here is that
// the fabricated tool errors out and the failure is disclosed.
func TestLLMInventedToolFailsHonestly(t *testing.T) {
	failures := RunLLMCase(LLMCase{
		Name:     "llm-invented-tool",
		Message:  "帮我修一下打印机卡纸",
		MaxSteps: 3,
		Catalog:  "## 可用的动态能力（工具）\n- `kafka.metrics.read`",
		ModelResponses: []string{
			// 编造一个不存在的工具
			intentJSON("printer.fix.paper_jam", map[string]any{}, 0.7, 1),
		},
		Tool: func(intent assistant.Intent, _ int) (assistant.StepOutcome, error) {
			return toolErr(intent.ToolName, "unknown tool: printer.fix.paper_jam")
		},
		Assert: func(run assistant.AgentRun, rendered assistant.Response) []string {
			var fails []string
			lower := strings.ToLower(rendered.Message)
			if !strings.Contains(lower, "printer") && !strings.Contains(lower, "失败") && !strings.Contains(lower, "未") {
				fails = append(fails, "invented tool failure must surface to the operator, got: "+rendered.Message)
			}
			return fails
		},
	})
	report(t, "llm-invented-tool", failures)
}

// report prints a case result in the distribution-report format; failures
// fail the test, passes just log so a full run reads like a scorecard.
func report(t *testing.T, name string, failures []string) {
	t.Helper()
	if len(failures) > 0 {
		for _, f := range failures {
			t.Errorf("[%s] %s", name, f)
		}
		return
	}
	t.Logf("[PASS] %s", name)
}
