package assistant

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// retryChat wraps a model.BaseChatModel so that transient one-shot completion
// failures are retried a bounded number of times before giving up. We saw the
// OpenAI-compatible provider occasionally drop the connection or stall while
// awaiting response headers ("Client.Timeout exceeded while awaiting headers"),
// which otherwise surfaces to the user as a hard "请求失败". The stream path is
// passed through unchanged — a partial stream can't be transparently replayed.
type retryChat struct {
	model.BaseChatModel
	maxAttempts int
	baseBackoff time.Duration
}

// WithChatRetry wraps chat so Generate retries transient failures up to
// maxAttempts times (>=1) with an exponential backoff starting at baseBackoff.
func withChatRetry(chat model.BaseChatModel, maxAttempts int, baseBackoff time.Duration) model.BaseChatModel {
	if chat == nil || maxAttempts < 1 {
		return chat
	}
	return &retryChat{BaseChatModel: chat, maxAttempts: maxAttempts, baseBackoff: baseBackoff}
}

// Generate overrides the embedded model's Generate with retry-on-transient.
func (c *retryChat) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	var resp *schema.Message
	var lastErr error
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		var err error
		resp, err = c.BaseChatModel.Generate(ctx, input, opts...)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if attempt == c.maxAttempts || !isTransientChatError(ctx, err) {
			break
		}
		// 429 用更长退避（3s, 6s, 12s），其他瞬态错误用标准退避
		delay := backoffFor(attempt, c.baseBackoff)
		if isRateLimitError(err) {
			delay = time.Duration(attempt) * 3 * time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return resp, lastErr
}

// isRateLimitError 判断是否是 429 限流错误。
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "429") || strings.Contains(msg, "too many requests") || strings.Contains(msg, "rate limit")
}

// isTransientChatError reports whether err is worth retrying: a network-level
// timeout or connection failure, or an HTTP 5xx. It returns false when the
// caller's own ctx has expired (retrying there would be pointless) or the
// error is a definitive 4xx/model error we shouldn't replay.
func isTransientChatError(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	// Our provider surfaces "Post ...: context deadline exceeded (Client.Timeout
	// exceeded while awaiting headers)" as a *url.Error wrapping a net timeout.
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	msg := err.Error()
	if strings.Contains(msg, "Client.Timeout exceeded while awaiting headers") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "EOF") {
		return true
	}
	// HTTP 429/5xx from an OpenAI-compatible backend.
	lower := strings.ToLower(msg)
	return strings.Contains(lower, " 429") ||
		strings.Contains(lower, " 500") || strings.Contains(lower, " 502") ||
		strings.Contains(lower, " 503") || strings.Contains(lower, " 504")
}

// backoffFor returns the delay before the nth retry: base, 2*base, 4*base, ...
// capped at 8*base to keep worst-case wait bounded.
func backoffFor(attempt int, base time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	mult := 1 << (attempt - 1)
	if mult > 8 {
		mult = 8
	}
	return base * time.Duration(mult)
}
