package capabilities

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// SQLCapabilityStore 是 CapabilityStore 的 MySQL/SQLite 实现：能力以行存储在
// capabilities 表（PK = name + source），多节点共享读写，替代文件目录作为运行时
// 事实源。
type SQLCapabilityStore struct {
	db *sql.DB
}

func NewSQLCapabilityStore(db *sql.DB) *SQLCapabilityStore {
	return &SQLCapabilityStore{db: db}
}

func (s *SQLCapabilityStore) Configured() error {
	if s.db == nil {
		return ErrCapabilityRootNotConfigured
	}
	return nil
}

func (s *SQLCapabilityStore) ListAll(ctx context.Context) ([]ManagedCapability, error) {
	if err := s.Configured(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, source, status, domain, resource_type, operation, risk,
		        schema_version, backend, input_schema, output, governance, auth, ai,
		        verify, depends_on, path, modified_at
		 FROM capabilities ORDER BY
			CASE source WHEN 'discovered' THEN 0 WHEN 'published' THEN 1 ELSE 2 END,
			name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ManagedCapability
	for rows.Next() {
		item, err := scanCapability(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *SQLCapabilityStore) Get(ctx context.Context, name string) (ManagedCapability, error) {
	if err := s.Configured(); err != nil {
		return ManagedCapability{}, err
	}
	// discovered 优先，和文件系统行为一致
	for _, source := range []string{SourceDiscovered, SourcePublished} {
		row := s.db.QueryRowContext(ctx,
			`SELECT name, source, status, domain, resource_type, operation, risk,
			        schema_version, backend, input_schema, output, governance, auth, ai,
			        verify, depends_on, path, modified_at
			 FROM capabilities WHERE name = ? AND source = ?`, name, source)
		item, err := scanSingleCapability(row)
		if err == nil {
			return item, nil
		}
		if err != sql.ErrNoRows {
			return ManagedCapability{}, err
		}
	}
	return ManagedCapability{}, ErrCapabilityNotFound
}

func (s *SQLCapabilityStore) SaveDraft(ctx context.Context, cap Capability) (ManagedCapability, error) {
	if err := s.Configured(); err != nil {
		return ManagedCapability{}, err
	}
	if err := validateManagedCapabilityName(cap.Name); err != nil {
		return ManagedCapability{}, err
	}
	if err := s.upsert(ctx, SourceDiscovered, cap); err != nil {
		return ManagedCapability{}, err
	}
	return s.Get(ctx, cap.Name)
}

func (s *SQLCapabilityStore) SavePublished(ctx context.Context, cap Capability) (ManagedCapability, error) {
	if err := s.Configured(); err != nil {
		return ManagedCapability{}, err
	}
	if err := validateManagedCapabilityName(cap.Name); err != nil {
		return ManagedCapability{}, err
	}
	if err := s.upsert(ctx, SourcePublished, cap); err != nil {
		return ManagedCapability{}, err
	}
	return s.Get(ctx, cap.Name)
}

func (s *SQLCapabilityStore) MoveDraftToPublished(ctx context.Context, name string) (ManagedCapability, error) {
	if err := s.Configured(); err != nil {
		return ManagedCapability{}, err
	}
	if err := validateManagedCapabilityName(name); err != nil {
		return ManagedCapability{}, err
	}
	// 读草稿
	draft, err := s.getBySource(ctx, name, SourceDiscovered)
	if err != nil {
		return ManagedCapability{}, err
	}
	// 检查 published 已存在
	if exists, err := s.Has(ctx, SourcePublished, name); err != nil {
		return ManagedCapability{}, err
	} else if exists {
		return ManagedCapability{}, fmt.Errorf("%w: %q is already published, unpublish the old version first", ErrCapabilityNameConflict, name)
	}
	// 写 published
	cap := draft.Capability
	cap.Status = StatusPublished
	if err := s.upsert(ctx, SourcePublished, cap); err != nil {
		return ManagedCapability{}, err
	}
	// 删 discovered
	if _, err := s.db.ExecContext(ctx, `DELETE FROM capabilities WHERE name = ? AND source = ?`, name, SourceDiscovered); err != nil {
		return ManagedCapability{}, err
	}
	return s.Get(ctx, name)
}

func (s *SQLCapabilityStore) MovePublishedToDraft(ctx context.Context, name string) (ManagedCapability, error) {
	if err := s.Configured(); err != nil {
		return ManagedCapability{}, err
	}
	if err := validateManagedCapabilityName(name); err != nil {
		return ManagedCapability{}, err
	}
	pub, err := s.getBySource(ctx, name, SourcePublished)
	if err != nil {
		return ManagedCapability{}, err
	}
	if exists, err := s.Has(ctx, SourceDiscovered, name); err != nil {
		return ManagedCapability{}, err
	} else if exists {
		return ManagedCapability{}, fmt.Errorf("%w: %q already exists as a draft, remove the draft first", ErrCapabilityNameConflict, name)
	}
	cap := pub.Capability
	cap.Status = StatusNeedsReview
	if err := s.upsert(ctx, SourceDiscovered, cap); err != nil {
		return ManagedCapability{}, err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM capabilities WHERE name = ? AND source = ?`, name, SourcePublished); err != nil {
		return ManagedCapability{}, err
	}
	return s.getBySource(ctx, name, SourceDiscovered)
}

// DeleteDraft 删除草稿行。已发布能力不在此 source，天然不可删。
func (s *SQLCapabilityStore) DeleteDraft(ctx context.Context, name string) error {
	if err := s.Configured(); err != nil {
		return err
	}
	if err := validateManagedCapabilityName(name); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM capabilities WHERE name = ? AND source = ?`, name, SourceDiscovered)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return ErrCapabilityNotFound
	}
	return nil
}

func (s *SQLCapabilityStore) Has(ctx context.Context, source string, name string) (bool, error) {
	if err := s.Configured(); err != nil {
		return false, err
	}
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM capabilities WHERE name = ? AND source = ? LIMIT 1`, name, source).Scan(&one)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, err
}

// getBySource 按 source 查找单个能力，不存在返回 ErrCapabilityNotFound。
func (s *SQLCapabilityStore) getBySource(ctx context.Context, name, source string) (ManagedCapability, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT name, source, status, domain, resource_type, operation, risk,
		        schema_version, backend, input_schema, output, governance, auth, ai,
		        verify, depends_on, path, modified_at
		 FROM capabilities WHERE name = ? AND source = ?`, name, source)
	item, err := scanSingleCapability(row)
	if err == sql.ErrNoRows {
		return ManagedCapability{}, ErrCapabilityNotFound
	}
	return item, err
}

// upsert 插入或更新一条能力记录（PK = name + source）。
// MySQL: INSERT ... ON DUPLICATE KEY UPDATE；SQLite: INSERT OR REPLACE。
func (s *SQLCapabilityStore) upsert(ctx context.Context, source string, cap Capability) error {
	b, err := marshalCapabilityJSON(cap)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, s.upsertSQL(),
		cap.Name, source, cap.Status, cap.Domain, cap.ResourceType, cap.Operation, cap.Risk,
		cap.SchemaVersion, b.Backend, b.InputSchema, b.Output, b.Governance, b.Auth, b.AI,
		b.Verify, b.DependsOn, cap.SchemaVersion, cap.Domain, cap.ResourceType, cap.Operation, cap.Risk,
		b.Backend, b.InputSchema, b.Output, b.Governance, b.Auth, b.AI, b.Verify, b.DependsOn,
	)
	return err
}

func (s *SQLCapabilityStore) upsertSQL() string {
	if s.isSQLite() {
		return `INSERT OR REPLACE INTO capabilities
			(name, source, status, domain, resource_type, operation, risk, schema_version,
			 backend, input_schema, output, governance, auth, ai, verify, depends_on, modified_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`
	}
	return `INSERT INTO capabilities
		(name, source, status, domain, resource_type, operation, risk, schema_version,
		 backend, input_schema, output, governance, auth, ai, verify, depends_on, modified_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP(6))
		ON DUPLICATE KEY UPDATE
			status = VALUES(status), domain = VALUES(domain), resource_type = VALUES(resource_type),
			operation = VALUES(operation), risk = VALUES(risk), schema_version = VALUES(schema_version),
			backend = VALUES(backend), input_schema = VALUES(input_schema), output = VALUES(output),
			governance = VALUES(governance), auth = VALUES(auth), ai = VALUES(ai),
			verify = VALUES(verify), depends_on = VALUES(depends_on), modified_at = CURRENT_TIMESTAMP(6)`
}

func (s *SQLCapabilityStore) isSQLite() bool {
	if s.db == nil || s.db.Driver() == nil {
		return false
	}
	return strings.Contains(fmt.Sprintf("%T", s.db.Driver()), "sqlite")
}

// capabilityJSONRows 用于 upsert 参数的 JSON 字段。
type capabilityJSONRows struct {
	Backend    []byte
	InputSchema []byte
	Output     []byte
	Governance []byte
	Auth       []byte
	AI         []byte
	Verify     sql.NullString
	DependsOn  sql.NullString
}

func marshalCapabilityJSON(cap Capability) (capabilityJSONRows, error) {
	var b capabilityJSONRows
	var err error
	if b.Backend, err = json.Marshal(cap.Backend); err != nil {
		return b, fmt.Errorf("marshal backend: %w", err)
	}
	if b.InputSchema, err = json.Marshal(cap.InputSchema); err != nil {
		return b, fmt.Errorf("marshal input_schema: %w", err)
	}
	if b.Output, err = json.Marshal(cap.Output); err != nil {
		return b, fmt.Errorf("marshal output: %w", err)
	}
	if b.Governance, err = json.Marshal(cap.Governance); err != nil {
		return b, fmt.Errorf("marshal governance: %w", err)
	}
	if b.Auth, err = json.Marshal(cap.Auth); err != nil {
		return b, fmt.Errorf("marshal auth: %w", err)
	}
	if b.AI, err = json.Marshal(cap.AI); err != nil {
		return b, fmt.Errorf("marshal ai: %w", err)
	}
	if cap.Verify != nil {
		v, err := json.Marshal(cap.Verify)
		if err != nil {
			return b, fmt.Errorf("marshal verify: %w", err)
		}
		b.Verify = sql.NullString{String: string(v), Valid: true}
	}
	if len(cap.DependsOn) > 0 {
		v, err := json.Marshal(cap.DependsOn)
		if err != nil {
			return b, fmt.Errorf("marshal depends_on: %w", err)
		}
		b.DependsOn = sql.NullString{String: string(v), Valid: true}
	}
	return b, nil
}

func scanSingleCapability(row *sql.Row) (ManagedCapability, error) {
	return scanCapabilityRow(row)
}

func scanCapability(rows *sql.Rows) (ManagedCapability, error) {
	return scanCapabilityRow(rows)
}

func scanCapabilityRow(r interface{ Scan(dest ...any) error }) (ManagedCapability, error) {
	var item ManagedCapability
	var backend, inputSchema, output, governance, auth, ai []byte
	var verify, dependsOn, path sql.NullString
	var modifiedAt time.Time

	err := r.Scan(
		&item.Name, &item.Source, &item.Status, &item.Domain, &item.ResourceType,
		&item.Operation, &item.Risk, &item.SchemaVersion,
		&backend, &inputSchema, &output, &governance, &auth, &ai,
		&verify, &dependsOn, &path, &modifiedAt,
	)
	if err != nil {
		return ManagedCapability{}, err
	}
	if err := json.Unmarshal(backend, &item.Backend); err != nil {
		return ManagedCapability{}, fmt.Errorf("unmarshal backend for %s: %w", item.Name, err)
	}
	if err := json.Unmarshal(inputSchema, &item.InputSchema); err != nil {
		return ManagedCapability{}, fmt.Errorf("unmarshal input_schema for %s: %w", item.Name, err)
	}
	if err := json.Unmarshal(output, &item.Output); err != nil {
		return ManagedCapability{}, fmt.Errorf("unmarshal output for %s: %w", item.Name, err)
	}
	if err := json.Unmarshal(governance, &item.Governance); err != nil {
		return ManagedCapability{}, fmt.Errorf("unmarshal governance for %s: %w", item.Name, err)
	}
	if err := json.Unmarshal(auth, &item.Auth); err != nil {
		return ManagedCapability{}, fmt.Errorf("unmarshal auth for %s: %w", item.Name, err)
	}
	if err := json.Unmarshal(ai, &item.AI); err != nil {
		return ManagedCapability{}, fmt.Errorf("unmarshal ai for %s: %w", item.Name, err)
	}
	if verify.Valid {
		item.Verify = new(VerifySpec)
		if err := json.Unmarshal([]byte(verify.String), item.Verify); err != nil {
			return ManagedCapability{}, fmt.Errorf("unmarshal verify for %s: %w", item.Name, err)
		}
	}
	if dependsOn.Valid {
		if err := json.Unmarshal([]byte(dependsOn.String), &item.DependsOn); err != nil {
			return ManagedCapability{}, fmt.Errorf("unmarshal depends_on for %s: %w", item.Name, err)
		}
	}
	if path.Valid {
		item.Path = path.String
	}
	item.ModifiedAt = modifiedAt
	item.Validation = validationFromError(Validate(item.Capability))
	return item, nil
}

// SeedFromYAML 从文件系统的 published/ 目录加载已有能力到 DB（首次启动一次性）。
// 跳过 DB 中已存在的（幂等）。返回种子数量。
func (s *SQLCapabilityStore) SeedFromYAML(ctx context.Context, publishedDir string) (int, error) {
	if err := s.Configured(); err != nil {
		return 0, err
	}
	if publishedDir == "" {
		return 0, nil
	}
	// publishedDir 是 .../published 的完整路径，但 FileCapabilityStore.ListAll
	// 期望 root 下有 discovered/ 和 published/ 两个子目录，所以取其父目录。
	fileStore := NewFileCapabilityStore(filepath.Dir(publishedDir))
	items, err := fileStore.ListAll(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, item := range items {
		if item.Source != SourcePublished {
			continue
		}
		if exists, err := s.Has(ctx, SourcePublished, item.Name); err != nil {
			return count, err
		} else if exists {
			continue // 跳过已存在
		}
		if err := s.upsert(ctx, SourcePublished, item.Capability); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

var _ CapabilityStore = (*SQLCapabilityStore)(nil)
