package assistant_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestEinoPlannerParsesModelJSONIntent(t *testing.T) {
	t.Parallel()
	chat := fakeEinoChatModel{content: `{"tool_name":"cluster.status.read","input":{"environment":"prod"},"confidence":0.91,"explanation":"read cluster status"}`}
	planner := assistant.NewEinoPlanner(&chat)

	intent, err := planner.Plan(context.Background(), user(), "查看 prod 集群状态", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if intent.ToolName != tools.ClusterStatusRead || intent.Input["environment"] != "prod" || intent.Confidence != 0.91 {
		t.Fatalf("intent = %+v, want cluster read intent", intent)
	}
	if len(chat.input) != 2 || chat.input[0].Role != schema.System || chat.input[1].Role != schema.User {
		t.Fatalf("model input = %+v, want system and user messages", chat.input)
	}
}

func TestEinoPlannerClarifiesWhenModelReturnsNoTool(t *testing.T) {
	t.Parallel()
	chat := fakeEinoChatModel{content: `{"tool_name":"","input":{},"confidence":0.2,"explanation":"unclear"}`}
	planner := assistant.NewEinoPlanner(&chat)

	_, err := planner.Plan(context.Background(), user(), "帮我看看", nil, assistant.PageContext{})
	if !errors.Is(err, assistant.ErrClarificationNeeded) {
		t.Fatalf("error = %v, want ErrClarificationNeeded", err)
	}
}

func TestEinoPlannerParsesModelJSONDiagnosticIntent(t *testing.T) {
	t.Parallel()
	chat := fakeEinoChatModel{content: `{"tool_name":"","input":{},"diagnostic":{"domain":"glusterfs","environment":"prod","resource_type":"volume","resource_name":"data","runbook":"health"},"confidence":0.88,"explanation":"check volume health"}`}
	planner := assistant.NewEinoPlanner(&chat)

	intent, err := planner.Plan(context.Background(), user(), "检查 prod glusterfs data volume 健康", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if intent.Diagnostic == nil {
		t.Fatalf("intent = %+v, want diagnostic intent", intent)
	}
	if intent.Diagnostic.Domain != "glusterfs" || intent.Diagnostic.Environment != "prod" || intent.Diagnostic.ResourceName != "data" {
		t.Fatalf("diagnostic = %+v, want glusterfs prod data request", intent.Diagnostic)
	}
	if intent.ToolName != "" || intent.Confidence != 0.88 {
		t.Fatalf("intent = %+v, want diagnostic-only candidate", intent)
	}
}

func TestEinoPlannerIsCandidateOnlyAndMayReturnUnknownTool(t *testing.T) {
	t.Parallel()
	chat := fakeEinoChatModel{content: `{"tool_name":"shell.exec","input":{"command":"rm -rf /"},"confidence":0.99,"explanation":"forged"}`}
	planner := assistant.NewEinoPlanner(&chat)

	intent, err := planner.Plan(context.Background(), user(), "do dangerous thing", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if intent.ToolName != "shell.exec" {
		t.Fatalf("intent = %+v, want candidate unknown tool", intent)
	}
}

func TestEinoPlannerClarifiesWhenConfidenceBelowThreshold(t *testing.T) {
	t.Parallel()
	chat := fakeEinoChatModel{content: `{"tool_name":"cluster.status.read","input":{"environment":"prod"},"confidence":0.5,"explanation":"unclear request"}`}
	planner := assistant.NewEinoPlanner(&chat)

	_, err := planner.Plan(context.Background(), user(), "帮我看看集群", nil, assistant.PageContext{})
	if !errors.Is(err, assistant.ErrClarificationNeeded) {
		t.Fatalf("error = %v, want ErrClarificationNeeded", err)
	}
}

type fakeEinoChatModel struct {
	content          string
	reasoningContent string
	streamChunks     []*schema.Message
	streamErr        error
	input            []*schema.Message
}

func (m *fakeEinoChatModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.input = input
	msg := schema.AssistantMessage(m.content, nil)
	msg.ReasoningContent = m.reasoningContent
	return msg, nil
}

func (m *fakeEinoChatModel) Stream(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.input = input
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	if len(m.streamChunks) > 0 {
		return schema.StreamReaderFromArray(m.streamChunks), nil
	}
	// No explicit chunks: stream the full content as a single chunk so
	// tests that only set `content` still exercise the streaming path.
	msg := schema.AssistantMessage(m.content, nil)
	msg.ReasoningContent = m.reasoningContent
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func TestEinoPlannerPlanStreamForwardsDeltasAndTerminalIntent(t *testing.T) {
	t.Parallel()
	// Split the JSON across three chunks to verify deltas are forwarded
	// verbatim and the concatenated text parses to the same intent as Plan.
	json := `{"tool_name":"cluster.status.read","input":{"environment":"prod"},"confidence":0.91,"explanation":"read cluster status"}`
	chunks := []*schema.Message{
		schema.AssistantMessage(json[:15], nil),
		schema.AssistantMessage(json[15:60], nil),
		schema.AssistantMessage(json[60:], nil),
	}
	chat := fakeEinoChatModel{streamChunks: chunks}
	planner := assistant.NewEinoPlanner(&chat)

	events, err := planner.PlanStream(context.Background(), user(), "查看 prod 集群状态", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("PlanStream start: %v", err)
	}
	var (
		deltas  []string
		intent  *assistant.Intent
		done    bool
		lastErr error
	)
	for ev := range events {
		if ev.Delta != "" {
			deltas = append(deltas, ev.Delta)
		}
		if ev.Done {
			done = true
			intent = ev.Intent
			lastErr = ev.Err
		}
	}
	if !done {
		t.Fatal("no terminal event received")
	}
	if lastErr != nil {
		t.Fatalf("terminal err = %v, want nil", lastErr)
	}
	if intent == nil || intent.ToolName != tools.ClusterStatusRead || intent.Input["environment"] != "prod" || intent.Confidence != 0.91 {
		t.Fatalf("intent = %+v, want cluster read intent", intent)
	}
	if got, want := strings.Join(deltas, ""), json; got != want {
		t.Fatalf("concatenated deltas = %q, want %q", got, want)
	}
}

func TestEinoPlannerPlanStreamDegradedToPlanWhenStreamErrors(t *testing.T) {
	t.Parallel()
	// chat.Stream returns an error; PlanStream must fall back to Plan
	// (chat.Generate) and emit a single terminal Done event with the intent.
	chat := fakeEinoChatModel{
		content:   `{"tool_name":"cluster.status.read","input":{"environment":"prod"},"confidence":0.91,"explanation":"ok"}`,
		streamErr: errors.New("stream unavailable"),
	}
	planner := assistant.NewEinoPlanner(&chat)

	events, err := planner.PlanStream(context.Background(), user(), "查看 prod 集群状态", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("PlanStream start: %v", err)
	}
	var (
		deltas  []string
		intent  *assistant.Intent
		done    bool
		lastErr error
	)
	for ev := range events {
		if ev.Delta != "" {
			deltas = append(deltas, ev.Delta)
		}
		if ev.Done {
			done = true
			intent = ev.Intent
			lastErr = ev.Err
		}
	}
	if !done {
		t.Fatal("no terminal event received")
	}
	if lastErr != nil {
		t.Fatalf("terminal err = %v, want nil (fallback should succeed)", lastErr)
	}
	if intent == nil || intent.ToolName != tools.ClusterStatusRead {
		t.Fatalf("intent = %+v, want cluster read intent from fallback", intent)
	}
	if len(deltas) != 0 {
		t.Fatalf("deltas = %v, want none in fallback mode", deltas)
	}
}

func TestEinoPlannerPlanStreamForwardsReasoningAsThinking(t *testing.T) {
	t.Parallel()
	// Model returns reasoning content interleaved with answer content.
	// PlanStream must forward reasoning chunks as Thinking events and
	// answer chunks as Delta events.
	reasoning := "用户想查看集群状态，需要调用 cluster.status.read 工具"
	json := `{"tool_name":"cluster.status.read","input":{"environment":"prod"},"confidence":0.91,"explanation":"read cluster status"}`
	chunks := []*schema.Message{
		{Role: schema.Assistant, ReasoningContent: reasoning[:10]},
		{Role: schema.Assistant, ReasoningContent: reasoning[10:]},
		{Role: schema.Assistant, Content: json[:20]},
		{Role: schema.Assistant, Content: json[20:]},
	}
	chat := fakeEinoChatModel{streamChunks: chunks}
	planner := assistant.NewEinoPlanner(&chat)

	events, err := planner.PlanStream(context.Background(), user(), "查看 prod 集群状态", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("PlanStream start: %v", err)
	}
	var (
		thinking []string
		deltas   []string
		intent   *assistant.Intent
		done     bool
	)
	for ev := range events {
		if ev.Thinking != "" {
			thinking = append(thinking, ev.Thinking)
		}
		if ev.Delta != "" {
			deltas = append(deltas, ev.Delta)
		}
		if ev.Done {
			done = true
			intent = ev.Intent
		}
	}
	if !done {
		t.Fatal("no terminal event received")
	}
	if intent == nil || intent.ToolName != tools.ClusterStatusRead {
		t.Fatalf("intent = %+v, want cluster read intent", intent)
	}
	if got := strings.Join(thinking, ""); got != reasoning {
		t.Fatalf("concatenated thinking = %q, want %q", got, reasoning)
	}
	if got := strings.Join(deltas, ""); got != json {
		t.Fatalf("concatenated deltas = %q, want %q", got, json)
	}
}

func TestEinoPlannerPlanStreamNoReasoningWhenModelDoesNotReturnIt(t *testing.T) {
	t.Parallel()
	// When the model does not return reasoning content, no Thinking events
	// should be emitted.
	json := `{"tool_name":"cluster.status.read","input":{"environment":"prod"},"confidence":0.91,"explanation":"ok"}`
	chat := fakeEinoChatModel{content: json}
	planner := assistant.NewEinoPlanner(&chat)

	events, err := planner.PlanStream(context.Background(), user(), "查看 prod 集群状态", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("PlanStream start: %v", err)
	}
	var thinking []string
	for ev := range events {
		if ev.Thinking != "" {
			thinking = append(thinking, ev.Thinking)
		}
	}
	if len(thinking) != 0 {
		t.Fatalf("thinking = %v, want none when model has no reasoning", thinking)
	}
}
