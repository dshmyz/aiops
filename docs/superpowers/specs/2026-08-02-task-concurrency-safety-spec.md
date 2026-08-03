# 任务准 - 并发安全专项 spec

**状态**：已完成（2026-08-02，全量回归零回归）
**创建时间**：2026-08-02
**来源**：[2026-08-02-sxdevops-aiops-gap-analysis.md](./2026-08-02-sxdevops-aiops-gap-analysis.md) 阶段 1 专项项
**阻塞**：多实例部署（生产高可用/扩容）

## 1. 问题陈述

### 1.1 现状

当前 scheduler 单实例运行时正常，但**多实例部署会出三类问题**：

1. **重复执行**：两个实例同时 `ListDueTasks` 拿到同一批到期任务，各自执行一遍 → 同一任务同一周期被执行两次
2. **补跑风暴**：进程宕机一段时间后重启，`ListDueTasks` 一次性返回所有积压到期任务，全部立即执行 → 瞬时流量冲击下游 capability
3. **数据竞争**：`AppendRun` + `UpdateTask` 非原子，并发执行同一 task 时 `last_run_at` / `last_status` / `next_run_at` 互相覆盖，状态不一致

### 1.2 当前代码的具体问题点

| 问题 | 位置 | 根因 |
|---|---|---|
| ListDueTasks 无锁 | [scheduled_tasks.go:408-434](file:///Users/gracegaoya/Documents/New%20project/internal/store/scheduled_tasks.go#L408-434) | 普通 `SELECT ... WHERE next_run_at <= ?`，多实例同时读到同一批 |
| UpdateTask 无版本号 | [scheduled_tasks.go:365-391](file:///Users/gracegaoya/Documents/New%20project/internal/store/scheduled_tasks.go#L365-391) | `UPDATE ... WHERE id = ? AND subject = ?`，无乐观锁，后写覆盖前写 |
| AppendRun + UpdateTask 非原子 | [scheduler.go:197-216](file:///Users/gracegaoya/Documents/New%20project/internal/scheduler/scheduler.go#L197-216) | 两个独立 DB 调用，中间崩溃会留下 run 无对应 task 状态更新 |
| ListDueTasks 无 limit 默认值 | [scheduler.go:133](file:///Users/gracegaoya/Documents/New%20project/internal/scheduler/scheduler.go#L133) | `ListDueTasks(ctx, now, 0)` 传 0 = 无限制，积压时全量返回 |
| 无补跑过期保护 | scheduler.go 全文 | 任务到期超过一定时间（如 1 小时）仍会执行，可能执行已过时的巡检 |

### 1.3 不在本次范围内

- 分布式锁的跨数据中心一致性（假设单 MySQL 实例，单数据中心）
- 调度器的 leader election（多实例只有一个跑调度，是另一种方案，但本次选"多实例都跑+DB锁防重"更简单）
- capability 执行本身的幂等性（capability 层职责，非 scheduler 层）

## 2. 设计方案

### 2.1 核心策略：DB 乐观锁 + 原子事务 + 补跑保护

不用 Redis 分布式锁（引入新依赖），用 MySQL 已有的行锁 + 乐观锁机制：

```
方案对比：
A. SELECT ... FOR UPDATE（悲观锁）  — 持锁时间长（整个 capability 执行期间），阻塞其他实例
B. 乐观锁 CAS 更新 next_run_at      — 抢占式认领，执行不持锁，失败者跳过 ✅ 选这个
C. Redis 分布式锁                   — 引入新依赖，过度设计
```

### 2.2 抢占式认领机制（解决问题 1：重复执行）

在 `ListDueTasks` 后、`executeTask` 前，用 CAS 更新 `next_run_at` 抢占任务：

```sql
-- 原子认领：把 next_run_at 推进到下一个周期，只有影响的行数=1 的实例获得执行权
UPDATE copilot_scheduled_tasks
SET next_run_at = ?,          -- 推进到下个周期
    last_run_at = ?,          -- 记录本次执行时间
    last_status = 'running',  -- 标记执行中
    updated_at = ?
WHERE id = ? AND next_run_at = ?  -- CAS：只有 next_run_at 仍是原值时才能更新
```

- 多个实例同时执行这条 UPDATE，只有一个会 `affected_rows = 1`，其他 `affected_rows = 0`
- `affected_rows = 0` 的实例跳过该任务（已被其他实例认领）
- `affected_rows = 1` 的实例获得执行权，开始执行 capability

**新增 store 方法**：`ClaimTask(ctx, taskID, expectedNextRunAt, newNextRunAt, now) (bool, error)`

### 2.3 原子事务（解决问题 3：数据竞争）

把 `AppendRun` + `UpdateTask` 合并成一个事务方法 `AppendRunAndUpdateTask`：

```go
// AppendRunAndUpdateTask 在单个事务内追加 run 记录并更新 task 状态。
// 任意一步失败整体回滚，避免 run 存在但 task 状态未更新的不一致。
func (s *SQLScheduledTaskStore) AppendRunAndUpdateTask(ctx context.Context, run ScheduledTaskRun, task ScheduledTask) error {
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil { return err }
    defer func() { _ = tx.Rollback() }()  // 已提交时 Rollback 是 no-op
    
    // 1. INSERT run
    if _, err := tx.ExecContext(ctx, `INSERT INTO copilot_scheduled_task_runs ...`, ...); err != nil {
        return err
    }
    // 2. UPDATE task（含 CAS 乐观锁）
    result, err := tx.ExecContext(ctx, `UPDATE copilot_scheduled_tasks SET ... WHERE id = ? AND updated_at = ?`, ...)
    if err != nil { return err }
    if affected, _ := result.RowsAffected(); affected == 0 {
        return ErrConcurrentModification  // 并发冲突，其他实例已修改
    }
    
    return tx.Commit()
}
```

**新增 store 方法**：`AppendRunAndUpdateTask(ctx, run, task, expectedUpdatedAt) error`
**新增错误**：`ErrConcurrentModification`（CAS 失败时返回）

### 2.4 补跑保护（解决问题 2：补跑风暴 + 过期执行）

两层保护：

**a) ListDueTasks 加默认 limit**：
- scheduler 调用 `ListDueTasks(ctx, now, 100)` 而非 `0`（无限制）
- 每轮最多处理 100 个任务，多余的等下一轮（1 分钟后）

**b) 过期跳过**：
- 任务到期超过 `maxLag`（默认 1 小时）则跳过执行，只记录 audit
- 避免执行"昨天就该跑但宕机到现在"的任务（数据已过时）

```go
const maxLag = time.Hour  // 到期超过 1 小时的任务跳过

if task.NextRunAt.Before(now.Add(-maxLag)) {
    // 过期跳过：只推进 next_run_at + 记 audit，不执行 capability
    recordAudit(ctx, auditService, ..., audit.ActionScheduledTaskSkipped, ...)
    // CAS 推进 next_run_at
    return
}
```

**新增 audit action**：`scheduled_task_skipped`

### 2.5 流程整合

新的 `executeAndRecord` 流程：

```
1. ListDueTasks(now, limit=100)
2. 对每个 task：
   a. 检查过期：NextRunAt < now - maxLag → 记 audit skipped + CAS 推进 next_run_at，跳过
   b. CAS 认领：ClaimTask(taskID, expectedNextRunAt=task.NextRunAt, newNextRunAt=下个周期)
      - 失败（affected=0）：已被其他实例认领，跳过
      - 成功：获得执行权
   c. 执行 capability（ExecuteTrustedRead）
   d. AppendRunAndUpdateTask(run, task, expectedUpdatedAt)  // 原子事务
      - ErrConcurrentModification：记 warning，不重试（其他实例已介入）
```

## 3. 模块划分与改动点

### 3.1 store 层（[internal/store/scheduled_tasks.go](file:///Users/gracegaoya/Documents/New%20project/internal/store/scheduled_tasks.go)）

| 改动 | 类型 | 说明 |
|---|---|---|
| `ClaimTask(ctx, taskID, expectedNextRunAt, newNextRunAt, now) (bool, error)` | 新增方法 | 原子 CAS 认领，返回是否抢到 |
| `AppendRunAndUpdateTask(ctx, run, task, expectedUpdatedAt) error` | 新增方法 | 事务内 AppendRun + UpdateTask |
| `ErrConcurrentModification` | 新增错误 | CAS 失败时返回 |
| `ScheduledTaskStore` 接口 | 扩展 | 加上述两个方法 |
| `MemoryScheduledTaskStore` | 实现 | 内存版实现（测试用，用 mutex 模拟） |
| `SQLScheduledTaskStore` | 实现 | MySQL 版实现（真实事务 + CAS） |
| `ScheduledTask.UpdatedAt` | 现有 | 复用作为乐观锁版本号（无需新增 version 字段） |

### 3.2 audit 层（[internal/audit/enums.go](file:///Users/gracegaoya/Documents/New%20project/internal/audit/enums.go)）

| 改动 | 类型 | 说明 |
|---|---|---|
| `ActionScheduledTaskSkipped = "scheduled_task_skipped"` | 新增枚举 | 过期跳过事件 |
| `allowedActions` map | 扩展 | 加入新 action |

### 3.3 scheduler 层（[internal/scheduler/scheduler.go](file:///Users/gracegaoya/Documents/New%20project/internal/scheduler/scheduler.go)）

| 改动 | 类型 | 说明 |
|---|---|---|
| `maxLag` 常量 | 新增 | 过期跳过阈值，默认 1h |
| `executeAndRecord` | 重构 | 加入过期检查 + CAS 认领 + 原子事务 |
| `tickOnce` | 改动 | `ListDueTasks` 传 limit=100 |
| `claimTask` 辅助函数 | 新增 | 封装 CAS 认领逻辑 |
| `handleConcurrentModification` | 新增 | 处理 CAS 冲突（记 warning，不重试） |

### 3.4 不涉及的层

- HTTP API：无改动（Trigger 走相同的 executeAndRecord，自动受益）
- 前端：无改动
- migration：**无新表无新字段**（复用 updated_at 做乐观锁，复用 next_run_at 做 CAS）

## 4. TDD 推进计划

### 4.1 第一批：store 层（红灯→绿灯）

| 测试 | 验证点 |
|---|---|
| `TestClaimTaskSucceedsWhenNextRunAtMatches` | CAS 匹配时认领成功，返回 true |
| `TestClaimTaskFailsWhenNextRunAtAlreadyAdvanced` | CAS 不匹配（被其他实例认领）时返回 false |
| `TestAppendRunAndUpdateTaskAtomicSuccess` | 事务成功：run 和 task 都更新 |
| `TestAppendRunAndUpdateTaskRollbackOnTaskConflict` | task CAS 失败时 run 也回滚 |
| `TestMemoryStoreClaimTaskConcurrent` | 内存版并发模拟：两个 goroutine 同时 Claim，只有一个成功 |

### 4.2 第二批：scheduler 层（红灯→绿灯）

| 测试 | 验证点 |
|---|---|
| `TestTickOnceSkipsExpiredTask` | NextRunAt < now - maxLag 时跳过执行，记 skipped audit |
| `TestTickOnceClaimsTaskBeforeExecute` | 认领成功才执行 capability |
| `TestTickOnceSkipsTaskAlreadyClaimed` | ClaimTask 返回 false 时不执行 |
| `TestTickOnceRespectsListLimit` | ListDueTasks 传 limit=100 |
| `TestExecuteAndRecordHandlesConcurrentModification` | AppendRunAndUpdateTask 返回 ErrConcurrentModification 时记 warning 不重试 |
| `TestTriggerAlsoUsesClaimAndAtomicUpdate` | 手动 Trigger 也走新流程（不 CAS 认领，但用原子事务） |

### 4.3 第三批：回归测试

- `go test ./...` 全量回归
- 重点验证：单实例场景行为不变（所有现有测试仍通过）

## 5. 风险与约束

### 5.1 已知风险

| 风险 | 缓解 |
|---|---|
| ClaimTask 和执行之间实例崩溃 → 任务被认领但未执行 | next_run_at 已推进，下个周期会再执行；可接受（少跑一次比重复跑好） |
| maxLag=1h 可能误跳过合法的长间隔任务 | 任务预设周期通常 ≤1h，1h 间隔以上的任务可配置 maxLag |
| AppendRunAndUpdateTask 的 expectedUpdatedAt 需正确传入 | scheduler 在 ClaimTask 时拿到 task 快照，用其 UpdatedAt |

### 5.2 设计约束

- **不引入 Redis 依赖**：用 MySQL 事务 + CAS，保持依赖栈简单
- **不新增 migration**：复用现有字段（updated_at 做乐观锁，next_run_at 做 CAS）
- **单实例行为不变**：所有现有测试必须通过，不破坏现有语义
- **手动 Trigger 不 CAS 认领**：Trigger 是 admin 显式触发，不与 scheduler 抢占，但用原子事务保证一致性

## 6. 验收标准

1. `go test ./...` 全绿，零回归
2. 并发测试：模拟两实例同时 tickOnce，同一任务只执行一次
3. 过期测试：NextRunAt 超过 maxLag 的任务被跳过并记 audit
4. 原子性测试：AppendRunAndUpdateTask 中间失败时 run 和 task 都不写入
5. 单实例回归：所有现有 scheduler/store 测试通过

## 7. 推进节奏

- 阶段 A：store 层 TDD（ClaimTask + AppendRunAndUpdateTask + 内存版并发测试）
- 阶段 B：scheduler 层 TDD（过期跳过 + CAS 认领 + 并发冲突处理）
- 阶段 C：全量回归 + 验收
