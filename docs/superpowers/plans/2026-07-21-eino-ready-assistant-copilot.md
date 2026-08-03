# Eino-Ready Assistant Copilot Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `POST /v1/assistant/messages` so operators can ask natural-language questions that become governed read results or pending write plans.

**Architecture:** Add an `internal/assistant` package with a `Planner` interface, deterministic default planner, and service orchestration. The assistant treats planner output as untrusted candidate intent and reuses the existing Go registry, policy, read-only execution, action plan, and audit services. Eino is reserved behind a future `EinoPlanner` implementation; this plan does not add external model calls.

**Tech Stack:** Go 1.24, net/http, existing HMAC JWT authenticator, existing tools/policy/plans/execution/audit/store packages, SQLite local tests, MySQL production/integration.

## Global Constraints

- JWT only projects `sub`, `roles`, `allowed_environments`, and request ID.
- Planner output is candidate data only; it cannot grant permissions, add tools, weaken schemas, or execute operations.
- Tool authorization must call `tools.Lookup` and `policy.Evaluate`.
- Write intents must create `pending_confirmation` plans and must not execute.
- Assistant responses must not expose raw confirmation tokens.
- Local default tests use SQLite for real SQL constraints; MySQL remains production/integration.
- No Eino runtime dependency, external LLM call, RAG, real write executor, shell, SQL, or raw operations API access in this phase.

---

## File Structure

- `internal/assistant/planner.go`: `Planner`, `Intent`, deterministic parser.
- `internal/assistant/service.go`: assistant orchestration and response shaping.
- `internal/assistant/*_test.go`: parser and service tests.
- `internal/httpapi/router.go`: add `POST /v1/assistant/messages` route.
- `internal/httpapi/router_test.go`: assistant HTTP tests.
- `tests/e2e/assistant_test.go`: SQLite-backed assistant e2e test.
- `cmd/copilot-api/main.go`: wire assistant service into router.
- `README.md`: document assistant endpoint and Eino boundary.

### Task 1: Planner Interface and Deterministic Parser

**Files:**
- Create: `internal/assistant/planner.go`
- Test: `internal/assistant/planner_test.go`

**Interfaces:**
- Produces:
  - `type Intent struct { ToolName string; Input map[string]any; Confidence float64; Explanation string }`
  - `type Planner interface { Plan(context.Context, identity.CurrentUser, string) (Intent, error) }`
  - `type DeterministicPlanner struct{}`
  - `func (DeterministicPlanner) Plan(context.Context, identity.CurrentUser, string) (Intent, error)`
  - `var ErrClarificationNeeded error`

- [ ] **Step 1: Write failing parser tests**

```go
func TestDeterministicPlannerParsesChineseClusterStatus(t *testing.T) {
  intent, err := assistant.DeterministicPlanner{}.Plan(context.Background(), user(), "查看 prod 集群状态")
  if err != nil { t.Fatalf("plan: %v", err) }
  if intent.ToolName != tools.ClusterStatusRead { t.Fatalf("tool = %q", intent.ToolName) }
  if intent.Input["environment"] != "prod" { t.Fatalf("input = %#v", intent.Input) }
}

func TestDeterministicPlannerParsesTopicRetentionWrite(t *testing.T) {
  intent, err := assistant.DeterministicPlanner{}.Plan(context.Background(), user(), "把 prod 的 orders topic retention 改成 72 小时")
  if err != nil { t.Fatalf("plan: %v", err) }
  if intent.ToolName != tools.TopicRetentionSet { t.Fatalf("tool = %q", intent.ToolName) }
  if intent.Input["topic"] != "orders" || intent.Input["retention_hours"] != 72 {
    t.Fatalf("input = %#v", intent.Input)
  }
}
```

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/assistant -run TestDeterministicPlanner`

Expected: FAIL because package has no implementation.

- [ ] **Step 3: Implement minimal parser**

Create `internal/assistant/planner.go` with:

```go
package assistant

import (
  "context"
  "errors"
  "regexp"
  "strconv"
  "strings"

  "github.com/gracegaoya/ai-operations-copilot/internal/identity"
  "github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

var ErrClarificationNeeded = errors.New("clarification needed")

type Intent struct {
  ToolName string
  Input map[string]any
  Confidence float64
  Explanation string
}

type Planner interface {
  Plan(context.Context, identity.CurrentUser, string) (Intent, error)
}

type DeterministicPlanner struct{}

func (DeterministicPlanner) Plan(_ context.Context, _ identity.CurrentUser, message string) (Intent, error) {
  text := strings.ToLower(strings.TrimSpace(message))
  env, ok := extractEnvironment(text)
  if !ok {
    return Intent{}, ErrClarificationNeeded
  }
  if containsAny(text, "status", "状态", "health", "健康") {
    return Intent{ToolName: tools.ClusterStatusRead, Input: map[string]any{"environment": env}, Confidence: 0.9, Explanation: "cluster status intent"}, nil
  }
  if containsAny(text, "retention", "保留", "留存") {
    topic, topicOK := extractTopic(text)
    hours, hoursOK := extractHours(text)
    if !topicOK || !hoursOK {
      return Intent{}, ErrClarificationNeeded
    }
    return Intent{ToolName: tools.TopicRetentionSet, Input: map[string]any{"environment": env, "topic": topic, "retention_hours": hours}, Confidence: 0.8, Explanation: "topic retention intent"}, nil
  }
  return Intent{}, ErrClarificationNeeded
}
```

- [ ] **Step 4: Run GREEN**

Run: `go test -count=1 ./internal/assistant -run TestDeterministicPlanner`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/assistant/planner.go internal/assistant/planner_test.go
git commit -m "feat: add deterministic assistant planner"
```

### Task 2: Assistant Service Orchestration

**Files:**
- Create: `internal/assistant/service.go`
- Test: `internal/assistant/service_test.go`

**Interfaces:**
- Consumes: `Planner`, `execution.ReadOnlyService`, `plans.Service`, `policy.Evaluate`, `tools.Lookup`.
- Produces:
  - `type Service struct`
  - `func NewService(planner Planner, reads *execution.ReadOnlyService, plans *plans.Service) *Service`
  - `func (s *Service) HandleMessage(ctx context.Context, user identity.CurrentUser, message string) (Response, error)`
  - `type Response struct { Type string; Tool string; Answer map[string]any; PlanID string; Status string; ExpiresAt time.Time; Summary string; Message string }`

- [ ] **Step 1: Write failing service tests**

```go
func TestAssistantViewerReadReturnsAnswer(t *testing.T) {
  service := newAssistant(t, fakePlanner{intent: assistant.Intent{ToolName: tools.ClusterStatusRead, Input: map[string]any{"environment":"prod"}}})
  response, err := service.HandleMessage(context.Background(), viewer(), "查看 prod 集群状态")
  if err != nil { t.Fatalf("handle message: %v", err) }
  if response.Type != "answer" || response.Tool != tools.ClusterStatusRead { t.Fatalf("response = %+v", response) }
}

func TestAssistantAdminWriteCreatesPendingPlanWithoutToken(t *testing.T) {
  service := newAssistant(t, fakePlanner{intent: assistant.Intent{ToolName: tools.TopicRetentionSet, Input: map[string]any{"environment":"prod","topic":"orders","retention_hours":72}}})
  response, err := service.HandleMessage(context.Background(), admin(), "retention")
  if err != nil { t.Fatalf("handle message: %v", err) }
  if response.Type != "confirmation_required" || response.PlanID == "" || response.Status != string(plans.PendingConfirmation) {
    t.Fatalf("response = %+v", response)
  }
}
```

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/assistant -run TestAssistant`

Expected: FAIL because service is not implemented.

- [ ] **Step 3: Implement service**

Service logic:
- If planner returns `ErrClarificationNeeded`, return `Response{Type:"clarification_needed", Message:"I can help with cluster status or topic retention. Please include environment and required parameters."}`.
- Resolve the intent tool with `tools.Lookup`; unknown tools return policy denial.
- Call `policy.Evaluate(user, tool, intent.Input)`.
- If denied, return error wrapping `ErrPolicyDenied` with the policy reason.
- If canonical tool operation is read, call `reads.ExecuteRead`.
- If canonical tool operation is write, call `plans.CreatePlan` and return a confirmation response without `ConfirmationToken`.

- [ ] **Step 4: Run GREEN**

Run: `go test -count=1 ./internal/assistant`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/assistant
git commit -m "feat: orchestrate governed assistant intents"
```

### Task 3: Assistant HTTP Route

**Files:**
- Modify: `internal/httpapi/router.go`
- Modify: `internal/httpapi/router_test.go`
- Modify: `cmd/copilot-api/main.go`

**Interfaces:**
- Consumes: `assistant.Service.HandleMessage`.
- Produces: `POST /v1/assistant/messages`.

- [ ] **Step 1: Write failing HTTP tests**

```go
func TestAssistantMessagesRequiresAuthentication(t *testing.T) {
  router, _ := testRouter(t, &readRunner{})
  res := httptest.NewRecorder()
  router.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/v1/assistant/messages", strings.NewReader(`{"message":"查看 prod 集群状态"}`)))
  if res.Code != http.StatusUnauthorized { t.Fatalf("status = %d", res.Code) }
}
```

Also add tests for viewer read success, viewer write `403`, and admin write
confirmation response without `confirmation_token`.

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/httpapi -run TestAssistant`

Expected: FAIL because the route is not implemented.

- [ ] **Step 3: Implement route**

Modify router:
- Add optional assistant service dependency.
- Route exact `POST /v1/assistant/messages`.
- Decode body `{ "message": string }` with 10 KB input cap.
- Authenticate with the existing authenticator.
- Call assistant service under 5 second timeout.
- Map clarification to `200`, policy denial to `403`, execution failure to `502`, bad JSON to `400`, missing auth to `401`.
- Keep existing read route behavior unchanged.

- [ ] **Step 4: Run GREEN**

Run: `go test -count=1 ./internal/httpapi`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi cmd/copilot-api/main.go
git commit -m "feat: expose assistant message endpoint"
```

### Task 4: SQLite E2E and Documentation

**Files:**
- Create: `tests/e2e/assistant_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `POST /v1/assistant/messages`, SQLite `store.NewSQLActionPlanStore`.

- [ ] **Step 1: Write failing SQLite e2e test**

```go
func TestAssistantWriteMessageStoresPendingPlanInSQLite(t *testing.T) {
  db := openSQLite(t)
  require.NoError(t, store.ApplySQLiteMigrations(db))
  router := buildAssistantRouterWithSQLite(db)
  res := httptest.NewRecorder()
  router.ServeHTTP(res, signedAssistantRequest(adminJWT(), `{"message":"把 prod 的 orders topic retention 改成 72 小时"}`))
  require.Equal(t, http.StatusOK, res.Code)
  require.Equal(t, 1, countRows(db, "action_plans"))
}
```

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./tests/e2e -run TestAssistantWriteMessageStoresPendingPlanInSQLite`

Expected: FAIL until helper wiring exists.

- [ ] **Step 3: Implement e2e wiring and README**

README must document:
- deterministic planner is default;
- Eino is the planned Go-native planner adapter;
- Eino output remains untrusted candidate intent;
- local tests use SQLite;
- MySQL production/integration remains unchanged.

- [ ] **Step 4: Run acceptance**

Run: `go test -count=1 ./... && go vet ./... && git diff --check`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tests/e2e/assistant_test.go README.md
git commit -m "test: cover assistant endpoint with sqlite e2e"
```

## Plan Self-Review

- The plan implements the Eino-ready assistant endpoint while keeping external
  model calls out of scope.
- The planner boundary is explicit and treats deterministic or future Eino
  output as candidate data only.
- The plan covers parser tests, service tests, HTTP tests, SQLite e2e, and docs.
- Existing Go safety boundaries remain authoritative: tools, policy, plans,
  execution, audit.
