package mcp_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/mcp"
)

// --- HealthChecker 测试 ---

// slowLister 模拟延迟的 ToolLister，用于测试健康检查的 latency 测量。
type slowLister struct {
	tools []mcp.MCPTool
	delay time.Duration
}

func (s slowLister) List(_ context.Context, _ mcp.MCPServerConfig) ([]mcp.MCPTool, error) {
	time.Sleep(s.delay)
	return s.tools, nil
}

func TestHealthCheckReportsHealthyWhenListSucceeds(t *testing.T) {
	t.Parallel()
	config := mcp.MCPServerConfig{Name: "grafana", Command: "mcp-server"}
	lister := fakeLister{tools: []mcp.MCPTool{
		{Name: "query_metrics"},
		{Name: "query_logs"},
	}}
	checker := mcp.NewHealthChecker(lister)

	report := checker.HealthCheck(context.Background(), config)

	if report.ServerName != "grafana" {
		t.Errorf("ServerName = %q, want grafana", report.ServerName)
	}
	if report.Status != mcp.HealthStatusHealthy {
		t.Errorf("Status = %q, want %q", report.Status, mcp.HealthStatusHealthy)
	}
	if report.ToolCount != 2 {
		t.Errorf("ToolCount = %d, want 2", report.ToolCount)
	}
	if report.Error != "" {
		t.Errorf("Error = %q, want empty for healthy server", report.Error)
	}
	if report.Latency <= 0 {
		t.Error("Latency should be positive for a successful check")
	}
	if report.CheckedAt.IsZero() {
		t.Error("CheckedAt should be set")
	}
}

func TestHealthCheckReportsUnhealthyWhenListFails(t *testing.T) {
	t.Parallel()
	config := mcp.MCPServerConfig{Name: "broken", Command: "mcp-broken"}
	lister := fakeLister{err: errors.New("connection refused")}
	checker := mcp.NewHealthChecker(lister)

	report := checker.HealthCheck(context.Background(), config)

	if report.Status != mcp.HealthStatusUnhealthy {
		t.Errorf("Status = %q, want %q", report.Status, mcp.HealthStatusUnhealthy)
	}
	if report.Error == "" {
		t.Error("Error should be non-empty for unhealthy server")
	}
	if report.ToolCount != 0 {
		t.Errorf("ToolCount = %d, want 0 for failed check", report.ToolCount)
	}
}

func TestHealthCheckMeasuresLatency(t *testing.T) {
	t.Parallel()
	config := mcp.MCPServerConfig{Name: "slow", Command: "mcp-slow"}
	lister := slowLister{tools: []mcp.MCPTool{{Name: "tool1"}}, delay: 20 * time.Millisecond}
	checker := mcp.NewHealthChecker(lister)

	report := checker.HealthCheck(context.Background(), config)

	if report.Status != mcp.HealthStatusHealthy {
		t.Errorf("Status = %q, want healthy", report.Status)
	}
	// 延迟应至少 20ms（允许一定误差，不设上界避免 flaky）
	if report.Latency < 15*time.Millisecond {
		t.Errorf("Latency = %v, want >= 15ms", report.Latency)
	}
}

func TestHealthCheckReportsDegradedWhenNoTools(t *testing.T) {
	t.Parallel()
	config := mcp.MCPServerConfig{Name: "empty", Command: "mcp-empty"}
	lister := fakeLister{tools: []mcp.MCPTool{}}
	checker := mcp.NewHealthChecker(lister)

	report := checker.HealthCheck(context.Background(), config)

	// 连接成功但无工具，标记为 degraded（服务器存活但未暴露能力）
	if report.Status != mcp.HealthStatusDegraded {
		t.Errorf("Status = %q, want %q", report.Status, mcp.HealthStatusDegraded)
	}
	if report.ToolCount != 0 {
		t.Errorf("ToolCount = %d, want 0", report.ToolCount)
	}
}

// --- HealthChecker 批量检查测试 ---

func TestHealthCheckAllChecksMultipleServers(t *testing.T) {
	t.Parallel()
	configs := []mcp.MCPServerConfig{
		{Name: "grafana", Command: "mcp-grafana"},
		{Name: "broken", Command: "mcp-broken"},
	}
	// fakeLister 对所有服务器返回相同结果；用一个能区分的 lister
	lister := &perServerLister{
		byName: map[string]fakeLister{
			"grafana": {tools: []mcp.MCPTool{{Name: "query"}}},
			"broken":  {err: errors.New("connection refused")},
		},
	}
	checker := mcp.NewHealthChecker(lister)

	reports := checker.HealthCheckAll(context.Background(), configs)

	if len(reports) != 2 {
		t.Fatalf("reports len = %d, want 2", len(reports))
	}
	byName := map[string]mcp.HealthReport{}
	for _, r := range reports {
		byName[r.ServerName] = r
	}
	if byName["grafana"].Status != mcp.HealthStatusHealthy {
		t.Errorf("grafana status = %q, want healthy", byName["grafana"].Status)
	}
	if byName["broken"].Status != mcp.HealthStatusUnhealthy {
		t.Errorf("broken status = %q, want unhealthy", byName["broken"].Status)
	}
}

// perServerLister 按服务器名返回不同结果，用于批量健康检查测试。
type perServerLister struct {
	byName map[string]fakeLister
}

func (p *perServerLister) List(_ context.Context, config mcp.MCPServerConfig) ([]mcp.MCPTool, error) {
	if l, ok := p.byName[config.Name]; ok {
		return l.List(context.Background(), config)
	}
	return nil, errors.New("unknown server")
}
