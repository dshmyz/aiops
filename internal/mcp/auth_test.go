package mcp_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpgo_server "github.com/mark3labs/mcp-go/server"

	capmcp "github.com/gracegaoya/ai-operations-copilot/internal/mcp"
)

// TestMCPAuthToken 验证 Bearer token 鉴权：无 token / 错误 token → 401，
// 正确 token 放行。修复前 /mcp 无鉴权，任何客户端可调用已发布读能力。
func TestMCPAuthToken(t *testing.T) {
	raw := mcpgo_server.NewMCPServer("test", "0.0.1", mcpgo_server.WithToolCapabilities(false))
	srv := capmcp.NewMCPServerFrom(raw, nil, nil, nil).WithAuthToken("secret-token")
	handler := srv.Handler()

	cases := []struct {
		name       string
		method     string
		body       string
		authHeader string
		wantCode   int
	}{
		{name: "missing", method: http.MethodGet, wantCode: http.StatusUnauthorized},
		{name: "wrong scheme", method: http.MethodGet, authHeader: "Basic secret-token", wantCode: http.StatusUnauthorized},
		{name: "wrong token", method: http.MethodGet, authHeader: "Bearer wrong", wantCode: http.StatusUnauthorized},
		// correct 走标准 MCP JSON-RPC initialize（POST），验证带正确 token 时
		// 通过鉴权进入 MCP 处理链；GET 无 stream Accept 会触发 mcp-go SSE 路径
		// 阻塞，不适合在此验证。
		{name: "correct", method: http.MethodPost, body: `{"jsonrpc":"2.0","method":"initialize","id":1,"params":{}}`, authHeader: "Bearer secret-token", wantCode: http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/mcp", strings.NewReader(tc.body))
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", rr.Code, tc.wantCode)
			}
		})
	}
}

// TestMCPNoAuthTokenPassthrough 验证未配置 token 时不做鉴权（向后兼容），
// 请求直接到达 MCP 处理链（POST initialize 返回 MCP 协议响应而非 401）。
func TestMCPNoAuthTokenPassthrough(t *testing.T) {
	raw := mcpgo_server.NewMCPServer("test", "0.0.1", mcpgo_server.WithToolCapabilities(false))
	srv := capmcp.NewMCPServerFrom(raw, nil, nil, nil) // 未 WithAuthToken
	handler := srv.Handler()

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"initialize","id":1,"params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code == http.StatusUnauthorized {
		t.Fatalf("status = %d, want non-401 (no auth configured)", rr.Code)
	}
}
