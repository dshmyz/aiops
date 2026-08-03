-- Capability marketplace schema
-- Stores published capabilities with versioning, ratings, and usage stats

CREATE TABLE IF NOT EXISTS capability_registry (
    id CHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,  -- e.g. "k8s.pod.restart"
    domain VARCHAR(64) NOT NULL,  -- e.g. "kubernetes"
    resource_type VARCHAR(64) NOT NULL,
    operation VARCHAR(32) NOT NULL,  -- read/write
    risk_level VARCHAR(32) NOT NULL,  -- low/medium/high

    -- Ownership and visibility
    owner_id VARCHAR(255) NOT NULL,  -- user or team ID
    visibility VARCHAR(32) NOT NULL DEFAULT 'private',  -- private/team/public
    organization_id VARCHAR(255) NULL,

    -- Metadata
    description TEXT NOT NULL,
    tags JSON NULL,  -- ["cache", "redis", "operations"]
    category VARCHAR(64) NULL,  -- Infrastructure, Database, Networking, etc.

    -- Stats
    download_count INT UNSIGNED NOT NULL DEFAULT 0,
    usage_count INT UNSIGNED NOT NULL DEFAULT 0,  -- execution count
    avg_rating DECIMAL(3,2) NULL,  -- 0.00 - 5.00
    rating_count INT UNSIGNED NOT NULL DEFAULT 0,

    -- Status
    status VARCHAR(32) NOT NULL DEFAULT 'draft',  -- draft/published/deprecated
    published_at DATETIME(6) NULL,
    deprecated_at DATETIME(6) NULL,
    deprecation_reason TEXT NULL,

    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (id),
    UNIQUE KEY capability_registry_name_uq (name),
    KEY capability_registry_domain_status_idx (domain, status),
    KEY capability_registry_owner_idx (owner_id),
    KEY capability_registry_organization_idx (organization_id),
    KEY capability_registry_avg_rating_idx (avg_rating DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS capability_versions (
    id CHAR(36) NOT NULL,
    capability_id CHAR(36) NOT NULL,
    version VARCHAR(32) NOT NULL,  -- semantic version: 1.0.0, 1.1.0-beta

    -- Full YAML content
    yaml_content TEXT NOT NULL,
    yaml_hash CHAR(64) NOT NULL,  -- SHA256 of yaml_content

    -- Parsed fields for quick access
    schema_version INT NOT NULL,
    backend_adapter VARCHAR(64) NOT NULL,
    input_schema JSON NOT NULL,
    output_schema JSON NULL,
    governance JSON NOT NULL,

    -- Change log
    changelog TEXT NULL,  -- what's new in this version
    breaking_changes TEXT NULL,  -- migration guide for breaking changes

    -- Status
    status VARCHAR(32) NOT NULL DEFAULT 'draft',  -- draft/published/deprecated
    published_at DATETIME(6) NULL,
    published_by VARCHAR(255) NULL,

    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),

    PRIMARY KEY (id),
    UNIQUE KEY capability_versions_capability_version_uq (capability_id, version),
    KEY capability_versions_yaml_hash_idx (yaml_hash),
    KEY capability_versions_status_idx (status),
    CONSTRAINT capability_versions_capability_id_fk
        FOREIGN KEY (capability_id) REFERENCES capability_registry (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS capability_ratings (
    id CHAR(36) NOT NULL,
    capability_id CHAR(36) NOT NULL,
    user_id VARCHAR(255) NOT NULL,

    -- Rating
    rating TINYINT UNSIGNED NOT NULL,  -- 1-5 stars
    review TEXT NULL,

    -- Context
    version_used VARCHAR(32) NULL,  -- which version they rated
    environment VARCHAR(64) NULL,  -- where they used it

    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    PRIMARY KEY (id),
    UNIQUE KEY capability_ratings_capability_user_uq (capability_id, user_id),
    KEY capability_ratings_rating_idx (rating),
    CONSTRAINT capability_ratings_capability_id_fk
        FOREIGN KEY (capability_id) REFERENCES capability_registry (id) ON DELETE CASCADE,
    CHECK (rating >= 1 AND rating <= 5)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS capability_downloads (
    id CHAR(36) NOT NULL,
    capability_id CHAR(36) NOT NULL,
    version_id CHAR(36) NOT NULL,

    -- Who and where
    user_id VARCHAR(255) NOT NULL,
    organization_id VARCHAR(255) NULL,
    environment VARCHAR(64) NULL,

    -- Context
    download_source VARCHAR(64) NOT NULL,  -- web-ui, cli, api
    ip_address VARCHAR(45) NULL,
    user_agent TEXT NULL,

    downloaded_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),

    PRIMARY KEY (id),
    KEY capability_downloads_capability_idx (capability_id),
    KEY capability_downloads_user_idx (user_id),
    KEY capability_downloads_downloaded_at_idx (downloaded_at),
    CONSTRAINT capability_downloads_capability_id_fk
        FOREIGN KEY (capability_id) REFERENCES capability_registry (id) ON DELETE CASCADE,
    CONSTRAINT capability_downloads_version_id_fk
        FOREIGN KEY (version_id) REFERENCES capability_versions (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS capability_dependencies (
    id CHAR(36) NOT NULL,
    capability_id CHAR(36) NOT NULL,
    depends_on_capability_id CHAR(36) NOT NULL,

    -- Dependency metadata
    dependency_type VARCHAR(32) NOT NULL,  -- required, optional, suggested
    version_constraint VARCHAR(128) NULL,  -- e.g. ">=1.0.0 <2.0.0"
    execution_order INT NULL,  -- if dependencies must run in sequence

    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),

    PRIMARY KEY (id),
    UNIQUE KEY capability_dependencies_pair_uq (capability_id, depends_on_capability_id),
    KEY capability_dependencies_depends_on_idx (depends_on_capability_id),
    CONSTRAINT capability_dependencies_capability_id_fk
        FOREIGN KEY (capability_id) REFERENCES capability_registry (id) ON DELETE CASCADE,
    CONSTRAINT capability_dependencies_depends_on_fk
        FOREIGN KEY (depends_on_capability_id) REFERENCES capability_registry (id) ON DELETE CASCADE,
    CHECK (capability_id != depends_on_capability_id)  -- prevent self-dependency
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS capability_usage_stats (
    id CHAR(36) NOT NULL,
    capability_id CHAR(36) NOT NULL,
    version_id CHAR(36) NULL,

    -- Execution context
    user_id VARCHAR(255) NOT NULL,
    organization_id VARCHAR(255) NULL,
    environment VARCHAR(64) NOT NULL,

    -- Outcome
    status VARCHAR(32) NOT NULL,  -- success, failed, cancelled
    execution_time_ms INT UNSIGNED NULL,
    error_category VARCHAR(64) NULL,

    -- Link to execution record
    action_plan_id CHAR(36) NULL,

    executed_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),

    PRIMARY KEY (id),
    KEY capability_usage_stats_capability_idx (capability_id),
    KEY capability_usage_stats_executed_at_idx (executed_at),
    KEY capability_usage_stats_status_idx (status),
    CONSTRAINT capability_usage_stats_capability_id_fk
        FOREIGN KEY (capability_id) REFERENCES capability_registry (id) ON DELETE CASCADE,
    CONSTRAINT capability_usage_stats_version_id_fk
        FOREIGN KEY (version_id) REFERENCES capability_versions (id) ON DELETE SET NULL,
    CONSTRAINT capability_usage_stats_action_plan_id_fk
        FOREIGN KEY (action_plan_id) REFERENCES action_plans (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
