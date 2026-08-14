package knowledge_test

import (
	"context"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/knowledge"
)

func TestMemoryStoreRetrieveBySubstring(t *testing.T) {
	t.Parallel()
	store := &knowledge.MemoryStore{}
	ctx := context.Background()
	_ = store.Add(ctx, knowledge.EmbeddedDocument{
		Document: knowledge.Document{Title: "Runbook A", Content: "When Kafka lag is high, check consumer group offsets.", Source: "runbook"},
	})
	_ = store.Add(ctx, knowledge.EmbeddedDocument{
		Document: knowledge.Document{Title: "Runbook B", Content: "GlusterFS heal command runs automatically.", Source: "runbook"},
	})

	docs, err := store.Retrieve(ctx, "Kafka lag", 1)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d, want 1", len(docs))
	}
	if !contains(docs[0].Content, "Kafka") {
		t.Fatalf("retrieved doc = %q, want Kafka runbook", docs[0].Content)
	}
}

func TestManagerAugmentPromptAppendsRetrievedDocs(t *testing.T) {
	t.Parallel()
	store := &knowledge.MemoryStore{}
	ctx := context.Background()
	_ = store.Add(ctx, knowledge.EmbeddedDocument{
		Document: knowledge.Document{Title: "Kafka Lag", Content: "Check consumer group offsets."},
	})
	mgr := knowledge.NewManager(store, nil, 1)
	augmented := mgr.AugmentPrompt(ctx, "Base prompt.", "Kafka lag high")
	if !contains(augmented, "Base prompt.") {
		t.Fatal("base prompt missing")
	}
	if !contains(augmented, "检索到的运维知识") {
		t.Fatal("retrieved knowledge section missing")
	}
	if !contains(augmented, "Check consumer group offsets") {
		t.Fatal("retrieved document content missing")
	}
}

func contains(s, sub string) bool {
	return len(sub) <= len(s) && (sub == "" || findSubstr(s, sub))
}

func findSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestExecutionIngesterOnComplete verifies that the ingester writes a
// structured document to the store after an execution completes.
func TestExecutionIngesterOnComplete(t *testing.T) {
	t.Parallel()
	store := &knowledge.MemoryStore{}
	ingester := knowledge.NewExecutionIngester(store, nil)

	event := execution.ExecutionEvent{
		PlanID:    "plan-123",
		ToolName:  "topic.retention.set",
		Input:     map[string]any{"environment": "prod", "topic": "orders", "retention_hours": 72},
		Status:    "succeeded",
		Subject:   "ops-alice",
		Timestamp: time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC),
	}
	ingester.OnExecutionComplete(context.Background(), event)

	docs, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d, want 1", len(docs))
	}
	doc := docs[0]
	if !contains(doc.Title, "topic.retention.set") {
		t.Fatalf("title = %q, want tool name", doc.Title)
	}
	if !contains(doc.Title, "succeeded") {
		t.Fatalf("title = %q, want status", doc.Title)
	}
	if doc.Source != "execution-history" {
		t.Fatalf("source = %q, want execution-history", doc.Source)
	}
	if !contains(doc.Content, "ops-alice") {
		t.Fatalf("content missing operator: %q", doc.Content)
	}
	if !contains(doc.Content, "plan-123") {
		t.Fatalf("content missing plan ID: %q", doc.Content)
	}
}

// TestExecutionIngesterFailedExecution verifies that failed executions are
// ingested with a distinct source tag.
func TestExecutionIngesterFailedExecution(t *testing.T) {
	t.Parallel()
	store := &knowledge.MemoryStore{}
	ingester := knowledge.NewExecutionIngester(store, nil)

	event := execution.ExecutionEvent{
		PlanID:    "plan-456",
		ToolName:  "cluster.restart",
		Status:    "failed",
		Subject:   "ops-bob",
		Timestamp: time.Now(),
	}
	ingester.OnExecutionComplete(context.Background(), event)

	docs, _ := store.List(context.Background())
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d, want 1", len(docs))
	}
	if docs[0].Source != "execution-failure" {
		t.Fatalf("source = %q, want execution-failure", docs[0].Source)
	}
}

// TestExecutionIngesterRetrieveByToolName verifies that ingested execution
// records are retrievable by tool name substring.
func TestExecutionIngesterRetrieveByToolName(t *testing.T) {
	t.Parallel()
	store := &knowledge.MemoryStore{}
	ingester := knowledge.NewExecutionIngester(store, nil)

	ingester.OnExecutionComplete(context.Background(), execution.ExecutionEvent{
		PlanID: "p1", ToolName: "topic.retention.set", Status: "succeeded",
		Input: map[string]any{"topic": "orders"}, Subject: "alice", Timestamp: time.Now(),
	})
	ingester.OnExecutionComplete(context.Background(), execution.ExecutionEvent{
		PlanID: "p2", ToolName: "cluster.status.read", Status: "succeeded",
		Subject: "bob", Timestamp: time.Now(),
	})

	docs, err := store.Retrieve(context.Background(), "topic.retention", 1)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d, want 1", len(docs))
	}
	if !contains(docs[0].Content, "topic.retention.set") {
		t.Fatalf("retrieved wrong doc: %q", docs[0].Content)
	}
}

// TestExecutionIngesterRichDocument verifies that the ingested document
// includes RequestID, ResultSummary, and Verification info from the event.
func TestExecutionIngesterRichDocument(t *testing.T) {
	t.Parallel()
	store := &knowledge.MemoryStore{}
	ingester := knowledge.NewExecutionIngester(store, nil)

	event := execution.ExecutionEvent{
		PlanID:        "plan-123",
		RequestID:     "req-abc-456",
		ToolName:      "topic.retention.set",
		Input:         map[string]any{"environment": "prod", "topic": "orders", "retention_hours": 72},
		Status:        "succeeded",
		Subject:       "ops-alice",
		Timestamp:     time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC),
		ResultSummary: `{"outcome":"succeeded"}`,
		Verification: &execution.VerificationResult{
			Status:    "ok",
			ToolName:  "topic.retention.read",
			ElapsedMs: 120,
		},
	}
	ingester.OnExecutionComplete(context.Background(), event)

	docs, _ := store.List(context.Background())
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d, want 1", len(docs))
	}
	doc := docs[0]
	if !contains(doc.Content, "req-abc-456") {
		t.Fatalf("content missing RequestID: %q", doc.Content)
	}
	if !contains(doc.Content, `{"outcome":"succeeded"}`) {
		t.Fatalf("content missing ResultSummary: %q", doc.Content)
	}
	if !contains(doc.Content, "topic.retention.read") {
		t.Fatalf("content missing verification tool: %q", doc.Content)
	}
}

// TestExecutionIngesterSourceConstants verifies that source values use
// the exported constants rather than hardcoded strings.
func TestExecutionIngesterSourceConstants(t *testing.T) {
	t.Parallel()
	if knowledge.SourceExecutionHistory != "execution-history" {
		t.Fatalf("SourceExecutionHistory = %q, want execution-history", knowledge.SourceExecutionHistory)
	}
	if knowledge.SourceExecutionFailure != "execution-failure" {
		t.Fatalf("SourceExecutionFailure = %q, want execution-failure", knowledge.SourceExecutionFailure)
	}
}

// --- Vector retrieval tests ---

// stubEmbedder returns pre-configured vectors for known texts.
type stubEmbedder struct {
	vectors map[string][]float32
}

func (e *stubEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if v, ok := e.vectors[text]; ok {
		return v, nil
	}
	// Return a default vector for unknown texts.
	return []float32{0.1, 0.1, 0.1}, nil
}

func TestMemoryStoreRetrieveByVector(t *testing.T) {
	t.Parallel()
	store := &knowledge.MemoryStore{}
	ctx := context.Background()

	// Add documents with known embeddings.
	_ = store.Add(ctx, knowledge.EmbeddedDocument{
		Document:  knowledge.Document{Title: "Kafka Ops", Content: "Kafka consumer lag troubleshooting"},
		Embedding: []float32{1.0, 0.0, 0.0},
	})
	_ = store.Add(ctx, knowledge.EmbeddedDocument{
		Document:  knowledge.Document{Title: "MinIO Ops", Content: "MinIO disk replacement procedure"},
		Embedding: []float32{0.0, 1.0, 0.0},
	})
	_ = store.Add(ctx, knowledge.EmbeddedDocument{
		Document:  knowledge.Document{Title: "GlusterFS Ops", Content: "GlusterFS volume heal"},
		Embedding: []float32{0.0, 0.0, 1.0},
	})

	// Query vector close to Kafka embedding.
	docs, err := store.RetrieveByVector(ctx, []float32{0.9, 0.1, 0.0}, 2)
	if err != nil {
		t.Fatalf("RetrieveByVector: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("len(docs) = %d, want 2", len(docs))
	}
	if docs[0].Title != "Kafka Ops" {
		t.Fatalf("first result = %q, want Kafka Ops", docs[0].Title)
	}
}

func TestMemoryStoreRetrieveByVectorNoEmbeddings(t *testing.T) {
	t.Parallel()
	store := &knowledge.MemoryStore{}
	ctx := context.Background()

	// Add document without embedding.
	_ = store.Add(ctx, knowledge.EmbeddedDocument{
		Document: knowledge.Document{Title: "No Embedding", Content: "plain text"},
	})

	docs, err := store.RetrieveByVector(ctx, []float32{1.0, 0.0, 0.0}, 5)
	if err != nil {
		t.Fatalf("RetrieveByVector: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("len(docs) = %d, want 0 (no embeddings stored)", len(docs))
	}
}

func TestManagerRetrieveUsesVectorPath(t *testing.T) {
	t.Parallel()
	store := &knowledge.MemoryStore{}
	ctx := context.Background()

	// Add documents with embeddings.
	_ = store.Add(ctx, knowledge.EmbeddedDocument{
		Document:  knowledge.Document{Title: "Kafka Runbook", Content: "Check consumer group offsets"},
		Embedding: []float32{1.0, 0.0, 0.0},
	})
	_ = store.Add(ctx, knowledge.EmbeddedDocument{
		Document:  knowledge.Document{Title: "MinIO Runbook", Content: "Replace failed disk"},
		Embedding: []float32{0.0, 1.0, 0.0},
	})

	// Embedder maps query to a vector close to Kafka.
	embedder := &stubEmbedder{
		vectors: map[string][]float32{
			"kafka lag high": {0.95, 0.05, 0.0},
		},
	}

	mgr := knowledge.NewManager(store, embedder, 1)
	docs, err := mgr.Retrieve(ctx, "kafka lag high", 1)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d, want 1", len(docs))
	}
	if docs[0].Title != "Kafka Runbook" {
		t.Fatalf("result = %q, want Kafka Runbook (vector similarity)", docs[0].Title)
	}
}

func TestManagerRetrieveFallsBackToSubstring(t *testing.T) {
	t.Parallel()
	store := &knowledge.MemoryStore{}
	ctx := context.Background()

	// Document without embedding - vector path will yield nothing.
	_ = store.Add(ctx, knowledge.EmbeddedDocument{
		Document: knowledge.Document{Title: "Plain Doc", Content: "Kafka consumer group reset"},
	})

	embedder := &stubEmbedder{
		vectors: map[string][]float32{
			"kafka reset": {1.0, 0.0, 0.0},
		},
	}

	mgr := knowledge.NewManager(store, embedder, 1)
	_, err := mgr.Retrieve(ctx, "kafka reset", 1)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	// Should fall back to substring: "kafka reset" is not a substring of content,
	// but "Kafka" won't match "kafka" (case-sensitive). Let's use a matching query.
	docs, err := mgr.Retrieve(ctx, "Kafka consumer", 1)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d, want 1 (substring fallback)", len(docs))
	}
}

func TestCosineSimilarityValues(t *testing.T) {
	t.Parallel()
	// Identical vectors → similarity 1.0.
	sim := knowledge.CosineSimilarity([]float32{1, 0, 0}, []float32{1, 0, 0})
	if sim < 0.999 {
		t.Fatalf("identical vectors sim = %f, want ~1.0", sim)
	}
	// Orthogonal vectors → similarity 0.0.
	sim = knowledge.CosineSimilarity([]float32{1, 0, 0}, []float32{0, 1, 0})
	if sim > 0.001 || sim < -0.001 {
		t.Fatalf("orthogonal vectors sim = %f, want ~0.0", sim)
	}
	// Opposite vectors → similarity -1.0.
	sim = knowledge.CosineSimilarity([]float32{1, 0, 0}, []float32{-1, 0, 0})
	if sim > -0.999 {
		t.Fatalf("opposite vectors sim = %f, want ~-1.0", sim)
	}
	// Dimension mismatch → -1.
	sim = knowledge.CosineSimilarity([]float32{1, 0}, []float32{1, 0, 0})
	if sim != -1 {
		t.Fatalf("dimension mismatch sim = %f, want -1", sim)
	}
}
