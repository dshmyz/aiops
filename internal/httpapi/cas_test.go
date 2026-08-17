package httpapi_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
)

// --- CAS ticket validation tests ---

func TestCASValidateTicketSuccess(t *testing.T) {
	// Mock CAS server returning a successful CAS 3.0 validation response.
	casServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/p3/serviceValidate" {
			http.NotFound(w, r)
			return
		}
		ticket := r.URL.Query().Get("ticket")
		if ticket != "ST-valid-123" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas">
  <cas:authenticationFailure code="INVALID_TICKET">Ticket not recognized</cas:authenticationFailure>
</cas:serviceResponse>`)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas">
  <cas:authenticationSuccess>
    <cas:user>zhangsan</cas:user>
    <cas:attributes>
      <cas:roles>admin,operator</cas:roles>
      <cas:email>zhangsan@example.com</cas:email>
    </cas:attributes>
  </cas:authenticationSuccess>
</cas:serviceResponse>`)
	}))
	defer casServer.Close()

	auth, err := httpapi.NewCASAuthenticator(httpapi.CASConfig{
		ServerURL:     casServer.URL,
		ServiceURL:    "http://localhost:5173",
		SessionSecret: []byte("test-secret"),
		HTTPClient:    casServer.Client(),
	})
	if err != nil {
		t.Fatalf("NewCASAuthenticator: %v", err)
	}

	user, cookie, err := auth.ValidateTicket("ST-valid-123")
	if err != nil {
		t.Fatalf("ValidateTicket: %v", err)
	}
	if user.Subject != "zhangsan" {
		t.Errorf("subject = %q, want zhangsan", user.Subject)
	}
	if len(user.Roles) != 2 || user.Roles[0] != "admin" || user.Roles[1] != "operator" {
		t.Errorf("roles = %v, want [admin operator]", user.Roles)
	}
	if cookie == "" {
		t.Error("expected non-empty session cookie value")
	}
}

func TestCASValidateTicketInvalid(t *testing.T) {
	casServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas">
  <cas:authenticationFailure code="INVALID_TICKET">Ticket ST-bad not recognized</cas:authenticationFailure>
</cas:serviceResponse>`)
	}))
	defer casServer.Close()

	auth, err := httpapi.NewCASAuthenticator(httpapi.CASConfig{
		ServerURL:     casServer.URL,
		ServiceURL:    "http://localhost:5173",
		SessionSecret: []byte("test-secret"),
		HTTPClient:    casServer.Client(),
	})
	if err != nil {
		t.Fatalf("NewCASAuthenticator: %v", err)
	}

	_, _, err = auth.ValidateTicket("ST-bad")
	if err == nil {
		t.Fatal("expected error for invalid ticket")
	}
	// 失败响应的 code 是属性（<cas:authenticationFailure code="...">），必须用
	// xml:"code,attr" 才能读到；报错应带上错误码而非笼统的 missing username。
	if got := err.Error(); !strings.Contains(got, "INVALID_TICKET") || strings.Contains(got, "missing username") {
		t.Errorf("error = %q, want CAS failure with code INVALID_TICKET", got)
	}
}

func TestCASValidateTicketDefaultRoles(t *testing.T) {
	// When CAS response has no roles attribute, default roles are used.
	casServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas">
  <cas:authenticationSuccess>
    <cas:user>lisi</cas:user>
  </cas:authenticationSuccess>
</cas:serviceResponse>`)
	}))
	defer casServer.Close()

	auth, err := httpapi.NewCASAuthenticator(httpapi.CASConfig{
		ServerURL:     casServer.URL,
		ServiceURL:    "http://localhost:5173",
		SessionSecret: []byte("test-secret"),
		HTTPClient:    casServer.Client(),
	})
	if err != nil {
		t.Fatalf("NewCASAuthenticator: %v", err)
	}

	user, _, err := auth.ValidateTicket("ST-any")
	if err != nil {
		t.Fatalf("ValidateTicket: %v", err)
	}
	if user.Subject != "lisi" {
		t.Errorf("subject = %q, want lisi", user.Subject)
	}
	// Default roles should be ["operator"].
	if len(user.Roles) != 1 || user.Roles[0] != "operator" {
		t.Errorf("roles = %v, want [operator]", user.Roles)
	}
}

// --- Session cookie round-trip ---

func TestCASSessionCookieRoundTrip(t *testing.T) {
	casServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas">
  <cas:authenticationSuccess>
    <cas:user>admin-user</cas:user>
    <cas:attributes>
      <cas:roles>admin</cas:roles>
    </cas:attributes>
  </cas:authenticationSuccess>
</cas:serviceResponse>`)
	}))
	defer casServer.Close()

	auth, err := httpapi.NewCASAuthenticator(httpapi.CASConfig{
		ServerURL:     casServer.URL,
		ServiceURL:    "http://localhost:5173",
		SessionSecret: []byte("round-trip-secret"),
		HTTPClient:    casServer.Client(),
	})
	if err != nil {
		t.Fatalf("NewCASAuthenticator: %v", err)
	}

	_, cookieValue, err := auth.ValidateTicket("ST-xxx")
	if err != nil {
		t.Fatalf("ValidateTicket: %v", err)
	}

	// Simulate a request carrying the session cookie.
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/prompts", nil)
	req.AddCookie(&http.Cookie{Name: "copilot_cas_session", Value: cookieValue})
	req.Header.Set("X-Request-ID", "test-req-1")

	user, err := auth.Authenticate(req)
	if err != nil {
		t.Fatalf("Authenticate with session cookie: %v", err)
	}
	if user.Subject != "admin-user" {
		t.Errorf("subject = %q, want admin-user", user.Subject)
	}
	if user.Roles[0] != "admin" {
		t.Errorf("roles[0] = %q, want admin", user.Roles[0])
	}
}

func TestCASSessionCookieTampered(t *testing.T) {
	auth, err := httpapi.NewCASAuthenticator(httpapi.CASConfig{
		ServerURL:     "https://cas.example.com",
		ServiceURL:    "http://localhost:5173",
		SessionSecret: []byte("secret"),
	})
	if err != nil {
		t.Fatalf("NewCASAuthenticator: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.AddCookie(&http.Cookie{Name: "copilot_cas_session", Value: "tampered.cookie"})

	_, err = auth.Authenticate(req)
	if err == nil {
		t.Fatal("expected error for tampered cookie")
	}
}

// --- Login URL generation ---

func TestCASLoginURL(t *testing.T) {
	auth, err := httpapi.NewCASAuthenticator(httpapi.CASConfig{
		ServerURL:     "https://cas.example.com/cas",
		ServiceURL:    "https://copilot.example.com",
		SessionSecret: []byte("secret"),
	})
	if err != nil {
		t.Fatalf("NewCASAuthenticator: %v", err)
	}

	loginURL := auth.LoginURL()
	want := "https://cas.example.com/cas/login?service=https%3A%2F%2Fcopilot.example.com%2Fv1%2Fauth%2Fcas%2Fcallback"
	if loginURL != want {
		t.Errorf("LoginURL = %q, want %q", loginURL, want)
	}
}

// --- MultiAuthenticator mode dispatch ---

func TestMultiAuthenticatorJWTMode(t *testing.T) {
	jwt := httpapi.NewHMACAuthenticator([]byte("test-secret"))
	multi := httpapi.NewMultiAuthenticator(httpapi.AuthModeJWT, jwt, nil)

	// No Authorization header → error.
	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	_, err := multi.Authenticate(req)
	if err == nil {
		t.Fatal("expected error in JWT mode without token")
	}
}

func TestMultiAuthenticatorBothModeFallsBackToCAS(t *testing.T) {
	casServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas">
  <cas:authenticationSuccess>
    <cas:user>fallback-user</cas:user>
  </cas:authenticationSuccess>
</cas:serviceResponse>`)
	}))
	defer casServer.Close()

	casAuth, err := httpapi.NewCASAuthenticator(httpapi.CASConfig{
		ServerURL:     casServer.URL,
		ServiceURL:    "http://localhost:5173",
		SessionSecret: []byte("both-secret"),
		HTTPClient:    casServer.Client(),
	})
	if err != nil {
		t.Fatalf("NewCASAuthenticator: %v", err)
	}

	// Get a valid session cookie.
	_, cookieValue, err := casAuth.ValidateTicket("ST-test")
	if err != nil {
		t.Fatalf("ValidateTicket: %v", err)
	}

	jwt := httpapi.NewHMACAuthenticator([]byte("jwt-secret"))
	multi := httpapi.NewMultiAuthenticator(httpapi.AuthModeBoth, jwt, casAuth)

	// Request with CAS cookie but no JWT → should succeed via CAS fallback.
	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.AddCookie(&http.Cookie{Name: "copilot_cas_session", Value: cookieValue})
	req.Header.Set("X-Request-ID", "req-both-1")

	user, err := multi.Authenticate(req)
	if err != nil {
		t.Fatalf("Authenticate in both mode with CAS cookie: %v", err)
	}
	if user.Subject != "fallback-user" {
		t.Errorf("subject = %q, want fallback-user", user.Subject)
	}
}

// --- Router CAS endpoint integration ---

func TestRouterAuthConfigEndpoint(t *testing.T) {
	jwt := httpapi.NewHMACAuthenticator([]byte("secret"))
	multi := httpapi.NewMultiAuthenticator(httpapi.AuthModeJWT, jwt, nil)
	handler := httpapi.NewRouter(multi, nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/config", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !contains(body, `"mode":"jwt"`) {
		t.Errorf("body = %s, want mode jwt", body)
	}
}

func TestRouterCASCallbackMissingTicket(t *testing.T) {
	casAuth, err := httpapi.NewCASAuthenticator(httpapi.CASConfig{
		ServerURL:     "https://cas.example.com",
		ServiceURL:    "http://localhost:5173",
		SessionSecret: []byte("secret"),
	})
	if err != nil {
		t.Fatalf("NewCASAuthenticator: %v", err)
	}
	jwt := httpapi.NewHMACAuthenticator([]byte("secret"))
	multi := httpapi.NewMultiAuthenticator(httpapi.AuthModeBoth, jwt, casAuth)
	handler := httpapi.NewRouter(multi, nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/cas/callback", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestRouterCASLoginRedirect(t *testing.T) {
	casAuth, err := httpapi.NewCASAuthenticator(httpapi.CASConfig{
		ServerURL:     "https://cas.example.com/cas",
		ServiceURL:    "http://localhost:5173",
		SessionSecret: []byte("secret"),
	})
	if err != nil {
		t.Fatalf("NewCASAuthenticator: %v", err)
	}
	jwt := httpapi.NewHMACAuthenticator([]byte("secret"))
	multi := httpapi.NewMultiAuthenticator(httpapi.AuthModeBoth, jwt, casAuth)
	handler := httpapi.NewRouter(multi, nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/cas/login", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	location := rec.Header().Get("Location")
	if location == "" {
		t.Fatal("expected Location header")
	}
	if !contains(location, "cas.example.com/cas/login") {
		t.Errorf("Location = %q, want CAS login URL", location)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestCASInsecureSkipVerify 钉住 CAS ticket 校验的 TLS 证书校验开关：默认校验证书，
// 对接自签 HTTPS 的 CAS 服务器会 TLS 握手失败；InsecureSkipVerify=true 时跳过
// 证书校验、可正常完成校验流程（走到 CAS 业务判定而非握手错误）。
func TestCASInsecureSkipVerify(t *testing.T) {
	// httptest.NewTLSServer 使用自签证书：默认客户端校验证书必然失败。
	casServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas">
  <cas:authenticationFailure code="INVALID_TICKET">Ticket not recognized</cas:authenticationFailure>
</cas:serviceResponse>`)
	}))
	defer casServer.Close()

	// 默认：校验证书 → TLS 握手报 x509 自签错误。
	auth, err := httpapi.NewCASAuthenticator(httpapi.CASConfig{
		ServerURL:     casServer.URL,
		ServiceURL:    "http://localhost:5173",
		SessionSecret: []byte("test-secret"),
	})
	if err != nil {
		t.Fatalf("NewCASAuthenticator: %v", err)
	}
	if _, _, err := auth.ValidateTicket("ST-x"); err == nil || !strings.Contains(err.Error(), "x509") {
		t.Fatalf("default client: err = %v, want x509 certificate error", err)
	}

	// InsecureSkipVerify=true：跳过证书校验 → 握手成功，请求到达 CAS 服务器并得到
	// CAS 层判定（拒绝票据），不再是 TLS 握手/x509 错误。
	insecure, err := httpapi.NewCASAuthenticator(httpapi.CASConfig{
		ServerURL:          casServer.URL,
		ServiceURL:         "http://localhost:5173",
		SessionSecret:      []byte("test-secret"),
		InsecureSkipVerify: true,
	})
	if err != nil {
		t.Fatalf("NewCASAuthenticator(insecure): %v", err)
	}
	if _, _, err := insecure.ValidateTicket("ST-x"); err == nil || strings.Contains(err.Error(), "x509") {
		t.Fatalf("insecure client: err = %v, want CAS-level rejection (no TLS certificate error)", err)
	}
}
