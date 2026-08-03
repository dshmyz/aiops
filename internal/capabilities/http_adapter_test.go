package capabilities_test

import (
	"context"
	"encoding/json"
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

	result, err := adapter.Execute(context.Background(), capability, map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive"})
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if result.Kind != "observation" || result.Resource.Name != "archive" || result.Resource.Environment != "prod" || result.Data["usage_pct"] != float64(86) {
		t.Fatalf("result = %+v, want normalized observation", result)
	}
	if result.Summary != "Bucket archive usage is 86%" {
		t.Fatalf("summary = %q", result.Summary)
	}
	if _, ok := result.Data["secret_token"]; ok {
		t.Fatalf("result leaked redacted field: %+v", result.Data)
	}
}

func TestHTTPAdapterRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	capability := validReadCapability()
	adapter := capabilities.NewHTTPAdapter(nil)

	for name, input := range map[string]map[string]any{
		"wrong string type": {"environment": "prod", "cluster": "m1", "bucket": 42},
		"unknown input":     {"environment": "prod", "cluster": "m1", "bucket": "archive", "extra": "nope"},
		"missing required":  {"environment": "prod", "cluster": "m1"},
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
			input := map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive", "limit": test.value}

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
	input := map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive"}

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
		"environment": "prod",
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
	if !strings.Contains(body, `"quota":100`) || strings.Contains(body, `"environment"`) || strings.Contains(body, `"cluster"`) || strings.Contains(body, `"bucket"`) {
		t.Fatalf("body = %q, want JSON body with only non-path, non-environment fields", body)
	}
	if result.Kind != "mutation" || result.Resource.Name != "archive" || result.Resource.Environment != "prod" {
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
		"environment": "prod",
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
		"environment": "prod",
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

	if _, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive"}); err == nil || !strings.Contains(err.Error(), "raw output mapping") {
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

	result, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive"})
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
	result, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive"})
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
	input := map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive"}
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

	if _, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, map[string]any{"environment": "prod", "cluster": "m/1", "bucket": "archive/2026"}); err != nil {
		t.Fatalf("Execute returned %v", err)
	}
}

func TestHTTPAdapterRejectsOversizedResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"usage_pct":86,"padding":"` + strings.Repeat("x", 10240) + `"}}`))
	}))
	defer server.Close()
	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL

	if _, err := capabilities.NewHTTPAdapter(nil).Execute(context.Background(), capability, map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive"}); err == nil {
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

	result, err := adapter.Execute(context.Background(), capability, map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive"})
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
		"environment": "prod",
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
	input := map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive"}

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
	input := map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive"}

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
		"environment": "prod",
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
		"environment": "prod",
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

	_, err := adapter.Execute(context.Background(), capability, map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("err = %v, want HTTP 503 after retries exhausted", err)
	}
}
