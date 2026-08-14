package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestSQLInspectionReportStoreLifecycle 覆盖 SQL 实现的 CRUD。
func TestSQLInspectionReportStoreLifecycle(t *testing.T) {
	t.Parallel()
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	ctx := context.Background()
	s := NewSQLInspectionReportStore(db)

	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	report := InspectionReport{
		Period:         InspectionPeriodDaily,
		WindowStart:    time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
		WindowEnd:      time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		GeneratedAt:    now,
		TotalTasks:     3,
		SucceededTasks: 2,
		FailedTasks:    1,
		TaskSummaries: []InspectionTaskSummary{
			{TaskID: "t1", TaskName: "Kafka 巡检", CapabilityName: "kafka.status.read", TotalRuns: 2, SucceededRuns: 2, FailedRuns: 0, LastStatus: ScheduledTaskStatusSucceeded, LastResultSummary: "ok", LastRunAt: now},
			{TaskID: "t2", TaskName: "MinIO 巡检", CapabilityName: "minio.status.read", TotalRuns: 1, SucceededRuns: 0, FailedRuns: 1, LastStatus: ScheduledTaskStatusFailed, LastError: "timeout", LastRunAt: now},
		},
		HTMLContent: "<html>report</html>",
	}

	// Create
	created, err := s.CreateReport(ctx, report)
	if err != nil {
		t.Fatalf("CreateReport: %v", err)
	}
	if created.ID == "" {
		t.Fatal("created ID is empty")
	}

	// Get
	fetched, err := s.GetReport(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetReport: %v", err)
	}
	if fetched.Period != InspectionPeriodDaily {
		t.Fatalf("Period = %q, want daily", fetched.Period)
	}
	if fetched.TotalTasks != 3 || fetched.SucceededTasks != 2 || fetched.FailedTasks != 1 {
		t.Fatalf("stats = %d/%d/%d, want 3/2/1", fetched.TotalTasks, fetched.SucceededTasks, fetched.FailedTasks)
	}
	if len(fetched.TaskSummaries) != 2 {
		t.Fatalf("TaskSummaries len = %d, want 2", len(fetched.TaskSummaries))
	}
	if fetched.TaskSummaries[0].TaskName != "Kafka 巡检" {
		t.Fatalf("first summary name = %q, want Kafka 巡检", fetched.TaskSummaries[0].TaskName)
	}
	if fetched.TaskSummaries[1].LastError != "timeout" {
		t.Fatalf("second summary error = %q, want timeout", fetched.TaskSummaries[1].LastError)
	}
	if fetched.HTMLContent != "<html>report</html>" {
		t.Fatalf("HTMLContent = %q", fetched.HTMLContent)
	}
	if !fetched.WindowStart.Equal(report.WindowStart) {
		t.Fatalf("WindowStart mismatch: %s vs %s", fetched.WindowStart, report.WindowStart)
	}

	// Get non-existent
	_, err = s.GetReport(ctx, "nonexistent")
	if !errors.Is(err, ErrInspectionReportNotFound) {
		t.Fatalf("GetReport nonexistent = %v, want ErrInspectionReportNotFound", err)
	}

	// Create second report for List ordering
	_, err = s.CreateReport(ctx, InspectionReport{
		Period: InspectionPeriodDaily, WindowStart: report.WindowEnd,
		WindowEnd: report.WindowEnd.Add(24 * time.Hour), GeneratedAt: now.Add(24 * time.Hour),
		TotalTasks: 1, SucceededTasks: 1, FailedTasks: 0, HTMLContent: "<html>day2</html>",
	})
	if err != nil {
		t.Fatalf("CreateReport second: %v", err)
	}

	// List (newest first)
	listed, err := s.ListReports(ctx, 0)
	if err != nil {
		t.Fatalf("ListReports: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("ListReports len = %d, want 2", len(listed))
	}
	if !listed[0].GeneratedAt.After(listed[1].GeneratedAt) {
		t.Fatal("ListReports not sorted by GeneratedAt DESC")
	}

	// List with limit
	limited, _ := s.ListReports(ctx, 1)
	if len(limited) != 1 {
		t.Fatalf("ListReports limit=1 len = %d, want 1", len(limited))
	}
}

// TestSQLInspectionReportStoreIdempotentClaim 验证多节点幂等认领：同一
// (period, window_start) 只写一份，重复创建返回首次写入的那份而非重复插入。
func TestSQLInspectionReportStoreIdempotentClaim(t *testing.T) {
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	ctx := context.Background()
	s := NewSQLInspectionReportStore(db)

	win := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	mk := func(total int) InspectionReport {
		return InspectionReport{
			Period:         InspectionPeriodDaily,
			WindowStart:    win,
			WindowEnd:      win.Add(24 * time.Hour),
			GeneratedAt:    time.Now().UTC(),
			TotalTasks:     total,
			HTMLContent:    "<html>r</html>",
			TaskSummaries:  []InspectionTaskSummary{},
		}
	}

	// 两个节点同时生成同日日报（同一窗口），应只落一份。
	first, err := s.CreateReport(ctx, mk(3))
	if err != nil {
		t.Fatalf("first CreateReport: %v", err)
	}
	second, err := s.CreateReport(ctx, mk(99))
	if err != nil {
		t.Fatalf("second CreateReport: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("idempotent claim violated: second.ID=%q != first.ID=%q", second.ID, first.ID)
	}
	if second.TotalTasks != 3 {
		t.Fatalf("second returned TotalTasks=%d, want the original writer's (3)", second.TotalTasks)
	}

	// 库存量应只有 1 条。
	listed, err := s.ListReports(ctx, 0)
	if err != nil {
		t.Fatalf("ListReports: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListReports len = %d, want 1 (one report per window)", len(listed))
	}

	// 不同窗口应各自独立成条。
	other := mk(5)
	other.WindowStart = win.Add(24 * time.Hour)
	other.WindowEnd = win.Add(48 * time.Hour)
	third, err := s.CreateReport(ctx, other)
	if err != nil {
		t.Fatalf("third CreateReport: %v", err)
	}
	if third.ID == first.ID {
		t.Fatal("different window should create a distinct report")
	}
}
