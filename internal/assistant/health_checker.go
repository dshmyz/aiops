package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// HealthChecker 定期检查已注册的端点健康状态。
type HealthChecker struct {
	probe     *HTTPProbeTool
	store     *KnowledgeStore
	interval  time.Duration
	endpoints []HealthEndpoint
}

// HealthEndpoint 需要定期检查的端点。
type HealthEndpoint struct {
	Name    string // 端点名称
	URL     string // 探活 URL
	Domain  string // 关联的 domain
	Enabled bool   // 是否启用
}

func NewHealthChecker(probe *HTTPProbeTool, store *KnowledgeStore, interval time.Duration) *HealthChecker {
	return &HealthChecker{
		probe:    probe,
		store:    store,
		interval: interval,
	}
}

// RegisterEndpoint 注册一个需要巡检的端点。
func (h *HealthChecker) RegisterEndpoint(name, url, domain string) {
	h.endpoints = append(h.endpoints, HealthEndpoint{
		Name:    name,
		URL:     url,
		Domain:  domain,
		Enabled: true,
	})
}

// Start 启动定期巡检。在后台 goroutine 中运行，通过 ctx 取消。
func (h *HealthChecker) Start(ctx context.Context) {
	if h.interval <= 0 {
		h.interval = 5 * time.Minute
	}
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	log.Printf("[health-checker] started, checking %d endpoints every %v", len(h.endpoints), h.interval)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[health-checker] stopped")
			return
		case <-ticker.C:
			h.checkAll(ctx)
		}
	}
}

// checkAll 检查所有注册的端点。
func (h *HealthChecker) checkAll(ctx context.Context) {
	for _, ep := range h.endpoints {
		if !ep.Enabled {
			continue
		}
		h.checkEndpoint(ctx, ep)
	}
}

// checkEndpoint 检查单个端点的健康状态。
func (h *HealthChecker) checkEndpoint(ctx context.Context, ep HealthEndpoint) {
	args, _ := json.Marshal(map[string]string{"url": ep.URL, "method": "GET"})
	result, err := h.probe.InvokableRun(ctx, string(args))
	if err != nil {
		log.Printf("[health-checker] %s failed: %v", ep.Name, err)
		h.recordResult(ctx, ep, "error", err.Error())
		return
	}

	var probeResult map[string]any
	_ = json.Unmarshal([]byte(result), &probeResult)

	// 默认 unknown：只有当 status_code 存在且判定健康时才记 healthy，
	// 避免探测响应不完整时把端点无证据地记为健康。
	status := "unknown"
	if statusCode, ok := probeResult["status_code"].(float64); ok {
		if statusCode >= 400 {
			status = "unhealthy"
		} else if statusCode >= 300 {
			status = "degraded"
		} else if statusCode >= 200 && statusCode < 300 {
			status = "healthy"
		}
	}
	if probeResult["status"] == "error" {
		status = "error"
	}

	log.Printf("[health-checker] %s: %s (status_code=%v)", ep.Name, status, probeResult["status_code"])
	h.recordResult(ctx, ep, status, "")
}

// recordResult 记录巡检结果到知识库。
func (h *HealthChecker) recordResult(ctx context.Context, ep HealthEndpoint, status, errMsg string) {
	if h.store == nil {
		return
	}
	findings := fmt.Sprintf("巡检 %s: %s", ep.Name, status)
	if errMsg != "" {
		findings += fmt.Sprintf(" (%s)", errMsg)
	}
	_ = h.store.Save(ctx, fmt.Sprintf("巡检: %s", ep.Name), ep.Domain, []string{"http.probe"}, findings, "")
}
