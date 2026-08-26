package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
)

// TestHTTPForbiddenRecordsAudit verifies that a role-check 403 writes an
// http_forbidden audit event with the caller subject (R2: HTTP 403 权限拒绝审计).
func TestHTTPForbiddenRecordsAudit(t *testing.T) {
	t.Parallel()
	router, repository := testRouter(t, &readRunner{})

	// viewer 访问 admin-only 的 /v1/executions → 403 + audit
	req := signedRequest(t, "/v1/executions", "", "viewer-1", []string{"viewer"})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", res.Code, res.Body.String())
	}
	events := repository.AuditEvents()
	found := false
	for _, ev := range events {
		if ev.Action == audit.ActionHTTPForbidden && ev.Subject == "viewer-1" {
			found = true
			if ev.Decision != audit.DecisionPermissionDenied {
				t.Errorf("decision = %q, want permission_denied", ev.Decision)
			}
			if ev.Metadata["path"] != "/v1/executions" {
				t.Errorf("path = %v, want /v1/executions", ev.Metadata["path"])
			}
		}
	}
	if !found {
		t.Errorf("no http_forbidden audit event for viewer-1; events = %+v", events)
	}
}
