package assistant_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// withTestTracer installs an in-memory exporter as the global tracer provider
// so assistant spans become inspectable. Returns the exporter; the cleanup
// restores the previous provider. Must not be combined with t.Parallel().
func withTestTracer(t *testing.T) *tracetest.InMemoryExporter {
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

func TestHandleMessageCreatesSpans(t *testing.T) {
	exporter := withTestTracer(t)

	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		ToolName: tools.ClusterStatusRead,
		Input:    map[string]any{"environment": "prod"},
	}})

	_, err := service.HandleMessage(context.Background(), viewer(), "查看 prod 集群状态", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}

	names := spanNames(exporter.GetSpans())
	for _, want := range []string{"assistant.HandleMessage", "planner.Plan", "execute_read"} {
		if !containsString(names, want) {
			t.Errorf("missing span %q; got %v", want, names)
		}
	}
}

func TestHandleMessageCreatesPlanSpanForWrite(t *testing.T) {
	exporter := withTestTracer(t)

	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		ToolName: tools.TopicRetentionSet,
		Input:    retentionInput(),
	}})

	_, err := service.HandleMessage(context.Background(), admin(), "retention", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}

	names := spanNames(exporter.GetSpans())
	if !containsString(names, "create_plan") {
		t.Errorf("missing create_plan span; got %v", names)
	}
	if !containsString(names, "assistant.HandleMessage") {
		t.Errorf("missing assistant.HandleMessage span; got %v", names)
	}
}

func spanNames(spans []tracetest.SpanStub) []string {
	names := make([]string, 0, len(spans))
	for _, s := range spans {
		names = append(names, s.Name)
	}
	return names
}

func containsString(slice []string, value string) bool {
	for _, s := range slice {
		if s == value {
			return true
		}
	}
	return false
}
