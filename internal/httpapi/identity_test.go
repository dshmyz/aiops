package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
)

// identityRouter builds a router with JWT auth (any authenticated user may see
// their own identity — no admin gate).
func identityRouter() http.Handler {
	return httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		nil,
	)
}

func identityGet(t *testing.T, router http.Handler, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/identity/me", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	return res
}

// TestServeIdentityMeReturnsSubjectAndRoles verifies a logged-in caller can
// read back their own subject + roles.
func TestServeIdentityMeReturnsSubjectAndRoles(t *testing.T) {
	router := identityRouter()
	token := signedJWT(t, map[string]any{
		"sub":   "goryun",
		"roles": []string{"admin", "operator"},
	})
	res := identityGet(t, router, token)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", res.Code, res.Body.String())
	}
	var body struct {
		Subject string   `json:"subject"`
		Roles   []string `json:"roles"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Subject != "goryun" {
		t.Errorf("subject = %q, want %q", body.Subject, "goryun")
	}
	if len(body.Roles) != 2 || body.Roles[0] != "admin" || body.Roles[1] != "operator" {
		t.Errorf("roles = %v, want [admin operator]", body.Roles)
	}
}

// TestServeIdentityMeRequiresAuth verifies an unauthenticated request gets 401.
func TestServeIdentityMeRequiresAuth(t *testing.T) {
	router := identityRouter()
	res := identityGet(t, router, "")

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.Code)
	}
}

// TestServeIdentityMeViewerAllowed verifies a non-admin (viewer) can also see
// their own identity — the endpoint is not admin-gated.
func TestServeIdentityMeViewerAllowed(t *testing.T) {
	router := identityRouter()
	token := signedJWT(t, map[string]any{
		"sub":   "viewer-1",
		"roles": []string{"viewer"},
	})
	res := identityGet(t, router, token)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", res.Code, res.Body.String())
	}
}

// devIdentityRouter 开启 dev admin 身份兜底，模拟「浏览器直连内嵌 SPA」形态。
func devIdentityRouter() http.Handler {
	return httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		nil,
		httpapi.WithDevelopmentAdminIdentity(),
	)
}

// TestServeIdentityMeDevAdminFallback verifies that with the dev switch on, an
// unauthenticated request (no Authorization) succeeds as the fixed dev admin,
// mirroring how the stateless SPA pages work in local dev without a proxy.
func TestServeIdentityMeDevAdminFallback(t *testing.T) {
	router := devIdentityRouter()
	res := identityGet(t, router, "")

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", res.Code, res.Body.String())
	}
	var body struct {
		Subject string   `json:"subject"`
		Roles   []string `json:"roles"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Subject != "admin-1" {
		t.Errorf("subject = %q, want %q (dev admin)", body.Subject, "admin-1")
	}
	if len(body.Roles) != 1 || body.Roles[0] != "admin" {
		t.Errorf("roles = %v, want [admin]", body.Roles)
	}
}

// TestServeIdentityMeDevAdminDoesNotOverrideExplicitAuth verifies the dev switch
// never masks an explicit-but-invalid Authorization header — a caller's explicit
// intent still gets 401, and a valid JWT is honored (not replaced).
func TestServeIdentityMeDevAdminDoesNotOverrideExplicitAuth(t *testing.T) {
	router := devIdentityRouter()

	// 显式带了一个非法头 → 仍 401，不被 dev 兜底吞掉。
	res := identityGet(t, router, "bogus-token")
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("invalid explicit token: status = %d, want 401 (body: %s)", res.Code, res.Body.String())
	}

	// 合法 JWT → 用真实 subject，而非注入的 admin-1。
	token := signedJWT(t, map[string]any{
		"sub":   "goryun",
		"roles": []string{"operator"},
	})
	res = identityGet(t, router, token)
	if res.Code != http.StatusOK {
		t.Fatalf("valid token: status = %d, want 200 (body: %s)", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"subject":"goryun"`) {
		t.Errorf("valid token should report real subject goryun, got body: %s", res.Body.String())
	}
}
