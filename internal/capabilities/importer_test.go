package capabilities_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestImportOpenAPICandidatesClassifiesWithoutManagedDrafts(t *testing.T) {
	t.Parallel()
	ensureImporterTestDomains(t)
	body := []byte(`openapi: 3.0.0
paths:
  /api/minio/{cluster}/buckets/{bucket}/capacity:
    get:
      operationId: getMinioBucketCapacity
      tags: [minio]
      summary: Read bucket capacity
      parameters:
        - name: cluster
          in: path
          required: true
          schema:
            type: string
        - name: bucket
          in: path
          required: true
          schema:
            type: string
  /api/kafka/{cluster}/topics/{topic}/retention:
    post:
      operationId: setKafkaTopicRetention
      tags: [kafka]
      summary: Set topic retention
      parameters:
        - name: cluster
          in: path
          required: true
          schema:
            type: string
        - name: topic
          in: path
          required: true
          schema:
            type: string
`)

	preview, err := capabilities.ImportOpenAPICandidates(body, nil)
	if err != nil {
		t.Fatalf("ImportOpenAPICandidates returned %v", err)
	}

	if preview.Source.Fingerprint == "" || !strings.HasPrefix(preview.Source.Fingerprint, "sha256:") {
		t.Fatalf("fingerprint = %q, want sha256 prefix", preview.Source.Fingerprint)
	}
	if preview.Stats.Total != 2 || preview.Stats.Recommended != 1 || preview.Stats.NeedsAdjustment != 1 || preview.Stats.Read != 1 || preview.Stats.Write != 1 {
		t.Fatalf("stats = %+v, want one recommended read and one needs-adjustment write", preview.Stats)
	}
	first := preview.Candidates[0]
	if first.ID != "GET /api/minio/{cluster}/buckets/{bucket}/capacity" {
		t.Fatalf("candidate id = %q", first.ID)
	}
	if first.Method != "GET" || first.Path != "/api/minio/{cluster}/buckets/{bucket}/capacity" {
		t.Fatalf("candidate method/path = %s %s", first.Method, first.Path)
	}
	if first.OperationID != "getMinioBucketCapacity" {
		t.Fatalf("operation id = %q", first.OperationID)
	}
	if first.Recommendation != capabilities.RecommendationRecommended {
		t.Fatalf("recommendation = %q, want recommended", first.Recommendation)
	}
	if first.Capability.Name != "minio.bucket.capacity.read.getminiobucketcapacity" || first.Capability.Domain != "minio" || first.Capability.ResourceType != "bucket" || first.Capability.Operation != tools.Read || first.Capability.Risk != tools.Low {
		t.Fatalf("candidate capability = %+v", first.Capability)
	}
	second := preview.Candidates[1]
	// 写能力不再一票否决：标为"需要调整"，用户可在调整面板把 operation 改成
	// read（POST 查询）或补齐治理后接入。
	if second.Recommendation != capabilities.RecommendationNeedsAdjustment || second.Capability.Operation != tools.Write {
		t.Fatalf("second candidate = %+v, want needs-adjustment write", second)
	}
	if len(second.Reasons) == 0 {
		t.Fatalf("second reasons empty, want explanation")
	}
}

func TestImportOpenAPICandidatesMarksDuplicatesAsNeedsAdjustment(t *testing.T) {
	t.Parallel()
	ensureImporterTestDomains(t)
	body := []byte(`openapi: 3.0.0
paths:
  /api/minio/{cluster}/buckets/{bucket}/capacity:
    get:
      operationId: getMinioBucketCapacity
      tags: [minio]
      summary: Read bucket capacity
`)
	existing := []capabilities.ManagedCapability{{
		Capability: capabilities.Capability{Name: "minio.bucket.capacity.read.getminiobucketcapacity"},
		Source:     capabilities.SourcePublished,
	}}

	preview, err := capabilities.ImportOpenAPICandidates(body, existing)
	if err != nil {
		t.Fatalf("ImportOpenAPICandidates returned %v", err)
	}

	if preview.Stats.NeedsAdjustment != 1 || preview.Candidates[0].Recommendation != capabilities.RecommendationNeedsAdjustment {
		t.Fatalf("preview = %+v, want duplicate needs adjustment", preview)
	}
	if !strings.Contains(strings.Join(preview.Candidates[0].Warnings, " "), "已有同名能力") {
		t.Fatalf("warnings = %v, want duplicate warning", preview.Candidates[0].Warnings)
	}
}

func TestApplyCandidateOverrideUsesAdminMetadata(t *testing.T) {
	t.Parallel()
	candidate := capabilities.ImportCandidate{
		Capability: capabilities.Capability{
			SchemaVersion: 1,
			Name:          "unknown.resource.status.read",
			Status:        capabilities.StatusNeedsReview,
			Domain:        "unknown",
			ResourceType:  "resource",
			Operation:     tools.Read,
			Risk:          tools.Low,
			Backend:       capabilities.BackendSpec{Adapter: "http", Method: "GET", Path: "/api/middleware/status", TimeoutMS: 3000},
			InputSchema:   map[string]capabilities.InputField{"cluster": {Type: "string", Required: true}},
			Output:        capabilities.OutputSpec{Kind: "observation", SummaryTemplate: "Read status", Fields: map[string]string{"status": "$.status"}},
			Auth:          capabilities.AuthSpec{Roles: []string{"viewer", "operator", "admin"}},
			AI:            capabilities.AISpec{Description: "Read status"},
		},
	}

	capability := capabilities.ApplyCandidateOverride(candidate, capabilities.ImportCandidateOverride{
		Name:         "minio.cluster.status.read",
		Domain:       "minio",
		ResourceType: "cluster",
		Operation:    tools.Read,
		Risk:         tools.Medium,
	})

	if capability.Name != "minio.cluster.status.read" || capability.Domain != "minio" || capability.ResourceType != "cluster" || capability.Risk != tools.Medium {
		t.Fatalf("capability = %+v, want overridden metadata", capability)
	}
	if capability.Backend.Path != "/api/middleware/status" || capability.Status != capabilities.StatusNeedsReview {
		t.Fatalf("capability backend/status changed unexpectedly: %+v", capability)
	}
}

func TestImportOpenAPIGeneratesReadAndWriteDrafts(t *testing.T) {
	t.Parallel()
	ensureImporterTestDomains(t)
	body := []byte(`openapi: 3.0.0
paths:
  /api/minio/clusters/{cluster}/buckets/{bucket}/capacity:
    get:
      tags: [minio]
      summary: Bucket capacity
      parameters:
        - name: cluster
          in: path
          required: true
          schema: {type: string}
        - name: bucket
          in: path
          required: true
          schema: {type: string}
  /api/kafka/clusters/{cluster}/topics/{topic}/retention:
    post:
      tags: [kafka]
      summary: Update retention
      parameters:
        - name: cluster
          in: path
          required: true
          schema: {type: string}
        - name: topic
          in: path
          required: true
          schema: {type: string}
`)

	drafts, err := capabilities.ImportOpenAPI(body)
	if err != nil {
		t.Fatalf("ImportOpenAPI returned %v", err)
	}
	if len(drafts) != 2 {
		t.Fatalf("draft count = %d, want 2", len(drafts))
	}
	if drafts[0].Name != "kafka.topic.retention.update" || drafts[0].Status != capabilities.StatusNeedsReview || drafts[0].Operation != tools.Write || drafts[0].Domain != "kafka" || drafts[0].ResourceType != "topic" || drafts[0].Governance.RequiresActionPlan {
		t.Fatalf("first draft = %+v, want lexically first kafka topic write draft needing review without auto-governance", drafts[0])
	}
	if drafts[1].Name != "minio.bucket.capacity.read" || drafts[1].Status != capabilities.StatusNeedsReview || drafts[1].Operation != tools.Read || drafts[1].Domain != "minio" || drafts[1].ResourceType != "bucket" {
		t.Fatalf("second draft = %+v, want minio bucket read draft", drafts[1])
	}
}

func TestImportOpenAPIUniquifiesGenericEndpointNamesForWriteDrafts(t *testing.T) {
	t.Parallel()
	body := []byte(`openapi: 3.0.0
paths:
  /api/first:
    post:
      summary: Run request
  /api/second:
    post:
      summary: Run request
`)

	drafts, err := capabilities.ImportOpenAPI(body)
	if err != nil {
		t.Fatalf("ImportOpenAPI returned %v", err)
	}
	if len(drafts) != 2 || drafts[0].Name == drafts[1].Name {
		t.Fatalf("draft names = %q and %q, want two distinct names", drafts[0].Name, drafts[1].Name)
	}
	if err := capabilities.WriteDrafts(t.TempDir(), drafts); err != nil {
		t.Fatalf("WriteDrafts returned %v for imported drafts", err)
	}
}

func TestImportOpenAPIUsesStableOperationIDInName(t *testing.T) {
	t.Parallel()
	ensureImporterTestDomains(t)
	body := []byte(`openapi: 3.0.0
paths:
  /api/topics/{topic}:
    post:
      operationId: Rebuild-Topic.Index
      tags: [kafka]
      summary: Rebuild topic index
`)

	first, err := capabilities.ImportOpenAPI(body)
	if err != nil {
		t.Fatalf("ImportOpenAPI returned %v", err)
	}
	second, err := capabilities.ImportOpenAPI(body)
	if err != nil {
		t.Fatalf("second ImportOpenAPI returned %v", err)
	}
	if len(first) != 1 || len(second) != 1 || first[0].Name != second[0].Name {
		t.Fatalf("operationId name is not stable: %q and %q", first[0].Name, second[0].Name)
	}
	if first[0].Name != "kafka.topic.resource.action.rebuild_topic_index" {
		t.Fatalf("name = %q, want normalized operationId suffix", first[0].Name)
	}
}

func TestWriteDraftsRejectsDuplicateNamesBeforeWriting(t *testing.T) {
	t.Parallel()
	outputDir := t.TempDir()
	drafts := []capabilities.Capability{
		{Name: "duplicate"},
		{Name: "duplicate"},
	}

	err := capabilities.WriteDrafts(filepath.Join(outputDir, "drafts"), drafts)
	if err == nil {
		t.Fatal("WriteDrafts returned nil for duplicate draft names")
	}
	if _, err := os.Stat(filepath.Join(outputDir, "drafts", "duplicate.yaml")); !os.IsNotExist(err) {
		t.Fatalf("duplicate draft file exists after rejected write, stat error = %v", err)
	}
}

func TestImportOpenAPIMergesPathAndOperationParameters(t *testing.T) {
	t.Parallel()
	body := []byte(`openapi: 3.0.0
paths:
  /api/kafka/clusters/{cluster}/topics/{topic}/retention:
    parameters:
      - name: cluster
        in: path
        required: true
        schema: {type: integer}
    post:
      tags: [kafka]
      summary: Update retention
      parameters:
        - name: cluster
          in: path
          required: true
          schema: {type: string}
        - name: topic
          in: path
          required: true
          schema: {type: string}
`)

	drafts, err := capabilities.ImportOpenAPI(body)
	if err != nil {
		t.Fatalf("ImportOpenAPI returned %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("draft count = %d, want 1", len(drafts))
	}
	for _, name := range []string{"cluster", "topic"} {
		field, ok := drafts[0].InputSchema[name]
		if !ok || field.Type != "string" || !field.Required {
			t.Fatalf("%s input = %+v, want required string", name, field)
		}
	}
}

func TestImportOpenAPIPreservesPathParameterWhenLocationDiffers(t *testing.T) {
	t.Parallel()
	body := []byte(`openapi: 3.0.0
paths:
  /api/kafka/clusters/{cluster}/topics:
    parameters:
      - name: cluster
        in: path
        required: true
        schema: {type: string}
    get:
      tags: [kafka]
      summary: List topics
      parameters:
        - name: cluster
          in: query
          required: false
          schema: {type: integer}
        - name: cluster
          in: header
          required: false
          schema: {type: boolean}
`)

	drafts, err := capabilities.ImportOpenAPI(body)
	if err != nil {
		t.Fatalf("ImportOpenAPI returned %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("draft count = %d, want 1", len(drafts))
	}
	cluster, ok := drafts[0].InputSchema["cluster"]
	if !ok || cluster.Type != "string" || !cluster.Required {
		t.Fatalf("cluster input = %+v, want required string path input", cluster)
	}
}

func TestImportOpenAPIImportsQueryParamsButSkipsHeaders(t *testing.T) {
	t.Parallel()
	body := []byte(`openapi: 3.0.0
paths:
  /api/kafka/clusters/{cluster}/topics:
    get:
      tags: [kafka]
      summary: List topics
      parameters:
        - name: cluster
          in: path
          required: true
          schema: {type: string}
        - name: limit
          in: query
          required: false
          schema: {type: integer}
        - name: X-Request-ID
          in: header
          required: true
          schema: {type: string}
`)

	drafts, err := capabilities.ImportOpenAPI(body)
	if err != nil {
		t.Fatalf("ImportOpenAPI returned %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("draft count = %d, want 1", len(drafts))
	}
	// Query parameter "limit" should be imported with In="query".
	limit, ok := drafts[0].InputSchema["limit"]
	if !ok {
		t.Fatal("input schema missing query parameter limit")
	}
	if limit.Type != "integer" || limit.In != "query" {
		t.Fatalf("limit input = %+v, want type=integer in=query", limit)
	}
	// Header parameter should not be imported.
	if _, ok := drafts[0].InputSchema["X-Request-ID"]; ok {
		t.Errorf("input schema includes header parameter X-Request-ID")
	}
}

func TestImportOpenAPIDoesNotImportQueryParamsForWriteOperations(t *testing.T) {
	t.Parallel()
	body := []byte(`openapi: 3.0.0
paths:
  /api/kafka/clusters/{cluster}/topics:
    post:
      tags: [kafka]
      summary: Create topic
      parameters:
        - name: cluster
          in: path
          required: true
          schema: {type: string}
        - name: replication
          in: query
          required: false
          schema: {type: integer}
`)

	drafts, err := capabilities.ImportOpenAPI(body)
	if err != nil {
		t.Fatalf("ImportOpenAPI returned %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("draft count = %d, want 1", len(drafts))
	}
	// POST 写能力也保留 query 参数（in:query），适配器拼到 URL 上而不进 body。
	replication, ok := drafts[0].InputSchema["replication"]
	if !ok || replication.In != "query" {
		t.Fatalf("query parameter replication = %+v, want imported with in:query", replication)
	}
}

func TestWriteDraftsRejectsPathTraversalBeforeWriting(t *testing.T) {
	t.Parallel()
	outputDir := t.TempDir()
	err := capabilities.WriteDrafts(filepath.Join(outputDir, "drafts"), []capabilities.Capability{{Name: "../outside"}})
	if err == nil {
		t.Fatal("WriteDrafts returned nil for a path-traversal draft name")
	}
	if _, statErr := os.Stat(filepath.Join(outputDir, "outside.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("path-traversal draft file exists outside output directory, stat error = %v", statErr)
	}
}

func TestImportOpenAPIIgnoresPathMetadataAndUnsupportedMethods(t *testing.T) {
	t.Parallel()
	ensureImporterTestDomains(t)
	body := []byte(`openapi: 3.0.0
paths:
  /api/kafka/topics/{topic}/retention:
    summary: Topic retention operations
    description: Path-level description
    parameters:
      - name: trace_id
        in: query
        required: false
        schema: {type: integer}
    options:
      summary: Unsupported operation
    trace:
      summary: Unsupported operation
    head:
      tags: [kafka]
      summary: Topic retention headers
    get:
      tags: [kafka]
      summary: Topic retention status
    delete:
      tags: [kafka]
      summary: Delete topic retention
`)

	drafts, err := capabilities.ImportOpenAPI(body)
	if err != nil {
		t.Fatalf("ImportOpenAPI returned %v", err)
	}
	if len(drafts) != 2 {
		t.Fatalf("draft count = %d, want 2 without HEAD draft", len(drafts))
	}
	byMethod := make(map[string]capabilities.Capability, len(drafts))
	for _, draft := range drafts {
		byMethod[draft.Backend.Method] = draft
	}
	if draft := byMethod["GET"]; draft.Name != "kafka.topic.status.read" {
		t.Fatalf("GET draft = %+v, want read draft", draft)
	}
	if draft := byMethod["DELETE"]; draft.Name != "kafka.topic.retention.delete" {
		t.Fatalf("DELETE draft = %+v, want delete draft", draft)
	}
}

func TestImportOpenAPIAssignsActionNameToNonUpdatePOST(t *testing.T) {
	t.Parallel()
	ensureImporterTestDomains(t)
	body := []byte(`openapi: 3.0.0
paths:
  /api/kafka/topics/{topic}/rebuild:
    post:
      tags: [kafka]
      summary: Rebuild topic index
      parameters:
        - name: verbose
          in: query
          required: false
          schema: {type: integer}
        - name: topic
          in: path
          required: true
          schema: {type: string}
`)

	drafts, err := capabilities.ImportOpenAPI(body)
	if err != nil {
		t.Fatalf("ImportOpenAPI returned %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("draft count = %d, want 1", len(drafts))
	}
	draft := drafts[0]
	if draft.Name != "kafka.topic.resource.action" {
		t.Fatalf("name = %q, want action-style POST name", draft.Name)
	}
	// POST 也保留 query 参数（in:query）；只有路径变量进 body/URL 模板。
	verbose, ok := draft.InputSchema["verbose"]
	if !ok || verbose.In != "query" || verbose.Required {
		t.Fatalf("verbose input = %+v, want optional in:query", verbose)
	}
	topic, ok := draft.InputSchema["topic"]
	if !ok || topic.Type != "string" || !topic.Required {
		t.Fatalf("topic input = %+v, want required string", topic)
	}
}

func TestImportOpenAPIRequestBodyForWriteOperation(t *testing.T) {
	// 写操作（POST）参数放在 requestBody 的 JSON schema 里，导入器此前忽略
	// requestBody 导致该能力缺失 body 参数（调用时"参数不够"）。此用例验证
	// body 字段被并入 input_schema，且 required 正确。
	body := []byte(`openapi: 3.0.0
paths:
  /api/kafka/clusters/{cluster}/topics/{topic}/retention:
    post:
      tags: [kafka]
      summary: Set topic retention
      parameters:
        - name: cluster
          in: path
          required: true
          schema: {type: string}
        - name: topic
          in: path
          required: true
          schema: {type: string}
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [retention_hours]
              properties:
                retention_hours:
                  type: integer
                note:
                  type: string
`)

	drafts, err := capabilities.ImportOpenAPI(body)
	if err != nil {
		t.Fatalf("ImportOpenAPI returned %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("draft count = %d, want 1", len(drafts))
	}
	// path 参数仍保留
	for _, name := range []string{"cluster", "topic"} {
		field, ok := drafts[0].InputSchema[name]
		if !ok || field.Type != "string" || !field.Required {
			t.Fatalf("%s input = %+v, want required string", name, field)
		}
	}
	// requestBody 字段并入 input_schema
	retention, ok := drafts[0].InputSchema["retention_hours"]
	if !ok || retention.Type != "integer" || !retention.Required {
		t.Fatalf("retention_hours input = %+v, want required integer from requestBody", retention)
	}
	// 非必填 body 字段也应导入为可选
	note, ok := drafts[0].InputSchema["note"]
	if !ok || note.Type != "string" || note.Required {
		t.Fatalf("note input = %+v, want optional string from requestBody", note)
	}
}

func TestImportOpenAPIRequestBodyCarriesDescriptionAndEnum(t *testing.T) {
	body := []byte(`openapi: 3.0.0
paths:
  /api/kafka/topics/{topic}/retention:
    post:
      tags: [kafka]
      summary: Set topic retention
      parameters:
        - name: topic
          in: path
          required: true
          description: 目标 topic 名
          schema: {type: string}
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [mode]
              properties:
                mode:
                  type: string
                  description: 覆盖模式
                  enum: [append, replace]
`)

	drafts, err := capabilities.ImportOpenAPI(body)
	if err != nil {
		t.Fatalf("ImportOpenAPI returned %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("draft count = %d, want 1", len(drafts))
	}
	topic := drafts[0].InputSchema["topic"]
	if topic.Description != "目标 topic 名" {
		t.Fatalf("topic description = %q, want 目标 topic 名", topic.Description)
	}
	mode := drafts[0].InputSchema["mode"]
	if mode.Description != "覆盖模式" {
		t.Fatalf("mode description = %q, want 覆盖模式", mode.Description)
	}
	if len(mode.Enum) != 2 || mode.Enum[0] != "append" || mode.Enum[1] != "replace" {
		t.Fatalf("mode enum = %v, want [append replace]", mode.Enum)
	}
	if !mode.Required {
		t.Fatalf("mode input = %+v, want required", mode)
	}
}

func TestImportOpenAPIRequestBodySkippedForReadOperation(t *testing.T) {
	// 读操作（GET）无 body，即使文档带空 requestBody 也不应把字段加进 input_schema。
	body := []byte(`openapi: 3.0.0
paths:
  /api/minio/buckets/{bucket}/capacity:
    get:
      tags: [minio]
      summary: Bucket capacity
      parameters:
        - name: bucket
          in: path
          required: true
          schema: {type: string}
`)

	drafts, err := capabilities.ImportOpenAPI(body)
	if err != nil {
		t.Fatalf("ImportOpenAPI returned %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("draft count = %d, want 1", len(drafts))
	}
	if field, ok := drafts[0].InputSchema["bucket"]; !ok || !field.Required {
		t.Fatalf("bucket input = %+v, want required string", field)
	}
	if len(drafts[0].InputSchema) != 1 { // bucket，无 body 字段
		t.Fatalf("input_schema keys = %d, want 1 (path var only)", len(drafts[0].InputSchema))
	}
}

// Swagger 2.0 文档（definitions + in:body 参数 + response.schema）应归一化导入，
// 而不是静默产出没有 body 参数、没有输出映射的残废草稿。
func TestImportOpenAPINormalizesSwagger2(t *testing.T) {
	body := []byte(`swagger: "2.0"
paths:
  /api/kafka/topics/{topic}/partitions:
    post:
      tags: [kafka]
      summary: Add partition
      parameters:
        - name: topic
          in: path
          required: true
          type: string
        - name: body
          in: body
          schema:
            type: object
            required: [count]
            properties:
              count:
                type: integer
                description: 目标分区数
      responses:
        200:
          description: ok
          schema:
            $ref: '#/definitions/PartitionResult'
definitions:
  PartitionResult:
    type: object
    properties:
      total:
        type: integer
      message:
        type: string
`)

	drafts, err := capabilities.ImportOpenAPI(body)
	if err != nil {
		t.Fatalf("ImportOpenAPI returned %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("draft count = %d, want 1", len(drafts))
	}
	draft := drafts[0]
	if field, ok := draft.InputSchema["topic"]; !ok || !field.Required {
		t.Fatalf("topic input = %+v, want required path var", field)
	}
	if field, ok := draft.InputSchema["count"]; !ok || field.Type != "integer" {
		t.Fatalf("count input = %+v, want integer body field", field)
	}
	if len(draft.Output.Fields) == 0 {
		t.Fatalf("output fields empty, want inferred from definitions via response schema")
	}
	for _, want := range []string{"total", "message"} {
		if _, ok := draft.Output.Fields[want]; !ok {
			t.Fatalf("output fields = %v, want %q mapped", draft.Output.Fields, want)
		}
	}
}

// 响应信封（{code,message,data:{...}}）导入时自动下钻：输出映射落在业务字段
// 上而不是 code/message 上。
func TestImportOpenAPIUnwrapsResponseEnvelope(t *testing.T) {
	body := []byte(`openapi: 3.0.0
paths:
  /api/minio/buckets/{bucket}/capacity:
    get:
      tags: [minio]
      summary: Bucket capacity
      parameters:
        - name: bucket
          in: path
          required: true
          schema: {type: string}
      responses:
        200:
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties:
                  code: {type: integer}
                  message: {type: string}
                  data:
                    type: object
                    properties:
                      usage_pct: {type: number}
                      bucket_name: {type: string}
`)

	drafts, err := capabilities.ImportOpenAPI(body)
	if err != nil {
		t.Fatalf("ImportOpenAPI returned %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("draft count = %d, want 1", len(drafts))
	}
	fields := drafts[0].Output.Fields
	if _, ok := fields["code"]; ok {
		t.Fatalf("output fields = %v, want envelope code skipped", fields)
	}
	if _, ok := fields["usage_pct"]; !ok {
		t.Fatalf("output fields = %v, want data.usage_pct mapped via envelope unwrap", fields)
	}
	if _, ok := fields["bucket_name"]; !ok {
		t.Fatalf("output fields = %v, want data.bucket_name mapped via envelope unwrap", fields)
	}
}

// $ref 解析失败（外部引用/组件名不匹配）以警告透出到预览候选，而不是静默丢字段。
func TestImportOpenAPIUnresolvedRefBecomesWarning(t *testing.T) {
	body := []byte(`openapi: 3.0.0
paths:
  /api/kafka/topics/{topic}/config:
    get:
      tags: [kafka]
      summary: Topic config
      parameters:
        - name: topic
          in: path
          required: true
          schema: {type: string}
      responses:
        200:
          description: ok
          content:
            application/json:
              schema:
                $ref: 'https://external.example.com/schemas.yaml#/TopicConfig'
`)

	preview, err := capabilities.ImportOpenAPICandidates(body, nil)
	if err != nil {
		t.Fatalf("ImportOpenAPICandidates returned %v", err)
	}
	if len(preview.Candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(preview.Candidates))
	}
	found := false
	for _, warning := range preview.Candidates[0].Warnings {
		if strings.Contains(warning, "$ref") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want unresolved $ref warning", preview.Candidates[0].Warnings)
	}
}

// fakeProbeChat 是 InferOutputFromSample 的最小 ChatCompleter stub。
type fakeProbeChat struct {
	response string
	err      error
}

func (f fakeProbeChat) Complete(_ context.Context, _, _ string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.response, nil
}

func probeDraft() capabilities.Capability {
	return capabilities.Capability{
		SchemaVersion: 1,
		Name:          "kafka.topic.health.read",
		Status:        capabilities.StatusNeedsReview,
		Domain:        "kafka",
		ResourceType:  "topic",
		Operation:     tools.Read,
		Risk:          tools.Low,
		Backend: capabilities.BackendSpec{
			Adapter: "http", Method: "GET",
			Path: "/api/kafka/topics/{topic}/health", TimeoutMS: 3000,
		},
		InputSchema: map[string]capabilities.InputField{
			"topic": {Type: "string", Required: true},
		},
		Output: capabilities.OutputSpec{Kind: "observation", SummaryTemplate: "查询完成"},
		Auth:   capabilities.AuthSpec{Roles: []string{"viewer"}},
	}
}

// LLM 按真实样本推断的映射经 extractPath 校验：取不到值的路径被丢弃，
// 全部无效时返回错误（调用方回退规则），不给幻觉留活路。
func TestInferOutputFromSampleValidatesAgainstSample(t *testing.T) {
	sample := []byte(`{"code":0,"message":"ok","data":{"status":"healthy","topic_name":"orders","lag":5}}`)
	chat := fakeProbeChat{response: "```json\n" + `{
		"summary_template": "topic {topic_name} 状态为 {status}",
		"severity_path": "$.data.status",
		"output_fields": {
			"status": "$.data.status",
			"topic_name": "$.data.topic_name",
			"hallucinated": "$.data.does_not_exist"
		}
	}` + "\n```"}
	draft := probeDraft()

	spec, err := capabilities.InferOutputFromSample(context.Background(), chat, draft, sample)
	if err != nil {
		t.Fatalf("InferOutputFromSample returned %v", err)
	}
	if spec.Fields["status"] != "$.data.status" || spec.Fields["topic_name"] != "$.data.topic_name" {
		t.Fatalf("fields = %v, want validated paths kept", spec.Fields)
	}
	if _, exists := spec.Fields["hallucinated"]; exists {
		t.Fatalf("fields = %v, want hallucinated path dropped", spec.Fields)
	}
	if spec.SeverityPath != "$.data.status" {
		t.Fatalf("severity_path = %q", spec.SeverityPath)
	}
	if spec.SummaryTemplate != "topic {topic_name} 状态为 {status}" {
		t.Fatalf("summary_template = %q", spec.SummaryTemplate)
	}
}

// LLM 输出的路径在真实响应上全部无效时返回错误，不让无效映射覆盖草稿。
func TestInferOutputFromSampleRejectsAllInvalidPaths(t *testing.T) {
	sample := []byte(`{"code":0,"data":{"status":"healthy"}}`)
	chat := fakeProbeChat{response: `{"output_fields":{"bad":"$.nowhere.to_be_found"}}`}
	if _, err := capabilities.InferOutputFromSample(context.Background(), chat, probeDraft(), sample); err == nil {
		t.Fatal("want error when all LLM paths invalid on sample")
	}
}

// ProbeAndInfer 集成：真实调用 mock 后端 + LLM 推断，映射来源标记为 llm_sample。
func TestManagerProbeAndInferUsesLLMOnSample(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0, "message": "ok",
			"data": map[string]any{"status": "healthy", "topic_name": "orders"},
		})
	}))
	defer server.Close()

	chat := fakeProbeChat{response: `{
		"summary_template": "topic {topic_name} 状态为 {status}",
		"severity_path": "$.data.status",
		"output_fields": {"status": "$.data.status", "topic_name": "$.data.topic_name"}
	}`}
	manager := NewManagerForTest(t).WithChat(chat)
	draft := probeDraft()
	draft.Backend.BaseURL = server.URL

	result, err := manager.ProbeAndInfer(context.Background(), draft, map[string]any{"topic": "orders"})
	if err != nil {
		t.Fatalf("ProbeAndInfer returned %v", err)
	}
	if result.Probe == nil {
		t.Fatal("probe result missing")
	}
	if result.InferredBy != "llm_sample" {
		t.Fatalf("inferred_by = %q, want llm_sample", result.InferredBy)
	}
	if result.Inferred.Fields["status"] != "$.data.status" {
		t.Fatalf("inferred fields = %v", result.Inferred.Fields)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", result.Warnings)
	}
}

// 无 LLM 时 ProbeAndInfer 回退规则推断（来源 rules），试调本身不受影响。
func TestManagerProbeAndInferFallsBackToRules(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0, "data": map[string]any{"status": "healthy"},
		})
	}))
	defer server.Close()

	manager := NewManagerForTest(t)
	draft := probeDraft()
	draft.Backend.BaseURL = server.URL

	result, err := manager.ProbeAndInfer(context.Background(), draft, map[string]any{"topic": "orders"})
	if err != nil {
		t.Fatalf("ProbeAndInfer returned %v", err)
	}
	if result.InferredBy != "rules" {
		t.Fatalf("inferred_by = %q, want rules", result.InferredBy)
	}
	if result.Probe == nil || result.Probe.Data["data.status"] != "healthy" {
		t.Fatalf("probe = %+v, want smart-extracted data", result.Probe)
	}
}

// 后端不可达时 ProbeAndInfer 不报错，而是把失败原因放进 warnings——
// 让"连不通"在导入时可见。
func TestManagerProbeAndInferReportsBackendFailure(t *testing.T) {
	manager := NewManagerForTest(t)
	draft := probeDraft()
	draft.Backend.BaseURL = "http://127.0.0.1:1"

	result, err := manager.ProbeAndInfer(context.Background(), draft, map[string]any{"topic": "orders"})
	if err != nil {
		t.Fatalf("ProbeAndInfer returned %v", err)
	}
	if result.Probe != nil {
		t.Fatalf("probe = %+v, want nil on backend failure", result.Probe)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("warnings empty, want backend failure reason")
	}
}

// NewManagerForTest 用临时目录构造测试 Manager。
func NewManagerForTest(t *testing.T) *capabilities.Manager {
	t.Helper()
	return capabilities.NewManager(t.TempDir(), nil)
}

// TestImportOpenAPIToleratesBoolInStringListFields 验证 JSON 规格里
// `required: true` / `enum: true`（布尔写进期望字符串数组的位置）不再导致
// "yaml: unmarshal errors: cannot unmarshal !!bool ... into []string"。
func TestImportOpenAPIToleratesBoolInStringListFields(t *testing.T) {
	t.Parallel()
	body := []byte(`{
  "openapi": "3.0.0",
  "info": {"title": "Tolerant API", "version": "1.0"},
  "paths": {
    "/things": {
      "get": {
        "operationId": "listThings",
        "summary": "list things",
        "tags": true,
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "required": true,
                  "properties": {
                    "id": {"type": "integer", "enum": true}
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}`)
	preview, err := capabilities.ImportOpenAPICandidates(body, nil)
	if err != nil {
		t.Fatalf("ImportOpenAPICandidates with bool-in-[]string fields returned %v", err)
	}
	if len(preview.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(preview.Candidates))
	}
}
