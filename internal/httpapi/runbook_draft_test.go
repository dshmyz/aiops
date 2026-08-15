package httpapi_test

import (
	"context"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestInferRunbookDraftRetentionProducesSequence(t *testing.T) {
	ensureMiddlewareTools(t)
	draft := httpapi.InferRunbookDraft("id-1", "retention", []string{"把保留改成 72 小时"})
	if draft.MissingReason != "" {
		t.Fatalf("MissingReason = %q, want empty for runbookable topic", draft.MissingReason)
	}
	// 风险用工具真实风险（topic.retention.set=medium），而非硬编码 low——
	// retention 调整可能删数据，如实标记避免被低风险自动执行路径消费。
	if draft.Name == "" || draft.RiskLevel != "medium" {
		t.Fatalf("draft = %+v, want name/risk populated (retention tool risk=medium)", draft)
	}
	if len(draft.ToolSequence) == 0 || draft.ToolSequence[0] != "topic.retention.set" {
		t.Fatalf("ToolSequence = %v, want to include topic.retention.set", draft.ToolSequence)
	}
	if len(draft.IntentPattern) == 0 {
		t.Fatal("IntentPattern should not be empty")
	}
}

func TestInferRunbookDraftCapabilityCallUsesReadTools(t *testing.T) {
	draft := httpapi.InferRunbookDraft("id-2", "capability-call", []string{"工具调用失败"})
	if draft.MissingReason != "" {
		t.Fatalf("MissingReason = %q, want empty", draft.MissingReason)
	}
	if len(draft.ToolSequence) == 0 {
		t.Fatal("ToolSequence should be non-empty")
	}
	// 生成序列只能引用合法只读工具名，不得凭空造路由不到的工具。
	for _, tm := range draft.ToolSequence {
		if tm != tools.ClusterStatusRead && tm != tools.QuerySystemPosture {
			t.Fatalf("ToolSequence contains unexpected tool %q", tm)
		}
	}
}

func TestInferRunbookDraftNonRunbookableTopicSkipped(t *testing.T) {
	draft := httpapi.InferRunbookDraft("id-3", "format", []string{"结果太啰嗦"})
	if draft.MissingReason == "" {
		t.Fatal("MissingReason should be set for non-runbookable topic")
	}
	if len(draft.ToolSequence) != 0 {
		t.Fatalf("ToolSequence = %v, want empty for skipped topic", draft.ToolSequence)
	}
}

func TestRunbookDraftServiceActivateWritesRegistry(t *testing.T) {
	ctx := context.Background()
	runbookStore := store.NewMemoryRunbookStore()
	svc := httpapi.NewRunbookDraftService(runbookStore)

	draft, err := svc.Infer(ctx, "latency", []string{"响应太慢"})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if draft.ID == "" || draft.Status != "draft" {
		t.Fatalf("draft = %+v, want id+status draft", draft)
	}

	activated, err := svc.Activate(ctx, draft.ID)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if activated.Status != "activated" {
		t.Fatalf("activated.Status = %q, want activated", activated.Status)
	}

	// 确认后应立即被 RunbookRouter 的可读视图命中（enable 语义落到注册表）。
	enabled, err := runbookStore.ListEnabledRunbooks(ctx)
	if err != nil {
		t.Fatalf("ListEnabledRunbooks: %v", err)
	}
	found := false
	for _, rb := range enabled {
		if rb.Slug == activated.Slug && rb.IsEnabled {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("activated runbook %q not in enabled registry: %+v", activated.Slug, enabled)
	}
	// 已启用 runbook 不再留在草稿列表。
	left, _ := svc.List(ctx)
	for _, d := range left {
		if d.ID == draft.ID {
			t.Fatalf("activated draft %q still present in draft list", draft.ID)
		}
	}
}

func TestRunbookDraftServiceActivateMissing(t *testing.T) {
	ctx := context.Background()
	svc := httpapi.NewRunbookDraftService(store.NewMemoryRunbookStore())
	if _, err := svc.Activate(ctx, "does-not-exist"); err != store.ErrNotFound {
		t.Fatalf("Activate missing = %v, want store.ErrNotFound", err)
	}
}

func TestRunbookDraftServiceActivateNonRunbookableRejected(t *testing.T) {
	ctx := context.Background()
	svc := httpapi.NewRunbookDraftService(store.NewMemoryRunbookStore())
	draft, err := svc.Infer(ctx, "format", []string{"太啰嗦"})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if _, err := svc.Activate(ctx, draft.ID); err == nil {
		t.Fatal("Activate of non-runbookable draft should error")
	}
}
