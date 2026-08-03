package httpapi_test

import (
	"context"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
	"github.com/gracegaoya/ai-operations-copilot/internal/knowledge"
)

// recordingStore captures the EmbeddedDocument passed to Add so tests can
// inspect whether an embedding was computed.
type recordingStore struct {
	added []knowledge.EmbeddedDocument
}

func (s *recordingStore) Add(_ context.Context, doc knowledge.EmbeddedDocument) error {
	s.added = append(s.added, doc)
	return nil
}

func (s *recordingStore) List(_ context.Context) ([]knowledge.Document, error) {
	out := make([]knowledge.Document, len(s.added))
	for i, d := range s.added {
		out[i] = d.Document
	}
	return out, nil
}

func (s *recordingStore) Retrieve(_ context.Context, _ string, _ int) ([]knowledge.Document, error) {
	return nil, nil
}

func (s *recordingStore) RetrieveByVector(_ context.Context, _ []float32, _ int) ([]knowledge.Document, error) {
	return nil, nil
}

// stubEmbedder returns a deterministic non-zero vector so tests can assert the
// embedding was actually computed and stored.
type stubEmbedder struct {
	calls  int
	vector []float32
}

func (e *stubEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	e.calls++
	return e.vector, nil
}

// TestAdminKnowledgeAddDocumentEmbedsWhenEmbedderConfigured verifies that
// manually added documents are embedded before being persisted, so they can be
// found by the vector retrieval path just like execution-ingested documents.
func TestAdminKnowledgeAddDocumentEmbedsWhenEmbedderConfigured(t *testing.T) {
	t.Parallel()
	store := &recordingStore{}
	embedder := &stubEmbedder{vector: []float32{0.1, 0.2, 0.3}}
	svc := httpapi.NewAdminKnowledgeService(store, embedder)

	if _, err := svc.AddDocument(context.Background(), "Kafka Runbook", "Restart consumer group", "runbook"); err != nil {
		t.Fatalf("AddDocument: %v", err)
	}

	if embedder.calls != 1 {
		t.Fatalf("embedder calls = %d, want 1", embedder.calls)
	}
	if len(store.added) != 1 {
		t.Fatalf("stored docs = %d, want 1", len(store.added))
	}
	got := store.added[0].Embedding
	if len(got) != len(embedder.vector) {
		t.Fatalf("embedding len = %d, want %d", len(got), len(embedder.vector))
	}
	for i := range got {
		if got[i] != embedder.vector[i] {
			t.Fatalf("embedding[%d] = %v, want %v", i, got[i], embedder.vector[i])
		}
	}
}

// TestAdminKnowledgeAddDocumentWorksWhenEmbedderAbsent verifies backward
// compatibility: when no embedder is configured, the document is still stored
// (without an embedding) so substring retrieval keeps working.
func TestAdminKnowledgeAddDocumentWorksWhenEmbedderAbsent(t *testing.T) {
	t.Parallel()
	store := &recordingStore{}
	svc := httpapi.NewAdminKnowledgeService(store, nil)

	doc, err := svc.AddDocument(context.Background(), "Plain Runbook", "Reset offsets", "runbook")
	if err != nil {
		t.Fatalf("AddDocument: %v", err)
	}
	if doc.Title != "Plain Runbook" {
		t.Fatalf("doc.Title = %q, want Plain Runbook", doc.Title)
	}
	if len(store.added) != 1 {
		t.Fatalf("stored docs = %d, want 1", len(store.added))
	}
	if len(store.added[0].Embedding) != 0 {
		t.Fatalf("embedding = %v, want empty when embedder absent", store.added[0].Embedding)
	}
}

// TestAdminKnowledgeAddDocumentEmbedsTitleAndContent verifies the embedder
// receives both the title and content as input, mirroring the ExecutionIngester
// behavior so retrieval quality is consistent across ingestion paths.
func TestAdminKnowledgeAddDocumentEmbedsTitleAndContent(t *testing.T) {
	t.Parallel()
	store := &recordingStore{}
	embedder := &capturingEmbedder{vector: []float32{0.5}}
	svc := httpapi.NewAdminKnowledgeService(store, embedder)

	if _, err := svc.AddDocument(context.Background(), "Title X", "Content Y", "runbook"); err != nil {
		t.Fatalf("AddDocument: %v", err)
	}
	if embedder.input == "" {
		t.Fatal("embedder received empty input")
	}
	if !contains(embedder.input, "Title X") || !contains(embedder.input, "Content Y") {
		t.Fatalf("embedder input = %q, want title and content", embedder.input)
	}
}

type capturingEmbedder struct {
	input  string
	vector []float32
}

func (e *capturingEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.input = text
	return e.vector, nil
}
