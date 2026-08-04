# Assistant 与自治 Agent 循环

本文档描述 assistant 的两种运行模式（单步 planner 与自治 agent 循环）、循环的
安全边界、中间工具结果的持久化与审计，以及前端的步骤化展示。

## 概述

assistant 的核心抽象是 **planner**：把用户消息解析为一个 `Intent`，后端再把这个
候选意图交给既有安全边界（静态工具注册表、policy、plan、execution、audit）去解析
和执行。Eino 的输出始终是**不可信的候选数据**，绝不会直接执行运维工具、SQL、shell
或原始中间件 API。

- **单步 planner**（默认）：一次 `Plan` → 解析**一个**意图 → 执行一次工具（读/写）。
- **自治 agent 循环**（opt-in）：`Plan → 执行只读工具 → 把结果反馈回 planner →
  重规划 → …`，直到 planner 给出 `final_answer`、要求澄清、遇到写意图交还给人，
  或耗尽 `maxSteps`。这是"多工具链式执行 + 结果反馈重规划"的智能所在。

## Assistant 边界：中间件能力外置（注册 + 执行走 HTTP）

中间件相关能力**不写死在 Go 代码里**——新增或调整一个中间件能力的接入，无需改 Go
代码、重新编译、重新发布：

- **注册外置**：中间件工具（`glusterfs.volume.health.read`、
  `minio.bucket.health.read`、`kafka.consumer_lag.read`、`topic.retention.set`）
  不在 Go 静态注册表（`internal/tools/registry.go` 的 `registeredTools`）中，而是声明为
  `examples/capabilities/published/*.yaml` 的 published 能力，由 `COPILOT_CAPABILITIES_DIR`
  加载（`capabilities.RegisterPublished` → 注册为动态工具）。静态注册表只保留 5 个**平台
  元工具**（`cluster.status.read`、`system.posture.read`、`alert.query`、`event.query`、
  `task.query`），它们是助手自身的内建查询，非中间件能力。
- **执行外置**：中间件读/写经 `CapabilityReadRunner` / `CapabilityWriteRunner` +
  `HTTPAdapter` 打到 yaml `backend.base_url + path` 指向的 HTTP 中间件后端。本地开发由
  mock 中间件（`examples/mock-middleware-api.js`，`:19090`）承接，需在 `.env` 配置
  `COPILOT_MOCK_MIDDLEWARE_URL`（与 yaml 的 `base_url` 一致）。未配置 backend 时中间件
  能力退化为不可用，平台元工具不受影响。
- **校验 schema 外置**：中间件工具的 input schema 迁到 yaml `input_schema`（动态工具
  经 `DynamicInputSchema` 校验），静态 `ValidateInput` 只处理平台元工具。
- **planner 路由去硬编码**：写意图（如 `topic.retention.set`）不再由确定性 planner 写死
  工具名，而是由 `CapabilityAwarePlanner` 按域/参数动态解析能力（工具 Domain 从 yaml
  读取）。

演进的必然代价：dev 环境未起 mock 时中间件能力不可用（HTTP 连接失败），而不是进程内
模拟。这符合"外置 HTTP"的取舍。


## 启用方式

循环是 **opt-in**：`assistant.Service.WithAgentLoop(true)` 才开启，默认关闭以保留
单步流式语义及既有测试行为。生产接线在 `cmd/copilot-api/main.go`，仅当
`COPILOT_ASSISTANT_PROVIDER='eino-openai'` 时启用。确定性 planner 永不循环
（忽略历史，循环只会重放同一意图）。

提供类同 OpenAI 的 Eino planner 的环境变量：

```bash
COPILOT_ASSISTANT_PROVIDER='eino-openai'
COPILOT_OPENAI_API_KEY='...'
COPILOT_OPENAI_MODEL='...'
COPILOT_OPENAI_BASE_URL='https://api.openai.com/v1'
```

- **步数上限**：`COPILOT_ASSISTANT_MAX_STEPS` 环境变量，默认 `8`。循环在
  有界步数内终止，绝不无限自旋；连续读失败上限 `maxAgentFailures=3`。

## 循环生命周期

`internal/assistant/agent_loop.go` 的 `AgentLoop` 驱动每次迭代：

```
loop:
  intent = planner.Plan(user, message, agentHistory, pageContext)
  if intent.Done:          -> 输出 final_answer，终止 (TerminalDone)
  if ErrClarificationNeeded: -> 输出澄清表单，终止 (TerminalClarification)
  if intent 是写工具:       -> 建 plan + confirmation_required，交还给人 (TerminalHandoff)
  result = executeReadStep(intent)   # advisory：只读工具
  若有错误:                 -> 把错误作为 tool_step 反馈给 LLM，在有界次数内选 fallback；
                              否则终止并上报错误
  发射 step(intent, result) # 前端步骤时间线
  agentHistory = append(agentHistory, asTurn(result))  # 反馈 + 持久化
  超过 maxSteps            -> 终止 (TerminalMaxSteps)
```

终止原因 `TerminalDone / TerminalMaxSteps / TerminalHandoff / TerminalClarification`
各自映射到对应的流式终止事件；流式契约保证**恰好一个**终止事件。

## 意图信号：final_answer

单步 planner 无法区分"我要结束"和"缺参数"。Eino planner 的 JSON 输出 schema 增加
顶层布尔 `final_answer` + `summary string`。`parseIntent` 在 `final_answer=true`
时返回带完成标记的 `Intent{Done, Answer}`（先于 confidence 阈值与空 tool_name 的
澄清判定）。热加载模板见 `prompts/planning.md`。

## 安全边界（自治循环的红线）

**自治 ≠ 无约束**，循环内：

- **只读工具**（advisory）可以**自主链式执行**。
- **写工具**（executive）自动执行仍需现有审批/action-plan 门槛；循环内写到这一步
  就**停在 plan 创建 + `confirmation_required`，交还给人确认**，绝不自动批准
  （`agentWriteStep` 只调 `plans.CreatePlan`，从不调 `CreateRunbookPlan` 或
  `s.execution`）。
- **低风险 Runbook 自动执行在循环内主动禁用**：即使低风险 runbook 匹配写意图，
  循环也停在 `confirmation_required`，让操作员拍板，而不是沿用单步路径的
  auto-exec。有测试 `TestAgentLoopNeverAutoExecutesLowRiskRunbook` 保证。
- 硬上限 `maxSteps`、每步超时、LLM 提示词"完成即输出 final_answer"。

## 中间工具结果持久化（tool_step）

多步循环的每一次只读工具结果都会持久化到对话历史，支持**跨轮引用 + 完整审计**。
`runAgentLoopInStream` 结束时用 `Service.persistAgentRun` 落库，形成链式结构：

```
user 消息 turn
  → step1 (assistant, response_type=tool_step, payload=ToolFact, parent→user)
  → step2 (assistant, response_type=tool_step, payload=ToolFact, parent→step1)
  → 终态响应 turn（parent→最后一步）
```

- 每一步 `copilot_assistant_turns` 记录：`response_type='tool_step'`，
  `response_payload` 存 ToolFact（`tool`/`input`/`result`）+ `step_index` +
  `summary`，`parent_turn_id` 指向上一步，因果链可重建。
- 回放时，`toPlannerTurn` 保留 `ResponseType`，下一轮 planner 能看到上一步的工具
  结果（跨轮引用）。
- executive / clarification 步不写 `tool_step`（写步的终态响应本身已持久化）。

## 审计步骤身份

循环内每次只读调用会带步骤身份进审计：`execution.AgentStep{StepIndex, Conversation}`
经 context 传递（定义在 `internal/execution`，避免 import cycle），
`ReadOnlyService.record` 把 `agent_step` + `conversation_turn_id` 写进 audit
metadata，让审计能看到每一步的输入/输出/决策归属。

## 前端步骤化展示

- 后端 `StreamEvent` 新增 `Step *StepEvent{Tool, StepIndex, Status, Summary, Input, Output}`
  （SSE `event: step`）。
- 前端在 `useAssistant.ts` 按 `step_index` 累积步骤列表；`AssistantSteps.vue` 把
  "已执行步骤"渲染为**独立区块**（工具名/状态/结果摘要，默认展开、可收起），与最终
  答复分开；可复用 `ProgressTimeline` 的视觉语言。
- 回放：持久化的 `tool_step` turn 在对话切换/刷新后各自重建为一个步骤区块（不再重复
  渲染文字气泡），与流式实时所得一致。

## 测试

- `internal/assistant/agent_loop_test.go`：`scriptedPlanner` 脚本多步——读到该写、
  错误后选 fallback、超 maxSteps 停、executive 停-交还。
- `internal/assistant/service_agent_test.go`：`agentFakePlanner`（Plan + PlanStream）
  端到端跑 `HandleMessageStream`——多步链给 step 事件 + final_answer；写停
  `confirmation_required`；持久化 `tool_step` 链 + 审计步骤身份；
  低风险 runbook 不自动执行。
- 前端：`ConversationTurnItem.test.ts`（步骤区块）、`useAssistantStream.test.ts`
  （`event: step` 解析）。
