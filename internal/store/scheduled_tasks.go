package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// 调度状态常量。
const (
	ScheduleKindPreset = "preset"
	ScheduleKindCron   = "cron"

	ScheduledTaskStatusSucceeded = "succeeded"
	ScheduledTaskStatusFailed    = "failed"
)

// ScheduledTask 描述一条定时巡检任务。Subject 用于多租户隔离；
// ScheduleKind 取 'preset' 或 'cron'，分别对应 Preset / CronExpr 字段。
// NextRunAt 是调度器断点，进程重启后从此字段恢复。
type ScheduledTask struct {
	ID             string
	Name           string
	Subject        string
	CapabilityName string
	Input          map[string]any
	ScheduleKind   string
	Preset         string
	CronExpr       string
	Timezone       string
	Enabled        bool
	LastRunAt      *time.Time
	LastStatus     string
	NextRunAt      time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ScheduledTaskRun 是一次执行的不可变记录。Status ∈ {'succeeded', 'failed'}。
// AuditEventID 关联到对应的 audit 事件，便于回溯。
type ScheduledTaskRun struct {
	ID            string
	TaskID        string
	StartedAt     time.Time
	FinishedAt    time.Time
	Status        string
	ResultSummary string
	ResultData    map[string]any
	Error         string
	AuditEventID  string
}

// ScheduledTaskFilter 范围查询过滤条件。Subject 必填；Enabled 为 nil 时不按
// 启用状态过滤；Limit > 0 时限制返回行数（按 CreatedAt 降序）。
type ScheduledTaskFilter struct {
	Subject string
	Enabled *bool
	Limit   int
	Offset  int
}

// ScheduledTaskStore 是定时任务持久化边界。Subject 隔离由 GetTask / UpdateTask /
// DeleteTask 强制保证；GetTaskByID / ListTasks / ListDueTasks / ListRuns / AppendRun /
// CountRecentFailures 不校验 Subject（上层 service 按需控制）。
type ScheduledTaskStore interface {
	CreateTask(ctx context.Context, task ScheduledTask) (ScheduledTask, error)
	GetTask(ctx context.Context, id, subject string) (ScheduledTask, error)
	GetTaskByID(ctx context.Context, id string) (ScheduledTask, error)
	ListTasks(ctx context.Context, filter ScheduledTaskFilter) ([]ScheduledTask, error)
	UpdateTask(ctx context.Context, task ScheduledTask) (ScheduledTask, error)
	DeleteTask(ctx context.Context, id, subject string) error
	ListDueTasks(ctx context.Context, now time.Time, limit int) ([]ScheduledTask, error)
	AppendRun(ctx context.Context, run ScheduledTaskRun) (ScheduledTaskRun, error)
	ListRuns(ctx context.Context, taskID string, limit int) ([]ScheduledTaskRun, error)
	CountRecentFailures(ctx context.Context, since time.Time) (int, error)
	// ClaimTask 原子 CAS 认领任务：仅当 task 的 next_run_at 仍等于 expectedNextRunAt 时，
	// 把 next_run_at 推进到 newNextRunAt、last_run_at 设为 now、last_status 设为 running，
	// 返回 true。否则（已被其他实例认领）返回 false，不修改任何字段。
	// 用于多实例部署防重复执行（专项：任务准-并发安全）。
	ClaimTask(ctx context.Context, taskID string, expectedNextRunAt, newNextRunAt, now time.Time) (bool, error)
	// AppendRunAndUpdateTask 在单个事务内追加 run 记录并更新 task 状态（含 CAS 乐观锁）。
	// expectedUpdatedAt 用于 CAS：仅当 task 的 updated_at 仍等于此值时才更新 task，
	// 否则返回 ErrConcurrentModification 且 run 也回滚（整体原子）。
	// 用于多实例部署防数据竞争（专项：任务准-并发安全）。
	AppendRunAndUpdateTask(ctx context.Context, run ScheduledTaskRun, task ScheduledTask, expectedUpdatedAt time.Time) error
}

// ErrConcurrentModification 在 AppendRunAndUpdateTask 的 task CAS 失败时返回，
// 表示其他实例已修改 task，本次更新应放弃（专项：任务准-并发安全）。
var ErrConcurrentModification = errors.New("concurrent modification: task was modified by another instance")

// MemoryScheduledTaskStore 提供并发安全的内存实现，用于单元测试。
type MemoryScheduledTaskStore struct {
	mu    sync.Mutex
	tasks map[string]ScheduledTask
	runs  []ScheduledTaskRun
}

func NewMemoryScheduledTaskStore() *MemoryScheduledTaskStore {
	return &MemoryScheduledTaskStore{
		tasks: make(map[string]ScheduledTask),
	}
}

func (s *MemoryScheduledTaskStore) CreateTask(_ context.Context, task ScheduledTask) (ScheduledTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if task.ID == "" {
		task.ID = uuid.NewString()
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now().UTC()
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	task.Input = clonePayload(task.Input)
	s.tasks[task.ID] = task
	return cloneScheduledTask(task), nil
}

func (s *MemoryScheduledTaskStore) GetTask(_ context.Context, id, subject string) (ScheduledTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok || task.Subject != subject {
		return ScheduledTask{}, ErrNotFound
	}
	return cloneScheduledTask(task), nil
}

func (s *MemoryScheduledTaskStore) GetTaskByID(_ context.Context, id string) (ScheduledTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok {
		return ScheduledTask{}, ErrNotFound
	}
	return cloneScheduledTask(task), nil
}

func (s *MemoryScheduledTaskStore) ListTasks(_ context.Context, filter ScheduledTaskFilter) ([]ScheduledTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	matched := make([]ScheduledTask, 0, len(s.tasks))
	for _, task := range s.tasks {
		if filter.Subject != "" && task.Subject != filter.Subject {
			continue
		}
		if filter.Enabled != nil && task.Enabled != *filter.Enabled {
			continue
		}
		matched = append(matched, cloneScheduledTask(task))
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].ID < matched[j].ID
		}
		return matched[i].CreatedAt.Before(matched[j].CreatedAt)
	})
	if filter.Offset > 0 && filter.Offset < len(matched) {
		matched = matched[filter.Offset:]
	} else if filter.Offset > 0 {
		matched = matched[:0]
	}
	if filter.Limit > 0 && len(matched) > filter.Limit {
		matched = matched[:filter.Limit]
	}
	return matched, nil
}

func (s *MemoryScheduledTaskStore) UpdateTask(_ context.Context, task ScheduledTask) (ScheduledTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.tasks[task.ID]
	if !ok || existing.Subject != task.Subject {
		return ScheduledTask{}, ErrNotFound
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = time.Now().UTC()
	}
	task.Input = clonePayload(task.Input)
	task.CreatedAt = existing.CreatedAt
	s.tasks[task.ID] = task
	return cloneScheduledTask(task), nil
}

func (s *MemoryScheduledTaskStore) DeleteTask(_ context.Context, id, subject string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.tasks[id]
	if !ok || existing.Subject != subject {
		return ErrNotFound
	}
	delete(s.tasks, id)
	return nil
}

func (s *MemoryScheduledTaskStore) ListDueTasks(_ context.Context, now time.Time, limit int) ([]ScheduledTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	matched := make([]ScheduledTask, 0, len(s.tasks))
	for _, task := range s.tasks {
		if !task.Enabled {
			continue
		}
		if task.NextRunAt.After(now) {
			continue
		}
		matched = append(matched, cloneScheduledTask(task))
	}
	// 按 next_run_at 升序处理到期任务
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].NextRunAt.Equal(matched[j].NextRunAt) {
			return matched[i].ID < matched[j].ID
		}
		return matched[i].NextRunAt.Before(matched[j].NextRunAt)
	})
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

func (s *MemoryScheduledTaskStore) AppendRun(_ context.Context, run ScheduledTaskRun) (ScheduledTaskRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if run.ID == "" {
		run.ID = uuid.NewString()
	}
	run.ResultData = clonePayload(run.ResultData)
	s.runs = append(s.runs, run)
	return cloneScheduledTaskRun(run), nil
}

// ClaimTask 内存版 CAS 认领：mu 保护原子性，next_run_at 匹配则推进并返回 true。
func (s *MemoryScheduledTaskStore) ClaimTask(_ context.Context, taskID string, expectedNextRunAt, newNextRunAt, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.tasks[taskID]
	if !ok {
		return false, ErrNotFound
	}
	if !existing.NextRunAt.Equal(expectedNextRunAt) {
		return false, nil
	}
	existing.NextRunAt = newNextRunAt
	existing.LastRunAt = &now
	existing.LastStatus = "running"
	existing.UpdatedAt = now
	s.tasks[taskID] = existing
	return true, nil
}

// AppendRunAndUpdateTask 内存版原子事务：mu 保护，task CAS 失败则不写 run。
func (s *MemoryScheduledTaskStore) AppendRunAndUpdateTask(_ context.Context, run ScheduledTaskRun, task ScheduledTask, expectedUpdatedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.tasks[task.ID]
	if !ok {
		return ErrNotFound
	}
	if !existing.UpdatedAt.Equal(expectedUpdatedAt) {
		return ErrConcurrentModification
	}
	if run.ID == "" {
		run.ID = uuid.NewString()
	}
	run.ResultData = clonePayload(run.ResultData)
	s.runs = append(s.runs, run)
	task.Input = clonePayload(task.Input)
	task.CreatedAt = existing.CreatedAt
	s.tasks[task.ID] = task
	return nil
}

func (s *MemoryScheduledTaskStore) ListRuns(_ context.Context, taskID string, limit int) ([]ScheduledTaskRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	matched := make([]ScheduledTaskRun, 0)
	for _, run := range s.runs {
		if run.TaskID != taskID {
			continue
		}
		matched = append(matched, cloneScheduledTaskRun(run))
	}
	// 按 started_at 降序（最新在前）
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].StartedAt.Equal(matched[j].StartedAt) {
			return matched[i].ID > matched[j].ID
		}
		return matched[i].StartedAt.After(matched[j].StartedAt)
	})
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

func (s *MemoryScheduledTaskStore) CountRecentFailures(_ context.Context, since time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, run := range s.runs {
		if run.Status != ScheduledTaskStatusFailed {
			continue
		}
		if run.FinishedAt.Before(since) {
			continue
		}
		count++
	}
	return count, nil
}

// SQLScheduledTaskStore 在 MySQL/SQLite 上持久化定时任务。它跨服务实例并发安全。
type SQLScheduledTaskStore struct{ db *sql.DB }

func NewSQLScheduledTaskStore(db *sql.DB) *SQLScheduledTaskStore {
	return &SQLScheduledTaskStore{db: db}
}

func (s *SQLScheduledTaskStore) CreateTask(ctx context.Context, task ScheduledTask) (ScheduledTask, error) {
	if task.ID == "" {
		task.ID = uuid.NewString()
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now().UTC()
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	input, err := marshalJSON(task.Input)
	if err != nil {
		return ScheduledTask{}, fmt.Errorf("marshal task input: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO copilot_scheduled_tasks
		(id, name, subject, capability_name, input, schedule_kind, preset, cron_expr, timezone, enabled, last_run_at, last_status, next_run_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.Name, task.Subject, task.CapabilityName, input,
		task.ScheduleKind, nullableString(task.Preset), nullableString(task.CronExpr),
		task.Timezone, task.Enabled, nullableTime(task.LastRunAt), nullableString(task.LastStatus),
		task.NextRunAt, task.CreatedAt, task.UpdatedAt)
	if err != nil {
		return ScheduledTask{}, fmt.Errorf("insert scheduled task: %w", err)
	}
	return task, nil
}

func (s *SQLScheduledTaskStore) GetTask(ctx context.Context, id, subject string) (ScheduledTask, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, subject, capability_name, input, schedule_kind, preset, cron_expr, timezone, enabled, last_run_at, last_status, next_run_at, created_at, updated_at
		FROM copilot_scheduled_tasks WHERE id = ? AND subject = ?`, id, subject)
	task, err := scanScheduledTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ScheduledTask{}, ErrNotFound
	}
	return task, err
}

func (s *SQLScheduledTaskStore) GetTaskByID(ctx context.Context, id string) (ScheduledTask, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, subject, capability_name, input, schedule_kind, preset, cron_expr, timezone, enabled, last_run_at, last_status, next_run_at, created_at, updated_at
		FROM copilot_scheduled_tasks WHERE id = ?`, id)
	task, err := scanScheduledTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ScheduledTask{}, ErrNotFound
	}
	return task, err
}

func (s *SQLScheduledTaskStore) ListTasks(ctx context.Context, filter ScheduledTaskFilter) ([]ScheduledTask, error) {
	query := `SELECT id, name, subject, capability_name, input, schedule_kind, preset, cron_expr, timezone, enabled, last_run_at, last_status, next_run_at, created_at, updated_at FROM copilot_scheduled_tasks`
	conditions := []string{}
	args := []any{}
	if filter.Subject != "" {
		conditions = append(conditions, "subject = ?")
		args = append(args, filter.Subject)
	}
	if filter.Enabled != nil {
		conditions = append(conditions, "enabled = ?")
		args = append(args, *filter.Enabled)
	}
	if len(conditions) > 0 {
		query += " WHERE " + joinAnd(conditions)
	}
	query += " ORDER BY created_at ASC, id ASC"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}
	if filter.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, filter.Offset)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query scheduled tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	tasks := []ScheduledTask{}
	for rows.Next() {
		task, err := scanScheduledTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan scheduled task: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scheduled tasks: %w", err)
	}
	return tasks, nil
}

func (s *SQLScheduledTaskStore) UpdateTask(ctx context.Context, task ScheduledTask) (ScheduledTask, error) {
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = time.Now().UTC()
	}
	input, err := marshalJSON(task.Input)
	if err != nil {
		return ScheduledTask{}, fmt.Errorf("marshal task input: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE copilot_scheduled_tasks SET
		name = ?, capability_name = ?, input = ?, schedule_kind = ?, preset = ?, cron_expr = ?, timezone = ?, enabled = ?, last_run_at = ?, last_status = ?, next_run_at = ?, updated_at = ?
		WHERE id = ? AND subject = ?`,
		task.Name, task.CapabilityName, input,
		task.ScheduleKind, nullableString(task.Preset), nullableString(task.CronExpr),
		task.Timezone, task.Enabled, nullableTime(task.LastRunAt), nullableString(task.LastStatus),
		task.NextRunAt, task.UpdatedAt, task.ID, task.Subject)
	if err != nil {
		return ScheduledTask{}, fmt.Errorf("update scheduled task: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ScheduledTask{}, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return ScheduledTask{}, ErrNotFound
	}
	return task, nil
}

func (s *SQLScheduledTaskStore) DeleteTask(ctx context.Context, id, subject string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM copilot_scheduled_tasks WHERE id = ? AND subject = ?`, id, subject)
	if err != nil {
		return fmt.Errorf("delete scheduled task: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLScheduledTaskStore) ListDueTasks(ctx context.Context, now time.Time, limit int) ([]ScheduledTask, error) {
	query := `SELECT id, name, subject, capability_name, input, schedule_kind, preset, cron_expr, timezone, enabled, last_run_at, last_status, next_run_at, created_at, updated_at
		FROM copilot_scheduled_tasks WHERE enabled = TRUE AND next_run_at <= ?
		ORDER BY next_run_at ASC, id ASC`
	args := []any{now}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query due scheduled tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	tasks := []ScheduledTask{}
	for rows.Next() {
		task, err := scanScheduledTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan scheduled task: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due scheduled tasks: %w", err)
	}
	return tasks, nil
}

func (s *SQLScheduledTaskStore) AppendRun(ctx context.Context, run ScheduledTaskRun) (ScheduledTaskRun, error) {
	if run.ID == "" {
		run.ID = uuid.NewString()
	}
	data, err := marshalJSON(run.ResultData)
	if err != nil {
		return ScheduledTaskRun{}, fmt.Errorf("marshal run result data: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO copilot_scheduled_task_runs
		(id, task_id, started_at, finished_at, status, result_summary, result_data, error, audit_event_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.TaskID, run.StartedAt, run.FinishedAt, run.Status,
		nullableString(run.ResultSummary), data, nullableString(run.Error), nullableString(run.AuditEventID))
	if err != nil {
		return ScheduledTaskRun{}, fmt.Errorf("insert scheduled task run: %w", err)
	}
	return run, nil
}

// ClaimTask MySQL 版 CAS 认领：原子 UPDATE，affected_rows=1 表示抢到。
func (s *SQLScheduledTaskStore) ClaimTask(ctx context.Context, taskID string, expectedNextRunAt, newNextRunAt, now time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE copilot_scheduled_tasks
		SET next_run_at = ?, last_run_at = ?, last_status = 'running', updated_at = ?
		WHERE id = ? AND next_run_at = ?`,
		newNextRunAt, now, now, taskID, expectedNextRunAt)
	if err != nil {
		return false, fmt.Errorf("claim scheduled task: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected: %w", err)
	}
	return affected == 1, nil
}

// AppendRunAndUpdateTask MySQL 版原子事务：BeginTx 内 INSERT run + CAS UPDATE task，
// task CAS 失败（affected=0）返回 ErrConcurrentModification 并回滚 run。
func (s *SQLScheduledTaskStore) AppendRunAndUpdateTask(ctx context.Context, run ScheduledTaskRun, task ScheduledTask, expectedUpdatedAt time.Time) error {
	if run.ID == "" {
		run.ID = uuid.NewString()
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = time.Now().UTC()
	}
	data, err := marshalJSON(run.ResultData)
	if err != nil {
		return fmt.Errorf("marshal run result data: %w", err)
	}
	input, err := marshalJSON(task.Input)
	if err != nil {
		return fmt.Errorf("marshal task input: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // 已提交时 Rollback 是 no-op

	if _, err := tx.ExecContext(ctx, `INSERT INTO copilot_scheduled_task_runs
		(id, task_id, started_at, finished_at, status, result_summary, result_data, error, audit_event_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.TaskID, run.StartedAt, run.FinishedAt, run.Status,
		nullableString(run.ResultSummary), data, nullableString(run.Error), nullableString(run.AuditEventID)); err != nil {
		return fmt.Errorf("insert run in tx: %w", err)
	}

	result, err := tx.ExecContext(ctx, `UPDATE copilot_scheduled_tasks SET
		name = ?, capability_name = ?, input = ?, schedule_kind = ?, preset = ?, cron_expr = ?, timezone = ?, enabled = ?, last_run_at = ?, last_status = ?, next_run_at = ?, updated_at = ?
		WHERE id = ? AND subject = ? AND updated_at = ?`,
		task.Name, task.CapabilityName, input,
		task.ScheduleKind, nullableString(task.Preset), nullableString(task.CronExpr),
		task.Timezone, task.Enabled, nullableTime(task.LastRunAt), nullableString(task.LastStatus),
		task.NextRunAt, task.UpdatedAt, task.ID, task.Subject, expectedUpdatedAt)
	if err != nil {
		return fmt.Errorf("update task in tx: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected in tx: %w", err)
	}
	if affected == 0 {
		return ErrConcurrentModification
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (s *SQLScheduledTaskStore) ListRuns(ctx context.Context, taskID string, limit int) ([]ScheduledTaskRun, error) {
	query := `SELECT id, task_id, started_at, finished_at, status, result_summary, result_data, error, audit_event_id
		FROM copilot_scheduled_task_runs WHERE task_id = ?
		ORDER BY started_at DESC, id DESC`
	args := []any{taskID}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query scheduled task runs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	runs := []ScheduledTaskRun{}
	for rows.Next() {
		run, err := scanScheduledTaskRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan scheduled task run: %w", err)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scheduled task runs: %w", err)
	}
	return runs, nil
}

func (s *SQLScheduledTaskStore) CountRecentFailures(ctx context.Context, since time.Time) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM copilot_scheduled_task_runs WHERE status = ? AND finished_at >= ?`, ScheduledTaskStatusFailed, since).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count recent failures: %w", err)
	}
	return count, nil
}

type scheduledTaskScanner interface {
	Scan(dest ...any) error
}

func scanScheduledTask(row scheduledTaskScanner) (ScheduledTask, error) {
	var task ScheduledTask
	var preset, cronExpr, lastStatus sql.NullString
	var lastRunAt sql.NullTime
	var input []byte
	err := row.Scan(&task.ID, &task.Name, &task.Subject, &task.CapabilityName, &input,
		&task.ScheduleKind, &preset, &cronExpr, &task.Timezone, &task.Enabled,
		&lastRunAt, &lastStatus, &task.NextRunAt, &task.CreatedAt, &task.UpdatedAt)
	if err != nil {
		return ScheduledTask{}, err
	}
	task.Preset = preset.String
	task.CronExpr = cronExpr.String
	task.LastStatus = lastStatus.String
	if lastRunAt.Valid {
		task.LastRunAt = pointerTo(lastRunAt.Time)
	}
	if len(input) > 0 {
		task.Input = map[string]any{}
		if err := json.Unmarshal(input, &task.Input); err != nil {
			return ScheduledTask{}, fmt.Errorf("unmarshal task input: %w", err)
		}
	}
	return task, nil
}

func scanScheduledTaskRun(row scheduledTaskScanner) (ScheduledTaskRun, error) {
	var run ScheduledTaskRun
	var resultSummary, errMsg, auditEventID sql.NullString
	var data []byte
	err := row.Scan(&run.ID, &run.TaskID, &run.StartedAt, &run.FinishedAt, &run.Status,
		&resultSummary, &data, &errMsg, &auditEventID)
	if err != nil {
		return ScheduledTaskRun{}, err
	}
	run.ResultSummary = resultSummary.String
	run.Error = errMsg.String
	run.AuditEventID = auditEventID.String
	if len(data) > 0 {
		run.ResultData = map[string]any{}
		if err := json.Unmarshal(data, &run.ResultData); err != nil {
			return ScheduledTaskRun{}, fmt.Errorf("unmarshal run result data: %w", err)
		}
	}
	return run, nil
}

func cloneScheduledTask(task ScheduledTask) ScheduledTask {
	task.Input = clonePayload(task.Input)
	if task.LastRunAt != nil {
		copy := *task.LastRunAt
		task.LastRunAt = &copy
	}
	return task
}

func cloneScheduledTaskRun(run ScheduledTaskRun) ScheduledTaskRun {
	run.ResultData = clonePayload(run.ResultData)
	return run
}

func marshalJSON(payload map[string]any) (any, error) {
	if payload == nil {
		return nil, nil
	}
	bytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return bytes, nil
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

func joinAnd(parts []string) string {
	result := ""
	for i, part := range parts {
		if i > 0 {
			result += " AND "
		}
		result += part
	}
	return result
}
