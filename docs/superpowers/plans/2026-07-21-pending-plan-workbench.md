# Pending Plan Workbench Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a production-shaped pending action plan workbench so authorized operators can list pending plans, inspect details, and confirm execution without exposing confirmation tokens in production.

**Architecture:** Extend the existing action plan store with a typed list query, add HTTP list/detail routes beside the existing confirm route, and render a compact pending-plan workbench in the React console. Authorization stays server-side: HTTP handlers authenticate JWTs, resolve registered tools, extract the immutable plan environment, and filter by `CurrentUser.AllowedEnvironments`; confirmation and execution continue to use the existing services.

**Tech Stack:** Go 1.24, net/http, existing HMAC JWT authenticator, SQLite local/e2e tests through `store.NewSQLActionPlanStore`, MySQL-compatible SQL, React, TypeScript, Vitest, Vite.

## Global Constraints

- Do not expose `confirmation_token` in production API responses.
- Keep `COPILOT_DEV_EXPOSE_CONFIRMATION_TOKEN=1` as the only development-token escape hatch.
- Do not change immutable plan input, confirmation-token hash storage, idempotency keys, or audit semantics.
- Do not add multi-person approval, comments, external connectors, or new operational write tools.
- Do not trust permissions from JWT claims; use server-side role and environment checks.
- Clients must not resubmit operational write parameters during confirmation.
- Local tests use SQLite; MySQL production/integration remains unchanged.

---

## File Structure

- `internal/store/action_plans.go`: add `PlanFilter`, `ListPlans`, memory list implementation, SQL list implementation.
- `internal/store/db_test.go`: add SQL store coverage for pending-only list behavior.
- `internal/httpapi/router.go`: add action plan query service interface, `WithActionPlans`, `GET /v1/action-plans`, and `GET /v1/action-plans/{id}` handlers.
- `internal/httpapi/router_test.go`: add list/detail authorization and response tests.
- `tests/e2e/assistant_test.go`: extend SQLite e2e coverage so assistant-created plans are visible through pending-list API.
- `apps/console/src/App.tsx`: add pending plan types, list/detail state, refresh/select/confirm actions, and workbench UI.
- `apps/console/src/App.test.tsx`: add workbench rendering, detail, production-token message, and confirm-flow tests.
- `apps/console/src/styles.css`: add compact styles for pending plan list/detail.

### Task 1: Store Pending Plan Listing

**Files:**
- Modify: `internal/store/action_plans.go`
- Modify: `internal/store/db_test.go`

**Interfaces:**
- Consumes: existing `store.PlanRecord`, `store.PlanStatus`, `store.ActionPlanStore`.
- Produces:
  - `type PlanFilter struct { Status PlanStatus }`
  - `func (s *MemoryActionPlanStore) ListPlans(context.Context, PlanFilter) ([]PlanRecord, error)`
  - `func (s *MySQLActionPlanStore) ListPlans(context.Context, PlanFilter) ([]PlanRecord, error)`
  - Add `ListPlans(context.Context, PlanFilter) ([]PlanRecord, error)` to `ActionPlanStore`.

- [ ] **Step 1: Write failing memory store test**

Add this test to `internal/store/db_test.go`:

```go
func TestMemoryStoreListPlansFiltersByStatus(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryActionPlanStore()
	pending := PlanRecord{ID: "pending-plan", ToolName: "topic.retention.set", InputJSON: []byte(`{"environment":"prod"}`), RiskLevel: "medium", Status: PlanPendingConfirmation, Version: 1, ExpiresAt: time.Now().Add(time.Minute)}
	confirmed := PlanRecord{ID: "confirmed-plan", ToolName: "topic.retention.set", InputJSON: []byte(`{"environment":"prod"}`), RiskLevel: "medium", Status: PlanConfirmed, Version: 2, ExpiresAt: time.Now().Add(time.Minute)}
	if err := repository.CreatePlan(ctx, pending, AuditEvent{ID: "audit-pending", PlanID: pending.ID, Action: "plan_created", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("create pending plan: %v", err)
	}
	if err := repository.CreatePlan(ctx, confirmed, AuditEvent{ID: "audit-confirmed", PlanID: confirmed.ID, Action: "plan_created", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("create confirmed plan: %v", err)
	}

	plans, err := repository.ListPlans(ctx, PlanFilter{Status: PlanPendingConfirmation})
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	if len(plans) != 1 || plans[0].ID != pending.ID {
		t.Fatalf("plans = %+v, want only pending plan", plans)
	}
	plans[0].InputJSON[0] = 'X'
	stored, err := repository.GetPlan(ctx, pending.ID)
	if err != nil {
		t.Fatalf("get stored plan: %v", err)
	}
	if string(stored.InputJSON) != `{"environment":"prod"}` {
		t.Fatalf("stored input mutated to %q", stored.InputJSON)
	}
}
```

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/store -run TestMemoryStoreListPlansFiltersByStatus`

Expected: FAIL because `PlanFilter` and `ListPlans` do not exist.

- [ ] **Step 3: Implement memory list support**

In `internal/store/action_plans.go`, add:

```go
type PlanFilter struct {
	Status PlanStatus
}
```

Add `ListPlans(context.Context, PlanFilter) ([]PlanRecord, error)` to `ActionPlanStore`.

Implement:

```go
func (s *MemoryActionPlanStore) ListPlans(_ context.Context, filter PlanFilter) ([]PlanRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	plans := make([]PlanRecord, 0, len(s.plans))
	for _, plan := range s.plans {
		if filter.Status != "" && plan.Status != filter.Status {
			continue
		}
		plans = append(plans, clonePlan(plan))
	}
	return plans, nil
}
```

- [ ] **Step 4: Run GREEN for memory store**

Run: `go test -count=1 ./internal/store -run TestMemoryStoreListPlansFiltersByStatus`

Expected: PASS.

- [ ] **Step 5: Write failing SQL store test**

Add this test to `internal/store/db_test.go`:

```go
func TestSQLStoreListPlansFiltersByStatus(t *testing.T) {
	db := openSQLite(t)
	repository := NewSQLActionPlanStore(db)
	ctx := context.Background()
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	pending := PlanRecord{ID: "pending-sql-plan", RequestID: "request-1", CreatedBy: "admin-1", ToolName: "topic.retention.set", InputJSON: []byte(`{"environment":"prod"}`), InputHash: "hash-1", RiskLevel: "medium", Status: PlanPendingConfirmation, Version: 1, ExpiresAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now}
	confirmed := PlanRecord{ID: "confirmed-sql-plan", RequestID: "request-2", CreatedBy: "admin-1", ToolName: "topic.retention.set", InputJSON: []byte(`{"environment":"prod"}`), InputHash: "hash-2", RiskLevel: "medium", Status: PlanConfirmed, Version: 2, ExpiresAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now}
	if err := repository.CreatePlan(ctx, pending, AuditEvent{ID: "audit-sql-pending", PlanID: pending.ID, RequestID: pending.RequestID, Subject: pending.CreatedBy, ToolName: pending.ToolName, Action: "plan_created", Decision: "permitted", CreatedAt: now}); err != nil {
		t.Fatalf("create pending plan: %v", err)
	}
	if err := repository.CreatePlan(ctx, confirmed, AuditEvent{ID: "audit-sql-confirmed", PlanID: confirmed.ID, RequestID: confirmed.RequestID, Subject: confirmed.CreatedBy, ToolName: confirmed.ToolName, Action: "plan_created", Decision: "permitted", CreatedAt: now}); err != nil {
		t.Fatalf("create confirmed plan: %v", err)
	}

	plans, err := repository.ListPlans(ctx, PlanFilter{Status: PlanPendingConfirmation})
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	if len(plans) != 1 || plans[0].ID != pending.ID {
		t.Fatalf("plans = %+v, want only pending SQL plan", plans)
	}
}
```

- [ ] **Step 6: Run RED for SQL**

Run: `go test -count=1 ./internal/store -run TestSQLStoreListPlansFiltersByStatus`

Expected: FAIL because SQL `ListPlans` is not implemented.

- [ ] **Step 7: Implement SQL list support**

Add this implementation to `internal/store/action_plans.go`:

```go
func (s *MySQLActionPlanStore) ListPlans(ctx context.Context, filter PlanFilter) ([]PlanRecord, error) {
	query := `SELECT id, request_id, created_by, tool_name, input_json, input_hash, risk_level, status, version, confirmation_token_hash, confirmed_by, confirmed_at, expires_at, created_at, updated_at FROM action_plans`
	args := []any{}
	if filter.Status != "" {
		query += ` WHERE status = ?`
		args = append(args, filter.Status)
	}
	query += ` ORDER BY created_at DESC, id DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var plans []PlanRecord
	for rows.Next() {
		plan, err := scanPlanRows(rows)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return plans, nil
}
```

Add a scanner for `*sql.Rows`:

```go
func scanPlanRows(rows *sql.Rows) (PlanRecord, error) {
	var plan PlanRecord
	var token, confirmedBy sql.NullString
	var confirmedAt sql.NullTime
	err := rows.Scan(&plan.ID, &plan.RequestID, &plan.CreatedBy, &plan.ToolName, &plan.InputJSON, &plan.InputHash, &plan.RiskLevel, &plan.Status, &plan.Version, &token, &confirmedBy, &confirmedAt, &plan.ExpiresAt, &plan.CreatedAt, &plan.UpdatedAt)
	if err != nil {
		return PlanRecord{}, err
	}
	plan.ConfirmationTokenHash = token.String
	plan.ConfirmedBy = confirmedBy.String
	if confirmedAt.Valid {
		plan.ConfirmedAt = pointerTo(confirmedAt.Time)
	}
	return plan, nil
}
```

- [ ] **Step 8: Run store tests**

Run: `go test -count=1 ./internal/store`

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/store/action_plans.go internal/store/db_test.go
git commit -m "feat: list action plans from store"
```

### Task 2: HTTP Pending Plan List and Detail

**Files:**
- Modify: `internal/httpapi/router.go`
- Modify: `internal/httpapi/router_test.go`

**Interfaces:**
- Consumes:
  - `store.ActionPlanStore.ListPlans(context.Context, store.PlanFilter) ([]store.PlanRecord, error)`
  - `store.ActionPlanStore.GetPlan(context.Context, string) (store.PlanRecord, error)`
  - `plans.DecodeInput([]byte) (map[string]any, error)`
  - `tools.Lookup(string) (tools.Tool, bool)`
- Produces:
  - `type ActionPlanQueryService interface { ListPlans(context.Context, store.PlanFilter) ([]store.PlanRecord, error); GetPlan(context.Context, string) (store.PlanRecord, error) }`
  - `func WithActionPlans(service ActionPlanQueryService) Option`
  - `GET /v1/action-plans?status=pending_confirmation`
  - `GET /v1/action-plans/{id}`

- [ ] **Step 1: Write failing authentication test**

Add to `internal/httpapi/router_test.go`:

```go
func TestListActionPlansRequiresAuthentication(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/action-plans?status=pending_confirmation", nil))

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
}
```

- [ ] **Step 2: Write failing list behavior tests**

Add:

```go
func TestListActionPlansReturnsOnlyAllowedPendingPlans(t *testing.T) {
	t.Parallel()
	router, _, planService := testRouterWithPlans(t, &readRunner{})
	prodPlan := createPendingPlan(t, planService)
	stagingInput := map[string]any{"environment": "staging", "topic": "orders", "retention_hours": 72}
	decision := policy.Evaluate(identity.CurrentUser{Subject: "admin-1", Roles: []string{"admin"}, AllowedEnvironments: []string{"staging"}, RequestID: "request-staging"}, tool(t, tools.TopicRetentionSet), stagingInput)
	stagingPlan, err := planService.CreatePlan(context.Background(), identity.CurrentUser{Subject: "admin-1", Roles: []string{"admin"}, AllowedEnvironments: []string{"staging"}, RequestID: "request-staging"}, decision, stagingInput)
	if err != nil {
		t.Fatalf("create staging plan: %v", err)
	}
	req := signedRequest(t, "/v1/action-plans?status=pending_confirmation", "", "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, prodPlan.ID) {
		t.Fatalf("body = %s, want prod plan", body)
	}
	if strings.Contains(body, stagingPlan.ID) {
		t.Fatalf("body = %s, must not include staging plan", body)
	}
	if strings.Contains(body, "confirmation_token") {
		t.Fatalf("body = %s, must not expose confirmation token", body)
	}
}

func TestListActionPlansRejectsUnsupportedStatus(t *testing.T) {
	t.Parallel()
	router, _ := testRouter(t, &readRunner{})
	req := signedRequest(t, "/v1/action-plans?status=confirmed", "", "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", res.Code, res.Body.String())
	}
}
```

- [ ] **Step 3: Write failing detail behavior tests**

Add:

```go
func TestGetActionPlanReturnsDetailForAllowedEnvironment(t *testing.T) {
	t.Parallel()
	router, _, planService := testRouterWithPlans(t, &readRunner{})
	plan := createPendingPlan(t, planService)
	req := signedRequest(t, "/v1/action-plans/"+plan.ID, "", "admin-1", []string{"admin"}, []string{"prod"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	body := res.Body.String()
	for _, want := range []string{`"id":"` + plan.ID + `"`, `"tool":"topic.retention.set"`, `"environment":"prod"`, `"topic":"orders"`, `"retention_hours":72`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want %s", body, want)
		}
	}
	if strings.Contains(body, "confirmation_token") {
		t.Fatalf("body = %s, must not expose confirmation token", body)
	}
}

func TestGetActionPlanRejectsDisallowedEnvironment(t *testing.T) {
	t.Parallel()
	router, _, planService := testRouterWithPlans(t, &readRunner{})
	plan := createPendingPlan(t, planService)
	req := signedRequest(t, "/v1/action-plans/"+plan.ID, "", "admin-1", []string{"admin"}, []string{"staging"})
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
	}
}
```

- [ ] **Step 4: Run RED**

Run: `go test -count=1 ./internal/httpapi -run 'Test(ListActionPlans|GetActionPlan)'`

Expected: FAIL because list/detail routes do not exist.

- [ ] **Step 5: Implement router dependencies and route dispatch**

In `internal/httpapi/router.go`, import `github.com/gracegaoya/ai-operations-copilot/internal/store`, `github.com/gracegaoya/ai-operations-copilot/internal/tools`, and `github.com/gracegaoya/ai-operations-copilot/internal/policy`.

Add:

```go
type ActionPlanQueryService interface {
	ListPlans(context.Context, store.PlanFilter) ([]store.PlanRecord, error)
	GetPlan(context.Context, string) (store.PlanRecord, error)
}
```

Add `actionPlans ActionPlanQueryService` to `Router`.

Add:

```go
func WithActionPlans(service ActionPlanQueryService) Option {
	return func(router *Router) {
		router.actionPlans = service
	}
}
```

In `ServeHTTP`, before the existing confirm route:

```go
if request.Method == http.MethodGet && request.URL.Path == "/v1/action-plans" {
	r.serveListActionPlans(writer, request)
	return
}
if request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/v1/action-plans/") {
	r.serveGetActionPlan(writer, request)
	return
}
```

- [ ] **Step 6: Implement response shaping helpers**

Add:

```go
type actionPlanResponse struct {
	ID        string         `json:"id"`
	Tool      string         `json:"tool"`
	Environment string      `json:"environment"`
	Risk      string         `json:"risk"`
	Status    string         `json:"status"`
	Version   uint           `json:"version"`
	ExpiresAt time.Time     `json:"expires_at"`
	CreatedBy string        `json:"created_by"`
	CreatedAt time.Time     `json:"created_at"`
	Input     map[string]any `json:"input,omitempty"`
}

func shapeActionPlan(plan store.PlanRecord, includeInput bool) (actionPlanResponse, bool) {
	input, err := plans.DecodeInput(plan.InputJSON)
	if err != nil {
		return actionPlanResponse{}, false
	}
	environment, ok := input["environment"].(string)
	if !ok || strings.TrimSpace(environment) == "" {
		return actionPlanResponse{}, false
	}
	if _, ok := tools.Lookup(plan.ToolName); !ok {
		return actionPlanResponse{}, false
	}
	response := actionPlanResponse{
		ID: plan.ID, Tool: plan.ToolName, Environment: environment,
		Risk: plan.RiskLevel, Status: string(plan.Status), Version: plan.Version,
		ExpiresAt: plan.ExpiresAt, CreatedBy: plan.CreatedBy, CreatedAt: plan.CreatedAt,
	}
	if includeInput {
		response.Input = input
	}
	return response, true
}
```

Add:

```go
func userAllowedEnvironment(user identity.CurrentUser, environment string) bool {
	for _, allowed := range user.AllowedEnvironments {
		if allowed == environment {
			return true
		}
	}
	return false
}
```

- [ ] **Step 7: Implement list and detail handlers**

Add:

```go
func (r *Router) serveListActionPlans(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil || r.actionPlans == nil {
		writeError(writer, http.StatusInternalServerError, "router is not configured")
		return
	}
	user, err := r.auth.Authenticate(request)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "authentication required")
		return
	}
	status := request.URL.Query().Get("status")
	if status != string(store.PlanPendingConfirmation) {
		writeError(writer, http.StatusBadRequest, "status must be pending_confirmation")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	records, err := r.actionPlans.ListPlans(ctx, store.PlanFilter{Status: store.PlanPendingConfirmation})
	if err != nil {
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	responses := []actionPlanResponse{}
	for _, record := range records {
		response, ok := shapeActionPlan(record, false)
		if !ok || !userAllowedEnvironment(user, response.Environment) {
			continue
		}
		responses = append(responses, response)
	}
	writeCappedJSON(writer, map[string]any{"plans": responses})
}

func (r *Router) serveGetActionPlan(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil || r.actionPlans == nil {
		writeError(writer, http.StatusInternalServerError, "router is not configured")
		return
	}
	user, err := r.auth.Authenticate(request)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "authentication required")
		return
	}
	planID := strings.TrimPrefix(request.URL.Path, "/v1/action-plans/")
	if strings.TrimSpace(planID) == "" || strings.Contains(planID, "/") {
		http.NotFound(writer, request)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	record, err := r.actionPlans.GetPlan(ctx, planID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(writer, request)
			return
		}
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	response, ok := shapeActionPlan(record, true)
	if !ok {
		http.NotFound(writer, request)
		return
	}
	if !userAllowedEnvironment(user, response.Environment) {
		writeError(writer, http.StatusForbidden, string(policy.EnvironmentDenied))
		return
	}
	writeCappedJSON(writer, response)
}
```

- [ ] **Step 8: Wire tests to query service**

Update `testRouterWithPlans` in `internal/httpapi/router_test.go`:

```go
return httpapi.NewRouter(
	httpapi.NewHMACAuthenticator([]byte("test-secret")),
	readService,
	httpapi.WithAssistant(assistantService),
	httpapi.WithActionPlans(repository),
	httpapi.WithActionPlanConfirmation(planService, executionService),
), repository, planService
```

- [ ] **Step 9: Run GREEN**

Run: `go test -count=1 ./internal/httpapi`

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/httpapi/router.go internal/httpapi/router_test.go
git commit -m "feat: expose pending action plan queries"
```

### Task 3: SQLite E2E Pending Plan Query

**Files:**
- Modify: `tests/e2e/assistant_test.go`

**Interfaces:**
- Consumes: `GET /v1/action-plans?status=pending_confirmation` from Task 2.
- Produces: SQLite-backed e2e proof that assistant-created write plans are visible through the pending list API.

- [ ] **Step 1: Write failing e2e assertion**

Extend `TestAssistantWriteMessageStoresPendingPlanInSQLite` after the direct database count:

```go
listReq := httptest.NewRequest(http.MethodGet, "/v1/action-plans?status=pending_confirmation", nil)
listReq.Header.Set("Authorization", "Bearer "+signedAdminJWT(t))
listReq.Header.Set("X-Request-ID", "assistant-e2e-list-request")
listRes := httptest.NewRecorder()

router.ServeHTTP(listRes, listReq)

if listRes.Code != http.StatusOK {
	t.Fatalf("list status = %d body = %s, want 200", listRes.Code, listRes.Body.String())
}
if !strings.Contains(listRes.Body.String(), `"tool":"topic.retention.set"`) || !strings.Contains(listRes.Body.String(), `"environment":"prod"`) {
	t.Fatalf("list body = %s, want pending prod retention plan", listRes.Body.String())
}
if strings.Contains(listRes.Body.String(), "confirmation_token") {
	t.Fatalf("list body = %s, must not expose confirmation token", listRes.Body.String())
}
```

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./tests/e2e -run TestAssistantWriteMessageStoresPendingPlanInSQLite`

Expected: FAIL if the e2e router has not been wired with `WithActionPlans`.

- [ ] **Step 3: Wire e2e router with query service**

Update router construction in `tests/e2e/assistant_test.go`:

```go
router := httpapi.NewRouter(
	httpapi.NewHMACAuthenticator([]byte("test-secret")),
	readService,
	httpapi.WithAssistant(assistant.NewService(assistant.DeterministicPlanner{}, readService, planService)),
	httpapi.WithActionPlans(repository),
)
```

- [ ] **Step 4: Run GREEN**

Run: `go test -count=1 ./tests/e2e -run TestAssistantWriteMessageStoresPendingPlanInSQLite`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tests/e2e/assistant_test.go
git commit -m "test: cover pending plan list e2e"
```

### Task 4: Console Pending Plan Workbench

**Files:**
- Modify: `apps/console/src/App.tsx`
- Modify: `apps/console/src/App.test.tsx`
- Modify: `apps/console/src/styles.css`

**Interfaces:**
- Consumes:
  - `GET /v1/action-plans?status=pending_confirmation` response `{ plans: PendingPlanSummary[] }`
  - `GET /v1/action-plans/{id}` response `PendingPlanDetail`
  - existing `POST /v1/action-plans/{id}/confirm`
- Produces:
  - `type PendingPlanSummary`
  - `type PendingPlanDetail`
  - A compact "待确认计划" UI with refresh, list, detail, and confirm behavior.

- [ ] **Step 1: Write failing pending list test**

Add to `apps/console/src/App.test.tsx`:

```tsx
it("loads and renders pending plans in the workbench", async () => {
  const fetchMock = vi.mocked(fetch);
  fetchMock.mockResolvedValueOnce(
    new Response(
      JSON.stringify({
        plans: [
          {
            id: "plan-123",
            tool: "topic.retention.set",
            environment: "prod",
            risk: "medium",
            status: "pending_confirmation",
            version: 1,
            expires_at: "2026-07-21T12:10:00Z",
            created_by: "admin-1",
            created_at: "2026-07-21T12:00:00Z"
          }
        ]
      }),
      { status: 200, headers: { "Content-Type": "application/json" } }
    )
  );
  render(<App />);

  await userEvent.click(screen.getByRole("button", { name: "刷新计划" }));

  await waitFor(() => expect(screen.getByText("plan-123")).toBeInTheDocument());
  expect(screen.getByText("topic.retention.set")).toBeInTheDocument();
  expect(fetchMock).toHaveBeenCalledWith(
    "/api/v1/action-plans?status=pending_confirmation",
    expect.objectContaining({ method: "GET" })
  );
});
```

- [ ] **Step 2: Write failing detail test**

Add:

```tsx
it("opens pending plan details", async () => {
  const fetchMock = vi.mocked(fetch);
  fetchMock
    .mockResolvedValueOnce(
      new Response(JSON.stringify({ plans: [{ id: "plan-123", tool: "topic.retention.set", environment: "prod", risk: "medium", status: "pending_confirmation", version: 1, expires_at: "2026-07-21T12:10:00Z", created_by: "admin-1", created_at: "2026-07-21T12:00:00Z" }] }), {
        status: 200,
        headers: { "Content-Type": "application/json" }
      })
    )
    .mockResolvedValueOnce(
      new Response(JSON.stringify({ id: "plan-123", tool: "topic.retention.set", environment: "prod", risk: "medium", status: "pending_confirmation", version: 1, expires_at: "2026-07-21T12:10:00Z", created_by: "admin-1", created_at: "2026-07-21T12:00:00Z", input: { environment: "prod", topic: "orders", retention_hours: 72 } }), {
        status: 200,
        headers: { "Content-Type": "application/json" }
      })
    );
  render(<App />);

  await userEvent.click(screen.getByRole("button", { name: "刷新计划" }));
  await userEvent.click(await screen.findByRole("button", { name: /plan-123/ }));

  await waitFor(() => expect(screen.getByText("orders")).toBeInTheDocument());
  expect(screen.getByText("retention_hours")).toBeInTheDocument();
});
```

- [ ] **Step 3: Write failing production-token message test**

Add:

```tsx
it("explains that production confirmation needs an approval token", async () => {
  const fetchMock = vi.mocked(fetch);
  fetchMock
    .mockResolvedValueOnce(new Response(JSON.stringify({ plans: [{ id: "plan-123", tool: "topic.retention.set", environment: "prod", risk: "medium", status: "pending_confirmation", version: 1, expires_at: "2026-07-21T12:10:00Z", created_by: "admin-1", created_at: "2026-07-21T12:00:00Z" }] }), { status: 200, headers: { "Content-Type": "application/json" } }))
    .mockResolvedValueOnce(new Response(JSON.stringify({ id: "plan-123", tool: "topic.retention.set", environment: "prod", risk: "medium", status: "pending_confirmation", version: 1, expires_at: "2026-07-21T12:10:00Z", created_by: "admin-1", created_at: "2026-07-21T12:00:00Z", input: { environment: "prod", topic: "orders", retention_hours: 72 } }), { status: 200, headers: { "Content-Type": "application/json" } }));
  render(<App />);

  await userEvent.click(screen.getByRole("button", { name: "刷新计划" }));
  await userEvent.click(await screen.findByRole("button", { name: /plan-123/ }));

  await waitFor(() => expect(screen.getByText(/生产环境需要外部审批 token/)).toBeInTheDocument());
});
```

- [ ] **Step 4: Write failing workbench confirm test**

Add:

```tsx
it("confirms a selected pending plan when a development token was captured", async () => {
  const fetchMock = vi.mocked(fetch);
  fetchMock
    .mockResolvedValueOnce(new Response(JSON.stringify({ type: "confirmation_required", tool: "topic.retention.set", plan_id: "plan-123", status: "pending_confirmation", version: 1, expires_at: "2026-07-21T12:10:00Z", summary: "Set topic orders retention in prod to 72 hours.", confirmation_token: "token-123" }), { status: 200, headers: { "Content-Type": "application/json" } }))
    .mockResolvedValueOnce(new Response(JSON.stringify({ id: "plan-123", tool: "topic.retention.set", environment: "prod", risk: "medium", status: "pending_confirmation", version: 1, expires_at: "2026-07-21T12:10:00Z", created_by: "admin-1", created_at: "2026-07-21T12:00:00Z", input: { environment: "prod", topic: "orders", retention_hours: 72 } }), { status: 200, headers: { "Content-Type": "application/json" } }))
    .mockResolvedValueOnce(new Response(JSON.stringify({ type: "execution_result", plan_id: "plan-123", execution_id: "execution-123", status: "succeeded", reused: false }), { status: 200, headers: { "Content-Type": "application/json" } }));
  render(<App />);

  await userEvent.clear(screen.getByLabelText("指令"));
  await userEvent.type(screen.getByLabelText("指令"), "把 prod 的 orders topic retention 改成 72 小时");
  await userEvent.click(screen.getByRole("button", { name: "发送" }));
  await userEvent.click(await screen.findByRole("button", { name: /plan-123/ }));
  await userEvent.click(screen.getByRole("button", { name: "确认选中计划" }));

  await waitFor(() => expect(screen.getByText("execution-123")).toBeInTheDocument());
  expect(fetchMock).toHaveBeenLastCalledWith(
    "/api/v1/action-plans/plan-123/confirm",
    expect.objectContaining({
      method: "POST",
      body: JSON.stringify({ expected_version: 1, confirmation_token: "token-123" })
    })
  );
});
```

- [ ] **Step 5: Run RED**

Run: `npm test -- --run`

Expected: FAIL because the workbench UI and fetch actions do not exist.

- [ ] **Step 6: Add pending plan types and state**

In `apps/console/src/App.tsx`, add:

```tsx
type PendingPlanSummary = {
  id: string;
  tool: string;
  environment: string;
  risk: string;
  status: string;
  version: number;
  expires_at: string;
  created_by: string;
  created_at: string;
};

type PendingPlanDetail = PendingPlanSummary & {
  input: Record<string, unknown>;
};
```

Add component state:

```tsx
const [pendingPlans, setPendingPlans] = useState<PendingPlanSummary[]>([]);
const [selectedPlan, setSelectedPlan] = useState<PendingPlanDetail | null>(null);
const [plansLoading, setPlansLoading] = useState(false);
const [plansError, setPlansError] = useState("");
const [planTokens, setPlanTokens] = useState<Record<string, string>>({});
```

- [ ] **Step 7: Capture development token from assistant responses**

After successful assistant response handling in `submit`, add:

```tsx
if ((payload as AssistantResponse).type === "confirmation_required" && (payload as Extract<AssistantResponse, { type: "confirmation_required" }>).confirmation_token) {
  const plan = payload as Extract<AssistantResponse, { type: "confirmation_required" }>;
  setPlanTokens((tokens) => ({ ...tokens, [plan.plan_id]: plan.confirmation_token ?? "" }));
}
```

- [ ] **Step 8: Add plan API actions**

Add:

```tsx
async function refreshPendingPlans() {
  setPlansLoading(true);
  setPlansError("");
  try {
    const result = await fetch(`${apiBase}/v1/action-plans?status=pending_confirmation`, {
      method: "GET",
      headers: await requestHeaders(subject, role, environment, jwtSecret)
    });
    const payload = await parseJSONResponse<{ plans: PendingPlanSummary[] }>(result, `${apiBase}/v1/action-plans?status=pending_confirmation`);
    if ("error" in payload) {
      throw new Error(payload.error);
    }
    if (!result.ok) {
      throw new Error(`HTTP ${result.status}`);
    }
    setPendingPlans(payload.plans);
  } catch (caught) {
    setPlansError(caught instanceof Error ? caught.message : "加载待确认计划失败");
  } finally {
    setPlansLoading(false);
  }
}

async function selectPendingPlan(planID: string) {
  setPlansError("");
  try {
    const result = await fetch(`${apiBase}/v1/action-plans/${planID}`, {
      method: "GET",
      headers: await requestHeaders(subject, role, environment, jwtSecret)
    });
    const payload = await parseJSONResponse<PendingPlanDetail>(result, `${apiBase}/v1/action-plans/${planID}`);
    if ("error" in payload) {
      throw new Error(payload.error);
    }
    if (!result.ok) {
      throw new Error(`HTTP ${result.status}`);
    }
    setSelectedPlan(payload);
  } catch (caught) {
    setPlansError(caught instanceof Error ? caught.message : "加载计划详情失败");
  }
}
```

Add a generic parser and keep `parseAssistantResponse` as a wrapper:

```tsx
async function parseJSONResponse<T>(result: Response, endpoint: string): Promise<T | { error: string }> {
  const contentType = result.headers.get("Content-Type") ?? "";
  if (contentType.toLowerCase().includes("application/json")) {
    return (await result.json()) as T | { error: string };
  }
  const preview = (await result.text()).trim().slice(0, 80);
  let hint = "API 返回了非 JSON 响应。请检查后端服务和 API 地址。";
  if (preview.includes("ECONNREFUSED")) {
    hint = "后端没有启动，或前端代理端口不对。请确认 Go API 正在监听 18080。";
  } else if (preview.startsWith("<!DOCTYPE") || preview.startsWith("<html")) {
    hint = "API 返回了 HTML，不是 JSON。请检查后端是否已启动、Vite 代理是否命中，以及 API 地址是否填写为 /api。";
  }
  return { error: `${hint} 当前请求：${endpoint}，HTTP ${result.status}。` };
}

async function parseAssistantResponse(result: Response, endpoint: string): Promise<AssistantResponse | { error: string }> {
  return parseJSONResponse<AssistantResponse>(result, endpoint);
}
```

- [ ] **Step 9: Add selected-plan confirmation action**

Add:

```tsx
async function confirmSelectedPlan() {
  if (!selectedPlan) {
    return;
  }
  const token = planTokens[selectedPlan.id];
  if (!token) {
    setPlansError("生产环境需要外部审批 token；本地演示请用 COPILOT_DEV_EXPOSE_CONFIRMATION_TOKEN=1 创建计划。");
    return;
  }
  setConfirming(true);
  setPlansError("");
  try {
    const result = await fetch(`${apiBase}/v1/action-plans/${selectedPlan.id}/confirm`, {
      method: "POST",
      headers: await requestHeaders(subject, role, environment, jwtSecret),
      body: JSON.stringify({ expected_version: selectedPlan.version, confirmation_token: token })
    });
    const payload = await parseAssistantResponse(result, `${apiBase}/v1/action-plans/${selectedPlan.id}/confirm`);
    if ("error" in payload) {
      throw new Error(payload.error);
    }
    if (!result.ok) {
      throw new Error(`HTTP ${result.status}`);
    }
    setResponse(payload as AssistantResponse);
    setSelectedPlan(null);
    setPendingPlans((plans) => plans.filter((plan) => plan.id !== selectedPlan.id));
  } catch (caught) {
    setPlansError(caught instanceof Error ? caught.message : "确认执行失败");
  } finally {
    setConfirming(false);
  }
}
```

- [ ] **Step 10: Render workbench UI**

Inside the main workbench layout, add a compact pending plan panel:

```tsx
<aside className="panel plansPanel">
  <div className="panelHeader">
    <h2>待确认计划</h2>
    <button type="button" onClick={refreshPendingPlans} disabled={plansLoading}>
      {plansLoading ? "刷新中" : "刷新计划"}
    </button>
  </div>
  <div className="planList">
    {pendingPlans.map((plan) => (
      <button type="button" className="planRow" key={plan.id} onClick={() => void selectPendingPlan(plan.id)}>
        <span>{plan.id}</span>
        <strong>{plan.tool}</strong>
        <small>{plan.environment} · {plan.risk} · {plan.status}</small>
      </button>
    ))}
    {pendingPlans.length === 0 ? <p className="hint">暂无待确认计划。</p> : null}
  </div>
  {selectedPlan ? (
    <div className="planDetail">
      <h3>{selectedPlan.id}</h3>
      <dl>
        <div><dt>tool</dt><dd>{selectedPlan.tool}</dd></div>
        <div><dt>environment</dt><dd>{selectedPlan.environment}</dd></div>
        <div><dt>version</dt><dd>{selectedPlan.version}</dd></div>
        {Object.entries(selectedPlan.input).map(([key, value]) => (
          <div key={key}><dt>{key}</dt><dd>{String(value)}</dd></div>
        ))}
      </dl>
      {!planTokens[selectedPlan.id] ? <p className="hint">生产环境需要外部审批 token。</p> : null}
      <button type="button" onClick={confirmSelectedPlan} disabled={confirming || !planTokens[selectedPlan.id]}>
        {confirming ? "确认中" : "确认选中计划"}
      </button>
    </div>
  ) : null}
  {plansError ? <p className="error">{plansError}</p> : null}
</aside>
```

- [ ] **Step 11: Add CSS**

In `apps/console/src/styles.css`, add styles for:

```css
.plansPanel {
  display: grid;
  grid-template-rows: auto minmax(160px, 1fr) auto;
  gap: 16px;
}

.planList {
  display: grid;
  gap: 8px;
  align-content: start;
}

.planRow {
  display: grid;
  gap: 4px;
  width: 100%;
  text-align: left;
}

.planRow span,
.planRow small {
  overflow-wrap: anywhere;
}

.planDetail {
  border-top: 1px solid rgba(255, 255, 255, 0.12);
  padding-top: 14px;
}
```

Adjust `.workbench` grid so four panels remain usable on desktop and stack on narrow screens:

```css
.workbench {
  grid-template-columns: minmax(220px, 0.8fr) minmax(320px, 1.35fr) minmax(260px, 1fr) minmax(260px, 1fr);
}
```

- [ ] **Step 12: Run frontend tests**

Run: `npm test -- --run`

Expected: PASS.

- [ ] **Step 13: Run frontend build**

Run: `npm run build`

Expected: PASS.

- [ ] **Step 14: Commit**

```bash
git add apps/console/src/App.tsx apps/console/src/App.test.tsx apps/console/src/styles.css
git commit -m "feat: add pending plan workbench"
```

### Task 5: Final Acceptance

**Files:**
- No new files.
- Verify all touched code.

**Interfaces:**
- Consumes: store list support, HTTP action plan list/detail routes, SQLite e2e coverage, and the React pending plan workbench.
- Produces: verified implementation ready for review.

- [ ] **Step 1: Run full Go tests**

Run: `go test -count=1 ./...`

Expected: PASS.

- [ ] **Step 2: Run Go vet**

Run: `go vet ./...`

Expected: PASS.

- [ ] **Step 3: Run diff whitespace check**

Run: `git diff --check`

Expected: PASS with no output.

- [ ] **Step 4: Run console tests**

Run from `apps/console`: `npm test -- --run`

Expected: PASS.

- [ ] **Step 5: Run console build**

Run from `apps/console`: `npm run build`

Expected: PASS.

- [ ] **Step 6: Inspect production token exposure**

Run:

```bash
rg -n "confirmation_token" internal apps/console/src README.md
```

Expected:

- Production assistant serialization still omits `confirmation_token`.
- `confirmation_token` appears only in confirm request bodies, tests, development-token branch, and local-demo documentation.

- [ ] **Step 7: Commit any verification-only doc updates**

If no files changed during acceptance, do not commit. If README or docs were updated during execution, run:

```bash
git add README.md docs
git commit -m "docs: update pending plan workbench notes"
```
