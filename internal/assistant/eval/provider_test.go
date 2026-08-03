package eval

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestScriptedProviderReturnsCaseResponse asserts that Generate returns the
// scripted JSON for the case name propagated through ctx.
func TestScriptedProviderReturnsCaseResponse(t *testing.T) {
	t.Parallel()
	cases := []Case{
		{
			Name:        "tool/case_one",
			ScriptedLLM: `{"tool_name":"cluster.status.read","input":{},"confidence":0.9,"explanation":"one"}`,
		},
		{
			Name:        "tool/case_two",
			ScriptedLLM: `{"tool_name":"topic.retention.set","input":{},"confidence":0.8,"explanation":"two"}`,
		},
	}
	provider := NewScriptedProvider(cases)

	for _, c := range cases {
		ctx := withCaseName(context.Background(), c.Name)
		msg, err := provider.Generate(ctx, nil)
		if err != nil {
			t.Fatalf("Generate %q: %v", c.Name, err)
		}
		if msg.Content != c.ScriptedLLM {
			t.Fatalf("Generate %q content = %q, want %q", c.Name, msg.Content, c.ScriptedLLM)
		}
	}
}

// TestScriptedProviderFailsOnUnknownCase asserts that Generate returns an
// error when ctx has no case name or the name is not registered. This guards
// against silent miswiring: a missing case name means the framework lost
// track of which case is running.
func TestScriptedProviderFailsOnUnknownCase(t *testing.T) {
	t.Parallel()
	provider := NewScriptedProvider([]Case{{Name: "registered", ScriptedLLM: "{}"}})

	// No case name in ctx.
	if _, err := provider.Generate(context.Background(), nil); err == nil {
		t.Fatalf("Generate with empty ctx: want error, got nil")
	}

	// Unknown case name.
	ctx := withCaseName(context.Background(), "not_registered")
	if _, err := provider.Generate(ctx, nil); err == nil {
		t.Fatalf("Generate with unknown case: want error, got nil")
	}
}

// TestScriptedProviderRecordsCallOrder asserts that Generate records the order
// of case names invoked. The Runner can use this to verify every case ran.
func TestScriptedProviderRecordsCallOrder(t *testing.T) {
	t.Parallel()
	cases := []Case{
		{Name: "a", ScriptedLLM: "{}"},
		{Name: "b", ScriptedLLM: "{}"},
		{Name: "c", ScriptedLLM: "{}"},
	}
	provider := NewScriptedProvider(cases)

	for _, c := range cases {
		ctx := withCaseName(context.Background(), c.Name)
		if _, err := provider.Generate(ctx, nil); err != nil {
			t.Fatalf("Generate %q: %v", c.Name, err)
		}
	}

	got := provider.CallOrder()
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("CallOrder len = %d, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i] != name {
			t.Fatalf("CallOrder[%d] = %q, want %q", i, got[i], name)
		}
	}
}

// TestScriptedProviderStreamWrapsGenerate asserts that Stream returns a single
// frame whose content matches what Generate would return for the same case.
// Eval does not exercise streaming, but BaseChatModel requires the method.
func TestScriptedProviderStreamWrapsGenerate(t *testing.T) {
	t.Parallel()
	content := `{"tool_name":"cluster.status.read","input":{},"confidence":0.9,"explanation":"ok"}`
	provider := NewScriptedProvider([]Case{{Name: "stream_case", ScriptedLLM: content}})

	ctx := withCaseName(context.Background(), "stream_case")
	reader, err := provider.Stream(ctx, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer reader.Close()

	var sb strings.Builder
	for {
		frame, frameErr := reader.Recv()
		if frameErr != nil {
			if errors.Is(frameErr, io.EOF) {
				break
			}
			t.Fatalf("Recv: %v", frameErr)
		}
		if frame == nil {
			continue
		}
		sb.WriteString(frame.Content)
	}

	if sb.String() != content {
		t.Fatalf("streamed content = %q, want %q", sb.String(), content)
	}
}
