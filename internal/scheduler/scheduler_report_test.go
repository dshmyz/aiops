package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/scheduler"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// TestSchedulerGenerateDailyReportPersists verifies that the scheduler
// generates a daily report and persists it to the report store.
func TestSchedulerGenerateDailyReportPersists(t *testing.T) {
	t.Parallel()
	taskStore := store.NewMemoryScheduledTaskStore()
	reportStore := &store.MemoryInspectionReportStore{}

	// Seed a task + run within yesterday's window.
	now := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	task, err := taskStore.CreateTask(context.Background(), store.ScheduledTask{
		Name: "Kafka 巡检", Subject: "admin", CapabilityName: "kafka.status.read",
		ScheduleKind: "preset", Preset: "daily", Timezone: "Asia/Shanghai",
		Enabled: true, NextRunAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	yesterday := time.Date(2026, 7, 31, 5, 0, 0, 0, time.UTC)
	_, _ = taskStore.AppendRun(context.Background(), store.ScheduledTaskRun{
		TaskID: task.ID, StartedAt: yesterday, FinishedAt: yesterday.Add(time.Minute),
		Status: store.ScheduledTaskStatusSucceeded, ResultSummary: "ok",
	})

	sched := scheduler.New(taskStore, nil, audit.NewService(nil), time.Minute, func() time.Time { return now })
	sched = sched.WithReportGeneration(reportStore, scheduler.NewReporter(taskStore, nil, func() time.Time { return now }))

	report, err := sched.GenerateDailyReport(context.Background())
	if err != nil {
		t.Fatalf("GenerateDailyReport: %v", err)
	}
	if report.ID == "" {
		t.Fatal("report ID is empty")
	}
	if report.TotalTasks != 1 {
		t.Fatalf("TotalTasks = %d, want 1", report.TotalTasks)
	}

	// Verify persistence.
	listed, err := reportStore.ListReports(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListReports: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != report.ID {
		t.Fatalf("persisted reports = %+v, want 1 with ID %s", listed, report.ID)
	}
}

// TestSchedulerGenerateDailyReportSkipsWhenNotConfigured verifies that
// calling GenerateDailyReport without a report store is a no-op (no error).
func TestSchedulerGenerateDailyReportSkipsWhenNotConfigured(t *testing.T) {
	t.Parallel()
	taskStore := store.NewMemoryScheduledTaskStore()
	sched := scheduler.New(taskStore, nil, audit.NewService(nil), time.Minute, time.Now)

	report, err := sched.GenerateDailyReport(context.Background())
	if err != nil {
		t.Fatalf("GenerateDailyReport: %v", err)
	}
	if report.ID != "" {
		t.Fatalf("report ID = %q, want empty (not configured)", report.ID)
	}
}

// TestSchedulerGenerateDailyReportUsesReadOnlyService ensures report
// generation does not depend on the execution ReadOnlyService being wired.
func TestSchedulerGenerateDailyReportWithReadOnlyService(t *testing.T) {
	t.Parallel()
	taskStore := store.NewMemoryScheduledTaskStore()
	reportStore := &store.MemoryInspectionReportStore{}
	now := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	reads := execution.NewReadOnlyService(nil, nil)
	sched := scheduler.New(taskStore, reads, nil, time.Minute, func() time.Time { return now })
	sched = sched.WithReportGeneration(reportStore, scheduler.NewReporter(taskStore, nil, func() time.Time { return now }))

	report, err := sched.GenerateDailyReport(context.Background())
	if err != nil {
		t.Fatalf("GenerateDailyReport: %v", err)
	}
	// No tasks → empty report but no error.
	if report.TotalTasks != 0 {
		t.Fatalf("TotalTasks = %d, want 0", report.TotalTasks)
	}
}
