package assistant

// BlockType 标识结构化响应 block 的类型，对齐 SxDevOps AIOps 2.0 的 13 种 block。
type BlockType string

const (
	// BlockIncidentCard 故障或异常摘要卡片
	BlockIncidentCard BlockType = "incident_card"
	// BlockEvidenceTimeline 告警、日志、链路、变更证据时间线
	BlockEvidenceTimeline BlockType = "evidence_timeline"
	// BlockQuerySuggestion PromQL、SQL、LogQL 等查询建议
	BlockQuerySuggestion BlockType = "query_suggestion"
	// BlockChartQuery 可直接跳转或渲染的指标查询
	BlockChartQuery BlockType = "chart_query"
	// BlockAlertRuleDraft 告警规则草稿
	BlockAlertRuleDraft BlockType = "alert_rule_draft"
	// BlockDashboardDraft 仪表盘草稿
	BlockDashboardDraft BlockType = "dashboard_draft"
	// BlockChangeCandidate 可能相关的变更记录
	BlockChangeCandidate BlockType = "change_candidate"
	// BlockRollbackPlan 发布回滚计划
	BlockRollbackPlan BlockType = "rollback_plan"
	// BlockK8sAction K8s 操作建议或待确认动作
	BlockK8sAction BlockType = "k8s_action"
	// BlockSelfHealRecommendation 自愈推荐卡片
	BlockSelfHealRecommendation BlockType = "self_heal_recommendation"
	// BlockApprovalForm 待补参或待确认表单
	BlockApprovalForm BlockType = "approval_form"
	// BlockToolTrace 工具调用追踪
	BlockToolTrace BlockType = "tool_trace"
	// BlockRiskNotice 风险提示
	BlockRiskNotice BlockType = "risk_notice"
)

// Block 是结构化响应块，前端按 Type 分发到对应渲染组件。
// 对齐 SxDevOps 的结构化 block 协议：Type 决定渲染形态，Payload 承载类型特定数据。
//
// 设计取舍：使用单一 Block 结构体 + Payload map[string]any 而非为每种 block
// 定义独立的 Go 结构体。理由：
//  1. block 载荷字段差异大，强类型结构体会导致 13 个结构体 + 13 个反序列化路径
//  2. Payload 作为 map 允许 LLM 和工具直接产出，无需 Go 侧逐字段定义
//  3. 前端按 Type 渲染时本身就需要类型分发，强类型 Go 结构体不带来额外安全
//  4. 未来若某类 block 稳定下来，可再单独定义强类型结构体
type Block struct {
	// Type 是 block 类型，决定前端渲染形态
	Type BlockType `json:"type"`
	// Title 是可选的展示标题
	Title string `json:"title,omitempty"`
	// Content 是可选的自然语言内容（如风险说明、摘要文本）
	Content string `json:"content,omitempty"`
	// Payload 承载类型特定的结构化数据，具体字段由 Type 决定。
	// 例如 EvidenceTimeline 的 events 数组、ApprovalForm 的 fields 数组等。
	Payload map[string]any `json:"payload,omitempty"`
}

// NewBlock 创建一个指定类型的 Block，便于调用方简洁构造。
func NewBlock(bt BlockType) Block {
	return Block{Type: bt}
}

// WithTitle 设置 block 标题并返回 block 自身（链式调用）。
func (b Block) WithTitle(title string) Block {
	b.Title = title
	return b
}

// WithContent 设置 block 内容并返回 block 自身（链式调用）。
func (b Block) WithContent(content string) Block {
	b.Content = content
	return b
}

// WithPayload 设置 block 载荷并返回 block 自身（链式调用）。
func (b Block) WithPayload(payload map[string]any) Block {
	b.Payload = payload
	return b
}
