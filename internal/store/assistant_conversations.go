package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ConversationRole distinguishes user input from assistant output in a turn.
const (
	ConversationRoleUser          = "user"
	ConversationRoleAssistant     = "assistant"
	ConversationRoleSystemSummary = "system_summary"
)

// ResponseTypeRollingSummary marks the system_summary turn produced by the
// rolling summarization compactor.
const ResponseTypeRollingSummary = "rolling_summary"

// ErrInvalidTitle is returned by RenameConversation when the requested title
// is empty (after trimming).
var ErrInvalidTitle = errors.New("title must not be empty")

// Conversation captures a multi-turn assistant dialogue scoped to a subject.
// Conversations are isolated by Subject and soft-deleted via ArchivedAt.
type Conversation struct {
	ID                 string     `json:"id"`
	Subject            string     `json:"subject"`
	Title              string     `json:"title"`
	LastMessagePreview string     `json:"last_message_preview"`
	CreatedAt          time.Time  `json:"created_at"`
	LastActiveAt       time.Time  `json:"last_active_at"`
	ArchivedAt         *time.Time `json:"archived_at"`
}

// Turn is one message inside a conversation, persisted with the assistant
// response payload so the conversation can be replayed without re-running
// the underlying capability.
type Turn struct {
	ID              string         `json:"id"`
	ConversationID  string         `json:"conversation_id"`
	ParentTurnID    string         `json:"parent_turn_id,omitempty"`
	Role            string         `json:"role"`
	Content         string         `json:"content"`
	ResponseType    string         `json:"response_type,omitempty"`
	ResponsePayload map[string]any `json:"response_payload,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
}

// ConversationFilter scopes conversation list queries. Subject is required.
// Archived=false (default) excludes archived conversations; Archived=true
// returns only archived ones. Limit>0 enables keyset pagination using
// NextCursor returned by the previous page.
type ConversationFilter struct {
	Subject  string
	Archived bool
	Limit    int
	Cursor   string // conversation ID; the page returns rows strictly older than it
}

// ConversationPage is one page of conversations. NextCursor is empty when
// there are no more rows.
type ConversationPage struct {
	Conversations []Conversation
	NextCursor    string
}

// TurnPage is one page of turns ordered by CreatedAt DESC, ID DESC (newest
// first). NextCursor is empty on the last page. Callers should treat it as
// opaque and pass it back to ListTurns to fetch the next page.
type TurnPage struct {
	Turns      []Turn
	NextCursor string
}

// AssistantConversationStore is the persistence boundary for multi-turn
// assistant dialogues. Subject isolation is enforced by GetConversation and
// ArchiveConversation; ListTurns does NOT re-check the subject (the service
// layer must call GetConversation first to authorize the request).
type AssistantConversationStore interface {
	CreateConversation(ctx context.Context, subject, title, preview string, now time.Time) (Conversation, error)
	AppendTurn(ctx context.Context, turn Turn) (Turn, error)
	ListConversations(ctx context.Context, filter ConversationFilter) (ConversationPage, error)
	GetConversation(ctx context.Context, id, subject string) (Conversation, error)
	ListTurns(ctx context.Context, conversationID string, limit int, beforeTurnID string) (TurnPage, error)
	ArchiveConversation(ctx context.Context, id, subject string, now time.Time) error

	// DeleteConversation permanently removes a conversation and all its turns.
	// Subject isolation applies: missing or foreign conversations surface as
	// ErrNotFound. Already-archived conversations are deletable too.
	DeleteConversation(ctx context.Context, id, subject string) error

	// RenameConversation updates a conversation's display title. Subject
	// isolation applies as above. The title is trimmed by the caller; empty
	// titles are rejected with ErrInvalidTitle.
	RenameConversation(ctx context.Context, id, subject, title string) error

	// GetSummary returns the current rolling summary turn for the conversation.
	// Returns ErrNotFound when no summary exists yet. The summary turn's
	// ParentTurnID identifies the last raw turn already covered by the summary;
	// turns newer than that are unsummarized.
	GetSummary(ctx context.Context, conversationID string) (Turn, error)

	// ReplaceSummary atomically removes any existing summary turn for the
	// conversation and inserts a new one. summary is the compacted text;
	// coveredUpToTurnID is stored as ParentTurnID so the service can skip
	// re-compacting turns already covered. now is the created_at timestamp.
	// Returns the newly inserted summary turn.
	ReplaceSummary(ctx context.Context, conversationID, summary, coveredUpToTurnID string, now time.Time) (Turn, error)
}

// MemoryAssistantConversationStore supplies deterministic unit-test
// persistence. It is safe for concurrent use.
type MemoryAssistantConversationStore struct {
	mu            sync.Mutex
	conversations map[string]Conversation
	turns         map[string][]Turn
}

func NewMemoryAssistantConversationStore() *MemoryAssistantConversationStore {
	return &MemoryAssistantConversationStore{
		conversations: make(map[string]Conversation),
		turns:         make(map[string][]Turn),
	}
}

func (s *MemoryAssistantConversationStore) CreateConversation(_ context.Context, subject, title, preview string, now time.Time) (Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv := Conversation{
		ID:                 uuid.NewString(),
		Subject:            subject,
		Title:              title,
		LastMessagePreview: preview,
		CreatedAt:          now,
		LastActiveAt:       now,
	}
	s.conversations[conv.ID] = conv
	return conv, nil
}

func (s *MemoryAssistantConversationStore) AppendTurn(_ context.Context, turn Turn) (Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, ok := s.conversations[turn.ConversationID]
	if !ok {
		return Turn{}, ErrNotFound
	}
	if turn.ID == "" {
		turn.ID = uuid.NewString()
	}
	if turn.CreatedAt.IsZero() {
		turn.CreatedAt = time.Now().UTC()
	}
	turn.ResponsePayload = clonePayload(turn.ResponsePayload)
	s.turns[turn.ConversationID] = append(s.turns[turn.ConversationID], turn)
	if turn.CreatedAt.After(conv.LastActiveAt) {
		conv.LastActiveAt = turn.CreatedAt
		s.conversations[turn.ConversationID] = conv
	}
	return turn, nil
}

func (s *MemoryAssistantConversationStore) ListConversations(_ context.Context, filter ConversationFilter) (ConversationPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	matched := make([]Conversation, 0, len(s.conversations))
	for _, conv := range s.conversations {
		if conv.Subject != filter.Subject {
			continue
		}
		isArchived := conv.ArchivedAt != nil
		if isArchived != filter.Archived {
			continue
		}
		matched = append(matched, conv)
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].LastActiveAt.Equal(matched[j].LastActiveAt) {
			return matched[i].ID > matched[j].ID
		}
		return matched[i].LastActiveAt.After(matched[j].LastActiveAt)
	})
	if filter.Cursor != "" {
		kept := matched[:0]
		seen := false
		for _, conv := range matched {
			if !seen {
				if conv.ID == filter.Cursor {
					seen = true
				}
				continue
			}
			kept = append(kept, conv)
		}
		matched = kept
	}
	if filter.Limit > 0 && len(matched) > filter.Limit {
		page := matched[:filter.Limit]
		return ConversationPage{Conversations: page, NextCursor: page[len(page)-1].ID}, nil
	}
	return ConversationPage{Conversations: matched}, nil
}

func (s *MemoryAssistantConversationStore) GetConversation(_ context.Context, id, subject string) (Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, ok := s.conversations[id]
	if !ok || conv.Subject != subject {
		return Conversation{}, ErrNotFound
	}
	return conv, nil
}

func (s *MemoryAssistantConversationStore) ListTurns(_ context.Context, conversationID string, limit int, beforeTurnID string) (TurnPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turns := append([]Turn(nil), s.turns[conversationID]...)
	sort.Slice(turns, func(i, j int) bool {
		if turns[i].CreatedAt.Equal(turns[j].CreatedAt) {
			return turns[i].ID > turns[j].ID
		}
		return turns[i].CreatedAt.After(turns[j].CreatedAt)
	})
	if beforeTurnID != "" {
		kept := turns[:0]
		seen := false
		for _, turn := range turns {
			if !seen {
				if turn.ID == beforeTurnID {
					seen = true
				}
				continue
			}
			kept = append(kept, turn)
		}
		turns = kept
	}
	if limit > 0 && len(turns) > limit {
		page := turns[:limit]
		return TurnPage{Turns: page, NextCursor: page[len(page)-1].ID}, nil
	}
	return TurnPage{Turns: turns}, nil
}

func (s *MemoryAssistantConversationStore) ArchiveConversation(_ context.Context, id, subject string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, ok := s.conversations[id]
	if !ok || conv.Subject != subject {
		return ErrNotFound
	}
	conv.ArchivedAt = pointerTo(now)
	s.conversations[id] = conv
	return nil
}

func (s *MemoryAssistantConversationStore) DeleteConversation(_ context.Context, id, subject string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, ok := s.conversations[id]
	if !ok || conv.Subject != subject {
		return ErrNotFound
	}
	delete(s.conversations, id)
	delete(s.turns, id)
	return nil
}

func (s *MemoryAssistantConversationStore) RenameConversation(_ context.Context, id, subject, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, ok := s.conversations[id]
	if !ok || conv.Subject != subject {
		return ErrNotFound
	}
	if title == "" {
		return ErrInvalidTitle
	}
	conv.Title = title
	s.conversations[id] = conv
	return nil
}

func (s *MemoryAssistantConversationStore) GetSummary(_ context.Context, conversationID string) (Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turns := s.turns[conversationID]
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role == ConversationRoleSystemSummary {
			return cloneSummaryTurn(turns[i]), nil
		}
	}
	return Turn{}, ErrNotFound
}

func (s *MemoryAssistantConversationStore) ReplaceSummary(_ context.Context, conversationID, summary, coveredUpToTurnID string, now time.Time) (Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.conversations[conversationID]; !ok {
		return Turn{}, ErrNotFound
	}
	filtered := s.turns[conversationID][:0]
	for _, t := range s.turns[conversationID] {
		if t.Role != ConversationRoleSystemSummary {
			filtered = append(filtered, t)
		}
	}
	summaryTurn := Turn{
		ID:             uuid.NewString(),
		ConversationID: conversationID,
		ParentTurnID:   coveredUpToTurnID,
		Role:           ConversationRoleSystemSummary,
		Content:        summary,
		ResponseType:   ResponseTypeRollingSummary,
		CreatedAt:      now,
	}
	filtered = append(filtered, summaryTurn)
	s.turns[conversationID] = filtered
	return cloneSummaryTurn(summaryTurn), nil
}

func cloneSummaryTurn(t Turn) Turn {
	copy := t
	copy.ResponsePayload = clonePayload(t.ResponsePayload)
	return copy
}

// SQLAssistantConversationStore persists conversations and turns in SQL. It
// is safe for concurrent use across service instances.
type SQLAssistantConversationStore struct{ db *sql.DB }

func NewSQLAssistantConversationStore(db *sql.DB) *SQLAssistantConversationStore {
	return &SQLAssistantConversationStore{db: db}
}

func (s *SQLAssistantConversationStore) CreateConversation(ctx context.Context, subject, title, preview string, now time.Time) (Conversation, error) {
	conv := Conversation{
		ID:                 uuid.NewString(),
		Subject:            subject,
		Title:              title,
		LastMessagePreview: preview,
		CreatedAt:          now,
		LastActiveAt:       now,
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO copilot_assistant_conversations
		(id, subject, title, last_message_preview, created_at, last_active_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		conv.ID, conv.Subject, conv.Title, conv.LastMessagePreview, conv.CreatedAt, conv.LastActiveAt)
	if err != nil {
		return Conversation{}, fmt.Errorf("insert conversation: %w", err)
	}
	return conv, nil
}

func (s *SQLAssistantConversationStore) AppendTurn(ctx context.Context, turn Turn) (Turn, error) {
	if turn.ID == "" {
		turn.ID = uuid.NewString()
	}
	if turn.CreatedAt.IsZero() {
		turn.CreatedAt = time.Now().UTC()
	}
	payload, err := nullablePayload(turn.ResponsePayload)
	if err != nil {
		return Turn{}, fmt.Errorf("marshal turn payload: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Turn{}, err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO copilot_assistant_turns
		(id, conversation_id, parent_turn_id, role, content, response_type, response_payload, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		turn.ID, turn.ConversationID, nullableString(turn.ParentTurnID), turn.Role, turn.Content, nullableString(turn.ResponseType), payload, turn.CreatedAt)
	if err != nil {
		return Turn{}, fmt.Errorf("insert turn: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE copilot_assistant_conversations SET last_active_at = ? WHERE id = ? AND last_active_at < ?`, turn.CreatedAt, turn.ConversationID, turn.CreatedAt); err != nil {
		return Turn{}, fmt.Errorf("update last_active_at: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Turn{}, err
	}
	return turn, nil
}

func (s *SQLAssistantConversationStore) ListConversations(ctx context.Context, filter ConversationFilter) (ConversationPage, error) {
	query := `SELECT id, subject, title, last_message_preview, created_at, last_active_at, archived_at FROM copilot_assistant_conversations WHERE subject = ? AND archived_at IS ` + archivePredicate(filter.Archived)
	args := []any{filter.Subject}
	if filter.Cursor != "" {
		query += ` AND (last_active_at < ? OR (last_active_at = ? AND id < ?))`
		cursor, err := s.cursorValues(ctx, filter.Cursor)
		if err != nil {
			return ConversationPage{}, err
		}
		args = append(args, cursor.LastActiveAt, cursor.LastActiveAt, cursor.ID)
	}
	query += ` ORDER BY last_active_at DESC, id DESC`
	fetchLimit := filter.Limit
	if fetchLimit > 0 {
		fetchLimit++
		query += ` LIMIT ?`
		args = append(args, fetchLimit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return ConversationPage{}, fmt.Errorf("query conversations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	conversations := []Conversation{}
	for rows.Next() {
		conv, err := scanConversation(rows)
		if err != nil {
			return ConversationPage{}, fmt.Errorf("scan conversation: %w", err)
		}
		conversations = append(conversations, conv)
	}
	if err := rows.Err(); err != nil {
		return ConversationPage{}, fmt.Errorf("iterate conversations: %w", err)
	}
	page := ConversationPage{Conversations: conversations}
	if filter.Limit > 0 && len(conversations) > filter.Limit {
		page.Conversations = conversations[:filter.Limit]
		page.NextCursor = page.Conversations[len(page.Conversations)-1].ID
	}
	return page, nil
}

func (s *SQLAssistantConversationStore) GetConversation(ctx context.Context, id, subject string) (Conversation, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, subject, title, last_message_preview, created_at, last_active_at, archived_at FROM copilot_assistant_conversations WHERE id = ? AND subject = ?`, id, subject)
	conv, err := scanConversation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, ErrNotFound
	}
	return conv, err
}

func (s *SQLAssistantConversationStore) ListTurns(ctx context.Context, conversationID string, limit int, beforeTurnID string) (TurnPage, error) {
	query := `SELECT id, conversation_id, parent_turn_id, role, content, response_type, response_payload, created_at FROM copilot_assistant_turns WHERE conversation_id = ?`
	args := []any{conversationID}
	if beforeTurnID != "" {
		query += ` AND (created_at < ? OR (created_at = ? AND id < ?))`
		cursor, err := s.turnCursorValues(ctx, beforeTurnID)
		if err != nil {
			return TurnPage{}, err
		}
		args = append(args, cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
	}
	query += ` ORDER BY created_at DESC, id DESC`
	fetchLimit := limit
	if fetchLimit > 0 {
		fetchLimit++
		query += ` LIMIT ?`
		args = append(args, fetchLimit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return TurnPage{}, fmt.Errorf("query turns: %w", err)
	}
	defer func() { _ = rows.Close() }()
	turns := []Turn{}
	for rows.Next() {
		turn, err := scanTurn(rows)
		if err != nil {
			return TurnPage{}, fmt.Errorf("scan turn: %w", err)
		}
		turns = append(turns, turn)
	}
	if err := rows.Err(); err != nil {
		return TurnPage{}, fmt.Errorf("iterate turns: %w", err)
	}
	page := TurnPage{Turns: turns}
	if limit > 0 && len(turns) > limit {
		page.Turns = turns[:limit]
		page.NextCursor = page.Turns[len(page.Turns)-1].ID
	}
	return page, nil
}

func (s *SQLAssistantConversationStore) ArchiveConversation(ctx context.Context, id, subject string, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE copilot_assistant_conversations SET archived_at = ? WHERE id = ? AND subject = ? AND archived_at IS NULL`, now, id, subject)
	if err != nil {
		return fmt.Errorf("archive conversation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		// Could be missing conversation or foreign subject; both surface as NotFound.
		return ErrNotFound
	}
	return nil
}

func (s *SQLAssistantConversationStore) DeleteConversation(ctx context.Context, id, subject string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Subject check first so a foreign id surfaces as NotFound, not as a
	// cascade of silent zero-row deletes.
	result, err := tx.ExecContext(ctx, `DELETE FROM copilot_assistant_conversations WHERE id = ? AND subject = ?`, id, subject)
	if err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM copilot_assistant_turns WHERE conversation_id = ?`, id); err != nil {
		return fmt.Errorf("delete conversation turns: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *SQLAssistantConversationStore) RenameConversation(ctx context.Context, id, subject, title string) error {
	if title == "" {
		return ErrInvalidTitle
	}
	result, err := s.db.ExecContext(ctx, `UPDATE copilot_assistant_conversations SET title = ? WHERE id = ? AND subject = ?`, title, id, subject)
	if err != nil {
		return fmt.Errorf("rename conversation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLAssistantConversationStore) GetSummary(ctx context.Context, conversationID string) (Turn, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, conversation_id, parent_turn_id, role, content, response_type, response_payload, created_at FROM copilot_assistant_turns WHERE conversation_id = ? AND role = ? ORDER BY created_at DESC, id DESC LIMIT 1`, conversationID, ConversationRoleSystemSummary)
	turn, err := scanTurn(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Turn{}, ErrNotFound
	}
	return turn, err
}

func (s *SQLAssistantConversationStore) ReplaceSummary(ctx context.Context, conversationID, summary, coveredUpToTurnID string, now time.Time) (Turn, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Turn{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM copilot_assistant_turns WHERE conversation_id = ? AND role = ?`, conversationID, ConversationRoleSystemSummary); err != nil {
		return Turn{}, fmt.Errorf("delete old summary: %w", err)
	}
	summaryTurn := Turn{
		ID:             uuid.NewString(),
		ConversationID: conversationID,
		ParentTurnID:   coveredUpToTurnID,
		Role:           ConversationRoleSystemSummary,
		Content:        summary,
		ResponseType:   ResponseTypeRollingSummary,
		CreatedAt:      now,
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO copilot_assistant_turns (id, conversation_id, parent_turn_id, role, content, response_type, response_payload, created_at) VALUES (?, ?, ?, ?, ?, ?, NULL, ?)`, summaryTurn.ID, summaryTurn.ConversationID, nullableString(summaryTurn.ParentTurnID), summaryTurn.Role, summaryTurn.Content, summaryTurn.ResponseType, summaryTurn.CreatedAt); err != nil {
		return Turn{}, fmt.Errorf("insert summary: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Turn{}, err
	}
	return summaryTurn, nil
}

type cursorValues struct {
	ID           string
	LastActiveAt time.Time
	CreatedAt    time.Time
}

func (s *SQLAssistantConversationStore) cursorValues(ctx context.Context, id string) (cursorValues, error) {
	var c cursorValues
	err := s.db.QueryRowContext(ctx, `SELECT id, last_active_at FROM copilot_assistant_conversations WHERE id = ?`, id).Scan(&c.ID, &c.LastActiveAt)
	if errors.Is(err, sql.ErrNoRows) {
		return cursorValues{}, ErrNotFound
	}
	return c, err
}

func (s *SQLAssistantConversationStore) turnCursorValues(ctx context.Context, id string) (cursorValues, error) {
	var c cursorValues
	err := s.db.QueryRowContext(ctx, `SELECT id, created_at FROM copilot_assistant_turns WHERE id = ?`, id).Scan(&c.ID, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return cursorValues{}, ErrNotFound
	}
	return c, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanConversation(row scanner) (Conversation, error) {
	var conv Conversation
	var archivedAt sql.NullTime
	err := row.Scan(&conv.ID, &conv.Subject, &conv.Title, &conv.LastMessagePreview, &conv.CreatedAt, &conv.LastActiveAt, &archivedAt)
	if err != nil {
		return Conversation{}, err
	}
	if archivedAt.Valid {
		conv.ArchivedAt = pointerTo(archivedAt.Time)
	}
	return conv, nil
}

func scanTurn(row scanner) (Turn, error) {
	var turn Turn
	var parentTurnID, responseType sql.NullString
	var payload []byte
	err := row.Scan(&turn.ID, &turn.ConversationID, &parentTurnID, &turn.Role, &turn.Content, &responseType, &payload, &turn.CreatedAt)
	if err != nil {
		return Turn{}, err
	}
	turn.ParentTurnID = parentTurnID.String
	turn.ResponseType = responseType.String
	if len(payload) > 0 {
		turn.ResponsePayload = map[string]any{}
		if err := json.Unmarshal(payload, &turn.ResponsePayload); err != nil {
			return Turn{}, fmt.Errorf("unmarshal turn payload: %w", err)
		}
	}
	return turn, nil
}

func archivePredicate(archived bool) string {
	if archived {
		return "NOT NULL"
	}
	return "NULL"
}

func clonePayload(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	copy := make(map[string]any, len(payload))
	for key, value := range payload {
		copy[key] = value
	}
	return copy
}

func nullablePayload(payload map[string]any) (any, error) {
	if payload == nil {
		return nil, nil
	}
	bytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return bytes, nil
}
