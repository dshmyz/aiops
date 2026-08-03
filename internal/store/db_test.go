package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	_ "github.com/mattn/go-sqlite3"
)

func TestSQLiteMigrationsCreateActionPlans(t *testing.T) {
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}

	if !sqliteTableExists(t, db, "action_plans") {
		t.Fatal("action_plans table was not created")
	}
}

func TestOpenWithDriverSupportsSQLiteForLocalRuntime(t *testing.T) {
	db, err := OpenWithDriver("sqlite", "file:runtime_local?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite runtime database: %v", err)
	}
	defer db.Close()

	if err := ApplyMigrationsForDriver("sqlite", db); err != nil {
		t.Fatalf("apply sqlite runtime migrations: %v", err)
	}
	if !sqliteTableExists(t, db, "action_plans") {
		t.Fatal("action_plans table was not created")
	}
}

// TestSQLiteMigrationsAddTraceIDToLegacyAuditEventsTable verifies that
// ApplySQLiteMigrations is idempotent on a database that predates the
// trace_id column: an old copilot_audit_events table created without
// trace_id must gain the column after migrations run, so local dev SQLite
// files created before the E-phase (trace correlation) work without
// requiring the operator to delete the database file.
//
// SQLite does not support `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`, so
// the migration logic must check PRAGMA table_info and conditionally
// ALTER. This test guards both the check and the column addition.
func TestSQLiteMigrationsAddTraceIDToLegacyAuditEventsTable(t *testing.T) {
	db := testSQLite(t)

	// 1) Create a legacy audit_events table WITHOUT trace_id, mirroring an
	//    older copilot-local.db file created before trace correlation.
	if _, err := db.Exec(`CREATE TABLE copilot_audit_events (
		id TEXT NOT NULL PRIMARY KEY,
		action_plan_id TEXT NULL,
		tool_execution_id TEXT NULL,
		request_id TEXT NOT NULL,
		actor_subject TEXT NOT NULL,
		tool_name TEXT NULL,
		action TEXT NOT NULL,
		decision TEXT NOT NULL DEFAULT '',
		metadata TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (action_plan_id) REFERENCES action_plans (id),
		FOREIGN KEY (tool_execution_id) REFERENCES tool_executions (id)
	)`); err != nil {
		t.Fatalf("create legacy audit_events table: %v", err)
	}

	// 2) Apply migrations on top of the legacy schema.
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations to legacy schema: %v", err)
	}

	// 3) trace_id column must now exist.
	if !sqliteColumnExists(t, db, "copilot_audit_events", "trace_id") {
		t.Fatal("trace_id column was not added to legacy copilot_audit_events table")
	}

	// 4) Re-running migrations must be a no-op (true idempotency).
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("re-apply sqlite migrations: %v", err)
	}
}

// TestSQLiteMigrationsAddConversationColumnsToLegacyTables verifies the same
// idempotent ALTER behavior for the conversation tables introduced in the
// multi-turn conversation feature: an old database with copilot_assistant_*
// tables missing parent_turn_id / response_type / response_payload must
// survive migration without manual intervention.
func TestSQLiteMigrationsAddConversationColumnsToLegacyTables(t *testing.T) {
	db := testSQLite(t)

	// Legacy conversations table without last_message_preview.
	if _, err := db.Exec(`CREATE TABLE copilot_assistant_conversations (
		id TEXT NOT NULL PRIMARY KEY,
		subject TEXT NOT NULL,
		title TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_active_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		archived_at DATETIME NULL
	)`); err != nil {
		t.Fatalf("create legacy conversations table: %v", err)
	}
	// Legacy turns table without parent_turn_id / response_type / response_payload.
	if _, err := db.Exec(`CREATE TABLE copilot_assistant_turns (
		id TEXT NOT NULL PRIMARY KEY,
		conversation_id TEXT NOT NULL,
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (conversation_id) REFERENCES copilot_assistant_conversations (id) ON DELETE CASCADE
	)`); err != nil {
		t.Fatalf("create legacy turns table: %v", err)
	}

	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations to legacy conversations: %v", err)
	}

	for _, col := range []string{"last_message_preview"} {
		if !sqliteColumnExists(t, db, "copilot_assistant_conversations", col) {
			t.Fatalf("column %q was not added to legacy conversations table", col)
		}
	}
	for _, col := range []string{"parent_turn_id", "response_type", "response_payload"} {
		if !sqliteColumnExists(t, db, "copilot_assistant_turns", col) {
			t.Fatalf("column %q was not added to legacy turns table", col)
		}
	}
}

func TestOpenWithDriverDefaultsToMySQLRequirement(t *testing.T) {
	if _, err := OpenWithDriver("", ""); err == nil {
		t.Fatal("empty default MySQL DSN was accepted")
	}
}

func TestMemoryStoreListPlansFiltersByStatus(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryActionPlanStore()
	pending := PlanRecord{ID: "pending-plan", ToolName: "topic.retention.set", InputJSON: []byte(`{"environment":"prod"}`), RiskLevel: "medium", Status: PlanPendingConfirmation, Version: 1, ExpiresAt: time.Now().Add(time.Minute)}
	confirmed := PlanRecord{ID: "confirmed-plan", ToolName: "topic.retention.set", InputJSON: []byte(`{"environment":"prod"}`), RiskLevel: "medium", Status: PlanConfirmed, Version: 2, ExpiresAt: time.Now().Add(time.Minute)}
	if err := repository.CreatePlan(ctx, pending, AuditEvent{ID: "audit-pending", PlanID: pending.ID, Action: "plan_created", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("create pending plan: %v", err)
	}
	if err := repository.CreatePlan(ctx, confirmed, AuditEvent{ID: "audit-confirmed", PlanID: confirmed.ID, Action: "plan_created", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("create confirmed plan: %v", err)
	}

	plans, err := repository.ListPlans(ctx, PlanFilter{Status: PlanPendingConfirmation})
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	if len(plans.Plans) != 1 || plans.Plans[0].ID != pending.ID {
		t.Fatalf("plans = %+v, want only pending plan", plans.Plans)
	}
	plans.Plans[0].InputJSON[0] = 'X'
	stored, err := repository.GetPlan(ctx, pending.ID)
	if err != nil {
		t.Fatalf("get stored plan: %v", err)
	}
	if string(stored.InputJSON) != `{"environment":"prod"}` {
		t.Fatalf("stored input mutated to %q", stored.InputJSON)
	}
}

func TestMemoryStoreListAuditFiltersByToolActionDecisionAndLimit(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryActionPlanStore()
	now := time.Now().UTC()
	events := []AuditEvent{
		{ID: "evt-1", PlanID: "plan-a", ToolName: "minio.bucket.capacity.read", Action: "plan_created", Decision: "permitted", CreatedAt: now.Add(-3 * time.Minute)},
		{ID: "evt-2", PlanID: "plan-b", ToolName: "kafka.topic.retention.set", Action: "plan_created", Decision: "permitted", CreatedAt: now.Add(-2 * time.Minute)},
		{ID: "evt-3", PlanID: "plan-b", ToolName: "kafka.topic.retention.set", Action: "plan_confirmed", Decision: "permitted", CreatedAt: now.Add(-1 * time.Minute)},
		{ID: "evt-4", PlanID: "plan-c", ToolName: "kafka.topic.retention.set", Action: "plan_confirmed", Decision: "denied", CreatedAt: now},
	}
	for _, event := range events {
		if err := repository.AppendAudit(ctx, event); err != nil {
			t.Fatalf("append audit %s: %v", event.ID, err)
		}
	}

	all, err := repository.ListAudit(ctx, AuditFilter{})
	if err != nil {
		t.Fatalf("ListAudit empty filter: %v", err)
	}
	if len(all.Events) != 4 {
		t.Fatalf("ListAudit returned %d events, want 4", len(all.Events))
	}

	kafkaOnly, err := repository.ListAudit(ctx, AuditFilter{ToolName: "kafka.topic.retention.set"})
	if err != nil {
		t.Fatalf("ListAudit tool filter: %v", err)
	}
	if len(kafkaOnly.Events) != 3 || kafkaOnly.Events[0].ID != "evt-2" {
		t.Fatalf("kafka events = %+v, want evt-2/3/4 in chronological order", kafkaOnly.Events)
	}

	confirmedPermitted, err := repository.ListAudit(ctx, AuditFilter{Action: "plan_confirmed", Decision: "permitted"})
	if err != nil {
		t.Fatalf("ListAudit action+decision filter: %v", err)
	}
	if len(confirmedPermitted.Events) != 1 || confirmedPermitted.Events[0].ID != "evt-3" {
		t.Fatalf("confirmed permitted = %+v, want evt-3", confirmedPermitted)
	}

	// Limit>0 without cursor: paginated mode, newest-first. Returns the newest
	// N events and a NextCursor pointing at the oldest event of this page.
	limited, err := repository.ListAudit(ctx, AuditFilter{Limit: 2})
	if err != nil {
		t.Fatalf("ListAudit limit: %v", err)
	}
	if len(limited.Events) != 2 || limited.Events[0].ID != "evt-4" || limited.Events[1].ID != "evt-3" {
		t.Fatalf("limited events = %+v, want newest two (evt-4/evt-3) descending", limited.Events)
	}
	if limited.NextCursor.ID != "evt-3" {
		t.Fatalf("NextCursor.ID = %q, want evt-3", limited.NextCursor.ID)
	}

	// Same cursor → next page older than evt-3.
	nextPage, err := repository.ListAudit(ctx, AuditFilter{
		Limit:           2,
		CursorCreatedAt: limited.NextCursor.CreatedAt,
		CursorID:        limited.NextCursor.ID,
	})
	if err != nil {
		t.Fatalf("ListAudit next page: %v", err)
	}
	if len(nextPage.Events) != 2 || nextPage.Events[0].ID != "evt-2" || nextPage.Events[1].ID != "evt-1" {
		t.Fatalf("next page events = %+v, want evt-2/evt-1", nextPage.Events)
	}
	if !nextPage.NextCursor.CreatedAt.IsZero() {
		t.Fatalf("NextCursor = %+v, want empty on last page", nextPage.NextCursor)
	}
}

// TestMemoryStoreListAuditFinalResultOnlyFilter 验证借鉴-4: 事件中心"最终结果
// 过滤"。FinalResultOnly=true 时隐藏 plan_rejected/execution_rejected 这类
// "未执行的驳回审批流"，让复盘聚焦在真正发生的结果上。
func TestMemoryStoreListAuditFinalResultOnlyFilter(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryActionPlanStore()
	now := time.Now().UTC()
	events := []AuditEvent{
		{ID: "evt-1", Action: "plan_created", Decision: "permitted", CreatedAt: now.Add(-5 * time.Minute)},
		{ID: "evt-2", Action: "plan_rejected", Decision: "denied", CreatedAt: now.Add(-4 * time.Minute)},
		{ID: "evt-3", Action: "plan_confirmed", Decision: "permitted", CreatedAt: now.Add(-3 * time.Minute)},
		{ID: "evt-4", Action: "execution_rejected", Decision: "denied", CreatedAt: now.Add(-2 * time.Minute)},
		{ID: "evt-5", Action: "execution_succeeded", Decision: "permitted", CreatedAt: now.Add(-1 * time.Minute)},
	}
	for _, event := range events {
		if err := repository.AppendAudit(ctx, event); err != nil {
			t.Fatalf("append audit %s: %v", event.ID, err)
		}
	}

	// 默认（FinalResultOnly=false）：返回全部 5 条，含驳回事件。
	all, err := repository.ListAudit(ctx, AuditFilter{})
	if err != nil {
		t.Fatalf("ListAudit default: %v", err)
	}
	if len(all.Events) != 5 {
		t.Fatalf("default returned %d events, want 5", len(all.Events))
	}

	// FinalResultOnly=true：隐藏 plan_rejected + execution_rejected，保留 3 条。
	finalOnly, err := repository.ListAudit(ctx, AuditFilter{FinalResultOnly: true})
	if err != nil {
		t.Fatalf("ListAudit final_result_only: %v", err)
	}
	if len(finalOnly.Events) != 3 {
		t.Fatalf("final_result_only returned %d events, want 3 (rejected hidden)", len(finalOnly.Events))
	}
	for _, evt := range finalOnly.Events {
		if evt.Action == "plan_rejected" || evt.Action == "execution_rejected" {
			t.Errorf("rejected event %s (action=%s) should be hidden by final_result_only", evt.ID, evt.Action)
		}
	}

	// FinalResultOnly 与 Limit 分页组合：仍按时间倒序分页，且过滤驳回事件。
	paged, err := repository.ListAudit(ctx, AuditFilter{FinalResultOnly: true, Limit: 2})
	if err != nil {
		t.Fatalf("ListAudit final_result_only+limit: %v", err)
	}
	if len(paged.Events) != 2 {
		t.Fatalf("paged returned %d events, want 2", len(paged.Events))
	}
	// newest-first: evt-5 (succeeded) 然后 evt-3 (confirmed)，跳过 evt-4 (rejected)
	if paged.Events[0].ID != "evt-5" || paged.Events[1].ID != "evt-3" {
		t.Fatalf("paged events = %+v, want evt-5/evt-3 (rejected evt-4 skipped)", paged.Events)
	}
}

func TestMemoryStoreListAuditFiltersByTimeRange(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryActionPlanStore()
	now := time.Now().UTC()
	events := []AuditEvent{
		{ID: "evt-1", PlanID: "plan-a", ToolName: "kafka.topic.retention.set", Action: "plan_created", Decision: "permitted", CreatedAt: now.Add(-3 * time.Hour)},
		{ID: "evt-2", PlanID: "plan-a", ToolName: "kafka.topic.retention.set", Action: "plan_confirmed", Decision: "permitted", CreatedAt: now.Add(-2 * time.Hour)},
		{ID: "evt-3", PlanID: "plan-b", ToolName: "kafka.topic.retention.set", Action: "plan_created", Decision: "permitted", CreatedAt: now.Add(-1 * time.Hour)},
		{ID: "evt-4", PlanID: "plan-b", ToolName: "kafka.topic.retention.set", Action: "plan_confirmed", Decision: "permitted", CreatedAt: now},
	}
	for _, event := range events {
		if err := repository.AppendAudit(ctx, event); err != nil {
			t.Fatalf("append audit %s: %v", event.ID, err)
		}
	}

	windowStart := now.Add(-150 * time.Minute) // include evt-2/evt-3/evt-4
	inWindow, err := repository.ListAudit(ctx, AuditFilter{CreatedAfter: windowStart})
	if err != nil {
		t.Fatalf("ListAudit after: %v", err)
	}
	if len(inWindow.Events) != 3 || inWindow.Events[0].ID != "evt-2" || inWindow.Events[2].ID != "evt-4" {
		t.Fatalf("after filter events = %+v, want evt-2/evt-3/evt-4", inWindow.Events)
	}

	windowEnd := now.Add(-90 * time.Minute) // include evt-1/evt-2
	beforeWindow, err := repository.ListAudit(ctx, AuditFilter{CreatedBefore: windowEnd})
	if err != nil {
		t.Fatalf("ListAudit before: %v", err)
	}
	if len(beforeWindow.Events) != 2 || beforeWindow.Events[0].ID != "evt-1" || beforeWindow.Events[1].ID != "evt-2" {
		t.Fatalf("before filter events = %+v, want evt-1/evt-2", beforeWindow.Events)
	}

	bounded, err := repository.ListAudit(ctx, AuditFilter{CreatedAfter: windowStart, CreatedBefore: now.Add(-30 * time.Minute)})
	if err != nil {
		t.Fatalf("ListAudit range: %v", err)
	}
	if len(bounded.Events) != 2 || bounded.Events[0].ID != "evt-2" || bounded.Events[1].ID != "evt-3" {
		t.Fatalf("range filter events = %+v, want evt-2/evt-3", bounded.Events)
	}
}

func TestSQLStoreListAuditFiltersByTool(t *testing.T) {
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	repository := NewSQLActionPlanStore(db)
	ctx := context.Background()
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	planX := PlanRecord{ID: "plan-x", RequestID: "req-1", CreatedBy: "admin", ToolName: "minio.bucket.capacity.read", InputJSON: []byte(`{"environment":"prod"}`), InputHash: "hash-x", RiskLevel: "low", Status: PlanConfirmed, Version: 1, ExpiresAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now}
	planY := PlanRecord{ID: "plan-y", RequestID: "req-2", CreatedBy: "operator", ToolName: "kafka.topic.retention.set", InputJSON: []byte(`{"environment":"prod"}`), InputHash: "hash-y", RiskLevel: "medium", Status: PlanConfirmed, Version: 1, ExpiresAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now}
	if err := repository.CreatePlan(ctx, planX, AuditEvent{ID: "sql-evt-1", PlanID: planX.ID, RequestID: planX.RequestID, Subject: planX.CreatedBy, ToolName: planX.ToolName, Action: "plan_created", Decision: "permitted", Metadata: map[string]any{"source": "test"}, CreatedAt: now}); err != nil {
		t.Fatalf("create first plan: %v", err)
	}
	if err := repository.CreatePlan(ctx, planY, AuditEvent{ID: "sql-evt-2", PlanID: planY.ID, RequestID: planY.RequestID, Subject: planY.CreatedBy, ToolName: planY.ToolName, Action: "plan_created", Decision: "permitted", Metadata: map[string]any{"source": "test"}, CreatedAt: now.Add(time.Minute)}); err != nil {
		t.Fatalf("create second plan: %v", err)
	}

	events, err := repository.ListAudit(ctx, AuditFilter{ToolName: "kafka.topic.retention.set"})
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(events.Events) != 1 || events.Events[0].ID != "sql-evt-2" {
		t.Fatalf("events = %+v, want sql-evt-2", events.Events)
	}
	if events.Events[0].Metadata["source"] != "test" {
		t.Fatalf("metadata = %+v, want source=test", events.Events[0].Metadata)
	}

	all, err := repository.ListAudit(ctx, AuditFilter{})
	if err != nil {
		t.Fatalf("ListAudit all: %v", err)
	}
	if len(all.Events) != 2 || all.Events[0].ID != "sql-evt-1" || all.Events[1].ID != "sql-evt-2" {
		t.Fatalf("all events = %+v, want chronological order", all.Events)
	}
}

// TestSQLStorePersistTraceIDRoundTrips verifies that the trace_id column
// survives an insert→list cycle through the SQL store. This is the persistence
// half of the audit-trace correlation: audit.Record captures the ID from the
// context, but the SQL store must actually keep it for the frontend to surface
// later.
func TestSQLStorePersistTraceIDRoundTrips(t *testing.T) {
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	repository := NewSQLActionPlanStore(db)
	ctx := context.Background()
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	plan := PlanRecord{
		ID:        "plan-trace",
		RequestID: "req-trace",
		CreatedBy: "admin",
		ToolName:  "kafka.topic.retention.set",
		InputJSON: []byte(`{"environment":"prod"}`),
		InputHash: "hash-trace",
		RiskLevel: "medium",
		Status:    PlanConfirmed,
		Version:   1,
		ExpiresAt: now.Add(time.Minute),
		CreatedAt: now,
		UpdatedAt: now,
	}
	traceID := "0123456789abcdef0123456789abcdef"
	if err := repository.CreatePlan(ctx, plan, AuditEvent{
		ID:        "audit-trace",
		PlanID:    plan.ID,
		RequestID: plan.RequestID,
		Subject:   plan.CreatedBy,
		ToolName:  plan.ToolName,
		Action:    "plan_created",
		Decision:  "permitted",
		TraceID:   traceID,
		Metadata:  map[string]any{},
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("create plan with trace: %v", err)
	}

	page, err := repository.ListAudit(ctx, AuditFilter{})
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(page.Events))
	}
	if page.Events[0].TraceID != traceID {
		t.Fatalf("TraceID = %q, want %q", page.Events[0].TraceID, traceID)
	}

	// Empty trace_id should also round-trip cleanly (e.g. scheduler events
	// recorded without a parent span — empty is the honest "no trace" signal).
	if err := repository.AppendAudit(ctx, AuditEvent{
		ID:        "audit-no-trace",
		RequestID: "req-trace",
		Subject:   "admin",
		ToolName:  "kafka.topic.retention.set",
		Action:    "plan_confirmed",
		Decision:  "permitted",
		Metadata:  map[string]any{},
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("append audit without trace: %v", err)
	}
	all, err := repository.ListAudit(ctx, AuditFilter{})
	if err != nil {
		t.Fatalf("ListAudit all: %v", err)
	}
	if len(all.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(all.Events))
	}
	var foundEmpty bool
	for _, event := range all.Events {
		if event.ID == "audit-no-trace" {
			foundEmpty = true
			if event.TraceID != "" {
				t.Fatalf("TraceID = %q, want empty for event without trace", event.TraceID)
			}
		}
	}
	if !foundEmpty {
		t.Fatalf("audit-no-trace event not found in %v", all.Events)
	}
}

func TestSQLStoreListAuditPaginatesByKeysetCursor(t *testing.T) {
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	repository := NewSQLActionPlanStore(db)
	ctx := context.Background()
	base := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	for i, id := range []string{"sql-page-1", "sql-page-2", "sql-page-3", "sql-page-4"} {
		minute := time.Duration(i) * time.Minute
		plan := PlanRecord{
			ID:        id,
			RequestID: "req-" + id,
			CreatedBy: "admin",
			ToolName:  "kafka.topic.retention.set",
			InputJSON: []byte(`{"environment":"prod"}`),
			InputHash: "hash-" + id,
			RiskLevel: "medium",
			Status:    PlanConfirmed,
			Version:   1,
			ExpiresAt: base.Add(time.Hour),
			CreatedAt: base.Add(minute),
			UpdatedAt: base.Add(minute),
		}
		if err := repository.CreatePlan(ctx, plan, AuditEvent{
			ID:        id,
			PlanID:    plan.ID,
			RequestID: plan.RequestID,
			Subject:   plan.CreatedBy,
			ToolName:  plan.ToolName,
			Action:    "plan_created",
			Decision:  "permitted",
			Metadata:  map[string]any{},
			CreatedAt: base.Add(minute),
		}); err != nil {
			t.Fatalf("create plan %s: %v", id, err)
		}
	}

	first, err := repository.ListAudit(ctx, AuditFilter{Limit: 2})
	if err != nil {
		t.Fatalf("ListAudit first page: %v", err)
	}
	if len(first.Events) != 2 || first.Events[0].ID != "sql-page-4" || first.Events[1].ID != "sql-page-3" {
		t.Fatalf("first page = %+v, want newest two descending", first.Events)
	}
	if first.NextCursor.ID != "sql-page-3" {
		t.Fatalf("first NextCursor.ID = %q, want sql-page-3", first.NextCursor.ID)
	}

	second, err := repository.ListAudit(ctx, AuditFilter{
		Limit:           2,
		CursorCreatedAt: first.NextCursor.CreatedAt,
		CursorID:        first.NextCursor.ID,
	})
	if err != nil {
		t.Fatalf("ListAudit second page: %v", err)
	}
	if len(second.Events) != 2 || second.Events[0].ID != "sql-page-2" || second.Events[1].ID != "sql-page-1" {
		t.Fatalf("second page = %+v, want sql-page-2/sql-page-1", second.Events)
	}
	if !second.NextCursor.CreatedAt.IsZero() {
		t.Fatalf("second NextCursor = %+v, want empty on last page", second.NextCursor)
	}
}

func TestSQLStoreListPlansFiltersByStatus(t *testing.T) {
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	repository := NewSQLActionPlanStore(db)
	ctx := context.Background()
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	pending := PlanRecord{ID: "pending-sql-plan", RequestID: "request-1", CreatedBy: "admin-1", ToolName: "topic.retention.set", InputJSON: []byte(`{"environment":"prod"}`), InputHash: "hash-1", RiskLevel: "medium", Status: PlanPendingConfirmation, Version: 1, ExpiresAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now}
	confirmed := PlanRecord{ID: "confirmed-sql-plan", RequestID: "request-2", CreatedBy: "admin-1", ToolName: "topic.retention.set", InputJSON: []byte(`{"environment":"prod"}`), InputHash: "hash-2", RiskLevel: "medium", Status: PlanConfirmed, Version: 2, ExpiresAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now}
	if err := repository.CreatePlan(ctx, pending, AuditEvent{ID: "audit-sql-pending", PlanID: pending.ID, RequestID: pending.RequestID, Subject: pending.CreatedBy, ToolName: pending.ToolName, Action: "plan_created", Decision: "permitted", CreatedAt: now}); err != nil {
		t.Fatalf("create pending plan: %v", err)
	}
	if err := repository.CreatePlan(ctx, confirmed, AuditEvent{ID: "audit-sql-confirmed", PlanID: confirmed.ID, RequestID: confirmed.RequestID, Subject: confirmed.CreatedBy, ToolName: confirmed.ToolName, Action: "plan_created", Decision: "permitted", CreatedAt: now}); err != nil {
		t.Fatalf("create confirmed plan: %v", err)
	}

	plans, err := repository.ListPlans(ctx, PlanFilter{Status: PlanPendingConfirmation})
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	if len(plans.Plans) != 1 || plans.Plans[0].ID != pending.ID {
		t.Fatalf("plans = %+v, want only pending SQL plan", plans.Plans)
	}
}

func TestSQLiteMigrationsCreateUniqueIdempotencyKey(t *testing.T) {
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO action_plans (id, tool_name, input_json, input_hash, risk_level, status)
		VALUES ('plan-for-idempotency-test', 'test.tool', '{}', 'hash', 'low', 'pending_confirmation')
	`); err != nil {
		t.Fatalf("create action plan: %v", err)
	}

	_, err := db.Exec(`
		INSERT INTO tool_executions (id, action_plan_id, idempotency_key, status)
		VALUES ('execution-one', 'plan-for-idempotency-test', 'repeatable-key', 'pending'), ('execution-two', 'plan-for-idempotency-test', 'repeatable-key', 'pending')
	`)
	if err == nil {
		t.Fatal("tool_executions accepted a duplicate idempotency key")
	}
}

func TestSQLiteStoreReusesExecutionAndKeepsAuditForeignKeyValid(t *testing.T) {
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	repository := NewSQLActionPlanStore(db)
	now := time.Date(2026, time.July, 21, 9, 0, 0, 0, time.UTC)
	plan := PlanRecord{
		ID:        "plan-reuse",
		RequestID: "req-reuse",
		CreatedBy: "operator-1",
		ToolName:  "middleware.kafka.topic_retention.set",
		InputJSON: []byte(`{"environment":"prod"}`),
		InputHash: "input-hash",
		RiskLevel: "L2",
		Status:    PlanConfirmed,
		Version:   1,
		ExpiresAt: now.Add(10 * time.Minute),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := repository.CreatePlan(context.Background(), plan, AuditEvent{
		ID:        "audit-plan",
		PlanID:    plan.ID,
		RequestID: plan.RequestID,
		Subject:   plan.CreatedBy,
		ToolName:  plan.ToolName,
		Action:    "plan_created",
		Decision:  "permitted",
		Metadata:  map[string]any{},
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("create plan: %v", err)
	}

	first, reused, err := repository.CreateExecutionIfAbsent(context.Background(), ExecutionRecord{
		ID:             "execution-one",
		ActionPlanID:   plan.ID,
		IdempotencyKey: "plan:plan-reuse:input-hash",
		Status:         "running",
		StartedAt:      &now,
		CreatedAt:      now,
	}, AuditEvent{
		ID:          "audit-execution-one",
		PlanID:      plan.ID,
		ExecutionID: "execution-one",
		RequestID:   plan.RequestID,
		Subject:     plan.CreatedBy,
		ToolName:    plan.ToolName,
		Action:      "execution_started",
		Decision:    "permitted",
		Metadata:    map[string]any{},
		CreatedAt:   now,
	})
	if err != nil || reused {
		t.Fatalf("first execution = %+v reused=%v err=%v, want new record", first, reused, err)
	}
	second, reused, err := repository.CreateExecutionIfAbsent(context.Background(), ExecutionRecord{
		ID:             "execution-two",
		ActionPlanID:   plan.ID,
		IdempotencyKey: first.IdempotencyKey,
		Status:         "running",
		StartedAt:      &now,
		CreatedAt:      now,
	}, AuditEvent{
		ID:          "audit-execution-two",
		PlanID:      plan.ID,
		ExecutionID: "execution-two",
		RequestID:   plan.RequestID,
		Subject:     plan.CreatedBy,
		ToolName:    plan.ToolName,
		Action:      "execution_started",
		Decision:    "permitted",
		Metadata:    map[string]any{},
		CreatedAt:   now,
	})
	if err != nil {
		t.Fatalf("reuse execution: %v", err)
	}
	if !reused || second.ID != first.ID {
		t.Fatalf("second execution = %+v reused=%v, want reused first execution %+v", second, reused, first)
	}
	var auditExecutionID string
	if err := db.QueryRow(`SELECT tool_execution_id FROM copilot_audit_events WHERE id = 'audit-execution-two'`).Scan(&auditExecutionID); err != nil {
		t.Fatalf("read reused audit: %v", err)
	}
	if auditExecutionID != first.ID {
		t.Fatalf("reused audit execution id = %q, want %q", auditExecutionID, first.ID)
	}
}

func TestMySQLMigrationsCreateActionPlans(t *testing.T) {
	db := testMySQL(t)
	if err := ApplyMigrations(db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	if !tableExists(t, db, "action_plans") {
		t.Fatal("action_plans table was not created")
	}
}

func TestMySQLMigrationsCreateUniqueIdempotencyKey(t *testing.T) {
	db := testMySQL(t)
	if err := ApplyMigrations(db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO action_plans (id, tool_name, input_json, input_hash, risk_level, status)
		VALUES ('plan-for-idempotency-test', 'test.tool', JSON_OBJECT(), 'hash', 'low', 'pending_confirmation')
	`); err != nil {
		t.Fatalf("create action plan: %v", err)
	}

	_, err := db.Exec(`
		INSERT INTO tool_executions (id, action_plan_id, idempotency_key, status)
		VALUES ('execution-one', 'plan-for-idempotency-test', 'repeatable-key', 'pending'), ('execution-two', 'plan-for-idempotency-test', 'repeatable-key', 'pending')
	`)
	if err == nil {
		t.Fatal("tool_executions accepted a duplicate idempotency key")
	}
}

func TestIntegrationDSNIsRequiredWhenRequested(t *testing.T) {
	t.Setenv("COPILOT_TEST_MYSQL_DSN", "")
	t.Setenv("COPILOT_REQUIRE_MYSQL", "1")

	_, err := integrationDSN()
	if err == nil {
		t.Fatal("integrationDSN accepted a missing MySQL DSN when MySQL is required")
	}
}

func integrationDSN() (string, error) {
	dsn := strings.TrimSpace(os.Getenv("COPILOT_TEST_MYSQL_DSN"))
	if dsn == "" && os.Getenv("COPILOT_REQUIRE_MYSQL") == "1" {
		return "", errors.New("COPILOT_TEST_MYSQL_DSN is required when COPILOT_REQUIRE_MYSQL=1")
	}
	return dsn, nil
}

func testMySQL(t *testing.T) *sql.DB {
	t.Helper()

	dsn, err := integrationDSN()
	if err != nil {
		t.Fatal(err)
	}
	if dsn == "" {
		t.Skip("set COPILOT_TEST_MYSQL_DSN to run MySQL migration integration tests")
	}

	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse COPILOT_TEST_MYSQL_DSN: %v", err)
	}

	databaseName := fmt.Sprintf("copilot_task1_test_%d", time.Now().UnixNano())
	adminConfig := *config
	adminConfig.DBName = ""
	adminDB, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatalf("open MySQL admin connection: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })

	if _, err := adminDB.Exec("CREATE DATABASE `" + databaseName + "`"); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminDB.Exec("DROP DATABASE IF EXISTS `" + databaseName + "`")
	})

	testConfig := *config
	testConfig.DBName = databaseName
	db, err := sql.Open("mysql", testConfig.FormatDSN())
	if err != nil {
		t.Fatalf("open MySQL test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return db
}

func tableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()

	var count int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?`,
		table,
	).Scan(&count)
	if err != nil {
		t.Fatalf("check table %q: %v", table, err)
	}

	return count == 1
}

func testSQLite(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", "file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable sqlite foreign keys: %v", err)
	}
	return db
}

func sqliteTableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()

	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count)
	if err != nil {
		t.Fatalf("check sqlite table %q: %v", table, err)
	}
	return count == 1
}

// sqliteColumnExists reports whether a column exists on a SQLite table.
// Used to verify ALTER TABLE migrations added the expected columns to
// legacy schemas without DROP/CREATE.
func sqliteColumnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		t.Fatalf("table_info %q: %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info row: %v", err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info: %v", err)
	}
	return false
}
