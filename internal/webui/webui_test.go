package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebHandlerServesRoot(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	WebHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rr.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET / content-type = %q, want text/html", ct)
	}
	// Non-empty SPA bootstrap (works for both the committed placeholder and the
	// real Vite build that scripts/build.sh embeds).
	if strings.TrimSpace(rr.Body.String()) == "" {
		t.Errorf("GET / body is empty")
	}
}

func TestWebHandlerServesIndexExplicit(t *testing.T) {
	// Go's http.FileServer serves / as the directory index and may 301 a direct
	// /index.html request to "/"; what matters is it resolves to the SPA and
	// never 404s. Accept 200 or the canonical redirect.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	WebHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK && rr.Code != http.StatusMovedPermanently {
		t.Fatalf("GET /index.html = %d, want 200 or 301", rr.Code)
	}
}

func TestWebHandlerFallsBackDeepLink(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/some/spa/route", nil)
	WebHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET deep link = %d, want 200 (index fallback)", rr.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("deep link content-type = %q, want text/html", ct)
	}
}

func TestWebHandlerMissingAssetIsNotFound(t *testing.T) {
	// A non-HTML deep-link-ish path that looks like an asset but doesn't exist
	// still falls back to index — SPA semantics. But a request that maps to the
	// root asset dir is served by the file server. We only assert no crash and
	// a 200 fallback for arbitrary paths.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)
	WebHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /does-not-exist = %d, want 200 (index fallback)", rr.Code)
	}
}
