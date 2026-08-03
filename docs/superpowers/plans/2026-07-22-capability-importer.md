# Capability Importer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a file-based Capability Importer and runtime registry path so existing middleware management APIs can be imported from OpenAPI, reviewed as YAML capabilities, loaded when published, and executed through governed Copilot tools.

**Architecture:** Add a focused `internal/capabilities` package for schema, validation, OpenAPI import, YAML loading, output normalization, and HTTP adapter execution. Extend `internal/tools` with startup-time dynamic capability registration while keeping canonical lookup, input validation, policy, audit, action-plan, and execution boundaries authoritative. Wire the API server to optionally load published capability YAML from `COPILOT_CAPABILITIES_DIR`.

**Tech Stack:** Go 1.24, `gopkg.in/yaml.v3`, standard `net/http`, existing Go tests, existing action-plan/policy/audit services.

## Global Constraints

- Existing middleware management APIs are imported as capability drafts, reviewed by humans, published into a registry, and then exposed to Copilot through governed tools.
- Newly discovered APIs must not become executable by AI automatically.
- The first version is file-based.
- There is no review UI in the first version.
- `discovered` files are importer output and are not available to Copilot.
- The runtime loader only accepts `status: published` from `capabilities/published`.
- Write capabilities are rejected unless they define action plan, approval, precheck, and rollback governance.
- The first adapter is a generic HTTP JSON adapter.
- The adapter never returns raw backend JSON directly to Copilot.
- Write capabilities are never directly executable from Copilot chat.
- The adapter can execute writes only when called by the confirmed action-plan execution path.
- Discovered capabilities are not loaded.
- Writes are never auto-published.
- Existing action plan, approval, execution, and audit boundaries remain the authority for all writes.

---

## File Structure

- Create `internal/capabilities/model.go`: capability structs, constants, normalized result structs.
- Create `internal/capabilities/validation.go`: schema validation, governance validation, path/input checks.
- Create `internal/capabilities/loader.go`: load `capabilities/published/*.yaml`, reject invalid files and duplicates.
- Create `internal/capabilities/importer.go`: import OpenAPI 3 paths into deterministic draft capabilities.
- Create `internal/capabilities/http_adapter.go`: generic HTTP JSON adapter, input validation, path building, JSON normalization, redaction.
- Create `internal/capabilities/jsonpath.go`: small dot-path extractor for mappings like `$.data.usage_pct`.
- Create `internal/capabilities/*_test.go`: focused tests for each unit.
- Modify `internal/tools/registry.go`: support startup-time registration of validated dynamic capabilities without weakening static canonical lookup.
- Modify `internal/tools/registry_test.go`: verify dynamic registration, duplicate rejection, input validation, and reset behavior.
- Modify `internal/policy/policy.go`: allow dynamic capability role permissions from capability metadata while preserving static role permissions.
- Modify `internal/policy/policy_test.go`: verify published read permissions and write governance remains confirmation-gated.
- Modify `cmd/copilot-api/main.go`: optionally load capabilities from `COPILOT_CAPABILITIES_DIR` and use an HTTP adapter-backed read runner for dynamic read tools.
- Modify `cmd/copilot-api/main_test.go`: verify loader wiring is optional and fails closed for invalid published files.
- Create `cmd/capability-importer/main.go`: CLI entry for OpenAPI import.
- Create `cmd/capability-importer/main_test.go`: CLI smoke tests with temp OpenAPI file.
- Modify `README.md`: document importer command, directory layout, publish flow, and runtime env vars.

---

### Task 1: Capability Schema And Validation

**Files:**
- Create: `internal/capabilities/model.go`
- Create: `internal/capabilities/validation.go`
- Create: `internal/capabilities/validation_test.go`

**Interfaces:**
- Produces: `capabilities.Capability`, `capabilities.Validate(Capability) error`, `capabilities.ToTool(Capability) (tools.Tool, error)`
- Consumes: `tools.Operation`, `tools.RiskLevel`

- [ ] **Step 1: Write failing validation tests**

Create `internal/capabilities/validation_test.go`:

```go
package capabilities_test

import (
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestValidateAcceptsPublishedReadCapability(t *testing.T) {
	t.Parallel()
	capability := validReadCapability()

	if err := capabilities.Validate(capability); err != nil {
		t.Fatalf("Validate returned %v", err)
	}
	tool, err := capabilities.ToTool(capability)
	if err != nil {
		t.Fatalf("ToTool returned %v", err)
	}
	if tool.Name != "minio.bucket.capacity.read" || tool.Operation != tools.Read || tool.Risk != tools.Low || tool.Domain != "minio" || tool.ResourceType != "bucket" {
		t.Fatalf("tool = %+v, want canonical read tool metadata", tool)
	}
}

func TestValidateRejectsWriteWithoutGovernance(t *testing.T) {
	t.Parallel()
	capability := validReadCapability()
	capability.Name = "kafka.topic.retention.update"
	capability.Operation = tools.Write
	capability.Risk = tools.Medium
	capability.Output = capabilities.OutputSpec{}

	err := capabilities.Validate(capability)
	if err == nil {
		t.Fatal("Validate accepted write capability without action-plan governance")
	}
}

func TestValidateRejectsPathVariableMissingInputSchema(t *testing.T) {
	t.Parallel()
	capability := validReadCapability()
	capability.Backend.Path = "/api/minio/{cluster}/{bucket}/{missing}"

	err := capabilities.Validate(capability)
	if err == nil {
		t.Fatal("Validate accepted path variable missing from input schema")
	}
}

func validReadCapability() capabilities.Capability {
	return capabilities.Capability{
		SchemaVersion: 1,
		Name:          "minio.bucket.capacity.read",
		Status:        capabilities.StatusPublished,
		Domain:        "minio",
		ResourceType:  "bucket",
		Operation:     tools.Read,
		Risk:          tools.Low,
		Backend: capabilities.BackendSpec{
			Adapter:   "http",
			Method:    "GET",
			Path:      "/api/minio/clusters/{cluster}/buckets/{bucket}/capacity",
			TimeoutMS: 3000,
		},
		InputSchema: map[string]capabilities.InputField{
			"environment": {Type: "string", Required: true},
			"cluster":     {Type: "string", Required: true},
			"bucket":      {Type: "string", Required: true},
		},
		Output: capabilities.OutputSpec{
			Kind:            "observation",
			SummaryTemplate: "Bucket {bucket} usage is {usage_pct}%",
			Fields: map[string]string{
				"usage_pct": "$.data.usage_pct",
			},
		},
		Auth: capabilities.AuthSpec{
			Roles:             []string{"viewer", "operator", "admin"},
			EnvironmentScoped: true,
		},
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/capabilities`

Expected: FAIL because `internal/capabilities` does not exist.

- [ ] **Step 3: Add model and validation**

Create `internal/capabilities/model.go`:

```go
package capabilities

import "github.com/gracegaoya/ai-operations-copilot/internal/tools"

const (
	StatusDiscovered  = "discovered"
	StatusNeedsReview = "needs_review"
	StatusPublished   = "published"
	StatusDeprecated  = "deprecated"
)

type Capability struct {
	SchemaVersion int                   `yaml:"schema_version" json:"schema_version"`
	Name          string                `yaml:"name" json:"name"`
	Status        string                `yaml:"status" json:"status"`
	Domain        string                `yaml:"domain" json:"domain"`
	ResourceType  string                `yaml:"resource_type" json:"resource_type"`
	Operation     tools.Operation       `yaml:"operation" json:"operation"`
	Risk          tools.RiskLevel       `yaml:"risk" json:"risk"`
	Backend       BackendSpec           `yaml:"backend" json:"backend"`
	InputSchema   map[string]InputField `yaml:"input_schema" json:"input_schema"`
	Output        OutputSpec            `yaml:"output" json:"output"`
	Governance    GovernanceSpec        `yaml:"governance" json:"governance"`
	Auth          AuthSpec              `yaml:"auth" json:"auth"`
	AI            AISpec                `yaml:"ai" json:"ai"`
}

type BackendSpec struct {
	Adapter   string `yaml:"adapter" json:"adapter"`
	Method    string `yaml:"method" json:"method"`
	Path      string `yaml:"path" json:"path"`
	TimeoutMS int    `yaml:"timeout_ms" json:"timeout_ms"`
	BaseURL   string `yaml:"base_url,omitempty" json:"base_url,omitempty"`
}

type InputField struct {
	Type     string `yaml:"type" json:"type"`
	Required bool   `yaml:"required" json:"required"`
}

type OutputSpec struct {
	Kind            string            `yaml:"kind" json:"kind"`
	SeverityPath    string            `yaml:"severity_path" json:"severity_path"`
	SummaryTemplate string            `yaml:"summary_template" json:"summary_template"`
	Fields          map[string]string `yaml:"fields" json:"fields"`
}

type GovernanceSpec struct {
	RequiresActionPlan bool         `yaml:"requires_action_plan" json:"requires_action_plan"`
	RequiresApproval   bool         `yaml:"requires_approval" json:"requires_approval"`
	PrecheckTools      []string     `yaml:"precheck_tools" json:"precheck_tools"`
	Rollback           RollbackSpec `yaml:"rollback" json:"rollback"`
}

type RollbackSpec struct {
	Strategy string `yaml:"strategy" json:"strategy"`
	Source   string `yaml:"source" json:"source"`
}

type AuthSpec struct {
	Roles             []string `yaml:"roles" json:"roles"`
	EnvironmentScoped bool     `yaml:"environment_scoped" json:"environment_scoped"`
}

type AISpec struct {
	Description string   `yaml:"description" json:"description"`
	Examples    []string `yaml:"examples" json:"examples"`
}

type NormalizedResult struct {
	Kind     string         `json:"kind"`
	Resource ResourceRef    `json:"resource"`
	Severity string         `json:"severity"`
	Summary  string         `json:"summary"`
	Data     map[string]any `json:"data"`
}

type ResourceRef struct {
	Domain      string `json:"domain"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Environment string `json:"environment"`
}
```

Create `internal/capabilities/validation.go`:

```go
package capabilities

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

var pathVariablePattern = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

func Validate(capability Capability) error {
	if capability.SchemaVersion != 1 {
		return errors.New("schema_version must be 1")
	}
	if strings.TrimSpace(capability.Name) == "" || strings.TrimSpace(capability.Domain) == "" || strings.TrimSpace(capability.ResourceType) == "" {
		return errors.New("name, domain, and resource_type are required")
	}
	if capability.Status != StatusDiscovered && capability.Status != StatusNeedsReview && capability.Status != StatusPublished && capability.Status != StatusDeprecated {
		return fmt.Errorf("invalid status %q", capability.Status)
	}
	if capability.Operation != tools.Read && capability.Operation != tools.Write {
		return fmt.Errorf("invalid operation %q", capability.Operation)
	}
	if capability.Risk != tools.Low && capability.Risk != tools.Medium && capability.Risk != tools.High {
		return fmt.Errorf("invalid risk %q", capability.Risk)
	}
	if strings.TrimSpace(capability.Backend.Adapter) != "http" {
		return fmt.Errorf("unsupported backend adapter %q", capability.Backend.Adapter)
	}
	if strings.TrimSpace(capability.Backend.Method) == "" || strings.TrimSpace(capability.Backend.Path) == "" {
		return errors.New("backend method and path are required")
	}
	if capability.InputSchema == nil {
		return errors.New("input_schema is required")
	}
	environment, ok := capability.InputSchema["environment"]
	if !ok || environment.Type != "string" || !environment.Required {
		return errors.New("input_schema.environment must be a required string")
	}
	if len(capability.Auth.Roles) == 0 || !capability.Auth.EnvironmentScoped {
		return errors.New("auth.roles and auth.environment_scoped are required")
	}
	for name, field := range capability.InputSchema {
		if field.Type != "string" && field.Type != "integer" && field.Type != "boolean" {
			return fmt.Errorf("input %q has unsupported type %q", name, field.Type)
		}
	}
	for _, name := range pathVariables(capability.Backend.Path) {
		if _, ok := capability.InputSchema[name]; !ok {
			return fmt.Errorf("path variable %q is missing from input_schema", name)
		}
	}
	if capability.Operation == tools.Read {
		if capability.Risk != tools.Low && capability.Risk != tools.Medium {
			return errors.New("read risk must be low or medium")
		}
		if strings.TrimSpace(capability.Output.Kind) == "" {
			return errors.New("read capability requires output.kind")
		}
		if strings.TrimSpace(capability.Output.SummaryTemplate) == "" && len(capability.Output.Fields) == 0 {
			return errors.New("read capability requires output fields or summary_template")
		}
	}
	if capability.Operation == tools.Write {
		if capability.Risk != tools.Medium && capability.Risk != tools.High {
			return errors.New("write risk must be medium or high")
		}
		if !capability.Governance.RequiresActionPlan || !capability.Governance.RequiresApproval {
			return errors.New("write capability requires action plan and approval governance")
		}
		if len(capability.Governance.PrecheckTools) == 0 {
			return errors.New("write capability requires precheck_tools")
		}
		if strings.TrimSpace(capability.Governance.Rollback.Strategy) == "" {
			return errors.New("write capability requires rollback strategy")
		}
	}
	return nil
}

func ToTool(capability Capability) (tools.Tool, error) {
	if err := Validate(capability); err != nil {
		return tools.Tool{}, err
	}
	rollback := ""
	if capability.Operation == tools.Write {
		rollback = "Rollback through capability strategy: " + capability.Governance.Rollback.Strategy
	}
	return tools.Tool{Name: capability.Name, Operation: capability.Operation, Risk: capability.Risk, RollbackDescription: rollback, Domain: capability.Domain, ResourceType: capability.ResourceType}, nil
}

func pathVariables(path string) []string {
	matches := pathVariablePattern.FindAllStringSubmatch(path, -1)
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		values = append(values, match[1])
	}
	return values
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/capabilities`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/capabilities/model.go internal/capabilities/validation.go internal/capabilities/validation_test.go
git commit -m "feat: add capability schema validation"
```

---

### Task 2: Published Capability Loader

**Files:**
- Create: `internal/capabilities/loader.go`
- Create: `internal/capabilities/loader_test.go`

**Interfaces:**
- Consumes: `capabilities.Capability`, `capabilities.Validate`
- Produces: `capabilities.LoadPublished(dir string) ([]Capability, error)`

- [ ] **Step 1: Write failing loader tests**

Create `internal/capabilities/loader_test.go`:

```go
package capabilities_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
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
  method: GET
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
  summary_template: "Bucket {bucket} usage is {usage_pct}%"
  fields:
    usage_pct: $.data.usage_pct
auth:
  roles: [viewer, operator, admin]
  environment_scoped: true
`
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/capabilities -run LoadPublished`

Expected: FAIL because `LoadPublished` is missing.

- [ ] **Step 3: Add loader**

Create `internal/capabilities/loader.go`:

```go
package capabilities

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

func LoadPublished(root string) ([]Capability, error) {
	pattern := filepath.Join(root, "published", "*.yaml")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	seen := map[string]string{}
	capabilities := make([]Capability, 0, len(paths))
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read capability %s: %w", path, err)
		}
		var capability Capability
		if err := yaml.Unmarshal(body, &capability); err != nil {
			return nil, fmt.Errorf("parse capability %s: %w", path, err)
		}
		if capability.Status != StatusPublished {
			return nil, fmt.Errorf("published file %s has status %q", path, capability.Status)
		}
		if err := Validate(capability); err != nil {
			return nil, fmt.Errorf("validate capability %s: %w", path, err)
		}
		if previous, ok := seen[capability.Name]; ok {
			return nil, fmt.Errorf("duplicate capability %q in %s and %s", capability.Name, previous, path)
		}
		seen[capability.Name] = path
		capabilities = append(capabilities, capability)
	}
	return capabilities, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/capabilities -run LoadPublished`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/capabilities/loader.go internal/capabilities/loader_test.go
git commit -m "feat: load published capabilities"
```

---

### Task 3: OpenAPI Importer

**Files:**
- Create: `internal/capabilities/importer.go`
- Create: `internal/capabilities/importer_test.go`
- Create: `cmd/capability-importer/main.go`
- Create: `cmd/capability-importer/main_test.go`

**Interfaces:**
- Produces: `capabilities.ImportOpenAPI(body []byte) ([]Capability, error)`, `capabilities.WriteDrafts(outputDir string, drafts []Capability) error`

- [ ] **Step 1: Write failing importer test**

Create `internal/capabilities/importer_test.go`:

```go
package capabilities_test

import (
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestImportOpenAPIGeneratesReadAndWriteDrafts(t *testing.T) {
	t.Parallel()
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
	if drafts[0].Status != capabilities.StatusNeedsReview || drafts[0].Operation != tools.Read || drafts[0].Domain != "minio" || drafts[0].ResourceType != "bucket" {
		t.Fatalf("first draft = %+v, want minio bucket read draft", drafts[0])
	}
	if drafts[1].Status != capabilities.StatusNeedsReview || drafts[1].Operation != tools.Write || drafts[1].Domain != "kafka" || drafts[1].ResourceType != "topic" || drafts[1].Governance.RequiresActionPlan {
		t.Fatalf("second draft = %+v, want kafka topic write draft needing review without auto-governance", drafts[1])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/capabilities -run ImportOpenAPI`

Expected: FAIL because importer functions are missing.

- [ ] **Step 3: Add importer**

Create `internal/capabilities/importer.go` with these concrete functions:

```go
package capabilities

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
	"gopkg.in/yaml.v3"
)

type openAPIDoc struct {
	Paths map[string]map[string]openAPIOperation `yaml:"paths"`
}

type openAPIOperation struct {
	Tags       []string           `yaml:"tags"`
	Summary    string             `yaml:"summary"`
	Parameters []openAPIParameter `yaml:"parameters"`
}

type openAPIParameter struct {
	Name     string        `yaml:"name"`
	Required bool          `yaml:"required"`
	Schema   openAPISchema `yaml:"schema"`
}

type openAPISchema struct {
	Type string `yaml:"type"`
}

func ImportOpenAPI(body []byte) ([]Capability, error) {
	var doc openAPIDoc
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(doc.Paths))
	for path := range doc.Paths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	drafts := []Capability{}
	for _, path := range paths {
		methods := make([]string, 0, len(doc.Paths[path]))
		for method := range doc.Paths[path] {
			methods = append(methods, method)
		}
		sort.Strings(methods)
		for _, method := range methods {
			operation := doc.Paths[path][method]
			drafts = append(drafts, inferCapability(strings.ToUpper(method), path, operation))
		}
	}
	return drafts, nil
}

func WriteDrafts(outputDir string, drafts []Capability) error {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}
	for _, draft := range drafts {
		body, err := yaml.Marshal(draft)
		if err != nil {
			return err
		}
		path := filepath.Join(outputDir, draft.Name+".yaml")
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func inferCapability(method, path string, operation openAPIOperation) Capability {
	text := strings.ToLower(path + " " + strings.Join(operation.Tags, " ") + " " + operation.Summary)
	domain := inferDomain(text)
	resourceType := inferResourceType(text)
	toolOperation := inferOperation(method)
	name := inferName(domain, resourceType, text, toolOperation)
	input := map[string]InputField{"environment": {Type: "string", Required: true}}
	for _, parameter := range operation.Parameters {
		fieldType := parameter.Schema.Type
		if fieldType == "" {
			fieldType = "string"
		}
		input[parameter.Name] = InputField{Type: normalizeSchemaType(fieldType), Required: parameter.Required}
	}
	capability := Capability{
		SchemaVersion: 1,
		Name:          name,
		Status:        StatusNeedsReview,
		Domain:        domain,
		ResourceType:  resourceType,
		Operation:     toolOperation,
		Risk:          inferRisk(text, toolOperation),
		Backend:       BackendSpec{Adapter: "http", Method: method, Path: path, TimeoutMS: 3000},
		InputSchema:   input,
		Auth:          AuthSpec{Roles: []string{"viewer", "operator", "admin"}, EnvironmentScoped: true},
		AI:            AISpec{Description: operation.Summary},
	}
	if toolOperation == tools.Read {
		capability.Output = OutputSpec{Kind: "observation", SummaryTemplate: operation.Summary, Fields: map[string]string{"status": "$.status"}}
	}
	return capability
}

func inferOperation(method string) tools.Operation {
	if method == "GET" || method == "HEAD" {
		return tools.Read
	}
	return tools.Write
}

func inferRisk(text string, operation tools.Operation) tools.RiskLevel {
	if regexp.MustCompile(`delete|drop|force|truncate|purge|format|remove`).MatchString(text) {
		return tools.High
	}
	if regexp.MustCompile(`restart|rebalance|heal|quota|retention|lifecycle`).MatchString(text) {
		return tools.Medium
	}
	if operation == tools.Read {
		return tools.Low
	}
	return tools.Medium
}

func inferDomain(text string) string {
	for _, domain := range []string{"minio", "glusterfs", "kafka"} {
		if strings.Contains(text, domain) || (domain == "glusterfs" && strings.Contains(text, "gluster")) {
			return domain
		}
	}
	return "unknown"
}

func inferResourceType(text string) string {
	for _, resource := range []string{"bucket", "volume", "topic", "broker"} {
		if strings.Contains(text, resource) {
			return resource
		}
	}
	if strings.Contains(text, "consumer-group") || strings.Contains(text, "consumer_group") || strings.Contains(text, "consumer") {
		return "consumer_group"
	}
	return "resource"
}

func inferName(domain, resourceType, text string, operation tools.Operation) string {
	leaf := "resource"
	for _, candidate := range []string{"capacity", "health", "status", "lag", "retention", "quota", "lifecycle"} {
		if strings.Contains(text, candidate) {
			leaf = candidate
			break
		}
	}
	suffix := "read"
	if operation == tools.Write {
		suffix = "update"
	}
	return fmt.Sprintf("%s.%s.%s.%s", domain, resourceType, leaf, suffix)
}

func normalizeSchemaType(value string) string {
	switch value {
	case "integer", "number":
		return "integer"
	case "boolean":
		return "boolean"
	default:
		return "string"
	}
}
```

- [ ] **Step 4: Add CLI**

Create `cmd/capability-importer/main.go`:

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
)

func main() {
	if err := run(os.Args); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 5 || args[1] != "import" || args[2] != "openapi" {
		return fmt.Errorf("usage: %s import openapi <openapi.yaml> <output-dir>", filepath.Base(args[0]))
	}
	body, err := os.ReadFile(args[3])
	if err != nil {
		return err
	}
	drafts, err := capabilities.ImportOpenAPI(body)
	if err != nil {
		return err
	}
	return capabilities.WriteDrafts(args[4], drafts)
}
```

Create `cmd/capability-importer/main_test.go`:

```go
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
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/capabilities ./cmd/capability-importer`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/capabilities/importer.go internal/capabilities/importer_test.go cmd/capability-importer/main.go cmd/capability-importer/main_test.go
git commit -m "feat: import capabilities from openapi"
```

---

### Task 4: Dynamic Tool Registry And Policy Metadata

**Files:**
- Modify: `internal/tools/registry.go`
- Modify: `internal/tools/registry_test.go`
- Modify: `internal/policy/policy.go`
- Modify: `internal/policy/policy_test.go`

**Interfaces:**
- Consumes: `capabilities.ToTool`
- Produces: `tools.RegisterDynamicTools([]tools.DynamicToolDefinition) error`, `tools.ResetDynamicToolsForTest()`, `policy.RegisterDynamicRolePermissions(map[string][]string)`

- [ ] **Step 1: Write failing tools tests**

Append to `internal/tools/registry_test.go`:

```go
func TestRegisterDynamicToolsAddsCanonicalLookupAndValidation(t *testing.T) {
	ResetDynamicToolsForTest()
	t.Cleanup(ResetDynamicToolsForTest)

	err := RegisterDynamicTools([]DynamicToolDefinition{{
		Tool: Tool{Name: "minio.bucket.capacity.read", Operation: Read, Risk: Low, Domain: "minio", ResourceType: "bucket"},
		InputSchema: map[string]DynamicInputField{
			"environment": {Type: "string", Required: true},
			"cluster":     {Type: "string", Required: true},
			"bucket":      {Type: "string", Required: true},
		},
	}})
	if err != nil {
		t.Fatalf("RegisterDynamicTools returned %v", err)
	}
	tool, ok := Lookup("minio.bucket.capacity.read")
	if !ok || tool.Domain != "minio" {
		t.Fatalf("Lookup dynamic tool = %+v, %v", tool, ok)
	}
	if err := ValidateInput(tool, map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive"}); err != nil {
		t.Fatalf("ValidateInput dynamic returned %v", err)
	}
	if err := ValidateInput(tool, map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive", "extra": true}); err == nil {
		t.Fatal("ValidateInput accepted unknown dynamic input")
	}
}
```

- [ ] **Step 2: Add dynamic registry support**

Modify `internal/tools/registry.go`:

```go
var dynamicTools = map[string]Tool{}
var dynamicInputs = map[string]map[string]DynamicInputField{}

type DynamicToolDefinition struct {
	Tool        Tool
	InputSchema map[string]DynamicInputField
}

type DynamicInputField struct {
	Type     string
	Required bool
}

func RegisterDynamicTools(definitions []DynamicToolDefinition) error {
	for _, definition := range definitions {
		tool := definition.Tool
		if _, exists := registeredTools[tool.Name]; exists {
			return fmt.Errorf("dynamic tool %q conflicts with static tool", tool.Name)
		}
		if _, exists := dynamicTools[tool.Name]; exists {
			return fmt.Errorf("dynamic tool %q is already registered", tool.Name)
		}
		if err := validateToolDefinition(tool); err != nil {
			return err
		}
		if len(definition.InputSchema) == 0 {
			return fmt.Errorf("dynamic tool %q requires input fields", tool.Name)
		}
		dynamicTools[tool.Name] = tool
		dynamicInputs[tool.Name] = cloneDynamicInputSchema(definition.InputSchema)
	}
	return nil
}

func cloneDynamicInputSchema(input map[string]DynamicInputField) map[string]DynamicInputField {
	clone := make(map[string]DynamicInputField, len(input))
	for name, field := range input {
		clone[name] = field
	}
	return clone
}

func ResetDynamicToolsForTest() {
	dynamicTools = map[string]Tool{}
	dynamicInputs = map[string]map[string]DynamicInputField{}
}
```

Update `Lookup`, `All`, and `ValidateInput` so dynamic tools are included. For `ValidateInput`, after the static switch default, check `dynamicInputs[tool.Name]`, reject unknown inputs, require every `Required` field, and validate `string`, `integer`, `number`, and `boolean` values. Keep this type local to `internal/tools` to avoid an import cycle from `tools` back into `capabilities`.

- [ ] **Step 3: Add policy tests and role metadata support**

Append to `internal/policy/policy_test.go`:

```go
func TestEvaluateAllowsDynamicReadForRegisteredRole(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	if err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
		Tool: tools.Tool{Name: "minio.bucket.capacity.read", Operation: tools.Read, Risk: tools.Low, Domain: "minio", ResourceType: "bucket"},
		InputSchema: map[string]tools.DynamicInputField{
			"environment": {Type: "string", Required: true},
			"cluster":     {Type: "string", Required: true},
			"bucket":      {Type: "string", Required: true},
		},
	}}); err != nil {
		t.Fatalf("register dynamic: %v", err)
	}
	RegisterDynamicRolePermissions(map[string][]string{"minio.bucket.capacity.read": {"viewer"}})
	t.Cleanup(ResetDynamicRolePermissionsForTest)

	d := Evaluate(user("viewer", "prod"), registeredTool(t, "minio.bucket.capacity.read"), map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive"})
	if !d.Allowed || d.RequiresConfirmation {
		t.Fatalf("dynamic read decision = %+v, want allowed read", d)
	}
}
```

Modify `internal/policy/policy.go` with startup-time dynamic role permissions:

```go
var dynamicRolePermissions = map[string]map[string]struct{}{}

func RegisterDynamicRolePermissions(toolRoles map[string][]string) {
	for tool, roles := range toolRoles {
		for _, role := range roles {
			if dynamicRolePermissions[role] == nil {
				dynamicRolePermissions[role] = map[string]struct{}{}
			}
			dynamicRolePermissions[role][tool] = struct{}{}
		}
	}
}

func ResetDynamicRolePermissionsForTest() {
	dynamicRolePermissions = map[string]map[string]struct{}{}
}
```

Update `hasToolPermission` to check static and dynamic permissions.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/tools ./internal/policy`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tools/registry.go internal/tools/registry_test.go internal/policy/policy.go internal/policy/policy_test.go
git commit -m "feat: register dynamic capability tools"
```

---

### Task 5: HTTP Adapter And Output Normalization

**Files:**
- Create: `internal/capabilities/jsonpath.go`
- Create: `internal/capabilities/http_adapter.go`
- Create: `internal/capabilities/http_adapter_test.go`

**Interfaces:**
- Produces: `capabilities.HTTPAdapter.Execute(ctx, capability, input) (NormalizedResult, error)`

- [ ] **Step 1: Write failing HTTP adapter test**

Create `internal/capabilities/http_adapter_test.go`:

```go
package capabilities_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
)

func TestHTTPAdapterBuildsRequestAndNormalizesOutput(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/minio/clusters/m1/buckets/archive/capacity" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"usage_pct": 86, "secret_token": "hide-me"}})
	}))
	defer server.Close()
	capability := validReadCapability()
	capability.Backend.BaseURL = server.URL
	adapter := capabilities.NewHTTPAdapter(http.DefaultClient)

	result, err := adapter.Execute(context.Background(), capability, map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive"})
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if result.Kind != "observation" || result.Resource.Name != "archive" || result.Data["usage_pct"] != float64(86) {
		t.Fatalf("result = %+v, want normalized observation", result)
	}
	if _, ok := result.Data["secret_token"]; ok {
		t.Fatalf("result leaked redacted field: %+v", result.Data)
	}
}
```

- [ ] **Step 2: Add JSON path extractor**

Create `internal/capabilities/jsonpath.go`:

```go
package capabilities

import (
	"fmt"
	"strings"
)

func extractPath(value any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "$" {
		return value, true
	}
	if !strings.HasPrefix(path, "$.") {
		return nil, false
	}
	current := value
	for _, part := range strings.Split(strings.TrimPrefix(path, "$."), ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func renderSummary(template string, input map[string]any, fields map[string]any) string {
	summary := template
	for key, value := range input {
		summary = strings.ReplaceAll(summary, "{"+key+"}", fmt.Sprint(value))
	}
	for key, value := range fields {
		summary = strings.ReplaceAll(summary, "{"+key+"}", fmt.Sprint(value))
	}
	return summary
}
```

- [ ] **Step 3: Add HTTP adapter**

Create `internal/capabilities/http_adapter.go`:

```go
package capabilities

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HTTPAdapter struct{ client *http.Client }

func NewHTTPAdapter(client *http.Client) *HTTPAdapter {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPAdapter{client: client}
}

func (a *HTTPAdapter) Execute(ctx context.Context, capability Capability, input map[string]any) (NormalizedResult, error) {
	if err := Validate(capability); err != nil {
		return NormalizedResult{}, err
	}
	if err := validateAdapterInput(capability, input); err != nil {
		return NormalizedResult{}, err
	}
	timeout := time.Duration(capability.Backend.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	requestContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	path, err := buildPath(capability.Backend.Path, input)
	if err != nil {
		return NormalizedResult{}, err
	}
	endpoint := strings.TrimRight(capability.Backend.BaseURL, "/") + path
	request, err := http.NewRequestWithContext(requestContext, capability.Backend.Method, endpoint, nil)
	if err != nil {
		return NormalizedResult{}, err
	}
	response, err := a.client.Do(request)
	if err != nil {
		return NormalizedResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return NormalizedResult{}, fmt.Errorf("backend returned HTTP %d", response.StatusCode)
	}
	var raw map[string]any
	limited := io.LimitReader(response.Body, 10*1024+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return NormalizedResult{}, err
	}
	if len(payload) > 10*1024 {
		return NormalizedResult{}, fmt.Errorf("backend response exceeds 10240 bytes")
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return NormalizedResult{}, err
	}
	fields := map[string]any{}
	for name, path := range capability.Output.Fields {
		if isSensitive(name) {
			continue
		}
		if value, ok := extractPath(raw, path); ok {
			fields[name] = value
		}
	}
	severity := "info"
	if capability.Output.SeverityPath != "" {
		if value, ok := extractPath(raw, capability.Output.SeverityPath); ok {
			severity = fmt.Sprint(value)
		}
	}
	name := firstString(input, "bucket", "topic", "volume", "name", "cluster")
	return NormalizedResult{Kind: capability.Output.Kind, Resource: ResourceRef{Domain: capability.Domain, Type: capability.ResourceType, Name: name, Environment: fmt.Sprint(input["environment"])}, Severity: severity, Summary: renderSummary(capability.Output.SummaryTemplate, input, fields), Data: fields}, nil
}

func validateAdapterInput(capability Capability, input map[string]any) error {
	for name, field := range capability.InputSchema {
		value, ok := input[name]
		if field.Required && !ok {
			return fmt.Errorf("missing required input %q", name)
		}
		if ok && field.Type == "string" {
			if text, ok := value.(string); !ok || strings.TrimSpace(text) == "" {
				return fmt.Errorf("input %q must be a non-empty string", name)
			}
		}
	}
	for name := range input {
		if _, ok := capability.InputSchema[name]; !ok {
			return fmt.Errorf("input %q is not allowed", name)
		}
	}
	return nil
}

func buildPath(path string, input map[string]any) (string, error) {
	for _, name := range pathVariables(path) {
		value := fmt.Sprint(input[name])
		if value == "" {
			return "", fmt.Errorf("missing path value %q", name)
		}
		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(value))
	}
	return path, nil
}

func firstString(input map[string]any, names ...string) string {
	for _, name := range names {
		if value, ok := input[name].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func isSensitive(name string) bool {
	lower := strings.ToLower(name)
	for _, marker := range []string{"password", "secret", "token", "key", "credential", "authorization"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/capabilities`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/capabilities/jsonpath.go internal/capabilities/http_adapter.go internal/capabilities/http_adapter_test.go
git commit -m "feat: execute capability http adapter"
```

---

### Task 6: Runtime Wiring For Published Read Capabilities

**Files:**
- Modify: `cmd/copilot-api/main.go`
- Modify: `cmd/copilot-api/main_test.go`
- Create: `internal/capabilities/runtime.go`
- Create: `internal/capabilities/runtime_test.go`

**Interfaces:**
- Produces: `capabilities.RegisterPublished(root string) ([]Capability, error)`, `capabilities.ReadRunner`

- [ ] **Step 1: Add runtime registration**

Create `internal/capabilities/runtime.go`:

```go
package capabilities

import (
	"context"

	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func RegisterPublished(root string) ([]Capability, error) {
	loaded, err := LoadPublished(root)
	if err != nil {
		return nil, err
	}
	definitions := make([]tools.DynamicToolDefinition, 0, len(loaded))
	roles := map[string][]string{}
	for _, capability := range loaded {
		tool, err := ToTool(capability)
		if err != nil {
			return nil, err
		}
		schema := make(map[string]tools.DynamicInputField, len(capability.InputSchema))
		for name, field := range capability.InputSchema {
			schema[name] = tools.DynamicInputField{Type: field.Type, Required: field.Required}
		}
		definitions = append(definitions, tools.DynamicToolDefinition{Tool: tool, InputSchema: schema})
		roles[capability.Name] = append([]string(nil), capability.Auth.Roles...)
	}
	if err := tools.RegisterDynamicTools(definitions); err != nil {
		return nil, err
	}
	policy.RegisterDynamicRolePermissions(roles)
	return loaded, nil
}

type CapabilityReadRunner struct {
	next         interface{ Read(context.Context, tools.Tool, map[string]any) (map[string]any, error) }
	capabilities map[string]Capability
	adapter      *HTTPAdapter
}

func NewCapabilityReadRunner(next interface{ Read(context.Context, tools.Tool, map[string]any) (map[string]any, error) }, loaded []Capability, adapter *HTTPAdapter) *CapabilityReadRunner {
	byName := map[string]Capability{}
	for _, capability := range loaded {
		byName[capability.Name] = capability
	}
	return &CapabilityReadRunner{next: next, capabilities: byName, adapter: adapter}
}

func (r *CapabilityReadRunner) Read(ctx context.Context, tool tools.Tool, input map[string]any) (map[string]any, error) {
	if capability, ok := r.capabilities[tool.Name]; ok {
		result, err := r.adapter.Execute(ctx, capability, input)
		if err != nil {
			return nil, err
		}
		return map[string]any{"kind": result.Kind, "resource": result.Resource, "severity": result.Severity, "summary": result.Summary, "data": result.Data}, nil
	}
	return r.next.Read(ctx, tool, input)
}
```

Add tests in `internal/capabilities/runtime_test.go` verifying a published read capability registers into `tools.Lookup` and `CapabilityReadRunner` delegates static tools to `next`.

- [ ] **Step 2: Wire API startup**

Modify `cmd/copilot-api/main.go`:

```go
loadedCapabilities := []capabilities.Capability{}
if dir := os.Getenv("COPILOT_CAPABILITIES_DIR"); dir != "" {
    loadedCapabilities, err = capabilities.RegisterPublished(dir)
    if err != nil {
        log.Fatalf("load published capabilities: %v", err)
    }
    log.Printf("loaded %d published capabilities", len(loadedCapabilities))
}
baseRunner := staticReadRunner{}
readRunner := execution.ReadRunner(baseRunner)
if len(loadedCapabilities) > 0 {
    readRunner = capabilities.NewCapabilityReadRunner(baseRunner, loadedCapabilities, capabilities.NewHTTPAdapter(http.DefaultClient))
}
readService := execution.NewReadOnlyService(readRunner, auditService)
```

Add import:

```go
"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/capabilities ./cmd/copilot-api`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/capabilities/runtime.go internal/capabilities/runtime_test.go cmd/copilot-api/main.go cmd/copilot-api/main_test.go
git commit -m "feat: wire published capability runtime"
```

---

### Task 7: End-to-End Capability Loader And Documentation

**Files:**
- Create: `tests/e2e/capabilities_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: capability loader, dynamic tools, read-only service, HTTP adapter

- [ ] **Step 1: Add e2e test**

Create `tests/e2e/capabilities_test.go`:

```go
package e2e

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
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

func TestPublishedCapabilityExecutesThroughReadOnlyPath(t *testing.T) {
	t.Parallel()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"usage_pct":86}}`))
	}))
	defer backend.Close()
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
	if result["kind"] != "observation" {
		t.Fatalf("result = %+v, want normalized observation", result)
	}
	events := repository.AuditEvents()
	if len(events) != 1 || events[0].Action != "readonly_tool_executed" {
		t.Fatalf("events = %+v, want read audit", events)
	}
}

type staticRunner struct{}

func (staticRunner) Read(context.Context, tools.Tool, map[string]any) (map[string]any, error) {
	return map[string]any{"fallback": true}, nil
}
```

Add any missing imports that the compiler reports, especially `internal/tools`.

- [ ] **Step 2: Update README**

Add:

```markdown
## Capability Importer

Import OpenAPI paths into reviewed capability drafts:

```bash
go run ./cmd/capability-importer import openapi ./middleware-openapi.yaml ./capabilities/discovered
```

Only reviewed files with `status: published` under `capabilities/published`
are loaded at runtime:

```bash
COPILOT_CAPABILITIES_DIR='./capabilities' go run ./cmd/copilot-api
```

Discovered capabilities are never available to Copilot. Published write
capabilities must define action-plan, approval, precheck, and rollback
governance or the loader rejects them.
```

- [ ] **Step 3: Run full verification**

Run:

```bash
go test ./...
go vet ./...
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add tests/e2e/capabilities_test.go README.md
git commit -m "docs: document capability importer runtime"
```

---

## Self-Review Notes

- Spec coverage: Tasks 1-2 cover schema and loader. Task 3 covers OpenAPI import and draft generation. Task 4 covers governed runtime tool registration and policy metadata. Task 5 covers generic HTTP adapter and output normalization. Task 6 wires published read capabilities into the existing read-only path. Task 7 covers e2e and documentation.
- Scope control: The plan does not add a graphical review page, live gateway discovery, OAuth UI, non-HTTP adapters, automatic write publishing, raw backend API access, or replacement of action plan/approval/execution/audit.
- Type consistency: `Capability`, `BackendSpec`, `InputField`, `OutputSpec`, `GovernanceSpec`, `AuthSpec`, and `NormalizedResult` are defined in Task 1 and reused by later tasks.
- Risk note: Dynamic registry mutates package-level tool state at startup. Tests must call reset helpers, and production must register capabilities before request handling begins.
