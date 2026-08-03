package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestClaimTaskSucceedsWhenNextRunAtMatches 验证 CAS 认领：expectedNextRunAt
// 匹配当前 next_run_at 时认领成功，next_run_at 被推进到 newNextRunAt，
// last_run_at 和 last_status 被更新，返回 true（专项：任务准-并发安全）。
func TestClaimTaskSucceedsWhenNextRunAtMatches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestScheduledTaskStore()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	task := sampleScheduledTask(now)
	task.NextRunAt = now // 已到期
	created, err := store.CreateTask(ctx, task)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	newNextRun := now.Add(5 * time.Minute)
	claimed, err := store.ClaimTask(ctx, created.ID, created.NextRunAt, newNextRun, now)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if !claimed {
		t.Fatal("claimed = false, want true (CAS should succeed when next_run_at matches)")
	}

	// 验证 task 状态被推进
	updated, err := store.GetTask(ctx, created.ID, created.Subject)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !updated.NextRunAt.Equal(newNextRun) {
		t.Fatalf("NextRunAt = %v, want %v (advanced by claim)", updated.NextRunAt, newNextRun)
	}
	if updated.LastStatus != "running" {
		t.Fatalf("LastStatus = %q, want running", updated.LastStatus)
	}
	if updated.LastRunAt == nil || !updated.LastRunAt.Equal(now) {
		t.Fatalf("LastRunAt = %v, want %v", updated.LastRunAt, now)
	}
}

// TestClaimTaskFailsWhenNextRunAtAlreadyAdvanced 验证 CAS 认领：expectedNextRunAt
// 不匹配（已被其他实例认领，next_run_at 已推进）时返回 false，不修改任何字段。
func TestClaimTaskFailsWhenNextRunAtAlreadyAdvanced(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestScheduledTaskStore()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	task := sampleScheduledTask(now)
	task.NextRunAt = now
	created, err := store.CreateTask(ctx, task)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// 模拟其他实例已认领：传入过期的 expectedNextRunAt
	staleExpected := created.NextRunAt.Add(-time.Minute)
	newNextRun := now.Add(5 * time.Minute)
	claimed, err := store.ClaimTask(ctx, created.ID, staleExpected, newNextRun, now)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if claimed {
		t.Fatal("claimed = true, want false (CAS should fail when next_run_at already advanced)")
	}

	// 验证 task 状态未被修改
	unchanged, err := store.GetTask(ctx, created.ID, created.Subject)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !unchanged.NextRunAt.Equal(created.NextRunAt) {
		t.Fatalf("NextRunAt = %v, want %v (should not change on failed claim)", unchanged.NextRunAt, created.NextRunAt)
	}
}

// TestAppendRunAndUpdateTaskAtomicSuccess 验证原子事务：run 和 task 都成功更新。
// task 的 updated_at 被推进（乐观锁版本号更新），run 被写入。
func TestAppendRunAndUpdateTaskAtomicSuccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestScheduledTaskStore()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	task := sampleScheduledTask(now)
	created, err := store.CreateTask(ctx, task)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	run := ScheduledTaskRun{
		TaskID:     created.ID,
		StartedAt:  now,
		FinishedAt: now.Add(time.Second),
		Status:     ScheduledTaskStatusSucceeded,
		ResultSummary: "ok",
	}
	updatedTask := created
	updatedTask.LastRunAt = &run.StartedAt
	updatedTask.LastStatus = ScheduledTaskStatusSucceeded
	updatedTask.UpdatedAt = now.Add(time.Second)

	if err := store.AppendRunAndUpdateTask(ctx, run, updatedTask, created.UpdatedAt); err != nil {
		t.Fatalf("AppendRunAndUpdateTask: %v", err)
	}

	// 验证 run 被写入
	runs, err := store.ListRuns(ctx, created.ID, 0)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	// 验证 task 被更新
	updated, err := store.GetTask(ctx, created.ID, created.Subject)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if updated.LastStatus != ScheduledTaskStatusSucceeded {
		t.Fatalf("LastStatus = %q, want %q", updated.LastStatus, ScheduledTaskStatusSucceeded)
	}
}

// TestAppendRunAndUpdateTaskRollbackOnTaskConflict 验证原子事务：task CAS 失败
// （expectedUpdatedAt 不匹配）时 run 也回滚，返回 ErrConcurrentModification。
func TestAppendRunAndUpdateTaskRollbackOnTaskConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestScheduledTaskStore()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	task := sampleScheduledTask(now)
	created, err := store.CreateTask(ctx, task)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	run := ScheduledTaskRun{
		TaskID:     created.ID,
		StartedAt:  now,
		FinishedAt: now.Add(time.Second),
		Status:     ScheduledTaskStatusSucceeded,
	}
	updatedTask := created
	updatedTask.LastStatus = ScheduledTaskStatusSucceeded
	updatedTask.UpdatedAt = now.Add(time.Second)
	// 传入过期的 expectedUpdatedAt，模拟其他实例已修改 task
	staleExpected := created.UpdatedAt.Add(-time.Minute)
	err = store.AppendRunAndUpdateTask(ctx, run, updatedTask, staleExpected)
	if !errors.Is(err, ErrConcurrentModification) {
		t.Fatalf("err = %v, want ErrConcurrentModification", err)
	}

	// 验证 run 未被写入（回滚）
	runs, err := store.ListRuns(ctx, created.ID, 0)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs = %d, want 0 (rollback should prevent run insert)", len(runs))
	}
}

// TestMemoryStoreClaimTaskConcurrent 验证内存版并发模拟：两个 goroutine 同时
// Claim 同一 task，只有一个返回 true，另一个返回 false。
func TestMemoryStoreClaimTaskConcurrent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestScheduledTaskStore()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	task := sampleScheduledTask(now)
	task.NextRunAt = now
	created, err := store.CreateTask(ctx, task)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	var wg sync.WaitGroup
	results := make(chan bool, 2)
	newNextRun := now.Add(5 * time.Minute)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, _ := store.ClaimTask(ctx, created.ID, created.NextRunAt, newNextRun, now)
			results <- claimed
		}()
	}
	wg.Wait()
	close(results)

	successCount := 0
	for claimed := range results {
		if claimed {
			successCount++
		}
	}
	if successCount != 1 {
		t.Fatalf("successCount = %d, want 1 (only one goroutine should claim)", successCount)
	}
}
