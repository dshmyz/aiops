package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// NotificationChannelRecord 是一条通知外发通道的 DB 记录。
// Secret 只写不回：查询/列表永远不返回明文（见 httpapi 层掩码）。
type NotificationChannelRecord struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"` // feishu | webhook
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Secret    string    `json:"-"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NotificationChannelStore CRUD 操作通知外发通道。
type NotificationChannelStore interface {
	List(ctx context.Context) ([]NotificationChannelRecord, error)
	Get(ctx context.Context, id string) (NotificationChannelRecord, error)
	Upsert(ctx context.Context, ch NotificationChannelRecord) error
	Delete(ctx context.Context, id string) error
}

// SQLNotificationChannelStore 实现 NotificationChannelStore（MySQL/SQLite）。
type SQLNotificationChannelStore struct {
	db *sql.DB
}

// NewSQLNotificationChannelStore 创建 SQL 通知通道 store。
func NewSQLNotificationChannelStore(db *sql.DB) *SQLNotificationChannelStore {
	return &SQLNotificationChannelStore{db: db}
}

func (s *SQLNotificationChannelStore) List(ctx context.Context) ([]NotificationChannelRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, type, name, url, secret, enabled, created_at, updated_at
		 FROM notification_channels ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list notification channels: %w", err)
	}
	defer rows.Close()

	var channels []NotificationChannelRecord
	for rows.Next() {
		var c NotificationChannelRecord
		if err := rows.Scan(&c.ID, &c.Type, &c.Name, &c.URL, &c.Secret, &c.Enabled, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan notification channel: %w", err)
		}
		channels = append(channels, c)
	}
	return channels, rows.Err()
}

func (s *SQLNotificationChannelStore) Get(ctx context.Context, id string) (NotificationChannelRecord, error) {
	var c NotificationChannelRecord
	err := s.db.QueryRowContext(ctx,
		`SELECT id, type, name, url, secret, enabled, created_at, updated_at
		 FROM notification_channels WHERE id = ?`, id).Scan(
		&c.ID, &c.Type, &c.Name, &c.URL, &c.Secret, &c.Enabled, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return NotificationChannelRecord{}, ErrNotFound
	}
	if err != nil {
		return NotificationChannelRecord{}, fmt.Errorf("get notification channel: %w", err)
	}
	return c, nil
}

func (s *SQLNotificationChannelStore) Upsert(ctx context.Context, ch NotificationChannelRecord) error {
	now := time.Now().UTC()
	if ch.CreatedAt.IsZero() {
		ch.CreatedAt = now
	}
	ch.UpdatedAt = now
	if ch.ID == "" {
		ch.ID = uuid.NewString()
	}
	// 空 secret 不覆盖已有值（前端编辑时 secret 不回显，保持原值）。
	if ch.Secret == "" {
		if existing, err := s.Get(ctx, ch.ID); err == nil {
			ch.Secret = existing.Secret
		}
	}

	if isSQLiteDriver(s.db) {
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO notification_channels (id, type, name, url, secret, enabled, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO UPDATE SET type=excluded.type, name=excluded.name, url=excluded.url,
			   secret=excluded.secret, enabled=excluded.enabled, updated_at=excluded.updated_at`,
			ch.ID, ch.Type, ch.Name, ch.URL, ch.Secret, ch.Enabled, ch.CreatedAt, ch.UpdatedAt)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notification_channels (id, type, name, url, secret, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE type=VALUES(type), name=VALUES(name), url=VALUES(url),
		   secret=VALUES(secret), enabled=VALUES(enabled), updated_at=VALUES(updated_at)`,
		ch.ID, ch.Type, ch.Name, ch.URL, ch.Secret, ch.Enabled, ch.CreatedAt, ch.UpdatedAt)
	return err
}

func (s *SQLNotificationChannelStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM notification_channels WHERE id = ?`, id)
	return err
}
