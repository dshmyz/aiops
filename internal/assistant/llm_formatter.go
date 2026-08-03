package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
)

// LLMFormatter 是基于 LLM 的二阶段整形器。
//
// 一阶段取证后，把用户原始问题、工具名、工具返回的事实和 Skill SOP
// 一起发给 LLM，要求 LLM 返回严格 JSON {summary, blocks}。
// 解析成功且 summary 非空时返回结果；任何失败（LLM 调用失败、JSON 解析失败、
// summary 为空）都返回 error，由 ChainedFormatter 回退到 CodeFallbackFormatter。
//
// 复用 EinoPlanner 的 model.BaseChatModel，与 planner、compactor 共享同一个
// LLM 提供方，避免重复建连。
type LLMFormatter struct {
	chat  model.BaseChatModel
	audit *llmAuditRecorder // nil → 不记录 LLM 调用审计（缺口-5 / R1）
}

// NewLLMFormatter 创建一个 LLM 整形器。chat 不能为 nil，否则 Format 返回 error。
func NewLLMFormatter(chat model.BaseChatModel) *LLMFormatter {
	return &LLMFormatter{chat: chat}
}

// WithLLMAudit wires LLM invocation auditing (缺口-5 / R1). audit may be nil.
func (f *LLMFormatter) WithLLMAudit(auditSvc *audit.Service, model string) *LLMFormatter {
	f.audit = newLLMAuditRecorder(auditSvc, model, "formatter")
	return f
}

const llmFormatSystemPrompt = `你是中间件运维副驾驶的二阶段整形器。

## 职责
接收一阶段取证的事实（用户问题、调用的工具、工具返回的 Answer、相关 Skill SOP），输出面向操作员的自然语言摘要和结构化 block。

## 输出格式
只返回严格 JSON，不要包含任何其他文本或 markdown 代码块：
{
  "summary": "面向操作员的一句话自然语言摘要，结合事实给出结论",
  "blocks": [
    {
      "type": "block 类型，见下方枚举",
      "title": "可选标题",
      "content": "可选自然语言内容",
      "payload": {}
    }
  ]
}

## block type 枚举（只能是以下值之一）
- incident_card: 故障或异常摘要卡片
- evidence_timeline: 告警、日志、链路、变更证据时间线
- query_suggestion: PromQL、SQL、LogQL 等查询建议
- chart_query: 可直接跳转或渲染的指标查询
- alert_rule_draft: 告警规则草稿
- dashboard_draft: 仪表盘草稿
- change_candidate: 可能相关的变更记录
- rollback_plan: 发布回滚计划
- k8s_action: K8s 操作建议或待确认动作
- self_heal_recommendation: 自愈推荐卡片
- approval_form: 待补参或待确认表单
- tool_trace: 工具调用追踪
- risk_notice: 风险提示

## 整形原则
1. summary 必须基于 Answer 中的事实，不要编造未给出的指标或结论
2. 事实不足时在 summary 中说明，不要臆测
3. 优先产出能帮助操作员快速定位问题的 block（如 incident_card、evidence_timeline）
4. 始终附带一个 tool_trace block 记录一阶段调用的工具
5. 涉及风险操作时附带 risk_notice block
6. 不要在 payload 中放入敏感凭证`

// llmFormatResponse 是 LLM 返回 JSON 的解析结构。
type llmFormatResponse struct {
	Summary string  `json:"summary"`
	Blocks  []Block `json:"blocks"`
}

// Format 调用 LLM 生成 Summary 和 Blocks。
// 任何失败都返回 error，由 ChainedFormatter 决定是否回退。
func (f *LLMFormatter) Format(ctx context.Context, req FormatRequest) (FormatResult, error) {
	if f == nil || f.chat == nil {
		return FormatResult{}, errors.New("LLM formatter requires a chat model")
	}
	// 数据源查询工具（alert/event/task）不需要 LLM 格式化，直接跳过
	// 让 ChainedFormatter 回退到 CodeFallbackFormatter。
	if isDataSourceQueryTool(req.Tool) {
		return FormatResult{}, errors.New("skip LLM for data-source query")
	}
	messages := []*schema.Message{
		schema.SystemMessage(llmFormatSystemPrompt),
		schema.UserMessage(buildLLMFormatUserMessage(req)),
	}
	started := time.Now()
	resp, err := f.chat.Generate(ctx, messages)
	if err != nil {
		return FormatResult{}, fmt.Errorf("llm formatter generate: %w", err)
	}
	if f.audit != nil {
		f.audit.record(ctx, started, resp)
	}
	if resp == nil {
		return FormatResult{}, errors.New("llm formatter empty response")
	}
	parsed, err := schema.NewMessageJSONParser[llmFormatResponse](nil).Parse(ctx, resp)
	if err != nil {
		return FormatResult{}, fmt.Errorf("llm formatter parse: %w", err)
	}
	if strings.TrimSpace(parsed.Summary) == "" {
		return FormatResult{}, errors.New("llm formatter returned empty summary")
	}
	blocks := filterValidBlocks(parsed.Blocks)
	return FormatResult{Summary: parsed.Summary, Blocks: blocks}, nil
}

// buildLLMFormatUserMessage 把 FormatRequest 序列化为 LLM 可读的文本。
// Answer 以 JSON 形式给出，便于 LLM 精确引用字段。
func buildLLMFormatUserMessage(req FormatRequest) string {
	var sb strings.Builder
	sb.WriteString("## 用户原始问题\n")
	if strings.TrimSpace(req.UserMessage) != "" {
		sb.WriteString(req.UserMessage)
	} else {
		sb.WriteString("（未提供）")
	}
	sb.WriteString("\n\n## 一阶段调用的工具\n")
	if strings.TrimSpace(req.Tool) != "" {
		sb.WriteString(req.Tool)
	} else {
		sb.WriteString("（无）")
	}
	sb.WriteString("\n\n## 工具返回的事实 (Answer)\n")
	if req.Answer == nil {
		sb.WriteString("（空）")
	} else {
		payload, err := json.Marshal(req.Answer)
		if err != nil {
			sb.WriteString(fmt.Sprintf("%v", req.Answer))
		} else {
			sb.Write(payload)
		}
	}
	if len(req.SkillContents) > 0 {
		sb.WriteString("\n\n## 关联 Skill SOP（整形时遵循其输出约束）\n")
		sb.WriteString(strings.Join(req.SkillContents, "\n\n---\n\n"))
	}
	if strings.TrimSpace(req.ActionCode) != "" {
		sb.WriteString("\n\n## 匹配的 Action\n")
		sb.WriteString(req.ActionCode)
	}
	return sb.String()
}

// validBlockTypes 是允许出现在 LLM 返回中的 block 类型集合。
// LLM 可能产出非枚举值的 type，过滤掉避免前端渲染异常。
var validBlockTypes = map[BlockType]struct{}{
	BlockIncidentCard:           {},
	BlockEvidenceTimeline:       {},
	BlockQuerySuggestion:        {},
	BlockChartQuery:             {},
	BlockAlertRuleDraft:         {},
	BlockDashboardDraft:         {},
	BlockChangeCandidate:        {},
	BlockRollbackPlan:           {},
	BlockK8sAction:              {},
	BlockSelfHealRecommendation: {},
	BlockApprovalForm:           {},
	BlockToolTrace:              {},
	BlockRiskNotice:             {},
}

// filterValidBlocks 过滤掉非法 type 的 block，保留合法的。
func filterValidBlocks(blocks []Block) []Block {
	out := make([]Block, 0, len(blocks))
	for _, b := range blocks {
		if _, ok := validBlockTypes[b.Type]; ok {
			out = append(out, b)
		}
	}
	return out
}
