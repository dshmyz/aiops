package capabilities_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestLoadPublishedLoadsOnlyPublishedDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "published", "minio.bucket.capacity.read.yaml"), validReadYAML("published"))
	mustWrite(t, filepath.Join(root, "discovered", "minio.bucket.capacity.read.yaml"), validReadYAML("needs_review"))

	loaded, err := capabilities.LoadPublished(root)
	if err != nil {
		t.Fatalf("LoadPublished returned %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "minio.bucket.capacity.read" || loaded[0].Status != capabilities.StatusPublished {
		t.Fatalf("loaded = %+v, want one published capability", loaded)
	}
}

func TestLoadPublishedRejectsDuplicateNames(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "published", "a.yaml"), validReadYAML("published"))
	mustWrite(t, filepath.Join(root, "published", "b.yaml"), validReadYAML("published"))

	_, err := capabilities.LoadPublished(root)
	if err == nil {
		t.Fatal("LoadPublished accepted duplicate capability names")
	}
}

func TestLoadPublishedRejectsStaticToolConflict(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)

	root := t.TempDir()
	conflicting := strings.Replace(validReadYAML("published"), "name: minio.bucket.capacity.read", "name: "+tools.ClusterStatusRead, 1)
	mustWrite(t, filepath.Join(root, "published", "cluster.status.read.yaml"), conflicting)

	if _, err := capabilities.LoadPublished(root); err == nil || !strings.Contains(err.Error(), "conflicts with an existing tool") {
		t.Fatalf("LoadPublished err = %v, want existing tool conflict", err)
	}
}

func TestLoadPublishedRejectsInvalidYAML(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "published", "invalid.yaml"), "name: [")

	_, err := capabilities.LoadPublished(root)
	if err == nil || !strings.Contains(err.Error(), "parse capability") {
		t.Fatalf("err = %v, want parse error", err)
	}
}

func TestLoadPublishedRejectsInvalidCapability(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	invalid := validReadYAML("published")
	invalid = strings.Replace(invalid, "schema_version: 1", "schema_version: 2", 1)
	mustWrite(t, filepath.Join(root, "published", "invalid.yaml"), invalid)

	_, err := capabilities.LoadPublished(root)
	if err == nil || !strings.Contains(err.Error(), "validate capability") {
		t.Fatalf("err = %v, want validation error", err)
	}
}

func TestRegisterPublishedRejectsReadWithMutatingBackendMethod(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	invalid := strings.Replace(validReadYAML("published"), "method: GET", "method: POST", 1)
	mustWrite(t, filepath.Join(root, "published", "invalid.yaml"), invalid)

	_, err := capabilities.RegisterPublished(root)
	if err == nil || !strings.Contains(err.Error(), "validate capability") {
		t.Fatalf("err = %v, want validation error before runtime registration", err)
	}
}

func TestLoadPublishedRejectsInvalidBaseURL(t *testing.T) {
	t.Parallel()

	for _, baseURL := range []string{"", "/relative", "http://", "https://[::1", "ftp://backend.example.com"} {
		root := t.TempDir()
		invalid := strings.Replace(validReadYAML("published"), "  base_url: https://backend.example.com\n", "  base_url: "+baseURL+"\n", 1)
		mustWrite(t, filepath.Join(root, "published", "invalid.yaml"), invalid)

		if _, err := capabilities.LoadPublished(root); err == nil || !strings.Contains(err.Error(), "validate capability") {
			t.Errorf("LoadPublished err = %v for base_url %q, want validation error", err, baseURL)
		}
	}
}

func TestLoadPublishedRejectsNonPublishedStatus(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "published", "review.yaml"), validReadYAML("needs_review"))

	_, err := capabilities.LoadPublished(root)
	if err == nil || !strings.Contains(err.Error(), "has status") {
		t.Fatalf("err = %v, want status error", err)
	}
}

func TestLoadPublishedRejectsMissingPublishedDirectory(t *testing.T) {
	t.Parallel()
	_, err := capabilities.LoadPublished(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "published directory") {
		t.Fatalf("err = %v, want missing directory error", err)
	}
}

func TestLoadPublishedParsesVerifySpec(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	body := strings.Replace(validKafkaWriteYAML("published"), "name: topic.retention.set", "name: kafka.topic.retention.set", 1) + `
verify:
  read_capability: kafka.consumer_group.lag.read
  input_mapping:
    cluster: "{cluster}"
    group: "{topic}"
  timeout_ms: 5000
`
	mustWrite(t, filepath.Join(root, "published", "kafka.topic.retention.set.yaml"), body)

	loaded, err := capabilities.LoadPublished(root)
	if err != nil {
		t.Fatalf("LoadPublished returned %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d capabilities, want 1", len(loaded))
	}
	verify := loaded[0].Verify
	if verify == nil {
		t.Fatal("Verify is nil, want parsed spec")
	}
	if verify.ReadCapability != "kafka.consumer_group.lag.read" {
		t.Errorf("Verify.ReadCapability = %q, want kafka.consumer_group.lag.read", verify.ReadCapability)
	}
	if verify.TimeoutMS != 5000 {
		t.Errorf("Verify.TimeoutMS = %d, want 5000", verify.TimeoutMS)
	}
	if cluster, ok := verify.InputMapping["cluster"]; !ok || cluster != "{cluster}" {
		t.Errorf("InputMapping[cluster] = %q, want \"{cluster}\"", cluster)
	}
	if group, ok := verify.InputMapping["group"]; !ok || group != "{topic}" {
		t.Errorf("InputMapping[group] = %q, want \"{topic}\"", group)
	}
}

func TestLoadPublishedOmitsVerifyWhenAbsent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	body := strings.Replace(validKafkaWriteYAML("published"), "name: topic.retention.set", "name: kafka.topic.retention.set", 1)
	mustWrite(t, filepath.Join(root, "published", "kafka.topic.retention.set.yaml"), body)

	loaded, err := capabilities.LoadPublished(root)
	if err != nil {
		t.Fatalf("LoadPublished returned %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d capabilities, want 1", len(loaded))
	}
	if loaded[0].Verify != nil {
		t.Errorf("Verify = %+v, want nil when not declared", loaded[0].Verify)
	}
}

// TestLoadPublishedParsesNumericBounds covers the production path: bounds are
// declared in YAML, and a mis-tagged field would parse to nil without any
// error — a guardrail that silently isn't there.
func TestLoadPublishedParsesNumericBounds(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	body := strings.Replace(
		strings.Replace(validKafkaWriteYAML("published"), "name: topic.retention.set", "name: kafka.topic.retention.bounded", 1),
		"  retention_hours:\n    type: integer\n    required: true\n",
		"  retention_hours:\n    type: integer\n    required: true\n    min: 1\n    max: 8760\n", 1)
	mustWrite(t, filepath.Join(root, "published", "kafka.topic.retention.bounded.yaml"), body)

	loaded, err := capabilities.LoadPublished(root)
	if err != nil {
		t.Fatalf("LoadPublished returned %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d capabilities, want 1", len(loaded))
	}
	hours := loaded[0].InputSchema["retention_hours"]
	if hours.Min == nil || *hours.Min != 1 {
		t.Errorf("retention_hours.Min = %v, want 1", hours.Min)
	}
	if hours.Max == nil || *hours.Max != 8760 {
		t.Errorf("retention_hours.Max = %v, want 8760", hours.Max)
	}
}

// TestLoadPublishedParsesDryRun 验证写能力 YAML 的 dry_run 段（摘要/命令/风险
// 警告模板）被完整解析。它是数据驱动的 dry-run 预览的数据源：命令与风险警告
// 声明在能力里，Go 侧不再为组件写死专属 handler。
func TestLoadPublishedParsesDryRun(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	body := strings.Replace(validKafkaWriteYAML("published"), "name: topic.retention.set", "name: kafka.topic.retention.dryrun", 1) + `
dry_run:
  summary: 将把 topic {topic} 的保留时间设置为 {retention_hours} 小时。
  command: kafka-configs --entity-name {topic} --add-config retention.hours={retention_hours}
  warnings:
    - 缩短保留时间可能导致超过 {retention_hours} 小时的历史消息被删除，请确认下游消费和审计需求。
`
	mustWrite(t, filepath.Join(root, "published", "kafka.topic.retention.dryrun.yaml"), body)

	loaded, err := capabilities.LoadPublished(root)
	if err != nil {
		t.Fatalf("LoadPublished returned %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d capabilities, want 1", len(loaded))
	}
	dryRun := loaded[0].DryRun
	if dryRun.Summary == "" {
		t.Error("DryRun.Summary is empty, want parsed summary template")
	}
	if dryRun.Command == "" {
		t.Error("DryRun.Command is empty, want parsed command template")
	}
	if len(dryRun.Warnings) != 1 || dryRun.Warnings[0] == "" {
		t.Fatalf("DryRun.Warnings = %v, want one parsed warning template", dryRun.Warnings)
	}
	if !strings.Contains(dryRun.Warnings[0], "{retention_hours}") {
		t.Errorf("DryRun.Warnings[0] = %q, want to keep {retention_hours} placeholder", dryRun.Warnings[0])
	}
}

// TestLoadPublishedOmitsDryRunWhenAbsent 验证未声明 dry_run 的能力 DryRun 为零值。
func TestLoadPublishedOmitsDryRunWhenAbsent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	body := strings.Replace(validKafkaWriteYAML("published"), "name: topic.retention.set", "name: kafka.topic.retention.nodryrun", 1)
	mustWrite(t, filepath.Join(root, "published", "kafka.topic.retention.nodryrun.yaml"), body)

	loaded, err := capabilities.LoadPublished(root)
	if err != nil {
		t.Fatalf("LoadPublished returned %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d capabilities, want 1", len(loaded))
	}
	if loaded[0].DryRun.Command != "" || len(loaded[0].DryRun.Warnings) != 0 {
		t.Errorf("DryRun = %+v, want zero value when not declared", loaded[0].DryRun)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func validReadYAML(status string) string {
	return `schema_version: 1
name: minio.bucket.capacity.read
status: ` + status + `
domain: minio
resource_type: bucket
operation: read
risk: low
backend:
  adapter: http
  base_url: https://backend.example.com
  method: GET
  path: /api/minio/clusters/{cluster}/buckets/{bucket}/capacity
  timeout_ms: 3000
input_schema:
  cluster:
    type: string
    required: true
  bucket:
    type: string
    required: true
output:
  kind: observation
  summary_template: "Bucket {bucket} usage is {usage_pct}%"
  fields:
    usage_pct: $.data.usage_pct
auth:
  roles: [viewer, operator, admin]
`
}

func validKafkaWriteYAML(status string) string {
	return `schema_version: 1
name: topic.retention.set
status: ` + status + `
domain: kafka
resource_type: topic
operation: write
risk: medium
backend:
  adapter: http
  base_url: https://backend.example.com
  method: POST
  path: /api/kafka/clusters/{cluster}/topics/{topic}/retention
  timeout_ms: 3000
input_schema:
  cluster:
    type: string
    required: true
  topic:
    type: string
    required: true
  retention_hours:
    type: integer
    required: true
output:
  kind: confirmation
  summary_template: "Set topic {topic} retention to {retention_hours}h"
auth:
  roles: [operator, admin]
governance:
  requires_action_plan: true
  requires_approval: true
  precheck_tools: [kafka.consumer_group.lag.read]
  rollback:
    strategy: restore previous retention via another confirmed action plan
`
}
