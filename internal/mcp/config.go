// Package mcp 实现外部 MCP (Model Context Protocol) 服务器接入层（缺口-2）。
//
// 它在启动时加载 MCP 服务器配置，连接服务器发现其工具，并把工具转换成
// internal/tools.DynamicToolDefinition 注册到统一工具表，使 orchestrator
// 和诊断链路能像调用内置工具一样调用外部 MCP 工具。
//
// 当前实现聚焦配置加载与工具转换逻辑；真实传输（stdio/SSE）由 ToolLister
// 接口的具体实现承担，便于单元测试和未来扩展。
package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// MCPServerConfig 描述一个外部 MCP 服务器的连接配置。
// Command（stdio 模式）与 URL（SSE/HTTP 模式）二选一。
type MCPServerConfig struct {
	// Name 是服务器名称，用作发现工具的命名前缀（<name>.<tool>），
	// 避免与内置工具名冲突。不能为空，不能含点号。
	Name string `json:"name"`
	// Command 是 stdio 模式下要执行的可执行命令。与 URL 互斥。
	Command string `json:"command"`
	// Args 是 stdio 模式下传给 Command 的参数。
	Args []string `json:"args"`
	// Env 是 stdio 模式下传给 Command 的环境变量。
	Env map[string]string `json:"env"`
	// URL 是 SSE/HTTP 模式下 MCP 服务器的端点。与 Command 互斥。
	URL string `json:"url"`
}

// LoadConfigs 从 JSON 字符串解析 MCP 服务器配置列表。
// 空字符串返回空切片（不报错），便于无配置场景静默跳过。
// 每条配置必须满足：Name 非空且不含点号；Command 或 URL 至少一个非空。
func LoadConfigs(raw string) ([]MCPServerConfig, error) {
	if strings.TrimSpace(raw) == "" {
		return []MCPServerConfig{}, nil
	}
	var configs []MCPServerConfig
	if err := json.Unmarshal([]byte(raw), &configs); err != nil {
		return nil, fmt.Errorf("parse MCP server configs: %w", err)
	}
	for i, c := range configs {
		if err := c.validate(); err != nil {
			return nil, fmt.Errorf("MCP server config[%d]: %w", i, err)
		}
	}
	return configs, nil
}

func (c MCPServerConfig) validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("name is required")
	}
	if strings.Contains(c.Name, ".") {
		return fmt.Errorf("name %q must not contain '.' (used as tool-name separator)", c.Name)
	}
	if strings.TrimSpace(c.Command) == "" && strings.TrimSpace(c.URL) == "" {
		return errors.New("either command or url is required")
	}
	return nil
}
