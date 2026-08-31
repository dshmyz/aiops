package store

import (
	"context"
	"testing"
	"time"
)

// TestSQLAlertStoreUpsertDuplicateKeepsOriginalID 锁定幽灵 UUID 修复：
// 同一身份重复 upsert 时必须返回 DB 中原始 ID（incident 成员归并与
// propagateResolve 反查都依赖它），而不是调用方新填的未持久化 UUID。
func TestSQLAlertStoreUpsertDuplicateKeepsOriginalID(t *testing.T) {
	t.Parallel()
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	ctx := context.Background()
	s := NewSQLAlertStore(db)

	now := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	first := Alert{
		ExternalID: "a1", Source: "grafana", Title: "CPU 高",
		Severity: "warning", Status: "firing", Domain: "kafka",
		ReceivedAt: now, UpdatedAt: now,
	}
	saved1, created, err := s.Upsert(ctx, first)
	if err != nil || !created {
		t.Fatalf("first upsert: created=%v err=%v", created, err)
	}

	// 重复推送：调用方照 Normalize 惯例填了全新 ID。
	dup := saved1
	dup.ID = "fresh-uuid-not-in-db"
	dup.Severity = "critical"
	saved2, created, err := s.Upsert(ctx, dup)
	if err != nil {
		t.Fatalf("dup upsert: %v", err)
	}
	if created {
		t.Error("dup upsert created = true, want false")
	}
	if saved2.ID != saved1.ID {
		t.Fatalf("dup upsert returned ID %q, want original %q", saved2.ID, saved1.ID)
	}
	// DB 行必须真的能按返回的 ID 取到，且级别已更新。
	got, err := s.Get(ctx, saved2.ID)
	if err != nil {
		t.Fatalf("Get by returned ID: %v", err)
	}
	if got.Severity != "critical" {
		t.Errorf("severity = %q, want critical", got.Severity)
	}
}

// TestSQLIncidentStoreLifecycle 覆盖 incident 归并的存储语义：
// find-open 窗口过滤、成员幂等、resolve 状态落库。
func TestSQLIncidentStoreLifecycle(t *testing.T) {
	t.Parallel()
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	ctx := context.Background()
	s := NewSQLIncidentStore(db) // 方言经探测，不依赖 driver 拼写

	now := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	key := IncidentKey{Domain: "kafka", ResourceType: "consumer_group", ResourceName: "orders"}

	created, err := s.UpsertIncident(ctx, AlertIncident{
		Status: "firing", Severity: "warning", Title: "积压",
		Domain: key.Domain, ResourceType: key.ResourceType, ResourceName: key.ResourceName,
		AlertCount: 1, FirstSeenAt: now, LastSeenAt: now,
	})
	if err != nil {
		t.Fatalf("create incident: %v", err)
	}
	if created.ID == "" || created.Status != "firing" {
		t.Fatalf("created = %+v", created)
	}

	// 窗口内可找到
	found, ok, err := s.FindOpenIncident(ctx, key, now.Add(-time.Minute))
	if err != nil || !ok {
		t.Fatalf("FindOpenIncident in window: ok=%v err=%v", ok, err)
	}
	if found.ID != created.ID {
		t.Errorf("found incident %q, want %q", found.ID, created.ID)
	}
	// 窗口外找不到
	_, ok, err = s.FindOpenIncident(ctx, key, now.Add(time.Minute))
	if err != nil || ok {
		t.Fatalf("FindOpenIncident outside window: ok=%v err=%v", ok, err)
	}

	// 成员幂等
	if err := s.AttachMember(ctx, created.ID, "alert-1"); err != nil {
		t.Fatalf("AttachMember: %v", err)
	}
	if err := s.AttachMember(ctx, created.ID, "alert-1"); err != nil {
		t.Fatalf("AttachMember dup: %v", err)
	}
	members, err := s.MemberAlertIDs(ctx, created.ID)
	if err != nil || len(members) != 1 || members[0] != "alert-1" {
		t.Fatalf("members = %v (err %v), want [alert-1]", members, err)
	}

	// 反查
	byAlert, ok, err := s.FindOpenIncidentByAlert(ctx, "alert-1")
	if err != nil || !ok || byAlert.ID != created.ID {
		t.Fatalf("FindOpenIncidentByAlert = %+v ok=%v err=%v", byAlert, ok, err)
	}

	// resolved 落库后 find-open 不再命中
	created.Status = "resolved"
	if _, err := s.UpsertIncident(ctx, created); err != nil {
		t.Fatalf("resolve incident: %v", err)
	}
	_, ok, err = s.FindOpenIncident(ctx, key, now.Add(-time.Minute))
	if err != nil || ok {
		t.Fatalf("FindOpenIncident after resolve: ok=%v err=%v, want false", ok, err)
	}
}

// TestSQLiteMigrationsCreateAlertIncidents 锁定 024 迁移与 SQLite bootstrap。
func TestSQLiteMigrationsCreateAlertIncidents(t *testing.T) {
	t.Parallel()
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	for _, table := range []string{"copilot_alert_incidents", "copilot_alert_incident_members"} {
		if !sqliteTableExists(t, db, table) {
			t.Errorf("table %q missing after sqlite bootstrap", table)
		}
	}
}
