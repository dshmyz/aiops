package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// 内置数据源直连工具的 LLM 暴露层。
//
// 修复前 AgentExecutor 的 LLM 工具集只含 published capabilities——alert.query、
// incident.view、system.posture.read 这些治理完备的静态数据源工具只在旧的
// planner 路径可达，主执行路径上的模型根本看不见它们。这里把同一 readRunner
// 链（Lookup→ValidateInput→policy→runner→audit，与只读 HTTP 端点完全同源）
// 包装成 eino 工具注入 executor，让模型在主路径上也能查告警、看事件、看全景。

// staticToolParam 描述一个工具参数（供 LLM schema 与文档共用）。
type staticToolParam struct {
	Type        string
	Description string
	Required    bool
	Enum        []string
}

// staticAgentToolSpec 是一个要暴露给 AgentExecutor 的内置只读工具。输入
// schema 必须与 tools.ValidateInput 对该工具的校验一致。
type staticAgentToolSpec struct {
	Name   string
	Desc   string
	Params map[string]staticToolParam
}

// staticAgentToolSpecs 是暴露给主执行路径的内置只读数据源工具清单。
var staticAgentToolSpecs = []staticAgentToolSpec{
	{
		Name: tools.AlertQuery,
		Desc: "查询当前活动告警列表，返回告警标题、严重级别、状态、域与触发时间。用于回答\"现在有哪些告警/有什么问题在响\"。",
		Params: map[string]staticToolParam{
			"severity": {Type: "string", Description: "按严重级别过滤", Enum: []string{"info", "warning", "critical"}},
			"status":   {Type: "string", Description: "按状态过滤", Enum: []string{"firing", "resolved"}},
			"domain":   {Type: "string", Description: "按域过滤（如 kafka、minio）"},
		},
	},
	{
		Name: tools.EventQuery,
		Desc: "查询审计事件中心，支持自然语言查询（如\"上周谁拒绝了 plan\"、\"今天执行过哪些写操作\"）。用于回答\"谁在什么时候做过什么\"。",
		Params: map[string]staticToolParam{
			"query": {Type: "string", Description: "自然语言或关键词查询", Required: true},
		},
	},
	{
		Name: tools.TaskQuery,
		Desc: "查询定时巡检任务及其最近执行历史。用于回答\"有哪些定时任务、跑得怎么样\"。",
		Params: map[string]staticToolParam{
			"status": {Type: "string", Description: "按启用状态过滤", Enum: []string{"enabled", "disabled"}},
			"limit":  {Type: "integer", Description: "返回条数上限（默认 20，最大 100）"},
		},
	},
	{
		Name: tools.IncidentView,
		Desc: "告警全景：给定一个告警或资源身份，把告警本体、相关审计事件、定时巡检、可跑只读能力与匹配 runbook 串成一张全景。用于回答\"这个告警牵扯了什么、最近改过什么、怎么处置\"。",
		Params: map[string]staticToolParam{
			"domain":        {Type: "string", Description: "域（如 kafka），可选"},
			"resource_type": {Type: "string", Description: "资源类型（如 consumer_group），可选"},
			"resource_name": {Type: "string", Description: "资源名，可选"},
		},
	},
	{
		Name: tools.QuerySystemPosture,
		Desc: "系统态势总览：聚合活动告警分布、最近探活结果与已发布能力概况，给出整体健康判断（healthy/warning/critical）。用于回答\"系统现在整体怎么样\"。",
	},
	{
		Name: tools.ClusterStatusRead,
		Desc: "集群整体状态：聚合告警与探活信号给出总体状态。用于回答\"集群健康吗\"。",
	},
	{
		Name: tools.PrometheusQuery,
		Desc: "执行一次 PromQL 即时查询（需已配置 COPILOT_PROMETHEUS_URL）。用于回答指标类问题（QPS、延迟、资源使用率等）。未配置时返回 unconfigured 说明。",
		Params: map[string]staticToolParam{
			"query": {Type: "string", Description: "PromQL 表达式，如 up{job=\"kafka\"} 或 sum(rate(http_requests_total[5m]))", Required: true},
		},
	},
}

// buildStaticAgentTools 把内置只读数据源工具包装成 eino 工具，供
// AgentExecutor 的 LLM 工具集使用。所有调用走与只读 HTTP 端点相同的
// readRunner 治理链。
func buildStaticAgentTools(runner execution.ReadRunner, auditSvc *audit.Service) []tool.BaseTool {
	out := make([]tool.BaseTool, 0, len(staticAgentToolSpecs))
	for _, spec := range staticAgentToolSpecs {
		out = append(out, &staticReadTool{spec: spec, runner: runner, audit: auditSvc})
	}
	return out
}

// staticReadTool 是一个内置只读工具的 eino 包装：解析 LLM 参数 →
// registry 校验 → policy 检查 → readRunner 执行 → 审计。
type staticReadTool struct {
	spec   staticAgentToolSpec
	runner execution.ReadRunner
	audit  *audit.Service
}

// Info 返回工具元数据，LLM 用它来决定是否调用此工具。
func (t *staticReadTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	toolDef, ok := tools.Lookup(t.spec.Name)
	if !ok {
		return nil, fmt.Errorf("static agent tool %q is not registered", t.spec.Name)
	}
	params := map[string]*schema.ParameterInfo{}
	for name, p := range t.spec.Params {
		info := &schema.ParameterInfo{
			Type:     schema.DataType(p.Type),
			Desc:     p.Description,
			Required: p.Required,
		}
		if p.Enum != nil {
			info.Enum = p.Enum
		}
		params[name] = info
	}
	return &schema.ToolInfo{
		Name:        toolDef.Name,
		Desc:        t.spec.Desc,
		ParamsOneOf: schema.NewParamsOneOfByParams(params),
	}, nil
}

// InvokableRun 执行只读工具调用，治理链与只读 HTTP 端点完全同源。
func (t *staticReadTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var input map[string]any
	if len(argumentsInJSON) > 0 {
		if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
			return "", fmt.Errorf("parse tool arguments: %w", err)
		}
	}

	toolDef, ok := tools.Lookup(t.spec.Name)
	if !ok {
		return "", fmt.Errorf("tool %q is not registered", t.spec.Name)
	}
	// 输入校验：与 registry 的固定 schema 一致，LLM 传参不能越界。
	if err := tools.ValidateInput(toolDef, input); err != nil {
		return "", fmt.Errorf("invalid arguments for %s: %w", t.spec.Name, err)
	}
	// 策略检查：与 CapabilityTool 相同的身份来源（ctx 注入的真实请求者）。
	caller, ok := assistant.ToolUserFromContext(ctx)
	if !ok {
		// 与 CapabilityTool 一致：空角色在 policy 里一律拒绝，fail-closed。
		caller = identity.CurrentUser{}
	}
	decision := policy.Evaluate(caller, toolDef, input)
	if !decision.Allowed {
		return "", fmt.Errorf("policy denied: %s", decision.Reason)
	}

	result, err := t.runner.Read(ctx, toolDef, input)
	if err != nil {
		if t.audit != nil {
			_ = t.audit.Record(ctx, audit.Event{
				ID:        audit.NewEventID(),
				Action:    audit.ActionToolExecuted,
				Decision:  audit.DecisionPermitted,
				Subject:   caller.Subject,
				RequestID: caller.RequestID,
				ToolName:  t.spec.Name,
				Metadata:  map[string]any{"input": input, "result": "error", "error": err.Error()},
			})
		}
		return "", fmt.Errorf("execute %s: %w", t.spec.Name, err)
	}
	if t.audit != nil {
		_ = t.audit.Record(ctx, audit.Event{
			ID:        audit.NewEventID(),
			Action:    audit.ActionToolExecuted,
			Decision:  audit.DecisionPermitted,
			Subject:   caller.Subject,
			RequestID: caller.RequestID,
			ToolName:  t.spec.Name,
			Metadata:  map[string]any{"input": input, "result": "ok", "output": result},
		})
	}
	out, err := json.Marshal(map[string]any{"tool": t.spec.Name, "data": result})
	if err != nil {
		return fmt.Sprint(result), nil
	}
	return string(out), nil
}
