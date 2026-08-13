package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
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

	// 3. 执行 HTTP 调用
	result, err := t.adapter.Execute(ctx, t.cap, input)
	if err != nil {
		return "", fmt.Errorf("execute %s: %w", t.cap.Name, err)
	}

	// 4. 审计记录
	if t.audit != nil {
		_ = t.audit.Record(ctx, audit.Event{
			Action:   "tool_executed",
			ToolName: t.cap.Name,
			Metadata: map[string]any{
				"input":  input,
				"status": result.Severity,
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
type HTTPProbeTool struct {
	client *http.Client
}

func NewHTTPProbeTool() *HTTPProbeTool {
	return &HTTPProbeTool{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (t *HTTPProbeTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: tools.HTTPProbe,
		Desc: "对任意 HTTP/HTTPS 端点发送请求，检查健康状态。返回状态码、响应时间、响应体摘要、TLS 证书到期时间等。用于探活、健康检查、API 可用性验证。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"url": {
				Desc:     "要探测的完整 URL（必须以 http:// 或 https:// 开头）",
				Required: true,
				Type:     schema.String,
			},
			"method": {
				Desc:     "HTTP 方法，默认 GET",
				Required: false,
				Type:     schema.String,
			},
			"timeout_seconds": {
				Desc:     "超时时间（秒），默认 10",
				Required: false,
				Type:     schema.Integer,
			},
		}),
	}, nil
}

func (t *HTTPProbeTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
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

	// 发请求（校验 URL 防止 SSRF 和 nil dereference）
	start := time.Now()
	client := &http.Client{Timeout: time.Duration(args.TimeoutSeconds) * time.Second}
	if err := validateProbeURL(args.URL); err != nil {
		return "", err
	}
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

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		return &url.URL{Path: raw}
	}
	return u
}

// validateProbeURL 校验探测 URL，防止 SSRF 攻击。
func validateProbeURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q, only http/https allowed", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("missing host")
	}
	// 阻止访问内部网络
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsPrivate() || ip.IsUnspecified() {
			return fmt.Errorf("internal address %q blocked", host)
		}
	}
	// 阻止知名内部域名
	for _, blocked := range []string{"localhost", "metadata.google.internal", "169.254.169.254"} {
		if host == blocked || strings.HasSuffix(host, "."+blocked) {
			return fmt.Errorf("internal host %q blocked", host)
		}
	}
	return nil
}

func escapeJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1]) // strip surrounding quotes
}

// --- Prometheus 工具 ---

type PrometheusQueryTool struct {
	baseURL string
	client  *http.Client
}

func NewPrometheusQueryTool(baseURL string) *PrometheusQueryTool {
	return &PrometheusQueryTool{baseURL: baseURL, client: &http.Client{Timeout: 30 * time.Second}}
}

func (t *PrometheusQueryTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: tools.PrometheusQuery,
		Desc: "执行 PromQL 查询，获取 Prometheus 监控指标。用于检查 CPU、内存、磁盘、网络、请求量、错误率等系统指标。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query":    {Desc: "PromQL 查询语句", Required: true, Type: schema.String},
			"duration": {Desc: "查询时间范围，如 '1h'、'24h'，默认 '1h'", Required: false, Type: schema.String},
		}),
	}, nil
}

func (t *PrometheusQueryTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Query    string `json:"query"`
		Duration string `json:"duration"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("parse arguments: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	if args.Duration == "" {
		args.Duration = "1h"
	}
	end := time.Now().Unix()
	start := end - 3600 // 1h default
	if d, err := time.ParseDuration(args.Duration); err == nil {
		start = end - int64(d.Seconds())
	}
	url := fmt.Sprintf("%s/api/v1/query_range?query=%s&start=%d&end=%d&step=60", t.baseURL, url.QueryEscape(args.Query), start, end)
	resp, err := t.client.Get(url)
	if err != nil {
		return "", fmt.Errorf("prometheus query: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	return string(body), nil
}

// --- K8s 工具 ---

type K8sPodListTool struct {
	client *http.Client
}

func NewK8sPodListTool() *K8sPodListTool {
	return &K8sPodListTool{client: &http.Client{Timeout: 10 * time.Second}}
}

func (t *K8sPodListTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: tools.K8sPodList,
		Desc: "列出 Kubernetes Pod 状态。用于检查 Pod 是否正常运行、是否有重启、OOM 等问题。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"namespace": {Desc: "K8s 命名空间，默认 'default'", Required: false, Type: schema.String},
			"label_selector": {Desc: "标签选择器，如 'app=nginx'", Required: false, Type: schema.String},
		}),
	}, nil
}

func (t *K8sPodListTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Namespace     string `json:"namespace"`
		LabelSelector string `json:"label_selector"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("parse arguments: %w", err)
	}
	if args.Namespace == "" {
		args.Namespace = "default"
	}
	// 通过 kubectl 代理或 API server 获取
	// 这里用环境变量 K8S_API_SERVER 配置 API 地址
	apiServer := os.Getenv("K8S_API_SERVER")
	if apiServer == "" {
		return `{"error":"K8S_API_SERVER not configured","hint":"set K8S_API_SERVER env var to your Kubernetes API server URL"}`, nil
	}
	apiURL := fmt.Sprintf("%s/api/v1/namespaces/%s/pods", apiServer, args.Namespace)
	if args.LabelSelector != "" {
		apiURL += "?labelSelector=" + url.QueryEscape(args.LabelSelector)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", err
	}
	// 如果有 service account token
	if token := os.Getenv("K8S_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("k8s api: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	return string(body), nil
}

type K8sPodLogsTool struct {
	client *http.Client
}

func NewK8sPodLogsTool() *K8sPodLogsTool {
	return &K8sPodLogsTool{client: &http.Client{Timeout: 10 * time.Second}}
}

func (t *K8sPodLogsTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: tools.K8sPodLogs,
		Desc: "获取 Kubernetes Pod 日志。用于排查 Pod 启动失败、OOM、应用报错等问题。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"pod_name":   {Desc: "Pod 名称", Required: true, Type: schema.String},
			"namespace":  {Desc: "命名空间，默认 'default'", Required: false, Type: schema.String},
			"container":  {Desc: "容器名（多容器 Pod 时指定）", Required: false, Type: schema.String},
			"tail_lines": {Desc: "返回最后 N 行，默认 100", Required: false, Type: schema.Integer},
		}),
	}, nil
}

func (t *K8sPodLogsTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		PodName   string `json:"pod_name"`
		Namespace string `json:"namespace"`
		Container string `json:"container"`
		TailLines int    `json:"tail_lines"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("parse arguments: %w", err)
	}
	if args.PodName == "" {
		return "", fmt.Errorf("pod_name is required")
	}
	if args.Namespace == "" {
		args.Namespace = "default"
	}
	if args.TailLines <= 0 {
		args.TailLines = 100
	}
	apiServer := os.Getenv("K8S_API_SERVER")
	if apiServer == "" {
		return `{"error":"K8S_API_SERVER not configured"}`, nil
	}
	apiURL := fmt.Sprintf("%s/api/v1/namespaces/%s/pods/%s/log?tailLines=%d", apiServer, args.Namespace, args.PodName, args.TailLines)
	if args.Container != "" {
		apiURL += "&container=" + url.QueryEscape(args.Container)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", err
	}
	if token := os.Getenv("K8S_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("k8s api: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	return string(body), nil
}

// --- 系统验证工具 ---

type SystemVerifyTool struct {
	probe *HTTPProbeTool
}

func NewSystemVerifyTool() *SystemVerifyTool {
	return &SystemVerifyTool{probe: NewHTTPProbeTool()}
}

func (t *SystemVerifyTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: tools.SystemVerify,
		Desc: "验证操作结果。在执行写操作（如修改配置、扩容、重启服务）后调用此工具确认变更是否生效。支持 HTTP 健康检查、指标查询等验证方式。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"url":         {Desc: "验证用的 HTTP 端点（健康检查）", Required: false, Type: schema.String},
			"query":       {Desc: "PromQL 查询（指标验证）", Required: false, Type: schema.String},
			"description": {Desc: "验证目的描述", Required: true, Type: schema.String},
		}),
	}, nil
}

func (t *SystemVerifyTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var args struct {
		URL         string `json:"url"`
		Query       string `json:"query"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("parse arguments: %w", err)
	}
	if args.Description == "" {
		return "", fmt.Errorf("description is required")
	}
	result := map[string]any{"description": args.Description, "verification_time": time.Now().Format(time.RFC3339)}
	if args.URL != "" {
		probeResp, err := t.probe.InvokableRun(ctx, fmt.Sprintf(`{"url":"%s","method":"GET"}`, args.URL), opts...)
		if err != nil {
			result["http_check"] = map[string]any{"status": "error", "error": err.Error()}
		} else {
			var probeResult map[string]any
			json.Unmarshal([]byte(probeResp), &probeResult)
			result["http_check"] = probeResult
			if code, ok := probeResult["status_code"].(float64); ok && code >= 200 && code < 400 {
				result["verified"] = true
			} else {
				result["verified"] = false
			}
		}
	}
	if args.Query != "" {
		result["metric_check"] = map[string]any{"query": args.Query, "note": "metrics verification pending prometheus integration"}
	}
	return jsonMarshal(result), nil
}

func jsonMarshal(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// --- 告警关联工具 ---

type AlertCorrelateTool struct {
	alertQuery func(ctx context.Context, filter map[string]any) ([]map[string]any, error)
}

func NewAlertCorrelateTool(alertQuery func(context.Context, map[string]any) ([]map[string]any, error)) *AlertCorrelateTool {
	return &AlertCorrelateTool{alertQuery: alertQuery}
}

func (t *AlertCorrelateTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: tools.AlertCorrelate,
		Desc: "查询活跃告警并按关联性分组。用于发现同一根因引发的多条告警，减少告警噪音，定位核心问题。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"environment": {Desc: "环境筛选（prod/staging/dev）", Required: false, Type: schema.String},
			"domain":      {Desc: "领域筛选（kafka/minio/glusterfs）", Required: false, Type: schema.String},
			"severity":    {Desc: "严重级别筛选（critical/warning/info）", Required: false, Type: schema.String},
		}),
	}, nil
}

func (t *AlertCorrelateTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Environment string `json:"environment"`
		Domain      string `json:"domain"`
		Severity    string `json:"severity"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("parse arguments: %w", err)
	}

	filter := map[string]any{}
	if args.Environment != "" {
		filter["environment"] = args.Environment
	}
	if args.Domain != "" {
		filter["domain"] = args.Domain
	}
	if args.Severity != "" {
		filter["severity"] = args.Severity
	}

	alerts, err := t.alertQuery(ctx, filter)
	if err != nil {
		return "", fmt.Errorf("query alerts: %w", err)
	}

	// 按 domain 分组
	groups := map[string][]map[string]any{}
	for _, a := range alerts {
		domain, _ := a["domain"].(string)
		if domain == "" {
			domain = "unknown"
		}
		groups[domain] = append(groups[domain], a)
	}

	// 构建关联结果
	result := map[string]any{
		"total":   len(alerts),
		"groups":  len(groups),
		"details": groups,
	}

	// 识别可能的根因
	if len(alerts) > 3 {
		result["correlation_hint"] = "多条告警同时触发，可能存在关联根因"
	}

	return jsonMarshal(result), nil
}

// --- 知识检索工具 ---

type KnowledgeSearchTool struct {
	store *KnowledgeStore
}

func NewKnowledgeSearchTool(store *KnowledgeStore) *KnowledgeSearchTool {
	return &KnowledgeSearchTool{store: store}
}

func (t *KnowledgeSearchTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: tools.KnowledgeSearch,
		Desc: "搜索历史诊断经验。当遇到类似问题时，查找过去的诊断记录和解决方案，避免重复排查。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query": {Desc: "搜索关键词（如告警名称、错误信息、组件名）", Required: true, Type: schema.String},
			"limit": {Desc: "返回条数，默认 5", Required: false, Type: schema.Integer},
		}),
	}, nil
}

func (t *KnowledgeSearchTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("parse arguments: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	results, err := t.store.Search(ctx, args.Query, args.Limit)
	if err != nil {
		return "", fmt.Errorf("search knowledge: %w", err)
	}
	return jsonMarshal(map[string]any{
		"query":   args.Query,
		"results": results,
		"count":   len(results),
	}), nil
}
