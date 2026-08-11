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
	"strings"
	"sync"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

const maxBackendResponseBytes = 10 * 1024

type AdapterConfig struct {
	MaxRetries       int
	InitialBackoff   time.Duration
	MaxBackoff       time.Duration
	FailureThreshold int
	ResetTimeout     time.Duration
	// OpenAPIInsecureSkipVerify 仅为**抓取外部 OpenAPI/Swagger 文档**放开 TLS
	// 证书校验（用于对接自签/内网 HTTPS 文档源）。能力执行始终走受信 client，
	// 不受此开关影响。生产默认 false（校验证书）。
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
	client        *http.Client
	openAPIClient *http.Client
	cfg           AdapterConfig
	cb            *circuitBreaker
}

// openapiClient returns the client used to fetch external OpenAPI/Swagger
// documents. When OpenAPIInsecureSkipVerify is set it is a transport that skips
// TLS certificate validation (only for this document-fetch path, never for
// capability execution).
func (a *HTTPAdapter) openapiClient() *http.Client {
	if a.openAPIClient != nil {
		return a.openAPIClient
	}
	return a.client
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
	var openAPIClient *http.Client
	if cfg.OpenAPIInsecureSkipVerify {
		base := client
		if base == nil {
			base = newDefaultClient()
		}
		transport, _ := base.Transport.(*http.Transport)
		if transport == nil {
			transport = &http.Transport{}
		}
		tr := transport.Clone()
		if tr.TLSClientConfig == nil {
			tr.TLSClientConfig = &tls.Config{}
		}
		tr.TLSClientConfig.InsecureSkipVerify = true //nolint:gosec // dev-only document fetch
		openAPIClient = &http.Client{Transport: tr}
	}
	return &HTTPAdapter{
		client:        client,
		openAPIClient: openAPIClient,
		cfg:           cfg,
		cb:            newCircuitBreaker(cfg.FailureThreshold, cfg.ResetTimeout),
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

		lastErr = fmt.Errorf("backend returned HTTP %d", resp.StatusCode)
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
		return NormalizedResult{}, err
	}

	fields := make(map[string]any)
	for name, path := range capability.Output.Fields {
		if isSensitive(name) || isSensitivePath(path) {
			continue
		}
		if value, ok := extractPath(raw, path); ok {
			if _, ok := scalarString(value); ok {
				fields[name] = value
			}
		}
	}
	severity := "info"
	if capability.Output.SeverityPath != "" {
		if !isSensitivePath(capability.Output.SeverityPath) {
			if value, ok := extractPath(raw, capability.Output.SeverityPath); ok {
				if scalar, ok := scalarString(value); ok {
					severity = scalar
				}
			}
		}
	}
	name := firstString(input, "bucket", "topic", "volume", "name", "cluster")
	return NormalizedResult{
		Kind: capability.Output.Kind,
		Resource: ResourceRef{
			Domain:      capability.Domain,
			Type:        capability.ResourceType,
			Name:        name,
			Environment: fmt.Sprint(input["environment"]),
		},
		Severity: severity,
		Summary:  renderSummary(capability.Output.SummaryTemplate, input, fields),
		Data:     fields,
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
			return NormalizedResult{}, err
		}
	}

	fields := make(map[string]any)
	for name, fieldPath := range capability.Output.Fields {
		if isSensitive(name) || isSensitivePath(fieldPath) {
			continue
		}
		if value, ok := extractPath(raw, fieldPath); ok {
			if _, ok := scalarString(value); ok {
				fields[name] = value
			}
		}
	}
	name := firstString(input, "bucket", "topic", "volume", "name", "cluster")
	return NormalizedResult{
		Kind: capability.Output.Kind,
		Resource: ResourceRef{
			Domain:      capability.Domain,
			Type:        capability.ResourceType,
			Name:        name,
			Environment: fmt.Sprint(input["environment"]),
		},
		Severity: "info",
		Summary:  renderSummary(capability.Output.SummaryTemplate, input, fields),
		Data:     fields,
	}, nil
}

func buildWriteBody(capability Capability, input map[string]any) ([]byte, error) {
	pathVars := make(map[string]struct{}, len(pathVariables(capability.Backend.Path)))
	for _, name := range pathVariables(capability.Backend.Path) {
		pathVars[name] = struct{}{}
	}
	body := map[string]any{}
	for name, value := range input {
		if name == "environment" {
			continue
		}
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

func firstString(input map[string]any, names ...string) string {
	for _, name := range names {
		if value, ok := input[name].(string); ok && value != "" {
			return value
		}
	}
	return ""
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
