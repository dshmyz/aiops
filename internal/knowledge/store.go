package knowledge

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"

	"github.com/google/uuid"
)

// MemoryStore is an thread-unsafe in-memory vector store for tests and small
// deployments. Cosine similarity is computed in memory.
type MemoryStore struct {
	docs   []EmbeddedDocument
	nextID int
}

func (m *MemoryStore) Add(_ context.Context, doc EmbeddedDocument) error {
	if doc.ID == "" {
		doc.ID = uuid.New().String()
	}
	m.docs = append(m.docs, doc)
	return nil
}

func (m *MemoryStore) List(_ context.Context) ([]Document, error) {
	out := make([]Document, len(m.docs))
	for i, d := range m.docs {
		out[i] = d.Document
	}
	return out, nil
}

func (m *MemoryStore) Retrieve(_ context.Context, query string, topK int) ([]Document, error) {
	if topK <= 0 {
		return nil, nil
	}
	// For in-memory store without an embedder, we fall back to a naive
	// substring match. Production code should embed the query.
	type scored struct {
		score float64
		idx   int
	}
	scores := make([]scored, 0, len(m.docs))
	for i, d := range m.docs {
		if contains(d.Content, query) {
			scores = append(scores, scored{score: 1.0, idx: i})
		} else {
			scores = append(scores, scored{score: 0.0, idx: i})
		}
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].score > scores[j].score })
	out := make([]Document, 0, topK)
	for i := 0; i < len(scores) && i < topK; i++ {
		out = append(out, m.docs[scores[i].idx].Document)
	}
	return out, nil
}

// RetrieveByVector ranks all stored documents by cosine similarity to the
// query embedding and returns the top-k most similar.
func (m *MemoryStore) RetrieveByVector(_ context.Context, queryEmbedding []float32, topK int) ([]Document, error) {
	if topK <= 0 || len(queryEmbedding) == 0 {
		return nil, nil
	}
	type scored struct {
		score float64
		idx   int
	}
	scores := make([]scored, 0, len(m.docs))
	for i, d := range m.docs {
		if len(d.Embedding) == 0 {
			continue
		}
		sim := CosineSimilarity(queryEmbedding, d.Embedding)
		if sim > 0 {
			scores = append(scores, scored{score: sim, idx: i})
		}
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].score > scores[j].score })
	out := make([]Document, 0, topK)
	for i := 0; i < len(scores) && i < topK; i++ {
		out = append(out, m.docs[scores[i].idx].Document)
	}
	return out, nil
}

// SQLStore is a MySQL-backed vector store. Embeddings are stored as byte
// blobs; retrieval fetches all rows and ranks them in memory. This is suitable
// for operational knowledge bases with a few thousand documents; for larger
// scale use a dedicated vector database.
type SQLStore struct {
	db *sql.DB
}

// NewSQLStore creates a MySQL-backed knowledge store.
func NewSQLStore(db *sql.DB) *SQLStore {
	return &SQLStore{db: db}
}

func (s *SQLStore) Add(ctx context.Context, doc EmbeddedDocument) error {
	if doc.ID == "" {
		doc.ID = uuid.New().String()
	}
	embedding := floatSliceToBytes(doc.Embedding)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO copilot_knowledge_documents (id, title, content, embedding, source, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		doc.ID, doc.Title, doc.Content, embedding, doc.Source, doc.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert knowledge document: %w", err)
	}
	return nil
}

func (s *SQLStore) List(ctx context.Context) ([]Document, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, content, source, created_at FROM copilot_knowledge_documents ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list knowledge documents: %w", err)
	}
	defer rows.Close()
	var docs []Document
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.ID, &d.Title, &d.Content, &d.Source, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan knowledge document: %w", err)
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

func (s *SQLStore) Retrieve(ctx context.Context, query string, topK int) ([]Document, error) {
	if topK <= 0 {
		return nil, nil
	}
	// Without an embedder configured, fall back to naive substring matching on
	// the content. This keeps the store usable without an external embedding
	// service.
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, title, content, source, created_at FROM copilot_knowledge_documents WHERE content LIKE ? ORDER BY created_at DESC LIMIT ?`,
		"%"+query+"%", topK)
	if err != nil {
		return nil, fmt.Errorf("retrieve knowledge documents: %w", err)
	}
	defer rows.Close()
	var docs []Document
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.ID, &d.Title, &d.Content, &d.Source, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan knowledge document: %w", err)
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

// RetrieveByVector fetches all documents with embeddings, ranks them by cosine
// similarity to the query embedding, and returns the top-k. Suitable for
// operational knowledge bases with a few thousand documents.
func (s *SQLStore) RetrieveByVector(ctx context.Context, queryEmbedding []float32, topK int) ([]Document, error) {
	if topK <= 0 || len(queryEmbedding) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, title, content, source, created_at, embedding FROM copilot_knowledge_documents WHERE embedding IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("retrieve knowledge documents by vector: %w", err)
	}
	defer rows.Close()

	type scored struct {
		doc   Document
		score float64
	}
	var candidates []scored
	for rows.Next() {
		var d Document
		var embBytes []byte
		if err := rows.Scan(&d.ID, &d.Title, &d.Content, &d.Source, &d.CreatedAt, &embBytes); err != nil {
			return nil, fmt.Errorf("scan knowledge document: %w", err)
		}
		emb := bytesToFloatSlice(embBytes)
		if len(emb) == 0 {
			continue
		}
		sim := CosineSimilarity(queryEmbedding, emb)
		if sim > 0 {
			candidates = append(candidates, scored{doc: d, score: sim})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate knowledge documents: %w", err)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	out := make([]Document, 0, topK)
	for i := 0; i < len(candidates) && i < topK; i++ {
		out = append(out, candidates[i].doc)
	}
	return out, nil
}

func floatSliceToBytes(v []float32) []byte {
	b := make([]byte, len(v)*4)
	// Simple byte copy of the float32 values.
	for i, f := range v {
		bits := math.Float32bits(f)
		b[i*4] = byte(bits)
		b[i*4+1] = byte(bits >> 8)
		b[i*4+2] = byte(bits >> 16)
		b[i*4+3] = byte(bits >> 24)
	}
	return b
}

func bytesToFloatSlice(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		bits := uint32(b[i*4]) | uint32(b[i*4+1])<<8 | uint32(b[i*4+2])<<16 | uint32(b[i*4+3])<<24
		v[i] = math.Float32frombits(bits)
	}
	return v
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

// CosineSimilarity returns the cosine of the angle between two non-zero
// vectors. The caller should ensure the vectors have the same dimension.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return -1
	}
	var dot, normA, normB float64
	for i := range a {
		x := float64(a[i])
		y := float64(b[i])
		dot += x * y
		normA += x * x
		normB += y * y
	}
	if normA == 0 || normB == 0 {
		return -1
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
