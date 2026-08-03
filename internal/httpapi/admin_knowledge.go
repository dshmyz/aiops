package httpapi

import (
	"context"

	"github.com/gracegaoya/ai-operations-copilot/internal/knowledge"
)

// adminKnowledgeService wraps a knowledge.Store to satisfy the router's
// KnowledgeService interface. When an embedder is configured, manually added
// documents are embedded before persistence so they participate in vector
// retrieval just like execution-ingested documents.
type adminKnowledgeService struct {
	store    knowledge.Store
	embedder knowledge.Embedder
}

func (s *adminKnowledgeService) AddDocument(ctx context.Context, title, content, source string) (knowledge.Document, error) {
	doc := knowledge.EmbeddedDocument{Document: knowledge.Document{Title: title, Content: content, Source: source}}
	if s.embedder != nil {
		if vec, err := s.embedder.Embed(ctx, title+"\n"+content); err == nil {
			doc.Embedding = vec
		}
	}
	if err := s.store.Add(ctx, doc); err != nil {
		return knowledge.Document{}, err
	}
	return doc.Document, nil
}

func (s *adminKnowledgeService) ListDocuments(ctx context.Context) ([]knowledge.Document, error) {
	return s.store.List(ctx)
}

// NewAdminKnowledgeService adapts a knowledge.Store (and optional embedder) to
// the router's KnowledgeService interface. embedder may be nil, in which case
// documents are stored without embeddings and only substring retrieval applies.
func NewAdminKnowledgeService(store knowledge.Store, embedder knowledge.Embedder) KnowledgeService {
	return &adminKnowledgeService{store: store, embedder: embedder}
}
