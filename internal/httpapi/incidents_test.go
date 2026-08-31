package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"github.com/gracegaoya/ai-operations-copilot/internal/alert"
	"github.com/gracegaoya/ai-operations-copilot/internal/audit"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// testIncidentRouter 构建带 incident 查询路由的 JWT 鉴权 router，
// 内存 store 预置一个 firing incident + 两名成员。
func testIncidentRouter(t *testing.T) (http.Handler, *store.MemoryIncidentStore, *store.MemoryAlertStore) {
	t.Helper()
	alertStore := store.NewMemoryAlertStore()
	incidents := store.NewMemoryIncidentStore()

	now := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	inc, err := incidents.UpsertIncident(context.Background(), store.AlertIncident{
		Status: "firing", Severity: "warning", Title: "积压",
		Domain: "kafka", ResourceType: "consumer_group", ResourceName: "orders",
		AlertCount: 2, FirstSeenAt: now, LastSeenAt: now,
	})
	if err != nil {
		t.Fatalf("seed incident: %v", err)
	}
	alertIDs := map[string]string{} // externalID → store 分配的 ID
	for _, ext := range []string{"alert-1", "alert-2"} {
		saved, _, err := alertStore.Upsert(context.Background(), store.Alert{
			ExternalID: ext, Source: "grafana", Title: "积压 " + ext,
			Severity: "warning", Status: "firing", Domain: "kafka",
		})
		if err != nil {
			t.Fatalf("seed alert %s: %v", ext, err)
		}
		alertIDs[ext] = saved.ID
		if err := incidents.AttachMember(context.Background(), inc.ID, saved.ID); err != nil {
			t.Fatalf("seed member %s: %v", ext, err)
		}
	}

	query := NewIncidentQueryService(incidents, alertStore)
	router := NewRouter(
		NewHMACAuthenticator([]byte("test-secret")),
		nil,
		WithIncidentQuery(query),
	)
	return router, incidents, alertStore
}

func incidentGet(t *testing.T, router http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	return res
}

// incidentJWT 签发与 NewHMACAuthenticator([]byte("test-secret")) 匹配的 JWT。
func incidentJWT(t *testing.T) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"viewer-1","roles":["viewer"]}`))
	unsigned := header + "." + claims
	mac := hmac.New(sha256.New, []byte("test-secret"))
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// TestServeListIncidentsRequiresAuth 未带 token 必须 401——路由接进 mux 后
// 必须真正过 authenticate（而不是裸 404）。
func TestServeListIncidentsRequiresAuth(t *testing.T) {
	router, _, _ := testIncidentRouter(t)
	res := incidentGet(t, router, "/v1/incidents?status=firing", "")
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body: %s)", res.Code, res.Body.String())
	}
}

// TestServeListIncidentsReturnsJSON 锁定路由注册：/v1/incidents 必须返回
// JSON incident 列表（曾经的缺陷是 handler 存在但从未注册，永远 404）。
func TestServeListIncidentsReturnsJSON(t *testing.T) {
	router, _, _ := testIncidentRouter(t)
	token := incidentJWT(t)
	res := incidentGet(t, router, "/v1/incidents?status=firing", token)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", res.Code, res.Body.String())
	}
	var body struct {
		Incidents []store.AlertIncident `json:"incidents"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Incidents) != 1 || body.Incidents[0].Status != "firing" {
		t.Errorf("incidents = %+v, want 1 firing", body.Incidents)
	}
}

// TestServeGetIncidentReturnsMembers 详情必须带出成员告警全文。
func TestServeGetIncidentReturnsMembers(t *testing.T) {
	router, _, _ := testIncidentRouter(t)
	token := incidentJWT(t)

	// 先取列表拿 incident ID
	listRes := incidentGet(t, router, "/v1/incidents?status=firing", token)
	var list struct {
		Incidents []store.AlertIncident `json:"incidents"`
	}
	if err := json.Unmarshal(listRes.Body.Bytes(), &list); err != nil || len(list.Incidents) != 1 {
		t.Fatalf("list incidents: %v (%d)", err, len(list.Incidents))
	}
	id := list.Incidents[0].ID

	res := incidentGet(t, router, "/v1/incidents/"+id, token)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", res.Code, res.Body.String())
	}
	var body struct {
		Incident store.AlertIncident `json:"incident"`
		Alerts   []store.Alert       `json:"alerts"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Incident.ID != id || len(body.Alerts) != 2 {
		t.Errorf("incident=%+v alerts=%d, want id match and 2 alerts", body.Incident, len(body.Alerts))
	}
}

// TestServeGetIncidentNotFound 未知 ID 返回 JSON 404（非 mux 裸 404）。
func TestServeGetIncidentNotFound(t *testing.T) {
	router, _, _ := testIncidentRouter(t)
	token := incidentJWT(t)
	res := incidentGet(t, router, "/v1/incidents/no-such-id", token)
	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", res.Code, res.Body.String())
	}
	if res.Body.String() == "404 page not found\n" {
		t.Error("got mux bare 404, want JSON error body")
	}
}

// TestIncidentEndToEndWebhookToQuery 全链路：webhook 接入两条同键告警 →
// 归并为同一 incident → 恢复后经 /v1/incidents 可见 resolved。
func TestIncidentEndToEndWebhookToQuery(t *testing.T) {
	alertStore := store.NewMemoryAlertStore()
	incidents := store.NewMemoryIncidentStore()
	alertSvc := alert.NewService(alertStore).WithCorrelation(incidents, 30*time.Minute)
	auditStore := store.NewMemoryActionPlanStore()
	webhookSvc := NewAlertWebhookService(alertSvc, audit.NewService(auditStore))
	query := NewIncidentQueryService(incidents, alertStore)
	router := NewRouter(
		NewHMACAuthenticator([]byte("test-secret")),
		nil,
		WithAlertWebhook(webhookSvc),
		WithAlertWebhookSecret(testWebhookSecret),
		WithIncidentQuery(query),
	)
	token := incidentJWT(t)

	post := func(payload map[string]any) {
		t.Helper()
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/v1/alerts/webhook", bytes.NewReader(body))
		req.Header.Set("X-Webhook-Signature", signBody(body))
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("webhook status = %d (body: %s)", res.Code, res.Body.String())
		}
	}

	post(map[string]any{"external_id": "a1", "source": "grafana", "title": "积压",
		"severity": "warning", "status": "firing", "domain": "kafka"})
	post(map[string]any{"external_id": "a2", "source": "grafana", "title": "积压",
		"severity": "warning", "status": "firing", "domain": "kafka"})

	res := incidentGet(t, router, "/v1/incidents?status=firing", token)
	var list struct {
		Incidents []store.AlertIncident `json:"incidents"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Incidents) != 1 || list.Incidents[0].AlertCount != 2 {
		t.Fatalf("incidents = %+v, want 1 with count 2", list.Incidents)
	}

	// 两条告警恢复 → incident resolved
	post(map[string]any{"external_id": "a1", "source": "grafana", "title": "积压",
		"severity": "warning", "status": "resolved", "domain": "kafka"})
	post(map[string]any{"external_id": "a2", "source": "grafana", "title": "积压",
		"severity": "warning", "status": "resolved", "domain": "kafka"})

	res = incidentGet(t, router, "/v1/incidents?status=resolved", token)
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode resolved list: %v", err)
	}
	if len(list.Incidents) != 1 || list.Incidents[0].Status != "resolved" {
		t.Fatalf("resolved incidents = %+v, want 1 resolved", list.Incidents)
	}
}
