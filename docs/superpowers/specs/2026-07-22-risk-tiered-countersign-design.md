# Risk-Tiered Countersign Design

## Goal

Replace the single-step, single-token confirmation flow with a risk-tiered
parallel countersign model. Plans require multiple approvers based on risk
level, with role composition constraints. Any approver can reject, which
cancels the plan. The confirmation token model is removed entirely; all
approvals are authenticated by JWT and recorded as individual audit records.

## Scope

In scope:

- Risk-tiered approval requirements: count + role composition per risk level.
- Parallel countersign: approvers sign independently; quorum triggers execution.
- Reject action: any approver can reject, cancelling the plan.
- Creator cannot self-approve.
- New `plan_approvals` table and `ApprovalRecord` store type.
- New `POST /v1/action-plans/{id}/approve` and `/reject` endpoints.
- Plan detail response includes approval progress and requirement.
- Frontend workbench shows approval progress, approve/reject buttons.
- Remove `POST /v1/action-plans/{id}/confirm` and token-based confirmation.

Out of scope:

- Sequential approval chains (only parallel countersign).
- Comments, assignments, or escalation workflows.
- External approval connectors (Slack, email, ticketing).
- Real-time updates (WebSocket/SSE).
- Batch approve/reject.
- Changing the immutable plan input or idempotent execution model.
- Changing the assistant planner or tool registry.

## Approval Requirements

Approval requirements are defined in Go source alongside the existing risk
rules in `internal/policy`. They map each risk level to a minimum approver
count and optional role composition constraints.

```go
type ApprovalRequirement struct {
    MinApprovers  int
    RequiredRoles map[string]int // role -> minimum count
}

var riskApprovalRequirements = map[tools.RiskLevel]ApprovalRequirement{
    tools.Low:    {MinApprovers: 1, RequiredRoles: nil},
    tools.Medium: {MinApprovers: 2, RequiredRoles: {"admin": 1}},
    tools.High:   {MinApprovers: 3, RequiredRoles: {"admin": 2}},
}
```

Default thresholds:

| Risk   | Min Approvers | Role Requirement |
|--------|---------------|------------------|
| Low    | 1             | none             |
| Medium | 2             | >= 1 admin       |
| High   | 3             | >= 2 admin       |

`ApprovalVote` is a lightweight type in `policy` used for quorum evaluation:

```go
type ApprovalVote struct {
    Subject string
    Roles   []string
}
```

`Satisfied(votes []ApprovalVote) bool` evaluates whether collected approvals
meet the count and role requirements. The `plans.Service` layer converts
`[]store.ApprovalRecord` to `[]policy.ApprovalVote` when it needs to evaluate
quorum outside the store transaction. Inside `RecordApproval`, the store uses
an internal `quorumMet(approvals, minApprovers, requiredRoles)` helper with
the same logic to avoid a `store -> policy` dependency.

Constraints:

- Creator cannot self-approve (`approver.Subject != plan.CreatedBy`).
- Approver must have the plan environment in `AllowedEnvironments`.
- Approver's role must pass `policy.riskAllowed` for the tool's risk level.
- Each approver can sign at most once (database UNIQUE constraint).

## Data Model

### New Table: `plan_approvals`

Migration `003_plan_approvals.sql`:

```sql
CREATE TABLE plan_approvals (
    id               VARCHAR(128) PRIMARY KEY,
    plan_id          VARCHAR(128) NOT NULL,
    approver_subject VARCHAR(128) NOT NULL,
    decision         VARCHAR(32)  NOT NULL,
    roles_json       VARCHAR(512) NOT NULL,
    created_at       DATETIME     NOT NULL,
    UNIQUE(plan_id, approver_subject),
    FOREIGN KEY (plan_id) REFERENCES action_plans(id)
);
```

### PlanStatus Extension

```go
const (
    PlanPendingConfirmation PlanStatus = "pending_confirmation"
    PlanConfirmed           PlanStatus = "confirmed"
    PlanCancelled           PlanStatus = "cancelled" // new
)
```

No intermediate state is needed. A plan stays `pending_confirmation` until
quorum is met (transitions to `confirmed`) or any approver rejects
(transitions to `cancelled`).

### ApprovalRecord Type

```go
type ApprovalRecord struct {
    ID              string
    PlanID          string
    ApproverSubject string
    Decision        string   // "approve" | "reject"
    Roles           []string // parsed from roles_json
    CreatedAt       time.Time
}
```

### Store Interface Changes

```go
type ActionPlanStore interface {
    CreatePlan(context.Context, PlanRecord, AuditEvent) error
    GetPlan(context.Context, string) (PlanRecord, error)
    ListPlans(context.Context, PlanFilter) ([]PlanRecord, error)
    // Removed: ConfirmPlan
    // Added:
    RecordApproval(ctx context.Context, planID string, approval ApprovalRecord,
        minApprovers int, requiredRoles map[string]int,
        now time.Time, event AuditEvent) (ApprovalRecord, PlanRecord, error)
    ListApprovals(ctx context.Context, planID string) ([]ApprovalRecord, error)
    CreateExecutionIfAbsent(context.Context, ExecutionRecord, AuditEvent) (ExecutionRecord, bool, error)
    CompleteExecution(context.Context, string, string, []byte, string, AuditEvent) error
    AppendAudit(context.Context, AuditEvent) error
}
```

### RecordApproval Atomic Transaction

A single database transaction performs:

1. Query plan; verify `status = pending_confirmation` and not expired.
2. Verify `approver != plan.CreatedBy`.
3. Insert approval record (UNIQUE constraint catches duplicates).
4. If `decision = reject`: `UPDATE action_plans SET status = 'cancelled', version = version + 1`.
5. If `decision = approve`: query all approvals for this plan, evaluate
   `ApprovalRequirement.Satisfied()`. If satisfied, `UPDATE action_plans
   SET status = 'confirmed', version = version + 1`.
6. Insert audit event.
7. Return approval record and updated plan record.

The quorum evaluation runs inside the transaction to prevent a race where
two concurrent approvals both believe they reached quorum. Row-level locking
on the plan row ensures consistency.

### Package Dependencies

`ApprovalRequirement` and `Satisfied` live in `policy`. To avoid a circular
`store -> policy` dependency, `RecordApproval` accepts raw parameters
(`int`, `map[string]int`). The `plans.Service` layer reads the requirement
from `policy` and passes the scalar values to the store.

### confirmation_token_hash Column

The column is retained in `action_plans` for existing data compatibility but
is no longer written or checked by the new approval flow. A future migration
may drop it.

## State Machine

```
            assistant creates write plan
                      |
                      v
           +----------------------+
           | pending_confirmation |
           +----------+-----------+
                      |
      +----------------+----------------+
      |                |                |
  approve           reject           expires
  (quorum not met)    |            (stays pending
      |               v             until TTL cleanup)
      |         +-----------+
      |         | cancelled |
      |         +-----------+
      v
  approve (quorum met)
      |
      v
+-----------+
| confirmed |--> existing execution flow
+-----------+    (CreateExecutionIfAbsent -> CompleteExecution)
```

## Backend API

### New Endpoints

```http
POST /v1/action-plans/{id}/approve
POST /v1/action-plans/{id}/reject
```

Request body: empty JSON object `{}`. Identity is derived from JWT.

#### Approve Response (200)

```json
{
  "approval": {
    "id": "approval-123",
    "approver": "admin-1",
    "decision": "approve",
    "roles": ["admin"],
    "created_at": "2026-07-22T10:00:00Z"
  },
  "plan": {
    "id": "plan-123",
    "status": "confirmed",
    "version": 2
  },
  "executed": true
}
```

When quorum is not yet met, `plan.status` remains `pending_confirmation` and
`executed` is `false`.

#### Reject Response (200)

```json
{
  "approval": {
    "id": "approval-456",
    "approver": "admin-2",
    "decision": "reject",
    "roles": ["admin"],
    "created_at": "2026-07-22T10:01:00Z"
  },
  "plan": {
    "id": "plan-123",
    "status": "cancelled",
    "version": 3
  }
}
```

### Removed Endpoint

```http
POST /v1/action-plans/{id}/confirm   -- removed
```

### Modified Endpoints

`GET /v1/action-plans/{id}` detail response now includes approval progress:

```json
{
  "id": "plan-123",
  "tool": "topic.retention.set",
  "environment": "prod",
  "risk": "medium",
  "status": "pending_confirmation",
  "version": 1,
  "expires_at": "2026-07-22T12:10:00Z",
  "created_by": "operator-1",
  "created_at": "2026-07-22T12:00:00Z",
  "input": { "environment": "prod", "topic": "orders", "retention_hours": 72 },
  "approvals": [
    { "approver": "admin-1", "decision": "approve", "roles": ["admin"], "created_at": "..." }
  ],
  "approval_requirement": {
    "min_approvers": 2,
    "required_roles": { "admin": 1 },
    "current_approvers": 1
  }
}
```

`GET /v1/action-plans?status=` now accepts `pending_confirmation`,
`confirmed`, and `cancelled`.

### Error Mapping

| Scenario                              | HTTP |
|---------------------------------------|------|
| Missing or invalid JWT                | 401  |
| Plan not found                        | 404  |
| Plan not pending (confirmed/cancelled)| 409  |
| Approver is the plan creator          | 403  |
| Approver already signed               | 409  |
| Approver lacks environment access     | 403  |
| Approver role insufficient for risk   | 403  |
| Store failure                         | 502  |

Responses use the existing JSON error envelope.

### Execution Handoff

`plans.Service.Approve` calls `store.RecordApproval`. If the returned plan
has `status = confirmed`, the service triggers the existing execution flow
(`CreateExecutionIfAbsent` + `CompleteExecution`). The HTTP response sets
`executed: true` and includes the execution result. `plans.Service.Reject`
never triggers execution.

## Authorization

The approve and reject handlers authenticate with the existing HMAC JWT
authenticator. Permissions are never trusted from JWT claims; they are
derived server-side.

Before calling `RecordApproval`, the handler verifies:

1. JWT is valid (existing `auth.Authenticate`).
2. Approver has the plan environment in `AllowedEnvironments` (existing
   `userAllowedEnvironment`).
3. Approver is not the plan creator (`user.Subject != plan.CreatedBy`).
4. Approver's role passes `policy.riskAllowed` for the tool's risk level,
   ensuring a viewer cannot approve a high-risk plan.

The `RecordApproval` store method performs the final authoritative checks
inside its transaction: plan is pending, not expired, and the approver has
not already signed.

## Frontend

### Workbench Panel Changes

The pending plan list rows now show approval progress, e.g. `1/2 approved`.
Cancelled plans display a red indicator.

The detail panel adds:

- **Approval progress section**: shows `approval_requirement` (needs N
  approvers, including M admins) and the list of existing approvals
  (approver, roles, timestamp).
- **Approval action buttons**: replaces the existing confirm button.
  - `Approve` button (enabled when the current user has not signed and is
    not the creator).
  - `Reject` button (same conditions).
- When the current user has already signed: "You approved; waiting for
  other approvers."
- When the current user is the creator: "You cannot approve your own plan."

### State Changes

Remove:
- `planTokens` state and all token-related logic.

Add:
- `approving: boolean`
- `rejecting: boolean`

### Component Structure

Following the preference for extracted sub-components rather than
monolithic files:

```
App.tsx
  +-- IdentityPanel (existing)
  +-- ChatPanel (existing)
  +-- ResultPanel (existing)
  +-- PlansPanel (extracted to its own component file)
        +-- PlanList (list + progress indicator)
        +-- PlanDetail (detail + approval progress)
        +-- ApprovalActions (approve/reject buttons)
```

## Testing

### Backend Unit Tests (`internal/store`)

- `RecordApproval` with `approve`, quorum not met: plan stays pending.
- `RecordApproval` with `approve`, quorum met: plan transitions to confirmed.
- `RecordApproval` with `reject`: plan transitions to cancelled.
- `RecordApproval` rejects creator self-approval.
- `RecordApproval` rejects duplicate approval.
- `RecordApproval` rejects non-pending plan.
- `ListApprovals` returns all approvals for a plan.
- SQL store (SQLite) covers the same behaviors.

### Policy Tests (`internal/policy`)

- `ApprovalRequirement.Satisfied` for each risk level.
- Role composition insufficient case.

### HTTP Tests (`internal/httpapi`)

- `POST /approve` requires authentication.
- Approve with quorum not met returns 200 + `executed: false`.
- Approve with quorum met returns 200 + `executed: true`.
- Reject returns 200 + plan cancelled.
- Creator approve returns 403.
- Missing environment access returns 403.
- Duplicate approval returns 409.
- Detail response includes `approvals` and `approval_requirement`.

### E2E Tests (`tests/e2e`)

- SQLite: assistant creates write plan, admin-1 approves (pending), admin-2
  approves (confirmed + execution triggered).
- SQLite: assistant creates write plan, admin-1 rejects (cancelled).

### Frontend Tests (`apps/console`)

- List renders approval progress.
- Detail shows existing approvals.
- Approve button calls `POST /approve`.
- Reject button calls `POST /reject`.
- Creator does not see approve/reject buttons.
- Already-approved user sees waiting message.

## Migration and Compatibility

### Migration `003_plan_approvals.sql`

- Creates the `plan_approvals` table.
- Does not modify the `action_plans` table structure.

### Breaking Changes

- Removed `POST /v1/action-plans/{id}/confirm` endpoint.
- Removed `COPILOT_DEV_EXPOSE_CONFIRMATION_TOKEN` environment variable.
- Removed `ActionPlanStore.ConfirmPlan` method.
- Frontend removes `planTokens` state and token capture logic.

### Preserved Behavior

- Plan creation flow (assistant -> policy -> CreatePlan) is unchanged.
- Execution flow (CreateExecutionIfAbsent -> CompleteExecution) is unchanged.
- Immutable plan input, idempotent execution, and audit event model are
  unchanged.
- JWT authentication and environment visibility filtering are unchanged.

### Dev Mode Impact

Local demos no longer need a confirmation token. Any admin can approve
directly. Low-risk plans execute after a single approval, making the local
demo flow smoother than the previous token-based flow.

## Acceptance Criteria

- `go test -count=1 ./...` passes.
- `go vet ./...` passes.
- `git diff --check` passes.
- `npm test -- --run` passes in `apps/console`.
- `npm run build` passes in `apps/console`.
- No `confirmation_token` references remain in production code paths.
- Existing assistant read and write behavior remains compatible.
- Quorum race condition is covered by a concurrent test or documented
  transactional guarantee.
