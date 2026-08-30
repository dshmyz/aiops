package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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
按下面的分隔符标记顺序输出，每个标记独占一行。标记之外不要输出任何内容（不要用 markdown 代码块包裹）：
[[SUMMARY_START]]
面向操作员的一句话自然语言摘要，结合事实给出结论，纯文本
[[SUMMARY_END]]
[[BLOCKS_START]]
blocks 数组的合法 JSON（block 结构见下方枚举），例如 [{"type":"incident_card","title":"诊断结果","content":"...","payload":{}}]
[[BLOCKS_END]]

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
6. 不要在 payload 中放入敏感凭证
7. summary 是给操作员看的最终答复，必须直接回答用户原始问题；禁止复述内部流程或使用内部术语（如"一阶段/二阶段/整形/取证/工具返回了"），禁止以"无法进行问题定位/无法给出操作建议"这类系统口吻收尾
8. 当用户问的是组件/能力介绍（能做什么、是什么、有哪些功能）且 Answer 已是完整介绍时，直接把它整理成答案，不要当作"缺少故障数据"处理
9. summary 正文与 blocks JSON 内都不要出现「[[」或「]]」字符，避免与分隔符标记冲突`

// llmFormatResponse 是 LLM 返回 JSON 的解析结构（旧版严格 JSON 格式回退用）。
type llmFormatResponse struct {
	Summary string  `json:"summary"`
	Blocks  []Block `json:"blocks"`
}

// 分隔符标记。LLM 按标记顺序输出 SUMMARY 区间与 BLOCKS 区间，流式路径把
// SUMMARY 区间的 token 实时转发给前端，BLOCKS 区间在流结束后解析成结构化 block。
const (
	markerSummaryStart = "[[SUMMARY_START]]"
	markerSummaryEnd   = "[[SUMMARY_END]]"
	markerBlocksStart  = "[[BLOCKS_START]]"
	markerBlocksEnd    = "[[BLOCKS_END]]"
)

// parseLLMFormatContent 解析 LLM 整形器的响应文本。
// 优先解析分隔符格式（SUMMARY_START...SUMMARY_END + BLOCKS_START...BLOCKS_END），
// 兼容旧版严格 JSON（{"summary":..., "blocks":[...]}），最后兜底把非空纯文本散文
// 当作 summary（LLM 未按格式输出时保住最终答复，避免无谓回退 code 兜底）。
func parseLLMFormatContent(content string) (llmFormatResponse, error) {
	if summary, ok := extractBetween(content, markerSummaryStart, markerSummaryEnd); ok {
		resp := llmFormatResponse{Summary: strings.TrimSpace(summary)}
		if blocksJSON, ok := extractBetween(content, markerBlocksStart, markerBlocksEnd); ok {
			if err := json.Unmarshal([]byte(blocksJSON), &resp.Blocks); err != nil {
				return llmFormatResponse{}, fmt.Errorf("llm formatter parse blocks: %w", err)
			}
		}
		return resp, nil
	}
	if resp, err := parseStrictJSON(content); err == nil {
		return resp, nil
	}
	// 纯文本散文兜底：整个输出就是最终答复（无 block）。
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return llmFormatResponse{}, errors.New("llm formatter returned non-parseable content")
	}
	return llmFormatResponse{Summary: trimmed}, nil
}

// parseStrictJSON 解析旧版严格 JSON 格式（LLM 未按分隔符输出时的回退）。
func parseStrictJSON(content string) (llmFormatResponse, error) {
	cleaned := extractJSONBody(content)
	var resp llmFormatResponse
	if err := json.Unmarshal([]byte(cleaned), &resp); err == nil {
		return resp, nil
	}
	// 兜底：截取首尾花括号之间的 JSON 再试（容忍前后多余说明文字）。
	start := strings.Index(cleaned, "{")
	end := strings.LastIndex(cleaned, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(cleaned[start:end+1]), &resp); err == nil {
			return resp, nil
		}
	}
	return llmFormatResponse{}, errors.New("llm formatter returned non-parseable content")
}

// extractBetween 返回 content 中 startMarker 与 endMarker 之间的文本。
// 任一标记缺失时返回 ("", false)。
func extractBetween(content, startMarker, endMarker string) (string, bool) {
	start := strings.Index(content, startMarker)
	if start < 0 {
		return "", false
	}
	rest := content[start+len(startMarker):]
	end := strings.Index(rest, endMarker)
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

// forwardSummaryDelta 从已累积的流式文本中提取 SUMMARY 区间，把相对上一帧新增的
// 部分转发给 onDelta，返回最新已转发的 SUMMARY 长度。
//
// 每次基于完整累积文本重新定位标记，天然免疫分隔符被 chunk 切分的边界问题。
// markerSeen 表示累积文本中已出现过 [[SUMMARY_START]]：出现前按纯文本模式转发
// （LLM 输出散文时整个输出就是最终答复），出现后按分隔符模式只转发 SUMMARY 区间。
func forwardSummaryDelta(accumulated string, forwarded int, onDelta func(string), markerSeen bool) int {
	if !markerSeen {
		// 纯文本模式：未出现分隔符标记，整个输出即最终答复。尾部可能是不完整标记
		// 前缀（chunk 恰好停在 "[[SUMMARY_EN"），截掉再转发，避免半截标记泄漏。
		text := trimPartialMarkerTail(accumulated)
		if len(text) > forwarded {
			onDelta(text[forwarded:])
			return len(text)
		}
		return forwarded
	}
	start := strings.Index(accumulated, markerSummaryStart)
	if start < 0 {
		return 0 // 已进入分隔符模式但标记不在当前累积文本（理论上不出现）
	}
	rest := accumulated[start+len(markerSummaryStart):]
	var summary string
	if end := strings.Index(rest, markerSummaryEnd); end >= 0 {
		summary = rest[:end] // SUMMARY_END 已出现 → SUMMARY 区间定稿
	} else {
		summary = rest
	}
	// SUMMARY_END 尚未完整到达时，rest 尾部可能是不完整标记前缀（如 "[[SUM"），
	// 截掉再转发，避免半截标记泄漏到前端（与纯文本模式同一层防护）。
	summary = trimPartialMarkerTail(summary)
	if len(summary) > forwarded {
		onDelta(summary[forwarded:])
		return len(summary)
	}
	return forwarded
}

// markerPrefixes 是四个分隔符标记（纯文本模式用尾部截断）。
var markerPrefixes = []string{markerSummaryStart, markerSummaryEnd, markerBlocksStart, markerBlocksEnd}

// trimPartialMarkerTail 若 s 尾部是某个标记的不完整前缀，则截掉该前缀，避免把半截
// 标记转发给前端（纯文本模式用）。取最长匹配前缀，例如 "...增长趋势。[[SUMMARY_EN"
// 截掉整个 "[[SUMMARY_EN" 而非只截 1 个 "["。
func trimPartialMarkerTail(s string) string {
	for _, m := range markerPrefixes {
		for i := len(m) - 1; i >= 1; i-- {
			prefix := m[:i]
			if strings.HasSuffix(s, prefix) {
				return s[:len(s)-i]
			}
		}
	}
	return s
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
	parsed, err := parseLLMFormatContent(resp.Content)
	if err != nil {
		return FormatResult{}, fmt.Errorf("llm formatter parse: %w", err)
	}
	if strings.TrimSpace(parsed.Summary) == "" {
		return FormatResult{}, errors.New("llm formatter returned empty summary")
	}
	blocks := filterValidBlocks(parsed.Blocks)
	return FormatResult{Summary: parsed.Summary, Blocks: blocks}, nil
}

// FormatStream 以流式方式生成 Summary 与 Blocks。
// 把 chat.Stream 产出、落在 SUMMARY 区间的 token 实时转发给 onDelta，让前端
// 能增量渲染最终答案；流结束后解析 BLOCKS 区间得到结构化 block。
// 任何失败都返回 error，由 ChainedFormatter 回退到 CodeFallbackFormatter
// （无 delta，最终 response 事件仍权威覆盖前端文本）。
func (f *LLMFormatter) FormatStream(ctx context.Context, req FormatRequest, onDelta func(string)) (FormatResult, error) {
	if f == nil || f.chat == nil {
		return FormatResult{}, errors.New("LLM formatter requires a chat model")
	}
	if isDataSourceQueryTool(req.Tool) {
		return FormatResult{}, errors.New("skip LLM for data-source query")
	}
	messages := []*schema.Message{
		schema.SystemMessage(llmFormatSystemPrompt),
		schema.UserMessage(buildLLMFormatUserMessage(req)),
	}
	// 模型偶发返回空流（provider 侧瞬态，无错误），直接落到 code 兜底会让用户看到
	// "一波输出"。空流没有产出任何 delta，重试不会造成重复输出，只对完全空的重试
	// 一次；部分输出直接保留（已转发的 delta 无法重放）。
	var content string
	var lastParseErr error
	for attempt := 1; attempt <= formatterStreamMaxAttempts; attempt++ {
		content, _, lastParseErr = f.accumulateStream(ctx, messages, onDelta)
		if lastParseErr != nil {
			return FormatResult{}, lastParseErr
		}
		if strings.TrimSpace(content) != "" {
			break
		}
		if attempt < formatterStreamMaxAttempts {
			log.Printf("[formatter] FormatStream empty stream, retry %d/%d tool=%s", attempt, formatterStreamMaxAttempts, req.Tool)
		}
	}
	parsed, err := parseLLMFormatContent(content)
	if err != nil {
		return FormatResult{}, fmt.Errorf("llm formatter parse: %w", err)
	}
	if strings.TrimSpace(parsed.Summary) == "" {
		return FormatResult{}, errors.New("llm formatter returned empty summary")
	}
	blocks := filterValidBlocks(parsed.Blocks)
	return FormatResult{Summary: parsed.Summary, Blocks: blocks}, nil
}

// formatterStreamMaxAttempts 是 FormatStream 对空流的重试上限（含首次调用）。
const formatterStreamMaxAttempts = 2

// accumulateStream 单次流式拉取 formatter 的原始输出：逐帧把 SUMMARY 区间的 delta
// 转发给 onDelta，返回累积文本与最后一个携带 ResponseMeta 的 chunk。空流（模型未产出
// 任何文本）不返回 error，由调用方决定是否重试。
func (f *LLMFormatter) accumulateStream(ctx context.Context, messages []*schema.Message, onDelta func(string)) (string, *schema.Message, error) {
	reader, err := f.chat.Stream(ctx, messages)
	if err != nil {
		return "", nil, fmt.Errorf("llm formatter stream: %w", err)
	}
	defer reader.Close()
	started := time.Now()
	var builder strings.Builder
	var lastResponse *schema.Message
	forwarded := 0 // 已转发的文本长度（每帧只转发新增部分）
	markerSeen := false
	for {
		chunk, recvErr := reader.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				break
			}
			return "", nil, fmt.Errorf("llm formatter stream recv: %w", recvErr)
		}
		if chunk == nil {
			continue
		}
		if chunk.ResponseMeta != nil {
			lastResponse = chunk
		}
		if chunk.Content != "" {
			builder.WriteString(chunk.Content)
			if onDelta != nil {
				// 一旦累积文本出现 SUMMARY_START 就切到分隔符模式；此前处于纯文本
				// 转发模式（散文输出），切换时重置计数，按 summary 区间重新计。
				if !markerSeen && strings.Contains(builder.String(), markerSummaryStart) {
					markerSeen = true
					forwarded = 0
				}
				forwarded = forwardSummaryDelta(builder.String(), forwarded, onDelta, markerSeen)
			}
		}
	}
	if f.audit != nil {
		f.audit.record(ctx, started, lastResponse)
	}
	return builder.String(), lastResponse, nil
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
