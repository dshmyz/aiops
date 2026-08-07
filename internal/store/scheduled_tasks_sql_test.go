package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestSQLScheduledTaskStoreLifecycle 覆盖 SQL 实现的 CRUD 与 run 写入。
func TestSQLScheduledTaskStoreLifecycle(t *testing.T) {
	t.Parallel()
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	ctx := context.Background()
	store := NewSQLScheduledTaskStore(db)
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	task := sampleScheduledTask(now)
	created, err := store.CreateTask(ctx, task)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if created.ID == "" {
		t.Fatal("task ID must be populated")
	}

	// GetTask
	fetched, err := store.GetTask(ctx, created.ID, "alice")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if fetched.Name != task.Name {
		t.Fatalf("fetched name = %q, want %q", fetched.Name, task.Name)
	}
	if fetched.Input["bucket"] != "archive" {
		t.Fatalf("fetched input = %+v, want bucket=archive", fetched.Input)
	}
	if !fetched.Enabled {
		t.Fatal("fetched enabled = false, want true")
	}

	// GetTask subject 不匹配
	if _, err := store.GetTask(ctx, created.ID, "bob"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get alice task as bob = %v, want ErrNotFound", err)
	}

	// UpdateTask
	later := now.Add(time.Hour)
	created.Name = "renamed"
	created.Enabled = false
	created.LastRunAt = &now
	created.LastStatus = ScheduledTaskStatusSucceeded
	created.UpdatedAt = later
	updated, err := store.UpdateTask(ctx, created)
	if err != nil {
		t.Fatalf("update task: %v", err)
	}
	if updated.Name != "renamed" || updated.Enabled {
		t.Fatalf("updated task = %+v, want renamed and disabled", updated)
	}
	if updated.LastRunAt == nil || !updated.LastRunAt.Equal(now) {
		t.Fatalf("updated last_run_at = %v, want %v", updated.LastRunAt, now)
	}

	// ListTasks subject 过滤
	tasks, err := store.ListTasks(ctx, ScheduledTaskFilter{Subject: "alice"})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("alice tasks = %d, want 1", len(tasks))
	}

	// ListTasks enabled 过滤
	enabled := false
	tasks, err = store.ListTasks(ctx, ScheduledTaskFilter{Subject: "alice", Enabled: &enabled})
	if err != nil {
		t.Fatalf("list disabled tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Name != "renamed" {
		t.Fatalf("disabled tasks = %+v, want 1 renamed", tasks)
	}

	// ListDueTasks - 重新启用并设为到期
	created.Enabled = true
	created.NextRunAt = now.Add(-time.Minute)
	if _, err := store.UpdateTask(ctx, created); err != nil {
		t.Fatalf("update task for due: %v", err)
	}
	due, err := store.ListDueTasks(ctx, now, 50)
	if err != nil {
		t.Fatalf("list due tasks: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("due tasks = %d, want 1", len(due))
	}

	// AppendRun + ListRuns
	run, err := store.AppendRun(ctx, ScheduledTaskRun{
		TaskID:        created.ID,
		StartedAt:     now,
		FinishedAt:    now.Add(time.Second),
		Status:        ScheduledTaskStatusSucceeded,
		ResultSummary: "ok",
		ResultData:    map[string]any{"key": "value"},
	})
	if err != nil {
		t.Fatalf("append run: %v", err)
	}
	if run.ID == "" {
		t.Fatal("run ID must be populated")
	}

	runs, err := store.ListRuns(ctx, created.ID, 0)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != ScheduledTaskStatusSucceeded {
		t.Fatalf("runs = %+v, want 1 succeeded run", runs)
	}
	if runs[0].ResultData["key"] != "value" {
		t.Fatalf("run result_data = %+v, want key=value", runs[0].ResultData)
	}

	// CountRecentFailures
	failedRun, err := store.AppendRun(ctx, ScheduledTaskRun{
		TaskID:     created.ID,
		StartedAt:  now.Add(time.Minute),
		FinishedAt: now.Add(time.Minute).Add(time.Second),
		Status:     ScheduledTaskStatusFailed,
		Error:      "boom",
	})
	if err != nil {
		t.Fatalf("append failed run: %v", err)
	}
	if failedRun.ID == "" {
		t.Fatal("failed run ID must be populated")
	}
	count, err := store.CountRecentFailures(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("count recent failures: %v", err)
	}
	if count != 1 {
		t.Fatalf("recent failures = %d, want 1", count)
	}

	// DeleteTask
	if err := store.DeleteTask(ctx, created.ID, "alice"); err != nil {
		t.Fatalf("delete task: %v", err)
	}
	if _, err := store.GetTask(ctx, created.ID, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted task = %v, want ErrNotFound", err)
	}
}

// TestSQLScheduledTaskStoreListDueTasksAscending 验证 SQL 实现按 next_run_at 升序返回。
func TestSQLScheduledTaskStoreListDueTasksAscending(t *testing.T) {
	t.Parallel()
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	ctx := context.Background()
	store := NewSQLScheduledTaskStore(db)
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	// 三个到期任务，next_run_at 不同
	for i := 0; i < 3; i++ {
		task := sampleScheduledTask(now)
		task.Name = "due-" + string(rune('a'+i))
		task.NextRunAt = now.Add(-time.Duration(3-i) * time.Minute)
		if _, err := store.CreateTask(ctx, task); err != nil {
			t.Fatalf("create due task %d: %v", i, err)
		}
	}

	// 未到期任务
	future := sampleScheduledTask(now)
	future.Name = "future"
	future.NextRunAt = now.Add(10 * time.Minute)
	if _, err := store.CreateTask(ctx, future); err != nil {
		t.Fatalf("create future task: %v", err)
	}

	// 已禁用的到期任务
	disabled := sampleScheduledTask(now)
	disabled.Name = "disabled"
	disabled.Enabled = false
	disabled.NextRunAt = now.Add(-5 * time.Minute)
	if _, err := store.CreateTask(ctx, disabled); err != nil {
		t.Fatalf("create disabled task: %v", err)
	}

	due, err := store.ListDueTasks(ctx, now, 50)
	if err != nil {
		t.Fatalf("list due tasks: %v", err)
	}
	if len(due) != 3 {
		t.Fatalf("due tasks = %d, want 3 (future and disabled excluded)", len(due))
	}
	assertScheduledTasksAscendingByNextRunAt(t, due)
	if due[0].Name != "due-a" {
		t.Fatalf("first due = %s, want due-a", due[0].Name)
	}
}

// TestSQLScheduledTaskStoreListRunsDescending 验证 SQL 实现按 started_at 降序返回。
func TestSQLScheduledTaskStoreListRunsDescending(t *testing.T) {
	t.Parallel()
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	ctx := context.Background()
	store := NewSQLScheduledTaskStore(db)
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	created, err := store.CreateTask(ctx, sampleScheduledTask(now))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := store.AppendRun(ctx, ScheduledTaskRun{
			TaskID:     created.ID,
			StartedAt:  now.Add(time.Duration(i) * time.Minute),
			FinishedAt: now.Add(time.Duration(i)*time.Minute + time.Second),
			Status:     ScheduledTaskStatusSucceeded,
		}); err != nil {
			t.Fatalf("append run %d: %v", i, err)
		}
	}

	runs, err := store.ListRuns(ctx, created.ID, 0)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("runs = %d, want 3", len(runs))
	}
	for i := 1; i < len(runs); i++ {
		if !runs[i-1].StartedAt.After(runs[i].StartedAt) {
			t.Fatalf("runs not in descending order: %v before %v", runs[i-1].StartedAt, runs[i].StartedAt)
		}
	}
}

// TestSQLScheduledTaskStoreDeleteTaskRejectsForeignSubject 验证 SQL 实现的 subject 隔离。
func TestSQLScheduledTaskStoreDeleteTaskRejectsForeignSubject(t *testing.T) {
	t.Parallel()
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	ctx := context.Background()
	store := NewSQLScheduledTaskStore(db)
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

// TestSQLScheduledTaskStoreUpdateTaskRejectsForeignSubject 验证 UpdateTask 的 subject 隔离。
func TestSQLScheduledTaskStoreUpdateTaskRejectsForeignSubject(t *testing.T) {
	t.Parallel()
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	ctx := context.Background()
	store := NewSQLScheduledTaskStore(db)
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

// TestSQLScheduledTaskStoreInterfaceConformance 验证 SQL 实现满足接口
func TestSQLScheduledTaskStoreInterfaceConformance(t *testing.T) {
	t.Parallel()
	var _ ScheduledTaskStore = (*SQLScheduledTaskStore)(nil)
}

// TestSQLScheduledTaskStoreRunKindRoundTrip 验证 run_kind/runbook_slug（E2 Phase 3）
// 在 SQL 中正确往返：read 默认 'read'，runbook 任务保留 slug。
func TestSQLScheduledTaskStoreRunKindRoundTrip(t *testing.T) {
	t.Parallel()
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	ctx := context.Background()
	store := NewSQLScheduledTaskStore(db)
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	// 1) read 任务（scheduler.Create 经 normalizeRunKind 归一会写显式 'read'）往返
	readTask := sampleScheduledTask(now)
	readTask.RunKind = RunKindRead
	readCreated, err := store.CreateTask(ctx, readTask)
	if err != nil {
		t.Fatalf("create read task: %v", err)
	}
	if readCreated.RunKind != RunKindRead {
		t.Fatalf("stored run_kind = %q, want %q", readCreated.RunKind, RunKindRead)
	}
	fetched, err := store.GetTask(ctx, readCreated.ID, "alice")
	if err != nil {
		t.Fatalf("get read task: %v", err)
	}
	if fetched.RunKind != RunKindRead {
		t.Fatalf("fetched run_kind = %q, want read", fetched.RunKind)
	}
	if fetched.RunbookSlug != "" {
		t.Fatalf("fetched runbook_slug = %q, want empty", fetched.RunbookSlug)
	}

	// 2) run_kind=runbook ∈ runbook_slug 往返
	rbTask := sampleScheduledTask(now)
	rbTask.RunKind = RunKindRunbook
	rbTask.RunbookSlug = "minio-retention-low-risk"
	rbTask.CapabilityName = ""
	rbCreated, err := store.CreateTask(ctx, rbTask)
	if err != nil {
		t.Fatalf("create runbook task: %v", err)
	}
	if rbCreated.RunKind != RunKindRunbook || rbCreated.RunbookSlug != "minio-retention-low-risk" {
		t.Fatalf("stored runbook task = %+v", rbCreated)
	}
	rbFetched, err := store.GetTask(ctx, rbCreated.ID, "alice")
	if err != nil {
		t.Fatalf("get runbook task: %v", err)
	}
	if rbFetched.RunKind != RunKindRunbook {
		t.Fatalf("fetched run_kind = %q, want runbook", rbFetched.RunKind)
	}
	if rbFetched.RunbookSlug != "minio-retention-low-risk" {
		t.Fatalf("fetched runbook_slug = %q, want minio-retention-low-risk", rbFetched.RunbookSlug)
	}

	// 3) ListDueTasks / ListTasks 也带出 run_kind
	due, err := store.ListDueTasks(ctx, now.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	found := false
	for _, dt := range due {
		if dt.ID == rbCreated.ID {
			found = true
			if dt.RunKind != RunKindRunbook {
				t.Fatalf("due runbook task run_kind = %q, want runbook", dt.RunKind)
			}
		}
	}
	if !found {
		t.Fatal("runbook task not found among due tasks")
	}
}
