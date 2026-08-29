package mcp_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/mcp"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// TestStdioCallerToolsCall 验证 stdio 调用端能完成握手并发起 tools/call，
// 各种结果形态各自正确转换：
//   - structuredContent → 原样返回
//   - text 是 JSON object → 解析成 map
//   - text 是自由文本 → 包成 {"result": text}
//   - isError=true → 转为 error
func TestStdioCallerToolsCall(t *testing.T) {
	mockServer := buildCallMockServer(t)

	lister := mcp.NewStdioListerWithTimeout(5*time.Second, 5*time.Second)
	toolCaller, ok := lister.(mcp.ToolCaller)
	if !ok {
		t.Fatalf("stdio lister must implement ToolCaller")
	}
	config := mcp.MCPServerConfig{Name: "callable-server", Command: mockServer}
	ctx := t.Context()

	// structuredContent 原样返回
	got, err := toolCaller.Call(ctx, config, "structured.tool", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("Call structured: %v", err)
	}
	if got["usage_pct"] != float64(77) {
		t.Fatalf("structured result = %v, want usage_pct=77", got)
	}

	// JSON object 文本解析成 map
	got, err = toolCaller.Call(ctx, config, "json.tool", nil)
	if err != nil {
		t.Fatalf("Call json: %v", err)
	}
	if got["status"] != "ok" {
		t.Fatalf("json result = %v, want status=ok", got)
	}

	// 自由文本包成 {"result": text}
	got, err = toolCaller.Call(ctx, config, "text.tool", nil)
	if err != nil {
		t.Fatalf("Call text: %v", err)
	}
	if got["result"] != "restarted 2 pods" {
		t.Fatalf("text result = %v, want result=restarted 2 pods", got)
	}

	// isError → error
	if _, err = toolCaller.Call(ctx, config, "error.tool", nil); err == nil {
		t.Fatalf("Call error.tool: want error, got nil")
	}

	// 参数透传：echo.tool 原样返回收到的 arguments
	got, err = toolCaller.Call(ctx, config, "echo.tool", map[string]any{"pod": "api-0", "n": float64(2)})
	if err != nil {
		t.Fatalf("Call echo: %v", err)
	}
	if got["pod"] != "api-0" {
		t.Fatalf("echo result = %v, want pod=api-0 (arguments must reach the server)", got)
	}
}

// TestStdioCallerMCPRPCError 验证 JSON-RPC 层错误（方法不存在等）转为 error。
func TestStdioCallerMCPRPCError(t *testing.T) {
	mockServer := buildRPCErrorMockServer(t)
	lister := mcp.NewStdioListerWithTimeout(5*time.Second, 5*time.Second)
	toolCaller := lister.(mcp.ToolCaller)
	config := mcp.MCPServerConfig{Name: "rpc-error-server", Command: mockServer}
	if _, err := toolCaller.Call(t.Context(), config, "any.tool", nil); err == nil {
		t.Fatalf("want rpc error, got nil")
	}
}

// TestManagerRoutesCallToOwningServer 验证 Manager 按工具名前缀解析所属
// 服务器并转发调用；未发现/禁用服务器的工具不路由。
func TestManagerRoutesCallToOwningServer(t *testing.T) {
	env := newManagerTestEnv()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	srvName := uniqueName(t, "k8s-ops")
	disabledName := uniqueName(t, "disabled-ops")

	if _, err := env.store.Create(context.Background(), store.MCPServerRecord{
		ID: "srv-1", Name: srvName, Command: "mcp-k8s", Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := env.store.Create(context.Background(), store.MCPServerRecord{
		ID: "srv-2", Name: disabledName, Command: "mcp-disabled", Enabled: false,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.lister.setTools(srvName, []mcp.MCPTool{{Name: "pod.restart", InputSchema: mcp.MCPInputSchema{Type: "object"}}})
	env.lister.setTools(disabledName, []mcp.MCPTool{{Name: "noop.read", InputSchema: mcp.MCPInputSchema{Type: "object"}}})

	calls := []string{}
	env.manager.WithToolCaller(fakeCaller{fn: func(_ context.Context, config mcp.MCPServerConfig, toolName string, _ map[string]any) (map[string]any, error) {
		calls = append(calls, config.Name+"/"+toolName)
		return map[string]any{"ok": true}, nil
	}})

	if err := env.manager.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// 拥有方服务器 → 转发裸工具名
	if !env.manager.OwnsTool(srvName + ".pod.restart") {
		t.Fatalf("OwnsTool(%s.pod.restart) = false, want true", srvName)
	}
	// 禁用服务器的工具不路由
	if env.manager.OwnsTool(disabledName + ".noop.read") {
		t.Fatalf("OwnsTool(%s.noop.read) = true, want false", disabledName)
	}
	fullName := srvName + ".pod.restart"
	got, err := env.manager.Call(context.Background(), fullName, map[string]any{"name": "api-0"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got["ok"] != true {
		t.Fatalf("Call result = %v", got)
	}
	if len(calls) != 1 || calls[0] != srvName+"/pod.restart" {
		t.Fatalf("routed calls = %v, want [%s/pod.restart]", calls, srvName)
	}
	// 非工具与未知工具不归属 MCP
	if env.manager.OwnsTool("alert.query") || env.manager.OwnsTool("unknown.tool") {
		t.Fatalf("non-MCP tools must not be owned")
	}
	if _, err := env.manager.Call(context.Background(), "unknown.tool", nil); err == nil {
		t.Fatalf("Call(unknown.tool): want error, got nil")
	}
}

// TestManagerCallWithoutCaller 验证传输不支持调用时的明确错误（不静默假成功）。
func TestManagerCallWithoutCaller(t *testing.T) {
	env := newManagerTestEnv()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	srvName := uniqueName(t, "srv")

	if _, err := env.store.Create(context.Background(), store.MCPServerRecord{
		ID: "srv-1", Name: srvName, Command: "mcp-srv", Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.lister.setTools(srvName, []mcp.MCPTool{{Name: "t", InputSchema: mcp.MCPInputSchema{Type: "object"}}})

	// fakeManagerLister 只实现 List，不实现 ToolCaller → manager 无 caller
	if err := env.manager.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !env.manager.OwnsTool(srvName + ".t") {
		t.Fatalf("OwnsTool(%s.t) = false, want true", srvName)
	}
	_, err := env.manager.Call(context.Background(), srvName+".t", nil)
	if err == nil || !strings.Contains(err.Error(), "does not support tool calling") {
		t.Fatalf("want unsupported-transport error, got %v", err)
	}
}

// fakeCaller 记录路由结果的 ToolCaller stub。
type fakeCaller struct {
	fn func(ctx context.Context, config mcp.MCPServerConfig, toolName string, args map[string]any) (map[string]any, error)
}

func (f fakeCaller) Call(ctx context.Context, config mcp.MCPServerConfig, toolName string, args map[string]any) (map[string]any, error) {
	return f.fn(ctx, config, toolName, args)
}

// buildCallMockServer 构建支持 tools/call 的 mock MCP 服务器子进程。
func buildCallMockServer(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	src := tmp + "/mock_call_server.go"

	// 按工具名返回不同形态的结果；echo.tool 原样返回 arguments 便于断言透传。
	source := `package main

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
)

type callContent struct {
	Type string ` + "`json:\"type\"`" + `
	Text string ` + "`json:\"text\"`" + `
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	writer := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var req struct {
			JSONRPC string ` + "`json:\"jsonrpc\"`" + `
			ID      int    ` + "`json:\"id\"`" + `
			Method  string ` + "`json:\"method\"`" + `
			Params  struct {
				Name      string         ` + "`json:\"name\"`" + `
				Arguments map[string]any ` + "`json:\"arguments\"`" + `
			} ` + "`json:\"params\"`" + `
		}
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			writer.Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{},
					"serverInfo":      map[string]any{"name": "mock-call", "version": "0.0.1"},
				},
			})
		case "notifications/initialized":
		case "tools/call":
			var result map[string]any
			switch req.Params.Name {
			case "structured.tool":
				result = map[string]any{
					"structuredContent": map[string]any{"usage_pct": 77},
				}
			case "json.tool":
				result = map[string]any{
					"content": []callContent{{Type: "text", Text: "{\"status\":\"ok\"}"}},
				}
			case "text.tool":
				result = map[string]any{
					"content": []callContent{{Type: "text", Text: "restarted 2 pods"}},
				}
			case "error.tool":
				result = map[string]any{
					"content": []callContent{{Type: "text", Text: "pod not found"}},
					"isError": true,
				}
			case "echo.tool":
				argsJSON, _ := json.Marshal(req.Params.Arguments)
				result = map[string]any{
					"content": []callContent{{Type: "text", Text: string(argsJSON)}},
				}
			default:
				result = map[string]any{
					"content": []callContent{{Type: "text", Text: "unknown tool " + req.Params.Name}},
					"isError": true,
				}
			}
			writer.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		}
	}
}
`
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatalf("write mock call server source: %v", err)
	}
	bin := tmp + "/mock_call_server"
	cmd := exec.Command("go", "build", "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mock call server: %v\n%s", err, out)
	}
	return bin
}

// buildRPCErrorMockServer 构建一个握手成功但 tools/call 返回 JSON-RPC
// 错误的 mock 服务器。
func buildRPCErrorMockServer(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	src := tmp + "/mock_rpc_error_server.go"
	source := `package main

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
)

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	writer := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var req struct {
			JSONRPC string ` + "`json:\"jsonrpc\"`" + `
			ID      int    ` + "`json:\"id\"`" + `
			Method  string ` + "`json:\"method\"`" + `
		}
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			writer.Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}},
			})
		case "notifications/initialized":
		default:
			writer.Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"error": map[string]any{"code": -32601, "message": "method not found: " + req.Method},
			})
		}
	}
}
`
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatalf("write mock rpc error server: %v", err)
	}
	bin := tmp + "/mock_rpc_error_server"
	cmd := exec.Command("go", "build", "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mock rpc error server: %v\n%s", err, out)
	}
	return bin
}
