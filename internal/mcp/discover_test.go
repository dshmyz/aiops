package mcp_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/mcp"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// --- 配置加载测试 ---

func TestLoadConfigsParsesValidJSON(t *testing.T) {
	t.Parallel()
	raw := `[{"name":"grafana","command":"mcp-server-grafana","args":["--port=3000"]}]`
	configs, err := mcp.LoadConfigs(raw)
	if err != nil {
		t.Fatalf("LoadConfigs: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("configs len = %d, want 1", len(configs))
	}
	if configs[0].Name != "grafana" {
		t.Errorf("Name = %q, want grafana", configs[0].Name)
	}
	if configs[0].Command != "mcp-server-grafana" {
		t.Errorf("Command = %q", configs[0].Command)
	}
	if len(configs[0].Args) != 1 || configs[0].Args[0] != "--port=3000" {
		t.Errorf("Args = %v", configs[0].Args)
	}
}

func TestLoadConfigsEmptyStringReturnsEmpty(t *testing.T) {
	t.Parallel()
	configs, err := mcp.LoadConfigs("")
	if err != nil {
		t.Fatalf("LoadConfigs empty: %v", err)
	}
	if len(configs) != 0 {
		t.Fatalf("configs len = %d, want 0", len(configs))
	}
}

func TestLoadConfigsRejectsInvalidJSON(t *testing.T) {
	t.Parallel()
	if _, err := mcp.LoadConfigs("{not json"); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadConfigsRequiresName(t *testing.T) {
	t.Parallel()
	raw := `[{"command":"mcp-server"}]`
	if _, err := mcp.LoadConfigs(raw); err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestLoadConfigsRequiresCommandOrURL(t *testing.T) {
	t.Parallel()
	raw := `[{"name":"grafana"}]`
	if _, err := mcp.LoadConfigs(raw); err == nil {
		t.Fatal("expected error when neither command nor url is set")
	}
}

func TestLoadConfigsAcceptsURLInsteadOfCommand(t *testing.T) {
	t.Parallel()
	raw := `[{"name":"remote","url":"http://localhost:8080/sse"}]`
	configs, err := mcp.LoadConfigs(raw)
	if err != nil {
		t.Fatalf("LoadConfigs: %v", err)
	}
	if configs[0].URL != "http://localhost:8080/sse" {
		t.Errorf("URL = %q", configs[0].URL)
	}
	if configs[0].Command != "" {
		t.Errorf("Command should be empty when URL is used, got %q", configs[0].Command)
	}
}

// --- Discover 转换测试 ---

// fakeLister 是 ToolLister 的测试桩，返回预设的 MCP 工具列表。
type fakeLister struct {
	tools []mcp.MCPTool
	err   error
}

func (f fakeLister) List(_ context.Context, _ mcp.MCPServerConfig) ([]mcp.MCPTool, error) {
	return f.tools, f.err
}

func TestDiscoverConvertsMCPToolsToDynamicDefinitions(t *testing.T) {
	t.Parallel()
	configs := []mcp.MCPServerConfig{{Name: "grafana", Command: "mcp-server-grafana"}}
	lister := fakeLister{tools: []mcp.MCPTool{
		{
			Name:        "query_metrics",
			Description: "Query Grafana metrics",
			InputSchema: mcp.MCPInputSchema{
				Type: "object",
				Properties: map[string]mcp.MCPPropertySchema{
					"environment": {Type: "string", Description: "Target environment"},
					"query":       {Type: "string", Description: "PromQL query"},
				},
				Required: []string{"environment", "query"},
			},
		},
	}}

	defs, err := mcp.Discover(context.Background(), lister, configs)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("defs len = %d, want 1", len(defs))
	}
	def := defs[0]
	// 工具名应为 <server>.<tool>
	if def.Tool.Name != "grafana.query_metrics" {
		t.Errorf("Tool name = %q, want grafana.query_metrics", def.Tool.Name)
	}
	if def.Tool.Operation != tools.Read {
		t.Errorf("Operation = %v, want Read", def.Tool.Operation)
	}
	if def.Tool.Risk != tools.Low {
		t.Errorf("Risk = %v, want Low", def.Tool.Risk)
	}
	// input schema 应包含 environment 和 query
	if _, ok := def.InputSchema["environment"]; !ok {
		t.Error("InputSchema missing environment")
	}
	if _, ok := def.InputSchema["query"]; !ok {
		t.Error("InputSchema missing query")
	}
}

func TestDiscoverInjectsEnvironmentWhenMissing(t *testing.T) {
	t.Parallel()
	configs := []mcp.MCPServerConfig{{Name: "loki", URL: "http://localhost:3100/sse"}}
	lister := fakeLister{tools: []mcp.MCPTool{
		{
			Name:        "search_logs",
			Description: "Search Loki logs",
			InputSchema: mcp.MCPInputSchema{
				Type: "object",
				Properties: map[string]mcp.MCPPropertySchema{
					"query": {Type: "string"},
				},
				Required: []string{"query"},
			},
		},
	}}

	defs, err := mcp.Discover(context.Background(), lister, configs)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("defs len = %d, want 1", len(defs))
	}
	// 即使 MCP 工具没有 environment，转换后必须注入（tools.RegisterDynamicTools 强制要求）
	env, ok := defs[0].InputSchema["environment"]
	if !ok {
		t.Fatal("InputSchema missing injected environment")
	}
	if env.Type != "string" || !env.Required {
		t.Errorf("environment field = %+v, want required string", env)
	}
}

func TestDiscoverPreservesEnvironmentWhenPresent(t *testing.T) {
	t.Parallel()
	configs := []mcp.MCPServerConfig{{Name: "svc", Command: "mcp-server"}}
	lister := fakeLister{tools: []mcp.MCPTool{
		{
			Name: "tool1",
			InputSchema: mcp.MCPInputSchema{
				Type: "object",
				Properties: map[string]mcp.MCPPropertySchema{
					"environment": {Type: "string"},
				},
				Required: []string{"environment"},
			},
		},
	}}

	defs, err := mcp.Discover(context.Background(), lister, configs)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	env := defs[0].InputSchema["environment"]
	if !env.Required {
		t.Error("existing environment field should remain required")
	}
}

func TestDiscoverPrefixesToolNameWithServerName(t *testing.T) {
	t.Parallel()
	configs := []mcp.MCPServerConfig{{Name: "prometheus", Command: "mcp-server-prom"}}
	lister := fakeLister{tools: []mcp.MCPTool{
		{Name: "query", InputSchema: mcp.MCPInputSchema{
			Type:       "object",
			Properties: map[string]mcp.MCPPropertySchema{"environment": {Type: "string"}},
			Required:   []string{"environment"},
		}},
		{Name: "alerts", InputSchema: mcp.MCPInputSchema{
			Type:       "object",
			Properties: map[string]mcp.MCPPropertySchema{"environment": {Type: "string"}},
			Required:   []string{"environment"},
		}},
	}}

	defs, err := mcp.Discover(context.Background(), lister, configs)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("defs len = %d, want 2", len(defs))
	}
	wantNames := map[string]bool{"prometheus.query": false, "prometheus.alerts": false}
	for _, d := range defs {
		wantNames[d.Tool.Name] = true
	}
	for name, found := range wantNames {
		if !found {
			t.Errorf("missing tool %q", name)
		}
	}
}

func TestDiscoverPropagatesListerError(t *testing.T) {
	t.Parallel()
	configs := []mcp.MCPServerConfig{{Name: "broken", Command: "mcp-broken"}}
	lister := fakeLister{err: errors.New("connection refused")}
	_, err := mcp.Discover(context.Background(), lister, configs)
	if err == nil {
		t.Fatal("expected error when lister fails")
	}
}

func TestDiscoverSkipsUnsupportedPropertyTypes(t *testing.T) {
	t.Parallel()
	configs := []mcp.MCPServerConfig{{Name: "svc", Command: "mcp-server"}}
	lister := fakeLister{tools: []mcp.MCPTool{
		{
			Name: "tool_with_array",
			InputSchema: mcp.MCPInputSchema{
				Type: "object",
				Properties: map[string]mcp.MCPPropertySchema{
					"environment": {Type: "string"},
					"items":       {Type: "array"}, // unsupported, should be dropped
				},
				Required: []string{"environment"},
			},
		},
	}}

	defs, err := mcp.Discover(context.Background(), lister, configs)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("defs len = %d, want 1 (tool kept, array field dropped)", len(defs))
	}
	if _, ok := defs[0].InputSchema["items"]; ok {
		t.Error("unsupported array field should be dropped from InputSchema")
	}
	if _, ok := defs[0].InputSchema["environment"]; !ok {
		t.Error("environment field should be preserved")
	}
}

func TestDiscoverResultPassesToolsValidation(t *testing.T) {
	t.Parallel()
	// 确保转换结果能通过 tools.RegisterDynamicTools 的校验（environment 必需）。
	tools.ResetDynamicToolsForTest()
	defer tools.ResetDynamicToolsForTest()

	configs := []mcp.MCPServerConfig{{Name: "grafana", Command: "mcp-server"}}
	lister := fakeLister{tools: []mcp.MCPTool{
		{
			Name: "query",
			InputSchema: mcp.MCPInputSchema{
				Type: "object",
				Properties: map[string]mcp.MCPPropertySchema{
					"query": {Type: "string"},
				},
				Required: []string{"query"},
			},
		},
	}}

	defs, err := mcp.Discover(context.Background(), lister, configs)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if err := tools.RegisterDynamicTools(defs); err != nil {
		t.Fatalf("RegisterDynamicTools rejected discovered tools: %v", err)
	}
	// 验证工具已注册
	tool, ok := tools.Lookup("grafana.query")
	if !ok {
		t.Fatal("Lookup grafana.query failed after registration")
	}
	if !reflect.DeepEqual(tool.Operation, tools.Read) {
		t.Errorf("Operation = %v, want Read", tool.Operation)
	}
}
