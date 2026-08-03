// Package scheduler 周期性调度管理员配置的定时巡检任务，到期后调用只读 capability。
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// tracer returns the scheduler package's instrumentation scope.
func tracer() trace.Tracer {
	return otel.Tracer("github.com/gracegaoya/ai-operations-copilot/internal/scheduler")
}

// systemSchedulerSubject 是调度器内部身份标识，写入 audit event 的 subject 字段。
const systemSchedulerSubject = "system:scheduler"

// maxLag 是任务到期后允许的最大延迟执行时间。到期超过此阈值的任务被视为过期，
// 跳过执行只推进 next_run_at + 记 skipped audit，避免执行过时巡检（专项：任务准-并发安全）。
const maxLag = time.Hour

// dueTaskLimit 是单轮 tickOnce 最多处理的到期任务数，防止积压时补跑风暴（专项：任务准-并发安全）。
const dueTaskLimit = 100

// systemUser 返回调度器内部身份，复用 ReadOnlyService 的信任路径执行只读 capability。
// 定时任务由 admin 配置时已完成鉴权，调度器不再重复 policy 校验。
func systemUser() identity.CurrentUser {
	return identity.CurrentUser{
		Subject:             systemSchedulerSubject,
		Roles:               []string{"scheduler"},
		AllowedEnvironments: []string{},
		RequestID:           "scheduler",
	}
}

// Scheduler 周期性扫描到期任务并执行。进程重启后从 next_run_at 恢复调度断点。
// 配置 reportStore + reporter 后，还会每日自动生成巡检报告并持久化。
type Scheduler struct {
	store          store.ScheduledTaskStore
	reads          *execution.ReadOnlyService
	audit          *audit.Service
	tickInterval   time.Duration
	now            func() time.Time
	reportStore    store.InspectionReportStore
	reporter       *Reporter
	reportInterval time.Duration
}

// New 创建调度器。tickInterval 控制扫描周期；now 注入时钟便于测试。
func New(taskStore store.ScheduledTaskStore, reads *execution.ReadOnlyService, auditService *audit.Service, tick time.Duration, now func() time.Time) *Scheduler {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if tick <= 0 {
		tick = time.Minute
	}
	return &Scheduler{
		store:          taskStore,
		reads:          reads,
		audit:          auditService,
		tickInterval:   tick,
		now:            now,
		reportInterval: 24 * time.Hour,
	}
}

// WithReportGeneration 配置巡检报告生成。配置后 Start 会在独立 goroutine 中
// 按 reportInterval（默认 24h）周期性生成日报并持久化到 reportStore。
func (s *Scheduler) WithReportGeneration(reportStore store.InspectionReportStore, reporter *Reporter) *Scheduler {
	s.reportStore = reportStore
	s.reporter = reporter
	return s
}

// Start 进入调度循环，收到 ctx.Done 后退出。应在独立 goroutine 中运行。
// 如果配置了 reportStore + reporter，会额外启动一个日报生成 goroutine。
func (s *Scheduler) Start(ctx context.Context) {
	ticker := time.NewTicker(s.tickInterval)
	defer ticker.Stop()
	if s.reportStore != nil && s.reporter != nil {
		go s.dailyReportLoop(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.tickOnce(ctx)
		}
	}
}

// dailyReportLoop 按 reportInterval 周期性生成日报并持久化。
func (s *Scheduler) dailyReportLoop(ctx context.Context) {
	ticker := time.NewTicker(s.reportInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = s.GenerateDailyReport(ctx)
		}
	}
}

// GenerateDailyReport 生成前一天的巡检日报并持久化到 reportStore。
// 未配置 reportStore 或 reporter 时返回空报告且不报错（no-op）。
func (s *Scheduler) GenerateDailyReport(ctx context.Context) (store.InspectionReport, error) {
	if s.reportStore == nil || s.reporter == nil {
		return store.InspectionReport{}, nil
	}
	report, err := s.reporter.GenerateDaily(ctx)
	if err != nil {
		return store.InspectionReport{}, err
	}
	saved, err := s.reportStore.CreateReport(ctx, report)
	if err != nil {
		return store.InspectionReport{}, err
	}
	return saved, nil
}

// tickOnce 执行一次到期扫描。capability 执行失败不会返回 error（已记录为 failed run），
// 只有 store 层错误才向上传递。单轮最多处理 dueTaskLimit 个任务，防止补跑风暴（专项：任务准-并发安全）。
func (s *Scheduler) tickOnce(ctx context.Context) error {
	ctx, span := tracer().Start(ctx, "scheduler.tickOnce")
	defer span.End()
	now := s.now()
	dueTasks, err := s.store.ListDueTasks(ctx, now, dueTaskLimit)
	if err != nil {
		return fmt.Errorf("scheduler: list due tasks: %w", err)
	}
	for _, task := range dueTasks {
		if _, err := s.executeTask(ctx, task); err != nil {
			// store 层错误记录日志后继续处理下一个任务，避免一个任务卡死整个调度
			continue
		}
	}
	return nil
}

// executeTask 执行单个到期任务：调用 capability → 写 run → 写 audit → 更新 task。
// panic 会被 recover 并记为 failed，避免调度器崩溃。无论成功失败都更新 next_run_at，
// 避免任务卡死。返回 store 层错误（capability 错误已内部处理）。
func (s *Scheduler) executeTask(ctx context.Context, task store.ScheduledTask) (store.ScheduledTaskRun, error) {
	return executeAndRecord(ctx, s.store, s.reads, s.audit, task, s.now, true)
}

// executeAndRecord 执行一次任务并记录结果（run + audit + 更新 task）。
// updateNextRun=true 时重算 next_run_at（scheduler 定时触发）；
// updateNextRun=false 时保留原 next_run_at（手动 trigger）。
// 这是 Scheduler.executeTask 和 Service.Trigger 的共享实现。
//
// 专项：任务准-并发安全
//   - updateNextRun=true（scheduler 触发）时先做过期检查 + CAS 认领，防止多实例重复执行
//   - AppendRun + UpdateTask 合并为 AppendRunAndUpdateTask 原子事务，防止数据竞争
//   - CAS 冲突（ErrConcurrentModification）时记 warning 不重试，其他实例已介入
//
// 收口2: nowFn 是时钟函数（而非单个时间快照），startedAt 与 finishedAt
// 分别调用 nowFn()，使 duration 不再恒为 0。生产用 time.Now 自然推进，
// 测试用可推进时钟验证 duration > 0。
func executeAndRecord(ctx context.Context, taskStore store.ScheduledTaskStore, reads *execution.ReadOnlyService, auditService *audit.Service, task store.ScheduledTask, nowFn func() time.Time, updateNextRun bool) (store.ScheduledTaskRun, error) {
	ctx, span := tracer().Start(ctx, "scheduler.executeTask",
		trace.WithAttributes(
			attribute.String("task.id", task.ID),
			attribute.String("task.name", task.Name),
			attribute.String("task.capability", task.CapabilityName),
		))
	defer span.End()
	now := nowFn()

	// 专项：过期检查 — 到期超过 maxLag 的任务跳过执行，只推进 next_run_at + 记 skipped audit。
	// 仅 scheduler 触发时检查（updateNextRun=true），手动 Trigger 不检查（admin 显式触发）。
	if updateNextRun && task.NextRunAt.Before(now.Add(-maxLag)) {
		nextRun, err := computeNextRun(task.ScheduleKind, task.Preset, task.CronExpr, task.Timezone, now)
		if err != nil {
			nextRun = now.Add(5 * time.Minute)
		}
		recordAudit(ctx, auditService, uuid.NewString(), task, systemUser(), audit.ActionScheduledTaskSkipped, audit.DecisionPermitted, now, nil)
		_, _ = taskStore.ClaimTask(ctx, task.ID, task.NextRunAt, nextRun, now)
		return store.ScheduledTaskRun{}, nil
	}

	// 专项：CAS 认领 — scheduler 触发时先抢占任务，防止多实例重复执行。
	// ClaimTask 成功后 task 的 updated_at 被设为 now（ClaimTask SQL SET updated_at = now），
	// 用此值作为 AppendRunAndUpdateTask 的 expectedUpdatedAt（CAS 乐观锁）。
	expectedUpdatedAt := task.UpdatedAt
	if updateNextRun {
		nextRun, err := computeNextRun(task.ScheduleKind, task.Preset, task.CronExpr, task.Timezone, now)
		if err != nil {
			nextRun = now.Add(5 * time.Minute)
		}
		claimed, err := taskStore.ClaimTask(ctx, task.ID, task.NextRunAt, nextRun, now)
		if err != nil {
			return store.ScheduledTaskRun{}, fmt.Errorf("scheduler: claim task: %w", err)
		}
		if !claimed {
			// 已被其他实例认领，跳过
			return store.ScheduledTaskRun{}, nil
		}
		task.NextRunAt = nextRun
		expectedUpdatedAt = now // ClaimTask 设 updated_at = now
	}

	startedAt := nowFn()
	result, execErr := executeWithRecover(ctx, reads, task)
	finishedAt := nowFn()
	if execErr != nil {
		span.RecordError(execErr)
	}

	run := store.ScheduledTaskRun{
		TaskID:     task.ID,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
	}

	auditEventID := uuid.NewString()
	user := systemUser()

	if execErr != nil {
		run.Status = store.ScheduledTaskStatusFailed
		run.Error = execErr.Error()
		recordAudit(ctx, auditService, auditEventID, task, user, audit.ActionScheduledTaskFailed, audit.DecisionExecutionError, finishedAt, execErr)
	} else {
		run.Status = store.ScheduledTaskStatusSucceeded
		run.ResultData = result
		run.ResultSummary = summarizeResult(result)
		recordAudit(ctx, auditService, auditEventID, task, user, audit.ActionScheduledTaskSucceeded, audit.DecisionPermitted, finishedAt, nil)
	}
	run.AuditEventID = auditEventID

	// 专项：原子事务 — AppendRun + UpdateTask 合并，CAS 乐观锁防数据竞争。
	task.LastRunAt = &startedAt
	task.LastStatus = run.Status
	task.UpdatedAt = finishedAt
	if err := taskStore.AppendRunAndUpdateTask(ctx, run, task, expectedUpdatedAt); err != nil {
		if errors.Is(err, store.ErrConcurrentModification) {
			// CAS 冲突：其他实例已介入，记 warning 不重试。run 已回滚。
			return run, nil
		}
		return store.ScheduledTaskRun{}, fmt.Errorf("scheduler: append run and update task: %w", err)
	}
	return run, nil
}

// executeWithRecover 调用 ExecuteTrustedRead 并 recover panic，避免调度器崩溃。
func executeWithRecover(ctx context.Context, reads *execution.ReadOnlyService, task store.ScheduledTask) (result map[string]any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("scheduler: capability %q panicked: %v", task.CapabilityName, r)
		}
	}()
	result, err = reads.ExecuteTrustedRead(ctx, task.CapabilityName, task.Input)
	return
}

// recordAudit 写审计事件。auditService 为 nil 时静默跳过（测试可能不注入）。
func recordAudit(ctx context.Context, auditService *audit.Service, eventID string, task store.ScheduledTask, user identity.CurrentUser, action, decision string, now time.Time, execErr error) {
	if auditService == nil {
		return
	}
	metadata := map[string]any{
		"task_id":       task.ID,
		"task_name":     task.Name,
		"capability":    task.CapabilityName,
		"schedule_kind": task.ScheduleKind,
	}
	if execErr != nil {
		metadata["error"] = execErr.Error()
	}
	_ = auditService.Record(ctx, audit.Event{
		ID:        eventID,
		RequestID: user.RequestID,
		Subject:   user.Subject,
		ToolName:  task.CapabilityName,
		Action:    action,
		Decision:  decision,
		Metadata:  metadata,
		CreatedAt: now,
	})
}

// summarizeResult 从执行结果生成简短摘要，用于 run 记录的 result_summary 字段。
func summarizeResult(result map[string]any) string {
	if result == nil {
		return ""
	}
	if status, ok := result["status"].(string); ok && status != "" {
		return status
	}
	return "ok"
}
