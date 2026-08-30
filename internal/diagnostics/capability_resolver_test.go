package diagnostics_test

import (
	"context"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func sampleCap(name, domain, resourceType string, op tools.Operation, fields map[string]string) capabilities.Capability {
	schema := make(map[string]capabilities.InputField, len(fields))
	for f, typ := range fields {
		schema[f] = capabilities.InputField{Type: typ}
	}
	return capabilities.Capability{
		Name:         name,
		Domain:       domain,
		ResourceType: resourceType,
		Operation:    op,
		InputSchema:  schema,
	}
}

// TestPublishedCapabilityResolverResolvesDomainToTool verifies the resolver maps
// a diagnostic domain to the matching published read capability (tool name,
// resource type and input schema) rather than a hardcoded switch.
func TestPublishedCapabilityResolverResolvesDomainToTool(t *testing.T) {
	t.Parallel()
	loaded := []capabilities.Capability{
		sampleCap("glusterfs.volume.health.read", "glusterfs", "volume", tools.Read, map[string]string{"environment": "string", "name": "string"}),
		sampleCap("minio.bucket.health.read", "minio", "bucket", tools.Read, map[string]string{"environment": "string", "name": "string"}),
		sampleCap("kafka.consumer_lag.read", "kafka", "consumer_group", tools.Read, map[string]string{"environment": "string", "name": "string"}),
	}
	resolver := diagnostics.NewCapabilityResolver(loaded)

	// domain + resource type exact match.
	name, rt, schema, ok := resolver.ResolveDiagnosticTool("kafka", "consumer_group", string(tools.Read))
	if !ok || name != "kafka.consumer_lag.read" || rt != "consumer_group" {
		t.Fatalf("kafka resolve = (%q,%q,%v), want (kafka.consumer_lag.read,consumer_group,true)", name, rt, ok)
	}
	if schema == nil || schema["name"] == nil {
		t.Fatalf("kafka schema = %#v, want name field present", schema)
	}

	// resource type not requested falls back to the domain's first read tool.
	name, rt, _, ok = resolver.ResolveDiagnosticTool("minio", "", string(tools.Read))
	if !ok || name != "minio.bucket.health.read" || rt != "bucket" {
		t.Fatalf("minio resolve = (%q,%q,%v), want (minio.bucket.health.read,bucket,true)", name, rt, ok)
	}

	// write operation is not a diagnostic tool.
	_, _, _, ok = resolver.ResolveDiagnosticTool("kafka", "", string(tools.Write))
	if ok {
		t.Fatalf("write operation unexpectedly resolved as a diagnostic tool")
	}
}

// TestPublishedCapabilityResolverMissingDomainFallsBack verifies the resolver
// reports ok=false for an unknown domain so the service falls back to the
// hardcoded switch.
func TestPublishedCapabilityResolverMissingDomainFallsBack(t *testing.T) {
	t.Parallel()
	loaded := []capabilities.Capability{
		sampleCap("glusterfs.volume.health.read", "glusterfs", "volume", tools.Read, nil),
	}
	resolver := diagnostics.NewCapabilityResolver(loaded)

	_, _, _, ok := resolver.ResolveDiagnosticTool("redis", "volume", string(tools.Read))
	if ok {
		t.Fatal("unknown domain unexpectedly resolved")
	}
}

// TestPublishedCapabilityResolverInputSchemaExposesFieldNames verifies the
// resolver converts capability input_schema keys into the map the diagnostics
// service uses to key the read input (cluster + the resource field).
func TestPublishedCapabilityResolverInputSchemaExposesFieldNames(t *testing.T) {
	t.Parallel()
	loaded := []capabilities.Capability{
		sampleCap("glusterfs.volume.health.read", "glusterfs", "volume", tools.Read, map[string]string{"environment": "string", "name": "string"}),
	}
	resolver := diagnostics.NewCapabilityResolver(loaded)

	_, _, schema, ok := resolver.ResolveDiagnosticTool("glusterfs", "volume", string(tools.Read))
	if !ok {
		t.Fatal("glusterfs resolve failed")
	}
	if len(schema) != 2 {
		t.Fatalf("schema len = %d, want 2 (environment + name)", len(schema))
	}
	if schema["environment"] == nil || schema["name"] == nil {
		t.Fatalf("schema keys = %v, want environment and name", schema)
	}
}

// TestNewMiddlewareDomainDiagnosableViaYamlOnly is the payoff test for the
// externalization goal: a domain that the hardcoded resolveRunbook switch does
// NOT know (so without a resolver it would fail with ErrUnsupportedDomain) is
// fully diagnosable in production just by shipping a published capability. This
// proves adding a new middleware domain requires no Go change.
func TestNewMiddlewareDomainDiagnosableViaYamlOnly(t *testing.T) {
	t.Parallel()
	reads := &fakeReads{result: map[string]any{"status": "ok", "unhealthy": 0}}
	loaded := []capabilities.Capability{
		sampleCap("cache.memcached.health.read", "cache", "cluster", tools.Read, map[string]string{"cluster": "string", "name": "string"}),
	}
	service := diagnostics.NewService(reads, nil).
		WithCapabilityResolver(diagnostics.NewCapabilityResolver(loaded))

	// "cache" is not in resolveRunbook's switch (glusterfs/minio/kafka only).
	pkg, err := service.Run(context.Background(), user(), diagnostics.Request{
		Domain:       "cache",
		ResourceType: "cluster",
		ResourceName: "orders-cache",
	})
	if err != nil {
		t.Fatalf("new-domain via YAML capability failed: %v (want no UnsupportedDomain)", err)
	}
	if reads.toolName != "cache.memcached.health.read" {
		t.Fatalf("tool = %q, want cache.memcached.health.read from YAML capability", reads.toolName)
	}
	if pkg.Resources[0].Type != "cluster" {
		t.Fatalf("resource type = %q, want cluster (sourced from capability, not switch)", pkg.Resources[0].Type)
	}
}
