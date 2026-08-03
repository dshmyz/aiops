// Package plans creates immutable, policy-approved operation plans and owns
// their human-confirmation state transition.
package plans

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

type Status = store.PlanStatus

const (
	PendingConfirmation = store.PlanPendingConfirmation
	Confirmed           = store.PlanConfirmed
)

var (
	ErrConfirmationDenied = errors.New("plan confirmation was rejected")
	ErrPlanNotPermitted   = errors.New("policy decision does not permit a plan")
)

type Clock interface{ Now() time.Time }
type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// Plan exposes immutable snapshot metadata. ConfirmationToken exists only in
// the create response; the database retains its SHA-256 hash, never the token.
type Plan struct {
	ID                string
	RequestID         string
	Subject           string
	ToolName          string
	InputHash         string
	RiskLevel         string
	Status            Status
	Version           uint
	ExpiresAt         time.Time
	ConfirmedBy       string
	ConfirmedAt       *time.Time
	ConfirmationToken string
}

type Service struct {
	store store.ActionPlanStore
	clock Clock
}

func NewService(repository store.ActionPlanStore, clock Clock) *Service {
	if clock == nil {
		clock = systemClock{}
	}
	return &Service{store: repository, clock: clock}
}

func (s *Service) CreatePlan(ctx context.Context, user identity.CurrentUser, decision policy.Decision, input map[string]any) (Plan, error) {
	// Decision may have been constructed from untrusted request or model data.
	// Only its tool name identifies the requested operation; the registry and
	// policy evaluate it again at this persistence boundary.
	tool, ok := tools.Lookup(decision.Tool.Name)
	if !ok {
		return Plan{}, ErrPlanNotPermitted
	}
	decision = policy.Evaluate(user, tool, input)
	if !decision.Allowed {
		return Plan{}, ErrPlanNotPermitted
	}
	inputJSON, inputHash, err := CanonicalInput(input)
	if err != nil {
		return Plan{}, fmt.Errorf("canonicalize plan input: %w", err)
	}
	now := s.clock.Now().UTC()
	plan := store.PlanRecord{
		ID:        newID(),
		RequestID: user.RequestID,
		CreatedBy: user.Subject,
		ToolName:  decision.Tool.Name,
		InputJSON: inputJSON,
		InputHash: inputHash,
		RiskLevel: string(decision.Tool.Risk),
		Status:    Confirmed,
		Version:   1,
		ExpiresAt: now.Add(10 * time.Minute),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if decision.RequiresConfirmation {
		token, err := confirmationToken()
		if err != nil {
			return Plan{}, err
		}
		plan.Status = PendingConfirmation
		plan.ConfirmationTokenHash = hash(token)
		if err := s.store.CreatePlan(ctx, plan, auditEvent(newID(), plan, "plan_created", string(policy.Permitted), user.Subject, user.RequestID, now, nil)); err != nil {
			return Plan{}, err
		}
		response := toPlan(plan)
		response.ConfirmationToken = token
		return response, nil
	}
	if err := s.store.CreatePlan(ctx, plan, auditEvent(newID(), plan, "plan_created", string(policy.Permitted), user.Subject, user.RequestID, now, map[string]any{"confirmation_required": false})); err != nil {
		return Plan{}, err
	}
	return toPlan(plan), nil
}

// CreateRunbookPlan 为 Runbook 驱动的写操作创建 plan（借鉴-5）。
// runbookRiskLevel 为 "low" 时跳过人工确认（创建即 Confirmed），镜像
// CreatePlan 的 RequiresConfirmation==false 分支。policy 在此持久化边界
// 重新评估，与 CreatePlan 一致。
func (s *Service) CreateRunbookPlan(ctx context.Context, user identity.CurrentUser, decision policy.Decision, input map[string]any, runbookSlug, runbookRiskLevel string) (Plan, error) {
	tool, ok := tools.Lookup(decision.Tool.Name)
	if !ok {
		return Plan{}, ErrPlanNotPermitted
	}
	decision = policy.Evaluate(user, tool, input)
	if !decision.Allowed {
		return Plan{}, ErrPlanNotPermitted
	}
	inputJSON, inputHash, err := CanonicalInput(input)
	if err != nil {
		return Plan{}, fmt.Errorf("canonicalize plan input: %w", err)
	}
	now := s.clock.Now().UTC()
	plan := store.PlanRecord{
		ID:        newID(),
		RequestID: user.RequestID,
		CreatedBy: user.Subject,
		ToolName:  decision.Tool.Name,
		InputJSON: inputJSON,
		InputHash: inputHash,
		RiskLevel: string(decision.Tool.Risk),
		Status:    Confirmed,
		Version:   1,
		ExpiresAt: now.Add(10 * time.Minute),
		CreatedAt: now,
		UpdatedAt: now,
	}
	requiresConfirmation := decision.RequiresConfirmation && runbookRiskLevel != "low"
	metadata := map[string]any{"runbook": runbookSlug}
	if requiresConfirmation {
		token, err := confirmationToken()
		if err != nil {
			return Plan{}, err
		}
		plan.Status = PendingConfirmation
		plan.ConfirmationTokenHash = hash(token)
		if err := s.store.CreatePlan(ctx, plan, auditEvent(newID(), plan, "plan_created", string(policy.Permitted), user.Subject, user.RequestID, now, metadata)); err != nil {
			return Plan{}, err
		}
		response := toPlan(plan)
		response.ConfirmationToken = token
		return response, nil
	}
	metadata["confirmation_required"] = false
	if err := s.store.CreatePlan(ctx, plan, auditEvent(newID(), plan, "plan_created", string(policy.Permitted), user.Subject, user.RequestID, now, metadata)); err != nil {
		return Plan{}, err
	}
	return toPlan(plan), nil
}

// AttachDryRun 在 plan 创建后持久化 dry-run 预览结果（结果准 #5）。dryRun 为
// JSON 序列化的 DryRunResult。失败不阻塞调用（best-effort），返回更新后的 plan。
func (s *Service) AttachDryRun(ctx context.Context, planID string, dryRun []byte) error {
	if err := s.store.SetPlanDryRun(ctx, planID, dryRun); err != nil {
		return err
	}
	return nil
}

// ConfirmPlan makes exactly one optimistic transition from pending_confirmation
// to confirmed. The store's conditional update binds status, version, token,
// and expiry in a single operation.
func (s *Service) ConfirmPlan(ctx context.Context, id string, expectedVersion uint, token string, user identity.CurrentUser) (Plan, error) {
	now := s.clock.Now().UTC()
	existing, err := s.store.GetPlan(ctx, id)
	if err != nil {
		return Plan{}, err
	}
	if token == "" {
		if err := s.store.AppendAudit(ctx, auditEvent(newID(), existing, "plan_confirmation_rejected", "confirmation_denied", user.Subject, user.RequestID, now, nil)); err != nil {
			return Plan{}, errors.Join(ErrConfirmationDenied, fmt.Errorf("record confirmation rejection audit: %w", err))
		}
		return Plan{}, ErrConfirmationDenied
	}
	plan, err := s.store.ConfirmPlan(ctx, id, expectedVersion, hash(token), user.Subject, now, auditEvent(newID(), existing, "plan_confirmed", string(policy.Permitted), user.Subject, user.RequestID, now, nil))
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			if auditErr := s.store.AppendAudit(ctx, auditEvent(newID(), existing, "plan_confirmation_rejected", "confirmation_denied", user.Subject, user.RequestID, now, nil)); auditErr != nil {
				return Plan{}, errors.Join(ErrConfirmationDenied, fmt.Errorf("record confirmation rejection audit: %w", auditErr))
			}
			return Plan{}, ErrConfirmationDenied
		}
		return Plan{}, err
	}
	return toPlan(plan), nil
}

// CanonicalInput serializes an input map with encoding/json's stable map-key
// order and returns its SHA-256 hash. The same bytes become the stored snapshot.
func CanonicalInput(input map[string]any) ([]byte, string, error) {
	if input == nil {
		return nil, "", errors.New("input is required")
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(encoded)
	return encoded, hex.EncodeToString(sum[:]), nil
}

func DecodeInput(snapshot []byte) (map[string]any, error) {
	var input map[string]any
	if err := json.Unmarshal(snapshot, &input); err != nil {
		return nil, err
	}
	return input, nil
}

func toPlan(record store.PlanRecord) Plan {
	return Plan{ID: record.ID, RequestID: record.RequestID, Subject: record.CreatedBy, ToolName: record.ToolName, InputHash: record.InputHash, RiskLevel: record.RiskLevel, Status: record.Status, Version: record.Version, ExpiresAt: record.ExpiresAt, ConfirmedBy: record.ConfirmedBy, ConfirmedAt: record.ConfirmedAt}
}

func auditEvent(id string, plan store.PlanRecord, action, decision, subject, requestID string, now time.Time, metadata map[string]any) store.AuditEvent {
	return store.AuditEvent{ID: id, PlanID: plan.ID, RequestID: requestID, Subject: subject, ToolName: plan.ToolName, Action: action, Decision: decision, Metadata: metadata, CreatedAt: now}
}

func confirmationToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate confirmation token: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func newID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
}
