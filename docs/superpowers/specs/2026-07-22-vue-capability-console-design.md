# Vue Capability Console Design

## Goal

Build a standalone Vue-based Capability Console for onboarding existing
middleware management APIs into the Copilot runtime without requiring Swagger
first. Operators should be able to manually create, validate, test, and publish
read capabilities through a UI instead of editing YAML files by hand.

The first version focuses on safe read capability onboarding. Write
capabilities may be displayed or validated later, but they must not be
published into the runtime until a capability-aware confirmed executor exists.

## Product Shape

```text
Traditional middleware admin APIs
        |
        v
Vue Capability Console
        |
        v
Go capability management API
        |
        v
capabilities/discovered/*.yaml
capabilities/published/*.yaml
        |
        v
Copilot runtime loader
        |
        v
Governed read-only tools + HTTP adapter + audit
```

The console is an AI capability onboarding workbench, not a replacement for the
traditional middleware admin backend. The traditional backend continues to own
the real operational APIs. The Capability Console owns how those APIs are
described, tested, normalized, governed, and exposed to Copilot.

## App Location And Stack

Create a new standalone frontend app:

```text
apps/capability-console/
```

Use:

- Vue 3.
- Vite.
- TypeScript.
- Vue Router.
- Element Plus.

This keeps the existing React console untouched and lets the capability UI
evolve independently. Later, the Vue app can be linked from or embedded into
the traditional middleware admin backend.

## Storage Model

The first version continues to use the existing file-based registry:

```text
capabilities/
  discovered/
  published/
```

Reasons:

- It matches the runtime loader already implemented.
- YAML files remain transparent and easy to review in git.
- No database migration is needed for the first UI version.
- The same files support both manual UI creation and future Swagger imports.

The Go API must treat the capability root as configurable, using the same root
concept as `COPILOT_CAPABILITIES_DIR`. If no root is configured, management
endpoints should fail closed with a clear configuration error.

## First-Version Scope

In scope:

- List capabilities from `discovered` and `published`.
- Create a read capability draft manually.
- Edit a draft capability.
- Validate a capability using the existing schema validation.
- Test a read capability against its backend API through the existing HTTP
  adapter rules.
- Preview the normalized Copilot-visible output.
- Publish a validated read capability into `capabilities/published`.
- Unpublish a read capability back out of runtime availability.
- Show validation, policy, adapter, and file-operation errors clearly.

Out of scope:

- Swagger import UI.
- Publishing write capabilities into runtime.
- Database-backed capability storage.
- Multi-step approval workflow for capability publishing.
- OAuth or secret management UI.
- Query/header parameter support in the first adapter version.
- Replacing action plan, confirmation, execution, or audit services.
- Embedding into the traditional middleware backend.

## Capability Rules

The UI should guide users toward the runtime rules already enforced by the Go
backend:

- Only `status: published` files under `capabilities/published` can be loaded.
- Runtime read capabilities must use `operation: read`.
- Runtime read capabilities must use backend method `GET`.
- Published capabilities must have an absolute `http` or `https`
  `backend.base_url`.
- Runtime read capabilities support path parameters only in this version.
- Output mappings must produce normalized scalar fields, not raw backend JSON.
- Capability names must not conflict with static tools or existing dynamic
  tools.
- Published writes are not exposed to Copilot runtime.

The UI may show write fields as disabled or future-facing, but first-version
publish actions must reject write capabilities.

## Pages

### 1. Capability List

Purpose: give operators a clear inventory of what exists and what is available
to Copilot.

Main features:

- Tabs or segmented filter for `discovered`, `published`, and `deprecated`.
- Filters for domain, resource type, operation, risk, and status.
- Table columns:
  - name
  - domain
  - resource type
  - operation
  - risk
  - backend method/path
  - status
  - validation state
  - last modified time if available from filesystem metadata
- Actions:
  - view/edit
  - validate
  - test
  - publish for valid read drafts
  - unpublish for published reads

The list should make runtime availability obvious. A discovered draft is not
available to Copilot. A published read that validates is runtime-eligible.

### 2. Capability Editor

Purpose: let users create or edit a capability without writing YAML directly.

Sections:

- Identity:
  - name
  - status
  - domain
  - resource type
  - operation
  - risk
- Backend:
  - base URL
  - method
  - path
  - timeout
- Inputs:
  - field name
  - type
  - required
  - path-variable indicator
- Output:
  - kind
  - severity path
  - summary template
  - mapped fields
- Auth:
  - roles
  - environment scoped
- AI metadata:
  - description
  - examples

The editor should derive path variables from `backend.path` and show whether
each variable has a matching input schema entry.

### 3. Test And Preview

Purpose: prove the capability works before publishing.

Flow:

1. User enters test input values.
2. Backend validates the capability.
3. Backend executes the HTTP adapter using the provided inputs.
4. UI shows:
   - backend request method and path
   - success or error status
   - normalized result preview
   - Copilot-visible summary
   - extracted scalar data fields

Raw backend JSON should not be shown by default. If a later admin-only debug
view is added, it must be explicitly gated and clearly separate from what
Copilot sees.

### 4. Publish Confirmation

Purpose: make publication deliberate.

Before publishing, the UI shows:

- final capability name
- target file path under `capabilities/published`
- validation result
- backend base URL and path
- roles
- Copilot-visible output preview if a test has been run

The publish action should fail if:

- validation fails
- operation is not `read`
- backend method is not `GET`
- backend base URL is missing or invalid
- name conflicts with an existing static or dynamic tool
- a published file with the same name already exists unless replacing is
  explicitly supported in a later version

## Go Management API

Add capability management endpoints under `/v1/capabilities`.

Proposed first-version endpoints:

```text
GET    /v1/capabilities
GET    /v1/capabilities/{name}
POST   /v1/capabilities/drafts
PUT    /v1/capabilities/drafts/{name}
POST   /v1/capabilities/validate
POST   /v1/capabilities/test
POST   /v1/capabilities/{name}/publish
POST   /v1/capabilities/{name}/unpublish
```

Endpoint behavior:

- `GET /v1/capabilities` lists discovered and published files with validation
  status.
- `GET /v1/capabilities/{name}` returns one parsed capability plus its source
  location.
- `POST /v1/capabilities/drafts` writes a new YAML draft under
  `capabilities/discovered`.
- `PUT /v1/capabilities/drafts/{name}` updates an existing discovered draft.
- `POST /v1/capabilities/validate` validates a submitted capability without
  writing it.
- `POST /v1/capabilities/test` validates and executes a read capability through
  the HTTP adapter, returning only normalized output.
- `POST /v1/capabilities/{name}/publish` moves a valid read capability from
  `discovered` to `published` with `status: published`.
- `POST /v1/capabilities/{name}/unpublish` removes runtime availability by
  moving the published file back to `discovered` with `status: needs_review`.

All file-mutating endpoints must require authenticated users with an admin
role. The test endpoint must also require an admin role in the first version
because it can reveal backend operational data. Read-only list/get endpoints
may be allowed to operator/admin roles, subject to the existing identity model.

## Backend Components

Add a focused management package:

```text
internal/capabilities/manage.go
```

Responsibilities:

- read discovered and published YAML files
- parse and validate capability payloads
- write drafts atomically
- publish and unpublish files safely
- prevent path traversal through capability names
- avoid overwriting existing files unless an explicit replace option is later
  added
- call the existing HTTP adapter for read test execution

Add HTTP handlers in the existing `internal/httpapi` boundary or a small
adjacent handler package that plugs into the router.

Keep the runtime loader separate from the management API. The management API
edits files. Runtime availability still comes from startup loading.

## Data Flow

### Manual Draft Creation

```text
Vue form
  -> POST /v1/capabilities/drafts
  -> validate basic shape
  -> write capabilities/discovered/<name>.yaml
  -> return parsed capability + validation result
```

### Test Capability

```text
Vue test form
  -> POST /v1/capabilities/test
  -> Validate(capability)
  -> HTTPAdapter.Execute(ctx, capability, input)
  -> normalized result only
  -> UI preview
```

### Publish Read Capability

```text
Vue confirmation
  -> POST /v1/capabilities/{name}/publish
  -> load discovered capability
  -> force status: published
  -> Validate
  -> reject non-read or non-GET
  -> reject static/dynamic name conflict
  -> write capabilities/published/<name>.yaml atomically
  -> remove capabilities/discovered/<name>.yaml
```

Publishing uses move semantics: a capability exists in either `discovered` or
`published`, not both. Unpublishing moves the file back to `discovered` and
sets `status: needs_review` so it is no longer runtime-eligible until validated
and published again.

## Error Handling

The API should return structured JSON errors:

```json
{
  "error": "validation_failed",
  "message": "read capability backend method must be GET",
  "fields": {
    "backend.method": "must be GET for read capabilities"
  }
}
```

First version can use a simpler shape if that matches the existing router, but
the UI needs enough detail to put errors near the relevant form sections.

Common error categories:

- `capability_root_not_configured`
- `not_found`
- `validation_failed`
- `name_conflict`
- `filesystem_error`
- `backend_call_failed`
- `permission_denied`

## UI Design Direction

This is an operational admin tool, so the UI should be compact, predictable,
and form-forward:

- dense table for capability inventory
- drawer or full-page editor for capability details
- tabs inside the editor for identity, backend, inputs, output, auth, and test
- clear validation badges
- inline field errors
- confirmation dialog for publish/unpublish
- no marketing-style hero sections
- no decorative dashboard cards that obscure the workflow

Element Plus tables, forms, dialogs, drawers, tags, alerts, and descriptions
fit this well.

## Testing Strategy

Backend tests:

- management service lists discovered and published capabilities
- draft create/update writes safe YAML paths only
- validation endpoint returns field-level errors
- test endpoint executes only valid read GET capabilities
- publish rejects write capabilities
- publish rejects static or dynamic name conflicts
- publish rejects invalid base URL
- publish writes only under `capabilities/published`
- unpublish removes runtime availability file

Frontend tests:

- list page renders capabilities and filters by status/domain
- editor validates required fields before submit
- path variables are surfaced as required inputs
- test preview renders normalized result
- publish disabled until validation passes
- write capability publish action is unavailable

E2E or integration tests:

- create draft -> validate -> test -> publish -> runtime loader can load it
- published read executes through read-only service and audit

## Success Criteria

- A user can onboard one real read API without Swagger.
- The created YAML can be loaded by the existing runtime.
- A published read capability can be called through the governed read-only path.
- The UI never exposes write capability publishing in the first version.
- The test preview shows normalized Copilot output, not raw backend JSON.
- Existing React console remains unaffected.
- Existing Go and frontend test suites remain green.

## Future Extensions

- Swagger/OpenAPI import UI.
- Query and header parameter support.
- Capability version history.
- Diff view between draft and published.
- Review/approval workflow for publishing.
- Capability-aware confirmed write executor.
- Embedding into the traditional middleware admin backend.
- Database-backed capability registry if file-based workflows become too
  limiting.
