package capabilities_test

import (
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
	if preview.Stats.Total != 2 || preview.Stats.Recommended != 1 || preview.Stats.NotRecommended != 1 || preview.Stats.Read != 1 || preview.Stats.Write != 1 {
		t.Fatalf("stats = %+v, want one recommended read and one not-recommended write", preview.Stats)
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
	if second.Recommendation != capabilities.RecommendationNotRecommended || second.Capability.Operation != tools.Write {
		t.Fatalf("second candidate = %+v, want not recommended write", second)
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
	if _, ok := drafts[0].InputSchema["replication"]; ok {
		t.Fatal("query parameter replication should not be imported for POST (write) operations")
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
	if _, ok := draft.InputSchema["verbose"]; ok {
		t.Fatalf("verbose input present, want not injected (only path params become input): %+v", draft.InputSchema)
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
