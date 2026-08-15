package assistant

import (
	"context"
	"encoding/json"
	"fmt"
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
)

// AgentExecutor 基于 LLM function calling 的执行器。
// 替代原有的 EinoPlanner JSON 解析 + 手动 agent loop。
// LLM 通过原生 tool calling 选工具，框架自动执行，结果自动回传。
type AgentExecutor struct {
	chat          model.BaseChatModel  // 执行层：选工具、调参数
	reasoningChat model.BaseChatModel  // 分析层：深度推理、生成报告（可为 nil）
	tools         []tool.BaseTool
	toolMap       map[string]tool.BaseTool // name → tool 快速查找
	audit         *audit.Service
	modelName     string
	maxSteps      int
	knowledge     *KnowledgeStore // 知识库（可为 nil）
	skills        SkillLookup     // SOP/Skill 查询（可为 nil）
	cache         *ResponseCache  // LLM 响应缓存（可为 nil）
	rateLimiter   *RateLimiter    // LLM 调用限流（可为 nil）
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

// AgentRunResult 是执行结果。
type AgentRunResult struct {
	Answer      string         // LLM 最终回复
	ToolCalls   []ToolCallLog  // 每步工具调用记录
	Reasoning   []string       // 每轮 LLM 的中间推理（决策链）
	TurnCount   int            // LLM 调用次数
	Error       error
}

// ToolCallLog 记录一次工具调用。
type ToolCallLog struct {
	Tool    string         `json:"tool"`
	Input   string         `json:"input"`
	Output  map[string]any `json:"output,omitempty"`
	Error   string         `json:"error,omitempty"`
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
	result := e.runWithCallbackRole(ctx, role, message, history, onStep)
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
	Step    int    `json:"step"`
	Tool    string `json:"tool"`
	Status  string `json:"status"` // "done" | "error"
	Summary string `json:"summary"`
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

// runWithCallbackRole 是 RunWithRoleCallback 的实现（defer 指标在包装层处理）。
func (e *AgentExecutor) runWithCallbackRole(ctx context.Context, role AgentRole, message string, history []Turn, onStep func(AgentStepEvent)) *AgentRunResult {
	// 缓存检查：相同问题直接返回缓存结果
	if e.cache != nil {
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
		// SOP/Skill：只列出可用 skill 名，不注入全文。
		// 修复：全量注入 26 个 skill 的内容（每个截 500 字 ≈ 13000 字）会把
		// system prompt 塞爆、淹没用户指令，是 agent "变笨"的主因之一。
		if e.skills != nil {
			if skillSummaries, err := e.skills.ListSkillsByAction(ctx, ""); err == nil && len(skillSummaries) > 0 {
				var sb strings.Builder
				sb.WriteString("\n\n可参考的运维手册（SOP）：")
				slugs := make([]string, 0, len(skillSummaries))
				for _, s := range skillSummaries {
					slugs = append(slugs, s.Slug)
				}
				sb.WriteString(strings.Join(slugs, "、"))
				sb.WriteString("。如请求匹配某手册场景，遵循其要点执行；否则不要罗列手册内容。")
				systemPrompt += sb.String()
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

	// 意图规划：先让 LLM 判断用户意图（知识型 / 工具型+涉及域），按意图裁剪
	// 工具集。不用"域名+分隔符"这类用户不知道的规则——意图由 LLM 语义理解，
	// 用户怎么问都行。知识型 → 空工具集，LLM 直接回答；工具型 → 只给该域工具。
	intent := e.planIntent(ctx, message)
	allTools := e.toolsForDomain(intent.Domain)

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

		// 调 LLM（带 tools 参数）
		toolInfos := e.toolInfosFiltered(allTools)
		logWithCtx(ctx, "[agent] calling LLM with %d tools: %v", len(toolInfos), toolNames(toolInfos))
		e.acquireLLM()
		resp, err := e.chat.Generate(ctx, messages,
			model.WithTools(toolInfos),
		)
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
				honest := fmt.Sprintf("抱歉，本次检查的所有工具调用都失败了，无法获取任何数据。失败详情：%s", summarizeFailures(allToolCalls))
				e.saveKnowledgeWithReasoning(ctx, message, allToolCalls, reasoningTrail)
				result := &AgentRunResult{
					Answer:    honest,
					ToolCalls: allToolCalls,
					Reasoning: reasoningTrail,
					TurnCount: step + 1,
				}
				if e.cache != nil {
					e.cache.Set(ctx, message, result)
				}
				return result
			}
			// 如果已有工具调用结果，走分析层生成深度报告
			if len(allToolCalls) > 0 {
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
			result := &AgentRunResult{
				Answer:    resp.Content,
				ToolCalls: allToolCalls,
				Reasoning: reasoningTrail,
				TurnCount: step + 1,
			}
			if e.cache != nil && result.Answer != "" {
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

			// 执行工具
			result, execErr := e.executeTool(ctx, toolName, toolArgs)
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

			// 流式回调：通知前端工具调用完成
			if onStep != nil {
				status := "done"
				summary := ""
				if execErr != nil {
					status = "error"
					summary = execErr.Error()
				} else if toolLog.Output != nil {
					if s, ok := toolLog.Output["summary"].(string); ok {
						summary = s
					} else if s, ok := toolLog.Output["status"].(string); ok {
						summary = s
					}
				}
				onStep(AgentStepEvent{
					Step:    stepIdx,
					Tool:    toolName,
					Status:  status,
					Summary: summary,
				})
			}

			// 把工具结果追加到消息历史
			toolMsg := schema.ToolMessage(result, tc.ID)
			toolMsg.ToolName = toolName
			messages = append(messages, toolMsg)
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
			if e.cache != nil {
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
	// 缓存：存入响应缓存
	if e.cache != nil && result.Answer != "" {
		e.cache.Set(ctx, message, result)
	}
	return result
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
		if tc.Error != "" {
			failedCount++
			sb.WriteString(fmt.Sprintf("调用失败: %s\n", tc.Error))
		} else if len(tc.Output) == 0 {
			emptyCount++
			sb.WriteString("(工具返回空结果)\n")
		} else {
			// 压缩数据：只保留 summary/status 等关键字段，丢弃大体积 data
			summary := compressToolOutput(tc.Output)
			if summary == "(无结果)" || summary == "" {
				emptyCount++
			} else if strings.Contains(strings.ToLower(summary), "error") ||
				strings.Contains(strings.ToLower(summary), "failed") ||
				strings.Contains(strings.ToLower(summary), "不可用") {
				// 工具返回内容含 error/failed/不可用：如 K8s 未配置、探活失败等，
				// 归入失败而非成功，避免把错误响应统计成"成功获取数据"。
				failedCount++
			} else {
				successCount++
			}
			sb.WriteString(summary)
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

// intentPlan 是意图规划的输出：LLM 判断用户这次请求要工具还是直接回答。
type intentPlan struct {
	// Intent: "tool_call" | "knowledge"
	Intent string `json:"intent"`
	// Domain：tool_call 时涉及的工具域（如 kafka/minio），knowledge 时为 ""。
	Domain string `json:"domain"`
}

// planIntent 让 LLM 判断用户意图（知识型 / 工具型+涉及域）。
// 意图由 LLM 语义理解，用户怎么问都行——不依赖"域名+分隔符"这类用户不知道的
// 规则。知识型（要命令/步骤/方法）→ Intent=knowledge，agent 不拿工具直接回答；
// 工具型（查实时状态）→ Intent=tool_call + Domain，只给该域工具。
//
// 调用失败/解析失败时回退 Intent=tool_call、Domain=""（工具集为空，LLM 直接
// 回答），保证知识型请求不被误伤、工具型请求也不会拿到无关工具乱用。
func (e *AgentExecutor) planIntent(ctx context.Context, message string) intentPlan {
	if e.chat == nil {
		return intentPlan{Intent: "tool_call"}
	}
	const prompt = `判断用户请求的意图，只返回 JSON（不要 markdown 代码块）：
{
  "intent": "tool_call" | "knowledge",
  "domain": "字符串或 null",
  "explanation": "一句话判断依据"
}

判断标准：
- tool_call：用户想查询/诊断某个系统的实时状态或数据，需要调用监控工具获取数据。domain 填涉及的中间件域（如 kafka、minio、glusterfs、redis）。
- knowledge：用户想要命令清单、操作步骤、查询方法、排障思路等知识型回答，不需要调用工具。domain 填 null。
- 判断不出来时优先 tool_call。

用户消息：%s`

	messages := []*schema.Message{
		schema.SystemMessage("你是意图分类器，只返回 JSON。"),
		schema.UserMessage(fmt.Sprintf(prompt, message)),
	}
	e.acquireLLM()
	resp, err := e.chat.Generate(ctx, messages)
	e.releaseLLM()
	if err != nil {
		logWithCtx(ctx, "[agent] intent planning failed: %v", err)
		return intentPlan{Intent: "tool_call"}
	}
	var plan intentPlan
	if err := json.Unmarshal([]byte(extractJSONBody(resp.Content)), &plan); err != nil {
		logWithCtx(ctx, "[agent] intent planning parse failed, raw=%s", resp.Content)
		return intentPlan{Intent: "tool_call"}
	}
	if plan.Intent != "knowledge" {
		plan.Intent = "tool_call"
	}
	plan.Domain = strings.TrimSpace(plan.Domain)
	logWithCtx(ctx, "[agent] intent: %s domain=%q", plan.Intent, plan.Domain)
	return plan
}

// toolsForDomain 按意图规划的域裁剪工具集：只给该域已注册的能力工具。
// 知识型（Domain 为空）→ 空工具集，LLM 只能直接文字回答；未注册域 → 空（LLM
// 拿不到工具也直接回答）。http.probe 不进入 agent 工具集——探活由独立的健康
// 检查器承担，agent 专注已发布能力工具，避免通用工具被 LLM 滥用。
func (e *AgentExecutor) toolsForDomain(domain string) []tool.BaseTool {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return nil
	}
	out := make([]tool.BaseTool, 0, len(e.tools))
	for _, t := range e.tools {
		info, _ := t.Info(context.Background())
		if info == nil {
			continue
		}
		if strings.HasPrefix(info.Name, domain+".") {
			out = append(out, t)
		}
	}
	return out
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
- 当用户请求的是"命令清单 / 操作步骤 / 查询方法 / 排障思路"这类**知识型问题**，且没有匹配的监控工具时：**直接以中文 Markdown 给出完整的命令清单或操作指南**（如集群工具、查询命令、配置项），不要编造探测结果，也不要只说"已整理"却省略内容。`
}
