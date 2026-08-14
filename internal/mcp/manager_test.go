package mcp_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/mcp"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// --- Manager 热配置测试 ---

// managerTestEnv 封装 Manager 测试所需的依赖。
type managerTestEnv struct {
	store     *store.MemoryMCPServerStore
	lister    *fakeManagerLister
	manager   *mcp.Manager
	emitter   *recordingEmitter
}

// fakeManagerLister 是可控的 ToolLister，按服务器名返回不同工具集，
// 支持运行时修改返回值以模拟 MCP 服务器工具列表变化。
type fakeManagerLister struct {
	mu     sync.Mutex
	byName map[string][]mcp.MCPTool
	errs   map[string]error
}

func (f *fakeManagerLister) List(_ context.Context, config mcp.MCPServerConfig) ([]mcp.MCPTool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.errs[config.Name]; ok {
		return nil, err
	}
	tools := f.byName[config.Name]
	out := make([]mcp.MCPTool, len(tools))
	copy(out, tools)
	return out, nil
}

func (f *fakeManagerLister) setTools(name string, ts []mcp.MCPTool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byName[name] = ts
}

func (f *fakeManagerLister) setError(name string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[name] = err
}

func newManagerTestEnv() *managerTestEnv {
	tools.ResetDynamicToolsForTest()
	srvStore := store.NewMemoryMCPServerStore()
	lister := &fakeManagerLister{
		byName: map[string][]mcp.MCPTool{},
		errs:   map[string]error{},
	}
	emitter := &recordingEmitter{}
	manager := mcp.NewManager(srvStore, lister).WithEventEmitter(emitter.Emit)
	return &managerTestEnv{
		store:   srvStore,
		lister:  lister,
		manager: manager,
		emitter: emitter,
	}
}

func envTool(name string) mcp.MCPTool {
	return mcp.MCPTool{
		Name: name,
		InputSchema: mcp.MCPInputSchema{
			Type:       "object",
			Properties: map[string]mcp.MCPPropertySchema{"environment": {Type: "string"}},
			Required:   []string{"environment"},
		},
	}
}

// uniqueName 用测试名生成唯一服务器名，避免并行测试共享全局 dynamicTools 冲突。
func uniqueName(t *testing.T, suffix string) string {
	t.Helper()
	// 取 t.Name() 末尾段（子测试场景）做前缀，保证并行测试不撞名
	name := t.Name()
	if idx := len(name) - 1; idx > 0 {
		for i := idx; i >= 0; i-- {
			if name[i] == '/' {
				name = name[i+1:]
				break
			}
		}
	}
	return name + "-" + suffix
}

func TestManagerReloadRegistersNewServerTools(t *testing.T) {
	env := newManagerTestEnv()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	srvName := uniqueName(t, "grafana")

	// DB 中有一个启用的服务器
	if _, err := env.store.Create(context.Background(), store.MCPServerRecord{
		ID: "srv-1", Name: srvName, Command: "mcp-grafana", Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.lister.setTools(srvName, []mcp.MCPTool{envTool("query")})

	if err := env.manager.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// 工具应已注册
	tool, ok := tools.Lookup(srvName + ".query")
	if !ok {
		t.Fatalf("Lookup %s.query failed after Reload", srvName)
	}
	if tool.Domain != srvName {
		t.Errorf("Domain = %q, want %s", tool.Domain, srvName)
	}
}

func TestManagerReloadSkipsDisabledServers(t *testing.T) {
	env := newManagerTestEnv()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	srvName := uniqueName(t, "skip")

	if _, err := env.store.Create(context.Background(), store.MCPServerRecord{
		ID: "srv-skip", Name: srvName, Command: "mcp-grafana", Enabled: false,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.lister.setTools(srvName, []mcp.MCPTool{envTool("query")})

	if err := env.manager.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if _, ok := tools.Lookup(srvName + ".query"); ok {
		t.Fatalf("Lookup %s.query should fail for disabled server", srvName)
	}
}

func TestManagerReloadUnregistersRemovedServerTools(t *testing.T) {
	env := newManagerTestEnv()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	srvName := uniqueName(t, "rm")

	// 第一次 Reload：注册 srvName.query
	if _, err := env.store.Create(context.Background(), store.MCPServerRecord{
		ID: "srv-1", Name: srvName, Command: "mcp-grafana", Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.lister.setTools(srvName, []mcp.MCPTool{envTool("query")})
	if err := env.manager.Reload(context.Background()); err != nil {
		t.Fatalf("Reload 1: %v", err)
	}
	if _, ok := tools.Lookup(srvName + ".query"); !ok {
		t.Fatalf("%s.query should be registered after first Reload", srvName)
	}

	// 删除服务器后重新 Reload
	if err := env.store.Delete(context.Background(), "srv-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := env.manager.Reload(context.Background()); err != nil {
		t.Fatalf("Reload 2: %v", err)
	}

	// 工具应已注销
	if _, ok := tools.Lookup(srvName + ".query"); ok {
		t.Fatalf("%s.query should be unregistered after server deleted", srvName)
	}
}

func TestManagerReloadUnregistersDisabledServerTools(t *testing.T) {
	env := newManagerTestEnv()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	srvName := uniqueName(t, "dis")

	if _, err := env.store.Create(context.Background(), store.MCPServerRecord{
		ID: "srv-1", Name: srvName, Command: "mcp-grafana", Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.lister.setTools(srvName, []mcp.MCPTool{envTool("query")})
	if err := env.manager.Reload(context.Background()); err != nil {
		t.Fatalf("Reload 1: %v", err)
	}

	// 禁用服务器后重新 Reload
	if _, err := env.store.Update(context.Background(), store.MCPServerRecord{
		ID: "srv-1", Name: srvName, Command: "mcp-grafana", Enabled: false,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := env.manager.Reload(context.Background()); err != nil {
		t.Fatalf("Reload 2: %v", err)
	}

	if _, ok := tools.Lookup(srvName + ".query"); ok {
		t.Fatalf("%s.query should be unregistered after server disabled", srvName)
	}
}

func TestManagerReloadEmitsToolsChangedEvent(t *testing.T) {
	env := newManagerTestEnv()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	srvName := uniqueName(t, "evt")

	if _, err := env.store.Create(context.Background(), store.MCPServerRecord{
		ID: "srv-1", Name: srvName, Command: "mcp-grafana", Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.lister.setTools(srvName, []mcp.MCPTool{envTool("query")})

	_ = env.manager.Reload(context.Background())

	// 首次 Reload 新增工具，应触发 tools_changed 事件
	foundChanged := false
	for _, event := range env.emitter.events {
		if event.Type == mcp.EventTypeToolsChanged {
			foundChanged = true
		}
	}
	if !foundChanged {
		t.Fatal("expected tools_changed event on first Reload with new tools")
	}
}

func TestManagerReloadNoEventWhenNoChanges(t *testing.T) {
	env := newManagerTestEnv()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	srvName := uniqueName(t, "noevt")

	if _, err := env.store.Create(context.Background(), store.MCPServerRecord{
		ID: "srv-1", Name: srvName, Command: "mcp-grafana", Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.lister.setTools(srvName, []mcp.MCPTool{envTool("query")})

	_ = env.manager.Reload(context.Background())
	env.emitter.events = nil // 清空首次事件
	_ = env.manager.Reload(context.Background()) // 第二次无变更

	for _, event := range env.emitter.events {
		if event.Type == mcp.EventTypeToolsChanged {
			t.Fatal("tools_changed event should not fire when no changes")
		}
	}
}

func TestManagerReloadEmitsUnhealthyEventOnListerError(t *testing.T) {
	env := newManagerTestEnv()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	srvName := uniqueName(t, "broken")

	if _, err := env.store.Create(context.Background(), store.MCPServerRecord{
		ID: "srv-1", Name: srvName, Command: "mcp-broken", Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.lister.setError(srvName, errors.New("connection refused"))

	_ = env.manager.Reload(context.Background())

	foundUnhealthy := false
	for _, event := range env.emitter.events {
		if event.Type == mcp.EventTypeHealthUnhealthy {
			foundUnhealthy = true
		}
	}
	if !foundUnhealthy {
		t.Fatal("expected unhealthy event when lister fails")
	}
}

func TestManagerReloadHandlesServerWithChangedTools(t *testing.T) {
	env := newManagerTestEnv()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	srvName := uniqueName(t, "chg")

	if _, err := env.store.Create(context.Background(), store.MCPServerRecord{
		ID: "srv-1", Name: srvName, Command: "mcp-grafana", Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.lister.setTools(srvName, []mcp.MCPTool{envTool("query")})
	_ = env.manager.Reload(context.Background())

	// 服务器工具列表变化：移除 query，新增 alerts
	env.lister.setTools(srvName, []mcp.MCPTool{envTool("alerts")})
	env.emitter.events = nil
	_ = env.manager.Reload(context.Background())

	// 旧工具应注销
	if _, ok := tools.Lookup(srvName + ".query"); ok {
		t.Fatalf("%s.query should be unregistered after tool list change", srvName)
	}
	// 新工具应注册
	if _, ok := tools.Lookup(srvName + ".alerts"); !ok {
		t.Fatalf("%s.alerts should be registered after tool list change", srvName)
	}
	// 应触发 tools_changed 事件
	foundChanged := false
	for _, event := range env.emitter.events {
		if event.Type == mcp.EventTypeToolsChanged {
			foundChanged = true
		}
	}
	if !foundChanged {
		t.Fatal("expected tools_changed event when tool list changes")
	}
}

func TestManagerConcurrentReloadSafe(t *testing.T) {
	env := newManagerTestEnv()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	srvName := uniqueName(t, "conc")

	if _, err := env.store.Create(context.Background(), store.MCPServerRecord{
		ID: "srv-conc", Name: srvName, Command: "mcp-grafana", Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.lister.setTools(srvName, []mcp.MCPTool{envTool("query")})

	// 并发 Reload 不应 panic 或 data race
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = env.manager.Reload(context.Background())
		}()
	}
	wg.Wait()

	// 最终状态应正确
	if _, ok := tools.Lookup(srvName + ".query"); !ok {
		t.Fatalf("%s.query should be registered after concurrent Reload", srvName)
	}
}

func TestManagerReloadEmptyDBClearsAll(t *testing.T) {
	env := newManagerTestEnv()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	srvName := uniqueName(t, "empty")

	// 先注册一个服务器
	if _, err := env.store.Create(context.Background(), store.MCPServerRecord{
		ID: "srv-1", Name: srvName, Command: "mcp-grafana", Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	env.lister.setTools(srvName, []mcp.MCPTool{envTool("query")})
	_ = env.manager.Reload(context.Background())

	// 删除所有服务器后 Reload
	_ = env.store.Delete(context.Background(), "srv-1")
	_ = env.manager.Reload(context.Background())

	if _, ok := tools.Lookup(srvName + ".query"); ok {
		t.Fatal("all MCP tools should be unregistered when DB is empty")
	}
}

// 确保 time 包被使用（避免未使用 import 在某些编译器下报错）
var _ = time.Now
