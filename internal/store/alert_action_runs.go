package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// AlertActionRunRecord 是告警→动作编排的一次执行历史记录。
type AlertActionRunRecord struct {
	ID         int64           `json:"id"`
	RuleName   string          `json:"rule_name"`
	AlertID    string          `json:"alert_id,omitempty"`
	AlertTitle string          `json:"alert_title,omitempty"`
	Status     string          `json:"status"` // success | failure
	Steps      json.RawMessage `json:"steps"`  // []StepResult
	Summary    string          `json:"summary,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// AlertActionRunStore 持久化告警编排执行历史。
type AlertActionRunStore interface {
	Append(ctx context.Context, run AlertActionRunRecord) error
	RecentByRule(ctx context.Context, ruleName string, limit int) ([]AlertActionRunRecord, error)
	RuleStats(ctx context.Context, ruleName string) (AlertActionRunStats, error)
}

// AlertActionRunStats 是某条规则的触发统计。
type AlertActionRunStats struct {
	Total   int `json:"total"`
	Success int `json:"success"`
	Failure int `json:"failure"`
}

// SQLAlertActionRunStore 实现 AlertActionRunStore（MySQL/SQLite）。
type SQLAlertActionRunStore struct {
	db *sql.DB
}

// NewSQLAlertActionRunStore 创建执行历史 store。
func NewSQLAlertActionRunStore(db *sql.DB) *SQLAlertActionRunStore {
	return &SQLAlertActionRunStore{db: db}
}

func (s *SQLAlertActionRunStore) Append(ctx context.Context, run AlertActionRunRecord) error {
	if len(run.Steps) == 0 {
		run.Steps = json.RawMessage("[]")
	}
	if run.Status == "" {
		run.Status = "success"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO alert_action_runs (rule_name, alert_id, alert_title, status, steps, summary, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		run.RuleName, run.AlertID, run.AlertTitle, run.Status, run.Steps, run.Summary, run.CreatedAt)
	if err != nil {
		return fmt.Errorf("append alert action run: %w", err)
	}
	return nil
}

func (s *SQLAlertActionRunStore) RecentByRule(ctx context.Context, ruleName string, limit int) ([]AlertActionRunRecord, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, rule_name, alert_id, alert_title, status, steps, summary, created_at
		 FROM alert_action_runs WHERE rule_name = ? ORDER BY id DESC LIMIT ?`,
		ruleName, limit)
	if err != nil {
		return nil, fmt.Errorf("recent alert action runs: %w", err)
	}
	defer rows.Close()

	var runs []AlertActionRunRecord
	for rows.Next() {
		var r AlertActionRunRecord
		if err := rows.Scan(&r.ID, &r.RuleName, &r.AlertID, &r.AlertTitle, &r.Status, &r.Steps, &r.Summary, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan alert action run: %w", err)
		}
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if runs == nil {
		runs = []AlertActionRunRecord{}
	}
	return runs, nil
}

func (s *SQLAlertActionRunStore) RuleStats(ctx context.Context, ruleName string) (AlertActionRunStats, error) {
	var stats AlertActionRunStats
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*),
		        COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN status = 'failure' THEN 1 ELSE 0 END), 0)
		 FROM alert_action_runs WHERE rule_name = ?`,
		ruleName).Scan(&stats.Total, &stats.Success, &stats.Failure)
	if err != nil {
		return AlertActionRunStats{}, fmt.Errorf("alert action run stats: %w", err)
	}
	return stats, nil
}
