package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// fakeRunbookExecutor 实现 RunbookExecutor，可注入结果或错误，并记录调用次数与入参。
type fakeRunbookExecutor struct {
	calls    int
	result   map[string]any
	err      error
	lastTask store.ScheduledTask
}

func (f *fakeRunbookExecutor) Execute(_ context.Context, task store.ScheduledTask) (map[string]any, error) {
	f.calls++
	f.lastTask = task
	return f.result, f.err
}

func runbookTask(id string, slug string) store.ScheduledTask {
	return store.ScheduledTask{
		ID:             id,
		Name:           "runbook 任务",
		Subject:        "admin-1",
		CapabilityName: "",
		RunKind:        store.RunKindRunbook,
		RunbookSlug:    slug,
		Input:          map[string]any{"environment": "prod"},
		ScheduleKind:   store.ScheduleKindPreset,
		Preset:         "5m",
		Timezone:       "Asia/Shanghai",
		Enabled:        true,
		NextRunAt:      time.Date(2026, time.July, 27, 9, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2026, time.July, 27, 8, 55, 0, 0, time.UTC),
	}
}

// TestExecuteTaskBodyRunbookBypassesReadRunner 验证 run_kind=runbook 走 runbook 执行器
// 而非只读 capability（read runner 不应被调用）。
func TestExecuteTaskBodyRunbookBypassesReadRunner(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	repository := store.NewMemoryActionPlanStore()
	taskStore := store.NewMemoryScheduledTaskStore()
	auditService := audit.NewService(repository)
	readService := execution.NewReadOnlyService(&fakeReadRunner{}, auditService)
	fake := &fakeRunbookExecutor{result: map[string]any{"status": "ok"}}
	task := runbookTask("t1", "minio-retention-low-risk")
	task.CapabilityName = "minio-retention-low-risk"
	if _, err := taskStore.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	run, err := executeAndRecord(context.Background(), taskStore, readService, fake, auditService, task, func() time.Time { return now }, false)
	if err != nil {
		t.Fatalf("executeAndRecord: %v", err)
	}
	if run.Status != store.ScheduledTaskStatusSucceeded {
		t.Fatalf("run status = %q, want succeeded", run.Status)
	}
	if fake.calls != 1 {
		t.Fatalf("runbook executor calls = %d, want 1", fake.calls)
	}
	if fake.lastTask.RunbookSlug != "minio-retention-low-risk" {
		t.Fatalf("executor received slug = %q, want minio-retention-low-risk", fake.lastTask.RunbookSlug)
	}
	// 审计为 permitted
	events, err := repository.ListAudit(context.Background(), store.AuditFilter{Decision: audit.DecisionPermitted})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(events.Events) == 0 {
		t.Fatal("expected permitted audit event")
	}
}

// TestExecuteTaskBodyRunbookDeniedIsFailedRun 验证准入门拒绝（ErrRunbookDenied）时
// run 记为 failed 且审计为 denied（非静默）。
func TestExecuteTaskBodyRunbookDeniedIsFailedRun(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	repository := store.NewMemoryActionPlanStore()
	taskStore := store.NewMemoryScheduledTaskStore()
	auditService := audit.NewService(repository)
	readService := execution.NewReadOnlyService(&fakeReadRunner{}, auditService)
	fake := &fakeRunbookExecutor{err: ErrRunbookDenied}
	task := runbookTask("t1", "kafka-retention-low-risk")
	task.CapabilityName = "kafka-retention-low-risk"
	if _, err := taskStore.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	run, err := executeAndRecord(context.Background(), taskStore, readService, fake, auditService, task, func() time.Time { return now }, false)
	if err != nil {
		t.Fatalf("executeAndRecord: %v", err)
	}
	if run.Status != store.ScheduledTaskStatusFailed {
		t.Fatalf("run status = %q, want failed", run.Status)
	}
	if run.Error == "" {
		t.Fatal("run.error must be set for denied run")
	}
	// 审计 decision = denied
	events, err := repository.ListAudit(context.Background(), store.AuditFilter{Decision: audit.DecisionDenied})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(events.Events) == 0 {
		t.Fatal("expected denied audit event")
	}
	if events.Events[0].Decision != audit.DecisionDenied {
		t.Fatalf("audit decision = %q, want denied", events.Events[0].Decision)
	}
}

// TestExecuteTaskBodyRunbookNilExecutorFailsClosed 验证 runbook 执行器未注入时
// fail-closed（行为等同 denied）。
func TestExecuteTaskBodyRunbookNilExecutorFailsClosed(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryActionPlanStore()
	auditService := audit.NewService(repository)
	readService := execution.NewReadOnlyService(&fakeReadRunner{}, auditService)

	_, err := executeTaskBody(context.Background(), readService, nil, runbookTask("t1", "x"))
	if !errors.Is(err, ErrRunbookDenied) {
		t.Fatalf("err = %v, want ErrRunbookDenied", err)
	}
	_ = repository
	_ = auditService
}

// TestExecuteTaskBodyReadUsesReadRunner 验证 run_kind=read（默认）走只读执行器。
func TestExecuteTaskBodyReadUsesReadRunner(t *testing.T) {
	t.Parallel()
	runner := &fakeReadRunner{result: map[string]any{"status": "ok"}}
	repository := store.NewMemoryActionPlanStore()
	auditService := audit.NewService(repository)
	readService := execution.NewReadOnlyService(runner, auditService)
	// 即便注入 runbook 执行器，read 任务也不该调用它。
	fake := &fakeRunbookExecutor{result: map[string]any{"status": "ok"}}

	task := runbookTask("t1", "")
	task.RunKind = store.RunKindRead
	task.CapabilityName = "minio.bucket.health.read"

	result, err := executeTaskBody(context.Background(), readService, fake, task)
	if err != nil {
		t.Fatalf("executeTaskBody: %v", err)
	}
	if runner.callCount() != 1 {
		t.Fatalf("read runner calls = %d, want 1", runner.callCount())
	}
	if fake.calls != 0 {
		t.Fatalf("runbook executor calls = %d, want 0", fake.calls)
	}
	if result["status"] != "ok" {
		t.Fatalf("result = %v, want status ok", result)
	}
}

// TestNormalizeRunKind 验证 run_kind 归一化。
func TestNormalizeRunKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"", store.RunKindRead},
		{"read", store.RunKindRead},
		{"runbook", store.RunKindRunbook},
		{"garbage", store.RunKindRead},
	}
	for _, c := range cases {
		if got := normalizeRunKind(c.in); got != c.want {
			t.Errorf("normalizeRunKind(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
