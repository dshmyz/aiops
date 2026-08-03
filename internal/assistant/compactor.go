package assistant

import (
	"context"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
)

// Compactor reduces a sequence of conversation turns into a short text
// summary. The summary replaces the oldest turns in the planner's history
// window so long conversations stay within the LLM's token budget without
// losing early context.
//
// Implementations must be safe for concurrent use. The Service calls Compact
// at most once per HandleMessage invocation, only when the unsummarized turn
// count exceeds the configured threshold.
type Compactor interface {
	Compact(ctx context.Context, turns []Turn) (string, error)
}

// Rolling summarization thresholds. When the unsummarized turn count exceeds
// compactThreshold, the oldest (count - keepRecentTurns) turns are compacted
// into a summary. The most recent keepRecentTurns are always preserved
// verbatim so the planner can resolve short-term references like "刚才那个".
const (
	compactThreshold = 12
	keepRecentTurns  = 8
	compactBatchSize = compactThreshold - keepRecentTurns // 4 turns per round
)

// compactorTracer returns the tracer for compaction spans.
func compactorTracer() trace.Tracer {
	return otel.Tracer("github.com/gracegaoya/ai-operations-copilot/assistant/compactor")
}

// LLMCompactor implements Compactor using a chat model. It reuses the same
// model.BaseChatModel as EinoPlanner so a single LLM provider serves both
// planning and summarization.
type LLMCompactor struct {
	chat  model.BaseChatModel
	audit *llmAuditRecorder // nil → 不记录 LLM 调用审计（缺口-5 / R1）
}

// NewLLMCompactor wraps an existing chat model for rolling summarization.
// The chat model must be the same instance (or same provider) used by the
// planner so token budgets and rate limits are shared.
func NewLLMCompactor(chat model.BaseChatModel) *LLMCompactor {
	return &LLMCompactor{chat: chat}
}

// WithLLMAudit wires LLM invocation auditing (缺口-5 / R1). audit may be nil.
func (c *LLMCompactor) WithLLMAudit(auditSvc *audit.Service, model string) *LLMCompactor {
	c.audit = newLLMAuditRecorder(auditSvc, model, "compactor")
	return c
}

const compactionPrompt = `You are summarizing a multi-turn operations assistant conversation.
Compress the following turns into a concise summary (max 200 words) that preserves:
- Which tools/capabilities were invoked and their key parameters
- Any decisions, errors, or clarifications
- User preferences or constraints mentioned
Return plain text only, no JSON.`

// Compact calls the LLM to produce a summary of the given turns. The turns
// slice includes the previous summary (if any) as the first element with
// Role="system_summary"; subsequent elements are raw user/assistant turns.
func (c *LLMCompactor) Compact(ctx context.Context, turns []Turn) (string, error) {
	ctx, span := compactorTracer().Start(ctx, "compactor.Compact")
	defer span.End()
	if c == nil || c.chat == nil {
		return "", nil
	}
	messages := []*schema.Message{schema.SystemMessage(compactionPrompt)}
	for _, turn := range turns {
		content := turn.Content
		if content == "" {
			continue
		}
		switch turn.Role {
		case "system_summary":
			messages = append(messages, schema.SystemMessage("[Previous Summary]\n"+content))
		case "user":
			messages = append(messages, schema.UserMessage(content))
		case "assistant":
			messages = append(messages, schema.AssistantMessage(content, nil))
		}
	}
	started := time.Now()
	response, err := c.chat.Generate(ctx, messages)
	if err != nil {
		span.RecordError(err)
		return "", err
	}
	if c.audit != nil {
		c.audit.record(ctx, started, response)
	}
	return response.Content, nil
}
