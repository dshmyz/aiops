package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// TestMain 注册 importer 用例依赖的测试域。域清单派生自工具注册表，
// 不再硬编码，因此从 OpenAPI 推断 minio 域前需先注册该域。
func TestMain(m *testing.M) {
	if err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
		Tool: tools.Tool{Name: "minio.importer.test.read", Operation: tools.Read, Risk: tools.Low, Domain: "minio", ResourceType: "bucket"},
		InputSchema: map[string]tools.DynamicInputField{
			"environment": {Type: "string", Required: true},
		},
	}}); err != nil {
		panic("register importer test domain: " + err.Error())
	}
	code := m.Run()
	tools.ResetDynamicToolsForTest()
	os.Exit(code)
}

func TestRunImportsOpenAPIFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	input := filepath.Join(dir, "openapi.yaml")
	output := filepath.Join(dir, "capabilities")
	body := []byte(`openapi: 3.0.0
paths:
  /api/minio/clusters/{cluster}/buckets/{bucket}/capacity:
    get:
      tags: [minio]
      summary: Bucket capacity
      parameters:
        - name: cluster
          required: true
          schema: {type: string}
        - name: bucket
          required: true
          schema: {type: string}
`)
	if err := os.WriteFile(input, body, 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	if err := run([]string{"capability-importer", "import", "openapi", input, output}); err != nil {
		t.Fatalf("run returned %v", err)
	}
	if _, err := os.Stat(filepath.Join(output, "minio.bucket.capacity.read.yaml")); err != nil {
		t.Fatalf("draft was not written: %v", err)
	}
}
