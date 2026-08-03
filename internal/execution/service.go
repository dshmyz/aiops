// Package execution creates idempotent records and invokes a supplied executor
// only for a confirmed immutable plan snapshot.
package execution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

var (
	ErrPlanNotConfirmed = errors.New("plan is not confirmed")
	ErrImmutableInput   = errors.New("plan input is immutable")
	ErrPlanExpired      = errors.New("plan has expired")
)

// Executor is deliberately narrow. Production tool API integration belongs to
// a later task; this interface keeps the plan and idempotency enforcement here.
type Executor interface {
	Execute(context.Context, string, map[string]any) (map[string]any, error)
}

// ExecutionEvent describes a completed execution. Observers receive it after
// the execution reaches a terminal state (succeeded or failed).
type ExecutionEvent struct {
	PlanID        string
	RequestID     string
	ToolName      string
	Input         map[string]any
	Status        string // "succeeded" or "failed"
	Subject       string
	Timestamp     time.Time
	ResultSummary string              // 脱敏的结果摘要（sanitizedResultSummary 产出）
	Verification  *VerificationResult // 成功时的验证结果，失败时为 nil
}

// ExecutionObserver is notified after each fresh (non-reused) execution
// completes. Implementations must be non-blocking; the execution service
// calls OnComplete in-line but observers should offload heavy work (e.g.
// embedding + store write) to a goroutine if needed.
type ExecutionObserver interface {
	OnExecutionComplete(ctx context.Context, event ExecutionEvent)
}

type Service struct {
	store     store.ActionPlanStore
	executor  Executor
	verifier  Verifier
	observers []ExecutionObserver
	now       func() time.Time
}

func NewService(repository store.ActionPlanStore, executor Executor) *Service {
	return &Service{store: repository, executor: executor, now: func() time.Time { return time.Now().UTC() }}
}

// WithObservers appends execution observers that are notified after each
// fresh execution completes. Returns the service for chaining.
func (s *Service) WithObservers(observers ...ExecutionObserver) *Service {
	s.observers = append(s.observers, observers...)
	return s
}

func NewServiceWithClock(repository store.ActionPlanStore, executor Executor, now func() time.Time) *Service {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{store: repository, executor: executor, now: now}
}

// NewServiceWithVerifier wires an optional post-execution Verifier that runs
// after a fresh (non-reused) write execution succeeds. The verifier does not
// affect the execution status; failures surface on Execution.Verification.
func NewServiceWithVerifier(repository store.ActionPlanStore, executor Executor, verifier Verifier) *Service {
	return &Service{store: repository, executor: executor, verifier: verifier, now: func() time.Time { return time.Now().UTC() }}
}

// NewServiceWithClockAndVerifier wires both an optional Verifier and a custom
// clock, for tests that need deterministic time alongside verification.
func NewServiceWithClockAndVerifier(repository store.ActionPlanStore, executor Executor, now func() time.Time, verifier Verifier) *Service {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{store: repository, executor: executor, verifier: verifier, now: now}
}

type Execution struct {
	ID             string
	PlanID         string
	IdempotencyKey string
	Status         string
	Reused         bool
	Verification   *VerificationResult
}

// ExecuteConfirmedPlan compares caller input only to detect tampering. It
// decodes and passes the database snapshot to the executor, never this map.
func (s *Service) ExecuteConfirmedPlan(ctx context.Context, planID string, suppliedInput map[string]any) (Execution, error) {
	plan, err := s.store.GetPlan(ctx, planID)
	if err != nil {
		return Execution{}, err
	}
	_, suppliedHash, err := plans.CanonicalInput(suppliedInput)
	if err != nil {
		return Execution{}, fmt.Errorf("canonicalize supplied input: %w", err)
	}
	if suppliedHash != plan.InputHash {
		if err := s.store.AppendAudit(ctx, auditEvent(newID(), plan, "execution_rejected", "immutable_input", s.now(), "")); err != nil {
			return Execution{}, err
		}
		return Execution{}, ErrImmutableInput
	}
	if plan.Status != store.PlanConfirmed {
		if err := s.store.AppendAudit(ctx, auditEvent(newID(), plan, "execution_rejected", "confirmation_required", s.now(), "")); err != nil {
			return Execution{}, err
		}
		return Execution{}, ErrPlanNotConfirmed
	}
	if !plan.ExpiresAt.After(s.now()) {
		if err := s.store.AppendAudit(ctx, auditEvent(newID(), plan, "execution_rejected", "plan_expired", s.now(), "")); err != nil {
			return Execution{}, err
		}
		return Execution{}, ErrPlanExpired
	}

	now := s.now()
	key := "plan:" + plan.ID + ":" + plan.InputHash
	record := store.ExecutionRecord{ID: newID(), ActionPlanID: plan.ID, IdempotencyKey: key, Status: "running", StartedAt: &now, CreatedAt: now}
	created, reused, err := s.store.CreateExecutionIfAbsent(ctx, record, auditEvent(newID(), plan, "execution_started", "permitted", now, record.ID))
	if err != nil {
		return Execution{}, err
	}
	execution := toExecution(created, reused)
	if reused {
		return execution, nil
	}

	snapshot, err := plans.DecodeInput(plan.InputJSON)
	if err != nil {
		return execution, s.complete(ctx, plan, created, nil, fmt.Errorf("decode immutable plan snapshot: %w", err))
	}
	if s.executor == nil {
		return execution, s.complete(ctx, plan, created, nil, errors.New("executor is required"))
	}
	result, executeErr := s.executor.Execute(ctx, plan.ToolName, snapshot)
	if executeErr != nil {
		return execution, s.complete(ctx, plan, created, nil, executeErr)
	}
	summary := sanitizedResultSummary(result)
	if err := s.complete(ctx, plan, created, summary, nil); err != nil {
		return execution, err
	}
	execution.Status = "succeeded"
	execution.Verification = s.verifyIfConfigured(ctx, plan, snapshot)
	// 结果准 #5: 验证结果持久化到 tool_executions.verification，供事后复盘。
	if execution.Verification != nil {
		if encoded, err := json.Marshal(execution.Verification); err == nil {
			if err := s.store.SetExecutionVerification(ctx, execution.ID, encoded); err != nil {
				// 验证结果落库失败不改变执行结果（best-effort）。
				return execution, nil
			}
		}
	}
	event := s.buildEvent(plan, snapshot, "succeeded")
	event.ResultSummary = string(summary)
	event.Verification = execution.Verification
	s.notifyObservers(ctx, event)
	return execution, nil
}

// verifyIfConfigured runs the post-execution verifier when one is wired. The
// verifier is best-effort: failures surface on the returned VerificationResult
// and never propagate to the caller, so a successful write stays successful.
func (s *Service) verifyIfConfigured(ctx context.Context, plan store.PlanRecord, input map[string]any) *VerificationResult {
	if s.verifier == nil {
		return nil
	}
	return runVerifier(ctx, s.verifier, plan, input)
}

// ExecuteConfirmedStoredPlan executes the immutable input snapshot already
// persisted with a confirmed plan. It is the safest HTTP confirmation path:
// clients do not resubmit operational input during execution.
func (s *Service) ExecuteConfirmedStoredPlan(ctx context.Context, planID string) (Execution, error) {
	plan, err := s.store.GetPlan(ctx, planID)
	if err != nil {
		return Execution{}, err
	}
	snapshot, err := plans.DecodeInput(plan.InputJSON)
	if err != nil {
		return Execution{}, fmt.Errorf("decode immutable plan snapshot: %w", err)
	}
	return s.ExecuteConfirmedPlan(ctx, planID, snapshot)
}

func (s *Service) complete(ctx context.Context, plan store.PlanRecord, execution store.ExecutionRecord, result []byte, executionErr error) error {
	now := s.now()
	status, action, decision, errorSummary := "succeeded", "execution_succeeded", "permitted", ""
	if executionErr != nil {
		// 收口1: 持久化真实 executor 错误，而非硬编码 "execution failed"。
		// 截断到 errorSummaryMaxLen 以适配 DB 列 VARCHAR(1024)，避免写入失败。
		errorSummary = TruncateError(executionErr.Error(), errorSummaryMaxLen)
		status, action, decision = "failed", "execution_failed", "execution_error"
	}
	if err := s.store.CompleteExecution(ctx, execution.ID, status, result, errorSummary, auditEvent(newID(), plan, action, decision, now, execution.ID)); err != nil {
		return err
	}
	if executionErr != nil {
		s.notifyObservers(ctx, s.buildEvent(plan, nil, "failed"))
	}
	return executionErr
}

// errorSummaryMaxLen 对齐 DB 列 error_summary VARCHAR(1024)（migrations/001_copilot.sql:27）。
// 预留 8 字节余量给 UTF-8 多字节字符和省略号后缀，避免边界处字符被截断成乱码。
const errorSummaryMaxLen = 1000

// TruncateError 截断 error message 到 maxLen，超长时追加 "..." 后缀。
// 用于持久化 ErrorSummary，防止超长 error 导致 DB 写入失败。
func TruncateError(msg string, maxLen int) string {
	if len(msg) <= maxLen {
		return msg
	}
	// 按 rune 截断，避免在多字节字符中间切开产生乱码
	runes := []rune(msg)
	if len(runes) <= maxLen {
		// rune 数 <= maxLen 但 byte 数 > maxLen（多字节字符），按 rune 截到 maxLen
		return string(runes[:maxLen]) + "..."
	}
	return string(runes[:maxLen]) + "..."
}

// buildEvent constructs an ExecutionEvent from the plan record with common
// fields filled. Callers may set ResultSummary and Verification on the
// returned event for success paths.
func (s *Service) buildEvent(plan store.PlanRecord, input map[string]any, status string) ExecutionEvent {
	subject := plan.ConfirmedBy
	if subject == "" {
		subject = plan.CreatedBy
	}
	return ExecutionEvent{
		PlanID:    plan.ID,
		RequestID: plan.RequestID,
		ToolName:  plan.ToolName,
		Input:     input,
		Status:    status,
		Subject:   subject,
		Timestamp: s.now(),
	}
}

// notifyObservers calls all registered observers with the execution event.
// Panics in observers are recovered so a misbehaving observer cannot crash
// the execution pipeline.
func (s *Service) notifyObservers(ctx context.Context, event ExecutionEvent) {
	if len(s.observers) == 0 {
		return
	}
	for _, obs := range s.observers {
		func() {
			defer func() { _ = recover() }()
			obs.OnExecutionComplete(ctx, event)
		}()
	}
}

// resultSummaryMaxBytes 是持久化结果摘要的大小上限。真实 executor 输出经
// 脱敏后可能仍较大，限制大小避免超长 JSON 写入 DB（tool_executions.result_summary
// 是 JSON 列，但过大载荷无意义且拖慢查询）。
const resultSummaryMaxBytes = 10 * 1024

// sanitizedResultSummary 保留 executor 真实输出（结果准 #4: output 脱敏重设计），
// 递归剥离敏感键（含 token/password/secret/key/credential/authorization 的字段名），
// 追加 outcome 字段，并限制序列化大小。脱敏失败或超限时降级为固定摘要。
func sanitizedResultSummary(result map[string]any) []byte {
	scrubbed := scrubSensitiveKeys(result)
	scrubbed["outcome"] = "succeeded"
	body, err := json.Marshal(scrubbed)
	if err != nil || len(body) > resultSummaryMaxBytes {
		return []byte(`{"outcome":"succeeded"}`)
	}
	return body
}

// sensitiveMarkers 是与 capabilities.isSensitive 一致的敏感字段名标记集。
// 字段名（递归，含 map key 和嵌套 map/slice）命中任一标记即被剥离。
var sensitiveMarkers = []string{"password", "secret", "token", "key", "credential", "authorization"}

// scrubSensitiveKeys 递归删除键名含敏感标记的字段。返回新 map，不修改入参。
func scrubSensitiveKeys(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for k, v := range input {
		if isSensitiveName(k) {
			continue
		}
		out[k] = scrubValue(v)
	}
	return out
}

func scrubValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return scrubSensitiveKeys(t)
	case []any:
		items := make([]any, 0, len(t))
		for _, item := range t {
			items = append(items, scrubValue(item))
		}
		return items
	default:
		return v
	}
}

func isSensitiveName(name string) bool {
	lower := strings.ToLower(name)
	for _, marker := range sensitiveMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func toExecution(record store.ExecutionRecord, reused bool) Execution {
	return Execution{ID: record.ID, PlanID: record.ActionPlanID, IdempotencyKey: record.IdempotencyKey, Status: record.Status, Reused: reused}
}

func auditEvent(id string, plan store.PlanRecord, action, decision string, now time.Time, executionID string) store.AuditEvent {
	subject := plan.ConfirmedBy
	if subject == "" {
		subject = plan.CreatedBy
	}
	return store.AuditEvent{ID: id, PlanID: plan.ID, ExecutionID: executionID, RequestID: plan.RequestID, Subject: subject, ToolName: plan.ToolName, Action: action, Decision: decision, Metadata: map[string]any{"input_hash": plan.InputHash}, CreatedAt: now}
}

func newID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return hex.EncodeToString(value)
}
