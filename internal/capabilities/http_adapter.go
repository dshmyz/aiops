package capabilities

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

const maxBackendResponseBytes = 1024 * 1024

type AdapterConfig struct {
	MaxRetries       int
	InitialBackoff   time.Duration
	MaxBackoff       time.Duration
	FailureThreshold int
	ResetTimeout     time.Duration
	// OpenAPIInsecureSkipVerify 为 true 时，能力执行与抓取外部 OpenAPI/Swagger 文档
	// 都跳过 TLS 证书校验（对接自签/内网 HTTPS 后端）。生产默认 false（校验证书）。
	OpenAPIInsecureSkipVerify bool
}

type circuitState int

const (
	circuitClosed circuitState = iota
	circuitOpen
	circuitHalfOpen
)

type circuitBreaker struct {
	mu               sync.Mutex
	state            circuitState
	failures         int
	lastFailureTime  time.Time
	failureThreshold int
	resetTimeout     time.Duration
}

func newCircuitBreaker(failureThreshold int, resetTimeout time.Duration) *circuitBreaker {
	return &circuitBreaker{
		state:            circuitClosed,
		failureThreshold: failureThreshold,
		resetTimeout:     resetTimeout,
	}
}

func (cb *circuitBreaker) allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case circuitClosed:
		return true
	case circuitOpen:
		if time.Since(cb.lastFailureTime) >= cb.resetTimeout {
			cb.state = circuitHalfOpen
			return true
		}
		return false
	case circuitHalfOpen:
		return true
	default:
		return true
	}
}

func (cb *circuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures = 0
	cb.state = circuitClosed
}

func (cb *circuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures++
	cb.lastFailureTime = time.Now()
	if cb.state == circuitHalfOpen || cb.failures >= cb.failureThreshold {
		cb.state = circuitOpen
	}
}

type HTTPAdapter struct {
	client *http.Client
	cfg    AdapterConfig
	cb     *circuitBreaker
}

func defaultAdapterConfig() AdapterConfig {
	return AdapterConfig{
		MaxRetries:       3,
		InitialBackoff:   200 * time.Millisecond,
		MaxBackoff:       2 * time.Second,
		FailureThreshold: 5,
		ResetTimeout:     30 * time.Second,
	}
}

func newDefaultClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        20,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}

func NewHTTPAdapter(client *http.Client) *HTTPAdapter {
	return NewHTTPAdapterWithConfig(client, defaultAdapterConfig())
}

func NewHTTPAdapterWithConfig(client *http.Client, cfg AdapterConfig) *HTTPAdapter {
	if client == nil {
		client = newDefaultClient()
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = 200 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 2 * time.Second
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.ResetTimeout <= 0 {
		cfg.ResetTimeout = 30 * time.Second
	}
	if cfg.OpenAPIInsecureSkipVerify {
		// 全部放开证书校验：能力执行与 OpenAPI/Swagger 文档抓取都跳过 TLS 证书校验，
		// 用于对接自签/内网 HTTPS 后端。生产默认关闭；开启即信任所有端点，须自行评估风险。
		transport, _ := client.Transport.(*http.Transport)
		if transport == nil {
			transport = &http.Transport{}
		}
		tr := transport.Clone()
		if tr.TLSClientConfig == nil {
			tr.TLSClientConfig = &tls.Config{}
		}
		tr.TLSClientConfig.InsecureSkipVerify = true //nolint:gosec // explicit opt-in via env
		client = &http.Client{Transport: tr}
	}
	return &HTTPAdapter{
		client: client,
		cfg:    cfg,
		cb:     newCircuitBreaker(cfg.FailureThreshold, cfg.ResetTimeout),
	}
}

func (a *HTTPAdapter) Execute(ctx context.Context, capability Capability, input map[string]any) (NormalizedResult, error) {
	if capability.Status != StatusPublished {
		return NormalizedResult{}, errors.New("HTTP adapter only executes published capabilities")
	}
	if err := Validate(capability); err != nil {
		return NormalizedResult{}, err
	}
	if input == nil {
		return NormalizedResult{}, errors.New("input must not be nil")
	}
	if err := validateAdapterInput(capability, input); err != nil {
		return NormalizedResult{}, err
	}

	timeout := time.Duration(capability.Backend.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	requestContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	path, err := buildPath(capability.Backend.Path, input)
	if err != nil {
		return NormalizedResult{}, err
	}
	if capability.Operation == tools.Write {
		return a.executeWrite(requestContext, capability, input, path)
	}
	return a.executeRead(requestContext, capability, input, path)
}

// do wraps a single HTTP request with circuit-breaker and retry logic.
// Only GET requests are retried (idempotent); all other methods execute
// exactly once. The circuit breaker records one success or failure per
// logical request regardless of how many retry attempts were made.
func (a *HTTPAdapter) do(req *http.Request) (*http.Response, error) {
	if !a.cb.allow() {
		return nil, errors.New("circuit breaker is open")
	}

	ctx := req.Context()
	isRetryable := req.Method == http.MethodGet
	maxAttempts := 1
	if isRetryable {
		maxAttempts = a.cfg.MaxRetries + 1
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			backoff := a.computeBackoff(attempt)
			select {
			case <-ctx.Done():
				a.cb.recordFailure()
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		resp, err := a.client.Do(req)
		if err != nil {
			lastErr = err
			if !isRetryable {
				break
			}
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			a.cb.recordSuccess()
			return resp, nil
		}

		// 非 2xx：读取脱敏错误体供审计留档，作为 BackendError 对外透出。
		// 这里先读 body 再 close，否则原始错误体在 executeRead/Write 处已无从获取。
		badBody, _ := io.ReadAll(io.LimitReader(resp.Body, int64(maxBackendResponseBytes+1)))
		lastErr = newBackendError(fmt.Errorf("backend returned HTTP %d", resp.StatusCode), resp.StatusCode, badBody)
		resp.Body.Close()
		if !isRetryable {
			break
		}
	}

	a.cb.recordFailure()
	if lastErr == nil {
		lastErr = errors.New("request failed")
	}
	return nil, lastErr
}

func (a *HTTPAdapter) computeBackoff(attempt int) time.Duration {
	backoff := a.cfg.InitialBackoff
	for i := 1; i < attempt; i++ {
		backoff *= 2
	}
	if backoff > a.cfg.MaxBackoff {
		backoff = a.cfg.MaxBackoff
	}
	return backoff
}

func (a *HTTPAdapter) executeRead(ctx context.Context, capability Capability, input map[string]any, path string) (NormalizedResult, error) {
	if err := validateOutputMappings(capability.Output); err != nil {
		return NormalizedResult{}, err
	}
	endpoint := strings.TrimRight(capability.Backend.BaseURL, "/") + path
	if query := buildQueryValues(capability, input); len(query) > 0 {
		endpoint = endpoint + "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, capability.Backend.Method, endpoint, nil)
	if err != nil {
		return NormalizedResult{}, err
	}
	injectAuthHeader(request, capability)
	response, err := a.do(request)
	if err != nil {
		return NormalizedResult{}, err
	}
	defer response.Body.Close()

	limited := io.LimitReader(response.Body, maxBackendResponseBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return NormalizedResult{}, err
	}
	if len(payload) > maxBackendResponseBytes {
		return NormalizedResult{}, fmt.Errorf("backend response exceeds %d bytes", maxBackendResponseBytes)
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		// 顶层 JSON 数组也是合法结构化响应，纳入 data.items，避免误判为文本文档
		if arr, ok := topLevelArray(payload); ok {
			return normalizeTopLevelArray(payload, capability, input, arr), nil
		}
		// 非 JSON 响应：降级为文本文档能力，不再失败
		return normalizeTextResult(payload, capability, input), nil
	}

	fields := make(map[string]any)
	if len(capability.Output.Fields) == 0 {
		// 没有显式字段映射时，智能提取：递归展开嵌套对象、数组截断、敏感字段过滤
		fields = smartExtractFields(raw)
	} else {
		for name, path := range capability.Output.Fields {
			if isSensitive(name) || isSensitivePath(path) {
				continue
			}
			if value, ok := extractPath(raw, path); ok {
				// 标量值保留原始类型（与原行为一致）；数组/对象智能展开
				if _, ok := scalarString(value); ok {
					fields[name] = value
				} else if arr, ok := value.([]any); ok && len(arr) > 0 {
					// 数组字段：保留前 maxArrayItems 个标量元素 + 总数
					safeArr := make([]any, 0, maxArrayItems)
					for i, item := range arr {
						if i >= maxArrayItems {
							break
						}
						if _, ok := scalarString(item); ok {
							safeArr = append(safeArr, item)
						}
					}
					if len(safeArr) > 0 {
						fields[name] = safeArr
						fields[name+"_count"] = len(arr)
					}
				} else if obj, ok := value.(map[string]any); ok && len(obj) > 0 {
					// 对象字段：展开扁平子字段
					subFields := make(map[string]any)
					flattenObject(name, obj, subFields)
					for k, v := range subFields {
						fields[k] = v
					}
				}
			}
		}
	}
	// 严重级别：优先用显式配置的 severity_path，否则智能推断
	severity := "info"
	if capability.Output.SeverityPath != "" {
		if !isSensitivePath(capability.Output.SeverityPath) {
			if value, ok := extractPath(raw, capability.Output.SeverityPath); ok {
				if scalar, ok := scalarString(value); ok {
					severity = scalar
				}
			}
		}
	} else {
		severity = inferSeverityFromData(fields)
	}
	// status_mapping：按能力自配置把原始状态值归一到标准严重级（未命中则保持不变），
	// 让"值→层级"可针对具体接口配置，而非只能靠全局 classifySeverity 猜。
	severity = applyStatusMapping(severity, capability.Output.StatusMapping)
	name := resourceNameFromInput(capability, input)
	// 摘要：优先用 summary_template，否则智能生成
	summary := ""
	if strings.TrimSpace(capability.Output.SummaryTemplate) != "" {
		summary = renderSummary(capability.Output.SummaryTemplate, input, fields)
	} else {
		summary = inferSummaryFromData(fields, capability.ResourceType)
	}
	return NormalizedResult{
		Kind: capability.Output.Kind,
		Resource: ResourceRef{
			Domain: capability.Domain,
			Type:   capability.ResourceType,
			Name:   name,
		},
		Severity: severity,
		Summary:  summary,
		Data:     fields,
		Raw:      redactResponse(payload),
	}, nil
}

func (a *HTTPAdapter) executeWrite(ctx context.Context, capability Capability, input map[string]any, path string) (NormalizedResult, error) {
	body, err := buildWriteBody(capability, input)
	if err != nil {
		return NormalizedResult{}, err
	}
	endpoint := strings.TrimRight(capability.Backend.BaseURL, "/") + path
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, capability.Backend.Method, endpoint, reader)
	if err != nil {
		return NormalizedResult{}, err
	}
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	injectAuthHeader(request, capability)
	response, err := a.do(request)
	if err != nil {
		return NormalizedResult{}, err
	}
	defer response.Body.Close()

	limited := io.LimitReader(response.Body, maxBackendResponseBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return NormalizedResult{}, err
	}
	if len(payload) > maxBackendResponseBytes {
		return NormalizedResult{}, fmt.Errorf("backend response exceeds %d bytes", maxBackendResponseBytes)
	}
	raw := map[string]any{}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &raw); err != nil {
			if arr, ok := topLevelArray(payload); ok {
				return normalizeTopLevelArray(payload, capability, input, arr), nil
			}
			// 非 JSON 响应：降级为文本文档能力，不再失败
			return normalizeTextResult(payload, capability, input), nil
		}
	}

	fields := make(map[string]any)
	if len(capability.Output.Fields) == 0 {
		// 无显式映射时智能提取（与读操作一致）
		fields = smartExtractFields(raw)
	} else {
		for name, fieldPath := range capability.Output.Fields {
			if isSensitive(name) || isSensitivePath(fieldPath) {
				continue
			}
			if value, ok := extractPath(raw, fieldPath); ok {
				if _, ok := scalarString(value); ok {
					fields[name] = value
				} else if arr, ok := value.([]any); ok && len(arr) > 0 {
					safeArr := make([]any, 0, maxArrayItems)
					for i, item := range arr {
						if i >= maxArrayItems {
							break
						}
						if _, ok := scalarString(item); ok {
							safeArr = append(safeArr, item)
						}
					}
					if len(safeArr) > 0 {
						fields[name] = safeArr
						fields[name+"_count"] = len(arr)
					}
				} else if obj, ok := value.(map[string]any); ok && len(obj) > 0 {
					subFields := make(map[string]any)
					flattenObject(name, obj, subFields)
					for k, v := range subFields {
						fields[k] = v
					}
				}
			}
		}
	}
	name := resourceNameFromInput(capability, input)
	// 写操作也智能推断摘要
	summary := ""
	if strings.TrimSpace(capability.Output.SummaryTemplate) != "" {
		summary = renderSummary(capability.Output.SummaryTemplate, input, fields)
	} else {
		summary = inferSummaryFromData(fields, capability.ResourceType)
		if summary == "" {
			summary = "操作执行完成"
		}
	}
	return NormalizedResult{
		Kind: capability.Output.Kind,
		Resource: ResourceRef{
			Domain: capability.Domain,
			Type:   capability.ResourceType,
			Name:   name,
		},
		Severity: "info",
		Summary:  summary,
		Data:     fields,
		Raw:      redactResponse(payload),
	}, nil
}

// normalizeTopLevelArray 处理顶层 JSON 数组：作为结构化结果放入 data.items，
// 元素递归脱敏（复用 redactValue），避免被误判为文本文档降级。
func normalizeTopLevelArray(payload []byte, capability Capability, input map[string]any, arr []any) NormalizedResult {
	return NormalizedResult{
		Kind: capability.Output.Kind,
		Resource: ResourceRef{
			Domain: capability.Domain,
			Type:   capability.ResourceType,
			Name:   resourceNameFromInput(capability, input),
		},
		Severity: "info",
		Summary:  fmt.Sprintf("后端返回了 JSON 数组（%d 项），内容见 items 字段", len(arr)),
		Data:     map[string]any{"items": redactValue(arr)},
		Raw:      redactResponse(payload),
	}
}

// normalizeTextResult 处理无法解析为 JSON 的后端响应：作为"文本文档能力"，
// 把原文（截断到 maxNonJSONContentBytes）放入 data.content 交给 LLM，而不是让能力失败。
// severity 固定 info（无结构化状态字段可推断）；留档快照仍走 redactResponse。
func normalizeTextResult(payload []byte, capability Capability, input map[string]any) NormalizedResult {
	content := string(payload)
	truncated := false
	if len(content) > maxNonJSONContentBytes {
		content = content[:maxNonJSONContentBytes]
		truncated = true
	}
	fields := map[string]any{"content": content}
	if truncated {
		fields["truncated"] = true
	}
	return NormalizedResult{
		Kind: capability.Output.Kind,
		Resource: ResourceRef{
			Domain: capability.Domain,
			Type:   capability.ResourceType,
			Name:   resourceNameFromInput(capability, input),
		},
		Severity: "info",
		Summary:  "后端返回了非 JSON 文本，内容见 content 字段" + map[bool]string{true: "（已截断）"}[truncated],
		Data:     fields,
		Raw:      redactResponse(payload),
	}
}

func buildWriteBody(capability Capability, input map[string]any) ([]byte, error) {
	pathVars := make(map[string]struct{}, len(pathVariables(capability.Backend.Path)))
	for _, name := range pathVariables(capability.Backend.Path) {
		pathVars[name] = struct{}{}
	}
	body := map[string]any{}
	for name, value := range input {
		if _, isPath := pathVars[name]; isPath {
			continue
		}
		if field, ok := capability.InputSchema[name]; ok && field.In == "query" {
			continue
		}
		body[name] = value
	}
	if len(body) == 0 {
		return nil, nil
	}
	return json.Marshal(body)
}

func buildQueryValues(capability Capability, input map[string]any) url.Values {
	values := url.Values{}
	for name, field := range capability.InputSchema {
		if field.In != "query" {
			continue
		}
		if value, ok := input[name]; ok {
			values.Set(name, fmt.Sprint(value))
		}
	}
	return values
}

func validateOutputMappings(output OutputSpec) error {
	for name, path := range output.Fields {
		if strings.TrimSpace(path) == "$" {
			return fmt.Errorf("output field %q uses raw output mapping", name)
		}
	}
	return nil
}

func validateAdapterInput(capability Capability, input map[string]any) error {
	for name, field := range capability.InputSchema {
		value, ok := input[name]
		if field.Required && !ok {
			return fmt.Errorf("missing required input %q", name)
		}
		if !ok {
			continue
		}
		switch field.Type {
		case "string":
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return fmt.Errorf("input %q must be a non-empty string", name)
			}
		case "integer":
			if !validAdapterInteger(value) {
				return fmt.Errorf("input %q must be an integer", name)
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("input %q must be a boolean", name)
			}
		}
	}
	for name := range input {
		if _, ok := capability.InputSchema[name]; !ok {
			return fmt.Errorf("input %q is not allowed", name)
		}
	}
	return nil
}

func validAdapterInteger(value any) bool {
	switch value := value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case float32:
		return float32(int64(value)) == value
	case float64:
		return float64(int64(value)) == value
	case json.Number:
		_, err := value.Int64()
		return err == nil
	default:
		return false
	}
}

func buildPath(path string, input map[string]any) (string, error) {
	for _, name := range pathVariables(path) {
		value, ok := input[name]
		if !ok || fmt.Sprint(value) == "" {
			return "", fmt.Errorf("missing path value %q", name)
		}
		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(fmt.Sprint(value)))
	}
	return path, nil
}

// ResourceNameKey returns the input field name that identifies the affected
// resource for a capability. It is derived from the backend path variables —
// the REST path itself declares which input field is the resource (e.g.
// {topic}) — so callers never need a hardcoded field-name list. Prefers the
// path variable matching resource_type; falls back to the first path variable;
// empty when the path declares no variable.
func ResourceNameKey(capability Capability) string {
	vars := pathVariables(capability.Backend.Path)
	for _, v := range vars {
		if v == capability.ResourceType {
			return v
		}
	}
	if len(vars) > 0 {
		return vars[0]
	}
	return ""
}

// resourceNameFromInput returns the input value naming the affected resource,
// derived from the capability's backend path. Empty when the path declares no
// variable or the input carries no such value.
func resourceNameFromInput(capability Capability, input map[string]any) string {
	key := ResourceNameKey(capability)
	if key == "" {
		return ""
	}
	name, _ := input[key].(string)
	return name
}

func isSensitive(name string) bool {
	lower := strings.ToLower(name)
	for _, marker := range []string{"password", "secret", "token", "key", "credential", "authorization"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func isSensitivePath(path string) bool {
	path = strings.TrimSpace(path)
	for _, part := range strings.Split(strings.TrimPrefix(path, "$."), ".") {
		if isSensitive(part) {
			return true
		}
	}
	return false
}

// injectAuthHeader 根据 BackendAuthConfig 在 HTTP 请求中注入认证头。
func injectAuthHeader(req *http.Request, capability Capability) {
	if capability.Backend.Auth.Type != "bearer" {
		return
	}
	token := resolveAuthToken(capability.Backend.Auth.Token)
	if token == "" {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
}

// resolveAuthToken 解析 token 值。支持 ${ENV_VAR} 语法：以 ${ 开头、}
// 结尾时从环境变量读取；否则原样返回。
func resolveAuthToken(token string) string {
	token = strings.TrimSpace(token)
	if strings.HasPrefix(token, "${") && strings.HasSuffix(token, "}") && len(token) > 4 {
		envName := token[2 : len(token)-1]
		return os.Getenv(envName)
	}
	return token
}

func scalarString(value any) (string, bool) {
	switch value := value.(type) {
	case string:
		return value, true
	case bool:
		return fmt.Sprint(value), true
	case float32, float64,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(value), true
	default:
		return "", false
	}
}
