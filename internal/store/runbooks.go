package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Runbook 是可复用的命令/工具序列模板（借鉴-5: Runbook / 命令模板复用）。
// 回答"这类高频低风险动作怎么执行"：命中 IntentPattern 时套用 ToolSequence，
// 低风险 Runbook 可跳过人工确认自动执行。
type Runbook struct {
	ID              string           `json:"id"`
	Slug            string           `json:"slug"`
	Name            string           `json:"name"`
	IntentPattern   []string         `json:"intent_pattern"` // 关键词，最长命中优先
	ToolSequence    []string         `json:"tool_sequence"`  // 工具序列（按序）
	DefaultStrategy *RunbookStrategy `json:"default_strategy,omitempty"`
	RiskLevel       string           `json:"risk_level"` // low / medium / high
	IsBuiltin       bool             `json:"is_builtin"`
	IsEnabled       bool             `json:"is_enabled"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

// RunbookStrategy 是 Runbook 声明的默认执行策略（借鉴-3 建议的超时/重试/并发）。
type RunbookStrategy struct {
	TimeoutMS   int    `json:"timeout_ms,omitempty"`
	Retry       int    `json:"retry,omitempty"`
	Concurrency int    `json:"concurrency,omitempty"`
	RiskLevel   string `json:"risk_level,omitempty"`
}

// RunbookStore 持久化 Runbook 模板。
type RunbookStore interface {
	CreateRunbook(ctx context.Context, rb Runbook) (Runbook, error)
	GetRunbook(ctx context.Context, slug string) (Runbook, error)
	ListRunbooks(ctx context.Context) ([]Runbook, error)
	ListEnabledRunbooks(ctx context.Context) ([]Runbook, error)
	UpdateRunbook(ctx context.Context, rb Runbook) (Runbook, error)
	DeleteRunbook(ctx context.Context, id string) error
}

// NewMemoryRunbookStore 返回一个线程安全的内存 RunbookStore，用于测试。
func NewMemoryRunbookStore() *MemoryRunbookStore {
	return &MemoryRunbookStore{}
}

// MemoryRunbookStore 是 RunbookStore 的内存实现，线程安全。
type MemoryRunbookStore struct {
	mu       sync.RWMutex
	runbooks map[string]Runbook // id -> runbook
}

var _ RunbookStore = (*MemoryRunbookStore)(nil)

func (m *MemoryRunbookStore) CreateRunbook(_ context.Context, rb Runbook) (Runbook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runbooks == nil {
		m.runbooks = map[string]Runbook{}
	}
	for _, existing := range m.runbooks {
		if existing.Slug == rb.Slug {
			return Runbook{}, ErrConflict
		}
	}
	if rb.ID == "" {
		rb.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if rb.CreatedAt.IsZero() {
		rb.CreatedAt = now
	}
	rb.UpdatedAt = now
	m.runbooks[rb.ID] = rb
	return cloneRunbook(rb), nil
}

func (m *MemoryRunbookStore) GetRunbook(_ context.Context, slug string) (Runbook, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, rb := range m.runbooks {
		if rb.Slug == slug {
			return cloneRunbook(rb), nil
		}
	}
	return Runbook{}, ErrNotFound
}

func (m *MemoryRunbookStore) ListRunbooks(_ context.Context) ([]Runbook, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Runbook, 0, len(m.runbooks))
	for _, rb := range m.runbooks {
		out = append(out, cloneRunbook(rb))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func (m *MemoryRunbookStore) ListEnabledRunbooks(_ context.Context) ([]Runbook, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Runbook
	for _, rb := range m.runbooks {
		if !rb.IsEnabled {
			continue
		}
		out = append(out, cloneRunbook(rb))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func (m *MemoryRunbookStore) UpdateRunbook(_ context.Context, rb Runbook) (Runbook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runbooks[rb.ID]; !ok {
		return Runbook{}, ErrNotFound
	}
	rb.UpdatedAt = time.Now().UTC()
	m.runbooks[rb.ID] = rb
	return cloneRunbook(rb), nil
}

func (m *MemoryRunbookStore) DeleteRunbook(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runbooks[id]; !ok {
		return ErrNotFound
	}
	delete(m.runbooks, id)
	return nil
}

func cloneRunbook(rb Runbook) Runbook {
	out := rb
	if rb.IntentPattern != nil {
		out.IntentPattern = append([]string(nil), rb.IntentPattern...)
	}
	if rb.ToolSequence != nil {
		out.ToolSequence = append([]string(nil), rb.ToolSequence...)
	}
	if rb.DefaultStrategy != nil {
		strategy := *rb.DefaultStrategy
		out.DefaultStrategy = &strategy
	}
	return out
}
