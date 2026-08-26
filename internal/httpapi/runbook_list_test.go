package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

func seededRunbookStore() *store.MemoryRunbookStore {
	s := store.NewMemoryRunbookStore()
	// enabled + low
	_, _ = s.CreateRunbook(context.Background(), store.Runbook{
		Slug:         "minio-retention-low-risk",
		Name:         "MinIO 保留期低风险清理",
		RiskLevel:    "low",
		IsEnabled:    true,
		ToolSequence: []string{"bucket.retention.set"},
	})
	// enabled + medium（不可调度，不应出现在列表）
	_, _ = s.CreateRunbook(context.Background(), store.Runbook{
		Slug:         "kafka-retention-low-risk",
		Name:         "Kafka 保留期",
		RiskLevel:    "medium",
		IsEnabled:    true,
		ToolSequence: []string{"topic.retention.set"},
	})
	return s
}

// TestRunbooksListAllowsViewer verifies GET /v1/runbooks is readable by a
// logged-in user and only surfaces enabled + low-risk templates.
func TestRunbooksListAllowsViewer(t *testing.T) {
	t.Parallel()
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithRunbooks(seededRunbookStore()),
	)
	req := signedRequest(t, "/v1/runbooks", "", "viewer-1", []string{"viewer"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"configured":true`) {
		t.Fatalf("body = %s, want configured:true", body)
	}
	if !strings.Contains(body, `"minio-retention-low-risk"`) {
		t.Fatalf("body = %s, want low-risk runbook listed", body)
	}
	// medium 模板不应暴露（定时写安全边界，只能调度 low）。
	if strings.Contains(body, `"kafka-retention-low-risk"`) {
		t.Fatalf("body = %s, medium runbook must NOT be listed", body)
	}
}

// TestRunbooksListRequiresAuthentication verifies unauthenticated access is rejected.
func TestRunbooksListRequiresAuthentication(t *testing.T) {
	t.Parallel()
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithRunbooks(seededRunbookStore()),
	)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/runbooks", nil))

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
}

// TestRunbooksListConfiguredFalse verifies graceful degradation when the
// runbook store is not wired into the router.
func TestRunbooksListConfiguredFalse(t *testing.T) {
	t.Parallel()
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
	)
	req := signedRequest(t, "/v1/runbooks", "", "viewer-1", []string{"viewer"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"configured":false`) {
		t.Fatalf("body = %s, want configured:false", res.Body.String())
	}
}
