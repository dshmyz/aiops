package store

import (
	"context"
	"errors"
	"testing"
)

// TestSQLNotificationChannelStoreLifecycle 覆盖通知通道 CRUD：
// 自动 ID、空 secret 保留旧值、删除后 ErrNotFound。
func TestSQLNotificationChannelStoreLifecycle(t *testing.T) {
	t.Parallel()
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	ctx := context.Background()
	s := NewSQLNotificationChannelStore(db)

	rec := NotificationChannelRecord{
		Type: "webhook", Name: "内网网关",
		URL: "https://ops.local/hook", Secret: "s3cret", Enabled: true,
	}
	if err := s.Upsert(ctx, rec); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	list, err := s.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v (err %v), want 1", list, err)
	}
	got := list[0]
	if got.ID == "" {
		t.Fatal("upsert should assign id")
	}
	if got.Name != "内网网关" || got.Secret != "s3cret" || !got.Enabled {
		t.Fatalf("got = %+v", got)
	}

	fetched, err := s.Get(ctx, got.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if fetched.ID != got.ID {
		t.Fatalf("get returned %q, want %q", fetched.ID, got.ID)
	}

	// 空 secret 不覆盖已有值（编辑表单不回显 secret）。
	upd := got
	upd.Secret = ""
	upd.Name = "网关改名"
	if err := s.Upsert(ctx, upd); err != nil {
		t.Fatalf("update: %v", err)
	}
	after, err := s.Get(ctx, got.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if after.Secret != "s3cret" {
		t.Errorf("secret = %q, want preserved s3cret", after.Secret)
	}
	if after.Name != "网关改名" {
		t.Errorf("name = %q, want 网关改名", after.Name)
	}

	if err := s.Delete(ctx, got.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Get(ctx, got.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("get after delete = %v, want ErrNotFound", err)
	}
}

// TestSQLiteMigrationsCreateNotificationChannels 锁定 025 迁移与 SQLite bootstrap。
func TestSQLiteMigrationsCreateNotificationChannels(t *testing.T) {
	t.Parallel()
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	if !sqliteTableExists(t, db, "notification_channels") {
		t.Error("table notification_channels missing after sqlite bootstrap")
	}
}
