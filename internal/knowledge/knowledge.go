// Package knowledge provides a lightweight retrieval-augmented generation (RAG)
// layer for the assistant. Documents are stored with pre-computed embedding
// vectors; at query time the user's message is embedded and the most similar
// documents are injected into the planner prompt as retrieved context.
package knowledge

import (
	"context"
	"time"
)

// Document is one piece of operational knowledge (runbook, SOP, historical
// incident summary, etc.).
type Document struct {
	ID        string    `json:"id"`
	Title     string    `json:"title,omitempty"`
	Content   string    `json:"content"`
	Source    string    `json:"source,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// EmbeddedDocument extends Document with its vector embedding. Embeddings are
// stored as []float32 to keep memory and serialization compact.
type EmbeddedDocument struct {
	Document
	Embedding []float32 `json:"embedding"`
}

// Retriever finds the most relevant documents for a given query.
type Retriever interface {
	// Retrieve returns the top-k documents most similar to the query text.
	Retrieve(ctx context.Context, query string, topK int) ([]Document, error)
}

// Store persists and searches embedded documents.
type Store interface {
	Retriever
	// Add indexes a document and persists it.
	Add(ctx context.Context, doc EmbeddedDocument) error
	// List returns all stored documents (without embeddings).
	List(ctx context.Context) ([]Document, error)
}

// VectorRetrieverStore is an optional interface that stores can implement to
// support embedding-based vector similarity retrieval. When a Store also
// implements this interface, the Manager will use cosine similarity ranking
// instead of naive substring matching.
type VectorRetrieverStore interface {
	Store
	// RetrieveByVector returns the top-k documents whose stored embeddings are
	// most similar (cosine) to the provided query embedding vector.
	RetrieveByVector(ctx context.Context, queryEmbedding []float32, topK int) ([]Document, error)
}

// Remover is an optional interface that stores can implement to delete a
// document by ID. Used by indexers that need to re-index (upsert) a document
// with a stable ID (e.g. ability semantic indexing) without duplicating rows.
type Remover interface {
	Remove(ctx context.Context, id string) error
}
