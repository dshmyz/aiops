package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// stdioLister 通过 stdio 与 MCP 服务器子进程通信，实现 ToolLister。
// 它执行 initialize 握手后调用 tools/list 发现工具。
//
// 消息格式为 newline-delimited JSON-RPC 2.0（MCP stdio 传输规范）。
// 仅支持 stdio 模式（config.Command）；URL 模式（SSE/HTTP）未实现，
// 调用时返回错误。
type stdioLister struct {
	// handshakeTimeout 是 initialize 握手的超时。零值用默认 10s。
	handshakeTimeout time.Duration
	// listTimeout 是 tools/list 调用的超时。零值用默认 10s。
	listTimeout time.Duration
}

// NewStdioLister 创建一个基于 stdio 的 ToolLister。
func NewStdioLister() ToolLister {
	return &stdioLister{}
}

// jsonrpcRequest 是 JSON-RPC 2.0 请求/通知的通用结构。
type jsonrpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// jsonrpcResponse 是 JSON-RPC 2.0 响应结构。
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// toolsListResult 是 tools/list 方法的结果。
type toolsListResult struct {
	Tools []MCPTool `json:"tools"`
}

func (l *stdioLister) List(ctx context.Context, config MCPServerConfig) ([]MCPTool, error) {
	if strings.TrimSpace(config.Command) == "" {
		return nil, fmt.Errorf("stdio lister requires command, config %q has none", config.Name)
	}
	handshakeTimeout := l.handshakeTimeout
	if handshakeTimeout == 0 {
		handshakeTimeout = 10 * time.Second
	}
	listTimeout := l.listTimeout
	if listTimeout == 0 {
		listTimeout = 10 * time.Second
	}

	cmd := exec.CommandContext(ctx, config.Command, config.Args...)
	// 注入环境变量（在当前进程环境之上叠加）
	if len(config.Env) > 0 {
		cmd.Env = append(cmd.Environ(), envSlice(config.Env)...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start MCP server %q: %w", config.Name, err)
	}
	// 确保子进程在函数退出时被清理。
	defer func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	}()

	writer := json.NewEncoder(stdin)
	scanner := bufio.NewScanner(stdout)
	// MCP 工具 schema 可能较大，放宽 scanner buffer。
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// 1. initialize 握手
	initCtx, initCancel := context.WithTimeout(ctx, handshakeTimeout)
	defer initCancel()
	if err := send(writer, jsonrpcRequest{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "copilot-api", "version": "1.0"},
	}}); err != nil {
		return nil, fmt.Errorf("send initialize: %w", err)
	}
	if _, err := readResponse(initCtx, scanner, 1); err != nil {
		return nil, fmt.Errorf("read initialize response: %w", err)
	}
	// 2. initialized 通知（无 id，无需响应）
	if err := send(writer, jsonrpcRequest{JSONRPC: "2.0", Method: "notifications/initialized"}); err != nil {
		return nil, fmt.Errorf("send initialized notification: %w", err)
	}

	// 3. tools/list
	listCtx, listCancel := context.WithTimeout(ctx, listTimeout)
	defer listCancel()
	if err := send(writer, jsonrpcRequest{JSONRPC: "2.0", ID: 2, Method: "tools/list"}); err != nil {
		return nil, fmt.Errorf("send tools/list: %w", err)
	}
	resp, err := readResponse(listCtx, scanner, 2)
	if err != nil {
		return nil, fmt.Errorf("read tools/list response: %w", err)
	}
	var result toolsListResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("decode tools/list result: %w", err)
	}
	return result.Tools, nil
}

func send(w *json.Encoder, req jsonrpcRequest) error {
	return w.Encode(req)
}

// readResponse 读取 JSON-RPC 响应，跳过通知（无 id 的消息），
// 直到找到匹配 wantID 的响应或上下文超时。
func readResponse(ctx context.Context, scanner *bufio.Scanner, wantID int) (json.RawMessage, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return nil, fmt.Errorf("read stdout: %w", err)
			}
			return nil, io.ErrUnexpectedEOF
		}
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var resp jsonrpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			// 跳过无法解析的行（可能是服务器日志输出到 stdout）
			continue
		}
		// 跳过通知（id 为零值且无 result/error）
		if resp.ID == 0 && resp.Result == nil && resp.Error == nil {
			continue
		}
		if resp.ID != wantID {
			// 不是预期的响应，跳过（可能是异步通知）
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func envSlice(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
