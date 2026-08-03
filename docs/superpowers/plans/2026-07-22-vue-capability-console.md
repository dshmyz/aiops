# Vue Capability Console Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a standalone Vue console and Go management API so operators can manually create, validate, test, publish, and unpublish read capabilities without Swagger.

**Architecture:** Add a focused capability management service under `internal/capabilities` that owns safe YAML file operations and delegates schema checks to the existing `Validate` function. Add thin `/v1/capabilities` handlers to `internal/httpapi` for authentication, role checks, JSON decoding, and response shaping. Add a standalone `apps/capability-console` Vue app that calls the management API and keeps the existing React app unchanged.

**Tech Stack:** Go standard library, `gopkg.in/yaml.v3`, Vue 3, Vite, TypeScript, Vue Router, Element Plus, Vitest, Vue Test Utils.

## Global Constraints

- First version does not require Swagger/OpenAPI import.
- First version publishes only `operation: read` capabilities with backend method `GET`.
- Published capabilities must have an absolute `http` or `https` `backend.base_url`.
- Runtime read capabilities support path parameters only.
- Test preview returns normalized Copilot-visible output, not raw backend JSON.
- Publish moves a capability from `capabilities/discovered` to `capabilities/published`.
- Unpublish moves it back to `capabilities/discovered` and sets `status: needs_review`.
- File-mutating endpoints and the test endpoint require an admin role.
- List/get endpoints may be used by viewer, operator, or admin roles.
- Keep `apps/console` untouched.

---

### Task 1: Capability Management Service

**Files:**
- Create: `internal/capabilities/manage.go`
- Create: `internal/capabilities/manage_test.go`

**Interfaces:**
- Produces: `type Manager struct`
- Produces: `func NewManager(root string, adapter *HTTPAdapter) *Manager`
- Produces: `func (m *Manager) List(ctx context.Context) ([]ManagedCapability, error)`
- Produces: `func (m *Manager) Get(ctx context.Context, name string) (ManagedCapability, error)`
- Produces: `func (m *Manager) SaveDraft(ctx context.Context, capability Capability) (ManagedCapability, error)`
- Produces: `func (m *Manager) ValidateCapability(capability Capability) ValidationResult`
- Produces: `func (m *Manager) Test(ctx context.Context, capability Capability, input map[string]any) (NormalizedResult, error)`
- Produces: `func (m *Manager) Publish(ctx context.Context, name string) (ManagedCapability, error)`
- Produces: `func (m *Manager) Unpublish(ctx context.Context, name string) (ManagedCapability, error)`

- [ ] **Step 1: Write failing service tests**

Cover listing discovered/published files, rejecting path traversal, writing drafts atomically, publishing with move semantics, rejecting write publication, rejecting static/dynamic conflicts, unpublishing with `needs_review`, and testing only valid read GET capabilities.

Run: `go test ./internal/capabilities -run 'TestManager'`

Expected: FAIL because `NewManager` and related types do not exist.

- [ ] **Step 2: Implement minimal management service**

Add safe path helpers, YAML read/write helpers, validation response shaping, publish/unpublish file moves, conflict checks through `tools.Lookup`, and read test execution by temporarily forcing `status: published` before calling the HTTP adapter.

- [ ] **Step 3: Verify service tests pass**

Run: `go test ./internal/capabilities -run 'TestManager'`

Expected: PASS.

### Task 2: Capability HTTP API

**Files:**
- Modify: `internal/httpapi/router.go`
- Modify: `internal/httpapi/router_test.go`

**Interfaces:**
- Produces: `type CapabilityManagementService interface`
- Produces: `func WithCapabilities(service CapabilityManagementService) Option`
- Produces endpoints under `/v1/capabilities`.

- [ ] **Step 1: Write failing router tests**

Cover unauthenticated rejection, viewer list allowed, viewer test rejected, admin draft create, admin validation, admin publish, admin unpublish, and JSON error responses.

Run: `go test ./internal/httpapi -run 'TestCapability'`

Expected: FAIL because route option and endpoints do not exist.

- [ ] **Step 2: Implement router endpoints**

Route `GET /v1/capabilities`, `GET /v1/capabilities/{name}`, `POST /v1/capabilities/drafts`, `PUT /v1/capabilities/drafts/{name}`, `POST /v1/capabilities/validate`, `POST /v1/capabilities/test`, `POST /v1/capabilities/{name}/publish`, and `POST /v1/capabilities/{name}/unpublish`.

- [ ] **Step 3: Verify router tests pass**

Run: `go test ./internal/httpapi -run 'TestCapability'`

Expected: PASS.

### Task 3: API Wiring

**Files:**
- Modify: `cmd/copilot-api/main.go`
- Modify: `cmd/copilot-api/main_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `capabilities.NewManager(root, adapter)`
- Produces: router option wiring when `COPILOT_CAPABILITIES_DIR` is set.

- [ ] **Step 1: Write failing wiring tests**

Cover `routerOptions` includes capability management when a capability root is provided and omits it when no root is configured.

Run: `go test ./cmd/copilot-api -run 'TestRouterOptions'`

Expected: FAIL because capability management is not wired.

- [ ] **Step 2: Wire manager into router options**

Construct the manager from `COPILOT_CAPABILITIES_DIR` and pass it with `httpapi.WithCapabilities`.

- [ ] **Step 3: Update README**

Document the new management API and first-version Vue console workflow.

- [ ] **Step 4: Verify Go tests**

Run: `go test ./...`

Expected: PASS.

### Task 4: Standalone Vue Capability Console

**Files:**
- Create: `apps/capability-console/package.json`
- Create: `apps/capability-console/package-lock.json`
- Create: `apps/capability-console/index.html`
- Create: `apps/capability-console/tsconfig.json`
- Create: `apps/capability-console/tsconfig.node.json`
- Create: `apps/capability-console/vite.config.ts`
- Create: `apps/capability-console/src/main.ts`
- Create: `apps/capability-console/src/App.vue`
- Create: `apps/capability-console/src/api.ts`
- Create: `apps/capability-console/src/types.ts`
- Create: `apps/capability-console/src/capability.ts`
- Create: `apps/capability-console/src/styles.css`
- Create: `apps/capability-console/src/test/setup.ts`
- Create: `apps/capability-console/src/App.test.ts`

**Interfaces:**
- Consumes: `/v1/capabilities` management endpoints.
- Produces: a compact Element Plus operational UI for inventory, editor, validation, test preview, publish, and unpublish.

- [ ] **Step 1: Write failing frontend tests**

Cover list rendering, draft form path variable derivation, validation error rendering, publish disabled for writes, and normalized test preview.

Run: `cd apps/capability-console && npm test -- --run App.test.ts`

Expected: FAIL because the Vue app does not exist.

- [ ] **Step 2: Scaffold Vue app**

Install Vue, Vite Vue plugin, Element Plus, Vue Test Utils, jsdom, Vitest, and TypeScript. Add API client and typed model matching Go JSON.

- [ ] **Step 3: Implement UI**

Build a dense table, full-page editor area, validation badges, inline errors, test preview panel, and publish/unpublish controls.

- [ ] **Step 4: Verify frontend tests and build**

Run: `cd apps/capability-console && npm test -- --run App.test.ts`

Expected: PASS.

Run: `cd apps/capability-console && npm run build`

Expected: PASS.

### Task 5: Full Verification

**Files:**
- No new files.

- [ ] **Step 1: Run all Go tests**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 2: Run Go vet**

Run: `go vet ./...`

Expected: PASS.

- [ ] **Step 3: Run existing React console checks**

Run: `cd apps/console && npm test -- --run App.test.tsx`

Expected: PASS.

Run: `cd apps/console && npm run build`

Expected: PASS.

- [ ] **Step 4: Run new Vue console checks**

Run: `cd apps/capability-console && npm test -- --run App.test.ts`

Expected: PASS.

Run: `cd apps/capability-console && npm run build`

Expected: PASS.
