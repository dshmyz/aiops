package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/alert"
	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

const testWebhookSecret = "test-webhook-secret"

// testAlertRouter 构建带告警 webhook + audit 的测试 router。
func testAlertRouter(t *testing.T) (http.Handler, *store.MemoryAlertStore) {
	t.Helper()
	alertStore := store.NewMemoryAlertStore()
	alertSvc := alert.NewService(alertStore)
	auditStore := store.NewMemoryActionPlanStore()
	auditService := audit.NewService(auditStore)
	webhookSvc := NewAlertWebhookService(alertSvc, auditService)
	router := NewRouter(
		nil,
		nil,
		WithAlertWebhook(webhookSvc),
		WithAlertWebhookSecret(testWebhookSecret),
	)
	return router, alertStore
}

func signBody(body []byte) string {
	mac := hmac.New(sha256.New, []byte(testWebhookSecret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func validAlertBody() []byte {
	payload := map[string]any{
		"external_id": "a1",
		"source":      "grafana",
		"title":       "CPU 高",
		"severity":    "critical",
		"status":      "firing",
	}
	body, _ := json.Marshal(payload)
	return body
}

func postWebhook(router http.Handler, body []byte, signature string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/alerts/webhook", bytes.NewReader(body))
	if signature != "" {
		req.Header.Set("X-Webhook-Signature", signature)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestAlertWebhookRejectsMissingSignature(t *testing.T) {
	t.Parallel()
	router, _ := testAlertRouter(t)
	rec := postWebhook(router, validAlertBody(), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAlertWebhookRejectsWrongSignature(t *testing.T) {
	t.Parallel()
	router, _ := testAlertRouter(t)
	rec := postWebhook(router, validAlertBody(), "deadbeef")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAlertWebhookAcceptsValidSignatureAndStores(t *testing.T) {
	t.Parallel()
	router, alertStore := testAlertRouter(t)
	body := validAlertBody()
	rec := postWebhook(router, body, signBody(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	records := alertStore.AlertRecords()
	if len(records) != 1 {
		t.Fatalf("stored alerts = %d, want 1", len(records))
	}
	if records[0].ExternalID != "a1" || records[0].Source != "grafana" {
		t.Errorf("stored alert = %+v", records[0])
	}
}

func TestAlertWebhookRejectsOversizedBody(t *testing.T) {
	t.Parallel()
	router, _ := testAlertRouter(t)
	big := bytes.Repeat([]byte("x"), 300*1024)
	// 超大 body 的签名无法通过（LimitReader 预读超过 cap），应 401 而非 200。
	rec := postWebhook(router, big, signBody(big))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for oversized body", rec.Code)
	}
}

func TestAlertWebhookRejectsInvalidBody(t *testing.T) {
	t.Parallel()
	router, _ := testAlertRouter(t)
	// 缺 source 字段，但签名有效
	body := []byte(`{"external_id":"a1","title":"t","severity":"critical","status":"firing"}`)
	rec := postWebhook(router, body, signBody(body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAlertWebhookRejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	router, _ := testAlertRouter(t)
	body := []byte(`{not json`)
	rec := postWebhook(router, body, signBody(body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAlertWebhookGetMethodNotAllowed(t *testing.T) {
	t.Parallel()
	router, _ := testAlertRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/alerts/webhook", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	// ServeMux 方法前缀模式下，路径命中但方法不符返回带 Allow 头的 405
	//（语义比旧的统一 404 更准确）。
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 (route only matches POST)", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != http.MethodPost {
		t.Fatalf("Allow = %q, want POST", allow)
	}
}

func TestAlertWebhookRejectsWhenNotConfigured(t *testing.T) {
	t.Parallel()
	// 未注入 webhook service，但注入了 secret
	router := NewRouter(
		nil,
		nil,
		WithAlertWebhookSecret(testWebhookSecret),
	)
	body := validAlertBody()
	rec := postWebhook(router, body, signBody(body))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when unconfigured", rec.Code)
	}
}

func TestAlertWebhookFailClosedWithoutSecret(t *testing.T) {
	t.Parallel()
	alertStore := store.NewMemoryAlertStore()
	alertSvc := alert.NewService(alertStore)
	webhookSvc := NewAlertWebhookService(alertSvc, audit.NewService(store.NewMemoryActionPlanStore()))
	// 注入了 service 但未注入 secret
	router := NewRouter(nil, nil, WithAlertWebhook(webhookSvc))
	body := validAlertBody()
	rec := postWebhook(router, body, signBody(body))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 when secret unset (fail closed)", rec.Code)
	}
}

func TestAlertWebhookAuditTrail(t *testing.T) {
	t.Parallel()
	alertStore := store.NewMemoryAlertStore()
	alertSvc := alert.NewService(alertStore)
	auditStore := store.NewMemoryActionPlanStore()
	auditService := audit.NewService(auditStore)
	webhookSvc := NewAlertWebhookService(alertSvc, auditService)
	router := NewRouter(
		nil,
		nil,
		WithAlertWebhook(webhookSvc),
		WithAlertWebhookSecret(testWebhookSecret),
	)
	body := validAlertBody()
	rec := postWebhook(router, body, signBody(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	events := auditStore.AuditEvents()
	found := false
	for _, ev := range events {
		if ev.Action == audit.ActionAlertIngested && ev.Subject == "grafana" {
			found = true
		}
	}
	if !found {
		t.Errorf("no alert_ingested audit event with subject grafana; events = %+v", events)
	}
}

func postAlertmanager(router http.Handler, body []byte, signature string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/alerts/alertmanager", bytes.NewReader(body))
	if signature != "" {
		req.Header.Set("X-Webhook-Signature", signature)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func validAlertmanagerBody() []byte {
	payload := map[string]any{
		"version":   "4",
		"groupKey":  "{}/{namespace=prod}:{alertname=HighCPU}",
		"receiver":  "webhook",
		"status":    "firing",
		"alerts": []map[string]any{
			{
				"status":      "firing",
				"fingerprint": "fp-abc-1",
				"labels":      map[string]string{"alertname": "HighCPU", "namespace": "prod", "severity": "critical"},
				"annotations": map[string]string{"summary": "CPU 超过 90%", "description": "节点 ns1 CPU 持续过高"},
				"startsAt":    "2026-08-02T10:00:00Z",
			},
			{
				"status":      "firing",
				"fingerprint": "fp-abc-2",
				"labels":      map[string]string{"alertname": "LowDisk", "namespace": "prod", "severity": "warning"},
				"annotations": map[string]string{"summary": "磁盘空间不足"},
				"startsAt":    "2026-08-02T10:01:00Z",
			},
		},
	}
	body, _ := json.Marshal(payload)
	return body
}

// TestAlertmanagerWebhookRejectsMissingSignature: the dedicated Alertmanager route
// is gated by the same HMAC signature as the generic webhook.
func TestAlertmanagerWebhookRejectsMissingSignature(t *testing.T) {
	router, _ := testAlertRouter(t)
	rec := postAlertmanager(router, validAlertmanagerBody(), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestAlertmanagerWebhookIngestsMultipleAlerts: a native payload with several
// alerts[] entries is mapped and each lands as its own normalized alert.
func TestAlertmanagerWebhookIngestsMultipleAlerts(t *testing.T) {
	router, alertStore := testAlertRouter(t)
	body := validAlertmanagerBody()
	rec := postAlertmanager(router, body, signBody(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"acknowledged":2`) {
		t.Fatalf("body = %s, want acknowledged 2", rec.Body.String())
	}
	stored := alertStore.AlertRecords()
	if len(stored) != 2 {
		t.Fatalf("stored alerts = %d, want 2", len(stored))
	}
	// Both persisted with the alertmanager source and their fingerprint identity.
	byID := map[string]store.Alert{}
	for _, a := range stored {
		byID[a.ExternalID] = a
	}
	a1, ok := byID["fp:fp-abc-1"]
	if !ok {
		t.Fatalf("missing alert with external id fp:fp-abc-1; got %v", stored)
	}
	if a1.Source != "alertmanager" {
		t.Errorf("source = %q, want alertmanager", a1.Source)
	}
	if a1.Title != "CPU 超过 90%" {
		t.Errorf("title = %q, want annotation summary", a1.Title)
	}
	if a1.Severity != "critical" {
		t.Errorf("severity = %q, want critical", a1.Severity)
	}
	if _, ok := byID["fp:fp-abc-2"]; !ok {
		t.Fatalf("missing alert with external id fp:fp-abc-2; got %v", stored)
	}
}

// TestAlertmanagerWebhookRejectsOversizedBody: the size cap from the generic
// webhook path also applies to Alertmanager.
func TestAlertmanagerWebhookRejectsOversizedBody(t *testing.T) {
	router, _ := testAlertRouter(t)
	big := append(validAlertmanagerBody(), make([]byte, maxAlertWebhookBodyBytes+1)...)
	rec := postAlertmanager(router, big, signBody(big))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (oversized body rejected in signature check)", rec.Code)
	}
}

// captureChainDiagnoser 记录被触发执行的规则名，验证 webhook→registry→链式研判链路。
type captureChainDiagnoser struct {
	triggered []string
}

func (c *captureChainDiagnoser) ExecuteChain(_ context.Context, _ alert.Alert, action alert.AlertAction) {
	c.triggered = append(c.triggered, action.Name)
}

// httpapiRuleStore 是 AlertActionRuleStore 的内存实现，供 webhook 链路测试用。
type httpapiRuleStore struct {
	records map[string]store.AlertActionRuleRecord
}

func (f *httpapiRuleStore) List(_ context.Context) ([]store.AlertActionRuleRecord, error) {
	out := make([]store.AlertActionRuleRecord, 0, len(f.records))
	for _, r := range f.records {
		out = append(out, r)
	}
	return out, nil
}

func (f *httpapiRuleStore) Get(_ context.Context, name string) (store.AlertActionRuleRecord, error) {
	r, ok := f.records[name]
	if !ok {
		return store.AlertActionRuleRecord{}, store.ErrNotFound
	}
	return r, nil
}

func (f *httpapiRuleStore) Upsert(_ context.Context, rule store.AlertActionRuleRecord) error {
	if f.records == nil {
		f.records = map[string]store.AlertActionRuleRecord{}
	}
	f.records[rule.Name] = rule
	return nil
}

func (f *httpapiRuleStore) Delete(_ context.Context, name string) error {
	delete(f.records, name)
	return nil
}

// TestAlertWebhookDrivesRegistryChain 验证后台 DB 规则接入 webhook 后真正驱动链式研判：
// 命中规则执行、未命中规则不执行、停用规则不执行。
func TestAlertWebhookDrivesRegistryChain(t *testing.T) {
	t.Parallel()
	alertStore := store.NewMemoryAlertStore()
	alertSvc := alert.NewService(alertStore)
	auditService := audit.NewService(store.NewMemoryActionPlanStore())

	// 后台规则注册表（DB 驱动，可热重载）。
	ruleStore := &httpapiRuleStore{}
	registry := alert.NewAlertActionRegistry(ruleStore)
	ctx := context.Background()
	if err := registry.Upsert(ctx, alert.AlertAction{
		Name:            "kafka-lag",
		Description:     "kafka 消费延迟",
		ExecuteLastStep: false,
		AlertMatch:      alert.AlertMatch{AlertName: "HighLag"},
		ToolSequence:    []alert.AlertActionStep{{Tool: "kafka.consumer_group.lag.read"}},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	chain := &captureChainDiagnoser{}
	webhookSvc := NewAlertWebhookService(alertSvc, auditService).
		WithChainDiagnoser(chain).
		WithActions(registry)
	router := NewRouter(nil, nil,
		WithAlertWebhook(webhookSvc),
		WithAlertWebhookSecret(testWebhookSecret),
	)

	// 命中规则：alertname=HighLag（大小写不敏感，高亮链路放宽效果）。
	hit := map[string]any{
		"external_id": "a1", "source": "grafana", "title": "kafka lag",
		"severity": "critical", "status": "firing",
		"labels": map[string]string{"alertname": "highlag"},
	}
	body, _ := json.Marshal(hit)
	if rec := postWebhook(router, body, signBody(body)); rec.Code != http.StatusOK {
		t.Fatalf("hit status = %d, want 200", rec.Code)
	}
	// 未命中规则：alertname=DiskFull。
	miss := map[string]any{
		"external_id": "a2", "source": "grafana", "title": "disk full",
		"severity": "warning", "status": "firing",
		"labels": map[string]string{"alertname": "DiskFull"},
	}
	body2, _ := json.Marshal(miss)
	if rec := postWebhook(router, body2, signBody(body2)); rec.Code != http.StatusOK {
		t.Fatalf("miss status = %d, want 200", rec.Code)
	}

	// 异步 goroutine，等待执行完成。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(chain.triggered) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(chain.triggered) != 1 || chain.triggered[0] != "kafka-lag" {
		t.Fatalf("triggered = %v, want exactly [kafka-lag]", chain.triggered)
	}
}

// TestAlertWebhookRegistryDisabledRuleNotTriggered 验证停用的后台规则不触发执行。
func TestAlertWebhookRegistryDisabledRuleNotTriggered(t *testing.T) {
	t.Parallel()
	alertStore := store.NewMemoryAlertStore()
	alertSvc := alert.NewService(alertStore)
	auditService := audit.NewService(store.NewMemoryActionPlanStore())

	ruleStore := &httpapiRuleStore{}
	registry := alert.NewAlertActionRegistry(ruleStore)
	ctx := context.Background()
	if err := registry.Upsert(ctx, alert.AlertAction{
		Name:            "disabled-rule",
		Description:     "停用规则",
		ExecuteLastStep: false,
		AlertMatch:      alert.AlertMatch{AlertName: "HighLag"},
		ToolSequence:    []alert.AlertActionStep{{Tool: "kafka.consumer_group.lag.read"}},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := registry.SetEnabled(ctx, "disabled-rule", false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}

	chain := &captureChainDiagnoser{}
	webhookSvc := NewAlertWebhookService(alertSvc, auditService).
		WithChainDiagnoser(chain).
		WithActions(registry)
	router := NewRouter(nil, nil,
		WithAlertWebhook(webhookSvc),
		WithAlertWebhookSecret(testWebhookSecret),
	)
	body := map[string]any{
		"external_id": "a1", "source": "grafana", "title": "kafka lag",
		"severity": "critical", "status": "firing",
		"labels": map[string]string{"alertname": "HighLag"},
	}
	raw, _ := json.Marshal(body)
	if rec := postWebhook(router, raw, signBody(raw)); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// 停用规则不应触发执行。
	time.Sleep(300 * time.Millisecond)
	if len(chain.triggered) != 0 {
		t.Fatalf("triggered = %v, want none (disabled rule)", chain.triggered)
	}
}
