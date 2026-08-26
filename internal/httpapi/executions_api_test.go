package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// seedExecutionForHTTP 在 repository 上直接创建 plan + execution，用于 HTTP 层测试。
// 与 store 包的 seedExecutionForTest 同构，但这里用 store 包的公开 API。
func seedExecutionForHTTP(t *testing.T, repository *store.MemoryActionPlanStore, planID, toolName, status string, startedAt *time.Time, createdAt time.Time) string {
	t.Helper()
	ctx := context.Background()
	plan := store.PlanRecord{
		ID:        planID,
		RequestID: "req-" + planID,
		CreatedBy: "tester",
		ToolName:  toolName,
		Status:    store.PlanConfirmed,
		Version:   1,
		ExpiresAt: createdAt.Add(time.Hour),
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
	if err := repository.CreatePlan(ctx, plan, store.AuditEvent{ID: "audit-" + planID, Action: "plan_created", Decision: "permitted", CreatedAt: createdAt}); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	execID := "exec-" + planID
	exec := store.ExecutionRecord{
		ID:             execID,
		ActionPlanID:   planID,
		IdempotencyKey: "key-" + planID,
		Status:         status,
		StartedAt:      startedAt,
		CreatedAt:      createdAt,
	}
	if _, _, err := repository.CreateExecutionIfAbsent(ctx, exec, store.AuditEvent{ID: "audit-exec-" + planID, Action: "execution_started", Decision: "permitted", CreatedAt: createdAt}); err != nil {
		t.Fatalf("CreateExecutionIfAbsent: %v", err)
	}
	return execID
}

// TestListExecutionsReturnsJSONNotBase64 verifies result_summary / verification
// serialize as JSON objects, not base64 (Bug2: []byte fields were base64-encoded).
func TestListExecutionsReturnsJSONNotBase64(t *testing.T) {
	t.Parallel()
	router, repository := testRouter(t, &readRunner{})
	base := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	started := base.Add(time.Minute)
	execID := seedExecutionForHTTP(t, repository, "plan-1", "topic.retention.set", "running", &started, base)
	// 通过 CompleteExecution + SetExecutionVerification 落真实结果（结果准 #4/#5）
	summary := []byte(`{"outcome":"succeeded","status":"applied","topic":"orders"}`)
	verification := []byte(`{"status":"success","tool_name":"kafka.topic.retention.read"}`)
	if err := repository.CompleteExecution(context.Background(), execID, "succeeded", summary, "", store.AuditEvent{ID: "audit-complete", Action: "execution_succeeded", Decision: "permitted", CreatedAt: base}); err != nil {
		t.Fatalf("CompleteExecution: %v", err)
	}
	if err := repository.SetExecutionVerification(context.Background(), execID, verification); err != nil {
		t.Fatalf("SetExecutionVerification: %v", err)
	}

	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedRequest(t, "/v1/executions", "", "admin-1", []string{"admin"}))

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", res.Code, http.StatusOK, res.Body.String())
	}
	var body struct {
		Executions []struct {
			ResultSummary json.RawMessage `json:"result_summary"`
			Verification  json.RawMessage `json:"verification"`
		} `json:"executions"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Executions) == 0 {
		t.Fatal("no executions returned")
	}
	// result_summary / verification 必须是合法 JSON（而非 base64）
	for _, e := range body.Executions {
		if len(e.ResultSummary) > 0 && !json.Valid(e.ResultSummary) {
			t.Fatalf("result_summary is not valid JSON (base64?): %q", string(e.ResultSummary))
		}
		if len(e.Verification) > 0 && !json.Valid(e.Verification) {
			t.Fatalf("verification is not valid JSON (base64?): %q", string(e.Verification))
		}
	}
}

// TestListExecutionsRequiresAuthentication 验证未认证请求被拒绝。
func TestListExecutionsRequiresAuthentication(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/executions", nil))

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
}

// TestListExecutionsRejectsNonAdmin 验证只有 admin 角色可查（R5 admin-only 鉴权）。
func TestListExecutionsRejectsNonAdmin(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/executions", "", "viewer-1", []string{"viewer"}))

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (admin-only)", res.Code, http.StatusForbidden)
	}
}

// TestListExecutionsReturnsChronological 验证 admin 可查全部执行记录，按 created_at DESC 返回。
func TestListExecutionsReturnsChronological(t *testing.T) {
	t.Parallel()
	router, repository := testRouter(t, &readRunner{})
	base := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	started := base.Add(time.Minute)
	seedExecutionForHTTP(t, repository, "plan-1", "topic.retention.set", "succeeded", &started, base)
	seedExecutionForHTTP(t, repository, "plan-2", "minio.bucket.quota.set", "failed", &started, base.Add(time.Second))

	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedRequest(t, "/v1/executions", "", "admin-1", []string{"admin"}))

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", res.Code, http.StatusOK, res.Body.String())
	}
	var body struct {
		Executions []struct {
			ID       string `json:"id"`
			ToolName string `json:"tool_name"`
			Status   string `json:"status"`
		} `json:"executions"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Executions) != 2 {
		t.Fatalf("executions = %d, want 2", len(body.Executions))
	}
	// DESC：plan-2（base+1s）在前
	if body.Executions[0].ID != "exec-plan-2" {
		t.Fatalf("first = %q, want exec-plan-2", body.Executions[0].ID)
	}
	if body.Executions[0].ToolName != "minio.bucket.quota.set" {
		t.Fatalf("tool_name = %q, want minio.bucket.quota.set", body.Executions[0].ToolName)
	}
}

// TestListExecutionsFiltersByStatus 验证 status query 参数过滤。
func TestListExecutionsFiltersByStatus(t *testing.T) {
	t.Parallel()
	router, repository := testRouter(t, &readRunner{})
	base := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	started := base.Add(time.Minute)
	seedExecutionForHTTP(t, repository, "plan-1", "topic.retention.set", "succeeded", &started, base)
	seedExecutionForHTTP(t, repository, "plan-2", "minio.bucket.quota.set", "failed", &started, base.Add(time.Second))

	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedRequest(t, "/v1/executions?status=failed", "", "admin-1", []string{"admin"}))

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
	}
	var body struct {
		Executions []struct {
			ID string `json:"id"`
		} `json:"executions"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Executions) != 1 {
		t.Fatalf("executions = %d, want 1 (failed only)", len(body.Executions))
	}
	if body.Executions[0].ID != "exec-plan-2" {
		t.Fatalf("execution = %q, want exec-plan-2", body.Executions[0].ID)
	}
}

// TestListExecutionsFiltersByToolName 验证 tool query 参数过滤（关联 plan）。
func TestListExecutionsFiltersByToolName(t *testing.T) {
	t.Parallel()
	router, repository := testRouter(t, &readRunner{})
	base := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	started := base.Add(time.Minute)
	seedExecutionForHTTP(t, repository, "plan-1", "topic.retention.set", "succeeded", &started, base)
	seedExecutionForHTTP(t, repository, "plan-2", "minio.bucket.quota.set", "failed", &started, base.Add(time.Second))

	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedRequest(t, "/v1/executions?tool=topic.retention.set", "", "admin-1", []string{"admin"}))

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
	}
	var body struct {
		Executions []struct {
			ToolName string `json:"tool_name"`
		} `json:"executions"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Executions) != 1 {
		t.Fatalf("executions = %d, want 1 (topic.retention.set only)", len(body.Executions))
	}
	if body.Executions[0].ToolName != "topic.retention.set" {
		t.Fatalf("tool_name = %q, want topic.retention.set", body.Executions[0].ToolName)
	}
}

// TestListExecutionsPaginatesByKeyset 验证 keyset 分页：limit + cursor。
func TestListExecutionsPaginatesByKeyset(t *testing.T) {
	t.Parallel()
	router, repository := testRouter(t, &readRunner{})
	base := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	started := base.Add(time.Minute)
	seedExecutionForHTTP(t, repository, "plan-1", "topic.retention.set", "succeeded", &started, base)
	seedExecutionForHTTP(t, repository, "plan-2", "topic.retention.set", "succeeded", &started, base.Add(time.Second))
	seedExecutionForHTTP(t, repository, "plan-3", "topic.retention.set", "succeeded", &started, base.Add(2 * time.Second))

	// 第一页
	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedRequest(t, "/v1/executions?limit=2", "", "admin-1", []string{"admin"}))
	if res.Code != http.StatusOK {
		t.Fatalf("page1 status = %d, want %d", res.Code, http.StatusOK)
	}
	var page1 struct {
		Executions []struct {
			ID string `json:"id"`
		} `json:"executions"`
		NextCursor struct {
			CreatedAt string `json:"created_at"`
			ID        string `json:"id"`
		} `json:"next_cursor"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &page1); err != nil {
		t.Fatalf("unmarshal page1: %v", err)
	}
	if len(page1.Executions) != 2 {
		t.Fatalf("page1 executions = %d, want 2", len(page1.Executions))
	}
	if page1.Executions[0].ID != "exec-plan-3" {
		t.Fatalf("page1 first = %q, want exec-plan-3", page1.Executions[0].ID)
	}
	if page1.NextCursor.ID == "" {
		t.Fatal("page1 next_cursor is empty, want cursor for next page")
	}

	// 第二页
	res2 := httptest.NewRecorder()
	router.ServeHTTP(res2, signedRequest(t, "/v1/executions?limit=2&cursor_created_at="+page1.NextCursor.CreatedAt+"&cursor_id="+page1.NextCursor.ID, "", "admin-1", []string{"admin"}))
	if res2.Code != http.StatusOK {
		t.Fatalf("page2 status = %d, want %d", res2.Code, http.StatusOK)
	}
	var page2 struct {
		Executions []struct {
			ID string `json:"id"`
		} `json:"executions"`
		NextCursor struct {
			CreatedAt string `json:"created_at"`
			ID        string `json:"id"`
		} `json:"next_cursor"`
	}
	if err := json.Unmarshal(res2.Body.Bytes(), &page2); err != nil {
		t.Fatalf("unmarshal page2: %v", err)
	}
	if len(page2.Executions) != 1 {
		t.Fatalf("page2 executions = %d, want 1", len(page2.Executions))
	}
	if page2.Executions[0].ID != "exec-plan-1" {
		t.Fatalf("page2 first = %q, want exec-plan-1", page2.Executions[0].ID)
	}
	if page2.NextCursor.ID != "" {
		t.Fatalf("page2 next_cursor should be empty, got %q", page2.NextCursor.ID)
	}
}

// TestListExecutionsRejectsUnsupportedQueryParameter 验证未知 query 参数返回 400。
func TestListExecutionsRejectsUnsupportedQueryParameter(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/executions?foo=bar", "", "admin-1", []string{"admin"}))

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
}

// 编译期断言：确保我们用了 httpapi 包（避免 import 报错）。
var _ = httpapi.NewRouter
