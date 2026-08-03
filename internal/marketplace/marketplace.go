// Package marketplace persists published capabilities so teams can share,
// version, and rate them. It is a registry layer on top of internal/capabilities:
// the YAML stored here is the same schema the loader and validator accept, so a
// downloaded capability can be dropped straight into a published/ directory.
package marketplace

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/knowledge"
)

// ErrNotFound is returned when a capability or version does not exist.
var ErrNotFound = errors.New("capability not found")

// Visibility values. Private is the safe default for an unspecified request.
const (
	VisibilityPrivate = "private"
	VisibilityTeam    = "team"
	VisibilityPublic  = "public"
)

// Registry status values, tracked separately from a capability YAML's own
// status: a capability can be published to the marketplace and later deprecated
// there without rewriting the YAML.
const (
	StatusPublished  = "published"
	StatusDeprecated = "deprecated"
)

// Service manages the capability registry, its versions, and the marketplace
// signals (ratings, downloads, usage) that help operators pick a capability.
type Service struct {
	db     *sql.DB
	sqlite bool
	now    func() time.Time

	// 可选的语义检索依赖。启用后 Publish/Deprecate 会把能力的 AI 描述建成
	// knowledge 文档（带向量），SemanticSearch 用自然语言查询召回能力。
	semStore knowledge.Store
	semEmbed knowledge.Embedder
}

// NewService creates a marketplace service backed by db. It probes the driver
// so ratings use the dialect's own upsert (MySQL ON DUPLICATE KEY UPDATE vs
// SQLite ON CONFLICT), matching the pattern in internal/store/alerts_sql.go.
func NewService(db *sql.DB) *Service {
	return &Service{
		db:     db,
		sqlite: isSQLiteDriver(db),
		now:    func() time.Time { return time.Now().UTC() },
	}
}

// isSQLiteDriver reports whether db is backed by SQLite by probing the
// SQLite-only PRAGMA statement; MySQL rejects it with an error.
func isSQLiteDriver(db *sql.DB) bool {
	row := db.QueryRow("PRAGMA journal_mode")
	var mode string
	if err := row.Scan(&mode); err != nil {
		return false
	}
	return true
}

// Registry is one capability in the marketplace, with its aggregate signals.
type Registry struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Domain         string     `json:"domain"`
	ResourceType   string     `json:"resource_type"`
	Operation      string     `json:"operation"`
	RiskLevel      string     `json:"risk_level"`
	OwnerID        string     `json:"owner_id"`
	Visibility     string     `json:"visibility"`
	OrganizationID *string    `json:"organization_id,omitempty"`
	Description    string     `json:"description"`
	Tags           []string   `json:"tags,omitempty"`
	Category       *string    `json:"category,omitempty"`
	DownloadCount  int        `json:"download_count"`
	UsageCount     int        `json:"usage_count"`
	AvgRating      *float64   `json:"avg_rating,omitempty"`
	RatingCount    int        `json:"rating_count"`
	Status         string     `json:"status"`
	PublishedAt    *time.Time `json:"published_at,omitempty"`
	DeprecatedAt   *time.Time `json:"deprecated_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Version is one immutable revision of a capability's YAML.
type Version struct {
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

// Rating is one operator's score and review for a capability.
type Rating struct {
	ID           string    `json:"id"`
	CapabilityID string    `json:"capability_id"`
	UserID       string    `json:"user_id"`
	Rating       int       `json:"rating"`
	Review       *string   `json:"review,omitempty"`
	VersionUsed  *string   `json:"version_used,omitempty"`
	Environment  *string   `json:"environment,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Stats aggregates a capability's execution history.
type Stats struct {
	CapabilityID    string         `json:"capability_id"`
	TotalDownloads  int            `json:"total_downloads"`
	TotalExecutions int            `json:"total_executions"`
	SuccessRate     float64        `json:"success_rate"`
	AvgDurationMS   *float64       `json:"avg_duration_ms,omitempty"`
	ByEnvironment   map[string]int `json:"executions_by_environment"`
}

// PublishRequest publishes a new capability or a new version of an existing one.
type PublishRequest struct {
	YAMLContent    string   `json:"yaml_content"`
	Version        string   `json:"version"`
	OwnerID        string   `json:"owner_id"`
	Visibility     string   `json:"visibility"`
	OrganizationID *string  `json:"organization_id,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	Category       *string  `json:"category,omitempty"`
	Changelog      *string  `json:"changelog,omitempty"`
}

// SearchRequest filters and paginates a marketplace search.
type SearchRequest struct {
	Query      string   `json:"query,omitempty"`
	Domain     string   `json:"domain,omitempty"`
	Category   string   `json:"category,omitempty"`
	RiskLevel  string   `json:"risk_level,omitempty"`
	MinRating  *float64 `json:"min_rating,omitempty"`
	Visibility string   `json:"visibility,omitempty"`
	Status     string   `json:"status,omitempty"`
	SortBy     string   `json:"sort_by,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	Offset     int      `json:"offset,omitempty"`
}

// ParseCapabilityYAML decodes and validates capability YAML. Publishing runs
// the same validation the loader does, so the marketplace can never hand out a
// capability that would be rejected at load time.
func ParseCapabilityYAML(body []byte) (capabilities.Capability, error) {
	var capability capabilities.Capability
	if err := yaml.Unmarshal(body, &capability); err != nil {
		return capabilities.Capability{}, fmt.Errorf("parse capability YAML: %w", err)
	}
	if err := capabilities.Validate(capability); err != nil {
		return capabilities.Capability{}, fmt.Errorf("validate capability: %w", err)
	}
	return capability, nil
}

// Publish stores a capability version, creating the registry entry on first
// publish. The whole operation is one transaction so a failed version insert
// never leaves an empty registry entry behind.
func (s *Service) Publish(ctx context.Context, req PublishRequest) (*Registry, *Version, error) {
	parsed, err := ParseCapabilityYAML([]byte(req.YAMLContent))
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(req.Version) == "" {
		return nil, nil, errors.New("version is required")
	}
	if strings.TrimSpace(req.OwnerID) == "" {
		return nil, nil, errors.New("owner_id is required")
	}
	visibility, err := normalizeVisibility(req.Visibility)
	if err != nil {
		return nil, nil, err
	}

	sum := sha256.Sum256([]byte(req.YAMLContent))
	yamlHash := hex.EncodeToString(sum[:])
	now := s.now()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	// Look up the existing registry row inside the transaction so a concurrent
	// publish of the same name cannot slip between the read and the insert.
	var registryID string
	lookupErr := tx.QueryRowContext(ctx, `SELECT id FROM capability_registry WHERE name = ?`, parsed.Name).Scan(&registryID)
	switch {
	case errors.Is(lookupErr, sql.ErrNoRows):
		registryID = uuid.NewString()
		tagsJSON, err := json.Marshal(req.Tags)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal tags: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO capability_registry
			(id, name, domain, resource_type, operation, risk_level,
			 owner_id, visibility, organization_id, description, tags, category,
			 status, published_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			registryID, parsed.Name, parsed.Domain, parsed.ResourceType,
			string(parsed.Operation), string(parsed.Risk), req.OwnerID, visibility,
			req.OrganizationID, parsed.AI.Description, string(tagsJSON), req.Category,
			StatusPublished, now, now, now); err != nil {
			return nil, nil, fmt.Errorf("insert capability registry: %w", err)
		}
	case lookupErr != nil:
		return nil, nil, fmt.Errorf("look up capability %q: %w", parsed.Name, lookupErr)
	}

	versionID := uuid.NewString()
	inputSchemaJSON, err := json.Marshal(parsed.InputSchema)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal input_schema: %w", err)
	}
	outputSchemaJSON, err := json.Marshal(parsed.Output)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal output: %w", err)
	}
	governanceJSON, err := json.Marshal(parsed.Governance)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal governance: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO capability_versions
		(id, capability_id, version, yaml_content, yaml_hash,
		 schema_version, backend_adapter, input_schema, output_schema, governance,
		 changelog, status, published_at, published_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		versionID, registryID, req.Version, req.YAMLContent, yamlHash,
		parsed.SchemaVersion, parsed.Backend.Adapter, string(inputSchemaJSON),
		string(outputSchemaJSON), string(governanceJSON), req.Changelog,
		StatusPublished, now, req.OwnerID, now); err != nil {
		return nil, nil, fmt.Errorf("insert capability version: %w", err)
	}

	// The description and risk of the newest published version become the
	// registry's summary, so a re-publish that changes them is reflected in
	// search results rather than showing the first version's metadata forever.
	if _, err := tx.ExecContext(ctx, `UPDATE capability_registry
		SET description = ?, risk_level = ?, operation = ?, updated_at = ?
		WHERE id = ?`,
		parsed.AI.Description, string(parsed.Risk), string(parsed.Operation), now, registryID); err != nil {
		return nil, nil, fmt.Errorf("update capability registry: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}

	registry, err := s.Get(ctx, registryID)
	if err != nil {
		return nil, nil, err
	}
	// 语义索引是 best-effort：检索索引构建失败绝不让发布本身失败，所以这里
	// 忽略错误。索引在事务提交之后才写，避免半成品进检索。
	s.indexCapability(ctx, registry, parsed)
	version := &Version{
		ID:             versionID,
		CapabilityID:   registryID,
		Version:        req.Version,
		YAMLContent:    req.YAMLContent,
		YAMLHash:       yamlHash,
		SchemaVersion:  parsed.SchemaVersion,
		BackendAdapter: parsed.Backend.Adapter,
		InputSchema:    inputSchemaJSON,
		OutputSchema:   outputSchemaJSON,
		Governance:     governanceJSON,
		Changelog:      req.Changelog,
		Status:         StatusPublished,
		PublishedAt:    &now,
		PublishedBy:    &req.OwnerID,
		CreatedAt:      now,
	}
	return registry, version, nil
}

func normalizeVisibility(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case "":
		return VisibilityPrivate, nil
	case VisibilityPrivate, VisibilityTeam, VisibilityPublic:
		return strings.TrimSpace(value), nil
	default:
		return "", fmt.Errorf("invalid visibility %q", value)
	}
}

const registryColumns = `id, name, domain, resource_type, operation, risk_level,
	owner_id, visibility, organization_id, description, tags, category,
	download_count, usage_count, avg_rating, rating_count,
	status, published_at, deprecated_at, created_at, updated_at`

// Search returns matching capabilities plus the total match count. The filter
// clauses are built once and shared by the page query and the count query so
// the two can never disagree about what "matching" means.
func (s *Service) Search(ctx context.Context, req SearchRequest) ([]Registry, int, error) {
	where := " WHERE 1=1"
	args := []any{}

	if req.Domain != "" {
		where += " AND domain = ?"
		args = append(args, req.Domain)
	}
	if req.Category != "" {
		where += " AND category = ?"
		args = append(args, req.Category)
	}
	if req.RiskLevel != "" {
		where += " AND risk_level = ?"
		args = append(args, req.RiskLevel)
	}
	if req.MinRating != nil {
		where += " AND avg_rating >= ?"
		args = append(args, *req.MinRating)
	}
	if req.Visibility != "" {
		where += " AND visibility = ?"
		args = append(args, req.Visibility)
	}
	if req.Status != "" {
		where += " AND status = ?"
		args = append(args, req.Status)
	} else {
		where += " AND status = ?"
		args = append(args, StatusPublished)
	}
	if req.Query != "" {
		where += " AND (name LIKE ? OR description LIKE ?)"
		term := "%" + escapeLike(req.Query) + "%"
		args = append(args, term, term)
	}

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM capability_registry`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count capabilities: %w", err)
	}

	limit := req.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}
	query := `SELECT ` + registryColumns + ` FROM capability_registry` + where +
		` ORDER BY ` + sortClause(req.SortBy) + ` LIMIT ? OFFSET ?`

	rows, err := s.db.QueryContext(ctx, query, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("search capabilities: %w", err)
	}
	defer rows.Close()

	items := []Registry{}
	for rows.Next() {
		item, err := scanRegistry(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// sortClause maps the caller's sort_by to a fixed ORDER BY. Only these literals
// ever reach the query, so the caller cannot inject SQL through sort_by.
func sortClause(sortBy string) string {
	switch sortBy {
	case "downloads":
		return "download_count DESC, created_at DESC"
	case "rating":
		return "avg_rating DESC, rating_count DESC"
	case "usage":
		return "usage_count DESC, created_at DESC"
	case "name":
		return "name ASC"
	default:
		return "created_at DESC"
	}
}

// escapeLike neutralizes LIKE wildcards in user input so a search for "100%"
// does not match everything.
func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRegistry(row rowScanner) (Registry, error) {
	var item Registry
	var tagsJSON sql.NullString
	if err := row.Scan(
		&item.ID, &item.Name, &item.Domain, &item.ResourceType, &item.Operation, &item.RiskLevel,
		&item.OwnerID, &item.Visibility, &item.OrganizationID, &item.Description, &tagsJSON, &item.Category,
		&item.DownloadCount, &item.UsageCount, &item.AvgRating, &item.RatingCount,
		&item.Status, &item.PublishedAt, &item.DeprecatedAt, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return Registry{}, err
	}
	if tagsJSON.Valid && strings.TrimSpace(tagsJSON.String) != "" {
		// A malformed tags column should not fail the whole listing; the rest of
		// the row is still useful, so tags degrade to empty.
		_ = json.Unmarshal([]byte(tagsJSON.String), &item.Tags)
	}
	return item, nil
}

// Get returns one capability by registry ID.
func (s *Service) Get(ctx context.Context, id string) (*Registry, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+registryColumns+` FROM capability_registry WHERE id = ?`, id)
	item, err := scanRegistry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// GetByName returns one capability by its capability name (e.g. "k8s.pod.restart").
func (s *Service) GetByName(ctx context.Context, name string) (*Registry, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+registryColumns+` FROM capability_registry WHERE name = ?`, name)
	item, err := scanRegistry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// ListVersions returns every version of a capability, newest first.
func (s *Service) ListVersions(ctx context.Context, capabilityID string) ([]Version, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, capability_id, version, yaml_content, yaml_hash,
		schema_version, backend_adapter, input_schema, output_schema, governance,
		changelog, breaking_changes, status, published_at, published_by, created_at
		FROM capability_versions WHERE capability_id = ? ORDER BY created_at DESC`, capabilityID)
	if err != nil {
		return nil, fmt.Errorf("list capability versions: %w", err)
	}
	defer rows.Close()

	versions := []Version{}
	for rows.Next() {
		var v Version
		var inputSchema, outputSchema, governance sql.NullString
		if err := rows.Scan(
			&v.ID, &v.CapabilityID, &v.Version, &v.YAMLContent, &v.YAMLHash,
			&v.SchemaVersion, &v.BackendAdapter, &inputSchema, &outputSchema, &governance,
			&v.Changelog, &v.BreakingChanges, &v.Status, &v.PublishedAt, &v.PublishedBy, &v.CreatedAt,
		); err != nil {
			return nil, err
		}
		v.InputSchema = rawJSON(inputSchema)
		v.OutputSchema = rawJSON(outputSchema)
		v.Governance = rawJSON(governance)
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return versions, nil
}

func rawJSON(value sql.NullString) json.RawMessage {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	return json.RawMessage(value.String)
}

// GetVersion returns one version of a capability.
func (s *Service) GetVersion(ctx context.Context, capabilityID, versionID string) (*Version, error) {
	versions, err := s.ListVersions(ctx, capabilityID)
	if err != nil {
		return nil, err
	}
	for i := range versions {
		if versions[i].ID == versionID {
			return &versions[i], nil
		}
	}
	return nil, ErrNotFound
}

// Rate records or replaces a user's rating and recomputes the capability's
// aggregate score in the same transaction, so avg_rating never lags the
// underlying rows.
func (s *Service) Rate(ctx context.Context, capabilityID, userID string, rating int, review, versionUsed, environment *string) error {
	if rating < 1 || rating > 5 {
		return errors.New("rating must be between 1 and 5")
	}
	if strings.TrimSpace(userID) == "" {
		return errors.New("user_id is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	now := s.now()
	if s.sqlite {
		if _, err := tx.ExecContext(ctx, `INSERT INTO capability_ratings
			(id, capability_id, user_id, rating, review, version_used, environment, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(capability_id, user_id) DO UPDATE SET
				rating = excluded.rating,
				review = excluded.review,
				version_used = excluded.version_used,
				environment = excluded.environment,
				updated_at = excluded.updated_at`,
			uuid.NewString(), capabilityID, userID, rating, review, versionUsed, environment, now, now); err != nil {
			return fmt.Errorf("record rating: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `INSERT INTO capability_ratings
			(id, capability_id, user_id, rating, review, version_used, environment, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				rating = VALUES(rating),
				review = VALUES(review),
				version_used = VALUES(version_used),
				environment = VALUES(environment),
				updated_at = VALUES(updated_at)`,
			uuid.NewString(), capabilityID, userID, rating, review, versionUsed, environment, now, now); err != nil {
			return fmt.Errorf("record rating: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `UPDATE capability_registry SET
			avg_rating = (SELECT AVG(rating) FROM capability_ratings WHERE capability_id = ?),
			rating_count = (SELECT COUNT(*) FROM capability_ratings WHERE capability_id = ?),
			updated_at = ?
		WHERE id = ?`, capabilityID, capabilityID, now, capabilityID); err != nil {
		return fmt.Errorf("recompute rating aggregate: %w", err)
	}
	return tx.Commit()
}

// ListRatings returns a capability's ratings, newest first.
func (s *Service) ListRatings(ctx context.Context, capabilityID string, limit, offset int) ([]Rating, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM capability_ratings WHERE capability_id = ?`, capabilityID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count ratings: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id, capability_id, user_id, rating, review,
		version_used, environment, created_at, updated_at
		FROM capability_ratings WHERE capability_id = ?
		ORDER BY created_at DESC LIMIT ? OFFSET ?`, capabilityID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list ratings: %w", err)
	}
	defer rows.Close()

	ratings := []Rating{}
	for rows.Next() {
		var r Rating
		if err := rows.Scan(&r.ID, &r.CapabilityID, &r.UserID, &r.Rating, &r.Review,
			&r.VersionUsed, &r.Environment, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, 0, err
		}
		ratings = append(ratings, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return ratings, total, nil
}

// RecordDownload tracks a download and bumps the capability's download counter.
func (s *Service) RecordDownload(ctx context.Context, capabilityID, versionID, userID string, organizationID, environment *string, source string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `INSERT INTO capability_downloads
		(id, capability_id, version_id, user_id, organization_id, environment, download_source, downloaded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), capabilityID, versionID, userID, organizationID, environment, source, s.now()); err != nil {
		return fmt.Errorf("record download: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE capability_registry SET download_count = download_count + 1 WHERE id = ?`, capabilityID); err != nil {
		return fmt.Errorf("increment download count: %w", err)
	}
	return tx.Commit()
}

// RecordUsage tracks one execution of a capability so search can rank by real
// operational use rather than downloads alone.
func (s *Service) RecordUsage(ctx context.Context, capabilityID string, versionID *string, userID string, organizationID *string, environment, status string, durationMS *int, actionPlanID *string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `INSERT INTO capability_usage_stats
		(id, capability_id, version_id, user_id, organization_id, environment, status, execution_time_ms, action_plan_id, executed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), capabilityID, versionID, userID, organizationID, environment, status, durationMS, actionPlanID, s.now()); err != nil {
		return fmt.Errorf("record usage: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE capability_registry SET usage_count = usage_count + 1 WHERE id = ?`, capabilityID); err != nil {
		return fmt.Errorf("increment usage count: %w", err)
	}
	return tx.Commit()
}

// Stats aggregates a capability's download and execution history.
func (s *Service) Stats(ctx context.Context, capabilityID string) (*Stats, error) {
	result := &Stats{CapabilityID: capabilityID, ByEnvironment: map[string]int{}}

	var succeeded int
	var avgDuration sql.NullFloat64
	if err := s.db.QueryRowContext(ctx, `SELECT
			COUNT(*),
			COALESCE(SUM(status = 'success'), 0),
			AVG(execution_time_ms)
		FROM capability_usage_stats WHERE capability_id = ?`, capabilityID).
		Scan(&result.TotalExecutions, &succeeded, &avgDuration); err != nil {
		return nil, fmt.Errorf("aggregate usage stats: %w", err)
	}
	if result.TotalExecutions > 0 {
		result.SuccessRate = float64(succeeded) / float64(result.TotalExecutions)
	}
	if avgDuration.Valid {
		result.AvgDurationMS = &avgDuration.Float64
	}

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM capability_downloads WHERE capability_id = ?`, capabilityID).
		Scan(&result.TotalDownloads); err != nil {
		return nil, fmt.Errorf("count downloads: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT environment, COUNT(*)
		FROM capability_usage_stats WHERE capability_id = ? GROUP BY environment`, capabilityID)
	if err != nil {
		return nil, fmt.Errorf("aggregate usage by environment: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var environment string
		var count int
		if err := rows.Scan(&environment, &count); err != nil {
			return nil, err
		}
		result.ByEnvironment[environment] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// Deprecate marks a capability as deprecated without deleting its versions, so
// existing installs keep working while search stops surfacing it.
func (s *Service) Deprecate(ctx context.Context, capabilityID, reason string) error {
	now := s.now()
	result, err := s.db.ExecContext(ctx, `UPDATE capability_registry
		SET status = ?, deprecated_at = ?, deprecation_reason = ?, updated_at = ?
		WHERE id = ?`, StatusDeprecated, now, reason, now, capabilityID)
	if err != nil {
		return fmt.Errorf("deprecate capability: %w", err)
	}
	affected, err := result.RowsAffected()
	if err == nil && affected == 0 {
		return ErrNotFound
	}
	// 弃用的能力从语义检索里移除，避免自然语言搜索命中已下线的基础设施。
	s.removeCapabilityIndex(ctx, capabilityID)
	return nil
}
