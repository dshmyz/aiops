package observability

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// captureLog replaces the global zap logger with one that writes JSON to a
// buffer, so tests can assert on the emitted log entries. The original logger
// is restored on cleanup.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "ts"
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderCfg),
		zapcore.AddSync(&buf),
		zap.InfoLevel,
	)
	testLogger := zap.New(core)
	orig := loggerHolder.Load()
	SetLogger(testLogger)
	t.Cleanup(func() {
		if orig != nil {
			SetLogger(orig)
		}
	})
	return &buf
}

// extractJSON finds the first '{' in the log line and returns the JSON
// substring. The standard logger prepends a date/time prefix, so we need to
// strip it before unmarshalling.
func extractJSON(t *testing.T, line string) string {
	t.Helper()
	idx := strings.Index(line, "{")
	if idx < 0 {
		t.Fatalf("no JSON object in log line: %q", line)
	}
	return line[idx:]
}

func TestAccessLogMiddleware(t *testing.T) {
	buf := captureLog(t)

	al := NewAccessLog()
	handler := al.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	req.Header.Set("X-Request-ID", "req-abc-123")
	req.Header.Set("User-Agent", "test-agent/1.0")
	req.RemoteAddr = "192.168.1.10:54321"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var entry accessLogEntry
	if err := json.Unmarshal([]byte(extractJSON(t, buf.String())), &entry); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if entry.Method != "GET" {
		t.Errorf("method = %q, want GET", entry.Method)
	}
	if entry.Path != "/v1/capabilities" {
		t.Errorf("path = %q, want /v1/capabilities", entry.Path)
	}
	if entry.Status != http.StatusOK {
		t.Errorf("status = %d, want %d", entry.Status, http.StatusOK)
	}
	if entry.RequestID != "req-abc-123" {
		t.Errorf("request_id = %q, want req-abc-123", entry.RequestID)
	}
	if entry.RemoteAddr != "192.168.1.10:54321" {
		t.Errorf("remote_addr = %q, want 192.168.1.10:54321", entry.RemoteAddr)
	}
	if entry.UserAgent != "test-agent/1.0" {
		t.Errorf("user_agent = %q, want test-agent/1.0", entry.UserAgent)
	}
	if entry.DurationMs < 0 {
		t.Errorf("duration_ms = %d, should be >= 0", entry.DurationMs)
	}
}

func TestAccessLogRecordsDefaultStatusWhenNoWriteHeader(t *testing.T) {
	buf := captureLog(t)

	al := NewAccessLog()
	handler := al.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Intentionally do not call WriteHeader — handler defaults to 200.
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var entry accessLogEntry
	if err := json.Unmarshal([]byte(extractJSON(t, buf.String())), &entry); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if entry.Status != http.StatusOK {
		t.Errorf("status = %d, want 200 (default)", entry.Status)
	}
}

func TestAccessLogRecords4xx(t *testing.T) {
	buf := captureLog(t)

	al := NewAccessLog()
	handler := al.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/bad", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var entry accessLogEntry
	if err := json.Unmarshal([]byte(extractJSON(t, buf.String())), &entry); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if entry.Status != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", entry.Status, http.StatusBadRequest)
	}
}

func TestAccessLogRecords5xx(t *testing.T) {
	buf := captureLog(t)

	al := NewAccessLog()
	handler := al.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/assistant/messages", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var entry accessLogEntry
	if err := json.Unmarshal([]byte(extractJSON(t, buf.String())), &entry); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if entry.Status != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", entry.Status, http.StatusInternalServerError)
	}
}

func TestAccessLogRecords404(t *testing.T) {
	buf := captureLog(t)

	al := NewAccessLog()
	handler := al.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/missing", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var entry accessLogEntry
	if err := json.Unmarshal([]byte(extractJSON(t, buf.String())), &entry); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if entry.Status != http.StatusNotFound {
		t.Errorf("status = %d, want %d", entry.Status, http.StatusNotFound)
	}
}

func TestAccessLogJSONFields(t *testing.T) {
	buf := captureLog(t)

	al := NewAccessLog()
	handler := al.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/action-plans", nil)
	req.Header.Set("X-Request-ID", "trace-xyz")
	req.Header.Set("User-Agent", "curl/8.0")
	req.RemoteAddr = "10.0.0.1:9999"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Parse into a generic map to verify the exact JSON field names.
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(extractJSON(t, buf.String())), &raw); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}

	expected := map[string]interface{}{
		"method":      "POST",
		"path":        "/v1/action-plans",
		"status":      float64(http.StatusCreated),
		"duration_ms": raw["duration_ms"], // just check presence
		"request_id":  "trace-xyz",
		"remote_addr": "10.0.0.1:9999",
		"user_agent":  "curl/8.0",
	}

	for key, want := range expected {
		got, ok := raw[key]
		if !ok {
			t.Errorf("missing field %q in JSON output", key)
			continue
		}
		if key == "duration_ms" {
			continue // presence already checked
		}
		if got != want {
			t.Errorf("field %q = %v, want %v", key, got, want)
		}
	}
}
