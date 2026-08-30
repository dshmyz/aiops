package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

// bucket is a token-bucket rate limiter for a single key (subject or IP).
type bucket struct {
	tokens     float64
	lastRefill time.Time
}

// RateLimiterConfig holds the per-key and per-IP rate limit parameters.
type RateLimiterConfig struct {
	SubjectCapacity float64
	SubjectRefillPS float64
	IPCapacity      float64
	IPRefillPS      float64
}

// DefaultRateLimiterConfig returns production-safe defaults:
//   - Per-Subject: 30 req/min (burst 30, refill 0.5/s)
//   - Per-IP (unauthenticated fallback): 60 req/min (burst 60, refill 1/s)
func DefaultRateLimiterConfig() RateLimiterConfig {
	return RateLimiterConfig{
		SubjectCapacity: 30,
		SubjectRefillPS: 0.5,
		IPCapacity:      60,
		IPRefillPS:      1.0,
	}
}

// RateLimiter is a per-subject and per-IP in-memory token-bucket rate
// limiter. It is safe for concurrent use. Buckets are lazily created and
// periodically cleaned up to prevent unbounded memory growth.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	cfg     RateLimiterConfig
	stop    chan struct{}
}

// NewRateLimiter creates a RateLimiter with the given config and starts a
// background goroutine that purges idle buckets every 5 minutes.
func NewRateLimiter(cfg RateLimiterConfig) *RateLimiter {
	rl := &RateLimiter{
		buckets: make(map[string]*bucket),
		cfg:     cfg,
		stop:    make(chan struct{}),
	}
	go rl.cleanupLoop()
	return rl
}

// Stop terminates the cleanup goroutine. Safe to call multiple times.
func (rl *RateLimiter) Stop() {
	select {
	case <-rl.stop:
	default:
		close(rl.stop)
	}
}

func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.purgeIdle(10 * time.Minute)
		case <-rl.stop:
			return
		}
	}
}

func (rl *RateLimiter) purgeIdle(maxIdle time.Duration) {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for key, b := range rl.buckets {
		if now.Sub(b.lastRefill) > maxIdle {
			delete(rl.buckets, key)
		}
	}
}

// allow checks whether a single request is allowed for the given key. It
// refills the bucket based on elapsed time and consumes one token.
func (rl *RateLimiter) allow(key string, capacity, refillPS float64) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok {
		b = &bucket{tokens: capacity, lastRefill: now}
		rl.buckets[key] = b
	}

	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * refillPS
	if b.tokens > capacity {
		b.tokens = capacity
	}
	b.lastRefill = now

	if b.tokens < 1.0 {
		return false
	}
	b.tokens -= 1.0
	return true
}

// AllowSubject checks the per-subject bucket. Returns false when the rate
// limit is exceeded.
func (rl *RateLimiter) AllowSubject(subject string) bool {
	return rl.allow("sub:"+subject, rl.cfg.SubjectCapacity, rl.cfg.SubjectRefillPS)
}

// AllowIP checks the per-IP bucket. Returns false when the rate limit is
// exceeded.
func (rl *RateLimiter) AllowIP(ip string) bool {
	return rl.allow("ip:"+ip, rl.cfg.IPCapacity, rl.cfg.IPRefillPS)
}

// RateLimitMiddleware returns an http.Handler that enforces rate limits
// before delegating to next. When the request carries a valid Bearer token,
// the subject is extracted for per-subject limiting; otherwise the client IP
// is used. Requests exceeding the limit receive 429 with a Retry-After header.
func RateLimitMiddleware(rl *RateLimiter, auth Authenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var key string
		var allowed bool

		// Try to extract subject from a verified JWT. If authentication fails
		// (no token, invalid signature, expired), fall back to IP limiting so
		// unauthenticated requests are still throttled.
		if auth != nil {
			if user, err := auth.Authenticate(request); err == nil {
				key = user.Subject
				allowed = rl.AllowSubject(key)
			}
		}
		if key == "" {
			ip := clientIP(request)
			allowed = rl.AllowIP(ip)
		}

		if !allowed {
			writer.Header().Set("Retry-After", strconv.Itoa(int(60)))
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(writer).Encode(map[string]string{"error": "rate limit exceeded"})
			return
		}
		next.ServeHTTP(writer, request)
	})
}

// clientIP extracts the client IP from RemoteAddr, stripping the port.
func clientIP(request *http.Request) string {
	addr := request.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}

// Ensure the identity import is used (Authenticator returns identity.CurrentUser).
var _ = identity.CurrentUser{}
