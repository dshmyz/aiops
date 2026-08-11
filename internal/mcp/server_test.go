package mcp_test

import (
	"context"
	"os"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/server"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	capmcp "github.com/gracegaoya/ai-operations-copilot/internal/mcp"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestMain(m *testing.M) {
	code := m.Run()
	tools.ResetDynamicToolsForTest()
	os.Exit(code)
}

// fakeReadRunner 是只读执行器的测试替身。
type fakeReadRunner struct {
	result map[string]any
	err    error
}

func (f *fakeReadRunner) Read(_ context.Context, _ tools.Tool, _ map[string]any) (map[string]any, error) {
	return f.result, f.err
}

func newTestClient(t *testing.T, store capabilities.CapabilityStore, runner execution.ReadRunner) (*mcpclient.Client, func()) {
	t.Helper()
	mcpSrv := server.NewMCPServer("test", "0.0.1", server.WithToolCapabilities(false))
	capSrv := capmcp.NewMCPServerFrom(mcpSrv, store, runner, nil)

	// 先注册工具
	if err := capSrv.Init(t.Context()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// 再创建 client（此时工具已注册到 mcpSrv）
	c, err := mcpclient.NewInProcessClient(mcpSrv)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	_, err = c.Initialize(t.Context(), mcpgo.InitializeRequest{})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return c, func() {}
}

func TestMCPServerToolsListEmpty(t *testing.T) {
	c, _ := newTestClient(t, capabilities.NewFileCapabilityStore(t.TempDir()), &fakeReadRunner{})
	result, err := c.ListTools(t.Context(), mcpgo.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(result.Tools) != 0 {
		t.Fatalf("tools = %d, want 0", len(result.Tools))
	}
}

func TestMCPServerToolsListPublished(t *testing.T) {
	root := t.TempDir()
	publishedDir := root + "/published"
	mkdirAll(t, publishedDir)
	writeYAML(t, publishedDir+"/minio.read.yaml", `name: minio.bucket.capacity.read
status: published
domain: minio
resource_type: bucket
operation: read
risk: low
backend:
  adapter: http
  method: GET
  path: /api/minio/{cluster}/buckets/{name}/capacity
  timeout_ms: 3000
  base_url: http://127.0.0.1:19090
input_schema:
  environment:
    type: string
    required: true
    description: 目标环境
  name:
    type: string
    required: true
    description: 桶名称
ai:
  description: 读取 MinIO 桶容量
`)
	fileStore := capabilities.NewFileCapabilityStore(root)
	c, _ := newTestClient(t, fileStore, &fakeReadRunner{})

	result, err := c.ListTools(t.Context(), mcpgo.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(result.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(result.Tools))
	}
	if result.Tools[0].Name != "minio.bucket.capacity.read" {
		t.Fatalf("tool name = %q", result.Tools[0].Name)
	}
	if result.Tools[0].Description != "读取 MinIO 桶容量" {
		t.Fatalf("description = %q", result.Tools[0].Description)
	}
}

func TestMCPServerToolsCallExecutesRead(t *testing.T) {
	root := t.TempDir()
	publishedDir := root + "/published"
	mkdirAll(t, publishedDir)
	writeYAML(t, publishedDir+"/minio.read.yaml", `schema_version: 1
name: minio.bucket.capacity.read
status: published
domain: minio
resource_type: bucket
operation: read
risk: low
backend:
  adapter: http
  method: GET
  path: /api/minio/{cluster}/buckets/{name}/capacity
  timeout_ms: 3000
  base_url: http://127.0.0.1:19090
input_schema:
  environment:
    type: string
    required: true
  cluster:
    type: string
    required: true
  name:
    type: string
    required: true
auth:
  roles:
    - viewer
    - operator
    - admin
  environment_scoped: true
output:
  kind: observation
  summary_template: Bucket {name} usage
ai:
  description: 读取 MinIO 桶容量
`)
	fileStore := capabilities.NewFileCapabilityStore(root)
	fake := &fakeReadRunner{result: map[string]any{"usage_pct": 77, "limit_gb": 100}}
	c, _ := newTestClient(t, fileStore, fake)

	result, err := c.CallTool(t.Context(), mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{
			Name:      "minio.bucket.capacity.read",
			Arguments: map[string]any{"environment": "prod", "cluster": "default", "name": "archive"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
}

func TestMCPServerWriteToolRejected(t *testing.T) {
	root := t.TempDir()
	publishedDir := root + "/published"
	mkdirAll(t, publishedDir)
	writeYAML(t, publishedDir+"/kafka.write.yaml", `schema_version: 1
name: kafka.topic.retention.set
status: published
domain: kafka
resource_type: topic
operation: write
risk: medium
backend:
  adapter: http
  method: POST
  path: /api/kafka/default/topics/{topic}/retention
  timeout_ms: 5000
input_schema:
  environment:
    type: string
    required: true
  topic:
    type: string
    required: true
  retention_hours:
    type: integer
    required: true
ai:
  description: 设置 Kafka topic 保留期
`)
	fileStore := capabilities.NewFileCapabilityStore(root)
	c, _ := newTestClient(t, fileStore, &fakeReadRunner{})

	result, err := c.CallTool(t.Context(), mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{
			Name:      "kafka.topic.retention.set",
			Arguments: map[string]any{"environment": "prod", "topic": "orders", "retention_hours": 72},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("write tool should return error")
	}
}

func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func writeYAML(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
