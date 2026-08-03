# Middleware Ops Workbench Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a chat-first middleware operations workbench that returns structured diagnostics for GlusterFS, MinIO, and Kafka while keeping every write behind action plans, approval, execution, and audit.

**Architecture:** Add a small `internal/diagnostics` model and service beside the existing `assistant`, `tools`, and `execution` packages. Extend the static tool registry with domain metadata and read-only sample tools, then let `assistant.Service` attach diagnostic packages to chat responses without weakening policy. Update the React console to render diagnostics through domain-neutral components next to the existing pending-plan workflow.

**Tech Stack:** Go 1.x, standard `net/http`, existing Go test suite, React, TypeScript, Vite, Vitest, Testing Library.

## Global Constraints

- The primary experience is Chat-first Middleware Ops Workbench.
- Supported rollout order is GlusterFS, then MinIO, then Kafka.
- Copilot output remains untrusted candidate data.
- Server-side policy remains authoritative for role, environment, tool, input, and risk decisions.
- All write operations must use immutable action plans, approval, execution records, and audit.
- Domain registry is server-side only.
- Do not add WebSocket, SSE, external approval connectors, full CMDB, or full alert lifecycle.
- Keep diagnostic responses under the existing 10 KB capped JSON response limit.
- Preserve existing assistant response behavior for clients that ignore the new `diagnostic` field.

---

## File Structure

- Create `internal/diagnostics/model.go`: shared diagnostic types, severity validation, and package helpers.
- Create `internal/diagnostics/service.go`: runbook execution that uses the existing read-only service boundary.
- Create `internal/diagnostics/service_test.go`: diagnostic package and safety tests.
- Modify `internal/tools/registry.go`: add domain metadata fields and sample read-only domain tools.
- Modify `internal/tools/registry_test.go`: verify static registry, read/write classification, and input schemas.
- Modify `internal/assistant/planner.go`: detect domain diagnostic requests and return a diagnostic intent.
- Modify `internal/assistant/service.go`: run diagnostics when the planner returns a diagnostic intent.
- Modify `internal/assistant/service_test.go`: cover diagnostic responses and write safety.
- Modify `internal/httpapi/router_test.go`: verify assistant JSON includes `diagnostic` and does not expose token fields.
- Modify `cmd/copilot-api/main.go`: make the static read runner return deterministic sample domain data.
- Create `apps/console/src/types.ts`: TypeScript response and diagnostic types.
- Create `apps/console/src/DiagnosticView.tsx`: domain-neutral diagnostic renderer.
- Modify `apps/console/src/App.tsx`: consume the new types and render `DiagnosticView`.
- Modify `apps/console/src/App.test.tsx`: cover diagnostic rendering and legacy responses.
- Modify `apps/console/src/styles.css`: add compact diagnostic workbench styles.
- Modify `README.md`: document the new chat-first diagnostic flow and sample prompts.

---

### Task 1: Diagnostic Model

**Files:**
- Create: `internal/diagnostics/model.go`
- Test: `internal/diagnostics/model_test.go`

**Interfaces:**
- Produces: `diagnostics.ResourceRef`, `diagnostics.Observation`, `diagnostics.Finding`, `diagnostics.Recommendation`, `diagnostics.Package`, `diagnostics.Request`, `diagnostics.ValidatePackage(pkg Package) error`
- Consumes: `tools.RiskLevel`

- [ ] **Step 1: Write failing model tests**

Create `internal/diagnostics/model_test.go`:

```go
package diagnostics_test

import (
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestValidatePackageAcceptsStructuredDiagnostic(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 22, 9, 0, 0, 0, time.UTC)
	pkg := diagnostics.Package{
		ID:          "diag-1",
		Environment: "prod",
		Domains:     []string{"glusterfs"},
		Resources: []diagnostics.ResourceRef{{
			Domain: "glusterfs", Type: "volume", ID: "vol-prod-data", Name: "prod-data", Environment: "prod",
		}},
		Observations: []diagnostics.Observation{{
			ID: "obs-1", ResourceID: "vol-prod-data", Kind: "glusterfs.volume.health", Severity: diagnostics.SeverityWarning, Summary: "heal backlog is present", Data: map[string]any{"heal_pending": 12}, CollectedAt: now,
		}},
		Findings: []diagnostics.Finding{{
			ID: "finding-1", Severity: diagnostics.SeverityWarning, Summary: "volume needs heal review", EvidenceIDs: []string{"obs-1"}, Confidence: diagnostics.ConfidenceMedium,
		}},
		Recommendations: []diagnostics.Recommendation{{
			ID: "rec-1", Summary: "review heal status", Rationale: "pending entries are above zero", Risk: tools.Low, Actionable: false,
		}},
		CreatedAt: now,
	}

	if err := diagnostics.ValidatePackage(pkg); err != nil {
		t.Fatalf("ValidatePackage returned %v", err)
	}
}

func TestValidatePackageRejectsUnknownEvidenceReference(t *testing.T) {
	t.Parallel()
	pkg := diagnostics.Package{
		ID:          "diag-1",
		Environment: "prod",
		Domains:     []string{"glusterfs"},
		Resources:   []diagnostics.ResourceRef{{Domain: "glusterfs", Type: "volume", ID: "vol-prod-data", Name: "prod-data", Environment: "prod"}},
		Observations: []diagnostics.Observation{{
			ID: "obs-1", ResourceID: "vol-prod-data", Kind: "glusterfs.volume.health", Severity: diagnostics.SeverityOK, Summary: "healthy", CollectedAt: time.Now().UTC(),
		}},
		Findings: []diagnostics.Finding{{ID: "finding-1", Severity: diagnostics.SeverityWarning, Summary: "bad reference", EvidenceIDs: []string{"missing-observation"}, Confidence: diagnostics.ConfidenceLow}},
		CreatedAt: time.Now().UTC(),
	}

	if err := diagnostics.ValidatePackage(pkg); err == nil {
		t.Fatal("ValidatePackage accepted a finding with missing evidence")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/diagnostics`

Expected: FAIL because `internal/diagnostics` does not exist.

- [ ] **Step 3: Add diagnostic model**

Create `internal/diagnostics/model.go`:

```go
package diagnostics

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

type Severity string

const (
	SeverityOK       Severity = "ok"
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

type Confidence string

const (
	ConfidenceLow    Confidence = "low"
	ConfidenceMedium Confidence = "medium"
	ConfidenceHigh   Confidence = "high"
)

type Request struct {
	Domain       string
	Environment  string
	ResourceType string
	ResourceName string
	Runbook      string
}

type ResourceRef struct {
	Domain      string            `json:"domain"`
	Type        string            `json:"type"`
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Environment string            `json:"environment"`
	Labels      map[string]string `json:"labels,omitempty"`
}

type Observation struct {
	ID          string         `json:"id"`
	ResourceID  string         `json:"resource_id"`
	Kind        string         `json:"kind"`
	Severity    Severity       `json:"severity"`
	Summary     string         `json:"summary"`
	Data        map[string]any `json:"data,omitempty"`
	CollectedAt time.Time      `json:"collected_at"`
}

type Finding struct {
	ID          string     `json:"id"`
	Severity    Severity   `json:"severity"`
	Summary     string     `json:"summary"`
	EvidenceIDs []string   `json:"evidence_ids"`
	Confidence  Confidence `json:"confidence"`
}

type Recommendation struct {
	ID             string          `json:"id"`
	Summary        string          `json:"summary"`
	Rationale      string          `json:"rationale"`
	Risk           tools.RiskLevel `json:"risk"`
	Actionable     bool            `json:"actionable"`
	ToolName       string          `json:"tool_name,omitempty"`
	CandidateInput map[string]any  `json:"candidate_input,omitempty"`
}

type Package struct {
	ID              string           `json:"id"`
	Environment     string           `json:"environment"`
	Domains         []string         `json:"domains"`
	Resources       []ResourceRef    `json:"resources"`
	Observations    []Observation    `json:"observations"`
	Findings        []Finding        `json:"findings"`
	Recommendations []Recommendation `json:"recommendations"`
	PlanIDs         []string         `json:"plan_ids,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
}

func ValidatePackage(pkg Package) error {
	if strings.TrimSpace(pkg.ID) == "" {
		return errors.New("diagnostic id is required")
	}
	if strings.TrimSpace(pkg.Environment) == "" {
		return errors.New("diagnostic environment is required")
	}
	if len(pkg.Domains) == 0 {
		return errors.New("at least one domain is required")
	}
	resources := map[string]struct{}{}
	for _, resource := range pkg.Resources {
		if strings.TrimSpace(resource.ID) == "" || strings.TrimSpace(resource.Domain) == "" || strings.TrimSpace(resource.Environment) == "" {
			return errors.New("resource id, domain, and environment are required")
		}
		if resource.Environment != pkg.Environment {
			return fmt.Errorf("resource %q environment %q does not match diagnostic environment %q", resource.ID, resource.Environment, pkg.Environment)
		}
		resources[resource.ID] = struct{}{}
	}
	observations := map[string]struct{}{}
	for _, observation := range pkg.Observations {
		if strings.TrimSpace(observation.ID) == "" || strings.TrimSpace(observation.Kind) == "" || strings.TrimSpace(observation.Summary) == "" {
			return errors.New("observation id, kind, and summary are required")
		}
		if _, ok := resources[observation.ResourceID]; !ok {
			return fmt.Errorf("observation %q references unknown resource %q", observation.ID, observation.ResourceID)
		}
		if !validSeverity(observation.Severity) {
			return fmt.Errorf("observation %q has invalid severity %q", observation.ID, observation.Severity)
		}
		observations[observation.ID] = struct{}{}
	}
	for _, finding := range pkg.Findings {
		if strings.TrimSpace(finding.ID) == "" || strings.TrimSpace(finding.Summary) == "" {
			return errors.New("finding id and summary are required")
		}
		if !validSeverity(finding.Severity) {
			return fmt.Errorf("finding %q has invalid severity %q", finding.ID, finding.Severity)
		}
		if !validConfidence(finding.Confidence) {
			return fmt.Errorf("finding %q has invalid confidence %q", finding.ID, finding.Confidence)
		}
		for _, evidenceID := range finding.EvidenceIDs {
			if _, ok := observations[evidenceID]; !ok {
				return fmt.Errorf("finding %q references unknown evidence %q", finding.ID, evidenceID)
			}
		}
	}
	return nil
}

func validSeverity(value Severity) bool {
	return value == SeverityOK || value == SeverityInfo || value == SeverityWarning || value == SeverityCritical
}

func validConfidence(value Confidence) bool {
	return value == ConfidenceLow || value == ConfidenceMedium || value == ConfidenceHigh
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/diagnostics`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/diagnostics/model.go internal/diagnostics/model_test.go
git commit -m "feat: add diagnostic model"
```

---

### Task 2: Domain Tool Registry

**Files:**
- Modify: `internal/tools/registry.go`
- Modify: `internal/tools/registry_test.go`

**Interfaces:**
- Consumes: existing `tools.Lookup`, `tools.ValidateInput`
- Produces: tool constants `GlusterVolumeHealthRead`, `MinIOBucketHealthRead`, `KafkaConsumerLagRead`, plus `Tool.Domain` and `Tool.ResourceType`

- [ ] **Step 1: Write failing registry tests**

Append to `internal/tools/registry_test.go`:

```go
func TestDomainReadToolsAreRegisteredReadOnlyTools(t *testing.T) {
	for _, name := range []string{GlusterVolumeHealthRead, MinIOBucketHealthRead, KafkaConsumerLagRead} {
		tool, ok := Lookup(name)
		if !ok {
			t.Fatalf("tool %q was not registered", name)
		}
		if tool.Operation != Read {
			t.Fatalf("tool %q operation = %q, want read", name, tool.Operation)
		}
		if tool.Domain == "" || tool.ResourceType == "" {
			t.Fatalf("tool %q missing domain metadata: %+v", name, tool)
		}
		if err := ValidateInput(tool, map[string]any{"environment": "prod", "name": "orders"}); err != nil {
			t.Fatalf("ValidateInput(%q) returned %v", name, err)
		}
	}
}

func TestDomainReadToolsRejectUnknownParameters(t *testing.T) {
	tool, ok := Lookup(GlusterVolumeHealthRead)
	if !ok {
		t.Fatalf("tool %q was not registered", GlusterVolumeHealthRead)
	}
	err := ValidateInput(tool, map[string]any{"environment": "prod", "name": "data", "shell": "rm -rf /"})
	if err == nil {
		t.Fatal("ValidateInput accepted unknown diagnostic parameter")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tools`

Expected: FAIL because the domain tool constants and fields are missing.

- [ ] **Step 3: Extend the static registry**

Modify `internal/tools/registry.go`:

```go
const (
	ClusterStatusRead      = "cluster.status.read"
	TopicRetentionSet      = "topic.retention.set"
	GlusterVolumeHealthRead = "glusterfs.volume.health.read"
	MinIOBucketHealthRead   = "minio.bucket.health.read"
	KafkaConsumerLagRead    = "kafka.consumer_lag.read"
)
```

Extend `Tool`:

```go
type Tool struct {
	Name                string
	Operation           Operation
	Risk                RiskLevel
	RollbackDescription string
	Domain              string
	ResourceType        string
}
```

Add registry entries:

```go
GlusterVolumeHealthRead: {
	Name: GlusterVolumeHealthRead, Operation: Read, Risk: Low, Domain: "glusterfs", ResourceType: "volume",
},
MinIOBucketHealthRead: {
	Name: MinIOBucketHealthRead, Operation: Read, Risk: Low, Domain: "minio", ResourceType: "bucket",
},
KafkaConsumerLagRead: {
	Name: KafkaConsumerLagRead, Operation: Read, Risk: Low, Domain: "kafka", ResourceType: "consumer_group",
},
```

Add validation cases:

```go
case GlusterVolumeHealthRead, MinIOBucketHealthRead, KafkaConsumerLagRead:
	if err := onlyFields(input, "environment", "name"); err != nil {
		return err
	}
	if _, err := requiredString(input, "environment"); err != nil {
		return err
	}
	_, err := requiredString(input, "name")
	return err
```

Update `validateToolDefinition` so read tools with one of these names require `Domain` and `ResourceType`:

```go
if strings.Contains(tool.Name, ".") && tool.Name != ClusterStatusRead && tool.Name != TopicRetentionSet {
	if strings.TrimSpace(tool.Domain) == "" || strings.TrimSpace(tool.ResourceType) == "" {
		return fmt.Errorf("domain tool %q requires domain and resource type", tool.Name)
	}
}
```

- [ ] **Step 4: Run registry tests**

Run: `go test ./internal/tools`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tools/registry.go internal/tools/registry_test.go
git commit -m "feat: register middleware diagnostic tools"
```

---

### Task 3: Diagnostic Service And Runbooks

**Files:**
- Create: `internal/diagnostics/service.go`
- Create: `internal/diagnostics/service_test.go`

**Interfaces:**
- Consumes: `execution.ReadOnlyService.ExecuteRead(ctx, user, toolName, input)`
- Produces: `diagnostics.Service.Run(ctx, user, request) (diagnostics.Package, error)`

- [ ] **Step 1: Write failing service tests**

Create `internal/diagnostics/service_test.go`:

```go
package diagnostics_test

import (
	"context"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestServiceRunsGlusterReadToolIntoDiagnosticPackage(t *testing.T) {
	t.Parallel()
	reads := &fakeReads{result: map[string]any{"status": "warning", "capacity_pct": 82.5, "heal_pending": 12}}
	service := diagnostics.NewService(reads, diagnostics.ClockFunc(func() time.Time {
		return time.Date(2026, time.July, 22, 9, 0, 0, 0, time.UTC)
	}))

	pkg, err := service.Run(context.Background(), user(), diagnostics.Request{Domain: "glusterfs", Environment: "prod", ResourceType: "volume", ResourceName: "data", Runbook: "health"})
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if reads.toolName != tools.GlusterVolumeHealthRead {
		t.Fatalf("tool = %q, want %q", reads.toolName, tools.GlusterVolumeHealthRead)
	}
	if pkg.ID == "" || pkg.Environment != "prod" || len(pkg.Resources) != 1 || len(pkg.Observations) != 1 || len(pkg.Findings) != 1 || len(pkg.Recommendations) != 1 {
		t.Fatalf("package = %+v, want populated diagnostic", pkg)
	}
	if pkg.Resources[0].Domain != "glusterfs" || pkg.Resources[0].Name != "data" {
		t.Fatalf("resource = %+v, want glusterfs data volume", pkg.Resources[0])
	}
	if err := diagnostics.ValidatePackage(pkg); err != nil {
		t.Fatalf("invalid package: %v", err)
	}
}

func TestServiceRejectsUnknownDomain(t *testing.T) {
	t.Parallel()
	service := diagnostics.NewService(&fakeReads{}, nil)

	_, err := service.Run(context.Background(), user(), diagnostics.Request{Domain: "shell", Environment: "prod", ResourceName: "root"})
	if err == nil {
		t.Fatal("Run accepted an unknown domain")
	}
}

type fakeReads struct {
	toolName string
	input    map[string]any
	result   map[string]any
}

func (f *fakeReads) ExecuteRead(_ context.Context, _ identity.CurrentUser, toolName string, input map[string]any) (map[string]any, error) {
	f.toolName = toolName
	f.input = input
	return f.result, nil
}

func user() identity.CurrentUser {
	return identity.CurrentUser{Subject: "operator-1", Roles: []string{"viewer"}, AllowedEnvironments: []string{"prod"}, RequestID: "request-1"}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/diagnostics`

Expected: FAIL because `NewService`, `ClockFunc`, and `Run` are missing.

- [ ] **Step 3: Implement diagnostic service**

Create `internal/diagnostics/service.go`:

```go
package diagnostics

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

type ReadService interface {
	ExecuteRead(context.Context, identity.CurrentUser, string, map[string]any) (map[string]any, error)
}

type Clock interface {
	Now() time.Time
}

type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }

type Service struct {
	reads ReadService
	clock Clock
}

func NewService(reads ReadService, clock Clock) *Service {
	if clock == nil {
		clock = ClockFunc(func() time.Time { return time.Now().UTC() })
	}
	return &Service{reads: reads, clock: clock}
}

func (s *Service) Run(ctx context.Context, user identity.CurrentUser, request Request) (Package, error) {
	if s.reads == nil {
		return Package{}, errors.New("diagnostic read service is required")
	}
	toolName, resourceType, err := resolveRunbook(request)
	if err != nil {
		return Package{}, err
	}
	name := strings.TrimSpace(request.ResourceName)
	if name == "" {
		name = defaultResourceName(request.Domain, resourceType)
	}
	input := map[string]any{"environment": strings.TrimSpace(request.Environment), "name": name}
	result, err := s.reads.ExecuteRead(ctx, user, toolName, input)
	if err != nil {
		return Package{}, err
	}
	now := s.clock.Now().UTC()
	resourceID := request.Domain + ":" + resourceType + ":" + name
	severity := severityFromResult(result)
	observation := Observation{ID: newID("obs"), ResourceID: resourceID, Kind: strings.TrimSuffix(toolName, ".read"), Severity: severity, Summary: summaryFromResult(request.Domain, name, result), Data: result, CollectedAt: now}
	finding := Finding{ID: newID("finding"), Severity: severity, Summary: findingSummary(request.Domain, severity, name), EvidenceIDs: []string{observation.ID}, Confidence: ConfidenceMedium}
	recommendation := Recommendation{ID: newID("rec"), Summary: recommendationSummary(request.Domain, severity, name), Rationale: observation.Summary, Risk: tools.Low, Actionable: false}
	pkg := Package{
		ID:          newID("diag"),
		Environment: strings.TrimSpace(request.Environment),
		Domains:     []string{request.Domain},
		Resources:   []ResourceRef{{Domain: request.Domain, Type: resourceType, ID: resourceID, Name: name, Environment: strings.TrimSpace(request.Environment)}},
		Observations: []Observation{observation},
		Findings:    []Finding{finding},
		Recommendations: []Recommendation{recommendation},
		CreatedAt:   now,
	}
	if err := ValidatePackage(pkg); err != nil {
		return Package{}, err
	}
	return pkg, nil
}

func resolveRunbook(request Request) (string, string, error) {
	switch strings.TrimSpace(request.Domain) {
	case "glusterfs":
		return tools.GlusterVolumeHealthRead, "volume", nil
	case "minio":
		return tools.MinIOBucketHealthRead, "bucket", nil
	case "kafka":
		return tools.KafkaConsumerLagRead, "consumer_group", nil
	default:
		return "", "", fmt.Errorf("domain %q is not registered", request.Domain)
	}
}

func defaultResourceName(domain, resourceType string) string {
	return domain + "-" + resourceType
}

func severityFromResult(result map[string]any) Severity {
	status, _ := result["status"].(string)
	switch strings.ToLower(status) {
	case "critical", "red":
		return SeverityCritical
	case "warning", "yellow":
		return SeverityWarning
	case "green", "healthy", "available", "ok":
		return SeverityOK
	default:
		return SeverityInfo
	}
}

func summaryFromResult(domain, name string, result map[string]any) string {
	status, _ := result["status"].(string)
	if strings.TrimSpace(status) == "" {
		status = "available"
	}
	return fmt.Sprintf("%s resource %s reported status %s", domain, name, status)
}

func findingSummary(domain string, severity Severity, name string) string {
	if severity == SeverityOK {
		return fmt.Sprintf("%s resource %s has no immediate finding", domain, name)
	}
	return fmt.Sprintf("%s resource %s needs operator review", domain, name)
}

func recommendationSummary(domain string, severity Severity, name string) string {
	if severity == SeverityOK {
		return fmt.Sprintf("Continue monitoring %s resource %s", domain, name)
	}
	return fmt.Sprintf("Review %s resource %s before planning changes", domain, name)
}

func newID(prefix string) string {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return prefix + "-" + hex.EncodeToString(value)
}
```

- [ ] **Step 4: Run diagnostic tests**

Run: `go test ./internal/diagnostics`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/diagnostics/service.go internal/diagnostics/service_test.go
git commit -m "feat: add middleware diagnostic service"
```

---

### Task 4: Assistant Diagnostic Intent

**Files:**
- Modify: `internal/assistant/planner.go`
- Modify: `internal/assistant/service.go`
- Modify: `internal/assistant/service_test.go`

**Interfaces:**
- Consumes: `diagnostics.Service.Run`
- Produces: `assistant.Response.Diagnostic *diagnostics.Package`

- [ ] **Step 1: Write failing assistant tests**

Append to `internal/assistant/service_test.go`:

```go
func TestAssistantDiagnosticIntentReturnsPackage(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		Diagnostic: &diagnostics.Request{Domain: "glusterfs", Environment: "prod", ResourceType: "volume", ResourceName: "data", Runbook: "health"},
	}})

	response, err := service.HandleMessage(context.Background(), viewer(), "检查 prod glusterfs data volume 健康")
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "diagnostic" || response.Diagnostic == nil {
		t.Fatalf("response = %+v, want diagnostic response", response)
	}
	if response.Diagnostic.Environment != "prod" || response.Diagnostic.Domains[0] != "glusterfs" {
		t.Fatalf("diagnostic = %+v, want glusterfs prod package", response.Diagnostic)
	}
}

func TestDeterministicPlannerDetectsMiddlewareDiagnostics(t *testing.T) {
	t.Parallel()
	intent, err := assistant.DeterministicPlanner{}.Plan(context.Background(), viewer(), "检查 prod glusterfs data volume 健康")
	if err != nil {
		t.Fatalf("Plan returned %v", err)
	}
	if intent.Diagnostic == nil || intent.Diagnostic.Domain != "glusterfs" || intent.Diagnostic.Environment != "prod" || intent.Diagnostic.ResourceName != "data" {
		t.Fatalf("intent = %+v, want glusterfs diagnostic", intent)
	}
}
```

Add import:

```go
	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/assistant`

Expected: FAIL because `Intent.Diagnostic`, `Response.Diagnostic`, and diagnostic service wiring are missing.

- [ ] **Step 3: Extend assistant types and service**

Modify imports in `internal/assistant/planner.go` and `internal/assistant/service.go` to include:

```go
	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
```

Extend `Intent`:

```go
type Intent struct {
	ToolName    string
	Input       map[string]any
	Diagnostic  *diagnostics.Request
	Confidence  float64
	Explanation string
}
```

Extend `Response`:

```go
	Diagnostic        *diagnostics.Package `json:"diagnostic,omitempty"`
```

Extend `Service`:

```go
	diagnostics *diagnostics.Service
```

Change constructor signature:

```go
func NewService(planner Planner, reads *execution.ReadOnlyService, planService *plans.Service) *Service {
	if planner == nil {
		planner = DeterministicPlanner{}
	}
	return &Service{planner: planner, reads: reads, plans: planService, diagnostics: diagnostics.NewService(reads, nil)}
}
```

At the top of `HandleMessage`, after planner success:

```go
	if intent.Diagnostic != nil {
		if s.diagnostics == nil {
			return Response{}, errors.New("diagnostic service is required")
		}
		pkg, err := s.diagnostics.Run(ctx, user, *intent.Diagnostic)
		if err != nil {
			return Response{}, err
		}
		return Response{Type: "diagnostic", Diagnostic: &pkg, Message: "Diagnostic package is ready."}, nil
	}
```

In `DeterministicPlanner.Plan`, check domain diagnostics after environment extraction and before generic cluster status:

```go
	if domain, ok := extractDomain(text); ok && containsAny(text, "status", "状态", "health", "健康", "capacity", "容量", "lag", "延迟") {
		return Intent{Diagnostic: &diagnostics.Request{
			Domain: domain, Environment: environment, ResourceType: defaultResourceType(domain), ResourceName: extractResourceName(text, domain), Runbook: "health",
		}, Confidence: 0.75, Explanation: "middleware diagnostic intent"}, nil
	}
```

Add helpers:

```go
func extractDomain(text string) (string, bool) {
	for _, domain := range []string{"glusterfs", "minio", "kafka"} {
		if tokenExists(text, domain) {
			return domain, true
		}
	}
	return "", false
}

func defaultResourceType(domain string) string {
	switch domain {
	case "glusterfs":
		return "volume"
	case "minio":
		return "bucket"
	case "kafka":
		return "consumer_group"
	default:
		return "resource"
	}
}

func extractResourceName(text, domain string) string {
	words := strings.Fields(text)
	for index, word := range words {
		if word == domain && index+1 < len(words) {
			candidate := strings.Trim(words[index+1], " ,，。:：")
			if regexp.MustCompile(`^[a-z0-9._-]+$`).MatchString(candidate) && candidate != defaultResourceType(domain) {
				return candidate
			}
		}
	}
	return domain + "-" + defaultResourceType(domain)
}
```

- [ ] **Step 4: Run assistant tests**

Run: `go test ./internal/assistant`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/assistant/planner.go internal/assistant/service.go internal/assistant/service_test.go
git commit -m "feat: return diagnostics from assistant"
```

---

### Task 5: HTTP Contract And Static Runner Samples

**Files:**
- Modify: `internal/httpapi/router_test.go`
- Modify: `cmd/copilot-api/main.go`

**Interfaces:**
- Consumes: `assistant.Response` JSON
- Produces: deterministic sample outputs for GlusterFS, MinIO, Kafka read tools

- [ ] **Step 1: Write failing HTTP test**

Append to `internal/httpapi/router_test.go`:

```go
func TestAssistantMessagesReturnsMiddlewareDiagnostic(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{result: map[string]any{"status": "warning", "capacity_pct": 82.5}})
	req := signedRequest(t, "/v1/assistant/messages", `{"message":"检查 prod glusterfs data volume 健康"}`, "viewer-1", []string{"viewer"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	body := res.Body.String()
	for _, want := range []string{`"type":"diagnostic"`, `"diagnostic"`, `"glusterfs"`, `"observations"`, `"recommendations"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want %s", body, want)
		}
	}
	if strings.Contains(body, "confirmation_token") {
		t.Fatalf("body = %s, diagnostic response must not expose confirmation token", body)
	}
}
```

- [ ] **Step 2: Run HTTP test**

Run: `go test ./internal/httpapi -run TestAssistantMessagesReturnsMiddlewareDiagnostic`

Expected: PASS after Task 4. If it fails because the test router runner cannot accept the new tool, update the local test runner to record and return the supplied result for any read tool.

- [ ] **Step 3: Add deterministic static runner outputs**

Modify `staticReadRunner.Read` in `cmd/copilot-api/main.go`:

```go
func (staticReadRunner) Read(_ context.Context, tool tools.Tool, input map[string]any) (map[string]any, error) {
	switch tool.Name {
	case tools.GlusterVolumeHealthRead:
		return map[string]any{"tool": tool.Name, "environment": input["environment"], "name": input["name"], "status": "warning", "capacity_pct": 82.5, "heal_pending": 12}, nil
	case tools.MinIOBucketHealthRead:
		return map[string]any{"tool": tool.Name, "environment": input["environment"], "name": input["name"], "status": "ok", "objects": 42000, "quota_pct": 61.2}, nil
	case tools.KafkaConsumerLagRead:
		return map[string]any{"tool": tool.Name, "environment": input["environment"], "name": input["name"], "status": "warning", "lag": 1840}, nil
	default:
		return map[string]any{"tool": tool.Name, "environment": input["environment"], "status": "available"}, nil
	}
}
```

- [ ] **Step 4: Run backend tests**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/router_test.go cmd/copilot-api/main.go
git commit -m "test: cover diagnostic assistant responses"
```

---

### Task 6: Console Diagnostic Types And Renderer

**Files:**
- Create: `apps/console/src/types.ts`
- Create: `apps/console/src/DiagnosticView.tsx`
- Modify: `apps/console/src/App.tsx`
- Modify: `apps/console/src/App.test.tsx`

**Interfaces:**
- Consumes: assistant response JSON with optional `diagnostic`
- Produces: reusable `DiagnosticView({ diagnostic })`

- [ ] **Step 1: Write failing frontend test**

Append to `apps/console/src/App.test.tsx`:

```tsx
  it("renders a middleware diagnostic package from assistant response", async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          type: "diagnostic",
          message: "Diagnostic package is ready.",
          diagnostic: {
            id: "diag-123",
            environment: "prod",
            domains: ["glusterfs"],
            resources: [{ domain: "glusterfs", type: "volume", id: "glusterfs:volume:data", name: "data", environment: "prod" }],
            observations: [{ id: "obs-123", resource_id: "glusterfs:volume:data", kind: "glusterfs.volume.health", severity: "warning", summary: "heal backlog is present", data: { heal_pending: 12 }, collected_at: "2026-07-22T09:00:00Z" }],
            findings: [{ id: "finding-123", severity: "warning", summary: "volume needs operator review", evidence_ids: ["obs-123"], confidence: "medium" }],
            recommendations: [{ id: "rec-123", summary: "review heal status", rationale: "pending entries are above zero", risk: "low", actionable: false }],
            created_at: "2026-07-22T09:00:00Z"
          }
        }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      )
    );
    render(<App />);

    await userEvent.clear(screen.getByLabelText("指令"));
    await userEvent.type(screen.getByLabelText("指令"), "检查 prod glusterfs data volume 健康");
    await userEvent.click(screen.getByRole("button", { name: "发送" }));

    await waitFor(() => expect(screen.getByText("diag-123")).toBeInTheDocument());
    expect(screen.getByText("glusterfs")).toBeInTheDocument();
    expect(screen.getByText("heal backlog is present")).toBeInTheDocument();
    expect(screen.getByText("volume needs operator review")).toBeInTheDocument();
    expect(screen.getByText("review heal status")).toBeInTheDocument();
  });
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd apps/console && npm test -- --run App.test.tsx`

Expected: FAIL because diagnostic response type and renderer are missing.

- [ ] **Step 3: Move shared types into `types.ts`**

Create `apps/console/src/types.ts`:

```ts
export type Role = "viewer" | "operator" | "admin";

export type DiagnosticPackage = {
  id: string;
  environment: string;
  domains: string[];
  resources: ResourceRef[];
  observations: Observation[];
  findings: Finding[];
  recommendations: Recommendation[];
  plan_ids?: string[];
  created_at: string;
};

export type ResourceRef = {
  domain: string;
  type: string;
  id: string;
  name: string;
  environment: string;
  labels?: Record<string, string>;
};

export type Observation = {
  id: string;
  resource_id: string;
  kind: string;
  severity: "ok" | "info" | "warning" | "critical";
  summary: string;
  data?: Record<string, unknown>;
  collected_at: string;
};

export type Finding = {
  id: string;
  severity: "ok" | "info" | "warning" | "critical";
  summary: string;
  evidence_ids: string[];
  confidence: "low" | "medium" | "high";
};

export type Recommendation = {
  id: string;
  summary: string;
  rationale: string;
  risk: "low" | "medium" | "high";
  actionable: boolean;
  tool_name?: string;
  candidate_input?: Record<string, unknown>;
};

export type AssistantResponse =
  | { type: "answer"; tool: string; answer: Record<string, unknown>; diagnostic?: DiagnosticPackage }
  | {
      type: "confirmation_required";
      tool: string;
      plan_id: string;
      status: string;
      version: number;
      expires_at: string;
      summary: string;
      confirmation_token?: string;
      diagnostic?: DiagnosticPackage;
    }
  | { type: "diagnostic"; message: string; diagnostic: DiagnosticPackage }
  | { type: "clarification_needed"; message: string }
  | { type: "execution_result"; plan_id: string; execution_id: string; status: string; reused: boolean };
```

In `App.tsx`, remove local `Role` and `AssistantResponse` definitions and add:

```ts
import type { AssistantResponse, DiagnosticPackage, Role } from "./types";
import { DiagnosticView } from "./DiagnosticView";
```

- [ ] **Step 4: Add diagnostic renderer**

Create `apps/console/src/DiagnosticView.tsx`:

```tsx
import type { DiagnosticPackage } from "./types";

export function DiagnosticView({ diagnostic }: { diagnostic: DiagnosticPackage | null }) {
  if (!diagnostic) {
    return (
      <div className="emptyState">
        <span>诊断</span>
        <p>结构化诊断会显示在这里。</p>
      </div>
    );
  }

  return (
    <div className="diagnosticView">
      <div className="diagnosticHeader">
        <div>
          <span className="badge wait">诊断包</span>
          <h3>{diagnostic.id}</h3>
        </div>
        <small>{diagnostic.environment}</small>
      </div>
      <section>
        <h4>资源</h4>
        {diagnostic.resources.map((resource) => (
          <div className="compactRow" key={resource.id}>
            <strong>{resource.name}</strong>
            <span>{resource.domain}</span>
            <span>{resource.type}</span>
          </div>
        ))}
      </section>
      <section>
        <h4>证据</h4>
        {diagnostic.observations.map((observation) => (
          <article className={`diagnosticItem ${observation.severity}`} key={observation.id}>
            <span>{observation.kind}</span>
            <p>{observation.summary}</p>
            {observation.data ? <pre>{JSON.stringify(observation.data, null, 2)}</pre> : null}
          </article>
        ))}
      </section>
      <section>
        <h4>结论</h4>
        {diagnostic.findings.map((finding) => (
          <article className={`diagnosticItem ${finding.severity}`} key={finding.id}>
            <span>{finding.confidence}</span>
            <p>{finding.summary}</p>
          </article>
        ))}
      </section>
      <section>
        <h4>建议</h4>
        {diagnostic.recommendations.map((recommendation) => (
          <article className="diagnosticItem" key={recommendation.id}>
            <span>{recommendation.risk}</span>
            <p>{recommendation.summary}</p>
            <small>{recommendation.rationale}</small>
          </article>
        ))}
      </section>
    </div>
  );
}
```

- [ ] **Step 5: Render diagnostics in `App.tsx`**

Add state:

```ts
const [diagnostic, setDiagnostic] = useState<DiagnosticPackage | null>(null);
```

After `setResponse(payload as AssistantResponse);` in `submit`, add:

```ts
const nextResponse = payload as AssistantResponse;
if ("diagnostic" in nextResponse && nextResponse.diagnostic) {
  setDiagnostic(nextResponse.diagnostic);
}
```

In the workbench JSX, replace the result panel heading with diagnostic rendering before `ResultView`:

```tsx
<aside className="panel resultPanel">
  <h2>诊断工作台</h2>
  <DiagnosticView diagnostic={diagnostic} />
  <ResultView response={response} confirming={confirming} onConfirm={confirmPlan} />
  <div className="rail">
```

Update `responseText`:

```ts
if (response.type === "diagnostic") {
  return response.message;
}
```

Update `activityFromResponse`:

```ts
if (response.type === "diagnostic") {
  return { id: Date.now() + 8, label: "诊断", value: response.diagnostic.environment, tone: "ok" };
}
```

- [ ] **Step 6: Run frontend test**

Run: `cd apps/console && npm test -- --run App.test.tsx`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add apps/console/src/types.ts apps/console/src/DiagnosticView.tsx apps/console/src/App.tsx apps/console/src/App.test.tsx
git commit -m "feat: render middleware diagnostics in console"
```

---

### Task 7: Console Styling And Layout Polish

**Files:**
- Modify: `apps/console/src/styles.css`
- Modify: `apps/console/src/App.test.tsx`

**Interfaces:**
- Consumes: `DiagnosticView` class names
- Produces: compact diagnostic panel styles that fit existing dark operations UI

- [ ] **Step 1: Add a rendering regression test for compact diagnostic text**

Append to `apps/console/src/App.test.tsx`:

```tsx
  it("keeps legacy read answers visible when no diagnostic is present", async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ type: "answer", tool: "cluster.status.read", answer: { status: "green" } }), {
        status: 200,
        headers: { "Content-Type": "application/json" }
      })
    );
    render(<App />);

    await userEvent.click(screen.getByRole("button", { name: "发送" }));

    await waitFor(() => expect(screen.getByText("green")).toBeInTheDocument());
    expect(screen.getByText("结构化诊断会显示在这里。")).toBeInTheDocument();
  });
```

- [ ] **Step 2: Run test**

Run: `cd apps/console && npm test -- --run App.test.tsx`

Expected: PASS.

- [ ] **Step 3: Add CSS**

Append to `apps/console/src/styles.css`:

```css
.diagnosticView {
  display: grid;
  gap: 14px;
  margin-bottom: 18px;
}

.diagnosticHeader {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.diagnosticHeader small,
.diagnosticItem small {
  color: #9ab3ad;
  word-break: break-word;
}

.diagnosticView h4 {
  margin: 0 0 8px;
  color: #c7fff4;
  font-size: 12px;
  letter-spacing: 0;
  text-transform: uppercase;
}

.compactRow {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto;
  gap: 8px;
  align-items: center;
  padding: 9px 10px;
  border: 1px solid rgba(199, 255, 244, 0.12);
  border-radius: 6px;
  background: #0d1918;
}

.compactRow strong,
.compactRow span {
  min-width: 0;
  word-break: break-word;
}

.diagnosticItem {
  display: grid;
  gap: 7px;
  margin-bottom: 8px;
  padding: 10px;
  border: 1px solid rgba(154, 179, 173, 0.18);
  border-left: 3px solid #70d6c8;
  border-radius: 6px;
  background: #0d1918;
}

.diagnosticItem.warning {
  border-left-color: #facc15;
}

.diagnosticItem.critical {
  border-left-color: #f87171;
}

.diagnosticItem.ok {
  border-left-color: #2dd4bf;
}

.diagnosticItem span {
  color: #70d6c8;
  font-family: "SFMono-Regular", Consolas, monospace;
  font-size: 12px;
}

.diagnosticItem p {
  margin: 0;
  line-height: 1.45;
  word-break: break-word;
}
```

- [ ] **Step 4: Run frontend checks**

Run: `cd apps/console && npm test -- --run App.test.tsx`

Expected: PASS.

Run: `cd apps/console && npm run build`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/console/src/styles.css apps/console/src/App.test.tsx
git commit -m "style: add diagnostic workbench layout"
```

---

### Task 8: End-to-End Coverage And Documentation

**Files:**
- Modify: `tests/e2e/assistant_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: running in-memory or SQLite-backed API test helpers already used by e2e tests
- Produces: documented sample prompts and e2e proof that diagnostics do not bypass write governance

- [ ] **Step 1: Write failing e2e test**

Add to `tests/e2e/assistant_test.go` using the existing helper style in that file:

```go
func TestAssistantReturnsMiddlewareDiagnosticPackage(t *testing.T) {
	t.Parallel()
	server := newTestServer(t)
	req := signedJSONRequest(t, server.URL+"/v1/assistant/messages", `{"message":"检查 prod glusterfs data volume 健康"}`, "viewer-1", []string{"viewer"}, []string{"prod"})

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("assistant request: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status = %d body = %s, want 200", res.StatusCode, body)
	}
	var body struct {
		Type       string              `json:"type"`
		Diagnostic diagnostics.Package `json:"diagnostic"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Type != "diagnostic" || body.Diagnostic.Environment != "prod" || len(body.Diagnostic.Observations) == 0 {
		t.Fatalf("body = %+v, want diagnostic package", body)
	}
}
```

Add imports if missing:

```go
	"encoding/json"
	"io"
	"net/http"

	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
```

- [ ] **Step 2: Run e2e test**

Run: `go test ./tests/e2e -run TestAssistantReturnsMiddlewareDiagnosticPackage`

Expected: PASS after the helper imports and response contract are wired.

- [ ] **Step 3: Update README**

Add a section after `Assistant Boundary`:

```markdown
## Middleware Diagnostics

The assistant can return a structured diagnostic package for middleware health
questions. The first rollout order is GlusterFS, then MinIO, then Kafka.

Example prompts:

```text
检查 prod glusterfs data volume 健康
检查 prod minio archive bucket 健康
检查 prod kafka payments consumer lag
```

Diagnostic output includes resources, observations, findings, and
recommendations. Recommendations are not execution authority. Any write action
still resolves to a registered tool, creates an immutable action plan, and must
pass approval, execution, and audit.
```

- [ ] **Step 4: Run full verification**

Run: `go test ./...`

Expected: PASS.

Run: `cd apps/console && npm test -- --run App.test.tsx`

Expected: PASS.

Run: `cd apps/console && npm run build`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tests/e2e/assistant_test.go README.md
git commit -m "docs: document middleware diagnostics"
```

---

## Self-Review Notes

- Spec coverage: Tasks 1-3 cover unified model, domain registry, runbooks, and read tools. Task 4 covers structured packages from `POST /v1/assistant/messages`. Tasks 6-7 cover the console workbench. Task 8 covers e2e behavior and documentation. Existing action plan, approval, execution, and audit paths are preserved by not adding any direct write execution path.
- Scope control: This plan does not add a CMDB, alert lifecycle, WebSocket, SSE, external approval connector, or production middleware executor.
- Type consistency: The Go API uses `diagnostics.Package`; the TypeScript API uses `DiagnosticPackage` with matching JSON field names.
- Safety check: All domain diagnostics call `execution.ReadOnlyService.ExecuteRead`, which still performs static lookup, policy evaluation, read-only enforcement, and audit.
