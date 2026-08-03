package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		"environment": "prod",
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
	body := []byte(`{"external_id":"a1","title":"t","severity":"critical","status":"firing","environment":"prod"}`)
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
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route only matches POST)", rec.Code)
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
				"labels":      map[string]string{"alertname": "HighCPU", "namespace": "prod", "severity": "critical", "environment": "prod"},
				"annotations": map[string]string{"summary": "CPU 超过 90%", "description": "节点 ns1 CPU 持续过高"},
				"startsAt":    "2026-08-02T10:00:00Z",
			},
			{
				"status":      "firing",
				"fingerprint": "fp-abc-2",
				"labels":      map[string]string{"alertname": "LowDisk", "namespace": "prod", "severity": "warning", "environment": "prod"},
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
