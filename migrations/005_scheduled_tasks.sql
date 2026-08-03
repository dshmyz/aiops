CREATE TABLE IF NOT EXISTS copilot_scheduled_tasks (
    id CHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    subject VARCHAR(255) NOT NULL,
    capability_name VARCHAR(255) NOT NULL,
    input JSON NOT NULL,
    schedule_kind VARCHAR(16) NOT NULL,
    preset VARCHAR(16) NULL,
    cron_expr VARCHAR(64) NULL,
    timezone VARCHAR(64) NOT NULL DEFAULT 'Asia/Shanghai',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    last_run_at DATETIME(6) NULL,
    last_status VARCHAR(16) NULL,
    next_run_at DATETIME(6) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY copilot_scheduled_tasks_enabled_next_run_idx (enabled, next_run_at),
    KEY copilot_scheduled_tasks_subject_idx (subject)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS copilot_scheduled_task_runs (
    id CHAR(36) NOT NULL,
    task_id CHAR(36) NOT NULL,
    started_at DATETIME(6) NOT NULL,
    finished_at DATETIME(6) NOT NULL,
    status VARCHAR(16) NOT NULL,
    result_summary TEXT NULL,
    result_data JSON NULL,
    error TEXT NULL,
    audit_event_id CHAR(36) NULL,
    PRIMARY KEY (id),
    KEY copilot_scheduled_task_runs_task_started_idx (task_id, started_at),
    CONSTRAINT copilot_scheduled_task_runs_task_id_fk
        FOREIGN KEY (task_id) REFERENCES copilot_scheduled_tasks (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
