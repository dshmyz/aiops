package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Alert 是持久化的归一化告警。与 internal/alert.Alert 同构，但定义在
// store 包内避免 store → alert 依赖（repo 惯例：store 记录类型独立定义，
// 见 MCPServerRecord 与 internal/mcp 的关系）。
type Alert struct {
	ID           string            `json:"id"`
	ExternalID   string            `json:"external_id"`
	Source       string            `json:"source"`
	Title        string            `json:"title"`
	Description  string            `json:"description"`
	Severity     string            `json:"severity"`
	Status       string            `json:"status"`
	Domain       string            `json:"domain,omitempty"`
	ResourceType string            `json:"resource_type,omitempty"`
	ResourceName string            `json:"resource_name,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	FiredAt      time.Time         `json:"fired_at"`
	ResolvedAt   *time.Time        `json:"resolved_at,omitempty"`
	Raw          map[string]any    `json:"raw,omitempty"`
	ReceivedAt   time.Time         `json:"received_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// AlertFilter 是告警查询条件。零值字段不参与过滤。
type AlertFilter struct {
	Status   string
	Severity string
	Domain   string
	Limit    int
}

// defaultAlertLimit 是 AlertFilter.Limit 为 0 时的默认查询上限。
const defaultAlertLimit = 20

// maxAlertLimit 是告警查询的单次上限，防止一次查询拉空全表。
const maxAlertLimit = 200

// AlertStore 持久化归一化告警。告警身份 = Source + ExternalID，Upsert 幂等：
// 身份已存在时更新而非报错，返回 created=false。
type AlertStore interface {
	Upsert(ctx context.Context, a Alert) (Alert, bool, error)
	Get(ctx context.Context, id string) (Alert, error)
	UpdateDescription(ctx context.Context, id, description string) error
	Query(ctx context.Context, f AlertFilter) ([]Alert, error)
	ListActive(ctx context.Context, limit int) ([]Alert, error)
	Resolve(ctx context.Context, externalID, source string) (Alert, error)
}

// MemoryAlertStore 是并发安全的内存实现，用于单元测试。
type MemoryAlertStore struct {
	mu    sync.Mutex
	byID  map[string]Alert
	byKey map[string]string // key = Source + ":" + ExternalID → id
	clock func() time.Time
}

// NewMemoryAlertStore 创建一个内存告警 store。
func NewMemoryAlertStore() *MemoryAlertStore {
	return &MemoryAlertStore{
		byID:  map[string]Alert{},
		byKey: map[string]string{},
		clock: time.Now,
	}
}

// WithClock 注入自定义时钟，用于测试。
func (s *MemoryAlertStore) WithClock(clock func() time.Time) *MemoryAlertStore {
	s.clock = clock
	return s
}

func alertKey(source, externalID string) string {
	return source + ":" + externalID
}

// Upsert 幂等写入告警：身份（source+external_id）已存在则更新并返回
// created=false，否则新建并返回 created=true。
func (s *MemoryAlertStore) Upsert(ctx context.Context, a Alert) (Alert, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := alertKey(a.Source, a.ExternalID)
	if id, ok := s.byKey[key]; ok {
		existing := s.byID[id]
		a.ID = existing.ID
		a.ReceivedAt = existing.ReceivedAt
		a.UpdatedAt = s.clock()
		if a.Status == "resolved" && a.ResolvedAt == nil {
			resolvedAt := s.clock()
			a.ResolvedAt = &resolvedAt
		} else if a.Status != "resolved" {
			a.ResolvedAt = nil
		}
		s.byID[a.ID] = a
		return cloneStoreAlert(a), false, nil
	}
	a.ID = uuid.NewString()
	now := s.clock()
	a.ReceivedAt = now
	a.UpdatedAt = now
	if a.Status == "resolved" && a.ResolvedAt == nil {
		resolvedAt := now
		a.ResolvedAt = &resolvedAt
	}
	s.byID[a.ID] = a
	s.byKey[key] = a.ID
	return cloneStoreAlert(a), true, nil
}

func (s *MemoryAlertStore) Get(ctx context.Context, id string) (Alert, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.byID[id]
	if !ok {
		return Alert{}, ErrNotFound
	}
	return cloneStoreAlert(a), nil
}

func (s *MemoryAlertStore) UpdateDescription(_ context.Context, id, description string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.byID[id]
	if !ok {
		return ErrNotFound
	}
	a.Description = description
	a.UpdatedAt = s.clock()
	s.byID[id] = a
	return nil
}

func (s *MemoryAlertStore) Query(ctx context.Context, f AlertFilter) ([]Alert, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := f.Limit
	if limit <= 0 {
		limit = defaultAlertLimit
	}
	if limit > maxAlertLimit {
		limit = maxAlertLimit
	}
	// 按 UpdatedAt 降序，保证结果确定性。
	ids := sortedAlertIDs(s.byID)
	var out []Alert
	for _, id := range ids {
		a := s.byID[id]
		if f.Status != "" && a.Status != f.Status {
			continue
		}
		if f.Severity != "" && a.Severity != f.Severity {
			continue
		}
		if f.Domain != "" && a.Domain != f.Domain {
			continue
		}
		out = append(out, cloneStoreAlert(a))
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *MemoryAlertStore) ListActive(ctx context.Context, limit int) ([]Alert, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = defaultAlertLimit
	}
	if limit > maxAlertLimit {
		limit = maxAlertLimit
	}
	ids := sortedAlertIDs(s.byID)
	var out []Alert
	for _, id := range ids {
		a := s.byID[id]
		if a.Status != "firing" {
			continue
		}
		out = append(out, cloneStoreAlert(a))
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *MemoryAlertStore) Resolve(ctx context.Context, externalID, source string) (Alert, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byKey[alertKey(source, externalID)]
	if !ok {
		return Alert{}, ErrNotFound
	}
	a := s.byID[id]
	a.Status = "resolved"
	now := s.clock()
	if a.ResolvedAt == nil {
		a.ResolvedAt = &now
	}
	a.UpdatedAt = now
	s.byID[a.ID] = a
	return cloneStoreAlert(a), nil
}

// AlertRecords 返回告警副本列表（按 ID 排序），用于确定性测试断言。
func (s *MemoryAlertStore) AlertRecords() []Alert {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := sortedAlertIDs(s.byID)
	out := make([]Alert, 0, len(ids))
	for _, id := range ids {
		out = append(out, cloneStoreAlert(s.byID[id]))
	}
	return out
}

func sortedAlertIDs(m map[string]Alert) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	// 按 ID 字典序，保证 Memory 实现结果确定性；真实排序语义由 SQL 的
	// ORDER BY updated_at DESC 保证。
	sort.Strings(ids)
	return ids
}

func cloneStoreAlert(a Alert) Alert {
	out := a
	if a.Labels != nil {
		out.Labels = make(map[string]string, len(a.Labels))
		for k, v := range a.Labels {
			out.Labels[k] = v
		}
	}
	return out
}
