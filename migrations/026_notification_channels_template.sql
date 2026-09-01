-- Per-channel custom request body template for outbound webhooks (Go text/template).
-- Mirrored for SQLite in internal/store/db.go sqliteMigrations.
ALTER TABLE notification_channels ADD COLUMN template TEXT NOT NULL DEFAULT '';
