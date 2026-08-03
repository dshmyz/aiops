package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

func newMemoryMCPServerStore() *store.MemoryMCPServerStore {
	return store.NewMemoryMCPServerStore()
}

func TestMCPServerStoreCreate(t *testing.T) {
	t.Parallel()
	s := newMemoryMCPServerStore()
	server := store.MCPServerRecord{
		ID:      "srv-1",
		Name:    "grafana",
		Command: "mcp-server-grafana",
		Args:    []string{"--port=3000"},
		Env:     map[string]string{"API_KEY": "secret"},
		Enabled: true,
	}

	created, err := s.Create(context.Background(), server)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID != "srv-1" {
		t.Errorf("ID = %q", created.ID)
	}
	if created.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set by store")
	}
	if created.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be set by store")
	}
}

func TestMCPServerStoreCreateDuplicateName(t *testing.T) {
	t.Parallel()
	s := newMemoryMCPServerStore()
	server := store.MCPServerRecord{ID: "srv-1", Name: "grafana", Command: "mcp-server"}
	if _, err := s.Create(context.Background(), server); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	// 同名不同 ID
	dup := store.MCPServerRecord{ID: "srv-2", Name: "grafana", Command: "mcp-other"}
	_, err := s.Create(context.Background(), dup)
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("Create duplicate name: error = %v, want ErrConflict", err)
	}
}

func TestMCPServerStoreGet(t *testing.T) {
	t.Parallel()
	s := newMemoryMCPServerStore()
	server := store.MCPServerRecord{ID: "srv-1", Name: "grafana", Command: "mcp-server"}
	if _, err := s.Create(context.Background(), server); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.Get(context.Background(), "srv-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "grafana" {
		t.Errorf("Name = %q", got.Name)
	}
}

func TestMCPServerStoreGetNotFound(t *testing.T) {
	t.Parallel()
	s := newMemoryMCPServerStore()
	_, err := s.Get(context.Background(), "nonexistent")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get nonexistent: error = %v, want ErrNotFound", err)
	}
}

func TestMCPServerStoreList(t *testing.T) {
	t.Parallel()
	s := newMemoryMCPServerStore()
	if _, err := s.Create(context.Background(), store.MCPServerRecord{ID: "srv-1", Name: "grafana", Command: "mcp-grafana", Enabled: true}); err != nil {
		t.Fatalf("Create 1: %v", err)
	}
	if _, err := s.Create(context.Background(), store.MCPServerRecord{ID: "srv-2", Name: "loki", Command: "mcp-loki", Enabled: false}); err != nil {
		t.Fatalf("Create 2: %v", err)
	}

	servers, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(servers) != 2 {
		t.Fatalf("List len = %d, want 2", len(servers))
	}
}

func TestMCPServerStoreListEnabledOnly(t *testing.T) {
	t.Parallel()
	s := newMemoryMCPServerStore()
	if _, err := s.Create(context.Background(), store.MCPServerRecord{ID: "srv-1", Name: "grafana", Command: "mcp-grafana", Enabled: true}); err != nil {
		t.Fatalf("Create 1: %v", err)
	}
	if _, err := s.Create(context.Background(), store.MCPServerRecord{ID: "srv-2", Name: "loki", Command: "mcp-loki", Enabled: false}); err != nil {
		t.Fatalf("Create 2: %v", err)
	}

	servers, err := s.ListEnabled(context.Background())
	if err != nil {
		t.Fatalf("ListEnabled: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("ListEnabled len = %d, want 1", len(servers))
	}
	if servers[0].Name != "grafana" {
		t.Errorf("Name = %q, want grafana", servers[0].Name)
	}
}

func TestMCPServerStoreUpdate(t *testing.T) {
	t.Parallel()
	s := newMemoryMCPServerStore()
	created, err := s.Create(context.Background(), store.MCPServerRecord{ID: "srv-1", Name: "grafana", Command: "mcp-server", Enabled: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	originalUpdatedAt := created.UpdatedAt

	// 等待确保时间戳不同
	time.Sleep(10 * time.Millisecond)

	updated, err := s.Update(context.Background(), store.MCPServerRecord{ID: "srv-1", Name: "grafana", Command: "mcp-server-v2", Enabled: false})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Command != "mcp-server-v2" {
		t.Errorf("Command = %q, want mcp-server-v2", updated.Command)
	}
	if updated.Enabled != false {
		t.Errorf("Enabled = %v, want false", updated.Enabled)
	}
	if !updated.UpdatedAt.After(originalUpdatedAt) {
		t.Error("UpdatedAt should advance after Update")
	}
}

func TestMCPServerStoreUpdateNotFound(t *testing.T) {
	t.Parallel()
	s := newMemoryMCPServerStore()
	_, err := s.Update(context.Background(), store.MCPServerRecord{ID: "nonexistent", Name: "grafana", Command: "mcp-server"})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Update nonexistent: error = %v, want ErrNotFound", err)
	}
}

func TestMCPServerStoreDelete(t *testing.T) {
	t.Parallel()
	s := newMemoryMCPServerStore()
	if _, err := s.Create(context.Background(), store.MCPServerRecord{ID: "srv-1", Name: "grafana", Command: "mcp-server"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.Delete(context.Background(), "srv-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := s.Get(context.Background(), "srv-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get after Delete: error = %v, want ErrNotFound", err)
	}
}

func TestMCPServerStoreDeleteNotFound(t *testing.T) {
	t.Parallel()
	s := newMemoryMCPServerStore()
	err := s.Delete(context.Background(), "nonexistent")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Delete nonexistent: error = %v, want ErrNotFound", err)
	}
}
