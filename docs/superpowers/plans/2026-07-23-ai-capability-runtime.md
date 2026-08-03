# AI Capability Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the built-in assistant resolve natural-language requests to reviewed, published dynamic capabilities and execute safe read capabilities through the existing governed tool path.

**Architecture:** Add a small capability-aware planner wrapper around the existing `assistant.Planner` interface. The wrapper inspects the canonical tool registry, resolves only registered dynamic capabilities into candidate `assistant.Intent` values, and leaves execution, permission checks, and action plans inside the existing `assistant.Service`, `policy`, and `execution` layers.

**Tech Stack:** Go, existing `internal/tools` registry, existing `internal/assistant` planner boundary, existing `internal/policy`, existing `internal/execution`, standard `go test`.

## Global Constraints

- AI clients must not receive raw backend URLs, raw Swagger operations, or direct HTTP adapter access.
- The resolver is not allowed to execute tools. It only returns a candidate `assistant.Intent`.
- Authorization remains in `policy.Evaluate`; resolver output is untrusted candidate data.
- Read capabilities execute through `execution.ReadOnlyService.ExecuteRead`.
- Write capabilities must not execute directly; current runtime publication of write capabilities remains out of scope.
- MCP is not implemented in this phase; it remains a later export layer over the same registry and policy path.
- Use TDD: write each failing test first, run it red, implement minimal code, run green, then commit.

---

## File Structure

- Modify `internal/tools/registry.go`
  - Add exported read-only accessors for dynamic input schemas so the resolver can know required fields without touching package globals.

- Create `internal/assistant/capability_resolver.go`
  - Own deterministic matching from natural language to registered dynamic read capabilities.
  - Expose `NewCapabilityAwarePlanner(fallback Planner) Planner`.

- Create `internal/assistant/capability_resolver_test.go`
  - Unit tests for matching, missing parameters, fallback behavior, and no direct execution.

- Modify `internal/assistant/service.go`
  - Add typed clarification errors so resolver-specific missing-field messages can reach the HTTP response.

- Modify `internal/assistant/service_test.go`
  - Assert the assistant can answer through a dynamic read capability and deny disallowed environments before execution.

- Modify `cmd/copilot-api/main.go`
  - Wrap the configured planner with `assistant.NewCapabilityAwarePlanner(planner)` after published capabilities are registered.

- Modify `cmd/copilot-api/main_test.go`
  - Assert planner wiring uses the capability-aware wrapper without requiring an external model.

---

### Task 1: Expose Dynamic Tool Input Schemas Safely

**Files:**
- Modify: `internal/tools/registry.go`
- Test: `internal/tools/registry_test.go`

**Interfaces:**
- Consumes: existing `tools.RegisterDynamicTools(definitions []DynamicToolDefinition) error`
- Produces:
  - `func DynamicInputSchema(name string) (map[string]DynamicInputField, bool)`
  - `func IsDynamic(name string) bool`

- [ ] **Step 1: Write the failing test**

Add this test to `internal/tools/registry_test.go`:

```go
func TestDynamicInputSchemaReturnsCopy(t *testing.T) {
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
		t.Fatalf("register dynamic tool: %v", err)
	}

	schema, ok := DynamicInputSchema("minio.bucket.capacity.read")
	if !ok {
		t.Fatal("DynamicInputSchema returned ok=false")
	}
	schema["bucket"] = DynamicInputField{Type: "integer", Required: false}

	fresh, ok := DynamicInputSchema("minio.bucket.capacity.read")
	if !ok || fresh["bucket"].Type != "string" || !fresh["bucket"].Required {
		t.Fatalf("fresh schema = %+v, want unchanged copy", fresh)
	}
	if !IsDynamic("minio.bucket.capacity.read") {
		t.Fatal("IsDynamic returned false for registered dynamic tool")
	}
	if IsDynamic(ClusterStatusRead) {
		t.Fatal("IsDynamic returned true for static tool")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/tools -run TestDynamicInputSchemaReturnsCopy
```

Expected: FAIL because `DynamicInputSchema` and `IsDynamic` are undefined.

- [ ] **Step 3: Write minimal implementation**

Add these functions to `internal/tools/registry.go` near `IsStatic`:

```go
// IsDynamic reports whether name belongs to the reviewed runtime registry.
func IsDynamic(name string) bool {
	_, ok := dynamicTools[name]
	return ok
}

// DynamicInputSchema returns a copy of a registered dynamic tool input schema.
func DynamicInputSchema(name string) (map[string]DynamicInputField, bool) {
	schema, ok := dynamicInputs[name]
	if !ok {
		return nil, false
	}
	return cloneDynamicInputSchema(schema), true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/tools -run TestDynamicInputSchemaReturnsCopy
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tools/registry.go internal/tools/registry_test.go
git commit -m "feat: expose dynamic tool schemas"
```

---

### Task 2: Add Typed Assistant Clarification Messages

**Files:**
- Modify: `internal/assistant/planner.go`
- Modify: `internal/assistant/service.go`
- Test: `internal/assistant/service_test.go`

**Interfaces:**
- Consumes: existing `assistant.ErrClarificationNeeded`
- Produces:
  - `type ClarificationError struct { Message string }`
  - `func NewClarification(message string) error`

- [ ] **Step 1: Write the failing test**

Add this test to `internal/assistant/service_test.go`:

```go
func TestAssistantReturnsTypedClarificationMessage(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{err: assistant.NewClarification("缺少参数: cluster, bucket")})

	response, err := service.HandleMessage(context.Background(), viewer(), "查 minio 容量")
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "clarification_needed" || response.Message != "缺少参数: cluster, bucket" {
		t.Fatalf("response = %+v, want typed clarification message", response)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/assistant -run TestAssistantReturnsTypedClarificationMessage
```

Expected: FAIL because `assistant.NewClarification` is undefined.

- [ ] **Step 3: Write minimal implementation**

Add this to `internal/assistant/planner.go` near `ErrClarificationNeeded`:

```go
type ClarificationError struct {
	Message string
}

func (e ClarificationError) Error() string {
	if strings.TrimSpace(e.Message) == "" {
		return ErrClarificationNeeded.Error()
	}
	return e.Message
}

func (e ClarificationError) Is(target error) bool {
	return target == ErrClarificationNeeded
}

func NewClarification(message string) error {
	return ClarificationError{Message: strings.TrimSpace(message)}
}
```

Update `internal/assistant/service.go` in `HandleMessage`:

```go
	if err != nil {
		var clarification ClarificationError
		if errors.As(err, &clarification) {
			message := strings.TrimSpace(clarification.Message)
			if message == "" {
				message = clarificationMessage
			}
			return Response{Type: "clarification_needed", Message: message}, nil
		}
		if errors.Is(err, ErrClarificationNeeded) {
			return Response{Type: "clarification_needed", Message: clarificationMessage}, nil
		}
		return Response{}, err
	}
```

Also add `strings` to the `internal/assistant/service.go` imports.

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/assistant -run TestAssistantReturnsTypedClarificationMessage
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/assistant/planner.go internal/assistant/service.go internal/assistant/service_test.go
git commit -m "feat: return assistant clarification details"
```

---

### Task 3: Implement Capability-Aware Planner Resolution

**Files:**
- Create: `internal/assistant/capability_resolver.go`
- Create: `internal/assistant/capability_resolver_test.go`

**Interfaces:**
- Consumes:
  - `tools.All() []tools.Tool`
  - `tools.IsDynamic(name string) bool`
  - `tools.DynamicInputSchema(name string) (map[string]tools.DynamicInputField, bool)`
  - `assistant.NewClarification(message string) error`
- Produces:
  - `type CapabilityAwarePlanner struct { fallback Planner }`
  - `func NewCapabilityAwarePlanner(fallback Planner) Planner`
  - `func (p CapabilityAwarePlanner) Plan(ctx context.Context, user identity.CurrentUser, message string) (Intent, error)`

- [ ] **Step 1: Write the failing tests**

Create `internal/assistant/capability_resolver_test.go`:

```go
package assistant_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestCapabilityAwarePlannerResolvesDynamicReadCapability(t *testing.T) {
	registerDynamicCapacityTool(t)
	planner := assistant.NewCapabilityAwarePlanner(failingPlanner{})

	intent, err := planner.Plan(context.Background(), viewer(), "查一下 prod m1 archive bucket 的 minio 容量")
	if err != nil {
		t.Fatalf("Plan returned %v", err)
	}
	if intent.ToolName != "minio.bucket.capacity.read" {
		t.Fatalf("tool = %q, want dynamic capacity tool", intent.ToolName)
	}
	if intent.Input["environment"] != "prod" || intent.Input["cluster"] != "m1" || intent.Input["bucket"] != "archive" {
		t.Fatalf("input = %+v, want extracted environment, cluster, bucket", intent.Input)
	}
}

func TestCapabilityAwarePlannerClarifiesMissingDynamicInputs(t *testing.T) {
	registerDynamicCapacityTool(t)
	planner := assistant.NewCapabilityAwarePlanner(failingPlanner{})

	_, err := planner.Plan(context.Background(), viewer(), "查一下 prod minio bucket 容量")
	if !errors.Is(err, assistant.ErrClarificationNeeded) || !strings.Contains(err.Error(), "cluster") || !strings.Contains(err.Error(), "bucket") {
		t.Fatalf("error = %v, want clarification naming missing fields", err)
	}
}

func TestCapabilityAwarePlannerFallsBackWhenNoDynamicToolMatches(t *testing.T) {
	registerDynamicCapacityTool(t)
	planner := assistant.NewCapabilityAwarePlanner(fakePlanner{intent: assistant.Intent{ToolName: tools.ClusterStatusRead, Input: map[string]any{"environment": "prod"}}})

	intent, err := planner.Plan(context.Background(), viewer(), "查看 prod 集群状态")
	if err != nil {
		t.Fatalf("Plan returned %v", err)
	}
	if intent.ToolName != tools.ClusterStatusRead {
		t.Fatalf("tool = %q, want fallback static planner", intent.ToolName)
	}
}

type failingPlanner struct{}

func (failingPlanner) Plan(context.Context, identity.CurrentUser, string) (assistant.Intent, error) {
	return assistant.Intent{}, errors.New("fallback should not be called")
}

func registerDynamicCapacityTool(t *testing.T) {
	t.Helper()
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
		Tool: tools.Tool{Name: "minio.bucket.capacity.read", Operation: tools.Read, Risk: tools.Low, Domain: "minio", ResourceType: "bucket"},
		InputSchema: map[string]tools.DynamicInputField{
			"environment": {Type: "string", Required: true},
			"cluster":     {Type: "string", Required: true},
			"bucket":      {Type: "string", Required: true},
		},
	}})
	if err != nil {
		t.Fatalf("register dynamic capacity tool: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internal/assistant -run 'TestCapabilityAwarePlanner'
```

Expected: FAIL because `NewCapabilityAwarePlanner` is undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/assistant/capability_resolver.go`:

```go
package assistant

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

type CapabilityAwarePlanner struct {
	fallback Planner
}

func NewCapabilityAwarePlanner(fallback Planner) Planner {
	if fallback == nil {
		fallback = DeterministicPlanner{}
	}
	return CapabilityAwarePlanner{fallback: fallback}
}

func (p CapabilityAwarePlanner) Plan(ctx context.Context, user identity.CurrentUser, message string) (Intent, error) {
	intent, matched, err := resolveDynamicCapability(message)
	if matched || err != nil {
		return intent, err
	}
	return p.fallback.Plan(ctx, user, message)
}

func resolveDynamicCapability(message string) (Intent, bool, error) {
	text := strings.ToLower(strings.TrimSpace(message))
	if text == "" {
		return Intent{}, false, nil
	}
	for _, tool := range tools.All() {
		if !tools.IsDynamic(tool.Name) || tool.Operation != tools.Read {
			continue
		}
		schema, ok := tools.DynamicInputSchema(tool.Name)
		if !ok {
			continue
		}
		if !capabilityMatches(text, tool) {
			continue
		}
		input := extractDynamicInput(text, schema)
		missing := missingRequiredFields(schema, input)
		if len(missing) > 0 {
			return Intent{}, true, NewClarification("缺少参数: "+strings.Join(missing, ", "))
		}
		return Intent{
			ToolName:    tool.Name,
			Input:       input,
			Confidence:  0.7,
			Explanation: "matched published dynamic capability",
		}, true, nil
	}
	return Intent{}, false, nil
}

func capabilityMatches(text string, tool tools.Tool) bool {
	score := 0
	for _, token := range strings.Split(tool.Name, ".") {
		if token != "" && tokenExists(text, token) {
			score++
		}
	}
	if tool.Domain != "" && tokenExists(text, tool.Domain) {
		score += 2
	}
	if tool.ResourceType != "" && tokenExists(text, tool.ResourceType) {
		score++
	}
	return score >= 2
}

func extractDynamicInput(text string, schema map[string]tools.DynamicInputField) map[string]any {
	input := map[string]any{}
	if _, ok := schema["environment"]; ok {
		if environment, found := extractEnvironment(text); found {
			input["environment"] = environment
		}
	}
	words := normalizedWords(text)
	for name, field := range schema {
		if name == "environment" {
			continue
		}
		if value, ok := extractNamedValue(text, name); ok {
			input[name] = coerceDynamicValue(field.Type, value)
			continue
		}
		if value, ok := extractPositionalValue(words, name); ok {
			input[name] = coerceDynamicValue(field.Type, value)
		}
	}
	return input
}

func extractNamedValue(text, name string) (string, bool) {
	pattern := regexp.MustCompile(regexp.QuoteMeta(name) + `\s*[:=]\s*([a-z0-9._-]+)`)
	matches := pattern.FindStringSubmatch(text)
	if len(matches) == 2 {
		return matches[1], true
	}
	return "", false
}

func extractPositionalValue(words []string, name string) (string, bool) {
	for index, word := range words {
		if word != name || index == 0 {
			continue
		}
		candidate := words[index-1]
		if candidate != "prod" && candidate != "staging" && candidate != "dev" {
			return candidate, true
		}
	}
	return "", false
}

func normalizedWords(text string) []string {
	fields := strings.Fields(text)
	words := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, " ,，。:：")
		if field != "" {
			words = append(words, field)
		}
	}
	return words
}

func missingRequiredFields(schema map[string]tools.DynamicInputField, input map[string]any) []string {
	missing := []string{}
	for name, field := range schema {
		if field.Required {
			if _, ok := input[name]; !ok {
				missing = append(missing, name)
			}
		}
	}
	sort.Strings(missing)
	return missing
}

func coerceDynamicValue(kind, value string) any {
	switch kind {
	case "integer":
		var integer int
		if _, err := fmt.Sscanf(value, "%d", &integer); err == nil {
			return integer
		}
	case "boolean":
		return value == "true" || value == "是"
	}
	return value
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
go test ./internal/assistant -run 'TestCapabilityAwarePlanner'
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/assistant/capability_resolver.go internal/assistant/capability_resolver_test.go
git commit -m "feat: resolve dynamic capabilities in assistant"
```

---

### Task 4: Prove Assistant Dynamic Read Execution And Policy Denial

**Files:**
- Modify: `internal/assistant/service_test.go`

**Interfaces:**
- Consumes:
  - `assistant.NewCapabilityAwarePlanner(fallback Planner) Planner`
  - existing `assistant.Service.HandleMessage`
  - existing `policy.RegisterDynamicRolePermissions`
  - existing dynamic tool registration helpers
- Produces: tests proving dynamic capability intents still pass through service, policy, and read execution.

- [ ] **Step 1: Write the failing tests**

Add these tests to `internal/assistant/service_test.go`:

```go
func TestAssistantDynamicCapabilityReadReturnsAnswer(t *testing.T) {
	registerAssistantDynamicCapacityTool(t)
	service, _ := newAssistant(t, assistant.NewCapabilityAwarePlanner(fakePlanner{err: assistant.ErrClarificationNeeded}))

	response, err := service.HandleMessage(context.Background(), viewer(), "查一下 prod m1 archive bucket 的 minio 容量")
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "answer" || response.Tool != "minio.bucket.capacity.read" {
		t.Fatalf("response = %+v, want dynamic capability answer", response)
	}
	if response.Answer["tool"] != "minio.bucket.capacity.read" || response.Answer["environment"] != "prod" {
		t.Fatalf("answer = %+v, want read runner result", response.Answer)
	}
}

func TestAssistantDynamicCapabilityReadDeniesDisallowedEnvironment(t *testing.T) {
	registerAssistantDynamicCapacityTool(t)
	service, _ := newAssistant(t, assistant.NewCapabilityAwarePlanner(fakePlanner{err: assistant.ErrClarificationNeeded}))
	unauthorized := viewer()
	unauthorized.AllowedEnvironments = []string{"staging"}

	_, err := service.HandleMessage(context.Background(), unauthorized, "查一下 prod m1 archive bucket 的 minio 容量")
	if !errors.Is(err, assistant.ErrPolicyDenied) || !strings.Contains(err.Error(), "environment_denied") {
		t.Fatalf("error = %v, want environment policy denial", err)
	}
}

func registerAssistantDynamicCapacityTool(t *testing.T) {
	t.Helper()
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)
	err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
		Tool: tools.Tool{Name: "minio.bucket.capacity.read", Operation: tools.Read, Risk: tools.Low, Domain: "minio", ResourceType: "bucket"},
		InputSchema: map[string]tools.DynamicInputField{
			"environment": {Type: "string", Required: true},
			"cluster":     {Type: "string", Required: true},
			"bucket":      {Type: "string", Required: true},
		},
	}})
	if err != nil {
		t.Fatalf("register dynamic tool: %v", err)
	}
	policy.RegisterDynamicRolePermissions(map[string][]string{"minio.bucket.capacity.read": {"viewer"}})
}
```

- [ ] **Step 2: Run tests to verify they fail if Task 3 is absent**

Run:

```bash
go test ./internal/assistant -run 'TestAssistantDynamicCapabilityRead'
```

Expected: FAIL before Task 3 exists; PASS after Task 3 exists and the imports include `internal/policy`.

- [ ] **Step 3: Update imports**

Add `internal/policy` to `internal/assistant/service_test.go` imports:

```go
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
go test ./internal/assistant -run 'TestAssistantDynamicCapabilityRead'
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/assistant/service_test.go
git commit -m "test: cover assistant dynamic capability reads"
```

---

### Task 5: Wire Capability-Aware Planner At API Startup

**Files:**
- Modify: `cmd/copilot-api/main.go`
- Modify: `cmd/copilot-api/main_test.go`

**Interfaces:**
- Consumes:
  - `assistant.NewPlannerFromEnv(ctx, env)`
  - `assistant.NewCapabilityAwarePlanner(fallback Planner) Planner`
- Produces:
  - startup path wraps deterministic or Eino planner with capability resolution after `capabilities.RegisterPublished`.

- [ ] **Step 1: Write the failing test**

Add this test to `cmd/copilot-api/main_test.go`:

```go
func TestAssistantPlannerFromEnvIsCapabilityAware(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
		Tool: tools.Tool{Name: "minio.bucket.capacity.read", Operation: tools.Read, Risk: tools.Low, Domain: "minio", ResourceType: "bucket"},
		InputSchema: map[string]tools.DynamicInputField{
			"environment": {Type: "string", Required: true},
			"cluster":     {Type: "string", Required: true},
			"bucket":      {Type: "string", Required: true},
		},
	}})
	if err != nil {
		t.Fatalf("register dynamic: %v", err)
	}

	planner, mode, err := assistantPlannerFromEnv(context.Background(), map[string]string{})
	if err != nil {
		t.Fatalf("assistantPlannerFromEnv returned %v", err)
	}
	if mode != "deterministic+capabilities" {
		t.Fatalf("mode = %q, want deterministic+capabilities", mode)
	}
	intent, err := planner.Plan(context.Background(), identity.CurrentUser{}, "查一下 prod m1 archive bucket 的 minio 容量")
	if err != nil {
		t.Fatalf("Plan returned %v", err)
	}
	if intent.ToolName != "minio.bucket.capacity.read" {
		t.Fatalf("tool = %q, want dynamic capability", intent.ToolName)
	}
}
```

If `cmd/copilot-api/main_test.go` does not already import `context`, `identity`, and `tools`, add:

```go
	"context"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./cmd/copilot-api -run TestAssistantPlannerFromEnvIsCapabilityAware
```

Expected: FAIL because `assistantPlannerFromEnv` is undefined.

- [ ] **Step 3: Write minimal implementation**

In `cmd/copilot-api/main.go`, replace:

```go
	planner, plannerMode, err := assistant.NewPlannerFromEnv(serviceContext, assistant.EnvMapFromLookup(os.Getenv))
```

with:

```go
	planner, plannerMode, err := assistantPlannerFromEnv(serviceContext, assistant.EnvMapFromLookup(os.Getenv))
```

Add this helper near `publishedCapabilitiesFromEnv`:

```go
func assistantPlannerFromEnv(ctx context.Context, env map[string]string) (assistant.Planner, string, error) {
	planner, mode, err := assistant.NewPlannerFromEnv(ctx, env)
	if err != nil {
		return nil, "", err
	}
	return assistant.NewCapabilityAwarePlanner(planner), mode + "+capabilities", nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
go test ./cmd/copilot-api -run TestAssistantPlannerFromEnvIsCapabilityAware
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/copilot-api/main.go cmd/copilot-api/main_test.go
git commit -m "feat: wire capability-aware assistant planner"
```

---

### Task 6: Full Verification

**Files:**
- No new files unless failures require fixes in files touched by Tasks 1-5.

**Interfaces:**
- Consumes all previous tasks.
- Produces verified AI capability runtime behavior.

- [ ] **Step 1: Run focused tests**

Run:

```bash
go test ./internal/tools -run TestDynamicInputSchemaReturnsCopy
go test ./internal/assistant -run 'TestCapabilityAwarePlanner|TestAssistantDynamicCapabilityRead|TestAssistantReturnsTypedClarificationMessage'
go test ./cmd/copilot-api -run TestAssistantPlannerFromEnvIsCapabilityAware
```

Expected: all PASS.

- [ ] **Step 2: Run package and full backend verification**

Run:

```bash
go test ./internal/tools ./internal/assistant ./internal/policy ./internal/capabilities ./cmd/copilot-api
go test ./...
go vet ./...
```

Expected: all PASS.

- [ ] **Step 3: Confirm worktree only contains intentional files**

Run:

```bash
git status --short
```

Expected: only intentional task files are modified or staged. Existing unrelated `.superpowers/sdd/task-2-report.md` and `.DS_Store` files may remain unstaged and must not be committed.

- [ ] **Step 4: Commit verification fixes if needed**

If verification required any small fixes:

```bash
git add <intentional-files>
git commit -m "fix: stabilize ai capability runtime"
```

If no fixes were needed, do not create an empty commit.

---

## Self-Review

Spec coverage:

- Published dynamic read capability resolution is covered by Task 3.
- Missing required dynamic inputs are covered by Task 2 and Task 3.
- Assistant answer through canonical registry, policy, and read execution is covered by Task 4.
- API startup wiring is covered by Task 5.
- MCP remains out of scope and is not implemented in any task.
- Raw Swagger/backend URL exposure is avoided because the resolver reads only `tools.Tool` metadata and dynamic input schemas.

Placeholder scan:

- The plan contains no unfinished markers or unspecified edge handling.
- Every task includes exact files, functions, commands, expected results, and commit commands.

Type consistency:

- `tools.DynamicInputField`, `assistant.Intent`, `assistant.Planner`, `identity.CurrentUser`, and existing helper names match the current codebase.
- Task 3 depends on Task 1 and Task 2 interfaces exactly as defined.
