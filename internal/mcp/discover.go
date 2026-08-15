package mcp

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// MCPTool 描述 MCP 服务器通过 tools/list 暴露的一个工具。
// 字段对齐 MCP 协议的 Tool 定义。
type MCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema MCPInputSchema `json:"inputSchema"`
}

// MCPInputSchema 是 MCP 工具的输入参数 schema（JSON Schema 子集）。
type MCPInputSchema struct {
	Type       string                       `json:"type"` // 通常是 "object"
	Properties map[string]MCPPropertySchema `json:"properties"`
	Required   []string                     `json:"required"`
}

// MCPPropertySchema 描述 MCP 工具单个输入参数的类型。
type MCPPropertySchema struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// ToolLister 连接 MCP 服务器并列出其工具。抽象接口便于单元测试
// （fakeLister）和未来多种传输实现（stdio/SSE）。
type ToolLister interface {
	List(ctx context.Context, config MCPServerConfig) ([]MCPTool, error)
}

// supportedPropertyTypes 是 tools.DynamicInputField 允许的字段类型集合。
// tools.validateDynamicInputSchema 仅接受这些类型；不在集合内的字段会被丢弃。
var supportedPropertyTypes = map[string]bool{
	"string":  true,
	"integer": true,
	"number":  true,
	"boolean": true,
}

// Discover 连接所有配置的 MCP 服务器，把发现的工具转成
// tools.DynamicToolDefinition 列表，供 tools.RegisterDynamicTools 注册。
//
// 转换规则：
//   - 工具名格式为 <server_name>.<tool_name>，加前缀避免与内置工具冲突。
//   - Operation 默认 Read（MCP 协议不区分读写；写操作需在工具元数据层面
//     另行标记，当前不处理）。Risk 默认 Low。
//   - 输入 schema 按 supportedPropertyTypes 过滤，丢弃不支持类型的字段。
//   - 若 MCP 工具 schema 不含 environment 字段，自动注入 required string
//     environment，满足 tools.RegisterDynamicTools 的强制要求，并保持项目
//    多环境隔离的一致性。
//
// 部分成功语义：单台服务器连接失败（或某工具转换失败）时跳过该服务器并
// 记录日志，其余健康服务器照常注册——一台坏服务器不拖垮全部工具（修复前
// 全有或全无，坏一台导致所有 MCP 工具不可用且静默）。仅当所有服务器都失败
// 时才返回聚合错误。
func Discover(ctx context.Context, lister ToolLister, configs []MCPServerConfig) ([]tools.DynamicToolDefinition, error) {
	if lister == nil {
		return nil, fmt.Errorf("mcp discover: lister is nil")
	}
	var defs []tools.DynamicToolDefinition
	var failures []string
	for _, config := range configs {
		mcpTools, err := lister.List(ctx, config)
		if err != nil {
			failures = append(failures, fmt.Sprintf("server %q: %v", config.Name, err))
			continue
		}
		for _, mt := range mcpTools {
			def, err := convertMCPTool(config.Name, mt)
			if err != nil {
				log.Printf("mcp discover: skip tool %q.%q: %v", config.Name, mt.Name, err)
				continue
			}
			defs = append(defs, def)
		}
	}
	if len(defs) == 0 && len(failures) > 0 {
		return nil, fmt.Errorf("mcp discover: all servers failed: %s", strings.Join(failures, "; "))
	}
	if len(failures) > 0 {
		log.Printf("mcp discover: %d server(s) skipped, %d tool(s) loaded; skipped: %s", len(failures), len(defs), strings.Join(failures, "; "))
	}
	return defs, nil
}

// convertMCPTool 把单个 MCPTool 转成 tools.DynamicToolDefinition。
func convertMCPTool(serverName string, mt MCPTool) (tools.DynamicToolDefinition, error) {
	toolName := strings.TrimSpace(mt.Name)
	if toolName == "" {
		return tools.DynamicToolDefinition{}, fmt.Errorf("tool name is empty")
	}
	fullName := serverName + "." + toolName

	schema := buildInputSchema(mt.InputSchema)

	// tools.RegisterDynamicTools 强制要求 environment 为 required string。
	// 若 MCP 工具自带 environment 且满足要求则保留；否则注入。
	env, hasEnv := schema["environment"]
	if !hasEnv || env.Type != "string" || !env.Required {
		schema["environment"] = tools.DynamicInputField{Type: "string", Required: true}
	}

	return tools.DynamicToolDefinition{
		Tool: tools.Tool{
			Name:         fullName,
			Operation:    tools.Read,
			Risk:         tools.Low,
			Domain:       serverName,
			ResourceType: "mcp",
		},
		InputSchema: schema,
	}, nil
}

// buildInputSchema 把 MCPInputSchema.Properties 转成 tools 的 InputSchema map，
// 丢弃不支持类型的字段。required 标记来自 MCPInputSchema.Required 列表。
func buildInputSchema(schema MCPInputSchema) map[string]tools.DynamicInputField {
	requiredSet := make(map[string]bool, len(schema.Required))
	for _, r := range schema.Required {
		requiredSet[r] = true
	}
	out := make(map[string]tools.DynamicInputField, len(schema.Properties))
	for name, prop := range schema.Properties {
		if !supportedPropertyTypes[prop.Type] {
			continue
		}
		out[name] = tools.DynamicInputField{
			Type:     prop.Type,
			Required: requiredSet[name],
		}
	}
	return out
}
