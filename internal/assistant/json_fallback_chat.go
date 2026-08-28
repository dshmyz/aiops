package assistant

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// jsonFallbackChat wraps a json-mode chat with a non-json-mode replica.
// Some OpenAI-compatible upstreams (observed via OpenRouter against GLM and
// DeepSeek models) intermittently honor response_format=json_object by
// returning an EMPTY message body instead of JSON — the HTTP call succeeds,
// so withChatRetry never sees a failure, and the planner dies on
// "input json is empty". Re-issuing the request WITHOUT json_object recovers:
// the planning prompt already demands strict JSON, and free-text mode reliably
// produces it.
type jsonFallbackChat struct {
	jsonModel    model.BaseChatModel
	plainModel   model.BaseChatModel
	fallbackUsed func() // nil-safe hook for tests
}

// withJSONFallback pairs the json-mode chat with a plain-mode replica built
// from the same config. Returns the json chat unchanged when no plain replica
// is provided.
func withJSONFallback(jsonModel, plainModel model.BaseChatModel, onFallback func()) model.BaseChatModel {
	if jsonModel == nil || plainModel == nil {
		return jsonModel
	}
	return &jsonFallbackChat{jsonModel: jsonModel, plainModel: plainModel, fallbackUsed: onFallback}
}

func (c *jsonFallbackChat) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	resp, err := c.jsonModel.Generate(ctx, input, opts...)
	if err == nil && !looksLikeJSON(resp) {
		if c.fallbackUsed != nil {
			c.fallbackUsed()
		}
		return c.plainModel.Generate(ctx, input, opts...)
	}
	return resp, err
}

// Stream delegates to the json-mode model. A partial stream can't be
// transparently replayed, mirroring retryChat's contract.
func (c *jsonFallbackChat) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return c.jsonModel.Stream(ctx, input, opts...)
}

// looksLikeJSON reports whether the model's message plausibly carries the
// planning JSON. Empty/whitespace-only bodies are the observed failure mode;
// a body without any '{' cannot parse either.
func looksLikeJSON(resp *schema.Message) bool {
	if resp == nil {
		return false
	}
	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return false
	}
	return strings.Contains(content, "{")
}
