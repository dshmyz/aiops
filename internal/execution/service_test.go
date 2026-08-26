package execution_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestConfirmedPlanCannotChangeInput(t *testing.T) {
	t.Parallel()
	ctx, service, plan, _, _ := confirmedWritePlan(t)

	_, err := service.ExecuteConfirmedPlan(ctx, plan.ID, map[string]any{
		"topic": "orders", "retention_hours": 16,
	})
	if err == nil || !contains(err.Error(), "immutable") {
		t.Fatalf("execute changed input error = %v, want immutable error", err)
	}
}

func TestExecuteConfirmedPlanUsesStoredSnapshotAndIdempotencyKey(t *testing.T) {
	t.Parallel()
	ctx, service, plan, runner, _ := confirmedWritePlan(t)
	input := map[string]any{"topic": "orders", "retention_hours": 72}

	first, err := service.ExecuteConfirmedPlan(ctx, plan.ID, input)
	if err != nil {
		t.Fatalf("first execution: %v", err)
	}
	input["retention_hours"] = 16
	second, err := service.ExecuteConfirmedPlan(ctx, plan.ID, map[string]any{"topic": "orders", "retention_hours": 72})
	if err != nil {
		t.Fatalf("second execution: %v", err)
	}
	if first.ID != second.ID || !second.Reused {
		t.Fatalf("second execution = %+v, want reused first execution %+v", second, first)
	}
	if runner.calls != 1 {
		t.Fatalf("executor calls = %d, want 1", runner.calls)
	}
	if runner.input["retention_hours"] != float64(72) {
		t.Fatalf("executor input = %#v, want immutable stored snapshot", runner.input)
	}
}

func TestExecuteConfirmedStoredPlanUsesPersistedSnapshot(t *testing.T) {
	ctx, service, plan, runner, _ := confirmedWritePlan(t)

	result, err := service.ExecuteConfirmedStoredPlan(ctx, plan.ID)
	if err != nil {
		t.Fatalf("execute confirmed stored plan: %v", err)
	}
	if result.Status != "succeeded" {
		t.Fatalf("status = %q, want succeeded", result.Status)
	}
	if runner.input["topic"] != "orders" || runner.input["retention_hours"] != float64(72) {
		t.Fatalf("runner input = %+v, want persisted plan snapshot", runner.input)
	}
}

func TestUnconfirmedWriteDoesNotExecute(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryActionPlanStore()
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time { return fixedTime }))
	decision := policy.Evaluate(testUser(), tool(t, "topic.retention.set"), writeInput())
	plan, err := planService.CreatePlan(ctx, testUser(), decision, writeInput())
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	runner := &recordingExecutor{}
	service := execution.NewService(repository, runner)
	if _, err := service.ExecuteConfirmedPlan(ctx, plan.ID, writeInput()); err == nil {
		t.Fatal("unconfirmed write executed")
	}
	if runner.calls != 0 {
		t.Fatalf("executor calls = %d, want 0", runner.calls)
	}
}

func TestExpiredConfirmedPlanDoesNotExecuteAndIsAudited(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryActionPlanStore()
	expiredAt := time.Now().UTC().Add(-11 * time.Minute)
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time { return expiredAt }))
	decision := policy.Evaluate(testUser(), tool(t, "topic.retention.set"), writeInput())
	plan, err := planService.CreatePlan(ctx, testUser(), decision, writeInput())
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	plan, err = planService.ConfirmPlan(ctx, plan.ID, plan.Version, plan.ConfirmationToken, testUser())
	if err != nil {
		t.Fatalf("confirm plan: %v", err)
	}
	runner := &recordingExecutor{}
	service := execution.NewService(repository, runner)

	_, err = service.ExecuteConfirmedPlan(ctx, plan.ID, writeInput())
	if !errors.Is(err, execution.ErrPlanExpired) {
		t.Fatalf("execute expired plan error = %v, want ErrPlanExpired", err)
	}
	if runner.calls != 0 {
		t.Fatalf("executor calls = %d, want 0", runner.calls)
	}
	events := repository.AuditEvents()
	last := events[len(events)-1]
	if last.Action != "execution_rejected" || last.Decision != "plan_expired" {
		t.Fatalf("expiry audit = %+v, want plan_expired rejection", last)
	}
}

func TestExecutionStoresSanitizedResultSummary(t *testing.T) {
	t.Parallel()
	ctx, service, plan, runner, repository := confirmedWritePlan(t)
	runner.result = map[string]any{
		"password":     "secret",
		"api_token":    "token",
		"rows_changed": 1,
		"summary":      "retention updated",
		"data":         map[string]any{"status": "ok", "nested_secret": "x"},
	}

	if _, err := service.ExecuteConfirmedPlan(ctx, plan.ID, writeInput()); err != nil {
		t.Fatalf("execute plan: %v", err)
	}
	records := repository.ExecutionRecords()
	if len(records) != 1 {
		t.Fatalf("execution records = %d, want 1", len(records))
	}
	raw := string(records[0].ResultSummary)
	if raw == "" || contains(raw, "secret") || contains(raw, "api_token") || contains(raw, "password") || contains(raw, "nested_secret") {
		t.Fatalf("result summary = %s, want sanitized result with secrets stripped", raw)
	}
	var summary map[string]any
	if err := json.Unmarshal(records[0].ResultSummary, &summary); err != nil {
		t.Fatalf("decode result summary: %v", err)
	}
	if summary["outcome"] != "succeeded" {
		t.Fatalf("summary = %#v, want succeeded outcome", summary)
	}
	// 真实输出被保留（rows_changed / summary），敏感键被剥离
	if summary["rows_changed"] != float64(1) {
		t.Errorf("rows_changed = %v, want 1 (real output preserved)", summary["rows_changed"])
	}
	if summary["summary"] != "retention updated" {
		t.Errorf("summary field = %v, want retention updated", summary["summary"])
	}
	for _, event := range repository.AuditEvents() {
		metadata, err := json.Marshal(event.Metadata)
		if err != nil {
			t.Fatalf("marshal audit metadata: %v", err)
		}
		if contains(string(metadata), "secret") || contains(string(metadata), "api_token") || contains(string(metadata), "password") {
			t.Fatalf("audit metadata = %s, must not include executor secrets", metadata)
		}
	}
}

func TestSanitizedResultSummaryPreservesOutputAndStripsNestedSecrets(t *testing.T) {
	t.Parallel()
	// 通过 ExecuteConfirmedPlan 端到端验证：真实输出保留 + 嵌套敏感键剥离
	ctx, service, plan, runner, repository := confirmedWritePlan(t)
	runner.result = map[string]any{
		"kind":     "config",
		"resource": map[string]any{"name": "orders", "region": "cn-east"},
		"severity": "low",
		"summary":  "retention set to 72h",
		"data": map[string]any{
			"applied":    true,
			"apiKey":     "sk-123",
			"hosts":      []any{"node-01", "node-02"},
			"connection": map[string]any{"username": "svc", "secret_key": "abc"},
		},
	}
	if _, err := service.ExecuteConfirmedPlan(ctx, plan.ID, writeInput()); err != nil {
		t.Fatalf("execute plan: %v", err)
	}
	body := string(repository.ExecutionRecords()[0].ResultSummary)
	if contains(body, "apiKey") || contains(body, "secret_key") || contains(body, "sk-123") {
		t.Fatalf("sensitive data leaked: %s", body)
	}
	if !contains(body, `"summary":"retention set to 72h"`) {
		t.Errorf("real output not preserved: %s", body)
	}
	if !contains(body, `"applied":true`) {
		t.Errorf("nested data not preserved: %s", body)
	}
}

func TestSanitizedResultSummaryCapsOversizedOutput(t *testing.T) {
	t.Parallel()
	// 通过 ExecuteConfirmedPlan 验证超大输出降级为固定摘要
	ctx, service, plan, runner, repository := confirmedWritePlan(t)
	runner.result = map[string]any{"data": strings.Repeat("x", 20*1024)}

	if _, err := service.ExecuteConfirmedPlan(ctx, plan.ID, writeInput()); err != nil {
		t.Fatalf("execute plan: %v", err)
	}
	body := repository.ExecutionRecords()[0].ResultSummary
	if len(body) > 10*1024 {
		t.Fatalf("body = %d bytes, want capped at 10KB", len(body))
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["outcome"] != "succeeded" {
		t.Errorf("outcome = %v, want succeeded", decoded["outcome"])
	}
}

func TestReusedExecutionAuditUsesExistingExecutionID(t *testing.T) {
	t.Parallel()
	ctx, service, plan, _, repository := confirmedWritePlan(t)
	if _, err := service.ExecuteConfirmedPlan(ctx, plan.ID, writeInput()); err != nil {
		t.Fatalf("first execution: %v", err)
	}
	first := repository.ExecutionRecords()[0]
	if _, err := service.ExecuteConfirmedPlan(ctx, plan.ID, writeInput()); err != nil {
		t.Fatalf("reused execution: %v", err)
	}
	events := repository.AuditEvents()
	last := events[len(events)-1]
	if last.Action != "execution_reused" || last.ExecutionID != first.ID {
		t.Fatalf("reused audit = %+v, want execution ID %q", last, first.ID)
	}
}

func TestExecuteConfirmedPlanPopulatesVerificationWhenVerifierConfigured(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryActionPlanStore()
	now := time.Now().UTC()
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time { return now }))
	decision := policy.Evaluate(testUser(), tool(t, "topic.retention.set"), writeInput())
	plan, err := planService.CreatePlan(ctx, testUser(), decision, writeInput())
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	plan, err = planService.ConfirmPlan(ctx, plan.ID, plan.Version, plan.ConfirmationToken, testUser())
	if err != nil {
		t.Fatalf("confirm plan: %v", err)
	}
	runner := &recordingExecutor{}
	verifier := &stubVerifier{result: &execution.VerificationResult{ToolName: "kafka.topic.retention.read", Status: "success", Answer: map[string]any{"retention_hours": float64(72)}}}
	service := execution.NewServiceWithVerifier(repository, runner, verifier)

	execution, err := service.ExecuteConfirmedPlan(ctx, plan.ID, writeInput())
	if err != nil {
		t.Fatalf("execute plan: %v", err)
	}
	if execution.Verification == nil {
		t.Fatal("Verification is nil, want populated result")
	}
	if execution.Verification.ToolName != "kafka.topic.retention.read" {
		t.Errorf("Verification.ToolName = %q, want kafka.topic.retention.read", execution.Verification.ToolName)
	}
	if execution.Verification.Status != "success" {
		t.Errorf("Verification.Status = %q, want success", execution.Verification.Status)
	}
	if execution.Verification.Answer["retention_hours"] != float64(72) {
		t.Errorf("Verification.Answer = %+v, want retention_hours=72", execution.Verification.Answer)
	}
	if verifier.calls != 1 {
		t.Errorf("verifier calls = %d, want 1", verifier.calls)
	}
	if verifier.planID != plan.ID {
		t.Errorf("verifier plan id = %q, want %q", verifier.planID, plan.ID)
	}
	// 结果准 #5: 验证结果应持久化到 execution record
	records := repository.ExecutionRecords()
	if len(records) != 1 {
		t.Fatalf("execution records = %d, want 1", len(records))
	}
	if len(records[0].Verification) == 0 {
		t.Fatal("Verification not persisted on execution record")
	}
	var persisted map[string]any
	if err := json.Unmarshal(records[0].Verification, &persisted); err != nil {
		t.Fatalf("decode persisted verification: %v", err)
	}
	if persisted["status"] != "success" || persisted["tool_name"] != "kafka.topic.retention.read" {
		t.Errorf("persisted verification = %+v, want success kafka read", persisted)
	}
}

func TestExecuteConfirmedPlanOmitsVerificationWhenVerifierAbsent(t *testing.T) {
	t.Parallel()
	ctx, service, plan, _, _ := confirmedWritePlan(t)

	execution, err := service.ExecuteConfirmedPlan(ctx, plan.ID, writeInput())
	if err != nil {
		t.Fatalf("execute plan: %v", err)
	}
	if execution.Verification != nil {
		t.Errorf("Verification = %+v, want nil when no verifier configured", execution.Verification)
	}
}

func TestExecuteConfirmedPlanReusedExecutionSkipsVerifier(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryActionPlanStore()
	now := time.Now().UTC()
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time { return now }))
	decision := policy.Evaluate(testUser(), tool(t, "topic.retention.set"), writeInput())
	plan, err := planService.CreatePlan(ctx, testUser(), decision, writeInput())
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	plan, err = planService.ConfirmPlan(ctx, plan.ID, plan.Version, plan.ConfirmationToken, testUser())
	if err != nil {
		t.Fatalf("confirm plan: %v", err)
	}
	runner := &recordingExecutor{}
	verifier := &stubVerifier{result: &execution.VerificationResult{ToolName: "kafka.topic.retention.read", Status: "success"}}
	service := execution.NewServiceWithVerifier(repository, runner, verifier)

	if _, err := service.ExecuteConfirmedPlan(ctx, plan.ID, writeInput()); err != nil {
		t.Fatalf("first execution: %v", err)
	}
	verifier.calls = 0
	execution, err := service.ExecuteConfirmedPlan(ctx, plan.ID, writeInput())
	if err != nil {
		t.Fatalf("second execution: %v", err)
	}
	if !execution.Reused {
		t.Fatal("second execution not reused")
	}
	if verifier.calls != 0 {
		t.Errorf("verifier calls = %d, want 0 on reused execution", verifier.calls)
	}
	if execution.Verification != nil {
		t.Errorf("Verification = %+v, want nil on reused execution", execution.Verification)
	}
}

func TestExecuteConfirmedPlanVerifierFailureDoesNotBlockExecution(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryActionPlanStore()
	now := time.Now().UTC()
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time { return now }))
	decision := policy.Evaluate(testUser(), tool(t, "topic.retention.set"), writeInput())
	plan, err := planService.CreatePlan(ctx, testUser(), decision, writeInput())
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	plan, err = planService.ConfirmPlan(ctx, plan.ID, plan.Version, plan.ConfirmationToken, testUser())
	if err != nil {
		t.Fatalf("confirm plan: %v", err)
	}
	runner := &recordingExecutor{}
	verifier := &stubVerifier{err: errors.New("read endpoint down")}
	service := execution.NewServiceWithVerifier(repository, runner, verifier)

	execution, err := service.ExecuteConfirmedPlan(ctx, plan.ID, writeInput())
	if err != nil {
		t.Fatalf("execute plan: %v, want success despite verifier failure", err)
	}
	if execution.Status != "succeeded" {
		t.Errorf("Status = %q, want succeeded", execution.Status)
	}
	if execution.Verification == nil {
		t.Fatal("Verification is nil, want failed result")
	}
	if execution.Verification.Status != "failed" {
		t.Errorf("Verification.Status = %q, want failed", execution.Verification.Status)
	}
	if execution.Verification.Error != "read endpoint down" {
		t.Errorf("Verification.Error = %q, want 'read endpoint down'", execution.Verification.Error)
	}
}

type stubVerifier struct {
	result *execution.VerificationResult
	err    error
	calls  int
	planID string
}

func (v *stubVerifier) Verify(_ context.Context, plan store.PlanRecord, _ map[string]any) (*execution.VerificationResult, error) {
	v.calls++
	v.planID = plan.ID
	if v.err != nil {
		return &execution.VerificationResult{ToolName: "", Status: "failed", Error: v.err.Error()}, nil
	}
	return v.result, nil
}

var fixedTime = time.Date(2026, time.July, 21, 8, 0, 0, 0, time.UTC)

func confirmedWritePlan(t *testing.T) (context.Context, *execution.Service, plans.Plan, *recordingExecutor, *store.MemoryActionPlanStore) {
	t.Helper()
	ensureMiddlewareWriteTool(t)
	ctx := context.Background()
	repository := store.NewMemoryActionPlanStore()
	now := time.Now().UTC()
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time { return now }))
	decision := policy.Evaluate(testUser(), tool(t, "topic.retention.set"), writeInput())
	plan, err := planService.CreatePlan(ctx, testUser(), decision, writeInput())
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	plan, err = planService.ConfirmPlan(ctx, plan.ID, plan.Version, plan.ConfirmationToken, testUser())
	if err != nil {
		t.Fatalf("confirm plan: %v", err)
	}
	runner := &recordingExecutor{}
	return ctx, execution.NewService(repository, runner), plan, runner, repository
}

type recordingExecutor struct {
	mu     sync.Mutex
	calls  int
	input  map[string]any
	result map[string]any
}

func (e *recordingExecutor) Execute(_ context.Context, _ string, input map[string]any) (map[string]any, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	e.input = input
	if e.result != nil {
		return e.result, nil
	}
	return map[string]any{"ok": true}, nil
}

func writeInput() map[string]any {
	return map[string]any{"topic": "orders", "retention_hours": 72}
}

func testUser() identity.CurrentUser {
	return identity.CurrentUser{Subject: "operator-1", Roles: []string{"admin"}, RequestID: "req-task-3"}
}

func tool(t *testing.T, name string) tools.Tool {
	t.Helper()
	value, ok := tools.Lookup(name)
	if !ok {
		t.Fatalf("unknown test tool %q", name)
	}
	return value
}

// ensureMiddlewareWriteTool loads topic.retention.set into the dynamic registry
// and grants it operator/admin role permissions, mirroring the published YAML
// capability. It is idempotent and mutex-guarded so the parallel confirmed-write
// tests can all rely on the tool without racing on RegisterDynamicTools'
// duplicate rejection or missing policy grants.
var ensureMiddlewareWriteMu sync.Mutex

func ensureMiddlewareWriteTool(t *testing.T) {
	t.Helper()
	ensureMiddlewareWriteMu.Lock()
	defer ensureMiddlewareWriteMu.Unlock()
	if _, ok := tools.Lookup("topic.retention.set"); !ok {
		if err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
			Tool: tools.Tool{
				Name:                "topic.retention.set",
				Operation:           tools.Write,
				Risk:                tools.Medium,
				RollbackDescription: "reset_to_previous",
				Domain:              "kafka",
				ResourceType:        "topic",
				SupportsDryRun:      true,
			},
			InputSchema: map[string]tools.DynamicInputField{

				"topic":           {Type: "string", Required: true},
				"retention_hours": {Type: "integer", Required: true},
			},
		}}); err != nil {
			t.Fatalf("register middleware write tool: %v", err)
		}
	}
	// Policy grants are additive/idempotent; re-inject so a policy-level reset
	// elsewhere cannot leave the write un-routable for admin/operator.
	policy.RegisterDynamicRolePermissions(map[string][]string{
		"topic.retention.set": {"operator", "admin"},
	})
}

func contains(value, want string) bool { return strings.Contains(value, want) }

// recordingObserver captures execution events for assertion.
type recordingObserver struct {
	mu     sync.Mutex
	events []execution.ExecutionEvent
}

func (o *recordingObserver) OnExecutionComplete(_ context.Context, event execution.ExecutionEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, event)
}

// TestExecutionObserverNotifiedOnSuccess verifies that registered observers
// receive an event after a successful execution.
func TestExecutionObserverNotifiedOnSuccess(t *testing.T) {
	t.Parallel()
	ctx, service, plan, _, _ := confirmedWritePlan(t)
	observer := &recordingObserver{}
	service.WithObservers(observer)

	input := map[string]any{"topic": "orders", "retention_hours": 72}
	_, err := service.ExecuteConfirmedPlan(ctx, plan.ID, input)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.events) != 1 {
		t.Fatalf("observer events = %d, want 1", len(observer.events))
	}
	event := observer.events[0]
	if event.Status != "succeeded" {
		t.Fatalf("event.Status = %q, want succeeded", event.Status)
	}
	if event.ToolName != "topic.retention.set" {
		t.Fatalf("event.ToolName = %q, want %q", event.ToolName, "topic.retention.set")
	}
	if event.PlanID != plan.ID {
		t.Fatalf("event.PlanID = %q, want %q", event.PlanID, plan.ID)
	}
}

// TestExecutionObserverNotifiedOnFailure verifies that observers receive a
// failed event when the executor returns an error.
func TestExecutionObserverNotifiedOnFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryActionPlanStore()
	now := time.Now().UTC()
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time { return now }))
	decision := policy.Evaluate(testUser(), tool(t, "topic.retention.set"), writeInput())
	plan, _ := planService.CreatePlan(ctx, testUser(), decision, writeInput())
	plan, _ = planService.ConfirmPlan(ctx, plan.ID, plan.Version, plan.ConfirmationToken, testUser())

	failingExecutor := &failExecutor{}
	service := execution.NewService(repository, failingExecutor)
	observer := &recordingObserver{}
	service.WithObservers(observer)

	_, err := service.ExecuteConfirmedPlan(ctx, plan.ID, writeInput())
	if err == nil {
		t.Fatal("expected executor error")
	}

	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.events) != 1 {
		t.Fatalf("observer events = %d, want 1", len(observer.events))
	}
	if observer.events[0].Status != "failed" {
		t.Fatalf("event.Status = %q, want failed", observer.events[0].Status)
	}
}

// TestExecutionObserverNotCalledOnReused verifies that observers are NOT
// notified when an execution is reused (idempotency hit).
func TestExecutionObserverNotCalledOnReused(t *testing.T) {
	t.Parallel()
	ctx, service, plan, _, _ := confirmedWritePlan(t)
	observer := &recordingObserver{}
	service.WithObservers(observer)

	input := map[string]any{"topic": "orders", "retention_hours": 72}
	_, _ = service.ExecuteConfirmedPlan(ctx, plan.ID, input)
	_, _ = service.ExecuteConfirmedPlan(ctx, plan.ID, input) // reused

	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.events) != 1 {
		t.Fatalf("observer events = %d, want 1 (reused should not notify)", len(observer.events))
	}
}

// TestExecuteConfirmedPlanPersistsRealErrorSummary verifies that the real
// executor error message is persisted to the ExecutionRecord.ErrorSummary
// (收口1: 结果准 error 持久化). Previously the errorSummary was hardcoded to
// "execution failed", discarding the real cause and making failed executions
// impossible to复盘.
func TestExecuteConfirmedPlanPersistsRealErrorSummary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryActionPlanStore()
	now := time.Now().UTC()
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time { return now }))
	decision := policy.Evaluate(testUser(), tool(t, "topic.retention.set"), writeInput())
	plan, _ := planService.CreatePlan(ctx, testUser(), decision, writeInput())
	plan, _ = planService.ConfirmPlan(ctx, plan.ID, plan.Version, plan.ConfirmationToken, testUser())

	// 用可识别的 error message，便于断言真实错误被持久化
	const wantErr = "connection refused to broker k1:9092"
	failingExecutor := &typedFailExecutor{err: errors.New(wantErr)}
	service := execution.NewService(repository, failingExecutor)

	_, err := service.ExecuteConfirmedPlan(ctx, plan.ID, writeInput())
	if err == nil {
		t.Fatal("expected executor error")
	}

	records := repository.ExecutionRecords()
	if len(records) != 1 {
		t.Fatalf("execution records = %d, want 1", len(records))
	}
	got := records[0].ErrorSummary
	if !strings.Contains(got, wantErr) {
		t.Fatalf("ErrorSummary = %q, want it to contain %q (real executor error)", got, wantErr)
	}
	if got == "execution failed" {
		t.Fatal("ErrorSummary is hardcoded 'execution failed', want real executor error")
	}
}

// TestExecuteConfirmedPlanSucceedsClearsErrorSummary verifies that the
// ErrorSummary is empty on success (收口1: 回归保护，确保成功路径不被污染).
func TestExecuteConfirmedPlanSucceedsClearsErrorSummary(t *testing.T) {
	t.Parallel()
	ctx, service, plan, _, repository := confirmedWritePlan(t)

	input := map[string]any{"topic": "orders", "retention_hours": 72}
	if _, err := service.ExecuteConfirmedPlan(ctx, plan.ID, input); err != nil {
		t.Fatalf("execute: %v", err)
	}

	records := repository.ExecutionRecords()
	if len(records) != 1 {
		t.Fatalf("execution records = %d, want 1", len(records))
	}
	if records[0].ErrorSummary != "" {
		t.Fatalf("ErrorSummary = %q, want empty on success", records[0].ErrorSummary)
	}
	if records[0].Status != "succeeded" {
		t.Fatalf("Status = %q, want succeeded", records[0].Status)
	}
}

// TestExecutionEventContainsRequestIDAndResult verifies that the execution
// event includes the plan's RequestID and a sanitized result summary so
// observers (e.g. knowledge ingester) can correlate and document executions.
func TestExecutionEventContainsRequestIDAndResult(t *testing.T) {
	t.Parallel()
	ctx, service, plan, _, _ := confirmedWritePlan(t)
	observer := &recordingObserver{}
	service.WithObservers(observer)

	input := map[string]any{"topic": "orders", "retention_hours": 72}
	_, err := service.ExecuteConfirmedPlan(ctx, plan.ID, input)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.events) != 1 {
		t.Fatalf("observer events = %d, want 1", len(observer.events))
	}
	event := observer.events[0]
	if event.RequestID == "" {
		t.Fatal("event.RequestID is empty, want plan's RequestID")
	}
	if event.ResultSummary == "" {
		t.Fatal("event.ResultSummary is empty, want sanitized result summary")
	}
}

// TestExecutionEventContainsRequestIDOnFailure verifies that failed
// executions also carry the RequestID for correlation.
func TestExecutionEventContainsRequestIDOnFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := store.NewMemoryActionPlanStore()
	now := time.Now().UTC()
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time { return now }))
	decision := policy.Evaluate(testUser(), tool(t, "topic.retention.set"), writeInput())
	plan, _ := planService.CreatePlan(ctx, testUser(), decision, writeInput())
	plan, _ = planService.ConfirmPlan(ctx, plan.ID, plan.Version, plan.ConfirmationToken, testUser())

	failingExecutor := &failExecutor{}
	service := execution.NewService(repository, failingExecutor)
	observer := &recordingObserver{}
	service.WithObservers(observer)

	_, err := service.ExecuteConfirmedPlan(ctx, plan.ID, writeInput())
	if err == nil {
		t.Fatal("expected executor error")
	}

	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.events) != 1 {
		t.Fatalf("observer events = %d, want 1", len(observer.events))
	}
	if observer.events[0].RequestID == "" {
		t.Fatal("event.RequestID is empty on failure, want plan's RequestID")
	}
}

type failExecutor struct{}

func (e *failExecutor) Execute(context.Context, string, map[string]any) (map[string]any, error) {
	return nil, errors.New("executor failure")
}

// typedFailExecutor returns a caller-supplied error, so tests can assert that
// the real error message (not a hardcoded string) is persisted.
type typedFailExecutor struct{ err error }

func (e *typedFailExecutor) Execute(context.Context, string, map[string]any) (map[string]any, error) {
	return nil, e.err
}

// TestTruncateError verifies the error summary truncation logic (收口1):
// 超长 error 被截断并追加省略号，短 error 原样返回，多字节字符不在中间被切开。
func TestTruncateError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		msg  string
		max  int
		want string
	}{
		{name: "short passthrough", msg: "connection refused", max: 1000, want: "connection refused"},
		{name: "exact length", msg: "abc", max: 3, want: "abc"},
		{name: "truncate ascii", msg: "abcdef", max: 3, want: "abc..."},
		{name: "truncate multibyte by rune", msg: "连接被拒绝", max: 2, want: "连接..."},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := execution.TruncateError(tc.msg, tc.max)
			if got != tc.want {
				t.Fatalf("TruncateError(%q, %d) = %q, want %q", tc.msg, tc.max, got, tc.want)
			}
		})
	}
}
