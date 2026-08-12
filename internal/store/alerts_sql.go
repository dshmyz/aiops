package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var _ AlertStore = (*SQLAlertStore)(nil)

// SQLAlertStore 在 MySQL/SQLite 上持久化归一化告警。
type SQLAlertStore struct {
	db     *sql.DB
	sqlite bool
}

// NewSQLAlertStore 创建一个 SQL 告警 store。
func NewSQLAlertStore(db *sql.DB) *SQLAlertStore {
	return &SQLAlertStore{db: db, sqlite: isSQLiteDriver(db)}
}

// isSQLiteDriver 通过探测 SQLite 特有的 PRAGMA 判断当前驱动是否为 SQLite。
// MySQL 执行 `PRAGMA` 会报错，据此区分两种 upsert 方言。
func isSQLiteDriver(db *sql.DB) bool {
	row := db.QueryRow("PRAGMA journal_mode")
	var mode string
	if err := row.Scan(&mode); err != nil {
		return false
	}
	return true
}

func (s *SQLAlertStore) Upsert(ctx context.Context, a Alert) (Alert, bool, error) {
	labelsJSON, err := json.Marshal(a.Labels)
	if err != nil {
		return Alert{}, false, fmt.Errorf("marshal labels: %w", err)
	}
	var rawJSON []byte
	if a.Raw != nil {
		rawJSON, err = json.Marshal(a.Raw)
		if err != nil {
			return Alert{}, false, fmt.Errorf("marshal raw: %w", err)
		}
	}
	now := time.Now().UTC()
	if a.UpdatedAt.IsZero() {
		a.UpdatedAt = now
	}
	if a.ReceivedAt.IsZero() {
		a.ReceivedAt = now
	}
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	var resolvedAt any
	if a.ResolvedAt != nil {
		resolvedAt = a.ResolvedAt
	}

	// 先探测身份是否已存在：SQLite 的 ON CONFLICT DO UPDATE 对 insert 和
	// update 都返回 RowsAffected=1，无法据此区分 created；MySQL 则 insert=1、
	// update=2。这里统一用存在性探测，跨驱动行为一致。
	var existingID string
	err = s.db.QueryRowContext(ctx, `SELECT id FROM copilot_alerts WHERE source = ? AND external_id = ?`,
		a.Source, a.ExternalID).Scan(&existingID)
	existed := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Alert{}, false, fmt.Errorf("probe alert identity: %w", err)
	}

	if s.sqlite {
		_, err = s.db.ExecContext(ctx, `INSERT INTO copilot_alerts
			(id, external_id, source, title, description, severity, status, environment,
			 domain, resource_type, resource_name, labels, raw, fired_at, resolved_at,
			 received_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(source, external_id) DO UPDATE SET
				title = excluded.title,
				description = excluded.description,
				severity = excluded.severity,
				status = excluded.status,
				environment = excluded.environment,
				domain = excluded.domain,
				resource_type = excluded.resource_type,
				resource_name = excluded.resource_name,
				labels = excluded.labels,
				raw = excluded.raw,
				fired_at = excluded.fired_at,
				resolved_at = excluded.resolved_at,
				updated_at = excluded.updated_at`,
			a.ID, a.ExternalID, a.Source, a.Title, nullableString(a.Description), a.Severity,
			a.Status, a.Environment, a.Domain, a.ResourceType, a.ResourceName,
			string(labelsJSON), nullableJSON(rawJSON), a.FiredAt, resolvedAt,
			a.ReceivedAt, a.UpdatedAt)
	} else {
		_, err = s.db.ExecContext(ctx, `INSERT INTO copilot_alerts
			(id, external_id, source, title, description, severity, status, environment,
			 domain, resource_type, resource_name, labels, raw, fired_at, resolved_at,
			 received_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				title = VALUES(title),
				description = VALUES(description),
				severity = VALUES(severity),
				status = VALUES(status),
				environment = VALUES(environment),
				domain = VALUES(domain),
				resource_type = VALUES(resource_type),
				resource_name = VALUES(resource_name),
				labels = VALUES(labels),
				raw = VALUES(raw),
				fired_at = VALUES(fired_at),
				resolved_at = VALUES(resolved_at),
				updated_at = VALUES(updated_at)`,
			a.ID, a.ExternalID, a.Source, a.Title, nullableString(a.Description), a.Severity,
			a.Status, a.Environment, a.Domain, a.ResourceType, a.ResourceName,
			string(labelsJSON), nullableJSON(rawJSON), a.FiredAt, resolvedAt,
			a.ReceivedAt, a.UpdatedAt)
	}
	if err != nil {
		return Alert{}, false, fmt.Errorf("upsert alert: %w", err)
	}
	return a, !existed, nil
}

func (s *SQLAlertStore) Get(ctx context.Context, id string) (Alert, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, external_id, source, title, description,
		severity, status, environment, domain, resource_type, resource_name,
		labels, raw, fired_at, resolved_at, received_at, updated_at
		FROM copilot_alerts WHERE id = ?`, id)
	a, err := scanAlert(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Alert{}, ErrNotFound
		}
		return Alert{}, fmt.Errorf("get alert: %w", err)
	}
	return a, nil
}

func (s *SQLAlertStore) UpdateDescription(ctx context.Context, id, description string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE copilot_alerts SET description = ?, updated_at = ? WHERE id = ?`,
		description, time.Now().UTC(), id)
	return err
}

func (s *SQLAlertStore) Query(ctx context.Context, f AlertFilter) ([]Alert, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultAlertLimit
	}
	if limit > maxAlertLimit {
		limit = maxAlertLimit
	}
	where := make([]string, 0, 4)
	args := make([]any, 0, 4)
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	if f.Severity != "" {
		where = append(where, "severity = ?")
		args = append(args, f.Severity)
	}
	if f.Environment != "" {
		where = append(where, "environment = ?")
		args = append(args, f.Environment)
	}
	if f.Domain != "" {
		where = append(where, "domain = ?")
		args = append(args, f.Domain)
	}
	query := `SELECT id, external_id, source, title, description,
		severity, status, environment, domain, resource_type, resource_name,
		labels, raw, fired_at, resolved_at, received_at, updated_at
		FROM copilot_alerts`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY updated_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query alerts: %w", err)
	}
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, fmt.Errorf("scan alert: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *SQLAlertStore) ListActive(ctx context.Context, environment string, limit int) ([]Alert, error) {
	if limit <= 0 {
		limit = defaultAlertLimit
	}
	if limit > maxAlertLimit {
		limit = maxAlertLimit
	}
	args := []any{"firing"}
	where := "status = ?"
	if environment != "" {
		where += " AND environment = ?"
		args = append(args, environment)
	}
	query := `SELECT id, external_id, source, title, description,
		severity, status, environment, domain, resource_type, resource_name,
		labels, raw, fired_at, resolved_at, received_at, updated_at
		FROM copilot_alerts WHERE ` + where + ` ORDER BY severity DESC, updated_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list active alerts: %w", err)
	}
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, fmt.Errorf("scan alert: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *SQLAlertStore) Resolve(ctx context.Context, externalID, source string) (Alert, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE copilot_alerts
		SET status = 'resolved', resolved_at = COALESCE(resolved_at, ?), updated_at = ?
		WHERE source = ? AND external_id = ? AND status != 'resolved'`,
		now, now, source, externalID)
	if err != nil {
		return Alert{}, fmt.Errorf("resolve alert: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Alert{}, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return Alert{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT id, external_id, source, title, description,
		severity, status, environment, domain, resource_type, resource_name,
		labels, raw, fired_at, resolved_at, received_at, updated_at
		FROM copilot_alerts WHERE source = ? AND external_id = ?`, source, externalID)
	return scanAlert(row)
}

type alertScanner interface {
	Scan(dest ...any) error
}

func scanAlert(row alertScanner) (Alert, error) {
	var a Alert
	var labelsJSON, rawJSON sql.NullString
	var description, resolvedAt sql.NullString
	err := row.Scan(
		&a.ID, &a.ExternalID, &a.Source, &a.Title, &description,
		&a.Severity, &a.Status, &a.Environment, &a.Domain, &a.ResourceType, &a.ResourceName,
		&labelsJSON, &rawJSON, &a.FiredAt, &resolvedAt, &a.ReceivedAt, &a.UpdatedAt,
	)
	if err != nil {
		return Alert{}, err
	}
	a.Description = description.String
	if resolvedAt.Valid {
		if parsed, perr := time.Parse(time.RFC3339Nano, resolvedAt.String); perr == nil {
			a.ResolvedAt = &parsed
		}
	}
	if labelsJSON.Valid && labelsJSON.String != "" {
		if err := json.Unmarshal([]byte(labelsJSON.String), &a.Labels); err != nil {
			return Alert{}, fmt.Errorf("unmarshal labels: %w", err)
		}
	}
	if rawJSON.Valid && rawJSON.String != "" {
		if err := json.Unmarshal([]byte(rawJSON.String), &a.Raw); err != nil {
			return Alert{}, fmt.Errorf("unmarshal raw: %w", err)
		}
	}
	return a, nil
}
