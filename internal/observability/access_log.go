package observability

import (
	"net/http"
	"time"

	"go.uber.org/zap"
)

// accessLogEntry is the structured record emitted for each HTTP request via
// the zap logger. Fields mirror the previous JSON schema so downstream log
// pipelines (Loki, ES) can parse the same keys.
type accessLogEntry struct {
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     int    `json:"status"`
	DurationMs int64  `json:"duration_ms"`
	RequestID  string `json:"request_id,omitempty"`
	RemoteAddr string `json:"remote_addr,omitempty"`
	UserAgent  string `json:"user_agent,omitempty"`
}

// AccessLog is an HTTP middleware that logs every request as a structured
// zap log entry. Both 4xx and 5xx responses are logged (they are never
// skipped), making it suitable for audit and debugging pipelines.
type AccessLog struct{}

// NewAccessLog creates a new AccessLog middleware.
func NewAccessLog() *AccessLog {
	return &AccessLog{}
}

// Middleware wraps the given handler, recording method, path, status, latency,
// request_id, remote_addr, and user_agent for every request. The status code
// is captured via the package-internal statusWriter (defined in http.go) so
// that handlers that never call WriteHeader are correctly recorded as 200.
func (al *AccessLog) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		duration := time.Since(start)

		entry := accessLogEntry{
			Method:     r.Method,
			Path:       r.URL.Path,
			Status:     sw.status,
			DurationMs: duration.Milliseconds(),
			RequestID:  r.Header.Get("X-Request-ID"),
			RemoteAddr: r.RemoteAddr,
			UserAgent:  r.UserAgent(),
		}

		logger := Logger()
		fields := []zap.Field{
			zap.String("method", entry.Method),
			zap.String("path", entry.Path),
			zap.Int("status", entry.Status),
			zap.Int64("duration_ms", entry.DurationMs),
			zap.String("remote_addr", entry.RemoteAddr),
			zap.String("user_agent", entry.UserAgent),
		}
		if entry.RequestID != "" {
			fields = append(fields, zap.String("request_id", entry.RequestID))
		}

		switch {
		case entry.Status >= 500:
			logger.Error("http_request", fields...)
		case entry.Status >= 400:
			logger.Warn("http_request", fields...)
		default:
			logger.Info("http_request", fields...)
		}
	})
}
