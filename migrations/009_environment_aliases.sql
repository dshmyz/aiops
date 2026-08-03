CREATE TABLE IF NOT EXISTS copilot_environment_aliases (
    id CHAR(36) NOT NULL,
    environment TEXT NOT NULL,
    alias TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY copilot_environment_aliases_env_alias_idx (environment, alias),
    KEY copilot_environment_aliases_alias_idx (alias)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
