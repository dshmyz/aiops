package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// TestTickOnceSkipsExpiredTask 验证：任务到期超过 maxLag 时跳过执行，
// 不调用 capability，记 skipped audit，并推进 next_run_at（专项：任务准-并发安全）。
func TestTickOnceSkipsExpiredTask(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	runner := &fakeReadRunner{}
	sched, repository, taskStore := newTestScheduler(t, runner, now)
	ctx := context.Background()

	// 任务到期时间在 maxLag（1小时）之前
	task := sampleScheduledTask(now)
	task.NextRunAt = now.Add(-2 * time.Hour) // 到期 2 小时，超过 maxLag
	created, err := taskStore.CreateTask(ctx, task)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := sched.tickOnce(ctx); err != nil {
		t.Fatalf("tickOnce: %v", err)
	}

	// 不应执行 capability
	if runner.callCount() != 0 {
		t.Fatalf("runner calls = %d, want 0 (expired task should be skipped)", runner.callCount())
	}

	// 应记 skipped audit
	events := repository.AuditEvents()
	found := false
	for _, evt := range events {
		if evt.Action == audit.ActionScheduledTaskSkipped && evt.ToolName == task.CapabilityName {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no skipped audit event found, events = %+v", events)
	}

	// next_run_at 应被推进（不卡在过期时间）
	updated, err := taskStore.GetTask(ctx, created.ID, "alice")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if !updated.NextRunAt.After(now) {
		t.Fatalf("next_run_at = %v, want after now (should advance past expiry)", updated.NextRunAt)
	}

	// 不应写入 run 记录
	runs, err := taskStore.ListRuns(ctx, created.ID, 0)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs = %d, want 0 (skipped task should not produce run)", len(runs))
	}
}

// TestTickOnceClaimsTaskBeforeExecute 验证：scheduler 在执行前先 CAS 认领任务，
// 认领成功才执行 capability（专项：任务准-并发安全）。
func TestTickOnceClaimsTaskBeforeExecute(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	runner := &fakeReadRunner{}
	sched, _, taskStore := newTestScheduler(t, runner, now)
	ctx := context.Background()

	task := sampleScheduledTask(now)
	task.NextRunAt = now.Add(-time.Minute) // 已到期，未过期
	created, err := taskStore.CreateTask(ctx, task)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := sched.tickOnce(ctx); err != nil {
		t.Fatalf("tickOnce: %v", err)
	}

	// 认领成功 → 执行 capability
	if runner.callCount() != 1 {
		t.Fatalf("runner calls = %d, want 1 (claimed task should execute)", runner.callCount())
	}

	// task 的 next_run_at 应被推进（ClaimTask + AppendRunAndUpdateTask）
	updated, err := taskStore.GetTask(ctx, created.ID, "alice")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if !updated.NextRunAt.After(now) {
		t.Fatalf("next_run_at = %v, want after now", updated.NextRunAt)
	}
}

// TestTickOnceSkipsTaskAlreadyClaimed 验证：ClaimTask 返回 false（已被其他实例认领）时，
// 不执行 capability，不写 run（专项：任务准-并发安全）。
func TestTickOnceSkipsTaskAlreadyClaimed(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	runner := &fakeReadRunner{}
	sched, _, taskStore := newTestScheduler(t, runner, now)
	ctx := context.Background()

	task := sampleScheduledTask(now)
	task.NextRunAt = now.Add(-time.Minute)
	created, err := taskStore.CreateTask(ctx, task)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	// 模拟其他实例已认领：提前用 ClaimTask 推进 next_run_at
	newNextRun := now.Add(5 * time.Minute)
	if claimed, _ := taskStore.ClaimTask(ctx, created.ID, created.NextRunAt, newNextRun, now); !claimed {
		t.Fatal("pre-claim should succeed")
	}

	// scheduler tickOnce 时，DB 中 next_run_at 已 > now，ListDueTasks 不再返回此 task
	if err := sched.tickOnce(ctx); err != nil {
		t.Fatalf("tickOnce: %v", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner calls = %d, want 0 (task already claimed)", runner.callCount())
	}
}

// TestTickOnceRespectsListLimit 验证：tickOnce 调用 ListDueTasks 时传 limit，
// 避免积压时全量返回（专项：任务准-并发安全 - 补跑风暴保护）。
func TestTickOnceRespectsListLimit(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	runner := &fakeReadRunner{}
	sched, _, taskStore := newTestScheduler(t, runner, now)
	ctx := context.Background()

	// 创建 150 个到期任务（超过默认 limit=100）
	for i := 0; i < 150; i++ {
		task := sampleScheduledTask(now)
		task.NextRunAt = now.Add(-time.Minute)
		if _, err := taskStore.CreateTask(ctx, task); err != nil {
			t.Fatalf("create task %d: %v", i, err)
		}
	}

	if err := sched.tickOnce(ctx); err != nil {
		t.Fatalf("tickOnce: %v", err)
	}

	// 应只执行 limit=100 个
	if got := runner.callCount(); got > 100 {
		t.Fatalf("runner calls = %d, want <= 100 (list limit)", got)
	}
}

// TestExecuteAndRecordSingleInstanceNoConflict 验证：单实例下 AppendRunAndUpdateTask
// 的 CAS 不应冲突，run 正常写入（专项：任务准-并发安全 - 回归保障）。
func TestExecuteAndRecordSingleInstanceNoConflict(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	runner := &fakeReadRunner{}
	sched, _, taskStore := newTestScheduler(t, runner, now)
	ctx := context.Background()

	task := sampleScheduledTask(now)
	task.NextRunAt = now.Add(-time.Minute)
	created, err := taskStore.CreateTask(ctx, task)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := sched.tickOnce(ctx); err != nil {
		t.Fatalf("tickOnce: %v", err)
	}

	// 单实例下不应有并发冲突，run 应正常写入
	runs, err := taskStore.ListRuns(ctx, created.ID, 0)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1 (single instance should not conflict)", len(runs))
	}
}

// TestTriggerAlsoUsesAtomicUpdate 验证：手动 Trigger 走原子事务（AppendRunAndUpdateTask），
// 不做 CAS 认领（不与 scheduler 抢占），但保持数据一致性（专项：任务准-并发安全）。
func TestTriggerAlsoUsesAtomicUpdate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	svc, _, taskStore, runner := newTestService(t, now)
	ctx := context.Background()

	task, err := svc.Create(ctx, adminUser(), CreateRequest{
		Name:           "巡检",
		CapabilityName: "minio.bucket.health.read",
		Input:          map[string]any{"environment": "prod", "name": "archive"},
		ScheduleKind:   "preset",
		Preset:         "5m",
		Timezone:       "Asia/Shanghai",
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	originalNextRun := task.NextRunAt

	run, err := svc.Trigger(ctx, adminUser(), task.ID)
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if runner.callCount() != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.callCount())
	}
	if run.Status != store.ScheduledTaskStatusSucceeded {
		t.Fatalf("run status = %q, want succeeded", run.Status)
	}

	// Trigger 不应推进 next_run_at（不影响调度节奏）
	updated, err := taskStore.GetTask(ctx, task.ID, "admin-1")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if !updated.NextRunAt.Equal(originalNextRun) {
		t.Fatalf("next_run_at = %v, want %v (Trigger should not advance next_run_at)", updated.NextRunAt, originalNextRun)
	}

	// run 应写入
	runs, err := taskStore.ListRuns(ctx, task.ID, 0)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
}
