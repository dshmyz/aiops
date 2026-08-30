package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// seedExecutionForTest 创建一个 plan + 对应 execution，返回 execution ID。
// startedAt 为 nil 时表示执行中（未完成），非 nil 时已完成。
// toolName 用于测试 tool_name 过滤（需通过 action_plan 关联）。
func seedExecutionForTest(t *testing.T, repo store.ActionPlanStore, planID, toolName, status string, startedAt *time.Time, createdAt time.Time) string {
	t.Helper()
	ctx := context.Background()
	plan := store.PlanRecord{
		ID:        planID,
		RequestID: "req-" + planID,
		CreatedBy: "tester",
		ToolName:  toolName,
		Status:    store.PlanConfirmed,
		Version:   1,
		ExpiresAt: createdAt.Add(time.Hour),
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
	if err := repo.CreatePlan(ctx, plan, store.AuditEvent{ID: "audit-" + planID, Action: "plan_created", Decision: "permitted", CreatedAt: createdAt}); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	execID := "exec-" + planID
	exec := store.ExecutionRecord{
		ID:             execID,
		ActionPlanID:   planID,
		IdempotencyKey: "key-" + planID,
		Status:         status,
		StartedAt:      startedAt,
		CreatedAt:      createdAt,
	}
	if _, _, err := repo.CreateExecutionIfAbsent(ctx, exec, store.AuditEvent{ID: "audit-exec-" + planID, Action: "execution_started", Decision: "permitted", CreatedAt: createdAt}); err != nil {
		t.Fatalf("CreateExecutionIfAbsent: %v", err)
	}
	return execID
}

// TestListExecutionsReturnsAllByDefault 验证：无过滤时返回全部 execution，
// 按 created_at DESC 排序（最新在前），且 ToolName 从关联 plan 填充（R5 结果准 - 查询 API）。
func TestListExecutionsReturnsAllByDefault(t *testing.T) {
	t.Parallel()
	repo := store.NewMemoryActionPlanStore()
	ctx := context.Background()
	base := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	started := base.Add(time.Minute)
	seedExecutionForTest(t, repo, "plan-1", "topic.retention.set", "succeeded", &started, base)
	seedExecutionForTest(t, repo, "plan-2", "minio.bucket.quota.set", "failed", &started, base.Add(time.Second))

	page, err := repo.ListExecutions(ctx, store.ExecutionFilter{})
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if len(page.Executions) != 2 {
		t.Fatalf("executions = %d, want 2", len(page.Executions))
	}
	// DESC by created_at：plan-2（base+1s）在前
	if page.Executions[0].ID != "exec-plan-2" {
		t.Fatalf("first execution = %q, want exec-plan-2 (newest first)", page.Executions[0].ID)
	}
	// ToolName 从关联 plan 填充
	if page.Executions[0].ToolName != "minio.bucket.quota.set" {
		t.Fatalf("ToolName = %q, want minio.bucket.quota.set (joined from plan)", page.Executions[0].ToolName)
	}
	if page.Executions[1].ToolName != "topic.retention.set" {
		t.Fatalf("ToolName = %q, want topic.retention.set", page.Executions[1].ToolName)
	}
}

// TestListExecutionsFiltersByStatus 验证按执行状态过滤。
func TestListExecutionsFiltersByStatus(t *testing.T) {
	t.Parallel()
	repo := store.NewMemoryActionPlanStore()
	ctx := context.Background()
	base := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	started := base.Add(time.Minute)
	seedExecutionForTest(t, repo, "plan-1", "topic.retention.set", "succeeded", &started, base)
	seedExecutionForTest(t, repo, "plan-2", "minio.bucket.quota.set", "failed", &started, base.Add(time.Second))
	seedExecutionForTest(t, repo, "plan-3", "kafka.consumer.lag.read", "running", nil, base.Add(2*time.Second))

	page, err := repo.ListExecutions(ctx, store.ExecutionFilter{Status: "failed"})
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if len(page.Executions) != 1 {
		t.Fatalf("executions = %d, want 1 (failed only)", len(page.Executions))
	}
	if page.Executions[0].ID != "exec-plan-2" {
		t.Fatalf("execution = %q, want exec-plan-2", page.Executions[0].ID)
	}
}

// TestListExecutionsFiltersByActionPlanID 验证按 action_plan_id 查某 plan 的执行历史。
func TestListExecutionsFiltersByActionPlanID(t *testing.T) {
	t.Parallel()
	repo := store.NewMemoryActionPlanStore()
	ctx := context.Background()
	base := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	started := base.Add(time.Minute)
	seedExecutionForTest(t, repo, "plan-1", "topic.retention.set", "succeeded", &started, base)
	seedExecutionForTest(t, repo, "plan-1b", "topic.retention.set", "succeeded", &started, base.Add(time.Second))

	page, err := repo.ListExecutions(ctx, store.ExecutionFilter{ActionPlanID: "plan-1"})
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if len(page.Executions) != 1 {
		t.Fatalf("executions = %d, want 1 (plan-1 only)", len(page.Executions))
	}
	if page.Executions[0].ActionPlanID != "plan-1" {
		t.Fatalf("action_plan_id = %q, want plan-1", page.Executions[0].ActionPlanID)
	}
}

// TestListExecutionsFiltersByToolName 验证按 tool_name 过滤（需关联 plan）。
func TestListExecutionsFiltersByToolName(t *testing.T) {
	t.Parallel()
	repo := store.NewMemoryActionPlanStore()
	ctx := context.Background()
	base := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	started := base.Add(time.Minute)
	seedExecutionForTest(t, repo, "plan-1", "topic.retention.set", "succeeded", &started, base)
	seedExecutionForTest(t, repo, "plan-2", "minio.bucket.quota.set", "failed", &started, base.Add(time.Second))
	seedExecutionForTest(t, repo, "plan-3", "topic.retention.set", "succeeded", &started, base.Add(2*time.Second))

	page, err := repo.ListExecutions(ctx, store.ExecutionFilter{ToolName: "topic.retention.set"})
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if len(page.Executions) != 2 {
		t.Fatalf("executions = %d, want 2 (topic.retention.set only)", len(page.Executions))
	}
	for _, e := range page.Executions {
		if e.ToolName != "topic.retention.set" {
			t.Fatalf("ToolName = %q, want topic.retention.set", e.ToolName)
		}
	}
}

// TestListExecutionsFiltersByStartedAfterBefore 验证按 started_at 时间范围过滤。
// started_at 为 nil 的执行（running 状态）不匹配任何时间范围。
func TestListExecutionsFiltersByStartedAfterBefore(t *testing.T) {
	t.Parallel()
	repo := store.NewMemoryActionPlanStore()
	ctx := context.Background()
	base := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	started1 := base.Add(time.Minute)
	started2 := base.Add(2 * time.Minute)
	seedExecutionForTest(t, repo, "plan-1", "topic.retention.set", "succeeded", &started1, base)
	seedExecutionForTest(t, repo, "plan-2", "minio.bucket.quota.set", "succeeded", &started2, base.Add(time.Second))

	// 只查 started_at >= base+90s（即 started2）
	page, err := repo.ListExecutions(ctx, store.ExecutionFilter{
		StartedAfter: base.Add(90 * time.Second),
	})
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if len(page.Executions) != 1 {
		t.Fatalf("executions = %d, want 1 (started_after filter)", len(page.Executions))
	}
	if page.Executions[0].ID != "exec-plan-2" {
		t.Fatalf("execution = %q, want exec-plan-2", page.Executions[0].ID)
	}

	// 只查 started_at <= base+90s（即 started1）
	page, err = repo.ListExecutions(ctx, store.ExecutionFilter{
		StartedBefore: base.Add(90 * time.Second),
	})
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if len(page.Executions) != 1 {
		t.Fatalf("executions = %d, want 1 (started_before filter)", len(page.Executions))
	}
	if page.Executions[0].ID != "exec-plan-1" {
		t.Fatalf("execution = %q, want exec-plan-1", page.Executions[0].ID)
	}
}

// TestListExecutionsPaginatesByKeyset 验证 keyset 分页：Limit 限制页大小，
// NextCursor 指向当前页最后一条，传回 cursor 取下一页。
func TestListExecutionsPaginatesByKeyset(t *testing.T) {
	t.Parallel()
	repo := store.NewMemoryActionPlanStore()
	ctx := context.Background()
	base := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	started := base.Add(time.Minute)
	seedExecutionForTest(t, repo, "plan-1", "topic.retention.set", "succeeded", &started, base)
	seedExecutionForTest(t, repo, "plan-2", "topic.retention.set", "succeeded", &started, base.Add(time.Second))
	seedExecutionForTest(t, repo, "plan-3", "topic.retention.set", "succeeded", &started, base.Add(2*time.Second))

	// 第一页：Limit=2，返回最新的 2 条（plan-3, plan-2），NextCursor 指向 plan-2
	page1, err := repo.ListExecutions(ctx, store.ExecutionFilter{Limit: 2})
	if err != nil {
		t.Fatalf("ListExecutions page1: %v", err)
	}
	if len(page1.Executions) != 2 {
		t.Fatalf("page1 executions = %d, want 2", len(page1.Executions))
	}
	if page1.Executions[0].ID != "exec-plan-3" {
		t.Fatalf("page1 first = %q, want exec-plan-3", page1.Executions[0].ID)
	}
	if page1.Executions[1].ID != "exec-plan-2" {
		t.Fatalf("page1 second = %q, want exec-plan-2", page1.Executions[1].ID)
	}
	if page1.NextCursor.CreatedAt.IsZero() {
		t.Fatal("page1 NextCursor is empty, want cursor for next page")
	}

	// 第二页：用第一页的 cursor，返回 plan-1
	page2, err := repo.ListExecutions(ctx, store.ExecutionFilter{
		Limit:           2,
		CursorCreatedAt: page1.NextCursor.CreatedAt,
		CursorID:        page1.NextCursor.ID,
	})
	if err != nil {
		t.Fatalf("ListExecutions page2: %v", err)
	}
	if len(page2.Executions) != 1 {
		t.Fatalf("page2 executions = %d, want 1", len(page2.Executions))
	}
	if page2.Executions[0].ID != "exec-plan-1" {
		t.Fatalf("page2 first = %q, want exec-plan-1", page2.Executions[0].ID)
	}
	if !page2.NextCursor.CreatedAt.IsZero() {
		t.Fatal("page2 NextCursor should be empty (no more pages)")
	}
}
