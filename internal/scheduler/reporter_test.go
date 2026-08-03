package scheduler_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/scheduler"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// stubRenderer 是测试用 HTML 渲染器，记录传入的报告并返回固定 HTML。
type stubRenderer struct {
	lastReport scheduler.InspectionReport
	output     string
}

func (r *stubRenderer) Render(report scheduler.InspectionReport) string {
	r.lastReport = report
	if r.output != "" {
		return r.output
	}
	return "<html>stub</html>"
}

func newTaskStoreWithRuns() *store.MemoryScheduledTaskStore {
	s := store.NewMemoryScheduledTaskStore()
	return s
}

func addTask(t *testing.T, s *store.MemoryScheduledTaskStore, id, name, capability string) store.ScheduledTask {
	t.Helper()
	task, err := s.CreateTask(context.Background(), store.ScheduledTask{
		ID: id, Name: name, Subject: "admin", CapabilityName: capability,
		ScheduleKind: "preset", Preset: "daily", Timezone: "Asia/Shanghai",
		Enabled: true, NextRunAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return task
}

func addRun(t *testing.T, s *store.MemoryScheduledTaskStore, taskID string, started time.Time, status, summary, errMsg string) {
	t.Helper()
	finished := started.Add(30 * time.Second)
	_, err := s.AppendRun(context.Background(), store.ScheduledTaskRun{
		TaskID: taskID, StartedAt: started, FinishedAt: finished,
		Status: status, ResultSummary: summary, Error: errMsg,
	})
	if err != nil {
		t.Fatalf("AppendRun: %v", err)
	}
}

// TestReporterGenerateForWindowAggregatesRuns verifies that the reporter
// correctly aggregates runs within the window into per-task summaries and
// global statistics.
func TestReporterGenerateForWindowAggregatesRuns(t *testing.T) {
	t.Parallel()
	taskStore := newTaskStoreWithRuns()
	task1 := addTask(t, taskStore, "t1", "Kafka 健康巡检", "kafka.cluster.status.read")
	task2 := addTask(t, taskStore, "t2", "MinIO 容量巡检", "minio.cluster.capacity.read")

	windowStart := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	windowEnd := windowStart.Add(24 * time.Hour)

	// task1: 2 succeeded + 1 failed (in window)
	addRun(t, taskStore, task1.ID, windowStart.Add(1*time.Hour), store.ScheduledTaskStatusSucceeded, "ok", "")
	addRun(t, taskStore, task1.ID, windowStart.Add(2*time.Hour), store.ScheduledTaskStatusFailed, "", "connection refused")
	addRun(t, taskStore, task1.ID, windowStart.Add(3*time.Hour), store.ScheduledTaskStatusSucceeded, "ok", "")
	// task2: 1 succeeded (in window)
	addRun(t, taskStore, task2.ID, windowStart.Add(4*time.Hour), store.ScheduledTaskStatusSucceeded, "healthy", "")

	// out-of-window run should be excluded
	addRun(t, taskStore, task1.ID, windowStart.Add(-2*time.Hour), store.ScheduledTaskStatusSucceeded, "old", "")

	renderer := &stubRenderer{output: "<html>kafka+minio</html>"}
	now := windowEnd.Add(time.Hour)
	reporter := scheduler.NewReporter(taskStore, renderer, func() time.Time { return now })

	report, err := reporter.GenerateForWindow(context.Background(), store.InspectionPeriodDaily, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("GenerateForWindow: %v", err)
	}

	if report.Period != store.InspectionPeriodDaily {
		t.Fatalf("Period = %q, want daily", report.Period)
	}
	if !report.WindowStart.Equal(windowStart) || !report.WindowEnd.Equal(windowEnd) {
		t.Fatalf("Window = [%s, %s), want [%s, %s)", report.WindowStart, report.WindowEnd, windowStart, windowEnd)
	}
	if report.TotalTasks != 2 {
		t.Fatalf("TotalTasks = %d, want 2", report.TotalTasks)
	}
	// task1 has at least one failure → counted as failed; task2 all succeeded
	if report.SucceededTasks != 1 {
		t.Fatalf("SucceededTasks = %d, want 1", report.SucceededTasks)
	}
	if report.FailedTasks != 1 {
		t.Fatalf("FailedTasks = %d, want 1", report.FailedTasks)
	}

	// Verify per-task summaries
	summaryByID := map[string]store.InspectionTaskSummary{}
	for _, ts := range report.TaskSummaries {
		summaryByID[ts.TaskID] = ts
	}
	s1 := summaryByID[task1.ID]
	if s1.TotalRuns != 3 || s1.SucceededRuns != 2 || s1.FailedRuns != 1 {
		t.Fatalf("task1 summary = %+v, want 3 total/2 succ/1 fail", s1)
	}
	if s1.LastStatus != store.ScheduledTaskStatusSucceeded {
		t.Fatalf("task1 LastStatus = %q, want succeeded (last run)", s1.LastStatus)
	}
	if s1.LastResultSummary != "ok" {
		t.Fatalf("task1 LastResultSummary = %q, want ok", s1.LastResultSummary)
	}
	if s1.TaskName != "Kafka 健康巡检" || s1.CapabilityName != "kafka.cluster.status.read" {
		t.Fatalf("task1 meta = %+v, want Kafka 健康巡检/kafka.cluster.status.read", s1)
	}

	s2 := summaryByID[task2.ID]
	if s2.TotalRuns != 1 || s2.SucceededRuns != 1 || s2.FailedRuns != 0 {
		t.Fatalf("task2 summary = %+v, want 1 total/1 succ/0 fail", s2)
	}

	// HTML rendered
	if report.HTMLContent != "<html>kafka+minio</html>" {
		t.Fatalf("HTMLContent = %q, want rendered html", report.HTMLContent)
	}
	if !report.GeneratedAt.Equal(now) {
		t.Fatalf("GeneratedAt = %s, want %s", report.GeneratedAt, now)
	}
}

// TestReporterGenerateForWindowExcludesTasksWithoutRuns verifies that tasks
// with no runs in the window are excluded from the report.
func TestReporterGenerateForWindowExcludesTasksWithoutRuns(t *testing.T) {
	t.Parallel()
	taskStore := newTaskStoreWithRuns()
	task1 := addTask(t, taskStore, "t1", "Kafka 巡检", "kafka.status.read")
	addTask(t, taskStore, "t2", "空闲任务", "noop.read") // no runs

	windowStart := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	windowEnd := windowStart.Add(24 * time.Hour)
	addRun(t, taskStore, task1.ID, windowStart.Add(1*time.Hour), store.ScheduledTaskStatusSucceeded, "ok", "")

	reporter := scheduler.NewReporter(taskStore, &stubRenderer{}, func() time.Time { return windowEnd })

	report, err := reporter.GenerateForWindow(context.Background(), store.InspectionPeriodDaily, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("GenerateForWindow: %v", err)
	}
	if report.TotalTasks != 1 {
		t.Fatalf("TotalTasks = %d, want 1 (task without runs excluded)", report.TotalTasks)
	}
	if len(report.TaskSummaries) != 1 || report.TaskSummaries[0].TaskID != task1.ID {
		t.Fatalf("TaskSummaries = %+v, want only task1", report.TaskSummaries)
	}
}

// TestReporterGenerateDailyWindow verifies that GenerateDaily produces a
// report covering the previous UTC day [00:00, 24:00).
func TestReporterGenerateDailyWindow(t *testing.T) {
	t.Parallel()
	taskStore := newTaskStoreWithRuns()
	task1 := addTask(t, taskStore, "t1", "巡检", "status.read")

	// "now" is 2026-08-01 10:00 UTC → daily window is 2026-07-31 00:00 ~ 24:00
	now := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	yesterday := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)

	addRun(t, taskStore, task1.ID, yesterday.Add(5*time.Hour), store.ScheduledTaskStatusSucceeded, "ok", "")
	// run before yesterday — excluded
	addRun(t, taskStore, task1.ID, yesterday.Add(-2*time.Hour), store.ScheduledTaskStatusSucceeded, "old", "")

	reporter := scheduler.NewReporter(taskStore, &stubRenderer{}, func() time.Time { return now })
	report, err := reporter.GenerateDaily(context.Background())
	if err != nil {
		t.Fatalf("GenerateDaily: %v", err)
	}
	if !report.WindowStart.Equal(yesterday) {
		t.Fatalf("WindowStart = %s, want %s", report.WindowStart, yesterday)
	}
	expectedEnd := yesterday.Add(24 * time.Hour)
	if !report.WindowEnd.Equal(expectedEnd) {
		t.Fatalf("WindowEnd = %s, want %s", report.WindowEnd, expectedEnd)
	}
	if report.TotalTasks != 1 {
		t.Fatalf("TotalTasks = %d, want 1", report.TotalTasks)
	}
	if report.TaskSummaries[0].TotalRuns != 1 {
		t.Fatalf("TotalRuns = %d, want 1 (only in-window run)", report.TaskSummaries[0].TotalRuns)
	}
}

// TestReporterHTMLContentContainsKeyInfo verifies the default HTML renderer
// produces a report with task names, status counts, and the window.
func TestReporterHTMLContentContainsKeyInfo(t *testing.T) {
	t.Parallel()
	taskStore := newTaskStoreWithRuns()
	task1 := addTask(t, taskStore, "t1", "Kafka 健康巡检", "kafka.status.read")
	windowStart := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	windowEnd := windowStart.Add(24 * time.Hour)
	addRun(t, taskStore, task1.ID, windowStart.Add(1*time.Hour), store.ScheduledTaskStatusFailed, "", "timeout")

	reporter := scheduler.NewReporter(taskStore, scheduler.DefaultHTMLRenderer{}, func() time.Time { return windowEnd })
	report, err := reporter.GenerateForWindow(context.Background(), store.InspectionPeriodDaily, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("GenerateForWindow: %v", err)
	}
	html := report.HTMLContent
	for _, want := range []string{"Kafka 健康巡检", "failed", "2026-07-31"} {
		if !strings.Contains(html, want) {
			t.Fatalf("HTML missing %q:\n%s", want, html)
		}
	}
}
