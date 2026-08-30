package assistant

import (
	"context"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
)

// llmAuditRecorder 是 EinoPlanner / LLMFormatter / LLMCompactor 共享的
// LLM 调用审计记录器（缺口-5 / R1）。审计服务为 nil 时记录为 no-op，
// 保证现有测试（不注入 audit）行为不变。
type llmAuditRecorder struct {
	audit     *audit.Service
	model     string
	component string // planner / formatter / compactor
	now       func() time.Time
}

// newLLMAuditRecorder 创建一个 LLM 审计记录器。audit 可 nil（no-op）。
func newLLMAuditRecorder(audit *audit.Service, model, component string) *llmAuditRecorder {
	return &llmAuditRecorder{audit: audit, model: model, component: component, now: time.Now}
}

// record 在 LLM 调用后记录 llm_invoked 审计事件。response 提供 token 用量。
func (r *llmAuditRecorder) record(ctx context.Context, started time.Time, response *schema.Message) {
	if r == nil || r.audit == nil {
		return
	}
	metadata := map[string]any{
		"model":      r.model,
		"component":  r.component,
		"latency_ms": time.Since(started).Milliseconds(),
	}
	if response != nil && response.ResponseMeta != nil && response.ResponseMeta.Usage != nil {
		metadata["prompt_tokens"] = response.ResponseMeta.Usage.PromptTokens
		metadata["completion_tokens"] = response.ResponseMeta.Usage.CompletionTokens
		metadata["total_tokens"] = response.ResponseMeta.Usage.TotalTokens
	}
	event := audit.Event{
		ID:        uuid.NewString(),
		Subject:   "system:llm",
		ToolName:  r.component,
		Action:    audit.ActionLLMInvoked,
		Decision:  audit.DecisionPermitted,
		Metadata:  metadata,
		CreatedAt: r.now(),
	}
	_ = r.audit.Record(ctx, event)
}
