package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		"sub":                  "goryun",
		"roles":                []string{"admin", "operator"},
		"allowed_environments": []string{"prod"},
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
		"sub":                  "viewer-1",
		"roles":                []string{"viewer"},
		"allowed_environments": []string{"prod"},
	})
	res := identityGet(t, router, token)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", res.Code, res.Body.String())
	}
}
