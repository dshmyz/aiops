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

// stdioLister 通过 stdio 与 MCP 服务器子进程通信，实现 ToolLister 与
// ToolCaller。它执行 initialize 握手后调用 tools/list 发现工具、tools/call
// 执行工具。
//
// 消息格式为 newline-delimited JSON-RPC 2.0（MCP stdio 传输规范）。
// 仅支持 stdio 模式（config.Command）；URL 模式（SSE/HTTP）未实现，
// 调用时返回错误。
//
// 每次调用（List/Call）都新起一个子进程完成握手后即退出——与 Discover/
// 健康检查的进程模型一致，实现简单且不积累僵尸连接；代价是每次调用多
// 一次握手开销（~百毫秒级），后续如成为瓶颈可演进为持久连接池。
type stdioLister struct {
	// handshakeTimeout 是 initialize 握手的超时。零值用默认 10s。
	handshakeTimeout time.Duration
	// listTimeout 是 tools/list 调用的超时。零值用默认 10s。
	listTimeout time.Duration
	// callTimeout 是单次 tools/call 的超时。零值用默认 30s（工具执行
	// 可能比发现类调用慢得多）。
	callTimeout time.Duration
}

// NewStdioLister 创建一个基于 stdio 的 ToolLister。
func NewStdioLister() ToolLister {
	return &stdioLister{}
}

// WithCallTimeout 设置单次 tools/call 的超时，返回自身便于链式调用。
func (l *stdioLister) WithCallTimeout(d time.Duration) *stdioLister {
	l.callTimeout = d
	return l
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
	Tools      []MCPTool `json:"tools"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

// mcpSession 是与 stdio MCP 服务器的一次已握手会话：initialize 完成、
// initialized 通知已发，writer/scanner 可直接承载后续请求。
type mcpSession struct {
	writer  *json.Encoder
	scanner *bufio.Scanner
	cmd     *exec.Cmd
	stdin   io.WriteCloser
}

// startSession 启动子进程并完成 initialize 握手。
func startSession(ctx context.Context, config MCPServerConfig, handshakeTimeout time.Duration) (*mcpSession, error) {
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
	// Close 会在调用方 defer 中执行；Start 失败路径由上面直接返回（无泄漏）。
	s := &mcpSession{
		writer:  json.NewEncoder(stdin),
		scanner: bufio.NewScanner(stdout),
		cmd:     cmd,
		stdin:   stdin,
	}
	// MCP 工具 schema 可能较大，放宽 scanner buffer。
	s.scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	initCtx, initCancel := context.WithTimeout(ctx, handshakeTimeout)
	defer initCancel()
	if err := s.writer.Encode(jsonrpcRequest{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "copilot-api", "version": "1.0"},
	}}); err != nil {
		s.Close()
		return nil, fmt.Errorf("send initialize: %w", err)
	}
	if _, err := readResponse(initCtx, s.scanner, 1); err != nil {
		s.Close()
		return nil, fmt.Errorf("read initialize response: %w", err)
	}
	// initialized 通知（无 id，无需响应）
	if err := s.writer.Encode(jsonrpcRequest{JSONRPC: "2.0", Method: "notifications/initialized"}); err != nil {
		s.Close()
		return nil, fmt.Errorf("send initialized notification: %w", err)
	}
	return s, nil
}

// Close 关闭 stdin 并等待子进程退出。
func (s *mcpSession) Close() {
	_ = s.stdin.Close()
	_ = s.cmd.Wait()
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

	session, err := startSession(ctx, config, handshakeTimeout)
	if err != nil {
		return nil, err
	}
	defer session.Close()

	// tools/list（支持 cursor 分页：循环请求直到 nextCursor 为空）
	var allTools []MCPTool
	var cursor string
	reqID := 2
	for {
		listCtx, listCancel := context.WithTimeout(ctx, listTimeout)
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var req jsonrpcRequest
		if len(params) > 0 {
			req = jsonrpcRequest{JSONRPC: "2.0", ID: reqID, Method: "tools/list", Params: params}
		} else {
			req = jsonrpcRequest{JSONRPC: "2.0", ID: reqID, Method: "tools/list"}
		}
		if err := session.writer.Encode(req); err != nil {
			listCancel()
			return nil, fmt.Errorf("send tools/list: %w", err)
		}
		resp, err := readResponse(listCtx, session.scanner, reqID)
		listCancel()
		if err != nil {
			return nil, fmt.Errorf("read tools/list response: %w", err)
		}
		var page toolsListResult
		if err := json.Unmarshal(resp, &page); err != nil {
			return nil, fmt.Errorf("decode tools/list result: %w", err)
		}
		allTools = append(allTools, page.Tools...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		reqID++
	}
	return allTools, nil
}

// Call 实现ToolCaller：握手后发起一次 tools/call，把结果转换为
// map[string]any（转换规则见 parseToolsCallResult）。
func (l *stdioLister) Call(ctx context.Context, config MCPServerConfig, toolName string, args map[string]any) (map[string]any, error) {
	if strings.TrimSpace(config.Command) == "" {
		return nil, fmt.Errorf("stdio caller requires command, config %q has none", config.Name)
	}
	handshakeTimeout := l.handshakeTimeout
	if handshakeTimeout == 0 {
		handshakeTimeout = 10 * time.Second
	}
	callTimeout := l.callTimeout
	if callTimeout == 0 {
		callTimeout = 30 * time.Second
	}

	session, err := startSession(ctx, config, handshakeTimeout)
	if err != nil {
		return nil, err
	}
	defer session.Close()

	callCtx, callCancel := context.WithTimeout(ctx, callTimeout)
	defer callCancel()
	if err := session.writer.Encode(jsonrpcRequest{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: map[string]any{
		"name":      toolName,
		"arguments": args,
	}}); err != nil {
		return nil, fmt.Errorf("send tools/call: %w", err)
	}
	result, err := readResponse(callCtx, session.scanner, 2)
	if err != nil {
		return nil, fmt.Errorf("read tools/call response: %w", err)
	}
	return parseToolsCallResult(result, config.Name, toolName)
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

// NewStdioListerWithTimeout 创建指定超时的 stdio lister（测试用）。
func NewStdioListerWithTimeout(handshake, list time.Duration) ToolLister {
	return &stdioLister{handshakeTimeout: handshake, listTimeout: list}
}

func envSlice(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
