package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
)

var (
	ErrNotFound = errors.New("record not found")
	ErrConflict = errors.New("state changed")
)

type PlanStatus string

const (
	PlanPendingConfirmation PlanStatus = "pending_confirmation"
	PlanConfirmed           PlanStatus = "confirmed"
	// PlanRejected 是显式「拒绝」后的终态：运营者确认非故障但不想执行，让该 plan
	// 从待确认队列里消失而非等过期。rejected 的 plan 永不执行（execution 端只认
	// PlanConfirmed）。
	PlanRejected PlanStatus = "rejected"
)

type PlanFilter struct {
	Status PlanStatus

	// Limit is the page size for keyset pagination. When > 0, ListPlans
	// returns at most Limit plans in descending created_at order and
	// populates PlanPage.NextCursor. When 0, the legacy non-paginated
	// behavior is preserved (all matching plans, ascending order).
	Limit           int
	CursorCreatedAt time.Time
	CursorID        string
}

// PlanCursor identifies the last plan of a page for keyset pagination.
type PlanCursor struct {
	CreatedAt time.Time
	ID        string
}

// PlanPage is a page of action plans plus the cursor to fetch the next page.
type PlanPage struct {
	Plans      []PlanRecord
	NextCursor PlanCursor
}

// AuditFilter scopes audit event queries. Empty fields match all values.
// CreatedAfter and CreatedBefore are inclusive boundaries on CreatedAt.
// CursorCreatedAt+CursorID form an exclusive cursor for keyset pagination
// (events strictly older than the cursor are returned). When set, ListAudit
// returns events in descending created_at order so callers can page back in
// time. NextCursor in the result points at the oldest event of the current
// page; pass it back as the cursor to fetch the next page.
type AuditFilter struct {
	ToolName        string
	Action          string
	Decision        string
	Subject         string
	CreatedAfter    time.Time
	CreatedBefore   time.Time
	CursorCreatedAt time.Time
	CursorID        string
	Limit           int
	// FinalResultOnly hides "rejected / never-executed" audit events
	// (plan_rejected, execution_rejected) so the event center's "final result"
	// view focuses on what actually happened rather than dismissed approval
	// flows (借鉴-4: 事件中心最终结果过滤).
	FinalResultOnly bool
}

// AuditPage is a page of audit events plus the cursor to fetch the next page.
// NextCursor is empty when there are no more events to fetch.
type AuditPage struct {
	Events     []AuditEvent
	NextCursor AuditCursor
}

// AuditCursor identifies the last event of a page for keyset pagination.
// Callers should treat it as opaque and pass it back as AuditFilter.Cursor.
type AuditCursor struct {
	CreatedAt time.Time
	ID        string
}

// rejectedAuditActions lists the audit action values that represent a
// "dismissed / never-executed approval flow". 借鉴-4: 事件中心"最终结果过滤"
// 视图默认隐藏这些事件，让复盘聚焦在真正发生的结果上。值与 audit 包的
// ActionPlanRejected / ActionExecutionRejected 对应；store 层用字符串字面量
// 避免反向依赖 audit 包（audit → store，不可反过来）。
var rejectedAuditActions = map[string]struct{}{
	"plan_rejected":      {},
	"execution_rejected": {},
}

// IsRejectedAuditAction reports whether action represents a rejected /
// never-executed event. AuditFilter.FinalResultOnly uses this single helper
// so the in-memory and SQL stores share one definition of "rejected".
func IsRejectedAuditAction(action string) bool {
	_, ok := rejectedAuditActions[action]
	return ok
}

// RejectedAuditActionValues returns the rejected action values for SQL
// IN / NOT IN clauses. Kept in sync with IsRejectedAuditAction.
func RejectedAuditActionValues() []string {
	return []string{"plan_rejected", "execution_rejected"}
}

type PlanRecord struct {
	ID                    string
	RequestID             string
	CreatedBy             string
	ToolName              string
	InputJSON             []byte
	InputHash             string
	RiskLevel             string
	Status                PlanStatus
	Version               uint
	ConfirmationTokenHash string
	ConfirmedBy           string
	ConfirmedAt           *time.Time
	ExpiresAt             time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
	// DryRun 是写计划创建时的 dry-run 预览结果（结果准 #5），JSON 序列化后
	// 持久化在 action_plans.dry_run 列。供复盘确认时的完整执行计划。
	DryRun []byte
}

type ExecutionRecord struct {
	ID             string
	ActionPlanID   string
	IdempotencyKey string
	Status         string
	ResultSummary  []byte
	ErrorSummary   string
	StartedAt      *time.Time
	CompletedAt    *time.Time
	CreatedAt      time.Time
	// Verification 是执行后验证结果（结果准 #5），JSON 序列化后持久化在
	// tool_executions.verification 列。执行成功且配置了 verifier 时填充。
	Verification []byte
	// ToolName is NOT persisted in tool_executions; it is populated by
	// ListExecutions via JOIN on action_plans. Empty when the record was
	// loaded through CreateExecutionIfAbsent / CompleteExecution (which do
	// not join). This lets API consumers filter by tool_name and display it
	// without a second round-trip (R5 结果准 - execution 查询 API).
	ToolName string
}

// ExecutionFilter scopes tool_executions queries. Empty fields match all
// values. CursorCreatedAt+CursorID form an exclusive cursor for keyset
// pagination (executions strictly older than the cursor are returned).
// When Limit > 0, ListExecutions returns at most Limit executions in
// descending created_at order and populates NextCursor. ToolName filters
// via JOIN on action_plans (R5 结果准 - execution 查询 API).
type ExecutionFilter struct {
	Status          string
	ActionPlanID    string
	ToolName        string
	StartedAfter    time.Time
	StartedBefore   time.Time
	CursorCreatedAt time.Time
	CursorID        string
	Limit           int
}

// ExecutionPage is a page of executions plus the cursor to fetch the next page.
// NextCursor is empty when there are no more executions to fetch.
type ExecutionPage struct {
	Executions []ExecutionRecord
	NextCursor ExecutionCursor
}

// ExecutionCursor identifies the last execution of a page for keyset pagination.
type ExecutionCursor struct {
	CreatedAt time.Time
	ID        string
}

type AuditEvent struct {
	ID          string
	PlanID      string
	ExecutionID string
	RequestID   string
	Subject     string
	ToolName    string
	Action      string
	Decision    string
	Metadata    map[string]any
	// TraceID correlates the audit event with the OpenTelemetry trace that
	// produced it. Empty when no span is active in the recording context (e.g.
	// scheduler invocations without a parent request), so callers can
	// unambiguously detect "no trace" rather than guessing.
	TraceID   string
	CreatedAt time.Time
}

// ActionPlanStore is the transactional persistence boundary shared by plans,
// execution, and audit. Its conditional methods are the authoritative guards
// against duplicate confirmations and executions.
type ActionPlanStore interface {
	CreatePlan(context.Context, PlanRecord, AuditEvent) error
	GetPlan(context.Context, string) (PlanRecord, error)
	ListPlans(context.Context, PlanFilter) (PlanPage, error)
	ConfirmPlan(context.Context, string, uint, string, string, time.Time, AuditEvent) (PlanRecord, error)
	// RejectPlan 做一次从 pending_confirmation 到 rejected 的乐观迁移（幂等，绑定
	// status+version+expiry）。返回迁移后的 PlanRecord；冲突返回 ErrConflict。
	// 与 ConfirmPlan 同一套乐观并发语义，但不需要 confirmation_token_hash。
	RejectPlan(context.Context, string, uint, string, time.Time, AuditEvent) (PlanRecord, error)
	// SetPlanDryRun 在 plan 创建后持久化 dry-run 预览结果（结果准 #5）。
	// dryRun 为 JSON 序列化的 DryRunResult；空则清除。
	SetPlanDryRun(context.Context, string, []byte) error
	CreateExecutionIfAbsent(context.Context, ExecutionRecord, AuditEvent) (ExecutionRecord, bool, error)
	CompleteExecution(context.Context, string, string, []byte, string, AuditEvent) error
	// SetExecutionVerification 在执行完成后持久化验证结果（结果准 #5）。
	// verification 为 JSON 序列化的 VerificationResult；空则清除。
	SetExecutionVerification(context.Context, string, []byte) error
	ListExecutions(context.Context, ExecutionFilter) (ExecutionPage, error)
	AppendAudit(context.Context, AuditEvent) error
	ListAudit(context.Context, AuditFilter) (AuditPage, error)
}

// MemoryActionPlanStore supplies deterministic unit-test persistence while
// implementing the same conflict and uniqueness rules as MySQL.
type MemoryActionPlanStore struct {
	mu         sync.Mutex
	plans      map[string]PlanRecord
	executions map[string]ExecutionRecord
	keys       map[string]string
	audits     []AuditEvent
}

func NewMemoryActionPlanStore() *MemoryActionPlanStore {
	return &MemoryActionPlanStore{
		plans:      make(map[string]PlanRecord),
		executions: make(map[string]ExecutionRecord),
		keys:       make(map[string]string),
	}
}

func (s *MemoryActionPlanStore) CreatePlan(_ context.Context, plan PlanRecord, event AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.plans[plan.ID]; exists {
		return ErrConflict
	}
	s.plans[plan.ID] = clonePlan(plan)
	s.audits = append(s.audits, cloneAudit(event))
	return nil
}

func (s *MemoryActionPlanStore) GetPlan(_ context.Context, id string) (PlanRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, ok := s.plans[id]
	if !ok {
		return PlanRecord{}, ErrNotFound
	}
	return clonePlan(plan), nil
}

func (s *MemoryActionPlanStore) ListPlans(_ context.Context, filter PlanFilter) (PlanPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	plans := make([]PlanRecord, 0, len(s.plans))
	for _, plan := range s.plans {
		if filter.Status != "" && plan.Status != filter.Status {
			continue
		}
		plans = append(plans, clonePlan(plan))
	}

	// Paginated: descending (newest first), keyset cursor excludes the
	// cursor plan and anything newer. Limit is the page size.
	if filter.Limit > 0 {
		sort.Slice(plans, func(i, j int) bool {
			if plans[i].CreatedAt.Equal(plans[j].CreatedAt) {
				return plans[i].ID > plans[j].ID
			}
			return plans[i].CreatedAt.After(plans[j].CreatedAt)
		})
		kept := plans[:0]
		for _, plan := range plans {
			if !filter.CursorCreatedAt.IsZero() && filter.CursorID != "" {
				if plan.CreatedAt.After(filter.CursorCreatedAt) {
					continue
				}
				if plan.CreatedAt.Equal(filter.CursorCreatedAt) && plan.ID >= filter.CursorID {
					continue
				}
			}
			kept = append(kept, plan)
		}
		return trimmedPlanPage(kept, filter.Limit), nil
	}

	// Legacy: chronological ascending, no cursor.
	sort.Slice(plans, func(i, j int) bool {
		if plans[i].CreatedAt.Equal(plans[j].CreatedAt) {
			return plans[i].ID < plans[j].ID
		}
		return plans[i].CreatedAt.Before(plans[j].CreatedAt)
	})
	return PlanPage{Plans: plans}, nil
}

// trimmedPlanPage applies the limit and returns the next cursor pointing at
// the oldest plan in the page. Empty NextCursor means no more plans.
func trimmedPlanPage(plans []PlanRecord, limit int) PlanPage {
	if limit <= 0 || len(plans) <= limit {
		return PlanPage{Plans: plans}
	}
	page := plans[:limit]
	last := page[len(page)-1]
	return PlanPage{Plans: page, NextCursor: PlanCursor{CreatedAt: last.CreatedAt, ID: last.ID}}
}

func (s *MemoryActionPlanStore) ConfirmPlan(_ context.Context, id string, version uint, tokenHash, subject string, now time.Time, event AuditEvent) (PlanRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, ok := s.plans[id]
	if !ok {
		return PlanRecord{}, ErrNotFound
	}
	if plan.Status != PlanPendingConfirmation || plan.Version != version || plan.ConfirmationTokenHash != tokenHash || !plan.ExpiresAt.After(now) {
		return PlanRecord{}, ErrConflict
	}
	plan.Status = PlanConfirmed
	plan.Version++
	plan.ConfirmedBy = subject
	plan.ConfirmedAt = pointerTo(now)
	plan.UpdatedAt = now
	s.plans[id] = plan
	s.audits = append(s.audits, cloneAudit(event))
	return clonePlan(plan), nil
}

func (s *MemoryActionPlanStore) RejectPlan(_ context.Context, id string, version uint, subject string, now time.Time, event AuditEvent) (PlanRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, ok := s.plans[id]
	if !ok {
		return PlanRecord{}, ErrNotFound
	}
	// 幂等：已显式 rejected 的 plan 再次拒绝返回既有结果（不再断言 pending）。
	if plan.Status == PlanRejected {
		return clonePlan(plan), nil
	}
	if plan.Status != PlanPendingConfirmation || plan.Version != version || plan.ExpiresAt.Before(now) {
		return PlanRecord{}, ErrConflict
	}
	plan.Status = PlanRejected
	plan.Version++
	plan.UpdatedAt = now
	s.plans[id] = plan
	s.audits = append(s.audits, cloneAudit(event))
	return clonePlan(plan), nil
}

func (s *MemoryActionPlanStore) SetPlanDryRun(_ context.Context, id string, dryRun []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, ok := s.plans[id]
	if !ok {
		return ErrNotFound
	}
	plan.DryRun = append([]byte(nil), dryRun...)
	plan.UpdatedAt = time.Now().UTC()
	s.plans[id] = plan
	return nil
}

func (s *MemoryActionPlanStore) CreateExecutionIfAbsent(_ context.Context, execution ExecutionRecord, event AuditEvent) (ExecutionRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existingID, ok := s.keys[execution.IdempotencyKey]; ok {
		event.Action = "execution_reused"
		event.ExecutionID = existingID
		s.audits = append(s.audits, cloneAudit(event))
		return cloneExecution(s.executions[existingID]), true, nil
	}
	s.executions[execution.ID] = cloneExecution(execution)
	s.keys[execution.IdempotencyKey] = execution.ID
	s.audits = append(s.audits, cloneAudit(event))
	return cloneExecution(execution), false, nil
}

func (s *MemoryActionPlanStore) CompleteExecution(_ context.Context, id, status string, result []byte, errorSummary string, event AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	execution, ok := s.executions[id]
	if !ok {
		return ErrNotFound
	}
	if execution.Status != "running" {
		return ErrConflict
	}
	execution.Status = status
	execution.ResultSummary = append([]byte(nil), result...)
	execution.ErrorSummary = errorSummary
	execution.CompletedAt = pointerTo(event.CreatedAt)
	s.executions[id] = execution
	s.audits = append(s.audits, cloneAudit(event))
	return nil
}

func (s *MemoryActionPlanStore) SetExecutionVerification(_ context.Context, id string, verification []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	execution, ok := s.executions[id]
	if !ok {
		return ErrNotFound
	}
	execution.Verification = append([]byte(nil), verification...)
	s.executions[id] = execution
	return nil
}

func (s *MemoryActionPlanStore) AppendAudit(_ context.Context, event AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audits = append(s.audits, cloneAudit(event))
	return nil
}

func (s *MemoryActionPlanStore) ListAudit(_ context.Context, filter AuditFilter) (AuditPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := make([]AuditEvent, 0, len(s.audits))
	for _, event := range s.audits {
		if filter.ToolName != "" && event.ToolName != filter.ToolName {
			continue
		}
		if filter.Action != "" && event.Action != filter.Action {
			continue
		}
		if filter.Decision != "" && event.Decision != filter.Decision {
			continue
		}
		if filter.Subject != "" && event.Subject != filter.Subject {
			continue
		}
		if !filter.CreatedAfter.IsZero() && event.CreatedAt.Before(filter.CreatedAfter) {
			continue
		}
		if !filter.CreatedBefore.IsZero() && event.CreatedAt.After(filter.CreatedBefore) {
			continue
		}
		// 借鉴-4: 最终结果过滤——隐藏驳回/未执行事件，让复盘聚焦真正发生的结果。
		if filter.FinalResultOnly && IsRejectedAuditAction(event.Action) {
			continue
		}
		filtered = append(filtered, cloneAudit(event))
	}

	// Paginated: descending (newest first), keyset cursor excludes the cursor
	// event and anything newer. Limit is the page size.
	if filter.Limit > 0 {
		sort.Slice(filtered, func(i, j int) bool {
			if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
				return filtered[i].ID > filtered[j].ID
			}
			return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
		})
		kept := filtered[:0]
		for _, event := range filtered {
			if !filter.CursorCreatedAt.IsZero() && filter.CursorID != "" {
				if event.CreatedAt.After(filter.CursorCreatedAt) {
					continue
				}
				if event.CreatedAt.Equal(filter.CursorCreatedAt) && event.ID >= filter.CursorID {
					continue
				}
			}
			kept = append(kept, event)
		}
		return trimmedAuditPage(kept, filter.Limit), nil
	}

	// Legacy: chronological ascending, no cursor.
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].ID < filtered[j].ID
		}
		return filtered[i].CreatedAt.Before(filtered[j].CreatedAt)
	})
	return AuditPage{Events: filtered}, nil
}

// trimmedAuditPage applies the limit and returns the next cursor pointing at
// the oldest event in the page. Empty NextCursor means no more events.
func trimmedAuditPage(events []AuditEvent, limit int) AuditPage {
	if limit <= 0 || len(events) <= limit {
		return AuditPage{Events: events}
	}
	page := events[:limit]
	last := page[len(page)-1]
	return AuditPage{Events: page, NextCursor: AuditCursor{CreatedAt: last.CreatedAt, ID: last.ID}}
}

func (s *MemoryActionPlanStore) AuditEvents() []AuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := make([]AuditEvent, len(s.audits))
	for i, event := range s.audits {
		events[i] = cloneAudit(event)
	}
	return events
}

// ExecutionRecords returns copies for deterministic persistence tests.
func (s *MemoryActionPlanStore) ExecutionRecords() []ExecutionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	records := make([]ExecutionRecord, 0, len(s.executions))
	for _, record := range s.executions {
		records = append(records, cloneExecution(record))
	}
	return records
}

// ListExecutions 按过滤条件查询执行记录，按 created_at DESC 排序，支持 keyset 分页。
// ToolName 过滤通过关联 plans[action_plan_id].ToolName 实现（内存版无需 JOIN）。
// started_after/before 对 started_at 为 nil 的记录（running 状态）不匹配。
func (s *MemoryActionPlanStore) ListExecutions(_ context.Context, filter ExecutionFilter) (ExecutionPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records := make([]ExecutionRecord, 0, len(s.executions))
	for _, record := range s.executions {
		if filter.Status != "" && record.Status != filter.Status {
			continue
		}
		if filter.ActionPlanID != "" && record.ActionPlanID != filter.ActionPlanID {
			continue
		}
		// ToolName 需关联 plan：内存版直接查 plans map。
		if filter.ToolName != "" {
			plan, ok := s.plans[record.ActionPlanID]
			if !ok || plan.ToolName != filter.ToolName {
				continue
			}
		}
		if !filter.StartedAfter.IsZero() {
			if record.StartedAt == nil || record.StartedAt.Before(filter.StartedAfter) {
				continue
			}
		}
		if !filter.StartedBefore.IsZero() {
			if record.StartedAt == nil || record.StartedAt.After(filter.StartedBefore) {
				continue
			}
		}
		// 填充 ToolName（非持久化字段，查询时从关联 plan 取）
		enriched := cloneExecution(record)
		if plan, ok := s.plans[record.ActionPlanID]; ok {
			enriched.ToolName = plan.ToolName
		}
		records = append(records, enriched)
	}

	// 按 created_at DESC, id DESC 排序（与 PlanFilter 一致）
	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ID > records[j].ID
		}
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})

	// keyset 分页：cursor 排除 cursor 及更新的记录
	if filter.Limit > 0 {
		if !filter.CursorCreatedAt.IsZero() && filter.CursorID != "" {
			kept := records[:0]
			for _, r := range records {
				if r.CreatedAt.After(filter.CursorCreatedAt) {
					continue
				}
				if r.CreatedAt.Equal(filter.CursorCreatedAt) && r.ID >= filter.CursorID {
					continue
				}
				kept = append(kept, r)
			}
			records = kept
		}
		return trimmedExecutionPage(records, filter.Limit), nil
	}

	return ExecutionPage{Executions: records}, nil
}

// trimmedExecutionPage applies the limit and returns the next cursor pointing
// at the oldest execution in the page. Empty NextCursor means no more records.
func trimmedExecutionPage(records []ExecutionRecord, limit int) ExecutionPage {
	if limit <= 0 || len(records) <= limit {
		return ExecutionPage{Executions: records}
	}
	page := records[:limit]
	last := page[len(page)-1]
	return ExecutionPage{Executions: page, NextCursor: ExecutionCursor{CreatedAt: last.CreatedAt, ID: last.ID}}
}

// MySQLActionPlanStore persists state transitions using conditional MySQL
// updates and transactions. It is safe for concurrent service instances.
type MySQLActionPlanStore struct{ db *sql.DB }

func NewMySQLActionPlanStore(db *sql.DB) *MySQLActionPlanStore { return &MySQLActionPlanStore{db: db} }

// NewSQLActionPlanStore returns the shared SQL implementation used by MySQL
// in production and SQLite in local tests.
func NewSQLActionPlanStore(db *sql.DB) *MySQLActionPlanStore { return NewMySQLActionPlanStore(db) }

func (s *MySQLActionPlanStore) CreatePlan(ctx context.Context, plan PlanRecord, event AuditEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO action_plans
		(id, request_id, created_by, tool_name, input_json, input_hash, risk_level, status, version, confirmation_token_hash, expires_at, created_at, updated_at, dry_run)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		plan.ID, plan.RequestID, plan.CreatedBy, plan.ToolName, plan.InputJSON, plan.InputHash, plan.RiskLevel, plan.Status, plan.Version, nullableString(plan.ConfirmationTokenHash), plan.ExpiresAt, plan.CreatedAt, plan.UpdatedAt, nullableJSON(plan.DryRun))
	if err != nil {
		return err
	}
	if err := insertAudit(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *MySQLActionPlanStore) GetPlan(ctx context.Context, id string) (PlanRecord, error) {
	plan, err := scanPlan(s.db.QueryRowContext(ctx, `SELECT id, request_id, created_by, tool_name, input_json, input_hash, risk_level, status, version, confirmation_token_hash, confirmed_by, confirmed_at, expires_at, created_at, updated_at, dry_run FROM action_plans WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return PlanRecord{}, ErrNotFound
	}
	return plan, err
}

func (s *MySQLActionPlanStore) ListPlans(ctx context.Context, filter PlanFilter) (PlanPage, error) {
	query := `SELECT id, request_id, created_by, tool_name, input_json, input_hash, risk_level, status, version, confirmation_token_hash, confirmed_by, confirmed_at, expires_at, created_at, updated_at, dry_run FROM action_plans`
	conditions := []string{}
	args := []any{}
	if filter.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, filter.Status)
	}

	paginated := filter.Limit > 0
	if paginated && !filter.CursorCreatedAt.IsZero() && filter.CursorID != "" {
		conditions = append(conditions, "(created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, filter.CursorCreatedAt, filter.CursorCreatedAt, filter.CursorID)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	if paginated {
		query += " ORDER BY created_at DESC, id DESC"
	} else {
		query += " ORDER BY created_at DESC, id DESC"
	}

	fetchLimit := filter.Limit
	if paginated {
		fetchLimit++
	}
	if fetchLimit > 0 {
		query += " LIMIT ?"
		args = append(args, fetchLimit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return PlanPage{}, err
	}
	defer func() { _ = rows.Close() }()
	var plans []PlanRecord
	for rows.Next() {
		plan, err := scanPlanRows(rows)
		if err != nil {
			return PlanPage{}, err
		}
		plans = append(plans, plan)
	}
	if err := rows.Err(); err != nil {
		return PlanPage{}, err
	}

	page := PlanPage{Plans: plans}
	if paginated && len(plans) > filter.Limit {
		page.Plans = plans[:filter.Limit]
		last := page.Plans[len(page.Plans)-1]
		page.NextCursor = PlanCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func (s *MySQLActionPlanStore) ConfirmPlan(ctx context.Context, id string, version uint, tokenHash, subject string, now time.Time, event AuditEvent) (PlanRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PlanRecord{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE action_plans SET status = ?, version = version + 1, confirmed_by = ?, confirmed_at = ?, updated_at = ?
		WHERE id = ? AND status = ? AND version = ? AND confirmation_token_hash = ? AND expires_at > ?`, PlanConfirmed, subject, now, now, id, PlanPendingConfirmation, version, tokenHash, now)
	if err != nil {
		return PlanRecord{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return PlanRecord{}, err
	}
	if affected != 1 {
		return PlanRecord{}, ErrConflict
	}
	if err := insertAudit(ctx, tx, event); err != nil {
		return PlanRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return PlanRecord{}, err
	}
	return s.GetPlan(ctx, id)
}

func (s *MySQLActionPlanStore) RejectPlan(ctx context.Context, id string, version uint, subject string, now time.Time, event AuditEvent) (PlanRecord, error) {
	// 先尝试幂等：已 rejected 的 plan 直接返回（无需事务）。并发下仍可能
	// pending→rejected 竞争，由下面的乐观 UPDATE 兜底。
	if existing, err := s.GetPlan(ctx, id); err == nil && existing.Status == PlanRejected {
		return existing, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PlanRecord{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE action_plans SET status = ?, version = version + 1, updated_at = ?
		WHERE id = ? AND status = ? AND version = ? AND expires_at > ?`, PlanRejected, now, id, PlanPendingConfirmation, version, now)
	if err != nil {
		return PlanRecord{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return PlanRecord{}, err
	}
	if affected != 1 {
		return PlanRecord{}, ErrConflict
	}
	if err := insertAudit(ctx, tx, event); err != nil {
		return PlanRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return PlanRecord{}, err
	}
	return s.GetPlan(ctx, id)
}

func (s *MySQLActionPlanStore) CreateExecutionIfAbsent(ctx context.Context, execution ExecutionRecord, event AuditEvent) (ExecutionRecord, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ExecutionRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO tool_executions (id, action_plan_id, idempotency_key, status, started_at, created_at) VALUES (?, ?, ?, ?, ?, ?)`, execution.ID, execution.ActionPlanID, execution.IdempotencyKey, execution.Status, execution.StartedAt, execution.CreatedAt)
	reused := false
	if err != nil {
		if !isDuplicateKey(err) {
			return ExecutionRecord{}, false, err
		}
		reused = true
		event.Action = "execution_reused"
		execution, err = scanExecution(tx.QueryRowContext(ctx, `SELECT id, action_plan_id, idempotency_key, status, result_summary, error_summary, started_at, completed_at, created_at FROM tool_executions WHERE idempotency_key = ?`, execution.IdempotencyKey))
		if err != nil {
			return ExecutionRecord{}, false, err
		}
		event.ExecutionID = execution.ID
	}
	if err := insertAudit(ctx, tx, event); err != nil {
		return ExecutionRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return ExecutionRecord{}, false, err
	}
	return execution, reused, nil
}

func (s *MySQLActionPlanStore) CompleteExecution(ctx context.Context, id, status string, result []byte, errorSummary string, event AuditEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	updated, err := tx.ExecContext(ctx, `UPDATE tool_executions SET status = ?, result_summary = ?, error_summary = ?, completed_at = ? WHERE id = ? AND status = 'running'`, status, nullableJSON(result), nullableString(errorSummary), event.CreatedAt, id)
	if err != nil {
		return err
	}
	affected, err := updated.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrConflict
	}
	if err := insertAudit(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *MySQLActionPlanStore) AppendAudit(ctx context.Context, event AuditEvent) error {
	return insertAudit(ctx, s.db, event)
}

// SetExecutionVerification 在执行完成后持久化验证结果（结果准 #5）。
func (s *MySQLActionPlanStore) SetExecutionVerification(ctx context.Context, id string, verification []byte) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tool_executions SET verification = ? WHERE id = ?`, nullableJSON(verification), id)
	if err != nil {
		return fmt.Errorf("set execution verification: %w", err)
	}
	return nil
}

// SetPlanDryRun 在 plan 创建后持久化 dry-run 预览结果（结果准 #5）。
func (s *MySQLActionPlanStore) SetPlanDryRun(ctx context.Context, id string, dryRun []byte) error {
	_, err := s.db.ExecContext(ctx, `UPDATE action_plans SET dry_run = ?, updated_at = ? WHERE id = ?`, nullableJSON(dryRun), time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("set plan dry run: %w", err)
	}
	return nil
}

// ListExecutions 按过滤条件查询执行记录，JOIN action_plans 取 tool_name，
// 按 created_at DESC 排序，支持 keyset 分页（R5 结果准 - execution 查询 API）。
func (s *MySQLActionPlanStore) ListExecutions(ctx context.Context, filter ExecutionFilter) (ExecutionPage, error) {
	// JOIN action_plans 以支持 tool_name 过滤和返回。
	query := `SELECT e.id, e.action_plan_id, e.idempotency_key, e.status, e.result_summary, e.error_summary, e.verification, e.started_at, e.completed_at, e.created_at, p.tool_name
		FROM tool_executions e
		INNER JOIN action_plans p ON e.action_plan_id = p.id`
	conditions := []string{}
	args := []any{}
	if filter.Status != "" {
		conditions = append(conditions, "e.status = ?")
		args = append(args, filter.Status)
	}
	if filter.ActionPlanID != "" {
		conditions = append(conditions, "e.action_plan_id = ?")
		args = append(args, filter.ActionPlanID)
	}
	if filter.ToolName != "" {
		conditions = append(conditions, "p.tool_name = ?")
		args = append(args, filter.ToolName)
	}
	if !filter.StartedAfter.IsZero() {
		conditions = append(conditions, "e.started_at >= ?")
		args = append(args, filter.StartedAfter)
	}
	if !filter.StartedBefore.IsZero() {
		conditions = append(conditions, "e.started_at <= ?")
		args = append(args, filter.StartedBefore)
	}

	paginated := filter.Limit > 0
	if paginated && !filter.CursorCreatedAt.IsZero() && filter.CursorID != "" {
		conditions = append(conditions, "(e.created_at < ? OR (e.created_at = ? AND e.id < ?))")
		args = append(args, filter.CursorCreatedAt, filter.CursorCreatedAt, filter.CursorID)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY e.created_at DESC, e.id DESC"

	fetchLimit := filter.Limit
	if paginated {
		fetchLimit++
	}
	if fetchLimit > 0 {
		query += " LIMIT ?"
		args = append(args, fetchLimit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return ExecutionPage{}, err
	}
	defer func() { _ = rows.Close() }()
	var records []ExecutionRecord
	for rows.Next() {
		record, err := scanExecutionRows(rows)
		if err != nil {
			return ExecutionPage{}, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return ExecutionPage{}, err
	}

	page := ExecutionPage{Executions: records}
	if paginated && len(records) > filter.Limit {
		page.Executions = records[:filter.Limit]
		last := page.Executions[len(page.Executions)-1]
		page.NextCursor = ExecutionCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

// scanExecutionRows 扫描 ListExecutions 的多行结果（含 JOIN 的 tool_name）。
func scanExecutionRows(rows *sql.Rows) (ExecutionRecord, error) {
	var execution ExecutionRecord
	var result, errorSummary, verification sql.NullString
	var toolName sql.NullString
	var startedAt, completedAt sql.NullTime
	if err := rows.Scan(&execution.ID, &execution.ActionPlanID, &execution.IdempotencyKey, &execution.Status, &result, &errorSummary, &verification, &startedAt, &completedAt, &execution.CreatedAt, &toolName); err != nil {
		return ExecutionRecord{}, err
	}
	execution.ResultSummary = []byte(result.String)
	execution.ErrorSummary = errorSummary.String
	execution.Verification = []byte(verification.String)
	execution.ToolName = toolName.String
	if startedAt.Valid {
		execution.StartedAt = pointerTo(startedAt.Time)
	}
	if completedAt.Valid {
		execution.CompletedAt = pointerTo(completedAt.Time)
	}
	return execution, nil
}

func (s *MySQLActionPlanStore) ListAudit(ctx context.Context, filter AuditFilter) (AuditPage, error) {
	query := `SELECT id, action_plan_id, tool_execution_id, request_id, actor_subject, tool_name, action, decision, metadata, trace_id, created_at FROM copilot_audit_events`
	conditions := []string{}
	args := []any{}
	if filter.ToolName != "" {
		conditions = append(conditions, "tool_name = ?")
		args = append(args, filter.ToolName)
	}
	if filter.Action != "" {
		conditions = append(conditions, "action = ?")
		args = append(args, filter.Action)
	}
	if filter.Decision != "" {
		conditions = append(conditions, "decision = ?")
		args = append(args, filter.Decision)
	}
	if filter.Subject != "" {
		conditions = append(conditions, "actor_subject = ?")
		args = append(args, filter.Subject)
	}
	if !filter.CreatedAfter.IsZero() {
		conditions = append(conditions, "created_at >= ?")
		args = append(args, filter.CreatedAfter)
	}
	if !filter.CreatedBefore.IsZero() {
		conditions = append(conditions, "created_at <= ?")
		args = append(args, filter.CreatedBefore)
	}
	// 借鉴-4: 最终结果过滤——隐藏驳回/未执行事件。与内存版共用
	// RejectedAuditActionValues() 保持两端"rejected"定义一致。
	if filter.FinalResultOnly {
		rejected := RejectedAuditActionValues()
		placeholders := make([]string, len(rejected))
		for i, a := range rejected {
			placeholders[i] = "?"
			args = append(args, a)
		}
		conditions = append(conditions, "action NOT IN ("+strings.Join(placeholders, ", ")+")")
	}

	// Cursor pagination: when Limit > 0 we treat it as page size and return
	// events in descending created_at order so callers can page back in time
	// (newest page first). CursorCreatedAt+CursorID, when set, mark the
	// exclusive start of the next page. When Limit == 0 we preserve the legacy
	// chronological-ascending behavior for callers that don't paginate.
	paginated := filter.Limit > 0
	if paginated && !filter.CursorCreatedAt.IsZero() && filter.CursorID != "" {
		conditions = append(conditions, "(created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, filter.CursorCreatedAt, filter.CursorCreatedAt, filter.CursorID)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	if paginated {
		query += " ORDER BY created_at DESC, id DESC"
	} else {
		query += " ORDER BY created_at ASC"
	}

	// Fetch one extra row to detect a next page without trimming the page.
	fetchLimit := filter.Limit
	if paginated {
		fetchLimit++
	}
	if fetchLimit > 0 {
		query += " LIMIT ?"
		args = append(args, fetchLimit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return AuditPage{}, fmt.Errorf("query audit events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	events := []AuditEvent{}
	for rows.Next() {
		event, err := scanAudit(rows)
		if err != nil {
			return AuditPage{}, fmt.Errorf("scan audit event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return AuditPage{}, fmt.Errorf("iterate audit events: %w", err)
	}

	page := AuditPage{Events: events}
	if paginated && len(events) > filter.Limit {
		page.Events = events[:filter.Limit]
		last := page.Events[len(page.Events)-1]
		page.NextCursor = AuditCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func scanPlan(row *sql.Row) (PlanRecord, error) {
	var plan PlanRecord
	var token, confirmedBy, dryRun sql.NullString
	var confirmedAt sql.NullTime
	err := row.Scan(&plan.ID, &plan.RequestID, &plan.CreatedBy, &plan.ToolName, &plan.InputJSON, &plan.InputHash, &plan.RiskLevel, &plan.Status, &plan.Version, &token, &confirmedBy, &confirmedAt, &plan.ExpiresAt, &plan.CreatedAt, &plan.UpdatedAt, &dryRun)
	if err != nil {
		return PlanRecord{}, err
	}
	plan.ConfirmationTokenHash = token.String
	plan.ConfirmedBy = confirmedBy.String
	plan.DryRun = []byte(dryRun.String)
	if confirmedAt.Valid {
		plan.ConfirmedAt = pointerTo(confirmedAt.Time)
	}
	return plan, nil
}

func scanPlanRows(rows *sql.Rows) (PlanRecord, error) {
	var plan PlanRecord
	var token, confirmedBy, dryRun sql.NullString
	var confirmedAt sql.NullTime
	err := rows.Scan(&plan.ID, &plan.RequestID, &plan.CreatedBy, &plan.ToolName, &plan.InputJSON, &plan.InputHash, &plan.RiskLevel, &plan.Status, &plan.Version, &token, &confirmedBy, &confirmedAt, &plan.ExpiresAt, &plan.CreatedAt, &plan.UpdatedAt, &dryRun)
	if err != nil {
		return PlanRecord{}, err
	}
	plan.ConfirmationTokenHash = token.String
	plan.ConfirmedBy = confirmedBy.String
	plan.DryRun = []byte(dryRun.String)
	if confirmedAt.Valid {
		plan.ConfirmedAt = pointerTo(confirmedAt.Time)
	}
	return plan, nil
}

func scanExecution(row *sql.Row) (ExecutionRecord, error) {
	var execution ExecutionRecord
	var result sql.NullString
	var errorSummary sql.NullString
	var startedAt, completedAt sql.NullTime
	err := row.Scan(&execution.ID, &execution.ActionPlanID, &execution.IdempotencyKey, &execution.Status, &result, &errorSummary, &startedAt, &completedAt, &execution.CreatedAt)
	if err != nil {
		return ExecutionRecord{}, err
	}
	execution.ResultSummary = []byte(result.String)
	execution.ErrorSummary = errorSummary.String
	if startedAt.Valid {
		execution.StartedAt = pointerTo(startedAt.Time)
	}
	if completedAt.Valid {
		execution.CompletedAt = pointerTo(completedAt.Time)
	}
	return execution, nil
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertAudit(ctx context.Context, executor execer, event AuditEvent) error {
	metadata, err := json.Marshal(event.Metadata)
	if err != nil {
		return fmt.Errorf("marshal audit metadata: %w", err)
	}
	_, err = executor.ExecContext(ctx, `INSERT INTO copilot_audit_events (id, action_plan_id, tool_execution_id, request_id, actor_subject, tool_name, action, decision, metadata, trace_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.ID, nullableString(event.PlanID), nullableString(event.ExecutionID), event.RequestID, event.Subject, nullableString(event.ToolName), event.Action, event.Decision, metadata, nullableString(event.TraceID), event.CreatedAt)
	return err
}

func scanAudit(rows *sql.Rows) (AuditEvent, error) {
	var event AuditEvent
	var planID, executionID, subject, toolName, action, decision, traceID sql.NullString
	var metadata []byte
	if err := rows.Scan(&event.ID, &planID, &executionID, &event.RequestID, &subject, &toolName, &action, &decision, &metadata, &traceID, &event.CreatedAt); err != nil {
		return AuditEvent{}, err
	}
	event.PlanID = planID.String
	event.ExecutionID = executionID.String
	event.Subject = subject.String
	event.ToolName = toolName.String
	event.Action = action.String
	event.Decision = decision.String
	event.TraceID = traceID.String
	if len(metadata) > 0 {
		event.Metadata = map[string]any{}
		if err := json.Unmarshal(metadata, &event.Metadata); err != nil {
			return AuditEvent{}, fmt.Errorf("unmarshal audit metadata: %w", err)
		}
	}
	return event, nil
}

func isDuplicateKey(err error) bool {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return true
	}
	message := err.Error()
	return strings.Contains(message, "UNIQUE constraint failed") || strings.Contains(message, "constraint failed: UNIQUE")
}

func clonePlan(plan PlanRecord) PlanRecord {
	plan.InputJSON = append([]byte(nil), plan.InputJSON...)
	return plan
}
func cloneExecution(execution ExecutionRecord) ExecutionRecord {
	execution.ResultSummary = append([]byte(nil), execution.ResultSummary...)
	return execution
}
func cloneAudit(event AuditEvent) AuditEvent {
	copy := make(map[string]any, len(event.Metadata))
	for key, value := range event.Metadata {
		copy[key] = value
	}
	event.Metadata = copy
	return event
}
func pointerTo(value time.Time) *time.Time { return &value }
func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func nullableJSON(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
