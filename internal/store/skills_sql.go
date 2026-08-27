package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// NewSQLSkillStore returns a MySQL-backed SkillStore.
func NewSQLSkillStore(db *sql.DB) *SQLSkillStore {
	return &SQLSkillStore{db: db}
}

// SQLSkillStore is a MySQL implementation of SkillStore.
type SQLSkillStore struct{ db *sql.DB }

var _ SkillStore = (*SQLSkillStore)(nil)

func (s *SQLSkillStore) CreateSkill(ctx context.Context, skill Skill) (Skill, error) {
	if skill.ID == "" {
		skill.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if skill.CreatedAt.IsZero() {
		skill.CreatedAt = now
	}
	skill.UpdatedAt = now

	actions, err := json.Marshal(skill.ApplicableActions)
	if err != nil {
		return Skill{}, fmt.Errorf("marshal applicable_actions: %w", err)
	}
	tools, err := json.Marshal(skill.ToolDependencies)
	if err != nil {
		return Skill{}, fmt.Errorf("marshal tool_dependencies: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO copilot_aiops_skills
		 (id, slug, name, category, description, applicable_actions, tool_dependencies, content, output_contract, risk_level, is_builtin, is_enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		skill.ID, skill.Slug, skill.Name, skill.Category, skill.Description,
		string(actions), string(tools), skill.Content, skill.OutputContract,
		skill.RiskLevel, skill.IsBuiltin, skill.IsEnabled, skill.CreatedAt, skill.UpdatedAt,
	)
	if err != nil {
		return Skill{}, fmt.Errorf("insert skill: %w", err)
	}
	return skill, nil
}

func (s *SQLSkillStore) GetSkill(ctx context.Context, slug string) (Skill, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, slug, name, category, description, applicable_actions, tool_dependencies, content, output_contract, risk_level, is_builtin, is_enabled, created_at, updated_at
		 FROM copilot_aiops_skills WHERE slug = ?`, slug)
	return scanSkill(row)
}

func (s *SQLSkillStore) GetSkillByID(ctx context.Context, id string) (Skill, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, slug, name, category, description, applicable_actions, tool_dependencies, content, output_contract, risk_level, is_builtin, is_enabled, created_at, updated_at
		 FROM copilot_aiops_skills WHERE id = ?`, id)
	return scanSkill(row)
}

func (s *SQLSkillStore) ListSkills(ctx context.Context) ([]Skill, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, slug, name, category, description, applicable_actions, tool_dependencies, content, output_contract, risk_level, is_builtin, is_enabled, created_at, updated_at
		 FROM copilot_aiops_skills ORDER BY slug`)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	defer rows.Close()

	var out []Skill
	for rows.Next() {
		skill, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, skill)
	}
	return out, rows.Err()
}

func (s *SQLSkillStore) ListSkillsByAction(ctx context.Context, actionCode string) ([]Skill, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, slug, name, category, description, applicable_actions, tool_dependencies, content, output_contract, risk_level, is_builtin, is_enabled, created_at, updated_at
		 FROM copilot_aiops_skills WHERE is_enabled = 1 ORDER BY slug`)
	if err != nil {
		return nil, fmt.Errorf("list skills by action: %w", err)
	}
	defer rows.Close()

	var out []Skill
	for rows.Next() {
		skill, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		for _, a := range skill.ApplicableActions {
			if a == actionCode {
				out = append(out, skill)
				break
			}
		}
	}
	return out, rows.Err()
}

func (s *SQLSkillStore) UpdateSkill(ctx context.Context, skill Skill) (Skill, error) {
	skill.UpdatedAt = time.Now().UTC()

	actions, err := json.Marshal(skill.ApplicableActions)
	if err != nil {
		return Skill{}, fmt.Errorf("marshal applicable_actions: %w", err)
	}
	tools, err := json.Marshal(skill.ToolDependencies)
	if err != nil {
		return Skill{}, fmt.Errorf("marshal tool_dependencies: %w", err)
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE copilot_aiops_skills
		 SET slug = ?, name = ?, category = ?, description = ?, applicable_actions = ?, tool_dependencies = ?, content = ?, output_contract = ?, risk_level = ?, is_builtin = ?, is_enabled = ?, updated_at = ?
		 WHERE id = ?`,
		skill.Slug, skill.Name, skill.Category, skill.Description,
		string(actions), string(tools), skill.Content, skill.OutputContract,
		skill.RiskLevel, skill.IsBuiltin, skill.IsEnabled, skill.UpdatedAt, skill.ID,
	)
	if err != nil {
		return Skill{}, fmt.Errorf("update skill: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return Skill{}, ErrNotFound
	}
	return skill, nil
}

func (s *SQLSkillStore) DeleteSkill(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM copilot_aiops_skills WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete skill: %w", err)
	}
	return nil
}

// scanner is satisfied by both *sql.Row and *sql.Rows (defined in assistant_conversations.go).

func scanSkill(s scanner) (Skill, error) {
	var skill Skill
	var actionsJSON, toolsJSON string
	if err := s.Scan(
		&skill.ID, &skill.Slug, &skill.Name, &skill.Category, &skill.Description,
		&actionsJSON, &toolsJSON, &skill.Content, &skill.OutputContract,
		&skill.RiskLevel, &skill.IsBuiltin, &skill.IsEnabled, &skill.CreatedAt, &skill.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return Skill{}, ErrNotFound
		}
		return Skill{}, fmt.Errorf("scan skill: %w", err)
	}
	if err := json.Unmarshal([]byte(actionsJSON), &skill.ApplicableActions); err != nil {
		return Skill{}, fmt.Errorf("unmarshal applicable_actions: %w", err)
	}
	if err := json.Unmarshal([]byte(toolsJSON), &skill.ToolDependencies); err != nil {
		return Skill{}, fmt.Errorf("unmarshal tool_dependencies: %w", err)
	}
	return skill, nil
}
