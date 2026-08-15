package store

import (
	"context"
	"log"
)

// builtinSkills 是 P0 内置 Skill 种子数据，对齐 SxDevOps AIOps 2.0 的 Skill 库设计。
// 参考 SxDevOps 的 sx-alert-evidence-checklist、sx-log-query-guide 等 Skill。
// 每个内置 Skill 都标注 IsBuiltin=true，可通过 SeedBuiltinSkills 幂等播种。
var builtinSkills = []Skill{
	{
		Slug:              "middleware-evidence-checklist",
		Name:              "中间件诊断证据清单",
		Category:          "中间件排障",
		Description:       "约束中间件诊断时必须输出的证据结构和取证顺序",
		ApplicableActions: []string{"middleware.diagnose"},
		ToolDependencies:  []string{"cluster.status.read"},
		Content: `# 中间件诊断证据清单 SOP

## 取证顺序
1. 集群整体状态（cluster.status.read）：确认集群是否可达、节点数、健康状态
2. 域级健康检查（按已发布能力的 domain 选择对应只读工具）
3. 交叉验证：如果指标异常，检查是否有近期变更或事件

## 必须输出
- **结论**：一句话说明诊断结果（健康/异常+原因）
- **证据**：列出查到的关键指标值和状态
- **影响范围**：受影响的服务、环境、资源
- **下一步动作**：建议的处理措施（只读建议，不直接执行）

## 安全边界
- 只读取证，不执行任何写操作
- 不暴露连接字符串或凭证
- 指标异常时给出可能原因候选，不下单一结论`,
		OutputContract: "结论 + 证据列表 + 影响范围 + 下一步动作",
		RiskLevel:      "read_only",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "alert-evidence-checklist",
		Name:              "告警根因分析证据清单",
		Category:          "告警排障",
		Description:       "约束告警根因分析时必须收集的证据和分析步骤",
		ApplicableActions: []string{"alert.root_cause"},
		ToolDependencies:  []string{"cluster.status.read"},
		Content: `# 告警根因分析证据清单 SOP

## 分析步骤
1. **告警识别**：确认告警类型、严重程度、触发条件
2. **时间线取证**：告警触发时间、持续时间、是否恢复
3. **影响面评估**：受影响的服务、环境、用户范围
4. **关联分析**：
   - 同时间段是否有其他告警（关联告警）
   - 是否有近期变更（发布、配置修改、扩缩容）
   - 资源用量是否异常（CPU、内存、磁盘、网络）
5. **根因假设**：基于证据列出可能原因候选，按可能性排序

## 根因假设增强（必须执行）
- 至少输出 3 个候选根因（按可能性排序）。
- 每个候选根因必须给出字段：
  1) 候选根因类型（如：超时/限流/依赖不可用/资源耗尽/OOM/网络抖动等）
  2) 支持证据（Prometheus/告警 + 指标异常时间窗；说明最早异常点）
  3) 支持证据（Loki 日志关键词 + 出现时间窗 + 影响对象：instance/pod/namespace）
  4) CMDB 归属证据（业务/服务/主机/集群/模块）
  5) 反证或缺失（明确说明：证据不足则写“缺失/无法排除”，并说明缺失如何降低置信度）
  6) 置信度评分（0-100），并说明评分由哪些因子决定
- 最终“最可能根因”只允许从候选中选择（不得凭空新增根因），并解释：
  - 为什么它得分最高
  - 为什么其他候选得分更低（要么缺证据，要么与时间/对象不一致）

## 必须输出
- **候选根因表**：至少 3 条 + 每条字段（类型/证据/归属/反证/得分）
- **最可能根因**：置信度 + 判断依据（引用候选根因表中的证据）
- **证据**：时间线 + 指标值 + 关联事件
- **影响范围**：受影响的服务和用户
- **建议动作**：处理建议（只读，不直接执行）

## 安全边界
- 证据不足时明确说明不足，不臆测
- 不执行任何写操作或自愈动作
- 高风险操作必须走待确认流程`,
		OutputContract: "候选根因表（≥3）+ 最可能根因 + 证据时间线 + 影响范围 + 建议动作",
		RiskLevel:      "read_only",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "log-query-guide",
		Name:              "日志查询生成规范",
		Category:          "日志查询",
		Description:       "约束日志查询生成时的字段使用、过滤条件和输出格式",
		ApplicableActions: []string{"log.query_generate"},
		ToolDependencies:  []string{"cluster.status.read"},
		Content: `# 日志查询生成规范 SOP

## 查询生成原则
1. **优先用结构化字段**：service、level、trace_id、span_id、pod、namespace
2. **时间窗口**：默认最近 15 分钟，可根据问题调整
3. **过滤条件**：从宽到窄，先看 error/warn 再缩小范围
4. **聚合方式**：大量日志时先按 service+level 聚合计数，再下钻样本

## 必须输出
- **查询语句**：可直接复制使用的完整查询（LogQL/SQL/关键词）
- **字段说明**：查询中用到的字段含义
- **过滤项**：每个过滤条件的作用
- **预期结果**：查询会返回什么类型的数据

## 字段字典
- service：服务名（如 order-center、payment-service）
- level：日志级别（error/warn/info/debug）
- trace_id：链路追踪 ID，用于跨服务关联
- span_id：单次调用跨度 ID
- pod：K8s Pod 名
- namespace：K8s 命名空间

## 安全边界
- 查询语句不包含敏感信息（密码、token）
- 不执行破坏性查询（DELETE/DROP）
- 大范围查询需提示可能耗时`,
		OutputContract: "查询语句 + 字段说明 + 过滤项 + 预期结果",
		RiskLevel:      "read_only",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	// --- P1 扩展 Skill（9 个）---
	{
		Slug:              "capacity-planning-guide",
		Name:              "容量规划指南",
		Category:          "容量规划",
		Description:       "约束容量规划时的数据采集、趋势分析和扩容建议输出",
		ApplicableActions: []string{"capacity.plan"},
		ToolDependencies:  []string{"cluster.status.read"},
		Content: `# 容量规划 SOP

## 数据采集
1. 当前用量：集群/存储/消息的已用容量、对象数、分区数
2. 历史趋势：近 7/30 天增长率（如无可估算为近期平均）
3. 资源上限：物理容量、配额、副本数

## 分析步骤
1. 计算剩余可用容量 = 上限 - 已用
2. 按当前增长率推算耗尽时间
3. 给出扩容阈值建议（通常在剩余 20% 时触发）

## 必须输出
- **现状**：当前用量、上限、剩余比例
- **趋势**：增长率、预计耗尽时间
- **建议**：扩容时机、扩容规模、资源预留
- **成本**：预估扩容成本（如可估算）

## 安全边界
- 只读采集，不执行扩容
- 趋势数据不足时明确说明，不臆测
- 扩容建议标注为"建议"，需人工确认`,
		OutputContract: "现状 + 趋势 + 建议 + 成本",
		RiskLevel:      "draft",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "config-change-checklist",
		Name:              "配置变更检查清单",
		Category:          "配置管理",
		Description:       "约束配置变更对比时的 diff 输出、影响面评估和回滚点",
		ApplicableActions: []string{"config.diff"},
		ToolDependencies:  []string{"cluster.status.read"},
		Content: `# 配置变更检查清单 SOP

## 对比步骤
1. 获取 before/after 配置（版本号、时间戳、来源）
2. 逐字段 diff，标注变更类型（新增/修改/删除）
3. 识别敏感字段（密码、token、连接串）变更

## 影响面评估
1. 哪些服务依赖该配置
2. 变更是否需要重启/重载
3. 是否有灰度策略

## 必须输出
- **diff**：before/after 对比，逐项列出
- **影响面**：受影响的服务和组件
- **回滚点**：回滚到的目标版本和步骤
- **风险等级**：低/中/高 + 理由

## 安全边界
- 敏感字段在 diff 中脱敏
- 不直接执行配置变更
- 高风险变更需双人确认`,
		OutputContract: "diff + 影响面 + 回滚点 + 风险等级",
		RiskLevel:      "read_only",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "release-rollback-sop",
		Name:              "发布回滚 SOP",
		Category:          "发布管理",
		Description:       "约束发布回滚时的版本确认、步骤、影响范围和数据兼容性",
		ApplicableActions: []string{"release.rollback"},
		ToolDependencies:  []string{"cluster.status.read"},
		Content: `# 发布回滚 SOP

## 回滚前置确认
1. 当前版本号、目标回滚版本号
2. 回滚原因（发布失败/性能回退/功能异常）
3. 数据兼容性：回滚版本是否兼容当前数据 schema

## 回滚步骤
1. 通知相关方（停止写流量）
2. 按顺序回滚：应用 → 配置 → 数据（如有）
3. 验证回滚后健康状态
4. 恢复流量

## 必须输出
- **回滚计划**：目标版本 + 步骤 + 验证点
- **影响范围**：受影响服务和用户
- **数据风险**：是否有数据丢失/不一致风险
- **应急联系人**：回滚失败时的升级路径

## 安全边界
- 回滚是高风险操作，必须人工二次确认
- 数据不兼容时优先保留数据，不强制回滚
- 回滚失败立即升级，不自动重试`,
		OutputContract: "回滚计划 + 影响范围 + 数据风险 + 应急联系人",
		RiskLevel:      "execute",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "self-heal-recommendation-guide",
		Name:              "自愈推荐指南",
		Category:          "自愈",
		Description:       "约束自愈动作推荐时的证据依据、风险标注和确认流程",
		ApplicableActions: []string{"self.heal"},
		ToolDependencies:  []string{"cluster.status.read"},
		Content: `# 自愈推荐指南 SOP

## 推荐原则
1. 每个自愈动作必须基于明确证据（指标异常 + 根因假设）
2. 标注风险等级（low/medium/high）和可逆性
3. 优先推荐可逆、低风险动作

## 推荐流程
1. 列出诊断证据
2. 匹配可执行的自愈动作（重启 pod、扩缩容、清理临时文件等）
3. 评估每个动作的影响范围和副作用
4. 按风险从低到高排序

## 必须输出
- **证据**：触发自愈的指标和根因
- **推荐动作**：动作描述 + 风险等级 + 可逆性
- **影响范围**：受影响的服务和资源
- **确认要求**：是否需要人工确认（medium/high 必须）

## 安全边界
- 不自动执行 high 风险动作
- 自愈动作必须走 action plan 确认流程
- 失败后不自动重试，升级人工`,
		OutputContract: "证据 + 推荐动作 + 影响范围 + 确认要求",
		RiskLevel:      "write",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "dashboard-design-guide",
		Name:              "仪表盘设计指南",
		Category:          "可观测性",
		Description:       "约束仪表盘草稿生成的指标选择、布局和阈值设计",
		ApplicableActions: []string{"dashboard.generate"},
		ToolDependencies:  []string{"cluster.status.read"},
		Content: `# 仪表盘设计指南 SOP

## 指标选择
1. 核心指标：可用性、延迟、错误率、饱和度（USE/RED 方法）
2. 业务指标：吞吐量、成功率、关键路径延迟
3. 资源指标：CPU、内存、磁盘、网络

## 布局原则
1. 顶层：核心 SLI 摘要（健康状态一览）
2. 中层：关键指标趋势图（按服务分组）
3. 底层：明细表（异常 pod、慢请求、错误日志）

## 必须输出
- **面板列表**：每个面板的指标、查询语句、图类型
- **阈值**：告警阈值和颜色标注
- **布局**：面板排列顺序和分组
- **时间范围**：默认时间窗口和刷新频率

## 安全边界
- 查询语句不含敏感信息
- 高基数指标用聚合而非明细
- 标注数据来源和采集频率`,
		OutputContract: "面板列表 + 阈值 + 布局 + 时间范围",
		RiskLevel:      "draft",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "alert-rule-draft-guide",
		Name:              "告警规则草稿指南",
		Category:          "告警",
		Description:       "约束告警规则草稿的指标、阈值、持续时间和通知渠道设计",
		ApplicableActions: []string{"alert.rule.draft"},
		ToolDependencies:  []string{"cluster.status.read"},
		Content: `# 告警规则草稿指南 SOP

## 规则设计
1. 指标选择：基于 SLI（可用性、延迟、错误率）或资源饱和度
2. 阈值：基于历史基线 + 容量上限，避免抖动
3. 持续时间：避免瞬时抖动误报（通常 1-5 分钟）
4. 严重程度：critical/warning/info 分级

## 必须输出
- **规则名**：清晰描述触发条件
- **指标查询**：PromQL/LogQL 表达式
- **阈值 + 持续时间**：触发条件和窗口
- **通知渠道**：钉钉/电话/邮件 + 分级路由
- **runbook**：告警处理指引链接

## 安全边界
- 阈值需基于数据，不臆测
- 避免高基数标签导致告警风暴
- 草稿需人工评审后才能上线`,
		OutputContract: "规则名 + 指标查询 + 阈值 + 通知渠道 + runbook",
		RiskLevel:      "draft",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "knowledge-retrieval-guide",
		Name:              "知识检索指南",
		Category:          "知识库",
		Description:       "约束知识库问答时的检索、引用和答案组织",
		ApplicableActions: []string{"knowledge.qa"},
		ToolDependencies:  []string{"cluster.status.read"},
		Content: `# 知识检索指南 SOP

## 检索流程
1. 解析用户问题，提取关键词和意图
2. 在运维知识库中检索相关文档（SOP、runbook、事故复盘）
3. 按相关性排序，取 top 3-5

## 答案组织
1. 直接回答用户问题，引用来源文档
2. 补充相关注意事项和前置条件
3. 提供下一步操作建议

## 必须输出
- **答案**：基于知识库的直接回答
- **来源**：引用的文档名/链接
- **注意事项**：前置条件、风险提示
- **延伸**：相关文档推荐

## 安全边界
- 知识库无相关内容时明确说明，不编造
- 不执行知识库中描述的写操作，只提供建议
- 引用必须标注来源，便于核查`,
		OutputContract: "答案 + 来源 + 注意事项 + 延伸",
		RiskLevel:      "read_only",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "k8s-action-checklist",
		Name:              "K8s 操作检查清单",
		Category:          "K8s 运维",
		Description:       "约束 K8s 操作（重启、扩缩容、回滚）的影响评估和确认流程",
		ApplicableActions: []string{"self.heal", "release.rollback"},
		ToolDependencies:  []string{"cluster.status.read"},
		Content: `# K8s 操作检查清单 SOP

## 操作前检查
1. 确认目标资源（deployment/pod/configmap）和命名空间
2. 评估副本数和可用性（是否低于最小可用）
3. 检查是否有进行中的发布或告警

## 操作分类
1. **重启 pod**（low）：rolling restart，影响小
2. **扩缩容**（medium）：影响负载和成本
3. **回滚 deployment**（high）：影响版本和数据兼容

## 必须输出
- **目标资源**：kind/name/namespace
- **操作类型**：重启/扩缩容/回滚 + 风险等级
- **影响评估**：受影响的服务、副本数变化
- **验证点**：操作后健康检查方式

## 安全边界
- high 风险操作必须人工确认
- 扩缩容不超过资源配额上限
- 回滚前确认数据兼容性`,
		OutputContract: "目标资源 + 操作类型 + 影响评估 + 验证点",
		RiskLevel:      "write",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "risk-assessment-guide",
		Name:              "风险评估指南",
		Category:          "风险治理",
		Description:       "约束高风险操作的风险评估、缓解措施和确认要求",
		ApplicableActions: []string{"capacity.plan", "release.rollback", "self.heal"},
		ToolDependencies:  []string{"cluster.status.read"},
		Content: `# 风险评估指南 SOP

## 风险分级
- **low**：只读或可逆，无副作用（查询、重启单个 pod）
- **medium**：可逆但有短暂影响（扩缩容、配置重载）
- **high**：不可逆或大范围影响（回滚、数据迁移、删除）

## 评估维度
1. 影响范围：受影响的服务、用户、数据
2. 可逆性：能否回滚，回滚成本
3. 副作用：级联影响、依赖服务
4. 时间窗口：是否在业务高峰

## 必须输出
- **风险等级**：low/medium/high + 判定理由
- **影响范围**：受影响的服务和用户
- **缓解措施**：灰度、回滚点、监控加严
- **确认要求**：是否需双人审批、变更窗口

## 安全边界
- high 风险必须双人审批 + 变更窗口
- 评估不足时默认升级为更高风险等级
- 风险评估必须先于操作执行`,
		OutputContract: "风险等级 + 影响范围 + 缓解措施 + 确认要求",
		RiskLevel:      "read_only",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	// --- P2 扩展 Skill（1 个）---
	{
		Slug:              "cost-analysis-guide",
		Name:              "成本分析指南",
		Category:          "成本治理",
		Description:       "约束成本分析时的资源用量采集、闲置识别和优化建议输出",
		ApplicableActions: []string{"cost.analyze"},
		ToolDependencies:  []string{"cluster.status.read"},
		Content: `# 成本分析 SOP

## 数据采集
1. 资源清单：集群节点、存储卷、bucket、消息队列等资源总量
2. 用量数据：每个资源的实际使用量（容量、对象数、分区数、CPU/内存）
3. 利用率：已用/上限比例，识别长期低利用率的资源
4. 计费口径：如可获取，标注资源对应的成本档位

## 闲置识别规则
1. **存储类**（分布式存储 / 对象存储）：容量利用率 < 20% 且近 30 天无写入
2. **计算类**：节点 CPU/内存平均利用率 < 10%
3. **消息类**（消息队列）：分区长期无消费或消费延迟持续为 0
4. **空资源**：已创建但无业务使用的存储卷/对象桶/消息主题

## 分析步骤
1. 列出所有资源及其利用率
2. 按闲置识别规则筛选候选资源
3. 对每个候选资源评估下线/缩容的影响面（是否有依赖、是否在变更窗口内）
4. 给出优化动作（下线、缩容、降配）和预估节省

## 必须输出
- **资源清单**：资源名 + 类型 + 当前用量 + 利用率
- **闲置资源**：候选列表 + 命中的闲置规则 + 证据
- **优化建议**：动作 + 预估节省 + 影响面 + 风险等级
- **确认要求**：是否需人工确认（下线/缩容类必须人工确认）

## 安全边界
- 只读采集，不直接执行下线/缩容
- 闲置判定必须基于数据，不臆测
- 下线动作标注为"建议"，需人工确认后由对应 Action 执行
- 不暴露具体金额，仅给出相对节省比例或档位`,
		OutputContract: "资源清单 + 闲置资源 + 优化建议 + 确认要求",
		RiskLevel:      "read_only",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "sla-analysis-guide",
		Name:              "SLA 分析指南",
		Category:          "SLA 治理",
		Description:       "约束 SLA 分析时的 SLI 采集、达成度计算和违反风险预警",
		ApplicableActions: []string{"sla.analyze"},
		ToolDependencies:  []string{"cluster.status.read"},
		Content: `# SLA 分析 SOP

## SLI 采集
1. **可用性**：成功请求数 / 总请求数（按服务聚合）
2. **延迟**：P95/P99 延迟趋势，与 SLO 阈值对比
3. **错误率**：5xx/4xx 占比，按接口分组
4. **采集窗口**：近 7/30 天滚动窗口 + 当日实时

## 达成度计算
1. 计算 error budget（剩余预算 = 1 - SLO 目标）
2. 按当前消耗速率推算预算耗尽时间
3. 标注是否已违反或临近违反（预算剩余 < 20%）

## 违反风险识别
1. 趋势：SLI 是否持续恶化
2. 突发：是否有近期异常尖刺
3. 关联：是否伴随变更/告警/容量压力

## 必须输出
- **SLI 现状**：可用性/延迟/错误率 + 当前值与 SLO 对比
- **达成度**：error budget 消耗比例 + 预计耗尽时间
- **违反风险**：等级（正常/关注/预警/已违反）+ 依据
- **改进建议**：优化方向（限流/扩容/降级/修缺陷）+ 优先级

## 安全边界
- 只读采集，不执行调参
- SLI 数据不足时明确说明，不臆测
- 风险等级必须基于数据趋势，不主观判定`,
		OutputContract: "SLI 现状 + 达成度 + 违反风险 + 改进建议",
		RiskLevel:      "read_only",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "incident-review-sop",
		Name:              "事故复盘 SOP",
		Category:          "事故管理",
		Description:       "约束事故复盘时的时间线还原、根因分析和改进项落地",
		ApplicableActions: []string{"incident.review"},
		ToolDependencies:  []string{"cluster.status.read"},
		Content: `# 事故复盘 SOP

## 时间线还原
1. **事故窗口**：开始时间、检测时间、恢复时间、持续时长
2. **关键事件**：按时间轴排列告警、变更、自愈动作、人工介入
3. **影响面**：受影响服务、用户范围、业务损失（如可量化）

## 根因分析
1. **直接原因**：触发故障的最直接技术原因
2. **促成因素**：变更、配置、容量、依赖等放大因素
3. **深层原因**：流程/监控/预案缺失等系统性问题
4. 用 5-Why 或鱼骨图溯因，避免停在表层

## 必须输出
- **时间线**：事件轴 + 每个节点的时间/动作/负责人
- **根因**：直接原因 + 促成因素 + 深层原因
- **影响**：服务/用户/业务损失
- **改进项**：按"防止复发/缩短检测/缩短恢复"分类，含负责人和截止日期

## 安全边界
- 复盘对事不对人，不归责个人
- 改进项必须可执行、可跟踪，不写空话
- 根因证据不足时标注为"假设"，不强行定论
- 不在复盘中暴露敏感凭证或客户数据`,
		OutputContract: "时间线 + 根因 + 影响 + 改进项",
		RiskLevel:      "read_only",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "health-check-guide",
		Name:              "健康体检指南",
		Category:          "巡检",
		Description:       "约束健康体检时的多维度巡检、健康评分和风险项输出",
		ApplicableActions: []string{"health.check"},
		ToolDependencies:  []string{"cluster.status.read"},
		Content: `# 健康体检 SOP

## 巡检维度
1. **集群层**：节点数、健康状态、资源水位（CPU/内存/磁盘）
2. **中间件层**：按已发布能力的 domain 选择对应健康检查工具
3. **SLI 层**：核心服务可用性、延迟、错误率（如可采集）
4. **告警层**：近 24 小时活跃告警数、未恢复告警

## 健康评分
1. 每个维度按 健康/关注/异常 三档评分
2. 整体健康分 = 各维度加权（集群 30% + 中间件 30% + SLI 25% + 告警 15%）
3. 整体等级：
   - 绿色（≥90）：健康
   - 黄色（70-89）：关注，有风险项需跟进
   - 红色（<70）：异常，需立即处理

## 风险项识别
1. **容量风险**：剩余 < 20% 或趋势异常
2. **可用性风险**：SLI 接近 SLO 阈值或 error budget 不足
3. **延迟风险**：P95/P99 持续走高
4. **积压风险**：消息队列消费延迟持续增长
5. **告警积压**：未恢复告警 > 3 或持续 > 1h

## 必须输出
- **健康评分**：整体分值 + 等级 + 各维度分项
- **巡检明细**：每个维度的关键指标值和状态
- **风险项**：列表 + 风险类型 + 严重程度 + 建议动作
- **跟进建议**：按优先级排序的下一步动作（只读建议）

## 安全边界
- 只读巡检，不执行任何写操作
- 数据不足的维度标注"数据缺失"，不臆测评分
- 风险项必须基于数据，不主观判定
- 评分仅作参考，不替代专业判断`,
		OutputContract: "健康评分 + 巡检明细 + 风险项 + 跟进建议",
		RiskLevel:      "read_only",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "performance-bottleneck-guide",
		Name:              "性能瓶颈定位指南",
		Category:          "性能优化",
		Description:       "约束性能瓶颈定位时的指标采集、瓶颈判定和优化建议输出（无 trace 时退化为指标维度）",
		ApplicableActions: []string{"performance.bottleneck"},
		ToolDependencies:  []string{"cluster.status.read"},
		Content: `# 性能瓶颈定位 SOP

## 指标采集
1. **资源指标**（按节点/Pod 聚合）：
   - CPU：使用率、负载（load1/load5/load15）、throttling 比例
   - 内存：使用率、OOM 次数、swap 使用
   - 磁盘：IOPS、吞吐、await、使用率
   - 网络：带宽、重传率、丢包率
2. **接口指标**（按服务/接口聚合）：
   - QPS/吞吐量趋势
   - 延迟：P50/P95/P99 + 趋势
   - 错误率：5xx 占比、超时数
3. **中间件指标**（按需）：消息消费延迟、存储容量/IOPS、慢操作

## 瓶颈判定规则
1. **CPU 瓶颈**：使用率 > 80% 持续 5min，或 throttling > 10%
2. **内存瓶颈**：使用率 > 85%，或 OOM 次数 > 0
3. **磁盘瓶颈**：await > 20ms，或使用率 > 90%
4. **网络瓶颈**：重传率 > 1%，或带宽打满 > 80%
5. **接口瓶颈**：P95 同比上涨 > 50%，或错误率 > 1%
6. **积压瓶颈**：消息队列消费延迟持续增长且消费吞吐不增

## 定位流程
1. **指标对比**：问题时间窗 vs 基线，找出异常维度
2. **关联分析**：
   - 有 trace：从慢调用入口下钻，串联下游服务定位真正瓶颈点
   - 无 trace：按"接口慢→资源高→中间件积压"顺序逐层排查，标注每层证据
3. **排除法**：排除非瓶颈维度（指标正常的明确排除）
4. **优先级**：按"影响面 × 异常程度"排序候选瓶颈

## 必须输出
- **瓶颈定位**：明确瓶颈点（资源/接口/中间件）+ 判定依据
- **证据链**：每层的指标值 + 与基线对比 + 时间窗
- **影响面**：受影响的服务/接口/用户
- **优化建议**：动作（扩容/限流/降级/索引优化/配置调参）+ 预期效果 + 风险等级

## 无 trace 退化策略
- 明确说明"无全链路 trace 数据，采用指标维度定位"
- 候选瓶颈点标注为"疑似"而非"确认"
- 建议补充 trace 数据或 APM 接入以精确定位

## 安全边界
- 只读采集，不执行调参
- 指标不足时明确说明数据缺失，不臆测瓶颈
- 优化建议标注为"建议"，高风险动作需人工确认
- 不下单一结论，瓶颈判定必须附证据链`,
		OutputContract: "瓶颈定位 + 证据链 + 影响面 + 优化建议",
		RiskLevel:      "read_only",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "alert-query-guide",
		Name:              "告警查询指引",
		Category:          "告警排障",
		Description:       "约束回答\"当前有哪些告警\"时使用 alert.query 工具、过滤条件和输出结构",
		ApplicableActions: []string{"alert.root_cause"},
		ToolDependencies:  []string{"alert.query", "cluster.status.read"},
		Content: `# 告警查询指引 SOP

## 查询原则
1. 优先用 alert.query 获取活动告警（status=firing），默认 environment=prod
2. 可按 severity / domain 过滤；关键告警（critical）优先展示
3. 结合 cluster.status.read 交叉验证集群整体健康

## 必须输出
- **当前告警数** + 每条告警的标题、严重级别、环境、状态、触发时间
- **影响面**：涉及的服务/环境/资源
- **建议**：对 critical 告警给出下一步排查方向（只读）

## 安全边界
- 只读查询，不执行任何写操作
- 不臆测未查询到的告警`,
		OutputContract: "告警列表（标题+级别+环境+状态+时间）+ 影响面 + 建议",
		RiskLevel:      "read_only",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "on-call-handover",
		Name:              "值班交接",
		Category:          "事件响应",
		Description:       "值班交接报告模板：当前状态、风险项、未决事项与后续行动",
		ApplicableActions: []string{"incident.review"},
		ToolDependencies:  []string{"alert.query", "event.query", "incident.view"},
		Content: `# 值班交接报告 SOP

## 交接必须覆盖
1. **当前状态**：集群/服务整体健康、进行中的告警（数量+级别+持续时间）
2. **风险项**：已知异常、未恢复的告警、近期变更（发布/配置/扩容）
3. **未决事项**：待跟进的问题、等待确认的动作、卡点与负责人
4. **后续行动**：下一班次应优先做的事项（按优先级排序）

## 格式
- 用时间线组织：本班次关键事件（触发/处理/恢复）
- 每条风险项标注：现象 → 已采取动作 → 残留风险 → 建议

## 安全边界
- 只读取证，不执行写操作
- 交接内容只含事实与判断，不含猜测`,
		OutputContract: "当前状态 + 风险项清单 + 未决事项 + 后续行动",
		RiskLevel:      "read_only",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "incident-severity-guide",
		Name:              "故障定级指引",
		Category:          "事件响应",
		Description:       "P1-P4 故障定级判定标准与升级路径",
		ApplicableActions: []string{"alert.root_cause", "incident.review"},
		ToolDependencies:  []string{"alert.query", "incident.view"},
		Content: `# 故障定级指引 SOP

## 定级标准（按影响面与影响时长判定）
- **P1（严重）**：核心服务不可用或数据丢失，影响全部/大部分用户，无规避方案
- **P2（高）**：主要功能受损，影响部分用户，有临时规避方案
- **P3（中）**：非核心功能异常或体验受损，影响小范围用户
- **P4（低）**：轻微问题，无用户可感知影响

## 升级路径
- P1/P2：立即通知值班负责人，启动应急响应群，10 分钟内拉起处置
- P3：2 小时内处置，记录复盘
- P4：进入常规工单队列

## 输出要求
- 给出定级结论 + 判定依据（影响面/影响时长/规避方案存在性）
- 给出升级建议（是否升级、升级给谁、升级时限）`,
		OutputContract: "定级结论 + 判定依据 + 升级路径建议",
		RiskLevel:      "read_only",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "triage-methodology",
		Name:              "排障方法论",
		Category:          "排障方法论",
		Description:       "结构化排障框架：二分定位、先外围后内核、问题定义优先",
		ApplicableActions: []string{"middleware.diagnose", "alert.root_cause", "performance.bottleneck"},
		ToolDependencies:  []string{"cluster.status.read", "alert.query"},
		Content: `# 结构化排障方法论 SOP

## 第一步：定义问题（不要急于动手）
- 明确"期望行为 vs 实际行为"的差异
- 确定影响范围：单机/单域/全局？何时开始？持续多久？

## 第二步：定位（二分 + 先外围后内核）
1. 先确认基础设施层（网络/存储/主机）正常，再进应用层
2. 二分：隔离到服务 → 实例 → 进程 → 配置，每次缩小一半范围
3. 复现优先：能复现的问题解决更快，不能复现的记录触发条件继续观察
4. 时间线对照：异常开始时间 vs 变更/发布/告警时间，重叠即重点怀疑

## 第三步：假设验证
- 基于证据列出候选根因，按可能性排序
- 每个候选根因必须有可验证的下一步取证动作
- 验证顺序：成本最低的验证先做

## 第四步：处置与复盘
- 处置动作可回滚优先；记录做了什么、效果如何
- 复盘记录：根因、误判路径、可改进的监控/告警

## 安全边界
- 全程只读取证；写操作需单独决策
- 不下单一结论，根因必须附证据链`,
		OutputContract: "问题定义 + 定位过程 + 候选根因（附证据）+ 处置建议",
		RiskLevel:      "read_only",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "rollback-first-mentality",
		Name:              "回滚优先决策准则",
		Category:          "变更管理",
		Description:       "回滚 vs 就地修复的决策准则：可回滚时优先回滚到已知良好状态",
		ApplicableActions: []string{"release.rollback", "self.heal"},
		ToolDependencies:  []string{},
		Content: `# 回滚优先决策准则 SOP

## 核心原则
- **可回滚时优先回滚**：回到已知良好状态，比就地修复更可控、更快、风险更低
- 就地修复只适用于：无版本可回、回滚成本高于修复成本、回滚会破坏数据一致性

## 决策流程
1. 是否可回滚？（有上一版本/快照/配置备份）
2. 回滚窗口内吗？（发布多久了——发布越久，回滚引起的数据漂移风险越大）
3. 回滚影响哪些用户？（短暂闪断 vs 持续异常）
4. 就地修复是否已验证？（修复方案在测试环境跑过吗？）
5. 数据一致性：回滚后是否需要数据迁移/补偿？

## 输出要求
- 明确推荐"回滚"或"就地修复" + 三条理由
- 若回滚：给出回滚目标版本、步骤、验证方式
- 若就地修复：说明为什么回滚不可行

## 安全边界
- 涉及写操作时必须等待用户确认
- 不确定数据一致性时，先取证再决策`,
		OutputContract: "决策结论（回滚/修复）+ 理由 + 执行步骤 + 验证方式",
		RiskLevel:      "execute",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "slo-budget-guide",
		Name:              "SLO 与错误预算",
		Category:          "SRE 工程",
		Description:       "SLO/SLI/错误预算计算口径，告警规则应绑定错误预算消耗",
		ApplicableActions: []string{"sla.analyze", "alert.rule.draft"},
		ToolDependencies:  []string{},
		Content: `# SLO 与错误预算 SOP

## 基本概念
- **SLI**：可度量的服务指标（可用性=成功请求/总请求，延迟=满足阈值的比例，错误率）
- **SLO**：承诺目标（如"可用性 99.9%"、"P99 延迟 < 200ms"）
- **错误预算**：1 - SLO，即允许出错的窗口（月内 99.9% → 43.2 分钟）

## 计算口径
- 错误预算消耗 = 累计错误时间 / 预算总量
- 告警规则必须绑定错误预算：**消耗快时告警（burn-rate 快烧），而不是等预算耗尽**
- 推荐分级：2 小时窗口 burn-rate >= 14.4（快烧告警）、6 小时窗口 >= 6、24 小时窗口 >= 2

## 输出要求
- 给出 SLI 口径、SLO 目标、当前达成率、错误预算剩余
- 预算消耗趋势：本月已消耗比例 + 按当前速率预计何时耗尽
- 告警建议：给出绑定 burn-rate 的告警阈值与窗口

## 安全边界
- 只读计算；不修改已配置的 SLO 与告警规则
- 假设不透明时注明数据口径（统计窗口、成功判定标准）`,
		OutputContract: "SLI 口径 + SLO 目标 + 达成率 + 预算剩余 + 告警阈值建议",
		RiskLevel:      "read_only",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "runbook-authoring-sop",
		Name:              "Runbook 编写规范",
		Category:          "SRE 工程",
		Description:       "编写可执行 runbook 的规范：前置条件、回滚、可测试性",
		ApplicableActions: []string{"config.diff", "release.rollback"},
		ToolDependencies:  []string{},
		Content: `# Runbook 编写规范 SOP

## 结构要求（每个 runbook 必须包含）
1. **触发条件**：什么现象/告警触发该 runbook（明确、可判断）
2. **前置检查**：执行前必须验证的条件（环境、版本、资源状态）
3. **执行步骤**：编号步骤，每步给出命令/工具与预期结果
4. **验证方式**：如何确认操作生效（只读工具复查）
5. **回滚步骤**：失败时如何回退到初始状态
6. **风险与升级**：风险等级、失败时联系谁

## 编写原则
- 每一步可独立执行、可验证，不依赖上下文中的隐含假设
- 执行失败要有明确的失败处理路径（回滚 or 升级），不允许"卡住"
- 工具序列必须引用注册表内真实存在的工具名，不造路由不到的工具
- 低风险 runbook 可自动执行；中高风险必须人工确认

## 输出要求
- 按上述 6 段结构输出 runbook 草稿
- 标注风险等级与建议的执行模式（自动/确认后执行）`,
		OutputContract: "六段式 runbook 草稿 + 风险等级 + 执行模式建议",
		RiskLevel:      "read_only",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "change-window-guide",
		Name:              "变更窗口评估",
		Category:          "变更管理",
		Description:       "变更窗口/影响面评估：业务高峰期规避、灰度范围、失败预案",
		ApplicableActions: []string{"config.diff", "release.rollback"},
		ToolDependencies:  []string{},
		Content: `# 变更窗口评估 SOP

## 评估维度
1. **时机**：是否避开业务高峰期？影响面大的变更优先低峰窗口
2. **范围**：全量 or 灰度？灰度的批次、间隔、验证点
3. **影响面**：变更影响的资源/服务/数据；回滚代价
4. **失败预案**：失败判定标准 + 回滚触发条件 + 升级联系人
5. **审计**：变更理由、审批人、执行记录必须可追溯

## 判断准则
- 高风险变更（数据迁移、核心配置、跨域变更）默认走审批+灰度
- 可回滚性差的变更必须扩大预检范围
- 与正在进行的故障处置冲突的变更应延期

## 输出要求
- 变更评估结论：可执行 / 需延期 / 需调整窗口
- 建议窗口与理由、灰度方案、失败预案`,
		OutputContract: "窗口结论 + 建议时机 + 灰度方案 + 失败预案",
		RiskLevel:      "read_only",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
	{
		Slug:              "audit-trail-guide",
		Name:              "审计留痕规范",
		Category:          "风险与合规",
		Description:       "写操作的审计留痕要求：决策理由、执行记录、结果验证均可追溯",
		ApplicableActions: []string{"config.diff", "release.rollback", "self.heal"},
		ToolDependencies:  []string{},
		Content: `# 审计留痕规范 SOP

## 留痕要求（所有写操作必须满足）
1. **决策理由**：为什么做这个变更（告警/需求/故障依据），谁批准
2. **变更内容**：执行了什么工具、改了哪些参数（before → after）
3. **执行记录**：操作人、时间、环境、执行结果（成功/失败/部分成功）
4. **结果验证**：变更后的验证证据（只读工具复查结果）
5. **失败记录**：失败详情、已做的补偿动作、遗留风险

## 原则
- 写操作一律走既有准入链路（预检 → 确认 → 执行 → 审计），不得绕过
- 高风险变更的决策理由必须包含风险与收益的权衡说明
- 审计记录应足以支持事后复盘与合规检查，禁止事后补写关键事实

## 输出要求
- 给出本次写操作的审计记录（上述 5 项）
- 标注是否需要人工复核（高风险/部分失败必标）`,
		OutputContract: "五段式审计记录 + 复核标注",
		RiskLevel:      "write",
		IsBuiltin:      true,
		IsEnabled:      true,
	},
}

// SeedBuiltinSkills 将内置 Skill 播种到 store 中。
// 该操作是幂等的：已存在的 Skill（按 slug 匹配）不会被重复创建。
// 启动时调用一次即可。
func SeedBuiltinSkills(ctx context.Context, s SkillStore) error {
	for _, skill := range builtinSkills {
		// 检查是否已存在（按 slug 查询）
		_, err := s.GetSkill(ctx, skill.Slug)
		if err == nil {
			// 已存在，跳过（幂等）
			continue
		}
		// 不存在则创建
		if _, err := s.CreateSkill(ctx, skill); err != nil {
			log.Printf("seed builtin skill %q: %v", skill.Slug, err)
			return err
		}
	}
	return nil
}
