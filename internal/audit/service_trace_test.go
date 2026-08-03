package audit_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// withAuditTestTracer installs an in-memory exporter as the global tracer
// provider so audit.Record can observe the active span. Must not be combined
// with t.Parallel().
func withAuditTestTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(
		trace.WithSyncer(exporter),
		trace.WithSampler(trace.AlwaysSample()),
	)
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})
	return exporter
}

// TestRecordCapturesTraceIDFromContext verifies that audit.Record correlates
// the persisted event with the active OpenTelemetry span's trace ID. This is
// the foundation for jumping from an audit log entry back to the distributed
// trace that produced it.
func TestRecordCapturesTraceIDFromContext(t *testing.T) {
	withAuditTestTracer(t)
	repository := store.NewMemoryActionPlanStore()
	service := audit.NewService(repository)

	tracer := otel.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test.span")
	defer span.End()

	err := service.Record(ctx, audit.Event{
		ID:       "audit-trace-1",
		PlanID:   "plan-1",
		ToolName: "topic.retention.set",
		Action:   audit.ActionPlanCreated,
		Decision: audit.DecisionPermitted,
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	events := repository.AuditEvents()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	expected := span.SpanContext().TraceID().String()
	if events[0].TraceID != expected {
		t.Fatalf("TraceID = %q, want %q", events[0].TraceID, expected)
	}
}

// TestRecordLeavesTraceIDEmptyWhenContextHasNoSpan verifies that audit.Record
// does not synthesize a fake trace ID when no span is active (e.g. scheduler
// invocations or tests without a tracer provider). Empty is the honest signal
// that no trace exists for this event.
func TestRecordLeavesTraceIDEmptyWhenContextHasNoSpan(t *testing.T) {
	repository := store.NewMemoryActionPlanStore()
	service := audit.NewService(repository)

	err := service.Record(context.Background(), audit.Event{
		ID:       "audit-trace-2",
		PlanID:   "plan-1",
		ToolName: "topic.retention.set",
		Action:   audit.ActionPlanCreated,
		Decision: audit.DecisionPermitted,
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	events := repository.AuditEvents()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].TraceID != "" {
		t.Fatalf("TraceID = %q, want empty when no active span", events[0].TraceID)
	}
}
