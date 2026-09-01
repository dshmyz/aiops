package notification

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// fakeChannelStore 是 NotificationChannelStore 的内存实现，供管理器测试。
type fakeChannelStore struct {
	mu      sync.Mutex
	records map[string]store.NotificationChannelRecord
}

func newFakeChannelStore() *fakeChannelStore {
	return &fakeChannelStore{records: map[string]store.NotificationChannelRecord{}}
}

func (f *fakeChannelStore) List(context.Context) ([]store.NotificationChannelRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.NotificationChannelRecord, 0, len(f.records))
	for _, r := range f.records {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeChannelStore) Get(_ context.Context, id string) (store.NotificationChannelRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.records[id]
	if !ok {
		return store.NotificationChannelRecord{}, store.ErrNotFound
	}
	return r, nil
}

func (f *fakeChannelStore) Upsert(_ context.Context, ch store.NotificationChannelRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ch.ID == "" {
		ch.ID = "fake-" + ch.Name
	}
	f.records[ch.ID] = ch
	return nil
}

func (f *fakeChannelStore) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.records, id)
	return nil
}

// httptestServer 返回一个记请求数、恒返回 200 的 sink。
func httptestServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	return ts, &calls
}

// TestLoadSeedsFromEnv 验证 DB 为空时回退 env 通道，通知照常扇出。
// 不能并行：t.Setenv 修改进程级环境变量。
func TestLoadSeedsFromEnv(t *testing.T) {
	ts, calls := httptestServer(t)
	t.Setenv("COPILOT_FEISHU_WEBHOOK_URL", ts.URL)
	t.Setenv("COPILOT_WEBHOOK_URL", "")
	t.Setenv("COPILOT_WEBHOOK_SECRET", "")

	m := NewChannelManager(newFakeChannelStore())
	if err := m.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	chs := m.List()
	if len(chs) != 1 || chs[0].ID != "env-feishu" || chs[0].Type != "feishu" {
		t.Fatalf("seed channels = %+v, want [env-feishu]", chs)
	}
	if err := m.NotifyConfirmation(context.Background(), ConfirmationRequest{PlanID: "p1"}); err != nil {
		t.Fatalf("NotifyConfirmation: %v", err)
	}
	if *calls != 1 {
		t.Errorf("feishu sink calls = %d, want 1", *calls)
	}
}

// TestUpsertDeleteReloadsFanout 验证增删通道即时热更新（不重启）。
func TestUpsertDeleteReloadsFanout(t *testing.T) {
	t.Parallel()
	ts1, c1 := httptestServer(t)
	ts2, c2 := httptestServer(t)

	m := NewChannelManager(newFakeChannelStore())
	if err := m.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	ctx := context.Background()
	req := ConfirmationRequest{PlanID: "p1"}

	// 新增通道 1 → 扇出到 sink1（DB 非空后 env 回退失效）。
	if _, err := m.Upsert(ctx, store.NotificationChannelRecord{
		Type: "webhook", Name: "gw1", URL: ts1.URL, Enabled: true,
	}); err != nil {
		t.Fatalf("Upsert gw1: %v", err)
	}
	if err := m.NotifyConfirmation(ctx, req); err != nil {
		t.Fatalf("notify after gw1: %v", err)
	}
	if *c1 != 1 {
		t.Errorf("sink1 calls = %d, want 1", *c1)
	}

	// 新增通道 2 → 两个 sink 都收到。
	if _, err := m.Upsert(ctx, store.NotificationChannelRecord{
		Type: "webhook", Name: "gw2", URL: ts2.URL, Enabled: true,
	}); err != nil {
		t.Fatalf("Upsert gw2: %v", err)
	}
	if err := m.NotifyConfirmation(ctx, req); err != nil {
		t.Fatalf("notify after gw2: %v", err)
	}
	if *c1 != 2 || *c2 != 1 {
		t.Errorf("calls = (%d, %d), want (2, 1)", *c1, *c2)
	}

	// 删除通道 1 → 只到 sink2。
	gw1ID := ""
	for _, c := range m.List() {
		if c.Name == "gw1" {
			gw1ID = c.ID
		}
	}
	if err := m.Delete(ctx, gw1ID); err != nil {
		t.Fatalf("Delete gw1: %v", err)
	}
	if err := m.NotifyConfirmation(ctx, req); err != nil {
		t.Fatalf("notify after delete: %v", err)
	}
	if *c1 != 2 || *c2 != 2 {
		t.Errorf("calls = (%d, %d), want (2, 2)", *c1, *c2)
	}
}

// TestDisabledChannelNotFanOut 验证 disabled 通道不参与扇出（日志兜底仍返回 nil）。
func TestDisabledChannelNotFanOut(t *testing.T) {
	t.Parallel()
	ts, calls := httptestServer(t)
	m := NewChannelManager(newFakeChannelStore())
	if err := m.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := m.Upsert(context.Background(), store.NotificationChannelRecord{
		Type: "webhook", Name: "off", URL: ts.URL, Enabled: false,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := m.NotifyConfirmation(context.Background(), ConfirmationRequest{PlanID: "p1"}); err != nil {
		t.Fatalf("NotifyConfirmation: %v", err)
	}
	if *calls != 0 {
		t.Errorf("disabled sink calls = %d, want 0", *calls)
	}
}

// TestChannelManagerCustomTemplate 验证模板从通道配置穿透到请求体。
func TestChannelManagerCustomTemplate(t *testing.T) {
	t.Parallel()
	var gotBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	m := NewChannelManager(newFakeChannelStore())
	if err := m.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := m.Upsert(context.Background(), store.NotificationChannelRecord{
		Type: "webhook", Name: "tmpl", URL: ts.URL, Enabled: true,
		Template: `{"plan":"{{.PlanID}}","who":"{{.Subject}}"}`,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := m.NotifyConfirmation(context.Background(), ConfirmationRequest{PlanID: "p1", Subject: "ops"}); err != nil {
		t.Fatalf("NotifyConfirmation: %v", err)
	}
	if string(gotBody) != `{"plan":"p1","who":"ops"}` {
		t.Fatalf("rendered body = %s", gotBody)
	}
}

// TestNotifySucceedsWithFailingChannel 验证单个失败通道不阻断（日志兜底成功）。
func TestNotifySucceedsWithFailingChannel(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(ts.Close)

	m := NewChannelManager(newFakeChannelStore())
	if err := m.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := m.Upsert(context.Background(), store.NotificationChannelRecord{
		Type: "webhook", Name: "bad", URL: ts.URL, Enabled: true,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := m.NotifyConfirmation(context.Background(), ConfirmationRequest{PlanID: "p1"}); err != nil {
		t.Fatalf("NotifyConfirmation = %v, want nil (log notifier succeeds)", err)
	}
}
