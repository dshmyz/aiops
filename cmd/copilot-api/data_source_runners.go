package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/alert"
	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/mcp"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// --- system.posture.read / cluster.status.read：真实数据源聚合 --------------

// alertLister 是态势聚合需要的最小告警查询接口（*alert.Service 满足），
// 抽象成接口便于单测注入。
type alertLister interface {
	ListActive(ctx context.Context, limit int) ([]alert.Alert, error)
}

// probeSource 是态势聚合需要的最小巡检历史接口（*assistant.KnowledgeStore 满足）。
type probeSource interface {
	RecentProbes(ctx context.Context, limit int) ([]map[string]any, error)
}

// postureReadRunner 聚合告警、巡检历史与已发布能力，回答
// system.posture.read / cluster.status.read——修复前这两个静态工具只能如实
// 返回 "no data sources configured"，默认安装下智能体对平台状态一无所知。
// 数据缺失（无告警源/无巡检/无能力）时按缺失维度如实标注 unknown，不伪造。
type postureReadRunner struct {
	alerts       alertLister // 可为 nil
	probes       probeSource // 可为 nil
	capabilities []capabilities.Capability
}

func (r postureReadRunner) Read(ctx context.Context, tool tools.Tool, input map[string]any) (map[string]any, error) {
	if tool.Name != tools.QuerySystemPosture && tool.Name != tools.ClusterStatusRead {
		return nil, fmt.Errorf("unsupported tool %q for posture read runner", tool.Name)
	}
	return r.aggregate(ctx, tool.Name)
}

func (r postureReadRunner) aggregate(ctx context.Context, toolName string) (map[string]any, error) {
	// 告警：按 severity 聚合活动告警，附最近 10 条。
	bySeverity := map[string]int{"critical": 0, "warning": 0, "info": 0}
	firingTotal := 0
	var recent []map[string]any
	alertsKnown := false
	if r.alerts != nil {
		if active, err := r.alerts.ListActive(ctx, 200); err == nil {
			alertsKnown = true
			for _, a := range active {
				if a.Status != "firing" {
					continue
				}
				firingTotal++
				if _, ok := bySeverity[string(a.Severity)]; ok {
					bySeverity[string(a.Severity)]++
				}
				if len(recent) < 10 {
					recent = append(recent, map[string]any{
						"id":            a.ID,
						"title":         a.Title,
						"severity":      string(a.Severity),
						"status":        a.Status,
						"domain":        a.Domain,
						"resource_name": a.ResourceName,
						"fired_at":      a.FiredAt,
					})
				}
			}
		}
	}

	// 巡检：最近记录 + 失败数。findings 由 HealthChecker 写入，格式
	// "巡检 <name>: <status>[ (<err>)]"——按自家写入格式解析 status 段。
	probeViews := []map[string]any{}
	probeErrors := 0
	probesKnown := false
	if r.probes != nil {
		if rows, err := r.probes.RecentProbes(ctx, 12); err == nil {
			probesKnown = true
			for _, row := range rows {
				name := strings.TrimPrefix(fmt.Sprint(row["title"]), "巡检: ")
				findings := fmt.Sprint(row["findings"])
				status := "unknown"
				if body, ok := strings.CutPrefix(findings, "巡检 "+name+": "); ok {
					status = strings.TrimSpace(body)
					if idx := strings.Index(status, " ("); idx >= 0 {
						status = status[:idx]
					}
				}
				if status == "error" {
					probeErrors++
				}
				probeViews = append(probeViews, map[string]any{
					"name":       name,
					"status":     status,
					"domain":     row["domain"],
					"checked_at": row["created_at"],
				})
			}
		}
	}

	// 能力：已发布只读能力按域计数。
	domainCounts := map[string]int{}
	publishedTotal := 0
	for _, cap := range r.capabilities {
		if cap.Status == capabilities.StatusPublished && cap.Operation == tools.Read {
			domainCounts[cap.Domain]++
			publishedTotal++
		}
	}

	// 总体态势：有 critical 告警 → critical；有 warning 告警或探活失败 →
	// warning；有数据且全正常 → healthy；三类数据源全部缺失 → unknown。
	overall := "unknown"
	hasSignal := alertsKnown || probesKnown || publishedTotal > 0
	if hasSignal {
		overall = "healthy"
		if bySeverity["critical"] > 0 {
			overall = "critical"
		} else if bySeverity["warning"] > 0 || probeErrors > 0 {
			overall = "warning"
		}
	}

	result := map[string]any{
		"tool":           toolName,
		"overall_status": overall,
		"generated_at":   time.Now().UTC().Format(time.RFC3339),
		"alerts": map[string]any{
			"firing_total": firingTotal,
			"by_severity":  bySeverity,
			"recent":       recent,
			"known":        alertsKnown,
		},
		"probes": map[string]any{
			"recent": probeViews,
			"errors": probeErrors,
			"known":  probesKnown,
		},
		"capabilities": map[string]any{
			"read_published": publishedTotal,
			"by_domain":      domainCounts,
		},
	}
	if toolName == tools.ClusterStatusRead {
		// 与既有 staticReadRunner 输出形状兼容：cluster.status.read 用 status 键。
		result["status"] = overall
	}
	return result, nil
}

// --- prometheus.query：PromQL 即时查询 -------------------------------------

// promReadRunner 对操作者配置的 Prometheus（COPILOT_PROMETHEUS_URL）执行
// PromQL 即时查询。未配置时如实返回 unconfigured——不伪造、也不报错阻断
// （工具可以被 LLM 看到，回答里说明缺配置比隐藏工具更诚实）。
type promReadRunner struct {
	baseURL string
	client  *http.Client
}

func newPromReadRunner(baseURL string) promReadRunner {
	return promReadRunner{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// maxPromSeries 是单次查询返回的序列数上限，防止 range 向量把 read 响应
// 撑破 10KB 边界。
const maxPromSeries = 50

func (r promReadRunner) Read(ctx context.Context, tool tools.Tool, input map[string]any) (map[string]any, error) {
	if tool.Name != tools.PrometheusQuery {
		return nil, fmt.Errorf("unsupported tool %q for prometheus read runner", tool.Name)
	}
	if r.baseURL == "" {
		return map[string]any{
			"tool":   tool.Name,
			"status": "unconfigured",
			"note":   "COPILOT_PROMETHEUS_URL is not set; metrics queries are unavailable",
		}, nil
	}
	query, _ := input["query"].(string)
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is required")
	}
	endpoint := r.baseURL + "/api/v1/query?query=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build prometheus request: %w", err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query prometheus: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query prometheus: HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []any             `json:"value"` // [timestamp, "value"]
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode prometheus response: %w", err)
	}
	if payload.Status != "success" {
		return nil, fmt.Errorf("prometheus query %q failed: status=%s", query, payload.Status)
	}
	series := make([]map[string]any, 0, len(payload.Data.Result))
	truncated := false
	for i, s := range payload.Data.Result {
		if i >= maxPromSeries {
			truncated = true
			break
		}
		entry := map[string]any{"metric": s.Metric}
		if len(s.Value) == 2 {
			entry["timestamp"] = s.Value[0]
			entry["value"] = s.Value[1]
		}
		series = append(series, entry)
	}
	result := map[string]any{
		"tool":        tool.Name,
		"query":       query,
		"result_type": payload.Data.ResultType,
		"series":      series,
		"count":       len(payload.Data.Result),
	}
	if truncated {
		result["note"] = fmt.Sprintf("showing first %d of %d series", maxPromSeries, len(payload.Data.Result))
	}
	return result, nil
}

// --- MCP 工具执行路由 -------------------------------------------------------

// mcpReadRunner 把外部 MCP 服务器拥有的动态工具路由到真实 tools/call 执行，
// 其余工具透传给 inner（capability runner / 静态兜底）。修复前 MCP 工具
// "可见不可用"：注册进了工具表，但执行落到 staticReadRunner 的
// {"status":"unavailable"}，违反平台的如实披露原则。
type mcpReadRunner struct {
	manager *mcp.Manager
	inner   execution.ReadRunner
}

func (r mcpReadRunner) Read(ctx context.Context, tool tools.Tool, input map[string]any) (map[string]any, error) {
	if r.manager != nil && r.manager.OwnsTool(tool.Name) {
		return r.manager.Call(ctx, tool.Name, input)
	}
	return r.inner.Read(ctx, tool, input)
}

// 编译期接口断言：KnowledgeStore 必须满足 probeSource。
var _ probeSource = (*assistant.KnowledgeStore)(nil)
