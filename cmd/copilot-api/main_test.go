package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/mcp"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
	"go.uber.org/zap"
)

func TestPublishedCapabilitiesAreOptional(t *testing.T) {
	t.Setenv("COPILOT_CAPABILITIES_DIR", "")
	loaded, err := publishedCapabilitiesFromEnv()
	if err != nil {
		t.Fatalf("publishedCapabilitiesFromEnv returned %v", err)
	}
	if loaded == nil || len(loaded) != 0 {
		t.Fatalf("loaded = %+v, want empty capabilities", loaded)
	}
}

func TestPublishedCapabilitiesFailClosedWhenConfiguredDirectoryIsInvalid(t *testing.T) {
	t.Setenv("COPILOT_CAPABILITIES_DIR", t.TempDir())
	if _, err := publishedCapabilitiesFromEnv(); err == nil {
		t.Fatal("publishedCapabilitiesFromEnv accepted missing published directory")
	}
}

func TestAssistantPlannerFromEnvIsCapabilityAware(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
		Tool: tools.Tool{Name: "minio.bucket.capacity.read", Operation: tools.Read, Risk: tools.Low, Domain: "minio", ResourceType: "bucket"},
		InputSchema: map[string]tools.DynamicInputField{
			"environment": {Type: "string", Required: true},
			"cluster":     {Type: "string", Required: true},
			"bucket":      {Type: "string", Required: true},
		},
	}})
	if err != nil {
		t.Fatalf("register dynamic: %v", err)
	}

	planner, _, _, _, mode, err := assistantPlannerFromEnv(context.Background(), map[string]string{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("assistantPlannerFromEnv returned %v", err)
	}
	if mode != "deterministic+capabilities+actions" {
		t.Fatalf("mode = %q, want deterministic+capabilities+actions", mode)
	}
	intent, err := planner.Plan(context.Background(), identity.CurrentUser{}, "查一下 prod m1 archive bucket 的 minio 容量", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan returned %v", err)
	}
	if intent.ToolName != "minio.bucket.capacity.read" {
		t.Fatalf("tool = %q, want dynamic capability", intent.ToolName)
	}
}

func TestServeHTTPServesHealthAndStopsAfterCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- serveHTTP(ctx, listener, http.NotFoundHandler(), nil, nil, nil)
	}()

	response := waitForHealth(t, "http://"+listener.Addr().String()+"/healthz")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read health response: %v", err)
	}
	if string(body) != "ok\n" {
		t.Fatalf("health body = %q, want %q", body, "ok\\n")
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("serve HTTP: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop after context cancellation")
	}
}

func TestHealthHandlerMountsAPIUnderV1(t *testing.T) {
	handler := healthHandler(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
	}), nil, nil, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/tools/cluster.status.read/read", nil)

	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("api status = %d, want %d", recorder.Code, http.StatusAccepted)
	}
}

func TestHTTPAddressDefaultsAwayFromCommonFrontendPorts(t *testing.T) {
	t.Setenv("COPILOT_HTTP_ADDR", "")

	if got := httpAddress(); got != ":18080" {
		t.Fatalf("httpAddress() = %q, want %q", got, ":18080")
	}
}

func TestRouterOptionsWireActionPlanQueries(t *testing.T) {
	repository := store.NewMemoryActionPlanStore()
	handler := httpapi.NewRouter(
		stubAuthenticator{},
		nil,
		routerOptions(repository, nil, nil, nil, nil, nil)...,
	)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/action-plans?status=pending_confirmation", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want %d", recorder.Code, recorder.Body.String(), http.StatusOK)
	}
}

func TestCapabilityManagerFromEnvIsOptional(t *testing.T) {
	t.Setenv("COPILOT_CAPABILITIES_DIR", "")

	if manager := capabilityManagerFromEnv(nil, nil); manager != nil {
		t.Fatalf("capabilityManagerFromEnv() = %#v, want nil when root is not configured", manager)
	}
}

func TestRouterOptionsWireCapabilityManagementWhenConfigured(t *testing.T) {
	root := t.TempDir()
	t.Setenv("COPILOT_CAPABILITIES_DIR", root)
	repository := store.NewMemoryActionPlanStore()
	handler := httpapi.NewRouter(
		stubAuthenticator{},
		nil,
		routerOptions(repository, nil, nil, nil, capabilityManagerFromEnv(nil, nil), nil)...,
	)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want capability management route", recorder.Code, recorder.Body.String())
	}
}

func TestCapabilityManagerFromEnvHotRegistersPublishedCapability(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)

	root := t.TempDir()
	t.Setenv("COPILOT_CAPABILITIES_DIR", root)
	writeFile(t, filepath.Join(root, "discovered", "minio.bucket.capacity.read.yaml"), validReadCapabilityYAML("needs_review"))
	_, writeExecutor, _, runtime := buildCapabilityRuntimes(nil, capabilities.NewHTTPAdapter(http.DefaultClient), true, staticWriteExecutor{})
	adapter := capabilities.NewHTTPAdapter(http.DefaultClient)

	manager := capabilityManagerFromEnv(adapter, runtime)
	if manager == nil {
		t.Fatal("capabilityManagerFromEnv returned nil")
	}
	published, err := manager.Publish(context.Background(), "minio.bucket.capacity.read")
	if err != nil {
		t.Fatalf("Publish returned %v", err)
	}
	tool, ok := tools.Lookup(published.Name)
	if !ok {
		t.Fatal("published capability was not hot-registered")
	}
	decision := policy.Evaluate(identity.CurrentUser{Roles: []string{"viewer"}, AllowedEnvironments: []string{"prod"}}, tool, map[string]any{
		"environment": "prod",
		"cluster":     "m1",
		"bucket":      "archive",
	})
	if !decision.Allowed {
		t.Fatalf("published capability policy denied after hot registration: %+v", decision)
	}
	// Write executor must remain a no-op runner for read-only setups so static
	// write tools (e.g. topic.retention.set) keep working via the fallback.
	if _, ok := writeExecutor.(*capabilities.CapabilityWriteRunner); !ok {
		t.Fatalf("write executor = %T, want *CapabilityWriteRunner even without loaded capabilities", writeExecutor)
	}
}

func TestCapabilityRuntimesRouteHotPublishedWriteThroughHTTPAdapter(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)

	var capturedMethod, capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "applied"})
	}))
	defer server.Close()

	root := t.TempDir()
	t.Setenv("COPILOT_CAPABILITIES_DIR", root)
	writeFile(t, filepath.Join(root, "discovered", "minio.bucket.quota.set.yaml"), validWriteCapabilityYAML("needs_review", server.URL))
	if err := os.MkdirAll(filepath.Join(root, "published"), 0o755); err != nil {
		t.Fatalf("mkdir published: %v", err)
	}

	loaded, err := publishedCapabilitiesFromEnv()
	if err != nil {
		t.Fatalf("publishedCapabilitiesFromEnv returned %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("loaded %d capabilities, want 0 published at startup", len(loaded))
	}

	_, writeExecutor, _, runtime := buildCapabilityRuntimes(nil, capabilities.NewHTTPAdapter(server.Client()), true, staticWriteExecutor{})
	manager := capabilityManagerFromEnv(capabilities.NewHTTPAdapter(server.Client()), runtime)
	if manager == nil {
		t.Fatal("capabilityManagerFromEnv returned nil")
	}
	published, err := manager.Publish(context.Background(), "minio.bucket.quota.set")
	if err != nil {
		t.Fatalf("Publish returned %v", err)
	}
	tool, ok := tools.Lookup(published.Name)
	if !ok || tool.Operation != tools.Write {
		t.Fatalf("Lookup = %+v, %v; want hot-registered write tool", tool, ok)
	}

	runner, ok := writeExecutor.(*capabilities.CapabilityWriteRunner)
	if !ok {
		t.Fatalf("write executor = %T, want *CapabilityWriteRunner", writeExecutor)
	}
	result, err := runner.Execute(context.Background(), tool.Name, map[string]any{
		"environment": "prod",
		"cluster":     "m1",
		"bucket":      "archive",
		"quota":       100,
	})
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if capturedMethod != http.MethodPost || capturedPath != "/api/minio/clusters/m1/buckets/archive/quota" {
		t.Fatalf("captured = %s %s, want POST quota endpoint", capturedMethod, capturedPath)
	}
	if result["summary"] != "Bucket archive quota set to 100" {
		t.Fatalf("result = %+v, want adapter-normalized write response", result)
	}
}

func TestCapabilityRuntimesFallBackToStaticWhenUnconfigured(t *testing.T) {
	_, writeExecutor, _, runtime := buildCapabilityRuntimes(nil, nil, false, staticWriteExecutor{})

	if _, ok := writeExecutor.(staticWriteExecutor); !ok {
		t.Fatalf("write executor = %T, want staticWriteExecutor when capabilities are unconfigured", writeExecutor)
	}
	if runtime != nil {
		t.Fatalf("runtime = %T, want nil when capabilities are unconfigured", runtime)
	}
}

type stubAuthenticator struct{}

func (stubAuthenticator) Authenticate(*http.Request) (identity.CurrentUser, error) {
	return identity.CurrentUser{Roles: []string{"admin"}, AllowedEnvironments: []string{"prod"}}, nil
}

func waitForHealth(t *testing.T, url string) *http.Response {
	t.Helper()

	client := &http.Client{Timeout: 100 * time.Millisecond}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(url)
		if err == nil {
			return response
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("health endpoint %q did not become ready", url)
	return nil
}

func writeFile(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func validReadCapabilityYAML(status string) string {
	return `schema_version: 1
name: minio.bucket.capacity.read
status: ` + status + `
domain: minio
resource_type: bucket
operation: read
risk: low
backend:
  adapter: http
  method: GET
  base_url: https://backend.example.com
  path: /api/minio/clusters/{cluster}/buckets/{bucket}/capacity
  timeout_ms: 3000
input_schema:
  environment:
    type: string
    required: true
  cluster:
    type: string
    required: true
  bucket:
    type: string
    required: true
output:
  kind: observation
  summary_template: Bucket {bucket} usage is {usage_pct}%
  fields:
    usage_pct: $.data.usage_pct
auth:
  roles: [viewer, operator, admin]
  environment_scoped: true
ai:
  description: Read bucket capacity.
`
}

func validWriteCapabilityYAML(status, baseURL string) string {
	return `schema_version: 1
name: minio.bucket.quota.set
status: ` + status + `
domain: minio
resource_type: bucket
operation: write
risk: medium
backend:
  adapter: http
  method: POST
  base_url: ` + baseURL + `
  path: /api/minio/clusters/{cluster}/buckets/{bucket}/quota
  timeout_ms: 3000
input_schema:
  environment:
    type: string
    required: true
  cluster:
    type: string
    required: true
  bucket:
    type: string
    required: true
  quota:
    type: integer
    required: true
output:
  kind: mutation
  summary_template: Bucket {bucket} quota set to {quota}
auth:
  roles: [operator, admin]
  environment_scoped: true
governance:
  requires_action_plan: true
  requires_approval: true
  precheck_tools: [minio.bucket.capacity.read]
  rollback:
    strategy: restore_previous
ai:
  description: Set bucket quota.
`
}

func TestLoadDotEnvSetsVariablesFromFile(t *testing.T) {
	// 不并行：操作 os.Environ 会污染并行测试
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	content := "COPILOT_OPENAI_MODEL=gpt-4o\nCOPILOT_OPENAI_BASE_URL=https://api.openai.com/v1\n"
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	t.Setenv("COPILOT_OPENAI_MODEL", "")
	t.Setenv("COPILOT_OPENAI_BASE_URL", "")
	if err := loadDotEnv(envPath); err != nil {
		t.Fatalf("loadDotEnv returned %v", err)
	}
	if got := os.Getenv("COPILOT_OPENAI_MODEL"); got != "gpt-4o" {
		t.Errorf("COPILOT_OPENAI_MODEL = %q, want %q", got, "gpt-4o")
	}
	if got := os.Getenv("COPILOT_OPENAI_BASE_URL"); got != "https://api.openai.com/v1" {
		t.Errorf("COPILOT_OPENAI_BASE_URL = %q, want %q", got, "https://api.openai.com/v1")
	}
}

func TestLoadDotEnvSkipsCommentsAndBlankLines(t *testing.T) {
	// 不并行：操作 os.Environ
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	content := `# 这是注释
COPILOT_HTTP_ADDR=:8080

# 另一段注释
COPILOT_DATABASE_DRIVER=sqlite
`
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	t.Setenv("COPILOT_HTTP_ADDR", "")
	t.Setenv("COPILOT_DATABASE_DRIVER", "")
	if err := loadDotEnv(envPath); err != nil {
		t.Fatalf("loadDotEnv returned %v", err)
	}
	if got := os.Getenv("COPILOT_HTTP_ADDR"); got != ":8080" {
		t.Errorf("COPILOT_HTTP_ADDR = %q, want %q", got, ":8080")
	}
	if got := os.Getenv("COPILOT_DATABASE_DRIVER"); got != "sqlite" {
		t.Errorf("COPILOT_DATABASE_DRIVER = %q, want %q", got, "sqlite")
	}
}

func TestLoadDotEnvStripsSurroundingQuotes(t *testing.T) {
	// 不并行：操作 os.Environ
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	content := `COPILOT_OPENAI_API_KEY="sk-secret-with-spaces"
COPILOT_OPENAI_BASE_URL='https://example.invalid/v1'
`
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	t.Setenv("COPILOT_OPENAI_API_KEY", "")
	t.Setenv("COPILOT_OPENAI_BASE_URL", "")
	if err := loadDotEnv(envPath); err != nil {
		t.Fatalf("loadDotEnv returned %v", err)
	}
	if got := os.Getenv("COPILOT_OPENAI_API_KEY"); got != "sk-secret-with-spaces" {
		t.Errorf("COPILOT_OPENAI_API_KEY = %q, want %q", got, "sk-secret-with-spaces")
	}
	if got := os.Getenv("COPILOT_OPENAI_BASE_URL"); got != "https://example.invalid/v1" {
		t.Errorf("COPILOT_OPENAI_BASE_URL = %q, want %q", got, "https://example.invalid/v1")
	}
}

func TestLoadDotEnvDoesNotOverrideExistingEnv(t *testing.T) {
	// 不并行：操作 os.Environ
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	content := "COPILOT_OPENAI_MODEL=from-file\nCOPILOT_HTTP_ADDR=:9999\n"
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	// 真实环境变量已存在，文件里的值不应该覆盖
	t.Setenv("COPILOT_OPENAI_MODEL", "from-env")
	// COPILOT_HTTP_ADDR 未设置，应被文件设置
	t.Setenv("COPILOT_HTTP_ADDR", "")

	if err := loadDotEnv(envPath); err != nil {
		t.Fatalf("loadDotEnv returned %v", err)
	}
	if got := os.Getenv("COPILOT_OPENAI_MODEL"); got != "from-env" {
		t.Errorf("COPILOT_OPENAI_MODEL = %q, want %q (env should win)", got, "from-env")
	}
	if got := os.Getenv("COPILOT_HTTP_ADDR"); got != ":9999" {
		t.Errorf("COPILOT_HTTP_ADDR = %q, want %q (file fills unset var)", got, ":9999")
	}
}

func TestLoadDotEnvReturnsNilWhenFileMissing(t *testing.T) {
	// 不并行：文件操作虽安全，但保持与其他 loadDotEnv 测试一致
	dir := t.TempDir()
	missingPath := filepath.Join(dir, "does-not-exist.env")
	if err := loadDotEnv(missingPath); err != nil {
		t.Fatalf("loadDotEnv on missing file should return nil, got %v", err)
	}
}

func TestLoadDotEnvIgnoresMalformedLines(t *testing.T) {
	// 不并行：操作 os.Environ
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	// 行 1 没有等号；行 2 key 为空；行 3 合法；行 4 只有等号无 value
	content := "THIS_HAS_NO_EQUALS\n=missing_key\nCOPILOT_OTEL_EXPORTER=otel\n=\n"
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	t.Setenv("COPILOT_OTEL_EXPORTER", "")
	if err := loadDotEnv(envPath); err != nil {
		t.Fatalf("loadDotEnv returned %v", err)
	}
	if got := os.Getenv("COPILOT_OTEL_EXPORTER"); got != "otel" {
		t.Errorf("COPILOT_OTEL_EXPORTER = %q, want %q (合法行应被正常加载)", got, "otel")
	}
}

// TestMCPEventToAuditEventMapsAllEventTypes verifies that the MCP→audit
// conversion covers all three event types (收口3: MCP degraded 接线).
// Previously EventTypeHealthDegraded was silently dropped because main.go's
// emitter only handled EventTypeHealthUnhealthy.
func TestMCPEventToAuditEventMapsAllEventTypes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		eventType    mcp.EventType
		wantAction   string
		wantDecision string
	}{
		{
			name:         "unhealthy maps to mcp_health_unhealthy + denied",
			eventType:    mcp.EventTypeHealthUnhealthy,
			wantAction:   audit.ActionMCPHealthUnhealthy,
			wantDecision: audit.DecisionDenied,
		},
		{
			name:         "degraded maps to mcp_health_degraded + permitted",
			eventType:    mcp.EventTypeHealthDegraded,
			wantAction:   audit.ActionMCPHealthDegraded,
			wantDecision: audit.DecisionPermitted,
		},
		{
			name:         "tools_changed maps to mcp_tools_changed + permitted",
			eventType:    mcp.EventTypeToolsChanged,
			wantAction:   audit.ActionMCPToolsChanged,
			wantDecision: audit.DecisionPermitted,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			event := mcp.MCPEvent{
				Type:       tc.eventType,
				ServerName: "test-server",
				Message:    "test message",
				Metadata:   map[string]any{"latency_ms": int64(42)},
			}
			now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
			got := mcpEventToAuditEvent(event, now)
			if got.Action != tc.wantAction {
				t.Fatalf("Action = %q, want %q", got.Action, tc.wantAction)
			}
			if got.Decision != tc.wantDecision {
				t.Fatalf("Decision = %q, want %q", got.Decision, tc.wantDecision)
			}
			if got.ToolName != "test-server" {
				t.Fatalf("ToolName = %q, want test-server", got.ToolName)
			}
			if got.CreatedAt != now {
				t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, now)
			}
			if got.Metadata["latency_ms"] != int64(42) {
				t.Fatalf("Metadata = %+v, want latency_ms=42", got.Metadata)
			}
		})
	}
}

func TestCasSessionTTLFromEnv(t *testing.T) {
	logger := zap.NewNop()

	t.Run("parses valid duration", func(t *testing.T) {
		t.Setenv("COPILOT_CAS_SESSION_TTL", "30m")
		if got := casSessionTTL(logger); got != 30*time.Minute {
			t.Fatalf("casSessionTTL = %v, want 30m", got)
		}
	})

	t.Run("empty falls back to zero (authenticator default)", func(t *testing.T) {
		t.Setenv("COPILOT_CAS_SESSION_TTL", "")
		if got := casSessionTTL(logger); got != 0 {
			t.Fatalf("casSessionTTL = %v, want 0", got)
		}
	})

	t.Run("invalid falls back to zero with warning", func(t *testing.T) {
		t.Setenv("COPILOT_CAS_SESSION_TTL", "not-a-duration")
		if got := casSessionTTL(logger); got != 0 {
			t.Fatalf("casSessionTTL = %v, want 0", got)
		}
	})
}

func TestCasJSONListFromEnv(t *testing.T) {
	logger := zap.NewNop()

	t.Run("parses JSON array", func(t *testing.T) {
		t.Setenv("COPILOT_CAS_DEFAULT_ROLES", `["admin","operator"]`)
		got := casJSONList("COPILOT_CAS_DEFAULT_ROLES", logger)
		want := []string{"admin", "operator"}
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("casJSONList = %v, want %v", got, want)
		}
	})

	t.Run("empty returns nil (authenticator default)", func(t *testing.T) {
		t.Setenv("COPILOT_CAS_DEFAULT_ROLES", "")
		if got := casJSONList("COPILOT_CAS_DEFAULT_ROLES", logger); got != nil {
			t.Fatalf("casJSONList = %v, want nil", got)
		}
	})

	t.Run("invalid JSON returns nil", func(t *testing.T) {
		t.Setenv("COPILOT_CAS_DEFAULT_ROLES", `admin,operator`)
		if got := casJSONList("COPILOT_CAS_DEFAULT_ROLES", logger); got != nil {
			t.Fatalf("casJSONList = %v, want nil", got)
		}
	})
}
