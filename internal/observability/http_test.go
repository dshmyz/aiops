package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// withTestProvider installs an in-memory exporter as the global tracer
// provider so RequestTracing creates real, inspectable spans. Returns the
// exporter and a cleanup func that restores the previous provider.
func withTestProvider(t *testing.T) (*tracetest.InMemoryExporter, func()) {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	prevTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	return exporter, func() {
		otel.SetTracerProvider(prevTP)
		_ = tp.Shutdown(context.Background())
	}
}

func TestRequestTracingCreatesSpan(t *testing.T) {
	exporter, restore := withTestProvider(t)
	defer restore()

	handler := RequestTracing(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		if !span.IsRecording() {
			t.Error("span is not recording in handler")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/audit-events", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	got := spans[0]
	if got.SpanKind != trace.SpanKindServer {
		t.Errorf("span kind = %v, want server", got.SpanKind)
	}
	if got.Name != "GET /v1/audit-events" {
		t.Errorf("span name = %q, want %q", got.Name, "GET /v1/audit-events")
	}
}

func TestRequestTracingRecordsStatusCode(t *testing.T) {
	exporter, restore := withTestProvider(t)
	defer restore()

	handler := RequestTracing(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/missing", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if got := attrInt(spans[0].Attributes, "http.status_code"); got != http.StatusNotFound {
		t.Errorf("http.status_code = %d, want %d", got, http.StatusNotFound)
	}
}

func TestRequestTracingMarksErrorOn5xx(t *testing.T) {
	exporter, restore := withTestProvider(t)
	defer restore()

	handler := RequestTracing(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/assistant/messages", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Errorf("span status = %v, want Error", spans[0].Status.Code)
	}
}

func TestRequestTracingDoesNotMark4xxAsError(t *testing.T) {
	exporter, restore := withTestProvider(t)
	defer restore()

	handler := RequestTracing(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/bad", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Status.Code == codes.Error {
		t.Errorf("span status = Error, want non-Error for 4xx")
	}
}

func TestRequestTracingInjectsTraceparentHeader(t *testing.T) {
	_, restore := withTestProvider(t)
	defer restore()

	handler := RequestTracing(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/audit-events", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if tp := rec.Header().Get("Traceparent"); tp == "" {
		t.Error("expected Traceparent response header to be set")
	}
}

// attrInt searches the attribute list for an int-valued attribute with the
// given key and returns its value, or -1 when missing.
func attrInt(attrs []attribute.KeyValue, key string) int {
	for _, a := range attrs {
		if string(a.Key) == key && a.Value.Type() == attribute.INT64 {
			return int(a.Value.AsInt64())
		}
	}
	return -1
}

// TestStatusWriterImplementsFlusher verifies the middleware statusWriter
// forwards Flush to the underlying writer, so SSE endpoints
// (/v1/assistant/stream) can assert `writer.(http.Flusher)` successfully
// (Bug3: deterministic 模式流式 500).
func TestStatusWriterImplementsFlusher(t *testing.T) {
	t.Parallel()
	// 用一个会记录是否被 Flush 的底层 writer
	var flushed bool
	base := &flushTrackingWriter{flush: func() { flushed = true }}
	sw := &statusWriter{ResponseWriter: base}

	sw.Flush()
	if !flushed {
		t.Fatal("statusWriter.Flush did not delegate to underlying writer")
	}
	if _, ok := interface{}(sw).(http.Flusher); !ok {
		t.Fatal("statusWriter does not implement http.Flusher")
	}
}

type flushTrackingWriter struct {
	http.ResponseWriter
	flush func()
}

func (w *flushTrackingWriter) Flush() { w.flush() }
