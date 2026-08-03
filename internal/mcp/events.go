package mcp

// EventType 枚举 MCP 健康检查和工具变更事件类型（借鉴-6）。
// mcp 包不直接依赖 audit 包，通过 EventEmitter 回调把事件交给调用方
// （main.go）转成 audit.Event 记录，保持包间无依赖。
type EventType string

const (
	// EventTypeHealthUnhealthy 表示服务器连接失败（进程无法启动/握手失败/超时）。
	EventTypeHealthUnhealthy EventType = "mcp_health_unhealthy"
	// EventTypeHealthDegraded 表示服务器连接成功但未暴露任何工具。
	EventTypeHealthDegraded EventType = "mcp_health_degraded"
	// EventTypeToolsChanged 表示工具列表发生增删变更。
	EventTypeToolsChanged EventType = "mcp_tools_changed"
)

// MCPEvent 是 mcp 包向外发射的事件，由调用方转成审计事件。
type MCPEvent struct {
	Type       EventType
	ServerName string
	Message    string
	Metadata   map[string]any
}

// EmitFunc 是事件发射回调的类型。调用方（main.go）实现此回调，
// 把 MCPEvent 转成 audit.Event 记录。
type EmitFunc func(event MCPEvent)

// EmitToolChangesEvent 对比新旧快照，若有变更则通过 emit 发射 tools_changed 事件。
// Metadata 包含 added/removed 工具列表和 added_servers/removed_servers 服务器列表，
// 供审计记录完整变更详情。
func EmitToolChangesEvent(emit EmitFunc, old, current ToolSnapshot) {
	if emit == nil {
		return
	}
	changes := DiffToolChanges(old, current)
	if !changes.HasChanges() {
		return
	}
	emit(MCPEvent{
		Type:       EventTypeToolsChanged,
		ServerName: "", // 全局变更事件，不绑定单个服务器
		Message:    "MCP tools changed",
		Metadata: map[string]any{
			"added":           changes.Added,
			"removed":         changes.Removed,
			"added_servers":   changes.AddedServers,
			"removed_servers": changes.RemovedServers,
		},
	})
}
