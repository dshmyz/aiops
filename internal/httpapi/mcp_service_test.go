package httpapi

import (
	"context"
	"errors"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// fakeReloader 是可控的 MCPReloader，记录调用并可选返回错误。
type fakeReloader struct {
	called int
	err    error
}

func (f *fakeReloader) Reload(_ context.Context) error {
	f.called++
	return f.err
}

func TestMCPServerServiceCRUDDelegatesToStore(t *testing.T) {
	srvStore := store.NewMemoryMCPServerStore()
	service := NewMCPServerService(srvStore, nil)

	// Create
	created, err := service.CreateServer(context.Background(), store.MCPServerRecord{
		ID: "srv-1", Name: "grafana", Command: "mcp-grafana", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	if created.ID != "srv-1" {
		t.Fatalf("created ID = %q, want srv-1", created.ID)
	}

	// Get
	got, err := service.GetServer(context.Background(), "srv-1")
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if got.Name != "grafana" {
		t.Fatalf("got Name = %q, want grafana", got.Name)
	}

	// List
	list, err := service.ListServers(context.Background())
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}

	// Update
	updated, err := service.UpdateServer(context.Background(), store.MCPServerRecord{
		ID: "srv-1", Name: "grafana-2", Command: "mcp-grafana", Enabled: false,
	})
	if err != nil {
		t.Fatalf("UpdateServer: %v", err)
	}
	if updated.Name != "grafana-2" {
		t.Fatalf("updated Name = %q, want grafana-2", updated.Name)
	}

	// Delete
	if err := service.DeleteServer(context.Background(), "srv-1"); err != nil {
		t.Fatalf("DeleteServer: %v", err)
	}
	if _, err := service.GetServer(context.Background(), "srv-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetServer after delete err = %v, want ErrNotFound", err)
	}
}

func TestMCPServerServiceReloadDelegatesToReloader(t *testing.T) {
	srvStore := store.NewMemoryMCPServerStore()
	reloader := &fakeReloader{}
	service := NewMCPServerService(srvStore, reloader)

	if err := service.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if reloader.called != 1 {
		t.Fatalf("reloader.called = %d, want 1", reloader.called)
	}
}

func TestMCPServerServiceReloadErrorPropagates(t *testing.T) {
	srvStore := store.NewMemoryMCPServerStore()
	reloader := &fakeReloader{err: errors.New("boom")}
	service := NewMCPServerService(srvStore, reloader)

	if err := service.Reload(context.Background()); err == nil {
		t.Fatal("Reload should propagate reloader error")
	}
}

func TestMCPServerServiceReloadWithoutReloaderReturnsError(t *testing.T) {
	srvStore := store.NewMemoryMCPServerStore()
	service := NewMCPServerService(srvStore, nil)

	if err := service.Reload(context.Background()); err == nil {
		t.Fatal("Reload should error when reloader is nil")
	}
}

func TestMCPServerServiceCreatePropagatesConflict(t *testing.T) {
	srvStore := store.NewMemoryMCPServerStore()
	service := NewMCPServerService(srvStore, nil)

	record := store.MCPServerRecord{ID: "srv-1", Name: "grafana", Command: "mcp-grafana"}
	if _, err := service.CreateServer(context.Background(), record); err != nil {
		t.Fatalf("first CreateServer: %v", err)
	}
	// 同名冲突
	_, err := service.CreateServer(context.Background(), store.MCPServerRecord{
		ID: "srv-2", Name: "grafana", Command: "mcp-grafana",
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("conflict err = %v, want ErrConflict", err)
	}
}

func TestNewMCPServerServicePanicsOnNilStore(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when store is nil")
		}
	}()
	NewMCPServerService(nil, nil)
}
