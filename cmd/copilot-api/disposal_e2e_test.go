package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/golang-jwt/jwt/v5"

	"github.com/gracegaoya/ai-operations-copilot/internal/alert"
	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
	"github.com/gracegaoya/ai-operations-copilot/internal/notification"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// 告警处置闭环的自动化 E2E（此前只能手工验证，七处缺陷因此存活）：
// 告警 webhook 接入 → LLM 研判（脚本化假 chat，零配额）→ 注册表派生修复候选
// → 自动建待确认计划 → 通知携带 confirmation_token → 确认执行写能力 →
// 写后验证读回 / 拒绝路径。LLM、告警后端、计划存储全部内存/httptest，CI 可跑。

const (
	disposalJWTSecret   = "e2e-jwt-secret"
	disposalWebhookSec  = "e2e-webhook-secret"
	disposalMockCluster = "c1"
)

// --- 假后端：Kafka retention 读/写（带内存状态供写后验证读回） ---

type retentionBackend struct {
	mu    sync.Mutex
	state map[string]int // "cluster/topic" → retention_hours
	posts int
}

func newRetentionBackend() *retentionBackend {
	return &retentionBackend{state: map[string]int{}}
}

func (b *retentionBackend) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/kafka/") || !strings.HasSuffix(r.URL.Path, "/retention") {
			http.NotFound(w, r)
			return
		}
		parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/kafka/"), "/retention"), "/topics/")
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		cluster, topic := parts[0], parts[1]
		key := cluster + "/" + topic
		b.mu.Lock()
		defer b.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			hours, ok := b.state[key]
			if !ok {
				hours = 24
			}
			status := "ok"
			if hours <= 24 {
				status = "warning"
			}
			writeJSONMap(w, map[string]any{
				"status": status,
				"data":   map[string]any{"cluster": cluster, "topic": topic, "retention_hours": hours},
			})
		case http.MethodPost:
			var body struct {
				RetentionHours int `json:"retention_hours"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.RetentionHours > 0 {
				b.state[key] = body.RetentionHours
			}
			b.posts++
			writeJSONMap(w, map[string]any{"status": "accepted", "data": map[string]any{"cluster": cluster, "topic": topic, "retention_hours": body.RetentionHours}})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

func (b *retentionBackend) postCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.posts
}

func writeJSONMap(w http.ResponseWriter, m map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(m)
}

// --- 假 LLM：两次调用分别返回诊断计划与研判报告 ---

type disposalFakeChat struct {
	mu        sync.Mutex
	responses []string
	calls     int
}

func (c *disposalFakeChat) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := c.calls
	if idx >= len(c.responses) {
		idx = len(c.responses) - 1
	}
	c.calls++
	return schema.AssistantMessage(c.responses[idx], nil), nil
}

func (c *disposalFakeChat) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := c.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	sr, sw := schema.Pipe[*schema.Message](1)
	sw.Send(msg, nil)
	sw.Close()
	return sr, nil
}

// recordingNotifier 捕获确认通知（token 的分发渠道）。
type recordingNotifier struct {
	mu   sync.Mutex
	sent []notification.ConfirmationRequest
}

func (r *recordingNotifier) NotifyConfirmation(_ context.Context, req notification.ConfirmationRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, req)
	return nil
}

func (r *recordingNotifier) all() []notification.ConfirmationRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]notification.ConfirmationRequest, len(r.sent))
	copy(out, r.sent)
	return out
}

// --- 能力 YAML（指向测试自带的后端） ---

func writeDisposalCapabilities(t *testing.T, baseURL string) string {
	t.Helper()
	root := t.TempDir()
	pub := filepath.Join(root, "published")
	if err := os.MkdirAll(pub, 0o755); err != nil {
		t.Fatalf("mkdir published: %v", err)
	}
	readYAML := fmt.Sprintf(`schema_version: 1
name: kafka.topic.retention.read
status: published
domain: kafka
resource_type: topic
operation: read
risk: low
backend:
  adapter: http
  method: GET
  path: /api/kafka/{cluster}/topics/{topic}/retention
  timeout_ms: 3000
  base_url: %s
input_schema:
  cluster: {type: string, required: true}
  topic: {type: string, required: true}
output:
  kind: observation
  severity_path: $.status
  summary_template: "Topic {topic} retention is {retention_hours}h"
  fields:
    retention_hours: $.data.retention_hours
auth:
  roles: [viewer, operator, admin]
ai:
  description: 读取 Kafka topic 保留期
`, baseURL)
	writeYAML := fmt.Sprintf(`schema_version: 1
name: topic.retention.set
status: published
domain: kafka
resource_type: topic
operation: write
risk: medium
backend:
  adapter: http
  method: POST
  path: /api/kafka/default/topics/{topic}/retention
  timeout_ms: 3000
  base_url: %s
input_schema:
  topic: {type: string, required: true, description: 要调整保留期的 topic 名}
  retention_hours: {type: integer, required: true, min: 1, max: 8760, description: 保留时长（小时）}
output:
  kind: confirmation
  summary_template: "Retention for topic {topic} set to {retention_hours}h"
governance:
  requires_action_plan: true
  requires_approval: true
  precheck_tools: [kafka.topic.retention.read]
  rollback:
    strategy: reset_to_previous
    source: previous_value
auth:
  roles: [operator, admin]
ai:
  description: 设置 Kafka topic 保留期
verify:
  read_capability: kafka.topic.retention.read
  input_mapping:
    topic: "{topic}"
    cluster: "default"
  timeout_ms: 3000
`, baseURL)
	for name, content := range map[string]string{
		"kafka.topic.retention.read.yaml": readYAML,
		"topic.retention.set.yaml":        writeYAML,
	} {
		if err := os.WriteFile(filepath.Join(pub, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

// --- 栈组装（与 main.go 同构：buildCapabilityRuntimes + 只读服务 + 处置链） ---

type disposalStack struct {
	router   http.Handler
	backend  *retentionBackend
	notifier *recordingNotifier
	planRepo store.ActionPlanStore
}

func newDisposalStack(t *testing.T) *disposalStack {
	t.Helper()
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)

	backend := newRetentionBackend()
	mockSrv := httptest.NewServer(backend.handler())
	t.Cleanup(mockSrv.Close)

	t.Setenv("COPILOT_CAPABILITIES_DIR", writeDisposalCapabilities(t, mockSrv.URL))
	loaded, err := publishedCapabilitiesFromEnv()
	if err != nil {
		t.Fatalf("publishedCapabilitiesFromEnv: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded %d capabilities, want 2", len(loaded))
	}

	adapter := capabilities.NewHTTPAdapterWithConfig(nil, capabilities.AdapterConfig{MaxRetries: 1})
	readRunner, writeExecutor, verifier, _ := buildCapabilityRuntimes(loaded, adapter, true, staticWriteExecutor{})
	planRepo := store.NewMemoryActionPlanStore()
	auditSvc := audit.NewService(planRepo)
	readService := execution.NewReadOnlyService(readRunner, auditSvc)
	executionService := execution.NewServiceWithVerifier(planRepo, writeExecutor, verifier)
	planService := plans.NewService(planRepo, nil)

	diagService := diagnostics.NewService(readService, nil).
		WithCapabilityResolver(diagnostics.NewCapabilityResolver(loaded))
	alertSvc := alert.NewService(store.NewMemoryAlertStore())
	notifier := &recordingNotifier{}
	planCreator := alert.NewPlanCreator(planService, alertSvc).WithNotifier(notifier)
	fake := &disposalFakeChat{responses: []string{
		`{"diagnostic_steps":[{"domain":"kafka","runbook":"health"}],"confidence":0.9,"reasoning":"check retention"}`,
		`{"status":"warning","summary":"topic 保留期 24h 低于合规要求","root_cause":"保留期配置过短","impact":"审计数据可能提前删除","recommendations":[]}`,
	}}
	llmDiag := alert.NewLLMDiagnoser(fake, diagService, alertSvc).
		WithRecommendationPlanCreator(planCreator)
	alertWebhook := httpapi.NewAlertWebhookService(alertSvc, auditSvc).WithLLMDiagnoser(llmDiag)

	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte(disposalJWTSecret)),
		readService,
		httpapi.WithAlertWebhook(alertWebhook),
		httpapi.WithAlertWebhookSecret(disposalWebhookSec),
		httpapi.WithActionPlanConfirmation(planService, executionService),
		httpapi.WithActionPlans(planRepo),
	)
	return &disposalStack{router: router, backend: backend, notifier: notifier, planRepo: planRepo}
}

func (s *disposalStack) ingestAlert(t *testing.T, externalID, topic string) {
	t.Helper()
	body := fmt.Sprintf(`{"external_id":%q,"source":"e2e","title":"Kafka topic %s 保留期过短","severity":"warning","status":"firing","domain":"kafka","resource_type":"topic","resource_name":%q,"description":"自动化处置 E2E","labels":{"cluster":%q,"topic":%q}}`,
		externalID, topic, topic, disposalMockCluster, topic)
	mac := hmac.New(sha256.New, []byte(disposalWebhookSec))
	mac.Write([]byte(body))
	sig := hex.EncodeToString(mac.Sum(nil))
	req := httptest.NewRequest(http.MethodPost, "/v1/alerts/webhook", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", sig)
	res := httptest.NewRecorder()
	s.router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("ingest alert: status %d body %s", res.Code, res.Body.String())
	}
}

func (s *disposalStack) adminJWT(t *testing.T) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":   "admin-e2e",
		"roles": []string{"admin"},
		"exp":   time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString([]byte(disposalJWTSecret))
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return signed
}

func (s *disposalStack) pendingPlanIDs(t *testing.T) []string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/action-plans?status=pending_confirmation", nil)
	req.Header.Set("Authorization", "Bearer "+s.adminJWT(t))
	res := httptest.NewRecorder()
	s.router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("list plans: status %d body %s", res.Code, res.Body.String())
	}
	var parsed struct {
		Plans []struct {
			ID   string `json:"id"`
			Tool string `json:"tool"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("parse plans: %v (%s)", err, res.Body.String())
	}
	ids := make([]string, 0, len(parsed.Plans))
	for _, p := range parsed.Plans {
		ids = append(ids, p.ID)
	}
	return ids
}

// waitForPendingPlan 轮询等待自动研判建出计划（研判在后台 goroutine 异步执行）。
func (s *disposalStack) waitForPendingPlan(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ids := s.pendingPlanIDs(t); len(ids) > 0 {
			return ids[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no pending plan created within 5s")
	return ""
}

// --- 用例 ---

// TestDisposalE2EConfirmExecutesAndVerifies 全链路确认路径：告警 → 研判 →
// 建计划 → 通知带 token → 确认 → 写执行 → 写后验证读回。
func TestDisposalE2EConfirmExecutesAndVerifies(t *testing.T) {
	s := newDisposalStack(t)
	s.ingestAlert(t, "disp-e2e-confirm", "orders")

	planID := s.waitForPendingPlan(t)

	// 通知渠道必须携带 token——没有它自动计划无法被审批。
	sent := s.notifier.all()
	if len(sent) != 1 {
		t.Fatalf("confirmation notifications = %d, want 1", len(sent))
	}
	if sent[0].PlanID != planID || sent[0].ConfirmationToken == "" {
		t.Fatalf("notification missing plan/token: %+v", sent[0])
	}
	if sent[0].ToolName != "topic.retention.set" {
		t.Fatalf("notification tool = %s", sent[0].ToolName)
	}

	// 确认执行。
	body := fmt.Sprintf(`{"confirmation_token":%q,"expected_version":1}`, sent[0].ConfirmationToken)
	req := httptest.NewRequest(http.MethodPost, "/v1/action-plans/"+planID+"/confirm", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.adminJWT(t))
	res := httptest.NewRecorder()
	s.router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("confirm: status %d body %s", res.Code, res.Body.String())
	}
	var result struct {
		Status       string `json:"status"`
		ExecutionID  string `json:"execution_id"`
		Verification *struct {
			Status   string `json:"status"`
			ToolName string `json:"tool_name"`
		} `json:"verification"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
		t.Fatalf("parse confirm result: %v (%s)", err, res.Body.String())
	}
	if result.Status != "succeeded" {
		t.Fatalf("execution status = %s, want succeeded", result.Status)
	}
	if result.Verification == nil || result.Verification.Status != "success" {
		t.Fatalf("post-execution verification missing/failed: %+v", result.Verification)
	}
	if result.Verification.ToolName != "kafka.topic.retention.read" {
		t.Fatalf("verification tool = %s", result.Verification.ToolName)
	}
	if s.backend.postCount() == 0 {
		t.Fatal("write backend never received the POST")
	}
}

// TestDisposalE2ERejectPath 拒绝路径：第二条告警的计划被显式拒绝，终态
// rejected，不产生任何写执行。
func TestDisposalE2ERejectPath(t *testing.T) {
	s := newDisposalStack(t)
	s.ingestAlert(t, "disp-e2e-reject", "payments")
	planID := s.waitForPendingPlan(t)
	postsBefore := s.backend.postCount()

	body := `{"expected_version":1}`
	req := httptest.NewRequest(http.MethodPost, "/v1/action-plans/"+planID+"/reject", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.adminJWT(t))
	res := httptest.NewRecorder()
	s.router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("reject: status %d body %s", res.Code, res.Body.String())
	}
	var result struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
		t.Fatalf("parse reject result: %v (%s)", err, res.Body.String())
	}
	if result.Status != "rejected" {
		t.Fatalf("plan status = %s, want rejected", result.Status)
	}
	if s.backend.postCount() != postsBefore {
		t.Fatal("rejected plan must not execute any write")
	}
	if len(s.pendingPlanIDs(t)) != 0 {
		t.Fatal("rejected plan must leave the pending queue")
	}
}
