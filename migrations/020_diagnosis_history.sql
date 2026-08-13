-- 020_diagnosis_history.sql
-- 诊断历史知识库表：存储 agent 执行记录，支持经验检索。

CREATE TABLE IF NOT EXISTS diagnosis_history (
	id BIGINT PRIMARY KEY AUTO_INCREMENT,
	alert_title TEXT NOT NULL,
	domain VARCHAR(64) DEFAULT '',
	tools_called TEXT,
	findings TEXT,
	recommendations TEXT,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	KEY idx_diagnosis_history_created (created_at),
	KEY idx_diagnosis_history_domain (domain)
);
