package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// NewSQLRunbookStore returns a MySQL-backed RunbookStore.
func NewSQLRunbookStore(db *sql.DB) *SQLRunbookStore {
	return &SQLRunbookStore{db: db}
}

// SQLRunbookStore is a MySQL implementation of RunbookStore.
type SQLRunbookStore struct{ db *sql.DB }

var _ RunbookStore = (*SQLRunbookStore)(nil)

func (s *SQLRunbookStore) CreateRunbook(ctx context.Context, rb Runbook) (Runbook, error) {
	if rb.ID == "" {
		rb.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if rb.CreatedAt.IsZero() {
		rb.CreatedAt = now
	}
	rb.UpdatedAt = now

	pattern, err := json.Marshal(rb.IntentPattern)
	if err != nil {
		return Runbook{}, fmt.Errorf("marshal intent_pattern: %w", err)
	}
	sequence, err := json.Marshal(rb.ToolSequence)
	if err != nil {
		return Runbook{}, fmt.Errorf("marshal tool_sequence: %w", err)
	}
	var strategy []byte
	if rb.DefaultStrategy != nil {
		strategy, err = json.Marshal(rb.DefaultStrategy)
		if err != nil {
			return Runbook{}, fmt.Errorf("marshal default_strategy: %w", err)
		}
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO copilot_runbooks
		 (id, slug, name, intent_pattern, tool_sequence, default_strategy, risk_level, is_builtin, is_enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rb.ID, rb.Slug, rb.Name, string(pattern), string(sequence),
		nullableJSON(strategy), rb.RiskLevel, rb.IsBuiltin, rb.IsEnabled,
		rb.CreatedAt, rb.UpdatedAt,
	)
	if err != nil {
		if isDuplicateKey(err) {
			return Runbook{}, ErrConflict
		}
		return Runbook{}, fmt.Errorf("insert runbook: %w", err)
	}
	return rb, nil
}

func (s *SQLRunbookStore) GetRunbook(ctx context.Context, slug string) (Runbook, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, slug, name, intent_pattern, tool_sequence, default_strategy, risk_level, is_builtin, is_enabled, created_at, updated_at
		 FROM copilot_runbooks WHERE slug = ?`, slug)
	rb, err := scanRunbook(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Runbook{}, ErrNotFound
		}
		return Runbook{}, err
	}
	return rb, nil
}

func (s *SQLRunbookStore) ListRunbooks(ctx context.Context) ([]Runbook, error) {
	return s.listRunbooks(ctx, "")
}

func (s *SQLRunbookStore) ListEnabledRunbooks(ctx context.Context) ([]Runbook, error) {
	return s.listRunbooks(ctx, "WHERE is_enabled = 1")
}

func (s *SQLRunbookStore) listRunbooks(ctx context.Context, where string) ([]Runbook, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, slug, name, intent_pattern, tool_sequence, default_strategy, risk_level, is_builtin, is_enabled, created_at, updated_at
		 FROM copilot_runbooks `+where+` ORDER BY slug`)
	if err != nil {
		return nil, fmt.Errorf("list runbooks: %w", err)
	}
	defer rows.Close()

	var out []Runbook
	for rows.Next() {
		rb, err := scanRunbook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rb)
	}
	return out, rows.Err()
}

func (s *SQLRunbookStore) UpdateRunbook(ctx context.Context, rb Runbook) (Runbook, error) {
	rb.UpdatedAt = time.Now().UTC()

	pattern, err := json.Marshal(rb.IntentPattern)
	if err != nil {
		return Runbook{}, fmt.Errorf("marshal intent_pattern: %w", err)
	}
	sequence, err := json.Marshal(rb.ToolSequence)
	if err != nil {
		return Runbook{}, fmt.Errorf("marshal tool_sequence: %w", err)
	}
	var strategy []byte
	if rb.DefaultStrategy != nil {
		strategy, err = json.Marshal(rb.DefaultStrategy)
		if err != nil {
			return Runbook{}, fmt.Errorf("marshal default_strategy: %w", err)
		}
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE copilot_runbooks
		 SET slug = ?, name = ?, intent_pattern = ?, tool_sequence = ?, default_strategy = ?, risk_level = ?, is_builtin = ?, is_enabled = ?, updated_at = ?
		 WHERE id = ?`,
		rb.Slug, rb.Name, string(pattern), string(sequence), nullableJSON(strategy),
		rb.RiskLevel, rb.IsBuiltin, rb.IsEnabled, rb.UpdatedAt, rb.ID,
	)
	if err != nil {
		return Runbook{}, fmt.Errorf("update runbook: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return Runbook{}, ErrNotFound
	}
	return rb, nil
}

func (s *SQLRunbookStore) DeleteRunbook(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM copilot_runbooks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete runbook: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanRunbook(s scanner) (Runbook, error) {
	var rb Runbook
	var patternJSON, sequenceJSON, strategyJSON sql.NullString
	if err := s.Scan(
		&rb.ID, &rb.Slug, &rb.Name, &patternJSON, &sequenceJSON, &strategyJSON,
		&rb.RiskLevel, &rb.IsBuiltin, &rb.IsEnabled, &rb.CreatedAt, &rb.UpdatedAt,
	); err != nil {
		return Runbook{}, err
	}
	if patternJSON.Valid && patternJSON.String != "" {
		if err := json.Unmarshal([]byte(patternJSON.String), &rb.IntentPattern); err != nil {
			return Runbook{}, fmt.Errorf("unmarshal intent_pattern: %w", err)
		}
	}
	if sequenceJSON.Valid && sequenceJSON.String != "" {
		if err := json.Unmarshal([]byte(sequenceJSON.String), &rb.ToolSequence); err != nil {
			return Runbook{}, fmt.Errorf("unmarshal tool_sequence: %w", err)
		}
	}
	if strategyJSON.Valid && strategyJSON.String != "" {
		var strategy RunbookStrategy
		if err := json.Unmarshal([]byte(strategyJSON.String), &strategy); err != nil {
			return Runbook{}, fmt.Errorf("unmarshal default_strategy: %w", err)
		}
		rb.DefaultStrategy = &strategy
	}
	return rb, nil
}
