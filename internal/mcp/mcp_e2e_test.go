package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	capmcp "github.com/gracegaoya/ai-operations-copilot/internal/mcp"
	mcpgo_server "github.com/mark3labs/mcp-go/server"
)

func TestMCPServerE2E(t *testing.T) {
	root := t.TempDir()
	pub := root + "/published"
	if err := os.MkdirAll(pub, 0o755); err != nil {
		t.Fatalf("mkdir published: %v", err)
	}
	if err := os.WriteFile(pub+"/minio.read.yaml", []byte(`schema_version: 1
name: minio.bucket.capacity.read
status: published
domain: minio
resource_type: bucket
operation: read
risk: low
backend:
  adapter: http
  method: GET
  path: /api/minio/{cluster}/buckets/{name}/capacity
  timeout_ms: 3000
  base_url: http://127.0.0.1:19090
input_schema:
  environment: {type: string, required: true}
  cluster: {type: string, required: true}
  name: {type: string, required: true}
auth: {roles: [viewer, operator, admin], environment_scoped: true}
output: {kind: observation, summary_template: "Bucket {name} usage is {usage_pct}%", fields: {usage_pct: "$.data.usage_pct"}}
ai: {description: 读取 MinIO 桶容量}
`), 0o644); err != nil {
		t.Fatalf("write minio.yaml: %v", err)
	}

	store := capabilities.NewFileCapabilityStore(root)
	fake := &fakeReadRunner{result: map[string]any{"usage_pct": 77, "limit_gb": 100}}
	mcpSrvRaw := mcpgo_server.NewMCPServer("test", "0.0.1", mcpgo_server.WithToolCapabilities(false))
	mcpSrv := capmcp.NewMCPServerFrom(mcpSrvRaw, store, fake, nil)
	if err := mcpSrv.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// debug: check server's internal tool list
	serverTools := mcpSrv.MCPServerGo().ListTools()
	fmt.Printf("DEBUG: server has %d tools internally\n", len(serverTools))
	for name := range serverTools {
		fmt.Printf("DEBUG: tool=%s\n", name)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	httpSrv := &http.Server{Handler: mcpSrv.Handler()}
	go func() { _ = httpSrv.Serve(listener) }()
	t.Cleanup(func() { httpSrv.Close() })
	addr := listener.Addr().String()

	// initialize
	_, sessionID := mcpPostWithSession(t, addr, `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{}}`, "")
	fmt.Printf("initialize: session=%s\n", sessionID)

	// notifications/initialized (required by MCP protocol before tools/list)
	mcpPostWithSession(t, addr, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, sessionID)
	fmt.Println("notifications/initialized sent")

	// tools/list (must carry session ID)
	resp, _ := mcpPostWithSession(t, addr, `{"jsonrpc":"2.0","method":"tools/list","id":2}`, sessionID)
	var listResult struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &listResult); err != nil {
		t.Fatalf("unmarshal tools/list: %v", err)
	}
	fmt.Printf("tools/list: %d tools\n", len(listResult.Result.Tools))
	if len(listResult.Result.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(listResult.Result.Tools))
	}
	fmt.Printf("  name=%s desc=%s\n", listResult.Result.Tools[0].Name, listResult.Result.Tools[0].Description)

	// tools/call (must carry session ID)
	resp, _ = mcpPostWithSession(t, addr, `{"jsonrpc":"2.0","method":"tools/call","id":3,"params":{"name":"minio.bucket.capacity.read","arguments":{"environment":"prod","cluster":"default","name":"archive"}}}`, sessionID)
	fmt.Printf("tools/call raw: %s\n", string(resp))
	var callResult struct {
		Result struct {
			Content []struct{ Type string `json:"type"`; Text string `json:"text"` } `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct{ Code int `json:"code"`; Message string `json:"message"` } `json:"error"`
	}
	if err := json.Unmarshal(resp, &callResult); err != nil {
		t.Fatalf("unmarshal tools/call: %v", err)
	}
	if callResult.Error != nil {
		t.Fatalf("tools/call error: code=%d msg=%s", callResult.Error.Code, callResult.Error.Message)
	}
	text := callResult.Result.Content[0].Text
	fmt.Printf("tools/call: text=%s isError=%v\n", text, callResult.Result.IsError)
	if !strings.Contains(text, "77") || !strings.Contains(text, "100") {
		t.Fatalf("text = %q, want to contain 77 and 100", text)
	}

	fmt.Println("=== MCP E2E PASSED ===")
}

func mcpPostWithSession(t *testing.T, addr, body, sessionID string) ([]byte, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/mcp", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	newSessionID := resp.Header.Get("Mcp-Session-Id")
	return buf.Bytes(), newSessionID
}
