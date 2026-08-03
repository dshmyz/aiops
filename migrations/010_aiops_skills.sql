CREATE TABLE IF NOT EXISTS copilot_aiops_skills (
    id CHAR(36) NOT NULL,
    slug VARCHAR(128) NOT NULL,
    name VARCHAR(128) NOT NULL,
    category VARCHAR(64) NOT NULL DEFAULT '',
    description VARCHAR(255) NOT NULL DEFAULT '',
    applicable_actions JSON NOT NULL,
    tool_dependencies JSON NOT NULL,
    content TEXT NOT NULL,
    output_contract TEXT NOT NULL DEFAULT '',
    risk_level VARCHAR(16) NOT NULL DEFAULT 'read_only',
    is_builtin TINYINT(1) NOT NULL DEFAULT 0,
    is_enabled TINYINT(1) NOT NULL DEFAULT 1,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY copilot_aiops_skills_slug_idx (slug),
    KEY copilot_aiops_skills_category_idx (category)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
