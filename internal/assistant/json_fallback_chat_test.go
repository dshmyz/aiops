package assistant

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// jsonFallbackStubChat returns scripted contents in order (the last repeats),
// or the scripted error when set.
type jsonFallbackStubChat struct {
	contents []string
	err      error
	calls    atomic.Int64
}

func (s *jsonFallbackStubChat) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if s.err != nil {
		s.calls.Add(1)
		return nil, s.err
	}
	idx := int(s.calls.Add(1)) - 1
	if idx >= len(s.contents) {
		idx = len(s.contents) - 1
	}
	return schema.AssistantMessage(s.contents[idx], nil), nil
}

func (s *jsonFallbackStubChat) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := s.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](1)
	sw.Send(msg, nil)
	sw.Close()
	return sr, nil
}

// Empty json_object body must trigger one plain-mode re-issue, which returns
// the recoverable intent.
func TestJSONFallbackRecoversEmptyResponse(t *testing.T) {
	jsonChat := &jsonFallbackStubChat{contents: []string{"   "}}
	plainChat := &jsonFallbackStubChat{contents: []string{`{"tool_name":"cluster.status.read","input":{},"confidence":0.9}`}}
	fallbacks := 0
	chat := withJSONFallback(jsonChat, plainChat, func() { fallbacks++ })

	resp, err := chat.Generate(context.Background(), []*schema.Message{schema.UserMessage("查看集群状态")})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if fallbacks != 1 {
		t.Fatalf("fallback calls = %d, want 1", fallbacks)
	}
	if jsonChat.calls.Load() != 1 || plainChat.calls.Load() != 1 {
		t.Fatalf("json=%d plain=%d calls, want 1/1", jsonChat.calls.Load(), plainChat.calls.Load())
	}
	if !strings.Contains(resp.Content, "cluster.status.read") {
		t.Fatalf("recovered response should carry the plain-model JSON, got: %q", resp.Content)
	}
}

// A healthy json response must pass through with zero fallback calls.
func TestJSONFallbackNotTriggeredOnValidJSON(t *testing.T) {
	jsonChat := &jsonFallbackStubChat{contents: []string{`{"tool_name":"cluster.status.read","input":{},"confidence":0.9}`}}
	plainChat := &jsonFallbackStubChat{}
	chat := withJSONFallback(jsonChat, plainChat, nil)

	if _, err := chat.Generate(context.Background(), nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if plainChat.calls.Load() != 0 {
		t.Fatalf("plain model called %d times, want 0", plainChat.calls.Load())
	}
}

// An error from the json model is a real failure — pass through unchanged,
// no plain-model fallback (fallback only covers the silent-empty failure).
func TestJSONFallbackPassesThroughHardError(t *testing.T) {
	jsonChat := &jsonFallbackStubChat{err: context.DeadlineExceeded}
	plainChat := &jsonFallbackStubChat{}
	chat := withJSONFallback(jsonChat, plainChat, nil)

	_, err := chat.Generate(context.Background(), nil)
	if err != context.DeadlineExceeded {
		t.Fatalf("want the json model's error passed through, got %v", err)
	}
	if plainChat.calls.Load() != 0 {
		t.Fatalf("plain model must not be called on hard error, got %d calls", plainChat.calls.Load())
	}
}

// withJSONFallback without a plain replica must be a no-op wrapper.
func TestJSONFallbackNilPlainReturnsOriginal(t *testing.T) {
	jsonChat := &jsonFallbackStubChat{contents: []string{"   "}}
	if got := withJSONFallback(jsonChat, nil, nil); got != model.BaseChatModel(jsonChat) {
		t.Fatalf("withJSONFallback(nil plain) should return the original chat")
	}
}
