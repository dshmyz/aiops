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
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go.opentelemetry.io/otel/trace"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// AgentExecutor 基于 LLM function calling 的执行器。
// 替代原有的 EinoPlanner JSON 解析 + 手动 agent loop。
// LLM 通过原生 tool calling 选工具，框架自动执行，结果自动回传。
type AgentExecutor struct {
	chat          model.BaseChatModel // 执行层：选工具、调参数
	reasoningChat model.BaseChatModel // 分析层：深度推理、生成报告（可为 nil）
	tools         []tool.BaseTool
	toolMap       map[string]tool.BaseTool // name → tool 快速查找
	audit         *audit.Service
	modelName     string
	maxSteps      int
	knowledge     *KnowledgeStore                        // 知识库（可为 nil）
	skills        SkillLookup                            // SOP/Skill 查询（可为 nil）
	cache         *ResponseCache                         // LLM 响应缓存（可为 nil）
	rateLimiter   *RateLimiter                           // LLM 调用限流（可为 nil）
	writeGate     agentWriteGate                         // 写工具门：policy/E2 准入门/pending plan 三态（可为 nil）
	sequenceFor   func(context.Context, string) []string // 声明证据顺序解析器（可为 nil）
}

// AgentExecutorConfig 构建 AgentExecutor 所需的依赖。
type AgentExecutorConfig struct {
	ChatModel      model.BaseChatModel
	ReasoningModel model.BaseChatModel // 可选：推理模型，用于深度分析
	Capabilities   []capabilities.Capability
	Adapter        *capabilities.HTTPAdapter
	AuditService   *audit.Service
	ModelName      string
	MaxSteps       int
	KnowledgeStore *KnowledgeStore // 可选：知识库，用于经验积累和检索
	SkillLookup    SkillLookup     // 可选：SOP/Skill 查询
	CacheEnabled   bool            // 可选：启用 LLM 响应缓存
	RateLimit      int             // 可选：每分钟最大 LLM 请求数（0=不限）
}

// NewAgentExecutor 创建 AgentExecutor。
func NewAgentExecutor(cfg AgentExecutorConfig) (*AgentExecutor, error) {
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = 10
	}

	// 把动态能力包装为 eino tools。
	// 构造时的 user 仅作 fallback（ctx 无身份时），这里刻意保持空身份：
	// 空角色在 policy.Evaluate 里一律拒绝，确保漏注入 ctx 身份时 fail-closed
	//（拒绝执行），而不是静默以管理员身份运行。真实身份由调用方在
	// Run/HandleMessage 等入口通过 WithToolUser 注入 ctx。
	user := identity.CurrentUser{}
	einoTools := CapabilityToolsFromCapabilities(cfg.Capabilities, cfg.Adapter, cfg.AuditService, user)
	// 注：http.probe 不进入 agent 工具集——探活由独立的健康检查器（HealthChecker）
	// 承担，agent 专注已发布能力工具，避免通用工具被 LLM 滥用。

	toolMap := make(map[string]tool.BaseTool, len(einoTools))
	for _, t := range einoTools {
		info, _ := t.Info(context.Background())
		if info != nil {
			toolMap[info.Name] = t
		}
	}

	return &AgentExecutor{
		chat:          cfg.ChatModel,
		reasoningChat: cfg.ReasoningModel,
		tools:         einoTools,
		toolMap:       toolMap,
		audit:         cfg.AuditService,
		modelName:     cfg.ModelName,
		maxSteps:      cfg.MaxSteps,
		knowledge:     cfg.KnowledgeStore,
		skills:        cfg.SkillLookup,
	}, nil
}

// NewAgentExecutorWithCache 创建带缓存和限流的 AgentExecutor。
func NewAgentExecutorWithCache(cfg AgentExecutorConfig) (*AgentExecutor, error) {
	exec, err := NewAgentExecutor(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.CacheEnabled {
		exec.cache = NewResponseCache(100, 30*time.Minute)
	}
	if cfg.RateLimit > 0 {
		exec.rateLimiter = NewRateLimiter(cfg.RateLimit, 5) // 5 并发上限
	}
	return exec, nil
}

// WithWriteGate 挂载写工具门。写工具（Operation==Write）在 e.executeTool 之前先过此
// 门，返回与 agentWriteStep 一致的三态语义：拒绝（失败，让 LLM 换路径）/ 自动执行
// （E2 准入放行，结果作为工具结果回传消息历史）/ pending plan 交接（终止循环，由
// 调用方渲染为 confirmation_required）。nil（未装配）时写工具退化为工具层自身的
// RequiresConfirmation fail-closed 拦截（见 agent_tools.go），不会出现"无确认直接执行"。
func (e *AgentExecutor) WithWriteGate(gate agentWriteGate) *AgentExecutor {
	e.writeGate = gate
	return e
}

// WithSequenceFor 挂载 runbook 声明证据顺序解析器（Service 侧封装 runbookRouter）。
// 模型在声明步骤未收集齐前提前收尾时，executor 注入一次引导轮（而不是把半成结论
// 当最终答复）。空序列（未匹配 runbook）不产生任何引导。
func (e *AgentExecutor) WithSequenceFor(seqFor func(context.Context, string) []string) *AgentExecutor {
	e.sequenceFor = seqFor
	return e
}

// agentWriteOutcome 是一次写工具调用的处置结果，三态对应 policy.Evaluate
// （拒绝 / E2 自动执行 / 待人工确认交接）。Version/ExpiresAt 与 plan/Response 对齐
// （uint / time.Time），便于 executorWriteHandoff 与 confirmationResponseFromOutcome
// 直接透传。
type agentWriteOutcome struct {
	Denied            bool   // policy 拒绝写入（reason 携带原因）
	Reason            string // 拒绝原因（仅 Denied 使用）
	AutoExec          bool   // E2 准入门放行，已按已确认计划执行
	PlanID            string
	ExecutionID       string
	Status            string
	Version           uint
	ExpiresAt         time.Time
	Summary           string
	Reused            bool
	Blocks            []Block
	ConfirmationToken string
	Tool              string
	Trace             string
}

// agentWriteGate 拦截一次写工具调用。user 为空身份时 policy 一律拒绝（fail-closed）。
type agentWriteGate func(ctx context.Context, user identity.CurrentUser, toolName string, input map[string]any) (*agentWriteOutcome, error)

// writeOutcomeToolResult 把已落地的写调用（自动执行）渲染为工具结果 JSON，回传消息
// 历史供 LLM 继续推理。
func writeOutcomeToolResult(toolName string, out *agentWriteOutcome) string {
	data := map[string]any{
		"tool":    toolName,
		"summary": out.Summary,
	}
	if out.AutoExec {
		data["execution_id"] = out.ExecutionID
		data["status"] = out.Status
		data["reused"] = out.Reused
		data["plan_id"] = out.PlanID
	} else {
		data["plan_id"] = out.PlanID
		data["status"] = out.Status
		data["confirmation_token"] = out.ConfirmationToken
	}
	b, _ := json.Marshal(data)
	return string(b)
}

// executorPendingSequence 列出声明顺序中尚未被满足的成员（空=顺序已收齐）。
func executorPendingSequence(sequence []string, touched map[string]bool) []string {
	if len(sequence) == 0 {
		return nil
	}
	var pending []string
	for _, m := range sequence {
		if m == "" {
			continue
		}
		if !touched[m] {
			pending = append(pending, m)
		}
	}
	return pending
}

// executorSequenceSteer 是 executor 的声明顺序引导轮文案：点名剩余声明步骤，要求
// 模型先取证再收尾。
func executorSequenceSteer(pending []string) string {
	return "告警根因序列尚未收集齐声明的证据步骤，请先执行下列步骤完成取证：" + strings.Join(pending, "、") +
		"。全部执行完后再给面向用户的中文总结收尾。"
}

// AgentRunResult 是执行结果。
type AgentRunResult struct {
	Answer    string        // LLM 最终回复
	ToolCalls []ToolCallLog // 每步工具调用记录
	Reasoning []string      // 每轮 LLM 的中间推理（决策链）
	TurnCount int           // LLM 调用次数
	Error     error
	Handoff   *agentWriteOutcome // 写交接：已建 pending plan，调用方应渲染 confirmation_required
}

// ToolCallLog 记录一次工具调用。
type ToolCallLog struct {
	Tool   string         `json:"tool"`
	Input  string         `json:"input"`
	Output map[string]any `json:"output,omitempty"`
	Error  string         `json:"error,omitempty"`
}

// acquireLLM 限流：获取 LLM 调用许可。
func (e *AgentExecutor) acquireLLM() {
	if e.rateLimiter != nil {
		e.rateLimiter.Acquire()
	}
}

// releaseLLM 限流：释放 LLM 调用许可。
func (e *AgentExecutor) releaseLLM() {
	if e.rateLimiter != nil {
		e.rateLimiter.Release()
	}
}

// Run 同步执行 agent loop。
func (e *AgentExecutor) Run(ctx context.Context, message string, history []Turn) *AgentRunResult {
	return e.RunWithCallback(ctx, message, history, nil)
}

// RunWithRole 以指定智能体角色执行 agent loop（角色决定 system prompt 边界）。
// role 为空或 supervisor 时行为与 Run 完全一致。
func (e *AgentExecutor) RunWithRole(ctx context.Context, role AgentRole, message string, history []Turn) *AgentRunResult {
	return e.RunWithRoleCallback(ctx, role, message, history, nil)
}

// RunWithRoleCallback 以指定角色执行并流式回调工具步骤。
func (e *AgentExecutor) RunWithRoleCallback(ctx context.Context, role AgentRole, message string, history []Turn, onStep func(AgentStepEvent)) *AgentRunResult {
	return e.RunWithRoleCallbackStream(ctx, role, message, history, onStep, nil, nil)
}

// RunWithRoleCallbackStream 与 RunWithRoleCallback 相同，额外把最终答案的流式
// token 转发给 onDelta、把模型推理 token 实时转发给 onThinking。
// 流式请求的每个循环轮次都走流式生成：推理逐 chunk 转发 thinking（思考过程实时
// 可见），content 在无工具调用的终轮 flush 为 delta（最终答案增量渲染）。工具
// 调用 deltas 内部按 index 聚合，终轮不再额外重生成一轮。让前端增量渲染，不再
// 依赖二阶段 formatter。
func (e *AgentExecutor) RunWithRoleCallbackStream(ctx context.Context, role AgentRole, message string, history []Turn, onStep func(AgentStepEvent), onDelta func(string), onThinking func(string)) *AgentRunResult {
	result := e.runWithCallbackRole(ctx, role, message, history, onStep, onDelta, onThinking)
	// 指标：记录请求结果（覆盖所有 return 路径）
	if result == nil {
		agentMetrics.recordRequest(false, "agent returned nil result")
	} else {
		agentMetrics.recordRequest(result.Error == nil, errMsgOf(result.Error))
	}
	return result
}

// AgentStepEvent 是 agent 执行过程中的一个步骤事件（工具调用完成）。
type AgentStepEvent struct {
	Step    int            `json:"step"`
	Tool    string         `json:"tool"`
	Status  string         `json:"status"` // "done" | "error"
	Summary string         `json:"summary"`
	Input   map[string]any `json:"input,omitempty"`  // 工具入参（LLM 传来的 JSON 已解析）
	Output  map[string]any `json:"output,omitempty"` // 工具原始返回（能力调用含 data/summary/severity）
	Error   string         `json:"error,omitempty"`  // 失败原因；summary 空时前端兜底展示
}

// RunWithCallback 执行 agent loop，每完成一个工具调用就回调 onStep。
// onStep 为 nil 时等价于 Run。
func (e *AgentExecutor) RunWithCallback(ctx context.Context, message string, history []Turn, onStep func(AgentStepEvent)) *AgentRunResult {
	return e.RunWithRoleCallback(ctx, RoleSupervisor, message, history, onStep)
}

// errMsgOf 安全提取错误信息。
func errMsgOf(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// logWithCtx 输出带请求关联信息的日志。从 ctx 提取 trace_id（OTel span）和
// 请求者 request_id/subject，让 agent 路径的每条日志可归因到具体请求和用户，
// 便于跨日志断排障。ctx 无身份/无 span 时退化为普通 log.Printf，不引入噪声。
func logWithCtx(ctx context.Context, format string, args ...any) {
	if prefix := logPrefix(ctx); prefix != "" {
		log.Printf("%s "+format, append([]any{prefix}, args...)...)
		return
	}
	log.Printf(format, args...)
}

// logPrefix 计算日志的关联信息前缀 "[trace=... user=... req=...]"，无关联信息时返回空串。
func logPrefix(ctx context.Context) string {
	traceID := ""
	if sc := trace.SpanContextFromContext(ctx); sc.HasTraceID() {
		traceID = sc.TraceID().String()
	}
	reqID, subject := "", ""
	if user, ok := toolUserFromContext(ctx); ok {
		reqID = user.RequestID
		subject = user.Subject
	}
	var attrs []string
	if traceID != "" {
		attrs = append(attrs, "trace="+traceID)
	}
	if reqID != "" {
		attrs = append(attrs, "req="+reqID)
	}
	if subject != "" {
		attrs = append(attrs, "user="+subject)
	}
	if len(attrs) == 0 {
		return ""
	}
	return "[" + strings.Join(attrs, " ") + "]"
}

// runWithCallbackRole 是 RunWithRoleCallbackStream 的实现（defer 指标在包装层处理）。
// 流式请求（onDelta 非 nil）每轮走 streamRound：推理实时转发 onThinking、终轮
// content flush 给 onDelta。非流式请求（onDelta 为 nil）每轮走一次性 Generate。
func (e *AgentExecutor) runWithCallbackRole(ctx context.Context, role AgentRole, message string, history []Turn, onStep func(AgentStepEvent), onDelta func(string), onThinking func(string)) *AgentRunResult {
	// 缓存检查：相同问题直接返回缓存结果。流式请求（onDelta 非 nil）跳过缓存读，
	// 因为缓存命中会绕过流式生成，前端整段"一波"显示——必须重新调 LLM 才能产出
	// 逐 token delta。非流式路径仍享缓存（避免重复 LLM 调用）。
	if e.cache != nil && onDelta == nil {
		if cached := e.cache.Get(ctx, message); cached != nil {
			logWithCtx(ctx, "[agent] cache hit for: %s", message[:min(50, len(message))])
			return cached
		}
	}

	// 构建初始消息：角色提示词（supervisor/空 → 通用助手提示词）
	systemPrompt := roleSystemPrompt(role)

	// 知识库检索：查找类似问题的历史诊断经验
	if e.knowledge != nil {
		if pastCases, err := e.knowledge.Search(ctx, message, 3); err == nil && len(pastCases) > 0 {
			var kb strings.Builder
			kb.WriteString("\n\n## 历史诊断经验（参考）\n")
			for i, c := range pastCases {
				kb.WriteString(fmt.Sprintf("\n### 案例 %d: %s\n", i+1, c["title"]))
				if findings, ok := c["findings"].(string); ok && findings != "" {
					kb.WriteString(fmt.Sprintf("发现: %s\n", findings))
				}
				if tools, ok := c["tools"].(string); ok && tools != "" {
					kb.WriteString(fmt.Sprintf("工具: %s\n", tools))
				}
			}
			kb.WriteString("\n请参考以上历史经验，但不要照搬——当前情况可能不同。")
			systemPrompt += kb.String()
		}
		// 反馈学习：查找用户对类似问题的 👍/👎 评价和纠错
		if feedbacks, err := e.knowledge.SearchFeedback(ctx, message, 3); err == nil && len(feedbacks) > 0 {
			var fb strings.Builder
			fb.WriteString("\n\n## 用户反馈经验（重要）\n")
			for _, f := range feedbacks {
				rating, _ := f["rating"].(int)
				correction, _ := f["correction"].(string)
				if rating < 0 && correction != "" {
					fb.WriteString(fmt.Sprintf("- ❌ 用户不满意：\"%s\"。纠错：\"%s\"\n", f["query"], correction))
				} else if rating > 0 {
					fb.WriteString(fmt.Sprintf("- ✅ 用户满意：\"%s\" 的回答方式\n", f["query"]))
				}
			}
			fb.WriteString("\n请遵循以上用户反馈的偏好和纠错意见。")
			systemPrompt += fb.String()
		}
	}

	// SOP/Skill：按用户消息检索最相关的技能，只注入命中 top-N 的正文 + 输出约束。
	// 独立于知识库开关：技能有独立的 SkillLookup 依赖，不应随知识库是否启用而失效。
	// 修复：全量注入 26 个 skill 的内容（每个截 500 字 ≈ 13000 字）会把
	// system prompt 塞爆、淹没用户指令，是 agent "变笨"的主因之一；此前只列
	// slug 名又让模型读不到任何手册正文。这里取其折中——相关才注入正文。
	if e.skills != nil {
		if skillSummaries, err := e.skills.ListSkillsByAction(ctx, ""); err == nil && len(skillSummaries) > 0 {
			if rel := RelevantSkills(skillSummaries, message, 3); len(rel) > 0 {
				if block := FormatSkillPrompt(rel); block != "" {
					systemPrompt += block
				}
			}
		}
	}

	messages := []*schema.Message{
		schema.SystemMessage(systemPrompt),
	}
	// 追加历史
	for _, turn := range history {
		if turn.Role == "user" {
			messages = append(messages, schema.UserMessage(turn.Content))
		} else if turn.Role == "assistant" {
			messages = append(messages, schema.AssistantMessage(turn.Content, nil))
		}
	}
	messages = append(messages, schema.UserMessage(message))

	var allToolCalls []ToolCallLog
	var reasoningTrail []string
	consecutiveErrors := 0
	lastStep := 0
	seen := map[string]bool{} // 去重：同一工具+参数不重复调用

	// 写门身份：executor ctx 已由入口 WithToolUser 注入真实请求者。缺失时为空身份
	// ——policy 对空身份一律拒绝写（fail-closed），与工具层行为一致。
	gateUser, _ := toolUserFromContext(ctx)
	// runbook 声明证据顺序：匹配到启用 runbook 的 tool_sequence 时，模型在声明步骤
	// 未收集齐前提前收尾会收到一次引导轮；未匹配则为空，行为与历史一致。
	var sequence []string
	if e.sequenceFor != nil {
		sequence = e.sequenceFor(ctx, message)
	}
	sequenceTouched := map[string]bool{}
	steered := false // 声明顺序引导最多注入一次，防止无限引导
	var handoff *agentWriteOutcome

	// 工具集：全量已注册工具每轮都对模型开放，不再单独跑一次意图分类 LLM
	//（省一次 ~4s 往返）。意图与工具选择由模型在每轮内语义判断：知识型问题直接
	// 文字回答，实时数据问题从全量工具中选相关者（角色提示词 agent_role.go 两条
	// 都已写明）。
	allTools := e.tools

loop:
	for step := 0; step < e.maxSteps; step++ {
		lastStep = step
		// Kill switch：agent 被禁用时立即停止
		if !AgentEnabled() {
			return &AgentRunResult{Error: fmt.Errorf("agent disabled by operator"), ToolCalls: allToolCalls, TurnCount: step + 1}
		}
		if err := ctx.Err(); err != nil {
			return &AgentRunResult{Error: err, ToolCalls: allToolCalls, TurnCount: step + 1}
		}
		// 连续失败熔断：同一工具连续失败 3 次则停止循环
		if consecutiveErrors >= 3 {
			logWithCtx(ctx, "[agent] stopping after %d consecutive tool failures", consecutiveErrors)
			break
		}

		// 调 LLM（带 tools 参数）。流式请求整轮走流式：推理 token 逐 chunk 实时
		// 转发 thinking（独立通道，无闪烁）；content 先累积、flush 时机由本函数在
		// 无工具调用分支决定——真终轮 flush 为 delta，声明顺序引导轮丢弃（不让半成
		// 结论闪现），工具轮的先导叙述丢弃（避免被执行结果覆盖的闪烁）。工具调用
		// deltas 在 streamRound 内部按 index 聚合，返回完整 ToolCalls。
		toolInfos := e.toolInfosFiltered(allTools)
		logWithCtx(ctx, "[agent] calling LLM with %d tools: %v", len(toolInfos), toolNames(toolInfos))
		e.acquireLLM()
		var resp *schema.Message
		var chunks []string
		var err error
		if onDelta != nil {
			// 流式轮：推理 token 逐 chunk 实时转发 onThinking（独立通道无闪烁），
			// content chunk 缓冲在本轮内不动（见下方终轮分支：真终轮 flush、声明顺序
			// 引导时丢弃半成结论）。工具轮 content 是"边说边决定"的先导叙述，正常丢弃。
			resp, chunks, err = e.streamRound(ctx, messages, toolInfos, onThinking)
		} else {
			resp, err = e.chat.Generate(ctx, messages, model.WithTools(toolInfos))
		}
		e.releaseLLM()
		agentMetrics.recordLLMCall(err == nil)
		if err != nil {
			agentMetrics.recordRequest(false, err.Error())
			return &AgentRunResult{Error: fmt.Errorf("LLM generate: %w", err), ToolCalls: allToolCalls, TurnCount: step + 1}
		}
		logWithCtx(ctx, "[agent] LLM returned: content=%d chars, tool_calls=%d", len(resp.Content), len(resp.ToolCalls))
		// 决策链：记录本轮 LLM 的中间推理
		if len(resp.Content) > 0 && len(resp.ToolCalls) > 0 {
			reasoningTrail = append(reasoningTrail, resp.Content)
		}

		// 如果没有 tool calls → LLM 给出了最终回复
		if len(resp.ToolCalls) == 0 {
			// 数据诚实性兜底：所有工具都失败时，不给 LLM 脑补的机会
			if len(allToolCalls) > 0 && allToolsFailed(allToolCalls) {
				// 流式：已缓冲的内容照样增量渲染（token 手感保留，结论由兜底覆盖）
				if onDelta != nil {
					for _, c := range chunks {
						onDelta(c)
					}
				}
				honest := fmt.Sprintf("抱歉，本次检查的所有工具调用都失败了，无法获取任何数据。失败详情：%s", summarizeFailures(allToolCalls))
				e.saveKnowledgeWithReasoning(ctx, message, allToolCalls, reasoningTrail)
				result := &AgentRunResult{
					Answer:    honest,
					ToolCalls: allToolCalls,
					Reasoning: reasoningTrail,
					TurnCount: step + 1,
				}
				// 涉及写调用的执行结果不入缓存（见 cacheableResult）：重复写请求必须
				// 每次都重新过写门。
				if e.cache != nil && cacheableResult(allToolCalls) {
					e.cache.Set(ctx, message, result)
				}
				return result
			}
			// 声明证据顺序未完成 → 注入一次引导轮，不接受"半成结论"当最终答复。
			// 与 loop 侧 sequenceSteerTurn 语义一致：宁可多走一轮取证，也不让模型
			// 只查了一个维度就下结论。全部收齐或已引导过一次才放行真终轮。
			if pending := executorPendingSequence(sequence, sequenceTouched); len(pending) > 0 && !steered {
				steered = true
				steer := schema.UserMessage(executorSequenceSteer(pending))
				logWithCtx(ctx, "[agent] step %d: steering runbook sequence pending %v", step+1, pending)
				messages = append(messages, steer)
				continue
			}
			// 真终轮：把缓冲的 content 按原 chunk 次序 flush 为 delta（最终答案
			// 增量渲染）。声明顺序引导轮已 continue，不会走到这里。
			if onDelta != nil {
				for _, c := range chunks {
					onDelta(c)
				}
			}
			// 如果已有工具调用结果，走分析层生成深度报告。流式请求跳过分析层：
			// analyze 是又一次非流式 LLM 调用（深度报告），在 SSE 的 30s 预算内
			// 会挤占最终答案的流式窗口。流式路径直接采用 agent 自己流式生成的
			// 最终回答（streamRound 已 flush delta）。非流式请求保留 analyze 的
			// 深度报告。
			if len(allToolCalls) > 0 && onDelta == nil {
				analysis := e.analyze(ctx, message, allToolCalls)
				if analysis != "" {
					e.saveKnowledgeWithReasoning(ctx, message, allToolCalls, reasoningTrail)
					return &AgentRunResult{
						Answer:    analysis,
						ToolCalls: allToolCalls,
						Reasoning: reasoningTrail,
						TurnCount: step + 1,
					}
				}
			}
			e.saveKnowledgeWithReasoning(ctx, message, allToolCalls, reasoningTrail)
			answer := resp.Content
			// 流式轮：终轮 content 已在上方按 chunk flush 为 delta（最终答案逐段
			// 增量渲染），这里不再重复 flush。
			result := &AgentRunResult{
				Answer:    answer,
				ToolCalls: allToolCalls,
				Reasoning: reasoningTrail,
				TurnCount: step + 1,
			}
			// 涉及写调用的执行结果不入缓存（见 cacheableResult）：缓存会让重复写请求
			// 跳过写门重评估，拿到已随政策/自治状态而失效的过期结论。
			if e.cache != nil && result.Answer != "" && cacheableResult(allToolCalls) {
				e.cache.Set(ctx, message, result)
			}
			return result
		}

		// 有 tool calls → 执行工具
		messages = append(messages, resp) // assistant message with tool calls

		for _, tc := range resp.ToolCalls {
			toolName := tc.Function.Name
			toolArgs := tc.Function.Arguments

			// 去重：同一工具+参数不重复调用
			dedupKey := toolName + ":" + toolArgs
			if seen[dedupKey] {
				logWithCtx(ctx, "[agent] step %d: skipping duplicate %s", len(allToolCalls)+1, toolName)
				// 必须为这个 tool_call_id 补一条 ToolMessage，否则下一次
				// Generate 会因为缺少配对 tool message 而 400。
				dupMsg := schema.ToolMessage(`{"skipped": true, "reason": "duplicate tool call"}`, tc.ID)
				dupMsg.ToolName = toolName
				messages = append(messages, dupMsg)
				continue
			}
			seen[dedupKey] = true

			stepIdx := len(allToolCalls)
			logWithCtx(ctx, "[agent] step %d: calling %s(%s)", stepIdx+1, toolName, toolArgs)

			// 执行工具。写工具（registered Write）先过写门：拒绝 → 失败让 LLM 换路径；
			// E2 放行 → 自动执行（结果回传）；其它 → 交接（build pending plan 终止循环，
			// 由调用方渲染 confirmation_required，写绝不在此处直接执行）。
			result, execErr, gateOut := e.handleToolCall(ctx, gateUser, toolName, toolArgs)
			if gateOut != nil {
				handoff = gateOut
				if onStep != nil {
					onStep(AgentStepEvent{Step: stepIdx, Tool: toolName, Status: "done", Summary: gateOut.Summary})
				}
				break loop
			}
			// 声明证据顺序：本步工具已执行，标记可能命中的声明成员（子串匹配让域
			// 诊断也能满足命名该域的顺序成员）。
			sequenceTrackTouched(sequenceTouched, sequence, toolName)
			agentMetrics.recordToolCall(execErr == nil)

			toolLog := ToolCallLog{Tool: toolName, Input: toolArgs}
			if execErr != nil {
				toolLog.Error = execErr.Error()
				consecutiveErrors++
				logWithCtx(ctx, "[agent] step %d: %s failed: %v (consecutive_errors=%d)", stepIdx+1, toolName, execErr, consecutiveErrors)
				// 错误恢复：告诉 LLM 失败了，可以换工具
				errorHint := fmt.Sprintf("⚠️ 工具 %s 调用失败: %v。你可以尝试其他工具，或换一种方式检查。", toolName, execErr)
				errorMsg := schema.ToolMessage(errorHint, tc.ID)
				errorMsg.ToolName = toolName
				messages = append(messages, errorMsg)
			} else {
				_ = json.Unmarshal([]byte(result), &toolLog.Output)
				// 检查工具结果中的错误（如 HTTP probe 返回 {"status":"error"}）
				if toolLog.Output != nil {
					if status, _ := toolLog.Output["status"].(string); status == "error" {
						consecutiveErrors++
						errMsg, _ := toolLog.Output["error"].(string)
						logWithCtx(ctx, "[agent] step %d: %s returned error: %v (consecutive_errors=%d)", stepIdx+1, toolName, errMsg, consecutiveErrors)
						// 错误恢复：工具返回了错误状态
						errorHint := fmt.Sprintf("⚠️ 工具 %s 返回错误: %s。可以尝试其他工具或换种方式。", toolName, errMsg)
						errorMsg := schema.ToolMessage(errorHint, tc.ID)
						errorMsg.ToolName = toolName
						messages = append(messages, errorMsg)
					} else {
						consecutiveErrors = 0
					}
				} else {
					consecutiveErrors = 0
				}
				logWithCtx(ctx, "[agent] step %d: %s result: %s", step+1, toolName, result[:min(200, len(result))])
			}
			allToolCalls = append(allToolCalls, toolLog)

			// 流式回调：通知前端工具调用完成。带上 input/output/error：前端
			// AssistantSteps 有输入/输出展开面板，缺了这三个字段面板永远空白，
			// 用户只能看到一行工具名，无法判断"到底干没干、干了什么"。
			if onStep != nil {
				var inputMap map[string]any
				_ = json.Unmarshal([]byte(toolLog.Input), &inputMap) // 解析失败保持 nil
				event := AgentStepEvent{
					Step:   stepIdx,
					Tool:   toolName,
					Status: "done",
					Input:  inputMap,
					Output: toolLog.Output,
				}
				if execErr != nil {
					event.Status = "error"
					event.Error = execErr.Error()
					event.Output = nil
				} else if toolLog.Output != nil {
					if s, ok := toolLog.Output["summary"].(string); ok {
						event.Summary = s
					} else if s, ok := toolLog.Output["status"].(string); ok {
						event.Summary = s
					}
				}
				onStep(event)
			}

			// 把工具结果追加到消息历史
			toolMsg := schema.ToolMessage(result, tc.ID)
			toolMsg.ToolName = toolName
			messages = append(messages, toolMsg)
		}
	}

	// 写交接：循环在写门上中断（gateOut 非空），把已落库的 pending plan 带出。
	// 不写缓存——计划是一次性的，缓存会串到后续用户的确认状态。
	if handoff != nil {
		return &AgentRunResult{
			Answer:    handoff.Summary,
			ToolCalls: allToolCalls,
			Reasoning: reasoningTrail,
			TurnCount: lastStep + 1,
			Handoff:   handoff,
		}
	}

	// 达到最大步数或 LLM 结束调用 → 进入分析层
	// 收集所有工具结果，发给推理模型做深度分析
	if len(allToolCalls) > 0 {
		analysis := e.analyze(ctx, message, allToolCalls)
		if analysis != "" {
			e.saveKnowledgeWithReasoning(ctx, message, allToolCalls, reasoningTrail)
			result := &AgentRunResult{
				Answer:    analysis,
				ToolCalls: allToolCalls,
				Reasoning: reasoningTrail,
				TurnCount: lastStep + 1,
			}
			// 涉及写调用的执行结果不入缓存（见 cacheableResult）。
			if e.cache != nil && cacheableResult(allToolCalls) {
				e.cache.Set(ctx, message, result)
			}
			return result
		}
	}

	// 无工具调用或分析失败 → 让执行层 LLM 总结
	toolInfos := e.toolInfosFiltered(allTools)
	e.acquireLLM()
	resp, err := e.chat.Generate(ctx, messages, model.WithTools(toolInfos))
	e.releaseLLM()
	if err != nil {
		return &AgentRunResult{Error: fmt.Errorf("LLM final generate: %w", err), ToolCalls: allToolCalls, TurnCount: e.maxSteps}
	}
	result := &AgentRunResult{
		Answer:    resp.Content,
		ToolCalls: allToolCalls,
		Reasoning: reasoningTrail,
		TurnCount: e.maxSteps,
	}
	// 知识积累：保存诊断记录
	e.saveKnowledgeWithReasoning(ctx, message, allToolCalls, reasoningTrail)
	// 缓存：存入响应缓存（涉及写调用的执行不入缓存，见 cacheableResult——重复写请求
	// 必须重新过写门，不能命中会随政策/自治状态失效的过期结论）。
	if e.cache != nil && result.Answer != "" && cacheableResult(allToolCalls) {
		e.cache.Set(ctx, message, result)
	}
	return result
}

// streamRound 以流式方式生成单轮回复，是流式请求下每个循环轮次的统一生成入口。
//   - 推理 token（chunk.ReasoningContent）逐片段实时转发给 onThinking，让前端边
//     想边显示思考过程；
//   - content 片段缓冲在该轮内、不实时转发、随返回值交还给调用方：调用方在真终轮
//     （无工具调用 + 无声明顺序引导）按原 chunk 次序逐段转发 onDelta（即最终答案
//     逐段增量渲染），在声明顺序引导轮则丢弃缓冲（不让半成结论闪现）——flush 时机
//     由 runWithCallbackRole 决定，这里不再替它做决定；工具轮的先导叙述正常丢弃，
//     避免"边说边决定再被工具路径覆盖"的闪烁；
//   - 工具调用 deltas 按 index 聚合：OpenAI 兼容接口把一次函数调用按 index 分片
//     下发（id/name 在首片、arguments 跨片追加），聚合后返回完整的 ToolCalls。
//
// 返回累积消息、缓冲的 content chunk 列表，并保留流式响应中携带的 ResponseMeta
// （token 用量，供审计）。
func (e *AgentExecutor) streamRound(ctx context.Context, messages []*schema.Message, toolInfos []*schema.ToolInfo, onThinking func(string)) (*schema.Message, []string, error) {
	var reader *schema.StreamReader[*schema.Message]
	var err error
	if len(toolInfos) > 0 {
		reader, err = e.chat.Stream(ctx, messages, model.WithTools(toolInfos))
	} else {
		reader, err = e.chat.Stream(ctx, messages)
	}
	if err != nil {
		return nil, nil, err
	}
	defer reader.Close()
	var builder strings.Builder
	var contentChunks []string
	var lastResponse *schema.Message
	aggCalls := map[int]*schema.ToolCall{} // index → 聚合后的完整调用
	var callOrder []int
	for {
		chunk, recvErr := reader.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				break
			}
			return nil, nil, recvErr
		}
		if chunk == nil {
			continue
		}
		if chunk.ResponseMeta != nil {
			lastResponse = chunk
		}
		if chunk.ReasoningContent != "" && onThinking != nil {
			onThinking(chunk.ReasoningContent)
		}
		if chunk.Content != "" {
			builder.WriteString(chunk.Content)
			contentChunks = append(contentChunks, chunk.Content)
		}
		for _, tc := range chunk.ToolCalls {
			idx := 0
			if tc.Index != nil {
				idx = *tc.Index
			}
			agg, ok := aggCalls[idx]
			if !ok {
				agg = &tc
				aggCalls[idx] = agg
				callOrder = append(callOrder, idx)
				continue
			}
			agg.Function.Name += tc.Function.Name
			agg.Function.Arguments += tc.Function.Arguments
			if agg.ID == "" {
				agg.ID = tc.ID
			}
		}
	}
	msg := schema.AssistantMessage(builder.String(), nil)
	for _, idx := range callOrder {
		msg.ToolCalls = append(msg.ToolCalls, *aggCalls[idx])
	}
	if lastResponse != nil && lastResponse.ResponseMeta != nil {
		msg.ResponseMeta = lastResponse.ResponseMeta
	}
	// flush 时机交给调用方决定（真终轮 flush、声明顺序引导轮丢弃），这里只返回缓冲。
	return msg, contentChunks, nil
}

// saveKnowledge 异步保存诊断记录到知识库。
func (e *AgentExecutor) saveKnowledge(ctx context.Context, message string, toolCalls []ToolCallLog) {
	e.saveKnowledgeWithReasoning(ctx, message, toolCalls, nil)
}

// saveKnowledgeWithReasoning 保存诊断记录 + LLM 决策链。
func (e *AgentExecutor) saveKnowledgeWithReasoning(ctx context.Context, message string, toolCalls []ToolCallLog, reasoning []string) {
	if e.knowledge == nil || len(toolCalls) == 0 {
		return
	}
	// 序列化决策链
	reasoningJSON, _ := json.Marshal(reasoning)
	go func() {
		// 保存原始工具调用记录 + 决策链
		e.knowledge.SaveFromToolCallsWithReasoning(ctx, message, toolCalls, string(reasoningJSON))
		// 保存结构化摘要
		var toolsCalled []string
		var keyFacts []string
		anyFailed := false
		for _, tc := range toolCalls {
			toolsCalled = append(toolsCalled, tc.Tool)
			if tc.Error != "" {
				anyFailed = true
				keyFacts = append(keyFacts, tc.Tool+" failed")
			} else if summary, ok := tc.Output["summary"].(string); ok && summary != "" {
				keyFacts = append(keyFacts, summary)
			}
		}
		// 结论如实标记成败：全部成功 → success，任一失败 → failed，避免把
		// 失败操作包装成"成功经验"注入后续 planner prompt。
		outcome := "success"
		if anyFailed {
			outcome = "failed"
		}
		_ = e.knowledge.SaveConversationSummary(ctx, message, ConversationSummary{
			Intent:   message,
			Tools:    toolsCalled,
			Outcome:  outcome,
			KeyFacts: keyFacts,
		})
	}()
}

// analyze 用推理模型对工具结果做深度分析。
// 如果没有推理模型或分析失败，返回空字符串（由调用方 fallback）。
func (e *AgentExecutor) analyze(ctx context.Context, userMessage string, toolCalls []ToolCallLog) string {
	// 有推理模型用推理模型，没有则用主模型（不同 prompt）
	chat := e.reasoningChat
	if chat == nil {
		chat = e.chat
	}

	// 构建分析 prompt：用户问题 + 所有工具结果（压缩数据量）
	var sb strings.Builder
	sb.WriteString("你是资深运维专家。根据以下工具采集的数据，给出深度分析报告。\n\n")
	sb.WriteString("## 用户问题\n")
	sb.WriteString(userMessage)
	sb.WriteString("\n\n## 采集到的数据\n")

	// 数据完整性评估：统计成功/失败/空结果
	successCount := 0
	failedCount := 0
	emptyCount := 0
	for _, tc := range toolCalls {
		sb.WriteString(fmt.Sprintf("\n### %s\n", tc.Tool))
		_, failed, empty := classifyToolResult(tc)
		switch {
		case failed:
			failedCount++
			if tc.Error != "" {
				sb.WriteString(fmt.Sprintf("调用失败: %s\n", tc.Error))
			} else {
				sb.WriteString(compressToolOutput(tc.Output))
				sb.WriteString("\n")
			}
		case empty:
			emptyCount++
			sb.WriteString("(工具返回空结果)\n")
		default:
			successCount++
			sb.WriteString(compressToolOutput(tc.Output))
			sb.WriteString("\n")
		}
	}

	// 数据完整性声明：强制模型认识数据缺口
	sb.WriteString("\n## 数据完整性\n")
	sb.WriteString(fmt.Sprintf("- 成功获取数据的工具: %d 个\n", successCount))
	sb.WriteString(fmt.Sprintf("- 调用失败的工具: %d 个\n", failedCount))
	sb.WriteString(fmt.Sprintf("- 返回空结果的工具: %d 个\n", emptyCount))

	sb.WriteString(`
## 输出要求
用中文给出结构化分析报告，包含：
1. **数据完整度**：明确说明哪些维度有数据、哪些没有。如果某工具失败或返回空，必须写"该维度无数据，无法判断"，绝对禁止把无数据推断为正常
2. **核心发现**：从数据中提取的关键事实（具体数字、状态、异常点）
3. **根因分析**：基于数据推理问题的根本原因（如果有异常）
4. **影响评估**：这个问题的影响范围和严重程度
5. **建议操作**：具体的、可执行的运维操作建议

要求：
- 所有结论必须基于上面的数据，不要编造
- 无数据的维度必须明确标注，不能默认为健康
- 给出具体数字和状态，不要泛泛而谈
- 如果数据全部正常且完整，简洁总结即可，不需要长篇大论`)

	messages := []*schema.Message{
		schema.SystemMessage("你是资深运维专家，擅长从监控数据中分析问题根因。"),
		schema.UserMessage(sb.String()),
	}

	logWithCtx(ctx, "[agent] analysis: sending %d tool results to reasoning model", len(toolCalls))
	start := time.Now()
	e.acquireLLM()
	resp, err := chat.Generate(ctx, messages)
	e.releaseLLM()
	latency := time.Since(start)
	if err != nil {
		logWithCtx(ctx, "[agent] analysis failed: %v (%dms)", err, latency.Milliseconds())
		return ""
	}
	logWithCtx(ctx, "[agent] analysis completed: %d chars (%dms)", len(resp.Content), latency.Milliseconds())
	return resp.Content
}

// allToolsFailed 判断所有工具调用是否都失败了。
func allToolsFailed(toolCalls []ToolCallLog) bool {
	if len(toolCalls) == 0 {
		return false
	}
	for _, tc := range toolCalls {
		if tc.Error == "" && tc.Output != nil && len(tc.Output) > 0 {
			return false
		}
	}
	return true
}

// cacheableResult 判定一次执行结果是否可写进 LLM 响应缓存。
//
// 涉及写工具调用的结果依赖 policy / E2 自治开关 / 每日上限等**运行时状态**：如果把它
// 缓存起来，重复的同一个写请求会直接命中旧结论、跳过写门重评估——自动执行不真正再
// 跑、E2 计数不重记、被收回的权限仍播出"已执行"，是误导性的。因此含写调用（无论
// 结果是被拒还是被放行自动执行）的执行一律不入缓存；纯读执行照常缓存。
func cacheableResult(toolCalls []ToolCallLog) bool {
	for _, tc := range toolCalls {
		if t, ok := tools.Lookup(tc.Tool); ok && t.Operation == tools.Write {
			return false
		}
	}
	return true
}

// classifyToolResult 判定一次工具调用属于 成功/失败/空，供 analyze 的数据完整性统计。
//
//   - error 非空 / 结构化 status|severity 命中失败值 → failed；
//   - 无 output → empty；
//   - 摘要命中"本维度无数据"的明确短语（未配置/不可用/探活失败等）→ failed——
//     如 K8s 未配置、探活失败，是"该维度取不到数"，不能统计成"成功获取数据"；
//   - 其它 → success。
//
// 刻意不做通用文本子串扫描：summary 里出现 "error"/"failed" 往往是数据本身的
// 现象（"发现 3 条 error 日志"、"errors 记录 N 条"）而非取数失败，误扫会把有完整
// 数据的正常召回判成失败，进而让报告声称"该维度无数据"。结构化字段优先，摘要只
// 匹配合法的"无数据"短语。
func classifyToolResult(tc ToolCallLog) (success, failed, empty bool) {
	if tc.Error != "" {
		return false, true, false
	}
	if len(tc.Output) == 0 {
		return false, false, true
	}
	if v, _ := tc.Output["status"].(string); isFailedStatus(v) {
		return false, true, false
	}
	if v, _ := tc.Output["severity"].(string); isFailedStatus(v) {
		return false, true, false
	}
	if summary, _ := tc.Output["summary"].(string); summary != "" {
		if matchesNoDataPhrase(summary) {
			return false, true, false
		}
		return true, false, false
	}
	// 无 summary：结构化状态字段（status/severity）在即视为有内容；都没有则空。
	if _, hasStatus := tc.Output["status"]; hasStatus {
		return true, false, false
	}
	if _, hasSev := tc.Output["severity"]; hasSev {
		return true, false, false
	}
	return false, false, true
}

// failedStatusValues 是"工具取数失败"的结构化失败值。注意不含 "critical"/"danger"——
// 那些是"取到了关键异常数据"，属于成功召回，只是数据高危，归成失败会误报无数据。
var failedStatusValues = map[string]bool{
	"error": true, "failed": true, "unavailable": true, "not_available": true,
	"timeout": true, "aborted": true,
}

func isFailedStatus(v string) bool {
	return v != "" && failedStatusValues[strings.ToLower(v)]
}

// noDataSummaryPhrases 是"本维度无数据"的明确摘要短语（取数本身失败），命中任一即
// 视为失败归类。刻意不含 "error"/"failed" 单字——summary 中的 "包含 N 条 error 记录"
// 是数据内容，不是取数失败。
var noDataSummaryPhrases = []string{
	"不可用", "无数据", "未配置", "未接入", "未启用", "未部署",
	"获取失败", "探活失败", "调用失败", "无法获取", "暂不支持",
	"not available", "unavailable", "no data", "not configured", "not enabled",
}

func matchesNoDataPhrase(summary string) bool {
	if summary == "" {
		return false
	}
	lower := strings.ToLower(summary)
	for _, p := range noDataSummaryPhrases {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// summarizeFailures 汇总工具失败原因。
func summarizeFailures(toolCalls []ToolCallLog) string {
	var parts []string
	for _, tc := range toolCalls {
		if tc.Error != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", tc.Tool, tc.Error))
		} else {
			parts = append(parts, fmt.Sprintf("%s: 返回空结果", tc.Tool))
		}
	}
	return strings.Join(parts, "; ")
}

// compressToolOutput 从工具输出中提取关键摘要，丢弃大体积数据。
func compressToolOutput(output map[string]any) string {
	if output == nil {
		return "(无结果)"
	}
	// 优先提取 summary 字段
	if summary, ok := output["summary"].(string); ok && summary != "" {
		return summary
	}
	// 提取 status 字段
	if status, ok := output["status"].(string); ok {
		result := fmt.Sprintf("状态: %s", status)
		if err, ok := output["error"].(string); ok && err != "" {
			result += fmt.Sprintf(", 错误: %s", err)
		}
		return result
	}
	// 提取 severity 字段
	if severity, ok := output["severity"].(string); ok {
		return fmt.Sprintf("严重级别: %s", severity)
	}
	// 兜底：取前 200 字符
	data, _ := json.Marshal(output)
	if len(data) > 200 {
		return string(data[:200]) + "..."
	}
	return string(data)
}
func (e *AgentExecutor) executeTool(ctx context.Context, name string, args string) (string, error) {
	// Kill switch：写工具在 agent 被禁用时直接拒绝
	if !AgentEnabled() {
		return "", fmt.Errorf("agent disabled by operator")
	}
	t, ok := e.toolMap[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	if invokable, ok := t.(tool.InvokableTool); ok {
		return invokable.InvokableRun(ctx, args)
	}
	return "", fmt.Errorf("tool %s does not support invokable run", name)
}

// handleToolCall 执行一次工具调用，返回 (结果字符串, 错误, 写交接)。
// 对 registered Write 工具，在 InvokableRun 之前先过写门（e.writeGate）：
//   - Denied：按工具失败处理（execErr 非空，下游提示 LLM 换路径）；
//   - AutoExec：E2 准入门放行，已按已确认计划自动执行，结果作为工具结果回传历史；
//   - 其它：pending plan 交接（第三个返回值非空，调用方 break loop 终止循环）。
//
// 未挂写门（writeGate==nil）时写工具落到工具层自身的 RequiresConfirmation
// fail-closed 拦截（agent_tools.go）——任何路径都不会"无确认直接执行写"。
func (e *AgentExecutor) handleToolCall(ctx context.Context, user identity.CurrentUser, toolName, toolArgs string) (string, error, *agentWriteOutcome) {
	if e.writeGate != nil {
		if t, ok := tools.Lookup(toolName); ok && t.Operation == tools.Write {
			var input map[string]any
			if err := json.Unmarshal([]byte(toolArgs), &input); err != nil {
				return "", fmt.Errorf("parse write arguments: %w", err), nil
			}
			out, err := e.writeGate(ctx, user, toolName, input)
			if err != nil {
				return "", err, nil
			}
			if out.Denied {
				return "", fmt.Errorf("write denied by policy: %s", out.Reason), nil
			}
			if out.AutoExec {
				return writeOutcomeToolResult(toolName, out), nil, nil
			}
			return "", nil, out // 交接：计划已落库，等待调用方渲染 confirmation_required
		}
	}
	result, err := e.executeTool(ctx, toolName, toolArgs)
	return result, err, nil
}

// toolInfos 返回所有工具的 Info 列表（传给 LLM）。
// toolInfosFiltered 返回过滤后的工具 Info 列表。
func (e *AgentExecutor) toolInfosFiltered(filtered []tool.BaseTool) []*schema.ToolInfo {
	infos := make([]*schema.ToolInfo, 0, len(filtered))
	for _, t := range filtered {
		info, err := t.Info(context.Background())
		if err != nil {
			continue
		}
		infos = append(infos, info)
	}
	return infos
}

// extractJSONBody 从 LLM 输出中提取 JSON：剥离可能的 ```json ... ``` 代码块包裹
// 和首尾空白。输出不是 JSON 时原样返回（由调用方解析失败后回退）。
func extractJSONBody(raw string) string {
	raw = strings.TrimSpace(raw)
	if start := strings.Index(raw, "```"); start >= 0 {
		raw = raw[start+3:]
		if idx := strings.Index(raw, "```"); idx >= 0 {
			raw = raw[:idx]
		}
		// 去掉可能的 "json" 语言标记行
		raw = strings.TrimSpace(raw)
		raw = strings.TrimPrefix(raw, "json")
		raw = strings.TrimSpace(raw)
	}
	return strings.TrimSpace(raw)
}

// toolNames 返回工具名列表（日志用）。
func toolNames(infos []*schema.ToolInfo) []string {
	names := make([]string, len(infos))
	for i, info := range infos {
		names[i] = info.Name
	}
	return names
}

// loadPlanningPrompt 加载 system prompt。
func loadPlanningPrompt() string {
	return `你是一个中间件运维 AI 助手。

你可以使用工具来查询系统状态、执行诊断、检查健康/缓存/安全等。
根据用户请求，自主决定使用哪些工具。不需要用户指定工具名——你从可用工具中选择最合适的。

执行原则：
1. 先理解用户意图
2. 选择最相关的工具执行
3. 根据结果决定是否需要继续检查其他维度
4. 收集足够信息后给出综合结论
5. 不要重复执行同一个工具
6. 一个工具失败不阻塞其他检查

重要规则：
- 用户的每个问题都必须先调用工具获取数据，不要凭空回答
- 不要编造工具返回的数据
- 工具返回错误时，如实告知用户

工具使用边界：
- 只调用与用户请求相关的已注册工具（系统已按请求涉及的域裁剪好工具集，不在其中的工具不可用）。
- 当用户请求的是"命令清单 / 操作步骤 / 查询方法 / 排障思路"这类**知识型问题**，且没有匹配的监控工具时：**直接以中文 Markdown 给出完整的命令清单或操作指南**（如集群工具、查询命令、配置项），不要编造探测结果，也不要只说"已整理"却省略内容。
- 当用户问的是某组件/域"能做什么、是什么、有什么用、有哪些功能"这类**能力介绍问题**时：**不要调用工具**，基于当前可用工具的**名称、域、描述**如实整理介绍（描述优先用工具自带的说明），直接给出中文介绍；不要编造该域不存在的能力，也不要把"功能概述"说成实时故障数据。`
}
