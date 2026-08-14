-- 巡检报告：多节点幂等认领。
-- 同一 (period, window_start) 只允许一份报告：多实例并发生成日报时，
-- 先到先写，其余实例撞唯一键后返回已存在的那份（见 CreateReport 幂等逻辑）。
-- 该表此前仅存在于 SQLite 内联迁移；此处补齐 MySQL 侧，使 MySQL 部署的日报可用。
CREATE TABLE IF NOT EXISTS copilot_inspection_reports (
    id VARCHAR(36) NOT NULL,
    period VARCHAR(16) NOT NULL,
    window_start DATETIME(6) NOT NULL,
    window_end DATETIME(6) NOT NULL,
    generated_at DATETIME(6) NOT NULL,
    total_tasks INTEGER NOT NULL DEFAULT 0,
    succeeded_tasks INTEGER NOT NULL DEFAULT 0,
    failed_tasks INTEGER NOT NULL DEFAULT 0,
    task_summaries JSON NULL,
    html_content MEDIUMTEXT NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY copilot_inspection_reports_window_start_idx (period, window_start),
    KEY copilot_inspection_reports_generated_at_idx (generated_at DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
