package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Feedback records a user's rating and optional correction for one assistant
// turn. It closes the loop between assistant output and iterative prompt/model
// improvement.
type Feedback struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	TurnID         string    `json:"turn_id"`
	Subject        string    `json:"subject"`
	Rating         int       `json:"rating"`
	Correction     string    `json:"correction,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// FeedbackFilter scopes feedback list queries.
type FeedbackFilter struct {
	Subject        string
	ConversationID string
	Limit          int
	Offset         int
}

// FeedbackPage is one page of feedback entries.
type FeedbackPage struct {
	Items  []Feedback `json:"items"`
	Total  int        `json:"total"`
	Limit  int        `json:"limit"`
	Offset int        `json:"offset"`
}

// MarshalJSON ensures Items is serialized as [] rather than null when empty.
func (p FeedbackPage) MarshalJSON() ([]byte, error) {
	if p.Items == nil {
		p.Items = []Feedback{}
	}
	type alias FeedbackPage
	return json.Marshal(alias(p))
}

// FeedbackStore persists user feedback on assistant turns.
type FeedbackStore interface {
	// CreateFeedback stores a new feedback entry.
	CreateFeedback(ctx context.Context, feedback Feedback) (Feedback, error)
	// ListFeedback returns feedback entries matching the filter, ordered by
	// creation time descending (newest first).
	ListFeedback(ctx context.Context, filter FeedbackFilter) (FeedbackPage, error)
}

// NewSQLFeedbackStore creates a MySQL-backed feedback store.
func NewSQLFeedbackStore(db *sql.DB) *SQLFeedbackStore {
	return &SQLFeedbackStore{db: db}
}

// SQLFeedbackStore is a MySQL implementation of FeedbackStore.
type SQLFeedbackStore struct{ db *sql.DB }

func (s *SQLFeedbackStore) CreateFeedback(ctx context.Context, feedback Feedback) (Feedback, error) {
	if feedback.ID == "" {
		feedback.ID = uuid.New().String()
	}
	if feedback.CreatedAt.IsZero() {
		feedback.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO copilot_assistant_feedback (id, conversation_id, turn_id, subject, rating, correction, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		feedback.ID, feedback.ConversationID, feedback.TurnID, feedback.Subject, feedback.Rating, feedback.Correction, feedback.CreatedAt,
	)
	if err != nil {
		return Feedback{}, fmt.Errorf("insert feedback: %w", err)
	}
	return feedback, nil
}

func (s *SQLFeedbackStore) ListFeedback(ctx context.Context, filter FeedbackFilter) (FeedbackPage, error) {
	var page FeedbackPage
	page.Limit = filter.Limit
	page.Offset = filter.Offset

	where, args := feedbackWhere(filter)
	countSQL := "SELECT COUNT(*) FROM copilot_assistant_feedback" + where
	if err := s.db.QueryRowContext(ctx, countSQL, args...).Scan(&page.Total); err != nil {
		return page, fmt.Errorf("count feedback: %w", err)
	}

	limit, offset := normalizePagination(filter.Limit, filter.Offset)
	query := "SELECT id, conversation_id, turn_id, subject, rating, correction, created_at FROM copilot_assistant_feedback" + where + " ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?"
	rows, err := s.db.QueryContext(ctx, query, append(args, limit, offset)...)
	if err != nil {
		return page, fmt.Errorf("list feedback: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var f Feedback
		if err := rows.Scan(&f.ID, &f.ConversationID, &f.TurnID, &f.Subject, &f.Rating, &f.Correction, &f.CreatedAt); err != nil {
			return page, fmt.Errorf("scan feedback: %w", err)
		}
		page.Items = append(page.Items, f)
	}
	return page, rows.Err()
}

func feedbackWhere(filter FeedbackFilter) (string, []any) {
	parts := []string{}
	args := []any{}
	if filter.Subject != "" {
		parts = append(parts, "subject = ?")
		args = append(args, filter.Subject)
	}
	if filter.ConversationID != "" {
		parts = append(parts, "conversation_id = ?")
		args = append(args, filter.ConversationID)
	}
	if len(parts) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

func normalizePagination(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// NewMemoryFeedbackStore returns an in-memory FeedbackStore for tests.
func NewMemoryFeedbackStore() *MemoryFeedbackStore {
	return &MemoryFeedbackStore{}
}

// MemoryFeedbackStore is a thread-unsafe in-memory implementation of
// FeedbackStore intended for unit tests.
type MemoryFeedbackStore struct {
	feedback []Feedback
}

func (m *MemoryFeedbackStore) CreateFeedback(_ context.Context, feedback Feedback) (Feedback, error) {
	if feedback.ID == "" {
		feedback.ID = uuid.New().String()
	}
	if feedback.CreatedAt.IsZero() {
		feedback.CreatedAt = time.Now().UTC()
	}
	m.feedback = append(m.feedback, feedback)
	return feedback, nil
}

func (m *MemoryFeedbackStore) ListFeedback(_ context.Context, filter FeedbackFilter) (FeedbackPage, error) {
	page := FeedbackPage{Limit: filter.Limit, Offset: filter.Offset}
	limit, offset := normalizePagination(filter.Limit, filter.Offset)
	for _, f := range m.feedback {
		if filter.Subject != "" && f.Subject != filter.Subject {
			continue
		}
		if filter.ConversationID != "" && f.ConversationID != filter.ConversationID {
			continue
		}
		page.Items = append(page.Items, f)
	}
	page.Total = len(page.Items)
	if offset > page.Total {
		return FeedbackPage{Limit: limit, Offset: offset, Total: page.Total}, nil
	}
	end := offset + limit
	if end > page.Total {
		end = page.Total
	}
	page.Items = page.Items[offset:end]
	return page, nil
}

var (
	_ FeedbackStore = (*MemoryFeedbackStore)(nil)
	_ FeedbackStore = (*SQLFeedbackStore)(nil)
)
