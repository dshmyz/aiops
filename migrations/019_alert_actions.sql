-- 019_alert_actions.sql: 告警→动作编排规则（可配置的告警响应工作流）。
-- 存储告警匹配条件 + 工具执行序列，供 ChainDiagnoser 在告警到达时匹配执行。

CREATE TABLE IF NOT EXISTS alert_action_rules (
    name VARCHAR(255) NOT NULL PRIMARY KEY,
    alert_match JSON NOT NULL,          -- AlertMatch: {alertname, severity, domain}
    tool_sequence JSON NOT NULL,        -- []AlertActionStep: [{tool, input}]
    execute_last_step TINYINT(1) NOT NULL DEFAULT 0,
    description TEXT NOT NULL DEFAULT '',
    enabled TINYINT(1) NOT NULL DEFAULT 1,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
