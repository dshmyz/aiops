package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// fakeReadRunner 实现 execution.ReadRunner，可注入结果或错误，并记录调用次数。
type fakeReadRunner struct {
	mu       sync.Mutex
	calls    int
	result   map[string]any
	err      error
	panicMsg string
}

func (r *fakeReadRunner) Read(_ context.Context, tool tools.Tool, _ map[string]any) (map[string]any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.panicMsg != "" {
		panic(r.panicMsg)
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.result != nil {
		return r.result, nil
	}
	return map[string]any{"status": "ok", "tool": tool.Name}, nil
}

func (r *fakeReadRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// newTestScheduler 构造一个 Scheduler，使用 memory store + memory audit + fake runner。
func newTestScheduler(t *testing.T, runner *fakeReadRunner, now time.Time) (*Scheduler, *store.MemoryActionPlanStore, *store.MemoryScheduledTaskStore) {
	t.Helper()
	if runner == nil {
		runner = &fakeReadRunner{}
	}
	repository := store.NewMemoryActionPlanStore()
	auditService := audit.NewService(repository)
	taskStore := store.NewMemoryScheduledTaskStore()
	readService := execution.NewReadOnlyService(runner, auditService)
	sched := New(taskStore, readService, auditService, time.Minute, func() time.Time { return now })
	return sched, repository, taskStore
}

func TestSchedulerTickOnceExecutesDueTask(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	runner := &fakeReadRunner{}
	sched, repository, taskStore := newTestScheduler(t, runner, now)
	ctx := context.Background()

	task := sampleScheduledTask(now)
	task.NextRunAt = now.Add(-time.Minute) // 已到期
	created, err := taskStore.CreateTask(ctx, task)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := sched.tickOnce(ctx); err != nil {
		t.Fatalf("tickOnce: %v", err)
	}

	if runner.callCount() != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.callCount())
	}

	// 验证 task 已更新
	updated, err := taskStore.GetTask(ctx, created.ID, "alice")
	if err != nil {
		t.Fatalf("get updated task: %v", err)
	}
	if updated.LastRunAt == nil || !updated.LastRunAt.Equal(now) {
		t.Fatalf("last_run_at = %v, want %v", updated.LastRunAt, now)
	}
	if updated.LastStatus != store.ScheduledTaskStatusSucceeded {
		t.Fatalf("last_status = %q, want succeeded", updated.LastStatus)
	}
	if !updated.NextRunAt.After(now) {
		t.Fatalf("next_run_at = %v, want after now", updated.NextRunAt)
	}

	// 验证 run 已写入
	runs, err := taskStore.ListRuns(ctx, created.ID, 0)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	if runs[0].Status != store.ScheduledTaskStatusSucceeded {
		t.Fatalf("run status = %q, want succeeded", runs[0].Status)
	}
	if runs[0].AuditEventID == "" {
		t.Fatal("run audit_event_id must be set")
	}

	// 验证 audit event 已写入（succeeded）
	events := repository.AuditEvents()
	foundSucceeded := false
	for _, evt := range events {
		if evt.Action == audit.ActionScheduledTaskSucceeded {
			foundSucceeded = true
			if evt.Subject != "system:scheduler" {
				t.Fatalf("audit subject = %q, want system:scheduler", evt.Subject)
			}
		}
	}
	if !foundSucceeded {
		t.Fatalf("audit events = %+v, want ActionScheduledTaskSucceeded", events)
	}
}

func TestSchedulerTickOnceFailedRunStillUpdatesNextRunAt(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	runner := &fakeReadRunner{err: errors.New("capability unavailable")}
	sched, repository, taskStore := newTestScheduler(t, runner, now)
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

	// task 仍应更新 next_run_at（避免卡死）
	updated, err := taskStore.GetTask(ctx, created.ID, "alice")
	if err != nil {
		t.Fatalf("get updated task: %v", err)
	}
	if updated.LastStatus != store.ScheduledTaskStatusFailed {
		t.Fatalf("last_status = %q, want failed", updated.LastStatus)
	}
	if !updated.NextRunAt.After(now) {
		t.Fatalf("next_run_at = %v, want after now (avoid stuck)", updated.NextRunAt)
	}

	// run 应记为 failed
	runs, err := taskStore.ListRuns(ctx, created.ID, 0)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != store.ScheduledTaskStatusFailed {
		t.Fatalf("runs = %+v, want 1 failed run", runs)
	}
	if runs[0].Error == "" {
		t.Fatal("run error must be set on failure")
	}

	// audit 应记录 failed
	events := repository.AuditEvents()
	foundFailed := false
	for _, evt := range events {
		if evt.Action == audit.ActionScheduledTaskFailed {
			foundFailed = true
		}
	}
	if !foundFailed {
		t.Fatalf("audit events = %+v, want ActionScheduledTaskFailed", events)
	}
}

func TestSchedulerTickOncePanicRecoveredAsFailed(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	runner := &fakeReadRunner{panicMsg: "boom"}
	sched, _, taskStore := newTestScheduler(t, runner, now)
	ctx := context.Background()

	task := sampleScheduledTask(now)
	task.NextRunAt = now.Add(-time.Minute)
	created, err := taskStore.CreateTask(ctx, task)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := sched.tickOnce(ctx); err != nil {
		t.Fatalf("tickOnce should not surface error from panic: %v", err)
	}

	updated, err := taskStore.GetTask(ctx, created.ID, "alice")
	if err != nil {
		t.Fatalf("get updated task: %v", err)
	}
	if updated.LastStatus != store.ScheduledTaskStatusFailed {
		t.Fatalf("last_status = %q, want failed (panic recovered)", updated.LastStatus)
	}
	if !updated.NextRunAt.After(now) {
		t.Fatalf("next_run_at = %v, want after now (avoid stuck after panic)", updated.NextRunAt)
	}

	runs, err := taskStore.ListRuns(ctx, created.ID, 0)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != store.ScheduledTaskStatusFailed {
		t.Fatalf("runs = %+v, want 1 failed run after panic", runs)
	}
}

// TestSchedulerTickOnceRunDurationNonZero verifies that StartedAt and
// FinishedAt are captured separately so duration is non-zero (收口2).
// Previously both referenced the same `now` variable, making duration always 0.
func TestSchedulerTickOnceRunDurationNonZero(t *testing.T) {
	t.Parallel()
	// 用可推进的时钟：每次调用推进 1ms，模拟真实时间流逝。
	// 这样 startedAt（执行前）和 finishedAt（执行后）会有不同的值。
	start := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	nowMu := &sync.Mutex{}
	current := start
	nowFn := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		current = current.Add(time.Millisecond)
		return current
	}
	runner := &fakeReadRunner{}
	repository := store.NewMemoryActionPlanStore()
	auditService := audit.NewService(repository)
	taskStore := store.NewMemoryScheduledTaskStore()
	readService := execution.NewReadOnlyService(runner, auditService)
	sched := New(taskStore, readService, auditService, time.Minute, nowFn)
	ctx := context.Background()

	task := sampleScheduledTask(start)
	task.NextRunAt = start.Add(-time.Minute)
	created, err := taskStore.CreateTask(ctx, task)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := sched.tickOnce(ctx); err != nil {
		t.Fatalf("tickOnce: %v", err)
	}

	runs, err := taskStore.ListRuns(ctx, created.ID, 0)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	run := runs[0]
	if !run.FinishedAt.After(run.StartedAt) {
		t.Fatalf("FinishedAt (%v) must be after StartedAt (%v), duration is 0 (bug 收口2)", run.FinishedAt, run.StartedAt)
	}
}

func TestSchedulerTickOnceSkipsNotDueTask(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	runner := &fakeReadRunner{}
	sched, _, taskStore := newTestScheduler(t, runner, now)
	ctx := context.Background()

	// 未到期任务
	task := sampleScheduledTask(now)
	task.NextRunAt = now.Add(10 * time.Minute)
	if _, err := taskStore.CreateTask(ctx, task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := sched.tickOnce(ctx); err != nil {
		t.Fatalf("tickOnce: %v", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner calls = %d, want 0 (no due tasks)", runner.callCount())
	}
}

func TestSchedulerTickOnceSkipsDisabledTask(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	runner := &fakeReadRunner{}
	sched, _, taskStore := newTestScheduler(t, runner, now)
	ctx := context.Background()

	// 已禁用的到期任务
	task := sampleScheduledTask(now)
	task.Enabled = false
	task.NextRunAt = now.Add(-time.Minute)
	if _, err := taskStore.CreateTask(ctx, task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := sched.tickOnce(ctx); err != nil {
		t.Fatalf("tickOnce: %v", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner calls = %d, want 0 (disabled task)", runner.callCount())
	}
}

func TestSchedulerTickOnceSerializesMultipleDueTasks(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	runner := &fakeReadRunner{}
	sched, _, taskStore := newTestScheduler(t, runner, now)
	ctx := context.Background()

	// 三个到期任务，next_run_at 升序
	for i := 0; i < 3; i++ {
		task := sampleScheduledTask(now)
		task.Name = "due-" + string(rune('a'+i))
		task.NextRunAt = now.Add(-time.Duration(3-i) * time.Minute)
		if _, err := taskStore.CreateTask(ctx, task); err != nil {
			t.Fatalf("create task %d: %v", i, err)
		}
	}

	if err := sched.tickOnce(ctx); err != nil {
		t.Fatalf("tickOnce: %v", err)
	}
	if runner.callCount() != 3 {
		t.Fatalf("runner calls = %d, want 3", runner.callCount())
	}
}

func TestSchedulerSystemUserHasSchedulerRole(t *testing.T) {
	// 验证 systemUser 是预期的标识
	user := systemUser()
	if user.Subject != "system:scheduler" {
		t.Fatalf("subject = %q, want system:scheduler", user.Subject)
	}
	if len(user.Roles) != 1 || user.Roles[0] != "scheduler" {
		t.Fatalf("roles = %+v, want [scheduler]", user.Roles)
	}
}

// sampleScheduledTask 与 store 包中的同名函数保持字段一致（这里独立维护以避免循环依赖）。
func sampleScheduledTask(now time.Time) store.ScheduledTask {
	return store.ScheduledTask{
		Name:           "minio 容量巡检",
		Subject:        "alice",
		CapabilityName: "minio.bucket.capacity.read",
		Input:          map[string]any{"bucket": "archive"},
		ScheduleKind:   "preset",
		Preset:         "5m",
		Timezone:       "Asia/Shanghai",
		Enabled:        true,
		NextRunAt:      now.Add(5 * time.Minute),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}
