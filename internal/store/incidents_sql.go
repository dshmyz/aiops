package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SQLIncidentStore 是 IncidentStore 的 SQL 实现（SQLite 与 MySQL 双方言）。
// 表结构见 migrations/024_alert_incidents.sql 与 db.go 的 SQLite bootstrap。
type SQLIncidentStore struct {
	db     *sql.DB
	sqlite bool
}

// NewSQLIncidentStore 创建 SQL incident store。方言经 isSQLiteDriver 探测
// （与 SQLAlertStore 一致），不依赖调用方拼写 driver 名。
func NewSQLIncidentStore(db *sql.DB) *SQLIncidentStore {
	return &SQLIncidentStore{db: db, sqlite: isSQLiteDriver(db)}
}

const incidentColumns = `id, status, domain, resource_type, resource_name,
	severity, title, alert_count, first_seen_at, last_seen_at, updated_at`

func scanIncident(row interface{ Scan(...any) error }) (AlertIncident, error) {
	var inc AlertIncident
	err := row.Scan(&inc.ID, &inc.Status, &inc.Domain, &inc.ResourceType, &inc.ResourceName,
		&inc.Severity, &inc.Title, &inc.AlertCount, &inc.FirstSeenAt, &inc.LastSeenAt, &inc.UpdatedAt)
	return inc, err
}

// UpsertIncident 按 ID 全量写入（insert or replace 语义，时间字段以入参为准）。
func (s *SQLIncidentStore) UpsertIncident(ctx context.Context, inc AlertIncident) (AlertIncident, error) {
	if inc.ID == "" {
		inc.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if inc.UpdatedAt.IsZero() {
		inc.UpdatedAt = now
	}
	if inc.FirstSeenAt.IsZero() {
		inc.FirstSeenAt = now
	}
	if inc.LastSeenAt.IsZero() {
		inc.LastSeenAt = now
	}
	if inc.AlertCount <= 0 {
		inc.AlertCount = 1
	}
	if inc.Status == "" {
		inc.Status = "firing"
	}
	if inc.Severity == "" {
		inc.Severity = "info"
	}
	var err error
	if s.sqlite {
		_, err = s.db.ExecContext(ctx, `INSERT INTO copilot_alert_incidents
			(id, status, domain, resource_type, resource_name, severity, title,
			 alert_count, first_seen_at, last_seen_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				status = excluded.status, severity = excluded.severity, title = excluded.title,
				alert_count = excluded.alert_count, last_seen_at = excluded.last_seen_at,
				updated_at = excluded.updated_at`,
			inc.ID, inc.Status, inc.Domain, inc.ResourceType, inc.ResourceName,
			inc.Severity, inc.Title, inc.AlertCount, inc.FirstSeenAt, inc.LastSeenAt, inc.UpdatedAt)
	} else {
		_, err = s.db.ExecContext(ctx, `INSERT INTO copilot_alert_incidents
			(id, status, domain, resource_type, resource_name, severity, title,
			 alert_count, first_seen_at, last_seen_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				status = VALUES(status), severity = VALUES(severity), title = VALUES(title),
				alert_count = VALUES(alert_count), last_seen_at = VALUES(last_seen_at),
				updated_at = VALUES(updated_at)`,
			inc.ID, inc.Status, inc.Domain, inc.ResourceType, inc.ResourceName,
			inc.Severity, inc.Title, inc.AlertCount, inc.FirstSeenAt, inc.LastSeenAt, inc.UpdatedAt)
	}
	if err != nil {
		return AlertIncident{}, fmt.Errorf("upsert incident: %w", err)
	}
	// 所有列值均来自 Go 侧归一化后的入参（默认值已在上方补齐），DB 不做
	// 额外变换，直接返回入参，省去热路径上的一次读回往返。
	return inc, nil
}

func (s *SQLIncidentStore) GetIncident(ctx context.Context, id string) (AlertIncident, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+incidentColumns+` FROM copilot_alert_incidents WHERE id = ?`, id)
	inc, err := scanIncident(row)
	if err == sql.ErrNoRows {
		return AlertIncident{}, ErrNotFound
	}
	return inc, err
}

func (s *SQLIncidentStore) ListIncidents(ctx context.Context, f IncidentFilter) ([]AlertIncident, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT ` + incidentColumns + ` FROM copilot_alert_incidents WHERE 1=1`
	var args []any
	if f.Status != "" {
		query += ` AND status = ?`
		args = append(args, f.Status)
	}
	if f.Domain != "" {
		query += ` AND domain = ?`
		args = append(args, f.Domain)
	}
	query += ` ORDER BY last_seen_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	defer rows.Close()
	out := []AlertIncident{}
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			return nil, fmt.Errorf("scan incident: %w", err)
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}

func (s *SQLIncidentStore) FindOpenIncident(ctx context.Context, key IncidentKey, windowStart time.Time) (AlertIncident, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+incidentColumns+` FROM copilot_alert_incidents
		 WHERE status = 'firing' AND domain = ? AND resource_type = ? AND resource_name = ?
		   AND last_seen_at >= ?
		 ORDER BY last_seen_at DESC LIMIT 1`,
		key.Domain, key.ResourceType, key.ResourceName, windowStart)
	inc, err := scanIncident(row)
	if err == sql.ErrNoRows {
		return AlertIncident{}, false, nil
	}
	if err != nil {
		return AlertIncident{}, false, fmt.Errorf("find open incident: %w", err)
	}
	return inc, true, nil
}

func (s *SQLIncidentStore) FindOpenIncidentByAlert(ctx context.Context, alertID string) (AlertIncident, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT i.id, i.status, i.domain, i.resource_type, i.resource_name,
			i.severity, i.title, i.alert_count, i.first_seen_at, i.last_seen_at, i.updated_at
		 FROM copilot_alert_incidents i
		 JOIN copilot_alert_incident_members m ON m.incident_id = i.id
		 WHERE m.alert_id = ? AND i.status = 'firing' LIMIT 1`, alertID)
	inc, err := scanIncident(row)
	if err == sql.ErrNoRows {
		return AlertIncident{}, false, nil
	}
	if err != nil {
		return AlertIncident{}, false, fmt.Errorf("find incident by alert: %w", err)
	}
	return inc, true, nil
}

// AttachMember 幂等建立成员关系（主键去重）。
func (s *SQLIncidentStore) AttachMember(ctx context.Context, incidentID, alertID string) error {
	if s.sqlite {
		_, err := s.db.ExecContext(ctx, `INSERT INTO copilot_alert_incident_members
			(incident_id, alert_id) VALUES (?, ?)
			ON CONFLICT(incident_id, alert_id) DO NOTHING`, incidentID, alertID)
		if err != nil {
			return fmt.Errorf("attach member: %w", err)
		}
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT IGNORE INTO copilot_alert_incident_members
		(incident_id, alert_id) VALUES (?, ?)`, incidentID, alertID)
	if err != nil {
		return fmt.Errorf("attach member: %w", err)
	}
	return nil
}

func (s *SQLIncidentStore) MemberAlertIDs(ctx context.Context, incidentID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT alert_id FROM copilot_alert_incident_members
		 WHERE incident_id = ? ORDER BY attached_at ASC, alert_id ASC`, incidentID)
	if err != nil {
		return nil, fmt.Errorf("list incident members: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
