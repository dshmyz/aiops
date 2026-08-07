-- E2 准入：Low-Risk Admission Controller 的每日上限持久化计数。
-- 每个主体(subject)每个自然日(day, YYYYMMDD UTC)的自动执行次数。
-- 由 autonomy.SQLDailyLimiter 读写；PK(subject, day) 保证 upsert 幂等。
CREATE TABLE IF NOT EXISTS autonomy_daily_limit (
    subject VARCHAR(255) NOT NULL,
    day VARCHAR(8) NOT NULL,
    count INT NOT NULL DEFAULT 0,
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (subject, day)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
