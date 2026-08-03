package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// casAuthRouter 构建带 CAS auth + audit store 的 router，用于验证登录/登出审计。
func casAuthRouter(t *testing.T) (*CASAuthenticator, http.Handler, *store.MemoryActionPlanStore) {
	t.Helper()
	casServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/p3/serviceValidate" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("ticket") != "ST-valid-123" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas">
  <cas:authenticationFailure code="INVALID_TICKET">Ticket not recognized</cas:authenticationFailure>
</cas:serviceResponse>`)
			return
		}
		fmt.Fprint(w, `<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas">
  <cas:authenticationSuccess>
    <cas:user>zhangsan</cas:user>
    <cas:attributes>
      <cas:roles>admin,operator</cas:roles>
    </cas:attributes>
  </cas:authenticationSuccess>
</cas:serviceResponse>`)
	}))
	t.Cleanup(casServer.Close)

	auth, err := NewCASAuthenticator(CASConfig{
		ServerURL:     casServer.URL,
		ServiceURL:    "http://localhost:5173",
		SessionSecret: []byte("test-secret"),
		HTTPClient:    casServer.Client(),
	})
	if err != nil {
		t.Fatalf("NewCASAuthenticator: %v", err)
	}
	multi := NewMultiAuthenticator(AuthModeCAS, nil, auth)

	repository := store.NewMemoryActionPlanStore()
	auditService := audit.NewService(repository)
	router := NewRouter(
		multi,
		nil,
		WithAuditEvents(auditService),
	)
	return auth, router, repository
}

func TestCASAuthLoginRecordsAudit(t *testing.T) {
	t.Parallel()
	_, router, repository := casAuthRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/cas/callback?ticket=ST-valid-123", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 redirect", res.Code)
	}
	events := repository.AuditEvents()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1 (login)", len(events))
	}
	ev := events[0]
	if ev.Action != audit.ActionAuthLogin {
		t.Errorf("action = %q, want auth_login", ev.Action)
	}
	if ev.Subject != "zhangsan" {
		t.Errorf("subject = %q, want zhangsan", ev.Subject)
	}
}

func TestCASAuthLogoutRecordsAudit(t *testing.T) {
	t.Parallel()
	auth, router, repository := casAuthRouter(t)

	// 先造一个有效 session cookie，模拟已登录用户执行 logout
	user, cookieValue, err := auth.ValidateTicket("ST-valid-123")
	if err != nil {
		t.Fatalf("ValidateTicket: %v", err)
	}
	cookie := auth.SessionCookie(cookieValue)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/cas/logout", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 redirect", res.Code)
	}
	events := repository.AuditEvents()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1 (logout)", len(events))
	}
	ev := events[0]
	if ev.Action != audit.ActionAuthLogout {
		t.Errorf("action = %q, want auth_logout", ev.Action)
	}
	if ev.Subject != user.Subject {
		t.Errorf("subject = %q, want %q", ev.Subject, user.Subject)
	}
}
