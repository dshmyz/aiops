package capabilities_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestRegisterPublishedRegistersToolAndRolePermissions(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)

	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "published", "minio.bucket.capacity.read.yaml"), validReadYAML("published"))
	loaded, err := capabilities.RegisterPublished(root)
	if err != nil {
		t.Fatalf("RegisterPublished returned %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d capabilities, want 1", len(loaded))
	}
	tool, ok := tools.Lookup("minio.bucket.capacity.read")
	if !ok || tool.Operation != tools.Read {
		t.Fatalf("Lookup = %+v, %v; want registered read tool", tool, ok)
	}
	decision := policy.Evaluate(identity.CurrentUser{Roles: []string{"viewer"}, AllowedEnvironments: []string{"prod"}}, tool, map[string]any{
		"environment": "prod",
		"cluster":     "m1",
		"bucket":      "archive",
	})
	if !decision.Allowed {
		t.Fatalf("published viewer permission denied: %+v", decision)
	}
}

func TestRegisterPublishedCapabilityAddsToolAndRolePermission(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)

	capability := managedReadCapability("minio.bucket.capacity.read", "published")
	if err := capabilities.RegisterPublishedCapability(capability); err != nil {
		t.Fatalf("RegisterPublishedCapability returned %v", err)
	}
	tool, ok := tools.Lookup("minio.bucket.capacity.read")
	if !ok || tool.Operation != tools.Read {
		t.Fatalf("Lookup = %+v, %v; want hot-registered read tool", tool, ok)
	}
	decision := policy.Evaluate(identity.CurrentUser{Roles: []string{"viewer"}, AllowedEnvironments: []string{"prod"}}, tool, map[string]any{
		"environment": "prod",
		"cluster":     "m1",
		"bucket":      "archive",
	})
	if !decision.Allowed {
		t.Fatalf("viewer permission denied after hot registration: %+v", decision)
	}
}

func TestRegisterPublishedRegistersWriteCapabilityAsTool(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)

	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "published", "minio.bucket.capacity.read.yaml"), validReadYAML("published"))
	mustWrite(t, filepath.Join(root, "published", "minio.bucket.capacity.set.yaml"), validWriteYAML())

	loaded, err := capabilities.RegisterPublished(root)
	if err != nil {
		t.Fatalf("RegisterPublished returned %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded %d capabilities, want read and write", len(loaded))
	}
	writeTool, ok := tools.Lookup("minio.bucket.capacity.set")
	if !ok || writeTool.Operation != tools.Write || writeTool.Risk != tools.Medium {
		t.Fatalf("Lookup = %+v, %v; want registered write tool", writeTool, ok)
	}
	if writeTool.RollbackDescription == "" {
		t.Fatalf("write tool rollback description must be populated from governance")
	}
	decision := policy.Evaluate(identity.CurrentUser{Roles: []string{"admin"}, AllowedEnvironments: []string{"prod"}}, writeTool, map[string]any{
		"environment": "prod",
		"cluster":     "m1",
		"bucket":      "archive",
	})
	if !decision.Allowed || !decision.RequiresConfirmation {
		t.Fatalf("published write decision = %+v, want allowed with confirmation", decision)
	}
}

func TestRegisterPublishedRejectsWriteThatConflictsWithStaticTool(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)

	root := t.TempDir()
	conflicting := strings.Replace(validWriteYAML(), "name: minio.bucket.capacity.set", "name: "+tools.ClusterStatusRead, 1)
	mustWrite(t, filepath.Join(root, "published", "cluster.status.read.yaml"), conflicting)

	if _, err := capabilities.RegisterPublished(root); err == nil {
		t.Fatal("RegisterPublished accepted capability conflicting with static tool")
	}
}

func TestRegisterPublishedCapabilityRegistersWriteTool(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)

	capability := writeCapabilityForRegister()
	if err := capabilities.RegisterPublishedCapability(capability); err != nil {
		t.Fatalf("RegisterPublishedCapability returned %v", err)
	}
	tool, ok := tools.Lookup(capability.Name)
	if !ok || tool.Operation != tools.Write {
		t.Fatalf("Lookup = %+v, %v; want hot-registered write tool", tool, ok)
	}
	decision := policy.Evaluate(identity.CurrentUser{Roles: []string{"admin"}, AllowedEnvironments: []string{"prod"}}, tool, map[string]any{
		"environment": "prod",
		"cluster":     "m1",
		"bucket":      "archive",
		"quota":       100,
	})
	if !decision.Allowed || !decision.RequiresConfirmation {
		t.Fatalf("hot-registered write decision = %+v, want allowed with confirmation", decision)
	}
}

func TestCapabilityReadRunnerExecutesPublishedReadThroughHTTPAdapter(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/minio/clusters/m1/buckets/archive/capacity" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"usage_pct": 86}})
	}))
	defer server.Close()

	capability := validReadYAML("published")
	capability = strings.Replace(capability, "  base_url: https://backend.example.com\n", "  base_url: "+server.URL+"\n", 1)
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "published", "minio.bucket.capacity.read.yaml"), capability)
	loaded, err := capabilities.RegisterPublished(root)
	if err != nil {
		t.Fatalf("RegisterPublished returned %v", err)
	}
	tool, ok := tools.Lookup("minio.bucket.capacity.read")
	if !ok {
		t.Fatal("published read capability was not registered")
	}

	runner := capabilities.NewCapabilityReadRunner(readRunnerFunc(func(context.Context, tools.Tool, map[string]any) (map[string]any, error) {
		t.Fatal("published read fell through to static runner")
		return nil, nil
	}), loaded, capabilities.NewHTTPAdapter(http.DefaultClient))
	result, err := runner.Read(context.Background(), tool, map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive"})
	if err != nil {
		t.Fatalf("Read returned %v", err)
	}
	if result["summary"] != "Bucket archive usage is 86%" {
		t.Fatalf("result = %+v, want adapter-normalized response", result)
	}
}

func TestCapabilityReadRunnerAddsPublishedCapabilityAfterStartup(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/minio/clusters/m1/buckets/archive/capacity" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"usage_pct": 91}})
	}))
	defer server.Close()

	runner := capabilities.NewCapabilityReadRunner(readRunnerFunc(func(context.Context, tools.Tool, map[string]any) (map[string]any, error) {
		t.Fatal("hot-published read fell through to static runner")
		return nil, nil
	}), nil, capabilities.NewHTTPAdapter(server.Client()))
	capability := managedReadCapability("minio.bucket.capacity.read", "published")
	capability.Backend.BaseURL = server.URL
	if err := capabilities.RegisterPublishedCapability(capability); err != nil {
		t.Fatalf("RegisterPublishedCapability returned %v", err)
	}
	if err := runner.AddPublishedCapability(capability); err != nil {
		t.Fatalf("AddPublishedCapability returned %v", err)
	}
	tool, ok := tools.Lookup("minio.bucket.capacity.read")
	if !ok {
		t.Fatal("hot-published tool was not registered")
	}

	result, err := runner.Read(context.Background(), tool, map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive"})
	if err != nil {
		t.Fatalf("Read returned %v", err)
	}
	if result["summary"] != "Bucket archive usage is 91%" {
		t.Fatalf("result = %+v, want hot-published adapter response", result)
	}
}

func TestCapabilityReadRunnerDelegatesStaticTools(t *testing.T) {
	called := false
	next := readRunnerFunc(func(_ context.Context, tool tools.Tool, input map[string]any) (map[string]any, error) {
		called = true
		return map[string]any{"tool": tool.Name, "environment": input["environment"]}, nil
	})
	runner := capabilities.NewCapabilityReadRunner(next, []capabilities.Capability{{
		Name:      tools.ClusterStatusRead,
		Operation: tools.Write,
		Backend:   capabilities.BackendSpec{Method: "POST"},
	}}, nil)

	result, err := runner.Read(context.Background(), tools.Tool{Name: tools.ClusterStatusRead}, map[string]any{"environment": "prod"})
	if err != nil {
		t.Fatalf("Read returned %v", err)
	}
	if !called || result["tool"] != tools.ClusterStatusRead {
		t.Fatalf("result = %+v, called = %v; want static delegation", result, called)
	}
}

func TestCapabilityReadRunnerSkipsPublishedWriteCapability(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)

	called := false
	next := readRunnerFunc(func(_ context.Context, tool tools.Tool, _ map[string]any) (map[string]any, error) {
		called = true
		return map[string]any{"tool": tool.Name}, nil
	})
	writeCapability := writeCapabilityForRegister()
	if err := capabilities.RegisterPublishedCapability(writeCapability); err != nil {
		t.Fatalf("RegisterPublishedCapability returned %v", err)
	}
	runner := capabilities.NewCapabilityReadRunner(next, []capabilities.Capability{writeCapability}, nil)
	if err := runner.AddPublishedCapability(writeCapability); err != nil {
		t.Fatalf("AddPublishedCapability returned %v", err)
	}
	tool, ok := tools.Lookup(writeCapability.Name)
	if !ok {
		t.Fatal("write tool was not registered")
	}

	result, err := runner.Read(context.Background(), tool, map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive", "quota": 100})
	if err != nil {
		t.Fatalf("Read returned %v", err)
	}
	if !called {
		t.Fatalf("read runner handled write capability; want delegation to next runner")
	}
	if result["tool"] != writeCapability.Name {
		t.Fatalf("result = %+v, want next-runner response", result)
	}
}

func TestCapabilityReadRunnerDoesNotShadowStaticToolFromBypassedLoader(t *testing.T) {
	called := false
	next := readRunnerFunc(func(_ context.Context, tool tools.Tool, _ map[string]any) (map[string]any, error) {
		called = true
		return map[string]any{"tool": tool.Name}, nil
	})
	runner := capabilities.NewCapabilityReadRunner(next, []capabilities.Capability{{
		Name:      tools.ClusterStatusRead,
		Status:    capabilities.StatusPublished,
		Operation: tools.Read,
		Backend:   capabilities.BackendSpec{Method: http.MethodGet},
	}}, nil)

	result, err := runner.Read(context.Background(), tools.Tool{Name: tools.ClusterStatusRead}, nil)
	if err != nil {
		t.Fatalf("Read returned %v", err)
	}
	if !called || result["tool"] != tools.ClusterStatusRead {
		t.Fatalf("result = %+v, called = %v; want static delegation", result, called)
	}
}

func TestCapabilityReadRunnerDoesNotShadowStaticDomainToolFromBypassedLoader(t *testing.T) {
	called := false
	next := readRunnerFunc(func(_ context.Context, tool tools.Tool, _ map[string]any) (map[string]any, error) {
		called = true
		return map[string]any{"tool": tool.Name}, nil
	})
	// ClusterStatusRead is a platform meta-tool still owned by the static
	// allowlist (middleware tools were externalized as dynamic capabilities, but
	// the platform meta-reads stay static). A capability that happens to share a
	// static tool's name must never shadow it: the read runner delegates to the
	// static chain instead of the HTTP adapter.
	runner := capabilities.NewCapabilityReadRunner(next, []capabilities.Capability{{
		SchemaVersion: 1,
		Name:          tools.ClusterStatusRead,
		Status:        capabilities.StatusPublished,
		Domain:        "system",
		ResourceType:  "posture",
		Operation:     tools.Read,
		Risk:          tools.Low,
		Backend: capabilities.BackendSpec{
			Adapter:   "http",
			BaseURL:   "https://backend.example.com",
			Method:    http.MethodGet,
			Path:      "/api/status",
			TimeoutMS: 3000,
		},
		InputSchema: map[string]capabilities.InputField{
			"environment": {Type: "string", Required: true},
		},
		Output: capabilities.OutputSpec{
			Kind:            "observation",
			SummaryTemplate: "posture",
		},
		Auth: capabilities.AuthSpec{
			Roles:             []string{"viewer"},
			EnvironmentScoped: true,
		},
	}}, nil)

	result, err := runner.Read(context.Background(), tools.Tool{Name: tools.ClusterStatusRead}, map[string]any{"environment": "prod"})
	if err != nil {
		t.Fatalf("Read returned %v", err)
	}
	if !called || result["tool"] != tools.ClusterStatusRead {
		t.Fatalf("result = %+v, called = %v; want static domain delegation", result, called)
	}
}

type readRunnerFunc func(context.Context, tools.Tool, map[string]any) (map[string]any, error)

func (f readRunnerFunc) Read(ctx context.Context, tool tools.Tool, input map[string]any) (map[string]any, error) {
	return f(ctx, tool, input)
}

type writeExecutorFunc func(context.Context, string, map[string]any) (map[string]any, error)

func (f writeExecutorFunc) Execute(ctx context.Context, name string, input map[string]any) (map[string]any, error) {
	return f(ctx, name, input)
}

func TestCapabilityWriteRunnerExecutesPublishedWriteThroughHTTPAdapter(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/minio/clusters/m1/buckets/archive/quota" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "applied"})
	}))
	defer server.Close()

	capability := writeCapabilityForRegister()
	capability.Backend.BaseURL = server.URL
	if err := capabilities.RegisterPublishedCapability(capability); err != nil {
		t.Fatalf("RegisterPublishedCapability returned %v", err)
	}
	tool, ok := tools.Lookup(capability.Name)
	if !ok {
		t.Fatal("published write capability was not registered")
	}

	runner := capabilities.NewCapabilityWriteRunner(writeExecutorFunc(func(context.Context, string, map[string]any) (map[string]any, error) {
		t.Fatal("published write fell through to fallback executor")
		return nil, nil
	}), []capabilities.Capability{capability}, capabilities.NewHTTPAdapter(http.DefaultClient))
	result, err := runner.Execute(context.Background(), tool.Name, map[string]any{
		"environment": "prod",
		"cluster":     "m1",
		"bucket":      "archive",
		"quota":       100,
	})
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if result["summary"] != "Bucket archive quota set to 100" {
		t.Fatalf("result = %+v, want adapter-normalized response", result)
	}
}

func TestCapabilityWriteRunnerAddsPublishedCapabilityAfterStartup(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "applied"})
	}))
	defer server.Close()

	runner := capabilities.NewCapabilityWriteRunner(writeExecutorFunc(func(context.Context, string, map[string]any) (map[string]any, error) {
		t.Fatal("hot-published write fell through to fallback executor")
		return nil, nil
	}), nil, capabilities.NewHTTPAdapter(server.Client()))
	capability := writeCapabilityForRegister()
	capability.Backend.BaseURL = server.URL
	if err := capabilities.RegisterPublishedCapability(capability); err != nil {
		t.Fatalf("RegisterPublishedCapability returned %v", err)
	}
	if err := runner.AddPublishedCapability(capability); err != nil {
		t.Fatalf("AddPublishedCapability returned %v", err)
	}

	result, err := runner.Execute(context.Background(), capability.Name, map[string]any{
		"environment": "prod",
		"cluster":     "m1",
		"bucket":      "archive",
		"quota":       200,
	})
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if result["summary"] != "Bucket archive quota set to 200" {
		t.Fatalf("result = %+v, want hot-published adapter response", result)
	}
}

func TestCapabilityWriteRunnerDelegatesUnknownToolToNextExecutor(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)

	called := false
	fallback := writeExecutorFunc(func(_ context.Context, name string, input map[string]any) (map[string]any, error) {
		called = true
		return map[string]any{"tool": name, "environment": input["environment"]}, nil
	})
	runner := capabilities.NewCapabilityWriteRunner(fallback, nil, nil)

	result, err := runner.Execute(context.Background(), "topic.retention.set", map[string]any{"environment": "prod", "topic": "payments"})
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if !called || result["tool"] != "topic.retention.set" {
		t.Fatalf("result = %+v, called = %v; want fallback delegation", result, called)
	}
}

func TestCapabilityWriteRunnerRejectsUnknownToolWithoutFallback(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)

	runner := capabilities.NewCapabilityWriteRunner(nil, nil, nil)
	_, err := runner.Execute(context.Background(), "minio.bucket.quota.set", map[string]any{"environment": "prod"})
	if err == nil {
		t.Fatal("Execute accepted unregistered write tool without fallback")
	}
}

func TestCapabilityWriteRunnerVerifyReturnsNilWhenCapabilityHasNoVerifySpec(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)

	writeCapability := writeCapabilityForRegister()
	if err := capabilities.RegisterPublishedCapability(writeCapability); err != nil {
		t.Fatalf("RegisterPublishedCapability returned %v", err)
	}
	runner := capabilities.NewCapabilityWriteRunner(nil, []capabilities.Capability{writeCapability}, nil)
	plan := planRecordForVerify(writeCapability.Name, "operator-1", map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive", "quota": 100})

	result, err := runner.Verify(context.Background(), plan, map[string]any{"environment": "prod"})
	if err != nil {
		t.Fatalf("Verify returned %v", err)
	}
	if result != nil {
		t.Errorf("result = %+v, want nil when capability has no verify spec", result)
	}
}

func TestCapabilityWriteRunnerVerifyCallsReadCapabilityAndReturnsSuccess(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)

	readCalled := false
	readServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readCalled = true
		if r.URL.Path != "/api/kafka/k1/topics/orders/retention" {
			t.Fatalf("read path = %q, want retention read path", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "data": map[string]any{"retention_hours": 72}})
	}))
	defer readServer.Close()

	writeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "applied"})
	}))
	defer writeServer.Close()

	writeCapability := kafkaRetentionWriteCapabilityForVerify()
	writeCapability.Backend.BaseURL = writeServer.URL
	writeCapability.Verify.ReadCapability = "kafka.topic.retention.read"
	writeCapability.Verify.InputMapping = map[string]string{
		"environment": "{environment}",
		"cluster":     "{cluster}",
		"topic":       "{topic}",
	}
	readCapability := kafkaRetentionReadCapability()
	readCapability.Backend.BaseURL = readServer.URL

	if err := capabilities.RegisterPublishedCapability(readCapability); err != nil {
		t.Fatalf("register read capability: %v", err)
	}
	if err := capabilities.RegisterPublishedCapability(writeCapability); err != nil {
		t.Fatalf("register write capability: %v", err)
	}

	readRunner := capabilities.NewCapabilityReadRunner(readRunnerFunc(func(context.Context, tools.Tool, map[string]any) (map[string]any, error) {
		t.Fatal("published read fell through to fallback runner")
		return nil, nil
	}), []capabilities.Capability{readCapability}, capabilities.NewHTTPAdapter(http.DefaultClient))
	runner := capabilities.NewCapabilityWriteRunnerWithVerifier(nil, []capabilities.Capability{writeCapability}, capabilities.NewHTTPAdapter(http.DefaultClient), readRunner)

	plan := planRecordForVerify(writeCapability.Name, "operator-1", map[string]any{
		"environment":      "prod",
		"cluster":          "k1",
		"topic":            "orders",
		"retention_hours":  72,
	})
	result, err := runner.Verify(context.Background(), plan, map[string]any{
		"environment":      "prod",
		"cluster":          "k1",
		"topic":            "orders",
		"retention_hours":  72,
	})
	if err != nil {
		t.Fatalf("Verify returned %v", err)
	}
	if result == nil {
		t.Fatal("result is nil, want populated verification")
	}
	if result.ToolName != "kafka.topic.retention.read" {
		t.Errorf("result.ToolName = %q, want kafka.topic.retention.read", result.ToolName)
	}
	if result.Status != "success" {
		t.Errorf("result.Status = %q, want success", result.Status)
	}
	if !readCalled {
		t.Error("read capability was not invoked")
	}
	if result.Answer == nil {
		t.Error("result.Answer is nil, want populated")
	}
}

func TestCapabilityWriteRunnerVerifyReturnsFailedWhenReadCapabilityNotRegistered(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)

	writeCapability := kafkaRetentionWriteCapabilityForVerify()
	writeCapability.Backend.BaseURL = "https://backend.example.com"
	writeCapability.Verify.ReadCapability = "kafka.topic.retention.read"
	if err := capabilities.RegisterPublishedCapability(writeCapability); err != nil {
		t.Fatalf("register write capability: %v", err)
	}

	runner := capabilities.NewCapabilityWriteRunnerWithVerifier(nil, []capabilities.Capability{writeCapability}, nil, nil)
	plan := planRecordForVerify(writeCapability.Name, "operator-1", map[string]any{
		"environment":      "prod",
		"cluster":          "k1",
		"topic":            "orders",
		"retention_hours":  72,
	})

	result, err := runner.Verify(context.Background(), plan, map[string]any{"environment": "prod", "cluster": "k1", "topic": "orders"})
	if err != nil {
		t.Fatalf("Verify returned %v, want nil error", err)
	}
	if result == nil {
		t.Fatal("result is nil, want populated verification")
	}
	if result.Status != "failed" {
		t.Errorf("result.Status = %q, want failed", result.Status)
	}
}

func planRecordForVerify(toolName, subject string, input map[string]any) store.PlanRecord {
	_, hash, _ := plans.CanonicalInput(input)
	inputJSON, _ := json.Marshal(input)
	return store.PlanRecord{
		ID:            "plan-test",
		ToolName:      toolName,
		InputHash:     hash,
		InputJSON:     inputJSON,
		Status:        store.PlanConfirmed,
		CreatedBy:     subject,
		ConfirmedBy:   subject,
		ExpiresAt:      time.Now().UTC().Add(10 * time.Minute),
	}
}

func kafkaRetentionWriteCapabilityForVerify() capabilities.Capability {
	return capabilities.Capability{
		SchemaVersion: 1,
		Name:          "kafka.topic.retention.set",
		Status:        capabilities.StatusPublished,
		Domain:        "kafka",
		ResourceType:  "topic",
		Operation:     tools.Write,
		Risk:          tools.Medium,
		Backend: capabilities.BackendSpec{
			Adapter:   "http",
			Method:    http.MethodPost,
			Path:      "/api/kafka/{cluster}/topics/{topic}/retention",
			TimeoutMS: 3000,
		},
		InputSchema: map[string]capabilities.InputField{
			"environment":      {Type: "string", Required: true},
			"cluster":          {Type: "string", Required: true},
			"topic":            {Type: "string", Required: true},
			"retention_hours": {Type: "integer", Required: true},
		},
		Output: capabilities.OutputSpec{
			Kind:            "confirmation",
			SummaryTemplate: "Set topic {topic} retention to {retention_hours}h",
		},
		Auth: capabilities.AuthSpec{Roles: []string{"operator", "admin"}, EnvironmentScoped: true},
		Governance: capabilities.GovernanceSpec{
			RequiresActionPlan: true,
			RequiresApproval:   true,
			PrecheckTools:      []string{"kafka.topic.retention.read"},
			Rollback:           capabilities.RollbackSpec{Strategy: "restore previous retention via another confirmed action plan"},
		},
		Verify: &capabilities.VerifySpec{
			ReadCapability: "kafka.topic.retention.read",
			InputMapping: map[string]string{
				"environment": "{environment}",
				"cluster":     "{cluster}",
				"topic":       "{topic}",
			},
			TimeoutMS: 3000,
		},
	}
}

func kafkaRetentionReadCapability() capabilities.Capability {
	return capabilities.Capability{
		SchemaVersion: 1,
		Name:          "kafka.topic.retention.read",
		Status:        capabilities.StatusPublished,
		Domain:        "kafka",
		ResourceType:  "topic",
		Operation:     tools.Read,
		Risk:          tools.Low,
		Backend: capabilities.BackendSpec{
			Adapter:   "http",
			Method:    http.MethodGet,
			Path:      "/api/kafka/{cluster}/topics/{topic}/retention",
			TimeoutMS: 3000,
		},
		InputSchema: map[string]capabilities.InputField{
			"environment": {Type: "string", Required: true},
			"cluster":     {Type: "string", Required: true},
			"topic":       {Type: "string", Required: true},
		},
		Output: capabilities.OutputSpec{
			Kind:            "observation",
			SummaryTemplate: "Topic {topic} retention is {retention_hours}h",
			Fields:          map[string]string{"retention_hours": "$.data.retention_hours"},
		},
		Auth: capabilities.AuthSpec{Roles: []string{"viewer", "operator", "admin"}, EnvironmentScoped: true},
	}
}

func validWriteYAML() string {
	return strings.Replace(strings.Replace(strings.Replace(validReadYAML("published"), "name: minio.bucket.capacity.read", "name: minio.bucket.capacity.set", 1), "method: GET", "method: POST", 1),
		"operation: read\nrisk: low",
		"operation: write\nrisk: medium\ngovernance:\n  requires_action_plan: true\n  requires_approval: true\n  precheck_tools: [minio.bucket.capacity.read]\n  rollback:\n    strategy: restore_previous\n", 1)
}

func writeCapabilityForRegister() capabilities.Capability {
	return capabilities.Capability{
		SchemaVersion: 1,
		Name:          "minio.bucket.capacity.set",
		Status:        capabilities.StatusPublished,
		Domain:        "minio",
		ResourceType:  "bucket",
		Operation:     tools.Write,
		Risk:          tools.Medium,
		Backend: capabilities.BackendSpec{
			Adapter:   "http",
			BaseURL:   "https://backend.example.com",
			Method:    http.MethodPost,
			Path:      "/api/minio/clusters/{cluster}/buckets/{bucket}/quota",
			TimeoutMS: 3000,
		},
		InputSchema: map[string]capabilities.InputField{
			"environment": {Type: "string", Required: true},
			"cluster":     {Type: "string", Required: true},
			"bucket":      {Type: "string", Required: true},
			"quota":       {Type: "integer", Required: true},
		},
		Output: capabilities.OutputSpec{
			Kind:            "mutation",
			SummaryTemplate: "Bucket {bucket} quota set to {quota}",
		},
		Governance: capabilities.GovernanceSpec{
			RequiresActionPlan: true,
			RequiresApproval:   true,
			PrecheckTools:      []string{"minio.bucket.capacity.read"},
			Rollback:           capabilities.RollbackSpec{Strategy: "restore_previous"},
		},
		Auth: capabilities.AuthSpec{
			Roles:             []string{"operator", "admin"},
			EnvironmentScoped: true,
		},
	}
}
