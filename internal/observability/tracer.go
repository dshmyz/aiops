// Package observability wires OpenTelemetry tracing for the copilot backend.
// It exposes a single InitTracer entry point (stdout by default, OTLP when
// configured) and an HTTP server middleware that creates a root span per
// request.
package observability

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// noopSpanExporter 丢弃所有 span（OTLP 未配置时的默认行为，避免 stdout 被 span JSON 刷屏）。
type noopSpanExporter struct{}

func (n *noopSpanExporter) ExportSpans(_ context.Context, _ []sdktrace.ReadOnlySpan) error { return nil }
func (n *noopSpanExporter) Shutdown(_ context.Context) error                              { return nil }

// tracerName is the single tracer used across the copilot backend so that all
// spans share a common instrumentation scope.
const tracerName = "github.com/gracegaoya/ai-operations-copilot"

// Config configures the global tracer provider.
type Config struct {
	// ServiceName identifies the service in the trace backend (e.g. "copilot-api").
	ServiceName string
	// Exporter selects the exporter backend: "stdout" (default) or "otlp".
	Exporter string
	// OTLPEndpoint is the OTLP/HTTP collector address (e.g. "localhost:4318").
	// Only used when Exporter == "otlp".
	OTLPEndpoint string
	// SamplingRatio in [0,1]; defaults to 1.0 (always sample).
	SamplingRatio float64
}

// InitTracer initializes the global TracerProvider and returns a shutdown
// function the caller must invoke on graceful exit. When Exporter is "otlp"
// traces are sent via OTLP/HTTP; otherwise (or when empty) traces are written
// to stdout in pretty-printed JSON. The W3C tracecontext propagator is also
// installed so incoming traceparent headers are honored.
func InitTracer(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	exporter, err := buildExporter(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("build trace exporter: %w", err)
	}
	res, err := resource.New(ctx, resource.WithAttributes(
		semconv.ServiceName(cfg.ServiceName),
	))
	if err != nil {
		return nil, fmt.Errorf("build resource: %w", err)
	}
	ratio := cfg.SamplingRatio
	if ratio <= 0 {
		ratio = 1.0
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(ratio)),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return tp.Shutdown, nil
}

func buildExporter(ctx context.Context, cfg Config) (sdktrace.SpanExporter, error) {
	switch cfg.Exporter {
	case "":
		// 未配置 OTLP 时默认不导出 span 到 stdout（避免日志被 span JSON 刷屏）。
		// 如需本地调试可设 COPILOT_OTEL_EXPORTER=stdout。
		return &noopSpanExporter{}, nil
	case "stdout":
		return stdouttrace.New(stdouttrace.WithWriter(os.Stdout), stdouttrace.WithPrettyPrint())
	case "otlp":
		opts := []otlptracehttp.Option{otlptracehttp.WithInsecure()}
		if cfg.OTLPEndpoint != "" {
			opts = append(opts, otlptracehttp.WithEndpoint(cfg.OTLPEndpoint))
		}
		return otlptracehttp.New(ctx, opts...)
	default:
		return nil, fmt.Errorf("unknown exporter: %s", cfg.Exporter)
	}
}
