-- 018_capabilities.sql: 能力管理运行时事实源（CapabilityStore DB 实现）。
-- 存储 discovered/published 两个 source 的能力，替代 COPILOT_CAPABILITIES_DIR
-- 作为多节点共享的写权威。JSON 字段存 complex 结构（input_schema/backend/ai 等）。

CREATE TABLE IF NOT EXISTS capabilities (
    name VARCHAR(255) NOT NULL,
    source VARCHAR(32) NOT NULL DEFAULT 'discovered',   -- discovered / published
    status VARCHAR(32) NOT NULL DEFAULT 'needs_review',  -- needs_review / published
    domain VARCHAR(64) NOT NULL DEFAULT '',
    resource_type VARCHAR(64) NOT NULL DEFAULT '',
    operation VARCHAR(32) NOT NULL DEFAULT '',            -- read / write
    risk VARCHAR(32) NOT NULL DEFAULT 'low',              -- low / medium / high
    schema_version INT NOT NULL DEFAULT 1,
    backend JSON NULL,          -- BackendSpec
    input_schema JSON NULL,     -- map[string]InputField
    output JSON NULL,           -- OutputSpec
    governance JSON NULL,       -- GovernanceSpec
    auth JSON NULL,             -- AuthSpec
    ai JSON NULL,               -- AISpec
    verify JSON NULL,           -- *VerifySpec (nullable)
    depends_on JSON NULL,       -- []DependencySpec (nullable)
    path TEXT NULL,             -- 文件来源路径（可选，DB 自产的为 NULL）
    modified_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (name, source),
    INDEX idx_capabilities_status (status),
    INDEX idx_capabilities_domain (domain)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
