package mcp_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/mcp"
)

// --- 事件回调测试 ---

// recordingEmitter 记录所有收到的事件，用于断言。
type recordingEmitter struct {
	events []mcp.MCPEvent
}

func (r *recordingEmitter) Emit(event mcp.MCPEvent) {
	r.events = append(r.events, event)
}

func TestHealthCheckEmitsEventWhenUnhealthy(t *testing.T) {
	t.Parallel()
	config := mcp.MCPServerConfig{Name: "broken", Command: "mcp-broken"}
	lister := fakeLister{err: errors.New("connection refused")}
	emitter := &recordingEmitter{}
	checker := mcp.NewHealthChecker(lister).WithEventEmitter(emitter.Emit)

	checker.HealthCheck(context.Background(), config)

	if len(emitter.events) != 1 {
		t.Fatalf("events len = %d, want 1", len(emitter.events))
	}
	event := emitter.events[0]
	if event.Type != mcp.EventTypeHealthUnhealthy {
		t.Errorf("Type = %q, want %q", event.Type, mcp.EventTypeHealthUnhealthy)
	}
	if event.ServerName != "broken" {
		t.Errorf("ServerName = %q, want broken", event.ServerName)
	}
	if event.Message == "" {
		t.Error("Message should be non-empty for unhealthy event")
	}
}

func TestHealthCheckEmitsEventWhenDegraded(t *testing.T) {
	t.Parallel()
	config := mcp.MCPServerConfig{Name: "empty", Command: "mcp-empty"}
	lister := fakeLister{tools: []mcp.MCPTool{}}
	emitter := &recordingEmitter{}
	checker := mcp.NewHealthChecker(lister).WithEventEmitter(emitter.Emit)

	checker.HealthCheck(context.Background(), config)

	if len(emitter.events) != 1 {
		t.Fatalf("events len = %d, want 1", len(emitter.events))
	}
	if emitter.events[0].Type != mcp.EventTypeHealthDegraded {
		t.Errorf("Type = %q, want %q", emitter.events[0].Type, mcp.EventTypeHealthDegraded)
	}
}

func TestHealthCheckEmitsNoEventWhenHealthy(t *testing.T) {
	t.Parallel()
	config := mcp.MCPServerConfig{Name: "grafana", Command: "mcp-grafana"}
	lister := fakeLister{tools: []mcp.MCPTool{{Name: "query"}}}
	emitter := &recordingEmitter{}
	checker := mcp.NewHealthChecker(lister).WithEventEmitter(emitter.Emit)

	checker.HealthCheck(context.Background(), config)

	if len(emitter.events) != 0 {
		t.Fatalf("events len = %d, want 0 for healthy server", len(emitter.events))
	}
}

func TestHealthCheckAllEmitsEventsForEachUnhealthy(t *testing.T) {
	t.Parallel()
	configs := []mcp.MCPServerConfig{
		{Name: "grafana", Command: "mcp-grafana"},
		{Name: "broken", Command: "mcp-broken"},
		{Name: "empty", Command: "mcp-empty"},
	}
	lister := &perServerLister{
		byName: map[string]fakeLister{
			"grafana": {tools: []mcp.MCPTool{{Name: "query"}}},
			"broken":  {err: errors.New("refused")},
			"empty":   {tools: []mcp.MCPTool{}},
		},
	}
	emitter := &recordingEmitter{}
	checker := mcp.NewHealthChecker(lister).WithEventEmitter(emitter.Emit)

	checker.HealthCheckAll(context.Background(), configs)

	// 只有 broken 和 empty 应触发事件
	if len(emitter.events) != 2 {
		t.Fatalf("events len = %d, want 2 (broken + empty)", len(emitter.events))
	}
}

func TestEmitToolChangesFiresEventWhenToolsChange(t *testing.T) {
	t.Parallel()
	old := mcp.ToolSnapshot{"grafana": {"grafana.query"}}
	current := mcp.ToolSnapshot{"grafana": {"grafana.query", "grafana.alerts"}}
	emitter := &recordingEmitter{}

	mcp.EmitToolChangesEvent(emitter.Emit, old, current)

	if len(emitter.events) != 1 {
		t.Fatalf("events len = %d, want 1", len(emitter.events))
	}
	event := emitter.events[0]
	if event.Type != mcp.EventTypeToolsChanged {
		t.Errorf("Type = %q, want %q", event.Type, mcp.EventTypeToolsChanged)
	}
	if event.ServerName != "" {
		t.Errorf("ServerName = %q, want empty for global change event", event.ServerName)
	}
	// Metadata 应包含 added/removed 工具列表
	if event.Metadata == nil {
		t.Fatal("Metadata should not be nil for tools_changed event")
	}
	if _, ok := event.Metadata["added"]; !ok {
		t.Error("Metadata missing 'added' key")
	}
}

func TestEmitToolChangesNoEventWhenNoChange(t *testing.T) {
	t.Parallel()
	snapshot := mcp.ToolSnapshot{"grafana": {"grafana.query"}}
	emitter := &recordingEmitter{}

	mcp.EmitToolChangesEvent(emitter.Emit, snapshot, snapshot)

	if len(emitter.events) != 0 {
		t.Fatalf("events len = %d, want 0 for no change", len(emitter.events))
	}
}
