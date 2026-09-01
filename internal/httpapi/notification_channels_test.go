package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
	"github.com/gracegaoya/ai-operations-copilot/internal/notification"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// memChannelStore 是 NotificationChannelStore 的内存实现（httpapi 测试用）。
type memChannelStore struct {
	mu      sync.Mutex
	records map[string]store.NotificationChannelRecord
}

func newMemChannelStore() *memChannelStore {
	return &memChannelStore{records: map[string]store.NotificationChannelRecord{}}
}

func (m *memChannelStore) List(context.Context) ([]store.NotificationChannelRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.NotificationChannelRecord, 0, len(m.records))
	for _, r := range m.records {
		out = append(out, r)
	}
	return out, nil
}

func (m *memChannelStore) Get(_ context.Context, id string) (store.NotificationChannelRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[id]
	if !ok {
		return store.NotificationChannelRecord{}, store.ErrNotFound
	}
	return r, nil
}

func (m *memChannelStore) Upsert(_ context.Context, ch store.NotificationChannelRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch.ID == "" {
		ch.ID = "ch-" + ch.Name
	}
	m.records[ch.ID] = ch
	return nil
}

func (m *memChannelStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.records, id)
	return nil
}

// channelRouter 装配带 HMAC 鉴权 + 通道管理器的 router。
func channelRouter(t *testing.T) http.Handler {
	t.Helper()
	st := newMemChannelStore()
	mgr := notification.NewChannelManager(st)
	if err := mgr.Load(context.Background()); err != nil {
		t.Fatalf("manager load: %v", err)
	}
	return httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		nil,
		httpapi.WithNotificationChannels(mgr),
	)
}

func channelRequest(t *testing.T, router http.Handler, method, path string, body any, roles ...string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Authorization", "Bearer "+signedJWT(t, map[string]any{"sub": "tester", "roles": roles}))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func decodeChannels(t *testing.T, rec *httptest.ResponseRecorder) (int, []map[string]any) {
	t.Helper()
	var list struct {
		Channels []map[string]any `json:"channels"`
		Count    int              `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v (%s)", err, rec.Body.String())
	}
	return list.Count, list.Channels
}

func TestNotificationChannelsRequireAdmin(t *testing.T) {
	t.Parallel()
	router := channelRouter(t)
	rec := channelRequest(t, router, http.MethodGet, "/v1/admin/notification-channels", nil, "viewer")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestNotificationChannelsCRUDAndSecretMasked(t *testing.T) {
	t.Parallel()
	router := channelRouter(t)

	// 空列表
	rec := channelRequest(t, router, http.MethodGet, "/v1/admin/notification-channels", nil, "admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", rec.Code)
	}
	count, _ := decodeChannels(t, rec)
	if count != 0 {
		t.Fatalf("empty list count = %d, want 0", count)
	}

	// 创建
	rec = channelRequest(t, router, http.MethodPost, "/v1/admin/notification-channels",
		map[string]any{"type": "webhook", "name": "内网网关", "url": "https://ops.local/hook", "secret": "s3cret"}, "admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		Status string `json:"status"`
		ID     string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("decode create = %+v (err %v)", created, err)
	}

	// 列表含新通道，secret 绝不回显（连键都不应存在）
	rec = channelRequest(t, router, http.MethodGet, "/v1/admin/notification-channels", nil, "admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("list2 status = %d, want 200", rec.Code)
	}
	count, channels := decodeChannels(t, rec)
	if count != 1 {
		t.Fatalf("list2 count = %d, want 1", count)
	}
	ch := channels[0]
	if _, present := ch["secret"]; present {
		t.Errorf("response leaked secret key: %v", ch)
	}
	if url, _ := ch["url"].(string); url == "" {
		t.Errorf("channel url missing: %v", ch)
	}

	// 删除
	rec = channelRequest(t, router, http.MethodDelete, "/v1/admin/notification-channels/"+created.ID, nil, "admin")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = channelRequest(t, router, http.MethodGet, "/v1/admin/notification-channels", nil, "admin")
	count, _ = decodeChannels(t, rec)
	if count != 0 {
		t.Fatalf("after delete count = %d, want 0", count)
	}
}

func TestNotificationChannelsRejectInvalidBody(t *testing.T) {
	t.Parallel()
	router := channelRouter(t)
	rec := channelRequest(t, router, http.MethodPost, "/v1/admin/notification-channels",
		map[string]any{"type": "sms", "name": "x", "url": "https://x"}, "admin")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid type status = %d, want 400", rec.Code)
	}
}
