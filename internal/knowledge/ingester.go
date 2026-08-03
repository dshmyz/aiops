package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
)

// Source 常量：标识知识文档的来源类型，便于检索过滤与 UI 展示。
const (
	SourceExecutionHistory = "execution-history"
	SourceExecutionFailure = "execution-failure"
)

// ExecutionIngester implements execution.ExecutionObserver. After each
// successful execution it formats the operational context (tool, input, operator,
// timestamp) into a knowledge document and ingests it into the store. Over
// time this builds an organic runbook from real operations, enabling the RAG
// layer to surface "上次执行这个操作是…" context to the planner.
//
// Failed executions are also ingested (with a distinct source tag) so the
// assistant can warn operators about known failure patterns.
type ExecutionIngester struct {
	store    Store
	embedder Embedder
}

// NewExecutionIngester creates an observer that writes execution records into
// the knowledge store. embedder may be nil (falls back to zero-vector storage).
func NewExecutionIngester(store Store, embedder Embedder) *ExecutionIngester {
	return &ExecutionIngester{store: store, embedder: embedder}
}

// OnExecutionComplete formats the execution event as a knowledge document and
// adds it to the store. Errors are intentionally swallowed: knowledge ingestion
// is best-effort and must never block or fail the execution pipeline.
func (ing *ExecutionIngester) OnExecutionComplete(ctx context.Context, event execution.ExecutionEvent) {
	if ing == nil || ing.store == nil {
		return
	}
	doc := ing.buildDocument(event)
	embedded := EmbeddedDocument{Document: doc}
	if ing.embedder != nil {
		if vec, err := ing.embedder.Embed(ctx, doc.Title+"\n"+doc.Content); err == nil {
			embedded.Embedding = vec
		}
	}
	_ = ing.store.Add(ctx, embedded)
}

// buildDocument formats an execution event into a structured knowledge
// document suitable for RAG retrieval. The document includes RequestID for
// request correlation, ResultSummary for outcome context, and Verification
// for post-execution validation results.
func (ing *ExecutionIngester) buildDocument(event execution.ExecutionEvent) Document {
	source := SourceExecutionHistory
	if event.Status == "failed" {
		source = SourceExecutionFailure
	}

	title := fmt.Sprintf("[%s] %s 执行记录", event.Status, event.ToolName)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("工具：%s\n", event.ToolName))
	sb.WriteString(fmt.Sprintf("状态：%s\n", event.Status))
	sb.WriteString(fmt.Sprintf("操作人：%s\n", event.Subject))
	sb.WriteString(fmt.Sprintf("时间：%s\n", event.Timestamp.Format(time.RFC3339)))
	if event.RequestID != "" {
		sb.WriteString(fmt.Sprintf("请求 ID：%s\n", event.RequestID))
	}
	if len(event.Input) > 0 {
		inputJSON, err := json.Marshal(event.Input)
		if err == nil {
			sb.WriteString(fmt.Sprintf("参数：%s\n", string(inputJSON)))
		}
	}
	if event.ResultSummary != "" {
		sb.WriteString(fmt.Sprintf("结果摘要：%s\n", event.ResultSummary))
	}
	if event.Verification != nil {
		sb.WriteString(fmt.Sprintf("验证工具：%s\n", event.Verification.ToolName))
		sb.WriteString(fmt.Sprintf("验证状态：%s\n", event.Verification.Status))
		if event.Verification.ElapsedMs > 0 {
			sb.WriteString(fmt.Sprintf("验证耗时：%dms\n", event.Verification.ElapsedMs))
		}
		if event.Verification.Error != "" {
			sb.WriteString(fmt.Sprintf("验证错误：%s\n", event.Verification.Error))
		}
	}
	sb.WriteString(fmt.Sprintf("Plan ID：%s", event.PlanID))

	return Document{
		Title:     title,
		Content:   sb.String(),
		Source:    source,
		CreatedAt: event.Timestamp,
	}
}
