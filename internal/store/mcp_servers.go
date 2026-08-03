package store

import (
	"context"
	"sync"
	"time"
)

// MCPServerRecord 是外部 MCP 服务器的持久化记录（热配置）。
// Name 唯一，用作工具命名前缀。Command（stdio）与 URL（SSE）二选一。
// Enabled=false 的服务器在 Reload 时被跳过，但其配置保留在 DB 以便重新启用。
type MCPServerRecord struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	URL       string            `json:"url"`
	Enabled   bool              `json:"enabled"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// MCPServerStore 持久化 MCP 服务器配置，支持热配置 CRUD。
type MCPServerStore interface {
	Create(ctx context.Context, server MCPServerRecord) (MCPServerRecord, error)
	Get(ctx context.Context, id string) (MCPServerRecord, error)
	List(ctx context.Context) ([]MCPServerRecord, error)
	ListEnabled(ctx context.Context) ([]MCPServerRecord, error)
	Update(ctx context.Context, server MCPServerRecord) (MCPServerRecord, error)
	Delete(ctx context.Context, id string) error
}

// MemoryMCPServerStore 是并发安全的内存实现，用于单元测试。
type MemoryMCPServerStore struct {
	mu      sync.Mutex
	servers map[string]MCPServerRecord
	clock   func() time.Time
}

// NewMemoryMCPServerStore 创建一个内存 MCP 服务器 store。
func NewMemoryMCPServerStore() *MemoryMCPServerStore {
	return &MemoryMCPServerStore{
		servers: map[string]MCPServerRecord{},
		clock:   time.Now,
	}
}

// WithClock 注入自定义时钟，用于测试。
func (s *MemoryMCPServerStore) WithClock(clock func() time.Time) *MemoryMCPServerStore {
	s.clock = clock
	return s
}

func (s *MemoryMCPServerStore) Create(ctx context.Context, server MCPServerRecord) (MCPServerRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 名称唯一性检查
	for _, existing := range s.servers {
		if existing.Name == server.Name {
			return MCPServerRecord{}, ErrConflict
		}
	}
	now := s.clock()
	server.CreatedAt = now
	server.UpdatedAt = now
	s.servers[server.ID] = server
	return cloneMCPServer(server), nil
}

func (s *MemoryMCPServerStore) Get(ctx context.Context, id string) (MCPServerRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	server, ok := s.servers[id]
	if !ok {
		return MCPServerRecord{}, ErrNotFound
	}
	return cloneMCPServer(server), nil
}

func (s *MemoryMCPServerStore) List(ctx context.Context) ([]MCPServerRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]MCPServerRecord, 0, len(s.servers))
	for _, server := range s.servers {
		result = append(result, cloneMCPServer(server))
	}
	return result, nil
}

func (s *MemoryMCPServerStore) ListEnabled(ctx context.Context) ([]MCPServerRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]MCPServerRecord, 0)
	for _, server := range s.servers {
		if server.Enabled {
			result = append(result, cloneMCPServer(server))
		}
	}
	return result, nil
}

func (s *MemoryMCPServerStore) Update(ctx context.Context, server MCPServerRecord) (MCPServerRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.servers[server.ID]
	if !ok {
		return MCPServerRecord{}, ErrNotFound
	}
	// 名称唯一性检查（排除自身）
	for id, other := range s.servers {
		if id != server.ID && other.Name == server.Name {
			return MCPServerRecord{}, ErrConflict
		}
	}
	server.CreatedAt = existing.CreatedAt
	server.UpdatedAt = s.clock()
	s.servers[server.ID] = server
	return cloneMCPServer(server), nil
}

func (s *MemoryMCPServerStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.servers[id]; !ok {
		return ErrNotFound
	}
	delete(s.servers, id)
	return nil
}

func cloneMCPServer(server MCPServerRecord) MCPServerRecord {
	out := server
	if server.Args != nil {
		out.Args = append([]string(nil), server.Args...)
	}
	if server.Env != nil {
		out.Env = make(map[string]string, len(server.Env))
		for k, v := range server.Env {
			out.Env[k] = v
		}
	}
	return out
}
