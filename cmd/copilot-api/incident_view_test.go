package main

import (
	"context"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/alert"
	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/scheduler"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// noopReadRunner 是 incident_view 测试里给 scheduler 的占位读 runner；
// 测试只用到 List/ListRuns，不触发 reads 执行。
type noopReadRunner struct{}

func (noopReadRunner) Read(context.Context, tools.Tool, map[string]any) (map[string]any, error) {
	return map[string]any{}, nil
}

// buildIncidentHarness 用内存 store 组装 incidentViewReadRunner 所需的全部数据源。
func buildIncidentHarness(t *testing.T, now time.Time) (incidentViewReadRunner, store.ActionPlanStore, *store.MemoryAlertStore, *store.MemoryScheduledTaskStore) {
	t.Helper()

	repo := store.NewMemoryActionPlanStore()
	auditSvc := audit.NewService(repo)
	alertStore := store.NewMemoryAlertStore()
	alertSvc := alert.NewService(alertStore)
	schedStore := store.NewMemoryScheduledTaskStore()
	readService := execution.NewReadOnlyService(noopReadRunner{}, auditSvc)
	schedSvc := scheduler.NewService(schedStore, readService, auditSvc, func() time.Time { return now })
	runbookStore := store.NewMemoryRunbookStore()

	runner := incidentViewReadRunner{
		alerts:       alertSvc,
		audit:        auditSvc,
		plans:        repo,
		schedules:    schedSvc,
		runbooks:     runbookStore,
		capabilities: []capabilities.Capability{
			{Name: "minio.bucket.capacity.read", Domain: "minio", ResourceType: "bucket", Operation: tools.Read, InputSchema: map[string]capabilities.InputField{"cluster": {Type: "string"}, "bucket": {Type: "string"}}},
			{Name: "minio.bucket.retention.set", Domain: "minio", ResourceType: "bucket", Operation: tools.Write},
		},
	}
	return runner, repo, alertStore, schedStore
}

func seedPlan(t *testing.T, repo store.ActionPlanStore, id, toolName, inputJSON string, at time.Time) string {
	t.Helper()
	event := store.AuditEvent{
		ID:        "audit-" + id,
		PlanID:    id,
		ToolName:  toolName,
		Action:    "plan_created",
		Decision:  "apply",
		CreatedAt: at,
	}
	if err := repo.CreatePlan(context.Background(), store.PlanRecord{
		ID:        id,
		RequestID: "req-" + id,
		CreatedBy: "system:test",
		ToolName:  toolName,
		InputJSON: []byte(inputJSON),
		InputHash: "h",
		RiskLevel: "medium",
		Status:    store.PlanConfirmed,
		CreatedAt: at,
		UpdatedAt: at,
	}, event); err != nil {
		t.Fatalf("seed plan %s: %v", id, err)
	}
	return event.ID
}

func TestIncidentViewJoinsAlertAuditRunsRunbook(t *testing.T) {
	now := time.Now().UTC()
	runner, repo, alertStore, schedStore := buildIncidentHarness(t, now)

	// Leg A 锚点：minio/archive 的活动告警。
	createdAlert, _, err := alertStore.Upsert(context.Background(), store.Alert{
		ID:           "alert-1", // 内存 store 会忽略并生成真实 ID
		Source:       "prometheus",
		ExternalID:   "ext-1",
		Title:        "bucket capacity over 85%",
		Severity:     "critical",
		Domain:       "minio",
		ResourceType: "bucket",
		ResourceName: "archive",
		Environment:  "prod",
		Status:       "firing",
		FiredAt:      now.Add(-30 * time.Minute),
		ReceivedAt:   now.Add(-30 * time.Minute),
		UpdatedAt:    now.Add(-30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("seed alert: %v", err)
	}

	// 相关审计（写）：同域同资源 archive，PlanID 指向含 "archive" 的 plan。
	matchAuditID := seedPlan(t, repo, "plan-archive", "minio.bucket.retention.set",
		`{"environment":"prod","bucket":"archive"}`, now.Add(-20*time.Minute))

	// 同域异资源：完整时间窗内、但 input 是 "other"，应被资源确认排除。
	seedPlan(t, repo, "plan-other", "minio.bucket.retention.set",
		`{"environment":"prod","bucket":"other"}`, now.Add(-20*time.Minute))

	// 相关定时巡检 run：audit_event_id 桥接到 matchAuditID。
	task, err := schedStore.CreateTask(context.Background(), store.ScheduledTask{
		ID:             "task-1",
		Name:           "minio bucket 巡检",
		CapabilityName: "minio.bucket.capacity.read",
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if _, err := schedStore.AppendRun(context.Background(), store.ScheduledTaskRun{
		ID:           "run-1",
		TaskID:       task.ID,
		StartedAt:    now.Add(-20 * time.Minute),
		FinishedAt:   now.Add(-19 * time.Minute),
		Status:       "succeeded",
		AuditEventID: matchAuditID,
	}); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	// 启用的匹配 runbook（intent_pattern 含 minio + bucket）。
	if _, err := runner.runbooks.(*store.MemoryRunbookStore).CreateRunbook(context.Background(), store.Runbook{
		ID:            "rb-1",
		Slug:          "minio-bucket-capacity",
		Name:          "MinIO 桶容量处置",
		IntentPattern: []string{"minio", "bucket", "capacity"},
		ToolSequence:  []string{"minio.bucket.capacity.read"},
		RiskLevel:     "medium",
		IsEnabled:     true,
	}); err != nil {
		t.Fatalf("seed runbook: %v", err)
	}

	out, err := runner.Read(context.Background(), tools.Tool{Name: tools.IncidentView}, map[string]any{
		"domain":        "minio",
		"resource_type": "bucket",
		"resource_name": "archive",
		"environment":   "prod",
	})
	if err != nil {
		t.Fatalf("incident.view: %v", err)
	}

	if out["incident_id"] != createdAlert.ID {
		t.Fatalf("incident_id = %v, want %v", out["incident_id"], createdAlert.ID)
	}
	counts := out["counts"].(map[string]any)
	if got := counts["audit"]; got != 1 {
		t.Fatalf("audit count = %v, want 1 (other-resource excluded)", got)
	}
	if got := counts["recent_writes"]; got != 1 {
		t.Fatalf("recent_writes count = %v, want 1 (retention.set)", got)
	}
	if got := counts["scheduled_runs"]; got != 1 {
		t.Fatalf("scheduled_runs count = %v, want 1 (audit_event_id bridge)", got)
	}
	if got := counts["runbooks"]; got != 1 {
		t.Fatalf("runbooks count = %v, want 1", got)
	}
	// 只读探测建议：同域同资源类型的 read 能力应被列出，且未执行（仅静态建议）。
	probes := out["probes"].([]map[string]any)
	if len(probes) != 1 || probes[0]["tool_name"] != "minio.bucket.capacity.read" {
		t.Fatalf("probes = %+v, want minio.bucket.capacity.read", probes)
	}

	// 相关审计 timeline 应只含 archive（plan-other 被排除）。
	auditEvents := out["timeline"].([]map[string]any)
	if len(auditEvents) != 1 {
		t.Fatalf("timeline = %+v, want single archive event", auditEvents)
	}
	if auditEvents[0]["action_plan_id"] != "plan-archive" {
		t.Fatalf("timeline[0].action_plan_id = %v, want plan-archive", auditEvents[0]["action_plan_id"])
	}

	// runbook 带 confidence，且 resource_type 命中给高置信。
	runbooks := out["runbooks"].([]map[string]any)
	if runbooks[0]["confidence"] != float64(0.9) {
		t.Fatalf("runbook confidence = %v, want 0.9", runbooks[0]["confidence"])
	}
}

func TestIncidentViewEmptyWhenNoAlert(t *testing.T) {
	now := time.Now().UTC()
	runner, _, _, _ := buildIncidentHarness(t, now)

	out, err := runner.Read(context.Background(), tools.Tool{Name: tools.IncidentView}, map[string]any{
		"domain":        "kafka",
		"resource_type": "consumer_group",
		"resource_name": "nope",
		"environment":   "prod",
	})
	if err != nil {
		t.Fatalf("incident.view: %v", err)
	}
	if out["incident_id"] != "" {
		t.Fatalf("incident_id = %v, want empty when no matching alert", out["incident_id"])
	}
	counts := out["counts"].(map[string]any)
	if counts["audit"] != 0 || counts["recent_writes"] != 0 {
		t.Fatalf("expected zero evidence for unknown resource, got %+v", counts)
	}
}
