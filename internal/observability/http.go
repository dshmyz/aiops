package observability

import (
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// RequestTracing wraps an http.Handler with OpenTelemetry server-side tracing.
// Each request produces a span named "METHOD path" carrying http.method,
// http.target, and http.status_code attributes. Responses with status >= 500
// are marked as errors. The W3C traceparent is injected into the response
// header so the frontend can correlate a request with its trace.
func RequestTracing(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := propagation.TraceContext{}.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := otel.Tracer(tracerName).Start(
			ctx, r.Method+" "+r.URL.Path,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.target", r.URL.Path),
			),
		)
		defer span.End()

		// Inject request_id and trace_id into context so LoggerFromContext can
		// automatically attach them to every structured log entry downstream.
		if rid := r.Header.Get("X-Request-ID"); rid != "" {
			ctx = WithRequestID(ctx, rid)
		}
		if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
			ctx = WithTraceID(ctx, sc.TraceID().String())
		}

		ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		handler.ServeHTTP(ww, r.WithContext(ctx))

		span.SetAttributes(attribute.Int64("http.status_code", int64(ww.status)))
		if ww.status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(ww.status))
		}
		// Surface the trace context to the client so the frontend can log or
		// display the trace ID alongside failed requests.
		propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(w.Header()))
	})
}

// statusWriter captures the response status code without buffering the body.
// It is intentionally minimal: WriteHeader records the first status passed and
// delegates immediately; Write ensures a default 200 is recorded when the
// handler never calls WriteHeader.
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.status = status
	w.wrote = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.wrote = true
	}
	return w.ResponseWriter.Write(b)
}

// Flush 委托给底层 writer（若支持），使 statusWriter 也实现 http.Flusher。
// 否则 SSE 端点（/v1/assistant/stream）的 `writer.(http.Flusher)` 断言会失败，
// 导致 deterministic 模式流式请求返回 "streaming not supported"。
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
