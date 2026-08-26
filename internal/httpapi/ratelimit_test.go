package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
)

func TestRateLimiterAllowsUntilCapacity(t *testing.T) {
	t.Parallel()
	rl := httpapi.NewRateLimiter(httpapi.RateLimiterConfig{
		SubjectCapacity: 3,
		SubjectRefillPS: 0, // no refill for deterministic test
		IPCapacity:      100,
		IPRefillPS:      0,
	})
	defer rl.Stop()

	for i := 0; i < 3; i++ {
		if !rl.AllowSubject("user-1") {
			t.Fatalf("request %d should be allowed within capacity", i+1)
		}
	}
	if rl.AllowSubject("user-1") {
		t.Fatal("request 4 should be denied (capacity exhausted)")
	}
}

func TestRateLimiterRefillsOverTime(t *testing.T) {
	t.Parallel()
	rl := httpapi.NewRateLimiter(httpapi.RateLimiterConfig{
		SubjectCapacity: 2,
		SubjectRefillPS: 100, // very fast refill for test
		IPCapacity:      100,
		IPRefillPS:      0,
	})
	defer rl.Stop()

	// Exhaust the bucket.
	rl.AllowSubject("user-1")
	rl.AllowSubject("user-1")
	if rl.AllowSubject("user-1") {
		t.Fatal("third request should be denied before refill")
	}

	// Wait enough time for at least 1 token to refill.
	time.Sleep(30 * time.Millisecond)

	if !rl.AllowSubject("user-1") {
		t.Fatal("request after refill should be allowed")
	}
}

func TestRateLimiterIndependentPerSubject(t *testing.T) {
	t.Parallel()
	rl := httpapi.NewRateLimiter(httpapi.RateLimiterConfig{
		SubjectCapacity: 1,
		SubjectRefillPS: 0,
		IPCapacity:      100,
		IPRefillPS:      0,
	})
	defer rl.Stop()

	if !rl.AllowSubject("user-a") {
		t.Fatal("user-a first request should pass")
	}
	if rl.AllowSubject("user-a") {
		t.Fatal("user-a second request should be denied")
	}
	// user-b has its own bucket.
	if !rl.AllowSubject("user-b") {
		t.Fatal("user-b first request should pass (independent bucket)")
	}
}

func TestRateLimitMiddlewareReturns429(t *testing.T) {
	t.Parallel()
	rl := httpapi.NewRateLimiter(httpapi.RateLimiterConfig{
		SubjectCapacity: 1,
		SubjectRefillPS: 0,
		IPCapacity:      1,
		IPRefillPS:      0,
	})
	defer rl.Stop()

	called := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	})

	// No authenticator — falls back to IP limiting.
	handler := httpapi.RateLimitMiddleware(rl, nil, next)

	req := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	res1 := httptest.NewRecorder()
	handler.ServeHTTP(res1, req)

	if res1.Code != http.StatusOK {
		t.Fatalf("first request: status = %d, want 200", res1.Code)
	}

	res2 := httptest.NewRecorder()
	handler.ServeHTTP(res2, req)

	if res2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: status = %d, want 429", res2.Code)
	}
	if res2.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After header should be set on 429")
	}
	var body map[string]string
	if err := json.NewDecoder(res2.Body).Decode(&body); err != nil {
		t.Fatalf("decode 429 body: %v", err)
	}
	if body["error"] != "rate limit exceeded" {
		t.Fatalf("error = %q, want 'rate limit exceeded'", body["error"])
	}
	if called != 1 {
		t.Fatalf("downstream called %d times, want 1", called)
	}
}

func TestRateLimitMiddlewareWithAuth(t *testing.T) {
	t.Parallel()
	auth := httpapi.NewHMACAuthenticator([]byte("test-secret"))
	rl := httpapi.NewRateLimiter(httpapi.RateLimiterConfig{
		SubjectCapacity: 1,
		SubjectRefillPS: 0,
		IPCapacity:      1,
		IPRefillPS:      0,
	})
	defer rl.Stop()

	called := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	})
	handler := httpapi.RateLimitMiddleware(rl, auth, next)

	// First request with valid JWT → subject limiting.
	token := signedJWT(t, map[string]any{
		"sub":   "user-1",
		"roles": []string{"viewer"},
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.RemoteAddr = "1.2.3.4:5678"

	res1 := httptest.NewRecorder()
	handler.ServeHTTP(res1, req)
	if res1.Code != http.StatusOK {
		t.Fatalf("first authenticated request: status = %d, want 200", res1.Code)
	}

	// Second request same subject → 429.
	res2 := httptest.NewRecorder()
	handler.ServeHTTP(res2, req)
	if res2.Code != http.StatusTooManyRequests {
		t.Fatalf("second authenticated request: status = %d, want 429", res2.Code)
	}

	// Request from a different subject should still pass (independent bucket).
	token2 := signedJWT(t, map[string]any{
		"sub":   "user-2",
		"roles": []string{"viewer"},
	})
	req2 := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	req2.Header.Set("Authorization", "Bearer "+token2)
	req2.RemoteAddr = "1.2.3.4:5678"
	res3 := httptest.NewRecorder()
	handler.ServeHTTP(res3, req2)
	if res3.Code != http.StatusOK {
		t.Fatalf("different subject request: status = %d, want 200", res3.Code)
	}
}
