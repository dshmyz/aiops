package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

type EinoPlanner struct {
	chat              model.BaseChatModel
	parser            schema.MessageParser[einoIntent]
	systemPrompt      func() string // nil → use hardcoded einoPlanningPrompt
	knowledge         KnowledgeAugmenter
	audit             *llmAuditRecorder // nil → 不记录 LLM 调用审计（缺口-5 / R1）
	capabilityCatalog string            // 动态能力目录，追加到 system prompt
}

// ChatModel 返回底层的 chat model，用于创建 LLMParamExtractor 等。
func (p *EinoPlanner) ChatModel() model.BaseChatModel {
	return p.chat
}

// KnowledgeAugmenter retrieves relevant knowledge and appends it to the system
// prompt. The assistant package avoids a direct import of the knowledge package by
// using this minimal interface.
type KnowledgeAugmenter interface {
	AugmentPrompt(ctx context.Context, basePrompt, query string) string
}

func NewEinoPlanner(chat model.BaseChatModel) *EinoPlanner {
	return &EinoPlanner{
		chat:   chat,
		parser: schema.NewMessageJSONParser[einoIntent](nil),
	}
}

// NewEinoPlannerWithPromptSource creates an EinoPlanner whose system prompt is
// dynamically resolved via the provided function. This enables hot-reload from
// a prompt.Registry: each Plan/PlanStream call invokes promptFn() to get the
// latest prompt text without restarting the service.
func NewEinoPlannerWithPromptSource(chat model.BaseChatModel, promptFn func() string) *EinoPlanner {
	return &EinoPlanner{
		chat:         chat,
		parser:       schema.NewMessageJSONParser[einoIntent](nil),
		systemPrompt: promptFn,
	}
}

// WithKnowledge wires a knowledge augmenter that appends retrieved operational
// context to the system prompt before each call.
func (p *EinoPlanner) WithKnowledge(aug KnowledgeAugmenter) *EinoPlanner {
	p.knowledge = aug
	return p
}

// WithLLMAudit wires LLM invocation auditing (缺口-5 / R1). audit may be nil
// (no-op); model is the provider model name captured at construction time.
func (p *EinoPlanner) WithLLMAudit(auditSvc *audit.Service, model string) *EinoPlanner {
	p.audit = newLLMAuditRecorder(auditSvc, model, "planner")
	return p
}

// WithCapabilityCatalog 注入动态能力目录。目录文本会追加到 system prompt
// 末尾，让 LLM 看到所有可用的动态能力并据此选择工具。
func (p *EinoPlanner) WithCapabilityCatalog(catalog string) *EinoPlanner {
	p.capabilityCatalog = catalog
	return p
}

// currentPrompt returns the active system prompt text. When a prompt source
// function is configured it is called; otherwise the hardcoded constant is
// used as fallback.
func (p *EinoPlanner) currentPrompt(ctx context.Context, message string) string {
	var base string
	if p.systemPrompt != nil {
		if s := p.systemPrompt(); s != "" {
			base = s
		}
	}
	if base == "" {
		base = einoPlanningPrompt
	}
	if p.knowledge != nil {
		base = p.knowledge.AugmentPrompt(ctx, base, message)
	}
	if p.capabilityCatalog != "" {
		base += "\n\n" + p.capabilityCatalog
	}
	return base
}

// StreamEvent is one increment of a streaming planner/service response.
//
// Delta carries partial LLM output (a fragment of the JSON intent) as it
// arrives from the model. Thinking carries partial reasoning content (the
// model's chain-of-thought) when the model supports it (e.g. DeepSeek-R1,
// QwQ). ToolCall carries tool invocation info (name, input, output) emitted
// during execution so the frontend can display real-time tool call progress.
// The terminal event of a stream sets Done=true; on success Intent is
// populated with the resolved planner intent, and when the event originates
// from the assistant service HandleMessageStream, Response carries the final
// operator-facing response. On failure Err is populated instead.
//
// Err is not JSON-serializable and is tagged json:"-" so the struct can be
// safely marshaled by callers (e.g. an HTTP router writing SSE frames).
type StreamEvent struct {
	Delta    string         `json:"delta,omitempty"`
	Thinking string         `json:"thinking,omitempty"`
	ToolCall *ToolCallEvent `json:"tool_call,omitempty"`
	Progress *ProgressEvent `json:"progress,omitempty"`
	Step     *StepEvent     `json:"step,omitempty"`
	Intent   *Intent        `json:"intent,omitempty"`
	Response *Response      `json:"response,omitempty"`
	Done     bool           `json:"done"`
	Err      error          `json:"-"`
}

// StepEvent reports one executed agent-loop step so the frontend can render a
// step timeline (读/诊断/写) alongside the final answer. StepIndex disambiguates
// repeated tool calls; Status is "running" | "done" | "error".
type StepEvent struct {
	Tool      string         `json:"tool"`
	StepIndex int            `json:"step_index"`
	Status    string         `json:"status"`
	Summary   string         `json:"summary,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	Output    map[string]any `json:"output,omitempty"`
}

// ProgressEvent reports a pipeline stage transition so the frontend can render
// a "进度事件折叠" panel (planning → tool_executing → formatting). The stage
// is one of the Progress* constants below. Detail is an optional human-facing
// hint (e.g. the tool name being executed) and may be empty.
//
// The frontend folds these events into a compact progress timeline; the
// original chain-of-thought is never exposed (per SxDevOps 工程边界). Progress
// events are best-effort: the emitter is nil for non-streaming callers, so
// emitting is a no-op there.
type ProgressEvent struct {
	Stage  string `json:"stage"`
	Detail string `json:"detail,omitempty"`
}

// Pipeline stages reported via ProgressEvent. Stages are emitted in order
// (planning → tool_executing → formatting) by executeFromIntent and
// HandleMessageStream. The terminal Done event is the existing StreamEvent
// {Done: true} and is not duplicated as a progress stage.
const (
	// ProgressPlanning: 模型规划中. Emitted before the planner runs (Plan or
	// PlanStream), signals the LLM is parsing intent.
	ProgressPlanning = "planning"
	// ProgressToolExecuting: 工具执行中. Emitted before each tool invocation
	// (diagnostic / read / write). Detail carries the tool name when known.
	ProgressToolExecuting = "tool_executing"
	// ProgressFormatting: 二阶段整形中. Emitted before the formatter runs.
	// Only fires when a formatter is wired; absent otherwise.
	ProgressFormatting = "formatting"
)

// ToolCallEvent represents a single tool invocation during execution.
// Emitted before and after each tool call so the frontend can show
// real-time progress (calling → done).
type ToolCallEvent struct {
	Tool        string         `json:"tool"`
	Input       map[string]any `json:"input,omitempty"`
	RawResponse map[string]any `json:"raw_response,omitempty"`
	Done        bool           `json:"done"`
}

// maxHistoryTurns limits how many previous turns are forwarded to the LLM
// to keep prompts bounded. Set to 10 (5 user + 5 assistant) which is enough
// to resolve "刚才那个" / "同 environment 再查一个" style references.
const maxHistoryTurns = 10

// maxHistoryChars bounds the total character count of history messages
// forwarded to the LLM. Uses a char/4 token estimate (≈4000 tokens for
// 16000 chars) which is a reasonable budget for a planning prompt. Applied
// after maxHistoryTurns: whichever limit is hit first truncates.
const maxHistoryChars = 16_000

// diagnosticBlockChars is a fixed estimate for the [Last Intent] block of a
// diagnostic intent. Diagnostic fields are short and bounded, so a constant
// estimate avoids marshaling overhead.
const diagnosticBlockChars = 150

func (p *EinoPlanner) Plan(ctx context.Context, user identity.CurrentUser, message string, history []Turn, pageContext PageContext) (Intent, error) {
	ctx, span := tracer().Start(ctx, "eino_planner.Plan",
		trace.WithAttributes(
			attribute.Int("message.length", len(message)),
			attribute.Int("history.length", len(history)),
		))
	defer span.End()
	if p == nil || p.chat == nil {
		return Intent{}, errors.New("eino chat model is required")
	}
	messages := []*schema.Message{schema.SystemMessage(p.currentPrompt(ctx, message))}
	messages = append(messages, historyMessages(history)...)
	messages = append(messages, schema.UserMessage(injectPageContext(message, pageContext)))
	started := time.Now()
	response, err := p.chat.Generate(ctx, messages)
	if err != nil {
		span.RecordError(err)
		return Intent{}, err
	}
	if p.audit != nil {
		p.audit.record(ctx, started, response)
	}
	return p.parseIntent(ctx, response)
}

// parseIntent parses an LLM message into an einoIntent and applies the same
// post-processing as Plan: confidence threshold, diagnostic mapping, and
// empty tool_name clarification. Shared by Plan and PlanStream so the
// streaming path produces intents identical to the one-shot path.
func (p *EinoPlanner) parseIntent(ctx context.Context, response *schema.Message) (Intent, error) {
	parsed, err := p.parser.Parse(ctx, response)
	if err != nil {
		return Intent{}, err
	}
	// A final_answer marks a terminal intent: the planner used tools and is now
	// done, carrying a human-facing summary. This must be checked BEFORE the
	// confidence threshold and the empty-tool_name clarification — the model
	// legitimately leaves tool_name null when it is wrapping up.
	if parsed.FinalAnswer {
		return Intent{
			Done:   true,
			Answer: strings.TrimSpace(parsed.Summary),
			// Done intents carry no tool call; Confidence is preserved so the
			// trace can still report how sure the planner was.
			Confidence:  parsed.Confidence,
			Explanation: strings.TrimSpace(parsed.Explanation),
		}, nil
	}
	// Check confidence threshold: if below 0.7, request clarification
	if parsed.Confidence < 0.7 {
		return Intent{}, ErrClarificationNeeded
	}

	if parsed.Diagnostic != nil {
		return Intent{
			Diagnostic: &diagnostics.Request{
				Domain:       strings.TrimSpace(parsed.Diagnostic.Domain),
				Environment:  strings.TrimSpace(parsed.Diagnostic.Environment),
				ResourceType: strings.TrimSpace(parsed.Diagnostic.ResourceType),
				ResourceName: strings.TrimSpace(parsed.Diagnostic.ResourceName),
				Runbook:      strings.TrimSpace(parsed.Diagnostic.Runbook),
			},
			Confidence:  parsed.Confidence,
			Explanation: strings.TrimSpace(parsed.Explanation),
		}, nil
	}
	if strings.TrimSpace(parsed.ToolName) == "" {
		return Intent{}, ErrClarificationNeeded
	}
	if parsed.Input == nil {
		parsed.Input = map[string]any{}
	}
	return Intent{
		ToolName:    strings.TrimSpace(parsed.ToolName),
		Input:       parsed.Input,
		Confidence:  parsed.Confidence,
		Explanation: strings.TrimSpace(parsed.Explanation),
	}, nil
}

// PlanStream is the streaming variant of Plan. It calls chat.Stream and
// forwards each LLM chunk as a StreamEvent{Delta: ...} on the returned
// channel. When the stream completes, the accumulated text is parsed with the
// same JSON parser used by Plan and a terminal event (Done=true) is emitted
// carrying either the resolved Intent or an error.
//
// If chat.Stream is unavailable or returns an error, PlanStream degrades to
// the one-shot Plan path (chat.Generate) and emits a single Done event. This
// keeps the streaming contract (always exactly one terminal event) while
// falling back gracefully when the underlying model does not support
// streaming or the stream fails to start.
func (p *EinoPlanner) PlanStream(ctx context.Context, user identity.CurrentUser, message string, history []Turn, pageContext PageContext) (<-chan StreamEvent, error) {
	ctx, span := tracer().Start(ctx, "eino_planner.PlanStream",
		trace.WithAttributes(
			attribute.Int("message.length", len(message)),
			attribute.Int("history.length", len(history)),
		))
	defer span.End()
	if p == nil || p.chat == nil {
		return nil, errors.New("eino chat model is required")
	}

	messages := []*schema.Message{schema.SystemMessage(p.currentPrompt(ctx, message))}
	messages = append(messages, historyMessages(history)...)
	messages = append(messages, schema.UserMessage(injectPageContext(message, pageContext)))

	reader, err := p.chat.Stream(ctx, messages)
	if err != nil {
		// 流式降级为一次性: chat.Stream 不可用或出错, 退化为调用 Plan
		// (chat.Generate), 发送单个 Done 事件. 保持流式契约 (总有且仅有一个
		// 终止事件), 同时在底层模型不支持流式时优雅降级.
		span.AddEvent("stream_fallback", trace.WithAttributes(attribute.String("stream.error", err.Error())))
		return p.planStreamFallback(ctx, message, history, pageContext)
	}

	events := make(chan StreamEvent, 16)
	started := time.Now()
	go func() {
		defer close(events)
		defer reader.Close()
		var builder strings.Builder
		var lastResponse *schema.Message
		for {
			chunk, recvErr := reader.Recv()
			if recvErr != nil {
				if errors.Is(recvErr, io.EOF) {
					break
				}
				// Mid-stream error: emit a terminal error event and stop.
				span.RecordError(recvErr)
				events <- StreamEvent{Err: recvErr, Done: true}
				return
			}
			if chunk == nil {
				continue
			}
			// Track the last chunk carrying ResponseMeta for usage audit.
			if chunk.ResponseMeta != nil {
				lastResponse = chunk
			}
			if chunk.ReasoningContent != "" {
				events <- StreamEvent{Thinking: chunk.ReasoningContent}
			}
			if chunk.Content != "" {
				builder.WriteString(chunk.Content)
				events <- StreamEvent{Delta: chunk.Content}
			}
		}
		// Stream finished: parse accumulated text and emit the terminal event.
		intent, planErr := p.parseIntent(ctx, schema.AssistantMessage(builder.String(), nil))
		if p.audit != nil {
			p.audit.record(ctx, started, lastResponse)
		}
		if planErr != nil {
			events <- StreamEvent{Err: planErr, Done: true}
			return
		}
		intentCopy := intent
		events <- StreamEvent{Intent: &intentCopy, Done: true}
	}()
	return events, nil
}

// planStreamFallback is the degraded path used when chat.Stream is unavailable
// or returns an error. It calls Plan (chat.Generate) one-shot and emits a
// single terminal event carrying either the resolved Intent or an error.
func (p *EinoPlanner) planStreamFallback(ctx context.Context, message string, history []Turn, pageContext PageContext) (<-chan StreamEvent, error) {
	events := make(chan StreamEvent, 1)
	go func() {
		defer close(events)
		intent, err := p.Plan(ctx, identity.CurrentUser{}, message, history, pageContext)
		if err != nil {
			events <- StreamEvent{Err: err, Done: true}
			return
		}
		intentCopy := intent
		events <- StreamEvent{Intent: &intentCopy, Done: true}
	}()
	return events, nil
}

// historyMessages converts prior turns into the LLM SDK's message format.
//
// Two-stage truncation bounds token cost:
//  1. Keep only the most recent maxHistoryTurns turns (hard limit).
//  2. From those, walk newest-to-oldest accumulating estimated char count;
//     stop when adding the next turn would exceed maxHistoryChars. At least
//     one turn is always kept (even if it alone exceeds the budget), so the
//     most recent reference is never lost.
//
// Assistant turns carry a structured [Last Intent] block (when Turn.Intent
// is populated) so the LLM can resolve references like "同 environment" /
// "再查一个" deterministically instead of guessing from prose.
//
// Turns with empty content are skipped.
func historyMessages(history []Turn) []*schema.Message {
	if len(history) > maxHistoryTurns {
		history = history[len(history)-maxHistoryTurns:]
	}

	// Walk newest-to-oldest, accumulating char estimates. Keep at least one.
	var selected []Turn
	totalChars := 0
	for i := len(history) - 1; i >= 0; i-- {
		turn := history[i]
		if strings.TrimSpace(turn.Content) == "" {
			continue
		}
		turnChars := estimateTurnChars(turn)
		if totalChars+turnChars > maxHistoryChars && len(selected) > 0 {
			break
		}
		selected = append([]Turn{turn}, selected...)
		totalChars += turnChars
	}

	messages := make([]*schema.Message, 0, len(selected))
	for _, turn := range selected {
		content := strings.TrimSpace(turn.Content)
		switch turn.Role {
		case "system_summary":
			// Inject the rolling conversation summary as a system message so
			// the LLM retains early-turn context without verbatim history.
			messages = append(messages, schema.SystemMessage("[对话摘要]\n"+content))
		case "user":
			messages = append(messages, schema.UserMessage(content))
		case "assistant":
			// A persisted tool_step turn (from a prior request that ended on
			// maxSteps/fallback) replays as tool feedback, not a bare text turn.
			// This is the cross-turn counterpart of the loop's in-run feedback:
			// rendering the executed tool + raw result structurally lets a resumed
			// step continue from what the previous run already found instead of
			// re-reading it as prose. A plain assistant turn (including a prior
			// terminal answer) stays a normal assistant message.
			if turn.ResponseType == responseTypeToolStep && len(turn.Result) > 0 {
				messages = append(messages, schema.AssistantMessage(formatToolStepTurn(turn), nil))
				continue
			}
			// A persisted answer_converged terminal turn is the honest "not fully
			// reasoned through" marker from a prior maxSteps/fallback exit. Mark it
			// explicitly so a continuation reads the preceding tool_steps as a
			// completed first segment to build on, not as a finished conclusion.
			if turn.ResponseType == responseTypeFallbackAnswer {
				messages = append(messages, schema.AssistantMessage(formatFallbackAnswerTurn(content), nil))
				continue
			}
			messages = append(messages, schema.AssistantMessage(formatAssistantTurn(content, turn.Intent), nil))
		}
	}
	return messages
}

// formatToolStepTurn renders a replayed tool_step turn as structured tool
// feedback: which tool ran, its input, and the raw result it returned, plus the
// intent block when present — the same shape the in-loop feedbackTurn feeds the
// replanning LLM, so a continuation behaves like an in-run next step.
func formatToolStepTurn(turn Turn) string {
	var b strings.Builder
	b.WriteString("[已执行的工具步骤]\n")
	if tool, ok := turn.Result["tool"].(string); ok && tool != "" {
		b.WriteString("tool: " + tool + "\n")
	}
	if idx, ok := turn.Result["step_index"]; ok {
		b.WriteString("step_index: " + fmt.Sprint(idx) + "\n")
	}
	if input, ok := turn.Result["input"]; ok {
		if inJSON, err := json.Marshal(input); err == nil {
			b.WriteString("input: " + string(inJSON) + "\n")
		}
	}
	if result, ok := turn.Result["result"]; ok {
		if rJSON, err := json.Marshal(result); err == nil {
			b.WriteString("result: " + string(rJSON) + "\n")
		}
	}
	b.WriteString("summary: " + turn.Content + "\n")
	// Keep the intent block so later turns can resolve references the same way
	// the in-loop feedback does.
	if turn.Intent != nil {
		b.WriteString(formatAssistantTurn("", turn.Intent))
	}
	return strings.TrimSpace(b.String())
}

// formatAssistantTurn appends a [Last Intent] block to the assistant message
// when the turn has a structured Intent. This lets the LLM resolve references
// like "同 environment" / "再查一个" deterministically instead of guessing
// from the user's prose.
//
// Falls back to the original content when:
//   - intent is nil
//   - intent has neither ToolName nor Diagnostic
//   - intent.Input cannot be JSON-marshaled (e.g. contains a channel)
func formatAssistantTurn(content string, intent *Intent) string {
	if intent == nil {
		return content
	}
	if intent.Diagnostic != nil {
		return content + "\n\n[Last Intent]\ndiagnostic: domain=" + intent.Diagnostic.Domain +
			", environment=" + intent.Diagnostic.Environment +
			", resource_type=" + intent.Diagnostic.ResourceType +
			", resource_name=" + intent.Diagnostic.ResourceName
	}
	if intent.ToolName == "" {
		return content
	}
	inputJSON, err := json.Marshal(intent.Input)
	if err != nil {
		return content
	}
	return content + "\n\n[Last Intent]\ntool_name: " + intent.ToolName + "\ninput: " + string(inputJSON)
}

// formatFallbackAnswerTurn renders a replayed answer_converged terminal turn so
// the resumed planner treats the prior run as an incomplete first segment. A
// follow-up that continues should build on the already-executed tool_steps
// rather than interpreting this as a final, authoritative conclusion.
func formatFallbackAnswerTurn(content string) string {
	return "[上一轮未达到明确结论（兜底收尾）]\n" + content + "\n如本次继续深挖，请基于上述已执行的工具步骤继续，而不要重复执行已完成的部分。"
}

// estimateTurnChars estimates the character footprint of a turn in the LLM
// prompt, including the [Last Intent] block for assistant turns with Intent.
// Used for token budget truncation; not a precise token count.
func estimateTurnChars(turn Turn) int {
	content := strings.TrimSpace(turn.Content)
	if turn.Intent == nil {
		return len(content)
	}
	if turn.Intent.Diagnostic != nil {
		return len(content) + diagnosticBlockChars
	}
	if turn.Intent.ToolName == "" {
		return len(content)
	}
	inputJSON, err := json.Marshal(turn.Intent.Input)
	if err != nil {
		return len(content) + 50 // fallback estimate for marshaling failure
	}
	// Template: "\n\n[Last Intent]\ntool_name: \ninput: "
	return len(content) + 40 + len(turn.Intent.ToolName) + len(inputJSON)
}

type einoIntent struct {
	ToolName    string          `json:"tool_name"`
	Input       map[string]any  `json:"input"`
	Diagnostic  *einoDiagnostic `json:"diagnostic"`
	Confidence  float64         `json:"confidence"`
	Explanation string          `json:"explanation"`
	// FinalAnswer marks a terminal intent: the planner has finished answering
	// the user's question with the tools already used and outputs a human-facing
	// Summary. The agent loop should stop and emit this summary rather than
	// plan another step.
	FinalAnswer bool   `json:"final_answer"`
	Summary     string `json:"summary"`
}

type einoDiagnostic struct {
	Domain       string `json:"domain"`
	Environment  string `json:"environment"`
	ResourceType string `json:"resource_type"`
	ResourceName string `json:"resource_name"`
	Runbook      string `json:"runbook"`
}

const einoPlanningPrompt = `你是一个中间件运维副驾驶的意图规划器。

## 核心职责
分析用户消息，返回严格的JSON格式意图规划。你只能提出候选意图，Go后端会执行静态工具注册、策略、确认、执行和审计规则。

## 动态能力（重要）
系统注册了多个动态能力（见下方"可用的动态能力"列表）。当用户请求匹配其中某个能力时：
1. 直接用该能力的 tool_name 填入 tool_name 字段
2. 从用户消息中提取该能力 input_schema 所需的参数填入 input 字段
3. 用户未指定 environment 时默认 "prod"
4. 不需要走 diagnostic 通道，直接用 tool_name

## 输出格式
只返回JSON，不要包含任何其他文本：
{
  "tool_name": string | null,      // 工具名称，如 "cluster.status.read"、"topic.retention.set"
  "input": object | null,          // 工具输入参数，键值对形式
  "diagnostic": object | null,     // 诊断请求对象，仅用于健康/容量/延迟检查
  "confidence": number,            // 置信度，0.0-1.0
  "explanation": string,           // 简短的中文解释
  "final_answer": boolean,         // 是否已完成回答，true 时给出 summary
  "summary": string | null         // 完成时的最终答复（human-facing），final_answer=true 时必填
}

## 字段说明

### tool_name（工具名称）
- 字符串或null
- 当用户请求普通工具操作时填写
- 静态工具示例："cluster.status.read"、"topic.retention.set"
- 动态能力：从下方"可用的动态能力"列表中选择匹配的 tool_name
- 当用户请求诊断检查且没有匹配的动态能力时设为null

### input（输入参数）
- 对象或null
- 包含工具执行所需的参数
- 参数应从用户消息中提取
- 示例：{"cluster_name": "prod-cluster-01", "environment": "prod"}

### diagnostic（诊断对象）
- 对象或null
- 仅用于GlusterFS、MinIO或Kafka的健康、容量或消费者延迟检查
- 当有匹配的动态能力时，优先用 tool_name 而不是 diagnostic
- 结构：
  {
    "domain": "glusterfs" | "minio" | "kafka",
    "environment": "prod" | "staging" | "dev",
    "resource_type": "volume" | "bucket" | "consumer_group",
    "resource_name": string,
    "runbook": "health" | "capacity" | "consumer_lag"
  }
- 普通工具意图时必须设为null

### confidence（置信度）
- 浮点数，范围0.0-1.0
- 0.9以上：明确的意图
- 0.7-0.9：较明确的意图
- 0.5-0.7：需要进一步澄清
- 0.5以下：应返回clarification_needed

### explanation（解释）
- 简短的中文字符串
- 解释为什么做出这个意图判断
- 示例："用户想查看生产集群状态"

### final_answer（完成标记）
- 布尔值，默认 false
- 这是一个有状态的agent循环：你会在历史[Last Intent]中看到自己之前调用的工具结果。
- 当"已用工具回答完用户问题"时，输出 final_answer: true 并提供 summary（给用户看的中文最终答复），同时 tool_name/input/diagnostic 置 null。
- 当你"还需要调用另一个工具"（继续排查、验证、对比）时，输出 final_answer: false 并按正常意图填 tool_name。
- 只有当信息已足够、不需要再调用工具时才置 true——回答完就结束，不要空转。

### agent 循环收敛规则（重要）
- **不要重复执行已运行过的工具**：如果历史[Last Intent]中某个工具已经执行过且返回值确定，就不要再次调用同一个工具。反复返回同一结果的重复调用是空转，应直接 final_answer: true 汇总。
- **多能力系统要全面检查**：当用户请求涉及一个有多个能力的系统（如 moonlightbox 有 health/cache/security/dashboard），应该依次检查各个维度，收集完整信息后再给出综合结论。不要查一个就收尾。
- **单域诊断拿到结果后可以继续**：如果还有同 domain 的其他能力没查，继续检查下一个维度，直到覆盖主要方面再收尾。
- 只有当你需要**新的信息**（另一个域、另一个资源、或排查异常的下一步证据）时才调用新工具，并且**换用与之前不同的工具**。
- 若某工具执行失败，可根据错误换用其他候选工具，但失败次数超过预算仍无法推进时应 final_answer: true 汇总已知信息，而不是空转。

### summary（最终答复）
- 字符串或null
- 仅当 final_answer: true 时必填
- 用中文给出面向用户的简洁、完整的结论（包含关键数字/状态），不要重复输出 JSON。

## 参数提取规则
1. 从用户消息中直接提取明确的参数值
2. 使用历史对话中的信息补充缺失参数
3. 对于指代词（"刚才那个"、"同environment"等），从历史对话中查找对应值
4. 默认环境为"prod"，除非用户明确指定其他环境

## confidence阈值和处理逻辑
- confidence >= 0.7：正常返回意图
- confidence < 0.7：返回clarification_needed类型
- tool_name为空且diagnostic为空：返回clarification_needed类型
- 例外：final_answer: true 时不受上述限制——完成时 tool_name/diagnostic 本来就该为空

## 多轮对话利用指南
1. 优先查看历史对话中的[Last Intent]块
2. 当用户说"同environment"时，使用历史对话中的environment值
3. 当用户说"再查一个"时，使用历史对话中的tool_name
4. 当用户说"刚才那个"时，引用历史对话中的资源名称

## Few-shot示例

### 示例1：普通工具调用
用户："查看生产集群状态"
输出：
{
  "tool_name": "cluster.status.read",
  "input": {"environment": "prod"},
  "diagnostic": null,
  "confidence": 0.95,
  "explanation": "用户想查看生产环境集群状态"
}

### 示例2：诊断请求
用户："检查kafka消费者的延迟情况"
输出：
{
  "tool_name": null,
  "input": null,
  "diagnostic": {
    "domain": "kafka",
    "environment": "prod",
    "resource_type": "consumer_group",
    "resource_name": "*",
    "runbook": "consumer_lag"
  },
  "confidence": 0.9,
  "explanation": "用户想检查Kafka消费者延迟"
}

### 示例3：需要澄清
用户："帮我看一下"
输出：
{
  "tool_name": null,
  "input": null,
  "diagnostic": null,
  "confidence": 0.3,
  "explanation": "用户请求不明确，需要澄清具体要查看什么"
}

### 示例4：利用历史对话
历史：[Last Intent] tool_name: cluster.status.read, input: {"environment": "prod", "cluster_name": "cluster-01"}
用户："同environment再查topic状态"
输出：
{
  "tool_name": "topic.status.read",
  "input": {"environment": "prod"},
  "diagnostic": null,
  "confidence": 0.85,
  "explanation": "用户想在相同环境下查看topic状态"
}

### 示例5：告警查询
用户："当前有哪些告警？"
输出：
{
  "tool_name": "alert.query",
  "input": {"environment": "prod"},
  "diagnostic": null,
  "confidence": 0.9,
  "explanation": "用户想查看生产环境当前告警",
  "final_answer": false
}

### 示例6：工具结果反馈后完成回答
历史：[Last Intent] tool_name: alert.query, input: {"environment":"prod"}, result: [{"name":"kafka 慢消费者","severity":"warning"}]
用户："当前有哪些告警？"
输出：
{
  "tool_name": null,
  "input": null,
  "diagnostic": null,
  "confidence": 0.95,
  "explanation": "已用告警工具取得生产环境告警，可以回答",
  "final_answer": true,
  "summary": "生产环境当前有 1 条告警：kafka 慢消费者（warning）。"
}

### 示例7：单域健康检查完成立即收尾（不要空转重复同一工具）
历史：[Last Intent] diagnostic: domain=glusterfs, environment=prod, resource_type=volume, resource_name=data；结果摘要：glusterfs.volume.health.read：诊断完成：1 个观察，1 个发现，1 个建议
用户："检查 prod glusterfs data volume 健康"
输出：
{
  "tool_name": null,
  "input": null,
  "diagnostic": null,
  "confidence": 0.95,
  "explanation": "glusterfs data volume 健康检查已完成并拿到结论，无需再次调用同一工具，直接汇总",
  "final_answer": true,
  "summary": "prod glusterfs data volume 健康检查完成：1 个观察、1 个发现、1 个建议。"
}

## 重要约束
1. 只能提出候选意图，不能执行任何操作
2. Go后端会执行所有验证、策略和执行逻辑
3. 必须返回严格的JSON格式
4. 不要包含任何非JSON内容`

// injectPageContext prepends a page-context hint to the user message so the
// LLM planner can use it to fill in missing fields (environment, domain,
// resource) when the message itself does not mention them. The hint is a
// compact single-line prefix; message tokens still take precedence because
// the LLM sees both the hint and the original message. When pageContext is
// empty, the message is returned unchanged (backward compatible).
func injectPageContext(message string, pageContext PageContext) string {
	parts := make([]string, 0, 4)
	if pageContext.Environment != "" {
		parts = append(parts, "environment="+pageContext.Environment)
	}
	if pageContext.Domain != "" {
		parts = append(parts, "domain="+pageContext.Domain)
	}
	if pageContext.ResourceType != "" {
		parts = append(parts, "resource_type="+pageContext.ResourceType)
	}
	if pageContext.ResourceName != "" {
		parts = append(parts, "resource_name="+pageContext.ResourceName)
	}
	if len(parts) == 0 {
		return message
	}
	return "[页面上下文 " + strings.Join(parts, ", ") + "] " + message
}
