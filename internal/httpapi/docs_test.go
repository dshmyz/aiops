package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
)

// docsRouter builds a router wrapped in the configured docs directory.
func docsRouter(t *testing.T) http.Handler {
	t.Helper()
	// Tests run with cwd = internal/httpapi, so the repo root is two levels up.
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	t.Setenv("COPILOT_DOCS_DIR", filepath.Join(repoRoot, "docs"))
	return httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		nil,
	)
}

func docsGet(t *testing.T, router http.Handler, path, role string, hasRole bool) *httptest.ResponseRecorder {
	t.Helper()
	payload := map[string]any{"sub": "admin-1"}
	if hasRole {
		payload["roles"] = []string{role}
	}
	token := signedJWT(t, payload)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	return res
}

// TestServeDocsOperationManualServesMarkdown verifies GET /v1/docs/OPERATIONS.md
// returns the operations manual content to an admin.
func TestServeDocsOperationManualServesMarkdown(t *testing.T) {
	router := docsRouter(t)
	res := docsGet(t, router, "/v1/docs/OPERATIONS.md", "admin", true)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", res.Code, res.Body.String())
	}
	var out struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Name != "OPERATIONS.md" {
		t.Fatalf("name = %q, want OPERATIONS.md", out.Name)
	}
	if out.Content == "" {
		t.Fatal("content should not be empty")
	}
	// Sanity: manual should mention the launch checklist section.
	if !strings.Contains(out.Content, "上线前检查清单") {
		t.Fatalf("content missing expected section (len=%d)", len(out.Content))
	}
}

// TestServeDocsNonAdminForbidden verifies the docs endpoint is admin-only.
func TestServeDocsNonAdminForbidden(t *testing.T) {
	router := docsRouter(t)
	res := docsGet(t, router, "/v1/docs/OPERATIONS.md", "viewer", true)
	if res.Code != http.StatusForbidden {
		t.Fatalf("viewer status = %d, want 403", res.Code)
	}
}

// TestServeDocsUnauthenticated verifies requests without a token are rejected.
func TestServeDocsUnauthenticated(t *testing.T) {
	router := docsRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/docs/OPERATIONS.md", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.Code)
	}
}

// TestServeDocsRejectsUnknownAndTraversalNames verifies the allow-list and
// path-traversal guards reject anything other than the whitelisted doc.
func TestServeDocsRejectsUnknownAndTraversalNames(t *testing.T) {
	router := docsRouter(t)
	cases := []string{
		"/v1/docs/assistant.md", // known doc, but not in the allow-list
		"/v1/docs/../config.prod.yaml.example",
		"/v1/docs/%2e%2e%2fconfig.prod.yaml.example",
		"/v1/docs/README.md",
		"/v1/docs/",
	}
	for _, p := range cases {
		res := docsGet(t, router, p, "admin", true)
		// ServeMux 会对含 ../ 的路径做清洗并 307 重定向（清洗后落到兜底 404，
		// 同样不可达）；其余未知/越权名单路径仍直接 404。
		if res.Code != http.StatusNotFound && res.Code != http.StatusTemporaryRedirect {
			t.Fatalf("path %q status = %d, want 404 or 307", p, res.Code)
		}
	}
}
