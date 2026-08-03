-- 借鉴-5: Runbook / 命令模板复用。可复用的工具序列模板，命中 IntentPattern
-- 时套用 ToolSequence；低风险 Runbook 可跳过人工确认自动执行。
CREATE TABLE IF NOT EXISTS copilot_runbooks (
    id CHAR(36) NOT NULL,
    slug VARCHAR(128) NOT NULL,
    name VARCHAR(128) NOT NULL,
    intent_pattern JSON NOT NULL,
    tool_sequence JSON NOT NULL,
    default_strategy JSON NULL,
    risk_level VARCHAR(16) NOT NULL DEFAULT 'medium',
    is_builtin TINYINT(1) NOT NULL DEFAULT 0,
    is_enabled TINYINT(1) NOT NULL DEFAULT 1,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY copilot_runbooks_slug_idx (slug)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
