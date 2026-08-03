package mcp

import "sort"

// ToolSnapshot 记录某时刻所有外部 MCP 服务器暴露的工具集合（借鉴-6）。
// key 是服务器名，value 是该服务器的工具全名列表（<server>.<tool>）。
// 通过对比新旧快照可检测工具列表变更（新增/移除工具或服务器）。
type ToolSnapshot map[string][]string

// ToolChanges 描述两次快照之间的工具变更。
type ToolChanges struct {
	// Added 是新增的工具全名列表。
	Added []string
	// Removed 是移除的工具全名列表。
	Removed []string
	// AddedServers 是新出现的服务器名列表。
	AddedServers []string
	// RemovedServers 是消失的服务器名列表。
	RemovedServers []string
}

// HasChanges 报告是否有任何工具或服务器变更。
func (c ToolChanges) HasChanges() bool {
	return len(c.Added) > 0 || len(c.Removed) > 0 ||
		len(c.AddedServers) > 0 || len(c.RemovedServers) > 0
}

// BuildSnapshot 从配置和发现的工具列表构造快照。
// discovered 的 key 是服务器名，value 是该服务器的 MCPTool 列表；
// 构造时把工具名转成全名（<server>.<tool>），与 Discover 的命名规则一致。
func BuildSnapshot(configs []MCPServerConfig, discovered map[string][]MCPTool) ToolSnapshot {
	snapshot := make(ToolSnapshot, len(configs))
	for _, config := range configs {
		tools := discovered[config.Name]
		names := make([]string, 0, len(tools))
		for _, tool := range tools {
			names = append(names, config.Name+"."+tool.Name)
		}
		sort.Strings(names)
		snapshot[config.Name] = names
	}
	return snapshot
}

// DiffToolChanges 对比新旧快照，返回工具和服务器的变更列表。
// 算法：
//   - 服务器级：old 有 current 无 → RemovedServers；current 有 old 无 → AddedServers
//   - 工具级：同一服务器内 old 有 current 无 → Removed；current 有 old 无 → Added
//   - 新增服务器的所有工具计入 Added；移除服务器的所有工具计入 Removed
func DiffToolChanges(old, current ToolSnapshot) ToolChanges {
	changes := ToolChanges{
		Added:          []string{},
		Removed:        []string{},
		AddedServers:   []string{},
		RemovedServers: []string{},
	}

	oldSet := make(map[string]struct{}, len(old))
	for name := range old {
		oldSet[name] = struct{}{}
	}
	currentSet := make(map[string]struct{}, len(current))
	for name := range current {
		currentSet[name] = struct{}{}
	}

	// 新增服务器（current 有 old 无）：其全部工具计入 Added
	for name := range currentSet {
		if _, exists := oldSet[name]; !exists {
			changes.AddedServers = append(changes.AddedServers, name)
			changes.Added = append(changes.Added, current[name]...)
		}
	}
	// 移除服务器（old 有 current 无）：其全部工具计入 Removed
	for name := range oldSet {
		if _, exists := currentSet[name]; !exists {
			changes.RemovedServers = append(changes.RemovedServers, name)
			changes.Removed = append(changes.Removed, old[name]...)
		}
	}
	// 共存服务器：对比工具集合
	for name := range currentSet {
		if _, exists := oldSet[name]; !exists {
			continue // 已在 AddedServers 处理
		}
		changes.Added = append(changes.Added, diffAdded(old[name], current[name])...)
		changes.Removed = append(changes.Removed, diffRemoved(old[name], current[name])...)
	}

	sort.Strings(changes.Added)
	sort.Strings(changes.Removed)
	sort.Strings(changes.AddedServers)
	sort.Strings(changes.RemovedServers)
	return changes
}

// diffAdded 返回 current 中存在但 old 中不存在的工具名。
func diffAdded(old, current []string) []string {
	oldSet := make(map[string]struct{}, len(old))
	for _, name := range old {
		oldSet[name] = struct{}{}
	}
	var added []string
	for _, name := range current {
		if _, exists := oldSet[name]; !exists {
			added = append(added, name)
		}
	}
	return added
}

// diffRemoved 返回 old 中存在但 current 中不存在的工具名。
func diffRemoved(old, current []string) []string {
	currentSet := make(map[string]struct{}, len(current))
	for _, name := range current {
		currentSet[name] = struct{}{}
	}
	var removed []string
	for _, name := range old {
		if _, exists := currentSet[name]; !exists {
			removed = append(removed, name)
		}
	}
	return removed
}
