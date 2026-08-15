# 架构设计决策（Architecture Decisions）

本文记录 AIOps Copilot 关键架构决策，供团队接手与后续演进参考。每条含
**背景 → 决策 → 实现要点 → 演进约束**。

---

## 1. 域（domain）抽象：注册表派生 + 优雅降级

**背景**：早期把 glusterfs/minio/kafka 等中间件域硬编码在生产逻辑与 LLM
提示词里（别名、前缀推断、常量、推荐逻辑）。任何"内置假设某域存在"的地方，
在接入真实系统时都会漂移——系统说支持 kafka，实际没有数据源。

**决策**：域是**注册表（tools 注册表）的投影**，不再是内置假设。

- `tools.KnownDomains()` 是域名唯一来源，派生自静态工具 + 动态工具（已发布
  能力 / MCP）的 `Domain` 字段去重。
- 发布能力 = 注册域 = 全链路生效：多域拆分（orchestrator）、诊断 SOP 路由
  （resolveRunbook）、修复推荐（FindDomainWriteTool）、告警分类、LLM 提示词
  枚举自动获得该域，无需改代码。
- **优雅降级**：域未接入任何能力时，`diagnostics` 不返回错误，而是返回
  **通用诊断框架包**（`genericDiagnostic`，`Data["framework"]=true`）——按
  runbook 给出通用 SOP 检查维度 + "发布该域能力可获得真实指标"引导。用户永远
  得到结构化响应，而不是 400 报错。

**演进约束**：新增中间件只写 YAML 能力并发布，不碰 Go 代码；被删除的常量
（`GlusterVolumeHealthRead` 等）不得复活为硬编码。

---

## 2. 诚实降级原则（不伪造数据）

**背景**：排查发现多处"没有真实数据却返回健康/成功"的路径——`cluster.status.read`
谎报 `available`、K8s 工具未配置时返回 error JSON + nil error、知识库 Outcome 恒为
`success`、MCP 审计事件丢失被静默吞掉。

**决策**：全链路贯彻**没有真实数据就如实说"未知/未接入/失败"，绝不编造**。

- 静态读工具无数据源时返回 `status:"unknown" + note:"no data sources configured"`，
  而不是 `available`。
- 工具配置缺失返回真实 error，而不是 error JSON + nil error（假成功）。
- 知识库落库失败、审计记录失败、环境别名刷新失败均打日志，不静默。
- 诊断框架包在 assistant 摘要中标注"非实测数据"，链式研判标 `Degraded`。

**演进约束**：新增 runner/executor 时，无数据源必须走"诚实降级"而非 stub 值；
`status:"ok"/"available"` 类字面量必须有真实来源。

---

## 3. 多智能体：角色分派层（Supervisor + AgentRole）

**背景**：单一 AgentExecutor（ReAct 循环）无法表达"只读取证 / 写操作纪律 /
量化分析"等不同职责边界。

**决策**：复用**同一 AgentExecutor**，通过 `AgentRole` 注入不同 system prompt
边界——不引入独立进程/模型，避免过度设计。

- 5 个角色：`supervisor`（编排者，默认）、`diagnostic`（只读取证）、`change`
  （写操作 fail-closed）、`analysis`（量化计算）、`knowledge`（知识检索）。
- 15 个 Action 各带 `AgentRole` 字段；`Supervisor.Dispatch` 按消息路由到 Action
  → 取角色 → 执行。未命中 Action 回退 supervisor（向后兼容）。
- 角色提示词定义职责边界、输出结构、安全纪律，不重复工具枚举。

**演进约束**：新 Action 必须填 `AgentRole`；角色提示词保持与注册表派生一致，
不硬编码具体中间件。

---

## 4. 诊断 → 推荐 → 处置闭环

**背景**：诊断产出推荐后，落地是静默 best-effort——只取第一条、失败无原因、
告警自动研判只出结论不建处置 plan。

**决策**：推荐落地**可追踪、可归因、全覆盖**。

- `Response.Recommendations` 记录每条可执行推荐的状态：`plan_created` /
  `read_executed` / `skipped`（带原因：工具未注册 / 策略拒绝 / 建 plan 失败）。
  `RecommendationPlan` 兼容保留，指向首个成功 plan。
- **多推荐全部处理**（`actionableRecommendations` 遍历），不再只取第一条。
- **告警处置闭环**：`auto_diagnose` 注入 `RecommendationPlanCreator`，诊断产出
  可执行推荐时自动建待确认 plan，结果与失败原因写回告警 description。
  写操作始终等待人工确认，不自动执行。

**演进约束**：任何"诊断给建议"的路径都必须把结果（含失败原因）反馈给用户，
不允许静默消失。

---

## 5. 可信执行通道的安全性

**背景**：MCP server 把外部 AI 客户端一律当作 admin 读全环境，且审计事件丢失；
scheduler 的可信只读无 read 级审计。

**决策**：

- **MCP Bearer 鉴权**：配置 `COPILOT_MCP_SERVER_TOKEN` 后，`/mcp` 请求必须带
  `Authorization: Bearer <token>`（恒定时间比较），否则 401。未配置时启动 Warn
  醒目提示高风险。
- **可信读可审计**：`ExecuteTrustedReadAs(ctx, user, ...)` 以任务配置者身份做
  read 级审计（对齐 `ExecuteRead`），未注册 capability 审计标记 rejected。
  scheduler 传 `scheduledAdminIdentity(task)` 执行。

**演进约束**：新增对外暴露的只读/执行通道时，必须带鉴权 + 审计，禁止匿名 admin。

---

## 6. 测试与验证纪律

- 域/能力相关逻辑用**注册表派生**验证，不依赖硬编码常量。
- 所有降级路径有明确测试（框架包标记、skipped+reason、认证 401、可信读审计）。
- 全量验证：`go build ./...`、`go vet ./...`、`go test ./...`。
- 测试域工具（`demo.*`）用 `tools.ResetDynamicToolsForTest()` 隔离，测试后清理。
