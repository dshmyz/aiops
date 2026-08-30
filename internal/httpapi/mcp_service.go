package httpapi

import (
	"context"
	"fmt"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// MCPReloader 重新加载 MCP 服务器配置并增量注册/注销工具。
// mcp.Manager 实现此接口；httpapi 通过它解耦对 mcp 包的直接依赖，
// 避免顶层路由包引入 MCP 运行时（防止循环依赖）。
type MCPReloader interface {
	Reload(ctx context.Context) error
}

// MCPServerService 把 store.MCPServerStore（CRUD 持久化）和 MCPReloader
// （Reload 增量注册/注销工具）组合成 MCPService，供 router 注入。
//
// CRUD 操作直接委托给 store，保留 store.ErrNotFound / store.ErrConflict
// 哨兵错误，使 writeMCPError 能正确映射 HTTP 状态码。
// Reload 委托给 reloader；reloader 为 nil 时 Reload 返回明确错误，
// 允许"只管理配置、不热加载"的只读场景。
type MCPServerService struct {
	store    store.MCPServerStore
	reloader MCPReloader
}

// NewMCPServerService 创建 MCP 热配置服务。store 不能为 nil；
// reloader 为 nil 时仅支持 CRUD，Reload 调用会返回错误。
func NewMCPServerService(serverStore store.MCPServerStore, reloader MCPReloader) *MCPServerService {
	if serverStore == nil {
		panic("httpapi.NewMCPServerService: store is nil")
	}
	return &MCPServerService{store: serverStore, reloader: reloader}
}

func (s *MCPServerService) CreateServer(ctx context.Context, server store.MCPServerRecord) (store.MCPServerRecord, error) {
	return s.store.Create(ctx, server)
}

func (s *MCPServerService) GetServer(ctx context.Context, id string) (store.MCPServerRecord, error) {
	return s.store.Get(ctx, id)
}

func (s *MCPServerService) ListServers(ctx context.Context) ([]store.MCPServerRecord, error) {
	return s.store.List(ctx)
}

func (s *MCPServerService) UpdateServer(ctx context.Context, server store.MCPServerRecord) (store.MCPServerRecord, error) {
	return s.store.Update(ctx, server)
}

func (s *MCPServerService) DeleteServer(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

func (s *MCPServerService) Reload(ctx context.Context) error {
	if s.reloader == nil {
		return fmt.Errorf("MCP reload is not configured")
	}
	return s.reloader.Reload(ctx)
}
