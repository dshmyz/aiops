# SxDevOps AIOps 智能体实现说明 — 差距分析

> 对照来源：`https://github.com/dshmyz/sxdevops/blob/main/docs/AIOps智能体实现说明.md`
> 本文档记录对该文档逐章节的对照分析，标注已对齐能力与未覆盖缺口，作为后续演进的依据。

## 1. 总体目标

SxDevOps 核心原则：

- 只通过平台内数据和受控工具回答。
- 优先使用 LLM tool-calling 选择工具，不依赖前端关键词拼装查询。
- 查询类问题直接返回事实与解释，动作类问题必须先生成草稿并等待确认。
- 回答必须基于工具事实，证据不足时明确说明不足。
- 会话、工具调用、待确认动作和执行结果都要可审计。

**已对齐**：上述五条原则均已在项目实现。tool-calling 由 `EinoPlanner` 承担；查询/动作分流由 `tools.Tool.Operation`（Read/Write）驱动；审计由 `audit.Service` 覆盖会话、工具调用、待确认动作、执行结果四类事件。

## 2. 整体架构

文档定义 6 层：前端聊天层、AIOps API 层、智能体调度层、平台内置 MCP 工具层、外部 MCP 接入层、Skill 与回答整形层。

| 层 | 我们的实现 | 状态 |
|----|-----------|------|
| 前端聊天层 | `apps/capability-console/src/components/AssistantTranscript.vue` 等 | 已对齐 |
| AIOps API 层 | `internal/httpapi/router.go` | 已对齐 |
| 智能体调度层 | `internal/assistant/service.go` + `planner.go` | 已对齐 |
| 平台内置 MCP 工具层 | `internal/tools/registry.go`（registeredTools 白名单） | 已对齐 |
| 外部 MCP 接入层 | — | **未覆盖**（见 §缺口-2） |
| Skill 与回答整形层 | `internal/assistant/llm_formatter.go` + `internal/store/skills_seed.go` | 已对齐 |

## 3. 问答链路

文档以"分析生产 order-center 最近异常"为例，定义 10 步链路。对照：

| 步骤 | 文档描述 | 我们的实现 | 状态 |
|------|---------|-----------|------|
| 1 | 前端发送问题到同步/异步消息接口 | `HandleMessage` / `HandleMessageStream` | 已对齐 |
| 2 | 后端创建用户消息和占位助手消息 | `persistTurns`（会话存储非 nil 时） | 已对齐 |
| 3 | 调度层加载模型配置、MCP、Skill、历史、页面上下文 | 模型/MCP/Skill/历史已加载；**页面上下文未接入** | 部分对齐（见 §缺口-3） |
| 4 | 第一阶段 LLM 做 tool-calling 规划 | `EinoPlanner.Plan` / `PlanStream` | 已对齐 |
| 5 | 后端按工具白名单执行 MCP / 平台工具 | `execution.ReadOnlyService` + `tools.Lookup` | 已对齐 |
| 6 | 工具返回结构化事实，形成事实集 | 单工具事实直接进 `Answer`；**多工具事实集聚合未实现** | 部分对齐（见 §缺口-4） |
| 7 | 后端生成一版基于事实的兜底草稿 | `CodeFallbackFormatter`（但仅看单个 Answer） | 部分对齐（见 §缺口-4） |
| 8 | 第二阶段 LLM 结合事实集和 Skill 模板做回答整形 | `LLMFormatter` + `ChainedFormatter[LLM, Code]` | 已对齐 |
| 9 | 整形不合格回退到代码兜底草稿 | `ChainedFormatter` 链式回退 | 已对齐 |
| 10 | 返回消息、**进度事件**、工具调用、证据块、待确认动作 | 工具调用/证据块/待确认动作已返回；**进度事件未定义** | 部分对齐（见 §缺口-1） |

## 4. MCP 与 Skill 的职责

文档定义 MCP 负责把平台能力标准化为模型可调用工具（可观测性、事件中心、任务中心、工单、容器管理、资源上下文 6 个方向）；Skill 不查数据，只约束回答结构。

**已对齐**：`registeredTools` 覆盖集群状态、Kafka、GlusterFS、MinIO、系统态势、告警、事件、任务等 9 个静态工具（含 `system.posture.read`/`alert.query`/`event.query`/`task.query`）；18 个内置 Skill（`copilot_aiops_skills` 表）按告警/异常分析、工单汇总、任务生成等场景约束输出结构。

**未对齐**：工单系统、容器管理、资源上下文 3 个 MCP 方向暂无数据源，未建工具（零数据无意义，待数据层出现再暴露）。事件中心/任务中心已补齐（2026-08-02）。

## 5. 工具执行边界

文档要求后端工具层负责：RBAC 权限校验、参数清洗与资源范围约束、超时限流、统一审计、对接内置工具和外部 MCP。

**已对齐**：
- RBAC：`policy.Evaluate(user, tool, input)`
- 参数清洗：`tools.ValidateInput`
- 审计：`audit.Service.Record`
- 内置工具白名单：`registeredTools` + `DynamicToolDefinition`

**未对齐（2026-08-02 已闭环）**：~~超时与限流在工具执行层未统一封装（`ReadOnlyService.ExecuteRead` 未设超时）~~ → 已加 `defaultReadTimeout=5s` + `WithTimeout` 封装（`readonly.go` readCtx），HTTP 现有 deadline 自动取 min；外部 MCP 对接已在缺口-2 完成。

## 6. 双阶段回答

文档定义三段式：第一阶段取证 → 第二阶段整形 → 代码兜底。

**已对齐**：
- 一阶段取证：`EinoPlanner.Plan` 解析 intent，`ReadOnlyService.ExecuteRead` 执行工具
- 二阶段整形：`LLMFormatter.Format` 调 LLM 生成 Summary + Blocks
- 代码兜底：`CodeFallbackFormatter` 从 Answer 提取关键字段
- 链式串联：`ChainedFormatter[LLM, Code]`，LLM 失败自动回退

**措辞差异**：文档 §3 第 7 步强调兜底草稿在二阶段 LLM 整形**之前**就生成（基于事实集），而非 LLM 失败后才生成。我们的 `ChainedFormatter` 是"LLM 先跑，失败再回退代码"，执行顺序不同。文档的语义是"兜底草稿始终生成，作为 LLM 的保底对照"，我们是"兜底草稿仅在 LLM 失败时生成"。功能等价但语义不完全一致。

## 7. 待确认动作

文档要求：任务生成、自愈、执行类操作不直接落地，先生成待确认动作，用户确认后再执行。

**已对齐**：
- 写操作创建 pending plan：`plans.Service.CreatePlan` → `confirmation_required` 响应
- 确认后执行：`execution.Service.ExecuteConfirmedPlan`
- dry-run 预演：`DryRunService` 在 plan 创建后自动预演，结果作为 `risk_notice` block 附到响应
- preflight 预检：`preflight.go` 补齐参数 + 权限校验

## 8. 前端体验层

文档列出的前端能力：会话历史、异步轮询与流式显示、**进度事件折叠**、待确认动作卡片、工具调用与证据展示、**页面上下文带入**。

**已对齐**：会话历史（`ConversationSidebar`）、流式显示（`useAssistantStream`）、待确认动作卡片（`AssistantInlineConfirm`）、工具调用展示（`AssistantTraceView`）、block 渲染（`BlockRenderer`）。

**未对齐**：
- 进度事件折叠：前端无对应组件，后端无进度事件定义（见 §缺口-1）
- 页面上下文带入：前端无上下文采集，后端 `HandleMessage` 签名无 page context 参数（见 §缺口-3）

## 9. 审计与权限

文档要求覆盖：会话审计、工具调用审计、模型调用与成本审计、待确认动作审计、协同任务与 Runbook 审计。

**已对齐**：会话审计（`AssistantConversationStore`）、工具调用审计（`audit.Service` 记录 readonly/write/plan 事件）、待确认动作审计（plan 状态变更审计）。

**未对齐**：模型调用与成本审计（token 用量、调用耗时）未单独记录；协同任务与 Runbook 审计未实现。

## 10. 工程边界

文档明确的边界：Skill 整形是主路径、代码兜底是最后保障；外部 MCP 实际可用性依赖外部服务；指标日志 Trace 分析质量依赖数据源覆盖；进度事件不暴露原始 CoT；动作类必须经过预检、确认、权限校验、审计。

**已对齐**：除"外部 MCP"和"进度事件"外，其余边界均已遵守。

---

## 更新记录

> 以下为 2026-08-02 实施后的状态更新。缺口-1/2/3/4 已完成，借鉴-2（intent 分类）已完成。详见下文各缺口的"实施状态"段。

## 缺口清单与建议优先级

### 缺口-1：进度事件（优先级：高）✅ 已完成

**现状**：`HandleMessageStream` 只发 `Delta`、`Thinking`、`ToolCall`、`Done` 四类事件，没有覆盖规划阶段（"模型规划中"）、整形阶段（"生成回复中"）的进度事件。前端无法展示文档 §8 描述的"进度事件折叠"。

**影响**：用户在等待 LLM 响应时看不到阶段进展，体验差于 SxDevOps。

**建议**：在 `StreamEvent` 增加 `Progress *ProgressEvent` 字段，定义 `planning` / `tool_executing` / `formatting` / `done` 四种阶段，在 `executeFromIntent` 各阶段边界发送。前端 `AssistantTranscript` 折叠展示。

**实施状态（2026-08-02）**：已完成。`StreamEvent` 新增 `Progress *ProgressEvent` 字段（[eino_planner.go:96](file:///Users/gracegaoya/Documents/New%20project/internal/assistant/eino_planner.go#L96)），定义 `ProgressPlanning` / `ProgressToolExecuting` / `ProgressFormatting` 三阶段常量（[eino_planner.go:121-131](file:///Users/gracegaoya/Documents/New%20project/internal/assistant/eino_planner.go#L121-L131)）。`service.go` 在各阶段边界发送进度事件。前端新增 `ProgressTimeline.vue` 组件折叠展示三阶段。

### 缺口-2：外部 MCP 接入层（优先级：中）✅ 已完成

**现状**：只有 `registeredTools` 静态白名单 + `DynamicToolDefinition` 运行时注册，没有"配置一个外部 MCP server（HTTP/stdio）并自动把它的工具纳入白名单"的能力。

**影响**：无法接入第三方 MCP（如 GitHub MCP、Slack MCP），平台扩展性受限。

**建议**：新增 `internal/mcp/` 包，定义 `MCPServerConfig`（name、transport、endpoint、auth），实现 `Discover()` 拉取远程工具列表并转成 `DynamicToolDefinition` 注册。`main.go` 启动时加载 `COPILOT_MCP_SERVERS` 配置并注册。

**实施状态（2026-08-02）**：已完成。
- 基础接入：`internal/mcp/` 包实现 config/discover/stdio，`main.go` 通过 `COPILOT_MCP_SERVERS` 环境变量加载（13 个测试）。
- 健康检查：`internal/mcp/health.go` + `snapshot.go` + `events.go` + `audit/enums.go`，三态（healthy/degraded/unhealthy）+ 延迟测量 + 审计事件（18 个测试）。
- 热配置：DB 持久化 + REST API（`/v1/mcp/servers` CRUD + `/v1/mcp/servers/reload`，admin-only）+ `Manager` 增量注册/注销 + 互斥锁并发安全。环境变量与 DB 配置共存，要求 server name 唯一。
- 借鉴-6（外部 MCP 健康检查）随之完成。

### 缺口-3：页面上下文带入（优先级：高）✅ 已完成

**现状**：`HandleMessage(ctx, user, message, conversationID)` 无 page context 参数，planner 拿不到"用户当前在看哪个服务/环境/资源"。

**影响**：用户在 GlusterFS 页面问"这个 volume 健康吗"，planner 无法从上下文推断 domain=glusterfs、resource=data，只能靠关键词匹配，容易误判。

**建议**：`HandleMessage` 增加可选 `PageContext` 参数（domain、environment、resourceType、resourceName），planner 优先用上下文补全 intent。前端 `AIOpsChatWidget` 在 `sxdevops-aiops-open` 事件时采集当前路由上下文并随消息发送。

**实施状态（2026-08-02）**：已完成。
- 类型定义：`PageContext{Domain, Environment, ResourceType, ResourceName}`（[planner.go:155-160](file:///Users/gracegaoya/Documents/New%20project/internal/assistant/planner.go#L155-L160)），前后端 1:1 对齐。
- HTTP 协议：`/v1/assistant/messages` 和 `/v1/assistant/stream` 接收 `page_context` 字段，并做 legacy `environment` 字段合并兼容。
- 调用链：`HandleMessage` / `HandleMessageStream` / `Planner.Plan` / `Planner.PlanStream` 签名全透传 pageContext，无遗留旧签名。
- Planner 消费：`EinoPlanner.injectPageContext` 拼消息前缀注入 prompt（[eino_planner.go:568-586](file:///Users/gracegaoya/Documents/New%20project/internal/assistant/eino_planner.go#L568-L586)）；`DeterministicPlanner` 用 pageContext 兜底 environment/domain。
- 前端：`useAssistant.ts` 的 `assistantPageContext` ref + `setAssistantPageContext` 方法，`ManagementView` 的"问 AI"按钮 emit `ask-ai`，`App.vue` 接收并设置上下文，上下文 badge + 清除按钮。
- **增强（ActionRouter 感知 pageContext）**：`ActionRouter.Route` 签名增加 `pageContext`，消息未命中关键词时用 `LookupAction(pageContext.Domain)` 兜底（[action_router.go:77-84](file:///Users/gracegaoya/Documents/New%20project/internal/assistant/action_router.go#L77-L84)）。例如在 minio 页面发"健康状态如何"会兜底命中 `middleware.diagnose`。message 始终优先，符合 PageContext 文档"Message tokens always take precedence"。3 个新测试覆盖兜底/优先级/未知 domain。

### 缺口-4：事实集聚合（优先级：中）✅ 已完成

**现状**：`CodeFallbackFormatter` 只看单个 `Answer`。诊断包 + 读工具 + 推荐执行的多工具场景下，兜底草稿会漏掉部分事实。

**影响**：LLM 整形失败时，兜底草稿信息不完整。

**建议**：`FormatRequest` 增加 `FactSet []ToolFact` 字段（每个工具的 name + input + result），`CodeFallbackFormatter` 遍历 FactSet 生成草稿，而非只看单个 Answer。

**实施状态（2026-08-02）**：已完成。修复 `service.go` 中诊断分支调用顺序问题——诊断分支原在执行推荐读工具前就调 `formatResponse`，导致推荐结果未进 FactSet。改为先收集推荐读工具的事实、合并进 FactSet、再传给 `formatResponse`。新增 2 个测试覆盖有/无推荐的场景。

### 缺口-5：模型调用与成本审计（优先级：低）⬜ 未开始

**现状**：`audit.Service` 记录工具调用和 plan 事件，但不记录 LLM 调用的 token 用量和耗时。

**影响**：无法统计 AIOps 的 LLM 成本。

**建议**：`EinoPlanner` 和 `LLMFormatter` 在 LLM 调用后记录 `audit.Event{Action: "llm_invoked", Metadata: {model, prompt_tokens, completion_tokens, latency_ms}}`。

**实施状态**：未开始。借鉴-2（intent 分类）已完成，可作为成本统计的分类基础（按 IntentType 聚合 LLM 调用成本）。

---

## 附录 B：产品介绍（promo.html）新增借鉴点

> 对照来源：`https://github.com/dshmyz/sxdevops/blob/main/docs/sxdevops-ai-agent-promo.html`
> 以下条目为产品介绍（15 张 slide）中**新冒出**的、§缺口-1 ~ §缺口-5 未覆盖的设计点，按优先级排列。

### 借鉴-1：系统态势 SLA 作为排障入口（优先级：高）

**来源**：slide 4「系统态势 SLA：先看全局，再收敛故障边界」

**现状**：orchestrator 当前是"收到多域请求 → 拆分并发子诊断 → 合并诊断包"，**缺少一个全局态势预览层**作为排障入口。用户必须先知道"哪里有问题"才能发起诊断。

**借鉴点**：SxDevOps 把"全局发现 → 边界收敛 → 继续排障"做成三段式入口：先按系统汇总最近 SLA 历史和当前状态，识别故障系统、未知系统和健康趋势，再从环境分组、系统条带和故障标记中确认影响范围，最后进入证据链。

**建议**：在 orchestrator 之上增加一个"系统态势快照"工具（`query_system_posture`），按系统维度聚合最近 SLA/健康状态，作为诊断的前置步骤。也可作为独立工具暴露给 LLM，让 Agent 在不确定故障边界时先调用此工具圈定范围，再决定是否拆分多域诊断。

**与现有缺口的关系**：与 §缺口-3（页面上下文）互补——页面上下文解决"用户已知在哪"，态势 SLA 解决"用户不知道在哪"。

---

### 借鉴-2：咨询/生成/执行三类 intent 分类（优先级：高）

**来源**：slide 7 callout「核心边界：咨询类问题直接返回事实；生成类问题先出草稿；执行类动作必须确认后才落任务」

**现状**：我们的 `plan→policy→execution` 链路依据 `tools.Tool.Operation`（Read/Write）区分读写，但**没有显式的"问题类型分类"作为 policy 输入**。dry-run 预演对所有写 plan 一视同仁，confirmation_required 的触发条件仅看工具类型，不看问题意图。

**借鉴点**：SxDevOps 把问题分为三类，每类走不同链路：
- 咨询类（"最近告警是什么"）→ 直接返回事实，不生成草稿
- 生成类（"帮我写个巡检任务"）→ 先出草稿，用户可改可弃
- 执行类（"立即执行巡检"）→ 必须确认才落任务

**建议**：在 `policy.Evaluate` 前加一个 intent 分类步骤（可复用 LLM 或基于规则），输出 `IntentType ∈ {advisory, generative, executive}`。policy 依据 IntentType 调整 confirmation 策略——advisory 直接放行，generative 生成草稿但不强制确认，executive 强制 confirmation_required。dry-run 预演的详细程度也可随 IntentType 调整。

**与现有缺口的关系**：强化 §缺口-5（模型成本审计）的分类基础——不同 intent 的 LLM 调用成本可分开统计。

---

### 借鉴-3：任务草稿自动补齐执行策略（优先级：中）

**来源**：slide 7「自动补齐目标主机、命令、超时、风险提示和执行策略」

**现状**：`DryRunResult` 已返回 `Summary / AffectedResources / Commands / Warnings`，但**执行策略（超时、重试、并发度）和目标主机的自动补齐**未覆盖。preflight 只做参数校验和权限校验，不做策略建议。

**借鉴点**：SxDevOps 的任务草稿不只列出"要做什么"，还自动补齐"怎么做"——目标主机、命令、超时、风险提示、执行策略一应俱全，用户确认时看到的是完整执行计划。

**建议**：扩展 `DryRunResult`，新增 `SuggestedStrategy` 字段：
```go
type SuggestedStrategy struct {
    Timeout      time.Duration
    Retry        int
    Concurrency  int
    TargetHosts  []string
    RiskLevel    string  // low / medium / high
}
```
`DryRunService` 根据工具类型和影响资源推断策略（如批量操作自动设并发度，长命令自动设超时）。`risk_notice` block 渲染时把策略一并展示。

**与现有缺口的关系**：是对 §缺口-5 之外 dry-run 能力的横向扩展，独立于现有缺口。

---

### 借鉴-4：事件中心"最终结果过滤"视图（优先级：中）

**来源**：slide 6「只保留最终执行结果和关键写操作，默认过滤未执行的驳回审批流」

**现状**：`ScheduledTaskRun` 和审计事件目前是全量记录，复盘时驳回/未执行的噪声可能淹没关键事件。

**借鉴点**：SxDevOps 的事件中心不是流水账，而是围绕"最终执行结果、关键写操作、失败定位"的复盘面板。产品设计上**默认过滤未执行的驳回审批流**，让复盘聚焦在"真正发生了什么"。

**建议**：审计事件查询接口增加 `final_result_only` 过滤参数，默认隐藏 `status = rejected | cancelled | skipped` 的事件。巡检 HTML 报告（InspectionReport）聚合时也按此过滤，避免驳回任务的历史记录干扰判断。前端事件中心视图提供"显示全部 / 仅最终结果"切换。

**与现有缺口的关系**：与巡检报告聚合（InspectionReport）直接相关，是对现有审计数据展示策略的优化。

---

### 借鉴-5：Runbook / 命令模板复用机制（优先级：中）

**来源**：slide 14 Roadmap「在只读诊断后，接入审批、命令模板、Runbook 和任务编排，形成低风险自动化闭环」

**现状**：目前每次写操作都走完整 dry-run + confirmation 链路，**高频确认动作无法沉淀复用**。同类问题重复走全链路，效率低。

**借鉴点**：SxDevOps 的 Roadmap 把"命令模板、Runbook、任务编排"作为处置编排层，让高频低风险动作可套用模板，减少每次从零规划。

**建议**：新增"命令模板/Runbook"实体（`copilot_runbooks` 表），字段包含 name、intent_pattern、tool_sequence、default_strategy、risk_level。planner 在识别到匹配 intent_pattern 的问题时，优先套用 Runbook 跳过部分 dry-run 步骤（仅对 Runbook 标记为 `low risk` 的动作）。用户确认后，执行结果可回写优化 Runbook 的 default_strategy。

**与现有缺口的关系**：是 §缺口-2（外部 MCP）之后的扩展性演进方向，也是"先可信再自动"第三阶段的落地载体。

---

### 借鉴-6：外部 MCP 健康检查与诊断（优先级：中，补充 §缺口-2）

**来源**：slide 14「继续扩展平台内置 MCP，同时增强外部 MCP 健康检查、工具发现、鉴权与超时诊断」

**现状**：§缺口-2 只提到"接入外部 MCP server"，未细化接入后的运维保障。

**借鉴点**：SxDevOps 明确外部 MCP 需要四个诊断能力：健康检查（server 是否存活）、工具发现（拉取最新工具列表）、鉴权（token 是否有效）、超时诊断（响应是否在 SLA 内）。

**建议**：补充 §缺口-2 的实现，`internal/mcp/` 包增加 `HealthChecker`：
- `HealthCheck(server MCPServerConfig) HealthReport`：定期 ping 所有已配置的外部 MCP server
- `DiscoverTools(server) ([]ToolDefinition, error)`：定期拉取工具列表，对比上次快照发现变更
- 鉴权失败 / 超时 / 工具列表变更均发审计事件
- 外部 MCP 状态在数据源管理界面（project_memory 中规划）统一展示

**与现有缺口的关系**：是对 §缺口-2 的细化补充。

---

### 借鉴-7："先可信，再自动"三阶段演进路径（战略层面）

**来源**：slide 14 末尾「1. 先建立可信事实链路：告警准、事件准、任务准、结果准。2. 再让 AI 负责证据整合、结构化输出和动作草稿生成。3. 最后把高频、低风险动作纳入自动化闭环。」

**现状**：我们目前在阶段 2（AI 负责证据整合、结构化输出、动作草稿生成），dry-run + confirmation 已落地。但阶段 1（可信事实链路）的完备度值得审视——数据源管理（project_memory 中大量约束）正是阶段 1 的基建，但尚未完工。

**借鉴点**：SxDevOps 把演进路径明确为三阶段，每阶段有清晰目标。这个框架可作为我们演进的校准坐标。

**建议**：把"可信事实链路"作为隐式前置里程碑，在数据源管理（environment_datasource_bindings、连通性测试、关联配置）完工前，暂缓推进借鉴-5（Runbook 自动化闭环）。具体可建立一份"事实可信度自检清单"：告警是否准、事件是否准、任务是否准、结果是否准——四项全绿才进入阶段 3。

**与现有缺口的关系**：是所有借鉴点的战略框架，统领优先级排序。

**实施状态（2026-08-02）**：完成阶段 1 深度审计，判定为**未全绿**。借鉴-5（Runbook 自动化）继续暂缓。

### 阶段 1 四项自检清单 — 审计结果

| 项 | 就绪度 | 关键红灯 | 证据 |
|---|---|---|---|
| **告警准** | ✅ 已就绪（2026-08-02） | 六层全建：`internal/alert/` 包（model/ingest/service）+ webhook 接入路由（HMAC 签名）+ 归一化 + `copilot_alerts` 存储表 + `COPILOT_ALERT_WEBHOOK_SECRET` 配置 + `alert.query` 查询工具（含 Skill + eval + 页面上下文） | `internal/alert/`；`router.go` `/v1/alerts/webhook`；`migrations/012_alerts.sql`；`registry.go` `alert.query`；`skills_seed.go` `alert-query-guide` |
| **事件准** | 🟡 部分就绪 | 7 项红灯：R1 LLM 调用零审计 / R2 HTTP 权限拒绝零审计 / R3 登录登出零审计 / R4 任务跳过过期零审计 / R5 MCP degraded 事件被丢弃 / R6 无防丢失机制 / R7 不支持 plan_id 过滤 | `enums.go:7-25` 15 个 action；`readonly.go:54-72` 工具调用失败已覆盖；`router.go` 14 处 403 无 audit.Record；`health.go:97` 发射 degraded 但 `main.go:143-162` emitter 无对应分支；`action_plans.go:766-773` 单条 INSERT 无重试 |
| **任务准** | 🟡 部分就绪 | 9 项问题：duration 永远为 0（bug）/ 无并发锁 / 补跑无过期保护 / AppendRun 与 UpdateTask 非原子 / 时区静默回退 / DSN loc 未强制 / 无 active 状态 / ListDueTasks 无 limit / audit 写入失败静默忽略 | `scheduler.go:165-167` startedAt==finishedAt 引用同一 now；`scheduled_tasks.go:408-434` ListDueTasks 普通 SELECT 无 FOR UPDATE；`schedule.go:88-90` loadLocation 失败返回 nil error；`scheduled_tasks.go:17-23` 仅 succeeded/failed 两状态 |
| **结果准** | ✅ 已就绪（2026-08-02） | ① error 真实信息持久化（TruncateError）② ResultSummary 保留真实输出 + 递归剥离敏感键 + 10KB 上限 ③ dry-run/VerificationResult 落库（action_plans.dry_run + tool_executions.verification，migration 013）+ `/v1/executions` 查询 API | `service.go` TruncateError + sanitizedResultSummary（scrubSensitiveKeys）+ SetExecutionVerification；`plans.Service.AttachDryRun`；`router.go` `/v1/executions` |

### 全绿度判定

**阶段 1 总判定：收口项基本清零，剩余均为专项级工作。**

- 事件准：🟡→🟢 收口项已清 4/5（MCP degraded / 防丢失 / plan_id 过滤 / final_result_only），剩 R1 LLM 审计（小改）；R2/R3/R4（HTTP权限/登录登出/任务跳过 审计补齐）为中等改动
- 任务准：🟡 duration=0 bug 已修；剩余并发安全/补跑风暴/原子性为专项级重构
- 结果准：🟡→🟢 error 持久化 + 查询 API 已做；剩余 output 脱敏重设计 / dry-run 落库为专项级
- 告警准：❌→✅ **六层基建已全建**（2026-08-02）——webhook 接入（HMAC 签名 + 审计）+ 归一化 + `copilot_alerts` 表 + `alert.query` 工具 + `alert-query-guide` Skill + eval 用例。为借鉴-5（Runbook 自动化）提供了触发信号数据源

### 借鉴-5（Runbook 自动化）解禁建议

**结论：继续暂缓，不予解禁。**

理由：
1. **告警准 0%** 是硬阻塞 — Runbook 自动化闭环依赖"告警准"作为触发信号，无告警数据源则 Runbook 无从触发。但告警准是独立专项（六层全建），不应作为收口项推进；等借鉴-5 真正需要解禁时再启动
2. **结果准的 error/output 丢失** 会使 Runbook 执行失败时无法复盘 — 自动化闭环必须可追溯（error 持久化已修，output 脱敏重设计为专项）
3. **任务准的并发不安全** 会使 Runbook 在多实例部署时重复执行 — 自动化必须可幂等（专项级重构）

### 阶段 1 推进项分类（收口 vs 专项）

原列表把 7 项排成线性优先级，掩盖了工作量差异。此处按"收口项（小改，补缺）/ 专项项（独立功能或大重构）"拆分，避免用收口方法论做专项的事。

#### 收口项（小改，几十行代码级）

1. ✅ **结果准 - error 真实信息持久化**：`service.go:193-206` 把 `executionErr.Error()` 写入 ErrorSummary — 已完成（`internal/execution/service.go` TruncateError，2026-08-02）
2. ✅ **任务准 - duration=0 bug 修复**：`scheduler.go:165-167` 把 finishedAt 改为执行后重新取 now — 已完成（`internal/scheduler/scheduler.go` executeAndRecord nowFn，2026-08-02）
3. ✅ **事件准 - R5 MCP degraded 事件接线**：`main.go:143-162` emitter 补 degraded 分支 — 已完成（`cmd/copilot-api/main.go` mcpEventToAuditEvent，2026-08-02）
4. ✅ **事件准 - R6 防丢失机制**：audit.Service 增加重试 + 本地落盘兜底 — 已完成（`internal/audit/fallback.go` + `service.go` WithFallback/Close，2026-08-02）
5. ✅ **结果准 - execution 查询 API**：新增 `GET /v1/executions` + `ListExecutions` — 已完成（`internal/store/action_plans.go` + `internal/httpapi/router.go` serveListExecutions，admin-only + 完整字段 + keyset 分页，2026-08-02）
6. **事件准 - R1 LLM 调用审计**（即缺口-5）：EinoPlanner / LLMFormatter 记录 `llm_invoked` 事件 — 待做，小改
7. **事件准 - R2/R3/R4 审计补齐**：HTTP 权限拒绝 / 登录登出 / 任务跳过过期 记录审计事件 — 中等改动，14 处 403 + 登录登出钩子

**收口项结论**：1-5 已完成，6-7 为剩余小改，做完后事件准的"收口级"红灯基本清零。

#### 专项项（独立功能或大重构，天级别）

- **告警准 - 告警数据源接入层**：六层全建（`internal/alert/` 包 + webhook 接入 + 归一化 + 存储表 migration + 关联配置 + 告警查询工具）。**等借鉴-5 解禁需求出现时启动**，不作为收口项推进
- ✅ **任务准 - 并发安全**：分布式锁 + 补跑过期保护 + AppendRun/UpdateTask 原子性 — 已完成（`internal/store/scheduled_tasks.go` ClaimTask + AppendRunAndUpdateTask + `internal/scheduler/scheduler.go` 过期跳过 + CAS 认领 + 原子事务，2026-08-02，详见 [专项 spec](./2026-08-02-task-concurrency-safety-spec.md)）
- **结果准 - output 脱敏重设计**：当前 ResultSummary 恒为 `{"outcome":"succeeded"}`，需重新设计脱敏策略保留真实输出
- **结果准 - dry-run / VerificationResult 落库**：dry-run 预览结果和验证结果持久化，支持事后复盘

**专项项结论**：这些是独立里程碑级工作，不应塞进"阶段 1 收口"节奏。各自单独规划。

---

## 更新后的优先级总览

| 编号 | 名称 | 优先级 | 来源 | 与原缺口关系 | 状态 |
|------|------|--------|------|-------------|------|
| 缺口-1 | 进度事件 | 高 | 实现说明 | — | ✅ 已完成 |
| 缺口-3 | 页面上下文带入 | 高 | 实现说明 | — | ✅ 已完成（含 ActionRouter 兜底增强） |
| 借鉴-1 | 系统态势 SLA 入口 | 高 | promo slide 4 | 与缺口-3 互补 | ✅ 已完成 |
| 借鉴-2 | 咨询/生成/执行 intent 分类 | 高 | promo slide 7 | 强化缺口-5 | ✅ 已完成 |
| 缺口-2 | 外部 MCP 接入层 | 中 | 实现说明 | — | ✅ 已完成（含热配置 + 健康检查） |
| 缺口-4 | 事实集聚合 | 中 | 实现说明 | — | ✅ 已完成 |
| 借鉴-3 | 任务草稿自动补齐执行策略 | 中 | promo slide 7 | dry-run 横向扩展 | ✅ 已完成 |
| 借鉴-4 | 事件中心最终结果过滤 | 中 | promo slide 6 | 巡检报告优化 | ✅ 已完成 |
| 借鉴-5 | Runbook / 命令模板复用 | 中 | promo slide 14 | 阶段 3 落地载体 | ✅ 已完成（2026-08-02，低风险自动执行） |
| 借鉴-6 | 外部 MCP 健康检查 | 中 | promo slide 14 | 细化缺口-2 | ✅ 已完成（随缺口-2 完成） |
| 借鉴-7 | 先可信再自动三阶段 | 战略 | promo slide 14 | 统领优先级 | 🟢 阶段1四项全绿 + 专项项全清 + 借鉴-5 已落地（进入阶段 3：低风险高频动作纳入自动化闭环） |
| 缺口-5 | 模型成本审计（LLM 调用） | 低 | 实现说明 | — | ✅ 已完成（llm_invoked + 403 + 登录登出审计） |

**事件准收口项（R1/R2/R3/R4）全部清零（2026-08-02）**：
- R1/缺口-5：`EinoPlanner`/`LLMFormatter`/`LLMCompactor` 记录 `llm_invoked`（model/tokens/latency）
- R2：17 处角色检查 403 + assistant/plan 确认 403 记 `http_forbidden`（`writeForbidden`/`recordForbidden`）
- R3：CAS callback/logout 记 `auth_login`/`auth_logout`
- R4：scheduler 跳过过期任务记 `scheduled_task_skipped`（此前已实现）

**结果准专项完成（2026-08-02）**：
- output 脱敏重设计：#4 保留真实 executor 输出 + 递归剥离敏感键 + 10KB 上限
- dry-run / VerificationResult 落库：#5 `action_plans.dry_run` + `tool_executions.verification` JSON 列（migration 013）

**下一步建议**：
- 高优先级剩余项为借鉴-1（系统态势 SLA 入口），与缺口-3 互补——缺口-3 解决"用户已知在哪"，态势 SLA 解决"用户不知道在哪"。
- 缺口-5（模型成本审计）可借助借鉴-2 的 IntentType 分类按类聚合成本，作为下一个低优先级推进项。
- 借鉴-7 的三阶段框架作为战略校准：当前处于阶段 2（AI 负责证据整合、结构化输出、动作草稿生成），阶段 1（可信事实链路，即数据源管理）完工前暂缓借鉴-5（Runbook 自动化）。

---

## 附录 C：P2 Action/Skill 扩展（2026-08-02）

在缺口-1~4 完成后，本轮同步推进了 P2 Action/Skill 扩展，丰富治理链路覆盖面。所有扩展均按 TDD 推进（先改测试断言→红灯→补实现→绿灯→全量回归）。

### 新增 Action（5 个，总数 10→15）

| Action Code | DisplayName | RiskLevel | AgentMode | Skills |
|---|---|---|---|---|
| `cost.analyze` | 成本分析 | read_only | react | cost-analysis-guide, risk-assessment-guide |
| `sla.analyze` | SLA 分析 | read_only | react | sla-analysis-guide |
| `incident.review` | 事故复盘 | read_only | react | incident-review-sop |
| `health.check` | 健康巡检 | read_only | react | health-check-guide |
| `performance.bottleneck` | 性能瓶颈定位 | read_only | react | performance-bottleneck-guide |

### 新增 Skill（5 个，总数 12→17）

| Skill Slug | Category | ApplicableActions |
|---|---|---|
| `cost-analysis-guide` | 成本管理 | cost.analyze |
| `sla-analysis-guide` | SLA 管理 | sla.analyze |
| `incident-review-sop` | 事故复盘 | incident.review |
| `health-check-guide` | 健康巡检 | health.check |
| `performance-bottleneck-guide` | 性能排障 | performance.bottleneck |

### eval 套件扩容（90→100 条）

在 diagnostic 类新增 10 条 P2 场景用例（cost/sla/incident/health/performance 各 2 条），覆盖 P2 Action 关键词命中和 diagnostic 路径产出。修复预先存在的 `fuzzy_en_status` 用例（confidence 0.65→0.75，通过 planner 0.7 阈值）。eval 套件全绿：tool 类 100% 通过，clarification/diagnostic/history 类 ≥90% 通过。

### 关键设计点

- **最长关键词优先策略验证**：`incident.review` 用"事故复盘"而非宽泛的"事故"避免与 `log.query_generate` 的"生成"冲突；`health.check` 用"健康巡检"(长度4) 胜过 `middleware.diagnose` 的"健康"(长度2)。
- **无 trace 退化策略**：`performance-bottleneck-guide` 明确写了无 trace 退化策略——候选瓶颈标注"疑似"而非"确认"，建议补 APM 数据。
- **ActionRouter 感知 pageContext**：`Route` 签名增加 `pageContext`，消息未命中时用 `LookupAction(pageContext.Domain)` 兜底，复用现有匹配零硬编码，message 始终优先。
