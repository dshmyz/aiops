# 运维总览 + 渐进自治（D + E）设计

日期：2026-08-07
状态：已批准（设计评审通过）· 三阶段均已实现并交付

## 1. 目标与范围

把「让 AI 更自治（E）」与「运维总览 dashboard（D）」作为**一次设计、两层实现**落地：

- **E1 半自动（降低人工摩擦，无自动执行）**——dashboard 承接「待确认 plan 一键确认/拒绝/重试」
  操作面，让运营者不必轮询、不必另开页面。
- **E2 自动执行（真正的「自治」，有明确风险边界）**——统一一个**低风险准入门**
  （Low-Risk Admission Controller），让三类自动执行源（直接对话低风险 runbook / 定时触发低风险
  runbook / agent loop 内低风险写）共用一组准入语义，且**默认关闭**。
- **D（可见层）**——以此前各视图已有的数据为基础，做一屏静态聚合，把 E1 操作面与 E2 的
  自动执行状态呈现给运营者。

本设计**显式排除**（YAGNI）：
- **条件触发**（如「磁盘 >80% 触发」）——需要事件源 + 规则引擎，超出范围，记为后续可扩展点。
- 定时触发**任意写工具**——定时任务只能触发**预先评审过的低风险 runbook 模板**，绝不接受
  「定时执行任意 tool + input」。
- D 造大而全的聚合端点——尽量复用现有端点，前端做薄组合。

## 2. 现状（代码实况，非文档）

### 2.1 前端无状态 + 无登录（背景约束）

前端 `apps/capability-console` 无状态：`api.ts` 的 `request()` 不携带 `Authorization`。后端
`MultiAuthenticator` 是 jwt 模式，强制 `Authorization: Bearer <JWT>`，且 jwt 模式**无登录端点**。
无 nginx / 未启用反代时，浏览器直连单二进制托管的 SPA 会 401；本地开发用 `COPILOT_DEV_INJECT_ADMIN=1`
后端开关解决（见 `docs/OPERATIONS.md` §8.4）。

> 本设计的所有交互（确认/拒绝/触发）都经由现有 JWT / dev-inject 身份路径，不改变认证模型。

### 2.2 E2 现状：低风险 runbook 自动执行是「无条件」的

读代码确认存在**两类不对称**的写行为：

- **直接执行路径**（`internal/assistant/service.go` ~L748）：`runbookRisk == "low"` 且 `s.execution != nil`
  时，低风险 runbook **无条件自动执行**——创建已确认 plan → 立即执行 → 返回 `execution_result`。
  **没有总开关 / opt-in / 门槛**。
- **自治 agent loop**（`internal/assistant/service_agent.go` `agentWriteStep`）：写操作**永远**
  创建 pending plan → `StepExecutive`，**从不自动执行**（即使低风险）。印证文档「loop 内从不自动执行写」。

**结论**：同一个「低风险 runbook」在直接对话能自动写系统，在 agent loop 里却完全不能；两条路径都
没有一个**统一、显式、可审计**的低风险准入门。设计 E2 的第一步就是把这个裂缝收敛成一个准入门。

### 2.3 已有可复用端点（D 的数据基础）

- `GET /v1/action-plans`、`POST /v1/action-plans/{id}/confirm`（确认+执行一步到位）
- `GET /v1/scheduled-tasks`、`GET /v1/scheduled-tasks/{id}/runs`、`POST .../run`
- `GET /v1/inspection-reports`、`GET /v1/executions`、`GET /v1/audit-events`
- `GET /v1/incidents`（告警全景，Phase 1 已做）、`GET /v1/identity/me`

`POST /v1/action-plans/{id}/confirm` 已存在；**显式 reject / deny 端点不存在**——pending plan 目前只能
等过期（`plan_rejected` 审计只在 confirmation token 非法时出现）。

### 2.4 定时巡检是「只读」设计

`internal/scheduler/service.go` 的 `CreateRequest` 指向 `CapabilityName`（只读 capability），到期只调
只读执行。从「只读」扩展到「写（低风险 runbook）」是**新增目标类型**，须区分 `run_kind`。

## 3. 架构分层

```
┌──────────────────────────────────────────────────┐
│ D: 运维总览 dashboard（操作面 / 可见层）            │
│   待确认 plan 一键确认/拒绝 · 统计卡 · 各源状态     │
├──────────────────────────────────────────────────┤
│ E1 半自动（无自动执行）                             │
│   dashboard 承接确认/拒绝操作面，复用既有 confirm    │
├──────────────────────────────────────────────────┤
│ E2 自动执行（显式 opt-in + 统一准入门）             │
│   Low-Risk Admission Controller                   │
│   ├ ① 直接对话：低风险 runbook（现状自动→收回）      │
│   ├ ② 定时/条件：cron 触发低风险 runbook            │
│   └ ③ agent loop：低风险写（当前完全禁止）           │
└──────────────────────────────────────────────────┘
```

- **E1 并入 D**（数据平面复用），无自动执行，风险只来自「按钮位置 + 权限正确」。
- **E2 独立**：机器自己写系统，需要独立的准入 + 默认 fail-closed。
- **实现分期**：先 E1/D（低风险、可立即交付），后 E2（自动执行，需准入 + 开关）。

## 4. E1 + D 设计

### 4.1 新增后端能力（极少量）

**`POST /v1/action-plans/{id}/reject`（新增）**
- 把 pending plan 过渡到终止态（新增 `plan_rejected` 状态或复用“拒绝即终态”），写
  `plan_rejected` 审计事件（含 `expected_version` 乐观锁，避免并发确认/拒绝竞态）。
- 权限/策略门与 `confirm` 一致（需 `RequiresConfirmation`）；非法链返回 404/409。
- 幂等：已 reject 的 plan 再 reject 返回既有结果或 409。

**`GET /v1/overview`（新增，轻量聚合）**
- 只回**顶部统计卡计数**，一次 round-trip：
  - 待确认 plan 数（pending）
  - 今日执行成功 / 失败数
  - 活动告警数
  - 运行中的定时任务数
- 卡片以下是前端 fan-out 到各既有列表端点，**overview 不做深度聚合、不重复造详情数据**。
- 计数按 `/v1/identity/me` 角色裁剪：admin 可见执行数；viewer/operator 隐藏 admin 数字化。

### 4.2 Dashboard 视图（静态首屏，无新路由库）

```
┌────────────────────────────────────────────────┐
│  运维总览                                      │
│  [待确认 3] [今日执行 ✔5 ✗1] [活动告警 2] [任务 8] │  ← GET /v1/overview
├────────────────────────────────────────────────┤
│  待确认计划 (Top 5, 过期高亮)  ← E1 操作面        │
│    规划摘要 · 风险徽标 · [确认] [拒绝]           │
├────────────────────────────────────────────────┤
│  活动告警 (最新 5 → 告警全景)                    │
├────────────────────────────────────────────────┤
│  最近执行 (最新 5 → 执行历史, admin)             │
└────────────────────────────────────────────────┘
```

- 待确认卡片即 E1 的确认/拒绝操作面：确认复用现有 `confirm`，拒绝走新增 `reject`。
- 每张卡「查看更多」跳对应既有视图，不重造详情页。
- E2 上线后，顶部统计卡预留「自动执行 N 次，来源分布」展示位。

### 4.3 测试
- 后端：reject 单测（终态 + 审计写、非法链 404/409、权限门、并发乐观锁）；overview 计数单测 + 角色裁剪。
- 前端：DashboardView 组合查询、reject/confirm 调用、统计卡渲染（vitest）。

## 5. E2 设计：Low-Risk Admission Controller

### 5.1 准入门（所有自动执行源共用）

```
自动执行触发（3 类）
  ① 直接对话：低风险 runbook 命中
  ② 定时触发：cron 命中的低风险 runbook
  ③ agent loop：低风险写
         │
         ▼
  ┌─ Low-Risk Admission Controller ───────────────┐
  │  ① 总开关 COPILOT_AUTONOMY_ENABLED（默认 0）   │  ← 一票否决
  │  ② 策略判低风险 (role+env+tool+input+risk)     │  ← 复用 policy.Evaluate
  │  ③ 工具白名单（仅风险显式标 low 的工具）        │
  │  ④ 速率/每日上限（防刷）                       │
  │  ⑤ 每次自动执行强制 dry-run 预演通过            │
  │  ⑥ 全程审计 + 通知（非静默）                   │
  └──────────────────────────────────────────────┘
               │ 全过 → 执行（已确认 plan 语义）
```

**语义要点：**
- **收回现状**：当前「直接对话低风险 runbook 无条件自动执行」改为**必须翻转 `COPILOT_AUTONOMY_ENABLED`
  才生效**（默认 fail-closed）。这是有意为之——无门槛自动写系统不该是默认。
- **复用 `policy.Evaluate`**，不新造风险判定；低风险 = `riskRank == low` 且工具在白名单。
- **每次强制 dry-run** 复核通过（复用 `previewWritePlan`）。
- **审计**：每次自动执行的执行记录带 `autonomy_source`（direct / scheduler / agent-loop）+ `admission`
  命中条件 + `auto` 标记，全程可回溯。
- **通知**：自动执行结果推 Feishu（复用 `COPILOT_FEISHU_WEBHOOK_URL`），不静默。

### 5.2 三类源
- **① 直接对话**：现有 `service.go` 低风险 runbook 路径收回为经 API，默认关。
- **② 定时触发**：见 §5.3。
- **③ agent loop**：`agentWriteStep` 对低风险写，在准入门过时改为自动执行（当前硬禁止保留为默认）。

### 5.3 定时触发（cron 低风险 runbook）

扩展 `internal/scheduler/service.go`，复用其 cron/preset/timezone/`next_run` 计算与 admin 权限模型，
新增 `run_kind` 区分只读 vs 写触发：

```
run_kind: "read"（现状）          |  run_kind: "runbook"（新增）
  → reads.ExecuteRead（只读）     |    → 命中低风险 runbook 模板
                                  |    → 过 Low-Risk Admission →
                                  |       创建已确认 plan → 执行
```

- 定时写触发**只做低风险 runbook 自动执行**，不接受「定时执行任意 tool + input」（防无人值守写后门）。
- **条件触发不做**（YAGNI），记为后续可扩展点。

## 6. 环境变量 / 配置

| 变量 | 默认 | 说明 |
|------|------|------|
| `COPILOT_AUTONOMY_ENABLED` | `0` | E2 自动执行总开关；`0`/未设 = 所有自动执行禁用（fail-closed）。生产必须为 0 |
| `COPILOT_AUTONOMY_DAILY_LIMIT` | `100` | 每日自动执行上限（防刷），`0` = 不限制（不建议生产使用） |

对应更新 `docs/OPERATIONS.md`（§3.1 配置表 / §3.3 生产安全 / 上线清单 / §10 已知限制）与 `.env.example`。

## 7. 文档 / 合规

- `COPILOT_AUTONOMY_ENABLED` 必须 fail-closed，生产为 0/未设。
- reject 端点与自动执行均全程审计；自动执行带 `autonomy_source` 可回溯。
- 更新 `docs/OPERATIONS.md`：新端点（`/v1/overview`、`/v1/action-plans/{id}/reject`）、
  E2 准入门、已知限制（现状无条件自动执行已被收回为显式 opt-in）。
- 更新 `.env.example`：新增 `COPILOT_AUTONOMY_ENABLED`。

## 8. 关键文件

- 后端：`internal/httpapi/router.go`（overview / reject 路由）、`cmd/copilot-api/main.go`（env + 装配）、
  `internal/plans/service.go`（reject + rejected 态）、`internal/store/action_plans.go`（状态/审计）、
  `internal/scheduler/service.go`（run_kind 写触发）、`internal/assistant/service.go`（准入 + 收回现状）、
  `internal/policy/policy.go`（低风险判定复用）。
- 前端：`apps/capability-console/src/` 新增 `DashboardView.vue` + types/api + composable，`App.vue` 挂载。
- 测试：各后端单测 + 前端 vitest；`make all-checks` 全绿。

## 9. 分期

1. **Phase 1（E1 + D）**：overview + reject 端点 + DashboardView 操作面。低风险，可立即交付。✅
2. **Phase 2（E2 准入门）**：`COPILOT_AUTONOMY_ENABLED` + Admission Controller + 收回现状 +
   agent loop 低风险写。✅
3. **Phase 3（E2 定时）**：scheduler `run_kind: runbook` 写触发。✅（`internal/scheduler/runbook_executor.go`
   的 `RunbookAutoExecutor` 命中共用的 `autonomy.Controller`，fail-closed；scheduler / Service / httpapi DTO /
   store 均新增 `run_kind` + `runbook_slug`，迁移 `016`）

## 10. 后续可扩展点（非本期）

- 条件触发（事件源 + 规则引擎）。
- Dashboard 深度聚合 / 自助图表。
- 自动执行与准入的更多数据（每日上限、来源分布看板）。
