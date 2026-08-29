package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ToolCaller 通过 MCP 服务器执行一次工具调用。与 ToolLister 一样抽象成
// 接口，便于单元测试与未来多种传输实现（stdio/SSE）。
type ToolCaller interface {
	Call(ctx context.Context, config MCPServerConfig, toolName string, args map[string]any) (map[string]any, error)
}

// toolsCallResult 是 tools/call 方法的结果（MCP CallToolResult）。
type toolsCallResult struct {
	Content []toolsCallContent `json:"content"`
	IsError bool               `json:"isError"`
	// structuredContent 是 MCP 2025-06-06 规范新增的结构化结果；老服务器没有。
	StructuredContent map[string]any `json:"structuredContent,omitempty"`
}

type toolsCallContent struct {
	Type string `json:"type"` // "text" / "image" / ...
	Text string `json:"text"`
}

// parseToolsCallResult 把 tools/call 的 result 转成供 agent 消费的
// map[string]any。转换规则：
//   - isError=true → 返回 error（走执行失败披露路径，不伪装成功）
//   - structuredContent 存在 → 原样返回
//   - 拼接全部 text 内容；若整体是 JSON object → 解析后返回（下游
//     JSONPath/事实抽取才能工作）；否则包成 {"result": text}
func parseToolsCallResult(result json.RawMessage, server, tool string) (map[string]any, error) {
	var parsed toolsCallResult
	if err := json.Unmarshal(result, &parsed); err != nil {
		return nil, fmt.Errorf("mcp %s: decode tools/call result for %q: %w", server, tool, err)
	}
	var textParts []string
	for _, c := range parsed.Content {
		if c.Type == "text" {
			textParts = append(textParts, c.Text)
		}
	}
	text := strings.Join(textParts, "\n")
	if parsed.IsError {
		if text == "" {
			text = "unknown tool error"
		}
		return nil, fmt.Errorf("mcp %s: tool %q reported error: %s", server, tool, text)
	}
	if parsed.StructuredContent != nil {
		return parsed.StructuredContent, nil
	}
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") {
		var asJSON map[string]any
		if err := json.Unmarshal([]byte(trimmed), &asJSON); err == nil {
			return asJSON, nil
		}
	}
	return map[string]any{"result": text}, nil
}

// WithToolCaller 注入工具调用实现。不注入时，若构造传入的 lister 同时
// 实现了 ToolCaller（stdioLister 就是），自动复用；否则 Call 返回错误。
func (m *Manager) WithToolCaller(caller ToolCaller) *Manager {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.caller = caller
	return m
}

// OwnsTool 报告该工具名是否由某台已发现的 MCP 服务器拥有。用于执行链
// 路由：MCP 工具走真实 tools/call，其余工具透传给下层 runner。
func (m *Manager) OwnsTool(toolName string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.callable[toolName]
	return ok
}

// Call 执行一次 MCP 工具调用：按工具名前缀解析所属服务器，取其配置
// 委托给 caller。工具名格式为 <server_name>.<tool_name>（服务器名不含点，
// 见 MCPServerConfig.validate，因此按第一个点切分无歧义）。
func (m *Manager) Call(ctx context.Context, toolName string, args map[string]any) (map[string]any, error) {
	m.mu.Lock()
	config, ok := m.callable[toolName]
	caller := m.caller
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("mcp call: no discovered server owns tool %q", toolName)
	}
	if caller == nil {
		return nil, fmt.Errorf("mcp call: transport for server %q does not support tool calling", config.Name)
	}
	server, bare := splitServerTool(toolName)
	if server != config.Name {
		return nil, fmt.Errorf("mcp call: tool %q prefix %q does not match owning server %q", toolName, server, config.Name)
	}
	return caller.Call(ctx, config, bare, args)
}

// splitServerTool 按第一个点把 <server>.<tool> 切成两段。
func splitServerTool(fullName string) (server, tool string) {
	idx := strings.Index(fullName, ".")
	if idx < 0 {
		return fullName, ""
	}
	return fullName[:idx], fullName[idx+1:]
}

// rebuildCallables 用 Reload 的发现结果重建 工具名→服务器配置 映射。
// 只有发现成功（健康）的服务器纳入——注销/禁用服务器的工具不在注册表里，
// 这里保持同一口径。调用方需持有 m.mu。
func (m *Manager) rebuildCallables(configs []MCPServerConfig, discovered map[string][]MCPTool) {
	m.callable = make(map[string]MCPServerConfig)
	for _, config := range configs {
		if len(discovered[config.Name]) == 0 {
			continue
		}
		for _, mt := range discovered[config.Name] {
			m.callable[config.Name+"."+mt.Name] = config
		}
	}
}
