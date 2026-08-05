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
		Description:       "约束中间件（kafka/minio/glusterfs）诊断时必须输出的证据结构和取证顺序",
		ApplicableActions: []string{"middleware.diagnose"},
		ToolDependencies:  []string{"cluster.status.read", "glusterfs.volume.health.read", "minio.bucket.health.read", "kafka.consumer.lag.read"},
		Content: `# 中间件诊断证据清单 SOP

## 取证顺序
1. 集群整体状态（cluster.status.read）：确认集群是否可达、节点数、健康状态
2. 域级健康检查（按 domain 选择对应工具）：
   - glusterfs → volume 健康状态、容量、副本分布
   - minio → bucket 健康状态、容量、对象数
   - kafka → consumer_group 延迟、分区状态
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
		ToolDependencies:  []string{"cluster.status.read", "glusterfs.volume.health.read", "minio.bucket.health.read"},
		Content: `# 容量规划 SOP

## 数据采集
1. 当前用量：集群/卷/bucket 的已用容量、对象数、分区数
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
		ToolDependencies:  []string{"cluster.status.read", "glusterfs.volume.health.read", "minio.bucket.health.read", "kafka.consumer_lag.read"},
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
		ToolDependencies:  []string{"cluster.status.read", "glusterfs.volume.health.read", "minio.bucket.health.read"},
		Content: `# 成本分析 SOP

## 数据采集
1. 资源清单：集群节点、存储卷、bucket、kafka 集群等资源总量
2. 用量数据：每个资源的实际使用量（容量、对象数、分区数、CPU/内存）
3. 利用率：已用/上限比例，识别长期低利用率的资源
4. 计费口径：如可获取，标注资源对应的成本档位

## 闲置识别规则
1. **存储类**（glusterfs volume / minio bucket）：容量利用率 < 20% 且近 30 天无写入
2. **计算类**：节点 CPU/内存平均利用率 < 10%
3. **消息类**（kafka）：分区长期无消费或消费延迟持续为 0
4. **空资源**：已创建但无业务使用的 bucket/volume/topic

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
		ToolDependencies:  []string{"cluster.status.read", "glusterfs.volume.health.read", "minio.bucket.health.read", "kafka.consumer_lag.read"},
		Content: `# 健康体检 SOP

## 巡检维度
1. **集群层**：节点数、健康状态、资源水位（CPU/内存/磁盘）
2. **中间件层**：
   - glusterfs：volume 健康、容量、副本分布
   - minio：bucket 健康、容量、对象数
   - kafka：consumer_group 延迟、分区状态
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
4. **积压风险**：kafka lag 持续增长
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
		ToolDependencies:  []string{"cluster.status.read", "glusterfs.volume.health.read", "minio.bucket.health.read", "kafka.consumer_lag.read"},
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
3. **中间件指标**（按需）：
   - kafka：消费延迟、生产 TPS
   - 存储类：容量、IOPS、慢操作

## 瓶颈判定规则
1. **CPU 瓶颈**：使用率 > 80% 持续 5min，或 throttling > 10%
2. **内存瓶颈**：使用率 > 85%，或 OOM 次数 > 0
3. **磁盘瓶颈**：await > 20ms，或使用率 > 90%
4. **网络瓶颈**：重传率 > 1%，或带宽打满 > 80%
5. **接口瓶颈**：P95 同比上涨 > 50%，或错误率 > 1%
6. **积压瓶颈**：kafka lag 持续增长且消费 TPS 不增

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
