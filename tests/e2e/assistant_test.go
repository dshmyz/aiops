package e2e_test

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestAssistantReturnsMiddlewareDiagnosticPackage(t *testing.T) {
	t.Parallel()
	db := openAssistantSQLite(t)
	repository := store.NewSQLActionPlanStore(db)
	auditService := audit.NewService(repository)
	readService := execution.NewReadOnlyService(e2eReadRunner{}, auditService)
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time {
		return time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	}))
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		readService,
		httpapi.WithAssistant(assistant.NewService(assistant.DeterministicPlanner{}, readService, planService, nil)),
		httpapi.WithActionPlans(repository),
	)
	req := httptest.NewRequest(http.MethodPost, "/v1/assistant/messages", strings.NewReader(`{"message":"检查 prod glusterfs data volume 健康"}`))
	req.Header.Set("Authorization", "Bearer "+signedJWT(t))
	req.Header.Set("X-Request-ID", "assistant-diagnostic-e2e-request")
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	var body struct {
		Type       string              `json:"type"`
		Tool       string              `json:"tool"`
		Diagnostic diagnostics.Package `json:"diagnostic"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Type != "answer" || body.Tool != tools.GlusterVolumeHealthRead || body.Diagnostic.Environment != "prod" || len(body.Diagnostic.Observations) == 0 {
		t.Fatalf("body = %+v, want answer response with diagnostic package", body)
	}
	var auditCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM copilot_audit_events WHERE request_id = ? AND tool_name = ? AND action = 'readonly_tool_executed' AND decision = 'permitted'`, "assistant-diagnostic-e2e-request", tools.GlusterVolumeHealthRead).Scan(&auditCount); err != nil {
		t.Fatalf("count diagnostic audit events: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("diagnostic audit count = %d, want 1 permitted read-only execution", auditCount)
	}
	var planCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM action_plans WHERE request_id = ?`, "assistant-diagnostic-e2e-request").Scan(&planCount); err != nil {
		t.Fatalf("count diagnostic action plans: %v", err)
	}
	if planCount != 0 {
		t.Fatalf("diagnostic action plan count = %d, want 0", planCount)
	}
	var executionCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tool_executions`).Scan(&executionCount); err != nil {
		t.Fatalf("count diagnostic tool executions: %v", err)
	}
	if executionCount != 0 {
		t.Fatalf("diagnostic tool execution count = %d, want 0", executionCount)
	}
}

// TestAssistantFormatterProducesBlocks 验证当 assistant 配置了二阶段整形器时，
// answer 响应包含 Summary 和 Blocks 字段（端到端验证双阶段应答链路）。
func TestAssistantFormatterProducesBlocks(t *testing.T) {
	t.Parallel()
	db := openAssistantSQLite(t)
	repository := store.NewSQLActionPlanStore(db)
	auditService := audit.NewService(repository)
	readService := execution.NewReadOnlyService(e2eReadRunner{}, auditService)
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time {
		return time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	}))
	svc := assistant.NewService(assistant.DeterministicPlanner{}, readService, planService, nil).
		WithFormatter(assistant.NewCodeFallbackFormatter())
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		readService,
		httpapi.WithAssistant(svc),
		httpapi.WithActionPlans(repository),
	)
	req := httptest.NewRequest(http.MethodPost, "/v1/assistant/messages", strings.NewReader(`{"message":"检查 prod glusterfs data volume 健康"}`))
	req.Header.Set("Authorization", "Bearer "+signedJWT(t))
	req.Header.Set("X-Request-ID", "assistant-formatter-e2e")
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	var body struct {
		Type    string                   `json:"type"`
		Tool    string                   `json:"tool"`
		Summary string                   `json:"summary"`
		Blocks  []assistant.Block        `json:"blocks"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Type != "answer" {
		t.Fatalf("Type = %q, want answer", body.Type)
	}
	if body.Summary == "" {
		t.Error("Summary is empty, formatter should have produced one")
	}
	if len(body.Blocks) == 0 {
		t.Fatal("Blocks is empty, formatter should have produced blocks")
	}
	// CodeFallbackFormatter 至少产出 tool_trace block
	hasToolTrace := false
	for _, b := range body.Blocks {
		if b.Type == assistant.BlockToolTrace {
			hasToolTrace = true
		}
	}
	if !hasToolTrace {
		t.Error("Blocks missing tool_trace, CodeFallbackFormatter should always produce it")
	}
}

func TestAssistantWriteMessageStoresPendingPlanInSQLite(t *testing.T) {
	db := openAssistantSQLite(t)
	repository := store.NewSQLActionPlanStore(db)
	auditService := audit.NewService(repository)
	readService := execution.NewReadOnlyService(e2eReadRunner{}, auditService)
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time {
		return time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	}))
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		readService,
		httpapi.WithAssistant(assistant.NewService(assistant.DeterministicPlanner{}, readService, planService, nil)),
		httpapi.WithActionPlans(repository),
	)
	req := httptest.NewRequest(http.MethodPost, "/v1/assistant/messages", strings.NewReader(`{"message":"把 prod 的 orders topic retention 改成 72 小时"}`))
	req.Header.Set("Authorization", "Bearer "+signedAdminJWT(t))
	req.Header.Set("X-Request-ID", "assistant-e2e-request")
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "confirmation_token") {
		t.Fatalf("body = %s, must not expose confirmation token", res.Body.String())
	}
	var planCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM action_plans WHERE request_id = 'assistant-e2e-request' AND status = 'pending_confirmation'`).Scan(&planCount); err != nil {
		t.Fatalf("count action plans: %v", err)
	}
	if planCount != 1 {
		t.Fatalf("plan count = %d, want 1", planCount)
	}
	listReq := httptest.NewRequest(http.MethodGet, "/v1/action-plans?status=pending_confirmation", nil)
	listReq.Header.Set("Authorization", "Bearer "+signedAdminJWT(t))
	listReq.Header.Set("X-Request-ID", "assistant-e2e-list-request")
	listRes := httptest.NewRecorder()

	router.ServeHTTP(listRes, listReq)

	if listRes.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s, want 200", listRes.Code, listRes.Body.String())
	}
	if !strings.Contains(listRes.Body.String(), `"tool":"topic.retention.set"`) || !strings.Contains(listRes.Body.String(), `"environment":"prod"`) {
		t.Fatalf("list body = %s, want pending prod retention plan", listRes.Body.String())
	}
	if strings.Contains(listRes.Body.String(), "confirmation_token") {
		t.Fatalf("list body = %s, must not expose confirmation token", listRes.Body.String())
	}
}

func openAssistantSQLite(t *testing.T) *sql.DB {
	t.Helper()
	// Use a unique database name per test to avoid parallel migration conflicts
	// on the shared in-memory SQLite instance.
	dsn := "file:e2e_assistant_" + t.Name() + "?mode=memory&cache=shared"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	return db
}

func signedAdminJWT(t *testing.T) string {
	t.Helper()
	header := encodeSegment(t, map[string]any{"alg": "HS256", "typ": "JWT"})
	claims := encodeSegment(t, map[string]any{
		"sub":                  "admin-1",
		"roles":                []string{"admin"},
		"allowed_environments": []string{"prod"},
	})
	return signToken(header, claims)
}

func signToken(header, claims string) string {
	unsigned := header + "." + claims
	mac := hmac.New(sha256.New, []byte("test-secret"))
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// einoMockChatModel is a minimal model.BaseChatModel implementation for E2E
// tests. It returns pre-configured content for Generate and streams it as
// chunks for Stream, exercising the full Eino planner path without a real LLM.
type einoMockChatModel struct {
	content string
	chunks  []string
}

func (m *einoMockChatModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage(m.content, nil), nil
}

func (m *einoMockChatModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	chunks := m.chunks
	if len(chunks) == 0 {
		chunks = []string{m.content}
	}
	messages := make([]*schema.Message, 0, len(chunks))
	for _, c := range chunks {
		messages = append(messages, schema.AssistantMessage(c, nil))
	}
	return schema.StreamReaderFromArray(messages), nil
}

// TestEinoMockProviderIntegrationReadFlow exercises the full HTTP → router →
// assistant service → Eino planner (mock model) → read service → response path
// for a read-only tool invocation.
func TestEinoMockProviderIntegrationReadFlow(t *testing.T) {
	t.Parallel()
	db := openAssistantSQLite(t)
	repository := store.NewSQLActionPlanStore(db)
	auditService := audit.NewService(repository)
	readService := execution.NewReadOnlyService(e2eReadRunner{}, auditService)
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time {
		return time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	}))
	mockChat := &einoMockChatModel{
		content: `{"tool_name":"cluster.status.read","input":{"environment":"prod"},"confidence":0.92,"explanation":"check cluster status"}`,
	}
	planner := assistant.NewEinoPlanner(mockChat)
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		readService,
		httpapi.WithAssistant(assistant.NewService(planner, readService, planService, nil)),
		httpapi.WithActionPlans(repository),
	)

	req := httptest.NewRequest(http.MethodPost, "/v1/assistant/messages", strings.NewReader(`{"message":"查看 prod 集群状态"}`))
	req.Header.Set("Authorization", "Bearer "+signedJWT(t))
	req.Header.Set("X-Request-ID", "eino-e2e-read")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	var body struct {
		Type   string         `json:"type"`
		Tool   string         `json:"tool"`
		Answer map[string]any `json:"answer"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Type != "answer" || body.Tool != tools.ClusterStatusRead {
		t.Fatalf("body = %+v, want answer for cluster.status.read", body)
	}
	if body.Answer["status"] != "green" {
		t.Fatalf("answer.status = %v, want green", body.Answer["status"])
	}
}

// TestEinoMockProviderIntegrationWriteFlow verifies that the Eino planner path
// correctly creates a pending action plan for write operations.
func TestEinoMockProviderIntegrationWriteFlow(t *testing.T) {
	t.Parallel()
	db := openAssistantSQLite(t)
	repository := store.NewSQLActionPlanStore(db)
	auditService := audit.NewService(repository)
	readService := execution.NewReadOnlyService(e2eReadRunner{}, auditService)
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time {
		return time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	}))
	mockChat := &einoMockChatModel{
		content: `{"tool_name":"topic.retention.set","input":{"environment":"prod","topic":"orders","retention_hours":72},"confidence":0.95,"explanation":"set retention"}`,
	}
	planner := assistant.NewEinoPlanner(mockChat)
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		readService,
		httpapi.WithAssistant(assistant.NewService(planner, readService, planService, nil)),
		httpapi.WithActionPlans(repository),
	)

	req := httptest.NewRequest(http.MethodPost, "/v1/assistant/messages", strings.NewReader(`{"message":"把 prod 的 orders topic retention 改成 72 小时"}`))
	req.Header.Set("Authorization", "Bearer "+signedAdminJWT(t))
	req.Header.Set("X-Request-ID", "eino-e2e-write")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	var body struct {
		Type   string `json:"type"`
		Tool   string `json:"tool"`
		PlanID string `json:"plan_id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Type != "confirmation_required" || body.Status != "pending_confirmation" {
		t.Fatalf("body = %+v, want confirmation_required plan", body)
	}
	if body.PlanID == "" {
		t.Fatal("plan_id = empty, want a stored plan")
	}
}

// TestEinoSSEStreamEndToEnd verifies the /v1/assistant/stream SSE endpoint:
// delta frames are emitted as "data:" lines, the final response arrives as
// "event: response", and the stream terminates with "event: done".
func TestEinoSSEStreamEndToEnd(t *testing.T) {
	t.Parallel()
	db := openAssistantSQLite(t)
	repository := store.NewSQLActionPlanStore(db)
	auditService := audit.NewService(repository)
	readService := execution.NewReadOnlyService(e2eReadRunner{}, auditService)
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time {
		return time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	}))
	// Split the JSON intent across multiple chunks to verify delta forwarding.
	intentJSON := `{"tool_name":"cluster.status.read","input":{"environment":"prod"},"confidence":0.91,"explanation":"read cluster status"}`
	mockChat := &einoMockChatModel{
		chunks: []string{intentJSON[:20], intentJSON[20:60], intentJSON[60:]},
	}
	planner := assistant.NewEinoPlanner(mockChat)
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		readService,
		httpapi.WithAssistant(assistant.NewService(planner, readService, planService, nil)),
		httpapi.WithActionPlans(repository),
	)

	req := httptest.NewRequest(http.MethodPost, "/v1/assistant/stream", strings.NewReader(`{"message":"查看 prod 集群状态"}`))
	req.Header.Set("Authorization", "Bearer "+signedJWT(t))
	req.Header.Set("X-Request-ID", "eino-e2e-sse")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if ct := res.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	// Parse SSE frames from the response body.
	var (
		deltaCount    int
		hasResponse   bool
		hasDone       bool
		responseBody  string
	)
	scanner := bufio.NewScanner(res.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			var frame map[string]any
			if err := json.Unmarshal([]byte(data), &frame); err != nil {
				continue
			}
			if _, ok := frame["delta"]; ok {
				deltaCount++
			}
		}
		if strings.HasPrefix(line, "event: response") {
			hasResponse = true
			// Next line is the data payload.
			if scanner.Scan() {
				responseBody = strings.TrimPrefix(scanner.Text(), "data: ")
			}
		}
		if strings.HasPrefix(line, "event: done") {
			hasDone = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan SSE body: %v", err)
	}
	if deltaCount != 3 {
		t.Fatalf("delta frames = %d, want 3", deltaCount)
	}
	if !hasResponse {
		t.Fatal("missing 'event: response' frame")
	}
	if !hasDone {
		t.Fatal("missing 'event: done' frame")
	}
	// Verify the response payload contains the expected answer.
	var resp struct {
		Type string         `json:"type"`
		Tool string         `json:"tool"`
		Answer map[string]any `json:"answer"`
	}
	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		t.Fatalf("unmarshal response frame: %v", err)
	}
	if resp.Type != "answer" || resp.Tool != tools.ClusterStatusRead {
		t.Fatalf("response = %+v, want answer for cluster.status.read", resp)
	}
}
