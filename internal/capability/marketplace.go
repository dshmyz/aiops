package capability

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// MarketplaceService manages capability registry, versions, and marketplace features
type MarketplaceService struct {
	db *sql.DB
}

func NewMarketplaceService(db *sql.DB) *MarketplaceService {
	return &MarketplaceService{db: db}
}

// CapabilityRegistry represents a capability in the marketplace
type CapabilityRegistry struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Domain         string          `json:"domain"`
	ResourceType   string          `json:"resource_type"`
	Operation      string          `json:"operation"`
	RiskLevel      string          `json:"risk_level"`
	OwnerID        string          `json:"owner_id"`
	Visibility     string          `json:"visibility"`
	OrganizationID *string         `json:"organization_id,omitempty"`
	Description    string          `json:"description"`
	Tags           json.RawMessage `json:"tags,omitempty"`
	Category       *string         `json:"category,omitempty"`
	DownloadCount  int             `json:"download_count"`
	UsageCount     int             `json:"usage_count"`
	AvgRating      *float64        `json:"avg_rating,omitempty"`
	RatingCount    int             `json:"rating_count"`
	Status         string          `json:"status"`
	PublishedAt    *time.Time      `json:"published_at,omitempty"`
	DeprecatedAt   *time.Time      `json:"deprecated_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// CapabilityVersion represents a versioned capability
type CapabilityVersion struct {
	ID              string          `json:"id"`
	CapabilityID    string          `json:"capability_id"`
	Version         string          `json:"version"`
	YAMLContent     string          `json:"yaml_content"`
	YAMLHash        string          `json:"yaml_hash"`
	SchemaVersion   int             `json:"schema_version"`
	BackendAdapter  string          `json:"backend_adapter"`
	InputSchema     json.RawMessage `json:"input_schema"`
	OutputSchema    json.RawMessage `json:"output_schema,omitempty"`
	Governance      json.RawMessage `json:"governance"`
	Changelog       *string         `json:"changelog,omitempty"`
	BreakingChanges *string         `json:"breaking_changes,omitempty"`
	Status          string          `json:"status"`
	PublishedAt     *time.Time      `json:"published_at,omitempty"`
	PublishedBy     *string         `json:"published_by,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
}

// CapabilityRating represents a user rating
type CapabilityRating struct {
	ID           string     `json:"id"`
	CapabilityID string     `json:"capability_id"`
	UserID       string     `json:"user_id"`
	Rating       int        `json:"rating"`
	Review       *string    `json:"review,omitempty"`
	VersionUsed  *string    `json:"version_used,omitempty"`
	Environment  *string    `json:"environment,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// PublishCapabilityRequest contains the data needed to publish a new capability
type PublishCapabilityRequest struct {
	YAMLContent    string   `json:"yaml_content"`
	Version        string   `json:"version"`
	OwnerID        string   `json:"owner_id"`
	Visibility     string   `json:"visibility"`
	OrganizationID *string  `json:"organization_id,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	Category       *string  `json:"category,omitempty"`
	Changelog      *string  `json:"changelog,omitempty"`
}

// SearchCapabilitiesRequest filters for capability search
type SearchCapabilitiesRequest struct {
	Query        string   `json:"query,omitempty"`
	Domain       string   `json:"domain,omitempty"`
	Category     string   `json:"category,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	RiskLevel    string   `json:"risk_level,omitempty"`
	MinRating    *float64 `json:"min_rating,omitempty"`
	Visibility   string   `json:"visibility,omitempty"`
	Status       string   `json:"status,omitempty"`
	SortBy       string   `json:"sort_by,omitempty"` // downloads, rating, created_at
	Limit        int      `json:"limit,omitempty"`
	Offset       int      `json:"offset,omitempty"`
}

// PublishCapability publishes a new capability or new version of existing capability
func (s *MarketplaceService) PublishCapability(ctx context.Context, req PublishCapabilityRequest) (*CapabilityRegistry, *CapabilityVersion, error) {
	// Parse YAML to extract metadata
	parsed, err := ParseCapabilityYAML([]byte(req.YAMLContent))
	if err != nil {
		return nil, nil, fmt.Errorf("invalid capability YAML: %w", err)
	}

	// Compute YAML hash
	hash := sha256.Sum256([]byte(req.YAMLContent))
	yamlHash := hex.EncodeToString(hash[:])

	// Check if capability already exists
	var existingID string
	err = s.db.QueryRowContext(ctx, `
		SELECT id FROM capability_registry WHERE name = ?
	`, parsed.Name).Scan(&existingID)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	var registry *CapabilityRegistry
	var version *CapabilityVersion

	now := time.Now()

	if err == sql.ErrNoRows {
		// Create new capability
		registryID := uuid.New().String()
		tagsJSON, _ := json.Marshal(req.Tags)

		_, err = tx.ExecContext(ctx, `
			INSERT INTO capability_registry (
				id, name, domain, resource_type, operation, risk_level,
				owner_id, visibility, organization_id, description, tags, category,
				status, published_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, registryID, parsed.Name, parsed.Domain, parsed.ResourceType, parsed.Operation,
			parsed.Risk, req.OwnerID, req.Visibility, req.OrganizationID, parsed.AI.Description,
			tagsJSON, req.Category, "published", now, now, now)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create capability registry: %w", err)
		}

		registry = &CapabilityRegistry{
			ID:           registryID,
			Name:         parsed.Name,
			Domain:       parsed.Domain,
			ResourceType: parsed.ResourceType,
			Operation:    parsed.Operation,
			RiskLevel:    parsed.Risk,
			OwnerID:      req.OwnerID,
			Visibility:   req.Visibility,
			Description:  parsed.AI.Description,
			Status:       "published",
			PublishedAt:  &now,
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		existingID = registryID
	} else if err != nil {
		return nil, nil, err
	}

	// Create new version
	versionID := uuid.New().String()
	inputSchemaJSON, _ := json.Marshal(parsed.InputSchema)
	governanceJSON, _ := json.Marshal(parsed.Governance)

	_, err = tx.ExecContext(ctx, `
		INSERT INTO capability_versions (
			id, capability_id, version, yaml_content, yaml_hash,
			schema_version, backend_adapter, input_schema, governance,
			changelog, status, published_at, published_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, versionID, existingID, req.Version, req.YAMLContent, yamlHash,
		parsed.SchemaVersion, parsed.Backend.Adapter, inputSchemaJSON, governanceJSON,
		req.Changelog, "published", now, req.OwnerID, now)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create capability version: %w", err)
	}

	version = &CapabilityVersion{
		ID:             versionID,
		CapabilityID:   existingID,
		Version:        req.Version,
		YAMLContent:    req.YAMLContent,
		YAMLHash:       yamlHash,
		SchemaVersion:  parsed.SchemaVersion,
		BackendAdapter: parsed.Backend.Adapter,
		InputSchema:    inputSchemaJSON,
		Governance:     governanceJSON,
		Changelog:      req.Changelog,
		Status:         "published",
		PublishedAt:    &now,
		PublishedBy:    &req.OwnerID,
		CreatedAt:      now,
	}

	if err = tx.Commit(); err != nil {
		return nil, nil, err
	}

	if registry == nil {
		// Fetch existing registry
		registry, err = s.GetCapability(ctx, existingID)
		if err != nil {
			return nil, nil, err
		}
	}

	return registry, version, nil
}

// SearchCapabilities finds capabilities matching the search criteria
func (s *MarketplaceService) SearchCapabilities(ctx context.Context, req SearchCapabilitiesRequest) ([]*CapabilityRegistry, int, error) {
	query := `
		SELECT id, name, domain, resource_type, operation, risk_level,
		       owner_id, visibility, organization_id, description, tags, category,
		       download_count, usage_count, avg_rating, rating_count,
		       status, published_at, deprecated_at, created_at, updated_at
		FROM capability_registry
		WHERE 1=1
	`
	args := []interface{}{}

	if req.Domain != "" {
		query += " AND domain = ?"
		args = append(args, req.Domain)
	}
	if req.Category != "" {
		query += " AND category = ?"
		args = append(args, req.Category)
	}
	if req.RiskLevel != "" {
		query += " AND risk_level = ?"
		args = append(args, req.RiskLevel)
	}
	if req.MinRating != nil {
		query += " AND avg_rating >= ?"
		args = append(args, *req.MinRating)
	}
	if req.Visibility != "" {
		query += " AND visibility = ?"
		args = append(args, req.Visibility)
	}
	if req.Status != "" {
		query += " AND status = ?"
		args = append(args, req.Status)
	} else {
		query += " AND status = 'published'"
	}

	// Full-text search on name and description
	if req.Query != "" {
		query += " AND (name LIKE ? OR description LIKE ?)"
		searchTerm := "%" + req.Query + "%"
		args = append(args, searchTerm, searchTerm)
	}

	// Sorting
	sortBy := "created_at DESC"
	if req.SortBy == "downloads" {
		sortBy = "download_count DESC"
	} else if req.SortBy == "rating" {
		sortBy = "avg_rating DESC, rating_count DESC"
	} else if req.SortBy == "usage" {
		sortBy = "usage_count DESC"
	}
	query += " ORDER BY " + sortBy

	// Pagination
	limit := 20
	if req.Limit > 0 && req.Limit <= 100 {
		limit = req.Limit
	}
	offset := 0
	if req.Offset > 0 {
		offset = req.Offset
	}
	query += " LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	capabilities := []*CapabilityRegistry{}
	for rows.Next() {
		c := &CapabilityRegistry{}
		err := rows.Scan(
			&c.ID, &c.Name, &c.Domain, &c.ResourceType, &c.Operation, &c.RiskLevel,
			&c.OwnerID, &c.Visibility, &c.OrganizationID, &c.Description, &c.Tags, &c.Category,
			&c.DownloadCount, &c.UsageCount, &c.AvgRating, &c.RatingCount,
			&c.Status, &c.PublishedAt, &c.DeprecatedAt, &c.CreatedAt, &c.UpdatedAt,
		)
		if err != nil {
			return nil, 0, err
		}
		capabilities = append(capabilities, c)
	}

	// Get total count
	countQuery := "SELECT COUNT(*) FROM capability_registry WHERE 1=1"
	countArgs := args[:len(args)-2] // exclude LIMIT and OFFSET
	var total int
	err = s.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	return capabilities, total, nil
}

// GetCapability retrieves a capability by ID
func (s *MarketplaceService) GetCapability(ctx context.Context, id string) (*CapabilityRegistry, error) {
	c := &CapabilityRegistry{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, domain, resource_type, operation, risk_level,
		       owner_id, visibility, organization_id, description, tags, category,
		       download_count, usage_count, avg_rating, rating_count,
		       status, published_at, deprecated_at, created_at, updated_at
		FROM capability_registry WHERE id = ?
	`, id).Scan(
		&c.ID, &c.Name, &c.Domain, &c.ResourceType, &c.Operation, &c.RiskLevel,
		&c.OwnerID, &c.Visibility, &c.OrganizationID, &c.Description, &c.Tags, &c.Category,
		&c.DownloadCount, &c.UsageCount, &c.AvgRating, &c.RatingCount,
		&c.Status, &c.PublishedAt, &c.DeprecatedAt, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// GetCapabilityVersions retrieves all versions of a capability
func (s *MarketplaceService) GetCapabilityVersions(ctx context.Context, capabilityID string) ([]*CapabilityVersion, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, capability_id, version, yaml_content, yaml_hash,
		       schema_version, backend_adapter, input_schema, output_schema, governance,
		       changelog, breaking_changes, status, published_at, published_by, created_at
		FROM capability_versions
		WHERE capability_id = ?
		ORDER BY created_at DESC
	`, capabilityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	versions := []*CapabilityVersion{}
	for rows.Next() {
		v := &CapabilityVersion{}
		err := rows.Scan(
			&v.ID, &v.CapabilityID, &v.Version, &v.YAMLContent, &v.YAMLHash,
			&v.SchemaVersion, &v.BackendAdapter, &v.InputSchema, &v.OutputSchema, &v.Governance,
			&v.Changelog, &v.BreakingChanges, &v.Status, &v.PublishedAt, &v.PublishedBy, &v.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, nil
}

// RateCapability submits or updates a user rating
func (s *MarketplaceService) RateCapability(ctx context.Context, capabilityID, userID string, rating int, review *string, versionUsed *string, environment *string) error {
	if rating < 1 || rating > 5 {
		return fmt.Errorf("rating must be between 1 and 5")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now()
	ratingID := uuid.New().String()

	// Upsert rating
	_, err = tx.ExecContext(ctx, `
		INSERT INTO capability_ratings (id, capability_id, user_id, rating, review, version_used, environment, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			rating = VALUES(rating),
			review = VALUES(review),
			version_used = VALUES(version_used),
			environment = VALUES(environment),
			updated_at = VALUES(updated_at)
	`, ratingID, capabilityID, userID, rating, review, versionUsed, environment, now, now)
	if err != nil {
		return err
	}

	// Recalculate average rating
	_, err = tx.ExecContext(ctx, `
		UPDATE capability_registry
		SET avg_rating = (
			SELECT AVG(rating) FROM capability_ratings WHERE capability_id = ?
		),
		rating_count = (
			SELECT COUNT(*) FROM capability_ratings WHERE capability_id = ?
		)
		WHERE id = ?
	`, capabilityID, capabilityID, capabilityID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// RecordDownload tracks a capability download
func (s *MarketplaceService) RecordDownload(ctx context.Context, capabilityID, versionID, userID string, organizationID *string, environment *string, source string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	downloadID := uuid.New().String()
	now := time.Now()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO capability_downloads (id, capability_id, version_id, user_id, organization_id, environment, download_source, downloaded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, downloadID, capabilityID, versionID, userID, organizationID, environment, source, now)
	if err != nil {
		return err
	}

	// Increment download count
	_, err = tx.ExecContext(ctx, `
		UPDATE capability_registry SET download_count = download_count + 1 WHERE id = ?
	`, capabilityID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// RecordUsage tracks a capability execution
func (s *MarketplaceService) RecordUsage(ctx context.Context, capabilityID string, versionID *string, userID string, organizationID *string, environment string, status string, executionTimeMs *int, actionPlanID *string) error {
	usageID := uuid.New().String()
	now := time.Now()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO capability_usage_stats (id, capability_id, version_id, user_id, organization_id, environment, status, execution_time_ms, action_plan_id, executed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, usageID, capabilityID, versionID, userID, organizationID, environment, status, executionTimeMs, actionPlanID, now)
	if err != nil {
		return err
	}

	// Increment usage count
	_, err = tx.ExecContext(ctx, `
		UPDATE capability_registry SET usage_count = usage_count + 1 WHERE id = ?
	`, capabilityID)
	if err != nil {
		return err
	}

	return tx.Commit()
}
