package observability

import (
	"context"
	"os"
	"strings"
	"sync/atomic"

	"go.uber.org/zap"
)

// loggerHolder keeps a single process-wide zap.Logger so that all packages
// can retrieve it via Logger() without threading it through every constructor.
var loggerHolder atomic.Pointer[zap.Logger]

// InitLogger initializes the global zap.Logger with the given level string.
// Supported levels: debug, info, warn, error (case-insensitive). Unknown
// levels default to info. The logger writes JSON to stdout and includes the
// "service" field on every entry.
func InitLogger(level string) *zap.Logger {
	zapLevel := zap.InfoLevel
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		zapLevel = zap.DebugLevel
	case "info":
		zapLevel = zap.InfoLevel
	case "warn", "warning":
		zapLevel = zap.WarnLevel
	case "error":
		zapLevel = zap.ErrorLevel
	}

	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(zapLevel)
	cfg.Encoding = "json"
	cfg.OutputPaths = []string{"stdout"}
	cfg.ErrorOutputPaths = []string{"stderr"}
	cfg.InitialFields = map[string]any{
		"service": "copilot-api",
	}

	l, err := cfg.Build()
	if err != nil {
		// Fall back to a no-op logger rather than crashing.
		l = zap.NewNop()
	}
	loggerHolder.Store(l)
	return l
}

// Logger returns the process-wide zap.Logger. If InitLogger was never called
// it lazily initializes with defaults (info level, JSON, stdout) so early
// callers (e.g. before main finishes setup) never get a nil panic.
func Logger() *zap.Logger {
	l := loggerHolder.Load()
	if l == nil {
		l = InitLogger(os.Getenv("COPILOT_LOG_LEVEL"))
	}
	return l
}

// SetLogger replaces the process-wide zap.Logger. Intended for tests that need
// to capture log output by writing to a buffer-backed logger.
func SetLogger(l *zap.Logger) {
	loggerHolder.Store(l)
}

// contextKey is unexported to avoid collisions with other context keys.
type contextKey int

const (
	// requestIDKey holds the X-Request-ID string value in context.
	requestIDKey contextKey = iota
	// traceIDKey holds the OpenTelemetry trace ID in context.
	traceIDKey
	// subjectKey holds the authenticated subject in context.
	subjectKey
)

// WithRequestID returns a context carrying the request ID so downstream
// loggers can include it via LoggerFromContext.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

// WithTraceID returns a context carrying the trace ID.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

// WithSubject returns a context carrying the authenticated subject.
func WithSubject(ctx context.Context, subject string) context.Context {
	return context.WithValue(ctx, subjectKey, subject)
}

// LoggerFromContext returns a zap.Logger enriched with request_id, trace_id,
// and subject fields pulled from the context. When none are present the base
// logger is returned unchanged.
func LoggerFromContext(ctx context.Context) *zap.Logger {
	l := Logger()
	fields := make([]zap.Field, 0, 3)
	if v, ok := ctx.Value(requestIDKey).(string); ok && v != "" {
		fields = append(fields, zap.String("request_id", v))
	}
	if v, ok := ctx.Value(traceIDKey).(string); ok && v != "" {
		fields = append(fields, zap.String("trace_id", v))
	}
	if v, ok := ctx.Value(subjectKey).(string); ok && v != "" {
		fields = append(fields, zap.String("subject", v))
	}
	if len(fields) == 0 {
		return l
	}
	return l.With(fields...)
}
