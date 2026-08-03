CREATE TABLE IF NOT EXISTS action_plans (
    id CHAR(36) NOT NULL,
    request_id VARCHAR(128) NOT NULL DEFAULT '',
    created_by VARCHAR(255) NOT NULL DEFAULT '',
    tool_name VARCHAR(255) NOT NULL,
    input_json JSON NOT NULL,
    input_hash CHAR(64) NOT NULL,
    risk_level VARCHAR(32) NOT NULL,
    status VARCHAR(32) NOT NULL,
    version INT UNSIGNED NOT NULL DEFAULT 1,
    confirmation_token_hash CHAR(64) NULL,
    confirmed_by VARCHAR(255) NULL,
    confirmed_at DATETIME(6) NULL,
    expires_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY action_plans_status_expires_at_idx (status, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS tool_executions (
    id CHAR(36) NOT NULL,
    action_plan_id CHAR(36) NOT NULL,
    idempotency_key VARCHAR(255) NOT NULL,
    status VARCHAR(32) NOT NULL,
    result_summary JSON NULL,
    error_summary VARCHAR(1024) NULL,
    started_at DATETIME(6) NULL,
    completed_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY tool_executions_idempotency_key_uq (idempotency_key),
    KEY tool_executions_action_plan_id_idx (action_plan_id),
    CONSTRAINT tool_executions_action_plan_id_fk
        FOREIGN KEY (action_plan_id) REFERENCES action_plans (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS copilot_audit_events (
    id CHAR(36) NOT NULL,
    action_plan_id CHAR(36) NULL,
    request_id VARCHAR(128) NOT NULL,
    actor_subject VARCHAR(255) NOT NULL,
    tool_name VARCHAR(255) NULL,
    action VARCHAR(128) NOT NULL,
    metadata JSON NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY copilot_audit_events_request_id_idx (request_id),
    KEY copilot_audit_events_action_plan_id_idx (action_plan_id),
    CONSTRAINT copilot_audit_events_action_plan_id_fk
        FOREIGN KEY (action_plan_id) REFERENCES action_plans (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
