package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// 报告周期常量。
const (
	InspectionPeriodDaily  = "daily"
	InspectionPeriodWeekly = "weekly"
)

// InspectionReport 是一次巡检报告的持久化记录。由 Reporter 按时间窗口聚合
// 多个 ScheduledTask 的 runs 生成，包含统计摘要和渲染后的 HTML 内容。
type InspectionReport struct {
	ID             string                  `json:"id"`
	Period         string                  `json:"period"`
	WindowStart    time.Time               `json:"window_start"`
	WindowEnd      time.Time               `json:"window_end"`
	GeneratedAt    time.Time               `json:"generated_at"`
	TotalTasks     int                     `json:"total_tasks"`
	SucceededTasks int                     `json:"succeeded_tasks"`
	FailedTasks    int                     `json:"failed_tasks"`
	TaskSummaries  []InspectionTaskSummary `json:"task_summaries,omitempty"`
	HTMLContent    string                  `json:"html_content"`
}

// InspectionTaskSummary 是单个定时任务在报告窗口内的执行聚合。
type InspectionTaskSummary struct {
	TaskID            string    `json:"task_id"`
	TaskName          string    `json:"task_name"`
	CapabilityName    string    `json:"capability_name"`
	TotalRuns         int       `json:"total_runs"`
	SucceededRuns     int       `json:"succeeded_runs"`
	FailedRuns        int       `json:"failed_runs"`
	LastStatus        string    `json:"last_status,omitempty"`
	LastResultSummary string    `json:"last_result_summary,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
	LastRunAt         time.Time `json:"last_run_at,omitempty"`
}

// InspectionReportStore 持久化巡检报告。
type InspectionReportStore interface {
	CreateReport(ctx context.Context, report InspectionReport) (InspectionReport, error)
	GetReport(ctx context.Context, id string) (InspectionReport, error)
	ListReports(ctx context.Context, limit int) ([]InspectionReport, error)
}

// MemoryInspectionReportStore 提供并发安全的内存实现，用于单元测试。
type MemoryInspectionReportStore struct {
	mu      sync.Mutex
	reports []InspectionReport
}

func (m *MemoryInspectionReportStore) CreateReport(_ context.Context, report InspectionReport) (InspectionReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if report.ID == "" {
		report.ID = uuid.New().String()
	}
	m.reports = append(m.reports, report)
	return report, nil
}

func (m *MemoryInspectionReportStore) GetReport(_ context.Context, id string) (InspectionReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.reports {
		if r.ID == id {
			return r, nil
		}
	}
	return InspectionReport{}, ErrInspectionReportNotFound
}

func (m *MemoryInspectionReportStore) ListReports(_ context.Context, limit int) ([]InspectionReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 按 GeneratedAt 降序返回。
	out := make([]InspectionReport, len(m.reports))
	copy(out, m.reports)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ErrInspectionReportNotFound 表示指定 ID 的巡检报告不存在。
var ErrInspectionReportNotFound = errInspectionReportNotFound{}

type errInspectionReportNotFound struct{}

func (errInspectionReportNotFound) Error() string { return "inspection report not found" }

// SQLInspectionReportStore 在 MySQL/SQLite 上持久化巡检报告。
type SQLInspectionReportStore struct{ db *sql.DB }

func NewSQLInspectionReportStore(db *sql.DB) *SQLInspectionReportStore {
	return &SQLInspectionReportStore{db: db}
}

func (s *SQLInspectionReportStore) CreateReport(ctx context.Context, report InspectionReport) (InspectionReport, error) {
	if report.ID == "" {
		report.ID = uuid.NewString()
	}
	summariesJSON, err := json.Marshal(report.TaskSummaries)
	if err != nil {
		return InspectionReport{}, fmt.Errorf("marshal task summaries: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO copilot_inspection_reports
		(id, period, window_start, window_end, generated_at, total_tasks, succeeded_tasks, failed_tasks, task_summaries, html_content)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		report.ID, report.Period, report.WindowStart, report.WindowEnd, report.GeneratedAt,
		report.TotalTasks, report.SucceededTasks, report.FailedTasks, string(summariesJSON), report.HTMLContent)
	if err != nil {
		return InspectionReport{}, fmt.Errorf("insert inspection report: %w", err)
	}
	return report, nil
}

func (s *SQLInspectionReportStore) GetReport(ctx context.Context, id string) (InspectionReport, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, period, window_start, window_end, generated_at, total_tasks, succeeded_tasks, failed_tasks, task_summaries, html_content
		FROM copilot_inspection_reports WHERE id = ?`, id)
	report, err := scanInspectionReport(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return InspectionReport{}, ErrInspectionReportNotFound
		}
		return InspectionReport{}, fmt.Errorf("get inspection report: %w", err)
	}
	return report, nil
}

func (s *SQLInspectionReportStore) ListReports(ctx context.Context, limit int) ([]InspectionReport, error) {
	query := `SELECT id, period, window_start, window_end, generated_at, total_tasks, succeeded_tasks, failed_tasks, task_summaries, html_content
		FROM copilot_inspection_reports ORDER BY generated_at DESC`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list inspection reports: %w", err)
	}
	defer rows.Close()
	var reports []InspectionReport
	for rows.Next() {
		report, err := scanInspectionReport(rows)
		if err != nil {
			return nil, fmt.Errorf("scan inspection report: %w", err)
		}
		reports = append(reports, report)
	}
	return reports, rows.Err()
}

type reportScanner interface {
	Scan(dest ...any) error
}

func scanInspectionReport(row reportScanner) (InspectionReport, error) {
	var report InspectionReport
	var summariesJSON string
	err := row.Scan(
		&report.ID, &report.Period, &report.WindowStart, &report.WindowEnd, &report.GeneratedAt,
		&report.TotalTasks, &report.SucceededTasks, &report.FailedTasks,
		&summariesJSON, &report.HTMLContent,
	)
	if err != nil {
		return InspectionReport{}, err
	}
	if summariesJSON != "" {
		if err := json.Unmarshal([]byte(summariesJSON), &report.TaskSummaries); err != nil {
			return InspectionReport{}, fmt.Errorf("unmarshal task summaries: %w", err)
		}
	}
	return report, nil
}
