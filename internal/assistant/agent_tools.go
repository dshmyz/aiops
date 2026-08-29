package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// CapabilityTool 把一个动态能力（YAML 发布的 HTTP 工具）包装为 eino tool.InvokableTool，
// 让 LLM 通过原生 function calling 调用它。
type CapabilityTool struct {
	cap     capabilities.Capability
	adapter *capabilities.HTTPAdapter
	audit   *audit.Service
	user    identity.CurrentUser
}

// NewCapabilityTool 创建一个 eino tool 包装器。
// user 参数保留为 fallback（context 中没有身份时使用），
// 实际执行时优先使用 context 中的真实请求者身份。
func NewCapabilityTool(cap capabilities.Capability, adapter *capabilities.HTTPAdapter, auditSvc *audit.Service, user identity.CurrentUser) *CapabilityTool {
	return &CapabilityTool{cap: cap, adapter: adapter, audit: auditSvc, user: user}
}

// Info 返回工具元数据，LLM 用它来决定是否调用此工具。
func (t *CapabilityTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	// 把 capabilities.InputSchema 转为 eino 的 JSON schema
	params := inputSchemaToJSONSchema(t.cap.InputSchema)
	return &schema.ToolInfo{
		Name:        t.cap.Name,
		Desc:        t.cap.AI.Description,
		ParamsOneOf: params,
	}, nil
}

type userContextKey struct{}

// WithToolUser 把请求者身份放入 context，供 CapabilityTool.InvokableRun 读取。
func WithToolUser(ctx context.Context, user identity.CurrentUser) context.Context {
	return context.WithValue(ctx, userContextKey{}, user)
}

// toolUserFromContext 从 context 读取请求者身份。
func toolUserFromContext(ctx context.Context) (identity.CurrentUser, bool) {
	user, ok := ctx.Value(userContextKey{}).(identity.CurrentUser)
	return user, ok
}

// ToolUserFromContext 返回 WithToolUser 注入的请求者身份。供宿主进程的
// 自定义工具包装器（如内置数据源直连工具）读取，与 CapabilityTool 同一来源，
// 保证策略检查与审计归因使用同一份身份。
func ToolUserFromContext(ctx context.Context) (identity.CurrentUser, bool) {
	return toolUserFromContext(ctx)
}

// InvokableRun 执行工具调用：策略检查 → HTTP 执行 → 审计 → 返回结果。
func (t *CapabilityTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	// 1. 解析 LLM 传来的 JSON 参数
	var input map[string]any
	if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
		return "", fmt.Errorf("parse tool arguments: %w", err)
	}

	// 1.5 信任等级检查：readonly 模式下写工具一律拒绝
	if t.cap.Operation == tools.Write && !AllowWrite() {
		return "", fmt.Errorf("trust level %q: write tools are disabled", GetTrustLevel())
	}

	// 2. 策略检查（优先用 context 中的真实请求者身份）
	caller, ok := toolUserFromContext(ctx)
	if !ok {
		// fallback：无 context 身份时用构造时传入的用户
		caller = t.user
	}
	toolDef := tools.Tool{
		Name:         t.cap.Name,
		Operation:    t.cap.Operation,
		Risk:         t.cap.Risk,
		Domain:       t.cap.Domain,
		ResourceType: t.cap.ResourceType,
	}
	decision := policy.Evaluate(caller, toolDef, input)
	if !decision.Allowed {
		return "", fmt.Errorf("policy denied: %s", decision.Reason)
	}
	// 3. RequiresConfirmation：写工具在两个信任等级（confirm/auto）下都必须先人工
	// 确认，policy 只标记不执行。等待确认的职责在 Service 写门（agentWriteStep）
	// 和 executor 写门（executorWriteGate）——它们会建 pending plan 交还给人；但任何
	// 绕开写门直接 InvokableRun 的路径都必须对 confirm 级写 fail-closed 拒绝，否则
	// "写要在人工确认处停下"这一承诺会在活跃执行路径上被静默绕过（直接执行）。
	if decision.RequiresConfirmation {
		return "", fmt.Errorf("write requires human confirmation: %s (create a plan for approval)", t.cap.Name)
	}

	// 3. 执行 HTTP 调用
	result, err := t.adapter.Execute(ctx, t.cap, input)
	if err != nil {
		// 失败也留档（便于排查）：记录输入、错误，以及后端失败时带出的脱敏响应体。
		if t.audit != nil {
			meta := map[string]any{"input": input, "result": "error", "error": err.Error()}
			var be *capabilities.BackendError
			if errors.As(err, &be) {
				meta["response_raw"] = be.BodyRedacted
				meta["status_code"] = be.StatusCode
			}
			_ = t.audit.Record(ctx, audit.Event{
				ID:        audit.NewEventID(),
				Action:    audit.ActionToolExecuted,
				Decision:  audit.DecisionPermitted,
				Subject:   caller.Subject,
				RequestID: caller.RequestID,
				ToolName:  t.cap.Name,
				Metadata:  meta,
			})
		}
		return "", fmt.Errorf("execute %s: %w", t.cap.Name, err)
	}

	// 4. 审计记录（绑定执行者身份，使 tool_executed 可归因）
	if t.audit != nil {
		_ = t.audit.Record(ctx, audit.Event{
			ID:        audit.NewEventID(),
			Action:    audit.ActionToolExecuted,
			Decision:  audit.DecisionPermitted,
			Subject:   caller.Subject,
			RequestID: caller.RequestID,
			ToolName:  t.cap.Name,
			Metadata: map[string]any{
				"input":        input,
				"output":       result.Data,
				"summary":      result.Summary,
				"severity":     result.Severity,
				"response_raw": result.Raw,
				"result":       "ok",
			},
		})
	}

	// 5. 返回 JSON 结果给 LLM
	output := map[string]any{
		"tool":     t.cap.Name,
		"severity": result.Severity,
		"summary":  result.Summary,
		"data":     result.Data,
		"resource": result.Resource,
	}
	jsonBytes, err := json.Marshal(output)
	if err != nil {
		return result.Summary, nil
	}
	return string(jsonBytes), nil
}

// inputSchemaToJSONSchema 把 capabilities.InputField 转为 eino 的 ParamsOneOf JSON schema。
func inputSchemaToJSONSchema(inputSchema map[string]capabilities.InputField) *schema.ParamsOneOf {
	params := map[string]*schema.ParameterInfo{}
	for name, field := range inputSchema {
		info := &schema.ParameterInfo{
			Type:     schema.DataType(field.Type),
			Desc:     field.Description,
			Required: field.Required,
		}
		if field.Enum != nil {
			info.Enum = field.Enum
		}
		params[name] = info
	}
	return schema.NewParamsOneOfByParams(params)
}

// CapabilityToolsFromCapabilities 把一批动态能力转换为 eino tool 列表。
func CapabilityToolsFromCapabilities(
	caps []capabilities.Capability,
	adapter *capabilities.HTTPAdapter,
	auditSvc *audit.Service,
	user identity.CurrentUser,
) []tool.BaseTool {
	tools := make([]tool.BaseTool, 0, len(caps))
	for _, cap := range caps {
		if cap.Status != capabilities.StatusPublished {
			continue
		}
		tools = append(tools, NewCapabilityTool(cap, adapter, auditSvc, user))
	}
	return tools
}

// --- 通用运维工具 ---

// HTTPProbeTool 通用 HTTP 探活工具：对任意 URL 发请求，返回状态码、响应时间、TLS 证书等。
// 注意：不发请求时无状态，每次调用按需 new 一个带超时的 client（超时可随参数变化）。
// 仅供 HealthChecker 巡检操作者显式配置的端点——属于可信运维配置，无需 SSRF 防线。
type HTTPProbeTool struct{}

func NewInternalHTTPProbeTool() *HTTPProbeTool {
	return &HTTPProbeTool{}
}

func (t *HTTPProbeTool) InvokableRun(ctx context.Context, argumentsInJSON string) (string, error) {
	var args struct {
		URL            string `json:"url"`
		Method         string `json:"method"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("parse arguments: %w", err)
	}
	if args.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	if args.Method == "" {
		args.Method = "GET"
	}
	if args.TimeoutSeconds <= 0 {
		args.TimeoutSeconds = 10
	}

	start := time.Now()
	client := &http.Client{Timeout: time.Duration(args.TimeoutSeconds) * time.Second}
	req, err := http.NewRequest(args.Method, args.URL, nil)
	if err != nil {
		return "", fmt.Errorf("invalid request: %w", err)
	}
	req.Header.Set("User-Agent", "aiops-probe/1.0")
	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return fmt.Sprintf(`{"url":"%s","status":"error","error":"%s","latency_ms":%d}`, args.URL, err.Error(), latency.Milliseconds()), nil
	}
	defer resp.Body.Close()

	// 读取响应体（限制 1KB）
	limited := io.LimitReader(resp.Body, 1024)
	body, _ := io.ReadAll(limited)
	bodyPreview := string(body)
	if len(bodyPreview) > 500 {
		bodyPreview = bodyPreview[:500] + "..."
	}

	// TLS 证书信息
	certInfo := ""
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		cert := resp.TLS.PeerCertificates[0]
		certInfo = fmt.Sprintf(`,"tls_cert_expiry":"%s","tls_cert_issuer":"%s"`, cert.NotAfter.Format(time.RFC3339), cert.Issuer.CommonName)
	}

	result := fmt.Sprintf(`{
		"url": "%s",
		"method": "%s",
		"status_code": %d,
		"status_text": "%s",
		"latency_ms": %d,
		"content_length": %d,
		"content_type": "%s",
		"body_preview": "%s"%s
	}`,
		args.URL, args.Method, resp.StatusCode, resp.Status, latency.Milliseconds(),
		resp.ContentLength, resp.Header.Get("Content-Type"), escapeJSON(bodyPreview), certInfo)
	return result, nil
}

func escapeJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1]) // strip surrounding quotes
}
