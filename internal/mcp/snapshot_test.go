package mcp_test

import (
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/mcp"
)

// --- ToolSnapshot 变更检测测试 ---

func TestDiffToolChangesDetectsAddedTools(t *testing.T) {
	t.Parallel()
	old := mcp.ToolSnapshot{
		"grafana": {"grafana.query_metrics"},
	}
	current := mcp.ToolSnapshot{
		"grafana": {"grafana.query_metrics", "grafana.query_logs"},
	}

	changes := mcp.DiffToolChanges(old, current)

	if len(changes.Added) != 1 {
		t.Fatalf("Added len = %d, want 1", len(changes.Added))
	}
	if changes.Added[0] != "grafana.query_logs" {
		t.Errorf("Added[0] = %q, want grafana.query_logs", changes.Added[0])
	}
	if len(changes.Removed) != 0 {
		t.Errorf("Removed len = %d, want 0", len(changes.Removed))
	}
}

func TestDiffToolChangesDetectsRemovedTools(t *testing.T) {
	t.Parallel()
	old := mcp.ToolSnapshot{
		"grafana": {"grafana.query_metrics", "grafana.query_logs"},
	}
	current := mcp.ToolSnapshot{
		"grafana": {"grafana.query_metrics"},
	}

	changes := mcp.DiffToolChanges(old, current)

	if len(changes.Removed) != 1 {
		t.Fatalf("Removed len = %d, want 1", len(changes.Removed))
	}
	if changes.Removed[0] != "grafana.query_logs" {
		t.Errorf("Removed[0] = %q, want grafana.query_logs", changes.Removed[0])
	}
	if len(changes.Added) != 0 {
		t.Errorf("Added len = %d, want 0", len(changes.Added))
	}
}

func TestDiffToolChangesDetectsAddedServer(t *testing.T) {
	t.Parallel()
	old := mcp.ToolSnapshot{
		"grafana": {"grafana.query"},
	}
	current := mcp.ToolSnapshot{
		"grafana": {"grafana.query"},
		"loki":    {"loki.search"},
	}

	changes := mcp.DiffToolChanges(old, current)

	if len(changes.Added) != 1 {
		t.Fatalf("Added len = %d, want 1 (new server's tool)", len(changes.Added))
	}
	if changes.Added[0] != "loki.search" {
		t.Errorf("Added[0] = %q, want loki.search", changes.Added[0])
	}
	if len(changes.AddedServers) != 1 || changes.AddedServers[0] != "loki" {
		t.Errorf("AddedServers = %v, want [loki]", changes.AddedServers)
	}
}

func TestDiffToolChangesDetectsRemovedServer(t *testing.T) {
	t.Parallel()
	old := mcp.ToolSnapshot{
		"grafana": {"grafana.query"},
		"loki":    {"loki.search"},
	}
	current := mcp.ToolSnapshot{
		"grafana": {"grafana.query"},
	}

	changes := mcp.DiffToolChanges(old, current)

	if len(changes.Removed) != 1 {
		t.Fatalf("Removed len = %d, want 1", len(changes.Removed))
	}
	if changes.Removed[0] != "loki.search" {
		t.Errorf("Removed[0] = %q, want loki.search", changes.Removed[0])
	}
	if len(changes.RemovedServers) != 1 || changes.RemovedServers[0] != "loki" {
		t.Errorf("RemovedServers = %v, want [loki]", changes.RemovedServers)
	}
}

func TestDiffToolChangesNoChangeReturnsEmpty(t *testing.T) {
	t.Parallel()
	snapshot := mcp.ToolSnapshot{
		"grafana": {"grafana.query_metrics", "grafana.query_logs"},
		"loki":    {"loki.search"},
	}

	changes := mcp.DiffToolChanges(snapshot, snapshot)

	if len(changes.Added) != 0 || len(changes.Removed) != 0 {
		t.Errorf("Added=%v Removed=%v, want both empty", changes.Added, changes.Removed)
	}
	if len(changes.AddedServers) != 0 || len(changes.RemovedServers) != 0 {
		t.Errorf("server changes should be empty")
	}
	if changes.HasChanges() {
		t.Error("HasChanges() = true, want false for identical snapshots")
	}
}

func TestDiffToolChangesHasChangesTrueWhenToolsAdded(t *testing.T) {
	t.Parallel()
	old := mcp.ToolSnapshot{"grafana": {"grafana.query"}}
	current := mcp.ToolSnapshot{"grafana": {"grafana.query", "grafana.alerts"}}

	changes := mcp.DiffToolChanges(old, current)

	if !changes.HasChanges() {
		t.Error("HasChanges() = false, want true when tools added")
	}
}

// --- BuildSnapshot 测试 ---

func TestBuildSnapshotFromConfigsAndTools(t *testing.T) {
	t.Parallel()
	configs := []mcp.MCPServerConfig{
		{Name: "grafana", Command: "mcp-grafana"},
	}
	discovered := map[string][]mcp.MCPTool{
		"grafana": {
			{Name: "query_metrics"},
			{Name: "query_logs"},
		},
	}

	snapshot := mcp.BuildSnapshot(configs, discovered)

	if len(snapshot) != 1 {
		t.Fatalf("snapshot len = %d, want 1", len(snapshot))
	}
	tools, ok := snapshot["grafana"]
	if !ok {
		t.Fatal("snapshot missing grafana")
	}
	if len(tools) != 2 {
		t.Fatalf("grafana tools len = %d, want 2", len(tools))
	}
	// 工具名应带服务器前缀
	wantNames := map[string]bool{"grafana.query_metrics": false, "grafana.query_logs": false}
	for _, name := range tools {
		wantNames[name] = true
	}
	for name, found := range wantNames {
		if !found {
			t.Errorf("snapshot missing tool %q", name)
		}
	}
}
