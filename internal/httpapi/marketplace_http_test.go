package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
	"github.com/gracegaoya/ai-operations-copilot/internal/marketplace"
)

// fakeMarketplaceService is a minimal implementation of httpapi.MarketplaceService
// that records calls so tests can assert routing, role gating, and owner
// assignment without touching a database.
type fakeMarketplaceService struct {
	publishOwner string
	searchCalled bool
	searchTotal  int
	versions     []marketplace.Version
}

func (f *fakeMarketplaceService) Publish(_ context.Context, req marketplace.PublishRequest) (*marketplace.Registry, *marketplace.Version, error) {
	f.publishOwner = req.OwnerID
	return &marketplace.Registry{ID: "reg-1", Name: "k8s.pod.restart"}, &marketplace.Version{ID: "ver-1", Version: "1.0.0"}, nil
}

func (f *fakeMarketplaceService) Search(_ context.Context, _ marketplace.SearchRequest) ([]marketplace.Registry, int, error) {
	f.searchCalled = true
	return []marketplace.Registry{{ID: "reg-1", Name: "k8s.pod.restart"}}, f.searchTotal, nil
}

func (f *fakeMarketplaceService) SemanticSearch(_ context.Context, _ string, _, _ int) ([]marketplace.Registry, error) {
	return nil, marketplace.ErrSemanticUnavailable
}

func (f *fakeMarketplaceService) Get(_ context.Context, id string) (*marketplace.Registry, error) {
	return &marketplace.Registry{ID: id, Name: "k8s.pod.restart"}, nil
}

func (f *fakeMarketplaceService) ListVersions(_ context.Context, _ string) ([]marketplace.Version, error) {
	return f.versions, nil
}

func (f *fakeMarketplaceService) GetVersion(_ context.Context, _, versionID string) (*marketplace.Version, error) {
	return &marketplace.Version{ID: versionID, Version: "1.0.0", YAMLContent: "schema_version: 1\n", YAMLHash: "abc"}, nil
}

func (f *fakeMarketplaceService) Rate(_ context.Context, _, _ string, _ int, _, _ *string) error { return nil }

func (f *fakeMarketplaceService) ListRatings(_ context.Context, _ string, _, _ int) ([]marketplace.Rating, int, error) {
	return nil, 0, nil
}

func (f *fakeMarketplaceService) RecordDownload(_ context.Context, _, _, _ string, _ *string, _ string) error {
	return nil
}

func (f *fakeMarketplaceService) Stats(_ context.Context, _ string) (*marketplace.Stats, error) {
	return &marketplace.Stats{CapabilityID: "reg-1"}, nil
}

func marketplaceRouter(t *testing.T, svc httpapi.MarketplaceService) http.Handler {
	t.Helper()
	return httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		nil,
		httpapi.WithMarketplace(svc),
	)
}

const publishBody = `{"yaml_content":"schema_version: 1\nname: k8s.pod.restart\nstatus: needs_review\ndomain: kubernetes\nresource_type: pod\noperation: write\nrisk: medium\nbackend:\n  adapter: http\n  method: DELETE\n  path: /api/v1/namespaces/{namespace}/pods/{pod_name}\n  timeout_ms: 10000\ninput_schema:\n  namespace:\n    type: string\n    required: true\n  pod_name:\n    type: string\n    required: true\ngovernance:\n  requires_approval: true\n  rollback:\n    strategy: manual\nauth:\n  roles: [operator]\nai:\n  description: Restart a kubernetes pod\n  examples: [\"restart pod nginx in default\"]\n","version":"1.0.0","visibility":"public"}`

// TestMarketplaceRequiresAuthentication: an authenticated identity must have at
// least one role; a signed token carrying no roles is rejected before the route
// reaches the service.
func TestMarketplaceRequiresAuthentication(t *testing.T) {
	t.Parallel()
	svc := &fakeMarketplaceService{}
	router := marketplaceRouter(t, svc)

	req := signedRequest(t, "/v1/marketplace/capabilities?query=restart", "", "nobody-1", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.Code)
	}
	if svc.searchCalled {
		t.Fatal("service called for an unauthenticated identity")
	}
}

func TestMarketplaceSearchViewerAllowed(t *testing.T) {
	t.Parallel()
	svc := &fakeMarketplaceService{searchTotal: 1}
	router := marketplaceRouter(t, svc)

	req := signedRequest(t, "/v1/marketplace/capabilities?query=restart", "", "viewer-1", []string{"viewer"})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	if !svc.searchCalled {
		t.Fatal("viewer was not allowed to search")
	}
	if !strings.Contains(res.Body.String(), `"total":1`) {
		t.Fatalf("body = %s, want total 1", res.Body.String())
	}
}

// TestMarketplacePublishRequiresAdmin: publishing makes a capability executable
// infrastructure, so only admins may do it.
func TestMarketplacePublishRequiresAdmin(t *testing.T) {
	t.Parallel()
	svc := &fakeMarketplaceService{}
	router := marketplaceRouter(t, svc)

	req := signedRequest(t, "/v1/marketplace/capabilities", publishBody, "operator-1", []string{"operator"})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", res.Code)
	}
	if svc.publishOwner != "" {
		t.Fatal("publish reached the service for a non-admin")
	}
}

// TestMarketplacePublishForcesOwnerFromSubject: the owner comes from the
// authenticated subject, never from the request body, so a caller cannot publish
// on someone else's behalf.
func TestMarketplacePublishForcesOwnerFromSubject(t *testing.T) {
	t.Parallel()
	svc := &fakeMarketplaceService{}
	router := marketplaceRouter(t, svc)

	req := signedRequest(t, "/v1/marketplace/capabilities", publishBody, "admin-1", []string{"admin"})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	if svc.publishOwner != "admin-1" {
		t.Fatalf("owner = %q, want admin-1 (subject, not request body)", svc.publishOwner)
	}
}

func TestMarketplaceStatsViewerAllowed(t *testing.T) {
	t.Parallel()
	svc := &fakeMarketplaceService{}
	router := marketplaceRouter(t, svc)

	req := signedRequest(t, "/v1/marketplace/capabilities/reg-1/stats", "", "viewer-1", []string{"viewer"})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	if !strings.Contains(res.Body.String(), `"capability_id":"reg-1"`) {
		t.Fatalf("body = %s, want stats", res.Body.String())
	}
}

// TestMarketplaceUnconfiguredReturns503: when no marketplace service is wired,
// the read routes must give a clear 503 rather than a confusing 500.
func TestMarketplaceUnconfiguredReturns503(t *testing.T) {
	t.Parallel()
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		nil,
	)

	req := signedRequest(t, "/v1/marketplace/capabilities", "", "viewer-1", []string{"viewer"})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", res.Code)
	}
}

// TestMarketplaceSemanticUnconfiguredReturns503: semantic=true requires a
// semantic service; when the service reports semantic search is unavailable,
// the read route returns a clear 503 rather than a keyword fallback.
func TestMarketplaceSemanticUnconfiguredReturns503(t *testing.T) {
	t.Parallel()
	svc := &fakeMarketplaceService{}
	router := marketplaceRouter(t, svc)

	req := signedRequest(t, "/v1/marketplace/capabilities?semantic=true&query=restart%20kafka", "", "viewer-1", []string{"viewer"})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", res.Code, res.Body.String())
	}
}
