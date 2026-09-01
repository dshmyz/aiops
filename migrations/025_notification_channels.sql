-- Notification outbound channels (Feishu / generic webhook).
-- Mirrored for SQLite in internal/store/db.go sqliteMigrations.
CREATE TABLE IF NOT EXISTS notification_channels (
  id VARCHAR(36) NOT NULL PRIMARY KEY,
  type VARCHAR(16) NOT NULL,
  name VARCHAR(255) NOT NULL,
  url VARCHAR(2048) NOT NULL,
  secret VARCHAR(512) NOT NULL DEFAULT '',
  enabled TINYINT(1) NOT NULL DEFAULT 1,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
