package main

import (
	"os"
	"path/filepath"
	"testing"
)

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
