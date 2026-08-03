package store

import (
	"context"
	"testing"
	"time"
)

func TestSQLAlertStoreLifecycle(t *testing.T) {
	t.Parallel()
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	ctx := context.Background()
	s := NewSQLAlertStore(db)

	now := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	alert := Alert{
		ExternalID:   "a1",
		Source:       "grafana",
		Title:        "CPU 高",
		Description:  "node-01 CPU > 90%",
		Severity:     "critical",
		Status:       "firing",
		Environment:  "prod",
		Domain:       "kafka",
		ResourceType: "consumer_group",
		ResourceName: "orders",
		Labels:       map[string]string{"host": "node-01"},
		FiredAt:      now,
		Raw:          map[string]any{"external_id": "a1"},
		ReceivedAt:   now,
		UpdatedAt:    now,
	}

	// Upsert 首次插入 → created=true
	created, isCreated, err := s.Upsert(ctx, alert)
	if err != nil {
		t.Fatalf("Upsert first: %v", err)
	}
	if !isCreated {
		t.Error("Upsert first created = false, want true")
	}
	if created.ID == "" {
		t.Fatal("Upsert created ID is empty")
	}
	if created.ReceivedAt.IsZero() {
		t.Error("ReceivedAt should be set")
	}

	// Get
	fetched, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if fetched.Severity != "critical" || fetched.Status != "firing" {
		t.Errorf("Get = %+v", fetched)
	}
	if fetched.Labels["host"] != "node-01" {
		t.Errorf("Labels = %+v", fetched.Labels)
	}

	// Upsert 同身份再次推送 → created=false（update）
	alert.ID = created.ID
	alert.Title = "CPU 高（更新）"
	_, isCreated2, err := s.Upsert(ctx, alert)
	if err != nil {
		t.Fatalf("Upsert second: %v", err)
	}
	if isCreated2 {
		t.Error("Upsert second created = true, want false")
	}

	// Query 按 filter 过滤
	results, err := s.Query(ctx, AlertFilter{Status: "firing", Environment: "prod"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 || results[0].Title != "CPU 高（更新）" {
		t.Errorf("Query results = %+v", results)
	}

	// ListActive
	active, err := s.ListActive(ctx, "prod", 50)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(active) != 1 || active[0].ExternalID != "a1" {
		t.Errorf("ListActive = %+v", active)
	}

	// Resolve
	resolved, err := s.Resolve(ctx, "a1", "grafana")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Status != "resolved" || resolved.ResolvedAt == nil {
		t.Errorf("Resolve = %+v", resolved)
	}

	// Resolve 不存在的告警 → ErrNotFound
	if _, err := s.Resolve(ctx, "missing", "grafana"); err != ErrNotFound {
		t.Errorf("Resolve missing = %v, want ErrNotFound", err)
	}
}

func TestSQLAlertStoreQueryFilters(t *testing.T) {
	t.Parallel()
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	ctx := context.Background()
	s := NewSQLAlertStore(db)

	now := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	for _, a := range []Alert{
		{ExternalID: "a1", Source: "grafana", Title: "t1", Severity: "critical", Status: "firing", Environment: "prod", FiredAt: now, ReceivedAt: now, UpdatedAt: now},
		{ExternalID: "a2", Source: "grafana", Title: "t2", Severity: "info", Status: "resolved", Environment: "prod", FiredAt: now, ReceivedAt: now, UpdatedAt: now},
		{ExternalID: "a3", Source: "grafana", Title: "t3", Severity: "warning", Status: "firing", Environment: "staging", FiredAt: now, ReceivedAt: now, UpdatedAt: now},
	} {
		if _, _, err := s.Upsert(ctx, a); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}

	severity, err := s.Query(ctx, AlertFilter{Severity: "critical", Environment: "prod"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(severity) != 1 || severity[0].ExternalID != "a1" {
		t.Errorf("severity filter = %+v", severity)
	}

	limit, err := s.Query(ctx, AlertFilter{Status: "firing", Limit: 1})
	if err != nil {
		t.Fatalf("Query limit: %v", err)
	}
	if len(limit) != 1 {
		t.Errorf("limit filter len = %d, want 1", len(limit))
	}
}

func TestSQLiteMigrationsIncludeCopilotAlerts(t *testing.T) {
	found := false
	for _, stmt := range sqliteMigrations {
		if stmt == `CREATE TABLE IF NOT EXISTS copilot_alerts (
		id TEXT NOT NULL PRIMARY KEY,
		external_id TEXT NOT NULL,
		source TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL DEFAULT '',
		description TEXT NULL,
		severity TEXT NOT NULL DEFAULT 'warning',
		status TEXT NOT NULL DEFAULT 'firing',
		environment TEXT NOT NULL DEFAULT '',
		domain TEXT NOT NULL DEFAULT '',
		resource_type TEXT NOT NULL DEFAULT '',
		resource_name TEXT NOT NULL DEFAULT '',
		labels TEXT NOT NULL DEFAULT '{}',
		raw TEXT NULL,
		fired_at DATETIME NOT NULL,
		resolved_at DATETIME NULL,
		received_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)` {
			found = true
		}
	}
	if !found {
		t.Error("sqliteMigrations does not contain copilot_alerts table")
	}
	foundMysql := false
	for _, m := range migrations {
		if m == "012_alerts.sql" {
			foundMysql = true
		}
	}
	if !foundMysql {
		t.Error("migrations list does not contain 012_alerts.sql")
	}
}
