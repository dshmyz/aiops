package knowledge

import (
	"context"
	"fmt"
	"strings"
)

// Manager combines an embedder and a store to provide RAG capabilities to the
// assistant. When the planner asks for context, it retrieves the top-k most
// relevant knowledge documents and formats them as a prompt appendix.
type Manager struct {
	store    Store
	embedder Embedder
	topK     int
}

// NewManager creates a RAG manager. If embedder is nil, the manager falls back
// to substring-based retrieval (useful for testing or offline deployments).
func NewManager(store Store, embedder Embedder, topK int) *Manager {
	if topK <= 0 {
		topK = 3
	}
	return &Manager{store: store, embedder: embedder, topK: topK}
}

// Store returns the underlying knowledge store.
func (m *Manager) Store() Store {
	return m.store
}

// Embedder returns the configured embedder, or nil when no embedder is set.
// Callers (e.g. admin ingestion paths) use this to embed manually added
// documents so they participate in vector retrieval.
func (m *Manager) Embedder() Embedder {
	return m.embedder
}

// AugmentPrompt returns the base prompt with relevant knowledge documents
// appended as an "运维知识库" section. If no documents are found or the store
// is unavailable, the base prompt is returned unchanged.
func (m *Manager) AugmentPrompt(ctx context.Context, basePrompt, query string) string {
	if m == nil || m.store == nil {
		return basePrompt
	}
	docs, err := m.Retrieve(ctx, query, m.topK)
	if err != nil || len(docs) == 0 {
		return basePrompt
	}
	var sb strings.Builder
	sb.WriteString(basePrompt)
	sb.WriteString("\n\n## 检索到的运维知识\n")
	for i, doc := range docs {
		sb.WriteString(fmt.Sprintf("### 文档 %d", i+1))
		if doc.Title != "" {
			sb.WriteString(fmt.Sprintf("：%s", doc.Title))
		}
		sb.WriteString("\n")
		if doc.Source != "" {
			sb.WriteString(fmt.Sprintf("来源：%s\n", doc.Source))
		}
		sb.WriteString(doc.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}

// Retrieve fetches the top-k documents relevant to the query. When an
// embedder is configured and the store supports vector retrieval, it embeds the
// query and ranks documents by cosine similarity. Otherwise it falls back to
// substring matching.
func (m *Manager) Retrieve(ctx context.Context, query string, topK int) ([]Document, error) {
	if m == nil || m.store == nil {
		return nil, nil
	}
	// Vector similarity path: embed the query and use cosine ranking.
	if m.embedder != nil {
		if vs, ok := m.store.(VectorRetrieverStore); ok {
			queryVec, err := m.embedder.Embed(ctx, query)
			if err == nil && len(queryVec) > 0 {
				docs, vecErr := vs.RetrieveByVector(ctx, queryVec, topK)
				if vecErr == nil && len(docs) > 0 {
					return docs, nil
				}
			}
			// Fall through to substring if vector retrieval yields no results.
		}
	}
	return m.store.Retrieve(ctx, query, topK)
}
