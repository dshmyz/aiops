package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// AlertActionRuleRecord 是告警→动作编排规则的 DB 记录。
type AlertActionRuleRecord struct {
	Name            string          `json:"name"`
	AlertMatch      json.RawMessage `json:"alert_match"`
	ToolSequence    json.RawMessage `json:"tool_sequence"`
	ExecuteLastStep bool            `json:"execute_last_step"`
	Description     string          `json:"description"`
	Enabled         bool            `json:"enabled"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// AlertActionRuleStore CRUD 操作告警→动作编排规则。
type AlertActionRuleStore interface {
	List(ctx context.Context) ([]AlertActionRuleRecord, error)
	Get(ctx context.Context, name string) (AlertActionRuleRecord, error)
	Upsert(ctx context.Context, rule AlertActionRuleRecord) error
	Delete(ctx context.Context, name string) error
}

// SQLAlertActionRuleStore 实现 AlertActionRuleStore（MySQL/SQLite）。
type SQLAlertActionRuleStore struct {
	db *sql.DB
}

// NewSQLAlertActionRuleStore 创建 SQL 告警动作规则 store。
func NewSQLAlertActionRuleStore(db *sql.DB) *SQLAlertActionRuleStore {
	return &SQLAlertActionRuleStore{db: db}
}

func (s *SQLAlertActionRuleStore) List(ctx context.Context) ([]AlertActionRuleRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, alert_match, tool_sequence, execute_last_step, description, enabled, created_at, updated_at
		 FROM alert_action_rules ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list alert action rules: %w", err)
	}
	defer rows.Close()

	var rules []AlertActionRuleRecord
	for rows.Next() {
		var r AlertActionRuleRecord
		if err := rows.Scan(&r.Name, &r.AlertMatch, &r.ToolSequence, &r.ExecuteLastStep, &r.Description, &r.Enabled, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan alert action rule: %w", err)
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

func (s *SQLAlertActionRuleStore) Get(ctx context.Context, name string) (AlertActionRuleRecord, error) {
	var r AlertActionRuleRecord
	err := s.db.QueryRowContext(ctx,
		`SELECT name, alert_match, tool_sequence, execute_last_step, description, enabled, created_at, updated_at
		 FROM alert_action_rules WHERE name = ?`, name).Scan(
		&r.Name, &r.AlertMatch, &r.ToolSequence, &r.ExecuteLastStep, &r.Description, &r.Enabled, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return AlertActionRuleRecord{}, ErrNotFound
	}
	if err != nil {
		return AlertActionRuleRecord{}, fmt.Errorf("get alert action rule: %w", err)
	}
	return r, nil
}

func (s *SQLAlertActionRuleStore) Upsert(ctx context.Context, rule AlertActionRuleRecord) error {
	now := time.Now().UTC()
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = now
	}
	rule.UpdatedAt = now
	if rule.Name == "" {
		rule.Name = uuid.NewString()
	}

	if isSQLiteDriver(s.db) {
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO alert_action_rules (name, alert_match, tool_sequence, execute_last_step, description, enabled, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(name) DO UPDATE SET alert_match=excluded.alert_match, tool_sequence=excluded.tool_sequence,
			   execute_last_step=excluded.execute_last_step, description=excluded.description, enabled=excluded.enabled, updated_at=excluded.updated_at`,
			rule.Name, rule.AlertMatch, rule.ToolSequence, rule.ExecuteLastStep, rule.Description, rule.Enabled, rule.CreatedAt, rule.UpdatedAt)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO alert_action_rules (name, alert_match, tool_sequence, execute_last_step, description, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE alert_match=VALUES(alert_match), tool_sequence=VALUES(tool_sequence),
		   execute_last_step=VALUES(execute_last_step), description=VALUES(description), enabled=VALUES(enabled), updated_at=VALUES(updated_at)`,
		rule.Name, rule.AlertMatch, rule.ToolSequence, rule.ExecuteLastStep, rule.Description, rule.Enabled, rule.CreatedAt, rule.UpdatedAt)
	return err
}

func (s *SQLAlertActionRuleStore) Delete(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM alert_action_rules WHERE name = ?`, name)
	return err
}
