package mcp

import (
	"context"
	"fmt"
	"sync"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// Manager 管理外部 MCP 服务器的热配置生命周期。
// 它从 DB 加载启用的服务器配置，连接发现工具，注册到统一工具表；
// 配置变更后调用 Reload 增量注册新工具、注销已移除/禁用服务器的工具。
//
// Reload 流程：
//  1. 从 DB 加载所有启用的 MCPServerRecord
//  2. 对每个服务器调用 Discover 发现工具
//  3. 用 DiffToolChanges 对比新旧快照
//  4. 新增工具 → tools.RegisterDynamicTools
//  5. 移除工具 → tools.UnregisterDynamicTools
//  6. 更新内部快照
//  7. 通过 EventEmitter 回调发射 tools_changed / unhealthy 事件
//
// Manager 持有 sync.Mutex 保护 Reload 的并发安全，同时正在执行的工具
// 调用通过 tools.Lookup 的 RWMutex 保护，不受 Reload 影响。
type Manager struct {
	store  store.MCPServerStore
	lister ToolLister
	mu     sync.Mutex
	// snapshot 是当前已注册工具的快照，key 是服务器名。
	snapshot ToolSnapshot
	// emitter 是可选的事件回调，工具变更/健康异常时触发。
	emitter EmitFunc
}

// NewManager 创建一个 MCP 热配置管理器。store 和 lister 不能为 nil。
func NewManager(store store.MCPServerStore, lister ToolLister) *Manager {
	if store == nil {
		panic("mcp.NewManager: store is nil")
	}
	if lister == nil {
		panic("mcp.NewManager: lister is nil")
	}
	return &Manager{
		store:    store,
		lister:   lister,
		snapshot: ToolSnapshot{},
	}
}

// WithEventEmitter 注入事件回调，返回 Manager 自身便于链式调用。
func (m *Manager) WithEventEmitter(emit EmitFunc) *Manager {
	m.emitter = emit
	return m
}

// Reload 从 DB 重新加载配置，增量注册/注销工具，更新快照。
// 返回 nil 表示成功；DB 错误或 Discover 致命错误时返回 error。
// 单个服务器连接失败不阻塞其他服务器，只触发 unhealthy 事件。
func (m *Manager) Reload(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 1. 从 DB 加载启用的服务器
	records, err := m.store.ListEnabled(ctx)
	if err != nil {
		return fmt.Errorf("mcp reload: list enabled servers: %w", err)
	}

	// 2. 转成 MCPServerConfig 并 Discover 每个服务器的工具
	configs := make([]MCPServerConfig, 0, len(records))
	discovered := make(map[string][]MCPTool, len(records))
	for _, record := range records {
		config := recordToConfig(record)
		configs = append(configs, config)
		mcpTools, err := m.lister.List(ctx, config)
		if err != nil {
			// 连接失败：触发 unhealthy 事件，该服务器工具不纳入新快照
			m.emitEvent(MCPEvent{
				Type:       EventTypeHealthUnhealthy,
				ServerName: config.Name,
				Message:    fmt.Sprintf("reload: list tools failed: %s", err.Error()),
				Metadata:   map[string]any{"error": err.Error()},
			})
			continue
		}
		discovered[config.Name] = mcpTools
	}

	// 3. 构建新快照
	newSnapshot := BuildSnapshot(configs, discovered)

	// 4. Diff 新旧快照
	changes := DiffToolChanges(m.snapshot, newSnapshot)

	// 5. 注销已移除的工具
	if len(changes.Removed) > 0 {
		if err := tools.UnregisterDynamicTools(changes.Removed); err != nil {
			return fmt.Errorf("mcp reload: unregister tools: %w", err)
		}
	}

	// 6. 注册新增的工具
	if len(changes.Added) > 0 {
		defs, err := discoverForAdded(ctx, m.lister, configs, discovered, changes.Added)
		if err != nil {
			return fmt.Errorf("mcp reload: register tools: %w", err)
		}
		if err := tools.RegisterDynamicTools(defs); err != nil {
			return fmt.Errorf("mcp reload: register tools: %w", err)
		}
	}

	// 7. 发射 tools_changed 事件
	if changes.HasChanges() {
		m.emitEvent(MCPEvent{
			Type:       EventTypeToolsChanged,
			ServerName: "",
			Message:    "MCP tools changed during reload",
			Metadata: map[string]any{
				"added":           changes.Added,
				"removed":         changes.Removed,
				"added_servers":   changes.AddedServers,
				"removed_servers": changes.RemovedServers,
			},
		})
	}

	// 8. 更新快照
	m.snapshot = newSnapshot
	return nil
}

// discoverForAdded 把新增的工具转成 DynamicToolDefinition。
// 复用已 Discover 的结果，避免重复连接。
func discoverForAdded(_ context.Context, _ ToolLister, configs []MCPServerConfig, discovered map[string][]MCPTool, added []string) ([]tools.DynamicToolDefinition, error) {
	addedSet := make(map[string]bool, len(added))
	for _, name := range added {
		addedSet[name] = true
	}
	var defs []tools.DynamicToolDefinition
	for _, config := range configs {
		mcpTools := discovered[config.Name]
		for _, mt := range mcpTools {
			fullName := config.Name + "." + mt.Name
			if !addedSet[fullName] {
				continue
			}
			def, err := convertMCPTool(config.Name, mt)
			if err != nil {
				return nil, err
			}
			defs = append(defs, def)
		}
	}
	return defs, nil
}

// recordToConfig 把 store.MCPServerRecord 转成 mcp.MCPServerConfig。
func recordToConfig(record store.MCPServerRecord) MCPServerConfig {
	return MCPServerConfig{
		Name:    record.Name,
		Command: record.Command,
		Args:    record.Args,
		Env:     record.Env,
		URL:     record.URL,
	}
}

// emitEvent 是 nil-safe 的事件发射。
func (m *Manager) emitEvent(event MCPEvent) {
	if m.emitter != nil {
		m.emitter(event)
	}
}
