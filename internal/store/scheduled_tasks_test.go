package store

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"
)

// newTestScheduledTaskStore returns a fresh memory store for deterministic tests.
func newTestScheduledTaskStore() *MemoryScheduledTaskStore {
	return NewMemoryScheduledTaskStore()
}

func sampleScheduledTask(now time.Time) ScheduledTask {
	return ScheduledTask{
		ID:             "",
		Name:           "minio 容量巡检",
		Subject:        "alice",
		CapabilityName: "minio.bucket.capacity.read",
		Input:          map[string]any{"environment": "prod", "bucket": "archive"},
		ScheduleKind:   "preset",
		Preset:         "5m",
		Timezone:       "Asia/Shanghai",
		Enabled:        true,
		NextRunAt:      now.Add(5 * time.Minute),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func TestMemoryScheduledTaskStoreCreateAndGet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestScheduledTaskStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	task := sampleScheduledTask(now)
	created, err := store.CreateTask(ctx, task)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if created.ID == "" {
		t.Fatal("task ID must be populated")
	}
	if created.Name != task.Name || created.Subject != task.Subject {
		t.Fatalf("created task = %+v, want original fields", created)
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("created_at must be set")
	}

	fetched, err := store.GetTask(ctx, created.ID, "alice")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if fetched.ID != created.ID {
		t.Fatalf("fetched ID = %s, want %s", fetched.ID, created.ID)
	}
	if fetched.Input["bucket"] != "archive" {
		t.Fatalf("fetched input = %+v, want bucket=archive", fetched.Input)
	}
}

func TestMemoryScheduledTaskStoreGetTaskMissingReturnsNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestScheduledTaskStore()

	if _, err := store.GetTask(ctx, "nonexistent", "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get missing task = %v, want ErrNotFound", err)
	}
}

func TestMemoryScheduledTaskStoreGetTaskRejectsForeignSubject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestScheduledTaskStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	created, err := store.CreateTask(ctx, sampleScheduledTask(now))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := store.GetTask(ctx, created.ID, "bob"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get alice task as bob = %v, want ErrNotFound", err)
	}
}

func TestMemoryScheduledTaskStoreListFiltersBySubject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestScheduledTaskStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	aliceTask := sampleScheduledTask(now)
	aliceTask.Name = "alice task"
	if _, err := store.CreateTask(ctx, aliceTask); err != nil {
		t.Fatalf("create alice task: %v", err)
	}

	bobTask := sampleScheduledTask(now)
	bobTask.Subject = "bob"
	bobTask.Name = "bob task"
	if _, err := store.CreateTask(ctx, bobTask); err != nil {
		t.Fatalf("create bob task: %v", err)
	}

	tasks, err := store.ListTasks(ctx, ScheduledTaskFilter{Subject: "alice"})
	if err != nil {
		t.Fatalf("list alice tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Subject != "alice" {
		t.Fatalf("alice tasks = %+v, want 1 alice task", tasks)
	}
}

func TestMemoryScheduledTaskStoreListFiltersByEnabled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestScheduledTaskStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	enabledTask := sampleScheduledTask(now)
	enabledTask.Name = "enabled"
	if _, err := store.CreateTask(ctx, enabledTask); err != nil {
		t.Fatalf("create enabled task: %v", err)
	}

	disabledTask := sampleScheduledTask(now)
	disabledTask.Name = "disabled"
	disabledTask.Enabled = false
	if _, err := store.CreateTask(ctx, disabledTask); err != nil {
		t.Fatalf("create disabled task: %v", err)
	}

	enabled := true
	tasks, err := store.ListTasks(ctx, ScheduledTaskFilter{Subject: "alice", Enabled: &enabled})
	if err != nil {
		t.Fatalf("list enabled tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Name != "enabled" {
		t.Fatalf("enabled tasks = %+v, want 1 enabled task", tasks)
	}

	disabled := false
	tasks, err = store.ListTasks(ctx, ScheduledTaskFilter{Subject: "alice", Enabled: &disabled})
	if err != nil {
		t.Fatalf("list disabled tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Name != "disabled" {
		t.Fatalf("disabled tasks = %+v, want 1 disabled task", tasks)
	}
}

func TestMemoryScheduledTaskStoreListAppliesLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestScheduledTaskStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		task := sampleScheduledTask(now)
		task.Name = "task-" + string(rune('a'+i))
		if _, err := store.CreateTask(ctx, task); err != nil {
			t.Fatalf("create task %d: %v", i, err)
		}
	}

	tasks, err := store.ListTasks(ctx, ScheduledTaskFilter{Subject: "alice", Limit: 2})
	if err != nil {
		t.Fatalf("list with limit: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("tasks = %d, want 2", len(tasks))
	}
}

func TestMemoryScheduledTaskStoreUpdateTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestScheduledTaskStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	created, err := store.CreateTask(ctx, sampleScheduledTask(now))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	later := now.Add(time.Hour)
	created.Name = "renamed"
	created.Enabled = false
	created.UpdatedAt = later
	updated, err := store.UpdateTask(ctx, created)
	if err != nil {
		t.Fatalf("update task: %v", err)
	}
	if updated.Name != "renamed" || !updated.UpdatedAt.Equal(later) {
		t.Fatalf("updated task = %+v, want renamed and later updated_at", updated)
	}

	fetched, err := store.GetTask(ctx, created.ID, "alice")
	if err != nil {
		t.Fatalf("get updated task: %v", err)
	}
	if fetched.Name != "renamed" || fetched.Enabled {
		t.Fatalf("fetched task = %+v, want renamed and disabled", fetched)
	}
}

func TestMemoryScheduledTaskStoreUpdateTaskRejectsForeignSubject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestScheduledTaskStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	created, err := store.CreateTask(ctx, sampleScheduledTask(now))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	created.Subject = "bob"
	if _, err := store.UpdateTask(ctx, created); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update alice task as bob = %v, want ErrNotFound", err)
	}
}

func TestMemoryScheduledTaskStoreDeleteTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestScheduledTaskStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	created, err := store.CreateTask(ctx, sampleScheduledTask(now))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := store.DeleteTask(ctx, created.ID, "alice"); err != nil {
		t.Fatalf("delete task: %v", err)
	}

	if _, err := store.GetTask(ctx, created.ID, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted task = %v, want ErrNotFound", err)
	}
}

func TestMemoryScheduledTaskStoreDeleteTaskRejectsForeignSubject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestScheduledTaskStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	created, err := store.CreateTask(ctx, sampleScheduledTask(now))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := store.DeleteTask(ctx, created.ID, "bob"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete alice task as bob = %v, want ErrNotFound", err)
	}

	// 任务仍存在
	if _, err := store.GetTask(ctx, created.ID, "alice"); err != nil {
		t.Fatalf("get task after foreign delete = %v, want no error", err)
	}
}

func TestMemoryScheduledTaskStoreListDueTasks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestScheduledTaskStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	// 三个到期任务，next_run_at 升序
	due1 := sampleScheduledTask(now)
	due1.Name = "due-1"
	due1.NextRunAt = now.Add(-3 * time.Minute)
	if _, err := store.CreateTask(ctx, due1); err != nil {
		t.Fatalf("create due1: %v", err)
	}
	due2 := sampleScheduledTask(now)
	due2.Name = "due-2"
	due2.NextRunAt = now.Add(-1 * time.Minute)
	if _, err := store.CreateTask(ctx, due2); err != nil {
		t.Fatalf("create due2: %v", err)
	}

	// 未到期任务
	future := sampleScheduledTask(now)
	future.Name = "future"
	future.NextRunAt = now.Add(10 * time.Minute)
	if _, err := store.CreateTask(ctx, future); err != nil {
		t.Fatalf("create future: %v", err)
	}

	// 已禁用的到期任务
	disabled := sampleScheduledTask(now)
	disabled.Name = "disabled"
	disabled.Enabled = false
	disabled.NextRunAt = now.Add(-5 * time.Minute)
	if _, err := store.CreateTask(ctx, disabled); err != nil {
		t.Fatalf("create disabled: %v", err)
	}

	due, err := store.ListDueTasks(ctx, now, 50)
	if err != nil {
		t.Fatalf("list due tasks: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("due tasks = %d, want 2 (disabled and future excluded)", len(due))
	}
	// 按 next_run_at 升序
	if due[0].Name != "due-1" || due[1].Name != "due-2" {
		t.Fatalf("due order = %s %s, want due-1 due-2 (ascending)", due[0].Name, due[1].Name)
	}
}

func TestMemoryScheduledTaskStoreListDueTasksAppliesLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestScheduledTaskStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		task := sampleScheduledTask(now)
		task.Name = "due-" + string(rune('a'+i))
		task.NextRunAt = now.Add(-time.Duration(3-i) * time.Minute)
		if _, err := store.CreateTask(ctx, task); err != nil {
			t.Fatalf("create due task %d: %v", i, err)
		}
	}

	due, err := store.ListDueTasks(ctx, now, 2)
	if err != nil {
		t.Fatalf("list due tasks with limit: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("due tasks = %d, want 2", len(due))
	}
	// 升序：第一个应该是最早到期的
	if due[0].Name != "due-a" {
		t.Fatalf("first due = %s, want due-a", due[0].Name)
	}
}

func TestMemoryScheduledTaskStoreAppendRunAndListRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestScheduledTaskStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	created, err := store.CreateTask(ctx, sampleScheduledTask(now))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	// 追加 3 个 run，started_at 升序
	for i := 0; i < 3; i++ {
		run, err := store.AppendRun(ctx, ScheduledTaskRun{
			TaskID:        created.ID,
			StartedAt:     now.Add(time.Duration(i) * time.Minute),
			FinishedAt:    now.Add(time.Duration(i)*time.Minute + 30*time.Second),
			Status:        "succeeded",
			ResultSummary: "ok",
			ResultData:    map[string]any{"i": i},
		})
		if err != nil {
			t.Fatalf("append run %d: %v", i, err)
		}
		if run.ID == "" {
			t.Fatal("run ID must be populated")
		}
	}

	runs, err := store.ListRuns(ctx, created.ID, 0)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("runs = %d, want 3", len(runs))
	}
	// 按 started_at 降序（最新在前）
	if !runs[0].StartedAt.After(runs[1].StartedAt) {
		t.Fatalf("runs not in descending order: %v before %v", runs[0].StartedAt, runs[1].StartedAt)
	}
	if runs[0].ResultData["i"] != 2 {
		t.Fatalf("first run result_data = %+v, want i=2", runs[0].ResultData)
	}
}

func TestMemoryScheduledTaskStoreListRunsAppliesLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestScheduledTaskStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	created, err := store.CreateTask(ctx, sampleScheduledTask(now))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	for i := 0; i < 5; i++ {
		if _, err := store.AppendRun(ctx, ScheduledTaskRun{
			TaskID:     created.ID,
			StartedAt:  now.Add(time.Duration(i) * time.Minute),
			FinishedAt: now.Add(time.Duration(i)*time.Minute + time.Second),
			Status:     "succeeded",
		}); err != nil {
			t.Fatalf("append run %d: %v", i, err)
		}
	}

	runs, err := store.ListRuns(ctx, created.ID, 2)
	if err != nil {
		t.Fatalf("list runs with limit: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(runs))
	}
	// 最新两个：i=4, i=3
	if runs[0].StartedAt.Equal(now.Add(4*time.Minute)) == false {
		t.Fatalf("first run started_at = %v, want %v", runs[0].StartedAt, now.Add(4*time.Minute))
	}
}

func TestMemoryScheduledTaskStoreCountRecentFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestScheduledTaskStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	created, err := store.CreateTask(ctx, sampleScheduledTask(now))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	// 失败的 run：在 24h 内
	if _, err := store.AppendRun(ctx, ScheduledTaskRun{
		TaskID:     created.ID,
		StartedAt:  now.Add(-2 * time.Hour),
		FinishedAt: now.Add(-2 * time.Hour).Add(time.Minute),
		Status:     "failed",
		Error:      "boom",
	}); err != nil {
		t.Fatalf("append failed run: %v", err)
	}
	// 失败的 run：在 24h 外
	if _, err := store.AppendRun(ctx, ScheduledTaskRun{
		TaskID:     created.ID,
		StartedAt:  now.Add(-48 * time.Hour),
		FinishedAt: now.Add(-48 * time.Hour).Add(time.Minute),
		Status:     "failed",
	}); err != nil {
		t.Fatalf("append old failed run: %v", err)
	}
	// 成功的 run：在 24h 内
	if _, err := store.AppendRun(ctx, ScheduledTaskRun{
		TaskID:     created.ID,
		StartedAt:  now.Add(-1 * time.Hour),
		FinishedAt: now.Add(-1 * time.Hour).Add(time.Minute),
		Status:     "succeeded",
	}); err != nil {
		t.Fatalf("append succeeded run: %v", err)
	}

	since := now.Add(-24 * time.Hour)
	count, err := store.CountRecentFailures(ctx, since)
	if err != nil {
		t.Fatalf("count recent failures: %v", err)
	}
	if count != 1 {
		t.Fatalf("recent failures = %d, want 1", count)
	}
}

// TestScheduledTaskStoreInterfaceConformance 验证两种实现都满足接口
func TestScheduledTaskStoreInterfaceConformance(t *testing.T) {
	t.Parallel()
	var _ ScheduledTaskStore = (*MemoryScheduledTaskStore)(nil)
}

// 验证排序的辅助：用于跨实现一致性测试
func assertScheduledTasksAscendingByNextRunAt(t *testing.T, tasks []ScheduledTask) {
	t.Helper()
	sorted := make([]ScheduledTask, len(tasks))
	copy(sorted, tasks)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].NextRunAt.Before(sorted[j].NextRunAt)
	})
	for i, task := range tasks {
		if !task.NextRunAt.Equal(sorted[i].NextRunAt) {
			t.Fatalf("tasks not in ascending next_run_at order: got %+v", tasks)
		}
	}
}
