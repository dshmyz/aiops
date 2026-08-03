package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// newTestService 构造一个 Service，使用 memory store + memory audit + fake runner。
func newTestService(t *testing.T, now time.Time) (*Service, *store.MemoryActionPlanStore, *store.MemoryScheduledTaskStore, *fakeReadRunner) {
	t.Helper()
	repository := store.NewMemoryActionPlanStore()
	auditService := audit.NewService(repository)
	taskStore := store.NewMemoryScheduledTaskStore()
	runner := &fakeReadRunner{}
	readService := execution.NewReadOnlyService(runner, auditService)
	svc := NewService(taskStore, readService, auditService, func() time.Time { return now })
	return svc, repository, taskStore, runner
}

func adminUser() identity.CurrentUser {
	return identity.CurrentUser{
		Subject:             "admin-1",
		Roles:               []string{"admin"},
		AllowedEnvironments: []string{"prod"},
		RequestID:           "req-admin",
	}
}

func viewerUser() identity.CurrentUser {
	return identity.CurrentUser{
		Subject:             "viewer-1",
		Roles:               []string{"viewer"},
		AllowedEnvironments: []string{"prod"},
		RequestID:           "req-viewer",
	}
}

func TestServiceCreateAdminSucceeds(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	svc, _, taskStore, _ := newTestService(t, now)
	ctx := context.Background()

	task, err := svc.Create(ctx, adminUser(), CreateRequest{
		Name:           "minio 巡检",
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
	if task.ID == "" {
		t.Fatal("task id must be set")
	}
	if task.Subject != "admin-1" {
		t.Fatalf("subject = %q, want admin-1", task.Subject)
	}
	// preset=5m → next_run_at 应在 now 之后
	if !task.NextRunAt.After(now) {
		t.Fatalf("next_run_at = %v, want after now", task.NextRunAt)
	}
	// 验证已持久化
	saved, err := taskStore.GetTask(ctx, task.ID, "admin-1")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if saved.Name != "minio 巡检" {
		t.Fatalf("saved name = %q, want 'minio 巡检'", saved.Name)
	}
}

func TestServiceCreateNonAdminReturnsForbidden(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	svc, _, _, _ := newTestService(t, now)
	ctx := context.Background()

	_, err := svc.Create(ctx, viewerUser(), CreateRequest{
		Name:           "minio 巡检",
		CapabilityName: "minio.bucket.health.read",
		Input:          map[string]any{"environment": "prod", "name": "archive"},
		ScheduleKind:   "preset",
		Preset:         "5m",
		Timezone:       "Asia/Shanghai",
		Enabled:        true,
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("create as viewer = %v, want ErrForbidden", err)
	}
}

func TestServiceCreatePresetDailyCalculatesNextRunAt(t *testing.T) {
	t.Parallel()
	// now = 2026-07-27 10:00 UTC = 2026-07-27 18:00 CST
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	svc, _, _, _ := newTestService(t, now)
	ctx := context.Background()

	task, err := svc.Create(ctx, adminUser(), CreateRequest{
		Name:           "daily 巡检",
		CapabilityName: "minio.bucket.health.read",
		Input:          map[string]any{"environment": "prod", "name": "archive"},
		ScheduleKind:   "preset",
		Preset:         "daily",
		Timezone:       "Asia/Shanghai",
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// daily → 次日 00:00 CST = 2026-07-27 16:00 UTC
	want := time.Date(2026, time.July, 27, 16, 0, 0, 0, time.UTC)
	if !task.NextRunAt.Equal(want) {
		t.Fatalf("next_run_at = %v, want %v", task.NextRunAt, want)
	}
}

func TestServiceCreateCronCalculatesNextRunAt(t *testing.T) {
	t.Parallel()
	// 2026-07-27 是周一
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	svc, _, _, _ := newTestService(t, now)
	ctx := context.Background()

	task, err := svc.Create(ctx, adminUser(), CreateRequest{
		Name:           "cron 巡检",
		CapabilityName: "minio.bucket.health.read",
		Input:          map[string]any{"environment": "prod", "name": "archive"},
		ScheduleKind:   "cron",
		CronExpr:       "0 2 * * 1-5",
		Timezone:       "Asia/Shanghai",
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// cron "0 2 * * 1-5" → 下个工作日 02:00 CST
	// now CST = 2026-07-27 18:00 → 下次 = 2026-07-28 02:00 CST = 2026-07-27 18:00 UTC
	want := time.Date(2026, time.July, 27, 18, 0, 0, 0, time.UTC)
	if !task.NextRunAt.Equal(want) {
		t.Fatalf("next_run_at = %v, want %v", task.NextRunAt, want)
	}
}

func TestServiceUpdateAdminRecalculatesNextRunAt(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	svc, _, taskStore, _ := newTestService(t, now)
	ctx := context.Background()

	original, err := svc.Create(ctx, adminUser(), CreateRequest{
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
	originalNextRun := original.NextRunAt

	// 更新为 daily
	updated, err := svc.Update(ctx, adminUser(), original.ID, UpdateRequest{
		Name:           "巡检-updated",
		CapabilityName: "minio.bucket.health.read",
		Input:          map[string]any{"environment": "prod", "name": "archive"},
		ScheduleKind:   "preset",
		Preset:         "daily",
		Timezone:       "Asia/Shanghai",
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "巡检-updated" {
		t.Fatalf("name = %q, want '巡检-updated'", updated.Name)
	}
	// schedule 变了，next_run_at 应重算
	if updated.NextRunAt.Equal(originalNextRun) {
		t.Fatal("next_run_at should be recalculated when schedule changes")
	}

	// 验证已持久化
	saved, err := taskStore.GetTask(ctx, original.ID, "admin-1")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if saved.Preset != "daily" {
		t.Fatalf("preset = %q, want daily", saved.Preset)
	}
}

func TestServiceUpdateNonAdminReturnsForbidden(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	svc, _, _, _ := newTestService(t, now)
	ctx := context.Background()

	original, err := svc.Create(ctx, adminUser(), CreateRequest{
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

	_, err = svc.Update(ctx, viewerUser(), original.ID, UpdateRequest{
		Name:           "巡检",
		CapabilityName: "minio.bucket.health.read",
		Input:          map[string]any{"environment": "prod", "name": "archive"},
		ScheduleKind:   "preset",
		Preset:         "1h",
		Timezone:       "Asia/Shanghai",
		Enabled:        true,
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("update as viewer = %v, want ErrForbidden", err)
	}
}

func TestServiceUpdateNonExistentReturnsNotFound(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	svc, _, _, _ := newTestService(t, now)
	ctx := context.Background()

	_, err := svc.Update(ctx, adminUser(), "nonexistent", UpdateRequest{
		Name:           "巡检",
		CapabilityName: "minio.bucket.health.read",
		Input:          map[string]any{"environment": "prod", "name": "archive"},
		ScheduleKind:   "preset",
		Preset:         "5m",
		Timezone:       "Asia/Shanghai",
		Enabled:        true,
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update nonexistent = %v, want ErrNotFound", err)
	}
}

func TestServiceDeleteAdminSucceeds(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	svc, _, taskStore, _ := newTestService(t, now)
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

	if err := svc.Delete(ctx, adminUser(), task.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := taskStore.GetTask(ctx, task.ID, "admin-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get deleted task = %v, want ErrNotFound", err)
	}
}

func TestServiceDeleteNonAdminReturnsForbidden(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	svc, _, _, _ := newTestService(t, now)
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

	if err := svc.Delete(ctx, viewerUser(), task.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("delete as viewer = %v, want ErrForbidden", err)
	}
}

func TestServiceGetReturnsTaskForAnyUser(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	svc, _, _, _ := newTestService(t, now)
	ctx := context.Background()

	created, err := svc.Create(ctx, adminUser(), CreateRequest{
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

	// viewer 也能读
	task, err := svc.Get(ctx, viewerUser(), created.ID)
	if err != nil {
		t.Fatalf("get as viewer: %v", err)
	}
	if task.ID != created.ID {
		t.Fatalf("task id = %q, want %q", task.ID, created.ID)
	}
}

func TestServiceListReturnsTasksForAnyUser(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	svc, _, _, _ := newTestService(t, now)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := svc.Create(ctx, adminUser(), CreateRequest{
			Name:           "巡检",
			CapabilityName: "minio.bucket.health.read",
			Input:          map[string]any{"environment": "prod", "name": "archive"},
			ScheduleKind:   "preset",
			Preset:         "5m",
			Timezone:       "Asia/Shanghai",
			Enabled:        true,
		}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	tasks, err := svc.List(ctx, viewerUser(), store.ScheduledTaskFilter{Subject: "admin-1"})
	if err != nil {
		t.Fatalf("list as viewer: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("tasks = %d, want 3", len(tasks))
	}
}

func TestServiceTriggerAdminExecutesWithoutUpdatingNextRunAt(t *testing.T) {
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
	if run.AuditEventID == "" {
		t.Fatal("run audit_event_id must be set")
	}

	// next_run_at 不应变化
	updated, err := taskStore.GetTask(ctx, task.ID, "admin-1")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if !updated.NextRunAt.Equal(originalNextRun) {
		t.Fatalf("next_run_at = %v, want %v (trigger should not update next_run_at)", updated.NextRunAt, originalNextRun)
	}
	// last_run_at 和 last_status 应更新
	if updated.LastRunAt == nil || !updated.LastRunAt.Equal(now) {
		t.Fatalf("last_run_at = %v, want %v", updated.LastRunAt, now)
	}
	if updated.LastStatus != store.ScheduledTaskStatusSucceeded {
		t.Fatalf("last_status = %q, want succeeded", updated.LastStatus)
	}
}

func TestServiceTriggerNonAdminReturnsForbidden(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	svc, _, _, runner := newTestService(t, now)
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

	_, err = svc.Trigger(ctx, viewerUser(), task.ID)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("trigger as viewer = %v, want ErrForbidden", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner calls = %d, want 0", runner.callCount())
	}
}

func TestServiceListRunsReturnsHistory(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	svc, _, _, _ := newTestService(t, now)
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

	// 手动触发两次
	if _, err := svc.Trigger(ctx, adminUser(), task.ID); err != nil {
		t.Fatalf("trigger 1: %v", err)
	}
	if _, err := svc.Trigger(ctx, adminUser(), task.ID); err != nil {
		t.Fatalf("trigger 2: %v", err)
	}

	runs, err := svc.ListRuns(ctx, viewerUser(), task.ID, 0)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(runs))
	}
}

func TestServiceCountRecentFailures(t *testing.T) {
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

	// 写一条 failed run
	runner.err = errors.New("capability unavailable")
	if _, err := svc.Trigger(ctx, adminUser(), task.ID); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	runner.err = nil

	// 写一条 succeeded run
	if _, err := svc.Trigger(ctx, adminUser(), task.ID); err != nil {
		t.Fatalf("trigger 2: %v", err)
	}

	// 统计 24h 内失败数
	count, err := taskStore.CountRecentFailures(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("count failures: %v", err)
	}
	if count != 1 {
		t.Fatalf("failures = %d, want 1", count)
	}

	// 通过 service 接口也验证
	svcCount, err := svc.CountRecentFailures(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("service count failures: %v", err)
	}
	if svcCount != 1 {
		t.Fatalf("service failures = %d, want 1", svcCount)
	}
}
