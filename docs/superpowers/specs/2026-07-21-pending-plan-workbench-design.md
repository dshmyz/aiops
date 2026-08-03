# Pending Plan Workbench Design

## Goal

Move action plan confirmation from a local development-token demo into a
production-shaped operator workflow. The console should let authorized admins
find pending plans, inspect immutable plan details, and confirm execution without
weakening the existing policy, confirmation, idempotency, or audit guarantees.

## Scope

In scope:

- List pending action plans for environments the current user is allowed to
  operate.
- Show action plan details with enough context for a human confirmation
  decision.
- Reuse the existing confirmation-and-execution endpoint.
- Keep development token exposure available only behind
  `COPILOT_DEV_EXPOSE_CONFIRMATION_TOKEN=1`.
- Cover the workflow with backend, frontend, and SQLite e2e tests.

Out of scope:

- Multi-person approvals.
- Comments, assignments, escalation, or approval history UI beyond existing
  audit events.
- Slack, email, ticketing, or external approval connectors.
- New operational write tools.
- Changing the immutable plan or idempotent execution model.

## User Experience

The console adds a "Pending Plans" workbench next to the existing assistant
interaction. When the assistant creates a write plan, the chat response still
shows the pending plan summary, and the workbench can refresh to display it.

The pending list shows:

- Plan ID.
- Tool name.
- Environment.
- Risk level.
- Status.
- Expiration time.
- Creator.

Selecting a row opens a detail panel with the same high-level metadata plus a
safe rendering of the immutable input snapshot. The confirm button uses the
existing `POST /v1/action-plans/{id}/confirm` endpoint.

In local demo mode, the assistant response may include a one-time confirmation
token, so the UI can perform a direct one-click confirm. In production mode,
the backend does not expose that token; the UI must make that state explicit and
leave confirmation unavailable until a real approval-token delivery channel is
added.

## Backend API

Add:

```http
GET /v1/action-plans?status=pending_confirmation
```

Response:

```json
{
  "plans": [
    {
      "id": "plan-123",
      "tool": "topic.retention.set",
      "environment": "prod",
      "risk": "medium",
      "status": "pending_confirmation",
      "version": 1,
      "expires_at": "2026-07-21T12:10:00Z",
      "created_by": "admin-1",
      "created_at": "2026-07-21T12:00:00Z"
    }
  ]
}
```

Add:

```http
GET /v1/action-plans/{id}
```

Response:

```json
{
  "id": "plan-123",
  "tool": "topic.retention.set",
  "environment": "prod",
  "risk": "medium",
  "status": "pending_confirmation",
  "version": 1,
  "expires_at": "2026-07-21T12:10:00Z",
  "created_by": "admin-1",
  "created_at": "2026-07-21T12:00:00Z",
  "input": {
    "environment": "prod",
    "topic": "orders",
    "retention_hours": 72
  }
}
```

Existing endpoint remains:

```http
POST /v1/action-plans/{id}/confirm
```

Confirmation continues to require:

- Valid JWT authentication.
- The current user can operate the plan environment.
- Matching `expected_version`.
- Valid one-time confirmation token.
- A non-expired `pending_confirmation` plan.

Execution still reads the stored immutable input snapshot. Clients never
resubmit operational write parameters during confirmation.

## Authorization

The list and detail endpoints authenticate with the existing HMAC JWT
authenticator. They must not trust permissions from JWT claims. Visibility is
derived from the server-side role and environment rules already used by
`policy.Evaluate`.

Plan list responses include only plans whose extracted `environment` is present
in `CurrentUser.AllowedEnvironments` and whose tool is registered. Unknown or
malformed stored plans are excluded from list responses and return `404` or
`403` from detail depending on whether the caller is allowed to know the plan
exists.

Viewers may inspect read-safe plan metadata only if they are allowed for the
environment, but they cannot confirm write plans. Admins can confirm eligible
plans. The confirmation path remains the authority for final permission checks.

## Data Access

Extend the action plan store with read methods rather than querying directly
from HTTP handlers:

- `ListPlans(ctx, filter)` returns plan records by status.
- `GetPlan(ctx, id)` already exists and should be reused for details.

The store should keep SQLite tests representative of MySQL behavior. The first
implementation may filter authorized environments in Go after loading pending
records, while `ListPlans(ctx, filter)` must accept explicit filter fields so a
future implementation can push the same constraints into SQL without changing
HTTP handlers.

## Error Handling

- Missing or invalid JWT: `401`.
- Bad status filter or unsupported query: `400`.
- Unknown route or missing plan ID: `404`.
- Authenticated user lacks access to plan environment: `403`.
- Store failure: `502`.
- Confirmation failure keeps existing mappings: denied confirmation is `403`,
  execution conflicts are `409`, executor failures are `502`.

Responses should use the existing JSON error envelope.

## Frontend

Add a compact workbench view in `apps/console/src/App.tsx` using the current
visual language. It should be work-focused and dense rather than a marketing
surface.

State:

- `pendingPlans`.
- `selectedPlan`.
- `plansLoading`.
- `plansError`.

Actions:

- Refresh pending plans.
- Select a plan.
- Confirm selected plan when a development confirmation token is available.

The UI must not display raw confirmation tokens. In development mode it may hold
the token in state long enough to call the confirm endpoint, following the
existing local demo behavior.

## Testing

Backend tests:

- `GET /v1/action-plans` requires authentication.
- Pending list returns only `pending_confirmation` plans.
- List filters out plans outside the user's allowed environments.
- Detail returns plan metadata and safe input for an allowed user.
- Detail rejects users outside the plan environment.
- Confirmed plans disappear from the pending list.

Frontend tests:

- Renders pending plans from the API.
- Opens detail for a selected plan.
- Shows a production-mode message when no confirmation token is available.
- Confirms and renders execution result when a development token is available.
- Shows readable errors for non-JSON API responses and backend failures.

E2E test:

- With SQLite, an assistant write message stores a pending plan, and the pending
  list endpoint returns it for an admin allowed in that environment.

## Acceptance Criteria

- `go test -count=1 ./...` passes.
- `go vet ./...` passes.
- `git diff --check` passes.
- `npm test -- --run` passes in `apps/console`.
- `npm run build` passes in `apps/console`.
- Production API responses never expose `confirmation_token`.
- Existing assistant read and write behavior remains compatible.
