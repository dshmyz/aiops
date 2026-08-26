CREATE TABLE IF NOT EXISTS copilot_environment_aliases (
    id TEXT NOT NULL PRIMARY KEY,
    environment TEXT NOT NULL,
    alias TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS copilot_environment_aliases_env_alias_idx ON copilot_environment_aliases (environment, alias);
CREATE INDEX IF NOT EXISTS copilot_environment_aliases_alias_idx ON copilot_environment_aliases (alias);
