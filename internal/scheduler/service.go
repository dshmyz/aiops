// Package scheduler 周期性调度管理员配置的定时巡检任务，到期后调用只读 capability。
package scheduler

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// ErrForbidden 表示非 admin 用户尝试执行需要 admin 权限的操作。
var ErrForbidden = errors.New("scheduled task operation requires admin role")

// CreateRequest 是创建定时任务的请求体。
type CreateRequest struct {
	Name           string
	CapabilityName string
	Input          map[string]any
	ScheduleKind   string
	Preset         string
	CronExpr       string
	Timezone       string
	Enabled        bool
}

// UpdateRequest 是更新定时任务的请求体。所有字段都会被写入（全量更新）。
type UpdateRequest struct {
	Name           string
	CapabilityName string
	Input          map[string]any
	ScheduleKind   string
	Preset         string
	CronExpr       string
	Timezone       string
	Enabled        bool
}

// Service 是定时巡检任务的 CRUD + 手动触发 API 层。admin 可创建/更新/删除/触发；
// 任意登录用户可查看任务列表和历史记录。
type Service struct {
	store store.ScheduledTaskStore
	reads *execution.ReadOnlyService
	audit *audit.Service
	now   func() time.Time
}

// NewService 创建 Service。now 注入时钟便于测试；为 nil 时用 time.Now。
func NewService(taskStore store.ScheduledTaskStore, reads *execution.ReadOnlyService, auditService *audit.Service, now func() time.Time) *Service {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{
		store: taskStore,
		reads: reads,
		audit: auditService,
		now:   now,
	}
}

// Create 创建定时任务。仅 admin 可调用；next_run_at 由 schedule 配置即时计算。
func (s *Service) Create(ctx context.Context, user identity.CurrentUser, req CreateRequest) (store.ScheduledTask, error) {
	if !requireAdmin(user) {
		return store.ScheduledTask{}, ErrForbidden
	}
	now := s.now()
	nextRun, err := computeNextRun(req.ScheduleKind, req.Preset, req.CronExpr, req.Timezone, now)
	if err != nil {
		return store.ScheduledTask{}, err
	}
	task := store.ScheduledTask{
		Name:           req.Name,
		Subject:        user.Subject,
		CapabilityName: req.CapabilityName,
		Input:          req.Input,
		ScheduleKind:   req.ScheduleKind,
		Preset:         req.Preset,
		CronExpr:       req.CronExpr,
		Timezone:       req.Timezone,
		Enabled:        req.Enabled,
		NextRunAt:      nextRun,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	return s.store.CreateTask(ctx, task)
}

// Update 更新定时任务。仅 admin 可调用；schedule 字段变化时重算 next_run_at。
func (s *Service) Update(ctx context.Context, user identity.CurrentUser, id string, req UpdateRequest) (store.ScheduledTask, error) {
	if !requireAdmin(user) {
		return store.ScheduledTask{}, ErrForbidden
	}
	existing, err := s.store.GetTask(ctx, id, user.Subject)
	if err != nil {
		return store.ScheduledTask{}, err
	}
	now := s.now()
	// schedule 配置变化时重算 next_run_at
	scheduleChanged := existing.ScheduleKind != req.ScheduleKind ||
		existing.Preset != req.Preset ||
		existing.CronExpr != req.CronExpr ||
		existing.Timezone != req.Timezone

	existing.Name = req.Name
	existing.CapabilityName = req.CapabilityName
	existing.Input = req.Input
	existing.ScheduleKind = req.ScheduleKind
	existing.Preset = req.Preset
	existing.CronExpr = req.CronExpr
	existing.Timezone = req.Timezone
	existing.Enabled = req.Enabled
	existing.UpdatedAt = now

	if scheduleChanged {
		nextRun, err := computeNextRun(req.ScheduleKind, req.Preset, req.CronExpr, req.Timezone, now)
		if err != nil {
			return store.ScheduledTask{}, err
		}
		existing.NextRunAt = nextRun
	}

	return s.store.UpdateTask(ctx, existing)
}

// Delete 删除定时任务。仅 admin 可调用。
func (s *Service) Delete(ctx context.Context, user identity.CurrentUser, id string) error {
	if !requireAdmin(user) {
		return ErrForbidden
	}
	return s.store.DeleteTask(ctx, id, user.Subject)
}

// Get 返回定时任务详情。任意登录用户可查看任意任务（不限 owner）。
func (s *Service) Get(ctx context.Context, _ identity.CurrentUser, id string) (store.ScheduledTask, error) {
	return s.store.GetTaskByID(ctx, id)
}

// List 返回定时任务列表。任意登录用户可调用（运维场景允许跨用户只读查看）。
func (s *Service) List(ctx context.Context, user identity.CurrentUser, filter store.ScheduledTaskFilter) ([]store.ScheduledTask, error) {
	return s.store.ListTasks(ctx, filter)
}

// Trigger 立即执行一次定时任务。仅 admin 可调用；写 run + audit，但不更新 next_run_at
// （手动触发不影响定时调度节奏）。
func (s *Service) Trigger(ctx context.Context, user identity.CurrentUser, id string) (store.ScheduledTaskRun, error) {
	if !requireAdmin(user) {
		return store.ScheduledTaskRun{}, ErrForbidden
	}
	task, err := s.store.GetTask(ctx, id, user.Subject)
	if err != nil {
		return store.ScheduledTaskRun{}, err
	}
	return executeAndRecord(ctx, s.store, s.reads, s.audit, task, s.now, false)
}

// ListRuns 返回指定任务的执行历史。任意登录用户可调用。
func (s *Service) ListRuns(ctx context.Context, _ identity.CurrentUser, taskID string, limit int) ([]store.ScheduledTaskRun, error) {
	return s.store.ListRuns(ctx, taskID, limit)
}

// CountRecentFailures 返回 since 之后的失败执行数。用于侧边栏 badge。
func (s *Service) CountRecentFailures(ctx context.Context, since time.Time) (int, error) {
	return s.store.CountRecentFailures(ctx, since)
}

// requireAdmin 检查用户是否拥有 admin 角色。
func requireAdmin(user identity.CurrentUser) bool {
	return slices.Contains(user.Roles, "admin")
}
