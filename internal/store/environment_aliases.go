package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// EnvironmentAlias maps a human-friendly alias (e.g. "生产", "ecommerce-prod")
// to a canonical environment identifier (e.g. "prod"). When a user's JWT
// carries allowed_environments = ["prod"], the alias resolver expands it to
// include every alias so that policy checks accept any of the names the user
// might type.
type EnvironmentAlias struct {
	ID          string `json:"id"`
	Environment string `json:"environment"`
	Alias       string `json:"alias"`
	DisplayName string `json:"display_name"`
}

// EnvironmentAliasStore persists environment aliases.
type EnvironmentAliasStore interface {
	// ListAliases returns every alias for the given canonical environments.
	// When envs is empty, all aliases are returned.
	ListAliases(ctx context.Context, envs []string) ([]EnvironmentAlias, error)
	// CreateAlias persists a new alias. ID is generated when empty.
	CreateAlias(ctx context.Context, alias EnvironmentAlias) (EnvironmentAlias, error)
	// DeleteAlias removes an alias by ID.
	DeleteAlias(ctx context.Context, id string) error
}

// SQLEnvironmentAliasStore implements EnvironmentAliasStore on MySQL/SQLite.
type SQLEnvironmentAliasStore struct{ db *sql.DB }

// NewSQLEnvironmentAliasStore returns a new alias store backed by db.
func NewSQLEnvironmentAliasStore(db *sql.DB) *SQLEnvironmentAliasStore {
	return &SQLEnvironmentAliasStore{db: db}
}

func (s *SQLEnvironmentAliasStore) ListAliases(ctx context.Context, envs []string) ([]EnvironmentAlias, error) {
	query := `SELECT id, environment, alias, display_name FROM copilot_environment_aliases`
	var args []any
	if len(envs) > 0 {
		placeholders := make([]string, len(envs))
		for i := range envs {
			placeholders[i] = "?"
			args = append(args, envs[i])
		}
		query += ` WHERE environment IN (` + joinPlaceholders(placeholders) + `)`
	}
	query += ` ORDER BY environment, alias`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list environment aliases: %w", err)
	}
	defer rows.Close()
	var aliases []EnvironmentAlias
	for rows.Next() {
		var a EnvironmentAlias
		if err := rows.Scan(&a.ID, &a.Environment, &a.Alias, &a.DisplayName); err != nil {
			return nil, fmt.Errorf("scan environment alias: %w", err)
		}
		aliases = append(aliases, a)
	}
	return aliases, rows.Err()
}

func (s *SQLEnvironmentAliasStore) CreateAlias(ctx context.Context, alias EnvironmentAlias) (EnvironmentAlias, error) {
	if alias.ID == "" {
		alias.ID = uuid.NewString()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO copilot_environment_aliases (id, environment, alias, display_name) VALUES (?, ?, ?, ?)`,
		alias.ID, alias.Environment, alias.Alias, alias.DisplayName)
	if err != nil {
		return EnvironmentAlias{}, fmt.Errorf("insert environment alias: %w", err)
	}
	return alias, nil
}

func (s *SQLEnvironmentAliasStore) DeleteAlias(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM copilot_environment_aliases WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete environment alias: %w", err)
	}
	return nil
}

func joinPlaceholders(parts []string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += ", "
		}
		result += p
	}
	return result
}
