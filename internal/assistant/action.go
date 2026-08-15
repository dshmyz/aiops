package assistant

import "strings"

// ActionRisk 标识 Action 的风险等级，决定是否需要预检、确认和审计。
// 对齐 SxDevOps AIOps 2.0 的 risk_level 字段。
type ActionRisk string

const (
	// ActionReadOnly 只读：直接执行，无需确认。
	ActionReadOnly ActionRisk = "read_only"
	// ActionDraft 草稿：生成候选参数，不落库。
	ActionDraft ActionRisk = "draft"
	// ActionWrite 写入：需要预检 + 确认 + 审计。
	ActionWrite ActionRisk = "write"
	// ActionExecute 执行：需要预检 + dry-run + 二次确认 + 审计。
	ActionExecute ActionRisk = "execute"
)

// AgentMode 决定 Action 的执行编排方式。
type AgentMode string

const (
	// AgentDirect 直接执行：单步工具调用，无 ReAct 循环。
	AgentDirect AgentMode = "direct"
	// AgentReAct 反思执行：LLM 多轮工具调用 + 反思。
	AgentReAct AgentMode = "react"
	// AgentPlanReAct 规划执行：先拆解计划再 ReAct 执行。
	AgentPlanReAct AgentMode = "plan_react"
)

// Action 是任务入口分类，回答"这是哪类任务、按什么流程做、风险边界是什么"。
// 参考 SxDevOps action_handlers.py 的 ActionHandler，但合并了路由匹配字段
// 和流程策略字段，使一个结构体承载完整的任务入口定义。
//
// Action 是纯代码注册表（不落库），因为任务入口分类是稳定的后端协议，
// 不需要运行时 CRUD。可复用的领域能力包由 Skill（数据库模型）承载。
type Action struct {
	// Code 是稳定编码，如 "middleware.diagnose"、"alert.root_cause"。
	Code string
	// DisplayName 是前端展示名称。
	DisplayName string
	// Keywords 是路由匹配关键词，消息命中任一关键词即匹配该 Action。
	Keywords []string
	// PromptHint 是注入 planner 提示词的上下文引导。
	PromptHint string
	// RiskLevel 决定是否需要预检、确认和审计。
	RiskLevel ActionRisk
	// AgentMode 决定执行编排方式。
	AgentMode AgentMode
	// AgentRole 决定执行时的智能体角色（影响 system prompt 边界）。
	// 未设置时按 RoleSupervisor（通用助手）处理。
	AgentRole AgentRole
	// Skills 是默认加载的 Skill slug 列表。
	Skills []string
}

// registeredActions 是 P0 内置 Action 注册表。
// 新增 Action 时在此追加，保持注册顺序（匹配时按顺序取第一个命中）。
var registeredActions = []Action{
	{
		Code:        "middleware.diagnose",
		DisplayName: "中间件诊断",
		Keywords:    []string{"健康", "状态", "health", "status", "容量", "capacity", "延迟", "lag", "中间件", "middleware"},
		PromptHint:  "中间件诊断上下文：优先识别 domain（以已注册/已发布能力的域为准）、environment、resource_type，按健康检查 SOP 取证。",
		RiskLevel:   ActionReadOnly,
		AgentMode:   AgentDirect,
		AgentRole:   RoleDiagnostic,
		Skills:      []string{"middleware-evidence-checklist"},
	},
	{
		Code:        "alert.root_cause",
		DisplayName: "告警根因分析",
		Keywords:    []string{"告警", "alert", "根因", "原因", "触发", "影响"},
		PromptHint:  "告警根因分析上下文：必须输出结论、证据、影响范围和下一步动作，按告警证据清单 SOP 取证。",
		RiskLevel:   ActionReadOnly,
		AgentMode:   AgentReAct,
		AgentRole:   RoleDiagnostic,
		Skills:      []string{"alert-evidence-checklist"},
	},
	{
		Code:        "log.query_generate",
		DisplayName: "日志查询生成",
		Keywords:    []string{"日志", "log", "查询", "query", "生成", "error", "warn"},
		PromptHint:  "日志查询生成上下文：约束查询语句、过滤项、字段解释和可复制输出，按日志查询规范生成。",
		RiskLevel:   ActionReadOnly,
		AgentMode:   AgentDirect,
		AgentRole:   RoleDiagnostic,
		Skills:      []string{"log-query-guide"},
	},
	// --- P1 扩展 Action（7 个）---
	{
		Code:        "capacity.plan",
		DisplayName: "容量规划",
		Keywords:    []string{"容量规划", "扩容", "容量评估", "capacity planning", "capacity plan"},
		PromptHint:  "容量规划上下文：基于当前用量和历史趋势给出扩容建议、资源预留和成本评估，按容量规划 SOP 输出。",
		RiskLevel:   ActionDraft,
		AgentMode:   AgentReAct,
		AgentRole:   RoleAnalysis,
		Skills:      []string{"capacity-planning-guide", "risk-assessment-guide"},
	},
	{
		Code:        "config.diff",
		DisplayName: "配置变更对比",
		Keywords:    []string{"配置对比", "配置变更", "配置 diff", "config diff"},
		PromptHint:  "配置变更对比上下文：输出 before/after diff、影响面和回滚点，按配置变更检查清单 SOP 输出。",
		RiskLevel:   ActionReadOnly,
		AgentMode:   AgentDirect,
		AgentRole:   RoleChange,
		Skills:      []string{"config-change-checklist"},
	},
	{
		Code:        "release.rollback",
		DisplayName: "发布回滚",
		Keywords:    []string{"回滚", "rollback", "发布回滚", "版本回退"},
		PromptHint:  "发布回滚上下文：确认目标版本、回滚步骤、影响范围和数据兼容性，按发布回滚 SOP 输出回滚计划。",
		RiskLevel:   ActionExecute,
		AgentMode:   AgentPlanReAct,
		AgentRole:   RoleChange,
		Skills:      []string{"release-rollback-sop", "risk-assessment-guide"},
	},
	{
		Code:        "self.heal",
		DisplayName: "自愈推荐",
		Keywords:    []string{"自愈", "self heal", "自动恢复", "修复建议"},
		PromptHint:  "自愈推荐上下文：基于诊断证据推荐可执行的自愈动作，标注风险等级并要求确认，按自愈推荐指南 SOP 输出。",
		RiskLevel:   ActionWrite,
		AgentMode:   AgentReAct,
		AgentRole:   RoleChange,
		Skills:      []string{"self-heal-recommendation-guide", "risk-assessment-guide"},
	},
	{
		Code:        "dashboard.generate",
		DisplayName: "仪表盘生成",
		Keywords:    []string{"仪表盘", "dashboard", "监控面板"},
		PromptHint:  "仪表盘生成上下文：根据问题域生成监控仪表盘草稿（指标、布局、阈值），按仪表盘设计指南 SOP 输出。",
		RiskLevel:   ActionDraft,
		AgentMode:   AgentDirect,
		AgentRole:   RoleAnalysis,
		Skills:      []string{"dashboard-design-guide"},
	},
	{
		Code:        "alert.rule.draft",
		DisplayName: "告警规则草稿",
		Keywords:    []string{"告警规则", "alert rule", "告警阈值", "告警配置"},
		PromptHint:  "告警规则草稿上下文：生成告警规则草稿（指标、阈值、持续时间、通知渠道），按告警规则草稿指南 SOP 输出。",
		RiskLevel:   ActionDraft,
		AgentMode:   AgentDirect,
		AgentRole:   RoleChange,
		Skills:      []string{"alert-rule-draft-guide"},
	},
	{
		Code:        "knowledge.qa",
		DisplayName: "知识库问答",
		Keywords:    []string{"知识库", "运维手册", "runbook", "knowledge"},
		PromptHint:  "知识库问答上下文：检索运维知识库回答问题，标注来源文档，按知识检索指南 SOP 输出。",
		RiskLevel:   ActionReadOnly,
		AgentMode:   AgentDirect,
		AgentRole:   RoleKnowledge,
		Skills:      []string{"knowledge-retrieval-guide"},
	},
	// --- P2 扩展 Action（1 个）---
	{
		Code:        "cost.analyze",
		DisplayName: "成本分析",
		Keywords:    []string{"成本", "cost", "资源成本", "闲置资源", "成本优化"},
		PromptHint:  "成本分析上下文：基于资源用量、利用率和成本数据，识别闲置资源并给出优化建议，按成本分析 SOP 输出。",
		RiskLevel:   ActionReadOnly,
		AgentMode:   AgentReAct,
		AgentRole:   RoleAnalysis,
		Skills:      []string{"cost-analysis-guide", "risk-assessment-guide"},
	},
	{
		Code:        "sla.analyze",
		DisplayName: "SLA 分析",
		Keywords:    []string{"SLA", "SLO", "SLI", "服务等级", "达成率", "违反", "可用性目标"},
		PromptHint:  "SLA 分析上下文：基于 SLI 指标（可用性/延迟/错误率）评估 SLA 达成度，识别违反风险与改进项，按 SLA 分析 SOP 输出。",
		RiskLevel:   ActionReadOnly,
		AgentMode:   AgentReAct,
		AgentRole:   RoleAnalysis,
		Skills:      []string{"sla-analysis-guide", "risk-assessment-guide"},
	},
	{
		Code:        "incident.review",
		DisplayName: "事故复盘",
		Keywords:    []string{"复盘", "postmortem", "事故复盘", "故障回顾", "incident review"},
		PromptHint:  "事故复盘上下文：基于事故时间线、告警、变更记录生成复盘报告（时间线/根因/影响/改进项），按事故复盘 SOP 输出。",
		RiskLevel:   ActionReadOnly,
		AgentMode:   AgentReAct,
		AgentRole:   RoleAnalysis,
		Skills:      []string{"incident-review-sop", "risk-assessment-guide"},
	},
	{
		Code:        "health.check",
		DisplayName: "健康体检",
		Keywords:    []string{"健康巡检", "体检", "巡检", "health check", "集群体检", "综合体检"},
		PromptHint:  "健康体检上下文：多维度巡检（集群/中间件/资源/SLI），输出整体健康评分和风险项清单，按健康体检 SOP 输出。",
		RiskLevel:   ActionReadOnly,
		AgentMode:   AgentReAct,
		AgentRole:   RoleDiagnostic,
		Skills:      []string{"health-check-guide"},
	},
	{
		Code:        "performance.bottleneck",
		DisplayName: "性能瓶颈定位",
		Keywords:    []string{"性能瓶颈", "响应变慢", "变慢", "慢请求", "延迟升高", "吞吐下降", "bottleneck"},
		PromptHint:  "性能瓶颈定位上下文：基于资源指标（CPU/内存/磁盘/网络）和接口指标（QPS/延迟/错误率）定位瓶颈点，有 trace 则串联分析、无 trace 退化为指标维度，按性能瓶颈定位 SOP 输出。",
		RiskLevel:   ActionReadOnly,
		AgentMode:   AgentReAct,
		AgentRole:   RoleDiagnostic,
		Skills:      []string{"performance-bottleneck-guide", "risk-assessment-guide"},
	},
}

// RegisteredActions 返回所有已注册的 Action（只读副本）。
func RegisteredActions() []Action {
	out := make([]Action, len(registeredActions))
	copy(out, registeredActions)
	return out
}

// LookupAction 按关键词匹配返回命中的 Action。
//
// 匹配策略：最长关键词优先。对每个 Action 取其命中的最长关键词长度，
// 选长度最大的 Action；长度相同则按注册顺序取第一个。
// 这样"容量规划"会匹配 capacity.plan（关键词"容量规划"长度 4）而非
// middleware.diagnose（关键词"容量"长度 2），避免宽泛关键词吞掉具体关键词。
//
// 无匹配时返回 ok=false，调用方应回退到现有 planner（向后兼容）。
func LookupAction(message string) (Action, bool) {
	text := strings.ToLower(strings.TrimSpace(message))
	if text == "" {
		return Action{}, false
	}
	bestIdx := -1
	bestLen := 0
	for idx, action := range registeredActions {
		hitLen := longestKeywordHit(text, action.Keywords)
		if hitLen == 0 {
			continue
		}
		if hitLen > bestLen {
			bestLen = hitLen
			bestIdx = idx
		}
	}
	if bestIdx < 0 {
		return Action{}, false
	}
	return registeredActions[bestIdx], true
}

// longestKeywordHit 返回 text 命中的最长关键词的字节长度。
// 未命中返回 0。关键词比较前统一转小写。
func longestKeywordHit(text string, keywords []string) int {
	longest := 0
	for _, kw := range keywords {
		if kw == "" {
			continue
		}
		lower := strings.ToLower(kw)
		if strings.Contains(text, lower) && len(lower) > longest {
			longest = len(lower)
		}
	}
	return longest
}
