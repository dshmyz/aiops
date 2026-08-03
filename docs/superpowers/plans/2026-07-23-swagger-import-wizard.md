# Swagger Import Wizard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a mixed-mode Swagger import wizard that previews OpenAPI candidates without saving drafts, lets admins select and adjust candidates, then commits only selected APIs as Capability drafts.

**Architecture:** Keep the existing single Vue app and current compatibility import endpoint. Add stateless backend preview/commit APIs: preview fetches OpenAPI and returns deterministic candidates with a fingerprint; commit re-fetches, validates the fingerprint, applies selections/overrides, and saves selected drafts. Frontend adds a wizard state model and renders the workflow inside `能力接入管理`, while the existing review/test/publish workbench remains the post-commit path.

**Tech Stack:** Go `net/http`, existing `internal/capabilities` manager/importer, Vue 3 `<script setup>`, TypeScript, Vitest, Vue Test Utils, Element Plus.

## Global Constraints

- Previewing a Swagger URL does not create any Capability draft files.
- The first version uses a mixed mode: system recommends candidate APIs automatically, and the admin can override recommendation decisions before saving anything.
- The first version uses the standard preview flow: preview Swagger first, show candidates without saving drafts, commit selected candidates later.
- Keep `POST /v1/capabilities/import/openapi-url` for compatibility.
- Add `POST /v1/capabilities/import/openapi-url/preview`.
- Add `POST /v1/capabilities/import/openapi-url/commit`.
- Only `admin` role can preview or commit, matching the current import endpoint.
- Commit should reject unsupported URL schemes with the current URL validation.
- If the fetched document fingerprint differs from the preview fingerprint, return `Swagger 文档已变化，请重新预览`.
- No database migration is required for the stateless first version.
- Do not add persisted import history.
- Do not add multi-user collaborative import sessions.
- Do not add batch publish.
- Do not publish write capabilities into AI runtime.
- Do not add LLM-based classification.
- Do not add OAuth or secret management UI.
- Do not build full output mapping inside the wizard.
- Do not replace the existing review editor.
- Do not add Vue Router.

---

## File Structure

- Create `internal/capabilities/import_preview.go`
  - Owns preview/commit DTOs, candidate recommendation types, candidate IDs, source fingerprinting, stats, override application, and OpenAPI candidate derivation without draft writes.
- Modify `internal/capabilities/importer.go`
  - Exposes a shared `ImportOpenAPICandidates` path so preview and legacy draft import use the same OpenAPI parsing/inference behavior.
- Modify `internal/capabilities/manage.go`
  - Adds stateless URL preview/commit manager methods and extracts shared OpenAPI URL fetch logic from `ImportOpenAPIFromURL`.
- Modify `internal/capabilities/manage_test.go`
  - Covers no-draft preview, selected-only commit, changed-fingerprint rejection, unsupported URL rejection, and legacy import compatibility.
- Modify `internal/httpapi/router.go`
  - Adds preview/commit methods to `CapabilityManagementService` and routes the two new endpoints.
- Modify `internal/httpapi/router_test.go`
  - Covers admin authorization, payload decoding, response writing, and new service calls.
- Modify `apps/capability-console/src/types.ts`
  - Adds `ImportPreview`, `ImportCandidate`, `ImportCommitSelection`, and related TypeScript interfaces.
- Modify `apps/capability-console/src/api.ts`
  - Adds `previewOpenAPIURL` and `commitOpenAPIURLImport`; keeps `importOpenAPIURL`.
- Create `apps/capability-console/src/importWizard.ts`
  - Pure TypeScript helpers for selections, overrides, filters, commit payloads, and candidate counts.
- Create `apps/capability-console/src/importWizard.test.ts`
  - Unit tests for default recommended selection, filters, overrides, and zero-selection commit state.
- Modify `apps/capability-console/src/App.vue`
  - Replaces the single import strip UI with the four-step wizard inside `能力接入管理`.
  - Keeps existing import batch/review/test/publish/preflight behaviors after commit.
- Modify `apps/capability-console/src/App.test.ts`
  - Adds wizard UI tests and updates existing Swagger import test to use preview + commit.
- Modify `apps/capability-console/src/styles.css`
  - Adds compact stepper, preview stats, candidate rows, selected edit grid, and commit summary styles.
- Modify `examples/README.md`
  - Updates demo instructions to describe previewing before draft creation.

---

### Task 1: OpenAPI Preview Candidate Model

**Files:**
- Create: `internal/capabilities/import_preview.go`
- Modify: `internal/capabilities/importer.go`
- Test: `internal/capabilities/importer_test.go`

**Interfaces:**
- Produces: `type ImportRecommendation string`
- Produces: constants `RecommendationRecommended`, `RecommendationNeedsAdjustment`, `RecommendationNotRecommended`
- Produces: `type ImportPreviewSource struct`
- Produces: `type ImportPreviewStats struct`
- Produces: `type ImportCandidateCapability struct`
- Produces: `type ImportCandidate struct`
- Produces: `type ImportPreview struct`
- Produces: `func ImportOpenAPICandidates(body []byte, existing []ManagedCapability) (ImportPreview, error)`
- Produces: `func OpenAPIFingerprint(body []byte) string`
- Produces: `func CandidateID(method, path string) string`
- Produces: `func ApplyCandidateOverride(candidate ImportCandidate, override ImportCandidateOverride) Capability`
- Consumes: existing `ImportOpenAPI(body []byte) ([]Capability, error)` behavior must remain compatible.

- [ ] **Step 1: Write failing candidate preview tests**

Append these tests to `internal/capabilities/importer_test.go`:

```go
func TestImportOpenAPICandidatesClassifiesWithoutManagedDrafts(t *testing.T) {
	t.Parallel()
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

	preview, err := ImportOpenAPICandidates(body, nil)
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
	if first.Recommendation != RecommendationRecommended {
		t.Fatalf("recommendation = %q, want recommended", first.Recommendation)
	}
	if first.Capability.Name != "minio.bucket.capacity.read.getminiobucketcapacity" || first.Capability.Domain != "minio" || first.Capability.ResourceType != "bucket" || first.Capability.Operation != tools.Read || first.Capability.Risk != tools.Low {
		t.Fatalf("candidate capability = %+v", first.Capability)
	}
	second := preview.Candidates[1]
	if second.Recommendation != RecommendationNotRecommended || second.Capability.Operation != tools.Write {
		t.Fatalf("second candidate = %+v, want not recommended write", second)
	}
	if len(second.Reasons) == 0 {
		t.Fatalf("second reasons empty, want explanation")
	}
}

func TestImportOpenAPICandidatesMarksDuplicatesAsNeedsAdjustment(t *testing.T) {
	t.Parallel()
	body := []byte(`openapi: 3.0.0
paths:
  /api/minio/{cluster}/buckets/{bucket}/capacity:
    get:
      operationId: getMinioBucketCapacity
      tags: [minio]
      summary: Read bucket capacity
`)
	existing := []ManagedCapability{{
		Capability: Capability{Name: "minio.bucket.capacity.read.getminiobucketcapacity"},
		Source:     SourcePublished,
	}}

	preview, err := ImportOpenAPICandidates(body, existing)
	if err != nil {
		t.Fatalf("ImportOpenAPICandidates returned %v", err)
	}

	if preview.Stats.NeedsAdjustment != 1 || preview.Candidates[0].Recommendation != RecommendationNeedsAdjustment {
		t.Fatalf("preview = %+v, want duplicate needs adjustment", preview)
	}
	if !strings.Contains(strings.Join(preview.Candidates[0].Warnings, " "), "已有同名能力") {
		t.Fatalf("warnings = %v, want duplicate warning", preview.Candidates[0].Warnings)
	}
}

func TestApplyCandidateOverrideUsesAdminMetadata(t *testing.T) {
	t.Parallel()
	candidate := ImportCandidate{
		Capability: Capability{
			SchemaVersion: 1,
			Name:          "unknown.resource.status.read",
			Status:        StatusNeedsReview,
			Domain:        "unknown",
			ResourceType:  "resource",
			Operation:     tools.Read,
			Risk:          tools.Low,
			Backend:       BackendSpec{Adapter: "http", Method: "GET", Path: "/api/middleware/status", TimeoutMS: 3000},
			InputSchema:   map[string]InputField{"environment": {Type: "string", Required: true}},
			Output:        OutputSpec{Kind: "observation", SummaryTemplate: "Read status", Fields: map[string]string{"status": "$.status"}},
			Auth:          AuthSpec{Roles: []string{"viewer", "operator", "admin"}, EnvironmentScoped: true},
			AI:            AISpec{Description: "Read status"},
		},
	}

	capability := ApplyCandidateOverride(candidate, ImportCandidateOverride{
		Name:         "minio.cluster.status.read",
		Domain:       "minio",
		ResourceType: "cluster",
		Operation:    tools.Read,
		Risk:         tools.Medium,
	})

	if capability.Name != "minio.cluster.status.read" || capability.Domain != "minio" || capability.ResourceType != "cluster" || capability.Risk != tools.Medium {
		t.Fatalf("capability = %+v, want overridden metadata", capability)
	}
	if capability.Backend.Path != "/api/middleware/status" || capability.Status != StatusNeedsReview {
		t.Fatalf("capability backend/status changed unexpectedly: %+v", capability)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test -count=1 ./internal/capabilities -run 'TestImportOpenAPICandidates|TestApplyCandidateOverride'
```

Expected: FAIL with undefined `ImportOpenAPICandidates`, `RecommendationRecommended`, `RecommendationNeedsAdjustment`, `RecommendationNotRecommended`, and `ApplyCandidateOverride`.

- [ ] **Step 3: Add preview candidate types**

Create `internal/capabilities/import_preview.go`:

```go
package capabilities

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

type ImportRecommendation string

const (
	RecommendationRecommended    ImportRecommendation = "recommended"
	RecommendationNeedsAdjustment ImportRecommendation = "needs_adjustment"
	RecommendationNotRecommended ImportRecommendation = "not_recommended"
)

type ImportPreviewSource struct {
	OpenAPIURL     string `json:"openapi_url,omitempty"`
	BackendBaseURL string `json:"backend_base_url,omitempty"`
	Fingerprint    string `json:"fingerprint"`
}

type ImportPreviewStats struct {
	Total           int `json:"total"`
	Recommended     int `json:"recommended"`
	NeedsAdjustment int `json:"needs_adjustment"`
	NotRecommended  int `json:"not_recommended"`
	Read            int `json:"read"`
	Write           int `json:"write"`
}

type ImportCandidateCapability struct {
	Name         string          `json:"name"`
	Domain       string          `json:"domain"`
	ResourceType string          `json:"resource_type"`
	Operation    tools.Operation `json:"operation"`
	Risk         tools.RiskLevel  `json:"risk"`
}

type ImportCandidate struct {
	ID             string                    `json:"id"`
	Method         string                    `json:"method"`
	Path           string                    `json:"path"`
	OperationID    string                    `json:"operation_id,omitempty"`
	Capability     Capability                `json:"capability"`
	Summary         ImportCandidateCapability `json:"summary"`
	Recommendation ImportRecommendation      `json:"recommendation"`
	Reasons        []string                  `json:"reasons"`
	Warnings       []string                  `json:"warnings"`
}

type ImportPreview struct {
	Source     ImportPreviewSource `json:"source"`
	Stats      ImportPreviewStats  `json:"stats"`
	Candidates []ImportCandidate   `json:"candidates"`
}

type ImportCandidateOverride struct {
	Name         string          `json:"name,omitempty"`
	Domain       string          `json:"domain,omitempty"`
	ResourceType string          `json:"resource_type,omitempty"`
	Operation    tools.Operation `json:"operation,omitempty"`
	Risk         tools.RiskLevel  `json:"risk,omitempty"`
}

func OpenAPIFingerprint(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func CandidateID(method, path string) string {
	return strings.ToUpper(strings.TrimSpace(method)) + " " + strings.TrimSpace(path)
}

func ImportOpenAPICandidates(body []byte, existing []ManagedCapability) (ImportPreview, error) {
	operations, err := parseOpenAPIOperations(body)
	if err != nil {
		return ImportPreview{}, err
	}
	existingNames := make(map[string]struct{}, len(existing))
	for _, item := range existing {
		existingNames[item.Name] = struct{}{}
	}
	preview := ImportPreview{
		Source: ImportPreviewSource{Fingerprint: OpenAPIFingerprint(body)},
	}
	for _, operation := range operations {
		candidate := ImportCandidate{
			ID:          CandidateID(operation.Method, operation.Path),
			Method:      operation.Method,
			Path:        operation.Path,
			OperationID: operation.Operation.OperationID,
			Capability:  operation.Capability,
			Summary: ImportCandidateCapability{
				Name:         operation.Capability.Name,
				Domain:       operation.Capability.Domain,
				ResourceType: operation.Capability.ResourceType,
				Operation:    operation.Capability.Operation,
				Risk:         operation.Capability.Risk,
			},
		}
		candidate.Recommendation, candidate.Reasons, candidate.Warnings = recommendCandidate(operation.Capability, existingNames)
		preview.Candidates = append(preview.Candidates, candidate)
	}
	preview.Stats = importPreviewStats(preview.Candidates)
	return preview, nil
}

func ApplyCandidateOverride(candidate ImportCandidate, override ImportCandidateOverride) Capability {
	capability := candidate.Capability
	if strings.TrimSpace(override.Name) != "" {
		capability.Name = strings.TrimSpace(override.Name)
	}
	if strings.TrimSpace(override.Domain) != "" {
		capability.Domain = strings.TrimSpace(override.Domain)
	}
	if strings.TrimSpace(override.ResourceType) != "" {
		capability.ResourceType = strings.TrimSpace(override.ResourceType)
	}
	if override.Operation != "" {
		capability.Operation = override.Operation
	}
	if override.Risk != "" {
		capability.Risk = override.Risk
	}
	capability.Status = StatusNeedsReview
	return capability
}

func recommendCandidate(capability Capability, existingNames map[string]struct{}) (ImportRecommendation, []string, []string) {
	reasons := []string{}
	warnings := []string{}
	if _, exists := existingNames[capability.Name]; exists {
		return RecommendationNeedsAdjustment, []string{"已有同名能力，需要确认命名"}, []string{"已有同名能力"}
	}
	if capability.Operation != tools.Read || capability.Backend.Method != http.MethodGet {
		return RecommendationNotRecommended, []string{"第一版暂不接入写入能力"}, nil
	}
	if capability.Domain == "unknown" || capability.ResourceType == "resource" {
		warnings = append(warnings, "领域或资源类型需要确认")
		return RecommendationNeedsAdjustment, []string{"需要调整识别结果"}, warnings
	}
	if capability.Output.SummaryTemplate == "" && len(capability.Output.Fields) == 0 {
		warnings = append(warnings, "缺少输出映射")
		return RecommendationNeedsAdjustment, []string{"需要补充输出映射"}, warnings
	}
	reasons = append(reasons, "GET read operation", "known middleware domain")
	return RecommendationRecommended, reasons, warnings
}

func importPreviewStats(candidates []ImportCandidate) ImportPreviewStats {
	stats := ImportPreviewStats{Total: len(candidates)}
	for _, candidate := range candidates {
		switch candidate.Recommendation {
		case RecommendationRecommended:
			stats.Recommended++
		case RecommendationNeedsAdjustment:
			stats.NeedsAdjustment++
		case RecommendationNotRecommended:
			stats.NotRecommended++
		}
		if candidate.Capability.Operation == tools.Read {
			stats.Read++
		} else {
			stats.Write++
		}
	}
	return stats
}
```

- [ ] **Step 4: Refactor OpenAPI parsing to expose operation metadata**

Modify `internal/capabilities/importer.go`.

Add this type near `openAPIParameter`:

```go
type importedOpenAPIOperation struct {
	Method     string
	Path       string
	Operation  openAPIOperation
	Capability Capability
}
```

Replace the body of `ImportOpenAPI` with:

```go
func ImportOpenAPI(body []byte) ([]Capability, error) {
	operations, err := parseOpenAPIOperations(body)
	if err != nil {
		return nil, err
	}
	drafts := make([]Capability, 0, len(operations))
	for _, operation := range operations {
		drafts = append(drafts, operation.Capability)
	}
	return drafts, nil
}
```

Add the shared parser below `ImportOpenAPI`:

```go
func parseOpenAPIOperations(body []byte) ([]importedOpenAPIOperation, error) {
	var doc openAPIDoc
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(doc.Paths))
	for path := range doc.Paths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	operations := []importedOpenAPIOperation{}
	usedNames := make(map[string]struct{})
	for _, path := range paths {
		var pathParameters []openAPIParameter
		if node, ok := doc.Paths[path]["parameters"]; ok {
			if err := node.Decode(&pathParameters); err != nil {
				return nil, err
			}
		}
		operationNodes := make(map[string]yaml.Node, len(doc.Paths[path]))
		for method, node := range doc.Paths[path] {
			method = strings.ToUpper(method)
			if isSupportedImportMethod(method) {
				operationNodes[method] = node
			}
		}
		methods := make([]string, 0, len(operationNodes))
		for method := range operationNodes {
			methods = append(methods, method)
		}
		sort.Strings(methods)
		for _, method := range methods {
			var operation openAPIOperation
			node := operationNodes[method]
			if err := node.Decode(&operation); err != nil {
				return nil, err
			}
			operation.Parameters = mergeOpenAPIParameters(pathParameters, operation.Parameters)
			draft := inferCapability(method, path, operation)
			draft.Name = uniqueCapabilityName(draft.Name, operation.OperationID, method, path, usedNames)
			operations = append(operations, importedOpenAPIOperation{
				Method:     method,
				Path:       path,
				Operation:  operation,
				Capability: draft,
			})
		}
	}
	return operations, nil
}
```

- [ ] **Step 5: Run focused tests to verify preview model passes**

Run:

```bash
go test -count=1 ./internal/capabilities -run 'TestImportOpenAPI|TestImportOpenAPICandidates|TestApplyCandidateOverride'
```

Expected: PASS.

- [ ] **Step 6: Run full capabilities package tests**

Run:

```bash
go test -count=1 ./internal/capabilities
```

Expected: PASS.

- [ ] **Step 7: Commit Task 1**

Run:

```bash
git add internal/capabilities/import_preview.go internal/capabilities/importer.go internal/capabilities/importer_test.go
git commit -m "feat: add openapi import preview candidates"
```

---

### Task 2: Stateless Manager Preview And Commit

**Files:**
- Modify: `internal/capabilities/manage.go`
- Test: `internal/capabilities/manage_test.go`

**Interfaces:**
- Consumes: `ImportOpenAPICandidates(body []byte, existing []ManagedCapability) (ImportPreview, error)`
- Consumes: `OpenAPIFingerprint(body []byte) string`
- Consumes: `ApplyCandidateOverride(candidate ImportCandidate, override ImportCandidateOverride) Capability`
- Produces: `type OpenAPIURLPreviewRequest`
- Produces: `type OpenAPIURLCommitRequest`
- Produces: `type OpenAPIURLCommitSelection`
- Produces: `type OpenAPIURLCommitResult`
- Produces: `type OpenAPIURLCommitSkipped`
- Produces: `var ErrOpenAPIFingerprintChanged`
- Produces: `func (m *Manager) PreviewOpenAPIFromURL(ctx context.Context, request OpenAPIURLPreviewRequest) (ImportPreview, error)`
- Produces: `func (m *Manager) CommitOpenAPIFromURL(ctx context.Context, request OpenAPIURLCommitRequest) (OpenAPIURLCommitResult, error)`
- Refactors: `func (m *Manager) fetchOpenAPIFromURL(ctx context.Context, openAPIURL string) ([]byte, string, error)`

- [ ] **Step 1: Write failing manager preview/commit tests**

Append these tests to `internal/capabilities/manage_test.go`:

```go
func TestManagerPreviewOpenAPIFromURLDoesNotWriteDrafts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`openapi: 3.0.0
paths:
  /api/minio/{cluster}/buckets/{bucket}/capacity:
    get:
      operationId: getMinioBucketCapacity
      tags: [minio]
      summary: Read bucket capacity
`))
	}))
	defer server.Close()
	manager := capabilities.NewManager(dir, capabilities.NewHTTPAdapter(server.Client()))

	preview, err := manager.PreviewOpenAPIFromURL(context.Background(), capabilities.OpenAPIURLPreviewRequest{
		OpenAPIURL:     server.URL,
		BackendBaseURL: "https://middleware.example.com",
	})
	if err != nil {
		t.Fatalf("PreviewOpenAPIFromURL returned %v", err)
	}

	if preview.Source.OpenAPIURL != server.URL || preview.Source.BackendBaseURL != "https://middleware.example.com" {
		t.Fatalf("source = %+v, want request source", preview.Source)
	}
	if preview.Stats.Total != 1 || preview.Stats.Recommended != 1 {
		t.Fatalf("stats = %+v, want one recommended candidate", preview.Stats)
	}
	if _, err := os.Stat(filepath.Join(dir, capabilities.SourceDiscovered)); !os.IsNotExist(err) {
		t.Fatalf("discovered dir stat err = %v, want no drafts written", err)
	}
}

func TestManagerCommitOpenAPIFromURLWritesOnlySelectedCandidates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := []byte(`openapi: 3.0.0
paths:
  /api/minio/{cluster}/buckets/{bucket}/capacity:
    get:
      operationId: getMinioBucketCapacity
      tags: [minio]
      summary: Read bucket capacity
  /api/kafka/{cluster}/topics/{topic}/retention:
    post:
      operationId: setKafkaTopicRetention
      tags: [kafka]
      summary: Set topic retention
`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(body)
	}))
	defer server.Close()
	manager := capabilities.NewManager(dir, capabilities.NewHTTPAdapter(server.Client()))
	preview, err := manager.PreviewOpenAPIFromURL(context.Background(), capabilities.OpenAPIURLPreviewRequest{
		OpenAPIURL:     server.URL,
		BackendBaseURL: "https://middleware.example.com",
	})
	if err != nil {
		t.Fatalf("PreviewOpenAPIFromURL returned %v", err)
	}

	result, err := manager.CommitOpenAPIFromURL(context.Background(), capabilities.OpenAPIURLCommitRequest{
		OpenAPIURL:     server.URL,
		BackendBaseURL: "https://middleware.example.com",
		Fingerprint:    preview.Source.Fingerprint,
		Selections: []capabilities.OpenAPIURLCommitSelection{{
			CandidateID: "GET /api/minio/{cluster}/buckets/{bucket}/capacity",
			Overrides: capabilities.ImportCandidateOverride{
				Name:         "minio.bucket.capacity.read",
				Domain:       "minio",
				ResourceType: "bucket",
				Operation:    tools.Read,
				Risk:         tools.Low,
			},
		}},
	})
	if err != nil {
		t.Fatalf("CommitOpenAPIFromURL returned %v", err)
	}

	if len(result.Capabilities) != 1 || result.Capabilities[0].Name != "minio.bucket.capacity.read" {
		t.Fatalf("capabilities = %+v, want selected minio draft only", result.Capabilities)
	}
	if result.Capabilities[0].Backend.BaseURL != "https://middleware.example.com" {
		t.Fatalf("base url = %q", result.Capabilities[0].Backend.BaseURL)
	}
	if _, err := manager.Get(context.Background(), "minio.bucket.capacity.read"); err != nil {
		t.Fatalf("selected draft not saved: %v", err)
	}
	if _, err := manager.Get(context.Background(), "kafka.topic.retention.update.setkafkatopicretention"); !errors.Is(err, capabilities.ErrCapabilityNotFound) {
		t.Fatalf("unselected draft lookup err = %v, want ErrCapabilityNotFound", err)
	}
}

func TestManagerCommitOpenAPIFromURLRejectsChangedFingerprint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := []byte(`openapi: 3.0.0
paths:
  /api/minio/{cluster}/buckets/{bucket}/capacity:
    get:
      operationId: getMinioBucketCapacity
      tags: [minio]
      summary: Read bucket capacity
`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(body)
	}))
	defer server.Close()
	manager := capabilities.NewManager(dir, capabilities.NewHTTPAdapter(server.Client()))

	_, err := manager.CommitOpenAPIFromURL(context.Background(), capabilities.OpenAPIURLCommitRequest{
		OpenAPIURL:     server.URL,
		BackendBaseURL: "https://middleware.example.com",
		Fingerprint:    "sha256:changed",
		Selections: []capabilities.OpenAPIURLCommitSelection{{
			CandidateID: "GET /api/minio/{cluster}/buckets/{bucket}/capacity",
		}},
	})

	if !errors.Is(err, capabilities.ErrOpenAPIFingerprintChanged) {
		t.Fatalf("CommitOpenAPIFromURL error = %v, want ErrOpenAPIFingerprintChanged", err)
	}
}

func TestManagerPreviewOpenAPIFromURLRejectsUnsupportedURL(t *testing.T) {
	t.Parallel()
	manager := capabilities.NewManager(t.TempDir(), capabilities.NewHTTPAdapter(nil))

	_, err := manager.PreviewOpenAPIFromURL(context.Background(), capabilities.OpenAPIURLPreviewRequest{
		OpenAPIURL:     "file:///tmp/openapi.yaml",
		BackendBaseURL: "https://middleware.example.com",
	})

	if !errors.Is(err, capabilities.ErrInvalidOpenAPIURL) {
		t.Fatalf("PreviewOpenAPIFromURL error = %v, want ErrInvalidOpenAPIURL", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test -count=1 ./internal/capabilities -run 'TestManagerPreviewOpenAPIFromURL|TestManagerCommitOpenAPIFromURL'
```

Expected: FAIL with undefined preview/commit manager types and methods.

- [ ] **Step 3: Add manager request/response types and error**

Modify `internal/capabilities/manage.go`.

Add below `OpenAPIURLImportRequest`:

```go
type OpenAPIURLPreviewRequest struct {
	OpenAPIURL     string `json:"openapi_url"`
	BackendBaseURL string `json:"backend_base_url"`
}

type OpenAPIURLCommitSelection struct {
	CandidateID string                  `json:"candidate_id"`
	Overrides   ImportCandidateOverride `json:"overrides"`
}

type OpenAPIURLCommitRequest struct {
	OpenAPIURL     string                     `json:"openapi_url"`
	BackendBaseURL string                     `json:"backend_base_url"`
	Fingerprint    string                     `json:"fingerprint"`
	Selections     []OpenAPIURLCommitSelection `json:"selections"`
}

type OpenAPIURLCommitSkipped struct {
	CandidateID string `json:"candidate_id"`
	Reason      string `json:"reason"`
}

type OpenAPIURLCommitResult struct {
	Capabilities []ManagedCapability       `json:"capabilities"`
	Skipped      []OpenAPIURLCommitSkipped `json:"skipped"`
}
```

Add to the `var` block:

```go
ErrOpenAPIFingerprintChanged = errors.New("OpenAPI fingerprint changed")
```

- [ ] **Step 4: Extract shared OpenAPI URL fetch helper**

In `internal/capabilities/manage.go`, replace the URL parsing/fetching/body-read block in `ImportOpenAPIFromURL` with:

```go
body, _, err := m.fetchOpenAPIFromURL(ctx, request.OpenAPIURL)
if err != nil {
	return nil, err
}
```

Add this method above `ImportOpenAPIFromURL`:

```go
func (m *Manager) fetchOpenAPIFromURL(ctx context.Context, openAPIURL string) ([]byte, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(openAPIURL))
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, "", ErrInvalidOpenAPIURL
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, "", err
	}
	response, err := m.adapter.client.Do(httpRequest)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("OpenAPI URL returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxOpenAPIImportBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) > maxOpenAPIImportBytes {
		return nil, "", fmt.Errorf("OpenAPI response exceeds %d bytes", maxOpenAPIImportBytes)
	}
	return body, parsed.String(), nil
}
```

- [ ] **Step 5: Implement preview manager method**

Add below `fetchOpenAPIFromURL`:

```go
func (m *Manager) PreviewOpenAPIFromURL(ctx context.Context, request OpenAPIURLPreviewRequest) (ImportPreview, error) {
	if err := m.configured(); err != nil {
		return ImportPreview{}, err
	}
	if err := validatePublishedBaseURL(request.BackendBaseURL); err != nil {
		return ImportPreview{}, err
	}
	body, normalizedURL, err := m.fetchOpenAPIFromURL(ctx, request.OpenAPIURL)
	if err != nil {
		return ImportPreview{}, err
	}
	existing, err := m.List(ctx)
	if err != nil {
		return ImportPreview{}, err
	}
	preview, err := ImportOpenAPICandidates(body, existing)
	if err != nil {
		return ImportPreview{}, err
	}
	preview.Source.OpenAPIURL = normalizedURL
	preview.Source.BackendBaseURL = strings.TrimSpace(request.BackendBaseURL)
	return preview, nil
}
```

- [ ] **Step 6: Implement commit manager method**

Add below `PreviewOpenAPIFromURL`:

```go
func (m *Manager) CommitOpenAPIFromURL(ctx context.Context, request OpenAPIURLCommitRequest) (OpenAPIURLCommitResult, error) {
	if err := m.configured(); err != nil {
		return OpenAPIURLCommitResult{}, err
	}
	if err := validatePublishedBaseURL(request.BackendBaseURL); err != nil {
		return OpenAPIURLCommitResult{}, err
	}
	body, _, err := m.fetchOpenAPIFromURL(ctx, request.OpenAPIURL)
	if err != nil {
		return OpenAPIURLCommitResult{}, err
	}
	if strings.TrimSpace(request.Fingerprint) != "" && OpenAPIFingerprint(body) != strings.TrimSpace(request.Fingerprint) {
		return OpenAPIURLCommitResult{}, ErrOpenAPIFingerprintChanged
	}
	existing, err := m.List(ctx)
	if err != nil {
		return OpenAPIURLCommitResult{}, err
	}
	preview, err := ImportOpenAPICandidates(body, existing)
	if err != nil {
		return OpenAPIURLCommitResult{}, err
	}
	candidates := make(map[string]ImportCandidate, len(preview.Candidates))
	for _, candidate := range preview.Candidates {
		candidates[candidate.ID] = candidate
	}
	result := OpenAPIURLCommitResult{}
	selected := make(map[string]struct{}, len(request.Selections))
	for _, selection := range request.Selections {
		selected[selection.CandidateID] = struct{}{}
		candidate, ok := candidates[selection.CandidateID]
		if !ok {
			result.Skipped = append(result.Skipped, OpenAPIURLCommitSkipped{CandidateID: selection.CandidateID, Reason: "candidate not found"})
			continue
		}
		capability := ApplyCandidateOverride(candidate, selection.Overrides)
		capability.Backend.BaseURL = strings.TrimSpace(request.BackendBaseURL)
		item, err := m.SaveDraft(ctx, capability)
		if err != nil {
			result.Skipped = append(result.Skipped, OpenAPIURLCommitSkipped{CandidateID: selection.CandidateID, Reason: err.Error()})
			continue
		}
		result.Capabilities = append(result.Capabilities, item)
	}
	for _, candidate := range preview.Candidates {
		if _, ok := selected[candidate.ID]; !ok {
			result.Skipped = append(result.Skipped, OpenAPIURLCommitSkipped{CandidateID: candidate.ID, Reason: "not selected"})
		}
	}
	return result, nil
}
```

- [ ] **Step 7: Run focused manager tests**

Run:

```bash
go test -count=1 ./internal/capabilities -run 'TestManagerPreviewOpenAPIFromURL|TestManagerCommitOpenAPIFromURL'
```

Expected: PASS.

- [ ] **Step 8: Run full capabilities package tests**

Run:

```bash
go test -count=1 ./internal/capabilities
```

Expected: PASS, including legacy `ImportOpenAPIFromURL` tests.

- [ ] **Step 9: Commit Task 2**

Run:

```bash
git add internal/capabilities/manage.go internal/capabilities/manage_test.go
git commit -m "feat: add openapi preview commit manager"
```

---

### Task 3: HTTP Preview And Commit Endpoints

**Files:**
- Modify: `internal/httpapi/router.go`
- Modify: `internal/httpapi/router_test.go`

**Interfaces:**
- Consumes: `capabilities.OpenAPIURLPreviewRequest`
- Consumes: `capabilities.OpenAPIURLCommitRequest`
- Consumes: `capabilities.ImportPreview`
- Consumes: `capabilities.OpenAPIURLCommitResult`
- Produces: `CapabilityManagementService.PreviewOpenAPIFromURL`
- Produces: `CapabilityManagementService.CommitOpenAPIFromURL`
- Produces: `POST /v1/capabilities/import/openapi-url/preview`
- Produces: `POST /v1/capabilities/import/openapi-url/commit`

- [ ] **Step 1: Write failing router tests**

In `internal/httpapi/router_test.go`, add these tests after `TestCapabilityImportOpenAPIURLReturnsImportedDrafts`:

```go
func TestCapabilityPreviewOpenAPIURLRequiresAdmin(t *testing.T) {
	t.Parallel()
	service := &capabilityManagementService{}
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(service),
	)
	body := `{"openapi_url":"https://admin.example.com/v3/api-docs","backend_base_url":"https://middleware.example.com"}`
	req := signedRequest(t, "/v1/capabilities/import/openapi-url/preview", body, "operator-1", []string{"operator"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
	}
	if service.previewCalls != 0 {
		t.Fatalf("preview calls = %d, want 0", service.previewCalls)
	}
}

func TestCapabilityPreviewOpenAPIURLReturnsCandidates(t *testing.T) {
	t.Parallel()
	service := &capabilityManagementService{
		preview: capabilities.ImportPreview{
			Source: capabilities.ImportPreviewSource{
				OpenAPIURL:     "https://admin.example.com/v3/api-docs",
				BackendBaseURL: "https://middleware.example.com",
				Fingerprint:    "sha256:test",
			},
			Stats: capabilities.ImportPreviewStats{Total: 1, Recommended: 1, Read: 1},
			Candidates: []capabilities.ImportCandidate{{
				ID:             "GET /api/minio/{cluster}/buckets/{bucket}/capacity",
				Method:         "GET",
				Path:           "/api/minio/{cluster}/buckets/{bucket}/capacity",
				Recommendation: capabilities.RecommendationRecommended,
				Capability: capabilities.Capability{
					Name:         "minio.bucket.capacity.read",
					Domain:       "minio",
					ResourceType: "bucket",
					Operation:    tools.Read,
					Risk:         tools.Low,
				},
			}},
		},
	}
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(service),
	)
	body := `{"openapi_url":"https://admin.example.com/v3/api-docs","backend_base_url":"https://middleware.example.com"}`
	req := signedRequest(t, "/v1/capabilities/import/openapi-url/preview", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if service.previewCalls != 1 {
		t.Fatalf("preview calls = %d, want 1", service.previewCalls)
	}
	if service.previewRequest.OpenAPIURL != "https://admin.example.com/v3/api-docs" || service.previewRequest.BackendBaseURL != "https://middleware.example.com" {
		t.Fatalf("preview request = %+v, want decoded request", service.previewRequest)
	}
	if !strings.Contains(res.Body.String(), `"candidates"`) || !strings.Contains(res.Body.String(), `"sha256:test"`) {
		t.Fatalf("body = %s, want preview response", res.Body.String())
	}
}

func TestCapabilityCommitOpenAPIURLReturnsSavedDrafts(t *testing.T) {
	t.Parallel()
	service := &capabilityManagementService{
		commitResult: capabilities.OpenAPIURLCommitResult{
			Capabilities: []capabilities.ManagedCapability{{
				Capability: capabilities.Capability{
					Name:         "minio.bucket.capacity.read",
					Status:       capabilities.StatusNeedsReview,
					Domain:       "minio",
					ResourceType: "bucket",
					Operation:    tools.Read,
					Risk:         tools.Low,
				},
				Source: capabilities.SourceDiscovered,
			}},
		},
	}
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		execution.NewReadOnlyService(&readRunner{}, nil),
		httpapi.WithCapabilities(service),
	)
	body := `{"openapi_url":"https://admin.example.com/v3/api-docs","backend_base_url":"https://middleware.example.com","fingerprint":"sha256:test","selections":[{"candidate_id":"GET /api/minio/{cluster}/buckets/{bucket}/capacity","overrides":{"name":"minio.bucket.capacity.read","domain":"minio","resource_type":"bucket","operation":"read","risk":"low"}}]}`
	req := signedRequest(t, "/v1/capabilities/import/openapi-url/commit", body, "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	if service.commitCalls != 1 {
		t.Fatalf("commit calls = %d, want 1", service.commitCalls)
	}
	if service.commitRequest.Fingerprint != "sha256:test" || len(service.commitRequest.Selections) != 1 {
		t.Fatalf("commit request = %+v, want decoded request", service.commitRequest)
	}
	if !strings.Contains(res.Body.String(), `"capabilities"`) || !strings.Contains(res.Body.String(), `"minio.bucket.capacity.read"`) {
		t.Fatalf("body = %s, want commit result", res.Body.String())
	}
}
```

Update the fake `capabilityManagementService` in the same test file with fields:

```go
preview        capabilities.ImportPreview
previewRequest capabilities.OpenAPIURLPreviewRequest
previewCalls   int
commitResult   capabilities.OpenAPIURLCommitResult
commitRequest  capabilities.OpenAPIURLCommitRequest
commitCalls    int
```

Add methods:

```go
func (s *capabilityManagementService) PreviewOpenAPIFromURL(_ context.Context, request capabilities.OpenAPIURLPreviewRequest) (capabilities.ImportPreview, error) {
	s.previewCalls++
	s.previewRequest = request
	return s.preview, nil
}

func (s *capabilityManagementService) CommitOpenAPIFromURL(_ context.Context, request capabilities.OpenAPIURLCommitRequest) (capabilities.OpenAPIURLCommitResult, error) {
	s.commitCalls++
	s.commitRequest = request
	return s.commitResult, nil
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test -count=1 ./internal/httpapi -run 'TestCapabilityPreviewOpenAPIURL|TestCapabilityCommitOpenAPIURL'
```

Expected: FAIL because `CapabilityManagementService` lacks preview/commit methods and router routes do not exist.

- [ ] **Step 3: Add preview/commit methods to service interface**

Modify `internal/httpapi/router.go` `CapabilityManagementService`:

```go
type CapabilityManagementService interface {
	List(context.Context) ([]capabilities.ManagedCapability, error)
	Get(context.Context, string) (capabilities.ManagedCapability, error)
	SaveDraft(context.Context, capabilities.Capability) (capabilities.ManagedCapability, error)
	ValidateCapability(capabilities.Capability) capabilities.ValidationResult
	Test(context.Context, capabilities.Capability, map[string]any) (capabilities.NormalizedResult, error)
	ImportOpenAPIFromURL(context.Context, capabilities.OpenAPIURLImportRequest) ([]capabilities.ManagedCapability, error)
	PreviewOpenAPIFromURL(context.Context, capabilities.OpenAPIURLPreviewRequest) (capabilities.ImportPreview, error)
	CommitOpenAPIFromURL(context.Context, capabilities.OpenAPIURLCommitRequest) (capabilities.OpenAPIURLCommitResult, error)
	Publish(context.Context, string) (capabilities.ManagedCapability, error)
	Unpublish(context.Context, string) (capabilities.ManagedCapability, error)
}
```

- [ ] **Step 4: Add router cases**

In `serveCapabilities`, add these cases before the legacy `/v1/capabilities/import/openapi-url` case:

```go
case request.Method == http.MethodPost && request.URL.Path == "/v1/capabilities/import/openapi-url/preview":
	var body capabilities.OpenAPIURLPreviewRequest
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 10*1024))
	if err := decoder.Decode(&body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid JSON input")
		return
	}
	preview, err := r.capability.PreviewOpenAPIFromURL(ctx, body)
	if err != nil {
		writeCapabilityError(writer, err)
		return
	}
	writeCapabilityJSON(writer, preview)
case request.Method == http.MethodPost && request.URL.Path == "/v1/capabilities/import/openapi-url/commit":
	var body capabilities.OpenAPIURLCommitRequest
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64*1024))
	if err := decoder.Decode(&body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid JSON input")
		return
	}
	result, err := r.capability.CommitOpenAPIFromURL(ctx, body)
	if err != nil {
		writeCapabilityError(writer, err)
		return
	}
	writeCapabilityJSON(writer, result)
```

- [ ] **Step 5: Map fingerprint mismatch to a user-facing error**

In `writeCapabilityError`, add:

```go
case errors.Is(err, capabilities.ErrOpenAPIFingerprintChanged):
	writeError(writer, http.StatusConflict, "Swagger 文档已变化，请重新预览")
```

- [ ] **Step 6: Run focused router tests**

Run:

```bash
go test -count=1 ./internal/httpapi -run 'TestCapabilityPreviewOpenAPIURL|TestCapabilityCommitOpenAPIURL|TestCapabilityImportOpenAPIURL'
```

Expected: PASS.

- [ ] **Step 7: Run HTTP API package tests**

Run:

```bash
go test -count=1 ./internal/httpapi
```

Expected: PASS.

- [ ] **Step 8: Commit Task 3**

Run:

```bash
git add internal/httpapi/router.go internal/httpapi/router_test.go
git commit -m "feat: expose openapi preview commit endpoints"
```

---

### Task 4: Frontend Import Wizard Models And API Helpers

**Files:**
- Modify: `apps/capability-console/src/types.ts`
- Modify: `apps/capability-console/src/api.ts`
- Create: `apps/capability-console/src/importWizard.ts`
- Create: `apps/capability-console/src/importWizard.test.ts`

**Interfaces:**
- Produces: `type ImportRecommendation = 'recommended' | 'needs_adjustment' | 'not_recommended'`
- Produces: `interface ImportPreviewSource`
- Produces: `interface ImportPreviewStats`
- Produces: `interface ImportCandidate`
- Produces: `interface ImportPreview`
- Produces: `interface ImportCandidateOverride`
- Produces: `interface ImportCommitSelection`
- Produces: `interface OpenAPIURLCommitPayload`
- Produces: `interface OpenAPIURLCommitResult`
- Produces: `function previewOpenAPIURL(payload: OpenAPIURLImportPayload): Promise<ImportPreview>`
- Produces: `function commitOpenAPIURLImport(payload: OpenAPIURLCommitPayload): Promise<OpenAPIURLCommitResult>`
- Produces: `function createCandidateSelections(preview: ImportPreview): Record<string, boolean>`
- Produces: `function createCandidateOverrides(preview: ImportPreview): Record<string, ImportCandidateOverride>`
- Produces: `function selectedCandidates(preview: ImportPreview, selections: Record<string, boolean>): ImportCandidate[]`
- Produces: `function buildCommitSelections(preview: ImportPreview, selections: Record<string, boolean>, overrides: Record<string, ImportCandidateOverride>): ImportCommitSelection[]`
- Produces: `interface ImportCandidateFilters`
- Produces: `function filterImportCandidates(preview: ImportPreview, filters: ImportCandidateFilters): ImportCandidate[]`

- [ ] **Step 1: Write failing import wizard helper tests**

Create `apps/capability-console/src/importWizard.test.ts`:

```ts
import { describe, expect, test } from 'vitest';
import {
  buildCommitSelections,
  createCandidateOverrides,
  createCandidateSelections,
  filterImportCandidates,
  selectedCandidates,
} from './importWizard';
import type { ImportPreview } from './types';

const preview: ImportPreview = {
  source: {
    openapi_url: 'https://admin.example.com/v3/api-docs',
    backend_base_url: 'https://middleware.example.com',
    fingerprint: 'sha256:test',
  },
  stats: {
    total: 3,
    recommended: 1,
    needs_adjustment: 1,
    not_recommended: 1,
    read: 2,
    write: 1,
  },
  candidates: [
    {
      id: 'GET /api/minio/{cluster}/buckets/{bucket}/capacity',
      method: 'GET',
      path: '/api/minio/{cluster}/buckets/{bucket}/capacity',
      operation_id: 'getMinioBucketCapacity',
      capability: {
        name: 'minio.bucket.capacity.read',
        domain: 'minio',
        resource_type: 'bucket',
        operation: 'read',
        risk: 'low',
      },
      recommendation: 'recommended',
      reasons: ['GET read operation'],
      warnings: [],
    },
    {
      id: 'GET /api/unknown/status',
      method: 'GET',
      path: '/api/unknown/status',
      capability: {
        name: 'unknown.resource.status.read',
        domain: 'unknown',
        resource_type: 'resource',
        operation: 'read',
        risk: 'low',
      },
      recommendation: 'needs_adjustment',
      reasons: ['需要调整识别结果'],
      warnings: ['领域或资源类型需要确认'],
    },
    {
      id: 'POST /api/kafka/{cluster}/topics/{topic}/retention',
      method: 'POST',
      path: '/api/kafka/{cluster}/topics/{topic}/retention',
      capability: {
        name: 'kafka.topic.retention.update',
        domain: 'kafka',
        resource_type: 'topic',
        operation: 'write',
        risk: 'medium',
      },
      recommendation: 'not_recommended',
      reasons: ['第一版暂不接入写入能力'],
      warnings: [],
    },
  ],
};

describe('import wizard helpers', () => {
  test('selects recommended candidates by default', () => {
    const selections = createCandidateSelections(preview);

    expect(selections['GET /api/minio/{cluster}/buckets/{bucket}/capacity']).toBe(true);
    expect(selections['GET /api/unknown/status']).toBe(false);
    expect(selections['POST /api/kafka/{cluster}/topics/{topic}/retention']).toBe(false);
    expect(selectedCandidates(preview, selections).map((candidate) => candidate.id)).toEqual([
      'GET /api/minio/{cluster}/buckets/{bucket}/capacity',
    ]);
  });

  test('creates editable overrides from candidate summaries', () => {
    const overrides = createCandidateOverrides(preview);

    expect(overrides['GET /api/minio/{cluster}/buckets/{bucket}/capacity']).toEqual({
      name: 'minio.bucket.capacity.read',
      domain: 'minio',
      resource_type: 'bucket',
      operation: 'read',
      risk: 'low',
    });
  });

  test('builds commit selections from selected candidates and overrides', () => {
    const selections = createCandidateSelections(preview);
    selections['GET /api/unknown/status'] = true;
    const overrides = createCandidateOverrides(preview);
    overrides['GET /api/unknown/status'] = {
      name: 'middleware.status.read',
      domain: 'middleware',
      resource_type: 'service',
      operation: 'read',
      risk: 'low',
    };

    expect(buildCommitSelections(preview, selections, overrides)).toEqual([
      {
        candidate_id: 'GET /api/minio/{cluster}/buckets/{bucket}/capacity',
        overrides: {
          name: 'minio.bucket.capacity.read',
          domain: 'minio',
          resource_type: 'bucket',
          operation: 'read',
          risk: 'low',
        },
      },
      {
        candidate_id: 'GET /api/unknown/status',
        overrides: {
          name: 'middleware.status.read',
          domain: 'middleware',
          resource_type: 'service',
          operation: 'read',
          risk: 'low',
        },
      },
    ]);
  });

  test('filters candidates by recommendation domain and search text', () => {
    expect(filterImportCandidates(preview, {
      recommendation: 'recommended',
      domain: 'all',
      search: '',
    }).map((candidate) => candidate.id)).toEqual([
      'GET /api/minio/{cluster}/buckets/{bucket}/capacity',
    ]);

    expect(filterImportCandidates(preview, {
      recommendation: 'all',
      domain: 'kafka',
      search: 'retention',
    }).map((candidate) => candidate.id)).toEqual([
      'POST /api/kafka/{cluster}/topics/{topic}/retention',
    ]);
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd apps/capability-console
npm test -- --run src/importWizard.test.ts
```

Expected: FAIL because `importWizard.ts` and preview types do not exist.

- [ ] **Step 3: Add TypeScript preview and commit types**

Append to `apps/capability-console/src/types.ts`:

```ts
export type ImportRecommendation = 'recommended' | 'needs_adjustment' | 'not_recommended';

export interface ImportPreviewSource {
  openapi_url: string;
  backend_base_url: string;
  fingerprint: string;
}

export interface ImportPreviewStats {
  total: number;
  recommended: number;
  needs_adjustment: number;
  not_recommended: number;
  read: number;
  write: number;
}

export interface ImportCandidateSummary {
  name: string;
  domain: string;
  resource_type: string;
  operation: CapabilityOperation;
  risk: CapabilityRisk;
}

export interface ImportCandidate {
  id: string;
  method: string;
  path: string;
  operation_id?: string;
  capability: ImportCandidateSummary;
  summary?: ImportCandidateSummary;
  recommendation: ImportRecommendation;
  reasons: string[];
  warnings: string[];
}

export interface ImportPreview {
  source: ImportPreviewSource;
  stats: ImportPreviewStats;
  candidates: ImportCandidate[];
}

export interface ImportCandidateOverride {
  name: string;
  domain: string;
  resource_type: string;
  operation: CapabilityOperation;
  risk: CapabilityRisk;
}

export interface ImportCommitSelection {
  candidate_id: string;
  overrides: ImportCandidateOverride;
}

export interface OpenAPIURLCommitPayload {
  openapi_url: string;
  backend_base_url: string;
  fingerprint: string;
  selections: ImportCommitSelection[];
}

export interface OpenAPIURLCommitResult {
  capabilities: ManagedCapability[];
  skipped: Array<{ candidate_id: string; reason: string }>;
}
```

- [ ] **Step 4: Add frontend API helpers**

Modify the import line in `apps/capability-console/src/api.ts`:

```ts
import type {
  AssistantConsoleResponse,
  Capability,
  ImportPreview,
  ManagedCapability,
  NormalizedResult,
  OpenAPIURLCommitPayload,
  OpenAPIURLCommitResult,
  ValidationResult,
} from './types';
```

Add after `importOpenAPIURL`:

```ts
export async function previewOpenAPIURL(payload: OpenAPIURLImportPayload): Promise<ImportPreview> {
  return request<ImportPreview>('/v1/capabilities/import/openapi-url/preview', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

export async function commitOpenAPIURLImport(payload: OpenAPIURLCommitPayload): Promise<OpenAPIURLCommitResult> {
  const body = await request<{ capabilities?: Partial<ManagedCapability>[]; skipped?: OpenAPIURLCommitResult['skipped'] }>('/v1/capabilities/import/openapi-url/commit', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
  return {
    capabilities: (body.capabilities ?? []).map(normalizeCapability),
    skipped: body.skipped ?? [],
  };
}
```

- [ ] **Step 5: Implement pure import wizard helpers**

Create `apps/capability-console/src/importWizard.ts`:

```ts
import type {
  ImportCandidate,
  ImportCandidateOverride,
  ImportCommitSelection,
  ImportPreview,
  ImportRecommendation,
} from './types';

export interface ImportCandidateFilters {
  recommendation: ImportRecommendation | 'all';
  domain: string;
  search: string;
}

export function createCandidateSelections(preview: ImportPreview): Record<string, boolean> {
  return Object.fromEntries(preview.candidates.map((candidate) => [
    candidate.id,
    candidate.recommendation === 'recommended',
  ]));
}

export function createCandidateOverrides(preview: ImportPreview): Record<string, ImportCandidateOverride> {
  return Object.fromEntries(preview.candidates.map((candidate) => {
    const summary = candidate.summary ?? candidate.capability;
    return [candidate.id, {
      name: summary.name,
      domain: summary.domain,
      resource_type: summary.resource_type,
      operation: summary.operation,
      risk: summary.risk,
    }];
  }));
}

export function selectedCandidates(preview: ImportPreview, selections: Record<string, boolean>): ImportCandidate[] {
  return preview.candidates.filter((candidate) => selections[candidate.id]);
}

export function buildCommitSelections(
  preview: ImportPreview,
  selections: Record<string, boolean>,
  overrides: Record<string, ImportCandidateOverride>,
): ImportCommitSelection[] {
  return selectedCandidates(preview, selections).map((candidate) => ({
    candidate_id: candidate.id,
    overrides: overrides[candidate.id] ?? createCandidateOverrides({ ...preview, candidates: [candidate] })[candidate.id],
  }));
}

export function filterImportCandidates(preview: ImportPreview, filters: ImportCandidateFilters): ImportCandidate[] {
  const search = filters.search.trim().toLowerCase();
  return preview.candidates.filter((candidate) => {
    if (filters.recommendation !== 'all' && candidate.recommendation !== filters.recommendation) {
      return false;
    }
    const summary = candidate.summary ?? candidate.capability;
    if (filters.domain !== 'all' && summary.domain !== filters.domain) {
      return false;
    }
    if (search === '') {
      return true;
    }
    return [
      candidate.id,
      candidate.method,
      candidate.path,
      candidate.operation_id ?? '',
      summary.name,
      summary.domain,
      summary.resource_type,
    ].some((value) => value.toLowerCase().includes(search));
  });
}

export function importPreviewDomains(preview: ImportPreview): string[] {
  return Array.from(new Set(preview.candidates.map((candidate) => (candidate.summary ?? candidate.capability).domain || 'other'))).sort();
}
```

- [ ] **Step 6: Run helper tests**

Run:

```bash
cd apps/capability-console
npm test -- --run src/importWizard.test.ts
```

Expected: PASS.

- [ ] **Step 7: Run frontend tests**

Run:

```bash
cd apps/capability-console
npm test
```

Expected: PASS.

- [ ] **Step 8: Commit Task 4**

Run:

```bash
git add apps/capability-console/src/types.ts apps/capability-console/src/api.ts apps/capability-console/src/importWizard.ts apps/capability-console/src/importWizard.test.ts
git commit -m "feat: add import wizard frontend model"
```

---

### Task 5: Vue Import Wizard UI

**Files:**
- Modify: `apps/capability-console/src/App.vue`
- Modify: `apps/capability-console/src/App.test.ts`
- Modify: `apps/capability-console/src/styles.css`

**Interfaces:**
- Consumes: `previewOpenAPIURL(payload: OpenAPIURLImportPayload): Promise<ImportPreview>`
- Consumes: `commitOpenAPIURLImport(payload: OpenAPIURLCommitPayload): Promise<OpenAPIURLCommitResult>`
- Consumes: import wizard helpers from `src/importWizard.ts`
- Produces selectors:
  - `data-test="import-wizard"`
  - `data-test="import-step-source"`
  - `data-test="preview-openapi-url"`
  - `data-test="import-preview"`
  - `data-test="candidate-row-<candidate.id>"`
  - `data-test="candidate-selected-<candidate.id>"`
  - `data-test="candidate-name-<candidate.id>"`
  - `data-test="candidate-domain-<candidate.id>"`
  - `data-test="candidate-resource-<candidate.id>"`
  - `data-test="candidate-operation-<candidate.id>"`
  - `data-test="candidate-risk-<candidate.id>"`
  - `data-test="commit-openapi-import"`
  - `data-test="import-commit-summary"`

- [ ] **Step 1: Write failing wizard UI tests**

In `apps/capability-console/src/App.test.ts`, update the fetch stub in `beforeEach`:

```ts
if (url === '/v1/capabilities/import/openapi-url/preview') {
  return ok({
    source: {
      openapi_url: 'https://admin.example.com/v3/api-docs',
      backend_base_url: 'https://middleware.example.com',
      fingerprint: 'sha256:test',
    },
    stats: {
      total: 2,
      recommended: 1,
      needs_adjustment: 0,
      not_recommended: 1,
      read: 1,
      write: 1,
    },
    candidates: [
      {
        id: 'GET /api/minio/{cluster}/buckets/{bucket}/capacity',
        method: 'GET',
        path: '/api/minio/{cluster}/buckets/{bucket}/capacity',
        operation_id: 'getMinioBucketCapacity',
        capability: {
          name: 'minio.bucket.capacity.read.imported',
          domain: 'minio',
          resource_type: 'bucket',
          operation: 'read',
          risk: 'low',
        },
        recommendation: 'recommended',
        reasons: ['GET read operation'],
        warnings: [],
      },
      {
        id: 'POST /api/kafka/{cluster}/topics/{topic}/retention',
        method: 'POST',
        path: '/api/kafka/{cluster}/topics/{topic}/retention',
        operation_id: 'setKafkaTopicRetention',
        capability: {
          name: 'kafka.topic.retention.update',
          domain: 'kafka',
          resource_type: 'topic',
          operation: 'write',
          risk: 'medium',
        },
        recommendation: 'not_recommended',
        reasons: ['第一版暂不接入写入能力'],
        warnings: [],
      },
    ],
  });
}
if (url === '/v1/capabilities/import/openapi-url/commit') {
  return ok({
    capabilities: [
      {
        name: 'minio.bucket.capacity.read.imported',
        status: 'needs_review',
        source: 'discovered',
        domain: 'minio',
        resource_type: 'bucket',
        operation: 'read',
        risk: 'low',
        backend: { method: 'GET', base_url: 'https://middleware.example.com', path: '/api/minio/{cluster}/buckets/{bucket}/capacity' },
        validation: { valid: true },
      },
    ],
    skipped: [{ candidate_id: 'POST /api/kafka/{cluster}/topics/{topic}/retention', reason: 'not selected' }],
  });
}
```

Add this test near existing Swagger import tests:

```ts
test('previews Swagger candidates before generating selected drafts', async () => {
  const fetchMock = vi.mocked(fetch);
  const wrapper = mountApp();
  await flushPromises();
  await openManagement(wrapper);

  expect(wrapper.find('[data-test="import-wizard"]').exists()).toBe(true);
  expect(wrapper.find('[data-test="commit-openapi-import"]').attributes('disabled')).toBeDefined();

  await wrapper.find('[data-test="openapi-url-input"]').setValue('https://admin.example.com/v3/api-docs');
  await wrapper.find('[data-test="backend-base-url-input"]').setValue('https://middleware.example.com');
  await wrapper.find('[data-test="preview-openapi-url"]').trigger('click');
  await flushPromises();

  const previewCall = fetchMock.mock.calls.find(([input]) => String(input) === '/v1/capabilities/import/openapi-url/preview');
  expect(previewCall).toBeDefined();
  expect(JSON.parse(String(previewCall?.[1]?.body))).toEqual({
    openapi_url: 'https://admin.example.com/v3/api-docs',
    backend_base_url: 'https://middleware.example.com',
  });
  expect(wrapper.find('[data-test="import-preview"]').text()).toContain('推荐接入');
  expect(wrapper.find('[data-test="import-preview"]').text()).toContain('minio.bucket.capacity.read.imported');
  expect(wrapper.find('[data-test="import-preview"]').text()).toContain('第一版暂不接入写入能力');
  expect((wrapper.find('[data-test="candidate-selected-GET /api/minio/{cluster}/buckets/{bucket}/capacity"]').element as HTMLInputElement).checked).toBe(true);
  expect((wrapper.find('[data-test="candidate-selected-POST /api/kafka/{cluster}/topics/{topic}/retention"]').element as HTMLInputElement).checked).toBe(false);
  expect(wrapper.find('[data-test="capability-table-body"]').text()).not.toContain('minio.bucket.capacity.read.imported');

  await wrapper.find('[data-test="candidate-name-GET /api/minio/{cluster}/buckets/{bucket}/capacity"]').setValue('minio.bucket.capacity.read.custom');
  await wrapper.find('[data-test="commit-openapi-import"]').trigger('click');
  await flushPromises();

  const commitCall = fetchMock.mock.calls.find(([input]) => String(input) === '/v1/capabilities/import/openapi-url/commit');
  expect(commitCall).toBeDefined();
  expect(JSON.parse(String(commitCall?.[1]?.body))).toEqual({
    openapi_url: 'https://admin.example.com/v3/api-docs',
    backend_base_url: 'https://middleware.example.com',
    fingerprint: 'sha256:test',
    selections: [
      {
        candidate_id: 'GET /api/minio/{cluster}/buckets/{bucket}/capacity',
        overrides: {
          name: 'minio.bucket.capacity.read.custom',
          domain: 'minio',
          resource_type: 'bucket',
          operation: 'read',
          risk: 'low',
        },
      },
    ],
  });
  expect(wrapper.find('[data-test="import-result"]').text()).toContain('已生成 1 个待评审草稿');
  expect(wrapper.find('[data-test="capability-table-body"]').text()).toContain('minio.bucket.capacity.read.imported');
  expect(wrapper.find('[data-test="import-batch"]').text()).toContain('本次导入');
});
```

Add zero-selection test:

```ts
test('disables Swagger commit when no candidates are selected', async () => {
  const wrapper = mountApp();
  await flushPromises();
  await openManagement(wrapper);

  await wrapper.find('[data-test="preview-openapi-url"]').trigger('click');
  await flushPromises();
  await wrapper.find('[data-test="candidate-selected-GET /api/minio/{cluster}/buckets/{bucket}/capacity"]').setValue(false);

  expect(wrapper.find('[data-test="commit-openapi-import"]').attributes('disabled')).toBeDefined();
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
cd apps/capability-console
npm test -- --run src/App.test.ts -t 'previews Swagger candidates|disables Swagger commit'
```

Expected: FAIL because wizard selectors and preview/commit handlers do not exist.

- [ ] **Step 3: Add imports and wizard state to `App.vue`**

Modify API imports:

```ts
  commitOpenAPIURLImport,
  previewOpenAPIURL,
```

Modify helper imports:

```ts
import {
  buildCommitSelections,
  createCandidateOverrides,
  createCandidateSelections,
  filterImportCandidates,
  importPreviewDomains,
  selectedCandidates,
} from './importWizard';
```

Modify type imports:

```ts
import type {
  AssistantConsoleResponse,
  Capability,
  CapabilityOperation,
  CapabilityRisk,
  ImportCandidateOverride,
  ImportRecommendation,
  ImportPreview,
  InputField,
  ManagedCapability,
  NormalizedResult,
  ValidationResult,
} from './types';

import type { ImportCandidateFilters } from './importWizard';
```

Add state near existing import refs:

```ts
type ImportWizardStep = 'source' | 'candidates' | 'adjust' | 'commit';

const importWizardStep = ref<ImportWizardStep>('source');
const importPreview = ref<ImportPreview | null>(null);
const importPreviewLoading = ref(false);
const importCommitLoading = ref(false);
const candidateSelections = ref<Record<string, boolean>>({});
const candidateOverrides = ref<Record<string, ImportCandidateOverride>>({});
const candidateFilters = ref<ImportCandidateFilters>({
  recommendation: 'all',
  domain: 'all',
  search: '',
});
```

Also import `ImportCandidateOverride` from `./types`.

- [ ] **Step 4: Add computed wizard values**

Add near current import batch computeds:

```ts
const visibleImportCandidates = computed(() => (importPreview.value ? filterImportCandidates(importPreview.value, candidateFilters.value) : []));
const importCandidateDomains = computed(() => (importPreview.value ? importPreviewDomains(importPreview.value) : []));
const selectedImportCandidates = computed(() => (importPreview.value ? selectedCandidates(importPreview.value, candidateSelections.value) : []));
const canCommitImportPreview = computed(() => selectedImportCandidates.value.length > 0 && !importCommitLoading.value);
const importCommitSummary = computed(() => {
  const selected = selectedImportCandidates.value;
  const reads = selected.filter((candidate) => (candidate.summary ?? candidate.capability).operation === 'read').length;
  const writes = selected.length - reads;
  return { selected: selected.length, reads, writes };
});
```

- [ ] **Step 5: Add preview/commit handlers**

Replace or keep `importSwaggerURL` for compatibility, then add:

```ts
async function previewSwaggerURL() {
  error.value = '';
  importMessage.value = '';
  importPreviewLoading.value = true;
  try {
    const preview = await previewOpenAPIURL({
      openapi_url: importOpenAPIURLText.value,
      backend_base_url: importBackendBaseURL.value,
    });
    importPreview.value = preview;
    candidateSelections.value = createCandidateSelections(preview);
    candidateOverrides.value = createCandidateOverrides(preview);
    candidateFilters.value = { recommendation: 'all', domain: 'all', search: '' };
    importWizardStep.value = preview.candidates.length === 0 ? 'source' : 'candidates';
    importMessage.value = preview.candidates.length === 0 ? '没有识别到可接入 API' : `已预览 ${preview.candidates.length} 个候选 API`;
  } catch (err) {
    error.value = err instanceof Error ? err.message : '预览 Swagger URL 失败';
  } finally {
    importPreviewLoading.value = false;
  }
}

async function commitSwaggerImport() {
  if (!importPreview.value || !canCommitImportPreview.value) {
    return;
  }
  error.value = '';
  importCommitLoading.value = true;
  try {
    const result = await commitOpenAPIURLImport({
      openapi_url: importPreview.value.source.openapi_url || importOpenAPIURLText.value,
      backend_base_url: importPreview.value.source.backend_base_url || importBackendBaseURL.value,
      fingerprint: importPreview.value.source.fingerprint,
      selections: buildCommitSelections(importPreview.value, candidateSelections.value, candidateOverrides.value),
    });
    importBatch.value = createImportBatch(result.capabilities, capabilities.value);
    importDomainFilter.value = 'all';
    for (const item of result.capabilities) {
      upsert(item);
    }
    if (result.capabilities.length > 0) {
      selectCapability(result.capabilities[0]);
    }
    importWizardStep.value = 'commit';
    importMessage.value = result.capabilities.length === 0 ? '没有生成草稿' : `已生成 ${result.capabilities.length} 个待评审草稿`;
  } catch (err) {
    error.value = err instanceof Error ? err.message : '生成 Capability 草稿失败';
  } finally {
    importCommitLoading.value = false;
  }
}
```

Add small setter:

```ts
function updateCandidateOverride(id: string, patch: Partial<ImportCandidateOverride>) {
  candidateOverrides.value = {
    ...candidateOverrides.value,
    [id]: {
      ...candidateOverrides.value[id],
      ...patch,
    },
  };
}
```

- [ ] **Step 6: Replace import strip template with wizard source and preview**

Replace the existing `<section class="import-strip"...>` block with:

```vue
<section data-test="import-wizard" class="import-wizard" aria-label="Swagger 接入向导">
  <div class="wizard-steps">
    <button class="wizard-step" :class="{ active: importWizardStep === 'source' }" @click="importWizardStep = 'source'">1 来源</button>
    <button class="wizard-step" :class="{ active: importWizardStep === 'candidates' }" :disabled="!importPreview" @click="importWizardStep = 'candidates'">2 候选 API</button>
    <button class="wizard-step" :class="{ active: importWizardStep === 'adjust' }" :disabled="!importPreview" @click="importWizardStep = 'adjust'">3 调整选择</button>
    <button class="wizard-step" :class="{ active: importWizardStep === 'commit' }" :disabled="!importPreview" @click="importWizardStep = 'commit'">4 生成草稿</button>
  </div>

  <div data-test="import-step-source" class="import-strip">
    <label>
      <span>Swagger / OpenAPI 地址</span>
      <input data-test="openapi-url-input" v-model="importOpenAPIURLText" class="filter-input" placeholder="http://你的后台/v3/api-docs" />
    </label>
    <label>
      <span>中间件后台 Base URL</span>
      <input data-test="backend-base-url-input" v-model="importBackendBaseURL" class="filter-input" placeholder="https://middleware.example.com" />
    </label>
    <el-button data-test="preview-openapi-url" type="primary" :loading="importPreviewLoading" @click="previewSwaggerURL">预览 API</el-button>
    <strong v-if="importMessage" data-test="import-result">{{ importMessage }}</strong>
  </div>

  <section v-if="importPreview" data-test="import-preview" class="import-preview">
    <div class="import-batch-stats">
      <div><span>全部接口</span><strong>{{ importPreview.stats.total }}</strong></div>
      <div><span>推荐接入</span><strong>{{ importPreview.stats.recommended }}</strong></div>
      <div><span>需要调整</span><strong>{{ importPreview.stats.needs_adjustment }}</strong></div>
      <div><span>暂不接入</span><strong>{{ importPreview.stats.not_recommended }}</strong></div>
      <div><span>读取</span><strong>{{ importPreview.stats.read }}</strong></div>
      <div><span>写入</span><strong>{{ importPreview.stats.write }}</strong></div>
    </div>
    <div class="filters">
      <input data-test="candidate-search" v-model="candidateFilters.search" class="filter-input" placeholder="搜索候选 API" />
      <select data-test="candidate-recommendation-filter" v-model="candidateFilters.recommendation" class="filter-select">
        <option value="all">全部建议</option>
        <option value="recommended">推荐接入</option>
        <option value="needs_adjustment">需要调整</option>
        <option value="not_recommended">暂不接入</option>
      </select>
      <select data-test="candidate-domain-filter" v-model="candidateFilters.domain" class="filter-select">
        <option value="all">全部领域</option>
        <option v-for="domain in importCandidateDomains" :key="domain" :value="domain">{{ domain }}</option>
      </select>
    </div>
    <div class="candidate-list">
      <article v-for="candidate in visibleImportCandidates" :key="candidate.id" class="candidate-row" :data-test="`candidate-row-${candidate.id}`">
        <input
          :data-test="`candidate-selected-${candidate.id}`"
          type="checkbox"
          v-model="candidateSelections[candidate.id]"
        />
        <div class="method-pill">{{ candidate.method }}</div>
        <div class="candidate-main">
          <strong>{{ candidate.path }}</strong>
          <small>{{ candidate.operation_id || candidate.id }}</small>
          <small>{{ candidate.reasons.join(' / ') }}</small>
        </div>
        <div class="candidate-edit-grid">
          <input :data-test="`candidate-name-${candidate.id}`" class="mini-input" :value="candidateOverrides[candidate.id]?.name" @input="updateCandidateOverride(candidate.id, { name: ($event.target as HTMLInputElement).value })" />
          <input :data-test="`candidate-domain-${candidate.id}`" class="mini-input" :value="candidateOverrides[candidate.id]?.domain" @input="updateCandidateOverride(candidate.id, { domain: ($event.target as HTMLInputElement).value })" />
          <input :data-test="`candidate-resource-${candidate.id}`" class="mini-input" :value="candidateOverrides[candidate.id]?.resource_type" @input="updateCandidateOverride(candidate.id, { resource_type: ($event.target as HTMLInputElement).value })" />
          <select :data-test="`candidate-operation-${candidate.id}`" class="mini-select" :value="candidateOverrides[candidate.id]?.operation" @change="updateCandidateOverride(candidate.id, { operation: ($event.target as HTMLSelectElement).value as CapabilityOperation })">
            <option value="read">读取</option>
            <option value="write">写入</option>
          </select>
          <select :data-test="`candidate-risk-${candidate.id}`" class="mini-select" :value="candidateOverrides[candidate.id]?.risk" @change="updateCandidateOverride(candidate.id, { risk: ($event.target as HTMLSelectElement).value as CapabilityRisk })">
            <option value="low">低</option>
            <option value="medium">中</option>
            <option value="high">高</option>
          </select>
        </div>
        <div class="verdict-cell">
          <strong>{{ recommendationLabel(candidate.recommendation) }}</strong>
          <small>{{ candidate.warnings.join(' / ') || candidate.reasons.join(' / ') }}</small>
        </div>
      </article>
    </div>
    <div data-test="import-commit-summary" class="commit-summary">
      已选择 {{ importCommitSummary.selected }} 个候选 API，其中读取 {{ importCommitSummary.reads }} 个，写入/高风险 {{ importCommitSummary.writes }} 个。
      <el-button data-test="commit-openapi-import" type="primary" :disabled="!canCommitImportPreview" :loading="importCommitLoading" @click="commitSwaggerImport">生成 Capability 草稿</el-button>
    </div>
  </section>
</section>
```

Add label helper in script:

```ts
function recommendationLabel(value: ImportRecommendation): string {
  if (value === 'recommended') {
    return '推荐接入';
  }
  if (value === 'needs_adjustment') {
    return '需要调整';
  }
  return '暂不接入';
}
```

Import `ImportRecommendation`, `CapabilityOperation`, and `CapabilityRisk` types from `./types`.

- [ ] **Step 7: Add wizard styles**

Add to `apps/capability-console/src/styles.css` near import styles:

```css
.import-wizard {
  background: #ffffff;
  border: 1px solid #cbd6da;
  border-left: 4px solid #0f766e;
  border-radius: 8px;
  margin: 0 auto 16px;
  max-width: 1440px;
  padding: 12px;
}

.wizard-steps {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 12px;
}

.wizard-step {
  background: #f6f9fa;
  border: 1px solid #d8e4e2;
  border-radius: 6px;
  color: #344a48;
  cursor: pointer;
  font-weight: 800;
  height: 32px;
  padding: 0 10px;
}

.wizard-step.active {
  background: #e4f3f1;
  border-color: #0f766e;
  color: #0f5d57;
}

.wizard-step:disabled {
  color: #9aa8aa;
  cursor: not-allowed;
}

.import-preview {
  display: grid;
  gap: 10px;
}

.candidate-list {
  display: grid;
  gap: 8px;
}

.candidate-row {
  align-items: center;
  background: #f9fbfb;
  border: 1px solid #d9e4e7;
  border-radius: 8px;
  display: grid;
  gap: 10px;
  grid-template-columns: 28px 64px minmax(180px, 1fr) minmax(360px, 1.2fr) minmax(120px, 0.5fr);
  padding: 10px;
}

.candidate-main {
  display: grid;
  gap: 3px;
  min-width: 0;
}

.candidate-main strong,
.candidate-main small {
  min-width: 0;
  word-break: break-word;
}

.candidate-main small {
  color: #65777d;
  font-size: 12px;
}

.candidate-edit-grid {
  display: grid;
  gap: 6px;
  grid-template-columns: minmax(120px, 1fr) minmax(80px, 0.6fr) minmax(90px, 0.7fr) 72px 72px;
}

.commit-summary {
  align-items: center;
  background: #f6f9fa;
  border: 1px solid #d8e4e2;
  border-radius: 8px;
  color: #344a48;
  display: flex;
  flex-wrap: wrap;
  font-size: 13px;
  font-weight: 800;
  gap: 10px;
  justify-content: space-between;
  padding: 10px;
}
```

Add to the existing `@media (max-width: 980px)` block:

```css
.candidate-row,
.candidate-edit-grid {
  grid-template-columns: 1fr;
}
```

- [ ] **Step 8: Run focused wizard UI tests**

Run:

```bash
cd apps/capability-console
npm test -- --run src/App.test.ts -t 'previews Swagger candidates|disables Swagger commit'
```

Expected: PASS.

- [ ] **Step 9: Run full frontend tests and build**

Run:

```bash
cd apps/capability-console
npm test
npm run build
```

Expected: PASS.

- [ ] **Step 10: Commit Task 5**

Run:

```bash
git add apps/capability-console/src/App.vue apps/capability-console/src/App.test.ts apps/capability-console/src/styles.css
git commit -m "feat: add swagger import wizard UI"
```

---

### Task 6: Demo Docs And Final Verification

**Files:**
- Modify: `examples/README.md`

**Interfaces:**
- Consumes: preview URL `POST /v1/capabilities/import/openapi-url/preview`
- Consumes: commit URL `POST /v1/capabilities/import/openapi-url/commit`
- Produces: updated demo instructions explaining preview before draft creation.

- [ ] **Step 1: Write failing documentation check**

Run:

```bash
rg -n "预览 API|生成 Capability 草稿|不会创建草稿|preview" examples/README.md
```

Expected: FAIL/no matches for the new wizard wording.

- [ ] **Step 2: Update demo README**

In `examples/README.md`, replace the first paragraph under `## Swagger Import Test` after the URL/base URL blocks with:

```markdown
In `能力接入管理`, use `预览 API` first. Previewing the Swagger URL analyzes
candidate APIs but does not create Capability drafts yet. Recommended read
candidates are selected by default; write or risky candidates stay unselected
unless you explicitly choose them.

After reviewing the candidate list, click `生成 Capability 草稿`. Only selected
candidates become discovered drafts. The existing `本次导入` workbench then
shows the saved drafts so you can open them in the review panel, adjust output
mappings, test, publish safe reads, and run AI preflight.
```

- [ ] **Step 3: Run documentation check**

Run:

```bash
rg -n "预览 API|生成 Capability 草稿|does not create Capability drafts" examples/README.md
```

Expected: PASS with matches in `examples/README.md`.

- [ ] **Step 4: Run full frontend verification**

Run:

```bash
cd apps/capability-console
npm test
npm run build
```

Expected: PASS.

- [ ] **Step 5: Run full backend verification**

Run:

```bash
go test -count=1 ./...
go vet ./...
git diff --check
```

Expected: PASS.

- [ ] **Step 6: Browser smoke test**

With backend and frontend dev servers running, verify:

1. Open `http://127.0.0.1:5174/`.
2. Click `能力接入管理`.
3. Fill:
   - Swagger URL: `http://127.0.0.1:19090/v3/api-docs`
   - Backend Base URL: `http://127.0.0.1:19090`
4. Click `预览 API`.
5. Confirm candidate rows appear and `minio.bucket.capacity` is selected by default.
6. Confirm no new draft appears in the capability queue before commit.
7. Click `生成 Capability 草稿`.
8. Confirm saved drafts appear in the capability queue and `本次导入` workbench.

- [ ] **Step 7: Commit Task 6**

Run:

```bash
git add examples/README.md
git commit -m "docs: describe swagger import wizard demo"
```

---

## Final Review And Verification

After all tasks:

- [ ] Generate review package for the full range from the plan commit to `HEAD`.
- [ ] Run a final code review with a reviewer that checks:
  - Preview does not write drafts.
  - Commit writes only selected candidates.
  - Fingerprint mismatch returns `Swagger 文档已变化，请重新预览`.
  - Legacy import endpoint still works.
  - Vue wizard uses preview + commit and does not call legacy import for the default path.
  - Existing review/test/publish/AI preflight flows still work.
- [ ] Fix any Critical or Important findings.
- [ ] Re-run:

```bash
cd apps/capability-console
npm test
npm run build
cd ../..
go test -count=1 ./...
go vet ./...
git diff --check
git status --short
```

Expected: all commands pass. `git status --short` may show unrelated pre-existing dirty files; do not stage or revert them.
