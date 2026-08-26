package capabilities_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
)

func TestHTTPAdapterBuildsRequestAndNormalizesOutput(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/minio/clusters/m1/buckets/archive/capacity" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"usage_pct": 86, "secret_token": "hide-me"}})
	}))
	defer server.Close()
	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	adapter := capabilities.NewHTTPAdapter(http.DefaultClient)

	result, err := adapter.Execute(context.Background(), capability, map[string]any{"cluster": "m1", "bucket": "archive"})
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if result.Kind != "observation" || result.Resource.Name != "archive" || result.Data["usage_pct"] != float64(86) {
		t.Fatalf("result = %+v, want normalized observation", result)
	}
	if result.Summary != "Bucket archive usage is 86%" {
		t.Fatalf("summary = %q", result.Summary)
	}
	if _, ok := result.Data["secret_token"]; ok {
		t.Fatalf("result leaked redacted field: %+v", result.Data)
	}
}

// status_mapping 把接口的原始状态值映射成标准严重级：显式 severity_path 取到 RED，
// 经 status_mapping 归一为 critical，而不是把原始值当严重级透传。
func TestHTTPAdapterStatusMappingNormalizesSeverityPathValue(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"status":"RED","usage_pct":86}}`))
	}))
	defer server.Close()
	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	capability.Output.SeverityPath = "$.data.status"
	capability.Output.StatusMapping = map[string]string{"RED": "critical", "YELLOW": "warning", "running": "ok"}
	input := map[string]any{"cluster": "m1", "bucket": "archive"}
	result, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, input)
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if result.Severity != "critical" {
		t.Fatalf("severity = %q, want critical", result.Severity)
	}
}

// status_mapping 未命中时保持原严重级（向后兼容，不覆盖既有推断/透传）。
func TestHTTPAdapterStatusMappingUnmatchedKeepsValue(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"status":"warning","usage_pct":86}}`))
	}))
	defer server.Close()
	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	capability.Output.SeverityPath = "$.data.status"
	capability.Output.StatusMapping = map[string]string{"RED": "critical"}
	input := map[string]any{"cluster": "m1", "bucket": "archive"}
	result, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, input)
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if result.Severity != "warning" {
		t.Fatalf("severity = %q, want warning (unmatched mapping preserved)", result.Severity)
	}
}

// 留档快照 Raw 必须对任意层级敏感字段做脱敏，供审计不含密钥。
func TestHTTPAdapterRawResponseRedactedForAudit(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"usage_pct":86,"nested":{"secret_token":"hide","zone":"a"}}}`))
	}))
	defer server.Close()
	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	result, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, map[string]any{"cluster": "m1", "bucket": "archive"})
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	// Raw 不含任何敏感字段内容，且保留正常返回数据
	if strings.Contains(result.Raw, "secret_token") || strings.Contains(result.Raw, "hide") {
		t.Fatalf("audit Raw leaked sensitive data: %s", result.Raw)
	}
	if !strings.Contains(result.Raw, "usage_pct") {
		t.Fatalf("audit Raw should keep non-sensitive data: %s", result.Raw)
	}
	// 最终给 LLM 的 Data 不含敏感字段（既有行为），且与留档分离
	if _, ok := result.Data["secret_token"]; ok {
		t.Fatalf("LLM Data leaked sensitive field: %+v", result.Data)
	}
}

// 非 JSON 响应不得导致能力失败：作为文本文档交给 LLM（data.content），severity 为 info。
func TestHTTPAdapterSupportsNonJSONTextResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("cluster m1 healthy\nnode-1 up\nnode-2 DOWN"))
	}))
	defer server.Close()
	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	result, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, map[string]any{"cluster": "m1", "bucket": "archive"})
	if err != nil {
		t.Fatalf("non-JSON response should not fail: %v", err)
	}
	content, ok := result.Data["content"].(string)
	if !ok || !strings.Contains(content, "node-2 DOWN") {
		t.Fatalf("data.content = %v, want text body", result.Data["content"])
	}
	if result.Severity != "info" {
		t.Fatalf("severity = %q, want info for text response", result.Severity)
	}
}

// 超长非 JSON 文本截断到 maxNonJSONContentBytes 并标注 truncated。
func TestHTTPAdapterNonJSONTextIsTruncated(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("line-metric-", 3000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(long))
	}))
	defer server.Close()
	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	result, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, map[string]any{"cluster": "m1", "bucket": "archive"})
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	content, _ := result.Data["content"].(string)
	if len(content) >= len(long) {
		t.Fatalf("content should be truncated: len=%d vs original=%d", len(content), len(long))
	}
	if len(content) == 0 || !strings.HasPrefix(content, "line-metric-") {
		t.Fatalf("truncated content head missing, got prefix %q", content[:min(11, len(content))])
	}
	if result.Data["truncated"] != true {
		t.Fatalf("truncated flag not set")
	}
	// 留档快照仍存在，且不承载未截断的完整长文
	if !strings.Contains(result.Raw, "line-metric") {
		t.Fatalf("raw snapshot should capture text for audit")
	}
}

// 顶层 JSON 数组是合法结构化响应，应纳入 data.items，而非当成文本 content 降级。
func TestHTTPAdapterTopLevelArrayBecomesStructured(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"orders","lag":5},{"name":"payments","lag":9}]`))
	}))
	defer server.Close()
	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	result, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, map[string]any{"cluster": "m1", "bucket": "archive"})
	if err != nil {
		t.Fatalf("top-level array should not fail: %v", err)
	}
	if _, hasContent := result.Data["content"]; hasContent {
		t.Fatalf("top-level array must not go through text fallback: %+v", result.Data)
	}
	items, ok := result.Data["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("data.items = %+v, want array of 2", result.Data["items"])
	}
}

// 失败响应（非 2xx）原始脱敏 body 通过 BackendError 携带，供审计留档，且不污染 Error() 消息。
func TestHTTPAdapterNon2xxCarriesRedactedBodyForAudit(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":"boom","secret_token":"x"}`))
	}))
	defer server.Close()
	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	_, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, map[string]any{"cluster": "m1", "bucket": "archive"})
	if err == nil {
		t.Fatalf("non-2xx should fail")
	}
	if strings.Contains(err.Error(), "boom") {
		t.Fatalf("BackendError Error() leaks body: %v", err)
	}
	var be *capabilities.BackendError
	if !errors.As(err, &be) {
		t.Fatalf("expected *capabilities.BackendError, got %T", err)
	}
	if !strings.Contains(be.BodyRedacted, "boom") {
		t.Fatalf("BodyRedacted should carry body: %q", be.BodyRedacted)
	}
	if strings.Contains(be.BodyRedacted, "secret_token") {
		t.Fatalf("BodyRedacted leaked sensitive: %q", be.BodyRedacted)
	}
	if be.StatusCode != 500 {
		t.Fatalf("StatusCode = %d, want 500", be.StatusCode)
	}
}

func TestHTTPAdapterRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	capability := validReadCapability()
	adapter := capabilities.NewHTTPAdapter(nil)

	for name, input := range map[string]map[string]any{
		"wrong string type": {"cluster": "m1", "bucket": 42},
		"unknown input":     {"cluster": "m1", "bucket": "archive", "extra": "nope"},
		"missing required":  {"cluster": "m1"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := adapter.Execute(context.Background(), capability, input); err == nil {
				t.Fatal("Execute accepted invalid input")
			}
		})
	}
}

func TestHTTPAdapterIntegerInputMatchesGovernedValidation(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"usage_pct":86}}`))
	}))
	defer server.Close()

	for name, test := range map[string]struct {
		value   any
		wantErr bool
	}{
		"integral float32":       {value: float32(3)},
		"integral float64":       {value: float64(3)},
		"integral json number":   {value: json.Number("3")},
		"fractional float32":     {value: float32(3.5), wantErr: true},
		"fractional float64":     {value: float64(3.5), wantErr: true},
		"fractional json number": {value: json.Number("3.5"), wantErr: true},
		"invalid json number":    {value: json.Number("not-a-number"), wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			capability := validReadCapability()
			capability.Backend.BaseURL = server.URL
			capability.InputSchema["limit"] = capabilities.InputField{Type: "integer", Required: true}
			input := map[string]any{"cluster": "m1", "bucket": "archive", "limit": test.value}

			_, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, input)
			if test.wantErr && err == nil {
				t.Fatal("Execute accepted invalid integer input")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("Execute rejected valid integer input: %v", err)
			}
		})
	}
}

func TestHTTPAdapterRejectsUnpublishedAndWriteCapabilities(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"usage_pct":86}}`))
	}))
	defer server.Close()
	adapter := capabilities.NewHTTPAdapter(nil)
	input := map[string]any{"cluster": "m1", "bucket": "archive"}

	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	capability.Status = capabilities.StatusNeedsReview
	if _, err := adapter.Execute(context.Background(), capability, input); err == nil || !strings.Contains(err.Error(), "published") {
		t.Fatal("Execute accepted unpublished capability")
	}
}

func TestHTTPAdapterExecutesWriteCapabilityWithJSONBody(t *testing.T) {
	t.Parallel()
	var capturedMethod, capturedPath, capturedContentType string
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		capturedContentType = r.Header.Get("Content-Type")
		capturedBody, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "applied", "quota": 100})
	}))
	defer server.Close()
	capability := validWriteCapability()
	capability.Backend.BaseURL = server.URL
	adapter := capabilities.NewHTTPAdapter(nil)

	result, err := adapter.Execute(context.Background(), capability, map[string]any{
		"cluster":     "m1",
		"bucket":      "archive",
		"quota":       100,
	})
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if capturedMethod != http.MethodPost {
		t.Fatalf("captured method = %q, want POST", capturedMethod)
	}
	if capturedPath != "/api/minio/clusters/m1/buckets/archive/quota" {
		t.Fatalf("path = %q, want quota endpoint with path variables", capturedPath)
	}
	if capturedContentType != "application/json" {
		t.Fatalf("content-type = %q, want application/json", capturedContentType)
	}
	body := string(capturedBody)
	if !strings.Contains(body, `"quota":100`) || strings.Contains(body, `"cluster"`) || strings.Contains(body, `"bucket"`) {
		t.Fatalf("body = %q, want JSON body with only non-path fields", body)
	}
	if result.Kind != "mutation" || result.Resource.Name != "archive" {
		t.Fatalf("result = %+v, want mutation normalized result", result)
	}
	if result.Summary != "Bucket archive quota set to 100" {
		t.Fatalf("summary = %q, want rendered template", result.Summary)
	}
}

func TestHTTPAdapterExecutesWriteCapabilityWithEmptyResponseBody(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	capability := validWriteCapability()
	capability.Backend.BaseURL = server.URL
	adapter := capabilities.NewHTTPAdapter(nil)

	result, err := adapter.Execute(context.Background(), capability, map[string]any{
		"cluster":     "m1",
		"bucket":      "archive",
		"quota":       200,
	})
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if result.Kind != "mutation" {
		t.Fatalf("result kind = %q, want mutation", result.Kind)
	}
	if result.Summary != "Bucket archive quota set to 200" {
		t.Fatalf("summary = %q, want rendered template", result.Summary)
	}
}

func TestHTTPAdapterWriteCapabilityRejectsBackendError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"invalid quota"}`))
	}))
	defer server.Close()
	capability := validWriteCapability()
	capability.Backend.BaseURL = server.URL

	_, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, map[string]any{
		"cluster":     "m1",
		"bucket":      "archive",
		"quota":       -1,
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 422") {
		t.Fatalf("err = %v, want HTTP 422 backend error", err)
	}
}

func TestHTTPAdapterRejectsRawOutputMapping(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"usage_pct":86}}`))
	}))
	defer server.Close()
	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	capability.Output.Fields = map[string]string{"raw": "$"}

	if _, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, map[string]any{"cluster": "m1", "bucket": "archive"}); err == nil || !strings.Contains(err.Error(), "raw output mapping") {
		t.Fatal("Execute accepted raw output mapping")
	}
}

func TestHTTPAdapterRedactsSensitiveOutputPaths(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"secret_token":"hide-me","status":"warning"}}`))
	}))
	defer server.Close()
	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	capability.Output.Fields["token_alias"] = "$.data.secret_token"

	result, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, map[string]any{"cluster": "m1", "bucket": "archive"})
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if _, ok := result.Data["token_alias"]; ok {
		t.Fatalf("result leaked sensitive path alias: %+v", result.Data)
	}
}

func TestHTTPAdapterSkipsNonScalarOutputFields(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"usage_pct":86,"secret_token":"hide-me"}}`))
	}))
	defer server.Close()

	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	capability.Output.Fields["payload"] = "$.data"
	result, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, map[string]any{"cluster": "m1", "bucket": "archive"})
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if _, ok := result.Data["payload"]; ok {
		t.Fatalf("result included non-scalar payload: %+v", result.Data)
	}
	if strings.Contains(result.Summary, "secret_token") || strings.Contains(result.Summary, "hide-me") {
		t.Fatalf("result leaked nested data in summary: %q", result.Summary)
	}
}

func TestHTTPAdapterOnlyExtractsScalarNonSensitiveSeverity(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"severity":"warning","details":{"level":"critical"},"secret_token":"error"}}`))
	}))
	defer server.Close()
	input := map[string]any{"cluster": "m1", "bucket": "archive"}
	adapter := capabilities.NewHTTPAdapter(nil)

	for name, path := range map[string]string{
		"object":    "$.data.details",
		"sensitive": "$.data.secret_token",
	} {
		t.Run(name, func(t *testing.T) {
			capability := validReadCapability()
			capability.Backend.BaseURL = server.URL
			capability.Output.SeverityPath = path
			result, err := adapter.Execute(context.Background(), capability, input)
			if err != nil {
				t.Fatalf("Execute returned %v", err)
			}
			if result.Severity != "info" {
				t.Fatalf("severity = %q, want default info", result.Severity)
			}
		})
	}

	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	capability.Output.SeverityPath = "$.data.severity"
	result, err := adapter.Execute(context.Background(), capability, input)
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if result.Severity != "warning" {
		t.Fatalf("severity = %q, want warning", result.Severity)
	}
}

func TestHTTPAdapterRejectsNilInput(t *testing.T) {
	t.Parallel()
	if _, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), validReadCapability(), nil); err == nil || !strings.Contains(err.Error(), "input must not be nil") {
		t.Fatal("Execute accepted nil input")
	}
}

func TestHTTPAdapterEscapesPathValues(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/minio/clusters/m%2F1/buckets/archive%2F2026/capacity" {
			t.Fatalf("escaped path = %q", r.URL.EscapedPath())
		}
		_, _ = w.Write([]byte(`{"data":{"usage_pct":86}}`))
	}))
	defer server.Close()
	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL

	if _, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, map[string]any{"cluster": "m/1", "bucket": "archive/2026"}); err != nil {
		t.Fatalf("Execute returned %v", err)
	}
}

func TestHTTPAdapterRejectsOversizedResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"usage_pct":86,"padding":"` + strings.Repeat("x", 1024*1024) + `"}}`))
	}))
	defer server.Close()
	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL

	if _, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, map[string]any{"cluster": "m1", "bucket": "archive"}); err == nil {
		t.Fatal("Execute accepted oversized response")
	}
}

func TestHTTPAdapterRetriesGetOnServerError(t *testing.T) {
	t.Parallel()
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requestCount, 1)
		if count < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"usage_pct":86}}`))
	}))
	defer server.Close()

	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	cfg := capabilities.AdapterConfig{
		MaxRetries:       3,
		InitialBackoff:   1 * time.Millisecond,
		MaxBackoff:       10 * time.Millisecond,
		FailureThreshold: 10,
		ResetTimeout:     1 * time.Second,
	}
	adapter := capabilities.NewHTTPAdapterWithConfig(nil, cfg)

	result, err := adapter.Execute(context.Background(), capability, map[string]any{"cluster": "m1", "bucket": "archive"})
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if result.Data["usage_pct"] != float64(86) {
		t.Fatalf("result data = %+v, want usage_pct=86", result.Data)
	}
	if count := atomic.LoadInt32(&requestCount); count != 3 {
		t.Fatalf("request count = %d, want 3 (2 failures + 1 success)", count)
	}
}

func TestHTTPAdapterDoesNotRetryPostRequests(t *testing.T) {
	t.Parallel()
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	capability := validWriteCapability()
	capability.Backend.BaseURL = server.URL
	cfg := capabilities.AdapterConfig{
		MaxRetries:       3,
		InitialBackoff:   1 * time.Millisecond,
		MaxBackoff:       10 * time.Millisecond,
		FailureThreshold: 10,
		ResetTimeout:     1 * time.Second,
	}
	adapter := capabilities.NewHTTPAdapterWithConfig(nil, cfg)

	_, err := adapter.Execute(context.Background(), capability, map[string]any{
		"cluster":     "m1",
		"bucket":      "archive",
		"quota":       100,
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("err = %v, want HTTP 503", err)
	}
	if count := atomic.LoadInt32(&requestCount); count != 1 {
		t.Fatalf("request count = %d, want 1 (no retry for POST)", count)
	}
}

func TestHTTPAdapterCircuitBreakerOpensAfterThreshold(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	cfg := capabilities.AdapterConfig{
		MaxRetries:       0,
		InitialBackoff:   1 * time.Millisecond,
		MaxBackoff:       10 * time.Millisecond,
		FailureThreshold: 3,
		ResetTimeout:     5 * time.Second,
	}
	adapter := capabilities.NewHTTPAdapterWithConfig(nil, cfg)
	input := map[string]any{"cluster": "m1", "bucket": "archive"}

	for i := 0; i < 3; i++ {
		_, err := adapter.Execute(context.Background(), capability, input)
		if err == nil {
			t.Fatalf("request %d should have failed", i+1)
		}
	}

	_, err := adapter.Execute(context.Background(), capability, input)
	if err == nil || !strings.Contains(err.Error(), "circuit breaker") {
		t.Fatalf("err = %v, want circuit breaker open error", err)
	}
}

func TestHTTPAdapterCircuitBreakerHalfOpenRecovery(t *testing.T) {
	t.Parallel()
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requestCount, 1)
		if count <= 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"usage_pct":86}}`))
	}))
	defer server.Close()

	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	cfg := capabilities.AdapterConfig{
		MaxRetries:       0,
		InitialBackoff:   1 * time.Millisecond,
		MaxBackoff:       10 * time.Millisecond,
		FailureThreshold: 3,
		ResetTimeout:     50 * time.Millisecond,
	}
	adapter := capabilities.NewHTTPAdapterWithConfig(nil, cfg)
	input := map[string]any{"cluster": "m1", "bucket": "archive"}

	for i := 0; i < 3; i++ {
		_, _ = adapter.Execute(context.Background(), capability, input)
	}

	time.Sleep(100 * time.Millisecond)

	result, err := adapter.Execute(context.Background(), capability, input)
	if err != nil {
		t.Fatalf("Execute returned %v, want success after half-open recovery", err)
	}
	if result.Data["usage_pct"] != float64(86) {
		t.Fatalf("result data = %+v, want usage_pct=86", result.Data)
	}
}

func TestHTTPAdapterAppendsQueryParamsToReadURL(t *testing.T) {
	t.Parallel()
	var capturedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"data":{"usage_pct":86}}`))
	}))
	defer server.Close()

	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	capability.InputSchema["detail"] = capabilities.InputField{Type: "string", In: "query"}

	adapter := capabilities.NewHTTPAdapter(nil)
	_, err := adapter.Execute(context.Background(), capability, map[string]any{
		"cluster":     "m1",
		"bucket":      "archive",
		"detail":      "full",
	})
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if !strings.Contains(capturedQuery, "detail=full") {
		t.Fatalf("query = %q, want detail=full", capturedQuery)
	}
}

func TestHTTPAdapterExcludesQueryParamsFromWriteBody(t *testing.T) {
	t.Parallel()
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "applied"})
	}))
	defer server.Close()

	capability := validWriteCapability()
	capability.Backend.BaseURL = server.URL
	capability.InputSchema["detail"] = capabilities.InputField{Type: "string", In: "query"}

	adapter := capabilities.NewHTTPAdapter(nil)
	_, err := adapter.Execute(context.Background(), capability, map[string]any{
		"cluster":     "m1",
		"bucket":      "archive",
		"quota":       100,
		"detail":      "should-not-be-in-body",
	})
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	body := string(capturedBody)
	if strings.Contains(body, "should-not-be-in-body") {
		t.Fatalf("write body leaked query param: %s", body)
	}
	if !strings.Contains(body, `"quota":100`) {
		t.Fatalf("write body = %s, want quota field", body)
	}
}

func TestHTTPAdapterRetriesExhaustedReturnsError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	cfg := capabilities.AdapterConfig{
		MaxRetries:       2,
		InitialBackoff:   1 * time.Millisecond,
		MaxBackoff:       5 * time.Millisecond,
		FailureThreshold: 10,
		ResetTimeout:     1 * time.Second,
	}
	adapter := capabilities.NewHTTPAdapterWithConfig(nil, cfg)

	_, err := adapter.Execute(context.Background(), capability, map[string]any{"cluster": "m1", "bucket": "archive"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("err = %v, want HTTP 503 after retries exhausted", err)
	}
}

func TestHTTPAdapterInjectsBearerTokenOnRead(t *testing.T) {
	t.Parallel()
	var capturedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":{"usage_pct":86}}`))
	}))
	defer server.Close()

	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	capability.Backend.Auth = capabilities.BackendAuthConfig{
		Type:  "bearer",
		Token: "test-token-123",
	}

	result, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, map[string]any{
		"cluster": "m1", "bucket": "archive",
	})
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if capturedAuth != "Bearer test-token-123" {
		t.Fatalf("Authorization = %q, want 'Bearer test-token-123'", capturedAuth)
	}
	if result.Data["usage_pct"] != float64(86) {
		t.Fatalf("result data = %+v", result.Data)
	}
}

func TestHTTPAdapterInjectsBearerTokenOnWrite(t *testing.T) {
	t.Parallel()
	var capturedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"status":"applied"}`))
	}))
	defer server.Close()

	capability := validWriteCapability()
	capability.Backend.BaseURL = server.URL
	capability.Backend.Auth = capabilities.BackendAuthConfig{
		Type:  "bearer",
		Token: "write-token-456",
	}

	_, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, map[string]any{
		"cluster": "m1", "bucket": "archive", "quota": 100,
	})
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if capturedAuth != "Bearer write-token-456" {
		t.Fatalf("Authorization = %q, want 'Bearer write-token-456'", capturedAuth)
	}
}

func TestHTTPAdapterNoAuthWhenTypeEmpty(t *testing.T) {
	t.Parallel()
	var capturedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":{"usage_pct":86}}`))
	}))
	defer server.Close()

	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	// BackendAuthConfig zero value — no auth

	_, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, map[string]any{
		"cluster": "m1", "bucket": "archive",
	})
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if capturedAuth != "" {
		t.Fatalf("Authorization = %q, want empty", capturedAuth)
	}
}

// TestResourceNameKeyDerivesFromBackendPath 验证影响资源的字段名从 backend.path
// 路径变量派生（{bucket} 就是输入里的资源字段），而不是写死的字段名列表。优先匹配
// resource_type，无匹配时取第一个路径变量，路径无变量时为空。
func TestResourceNameKeyDerivesFromBackendPath(t *testing.T) {
	t.Parallel()
	cap := validReadCapability()
	if key := capabilities.ResourceNameKey(cap); key != "bucket" {
		t.Fatalf("ResourceNameKey = %q, want %q (path variable matching resource_type)", key, "bucket")
	}

	// 路径变量与 resource_type 不匹配时，回退到第一个路径变量。
	cap.ResourceType = "volume"
	if key := capabilities.ResourceNameKey(cap); key != "cluster" {
		t.Fatalf("ResourceNameKey = %q, want first path variable %q", key, "cluster")
	}

	// 路径无变量时返回空，调用方因此不展示影响资源。
	cap.Backend.Path = "/api/minio/buckets/all"
	if key := capabilities.ResourceNameKey(cap); key != "" {
		t.Fatalf("ResourceNameKey = %q, want empty for variable-less path", key)
	}
}
