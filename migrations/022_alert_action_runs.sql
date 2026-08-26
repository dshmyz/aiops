-- 022_alert_action_runs.sql: 告警→动作编排的执行历史。
-- 每次 ChainDiagnoser 命中并执行一条规则，记录一条 run；逐步执行结果以 JSON 存 steps，
-- 供管理后台展示触发次数、成功率、最近一次执行快照。

CREATE TABLE IF NOT EXISTS alert_action_runs (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    rule_name VARCHAR(255) NOT NULL,
    alert_id VARCHAR(64) NOT NULL DEFAULT '',
    alert_title VARCHAR(512) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'success', -- success | failure
    steps JSON NOT NULL,                          -- []StepResult: {step, tool, error, degraded}
    summary TEXT NOT NULL DEFAULT '',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    KEY idx_alert_action_runs_rule (rule_name, created_at),
    KEY idx_alert_action_runs_alert (alert_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
