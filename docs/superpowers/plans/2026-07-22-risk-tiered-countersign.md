# Risk-Tiered Countersign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the single-step, single-token confirmation flow with a risk-tiered parallel countersign model. Plans require multiple approvers based on risk level with role composition constraints. Any approver can reject, cancelling the plan. The confirmation token model is removed entirely.

**Architecture:** Add a `plan_approvals` table and `ApprovalRecord` store type. Extend `policy` with `ApprovalRequirement` and `Satisfied`. Replace `store.ConfirmPlan` with `store.RecordApproval` (atomic transaction: insert approval + evaluate quorum + transition plan state). Replace `POST /confirm` with `POST /approve` and `POST /reject`. The `plans.Service` layer orchestrates approval and triggers existing execution on quorum. The frontend extracts `PlansPanel` into sub-components and replaces token-based confirmation with approve/reject actions.

**Tech Stack:** Go 1.24, net/http, existing HMAC JWT authenticator, SQLite local/e2e tests through `store.NewSQLActionPlanStore`, MySQL-compatible SQL, React, TypeScript, Vitest, Vite.

## Global Constraints

- Do not expose `confirmation_token` in any API response.
- Remove `COPILOT_DEV_EXPOSE_CONFIRMATION_TOKEN` and all token-based confirmation logic.
- Do not change immutable plan input, idempotency keys, or audit semantics.
- Do not add sequential approval chains, comments, external connectors, or new operational write tools.
- Do not trust permissions from JWT claims; use server-side role and environment checks.
- Clients must not resubmit operational write parameters during approval.
- Local tests use SQLite; MySQL production/integration remains unchanged.
- Creator cannot self-approve.
- Each approver can sign at most once (database UNIQUE constraint).

---

## File Structure

- `migrations/003_plan_approvals.sql`: new migration for `plan_approvals` table.
- `internal/store/db.go`: register migration `003_plan_approvals.sql`, add SQLite DDL for `plan_approvals`.
- `internal/store/action_plans.go`: add `PlanCancelled`, `ApprovalRecord`, `RecordApproval`, `ListApprovals`, `quorumMet`; remove `ConfirmPlan`.
- `internal/store/db_test.go`: add memory and SQL store tests for `RecordApproval` and `ListApprovals`.
- `internal/policy/approval.go`: new file with `ApprovalRequirement`, `ApprovalVote`, `Satisfied`, `riskApprovalRequirements`.
- `internal/policy/approval_test.go`: new file with `Satisfied` tests.
- `internal/plans/service.go`: remove `ConfirmPlan`, `confirmationToken`, `hash`; add `Approve`, `Reject`, `ApprovalResult`; remove `ConfirmationToken` from `Plan` struct.
- `internal/plans/service_test.go`: replace confirm tests with approve/reject tests.
- `internal/httpapi/router.go`: remove `PlanConfirmationService`, `serveConfirmActionPlan`, `WithDevelopmentConfirmationTokens`; add `PlanApprovalService`, `serveApproveActionPlan`, `serveRejectActionPlan`, `WithPlanApproval`; extend detail response with approvals.
- `internal/httpapi/router_test.go`: replace confirm tests with approve/reject tests; update helpers.
- `tests/e2e/assistant_test.go`: replace confirm e2e with approve/reject e2e.
- `cmd/copilot-api/main.go`: remove `COPILOT_DEV_EXPOSE_CONFIRMATION_TOKEN`, wire `WithPlanApproval`.
- `apps/console/src/App.tsx`: remove token logic, add approve/reject actions, extract `PlansPanel`.
- `apps/console/src/components/PlansPanel.tsx`: new extracted component.
- `apps/console/src/components/PlanList.tsx`: new sub-component.
- `apps/console/src/components/PlanDetail.tsx`: new sub-component.
- `apps/console/src/components/ApprovalActions.tsx`: new sub-component.
- `apps/console/src/App.test.tsx`: replace confirm tests with approve/reject tests.
- `apps/console/src/styles.css`: update styles for approval UI.

### Task 1: Migration and Store Types

**Files:**
- Create: `migrations/003_plan_approvals.sql`
- Modify: `internal/store/db.go`
- Modify: `internal/store/action_plans.go`

**Interfaces:**
- Consumes: existing `store.PlanRecord`, `store.PlanStatus`, `store.ActionPlanStore`.
- Produces:
  - `migrations/003_plan_approvals.sql` with `plan_approvals` table.
  - SQLite DDL for `plan_approvals` in `sqliteMigrations`.
  - `PlanCancelled` status constant.
  - `ApprovalRecord` type.
  - `RecordApproval` and `ListApprovals` method signatures on `ActionPlanStore`.
  - `quorumMet` helper function.
  - Removal of `ConfirmPlan` from `ActionPlanStore`.

- [ ] **Step 1: Create migration file**

Create `migrations/003_plan_approvals.sql`:

```sql
CREATE TABLE IF NOT EXISTS plan_approvals (
    id CHAR(36) NOT NULL,
    plan_id CHAR(36) NOT NULL,
    approver_subject VARCHAR(255) NOT NULL,
    decision VARCHAR(32) NOT NULL,
    roles_json VARCHAR(512) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY plan_approvals_plan_id_approver_subject_uq (plan_id, approver_subject),
    KEY plan_approvals_plan_id_idx (plan_id),
    CONSTRAINT plan_approvals_plan_id_fk
        FOREIGN KEY (plan_id) REFERENCES action_plans (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
```

- [ ] **Step 2: Register migration and add SQLite DDL**

In `internal/store/db.go`:

- Add `"003_plan_approvals.sql"` to the `migrations` slice.
- Add SQLite DDL for `plan_approvals` to `sqliteMigrations`:

```go
`CREATE TABLE IF NOT EXISTS plan_approvals (
    id TEXT NOT NULL PRIMARY KEY,
    plan_id TEXT NOT NULL,
    approver_subject TEXT NOT NULL,
    decision TEXT NOT NULL,
    roles_json TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (plan_id, approver_subject),
    FOREIGN KEY (plan_id) REFERENCES action_plans (id)
)`,
`CREATE INDEX IF NOT EXISTS plan_approvals_plan_id_idx ON plan_approvals (plan_id)`,
```

- [ ] **Step 3: Add PlanCancelled status**

In `internal/store/action_plans.go`, add to the `PlanStatus` constants:

```go
const (
    PlanPendingConfirmation PlanStatus = "pending_confirmation"
    PlanConfirmed           PlanStatus = "confirmed"
    PlanCancelled           PlanStatus = "cancelled"
)
```

- [ ] **Step 4: Add ApprovalRecord type**

In `internal/store/action_plans.go`, add:

```go
type ApprovalRecord struct {
    ID              string
    PlanID          string
    ApproverSubject string
    Decision        string
    Roles           []string
    CreatedAt       time.Time
}
```

- [ ] **Step 5: Add quorumMet helper**

In `internal/store/action_plans.go`, add an unexported helper that avoids a `store -> policy` dependency:

```go
func quorumMet(approvals []ApprovalRecord, minApprovers int, requiredRoles map[string]int) bool {
    if len(approvals) < minApprovers {
        return false
    }
    roleCount := map[string]int{}
    for _, approval := range approvals {
        for _, role := range approval.Roles {
            roleCount[role]++
        }
    }
    for role, need := range requiredRoles {
        if roleCount[role] < need {
            return false
        }
    }
    return true
}
```

- [ ] **Step 6: Update ActionPlanStore interface**

In `internal/store/action_plans.go`, replace `ConfirmPlan` with `RecordApproval` and `ListApprovals`:

```go
type ActionPlanStore interface {
    CreatePlan(context.Context, PlanRecord, AuditEvent) error
    GetPlan(context.Context, string) (PlanRecord, error)
    ListPlans(context.Context, PlanFilter) ([]PlanRecord, error)
    RecordApproval(ctx context.Context, planID string, approval ApprovalRecord,
        minApprovers int, requiredRoles map[string]int,
        now time.Time, event AuditEvent) (ApprovalRecord, PlanRecord, error)
    ListApprovals(ctx context.Context, planID string) ([]ApprovalRecord, error)
    CreateExecutionIfAbsent(context.Context, ExecutionRecord, AuditEvent) (ExecutionRecord, bool, error)
    CompleteExecution(context.Context, string, string, []byte, string, AuditEvent) error
    AppendAudit(context.Context, AuditEvent) error
}
```

- [ ] **Step 7: Implement memory store RecordApproval**

In `internal/store/action_plans.go`, replace `MemoryActionPlanStore.ConfirmPlan` with:

```go
func (s *MemoryActionPlanStore) RecordApproval(_ context.Context, planID string, approval ApprovalRecord,
    minApprovers int, requiredRoles map[string]int, now time.Time, event AuditEvent) (ApprovalRecord, PlanRecord, error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    plan, ok := s.plans[planID]
    if !ok {
        return ApprovalRecord{}, PlanRecord{}, ErrNotFound
    }
    if plan.Status != PlanPendingConfirmation || !plan.ExpiresAt.After(now) {
        return ApprovalRecord{}, PlanRecord{}, ErrConflict
    }
    if approval.ApproverSubject == plan.CreatedBy {
        return ApprovalRecord{}, PlanRecord{}, ErrConflict
    }
    for _, existing := range s.approvals[planID] {
        if existing.ApproverSubject == approval.ApproverSubject {
            return ApprovalRecord{}, PlanRecord{}, ErrConflict
        }
    }
    approval.ID = cloneString(approval.ID)
    approval.PlanID = planID
    approval.Roles = cloneStrings(approval.Roles)
    if s.approvals == nil {
        s.approvals = map[string][]ApprovalRecord{}
    }
    s.approvals[planID] = append(s.approvals[planID], approval)
    if approval.Decision == "reject" {
        plan.Status = PlanCancelled
        plan.Version++
        plan.UpdatedAt = now
        s.plans[planID] = plan
    } else if approval.Decision == "approve" {
        allApprovals := s.approvals[planID]
        if quorumMet(allApprovals, minApprovers, requiredRoles) {
            plan.Status = PlanConfirmed
            plan.Version++
            plan.ConfirmedBy = approval.ApproverSubject
            plan.ConfirmedAt = pointerTo(now)
            plan.UpdatedAt = now
            s.plans[planID] = plan
        }
    }
    s.audits = append(s.audits, cloneAudit(event))
    return cloneApproval(approval), clonePlan(plan), nil
}
```

Add `approvals` field to `MemoryActionPlanStore`:

```go
type MemoryActionPlanStore struct {
    mu         sync.Mutex
    plans      map[string]PlanRecord
    executions map[string]ExecutionRecord
    keys       map[string]string
    audits     []AuditEvent
    approvals  map[string][]ApprovalRecord
}
```

- [ ] **Step 8: Implement memory store ListApprovals**

```go
func (s *MemoryActionPlanStore) ListApprovals(_ context.Context, planID string) ([]ApprovalRecord, error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    records := s.approvals[planID]
    out := make([]ApprovalRecord, len(records))
    for i, record := range records {
        out[i] = cloneApproval(record)
    }
    return out, nil
}
```

- [ ] **Step 9: Add clone helpers**

```go
func cloneApproval(approval ApprovalRecord) ApprovalRecord {
    approval.Roles = cloneStrings(approval.Roles)
    return approval
}

func cloneStrings(values []string) []string {
    if values == nil {
        return nil
    }
    out := make([]string, len(values))
    copy(out, values)
    return out
}

func cloneString(value string) string {
    return string(append([]byte(nil), value...))
}
```

- [ ] **Step 10: Implement SQL store RecordApproval**

In `internal/store/action_plans.go`, replace `MySQLActionPlanStore.ConfirmPlan` with:

```go
func (s *MySQLActionPlanStore) RecordApproval(ctx context.Context, planID string, approval ApprovalRecord,
    minApprovers int, requiredRoles map[string]int, now time.Time, event AuditEvent) (ApprovalRecord, PlanRecord, error) {
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return ApprovalRecord{}, PlanRecord{}, err
    }
    defer func() { _ = tx.Rollback() }()

    plan, err := scanPlan(tx.QueryRowContext(ctx, `SELECT id, request_id, created_by, tool_name, input_json, input_hash, risk_level, status, version, confirmation_token_hash, confirmed_by, confirmed_at, expires_at, created_at, updated_at FROM action_plans WHERE id = ? FOR UPDATE`, planID))
    if err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return ApprovalRecord{}, PlanRecord{}, ErrNotFound
        }
        return ApprovalRecord{}, PlanRecord{}, err
    }
    if plan.Status != PlanPendingConfirmation || !plan.ExpiresAt.After(now) {
        return ApprovalRecord{}, PlanRecord{}, ErrConflict
    }
    if approval.ApproverSubject == plan.CreatedBy {
        return ApprovalRecord{}, PlanRecord{}, ErrConflict
    }

    rolesJSON, err := json.Marshal(approval.Roles)
    if err != nil {
        return ApprovalRecord{}, PlanRecord{}, fmt.Errorf("marshal approval roles: %w", err)
    }
    _, err = tx.ExecContext(ctx, `INSERT INTO plan_approvals (id, plan_id, approver_subject, decision, roles_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
        approval.ID, planID, approval.ApproverSubject, approval.Decision, rolesJSON, approval.CreatedAt)
    if err != nil {
        if isDuplicateKey(err) {
            return ApprovalRecord{}, PlanRecord{}, ErrConflict
        }
        return ApprovalRecord{}, PlanRecord{}, err
    }

    if approval.Decision == "reject" {
        if _, err := tx.ExecContext(ctx, `UPDATE action_plans SET status = ?, version = version + 1, updated_at = ? WHERE id = ?`, PlanCancelled, now, planID); err != nil {
            return ApprovalRecord{}, PlanRecord{}, err
        }
    } else if approval.Decision == "approve" {
        rows, err := tx.QueryContext(ctx, `SELECT id, plan_id, approver_subject, decision, roles_json, created_at FROM plan_approvals WHERE plan_id = ?`, planID)
        if err != nil {
            return ApprovalRecord{}, PlanRecord{}, err
        }
        var allApprovals []ApprovalRecord
        for rows.Next() {
            record, err := scanApprovalRow(rows)
            if err != nil {
                _ = rows.Close()
                return ApprovalRecord{}, PlanRecord{}, err
            }
            allApprovals = append(allApprovals, record)
        }
        _ = rows.Close()
        if rows.Err() != nil {
            return ApprovalRecord{}, PlanRecord{}, rows.Err()
        }
        if quorumMet(allApprovals, minApprovers, requiredRoles) {
            if _, err := tx.ExecContext(ctx, `UPDATE action_plans SET status = ?, version = version + 1, confirmed_by = ?, confirmed_at = ?, updated_at = ? WHERE id = ?`, PlanConfirmed, approval.ApproverSubject, now, now, planID); err != nil {
                return ApprovalRecord{}, PlanRecord{}, err
            }
        }
    }

    if err := insertAudit(ctx, tx, event); err != nil {
        return ApprovalRecord{}, PlanRecord{}, err
    }
    if err := tx.Commit(); err != nil {
        return ApprovalRecord{}, PlanRecord{}, err
    }
    updated, err := s.GetPlan(ctx, planID)
    if err != nil {
        return ApprovalRecord{}, PlanRecord{}, err
    }
    return approval, updated, nil
}
```

- [ ] **Step 11: Implement SQL store ListApprovals**

```go
func (s *MySQLActionPlanStore) ListApprovals(ctx context.Context, planID string) ([]ApprovalRecord, error) {
    rows, err := s.db.QueryContext(ctx, `SELECT id, plan_id, approver_subject, decision, roles_json, created_at FROM plan_approvals WHERE plan_id = ? ORDER BY created_at ASC`, planID)
    if err != nil {
        return nil, err
    }
    defer func() { _ = rows.Close() }()
    var approvals []ApprovalRecord
    for rows.Next() {
        approval, err := scanApprovalRow(rows)
        if err != nil {
            return nil, err
        }
        approvals = append(approvals, approval)
    }
    if err := rows.Err(); err != nil {
        return nil, err
    }
    return approvals, nil
}

func scanApprovalRow(rows *sql.Rows) (ApprovalRecord, error) {
    var approval ApprovalRecord
    var rolesJSON []byte
    if err := rows.Scan(&approval.ID, &approval.PlanID, &approval.ApproverSubject, &approval.Decision, &rolesJSON, &approval.CreatedAt); err != nil {
        return ApprovalRecord{}, err
    }
    if err := json.Unmarshal(rolesJSON, &approval.Roles); err != nil {
        return ApprovalRecord{}, fmt.Errorf("unmarshal approval roles: %w", err)
    }
    return approval, nil
}
```

- [ ] **Step 12: Remove ConfirmPlan from memory and SQL stores**

Delete the `MemoryActionPlanStore.ConfirmPlan` and `MySQLActionPlanStore.ConfirmPlan` methods entirely.

- [ ] **Step 13: Run store compilation check**

Run: `go build ./internal/store`

Expected: compilation succeeds (callers of `ConfirmPlan` will break; those are fixed in Task 3 and Task 4).

### Task 2: Policy Approval Requirements

**Files:**
- Create: `internal/policy/approval.go`
- Create: `internal/policy/approval_test.go`

**Interfaces:**
- Consumes: `tools.RiskLevel`.
- Produces:
  - `type ApprovalRequirement struct`
  - `type ApprovalVote struct`
  - `func ApprovalRequirementFor(risk tools.RiskLevel) ApprovalRequirement`
  - `func (req ApprovalRequirement) Satisfied(votes []ApprovalVote) bool`
  - `var riskApprovalRequirements`

- [ ] **Step 1: Write failing Satisfied tests**

Create `internal/policy/approval_test.go`:

```go
package policy_test

import (
    "testing"

    "github.com/gracegaoya/ai-operations-copilot/internal/policy"
    "github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestApprovalRequirementSatisfiedLowRiskSingleApprover(t *testing.T) {
    t.Parallel()
    req := policy.ApprovalRequirementFor(tools.Low)
    if !req.Satisfied([]policy.ApprovalVote{{Subject: "admin-1", Roles: []string{"admin"}}}) {
        t.Fatal("low risk should be satisfied by one approver")
    }
    if req.Satisfied(nil) {
        t.Fatal("low risk should not be satisfied by zero approvers")
    }
}

func TestApprovalRequirementSatisfiedMediumRiskRequiresAdmin(t *testing.T) {
    t.Parallel()
    req := policy.ApprovalRequirementFor(tools.Medium)
    twoOperators := []policy.ApprovalVote{
        {Subject: "op-1", Roles: []string{"operator"}},
        {Subject: "op-2", Roles: []string{"operator"}},
    }
    if req.Satisfied(twoOperators) {
        t.Fatal("medium risk should not be satisfied without an admin")
    }
    withAdmin := append(twoOperators, policy.ApprovalVote{Subject: "admin-1", Roles: []string{"admin"}})
    if !req.Satisfied(withAdmin) {
        t.Fatal("medium risk should be satisfied by 2+ approvers including 1 admin")
    }
}

func TestApprovalRequirementSatisfiedHighRiskRequiresTwoAdmins(t *testing.T) {
    t.Parallel()
    req := policy.ApprovalRequirementFor(tools.High)
    oneAdmin := []policy.ApprovalVote{
        {Subject: "op-1", Roles: []string{"operator"}},
        {Subject: "admin-1", Roles: []string{"admin"}},
        {Subject: "op-2", Roles: []string{"operator"}},
    }
    if req.Satisfied(oneAdmin) {
        t.Fatal("high risk should not be satisfied with only 1 admin")
    }
    twoAdmins := []policy.ApprovalVote{
        {Subject: "op-1", Roles: []string{"operator"}},
        {Subject: "admin-1", Roles: []string{"admin"}},
        {Subject: "admin-2", Roles: []string{"admin"}},
    }
    if !req.Satisfied(twoAdmins) {
        t.Fatal("high risk should be satisfied by 3+ approvers including 2 admins")
    }
}
```

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/policy -run TestApprovalRequirement`

Expected: FAIL because `ApprovalRequirement` and `Satisfied` do not exist.

- [ ] **Step 3: Implement approval.go**

Create `internal/policy/approval.go`:

```go
package policy

import "github.com/gracegaoya/ai-operations-copilot/internal/tools"

// ApprovalRequirement defines the count and role composition constraints for
// countersigning a plan at a given risk level.
type ApprovalRequirement struct {
    MinApprovers  int
    RequiredRoles map[string]int
}

// ApprovalVote is a lightweight snapshot of a single approver's identity used
// for quorum evaluation outside the store transaction.
type ApprovalVote struct {
    Subject string
    Roles   []string
}

var riskApprovalRequirements = map[tools.RiskLevel]ApprovalRequirement{
    tools.Low:    {MinApprovers: 1, RequiredRoles: nil},
    tools.Medium: {MinApprovers: 2, RequiredRoles: {"admin": 1}},
    tools.High:   {MinApprovers: 3, RequiredRoles: {"admin": 2}},
}

// ApprovalRequirementFor returns the countersign requirement for a risk level.
func ApprovalRequirementFor(risk tools.RiskLevel) ApprovalRequirement {
    return riskApprovalRequirements[risk]
}

// Satisfied reports whether the collected approval votes meet the count and
// role composition constraints.
func (req ApprovalRequirement) Satisfied(votes []ApprovalVote) bool {
    if len(votes) < req.MinApprovers {
        return false
    }
    roleCount := map[string]int{}
    for _, vote := range votes {
        for _, role := range vote.Roles {
            roleCount[role]++
        }
    }
    for role, need := range req.RequiredRoles {
        if roleCount[role] < need {
            return false
        }
    }
    return true
}
```

- [ ] **Step 4: Run GREEN**

Run: `go test -count=1 ./internal/policy -run TestApprovalRequirement`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/policy/approval.go internal/policy/approval_test.go migrations/003_plan_approvals.sql internal/store/db.go internal/store/action_plans.go
git commit -m "feat: add risk-tiered approval requirement and plan_approvals store"
```

### Task 3: Store RecordApproval and ListApprovals Tests

**Files:**
- Modify: `internal/store/db_test.go`

**Interfaces:**
- Consumes: `store.RecordApproval`, `store.ListApprovals`, `store.ApprovalRecord`.
- Produces: memory and SQL store tests for approval recording and quorum evaluation.

- [ ] **Step 1: Write failing memory store approval tests**

Add to `internal/store/db_test.go`:

```go
func TestMemoryStoreRecordApprovalApproveQuorumNotMetStaysPending(t *testing.T) {
    ctx := context.Background()
    repository := NewMemoryActionPlanStore()
    now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
    plan := PlanRecord{ID: "plan-medium", ToolName: "topic.retention.set", InputJSON: []byte(`{"environment":"prod"}`), RiskLevel: "medium", Status: PlanPendingConfirmation, Version: 1, ExpiresAt: now.Add(time.Minute), CreatedBy: "creator-1", CreatedAt: now, UpdatedAt: now}
    if err := repository.CreatePlan(ctx, plan, AuditEvent{ID: "audit-1", PlanID: plan.ID, Action: "plan_created", CreatedAt: now}); err != nil {
        t.Fatalf("create plan: %v", err)
    }
    approval := ApprovalRecord{ID: "approval-1", PlanID: plan.ID, ApproverSubject: "admin-1", Decision: "approve", Roles: []string{"admin"}, CreatedAt: now}
    _, updated, err := repository.RecordApproval(ctx, plan.ID, approval, 2, map[string]int{"admin": 1}, now, AuditEvent{ID: "audit-approval-1", PlanID: plan.ID, Action: "plan_approved", CreatedAt: now})
    if err != nil {
        t.Fatalf("record approval: %v", err)
    }
    if updated.Status != PlanPendingConfirmation {
        t.Fatalf("status = %q, want pending_confirmation", updated.Status)
    }
}

func TestMemoryStoreRecordApprovalApproveQuorumMetTransitionsToConfirmed(t *testing.T) {
    ctx := context.Background()
    repository := NewMemoryActionPlanStore()
    now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
    plan := PlanRecord{ID: "plan-medium-2", ToolName: "topic.retention.set", InputJSON: []byte(`{"environment":"prod"}`), RiskLevel: "medium", Status: PlanPendingConfirmation, Version: 1, ExpiresAt: now.Add(time.Minute), CreatedBy: "creator-1", CreatedAt: now, UpdatedAt: now}
    if err := repository.CreatePlan(ctx, plan, AuditEvent{ID: "audit-1", PlanID: plan.ID, Action: "plan_created", CreatedAt: now}); err != nil {
        t.Fatalf("create plan: %v", err)
    }
    approval1 := ApprovalRecord{ID: "approval-1", PlanID: plan.ID, ApproverSubject: "admin-1", Decision: "approve", Roles: []string{"admin"}, CreatedAt: now}
    if _, _, err := repository.RecordApproval(ctx, plan.ID, approval1, 2, map[string]int{"admin": 1}, now, AuditEvent{ID: "audit-a1", PlanID: plan.ID, Action: "plan_approved", CreatedAt: now}); err != nil {
        t.Fatalf("record first approval: %v", err)
    }
    approval2 := ApprovalRecord{ID: "approval-2", PlanID: plan.ID, ApproverSubject: "operator-1", Decision: "approve", Roles: []string{"operator"}, CreatedAt: now}
    _, updated, err := repository.RecordApproval(ctx, plan.ID, approval2, 2, map[string]int{"admin": 1}, now, AuditEvent{ID: "audit-a2", PlanID: plan.ID, Action: "plan_approved", CreatedAt: now})
    if err != nil {
        t.Fatalf("record second approval: %v", err)
    }
    if updated.Status != PlanConfirmed {
        t.Fatalf("status = %q, want confirmed", updated.Status)
    }
    if updated.Version != 2 {
        t.Fatalf("version = %d, want 2", updated.Version)
    }
}

func TestMemoryStoreRecordApprovalRejectTransitionsToCancelled(t *testing.T) {
    ctx := context.Background()
    repository := NewMemoryActionPlanStore()
    now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
    plan := PlanRecord{ID: "plan-reject", ToolName: "topic.retention.set", InputJSON: []byte(`{"environment":"prod"}`), RiskLevel: "medium", Status: PlanPendingConfirmation, Version: 1, ExpiresAt: now.Add(time.Minute), CreatedBy: "creator-1", CreatedAt: now, UpdatedAt: now}
    if err := repository.CreatePlan(ctx, plan, AuditEvent{ID: "audit-1", PlanID: plan.ID, Action: "plan_created", CreatedAt: now}); err != nil {
        t.Fatalf("create plan: %v", err)
    }
    rejection := ApprovalRecord{ID: "rejection-1", PlanID: plan.ID, ApproverSubject: "admin-1", Decision: "reject", Roles: []string{"admin"}, CreatedAt: now}
    _, updated, err := repository.RecordApproval(ctx, plan.ID, rejection, 2, map[string]int{"admin": 1}, now, AuditEvent{ID: "audit-r1", PlanID: plan.ID, Action: "plan_rejected", CreatedAt: now})
    if err != nil {
        t.Fatalf("record rejection: %v", err)
    }
    if updated.Status != PlanCancelled {
        t.Fatalf("status = %q, want cancelled", updated.Status)
    }
}

func TestMemoryStoreRecordApprovalRejectsCreatorSelfApproval(t *testing.T) {
    ctx := context.Background()
    repository := NewMemoryActionPlanStore()
    now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
    plan := PlanRecord{ID: "plan-self", ToolName: "topic.retention.set", InputJSON: []byte(`{"environment":"prod"}`), RiskLevel: "medium", Status: PlanPendingConfirmation, Version: 1, ExpiresAt: now.Add(time.Minute), CreatedBy: "creator-1", CreatedAt: now, UpdatedAt: now}
    if err := repository.CreatePlan(ctx, plan, AuditEvent{ID: "audit-1", PlanID: plan.ID, Action: "plan_created", CreatedAt: now}); err != nil {
        t.Fatalf("create plan: %v", err)
    }
    selfApproval := ApprovalRecord{ID: "approval-self", PlanID: plan.ID, ApproverSubject: "creator-1", Decision: "approve", Roles: []string{"admin"}, CreatedAt: now}
    _, _, err := repository.RecordApproval(ctx, plan.ID, selfApproval, 1, nil, now, AuditEvent{ID: "audit-self", PlanID: plan.ID, Action: "plan_approved", CreatedAt: now})
    if !errors.Is(err, ErrConflict) {
        t.Fatalf("err = %v, want ErrConflict", err)
    }
}

func TestMemoryStoreRecordApprovalRejectsDuplicateApproval(t *testing.T) {
    ctx := context.Background()
    repository := NewMemoryActionPlanStore()
    now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
    plan := PlanRecord{ID: "plan-dup", ToolName: "topic.retention.set", InputJSON: []byte(`{"environment":"prod"}`), RiskLevel: "medium", Status: PlanPendingConfirmation, Version: 1, ExpiresAt: now.Add(time.Minute), CreatedBy: "creator-1", CreatedAt: now, UpdatedAt: now}
    if err := repository.CreatePlan(ctx, plan, AuditEvent{ID: "audit-1", PlanID: plan.ID, Action: "plan_created", CreatedAt: now}); err != nil {
        t.Fatalf("create plan: %v", err)
    }
    approval := ApprovalRecord{ID: "approval-1", PlanID: plan.ID, ApproverSubject: "admin-1", Decision: "approve", Roles: []string{"admin"}, CreatedAt: now}
    if _, _, err := repository.RecordApproval(ctx, plan.ID, approval, 2, map[string]int{"admin": 1}, now, AuditEvent{ID: "audit-a1", PlanID: plan.ID, Action: "plan_approved", CreatedAt: now}); err != nil {
        t.Fatalf("first approval: %v", err)
    }
    duplicate := ApprovalRecord{ID: "approval-2", PlanID: plan.ID, ApproverSubject: "admin-1", Decision: "approve", Roles: []string{"admin"}, CreatedAt: now}
    _, _, err := repository.RecordApproval(ctx, plan.ID, duplicate, 2, map[string]int{"admin": 1}, now, AuditEvent{ID: "audit-a2", PlanID: plan.ID, Action: "plan_approved", CreatedAt: now})
    if !errors.Is(err, ErrConflict) {
        t.Fatalf("err = %v, want ErrConflict", err)
    }
}

func TestMemoryStoreListApprovalsReturnsAllForPlan(t *testing.T) {
    ctx := context.Background()
    repository := NewMemoryActionPlanStore()
    now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
    plan := PlanRecord{ID: "plan-list", ToolName: "topic.retention.set", InputJSON: []byte(`{"environment":"prod"}`), RiskLevel: "medium", Status: PlanPendingConfirmation, Version: 1, ExpiresAt: now.Add(time.Minute), CreatedBy: "creator-1", CreatedAt: now, UpdatedAt: now}
    if err := repository.CreatePlan(ctx, plan, AuditEvent{ID: "audit-1", PlanID: plan.ID, Action: "plan_created", CreatedAt: now}); err != nil {
        t.Fatalf("create plan: %v", err)
    }
    approval1 := ApprovalRecord{ID: "approval-1", PlanID: plan.ID, ApproverSubject: "admin-1", Decision: "approve", Roles: []string{"admin"}, CreatedAt: now}
    if _, _, err := repository.RecordApproval(ctx, plan.ID, approval1, 2, map[string]int{"admin": 1}, now, AuditEvent{ID: "audit-a1", PlanID: plan.ID, Action: "plan_approved", CreatedAt: now}); err != nil {
        t.Fatalf("record approval: %v", err)
    }
    approvals, err := repository.ListApprovals(ctx, plan.ID)
    if err != nil {
        t.Fatalf("list approvals: %v", err)
    }
    if len(approvals) != 1 || approvals[0].ApproverSubject != "admin-1" {
        t.Fatalf("approvals = %+v, want one from admin-1", approvals)
    }
}
```

- [ ] **Step 2: Run RED for memory store**

Run: `go test -count=1 ./internal/store -run TestMemoryStoreRecordApproval`

Expected: FAIL (methods not yet implemented or signature mismatch).

- [ ] **Step 3: Run GREEN for memory store**

Run: `go test -count=1 ./internal/store -run TestMemoryStoreRecordApproval`

Expected: PASS after Task 1 Step 7-9 implementation.

- [ ] **Step 4: Write failing SQL store approval tests**

Add to `internal/store/db_test.go`:

```go
func TestSQLStoreRecordApprovalApproveQuorumMetTransitionsToConfirmed(t *testing.T) {
    db := testSQLite(t)
    if err := ApplySQLiteMigrations(db); err != nil {
        t.Fatalf("apply migrations: %v", err)
    }
    repository := NewSQLActionPlanStore(db)
    ctx := context.Background()
    now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
    plan := PlanRecord{ID: "sql-plan-medium", RequestID: "req-1", CreatedBy: "creator-1", ToolName: "topic.retention.set", InputJSON: []byte(`{"environment":"prod"}`), InputHash: "hash-1", RiskLevel: "medium", Status: PlanPendingConfirmation, Version: 1, ExpiresAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now}
    if err := repository.CreatePlan(ctx, plan, AuditEvent{ID: "audit-1", PlanID: plan.ID, RequestID: "req-1", Subject: "creator-1", ToolName: plan.ToolName, Action: "plan_created", Decision: "permitted", CreatedAt: now}); err != nil {
        t.Fatalf("create plan: %v", err)
    }
    approval1 := ApprovalRecord{ID: "sql-approval-1", PlanID: plan.ID, ApproverSubject: "admin-1", Decision: "approve", Roles: []string{"admin"}, CreatedAt: now}
    if _, _, err := repository.RecordApproval(ctx, plan.ID, approval1, 2, map[string]int{"admin": 1}, now, AuditEvent{ID: "audit-a1", PlanID: plan.ID, RequestID: "req-1", Subject: "admin-1", ToolName: plan.ToolName, Action: "plan_approved", Decision: "permitted", CreatedAt: now}); err != nil {
        t.Fatalf("first approval: %v", err)
    }
    approval2 := ApprovalRecord{ID: "sql-approval-2", PlanID: plan.ID, ApproverSubject: "operator-1", Decision: "approve", Roles: []string{"operator"}, CreatedAt: now}
    _, updated, err := repository.RecordApproval(ctx, plan.ID, approval2, 2, map[string]int{"admin": 1}, now, AuditEvent{ID: "audit-a2", PlanID: plan.ID, RequestID: "req-1", Subject: "operator-1", ToolName: plan.ToolName, Action: "plan_approved", Decision: "permitted", CreatedAt: now})
    if err != nil {
        t.Fatalf("second approval: %v", err)
    }
    if updated.Status != PlanConfirmed {
        t.Fatalf("status = %q, want confirmed", updated.Status)
    }
    approvals, err := repository.ListApprovals(ctx, plan.ID)
    if err != nil {
        t.Fatalf("list approvals: %v", err)
    }
    if len(approvals) != 2 {
        t.Fatalf("approvals count = %d, want 2", len(approvals))
    }
}

func TestSQLStoreRecordApprovalRejectTransitionsToCancelled(t *testing.T) {
    db := testSQLite(t)
    if err := ApplySQLiteMigrations(db); err != nil {
        t.Fatalf("apply migrations: %v", err)
    }
    repository := NewSQLActionPlanStore(db)
    ctx := context.Background()
    now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
    plan := PlanRecord{ID: "sql-plan-reject", RequestID: "req-1", CreatedBy: "creator-1", ToolName: "topic.retention.set", InputJSON: []byte(`{"environment":"prod"}`), InputHash: "hash-1", RiskLevel: "medium", Status: PlanPendingConfirmation, Version: 1, ExpiresAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now}
    if err := repository.CreatePlan(ctx, plan, AuditEvent{ID: "audit-1", PlanID: plan.ID, RequestID: "req-1", Subject: "creator-1", ToolName: plan.ToolName, Action: "plan_created", Decision: "permitted", CreatedAt: now}); err != nil {
        t.Fatalf("create plan: %v", err)
    }
    rejection := ApprovalRecord{ID: "sql-rejection-1", PlanID: plan.ID, ApproverSubject: "admin-1", Decision: "reject", Roles: []string{"admin"}, CreatedAt: now}
    _, updated, err := repository.RecordApproval(ctx, plan.ID, rejection, 2, map[string]int{"admin": 1}, now, AuditEvent{ID: "audit-r1", PlanID: plan.ID, RequestID: "req-1", Subject: "admin-1", ToolName: plan.ToolName, Action: "plan_rejected", Decision: "denied", CreatedAt: now})
    if err != nil {
        t.Fatalf("rejection: %v", err)
    }
    if updated.Status != PlanCancelled {
        t.Fatalf("status = %q, want cancelled", updated.Status)
    }
}
```

- [ ] **Step 5: Run RED for SQL store**

Run: `go test -count=1 ./internal/store -run TestSQLStoreRecordApproval`

Expected: FAIL if SQL `RecordApproval` or `ListApprovals` is not implemented.

- [ ] **Step 6: Run GREEN for SQL store**

Run: `go test -count=1 ./internal/store`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/store/db_test.go
git commit -m "test: cover risk-tiered approval store behavior"
```

### Task 4: Plans Service Approve and Reject

**Files:**
- Modify: `internal/plans/service.go`
- Modify: `internal/plans/service_test.go`

**Interfaces:**
- Consumes: `store.RecordApproval`, `store.ListApprovals`, `policy.ApprovalRequirementFor`, `policy.ApprovalVote`.
- Produces:
  - `type ApprovalResult struct`
  - `func (s *Service) Approve(ctx, user, planID) (ApprovalResult, error)`
  - `func (s *Service) Reject(ctx, user, planID) (ApprovalResult, error)`
  - Removal of `ConfirmPlan`, `confirmationToken`, `hash`, `ConfirmationToken` field.

- [ ] **Step 1: Write failing approve test**

Replace confirm tests in `internal/plans/service_test.go` with:

```go
func TestApproveLowRiskPlanSingleApprovalExecutes(t *testing.T) {
    t.Parallel()
    ctx := context.Background()
    repository := store.NewMemoryActionPlanStore()
    service := plans.NewService(repository, fixedClock())
    plan := createWritePlan(t, ctx, service)

    result, err := service.Approve(ctx, admin(), plan.ID)
    if err != nil {
        t.Fatalf("approve: %v", err)
    }
    if result.Plan.Status != plans.Confirmed {
        t.Fatalf("status = %q, want confirmed", result.Plan.Status)
    }
    if !result.Executed {
        t.Fatal("low risk plan should execute after single approval")
    }
}

func TestApproveMediumRiskPlanRequiresTwoApprovers(t *testing.T) {
    t.Parallel()
    ctx := context.Background()
    repository := store.NewMemoryActionPlanStore()
    service := plans.NewService(repository, fixedClock())
    plan := createWritePlan(t, ctx, service)

    firstApprover := identity.CurrentUser{Subject: "admin-1", Roles: []string{"admin"}, AllowedEnvironments: []string{"prod"}, RequestID: "req-1"}
    result, err := service.Approve(ctx, firstApprover, plan.ID)
    if err != nil {
        t.Fatalf("first approve: %v", err)
    }
    if result.Plan.Status != plans.PendingConfirmation {
        t.Fatalf("status = %q, want pending after first approval", result.Plan.Status)
    }
    if result.Executed {
        t.Fatal("medium risk plan should not execute after one approval")
    }

    secondApprover := identity.CurrentUser{Subject: "operator-1", Roles: []string{"operator"}, AllowedEnvironments: []string{"prod"}, RequestID: "req-1"}
    result, err = service.Approve(ctx, secondApprover, plan.ID)
    if err != nil {
        t.Fatalf("second approve: %v", err)
    }
    if result.Plan.Status != plans.Confirmed {
        t.Fatalf("status = %q, want confirmed", result.Plan.Status)
    }
    if !result.Executed {
        t.Fatal("medium risk plan should execute after two approvals")
    }
}

func TestRejectCancelsPlan(t *testing.T) {
    t.Parallel()
    ctx := context.Background()
    repository := store.NewMemoryActionPlanStore()
    service := plans.NewService(repository, fixedClock())
    plan := createWritePlan(t, ctx, service)

    approver := identity.CurrentUser{Subject: "admin-1", Roles: []string{"admin"}, AllowedEnvironments: []string{"prod"}, RequestID: "req-1"}
    result, err := service.Reject(ctx, approver, plan.ID)
    if err != nil {
        t.Fatalf("reject: %v", err)
    }
    if result.Plan.Status != plans.Cancelled {
        t.Fatalf("status = %q, want cancelled", result.Plan.Status)
    }
    if result.Executed {
        t.Fatal("rejected plan should not execute")
    }
}

func TestApproveRejectsCreatorSelfApproval(t *testing.T) {
    t.Parallel()
    ctx := context.Background()
    repository := store.NewMemoryActionPlanStore()
    service := plans.NewService(repository, fixedClock())
    plan := createWritePlan(t, ctx, service)

    creator := identity.CurrentUser{Subject: "operator-1", Roles: []string{"admin"}, AllowedEnvironments: []string{"prod"}, RequestID: "req-1"}
    _, err := service.Approve(ctx, creator, plan.ID)
    if !errors.Is(err, plans.ErrApprovalDenied) {
        t.Fatalf("err = %v, want ErrApprovalDenied", err)
    }
}
```

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/plans -run 'TestApprove|TestReject'`

Expected: FAIL because `Approve`, `Reject`, `ApprovalResult` do not exist.

- [ ] **Step 3: Implement ApprovalResult and Approve/Reject**

In `internal/plans/service.go`:

Remove `ConfirmPlan`, `confirmationToken`, `hash`, and `ConfirmationToken` field from `Plan`.

Add `Cancelled` status alias:

```go
const (
    PendingConfirmation = store.PlanPendingConfirmation
    Confirmed           = store.PlanConfirmed
    Cancelled           = store.PlanCancelled
)
```

Replace `ErrConfirmationDenied` with:

```go
var (
    ErrApprovalDenied = errors.New("plan approval was rejected")
    ErrPlanNotPermitted = errors.New("policy decision does not permit a plan")
)
```

Add `ApprovalResult`:

```go
type ApprovalResult struct {
    Approval store.ApprovalRecord
    Plan     Plan
    Executed bool
    Execution execution.Execution
}
```

Add `Approve`:

```go
func (s *Service) Approve(ctx context.Context, user identity.CurrentUser, planID string) (ApprovalResult, error) {
    return s.recordApproval(ctx, user, planID, "approve")
}

func (s *Service) Reject(ctx context.Context, user identity.CurrentUser, planID string) (ApprovalResult, error) {
    return s.recordApproval(ctx, user, planID, "reject")
}

func (s *Service) recordApproval(ctx context.Context, user identity.CurrentUser, planID, decision string) (ApprovalResult, error) {
    now := s.clock.Now().UTC()
    existing, err := s.store.GetPlan(ctx, planID)
    if err != nil {
        return ApprovalResult{}, err
    }
    tool, ok := tools.Lookup(existing.ToolName)
    if !ok {
        return ApprovalResult{}, ErrPlanNotPermitted
    }
    requirement := policy.ApprovalRequirementFor(tool.Risk)
    approval := store.ApprovalRecord{
        ID: newID(), PlanID: planID,
        ApproverSubject: user.Subject,
        Decision: decision, Roles: user.Roles,
        CreatedAt: now,
    }
    action := "plan_approved"
    auditDecision := "permitted"
    if decision == "reject" {
        action = "plan_rejected"
        auditDecision = "denied"
    }
    event := auditEvent(newID(), existing, action, auditDecision, user.Subject, user.RequestID, now, nil)
    record, updatedPlan, err := s.store.RecordApproval(ctx, planID, approval, requirement.MinApprovers, requirement.RequiredRoles, now, event)
    if err != nil {
        if errors.Is(err, store.ErrConflict) {
            return ApprovalResult{}, ErrApprovalDenied
        }
        return ApprovalResult{}, err
    }
    result := ApprovalResult{Approval: record, Plan: toPlan(updatedPlan)}
    if updatedPlan.Status == Confirmed && s.execution != nil {
        executionResult, err := s.execution.ExecuteConfirmedStoredPlan(ctx, updatedPlan.ID)
        if err != nil {
            return result, err
        }
        result.Executed = true
        result.Execution = executionResult
    }
    return result, nil
}
```

Add `execution` field to `Service`:

```go
type Service struct {
    store     store.ActionPlanStore
    execution ExecutionRunner
    clock     Clock
}

type ExecutionRunner interface {
    ExecuteConfirmedStoredPlan(context.Context, string) (execution.Execution, error)
}

func NewService(repository store.ActionPlanStore, execution ExecutionRunner, clock Clock) *Service {
    if clock == nil {
        clock = systemClock{}
    }
    return &Service{store: repository, execution: execution, clock: clock}
}
```

- [ ] **Step 4: Update CreatePlan to remove token logic**

In `CreatePlan`, remove the `confirmationToken()`, `hash()`, and `ConfirmationToken` assignment. Write plans no longer set `ConfirmationTokenHash`:

```go
if decision.RequiresConfirmation {
    plan.Status = PendingConfirmation
    if err := s.store.CreatePlan(ctx, plan, auditEvent(newID(), plan, "plan_created", string(policy.Permitted), user.Subject, user.RequestID, now, nil)); err != nil {
        return Plan{}, err
    }
    return toPlan(plan), nil
}
```

- [ ] **Step 5: Run GREEN**

Run: `go test -count=1 ./internal/plans`

Expected: PASS (after updating test helpers to pass an execution service to `NewService`).

- [ ] **Step 6: Commit**

```bash
git add internal/plans/service.go internal/plans/service_test.go
git commit -m "feat: add plans service approve and reject"
```

### Task 5: HTTP Approve and Reject Handlers

**Files:**
- Modify: `internal/httpapi/router.go`
- Modify: `internal/httpapi/router_test.go`

**Interfaces:**
- Consumes: `plans.ApprovalResult`, `plans.PlanApprovalService`, `store.ListApprovals`.
- Produces:
  - `type PlanApprovalService interface { Approve(ctx, user, planID) (plans.ApprovalResult, error); Reject(ctx, user, planID) (plans.ApprovalResult, error) }`
  - `func WithPlanApproval(service PlanApprovalService) Option`
  - `POST /v1/action-plans/{id}/approve` handler.
  - `POST /v1/action-plans/{id}/reject` handler.
  - Extended detail response with `approvals` and `approval_requirement`.
  - Removal of `PlanConfirmationService`, `serveConfirmActionPlan`, `WithDevelopmentConfirmationTokens`, `devTokens`.

- [ ] **Step 1: Write failing approve authentication test**

Add to `internal/httpapi/router_test.go`:

```go
func TestApproveActionPlanRequiresAuthentication(t *testing.T) {
    t.Parallel()
    router, _ := testRouter(t, &readRunner{})
    res := httptest.NewRecorder()

    router.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/v1/action-plans/plan-1/approve", strings.NewReader(`{}`)))

    if res.Code != http.StatusUnauthorized {
        t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
    }
}
```

- [ ] **Step 2: Write failing approve behavior tests**

```go
func TestApproveActionPlanLowRiskExecutes(t *testing.T) {
    t.Parallel()
    router, _, planService := testRouterWithPlans(t, &readRunner{})
    plan := createPendingPlan(t, planService)
    req := signedRequest(t, "/v1/action-plans/"+plan.ID+"/approve", `{}`, "admin-2", []string{"admin"}, []string{"prod"})
    res := httptest.NewRecorder()

    router.ServeHTTP(res, req)

    if res.Code != http.StatusOK {
        t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
    }
    body := res.Body.String()
    if !strings.Contains(body, `"executed":true`) {
        t.Fatalf("body = %s, want executed true", body)
    }
    if !strings.Contains(body, `"status":"confirmed"`) {
        t.Fatalf("body = %s, want confirmed status", body)
    }
}

func TestApproveActionPlanMediumRiskQuorumNotMet(t *testing.T) {
    t.Parallel()
    router, _, planService := testRouterWithPlans(t, &readRunner{})
    plan := createPendingPlan(t, planService)
    req := signedRequest(t, "/v1/action-plans/"+plan.ID+"/approve", `{}`, "admin-2", []string{"admin"}, []string{"prod"})
    res := httptest.NewRecorder()

    router.ServeHTTP(res, req)

    if res.Code != http.StatusOK {
        t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
    }
    body := res.Body.String()
    if !strings.Contains(body, `"executed":false`) {
        t.Fatalf("body = %s, want executed false", body)
    }
    if !strings.Contains(body, `"status":"pending_confirmation"`) {
        t.Fatalf("body = %s, want pending status", body)
    }
}
```

Note: `topic.retention.set` is medium risk, so the first approval should keep it pending. To test low risk execution, create a low-risk plan (would need a low-risk write tool, or adjust the test to use the medium-risk plan with two approvers). For now, test medium-risk quorum behavior.

- [ ] **Step 3: Write failing reject test**

```go
func TestRejectActionPlanCancelsPlan(t *testing.T) {
    t.Parallel()
    router, _, planService := testRouterWithPlans(t, &readRunner{})
    plan := createPendingPlan(t, planService)
    req := signedRequest(t, "/v1/action-plans/"+plan.ID+"/reject", `{}`, "admin-2", []string{"admin"}, []string{"prod"})
    res := httptest.NewRecorder()

    router.ServeHTTP(res, req)

    if res.Code != http.StatusOK {
        t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
    }
    body := res.Body.String()
    if !strings.Contains(body, `"status":"cancelled"`) {
        t.Fatalf("body = %s, want cancelled status", body)
    }
}
```

- [ ] **Step 4: Write failing creator self-approval test**

```go
func TestApproveActionPlanRejectsCreatorSelfApproval(t *testing.T) {
    t.Parallel()
    router, _, planService := testRouterWithPlans(t, &readRunner{})
    plan := createPendingPlan(t, planService)
    req := signedRequest(t, "/v1/action-plans/"+plan.ID+"/approve", `{}`, "admin-1", []string{"admin"}, []string{"prod"})
    res := httptest.NewRecorder()

    router.ServeHTTP(res, req)

    if res.Code != http.StatusForbidden {
        t.Fatalf("status = %d body = %s, want 403", res.Code, res.Body.String())
    }
}
```

- [ ] **Step 5: Write failing detail approvals test**

```go
func TestGetActionPlanDetailIncludesApprovals(t *testing.T) {
    t.Parallel()
    router, _, planService := testRouterWithPlans(t, &readRunner{})
    plan := createPendingPlan(t, planService)
    approveReq := signedRequest(t, "/v1/action-plans/"+plan.ID+"/approve", `{}`, "admin-2", []string{"admin"}, []string{"prod"})
    router.ServeHTTP(httptest.NewRecorder(), approveReq)

    req := signedRequest(t, "/v1/action-plans/"+plan.ID, "", "admin-1", []string{"admin"}, []string{"prod"})
    res := httptest.NewRecorder()
    router.ServeHTTP(res, req)

    if res.Code != http.StatusOK {
        t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
    }
    body := res.Body.String()
    if !strings.Contains(body, `"approvals"`) {
        t.Fatalf("body = %s, want approvals field", body)
    }
    if !strings.Contains(body, `"approval_requirement"`) {
        t.Fatalf("body = %s, want approval_requirement field", body)
    }
    if !strings.Contains(body, `"admin-2"`) {
        t.Fatalf("body = %s, want admin-2 in approvals", body)
    }
}
```

- [ ] **Step 6: Run RED**

Run: `go test -count=1 ./internal/httpapi -run 'TestApprove|TestReject|TestGetActionPlanDetailIncludesApprovals'`

Expected: FAIL because approve/reject routes and extended detail do not exist.

- [ ] **Step 7: Implement PlanApprovalService interface and router wiring**

In `internal/httpapi/router.go`:

Remove `PlanConfirmationService`, `WithActionPlanConfirmation`, `WithDevelopmentConfirmationTokens`, `devTokens` field, `serveConfirmActionPlan`.

Add:

```go
type PlanApprovalService interface {
    Approve(context.Context, identity.CurrentUser, string) (plans.ApprovalResult, error)
    Reject(context.Context, identity.CurrentUser, string) (plans.ApprovalResult, error)
}

type ApprovalQueryService interface {
    ListApprovals(context.Context, string) ([]store.ApprovalRecord, error)
}
```

Update `Router` struct:

```go
type Router struct {
    auth        Authenticator
    reads       ReadService
    assistant   AssistantService
    approvals   PlanApprovalService
    execution   ExecutionService
    actionPlans ActionPlanQueryService
    approvalQuery ApprovalQueryService
}
```

Add:

```go
func WithPlanApproval(approvals PlanApprovalService, execution ExecutionService) Option {
    return func(router *Router) {
        router.approvals = approvals
        router.execution = execution
    }
}

func WithApprovalQuery(service ApprovalQueryService) Option {
    return func(router *Router) {
        router.approvalQuery = service
    }
}
```

- [ ] **Step 8: Add route dispatch**

In `ServeHTTP`, replace the confirm route with:

```go
if request.Method == http.MethodPost && strings.HasPrefix(request.URL.Path, "/v1/action-plans/") && strings.HasSuffix(request.URL.Path, "/approve") {
    r.serveApproveActionPlan(writer, request)
    return
}
if request.Method == http.MethodPost && strings.HasPrefix(request.URL.Path, "/v1/action-plans/") && strings.HasSuffix(request.URL.Path, "/reject") {
    r.serveRejectActionPlan(writer, request)
    return
}
```

- [ ] **Step 9: Implement approve and reject handlers**

```go
func (r *Router) serveApproveActionPlan(writer http.ResponseWriter, request *http.Request) {
    r.serveApprovalActionPlan(writer, request, "approve")
}

func (r *Router) serveRejectActionPlan(writer http.ResponseWriter, request *http.Request) {
    r.serveApprovalActionPlan(writer, request, "reject")
}

func (r *Router) serveApprovalActionPlan(writer http.ResponseWriter, request *http.Request, action string) {
    if r.auth == nil || r.approvals == nil || r.actionPlans == nil {
        writeError(writer, http.StatusInternalServerError, "router is not configured")
        return
    }
    user, err := r.auth.Authenticate(request)
    if err != nil {
        writeError(writer, http.StatusUnauthorized, "authentication required")
        return
    }
    suffix := "/" + action
    planID := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/v1/action-plans/"), suffix)
    if strings.TrimSpace(planID) == "" || strings.Contains(planID, "/") {
        writeError(writer, http.StatusNotFound, "action plan not found")
        return
    }
    ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
    defer cancel()
    record, err := r.actionPlans.GetPlan(ctx, planID)
    if err != nil {
        if errors.Is(err, store.ErrNotFound) {
            writeError(writer, http.StatusNotFound, "action plan not found")
            return
        }
        writeError(writer, http.StatusBadGateway, err.Error())
        return
    }
    tool, input, _, ok := canonicalActionPlan(record)
    if !ok {
        writeError(writer, http.StatusNotFound, "action plan not found")
        return
    }
    if !userAllowedEnvironment(user, input["environment"].(string)) {
        writeError(writer, http.StatusForbidden, string(policy.EnvironmentDenied))
        return
    }
    if user.Subject == record.CreatedBy {
        writeError(writer, http.StatusForbidden, "creator cannot approve own plan")
        return
    }
    decision := policy.Evaluate(user, tool, input)
    if !decision.Allowed || !decision.RequiresConfirmation {
        writeError(writer, http.StatusForbidden, string(decision.Reason))
        return
    }

    var result plans.ApprovalResult
    if action == "approve" {
        result, err = r.approvals.Approve(ctx, user, planID)
    } else {
        result, err = r.approvals.Reject(ctx, user, planID)
    }
    if err != nil {
        status := http.StatusBadGateway
        if errors.Is(err, plans.ErrApprovalDenied) {
            status = http.StatusConflict
        }
        writeError(writer, status, err.Error())
        return
    }
    response := map[string]any{
        "approval": map[string]any{
            "id":         result.Approval.ID,
            "approver":   result.Approval.ApproverSubject,
            "decision":   result.Approval.Decision,
            "roles":      result.Approval.Roles,
            "created_at": result.Approval.CreatedAt,
        },
        "plan": map[string]any{
            "id":      result.Plan.ID,
            "status":  string(result.Plan.Status),
            "version": result.Plan.Version,
        },
        "executed": result.Executed,
    }
    if result.Executed {
        response["execution_id"] = result.Execution.ID
        response["execution_status"] = result.Execution.Status
        response["reused"] = result.Execution.Reused
    }
    writeCappedJSON(writer, response)
}
```

- [ ] **Step 10: Extend detail response with approvals**

Update `serveGetActionPlan` to include approvals and approval requirement:

```go
func (r *Router) serveGetActionPlan(writer http.ResponseWriter, request *http.Request) {
    // ... existing authentication and plan lookup ...
    response, ok := shapeActionPlan(record, true)
    if !ok {
        writeError(writer, http.StatusNotFound, "action plan not found")
        return
    }
    if !userAllowedEnvironment(user, response.Environment) {
        writeError(writer, http.StatusForbidden, string(policy.EnvironmentDenied))
        return
    }
    if r.approvalQuery != nil {
        approvals, err := r.approvalQuery.ListApprovals(ctx, planID)
        if err != nil {
            writeError(writer, http.StatusBadGateway, err.Error())
            return
        }
        response.Approvals = shapeApprovals(approvals)
        requirement := policy.ApprovalRequirementFor(tools.RiskLevel(response.Risk))
        response.ApprovalRequirement = shapeApprovalRequirement(requirement, len(approvals))
    }
    writeCappedJSON(writer, response)
}
```

Add to `actionPlanResponse`:

```go
type actionPlanResponse struct {
    // ... existing fields ...
    Approvals          []approvalSummary    `json:"approvals,omitempty"`
    ApprovalRequirement *approvalRequirement `json:"approval_requirement,omitempty"`
}

type approvalSummary struct {
    Approver  string   `json:"approver"`
    Decision  string   `json:"decision"`
    Roles     []string `json:"roles"`
    CreatedAt time.Time `json:"created_at"`
}

type approvalRequirement struct {
    MinApprovers    int            `json:"min_approvers"`
    RequiredRoles   map[string]int `json:"required_roles"`
    CurrentApprovers int           `json:"current_approvers"`
}
```

- [ ] **Step 11: Update test helpers**

Update `testRouterWithPlans` to wire `WithPlanApproval` and `WithApprovalQuery`:

```go
return httpapi.NewRouter(
    httpapi.NewHMACAuthenticator([]byte("test-secret")),
    readService,
    httpapi.WithAssistant(assistantService),
    httpapi.WithActionPlans(repository),
    httpapi.WithPlanApproval(planService, executionService),
    httpapi.WithApprovalQuery(repository),
), repository, planService
```

- [ ] **Step 12: Run GREEN**

Run: `go test -count=1 ./internal/httpapi`

Expected: PASS.

- [ ] **Step 13: Commit**

```bash
git add internal/httpapi/router.go internal/httpapi/router_test.go
git commit -m "feat: add approve and reject action plan endpoints"
```

### Task 6: Remove Token Confirmation and Wire Main

**Files:**
- Modify: `cmd/copilot-api/main.go`
- Modify: `internal/httpapi/router.go` (cleanup)
- Modify: `internal/assistant/service.go` (if it references confirmation token)

- [ ] **Step 1: Update main.go**

Remove `COPILOT_DEV_EXPOSE_CONFIRMATION_TOKEN` logic. Update `routerOptions`:

```go
func routerOptions(repository httpapi.ActionPlanQueryService, assistantService httpapi.AssistantService, planService httpapi.PlanApprovalService, executionService httpapi.ExecutionService) []httpapi.Option {
    return []httpapi.Option{
        httpapi.WithAssistant(assistantService),
        httpapi.WithActionPlans(repository),
        httpapi.WithPlanApproval(planService, executionService),
        httpapi.WithApprovalQuery(repository),
    }
}
```

Update `planService` construction to pass `executionService`:

```go
planService := plans.NewService(repository, executionService, nil)
```

- [ ] **Step 2: Remove assistant token exposure**

In `internal/assistant/service.go`, remove `ConfirmationToken` from `Response` if present. Check and remove any `devTokens` branch in the assistant response.

- [ ] **Step 3: Run full Go build**

Run: `go build ./...`

Expected: compilation succeeds with no token references.

- [ ] **Step 4: Commit**

```bash
git add cmd/copilot-api/main.go internal/httpapi/router.go internal/assistant/service.go
git commit -m "refactor: remove token confirmation and wire plan approval"
```

### Task 7: E2E Approve and Reject Tests

**Files:**
- Modify: `tests/e2e/assistant_test.go`

- [ ] **Step 1: Write failing e2e approve test**

Replace the existing confirm e2e test with:

```go
func TestAssistantWritePlanApproveQuorumExecutesInSQLite(t *testing.T) {
    db := testSQLiteDB(t)
    repository := store.NewSQLActionPlanStore(db)
    planService := plans.NewService(repository, executionService, fixedClock())
    assistantService := assistant.NewService(assistant.DeterministicPlanner{}, readService, planService)
    router := httpapi.NewRouter(
        httpapi.NewHMACAuthenticator([]byte("test-secret")),
        readService,
        httpapi.WithAssistant(assistantService),
        httpapi.WithActionPlans(repository),
        httpapi.WithPlanApproval(planService, executionService),
        httpapi.WithApprovalQuery(repository),
    )

    // assistant creates write plan
    createReq := signedRequest(t, "/v1/assistant/messages", `{"message":"把 prod 的 orders topic retention 改成 72 小时"}`, "operator-1", []string{"operator"}, []string{"prod"})
    createRes := httptest.NewRecorder()
    router.ServeHTTP(createRes, createReq)
    // ... assert plan created, extract plan_id ...

    // first admin approves (quorum not met for medium risk)
    approveReq1 := signedRequest(t, "/v1/action-plans/"+planID+"/approve", `{}`, "admin-1", []string{"admin"}, []string{"prod"})
    approveRes1 := httptest.NewRecorder()
    router.ServeHTTP(approveRes1, approveRes1)
    // ... assert executed: false ...

    // second admin approves (quorum met)
    approveReq2 := signedRequest(t, "/v1/action-plans/"+planID+"/approve", `{}`, "admin-2", []string{"admin"}, []string{"prod"})
    approveRes2 := httptest.NewRecorder()
    router.ServeHTTP(approveRes2, approveRes2)
    // ... assert executed: true, status: confirmed ...
}
```

- [ ] **Step 2: Write failing e2e reject test**

```go
func TestAssistantWritePlanRejectCancelsInSQLite(t *testing.T) {
    // ... setup ...
    // assistant creates plan
    // admin rejects
    // assert status: cancelled, executed: false
}
```

- [ ] **Step 3: Run RED**

Run: `go test -count=1 ./tests/e2e -run TestAssistantWritePlanApprove`

Expected: FAIL.

- [ ] **Step 4: Run GREEN**

Run: `go test -count=1 ./tests/e2e`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tests/e2e/assistant_test.go
git commit -m "test: cover approve and reject e2e flow"
```

### Task 8: Frontend PlansPanel Extraction and Approval UI

**Files:**
- Modify: `apps/console/src/App.tsx`
- Create: `apps/console/src/components/PlansPanel.tsx`
- Create: `apps/console/src/components/PlanList.tsx`
- Create: `apps/console/src/components/PlanDetail.tsx`
- Create: `apps/console/src/components/ApprovalActions.tsx`
- Modify: `apps/console/src/App.test.tsx`
- Modify: `apps/console/src/styles.css`

- [ ] **Step 1: Write failing frontend approve test**

Add to `apps/console/src/App.test.tsx`:

```tsx
it("approves a pending plan", async () => {
  const fetchMock = vi.mocked(fetch);
  fetchMock
    .mockResolvedValueOnce(new Response(JSON.stringify({ plans: [{ id: "plan-123", tool: "topic.retention.set", environment: "prod", risk: "medium", status: "pending_confirmation", version: 1, expires_at: "2026-07-22T12:10:00Z", created_by: "operator-1", created_at: "2026-07-22T12:00:00Z" }] }), { status: 200, headers: { "Content-Type": "application/json" } }))
    .mockResolvedValueOnce(new Response(JSON.stringify({ id: "plan-123", tool: "topic.retention.set", environment: "prod", risk: "medium", status: "pending_confirmation", version: 1, expires_at: "2026-07-22T12:10:00Z", created_by: "operator-1", created_at: "2026-07-22T12:00:00Z", input: { environment: "prod", topic: "orders", retention_hours: 72 }, approvals: [], approval_requirement: { min_approvers: 2, required_roles: { admin: 1 }, current_approvers: 0 } }), { status: 200, headers: { "Content-Type": "application/json" } }))
    .mockResolvedValueOnce(new Response(JSON.stringify({ approval: { id: "approval-1", approver: "admin-1", decision: "approve", roles: ["admin"], created_at: "2026-07-22T12:01:00Z" }, plan: { id: "plan-123", status: "pending_confirmation", version: 1 }, executed: false }), { status: 200, headers: { "Content-Type": "application/json" } }));
  render(<App />);

  await userEvent.click(screen.getByRole("button", { name: "刷新计划" }));
  await userEvent.click(await screen.findByRole("button", { name: /plan-123/ }));
  await userEvent.click(screen.getByRole("button", { name: "批准" }));

  await waitFor(() => expect(screen.getByText(/等待其他审批人/)).toBeInTheDocument());
  expect(fetchMock).toHaveBeenLastCalledWith(
    "/api/v1/action-plans/plan-123/approve",
    expect.objectContaining({ method: "POST" })
  );
});
```

- [ ] **Step 2: Write failing reject test**

```tsx
it("rejects a pending plan", async () => {
  // ... similar setup ...
  await userEvent.click(screen.getByRole("button", { name: "拒绝" }));
  // ... assert cancelled status ...
});
```

- [ ] **Step 3: Write failing creator hidden buttons test**

```tsx
it("hides approve/reject buttons for plan creator", async () => {
  // ... create plan with created_by: "operator-1" ...
  // ... set current subject to "operator-1" ...
  // ... assert buttons not present ...
});
```

- [ ] **Step 4: Run RED**

Run: `npm test -- --run`

Expected: FAIL.

- [ ] **Step 5: Create PlansPanel component**

Create `apps/console/src/components/PlansPanel.tsx`:

```tsx
import { useState } from "react";
import { PlanList } from "./PlanList";
import { PlanDetail } from "./PlanDetail";

type PendingPlanSummary = { /* ... */ };
type PendingPlanDetail = PendingPlanSummary & { input: Record<string, unknown>; approvals: ApprovalSummary[]; approval_requirement: ApprovalRequirement };

export function PlansPanel({ /* props */ }) {
  // ... extracted from App.tsx ...
}
```

- [ ] **Step 6: Create PlanList component**

Create `apps/console/src/components/PlanList.tsx` with the list rendering and progress indicator.

- [ ] **Step 7: Create PlanDetail component**

Create `apps/console/src/components/PlanDetail.tsx` with detail and approval progress rendering.

- [ ] **Step 8: Create ApprovalActions component**

Create `apps/console/src/components/ApprovalActions.tsx`:

```tsx
export function ApprovalActions({ isCreator, hasApproved, onApprove, onReject, approving, rejecting }) {
  if (isCreator) {
    return <p className="hint">你不能审批自己创建的计划。</p>;
  }
  if (hasApproved) {
    return <p className="hint">你已批准，等待其他审批人。</p>;
  }
  return (
    <div className="approvalActions">
      <button type="button" onClick={onApprove} disabled={approving || rejecting}>批准</button>
      <button type="button" onClick={onReject} disabled={approving || rejecting}>拒绝</button>
    </div>
  );
}
```

- [ ] **Step 9: Update App.tsx**

Remove `planTokens` state, token capture logic, `confirmSelectedPlan`, and `confirmPlan`. Replace `PlansPanel` inline rendering with the extracted component. Add `approving` and `rejecting` state.

- [ ] **Step 10: Update styles.css**

Add styles for `.approvalActions`, `.approvalProgress`, `.cancelled`.

- [ ] **Step 11: Run GREEN**

Run: `npm test -- --run`

Expected: PASS.

- [ ] **Step 12: Run build**

Run: `npm run build`

Expected: PASS.

- [ ] **Step 13: Commit**

```bash
git add apps/console/src/
git commit -m "feat: add approval UI with extracted PlansPanel components"
```

### Task 9: Final Acceptance

**Files:**
- No new files.
- Verify all touched code.

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
rg -n "confirmation_token" internal apps/console/src cmd README.md
```

Expected:
- No `confirmation_token` references in production code paths.
- `confirmation_token_hash` column may still exist in migrations/store but is unused.

- [ ] **Step 7: Inspect dev token env var**

Run:

```bash
rg -n "COPILOT_DEV_EXPOSE_CONFIRMATION_TOKEN" .
```

Expected: No references remain.

- [ ] **Step 8: Commit any verification-only doc updates**

If no files changed during acceptance, do not commit. If README or docs were updated during execution, run:

```bash
git add README.md docs
git commit -m "docs: update risk-tiered countersign notes"
```
