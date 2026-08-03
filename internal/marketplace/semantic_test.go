package marketplace

import (
	"context"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/knowledge"
)

// stubEmbedder returns a fixed vector for every text, so tests exercise the
// vector path without a real embedding endpoint.
type stubEmbedder struct{}

func (stubEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	// Deterministic small vector keyed by first rune so different names land in
	// different vectors; good enough for retrieval-shape assertions.
	return []float32{1.0, float32(len(text)), 0.5}, nil
}

func semanticYAML(name, description string) string {
	return "schema_version: 1\n" +
		"name: " + name + "\n" +
		"status: published\n" +
		"domain: kubernetes\n" +
		"resource_type: pod\n" +
		"operation: write\n" +
		"risk: medium\n" +
		"backend:\n" +
		"    adapter: http\n" +
		"    method: POST\n" +
		"    path: /api/{namespace}/{pod}/restart\n" +
		"    timeout_ms: 10000\n" +
		"    base_url: http://127.0.0.1:19090\n" +
		"input_schema:\n" +
		"    namespace:\n" +
		"        type: string\n" +
		"        required: true\n" +
		"    pod:\n" +
		"        type: string\n" +
		"        required: true\n" +
		"    environment:\n" +
		"        type: string\n" +
		"        required: true\n" +
		"output:\n" +
		"    kind: change\n" +
		"    summary_template: Restarted {pod} in {namespace}\n" +
		"governance:\n" +
		"    requires_action_plan: true\n" +
		"    requires_approval: true\n" +
		"    precheck_tools:\n" +
		"        - k8s.pod.status.read\n" +
		"    rollback:\n" +
		"        strategy: \"manual\"\n" +
		"auth:\n" +
		"    roles:\n" +
		"        - operator\n" +
		"        - admin\n" +
		"    environment_scoped: true\n" +
		"ai:\n" +
		"    description: " + description + "\n" +
		"    examples:\n" +
		"        - restart the resource\n"
}

// TestSemanticSearchUnconfigured: without EnableSemantic the service must report
// semantic search as unavailable rather than panic or fall back silently.
func TestSemanticSearchUnconfigured(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.SemanticSearch(context.Background(), "restart kafka", 10, 20)
	if err != ErrSemanticUnavailable {
		t.Fatalf("err = %v, want ErrSemanticUnavailable", err)
	}
}

// TestSemanticSearchIndexOnPublish: publishing auto-indexes the capability so a
// natural-language query finds it.
func TestSemanticSearchIndexOnPublish(t *testing.T) {
	svc := newTestService(t)
	store := &knowledge.MemoryStore{}
	svc.EnableSemantic(store, stubEmbedder{})

	reg, _, err := svc.Publish(context.Background(), PublishRequest{
		YAMLContent: semanticYAML("kafka.broker.restart", "Restart a Kafka broker to recover from a stuck cluster"),
		Version:     "1.0.0",
		OwnerID:     "admin-1",
		Visibility:  VisibilityPublic,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	docs, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d, want 1 (auto-indexed)", len(docs))
	}
	if docs[0].ID != "capability:"+reg.ID {
		t.Errorf("doc ID = %q, want capability:%s", docs[0].ID, reg.ID)
	}
	if docs[0].Source != SourceCapabilitySemantic {
		t.Errorf("doc source = %q, want capability-marketplace", docs[0].Source)
	}
}

// TestSemanticSearchFindsCapability: the indexed capability is returned for a
// natural-language query even without an exact keyword match in its name.
func TestSemanticSearchFindsCapability(t *testing.T) {
	svc := newTestService(t)
	store := &knowledge.MemoryStore{}
	svc.EnableSemantic(store, stubEmbedder{})

	_, _, err := svc.Publish(context.Background(), PublishRequest{
		YAMLContent: semanticYAML("kafka.broker.restart", "Restart a Kafka broker to recover from a stuck cluster"),
		Version:     "1.0.0", OwnerID: "admin-1", Visibility: VisibilityPublic,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	results, err := svc.SemanticSearch(context.Background(), "我想重启 Kafka", 10, 20)
	if err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Name != "kafka.broker.restart" {
		t.Errorf("result name = %q, want kafka.broker.restart", results[0].Name)
	}
}

// TestSemanticReIndexDoesNotDuplicate: re-publishing the same capability replaces
// the document instead of stacking duplicates.
func TestSemanticReIndexDoesNotDuplicate(t *testing.T) {
	svc := newTestService(t)
	store := &knowledge.MemoryStore{}
	svc.EnableSemantic(store, stubEmbedder{})

	_, _, err := svc.Publish(context.Background(), PublishRequest{
		YAMLContent: semanticYAML("kafka.broker.restart", "v1 description"),
		Version:     "1.0.0", OwnerID: "admin-1", Visibility: VisibilityPublic,
	})
	if err != nil {
		t.Fatalf("Publish v1: %v", err)
	}
	if _, _, err := svc.Publish(context.Background(), PublishRequest{
		YAMLContent: semanticYAML("kafka.broker.restart", "v2 description"),
		Version:     "1.1.0", OwnerID: "admin-1", Visibility: VisibilityPublic,
	}); err != nil {
		t.Fatalf("Publish v2: %v", err)
	}

	docs, _ := store.List(context.Background())
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d, want 1 after re-publish", len(docs))
	}
}

// TestSemanticSearchFiltersDeprecated: a deprecated capability leaves the index.
func TestSemanticSearchFiltersDeprecated(t *testing.T) {
	svc := newTestService(t)
	store := &knowledge.MemoryStore{}
	svc.EnableSemantic(store, stubEmbedder{})

	reg, _, err := svc.Publish(context.Background(), PublishRequest{
		YAMLContent: semanticYAML("legacy.tool.run", "runs the legacy tool"),
		Version:     "1.0.0", OwnerID: "admin-1", Visibility: VisibilityPublic,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := svc.Deprecate(context.Background(), reg.ID, "no longer supported"); err != nil {
		t.Fatalf("Deprecate: %v", err)
	}
	docs, _ := store.List(context.Background())
	if len(docs) != 0 {
		t.Fatalf("len(docs) = %d, want 0 after deprecate", len(docs))
	}
	results, err := svc.SemanticSearch(context.Background(), "legacy tool", 10, 20)
	if err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("len(results) = %d, want 0 (deprecated removed)", len(results))
	}
}
