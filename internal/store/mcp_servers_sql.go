package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var _ MCPServerStore = (*SQLMCPServerStore)(nil)

// SQLMCPServerStore 在 MySQL/SQLite 上持久化 MCP 服务器配置。
type SQLMCPServerStore struct{ db *sql.DB }

// NewSQLMCPServerStore 创建一个 SQL MCP 服务器 store。
func NewSQLMCPServerStore(db *sql.DB) *SQLMCPServerStore {
	return &SQLMCPServerStore{db: db}
}

func (s *SQLMCPServerStore) Create(ctx context.Context, server MCPServerRecord) (MCPServerRecord, error) {
	argsJSON, err := json.Marshal(server.Args)
	if err != nil {
		return MCPServerRecord{}, fmt.Errorf("marshal args: %w", err)
	}
	envJSON, err := json.Marshal(server.Env)
	if err != nil {
		return MCPServerRecord{}, fmt.Errorf("marshal env: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO copilot_mcp_servers
		(id, name, command, args, env, url, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		server.ID, server.Name, server.Command, string(argsJSON), string(envJSON),
		server.URL, server.Enabled, server.CreatedAt, server.UpdatedAt)
	if err != nil {
		if isDuplicateKey(err) {
			return MCPServerRecord{}, ErrConflict
		}
		return MCPServerRecord{}, fmt.Errorf("insert MCP server: %w", err)
	}
	return server, nil
}

func (s *SQLMCPServerStore) Get(ctx context.Context, id string) (MCPServerRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, command, args, env, url, enabled, created_at, updated_at
		FROM copilot_mcp_servers WHERE id = ?`, id)
	server, err := scanMCPServer(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MCPServerRecord{}, ErrNotFound
		}
		return MCPServerRecord{}, fmt.Errorf("get MCP server: %w", err)
	}
	return server, nil
}

func (s *SQLMCPServerStore) List(ctx context.Context) ([]MCPServerRecord, error) {
	return s.listWithFilter(ctx, "")
}

func (s *SQLMCPServerStore) ListEnabled(ctx context.Context) ([]MCPServerRecord, error) {
	return s.listWithFilter(ctx, " WHERE enabled = 1")
}

func (s *SQLMCPServerStore) listWithFilter(ctx context.Context, where string) ([]MCPServerRecord, error) {
	query := `SELECT id, name, command, args, env, url, enabled, created_at, updated_at FROM copilot_mcp_servers` + where + ` ORDER BY name ASC`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list MCP servers: %w", err)
	}
	defer rows.Close()
	var servers []MCPServerRecord
	for rows.Next() {
		server, err := scanMCPServer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan MCP server: %w", err)
		}
		servers = append(servers, server)
	}
	return servers, rows.Err()
}

func (s *SQLMCPServerStore) Update(ctx context.Context, server MCPServerRecord) (MCPServerRecord, error) {
	argsJSON, err := json.Marshal(server.Args)
	if err != nil {
		return MCPServerRecord{}, fmt.Errorf("marshal args: %w", err)
	}
	envJSON, err := json.Marshal(server.Env)
	if err != nil {
		return MCPServerRecord{}, fmt.Errorf("marshal env: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE copilot_mcp_servers
		SET name = ?, command = ?, args = ?, env = ?, url = ?, enabled = ?, updated_at = ?
		WHERE id = ?`,
		server.Name, server.Command, string(argsJSON), string(envJSON),
		server.URL, server.Enabled, server.UpdatedAt, server.ID)
	if err != nil {
		if isDuplicateKey(err) {
			return MCPServerRecord{}, ErrConflict
		}
		return MCPServerRecord{}, fmt.Errorf("update MCP server: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return MCPServerRecord{}, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return MCPServerRecord{}, ErrNotFound
	}
	return server, nil
}

func (s *SQLMCPServerStore) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM copilot_mcp_servers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete MCP server: %w", err)
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

type mcpServerScanner interface {
	Scan(dest ...any) error
}

func scanMCPServer(row mcpServerScanner) (MCPServerRecord, error) {
	var server MCPServerRecord
	var argsJSON, envJSON string
	err := row.Scan(
		&server.ID, &server.Name, &server.Command, &argsJSON, &envJSON,
		&server.URL, &server.Enabled, &server.CreatedAt, &server.UpdatedAt,
	)
	if err != nil {
		return MCPServerRecord{}, err
	}
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &server.Args); err != nil {
			return MCPServerRecord{}, fmt.Errorf("unmarshal args: %w", err)
		}
	}
	if envJSON != "" {
		if err := json.Unmarshal([]byte(envJSON), &server.Env); err != nil {
			return MCPServerRecord{}, fmt.Errorf("unmarshal env: %w", err)
		}
	}
	return server, nil
}
