package observability

import (
	"context"
	"testing"
)

func TestInitTracerStdoutReturnsShutdown(t *testing.T) {
	shutdown, err := InitTracer(context.Background(), Config{
		ServiceName: "test",
		Exporter:    "stdout",
	})
	if err != nil {
		t.Fatalf("InitTracer: %v", err)
	}
	if shutdown == nil {
		t.Fatal("shutdown is nil")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestInitTracerEmptyExporterDefaultsToStdout(t *testing.T) {
	shutdown, err := InitTracer(context.Background(), Config{
		ServiceName: "test",
	})
	if err != nil {
		t.Fatalf("InitTracer: %v", err)
	}
	defer shutdown(context.Background())
}

func TestInitTracerUnknownExporterErrors(t *testing.T) {
	_, err := InitTracer(context.Background(), Config{
		ServiceName: "test",
		Exporter:    "bogus",
	})
	if err == nil {
		t.Fatal("expected error for unknown exporter")
	}
}
