package mcp

import (
	"context"
	"fmt"
	"time"
)

// HealthStatus 描述外部 MCP 服务器的健康状态。
type HealthStatus string

const (
	// HealthStatusHealthy 表示服务器连接成功且暴露了至少一个工具。
	HealthStatusHealthy HealthStatus = "healthy"
	// HealthStatusDegraded 表示服务器连接成功但未暴露任何工具（服务器存活但能力缺失）。
	HealthStatusDegraded HealthStatus = "degraded"
	// HealthStatusUnhealthy 表示服务器连接失败（进程无法启动、握手失败、超时等）。
	HealthStatusUnhealthy HealthStatus = "unhealthy"
)

// HealthReport 是单次健康检查的结果。
type HealthReport struct {
	ServerName string
	Status     HealthStatus
	Latency    time.Duration
	ToolCount  int
	Error      string
	CheckedAt  time.Time
}

// HealthChecker 对外部 MCP 服务器执行健康检查（借鉴-6）。
// 它复用 ToolLister 连接服务器：List 调用成功即说明服务器存活，
// 同时测量响应延迟和工具数量。连接成功但工具数为 0 标记为 degraded，
// 连接失败标记为 unhealthy。
type HealthChecker interface {
	HealthCheck(ctx context.Context, config MCPServerConfig) HealthReport
	HealthCheckAll(ctx context.Context, configs []MCPServerConfig) []HealthReport
	// WithEventEmitter 注入事件发射回调，返回新的 HealthChecker。
	// 健康检查 unhealthy/degraded 时通过回调发射 MCPEvent，供调用方
	// （main.go）转成审计事件记录。为 nil 时不发事件。
	WithEventEmitter(emit EmitFunc) HealthChecker
}

// healthChecker 是 HealthChecker 的默认实现，基于 ToolLister。
type healthChecker struct {
	lister ToolLister
	clock  func() time.Time
	// emitter 是可选的事件发射回调。健康检查 unhealthy/degraded 时触发事件，
	// 供调用方记录审计。为 nil 时不发事件。
	emitter EmitFunc
}

// NewHealthChecker 创建一个基于 lister 的健康检查器。
// lister 不能为 nil。
func NewHealthChecker(lister ToolLister) HealthChecker {
	if lister == nil {
		panic("mcp.NewHealthChecker: lister is nil")
	}
	return &healthChecker{lister: lister, clock: time.Now}
}

// WithEventEmitter 注入事件发射回调，返回新的 HealthChecker。
// 健康检查 unhealthy/degraded 时通过回调发射事件。为 nil 时不清除已设置的回调。
func (h *healthChecker) WithEventEmitter(emit EmitFunc) HealthChecker {
	h.emitter = emit
	return h
}

func (h *healthChecker) HealthCheck(ctx context.Context, config MCPServerConfig) HealthReport {
	start := time.Now()
	tools, err := h.lister.List(ctx, config)
	latency := time.Since(start)

	report := HealthReport{
		ServerName: config.Name,
		Latency:    latency,
		CheckedAt:  h.clock(),
	}
	if err != nil {
		report.Status = HealthStatusUnhealthy
		report.Error = err.Error()
		h.emitEvent(MCPEvent{
			Type:       EventTypeHealthUnhealthy,
			ServerName: config.Name,
			Message:    fmt.Sprintf("health check failed: %s", err.Error()),
			Metadata: map[string]any{
				"latency_ms": latency.Milliseconds(),
				"error":      err.Error(),
			},
		})
		return report
	}
	report.ToolCount = len(tools)
	if report.ToolCount == 0 {
		report.Status = HealthStatusDegraded
		report.Error = fmt.Sprintf("server %q connected but exposed no tools", config.Name)
		h.emitEvent(MCPEvent{
			Type:       EventTypeHealthDegraded,
			ServerName: config.Name,
			Message:    report.Error,
			Metadata: map[string]any{
				"latency_ms": latency.Milliseconds(),
				"tool_count": 0,
			},
		})
		return report
	}
	report.Status = HealthStatusHealthy
	return report
}

func (h *healthChecker) HealthCheckAll(ctx context.Context, configs []MCPServerConfig) []HealthReport {
	reports := make([]HealthReport, 0, len(configs))
	for _, config := range configs {
		reports = append(reports, h.HealthCheck(ctx, config))
	}
	return reports
}

// emitEvent 是 nil-safe 的事件发射辅助函数。
func (h *healthChecker) emitEvent(event MCPEvent) {
	if h.emitter != nil {
		h.emitter(event)
	}
}
