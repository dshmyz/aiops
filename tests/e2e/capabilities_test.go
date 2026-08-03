package e2e_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestPublishedCapabilityExecutesThroughReadOnlyPath(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/minio/clusters/m1/buckets/archive/capacity" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":{"usage_pct":86}}`))
	}))
	defer backend.Close()

	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)

	root := t.TempDir()
	body := strings.ReplaceAll(validPublishedCapabilityYAML(), "BASE_URL", backend.URL)
	if err := os.MkdirAll(filepath.Join(root, "published"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "published", "minio.bucket.capacity.read.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write capability: %v", err)
	}

	loaded, err := capabilities.RegisterPublished(root)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	repository := store.NewMemoryActionPlanStore()
	readRunner := capabilities.NewCapabilityReadRunner(staticRunner{}, loaded, capabilities.NewHTTPAdapter(http.DefaultClient))
	readService := execution.NewReadOnlyService(readRunner, audit.NewService(repository))
	user := identity.CurrentUser{Subject: "viewer-1", Roles: []string{"viewer"}, AllowedEnvironments: []string{"prod"}, RequestID: "capability-e2e"}

	result, err := readService.ExecuteRead(context.Background(), user, "minio.bucket.capacity.read", map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive"})
	if err != nil {
		t.Fatalf("ExecuteRead returned %v", err)
	}
	if result["kind"] != "observation" || result["data"].(map[string]any)["usage_pct"] != float64(86) {
		t.Fatalf("result = %+v, want normalized observation", result)
	}
	events := repository.AuditEvents()
	if len(events) != 1 || events[0].Action != "readonly_tool_executed" || events[0].Decision != "permitted" {
		t.Fatalf("events = %+v, want permitted read audit", events)
	}
}

type staticRunner struct{}

func (staticRunner) Read(context.Context, tools.Tool, map[string]any) (map[string]any, error) {
	return map[string]any{"fallback": true}, nil
}

func validPublishedCapabilityYAML() string {
	return `schema_version: 1
name: minio.bucket.capacity.read
status: published
domain: minio
resource_type: bucket
operation: read
risk: low
backend:
  adapter: http
  method: GET
  base_url: BASE_URL
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
`
}
