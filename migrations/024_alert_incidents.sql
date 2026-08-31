-- 024_alert_incidents.sql: 告警关联降噪的 incident 聚合。
-- 同一（domain, resource_type, resource_name）在时间窗内连续触发的告警归并为
-- 一个 incident：运营者处置"一次故障"而不是 N 条告警；同一 incident 只有
-- 首条告警触发自动研判，后续归并的告警不再逐条研判轰炸。
-- 成员告警经 copilot_alert_incident_members 关联，不侵入 copilot_alerts 表结构。

CREATE TABLE IF NOT EXISTS copilot_alert_incidents (
    id VARCHAR(64) NOT NULL PRIMARY KEY,
    status VARCHAR(16) NOT NULL DEFAULT 'firing',   -- firing | resolved
    domain VARCHAR(64) NOT NULL DEFAULT '',
    resource_type VARCHAR(64) NOT NULL DEFAULT '',
    resource_name VARCHAR(255) NOT NULL DEFAULT '',
    severity VARCHAR(16) NOT NULL DEFAULT 'info',   -- 成员中的最高级别
    title VARCHAR(512) NOT NULL DEFAULT '',         -- 首条告警标题
    alert_count INT NOT NULL DEFAULT 1,
    first_seen_at DATETIME(6) NOT NULL,
    last_seen_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    KEY idx_alert_incidents_open (status, domain, last_seen_at),
    KEY idx_alert_incidents_key (domain, resource_type, resource_name, last_seen_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS copilot_alert_incident_members (
    incident_id VARCHAR(64) NOT NULL,
    alert_id VARCHAR(64) NOT NULL,
    attached_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (incident_id, alert_id),
    KEY idx_alert_incident_members_alert (alert_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
