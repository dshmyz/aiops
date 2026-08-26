package e2e_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestReadOnlyToolEndpointUsesSQLiteAuditStore(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:e2e_readonly?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if err := store.ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}

	repository := store.NewSQLActionPlanStore(db)
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(e2eReadRunner{}, audit.NewService(repository)),
	)
	req := httptest.NewRequest(http.MethodPost, "/v1/tools/cluster.status.read/read", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+signedJWT(t))
	req.Header.Set("X-Request-ID", "e2e-request")
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	var auditCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM copilot_audit_events WHERE request_id = 'e2e-request' AND decision = 'permitted'`).Scan(&auditCount); err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("audit count = %d, want 1", auditCount)
	}
}

type e2eReadRunner struct{}

func (e2eReadRunner) Read(_ context.Context, tool tools.Tool, input map[string]any) (map[string]any, error) {
	return map[string]any{"tool": tool.Name, "status": "green"}, nil
}

func signedJWT(t *testing.T) string {
	t.Helper()
	header := encodeSegment(t, map[string]any{"alg": "HS256", "typ": "JWT"})
	claims := encodeSegment(t, map[string]any{
		"sub":   "operator-1",
		"roles": []string{"viewer"},
	})
	unsigned := header + "." + claims
	mac := hmac.New(sha256.New, []byte("test-secret"))
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func encodeSegment(t *testing.T, value map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal jwt segment: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded)
}
