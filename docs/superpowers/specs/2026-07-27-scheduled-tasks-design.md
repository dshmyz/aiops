# 管理员定时巡检任务设计

- 日期：2026-07-27
- 范围：copilot-api / capability-console
- 状态：已完成（2026-08-02，scheduler + /v1/scheduled-tasks + 前端 ScheduledTasksView 均已落地）

## 背景与动机

当前 AI 运维助手只支持「用户发消息 → 同步执行」的链路。运维场景中存在大量「每天凌晨 2 点检查所有 MinIO bucket 容量」「每 5 分钟巡检 Kafka topic 健康」这类**周期性巡检**需求，目前只能让运维人员手动调用 AI 助手或外部 cron 脚本绕过治理。

代码库现状：
- `action_plans` 表只覆盖「用户触发 → 确认 → 执行」同步链路，无 `next_run_at`/`cron_expr` 等调度字段
- 无 scheduler goroutine、无后台 worker、无 `/v1/scheduled-tasks` 路由
- 前端侧边栏只有 AI 助手 / 能力管理 / 待确认计划 / 审计记录 4 个入口
- migrations 001-004 无定时任务相关表
- identity 模块已有 `CurrentUser.Roles []string` 字段，可直接做管理员鉴权

`internal/execution/readonly.go` 的 `ReadOnlyService.ExecuteRead(ctx, user, toolName, input)` 已封装「HTTP 适配器调用只读 capability + 写审计」逻辑，可直接被调度器复用。

## 目标

- 管理员可在 UI 创建/编辑/启停定时巡检任务，配置预设频率或标准 cron 表达式
- 任务到期时自动调用已发布的只读 capability，无需人工触发
- 执行结果落库 + 写审计 + 在侧边栏 badge 呈现失败数
- 跨重启恢复：`next_run_at` 持久化，进程重启后从断点继续

## 非目标

- 不支持写入类 capability（写入类仍走 action_plan 链路）
- 不支持多实例部署（leader election 不做，MVP 单实例）
- 不支持 webhook / IM 通知（只做侧边栏 badge + 历史页）
- 不支持子任务 / 依赖 DAG
- 不支持物理删除（只做启停 + 软删除归档）

## 架构总览

```
┌────────────────────────────────────────────────────────┐
│ copilot-api 进程                                       │
│                                                        │
│  HTTP server (现有)                                    │
│    └── /v1/scheduled-tasks* (新增, RequireRole=admin)  │
│                                                        │
│  scheduler goroutine (新增, 启动时 Spawn)              │
│    └── 每 60s tick                                     │
│        → 查 enabled AND next_run_at <= now            │
│        → 调 ReadOnlyService.ExecuteRead                │
│        → 写 scheduled_task_runs                       │
│        → 写 audit_event (action=scheduled_task_run)   │
│        → 更新 next_run_at = computeNextRun(schedule)   │
└────────────────────────────────────────────────────────┘
                       │
                       ▼
              复用 ReadOnlyService
              (HTTP 适配器调用 + audit 写入)
```

## 数据模型

### migration `migrations/005_scheduled_tasks.sql`

```sql
CREATE TABLE IF NOT EXISTS copilot_scheduled_tasks (
    id CHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    subject VARCHAR(255) NOT NULL,
    capability_name VARCHAR(255) NOT NULL,
    input JSON NOT NULL,
    schedule_kind VARCHAR(16) NOT NULL,
    preset VARCHAR(16) NULL,
    cron_expr VARCHAR(64) NULL,
    timezone VARCHAR(64) NOT NULL DEFAULT 'Asia/Shanghai',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    last_run_at DATETIME(6) NULL,
    last_status VARCHAR(16) NULL,
    next_run_at DATETIME(6) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_scheduled_tasks_next_run (enabled, next_run_at),
    KEY idx_scheduled_tasks_subject (subject)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS copilot_scheduled_task_runs (
    id CHAR(36) NOT NULL,
    task_id CHAR(36) NOT NULL,
    started_at DATETIME(6) NOT NULL,
    finished_at DATETIME(6) NOT NULL,
    status VARCHAR(16) NOT NULL,
    result_summary TEXT NULL,
    result_data JSON NULL,
    error TEXT NULL,
    audit_event_id CHAR(36) NULL,
    PRIMARY KEY (id),
    KEY idx_task_runs_task_started (task_id, started_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
```

字段说明：
- `schedule_kind ∈ {'preset', 'cron'}`：preset 模式读 `preset` 字段；cron 模式读 `cron_expr`
- `preset ∈ {'5m', '1h', 'daily', 'weekly'}`：5m=每 5 分钟、1h=每小时、daily=每天 00:00、weekly=每周一 00:00
- `cron_expr`：标准 5 字段 cron（`minute hour day month weekday`），由 `robfig/cron/v3` 解析
- `timezone`：任务级时区，DB 存 UTC，调度按 task.timezone 计算
- `last_status`：最近一次执行状态，用于列表页快速展示
- `next_run_at`：调度器断点，进程重启后立即恢复

## 后端分层

### 1. store 层：`internal/store/scheduled_tasks.go`

```go
type ScheduledTask struct {
    ID             string
    Name           string
    Subject        string
    CapabilityName string
    Input          map[string]any
    ScheduleKind   string  // 'preset' | 'cron'
    Preset         string  // '5m' | '1h' | 'daily' | 'weekly'
    CronExpr       string
    Timezone       string
    Enabled        bool
    LastRunAt      *time.Time
    LastStatus     string  // 'succeeded' | 'failed' | ''
    NextRunAt      time.Time
    CreatedAt      time.Time
    UpdatedAt      time.Time
}

type ScheduledTaskRun struct {
    ID            string
    TaskID        string
    StartedAt     time.Time
    FinishedAt    time.Time
    Status        string  // 'succeeded' | 'failed'
    ResultSummary string
    ResultData    map[string]any
    Error         string
    AuditEventID  string
}

type ScheduledTaskFilter struct {
    Subject string
    Enabled *bool
    Limit   int
}

type ScheduledTaskStore interface {
    CreateTask(ctx context.Context, task ScheduledTask) (ScheduledTask, error)
    GetTask(ctx context.Context, id, subject string) (ScheduledTask, error)
    ListTasks(ctx context.Context, filter ScheduledTaskFilter) ([]ScheduledTask, error)
    UpdateTask(ctx context.Context, task ScheduledTask) (ScheduledTask, error)
    DeleteTask(ctx context.Context, id, subject string) error
    ListDueTasks(ctx context.Context, now time.Time, limit int) ([]ScheduledTask, error)
    AppendRun(ctx context.Context, run ScheduledTaskRun) (ScheduledTaskRun, error)
    ListRuns(ctx context.Context, taskID string, limit int) ([]ScheduledTaskRun, error)
    CountRecentFailures(ctx context.Context, since time.Time) (int, error)
}
```

提供 `MemoryScheduledTaskStore` 和 `SQLScheduledTaskStore` 双实现，与 `action_plans.go`、`assistant_conversations.go` 一致。

### 2. scheduler 层：`internal/scheduler/`

#### `schedule.go` - 纯函数计算下次执行时间

```go
// computeNextRun 根据 schedule 配置计算 next_run_at。
// timezone 为空时按 UTC。
func computeNextRun(kind, preset, cronExpr, timezone string, now time.Time) (time.Time, error)
```

preset 模式：
- `5m` → now.Truncate(5m).Add(5m)
- `1h` → now.Truncate(time.Hour).Add(time.Hour)
- `daily` → 次日 00:00（按 timezone）
- `weekly` → 下个周一 00:00（按 timezone）

cron 模式：用 `robfig/cron/v3` 的 `cron.ParseStandard` + 自定义 `Next(time.Time)` 计算。

#### `scheduler.go` - 调度器

```go
type Scheduler struct {
    store        ScheduledTaskStore
    reads        *execution.ReadOnlyService
    auditService *audit.Service
    tickInterval time.Duration
    now          func() time.Time
}

func (s *Scheduler) Start(ctx context.Context)  // 启动 goroutine
func (s *Scheduler) tickOnce(ctx context.Context) error  // 测试入口
```

`tickOnce` 逻辑：
1. `store.ListDueTasks(now, 50)` 拉到期任务
2. 串行执行每个任务（避免并发冲爆后端 API）
3. 调 `reads.ExecuteRead(ctx, systemUser, task.CapabilityName, task.Input)`
4. 写 `scheduled_task_runs`（succeeded/failed）
5. 写 `audit_event`（`action=scheduled_task_run`, `decision=permitted`/`denied`）
6. 更新 task 的 `last_run_at`/`last_status`/`next_run_at`（即使失败也更新 next_run_at，避免卡死）

`systemUser` 用 `identity.CurrentUser{Subject: "system:scheduler", Roles: []string{"scheduler"}}` 标识系统调用。

#### `service.go` - 应用层 CRUD + 鉴权

```go
type Service struct {
    store ScheduledTaskStore
    now   func() time.Time
}

func (s *Service) Create(ctx context.Context, user identity.CurrentUser, req CreateRequest) (ScheduledTask, error)
func (s *Service) Update(ctx context.Context, user identity.CurrentUser, id string, req UpdateRequest) (ScheduledTask, error)
func (s *Service) Delete(ctx context.Context, user identity.CurrentUser, id string) error
func (s *Service) Get(ctx context.Context, user identity.CurrentUser, id string) (ScheduledTask, error)
func (s *Service) List(ctx context.Context, user identity.CurrentUser, filter ScheduledTaskFilter) ([]ScheduledTask, error)
func (s *Service) Trigger(ctx context.Context, user identity.CurrentUser, id string) (ScheduledTaskRun, error)
func (s *Service) ListRuns(ctx context.Context, user identity.CurrentUser, taskID string, limit int) ([]ScheduledTaskRun, error)
func (s *Service) CountRecentFailures(ctx context.Context, since time.Time) (int, error)
```

所有写操作校验 `user.Roles` 包含 `admin`；查询类操作允许任意登录用户（运维人员可查看）。

`Trigger` 是手动触发：管理员在 UI 点击「立即运行」时调用，复用 scheduler 的执行逻辑但不更新 next_run_at。

### 3. httpapi 层：`internal/httpapi/router.go` 扩展

新增路由：

```
POST   /v1/scheduled-tasks                  RequireRole=admin
GET    /v1/scheduled-tasks                  任意登录用户
GET    /v1/scheduled-tasks/{id}             任意登录用户
PATCH  /v1/scheduled-tasks/{id}             RequireRole=admin
DELETE /v1/scheduled-tasks/{id}             RequireRole=admin
POST   /v1/scheduled-tasks/{id}/run         RequireRole=admin
GET    /v1/scheduled-tasks/{id}/runs        任意登录用户
GET    /v1/scheduled-tasks/failures/count   任意登录用户
```

新增 `RequireRole(role string)` 中间件 helper，校验 `user.Roles` 包含指定角色。

`Router` 新增 `scheduledTasks ScheduledTaskService` 字段和 `WithScheduledTasks(service)` Option，未注入时路由返回 503（与 conversations 一致）。

### 4. cmd/copilot-api/main.go 扩展

启动时创建 scheduler 并 Spawn：

```go
scheduler := scheduler.New(store, reads, auditService, 60*time.Second, time.Now)
go scheduler.Start(ctx)
```

进程退出时通过 ctx cancel 让 scheduler 优雅停止（正在执行的任务等完成后退出，下一次 tick 不再触发）。

## 前端

### 组件结构

```
apps/capability-console/src/
├── components/
│   ├── ScheduledTaskList.vue         列表 + 启停开关 + 删除
│   ├── ScheduledTaskForm.vue          创建/编辑表单（容器）
│   │   ├── SchedulePresetPicker.vue  预设模板选择
│   │   └── ScheduleCronInput.vue     cron 表达式 + 下次预览
│   ├── ScheduledTaskRunHistory.vue    执行历史（分页 + 失败红色高亮）
│   └── ScheduledTaskBadge.vue         侧边栏 badge
├── api.ts                            扩展 scheduledTask* 函数
├── types.ts                          扩展 ScheduledTask / ScheduledTaskRun 类型
└── App.vue                           新增侧边栏入口 + 视图
```

### types.ts 扩展

```typescript
export type ScheduleKind = 'preset' | 'cron';
export type SchedulePreset = '5m' | '1h' | 'daily' | 'weekly';
export type ScheduledTaskStatus = 'succeeded' | 'failed' | '';

export interface ScheduledTask {
  id: string;
  name: string;
  subject: string;
  capability_name: string;
  input: Record<string, unknown>;
  schedule_kind: ScheduleKind;
  preset: SchedulePreset | null;
  cron_expr: string | null;
  timezone: string;
  enabled: boolean;
  last_run_at: string | null;
  last_status: ScheduledTaskStatus;
  next_run_at: string;
  created_at: string;
  updated_at: string;
}

export interface ScheduledTaskRun {
  id: string;
  task_id: string;
  started_at: string;
  finished_at: string;
  status: 'succeeded' | 'failed';
  result_summary: string;
  result_data: Record<string, unknown> | null;
  error: string;
  audit_event_id: string;
}
```

### api.ts 扩展

```typescript
export async function listScheduledTasks(filter?: { enabled?: boolean }): Promise<ScheduledTask[]>
export async function getScheduledTask(id: string): Promise<ScheduledTask>
export async function createScheduledTask(payload: CreateScheduledTaskPayload): Promise<ScheduledTask>
export async function updateScheduledTask(id: string, payload: UpdateScheduledTaskPayload): Promise<ScheduledTask>
export async function deleteScheduledTask(id: string): Promise<void>
export async function triggerScheduledTask(id: string): Promise<ScheduledTaskRun>
export async function listScheduledTaskRuns(id: string, limit?: number): Promise<ScheduledTaskRun[]>
export async function countScheduledTaskFailures(): Promise<number>
```

### App.vue 扩展

新增视图 `ActiveView = 'assistant' | 'management' | 'plans' | 'audit' | 'scheduled-tasks'`。

侧边栏新增入口：
```vue
<button data-test="nav-scheduled-tasks" data-view="scheduled-tasks" ...>
  <span class="nav-icon">⏰</span>
  定时巡检
  <span v-if="scheduledTaskFailures > 0" data-test="nav-badge" class="nav-badge">{{ scheduledTaskFailures }}</span>
</button>
```

主视图区根据 `activeView === 'scheduled-tasks'` 渲染 `<ScheduledTaskList>`，并提供新建/编辑入口。

## 失败通知

- `GET /v1/scheduled-tasks/failures/count` 返回最近 24h 失败数
- App.vue onMounted 调用 + 每 60s 自动刷新
- badge 显示数字，进入「定时巡检」视图时也刷新
- 执行历史页失败行红色高亮 + 错误消息 tooltip

## 测试策略（TDD）

### 单元测试

| 文件 | 覆盖 |
|---|---|
| `internal/scheduler/schedule_test.go` | `computeNextRun`：各 preset + cron 边界（月末、闰年、跨年、跨时区） |
| `internal/scheduler/service_test.go` | CRUD + 非 admin 拒绝 + Trigger 不更新 next_run_at |
| `internal/store/scheduled_tasks_test.go` | SQL/Memory 双实现一致性、ListDueTasks 排序、CountRecentFailures |

### 集成测试

| 文件 | 覆盖 |
|---|---|
| `internal/scheduler/scheduler_test.go` | mock ReadOnlyService，验证 tickOnce：到期任务被执行 → 写 run → 写 audit → 更新 next_run_at；未到期任务不执行；执行失败也更新 next_run_at |
| `internal/httpapi/router_test.go` | 路由鉴权（非 admin 403）+ 创建 → 列表 → 触发 → 历史查询完整链路 |

### E2E 测试

`apps/capability-console/src/App.test.ts` 新增：
- 创建定时任务 → 列表显示 → 手动触发 → 历史页显示成功记录
- 失败时 badge 显示数字 → 进入视图后清除 badge
- 启停开关切换 → disabled 任务不出现在 due 列表

## 关键技术选型

- **cron 解析**：`github.com/robfig/cron/v3`（事实标准，支持 5 字段 + 描述符）
- **时区**：DB 存 UTC，调度按 task.timezone 计算；前端展示时按用户本地时区
- **并发**：tickOnce 内任务串行执行，避免并发冲爆后端 API；多任务到点时按 next_run_at 升序处理
- **幂等**：next_run_at 落 DB，重启后从断点恢复；同任务不会并发执行
- **审计**：复用 `audit.EventService.Record`，action=`scheduled_task_run`，metadata 包含 task_id 和 run_id

## 实现顺序

1. DB migration + store 接口 + Memory/SQL 双实现 + TDD
2. `schedule.go` computeNextRun 纯函数 + TDD（preset + cron 全覆盖）
3. `scheduler.go` Scheduler.tickOnce + TDD（mock ReadOnlyService）
4. `service.go` CRUD + Trigger + 鉴权 + TDD
5. `router.go` 路由 + RequireRole 中间件 + TDD
6. `cmd/copilot-api/main.go` Spawn scheduler
7. 前端 types.ts + api.ts 扩展 + TDD
8. 前端组件：SchedulePresetPicker + ScheduleCronInput + ScheduledTaskForm + ScheduledTaskList + ScheduledTaskRunHistory + ScheduledTaskBadge
9. App.vue 集成侧边栏入口 + 视图
10. E2E 测试 + 全量验证（go vet / go build / go test / vitest）
