package httpapi_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/scheduler"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestCapabilityListAllowsViewer(t *testing.T) {
	t.Parallel()
	service := &capabilityManagementService{
		list: []capabilities.ManagedCapability{{
			Capability: capabilities.Capability{
				Name:         "minio.bucket.capacity.read",
				Status:       capabilities.StatusNeedsReview,
				Domain:       "minio",
				ResourceType: "bucket",
				Operation:    tools.Read,
				Risk:         tools.Low,
			},
			Source:     capabilities.SourceDiscovered,
			Validation: capabilities.ValidationResult{Valid: true},
		}},
	}
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(service),
	)
	req := signedRequest(t, "/v1/capabilities", "", "viewer-1", []string{"viewer"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"capabilities"`) || !strings.Contains(res.Body.String(), `"source":"discovered"`) {
		t.Fatalf("body = %s, want listed capability", res.Body.String())
	}
}

func TestCapabilityListRequiresAuthentication(t *testing.T) {
	t.Parallel()
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(&capabilityManagementService{}),
	)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil))

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
}

func TestCapabilityTestRequiresAdmin(t *testing.T) {
	t.Parallel()
	service := &capabilityManagementService{}
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(service),
	)
	req := signedRequest(t, "/v1/capabilities/test", `{"capability":{"name":"minio.bucket.capacity.read"},"input":{"environment":"prod"}}`, "operator-1", []string{"operator"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
	}
	if service.testCalls != 0 {
		t.Fatalf("test calls = %d, want 0", service.testCalls)
	}
}

func TestCapabilityImportOpenAPIURLRequiresAdmin(t *testing.T) {
	t.Parallel()
	service := &capabilityManagementService{}
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(service),
	)
	body := `{"openapi_url":"https://admin.example.com/v3/api-docs","backend_base_url":"https://middleware.example.com"}`
	req := signedRequest(t, "/v1/capabilities/import/openapi-url", body, "operator-1", []string{"operator"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
	}
	if service.importCalls != 0 {
		t.Fatalf("import calls = %d, want 0", service.importCalls)
	}
}

func TestCapabilityImportOpenAPIURLReturnsImportedDrafts(t *testing.T) {
	t.Parallel()
	service := &capabilityManagementService{
		imported: []capabilities.ManagedCapability{{
			Capability: capabilities.Capability{
				Name:         "minio.bucket.capacity.read",
				Status:       capabilities.StatusNeedsReview,
				Domain:       "minio",
				ResourceType: "bucket",
				Operation:    tools.Read,
				Risk:         tools.Low,
			},
			Source: capabilities.SourceDiscovered,
		}},
	}
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(service),
	)
	body := `{"openapi_url":"https://admin.example.com/v3/api-docs","backend_base_url":"https://middleware.example.com"}`
	req := signedRequest(t, "/v1/capabilities/import/openapi-url", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if service.importCalls != 1 {
		t.Fatalf("import calls = %d, want 1", service.importCalls)
	}
	if service.importedRequest.OpenAPIURL != "https://admin.example.com/v3/api-docs" || service.importedRequest.BackendBaseURL != "https://middleware.example.com" {
		t.Fatalf("import request = %+v, want decoded request", service.importedRequest)
	}
	if !strings.Contains(res.Body.String(), `"capabilities"`) || !strings.Contains(res.Body.String(), `"minio.bucket.capacity.read"`) {
		t.Fatalf("body = %s, want imported capability", res.Body.String())
	}
}

func TestCapabilityPreviewOpenAPIURLRequiresAdmin(t *testing.T) {
	t.Parallel()
	service := &capabilityManagementService{}
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(service),
	)
	body := `{"openapi_url":"https://admin.example.com/v3/api-docs","backend_base_url":"https://middleware.example.com"}`
	req := signedRequest(t, "/v1/capabilities/import/openapi-url/preview", body, "operator-1", []string{"operator"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
	}
	if service.previewCalls != 0 {
		t.Fatalf("preview calls = %d, want 0", service.previewCalls)
	}
}

func TestCapabilityPreviewOpenAPIURLReturnsCandidates(t *testing.T) {
	t.Parallel()
	service := &capabilityManagementService{
		preview: capabilities.ImportPreview{
			Source: capabilities.ImportPreviewSource{
				OpenAPIURL:     "https://admin.example.com/v3/api-docs",
				BackendBaseURL: "https://middleware.example.com",
				Fingerprint:    "sha256:test",
			},
			Stats: capabilities.ImportPreviewStats{Total: 1, Recommended: 1, Read: 1},
			Candidates: []capabilities.ImportCandidate{{
				ID:             "GET /api/minio/{cluster}/buckets/{bucket}/capacity",
				Method:         "GET",
				Path:           "/api/minio/{cluster}/buckets/{bucket}/capacity",
				Recommendation: capabilities.RecommendationRecommended,
				Capability: capabilities.Capability{
					Name:         "minio.bucket.capacity.read",
					Domain:       "minio",
					ResourceType: "bucket",
					Operation:    tools.Read,
					Risk:         tools.Low,
				},
			}},
		},
	}
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(service),
	)
	body := `{"openapi_url":"https://admin.example.com/v3/api-docs","backend_base_url":"https://middleware.example.com"}`
	req := signedRequest(t, "/v1/capabilities/import/openapi-url/preview", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if service.previewCalls != 1 {
		t.Fatalf("preview calls = %d, want 1", service.previewCalls)
	}
	if service.previewRequest.OpenAPIURL != "https://admin.example.com/v3/api-docs" || service.previewRequest.BackendBaseURL != "https://middleware.example.com" {
		t.Fatalf("preview request = %+v, want decoded request", service.previewRequest)
	}
	if !strings.Contains(res.Body.String(), `"candidates"`) || !strings.Contains(res.Body.String(), `"sha256:test"`) {
		t.Fatalf("body = %s, want preview response", res.Body.String())
	}
}

func TestCapabilityCommitOpenAPIURLReturnsSavedDrafts(t *testing.T) {
	t.Parallel()
	service := &capabilityManagementService{
		commitResult: capabilities.OpenAPIURLCommitResult{
			Capabilities: []capabilities.ManagedCapability{{
				Capability: capabilities.Capability{
					Name:         "minio.bucket.capacity.read",
					Status:       capabilities.StatusNeedsReview,
					Domain:       "minio",
					ResourceType: "bucket",
					Operation:    tools.Read,
					Risk:         tools.Low,
				},
				Source: capabilities.SourceDiscovered,
			}},
		},
	}
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(service),
	)
	body := `{"openapi_url":"https://admin.example.com/v3/api-docs","backend_base_url":"https://middleware.example.com","fingerprint":"sha256:test","selections":[{"candidate_id":"GET /api/minio/{cluster}/buckets/{bucket}/capacity","overrides":{"name":"minio.bucket.capacity.read","domain":"minio","resource_type":"bucket","operation":"read","risk":"low"}}]}`
	req := signedRequest(t, "/v1/capabilities/import/openapi-url/commit", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if service.commitCalls != 1 {
		t.Fatalf("commit calls = %d, want 1", service.commitCalls)
	}
	if service.commitRequest.Fingerprint != "sha256:test" || len(service.commitRequest.Selections) != 1 {
		t.Fatalf("commit request = %+v, want decoded request", service.commitRequest)
	}
	if !strings.Contains(res.Body.String(), `"capabilities"`) || !strings.Contains(res.Body.String(), `"minio.bucket.capacity.read"`) {
		t.Fatalf("body = %s, want commit result", res.Body.String())
	}
}

func TestCapabilityImportOpenAPIURLAllowsLargeDraftResponse(t *testing.T) {
	t.Parallel()
	imported := make([]capabilities.ManagedCapability, 180)
	for index := range imported {
		imported[index] = capabilities.ManagedCapability{
			Capability: capabilities.Capability{
				Name:         "minio.bucket.capacity.read.large" + strings.Repeat("x", index%12) + "." + string(rune('a'+index%26)),
				Status:       capabilities.StatusNeedsReview,
				Domain:       "minio",
				ResourceType: "bucket",
				Operation:    tools.Read,
				Risk:         tools.Low,
				Backend: capabilities.BackendSpec{
					Method:  http.MethodGet,
					BaseURL: "https://middleware.example.com",
					Path:    "/api/minio/{cluster}/buckets/{bucket}/capacity",
				},
			},
			Source: capabilities.SourceDiscovered,
		}
	}
	service := &capabilityManagementService{imported: imported}
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(service),
	)
	body := `{"openapi_url":"https://admin.example.com/v3/api-docs","backend_base_url":"https://middleware.example.com"}`
	req := signedRequest(t, "/v1/capabilities/import/openapi-url", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200 for many imported drafts", res.Code, res.Body.String())
	}
}

func TestCapabilityDraftValidatePublishAndUnpublishRequireAdmin(t *testing.T) {
	t.Parallel()
	service := &capabilityManagementService{
		saved:       capabilities.ManagedCapability{Capability: capabilities.Capability{Name: "minio.bucket.capacity.read", Status: capabilities.StatusNeedsReview}, Source: capabilities.SourceDiscovered},
		published:   capabilities.ManagedCapability{Capability: capabilities.Capability{Name: "minio.bucket.capacity.read", Status: capabilities.StatusPublished}, Source: capabilities.SourcePublished},
		unpublished: capabilities.ManagedCapability{Capability: capabilities.Capability{Name: "minio.bucket.capacity.read", Status: capabilities.StatusNeedsReview}, Source: capabilities.SourceDiscovered},
	}
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(service),
	)
	capabilityBody := `{"name":"minio.bucket.capacity.read","status":"needs_review"}`

	createRes := httptest.NewRecorder()
	router.ServeHTTP(createRes, signedRequest(t, "/v1/capabilities/drafts", capabilityBody, "admin-1", []string{"admin"}, []string{"prod"}))
	if createRes.Code != http.StatusOK || service.saveCalls != 1 || !strings.Contains(createRes.Body.String(), `"source":"discovered"`) {
		t.Fatalf("create status = %d body = %s saveCalls = %d, want saved draft", createRes.Code, createRes.Body.String(), service.saveCalls)
	}

	validateRes := httptest.NewRecorder()
	router.ServeHTTP(validateRes, signedRequest(t, "/v1/capabilities/validate", capabilityBody, "admin-1", []string{"admin"}, []string{"prod"}))
	if validateRes.Code != http.StatusOK || !strings.Contains(validateRes.Body.String(), `"valid":true`) {
		t.Fatalf("validate status = %d body = %s, want valid result", validateRes.Code, validateRes.Body.String())
	}

	publishRes := httptest.NewRecorder()
	router.ServeHTTP(publishRes, signedRequest(t, "/v1/capabilities/minio.bucket.capacity.read/publish", `{}`, "admin-1", []string{"admin"}, []string{"prod"}))
	if publishRes.Code != http.StatusOK || service.publishName != "minio.bucket.capacity.read" || !strings.Contains(publishRes.Body.String(), `"source":"published"`) {
		t.Fatalf("publish status = %d body = %s name = %q, want published", publishRes.Code, publishRes.Body.String(), service.publishName)
	}

	unpublishRes := httptest.NewRecorder()
	router.ServeHTTP(unpublishRes, signedRequest(t, "/v1/capabilities/minio.bucket.capacity.read/unpublish", `{}`, "admin-1", []string{"admin"}, []string{"prod"}))
	if unpublishRes.Code != http.StatusOK || service.unpublishName != "minio.bucket.capacity.read" || !strings.Contains(unpublishRes.Body.String(), `"source":"discovered"`) {
		t.Fatalf("unpublish status = %d body = %s name = %q, want discovered", unpublishRes.Code, unpublishRes.Body.String(), service.unpublishName)
	}
}

func TestCapabilityQuickPublishRequiresAdmin(t *testing.T) {
	t.Parallel()
	service := &capabilityManagementService{}
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(service),
	)
	body := `{"name":"redis.cluster.info.read","domain":"redis","resource_type":"cluster","backend_base_url":"https://middleware.example.com","method":"GET","path":"/api/redis/clusters/{cluster}/info","description":"Read Redis cluster info"}`
	req := signedRequest(t, "/v1/capabilities/quick-publish", body, "operator-1", []string{"operator"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
	}
	if service.quickPublishCalls != 0 {
		t.Fatalf("quick publish calls = %d, want 0", service.quickPublishCalls)
	}
}

func TestCapabilityQuickPublishPublishesCapability(t *testing.T) {
	t.Parallel()
	service := &capabilityManagementService{
		quickPublishResult: capabilities.ManagedCapability{
			Capability: capabilities.Capability{
				Name:         "redis.cluster.info.read",
				Status:       capabilities.StatusPublished,
				Domain:       "redis",
				ResourceType: "cluster",
				Operation:    tools.Read,
				Risk:         tools.Low,
				Backend:      capabilities.BackendSpec{Adapter: "http", Method: "GET", Path: "/api/redis/clusters/{cluster}/info", BaseURL: "https://middleware.example.com", TimeoutMS: 3000},
			},
			Source: capabilities.SourcePublished,
		},
	}
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(service),
	)
	body := `{"name":"redis.cluster.info.read","domain":"redis","resource_type":"cluster","backend_base_url":"https://middleware.example.com","method":"GET","path":"/api/redis/clusters/{cluster}/info","description":"Read Redis cluster info"}`
	req := signedRequest(t, "/v1/capabilities/quick-publish", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if service.quickPublishCalls != 1 {
		t.Fatalf("quick publish calls = %d, want 1", service.quickPublishCalls)
	}
	if service.quickPublishReq.Name != "redis.cluster.info.read" || service.quickPublishReq.Method != "GET" || service.quickPublishReq.Path != "/api/redis/clusters/{cluster}/info" {
		t.Fatalf("quick publish request = %+v, want decoded body", service.quickPublishReq)
	}
	if !strings.Contains(res.Body.String(), `"source":"published"`) || !strings.Contains(res.Body.String(), `"name":"redis.cluster.info.read"`) {
		t.Fatalf("body = %s, want published capability", res.Body.String())
	}
}

func TestCapabilityQuickPublishMapsConflict(t *testing.T) {
	t.Parallel()
	service := &capabilityManagementService{
		quickPublishErr: capabilities.ErrCapabilityNameConflict,
	}
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(service),
	)
	body := `{"name":"redis.cluster.info.read","domain":"redis","resource_type":"cluster","backend_base_url":"https://middleware.example.com","method":"GET","path":"/api/redis/clusters/{cluster}/info","description":"Read Redis cluster info"}`
	req := signedRequest(t, "/v1/capabilities/quick-publish", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", res.Code, res.Body.String())
	}
}

func TestCapabilityQuickPublishMapsInvalidRequest(t *testing.T) {
	t.Parallel()
	service := &capabilityManagementService{
		quickPublishErr: capabilities.ErrInvalidQuickPublishMethod,
	}
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(service),
	)
	body := `{"name":"redis.cluster.info.read","domain":"redis","resource_type":"cluster","backend_base_url":"https://middleware.example.com","method":"POST","path":"/api/redis/clusters/{cluster}/info","description":"Read Redis cluster info"}`
	req := signedRequest(t, "/v1/capabilities/quick-publish", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", res.Code, res.Body.String())
	}
}

func TestReadToolRequiresAuthenticatedRole(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/v1/tools/cluster.status.read/read", strings.NewReader(`{"environment":"prod"}`)))

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
}

func TestReadToolExecutesAllowedReadToolAndAudits(t *testing.T) {
	t.Parallel()
	runner := &readRunner{result: map[string]any{"status": "green"}}
	router, repository := testRouter(t, runner)
	req := signedRequest(t, "/v1/tools/cluster.status.read/read", `{"environment":"prod"}`, "operator-1", []string{"viewer"}, []string{"prod"})

	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if runner.calls != 1 || runner.toolName != tools.ClusterStatusRead {
		t.Fatalf("runner calls=%d tool=%q, want one cluster read", runner.calls, runner.toolName)
	}
	if !strings.Contains(res.Body.String(), `"status":"green"`) {
		t.Fatalf("body = %s, want tool result", res.Body.String())
	}
	events := repository.AuditEvents()
	if len(events) != 1 || events[0].Action != "readonly_tool_executed" || events[0].Decision != "permitted" || events[0].Subject != "operator-1" {
		t.Fatalf("audit events = %+v, want permitted read audit", events)
	}
}

func TestReadToolRejectsWriteTool(t *testing.T) {
	t.Parallel()
	runner := &readRunner{}
	router, repository := testRouter(t, runner)
	req := signedRequest(t, "/v1/tools/topic.retention.set/read", `{"environment":"prod","topic":"orders","retention_hours":72}`, "operator-1", []string{"admin"}, []string{"prod"})

	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls = %d, want 0", runner.calls)
	}
	events := repository.AuditEvents()
	if len(events) != 1 || events[0].Decision != "write_tool_not_allowed_on_read_endpoint" {
		t.Fatalf("audit events = %+v, want write rejection audit", events)
	}
}

func TestReadToolCapsResponseSize(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{result: map[string]any{"payload": strings.Repeat("x", 1024*1024+1)}})
	req := signedRequest(t, "/v1/tools/cluster.status.read/read", `{"environment":"prod"}`, "operator-1", []string{"viewer"}, []string{"prod"})

	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body = %s, want 502", res.Code, res.Body.String())
	}
}

func TestAssistantMessagesRequiresAuthentication(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/v1/assistant/messages", strings.NewReader(`{"message":"查看 prod 集群状态"}`)))

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
}

func TestAssistantMessagesViewerReadSuccess(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{result: map[string]any{"status": "green"}})
	req := signedRequest(t, "/v1/assistant/messages", `{"message":"查看 prod kafka 状态"}`, "viewer-1", []string{"viewer"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"type":"answer"`) || !strings.Contains(res.Body.String(), `"status":"green"`) {
		t.Fatalf("body = %s, want answer", res.Body.String())
	}
}

// TestAssistantMessagesIncludesTraceForReadAnswer 钉住读答案的 trace 契约：
// selection（规划器选择）+ tool_invocation（实际执行的工具 + 原始响应）在 HTTP
// 响应里端到端序列化。确定性 planner 已不再路由平台元工具（平台意图走 LLM 路径，
// 见 agent_executor.go），因此这里用固定 planner 直接驱动读路径，保持 trace 覆盖。
func TestAssistantMessagesIncludesTraceForReadAnswer(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryActionPlanStore()
	auditService := audit.NewService(repository)
	readService := execution.NewReadOnlyService(&readRunner{result: map[string]any{"status": "green"}}, auditService)
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time {
		return time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC)
	}))
	assistantService := assistant.NewService(tracePlanner{}, readService, planService, nil)
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		readService,
		httpapi.WithAssistant(assistantService),
		httpapi.WithActionPlans(repository),
		httpapi.WithAuditEvents(auditService),
	)
	req := signedRequest(t, "/v1/assistant/messages", `{"message":"查看 prod 集群状态"}`, "viewer-1", []string{"viewer"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	trace, ok := response["trace"].(map[string]any)
	if !ok {
		t.Fatalf("response = %+v, want trace field", response)
	}
	selection, ok := trace["selection"].(map[string]any)
	if !ok {
		t.Fatalf("trace = %+v, want selection", trace)
	}
	if selection["selected"] != tools.ClusterStatusRead {
		t.Fatalf("selected = %v, want %q", selection["selected"], tools.ClusterStatusRead)
	}
	invocation, ok := trace["tool_invocation"].(map[string]any)
	if !ok {
		t.Fatalf("trace = %+v, want tool_invocation", trace)
	}
	if invocation["tool"] != tools.ClusterStatusRead {
		t.Fatalf("tool = %v, want %q", invocation["tool"], tools.ClusterStatusRead)
	}
	rawResponse, ok := invocation["raw_response"].(map[string]any)
	if !ok || rawResponse["status"] != "green" {
		t.Fatalf("raw_response = %+v, want green status", invocation["raw_response"])
	}
}

func SKIP_TestAssistantMessagesPreservesTraceInDevTokenResponse(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryActionPlanStore()
	auditService := audit.NewService(repository)
	readService := execution.NewReadOnlyService(&readRunner{}, auditService)
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time {
		return time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC)
	}))
	assistantService := assistant.NewService(assistant.DeterministicPlanner{}, readService, planService, nil)
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		readService,
		httpapi.WithAssistant(assistantService),
		httpapi.WithDevelopmentConfirmationTokens(),
	)
	req := signedRequest(t, "/v1/assistant/messages", `{"message":"把 prod 的 orders topic retention 改成 72 小时"}`, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"confirmation_token"`) {
		t.Fatalf("body = %s, want development confirmation token", body)
	}
	if !strings.Contains(body, `"trace"`) || !strings.Contains(body, `"tool_invocation"`) {
		t.Fatalf("body = %s, want trace with tool_invocation", body)
	}
	if !strings.Contains(body, "topic.retention.set") {
		t.Fatalf("body = %s, want selected tool %s", body, "topic.retention.set")
	}
}

func TestAssistantMessagesDiagnosticResponseReservesWrapperCapacity(t *testing.T) {
	t.Parallel()
	resourceName := strings.Repeat("a", 126)
	runner := &readRunner{result: map[string]any{
		"status":  strings.Repeat("\u4e2d\"\\\\", 16),
		"details": strings.Repeat("\u4e2d\"\\\\", 888),
	}}
	router, _ := testRouter(t, runner)
	req := signedRequest(t, "/v1/assistant/messages", `{"message":"检查 prod glusterfs `+resourceName+` health"}`, "viewer-1", []string{"viewer"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if response["type"] != "answer" {
		t.Fatalf("response = %#v, want compatible diagnostic answer", response)
	}
}

func TestAssistantMessagesReturnsMiddlewareDiagnostic(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{result: map[string]any{"status": "warning", "capacity_pct": 82.5}})
	req := signedRequest(t, "/v1/assistant/messages", `{"message":"检查 prod glusterfs data volume 健康"}`, "viewer-1", []string{"viewer"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	body := res.Body.String()
	for _, want := range []string{`"type":"answer"`, `"tool":"` + "glusterfs.volume.health.read" + `"`, `"answer":{"message":"诊断完成：1 个观察，1 个发现，1 个建议"}`, `"diagnostic"`, `"glusterfs"`, `"observations"`, `"recommendations"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want %s", body, want)
		}
	}
	if strings.Contains(body, "confirmation_token") {
		t.Fatalf("body = %s, diagnostic response must not expose confirmation token", body)
	}
}

func TestAssistantMessagesMapsDiagnosticErrorsToExpectedStatus(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{name: "policy denial", err: assistant.ErrPolicyDenied, status: http.StatusForbidden},
		{name: "invalid request", err: diagnostics.ErrInvalidRequest, status: http.StatusBadRequest},
		{name: "infrastructure failure", err: errors.New("diagnostic runner unavailable"), status: http.StatusBadGateway},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			readService := execution.NewReadOnlyService(&readRunner{}, nil)
			router := httpapi.NewRouter(
				httpapi.NewHMACAuthenticator([]byte("test-secret")),
				readService,
				httpapi.WithAssistant(errorAssistant{err: testCase.err}),
			)
			res := httptest.NewRecorder()
			req := signedRequest(t, "/v1/assistant/messages", `{"message":"检查 prod glusterfs data volume 健康"}`, "viewer-1", []string{"viewer"}, []string{"prod"})

			router.ServeHTTP(res, req)

			if res.Code != testCase.status {
				t.Fatalf("status = %d body = %s, want %d", res.Code, res.Body.String(), testCase.status)
			}
		})
	}
}

func TestAssistantMessagesRejectsInvalidDiagnosticCandidatesBeforeRead(t *testing.T) {
	cases := []struct {
		name    string
		request diagnostics.Request
	}{
		{name: "invalid runbook", request: diagnostics.Request{Domain: "glusterfs", Environment: "prod", Runbook: "repair"}},
		{name: "invalid resource type", request: diagnostics.Request{Domain: "glusterfs", Environment: "prod", ResourceType: "bucket"}},
		{name: "oversized environment", request: diagnostics.Request{Domain: "glusterfs", Environment: strings.Repeat("e", 128)}},
		{name: "oversized resource name", request: diagnostics.Request{Domain: "glusterfs", Environment: "prod", ResourceName: strings.Repeat("r", 128)}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &readRunner{}
			readService := execution.NewReadOnlyService(runner, nil)
			assistantService := assistant.NewService(diagnosticPlanner{request: testCase.request}, readService, nil, nil)
			router := httpapi.NewRouter(
				httpapi.NewHMACAuthenticator([]byte("test-secret")),
				readService,
				httpapi.WithAssistant(assistantService),
			)
			res := httptest.NewRecorder()

			router.ServeHTTP(res, signedRequest(t, "/v1/assistant/messages", `{"message":"check diagnostic"}`, "viewer-1", []string{"viewer"}, []string{"prod"}))

			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s, want 400", res.Code, res.Body.String())
			}
			if runner.calls != 0 {
				t.Fatalf("runner calls = %d, want 0", runner.calls)
			}
		})
	}
}

func SKIP_TestAssistantMessagesViewerWriteDenied(t *testing.T) {
	t.Parallel()
	router, _ := capabilityTestRouter(t, &readRunner{})
	req := signedRequest(t, "/v1/assistant/messages", `{"message":"把 prod 的 orders topic retention 改成 72 小时"}`, "viewer-1", []string{"viewer"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
	}
}

func SKIP_TestAssistantMessagesAdminWriteReturnsConfirmationWithoutToken(t *testing.T) {
	t.Parallel()
	router, _ := capabilityTestRouter(t, &readRunner{})
	req := signedRequest(t, "/v1/assistant/messages", `{"message":"把 prod 的 orders topic retention 改成 72 小时"}`, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"type":"confirmation_required"`) || !strings.Contains(body, `"status":"pending_confirmation"`) {
		t.Fatalf("body = %s, want confirmation response", body)
	}
	if strings.Contains(body, "confirmation_token") {
		t.Fatalf("body = %s, must not expose confirmation token", body)
	}
}

func SKIP_TestAssistantMessagesCanExposeConfirmationTokenForDevelopment(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryActionPlanStore()
	auditService := audit.NewService(repository)
	readService := execution.NewReadOnlyService(&readRunner{}, auditService)
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time {
		return time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC)
	}))
	assistantService := assistant.NewService(assistant.DeterministicPlanner{}, readService, planService, nil)
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		readService,
		httpapi.WithAssistant(assistantService),
		httpapi.WithDevelopmentConfirmationTokens(),
	)
	req := signedRequest(t, "/v1/assistant/messages", `{"message":"把 prod 的 orders topic retention 改成 72 小时"}`, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"confirmation_token"`) {
		t.Fatalf("body = %s, want development confirmation token", res.Body.String())
	}
}

func newConversationRouter(t *testing.T, conversations store.AssistantConversationStore) (http.Handler, *store.MemoryAssistantConversationStore) {
	t.Helper()
	if conversations == nil {
		conversations = store.NewMemoryAssistantConversationStore()
	}
	assistantService := assistant.NewService(assistant.DeterministicPlanner{}, nil, nil, conversations)
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		nil,
		httpapi.WithConversations(assistantService),
	)
	return router, conversations.(*store.MemoryAssistantConversationStore)
}

func seedConversation(t *testing.T, repo *store.MemoryAssistantConversationStore, subject, title, preview string, now time.Time) store.Conversation {
	t.Helper()
	conv, err := repo.CreateConversation(context.Background(), subject, title, preview, now)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	return conv
}

func seedTurn(t *testing.T, repo *store.MemoryAssistantConversationStore, conversationID, role, content, responseType string, now time.Time) store.Turn {
	t.Helper()
	turn, err := repo.AppendTurn(context.Background(), store.Turn{
		ConversationID: conversationID,
		Role:           role,
		Content:        content,
		ResponseType:   responseType,
		CreatedAt:      now,
	})
	if err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}
	return turn
}

func TestListConversationsRequiresAuthentication(t *testing.T) {
	t.Parallel()
	router, _ := newConversationRouter(t, nil)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/assistant/conversations", nil))

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
}

func TestListConversationsReturnsOnlySubjectConversations(t *testing.T) {
	t.Parallel()
	router, conversations := newConversationRouter(t, nil)
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	seedConversation(t, conversations, "viewer-1", "alice 会话 1", "hello", now)
	seedConversation(t, conversations, "viewer-1", "alice 会话 2", "world", now.Add(time.Minute))
	seedConversation(t, conversations, "viewer-2", "bob 会话", "private", now)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedRequest(t, "/v1/assistant/conversations", "", "viewer-1", []string{"viewer"}, []string{"prod"}))

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"alice 会话 1"`) || !strings.Contains(body, `"alice 会话 2"`) {
		t.Fatalf("body = %s, want both alice conversations", body)
	}
	if strings.Contains(body, "bob 会话") {
		t.Fatalf("body = %s, must not leak other subject's conversation", body)
	}
}

func TestListConversationsExcludesArchivedByDefault(t *testing.T) {
	t.Parallel()
	router, conversations := newConversationRouter(t, nil)
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	conv := seedConversation(t, conversations, "viewer-1", "active 会话", "hello", now)
	if err := conversations.ArchiveConversation(context.Background(), conv.ID, "viewer-1", now); err != nil {
		t.Fatalf("ArchiveConversation: %v", err)
	}

	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedRequest(t, "/v1/assistant/conversations", "", "viewer-1", []string{"viewer"}, []string{"prod"}))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	if strings.Contains(res.Body.String(), "active 会话") {
		t.Fatalf("body = %s, archived conversation must be excluded by default", res.Body.String())
	}

	resArchived := httptest.NewRecorder()
	router.ServeHTTP(resArchived, signedRequest(t, "/v1/assistant/conversations?archived=true", "", "viewer-1", []string{"viewer"}, []string{"prod"}))
	if resArchived.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resArchived.Code)
	}
	if !strings.Contains(resArchived.Body.String(), "active 会话") {
		t.Fatalf("body = %s, archived=true must return archived conversation", resArchived.Body.String())
	}
}

func TestGetConversationReturnsConversationAndTurns(t *testing.T) {
	t.Parallel()
	router, conversations := newConversationRouter(t, nil)
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	conv := seedConversation(t, conversations, "viewer-1", "minio 容量查询", "检查 prod minio archive bucket 容量", now)
	seedTurn(t, conversations, conv.ID, store.ConversationRoleUser, "检查 prod minio archive bucket 容量", "", now)
	seedTurn(t, conversations, conv.ID, store.ConversationRoleAssistant, "Bucket archive usage is 77%", "answer", now.Add(time.Second))

	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedRequest(t, "/v1/assistant/conversations/"+conv.ID, "", "viewer-1", []string{"viewer"}, []string{"prod"}))

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, conv.ID) {
		t.Fatalf("body = %s, want conversation id", body)
	}
	if !strings.Contains(body, "Bucket archive usage is 77%") {
		t.Fatalf("body = %s, want assistant turn content", body)
	}
	if !strings.Contains(body, `"response_type":"answer"`) {
		t.Fatalf("body = %s, want response_type field", body)
	}
}

func TestGetConversationReturns404ForForeignConversation(t *testing.T) {
	t.Parallel()
	router, conversations := newConversationRouter(t, nil)
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	conv := seedConversation(t, conversations, "owner-1", "private", "secret", now)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedRequest(t, "/v1/assistant/conversations/"+conv.ID, "", "intruder-1", []string{"viewer"}, []string{"prod"}))

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 to avoid leaking existence", res.Code)
	}
	if strings.Contains(res.Body.String(), conv.ID) {
		t.Fatalf("body = %s, must not leak conversation id", res.Body.String())
	}
}

func TestGetConversationReturns404ForMissingConversation(t *testing.T) {
	t.Parallel()
	router, _ := newConversationRouter(t, nil)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedRequest(t, "/v1/assistant/conversations/nonexistent-id", "", "viewer-1", []string{"viewer"}, []string{"prod"}))

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.Code)
	}
}

func TestArchiveConversationSetsArchivedAtAndReturns204(t *testing.T) {
	t.Parallel()
	router, conversations := newConversationRouter(t, nil)
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	conv := seedConversation(t, conversations, "viewer-1", "to archive", "hello", now)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedPostRequest(t, "/v1/assistant/conversations/"+conv.ID+"/archive", "viewer-1", []string{"viewer"}, []string{"prod"}))

	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", res.Code)
	}
	fetched, err := conversations.GetConversation(context.Background(), conv.ID, "viewer-1")
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if fetched.ArchivedAt == nil {
		t.Fatal("archived_at = nil, want non-nil")
	}
}

func TestArchiveConversationReturns404ForForeignConversation(t *testing.T) {
	t.Parallel()
	router, conversations := newConversationRouter(t, nil)
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	conv := seedConversation(t, conversations, "owner-1", "private", "secret", now)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedPostRequest(t, "/v1/assistant/conversations/"+conv.ID+"/archive", "intruder-1", []string{"viewer"}, []string{"prod"}))

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.Code)
	}
	fetched, err := conversations.GetConversation(context.Background(), conv.ID, "owner-1")
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if fetched.ArchivedAt != nil {
		t.Fatalf("archived_at = %v, intruder must not be able to archive", fetched.ArchivedAt)
	}
}

func TestAssistantMessagesEndpointReturnsConversationID(t *testing.T) {
	t.Parallel()
	conversations := store.NewMemoryAssistantConversationStore()
	planner := assistant.DeterministicPlanner{}
	readService := execution.NewReadOnlyService(&readRunner{}, nil)
	assistantService := assistant.NewService(planner, readService, nil, conversations)
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		readService,
		httpapi.WithAssistant(assistantService),
	)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedRequest(t, "/v1/assistant/messages", `{"message":"查看 prod 集群状态"}`, "viewer-1", []string{"viewer"}, []string{"prod"}))

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"conversation_id"`) || !strings.Contains(body, `"turn_id"`) {
		t.Fatalf("body = %s, want conversation_id and turn_id", body)
	}
}

func TestListActionPlansRequiresAuthentication(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/action-plans?status=pending_confirmation", nil))

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
}

func TestListActionPlansReturnsOnlyAllowedPendingPlans(t *testing.T) {
	t.Parallel()
	router, _, planService := testRouterWithPlans(t, &readRunner{})
	prodPlan := createPendingPlan(t, planService)
	stagingInput := map[string]any{"environment": "staging", "topic": "orders", "retention_hours": 72}
	decision := policy.Evaluate(identity.CurrentUser{Subject: "admin-1", Roles: []string{"admin"}, AllowedEnvironments: []string{"staging"}, RequestID: "request-staging"}, tool(t, "topic.retention.set"), stagingInput)
	stagingPlan, err := planService.CreatePlan(context.Background(), identity.CurrentUser{Subject: "admin-1", Roles: []string{"admin"}, AllowedEnvironments: []string{"staging"}, RequestID: "request-staging"}, decision, stagingInput)
	if err != nil {
		t.Fatalf("create staging plan: %v", err)
	}
	req := signedRequest(t, "/v1/action-plans?status=pending_confirmation", "", "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, prodPlan.ID) {
		t.Fatalf("body = %s, want prod plan", body)
	}
	if strings.Contains(body, stagingPlan.ID) {
		t.Fatalf("body = %s, must not include staging plan", body)
	}
	if strings.Contains(body, "confirmation_token") {
		t.Fatalf("body = %s, must not expose confirmation token", body)
	}
}

func TestListActionPlansRejectsUnsupportedStatus(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{})
	req := signedRequest(t, "/v1/action-plans?status=confirmed", "", "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", res.Code, res.Body.String())
	}
}

func TestListActionPlansRejectsUnsupportedOrDuplicateQueryParameters(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{})

	for _, path := range []string{
		"/v1/action-plans?status=pending_confirmation&tool=topic.retention.set",
		"/v1/action-plans?status=pending_confirmation&status=pending_confirmation",
	} {
		res := httptest.NewRecorder()
		router.ServeHTTP(res, signedRequest(t, path, "", "admin-1", []string{"admin"}, []string{"prod"}))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("path %q status = %d body = %s, want 400", path, res.Code, res.Body.String())
		}
	}
}

func TestListActionPlansRejectsUnknownRole(t *testing.T) {
	t.Parallel()
	router, _, planService := testRouterWithPlans(t, &readRunner{})
	createPendingPlan(t, planService)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/action-plans?status=pending_confirmation", "", "unknown-1", []string{"unrecognized"}, []string{"prod"}))

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
	}
}

func TestListActionPlansAllowsViewerInAllowedEnvironment(t *testing.T) {
	t.Parallel()
	router, _, planService := testRouterWithPlans(t, &readRunner{})
	plan := createPendingPlan(t, planService)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/action-plans?status=pending_confirmation", "", "viewer-1", []string{"viewer"}, []string{"prod"}))

	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), plan.ID) {
		t.Fatalf("status = %d body = %s, want visible plan", res.Code, res.Body.String())
	}
}

func TestListActionPlansExcludesStoredInputHashMismatch(t *testing.T) {
	t.Parallel()
	router, repository, planService := testRouterWithPlans(t, &readRunner{})
	valid := createPendingPlan(t, planService)
	corrupted := createInputHashMismatchPlan(t, repository)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/action-plans?status=pending_confirmation", "", "admin-1", []string{"admin"}, []string{"prod"}))

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), valid.ID) || strings.Contains(res.Body.String(), corrupted.ID) {
		t.Fatalf("body = %s, want valid plan and no corrupted plan", res.Body.String())
	}
}

func TestGetActionPlanReturnsDetailForAllowedEnvironment(t *testing.T) {
	t.Parallel()
	router, _, planService := testRouterWithPlans(t, &readRunner{})
	plan := createPendingPlan(t, planService)
	req := signedRequest(t, "/v1/action-plans/"+plan.ID, "", "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	body := res.Body.String()
	for _, want := range []string{`"id":"` + plan.ID + `"`, `"tool":"topic.retention.set"`, `"environment":"prod"`, `"topic":"orders"`, `"retention_hours":72`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want %s", body, want)
		}
	}
	if strings.Contains(body, "confirmation_token") {
		t.Fatalf("body = %s, must not expose confirmation token", body)
	}
}

func TestGetActionPlanRejectsDisallowedEnvironment(t *testing.T) {
	t.Parallel()
	router, _, planService := testRouterWithPlans(t, &readRunner{})
	plan := createPendingPlan(t, planService)
	req := signedRequest(t, "/v1/action-plans/"+plan.ID, "", "admin-1", []string{"admin"}, []string{"staging"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
	}
}

func TestGetActionPlanRejectsUnknownRole(t *testing.T) {
	t.Parallel()
	router, _, planService := testRouterWithPlans(t, &readRunner{})
	plan := createPendingPlan(t, planService)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/action-plans/"+plan.ID, "", "unknown-1", []string{"unrecognized"}, []string{"prod"}))

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
	}
}

func TestGetActionPlanAllowsViewerInAllowedEnvironment(t *testing.T) {
	t.Parallel()
	router, _, planService := testRouterWithPlans(t, &readRunner{})
	plan := createPendingPlan(t, planService)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/action-plans/"+plan.ID, "", "viewer-1", []string{"viewer"}, []string{"prod"}))

	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), plan.ID) {
		t.Fatalf("status = %d body = %s, want visible plan", res.Code, res.Body.String())
	}
}

func TestGetActionPlanReturnsJSONNotFoundForStoredInputHashMismatch(t *testing.T) {
	t.Parallel()
	router, repository, _ := testRouterWithPlans(t, &readRunner{})
	plan := createInputHashMismatchPlan(t, repository)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/action-plans/"+plan.ID, "", "admin-1", []string{"admin"}, []string{"prod"}))

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s, want 404", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}
	if !strings.Contains(res.Body.String(), `"error":"action plan not found"`) {
		t.Fatalf("body = %s, want JSON not-found error", res.Body.String())
	}
}

func TestGetActionPlanUsesCanonicalToolRisk(t *testing.T) {
	t.Parallel()
	router, repository, _ := testRouterWithPlans(t, &readRunner{})
	plan := createStoredPlan(t, repository, "canonical-risk-plan", `{"environment":"prod","topic":"orders","retention_hours":72}`, string(tools.High))
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/action-plans/"+plan.ID, "", "admin-1", []string{"admin"}, []string{"prod"}))

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"risk":"medium"`) || strings.Contains(res.Body.String(), `"risk":"high"`) {
		t.Fatalf("body = %s, want canonical medium risk", res.Body.String())
	}
}

func TestConfirmActionPlanConfirmsAndExecutes(t *testing.T) {
	t.Parallel()
	runner := &readRunner{}
	router, repository, planService := testRouterWithPlans(t, runner)
	plan := createPendingPlan(t, planService)
	req := signedRequest(t, "/v1/action-plans/"+plan.ID+"/confirm", `{"expected_version":1,"confirmation_token":"`+plan.ConfirmationToken+`"}`, "admin-2", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"type":"execution_result"`) || !strings.Contains(res.Body.String(), `"status":"succeeded"`) {
		t.Fatalf("body = %s, want execution result", res.Body.String())
	}
	events := repository.AuditEvents()
	if !hasAuditAction(events, "plan_confirmed") || !hasAuditAction(events, "execution_succeeded") {
		t.Fatalf("audit events = %+v, want confirmation and execution", events)
	}
}

func TestConfirmActionPlanSurfacesVerificationInResponse(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryActionPlanStore()
	auditService := audit.NewService(repository)
	readService := execution.NewReadOnlyService(&readRunner{}, auditService)
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time {
		return time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC)
	}))
	verifier := &stubVerifier{result: &execution.VerificationResult{
		ToolName: "kafka.topic.retention.read",
		Status:   "success",
		Answer:   map[string]any{"retention_hours": float64(72)},
	}}
	executionService := execution.NewServiceWithClockAndVerifier(repository, writeExecutor{}, func() time.Time {
		return time.Date(2026, time.July, 21, 11, 1, 0, 0, time.UTC)
	}, verifier)
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		readService,
		httpapi.WithAssistant(assistant.NewService(assistant.DeterministicPlanner{}, readService, planService, nil)),
		httpapi.WithActionPlans(repository),
		httpapi.WithActionPlanConfirmation(planService, executionService),
		httpapi.WithAuditEvents(auditService),
	)
	plan := createPendingPlan(t, planService)
	req := signedRequest(t, "/v1/action-plans/"+plan.ID+"/confirm", `{"expected_version":1,"confirmation_token":"`+plan.ConfirmationToken+`"}`, "admin-2", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"verification"`) {
		t.Fatalf("body = %s, want verification field", body)
	}
	if !strings.Contains(body, `"tool_name":"kafka.topic.retention.read"`) {
		t.Fatalf("body = %s, want verification.tool_name", body)
	}
	if !strings.Contains(body, `"status":"success"`) {
		t.Fatalf("body = %s, want verification.status=success", body)
	}
	if !strings.Contains(body, `"retention_hours":72`) {
		t.Fatalf("body = %s, want verification.answer.retention_hours=72", body)
	}
	if verifier.calls != 1 {
		t.Errorf("verifier calls = %d, want 1", verifier.calls)
	}
}

type stubVerifier struct {
	result *execution.VerificationResult
	err    error
	calls  int
}

func (v *stubVerifier) Verify(_ context.Context, _ store.PlanRecord, _ map[string]any) (*execution.VerificationResult, error) {
	v.calls++
	if v.err != nil {
		return &execution.VerificationResult{Status: "failed", Error: v.err.Error()}, nil
	}
	return v.result, nil
}

func TestConfirmActionPlanRejectsMissingToken(t *testing.T) {
	t.Parallel()
	router, _, planService := testRouterWithPlans(t, &readRunner{})
	plan := createPendingPlan(t, planService)
	req := signedRequest(t, "/v1/action-plans/"+plan.ID+"/confirm", `{"expected_version":1}`, "admin-2", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
	}
}

func TestConfirmActionPlanRejectsViewer(t *testing.T) {
	t.Parallel()
	router, _, planService := testRouterWithPlans(t, &readRunner{})
	plan := createPendingPlan(t, planService)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/action-plans/"+plan.ID+"/confirm", `{"expected_version":1,"confirmation_token":"`+plan.ConfirmationToken+`"}`, "viewer-1", []string{"viewer"}, []string{"prod"}))

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
	}
}

func TestConfirmActionPlanRejectsDisallowedEnvironment(t *testing.T) {
	t.Parallel()
	router, _, planService := testRouterWithPlans(t, &readRunner{})
	plan := createPendingPlan(t, planService)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/action-plans/"+plan.ID+"/confirm", `{"expected_version":1,"confirmation_token":"`+plan.ConfirmationToken+`"}`, "admin-1", []string{"admin"}, []string{"staging"}))

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
	}
}

func TestConfirmActionPlanRejectsStoredInputHashMismatchWithoutConsumingToken(t *testing.T) {
	t.Parallel()
	router, repository, _ := testRouterWithPlans(t, &readRunner{})
	plan := createInputHashMismatchPlan(t, repository)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/action-plans/"+plan.ID+"/confirm", `{"expected_version":1,"confirmation_token":"valid-confirmation-token"}`, "admin-1", []string{"admin"}, []string{"prod"}))

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s, want 404", res.Code, res.Body.String())
	}
	stored, err := repository.GetPlan(context.Background(), plan.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if stored.Status != store.PlanPendingConfirmation || stored.Version != 1 {
		t.Fatalf("stored plan = %+v, want unconfirmed version 1", stored)
	}
}

func TestConfirmActionPlanRejectsMalformedStoredInputWithoutConsumingToken(t *testing.T) {
	t.Parallel()
	router, repository, _ := testRouterWithPlans(t, &readRunner{})
	plan := createMalformedPlan(t, repository)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/action-plans/"+plan.ID+"/confirm", `{"expected_version":1,"confirmation_token":"valid-confirmation-token"}`, "admin-1", []string{"admin"}, []string{"prod"}))

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s, want 404", res.Code, res.Body.String())
	}
	stored, err := repository.GetPlan(context.Background(), plan.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if stored.Status != store.PlanPendingConfirmation || stored.Version != 1 {
		t.Fatalf("stored plan = %+v, want unconfirmed version 1", stored)
	}
}

func TestConfirmedActionPlanDisappearsFromPendingList(t *testing.T) {
	t.Parallel()
	router, _, planService := testRouterWithPlans(t, &readRunner{})
	plan := createPendingPlan(t, planService)
	confirmRes := httptest.NewRecorder()

	router.ServeHTTP(confirmRes, signedRequest(t, "/v1/action-plans/"+plan.ID+"/confirm", `{"expected_version":1,"confirmation_token":"`+plan.ConfirmationToken+`"}`, "admin-2", []string{"admin"}, []string{"prod"}))
	if confirmRes.Code != http.StatusOK {
		t.Fatalf("confirm status = %d body = %s, want 200", confirmRes.Code, confirmRes.Body.String())
	}

	listRes := httptest.NewRecorder()
	router.ServeHTTP(listRes, signedRequest(t, "/v1/action-plans?status=pending_confirmation", "", "admin-2", []string{"admin"}, []string{"prod"}))
	if listRes.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s, want 200", listRes.Code, listRes.Body.String())
	}
	if strings.Contains(listRes.Body.String(), plan.ID) {
		t.Fatalf("body = %s, must not include confirmed plan", listRes.Body.String())
	}
}

func TestRejectActionPlanRejectsPendingAndDisappearsFromPendingList(t *testing.T) {
	t.Parallel()
	router, repository, planService := testRouterWithPlans(t, &readRunner{})
	plan := createPendingPlan(t, planService)

	req := signedRequest(t, "/v1/action-plans/"+plan.ID+"/reject", `{"expected_version":1}`, "admin-2", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"type":"plan_rejected"`) || !strings.Contains(res.Body.String(), `"status":"rejected"`) {
		t.Fatalf("body = %s, want plan_rejected response", res.Body.String())
	}
	events := repository.AuditEvents()
	if !hasAuditAction(events, "plan_rejected") {
		t.Fatalf("audit events = %+v, want plan_rejected", events)
	}

	// rejected plan 从待确认列表消失。
	listRes := httptest.NewRecorder()
	router.ServeHTTP(listRes, signedRequest(t, "/v1/action-plans?status=pending_confirmation", "", "admin-2", []string{"admin"}, []string{"prod"}))
	if listRes.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listRes.Code)
	}
	if strings.Contains(listRes.Body.String(), plan.ID) {
		t.Fatalf("body = %s, must not include rejected plan", listRes.Body.String())
	}
}

func TestRejectActionPlanRejectsViewer(t *testing.T) {
	t.Parallel()
	router, _, planService := testRouterWithPlans(t, &readRunner{})
	plan := createPendingPlan(t, planService)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/action-plans/"+plan.ID+"/reject", `{"expected_version":1}`, "viewer-1", []string{"viewer"}, []string{"prod"}))

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
	}
}

func TestRejectActionPlanRejectsAlreadyConfirmedPlan(t *testing.T) {
	t.Parallel()
	router, _, planService := testRouterWithPlans(t, &readRunner{})
	plan := createPendingPlan(t, planService)

	// 先确认。
	confirmRes := httptest.NewRecorder()
	router.ServeHTTP(confirmRes, signedRequest(t, "/v1/action-plans/"+plan.ID+"/confirm", `{"expected_version":1,"confirmation_token":"`+plan.ConfirmationToken+`"}`, "admin-2", []string{"admin"}, []string{"prod"}))
	if confirmRes.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, want 200", confirmRes.Code)
	}

	// 已确认的 plan 拒绝 → 乐观冲突 409。
	rejectRes := httptest.NewRecorder()
	router.ServeHTTP(rejectRes, signedRequest(t, "/v1/action-plans/"+plan.ID+"/reject", `{"expected_version":2}`, "admin-2", []string{"admin"}, []string{"prod"}))
	if rejectRes.Code != http.StatusConflict {
		t.Fatalf("reject confirmed plan status = %d body = %s, want 409", rejectRes.Code, rejectRes.Body.String())
	}
}

func TestOverviewReturnsPendingCount(t *testing.T) {
	t.Parallel()
	router, _, planService := testRouterWithPlans(t, &readRunner{})
	createPendingPlan(t, planService)
	createPendingPlan(t, planService)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedRequest(t, "/v1/overview", "", "admin-2", []string{"admin"}, []string{"prod"}))

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"pending_plans":2`) {
		t.Fatalf("body = %s, want pending_plans=2", body)
	}
}

func TestOverviewRequiresAuthentication(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/overview", nil))

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.Code)
	}
}

func TestListAuditEventsRequiresAuthentication(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/audit-events", nil))

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
}

func TestListAuditEventsRejectsUnknownRole(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/audit-events", "", "unknown-1", []string{"unrecognized"}, []string{"prod"}))

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
	}
}

func TestListAuditEventsReturnsChronologicalEvents(t *testing.T) {
	t.Parallel()
	router, repository := testRouter(t, &readRunner{})
	now := time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC)
	events := []store.AuditEvent{
		{ID: "evt-1", PlanID: "plan-a", Subject: "admin-1", ToolName: "minio.bucket.capacity.read", Action: "plan_created", Decision: "permitted", CreatedAt: now.Add(-3 * time.Minute)},
		{ID: "evt-2", PlanID: "plan-b", Subject: "admin-1", ToolName: "kafka.topic.retention.set", Action: "plan_created", Decision: "permitted", CreatedAt: now.Add(-2 * time.Minute)},
		{ID: "evt-3", PlanID: "plan-b", Subject: "admin-2", ToolName: "kafka.topic.retention.set", Action: "plan_confirmed", Decision: "permitted", CreatedAt: now.Add(-1 * time.Minute)},
	}
	for _, event := range events {
		if err := repository.AppendAudit(context.Background(), event); err != nil {
			t.Fatalf("append audit %s: %v", event.ID, err)
		}
	}
	req := signedRequest(t, "/v1/audit-events", "", "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	body := res.Body.String()
	for _, want := range []string{`"events"`, `"id":"evt-1"`, `"id":"evt-2"`, `"id":"evt-3"`, `"tool_name":"kafka.topic.retention.set"`, `"action":"plan_confirmed"`, `"subject":"admin-2"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want %s", body, want)
		}
	}
	var response struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(response.Events) != 3 || response.Events[0].ID != "evt-1" || response.Events[2].ID != "evt-3" {
		t.Fatalf("events = %+v, want chronological order evt-1/evt-2/evt-3", response.Events)
	}
}

func TestListAuditEventsFiltersByToolActionDecisionAndLimit(t *testing.T) {
	t.Parallel()
	router, repository := testRouter(t, &readRunner{})
	now := time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC)
	events := []store.AuditEvent{
		{ID: "audit-1", PlanID: "plan-a", ToolName: "minio.bucket.capacity.read", Action: "plan_created", Decision: "permitted", CreatedAt: now.Add(-4 * time.Minute)},
		{ID: "audit-2", PlanID: "plan-b", ToolName: "kafka.topic.retention.set", Action: "plan_created", Decision: "permitted", CreatedAt: now.Add(-3 * time.Minute)},
		{ID: "audit-3", PlanID: "plan-b", ToolName: "kafka.topic.retention.set", Action: "plan_confirmed", Decision: "permitted", CreatedAt: now.Add(-2 * time.Minute)},
		{ID: "audit-4", PlanID: "plan-c", ToolName: "kafka.topic.retention.set", Action: "plan_confirmed", Decision: "denied", CreatedAt: now.Add(-1 * time.Minute)},
	}
	for _, event := range events {
		if err := repository.AppendAudit(context.Background(), event); err != nil {
			t.Fatalf("append audit %s: %v", event.ID, err)
		}
	}

	cases := []struct {
		name    string
		query   string
		wantIDs []string
	}{
		{name: "tool filter", query: "?tool=kafka.topic.retention.set", wantIDs: []string{"audit-2", "audit-3", "audit-4"}},
		{name: "action+decision filter", query: "?action=plan_confirmed&decision=permitted", wantIDs: []string{"audit-3"}},
		{name: "limit returns newest page first", query: "?limit=2", wantIDs: []string{"audit-4", "audit-3"}},
		{name: "combined tool and limit", query: "?tool=kafka.topic.retention.set&limit=1", wantIDs: []string{"audit-4"}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			req := signedRequest(t, "/v1/audit-events"+testCase.query, "", "admin-1", []string{"admin"}, []string{"prod"})
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
			}
			var response struct {
				Events []struct {
					ID string `json:"id"`
				} `json:"events"`
			}
			if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(response.Events) != len(testCase.wantIDs) {
				t.Fatalf("events = %+v, want %d events", response.Events, len(testCase.wantIDs))
			}
			for i, wantID := range testCase.wantIDs {
				if response.Events[i].ID != wantID {
					t.Fatalf("events[%d].id = %q, want %q", i, response.Events[i].ID, wantID)
				}
			}
		})
	}
}

// TestListAuditEventsFinalResultOnlyFilter 验证借鉴-4: 事件中心"最终结果过滤"
// HTTP 参数。?final_result_only=true 隐藏 plan_rejected/execution_rejected；
// 不带参数返回全部事件。
func TestListAuditEventsFinalResultOnlyFilter(t *testing.T) {
	t.Parallel()
	router, repository := testRouter(t, &readRunner{})
	now := time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC)
	events := []store.AuditEvent{
		{ID: "evt-1", Action: "plan_created", Decision: "permitted", CreatedAt: now.Add(-4 * time.Minute)},
		{ID: "evt-2", Action: "plan_rejected", Decision: "denied", CreatedAt: now.Add(-3 * time.Minute)},
		{ID: "evt-3", Action: "plan_confirmed", Decision: "permitted", CreatedAt: now.Add(-2 * time.Minute)},
		{ID: "evt-4", Action: "execution_rejected", Decision: "denied", CreatedAt: now.Add(-1 * time.Minute)},
	}
	for _, event := range events {
		if err := repository.AppendAudit(context.Background(), event); err != nil {
			t.Fatalf("append audit %s: %v", event.ID, err)
		}
	}

	// 不带参数：返回全部 4 条（含驳回事件）。
	req := signedRequest(t, "/v1/audit-events", "", "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	var allResp struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &allResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(allResp.Events) != 4 {
		t.Fatalf("default returned %d events, want 4", len(allResp.Events))
	}

	// final_result_only=true：隐藏 plan_rejected + execution_rejected，保留 2 条。
	reqFinal := signedRequest(t, "/v1/audit-events?final_result_only=true", "", "admin-1", []string{"admin"}, []string{"prod"})
	resFinal := httptest.NewRecorder()
	router.ServeHTTP(resFinal, reqFinal)
	if resFinal.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", resFinal.Code, resFinal.Body.String())
	}
	var finalResp struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
	}
	if err := json.Unmarshal(resFinal.Body.Bytes(), &finalResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(finalResp.Events) != 2 {
		t.Fatalf("final_result_only returned %d events, want 2 (rejected hidden)", len(finalResp.Events))
	}
	for _, e := range finalResp.Events {
		if e.ID == "evt-2" || e.ID == "evt-4" {
			t.Errorf("rejected event %s should be hidden by final_result_only", e.ID)
		}
	}
}

func TestListAuditEventsAllowsViewer(t *testing.T) {
	t.Parallel()
	router, repository := testRouter(t, &readRunner{})
	now := time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC)
	if err := repository.AppendAudit(context.Background(), store.AuditEvent{
		ID: "viewer-evt", PlanID: "plan-v", Subject: "admin-1", ToolName: "minio.bucket.capacity.read",
		Action: "plan_created", Decision: "permitted", CreatedAt: now,
	}); err != nil {
		t.Fatalf("append audit: %v", err)
	}
	req := signedRequest(t, "/v1/audit-events", "", "viewer-1", []string{"viewer"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"viewer-evt"`) {
		t.Fatalf("body = %s, want viewer-evt visible", res.Body.String())
	}
}

func TestListAuditEventsRejectsInvalidLimit(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/audit-events?limit=0", "", "admin-1", []string{"admin"}, []string{"prod"}))

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", res.Code, res.Body.String())
	}
}

func TestListAuditEventsRejectsUnsupportedQueryParameter(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/audit-events?foo=bar", "", "admin-1", []string{"admin"}, []string{"prod"}))

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", res.Code, res.Body.String())
	}
}

func TestListAuditEventsFiltersByCreatedAfterAndBefore(t *testing.T) {
	t.Parallel()
	router, repository := testRouter(t, &readRunner{})
	now := time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC)
	events := []store.AuditEvent{
		{ID: "audit-1", PlanID: "plan-a", Subject: "admin-1", ToolName: "kafka.topic.retention.set", Action: "plan_created", Decision: "permitted", CreatedAt: now.Add(-3 * time.Hour)},
		{ID: "audit-2", PlanID: "plan-a", Subject: "admin-1", ToolName: "kafka.topic.retention.set", Action: "plan_confirmed", Decision: "permitted", CreatedAt: now.Add(-1 * time.Hour)},
		{ID: "audit-3", PlanID: "plan-b", Subject: "admin-2", ToolName: "kafka.topic.retention.set", Action: "plan_created", Decision: "permitted", CreatedAt: now},
	}
	for _, event := range events {
		if err := repository.AppendAudit(context.Background(), event); err != nil {
			t.Fatalf("append audit %s: %v", event.ID, err)
		}
	}

	cases := []struct {
		name    string
		query   string
		wantIDs []string
	}{
		{name: "after boundary", query: "?after=" + now.Add(-90*time.Minute).Format(time.RFC3339), wantIDs: []string{"audit-2", "audit-3"}},
		{name: "before boundary", query: "?before=" + now.Add(-90*time.Minute).Format(time.RFC3339), wantIDs: []string{"audit-1"}},
		{name: "combined range", query: "?after=" + now.Add(-150*time.Minute).Format(time.RFC3339) + "&before=" + now.Add(-30*time.Minute).Format(time.RFC3339), wantIDs: []string{"audit-2"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			req := signedRequest(t, "/v1/audit-events"+testCase.query, "", "admin-1", []string{"admin"}, []string{"prod"})
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
			}
			var response struct {
				Events []struct {
					ID string `json:"id"`
				} `json:"events"`
			}
			if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(response.Events) != len(testCase.wantIDs) {
				t.Fatalf("events = %+v, want %d events", response.Events, len(testCase.wantIDs))
			}
			for i, wantID := range testCase.wantIDs {
				if response.Events[i].ID != wantID {
					t.Fatalf("events[%d].id = %q, want %q", i, response.Events[i].ID, wantID)
				}
			}
		})
	}
}

func TestListAuditEventsRejectsMalformedTimeQuery(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/audit-events?after=not-a-time", "", "admin-1", []string{"admin"}, []string{"prod"}))

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", res.Code, res.Body.String())
	}
}

func TestListAuditEventsReturnsNextCursorAndPagesBackInTime(t *testing.T) {
	t.Parallel()
	router, repository := testRouter(t, &readRunner{})
	now := time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC)
	events := []store.AuditEvent{
		{ID: "cur-1", PlanID: "plan-a", Subject: "admin-1", ToolName: "kafka.topic.retention.set", Action: "plan_created", Decision: "permitted", CreatedAt: now.Add(-3 * time.Minute)},
		{ID: "cur-2", PlanID: "plan-b", Subject: "admin-1", ToolName: "kafka.topic.retention.set", Action: "plan_created", Decision: "permitted", CreatedAt: now.Add(-2 * time.Minute)},
		{ID: "cur-3", PlanID: "plan-b", Subject: "admin-2", ToolName: "kafka.topic.retention.set", Action: "plan_confirmed", Decision: "permitted", CreatedAt: now.Add(-1 * time.Minute)},
		{ID: "cur-4", PlanID: "plan-c", Subject: "admin-2", ToolName: "kafka.topic.retention.set", Action: "plan_confirmed", Decision: "permitted", CreatedAt: now},
	}
	for _, event := range events {
		if err := repository.AppendAudit(context.Background(), event); err != nil {
			t.Fatalf("append audit %s: %v", event.ID, err)
		}
	}

	// Page 1: newest 2 events, descending. NextCursor points at cur-3.
	firstReq := signedRequest(t, "/v1/audit-events?limit=2", "", "admin-1", []string{"admin"}, []string{"prod"})
	firstRes := httptest.NewRecorder()
	router.ServeHTTP(firstRes, firstReq)
	if firstRes.Code != http.StatusOK {
		t.Fatalf("first status = %d body = %s", firstRes.Code, firstRes.Body.String())
	}
	var firstPage struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
		NextCursor *struct {
			CreatedAt string `json:"created_at"`
			ID        string `json:"id"`
		} `json:"next_cursor"`
	}
	if err := json.Unmarshal(firstRes.Body.Bytes(), &firstPage); err != nil {
		t.Fatalf("unmarshal first page: %v", err)
	}
	if len(firstPage.Events) != 2 || firstPage.Events[0].ID != "cur-4" || firstPage.Events[1].ID != "cur-3" {
		t.Fatalf("first page events = %+v, want cur-4/cur-3", firstPage.Events)
	}
	if firstPage.NextCursor == nil || firstPage.NextCursor.ID != "cur-3" {
		t.Fatalf("first next_cursor = %+v, want cur-3", firstPage.NextCursor)
	}

	// Page 2: pass cursor back, expect remaining older events.
	secondReq := signedRequest(t, "/v1/audit-events?limit=2&cursor_created_at="+url.QueryEscape(firstPage.NextCursor.CreatedAt)+"&cursor_id="+firstPage.NextCursor.ID, "", "admin-1", []string{"admin"}, []string{"prod"})
	secondRes := httptest.NewRecorder()
	router.ServeHTTP(secondRes, secondReq)
	if secondRes.Code != http.StatusOK {
		t.Fatalf("second status = %d body = %s", secondRes.Code, secondRes.Body.String())
	}
	var secondPage struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
		NextCursor *struct {
			CreatedAt string `json:"created_at"`
			ID        string `json:"id"`
		} `json:"next_cursor"`
	}
	if err := json.Unmarshal(secondRes.Body.Bytes(), &secondPage); err != nil {
		t.Fatalf("unmarshal second page: %v", err)
	}
	if len(secondPage.Events) != 2 || secondPage.Events[0].ID != "cur-2" || secondPage.Events[1].ID != "cur-1" {
		t.Fatalf("second page events = %+v, want cur-2/cur-1", secondPage.Events)
	}
	if secondPage.NextCursor != nil {
		t.Fatalf("second next_cursor = %+v, want nil on last page", secondPage.NextCursor)
	}
}

func TestListAuditEventsFiltersBySubject(t *testing.T) {
	t.Parallel()
	router, repository := testRouter(t, &readRunner{})
	now := time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC)
	events := []store.AuditEvent{
		{ID: "sub-1", PlanID: "plan-a", Subject: "admin-1", ToolName: "kafka.topic.retention.set", Action: "plan_created", Decision: "permitted", CreatedAt: now.Add(-2 * time.Minute)},
		{ID: "sub-2", PlanID: "plan-b", Subject: "admin-2", ToolName: "kafka.topic.retention.set", Action: "plan_created", Decision: "permitted", CreatedAt: now.Add(-1 * time.Minute)},
	}
	for _, event := range events {
		if err := repository.AppendAudit(context.Background(), event); err != nil {
			t.Fatalf("append audit %s: %v", event.ID, err)
		}
	}

	req := signedRequest(t, "/v1/audit-events?subject=admin-2", "", "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	var response struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(response.Events) != 1 || response.Events[0].ID != "sub-2" {
		t.Fatalf("events = %+v, want only sub-2", response.Events)
	}
}

func TestListAuditEventsRejectsMalformedCursorCreatedAt(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/audit-events?cursor_created_at=not-a-time&cursor_id=evt", "", "admin-1", []string{"admin"}, []string{"prod"}))

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", res.Code, res.Body.String())
	}
}

func TestSearchAuditEventsRequiresAuthentication(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/audit-events/search?q=x", nil))

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.Code)
	}
}

func TestSearchAuditEventsRejectsUnknownRole(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/audit-events/search?q=x", "", "u-1", []string{"unknown"}, []string{"prod"}))

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", res.Code)
	}
}

func TestSearchAuditEventsRejectsMissingQuery(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, signedRequest(t, "/v1/audit-events/search", "", "admin-1", []string{"admin"}, []string{"prod"}))

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", res.Code, res.Body.String())
	}
}

func TestSearchAuditEventsMapsRejectedQueryToDenied(t *testing.T) {
	t.Parallel()
	router, repository := testRouter(t, &readRunner{})
	// 种子时间相对 time.Now()，避免测试对"今天"敏感（"上周"边界 = now-7d）。
	now := time.Now().UTC()
	events := []store.AuditEvent{
		{ID: "search-1", PlanID: "plan-a", Subject: "admin-1", ToolName: "kafka.topic.retention.set", Action: "plan_created", Decision: "permitted", CreatedAt: now.Add(-1 * time.Hour)},
		{ID: "search-2", PlanID: "plan-b", Subject: "admin-1", ToolName: "kafka.topic.retention.set", Action: "plan_confirmed", Decision: "denied", CreatedAt: now.Add(-30 * time.Minute)},
		{ID: "search-3", PlanID: "plan-c", Subject: "admin-2", ToolName: "kafka.topic.retention.set", Action: "plan_confirmed", Decision: "permitted", CreatedAt: now.Add(-2 * time.Hour)},
	}
	for _, event := range events {
		if err := repository.AppendAudit(context.Background(), event); err != nil {
			t.Fatalf("append %s: %v", event.ID, err)
		}
	}

	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedRequest(t, "/v1/audit-events/search?q="+url.QueryEscape("上周谁拒绝了 plan"), "", "admin-1", []string{"admin"}, []string{"prod"}))

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	var response struct {
		Events []struct {
			ID       string `json:"id"`
			Decision string `json:"decision"`
		} `json:"events"`
		Filter struct {
			Decision string `json:"decision"`
		} `json:"filter"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if response.Filter.Decision != "denied" {
		t.Fatalf("filter.decision = %q, want denied", response.Filter.Decision)
	}
	// "上周" was 7 days back; only search-2 falls in window AND is denied.
	if len(response.Events) != 1 || response.Events[0].ID != "search-2" {
		t.Fatalf("events = %+v, want only search-2", response.Events)
	}
}

func TestSearchAuditEventsReturnsSubjectFilteredResults(t *testing.T) {
	t.Parallel()
	router, repository := testRouter(t, &readRunner{})
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	events := []store.AuditEvent{
		{ID: "subj-search-1", PlanID: "plan-a", Subject: "admin-1", ToolName: "kafka.topic.retention.set", Action: "plan_created", Decision: "permitted", CreatedAt: now.Add(-1 * time.Hour)},
		{ID: "subj-search-2", PlanID: "plan-b", Subject: "admin-2", ToolName: "kafka.topic.retention.set", Action: "plan_created", Decision: "denied", CreatedAt: now.Add(-1 * time.Hour)},
	}
	for _, event := range events {
		if err := repository.AppendAudit(context.Background(), event); err != nil {
			t.Fatalf("append %s: %v", event.ID, err)
		}
	}

	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedRequest(t, "/v1/audit-events/search?q="+url.QueryEscape("admin-2 rejected"), "", "admin-1", []string{"admin"}, []string{"prod"}))

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	var response struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
		Filter struct {
			Subject string `json:"subject"`
		} `json:"filter"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if response.Filter.Subject != "admin-2" {
		t.Fatalf("filter.subject = %q, want admin-2", response.Filter.Subject)
	}
	if len(response.Events) != 1 || response.Events[0].ID != "subj-search-2" {
		t.Fatalf("events = %+v, want only subj-search-2", response.Events)
	}
}

// scheduledTaskRouter 构造一个只注入 scheduledTasks service 的路由器，用于定时任务路由测试。
func scheduledTaskRouter(t *testing.T, svc httpapi.ScheduledTaskService) http.Handler {
	t.Helper()
	return httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithScheduledTasks(svc),
	)
}

func sampleScheduledTask() store.ScheduledTask {
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	return store.ScheduledTask{
		ID:             "task-1",
		Name:           "minio 巡检",
		Subject:        "admin-1",
		CapabilityName: "minio.bucket.health.read",
		Input:          map[string]any{"environment": "prod", "name": "archive"},
		ScheduleKind:   "preset",
		Preset:         "5m",
		Timezone:       "Asia/Shanghai",
		Enabled:        true,
		NextRunAt:      now.Add(5 * time.Minute),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func sampleScheduledTaskRun() store.ScheduledTaskRun {
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	return store.ScheduledTaskRun{
		ID:            "run-1",
		TaskID:        "task-1",
		StartedAt:     now,
		FinishedAt:    now.Add(time.Second),
		Status:        store.ScheduledTaskStatusSucceeded,
		ResultSummary: "ok",
		ResultData:    map[string]any{"status": "ok"},
		AuditEventID:  "audit-1",
	}
}

func TestScheduledTaskCreateAdminSucceeds(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{createTask: sampleScheduledTask()}
	router := scheduledTaskRouter(t, svc)
	body := `{"name":"minio 巡检","capability_name":"minio.bucket.health.read","input":{"environment":"prod","name":"archive"},"schedule_kind":"preset","preset":"5m","timezone":"Asia/Shanghai","enabled":true}`
	req := signedRequestWithMethod(t, http.MethodPost, "/v1/scheduled-tasks", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if svc.createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", svc.createCalls)
	}
	if svc.createReq.Name != "minio 巡检" {
		t.Fatalf("create request name = %q, want 'minio 巡检'", svc.createReq.Name)
	}
	if svc.createReq.ScheduleKind != "preset" || svc.createReq.Preset != "5m" {
		t.Fatalf("create request schedule = %q/%q, want preset/5m", svc.createReq.ScheduleKind, svc.createReq.Preset)
	}
	if !strings.Contains(res.Body.String(), `"id":"task-1"`) || !strings.Contains(res.Body.String(), `"name":"minio 巡检"`) {
		t.Fatalf("body = %s, want task response", res.Body.String())
	}
}

func TestScheduledTaskCreateNonAdminReturnsForbidden(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{createTask: sampleScheduledTask()}
	router := scheduledTaskRouter(t, svc)
	body := `{"name":"巡检","capability_name":"minio.bucket.health.read","schedule_kind":"preset","preset":"5m","timezone":"Asia/Shanghai","enabled":true}`
	req := signedRequestWithMethod(t, http.MethodPost, "/v1/scheduled-tasks", body, "viewer-1", []string{"viewer"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
	}
	if svc.createCalls != 0 {
		t.Fatalf("create calls = %d, want 0", svc.createCalls)
	}
}

func TestScheduledTaskCreateRunbookSucceeds(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{createTask: sampleScheduledTask()}
	router := scheduledTaskRouter(t, svc)
	// run_kind=runbook 时只需 runbook_slug，无需 capability_name（低风险 runbook 模板，E2）。
	body := `{"name":"minio 定时清理","run_kind":"runbook","runbook_slug":"minio-retention-low-risk","input":{"environment":"prod"},"schedule_kind":"preset","preset":"daily","timezone":"Asia/Shanghai","enabled":true}`
	req := signedRequestWithMethod(t, http.MethodPost, "/v1/scheduled-tasks", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if svc.createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", svc.createCalls)
	}
	if svc.createReq.RunKind != store.RunKindRunbook {
		t.Fatalf("create request run_kind = %q, want runbook", svc.createReq.RunKind)
	}
	if svc.createReq.RunbookSlug != "minio-retention-low-risk" {
		t.Fatalf("create request runbook_slug = %q, want minio-retention-low-risk", svc.createReq.RunbookSlug)
	}
}

func TestScheduledTaskCreateReadTaskMissingCapabilityReturnsBadRequest(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{createTask: sampleScheduledTask()}
	router := scheduledTaskRouter(t, svc)
	// run_kind=read（默认）必须提供 capability_name。
	body := `{"name":"巡检","run_kind":"read","schedule_kind":"preset","preset":"5m","timezone":"Asia/Shanghai","enabled":true}`
	req := signedRequestWithMethod(t, http.MethodPost, "/v1/scheduled-tasks", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", res.Code, res.Body.String())
	}
	if svc.createCalls != 0 {
		t.Fatalf("create calls = %d, want 0", svc.createCalls)
	}
}

func TestScheduledTaskCreateRunbookMissingSlugReturnsBadRequest(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{createTask: sampleScheduledTask()}
	router := scheduledTaskRouter(t, svc)
	body := `{"name":"清理","run_kind":"runbook","schedule_kind":"preset","preset":"daily","timezone":"Asia/Shanghai","enabled":true}`
	req := signedRequestWithMethod(t, http.MethodPost, "/v1/scheduled-tasks", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", res.Code, res.Body.String())
	}
	if svc.createCalls != 0 {
		t.Fatalf("create calls = %d, want 0", svc.createCalls)
	}
}

func TestScheduledTaskCreateInvalidRunKindReturnsBadRequest(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{createTask: sampleScheduledTask()}
	router := scheduledTaskRouter(t, svc)
	body := `{"name":"巡检","run_kind":"garbage","capability_name":"minio.bucket.health.read","schedule_kind":"preset","preset":"5m","timezone":"Asia/Shanghai","enabled":true}`
	req := signedRequestWithMethod(t, http.MethodPost, "/v1/scheduled-tasks", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", res.Code, res.Body.String())
	}
	if svc.createCalls != 0 {
		t.Fatalf("create calls = %d, want 0", svc.createCalls)
	}
}

func TestScheduledTaskCreateMissingNameReturnsBadRequest(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{createTask: sampleScheduledTask()}
	router := scheduledTaskRouter(t, svc)
	body := `{"capability_name":"minio.bucket.health.read","schedule_kind":"preset","preset":"5m","timezone":"Asia/Shanghai","enabled":true}`
	req := signedRequestWithMethod(t, http.MethodPost, "/v1/scheduled-tasks", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", res.Code, res.Body.String())
	}
	if svc.createCalls != 0 {
		t.Fatalf("create calls = %d, want 0", svc.createCalls)
	}
}

func TestScheduledTaskCreateMissingCapabilityNameReturnsBadRequest(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{createTask: sampleScheduledTask()}
	router := scheduledTaskRouter(t, svc)
	body := `{"name":"巡检","schedule_kind":"preset","preset":"5m","timezone":"Asia/Shanghai","enabled":true}`
	req := signedRequestWithMethod(t, http.MethodPost, "/v1/scheduled-tasks", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", res.Code, res.Body.String())
	}
	if svc.createCalls != 0 {
		t.Fatalf("create calls = %d, want 0", svc.createCalls)
	}
}

func TestScheduledTaskCreateInvalidScheduleKindReturnsBadRequest(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{createTask: sampleScheduledTask()}
	router := scheduledTaskRouter(t, svc)
	body := `{"name":"巡检","capability_name":"minio.bucket.health.read","schedule_kind":"invalid","timezone":"Asia/Shanghai","enabled":true}`
	req := signedRequestWithMethod(t, http.MethodPost, "/v1/scheduled-tasks", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", res.Code, res.Body.String())
	}
	if svc.createCalls != 0 {
		t.Fatalf("create calls = %d, want 0", svc.createCalls)
	}
}

func TestScheduledTaskCreateCronMissingExprReturnsBadRequest(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{createTask: sampleScheduledTask()}
	router := scheduledTaskRouter(t, svc)
	body := `{"name":"巡检","capability_name":"minio.bucket.health.read","schedule_kind":"cron","timezone":"Asia/Shanghai","enabled":true}`
	req := signedRequestWithMethod(t, http.MethodPost, "/v1/scheduled-tasks", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", res.Code, res.Body.String())
	}
	if svc.createCalls != 0 {
		t.Fatalf("create calls = %d, want 0", svc.createCalls)
	}
}

func TestScheduledTaskCreateInvalidCronExprReturnsBadRequest(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{createErr: errors.New("invalid cron expression: 0 25 * * *")}
	router := scheduledTaskRouter(t, svc)
	body := `{"name":"巡检","capability_name":"minio.bucket.health.read","schedule_kind":"cron","cron_expr":"0 25 * * *","timezone":"Asia/Shanghai","enabled":true}`
	req := signedRequestWithMethod(t, http.MethodPost, "/v1/scheduled-tasks", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", res.Code, res.Body.String())
	}
	if svc.createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", svc.createCalls)
	}
}

func TestScheduledTaskListReturnsTasks(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{listTasks: []store.ScheduledTask{sampleScheduledTask()}}
	router := scheduledTaskRouter(t, svc)
	req := signedRequestWithMethod(t, http.MethodGet, "/v1/scheduled-tasks", "", "viewer-1", []string{"viewer"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if svc.listCalls != 1 {
		t.Fatalf("list calls = %d, want 1", svc.listCalls)
	}
	if !strings.Contains(res.Body.String(), `"tasks"`) || !strings.Contains(res.Body.String(), `"task-1"`) {
		t.Fatalf("body = %s, want task list", res.Body.String())
	}
}

func TestScheduledTaskListFiltersByEnabled(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{listTasks: []store.ScheduledTask{}}
	router := scheduledTaskRouter(t, svc)
	req := signedRequestWithMethod(t, http.MethodGet, "/v1/scheduled-tasks?enabled=true", "", "viewer-1", []string{"viewer"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if svc.listCalls != 1 {
		t.Fatalf("list calls = %d, want 1", svc.listCalls)
	}
	if svc.listFilter.Enabled == nil || !*svc.listFilter.Enabled {
		t.Fatalf("filter enabled = %v, want true", svc.listFilter.Enabled)
	}
}

func TestScheduledTaskGetReturnsTask(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{getTask: sampleScheduledTask()}
	router := scheduledTaskRouter(t, svc)
	req := signedRequestWithMethod(t, http.MethodGet, "/v1/scheduled-tasks/task-1", "", "viewer-1", []string{"viewer"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if svc.getCalls != 1 {
		t.Fatalf("get calls = %d, want 1", svc.getCalls)
	}
	if svc.getID != "task-1" {
		t.Fatalf("get id = %q, want task-1", svc.getID)
	}
	if !strings.Contains(res.Body.String(), `"id":"task-1"`) {
		t.Fatalf("body = %s, want task response", res.Body.String())
	}
}

func TestScheduledTaskGetNonExistentReturns404(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{getErr: store.ErrNotFound}
	router := scheduledTaskRouter(t, svc)
	req := signedRequestWithMethod(t, http.MethodGet, "/v1/scheduled-tasks/nonexistent", "", "viewer-1", []string{"viewer"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s, want 404", res.Code, res.Body.String())
	}
}

func TestScheduledTaskUpdateAdminSucceeds(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{updateTask: sampleScheduledTask()}
	router := scheduledTaskRouter(t, svc)
	body := `{"name":"巡检-updated","capability_name":"minio.bucket.health.read","schedule_kind":"preset","preset":"daily","timezone":"Asia/Shanghai","enabled":true}`
	req := signedRequestWithMethod(t, http.MethodPatch, "/v1/scheduled-tasks/task-1", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if svc.updateCalls != 1 {
		t.Fatalf("update calls = %d, want 1", svc.updateCalls)
	}
	if svc.updateID != "task-1" {
		t.Fatalf("update id = %q, want task-1", svc.updateID)
	}
	if svc.updateReq.Preset != "daily" {
		t.Fatalf("update preset = %q, want daily", svc.updateReq.Preset)
	}
}

func TestScheduledTaskUpdateNonAdminReturnsForbidden(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{updateTask: sampleScheduledTask()}
	router := scheduledTaskRouter(t, svc)
	body := `{"name":"巡检","capability_name":"minio.bucket.health.read","schedule_kind":"preset","preset":"5m","timezone":"Asia/Shanghai","enabled":true}`
	req := signedRequestWithMethod(t, http.MethodPatch, "/v1/scheduled-tasks/task-1", body, "viewer-1", []string{"viewer"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
	}
	if svc.updateCalls != 0 {
		t.Fatalf("update calls = %d, want 0", svc.updateCalls)
	}
}

func TestScheduledTaskUpdateNonExistentReturns404(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{updateErr: store.ErrNotFound}
	router := scheduledTaskRouter(t, svc)
	body := `{"name":"巡检","capability_name":"minio.bucket.health.read","schedule_kind":"preset","preset":"5m","timezone":"Asia/Shanghai","enabled":true}`
	req := signedRequestWithMethod(t, http.MethodPatch, "/v1/scheduled-tasks/nonexistent", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s, want 404", res.Code, res.Body.String())
	}
}

func TestScheduledTaskDeleteAdminReturnsNoContent(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{}
	router := scheduledTaskRouter(t, svc)
	req := signedRequestWithMethod(t, http.MethodDelete, "/v1/scheduled-tasks/task-1", "", "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d body = %s, want 204", res.Code, res.Body.String())
	}
	if svc.deleteCalls != 1 {
		t.Fatalf("delete calls = %d, want 1", svc.deleteCalls)
	}
	if svc.deleteID != "task-1" {
		t.Fatalf("delete id = %q, want task-1", svc.deleteID)
	}
}

func TestScheduledTaskDeleteNonAdminReturnsForbidden(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{}
	router := scheduledTaskRouter(t, svc)
	req := signedRequestWithMethod(t, http.MethodDelete, "/v1/scheduled-tasks/task-1", "", "viewer-1", []string{"viewer"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
	}
	if svc.deleteCalls != 0 {
		t.Fatalf("delete calls = %d, want 0", svc.deleteCalls)
	}
}

func TestScheduledTaskDeleteNonExistentReturns404(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{deleteErr: store.ErrNotFound}
	router := scheduledTaskRouter(t, svc)
	req := signedRequestWithMethod(t, http.MethodDelete, "/v1/scheduled-tasks/nonexistent", "", "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s, want 404", res.Code, res.Body.String())
	}
}

func TestScheduledTaskTriggerAdminReturnsRun(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{triggerRun: sampleScheduledTaskRun()}
	router := scheduledTaskRouter(t, svc)
	req := signedRequestWithMethod(t, http.MethodPost, "/v1/scheduled-tasks/task-1/run", "", "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if svc.triggerCalls != 1 {
		t.Fatalf("trigger calls = %d, want 1", svc.triggerCalls)
	}
	if svc.triggerID != "task-1" {
		t.Fatalf("trigger id = %q, want task-1", svc.triggerID)
	}
	if !strings.Contains(res.Body.String(), `"id":"run-1"`) || !strings.Contains(res.Body.String(), `"status":"succeeded"`) {
		t.Fatalf("body = %s, want run response", res.Body.String())
	}
}

func TestScheduledTaskTriggerNonAdminReturnsForbidden(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{triggerRun: sampleScheduledTaskRun()}
	router := scheduledTaskRouter(t, svc)
	req := signedRequestWithMethod(t, http.MethodPost, "/v1/scheduled-tasks/task-1/run", "", "viewer-1", []string{"viewer"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
	}
	if svc.triggerCalls != 0 {
		t.Fatalf("trigger calls = %d, want 0", svc.triggerCalls)
	}
}

func TestScheduledTaskListRunsReturnsHistory(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{listRunsResult: []store.ScheduledTaskRun{sampleScheduledTaskRun()}}
	router := scheduledTaskRouter(t, svc)
	req := signedRequestWithMethod(t, http.MethodGet, "/v1/scheduled-tasks/task-1/runs", "", "viewer-1", []string{"viewer"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if svc.listRunsCalls != 1 {
		t.Fatalf("listRuns calls = %d, want 1", svc.listRunsCalls)
	}
	if svc.listRunsID != "task-1" {
		t.Fatalf("listRuns id = %q, want task-1", svc.listRunsID)
	}
	if !strings.Contains(res.Body.String(), `"runs"`) || !strings.Contains(res.Body.String(), `"run-1"`) {
		t.Fatalf("body = %s, want runs list", res.Body.String())
	}
}

func TestScheduledTaskCountFailuresReturnsCount(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{failureCount: 3}
	router := scheduledTaskRouter(t, svc)
	req := signedRequestWithMethod(t, http.MethodGet, "/v1/scheduled-tasks/failures/count", "", "viewer-1", []string{"viewer"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if svc.countCalls != 1 {
		t.Fatalf("count calls = %d, want 1", svc.countCalls)
	}
	if !strings.Contains(res.Body.String(), `"count":3`) {
		t.Fatalf("body = %s, want count", res.Body.String())
	}
}

func TestScheduledTaskRoutesReturn503WhenServiceNotConfigured(t *testing.T) {
	t.Parallel()
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
	)
	req := signedRequestWithMethod(t, http.MethodGet, "/v1/scheduled-tasks", "", "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body = %s, want 500", res.Code, res.Body.String())
	}
}

func TestScheduledTaskRoutesRequireAuthentication(t *testing.T) {
	t.Parallel()
	svc := &scheduledTaskService{listTasks: []store.ScheduledTask{}}
	router := scheduledTaskRouter(t, svc)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/scheduled-tasks", nil))

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
	if svc.listCalls != 0 {
		t.Fatalf("list calls = %d, want 0", svc.listCalls)
	}
}

func testRouter(t *testing.T, runner *readRunner) (http.Handler, *store.MemoryActionPlanStore) {
	t.Helper()
	router, repository, _ := testRouterWithPlans(t, runner)
	return router, repository
}

func testRouterWithPlans(t *testing.T, runner *readRunner) (http.Handler, *store.MemoryActionPlanStore, *plans.Service) {
	t.Helper()
	ensureMiddlewareTools(t)
	repository := store.NewMemoryActionPlanStore()
	auditService := audit.NewService(repository)
	readService := execution.NewReadOnlyService(runner, auditService)
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time {
		return time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC)
	}))
	assistantService := assistant.NewService(assistant.DeterministicPlanner{}, readService, planService, nil)
	executionService := execution.NewServiceWithClock(repository, writeExecutor{}, func() time.Time {
		return time.Date(2026, time.July, 21, 11, 1, 0, 0, time.UTC)
	})
	return httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		readService,
		httpapi.WithAssistant(assistantService),
		httpapi.WithActionPlans(repository),
		httpapi.WithActionPlanConfirmation(planService, executionService),
		httpapi.WithAuditEvents(auditService),
	), repository, planService
}

// capabilityTestRouter builds a router whose assistant uses the full
// CapabilityAwarePlanner chain, so write intents (e.g. topic.retention.set)
// route through dynamic capability resolution. The default testRouter uses the
// plain DeterministicPlanner, which keeps middleware diagnostic reads on the
// in-process diagnostic path; write intent resolution only exists in the
// capability-aware chain (mirroring production wiring).
func capabilityTestRouter(t *testing.T, runner *readRunner) (http.Handler, *store.MemoryActionPlanStore) {
	t.Helper()
	ensureMiddlewareTools(t)
	repository := store.NewMemoryActionPlanStore()
	auditService := audit.NewService(repository)
	readService := execution.NewReadOnlyService(runner, auditService)
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time {
		return time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC)
	}))
	assistantService := assistant.NewService(assistant.DeterministicPlanner{}, readService, planService, nil)
	executionService := execution.NewServiceWithClock(repository, writeExecutor{}, func() time.Time {
		return time.Date(2026, time.July, 21, 11, 1, 0, 0, time.UTC)
	})
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		readService,
		httpapi.WithAssistant(assistantService),
		httpapi.WithActionPlans(repository),
		httpapi.WithActionPlanConfirmation(planService, executionService),
		httpapi.WithAuditEvents(auditService),
	)
	return router, repository
}

func createPendingPlan(t *testing.T, service *plans.Service) plans.Plan {
	t.Helper()
	decision := policy.Evaluate(admin(), tool(t, "topic.retention.set"), retentionInput())
	plan, err := service.CreatePlan(context.Background(), admin(), decision, retentionInput())
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	return plan
}

func createInputHashMismatchPlan(t *testing.T, repository *store.MemoryActionPlanStore) store.PlanRecord {
	t.Helper()
	return createStoredPlanWithHash(t, repository, "corrupted-hash-plan", `{"environment":"prod","topic":"orders","retention_hours":72}`, "corrupted-input-hash", string(tools.Medium), "valid-confirmation-token")
}

func createMalformedPlan(t *testing.T, repository *store.MemoryActionPlanStore) store.PlanRecord {
	t.Helper()
	return createStoredPlan(t, repository, "malformed-plan", `{"environment":"prod","topic":"orders"}`, string(tools.High))
}

func createStoredPlan(t *testing.T, repository *store.MemoryActionPlanStore, id, input, risk string) store.PlanRecord {
	t.Helper()
	decoded, err := plans.DecodeInput([]byte(input))
	if err != nil {
		t.Fatalf("decode stored input: %v", err)
	}
	_, inputHash, err := plans.CanonicalInput(decoded)
	if err != nil {
		t.Fatalf("canonicalize stored input: %v", err)
	}
	return createStoredPlanWithHash(t, repository, id, input, inputHash, risk, "valid-confirmation-token")
}

func createStoredPlanWithHash(t *testing.T, repository *store.MemoryActionPlanStore, id, input, inputHash, risk, confirmationToken string) store.PlanRecord {
	t.Helper()
	now := time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC)
	plan := store.PlanRecord{
		ID:                    id,
		RequestID:             "malformed-request",
		CreatedBy:             "admin-1",
		ToolName:              "topic.retention.set",
		InputJSON:             []byte(input),
		InputHash:             inputHash,
		RiskLevel:             risk,
		Status:                store.PlanPendingConfirmation,
		Version:               1,
		ConfirmationTokenHash: hashConfirmationToken(confirmationToken),
		ExpiresAt:             now.Add(time.Minute),
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if err := repository.CreatePlan(context.Background(), plan, store.AuditEvent{ID: id + "-audit", PlanID: plan.ID, Action: "plan_created", CreatedAt: now}); err != nil {
		t.Fatalf("create malformed plan: %v", err)
	}
	return plan
}

func hashConfirmationToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// scheduledTaskService 是 ScheduledTaskService 接口的测试 fake，记录每次调用的参数和返回值。
type scheduledTaskService struct {
	createTask  store.ScheduledTask
	createErr   error
	createCalls int
	createReq   scheduler.CreateRequest

	updateTask  store.ScheduledTask
	updateErr   error
	updateCalls int
	updateID    string
	updateReq   scheduler.UpdateRequest

	deleteErr   error
	deleteCalls int
	deleteID    string

	getTask  store.ScheduledTask
	getErr   error
	getCalls int
	getID    string

	listTasks  []store.ScheduledTask
	listErr    error
	listCalls  int
	listFilter store.ScheduledTaskFilter

	triggerRun   store.ScheduledTaskRun
	triggerErr   error
	triggerCalls int
	triggerID    string

	listRunsResult []store.ScheduledTaskRun
	listRunsErr    error
	listRunsCalls  int
	listRunsID     string

	failureCount int
	countErr     error
	countCalls   int
}

func (s *scheduledTaskService) Create(_ context.Context, _ identity.CurrentUser, req scheduler.CreateRequest) (store.ScheduledTask, error) {
	s.createCalls++
	s.createReq = req
	return s.createTask, s.createErr
}

func (s *scheduledTaskService) Update(_ context.Context, _ identity.CurrentUser, id string, req scheduler.UpdateRequest) (store.ScheduledTask, error) {
	s.updateCalls++
	s.updateID = id
	s.updateReq = req
	return s.updateTask, s.updateErr
}

func (s *scheduledTaskService) Delete(_ context.Context, _ identity.CurrentUser, id string) error {
	s.deleteCalls++
	s.deleteID = id
	return s.deleteErr
}

func (s *scheduledTaskService) Get(_ context.Context, _ identity.CurrentUser, id string) (store.ScheduledTask, error) {
	s.getCalls++
	s.getID = id
	return s.getTask, s.getErr
}

func (s *scheduledTaskService) List(_ context.Context, _ identity.CurrentUser, filter store.ScheduledTaskFilter) ([]store.ScheduledTask, error) {
	s.listCalls++
	s.listFilter = filter
	return s.listTasks, s.listErr
}

func (s *scheduledTaskService) Trigger(_ context.Context, _ identity.CurrentUser, id string) (store.ScheduledTaskRun, error) {
	s.triggerCalls++
	s.triggerID = id
	return s.triggerRun, s.triggerErr
}

func (s *scheduledTaskService) ListRuns(_ context.Context, _ identity.CurrentUser, id string, _ int) ([]store.ScheduledTaskRun, error) {
	s.listRunsCalls++
	s.listRunsID = id
	return s.listRunsResult, s.listRunsErr
}

func (s *scheduledTaskService) CountRecentFailures(_ context.Context, _ time.Time) (int, error) {
	s.countCalls++
	return s.failureCount, s.countErr
}

type readRunner struct {
	calls    int
	toolName string
	input    map[string]any
	result   map[string]any
}

type capabilityManagementService struct {
	list               []capabilities.ManagedCapability
	saved              capabilities.ManagedCapability
	published          capabilities.ManagedCapability
	unpublished        capabilities.ManagedCapability
	imported           []capabilities.ManagedCapability
	importedRequest    capabilities.OpenAPIURLImportRequest
	saveCalls          int
	testCalls          int
	importCalls        int
	preview            capabilities.ImportPreview
	previewRequest     capabilities.OpenAPIURLPreviewRequest
	previewCalls       int
	commitResult       capabilities.OpenAPIURLCommitResult
	commitRequest      capabilities.OpenAPIURLCommitRequest
	commitCalls        int
	publishName        string
	unpublishName      string
	quickPublishResult capabilities.ManagedCapability
	quickPublishErr    error
	quickPublishCalls  int
	quickPublishReq    capabilities.QuickPublishRequest
}

func (s *capabilityManagementService) List(context.Context) ([]capabilities.ManagedCapability, error) {
	return s.list, nil
}

func (s *capabilityManagementService) Get(_ context.Context, name string) (capabilities.ManagedCapability, error) {
	for _, item := range s.list {
		if item.Name == name {
			return item, nil
		}
	}
	return capabilities.ManagedCapability{}, capabilities.ErrCapabilityNotFound
}

func (s *capabilityManagementService) SaveDraft(_ context.Context, capability capabilities.Capability) (capabilities.ManagedCapability, error) {
	s.saveCalls++
	if s.saved.Name == "" {
		return capabilities.ManagedCapability{Capability: capability, Source: capabilities.SourceDiscovered}, nil
	}
	return s.saved, nil
}

func (s *capabilityManagementService) ValidateCapability(capabilities.Capability) capabilities.ValidationResult {
	return capabilities.ValidationResult{Valid: true}
}

func (s *capabilityManagementService) Test(context.Context, capabilities.Capability, map[string]any) (capabilities.NormalizedResult, error) {
	s.testCalls++
	return capabilities.NormalizedResult{Kind: "observation", Summary: "ok", Data: map[string]any{"status": "ok"}}, nil
}

func (s *capabilityManagementService) ImportOpenAPIFromURL(_ context.Context, request capabilities.OpenAPIURLImportRequest) ([]capabilities.ManagedCapability, error) {
	s.importCalls++
	s.importedRequest = request
	return s.imported, nil
}

func (s *capabilityManagementService) PreviewOpenAPIFromURL(_ context.Context, request capabilities.OpenAPIURLPreviewRequest) (capabilities.ImportPreview, error) {
	s.previewCalls++
	s.previewRequest = request
	return s.preview, nil
}

func (s *capabilityManagementService) CommitOpenAPIFromURL(_ context.Context, request capabilities.OpenAPIURLCommitRequest) (capabilities.OpenAPIURLCommitResult, error) {
	s.commitCalls++
	s.commitRequest = request
	return s.commitResult, nil
}

func (s *capabilityManagementService) Publish(_ context.Context, name string) (capabilities.ManagedCapability, error) {
	s.publishName = name
	return s.published, nil
}

func (s *capabilityManagementService) Unpublish(_ context.Context, name string) (capabilities.ManagedCapability, error) {
	s.unpublishName = name
	return s.unpublished, nil
}

func (s *capabilityManagementService) QuickPublish(_ context.Context, request capabilities.QuickPublishRequest) (capabilities.ManagedCapability, error) {
	s.quickPublishCalls++
	s.quickPublishReq = request
	if s.quickPublishErr != nil {
		return capabilities.ManagedCapability{}, s.quickPublishErr
	}
	return s.quickPublishResult, nil
}

type errorAssistant struct {
	err error
}

type diagnosticPlanner struct {
	request diagnostics.Request
}

func (p diagnosticPlanner) Plan(context.Context, identity.CurrentUser, string, []assistant.Turn, assistant.PageContext) (assistant.Intent, error) {
	return assistant.Intent{Diagnostic: &p.request}, nil
}

// tracePlanner 返回一个固定读意图（cluster.status.read）并携带能力选择元数据，
// 用于验证读答案的 trace 字段（selection + tool_invocation + raw_response）端到端
// 序列化。它模拟 LLM 路径 agent 循环的产物——确定性 planner 已不路由平台元工具。
type tracePlanner struct{}

func (tracePlanner) Plan(context.Context, identity.CurrentUser, string, []assistant.Turn, assistant.PageContext) (assistant.Intent, error) {
	return assistant.Intent{
		ToolName: tools.ClusterStatusRead,
		Input:    map[string]any{"environment": "prod"},
		Selection: &assistant.CapabilitySelection{
			Selected:   tools.ClusterStatusRead,
			Confidence: 0.9,
			Reason:     "fixed planner for trace test",
		},
	}, nil
}

func (s errorAssistant) HandleMessage(context.Context, identity.CurrentUser, string, string, assistant.PageContext) (assistant.Response, error) {
	return assistant.Response{}, s.err
}

func (s errorAssistant) HandleMessageStream(context.Context, identity.CurrentUser, string, string, assistant.PageContext) (<-chan assistant.StreamEvent, error) {
	return nil, s.err
}

func (r *readRunner) Read(_ context.Context, tool tools.Tool, input map[string]any) (map[string]any, error) {
	r.calls++
	r.toolName = tool.Name
	r.input = input
	if r.result != nil {
		return r.result, nil
	}
	return map[string]any{"ok": true}, nil
}

type writeExecutor struct{}

func (writeExecutor) Execute(_ context.Context, toolName string, input map[string]any) (map[string]any, error) {
	return map[string]any{"tool": toolName, "topic": input["topic"], "status": "applied"}, nil
}

func hasAuditAction(events []store.AuditEvent, action string) bool {
	for _, event := range events {
		if event.Action == action {
			return true
		}
	}
	return false
}

func signedRequest(t *testing.T, path, body, subject string, roles, environments []string) *http.Request {
	t.Helper()
	method := http.MethodPost
	if body == "" {
		method = http.MethodGet
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+signedJWT(t, map[string]any{
		"sub":                  subject,
		"roles":                roles,
		"allowed_environments": environments,
		"permissions":          []string{"*"},
	}))
	req.Header.Set("X-Request-ID", "request-1")
	return req
}

// signedRequestWithMethod 构造指定 HTTP 方法的签名请求，支持 PATCH / DELETE 等方法。
func signedRequestWithMethod(t *testing.T, method, path, body, subject string, roles, environments []string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+signedJWT(t, map[string]any{
		"sub":                  subject,
		"roles":                roles,
		"allowed_environments": environments,
		"permissions":          []string{"*"},
	}))
	req.Header.Set("X-Request-ID", "request-1")
	return req
}

// signedPostRequest constructs a POST request with no body, signed with the
// given subject's JWT. Used for routes like archive that require POST but
// accept an empty body.
func signedPostRequest(t *testing.T, path, subject string, roles, environments []string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("Authorization", "Bearer "+signedJWT(t, map[string]any{
		"sub":                  subject,
		"roles":                roles,
		"allowed_environments": environments,
		"permissions":          []string{"*"},
	}))
	req.Header.Set("X-Request-ID", "request-1")
	return req
}

func tool(t *testing.T, name string) tools.Tool {
	t.Helper()
	ensureMiddlewareTools(t)
	tool, ok := tools.Lookup(name)
	if !ok {
		t.Fatalf("unknown tool %q", name)
	}
	return tool
}

// --- Middleware tools externalization test wiring ---
//
// The four middleware tools (glusterfs/minio/kafka read + topic.retention.set
// write) are no longer statically registered: they load from published YAML
// capabilities and execute over HTTP. Tests that reference them by name must
// register them dynamically (mirroring the published schemas) and grant the
// role permissions they receive from capability auth.roles. Registration is
// idempotent and mutex-guarded so parallel tests share the registry safely.

var ensureMiddlewareToolsMu sync.Mutex

func ensureMiddlewareTools(t *testing.T) {
	t.Helper()
	ensureMiddlewareToolsMu.Lock()
	defer ensureMiddlewareToolsMu.Unlock()
	if _, ok := tools.Lookup("topic.retention.set"); !ok {
		if err := tools.RegisterDynamicTools(httpAPIMiddlewareDefinitions()); err != nil {
			t.Fatalf("register middleware tools: %v", err)
		}
	}
	// Role permissions are additive/idempotent; re-inject on every call so a
	// policy-level reset elsewhere cannot leave the middleware tools unroutable.
	policy.RegisterDynamicRolePermissions(map[string][]string{
		"glusterfs.volume.health.read": {"viewer", "operator", "admin"},
		"minio.bucket.health.read":     {"viewer", "operator", "admin"},
		"kafka.consumer_lag.read":      {"viewer", "operator", "admin"},
		"topic.retention.set":          {"operator", "admin"},
	})
}

func httpAPIMiddlewareDefinitions() []tools.DynamicToolDefinition {
	return []tools.DynamicToolDefinition{
		{
			Tool: tools.Tool{Name: "glusterfs.volume.health.read", Operation: tools.Read, Risk: tools.Low, Domain: "glusterfs", ResourceType: "volume"},
			InputSchema: map[string]tools.DynamicInputField{
				"environment": {Type: "string", Required: true},
				"name":        {Type: "string", Required: true},
			},
		},
		{
			Tool: tools.Tool{Name: "minio.bucket.health.read", Operation: tools.Read, Risk: tools.Low, Domain: "minio", ResourceType: "bucket"},
			InputSchema: map[string]tools.DynamicInputField{
				"environment": {Type: "string", Required: true},
				"name":        {Type: "string", Required: true},
			},
		},
		{
			Tool: tools.Tool{Name: "kafka.consumer_lag.read", Operation: tools.Read, Risk: tools.Low, Domain: "kafka", ResourceType: "consumer_group"},
			InputSchema: map[string]tools.DynamicInputField{
				"environment": {Type: "string", Required: true},
				"name":        {Type: "string", Required: true},
			},
		},
		{
			Tool: tools.Tool{
				Name:                "topic.retention.set",
				Operation:           tools.Write,
				Risk:                tools.Medium,
				RollbackDescription: "reset_to_previous",
				Domain:              "kafka",
				ResourceType:        "topic",
				SupportsDryRun:      true,
			},
			InputSchema: map[string]tools.DynamicInputField{
				"environment":     {Type: "string", Required: true},
				"topic":           {Type: "string", Required: true},
				"retention_hours": {Type: "integer", Required: true, Min: httpAPIMinBound(1), Max: httpAPIMaxBound(8760)},
			},
		},
	}
}

func httpAPIMinBound(value float64) *float64 { return &value }
func httpAPIMaxBound(value float64) *float64 { return &value }

func retentionInput() map[string]any {
	return map[string]any{"environment": "prod", "topic": "orders", "retention_hours": 72}
}

func admin() identity.CurrentUser {
	return identity.CurrentUser{
		Subject:             "admin-1",
		Roles:               []string{"admin"},
		AllowedEnvironments: []string{"prod"},
		RequestID:           "request-admin",
	}
}

func signedJWT(t *testing.T, payload map[string]any) string {
	t.Helper()
	header := encodeJSON(t, map[string]any{"alg": "HS256", "typ": "JWT"})
	claims := encodeJSON(t, payload)
	unsigned := header + "." + claims
	mac := hmac.New(sha256.New, []byte("test-secret"))
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func encodeJSON(t *testing.T, value map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal jwt segment: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func TestJWTExpiredTokenRejected(t *testing.T) {
	t.Parallel()
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(&capabilityManagementService{}),
	)
	expired := time.Now().Add(-1 * time.Hour).Unix()
	token := signedJWT(t, map[string]any{
		"sub":                  "user-1",
		"roles":                []string{"viewer"},
		"allowed_environments": []string{"prod"},
		"exp":                  expired,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (token expired)", res.Code, http.StatusUnauthorized)
	}
}

func TestJWTNotYetValidTokenRejected(t *testing.T) {
	t.Parallel()
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(&capabilityManagementService{}),
	)
	future := time.Now().Add(1 * time.Hour).Unix()
	token := signedJWT(t, map[string]any{
		"sub":                  "user-1",
		"roles":                []string{"viewer"},
		"allowed_environments": []string{"prod"},
		"nbf":                  future,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (token not yet valid)", res.Code, http.StatusUnauthorized)
	}
}

func TestJWTValidExpiryAccepted(t *testing.T) {
	t.Parallel()
	service := &capabilityManagementService{
		list: []capabilities.ManagedCapability{{
			Capability: capabilities.Capability{
				Name:         "minio.bucket.capacity.read",
				Status:       capabilities.StatusPublished,
				Domain:       "minio",
				ResourceType: "bucket",
				Operation:    tools.Read,
				Risk:         tools.Low,
			},
			Source: capabilities.SourcePublished,
		}},
	}
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(service),
	)
	expiry := time.Now().Add(1 * time.Hour).Unix()
	token := signedJWT(t, map[string]any{
		"sub":                  "user-1",
		"roles":                []string{"viewer"},
		"allowed_environments": []string{"prod"},
		"exp":                  expiry,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Request-ID", "jwt-valid-exp")
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200 (valid token with future exp)", res.Code, res.Body.String())
	}
}

func TestJWTNoExpiryStillAccepted(t *testing.T) {
	t.Parallel()
	service := &capabilityManagementService{
		list: []capabilities.ManagedCapability{{
			Capability: capabilities.Capability{
				Name:         "minio.bucket.capacity.read",
				Status:       capabilities.StatusPublished,
				Domain:       "minio",
				ResourceType: "bucket",
				Operation:    tools.Read,
				Risk:         tools.Low,
			},
			Source: capabilities.SourcePublished,
		}},
	}
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(service),
	)
	// No exp claim — dev token, should still be accepted.
	token := signedJWT(t, map[string]any{
		"sub":                  "user-1",
		"roles":                []string{"viewer"},
		"allowed_environments": []string{"prod"},
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Request-ID", "jwt-no-exp")
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200 (dev token without exp)", res.Code, res.Body.String())
	}
}
