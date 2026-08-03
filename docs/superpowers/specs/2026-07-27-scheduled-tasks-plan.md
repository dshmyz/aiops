# 管理员定时巡检任务 - 实现计划

- 关联 Spec：[2026-07-27-scheduled-tasks-design.md](./2026-07-27-scheduled-tasks-design.md)
- 实现方式：TDD（先写测试再实现）
- 节奏：每步完成后立即跑该步测试 + 全量 `go vet` / `go build` / `go test` 验证，再进入下一步

## 步骤总览

| # | 模块 | 层 | 测试先行 | 关键产物 |
|---|---|---|---|---|
| 1 | DB migration | DB | - | `migrations/005_scheduled_tasks.sql` |
| 2 | store 接口 + Memory 实现 | 后端 | ✓ | `internal/store/scheduled_tasks.go` + 测试 |
| 3 | SQL 实现 + 集成测试 | 后端 | ✓ | 同文件 SQL 分支 |
| 4 | `schedule.go` computeNextRun | 后端 | ✓ | `internal/scheduler/schedule.go` |
| 5 | `scheduler.go` tickOnce | 后端 | ✓ | `internal/scheduler/scheduler.go` |
| 6 | `service.go` CRUD + 鉴权 | 后端 | ✓ | `internal/scheduler/service.go` |
| 7 | router 路由 + RequireRole | 后端 | ✓ | `internal/httpapi/router.go` 扩展 |
| 8 | main.go Spawn scheduler | 后端 | - | `cmd/copilot-api/main.go` |
| 9 | 后端全量验证 | 后端 | - | `go vet` / `go build` / `go test ./...` |
| 10 | 前端 types + api | 前端 | ✓ | `types.ts` + `api.ts` 扩展 |
| 11 | 子组件 SchedulePresetPicker / ScheduleCronInput | 前端 | ✓ | `components/Schedule*.vue` |
| 12 | 容器组件 ScheduledTaskForm | 前端 | ✓ | `components/ScheduledTaskForm.vue` |
| 13 | 列表 + 历史 + badge 组件 | 前端 | ✓ | `components/ScheduledTask{List,RunHistory,Badge}.vue` |
| 14 | App.vue 集成 | 前端 | - | 侧边栏入口 + 视图切换 |
| 15 | E2E + 全量验证 | 全栈 | ✓ | `App.test.ts` + `vitest` + `tsc` |

---

## Step 1: DB migration

**文件**：`migrations/005_scheduled_tasks.sql`

按 spec 创建 `copilot_scheduled_tasks` 和 `copilot_scheduled_task_runs` 两张表。

**验证**：
- 本地 MySQL 执行通过
- `make dev-up` 重启后表已创建

---

## Step 2: store 接口 + Memory 实现（TDD）

**测试文件**：`internal/store/scheduled_tasks_test.go`

按 TDD 先写测试用例，覆盖：
- `CreateTask` 写入并返回完整字段
- `GetTask` 存在/不存在
- `GetTask` subject 不匹配返回 NotFound（隔离校验）
- `ListTasks` 按 subject 过滤
- `ListTasks` 按 enabled 过滤
- `ListTasks` Limit 控制
- `UpdateTask` 更新字段并刷新 UpdatedAt
- `DeleteTask` 删除后再 Get 返回 NotFound
- `ListDueTasks` 只返回 `enabled=true AND next_run_at <= now`，按 next_run_at 升序
- `AppendRun` 追加执行记录并返回完整 Run
- `ListRuns` 按 task_id 过滤、按 started_at 降序
- `CountRecentFailures` 统计 `status='failed' AND finished_at >= since`

**实现文件**：`internal/store/scheduled_tasks.go`

- 定义 `ScheduledTask` / `ScheduledTaskRun` / `ScheduledTaskFilter` / `ScheduledTaskStore` 接口
- `MemoryScheduledTaskStore` 用 `sync.Mutex` + map + slice 实现
- `NotFound` 错误复用现有 `store.ErrNotFound`（如有）或新建
- 时间字段统一用 `time.Time`

**验证**：`go test ./internal/store/...`

---

## Step 3: SQL 实现 + 集成测试（TDD）

**测试文件**：`internal/store/scheduled_tasks_sql_test.go`（或合并到 Step 2 测试文件）

- 用现有的 SQL 测试 harness（参考 `assistant_conversations_sql_test.go` 模式）
- 覆盖与 Memory 实现一致的用例（用表驱动测试共享 case）
- 验证 JSON 字段读写
- 验证 NULL 字段处理（preset/cron_expr/last_run_at/last_status）
- 验证索引被正确使用（`EXPLAIN` 查询计划，可选）

**实现**：在 `internal/store/scheduled_tasks.go` 增加 `SQLScheduledTaskStore`

**验证**：`go test ./internal/store/... -run ScheduledTask`

---

## Step 4: schedule.go computeNextRun（TDD）

**测试文件**：`internal/scheduler/schedule_test.go`

按 TDD 先写测试用例：

### preset 模式
- `5m`：当前 10:03 → 下次 10:05；当前 10:05 → 下次 10:10
- `1h`：当前 10:30 → 下次 11:00；当前 11:00 → 下次 12:00
- `daily`：当前 2026-07-27 10:00（Asia/Shanghai）→ 下次 2026-07-28 00:00（Asia/Shanghai）
- `weekly`：当前 2026-07-27 周日 → 下次 2026-07-28 周一 00:00

### cron 模式
- `0 2 * * 1-5`：当前 2026-07-27 周一 10:00 → 下次 2026-07-28 周二 02:00
- `*/5 * * * *`：当前 10:03 → 下次 10:05
- `0 0 1 * *`：当前 2026-07-27 → 下次 2026-08-01 00:00
- `0 0 29 2 *`：非闰年跳到次年 3 月（验证闰年处理）
- `@daily` 描述符：等价于 `0 0 * * *`

### 时区
- timezone=Asia/Shanghai，UTC 时间 2026-07-27 16:00 → 下次 daily = 2026-07-28 00:00 CST = 2026-07-27 16:00 UTC
- timezone 为空时按 UTC

### 错误场景
- 无效 cron 表达式 `0 25 * * *`（小时 > 23）→ 返回 error
- 无效 preset `invalid` → 返回 error
- kind 未知 → 返回 error

**实现文件**：`internal/scheduler/schedule.go`

```go
func computeNextRun(kind, preset, cronExpr, timezone string, now time.Time) (time.Time, error)
```

- preset 用 switch 手算
- cron 用 `github.com/robfig/cron/v3` 的 `cron.ParseStandard` + `cron.Next(time.Time)`
- 时区用 `time.LoadLocation`，失败时回退 UTC

**验证**：`go test ./internal/scheduler/... -run Schedule`

---

## Step 5: scheduler.go tickOnce（TDD）

**测试文件**：`internal/scheduler/scheduler_test.go`

按 TDD 先写测试用例：
- 一个到期任务 → 调 mock ReadOnlyService.ExecuteRead → 写 run（status=succeeded）→ 写 audit（action=scheduled_task_run, decision=permitted）→ 更新 task 的 last_run_at/last_status/next_run_at
- 任务执行返回 error → 写 run（status=failed, error=...）→ 写 audit（action=scheduled_task_run, decision=denied）→ 仍然更新 next_run_at（避免卡死）
- 多个到期任务 → 按 next_run_at 升序串行执行
- 未到期任务（next_run_at > now）→ 不执行
- enabled=false 的任务 → 不执行
- ExecuteRead panic → recover 并记为 failed（避免 scheduler 崩溃）

**Mock**：用 `fakeReadOnlyService` 实现 `ReadRunner` 接口（参考 `assistant/service_test.go` 的 fake 模式）

**实现文件**：`internal/scheduler/scheduler.go`

```go
type Scheduler struct {
    store        store.ScheduledTaskStore
    reads        *execution.ReadOnlyService
    auditService *audit.Service
    tickInterval time.Duration
    now          func() time.Time
}

func New(store store.ScheduledTaskStore, reads *execution.ReadOnlyService, audit *audit.Service, tick time.Duration, now func() time.Time) *Scheduler
func (s *Scheduler) Start(ctx context.Context)  // goroutine 入口
func (s *Scheduler) tickOnce(ctx context.Context) error  // 测试入口
func (s *Scheduler) executeTask(ctx context.Context, task store.ScheduledTask) (store.ScheduledTaskRun, error)
```

`executeTask` 复用 ReadOnlyService.ExecuteRead，传入 `identity.CurrentUser{Subject: "system:scheduler", Roles: []string{"scheduler"}}`。

**验证**：`go test ./internal/scheduler/... -run Scheduler`

---

## Step 6: service.go CRUD + 鉴权（TDD）

**测试文件**：`internal/scheduler/service_test.go`

按 TDD 先写测试用例：
- `Create` admin 用户 → 创建成功 + next_run_at 正确计算
- `Create` 非 admin 用户 → 返回 ErrForbidden
- `Create` preset=daily → next_run_at = 次日 00:00
- `Create` cron=`0 2 * * 1-5` → next_run_at = 下个工作日 02:00
- `Update` admin → 更新字段 + 重算 next_run_at（schedule 字段变化时）
- `Update` 非 admin → ErrForbidden
- `Update` 不存在的 task → ErrNotFound
- `Delete` admin → 删除
- `Delete` 非 admin → ErrForbidden
- `Get` 任意登录用户 → 返回详情
- `List` 任意登录用户 → 返回列表
- `Trigger` admin → 立即执行一次 + 写 run + 不更新 next_run_at
- `Trigger` 非 admin → ErrForbidden
- `ListRuns` 任意用户 → 返回历史
- `CountRecentFailures` → 返回 24h 内失败数

**实现文件**：`internal/scheduler/service.go`

```go
type Service struct {
    store   store.ScheduledTaskStore
    runner  *execution.ReadOnlyService
    audit   *audit.Service
    now     func() time.Time
}

func NewService(store store.ScheduledTaskStore, runner *execution.ReadOnlyService, audit *audit.Service, now func() time.Time) *Service

func (s *Service) Create(ctx context.Context, user identity.CurrentUser, req CreateRequest) (store.ScheduledTask, error)
func (s *Service) Update(ctx context.Context, user identity.CurrentUser, id string, req UpdateRequest) (store.ScheduledTask, error)
func (s *Service) Delete(ctx context.Context, user identity.CurrentUser, id string) error
func (s *Service) Get(ctx context.Context, user identity.CurrentUser, id string) (store.ScheduledTask, error)
func (s *Service) List(ctx context.Context, user identity.CurrentUser, filter store.ScheduledTaskFilter) ([]store.ScheduledTask, error)
func (s *Service) Trigger(ctx context.Context, user identity.CurrentUser, id string) (store.ScheduledTaskRun, error)
func (s *Service) ListRuns(ctx context.Context, user identity.CurrentUser, taskID string, limit int) ([]store.ScheduledTaskRun, error)
func (s *Service) CountRecentFailures(ctx context.Context, since time.Time) (int, error)
```

`requireAdmin(user)` 内部方法：`slices.Contains(user.Roles, "admin")`。

`Trigger` 内部复用 `Scheduler.executeTask` 逻辑（避免重复），但传一个 `updateNextRun=false` 标志。

**验证**：`go test ./internal/scheduler/... -run Service`

---

## Step 7: router 路由 + RequireRole 中间件（TDD）

**测试文件**：`internal/httpapi/router_test.go` 扩展

按 TDD 先写测试用例：
- `POST /v1/scheduled-tasks` admin → 201 + 创建成功 + 调用 service.Create
- `POST /v1/scheduled-tasks` 非 admin → 403
- `POST /v1/scheduled-tasks` 请求体缺 name → 400
- `POST /v1/scheduled-tasks` schedule_kind=cron 但 cron_expr 无效 → 400
- `GET /v1/scheduled-tasks` 任意用户 → 200 + 列表
- `GET /v1/scheduled-tasks/{id}` 存在 → 200；不存在 → 404
- `PATCH /v1/scheduled-tasks/{id}` admin → 200；非 admin → 403
- `DELETE /v1/scheduled-tasks/{id}` admin → 204；非 admin → 403
- `POST /v1/scheduled-tasks/{id}/run` admin → 200 + 返回 run
- `GET /v1/scheduled-tasks/{id}/runs` → 200 + 历史列表
- `GET /v1/scheduled-tasks/failures/count` → 200 + {count: N}
- 未注入 service 时 → 503（与其他可选路由一致）

**实现**：`internal/httpapi/router.go` 扩展

```go
func RequireRole(role string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            user, ok := identity.FromContext(r.Context())
            if !ok || !slices.Contains(user.Roles, role) {
                writeError(w, http.StatusForbidden, "forbidden")
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

`Router` 新增 `scheduledTasks *scheduler.Service` 字段 + `WithScheduledTasks(svc)` Option。

路由注册：
```go
mux.Handle("POST /v1/scheduled-tasks", r.RequireRole("admin")(r.handleCreateScheduledTask))
mux.Handle("GET /v1/scheduled-tasks", r.Auth(r.handleListScheduledTasks))
mux.Handle("GET /v1/scheduled-tasks/failures/count", r.Auth(r.handleCountScheduledTaskFailures))
mux.Handle("GET /v1/scheduled-tasks/{id}", r.Auth(r.handleGetScheduledTask))
mux.Handle("PATCH /v1/scheduled-tasks/{id}", r.RequireRole("admin")(r.handleUpdateScheduledTask))
mux.Handle("DELETE /v1/scheduled-tasks/{id}", r.RequireRole("admin")(r.handleDeleteScheduledTask))
mux.Handle("POST /v1/scheduled-tasks/{id}/run", r.RequireRole("admin")(r.handleTriggerScheduledTask))
mux.Handle("GET /v1/scheduled-tasks/{id}/runs", r.Auth(r.handleListScheduledTaskRuns))
```

注意 `failures/count` 要在 `{id}` 之前注册（避免路径冲突）。

**验证**：`go test ./internal/httpapi/... -run ScheduledTask`

---

## Step 8: main.go Spawn scheduler

**文件**：`cmd/copilot-api/main.go`

```go
scheduledTaskStore := store.NewSQLScheduledTaskStore(db)
scheduledTaskService := scheduler.NewService(scheduledTaskStore, reads, auditService, time.Now)
router.WithScheduledTasks(scheduledTaskService)

schedulerInstance := scheduler.New(scheduledTaskStore, reads, auditService, 60*time.Second, time.Now)
go schedulerInstance.Start(ctx)
```

`ctx` 用应用级 context，进程收到 SIGTERM 时 cancel，让 scheduler 在当前 tick 完成后退出。

**验证**：`go build ./cmd/copilot-api`

---

## Step 9: 后端全量验证

```bash
go vet ./...
go build ./...
go test ./...
```

全绿后进入前端。

---

## Step 10: 前端 types + api（TDD）

**测试文件**：`apps/capability-console/src/api.test.ts` 扩展

按 TDD 先写测试用例：
- `listScheduledTasks()` → GET /v1/scheduled-tasks
- `listScheduledTasks({ enabled: true })` → GET /v1/scheduled-tasks?enabled=true
- `getScheduledTask(id)` → GET /v1/scheduled-tasks/{id}
- `createScheduledTask(payload)` → POST /v1/scheduled-tasks
- `updateScheduledTask(id, payload)` → PATCH /v1/scheduled-tasks/{id}
- `deleteScheduledTask(id)` → DELETE /v1/scheduled-tasks/{id}
- `triggerScheduledTask(id)` → POST /v1/scheduled-tasks/{id}/run
- `listScheduledTaskRuns(id, 10)` → GET /v1/scheduled-tasks/{id}/runs?limit=10
- `countScheduledTaskFailures()` → GET /v1/scheduled-tasks/failures/count

**实现**：
- `apps/capability-console/src/types.ts` 扩展 `ScheduledTask` / `ScheduledTaskRun` / `CreateScheduledTaskPayload` / `UpdateScheduledTaskPayload` 类型
- `apps/capability-console/src/api.ts` 扩展上述 8 个函数

**验证**：`npx vitest run src/api.test.ts`

---

## Step 11: 子组件 SchedulePresetPicker + ScheduleCronInput（TDD）

### SchedulePresetPicker.vue

**测试**：`components/SchedulePresetPicker.test.ts`
- 渲染 4 个 preset 选项（5m / 1h / daily / weekly）
- 当前选中值正确高亮
- 点击新选项 → emit `update:modelValue` 事件
- 每个选项显示中文标签 + 频率说明

**实现**：单选按钮组 + 描述文字。

### ScheduleCronInput.vue

**测试**：`components/ScheduleCronInput.test.ts`
- 输入框绑定 cron 表达式
- 输入合法表达式 → 显示"下次执行时间"预览（调用后端 API 或前端纯算？前端纯算用 cron 库）
- 输入非法表达式 → 显示红色错误提示
- emit `update:modelValue` + `valid` 状态

**实现**：textarea + 实时校验。前端用 `cron-parser` npm 包做校验和下次时间预览，避免每次按键都打后端。

**验证**：`npx vitest run components/SchedulePresetPicker.test.ts components/ScheduleCronInput.test.ts`

---

## Step 12: 容器组件 ScheduledTaskForm（TDD）

**测试**：`components/ScheduledTaskForm.test.ts`
- 创建模式：所有字段为空 + 提交按钮 disabled
- 创建模式：填齐必填字段 + 提交 → emit `submit` 事件，payload 正确
- 编辑模式：传入 `task` prop → 字段预填
- 编辑模式：修改字段 → emit `submit`，payload 含 id + 修改字段
- capability 选择从 `/v1/capabilities` 拉取的只读 capability 列表
- schedule_kind 切换：preset ↔ cron，对应子组件切换
- 取消按钮 → emit `cancel`
- 表单校验：name 空 / capability 空 / cron 模式但 cron_expr 非法 → 提交按钮 disabled

**实现**：`components/ScheduledTaskForm.vue`
- props: `task?: ScheduledTask`、`capabilities: ManagedCapability[]`
- emits: `submit`、`cancel`
- 内部状态：name / capability_name / input (JSON 文本) / schedule_kind / preset / cron_expr
- 根据 schedule_kind 渲染 SchedulePresetPicker 或 ScheduleCronInput

**验证**：`npx vitest run components/ScheduledTaskForm.test.ts`

---

## Step 13: 列表 + 历史 + badge 组件（TDD）

### ScheduledTaskList.vue

**测试**：`components/ScheduledTaskList.test.ts`
- 渲染任务列表（name / capability / 下次执行 / 上次状态）
- enabled 开关：点击 → emit `toggle-enabled`
- 「立即运行」按钮 → emit `trigger`
- 「编辑」按钮 → emit `edit`
- 「删除」按钮 → emit `delete`
- 上次状态 succeeded 显示绿色，failed 显示红色
- 空列表显示「暂无定时任务」

### ScheduledTaskRunHistory.vue

**测试**：`components/ScheduledTaskRunHistory.test.ts`
- 渲染执行历史列表（开始时间 / 状态 / 耗时 / 结果摘要）
- failed 行红色高亮 + 错误消息 tooltip
- 点击行 → 展开详细 result_data（JSON）
- 空历史显示「暂无执行记录」
- 分页：加载更多按钮

### ScheduledTaskBadge.vue

**测试**：`components/ScheduledTaskBadge.test.ts`
- count=0 → 不显示
- count=3 → 显示"3"
- count>99 → 显示"99+"

**验证**：`npx vitest run components/ScheduledTask{List,RunHistory,Badge}.test.ts`

---

## Step 14: App.vue 集成

**文件**：`apps/capability-console/src/App.vue`

- 新增 `ActiveView` 类型扩展 `'scheduled-tasks'`
- 新增状态：`scheduledTasks`、`scheduledTaskRuns`、`scheduledTaskFailures`、`scheduledTaskFormOpen`、`scheduledTaskEditing`
- 新增方法：`refreshScheduledTasks()`、`refreshScheduledTaskFailures()`、`openScheduledTaskForm()`、`editScheduledTask(task)`、`saveScheduledTask(payload)`、`deleteScheduledTask(id)`、`triggerScheduledTask(id)`、`toggleScheduledTaskEnabled(task)`
- 侧边栏新增「定时巡检」入口 + `<ScheduledTaskBadge>`
- onMounted 调用 `refreshScheduledTaskFailures()`，每 60s 自动刷新（用 `setInterval`）
- 主视图区新增 `<section v-if="activeView === 'scheduled-tasks'">`，渲染列表 + 表单 + 历史

---

## Step 15: E2E + 全量验证

**文件**：`apps/capability-console/src/App.test.ts` 扩展

新增测试用例：
- 管理员创建定时任务 → 列表显示 → 手动触发 → 历史页显示成功记录
- 失败任务 → 侧边栏 badge 显示数字 → 进入视图后清除
- 启停开关切换 → 状态正确反映
- 非 admin 用户访问 → UI 隐藏创建/编辑/删除按钮（mock 用户 roles）
- capability 列表只显示只读 capability
- cron 表达式非法时禁用提交按钮

**全量验证**：
```bash
cd /Users/gracegaoya/Documents/New\ project
go vet ./...
go build ./...
go test ./...
cd apps/capability-console
npx vue-tsc --noEmit
npx vitest run
```

全绿后完成。

---

## 注意事项

1. **依赖管理**：`robfig/cron/v3` 和前端 `cron-parser` 需在 Step 4 / Step 11 前 `go mod tidy` / `npm install`
2. **错误处理**：复用 `internal/store` 现有的 `ErrNotFound`；新增 `ErrForbidden`
3. **测试隔离**：SQL 测试用现有 testdb harness，每个用例独立事务回滚
4. **配置**：管理员 role 名约定为 `admin`（与 identity 模块一致）
5. **审计 action**：新增 `scheduled_task_run` 常量到 `internal/audit/enums.go`
6. **时钟注入**：所有时间相关逻辑用 `now func() time.Time` 注入，便于测试
