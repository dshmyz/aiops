package capabilities_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestProbeGenerateReadProbesRealBackend(t *testing.T) {
	t.Parallel()
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/minio/clusters/m1/buckets/archive/capacity" {
			t.Fatalf("path = %q, want real backend capacity path", request.URL.Path)
		}
		atomic.AddInt32(&hits, 1)
		_ = json.NewEncoder(writer).Encode(map[string]any{"data": map[string]any{"usage_pct": 88}})
	}))
	defer server.Close()

	manager := capabilities.NewManager(t.TempDir(), capabilities.NewHTTPAdapter(server.Client()))
	capability, probe, err := manager.ProbeAndGenerate(context.Background(), capabilities.ProbeGenerateRequest{
		Backend: capabilities.BackendSpec{
			Adapter: "http",
			Method:  http.MethodGet,
			BaseURL: server.URL,
			Path:    "/api/minio/clusters/{cluster}/buckets/{bucket}/capacity",
		},
		Description: "Read MinIO bucket capacity",
		Input:       map[string]any{"cluster": "m1", "bucket": "archive"},
	})
	if err != nil {
		t.Fatalf("ProbeAndGenerate returned %v", err)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("backend probed hits = %d, want real probe to have happened", hits)
	}
	if capability.Operation != tools.Read || capability.Risk != tools.Low {
		t.Fatalf("operation=%q risk=%q, want read/low", capability.Operation, capability.Risk)
	}
	if capability.Status != capabilities.StatusDiscovered {
		t.Fatalf("status = %q, want discovered draft", capability.Status)
	}
	if capability.Backend.Method != http.MethodGet || capability.Backend.BaseURL != server.URL {
		t.Fatalf("backend = %+v, want GET against probed base_url", capability.Backend)
	}
	if probe.Probe == nil || probe.Probe.Data["data.usage_pct"] != "88" {
		t.Fatalf("probe = %+v, want normalized result with flattened data.usage_pct", probe.Probe)
	}
	if probe.Inferred == nil || probe.Inferred.Fields["data.usage_pct"] == "" {
		t.Fatalf("inferred = %+v, want output fields mapped from real response", probe.Inferred)
	}
}

func TestProbeGenerateWriteUsesSampleBodyWithoutProbing(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		t.Fatalf("write capability must not issue a real HTTP request, got %s %s", request.Method, request.URL.Path)
	}))
	defer server.Close()

	manager := capabilities.NewManager(t.TempDir(), capabilities.NewHTTPAdapter(server.Client()))
	capability, probe, err := manager.ProbeAndGenerate(context.Background(), capabilities.ProbeGenerateRequest{
		Backend: capabilities.BackendSpec{
			Adapter: "http",
			Method:  http.MethodDelete,
			BaseURL: server.URL,
			Path:    "/api/redis/clusters/{cluster}/keys/{key}",
		},
		Description: "Delete a Redis key",
		SampleBody:  `{"success":true,"deleted":1}`,
	})
	if err != nil {
		t.Fatalf("ProbeAndGenerate returned %v", err)
	}
	if capability.Operation != tools.Write || capability.Risk != tools.High {
		t.Fatalf("operation=%q risk=%q, want write/high for DELETE", capability.Operation, capability.Risk)
	}
	if probe.Inferred == nil {
		t.Fatalf("inferred missing, want offline mapping from sample body")
	}
	if probe.InferredBy != "rules" {
		t.Fatalf("inferred_by = %q, want rules fallback without LLM", probe.InferredBy)
	}
	if probe.Probe == nil || probe.Probe.Data["deleted"] != "1" {
		t.Fatalf("probe = %+v, want normalized action_result from sample body", probe.Probe)
	}
}

func TestProbeGenerateValidatesRequest(t *testing.T) {
	t.Parallel()
	manager := capabilities.NewManager(t.TempDir(), nil)
	base := capabilities.BackendSpec{Adapter: "http", BaseURL: "https://middleware.example.com", Path: "/api/x"}
	cases := []struct {
		name    string
		backend capabilities.BackendSpec
	}{
		{"unsupported_method", capabilities.BackendSpec{Method: http.MethodOptions, BaseURL: base.BaseURL, Path: base.Path}},
		{"missing_path", capabilities.BackendSpec{Method: http.MethodGet, BaseURL: base.BaseURL}},
		{"invalid_base_url", capabilities.BackendSpec{Method: http.MethodGet, BaseURL: "not-a-url", Path: base.Path}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := manager.ProbeAndGenerate(context.Background(), capabilities.ProbeGenerateRequest{Backend: tc.backend}); err == nil {
				t.Fatalf("ProbeAndGenerate(%s) = nil err, want validation error", tc.name)
			}
		})
	}
}