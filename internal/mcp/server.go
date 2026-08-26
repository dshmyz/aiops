// Package mcp 实现 MCP (Model Context Protocol) HTTP Server，
// 把已发布的能力作为 MCP 工具对外暴露，供外部 AI 客户端（Claude Desktop、Cursor
// 等）调用。基于 mark3labs/mcp-go SDK 实现。
//
// 配置开关：COPILOT_MCP_SERVER_ENABLED=1，端口 COPILOT_MCP_SERVER_PORT（默认 18081）。
package mcp

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// mcpAuditID 生成审计事件 ID（16 字节随机十六进制，与 execution.newAuditID 对齐）。
func mcpAuditID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return hex.EncodeToString(value)
}

// MCPServer 是封装后的 MCP HTTP Server，基于 mcp-go SDK。
type MCPServer struct {
	store     capabilities.CapabilityStore
	runner    execution.ReadRunner
	audit     *audit.Service
	authToken string // 非空时要求 Authorization: Bearer <token>
	httpSrv   *server.StreamableHTTPServer
	mcpSrv    *server.MCPServer
	mu        sync.RWMutex
	toolsReg  bool
}

// NewMCPServer 创建 MCP HTTP Server。Handler() 返回 HTTP handler。
func NewMCPServer(store capabilities.CapabilityStore, runner execution.ReadRunner, auditSvc *audit.Service) *MCPServer {
	mcpSrv := server.NewMCPServer("aiops-copilot", "0.8.0",
		server.WithToolCapabilities(false),
		server.WithPaginationLimit(50), // 每页最多 50 个工具，避免数百工具一次性塞满客户端 context
	)
	return NewMCPServerFrom(mcpSrv, store, runner, auditSvc)
}

// NewMCPServerFrom 用已有的 MCPServer 构造（测试用）。
func NewMCPServerFrom(mcpSrv *server.MCPServer, store capabilities.CapabilityStore, runner execution.ReadRunner, auditSvc *audit.Service) *MCPServer {
	s := &MCPServer{store: store, runner: runner, audit: auditSvc, mcpSrv: mcpSrv}
	s.httpSrv = server.NewStreamableHTTPServer(mcpSrv)
	return s
}

// WithAuthToken 启用 Bearer token 鉴权：配置后所有 /mcp 请求必须携带
// Authorization: Bearer <token>，否则 401。不配置时不做鉴权（外部客户端可
// 以调用已发布读能力，但写能力仍被拒绝）。生产环境强烈建议配置。
func (s *MCPServer) WithAuthToken(token string) *MCPServer {
	s.authToken = strings.TrimSpace(token)
	return s
}

// Handler 返回可挂在 http.ServeMux 上的 HTTP handler。
// 调用前必须先调用 Init() 注册工具。配置了 auth token 时自动包一层鉴权。
func (s *MCPServer) Handler() http.Handler {
	h := http.Handler(s.httpSrv)
	if s.authToken == "" {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !constantTimeEqual(parts[1], s.authToken) {
			http.Error(w, `{"error":"unauthorized: missing or invalid bearer token"}`, http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// constantTimeEqual 用恒定时间比较两个 token，避免时序侧信道泄露。
func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// MCPServerGo 返回底层的 mcp-go server（调试/测试用）。
func (s *MCPServer) MCPServerGo() *server.MCPServer {
	return s.mcpSrv
}

// Init 从 store 加载已发布能力并注册为 MCP 工具。启动时调用一次即可。
func (s *MCPServer) Init(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.toolsReg {
		return nil
	}

	items, err := s.store.ListAll(ctx)
	if err != nil {
		return fmt.Errorf("list capabilities: %w", err)
	}

	for _, item := range items {
		if item.Source != capabilities.SourcePublished {
			continue
		}
		// 注册到内部工具表（供 policy.Evaluate 使用）。已注册则跳过（幂等）。
		_ = capabilities.RegisterPublishedCapability(item.Capability)
		tool := s.buildMCPTool(item)
		capturedItem := item
		s.mcpSrv.AddTool(tool, s.makeToolHandler(capturedItem))
	}
	s.toolsReg = true
	return nil
}

// buildMCPTool 把一个已发布的 capability 转成 mcp.Tool 定义。
func (s *MCPServer) buildMCPTool(item capabilities.ManagedCapability) mcp.Tool {
	props := make(map[string]any, len(item.InputSchema))
	var required []string
	for name, field := range item.InputSchema {
		p := map[string]any{"type": field.Type}
		if field.Description != "" {
			p["description"] = field.Description
		}
		if len(field.Enum) > 0 {
			p["enum"] = field.Enum
		}
		props[name] = p
		if field.Required {
			required = append(required, name)
		}
	}
	desc := item.AI.Description
	if desc == "" {
		desc = fmt.Sprintf("%s %s %s (%s)", item.Domain, item.ResourceType, item.Operation, item.Name)
	}
	return mcp.Tool{
		Name:        item.Name,
		Description: desc,
		InputSchema: mcp.ToolInputSchema{
			Type:       "object",
			Properties: props,
			Required:   required,
		},
	}
}

// makeToolHandler 为一个已发布能力创建 MCP tool handler。
func (s *MCPServer) makeToolHandler(item capabilities.ManagedCapability) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// 写能力不通过 MCP 直接执行——需要 action plan 审批。
		if item.Operation == tools.Write {
			return mcp.NewToolResultError(fmt.Sprintf(
				"写工具 %s 需要通过 action plan 审批，不能通过 MCP 直接执行。请在 Copilot 控制台中操作。",
				item.Name,
			)), nil
		}

		// 转成 tools.Tool 做权限校验
		tool, err := capabilities.ToTool(item.Capability)
		if err != nil {
			return nil, fmt.Errorf("convert capability: %w", err)
		}
		args, _ := req.Params.Arguments.(map[string]any)
		user := identity.CurrentUser{Roles: []string{"admin"}}
		decision := policy.Evaluate(user, tool, args)
		if !decision.Allowed {
			return mcp.NewToolResultError(fmt.Sprintf("权限拒绝：%s", decision.Reason)), nil
		}

		if s.runner == nil {
			return nil, fmt.Errorf("read service not available")
		}
		result, err := s.runner.Read(ctx, tool, args)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("执行失败：%v", err)), nil
		}

		// 审计记录。修复前事件未设 ID 且错误被吞掉，导致审计 100% 静默丢失
		//（"假装已审计"）；现在补 ID、失败至少 Warn 日志。
		if s.audit != nil {
			inputMap := args
			if inputMap == nil {
				inputMap = map[string]any{}
			}
			if err := s.audit.Record(ctx, audit.Event{
				ID:        mcpAuditID(),
				ToolName:  item.Name,
				Action:    "mcp_call",
				Decision:  audit.DecisionPermitted,
				Subject:   "mcp-client",
				Metadata:  map[string]any{"input": inputMap, "output": result},
				CreatedAt: time.Now().UTC(),
			}); err != nil {
				log.Printf("[mcp] audit record for %s failed: %v", item.Name, err)
			}
		}

		// 格式化结果
		text := formatMCPResult(item.Name, result)
		return mcp.NewToolResultText(text), nil
	}
}

// formatMCPResult 把工具执行结果格式化为 MCP 可读文本。
func formatMCPResult(toolName string, result map[string]any) string {
	if result == nil {
		return fmt.Sprintf("%s: 无结果", toolName)
	}
	if summary, ok := result["result_summary"].(string); ok && summary != "" {
		return summary
	}
	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Sprintf("%s: %v", toolName, result)
	}
	return string(b)
}

// MCPServerEnvConfig 从环境变量读取 MCP Server 配置。
// 端口与主 API 共享（/mcp 路径），无需单独配置端口。
type MCPServerEnvConfig struct {
	Enabled bool
	// Token 是 Bearer 鉴权 token。非空时 /mcp 请求必须携带
	// Authorization: Bearer <token>，否则 401。
	Token string
}

// MCPServerEnvConfigFromEnv 从环境变量构造配置。
func MCPServerEnvConfigFromEnv() MCPServerEnvConfig {
	return MCPServerEnvConfig{
		Enabled: os.Getenv("COPILOT_MCP_SERVER_ENABLED") == "1",
		Token:   strings.TrimSpace(os.Getenv("COPILOT_MCP_SERVER_TOKEN")),
	}
}
