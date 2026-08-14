package assistant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// fakeChat is a controllable model.BaseChatModel that records how many times
// Generate was called and returns a scripted result.
type fakeChat struct {
	calls   int
	results []func() (*schema.Message, error)
}

func (f *fakeChat) Generate(ctx context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if f.calls >= len(f.results) {
		return nil, errors.New("fakeChat: out of scripted results")
	}
	fn := f.results[f.calls]
	f.calls++
	return fn()
}

func (f *fakeChat) Stream(ctx context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("unused")
}

func errSink(msg string) func() (*schema.Message, error) {
	return func() (*schema.Message, error) { return nil, errors.New(msg) }
}

func okMsg() func() (*schema.Message, error) {
	return func() (*schema.Message, error) { return &schema.Message{Role: schema.Assistant}, nil }
}

// TestRetryChatSucceedsAfterTransient: a caller-side parent context that stays
// alive; the first attempt fails with a transient network timeout and the
// second succeeds. The wrapper must retry and return the success.
func TestRetryChatTransientThenSuccess(t *testing.T) {
	inner := &fakeChat{results: []func() (*schema.Message, error){
		errSink("Post url: context deadline exceeded (Client.Timeout exceeded while awaiting headers)"),
		okMsg(),
	}}
	rc := withChatRetry(inner, 3, time.Millisecond).(*retryChat)
	resp, err := rc.Generate(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error after retry: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if inner.calls != 2 {
		t.Fatalf("calls = %d, want 2", inner.calls)
	}
}

// TestRetryChatGivesUpAfterMax: persistent transient failures still fail after
// maxAttempts (no infinite loop).
func TestRetryChatGivesUpAfterMax(t *testing.T) {
	inner := &fakeChat{results: []func() (*schema.Message, error){
		errSink("connection reset"), errSink("connection reset"), errSink("connection reset"),
	}}
	rc := withChatRetry(inner, 2, time.Millisecond).(*retryChat)
	if _, err := rc.Generate(context.Background(), nil); err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if inner.calls != 2 {
		t.Fatalf("calls = %d, want 2 (maxAttempts)", inner.calls)
	}
}

// TestRetryChatDoesNotRetryNonTransient: a definitive 4xx/model error should not
// be replayed.
func TestRetryChatDoesNotRetryNonTransient(t *testing.T) {
	inner := &fakeChat{results: []func() (*schema.Message, error){
		errSink("invalid_api_key: bad key"), okMsg(),
	}}
	rc := withChatRetry(inner, 3, time.Millisecond).(*retryChat)
	if _, err := rc.Generate(context.Background(), nil); err == nil {
		t.Fatal("expected error for non-transient failure")
	}
	if inner.calls != 1 {
		t.Fatalf("calls = %d, want 1 (no retry on non-transient)", inner.calls)
	}
}

// TestRetryChatStopsWhenParentCtxDone: if the caller's context is already
// cancelled, no retry should be attempted — the error is returned directly.
func TestRetryChatStopsWhenParentCtxDone(t *testing.T) {
	inner := &fakeChat{results: []func() (*schema.Message, error){
		errSink("context deadline exceeded"), okMsg(),
	}}
	rc := withChatRetry(inner, 3, time.Millisecond).(*retryChat)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := rc.Generate(ctx, nil); err == nil {
		t.Fatal("expected error")
	}
	if inner.calls != 1 {
		t.Fatalf("calls = %d, want 1 (no retry after ctx cancelled)", inner.calls)
	}
}

// TestRetryAttemptsParsing: COPILOT_OPENAI_RETRY controls attempt count; bad
// values fall back to the default.
func TestRetryAttemptsParsing(t *testing.T) {
	if got := retryAttempts(map[string]string{}); got != defaultChatRetry {
		t.Errorf("default attempts = %d, want %d", got, defaultChatRetry)
	}
	if got := retryAttempts(map[string]string{envOpenAIRetry: "4"}); got != 4 {
		t.Errorf("attempts = %d, want 4", got)
	}
	if got := retryAttempts(map[string]string{envOpenAIRetry: "-1"}); got != defaultChatRetry {
		t.Errorf("negative attempts = %d, want default", got)
	}
	if got := retryAttempts(map[string]string{envOpenAIRetry: "abc"}); got != defaultChatRetry {
		t.Errorf("non-numeric attempts = %d, want default", got)
	}
}

// TestBackoffForCaps: backoff grows by powers of two and is capped.
func TestBackoffForCaps(t *testing.T) {
	base := 100 * time.Millisecond
	if got := backoffFor(1, base); got != base {
		t.Errorf("backoff(1) = %v, want %v", got, base)
	}
	if got := backoffFor(2, base); got != 2*base {
		t.Errorf("backoff(2) = %v, want %v", got, 2*base)
	}
	if got := backoffFor(10, base); got != 8*base {
		t.Errorf("backoff(10) = %v, want %v (capped at 8x)", got, 8*base)
	}
}

// TestIsTransientChatError: the exact observed error string is classified as
// transient, and a nil/cancelled ctx is not.
func TestIsTransientChatError(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		msg string
		ok  bool
	}{
		{"Post x: context deadline exceeded (Client.Timeout exceeded while awaiting headers)", true},
		{"connection reset by peer", true},
		{"Post x: dial tcp: connection refused", true},
		{"unexpected EOF", true},
		{"backend returned 503 Service Unavailable", true},
		{"invalid_api_key for org", false},
		{"status code 400", false},
	}
	for _, c := range cases {
		if got := isTransientChatError(ctx, errors.New(c.msg)); got != c.ok {
			t.Errorf("isTransient(%q) = %v, want %v", c.msg, got, c.ok)
		}
	}
	if isTransientChatError(context.TODO(), errors.New("boom")) {
		t.Error("nil ctx should not be transient")
	}
}
