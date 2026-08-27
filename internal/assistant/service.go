package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/autonomy"
	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/orchestrator"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// tracer returns the assistant package's instrumentation scope. Using a
// dedicated tracer name keeps assistant spans grouped in the trace backend.
func tracer() trace.Tracer {
	return otel.Tracer("github.com/gracegaoya/ai-operations-copilot/assistant")
}

var (
	ErrPolicyDenied        = errors.New("assistant intent denied by policy")
	ErrForeignConversation = errors.New("conversation does not belong to the current user")
)

const (
	// clarificationMessage 是确定性模式（未配置 LLM）下默认的澄清文案：
	// 该模式只做数据驱动的中间件域诊断，平台意图（告警/态势/事件/任务）与写意图
	// 均需 LLM 路径，提示用户点名已接入域或配置大模型。
	clarificationMessage   = "当前为确定性模式，仅支持中间件域诊断（请在消息中点名 kafka/minio/glusterfs 等已接入域）；告警/态势/事件/任务等平台查询需配置大模型（COPILOT_ASSISTANT_PROVIDER=eino-openai）。"
	conversationTitleMax   = 50
	conversationPreviewMax = 500
	historyTurnLimit       = 10
)

// DiagnosticRunner runs a diagnostic request and returns a package. The
// concrete *diagnostics.Service wired by the constructors satisfies it; tests
// may substitute a fake to surface actionable recommendations without the real
// diagnostics service.
type DiagnosticRunner interface {
	Run(ctx context.Context, user identity.CurrentUser, request diagnostics.Request) (diagnostics.Package, error)
}

// DryRunRunner previews a write operation without executing it. The concrete
// *execution.DryRunService satisfies it; tests may substitute a fake. When
// wired, the assistant auto-previews every pending write plan and attaches the
// result as a risk_notice block so the operator sees the impact before
// confirming.
type DryRunRunner interface {
	DryRun(ctx context.Context, toolName string, input map[string]any) (execution.DryRunResult, error)
}

type Service struct {
	planner       Planner
	reads         *execution.ReadOnlyService
	plans         *plans.Service
	diagnostics   DiagnosticRunner
	conversations store.AssistantConversationStore
	compactor     Compactor
	clock         func() time.Time
	// audit 记录 agent 循环内策略拒绝事件（读/写路径提前 return 时，走不到
	// ReadOnlyService 内部审计）。为 nil 时跳过记录，不阻塞调用。
	audit *audit.Service
	// toolCallEmitter is called before and after each tool invocation during
	// executeFromIntent. It is nil for non-streaming callers (HandleMessage)
	// and wired by HandleMessageStream to forward events to the SSE channel.
	toolCallEmitter func(ToolCallEvent)
	// progressEmitter is called at each pipeline stage transition (planning,
	// tool_executing, formatting) during executeFromIntent. It is nil for
	// non-streaming callers and wired by HandleMessageStream so the frontend
	// can render a progress timeline while waiting for the final response.
	progressEmitter func(ProgressEvent)
	// formatter 是二阶段整形器，在一阶段取证后把 Answer 转成 Summary + Blocks。
	// 为 nil 时不做二阶段整形，保持原有行为（向后兼容）。
	formatter ResponseFormatter
	// dryRun 预演写操作，在 plan 创建后自动预演并把结果作为 risk_notice block
	// 附到 confirmation_required 响应。为 nil 时不预演（向后兼容）。
	dryRun DryRunRunner
	// runbookRouter 匹配低风险 Runbook 并自动执行写操作（借鉴-5）。为 nil 时
	// 所有写操作走 confirmation_required（向后兼容）。
	runbookRouter *RunbookRouter
	// execution 在低风险 Runbook 自动执行时使用（ExecuteConfirmedStoredPlan）。
	// 为 nil 时低风险 Runbook 退化为 confirmation_required。
	execution ExecutionRunner
	// agentLoopEnabled opts the service into the autonomous agent loop for
	// agent-capable planners (EinoPlanner). Default false preserves single-plan
	// streaming semantics; enable it in production wiring for LLM planners.
	agentLoopEnabled bool
	// autonomy 是 Low-Risk Admission Controller（E2）。为 nil 时自动执行一律禁用
	// （fail-closed），所有写操作退化为 confirmation_required，与 launch 前的行为一致。
	autonomy *autonomy.Controller
	// agentExecutor 是基于 LLM function calling 的新执行器。
	// 不为 nil 时，handleStatelessWithHistory 走新路径（LLM 自主选工具），
	// 跳过旧的 planner + capability matching 链路。
	agentExecutor *AgentExecutor
	// supervisor 是通用助手入口（恒 supervisor 角色，见 supervisor.go）。
	// WithAgentExecutor 注入 executor 时自动创建，nil 时退化为直接调用 executor。
	supervisor *Supervisor
}

// ExecutionRunner 自动执行已确认的 plan（低风险 Runbook 路径）。
// *execution.Service 满足本接口。
type ExecutionRunner interface {
	ExecuteConfirmedStoredPlan(ctx context.Context, planID string) (execution.Execution, error)
}

// NewService wires the assistant service with optional conversation
// persistence. When conversations is nil the service behaves exactly like
// the pre-multiturn implementation: HandleMessage ignores conversationID and
// does not persist turns. clock may be nil, in which case time.Now is used.
func NewService(planner Planner, reads *execution.ReadOnlyService, planService *plans.Service, conversations store.AssistantConversationStore) *Service {
	return NewServiceWithCompactor(planner, reads, planService, conversations, nil)
}

// NewServiceWithCompactor is like NewService but also wires a rolling
// summarization compactor. When both compactor and conversations are non-nil,
// loadHistory compacted old turns into a summary once the unsummarized turn
// count exceeds compactThreshold. compactor is ignored when conversations is
// nil (stateless mode has no history to compact).
func NewServiceWithCompactor(planner Planner, reads *execution.ReadOnlyService, planService *plans.Service, conversations store.AssistantConversationStore, compactor Compactor) *Service {
	if planner == nil {
		planner = DeterministicPlanner{}
	}
	clock := time.Now
	return &Service{
		planner:       planner,
		reads:         reads,
		plans:         planService,
		diagnostics:   diagnostics.NewService(reads, nil),
		conversations: conversations,
		compactor:     compactor,
		clock:         clock,
	}
}

// WithDiagnostics overrides the diagnostics runner used to execute diagnostic
// intents. It is primarily a testing seam: production callers rely on the
// default *diagnostics.Service wired by the constructors. A nil argument is
// ignored so callers can chain safely.
func (s *Service) WithDiagnostics(d DiagnosticRunner) *Service {
	if d != nil {
		s.diagnostics = d
	}
	return s
}

// WithToolCallEmitter wires a callback that is invoked before and after each
// tool invocation during executeFromIntent. Used by HandleMessageStream to
// forward real-time tool call events to the SSE channel. A nil argument clears
// the emitter (non-streaming callers).
func (s *Service) WithToolCallEmitter(fn func(ToolCallEvent)) *Service {
	s.toolCallEmitter = fn
	return s
}

// WithProgressEmitter wires a callback that is invoked at each pipeline stage
// transition (planning, tool_executing, formatting) during executeFromIntent.
// Used by HandleMessageStream to forward progress events to the SSE channel so
// the frontend can render a progress timeline. A nil argument clears the
// emitter (non-streaming callers).
func (s *Service) WithProgressEmitter(fn func(ProgressEvent)) *Service {
	s.progressEmitter = fn
	return s
}

// emitProgress is a nil-safe helper for firing a progress event. It is a no-op
// when no emitter is wired (non-streaming callers), keeping executeFromIntent
// reusable across both streaming and one-shot paths.
func (s *Service) emitProgress(stage, detail string) {
	if s.progressEmitter != nil {
		s.progressEmitter(ProgressEvent{Stage: stage, Detail: detail})
	}
}

// WithFormatter wires a second-stage response formatter. When set, the service
// invokes Format after the first-stage tool execution to produce a natural
// language Summary and structured Blocks. When nil (default), the service
// behaves as before — no second-stage formatting, Answer is returned as-is.
func (s *Service) WithFormatter(f ResponseFormatter) *Service {
	s.formatter = f
	return s
}

// WithDryRunRunner wires a dry-run preview runner. When set, the service
// auto-previews every pending write plan after creation and attaches the
// result as a risk_notice block to the confirmation_required response. When
// nil (default), no preview is produced. Dry-run is best-effort: preview
// failures (e.g. tool not supported) are silently ignored so the plan is
// still returned for confirmation.
func (s *Service) WithDryRunRunner(r DryRunRunner) *Service {
	s.dryRun = r
	return s
}

// WithRunbookRouter wires Runbook 匹配（借鉴-5）。为 nil 时所有写操作走
// confirmation_required（向后兼容）。
func (s *Service) WithRunbookRouter(r *RunbookRouter) *Service {
	s.runbookRouter = r
	return s
}

// WithExecutionRunner wires 低风险 Runbook 自动执行器。为 nil 时低风险
// Runbook 退化为 confirmation_required。
func (s *Service) WithExecutionRunner(e ExecutionRunner) *Service {
	s.execution = e
	return s
}

// WithAuditService wires 审计服务，用于记录 agent 循环内策略拒绝事件。为 nil
// 时跳过审计，不阻塞调用。
func (s *Service) WithAuditService(a *audit.Service) *Service {
	s.audit = a
	return s
}

// WithAgentLoop opts the service into the autonomous agent loop for
// agent-capable planners (EinoPlanner). When enabled, HandleMessageStream runs
// the multi-step plan→execute→replan loop; when disabled it preserves the
// existing single-plan streaming path. Deterministic planners never loop, even
// when enabled.
func (s *Service) WithAgentLoop(enabled bool) *Service {
	s.agentLoopEnabled = enabled
	return s
}

// WithAutonomy wires the Low-Risk Admission Controller (E2). When set, low-risk
// auto-execution (direct runbook / agent-loop write) only happens if the
// controller admits it; when nil (default) all auto-execution is disabled
// (fail-closed) and writes fall back to confirmation_required.
func (s *Service) WithAutonomy(c *autonomy.Controller) *Service {
	s.autonomy = c
	return s
}

// WithAgentExecutor 注入基于 LLM function calling 的执行器。
// 设置后 handleStatelessWithHistory 走新路径：LLM 自主选工具、自动循环。
// 同时自动创建 Supervisor 作为通用助手分派入口。
//
// 安全边界装配（executor 活跃路径缺失就要出问题清单里的那三个）：
//  1. 写门：把 Service 侧的 policy/E2/待确认交接三态写门挂给 executor，Actively
//     execute 的写工具不再"在 confirm 信任等级下无确认直接执行"；
//  2. 声明证据顺序：把 runbookRouter 的 tool_sequence 解析挂给 executor，模型提前
//     收尾时会收到引导轮（该特性此前只在被 shadow 的 agent loop 里生效）。
func (s *Service) WithAgentExecutor(e *AgentExecutor) *Service {
	e.WithWriteGate(s.executorWriteGate)
	e.WithSequenceFor(s.agentLoopSequence)
	s.agentExecutor = e
	s.supervisor = NewSupervisor(e)
	return s
}

// admitAutoExec 是 Service 侧对自动执行的统一准入入口（E2）：把一次低风险写请求交给
// Low-Risk Admission Controller 判定。未装配控制器（nil）视为 fail-closed（拒绝自动
// 执行，回落人工确认），与登录前行为一致。调用方负责任何驳回都静默退化为普通写路径。
//
// templateRiskLow 决定风险门语义：
//   - nil：裸写（无 runbook 模板评审），用 Admit 严格门——工具自身 risk 必须 low
//     （agent loop 的裸写路径）。
//   - 非 nil：有模板评审单元，用 AdmitRunbook——模板评审为 low 即满足自动化授权
//     （direct / scheduled runbook 路径，工具可 Medium+ 但仍带 governance）。
func (s *Service) admitAutoExec(ctx context.Context, user identity.CurrentUser, tool tools.Tool, decision policy.Decision, source autonomy.Source, templateRiskLow *bool) bool {
	if s.autonomy == nil {
		return false // fail-closed：无控制器即不自动执行
	}
	if templateRiskLow == nil {
		return s.autonomy.Admit(ctx, user, tool, decision) == nil
	}
	return s.autonomy.AdmitRunbook(ctx, user, tool, decision, *templateRiskLow) == nil
}

// recordAutoExec 在自动执行成功后记一次每日上限计数（不阻塞）。控制器未装配或未启用
// 每日上限时为 no-op。
func (s *Service) recordAutoExec(ctx context.Context, user identity.CurrentUser) {
	if s.autonomy == nil {
		return
	}
	s.autonomy.Record(ctx, user)
}

// boolPtr returns a pointer to b, for callers distinguishing "no template"
// (nil) from a concrete template risk value in admitAutoExec.
func boolPtr(b bool) *bool { return &b }

type Response struct {
	Type               string               `json:"type"`
	Tool               string               `json:"tool,omitempty"`
	Answer             map[string]any       `json:"answer,omitempty"`
	PlanID             string               `json:"plan_id,omitempty"`
	Status             string               `json:"status,omitempty"`
	Version            uint                 `json:"version,omitempty"`
	ExpiresAt          time.Time            `json:"expires_at,omitempty"`
	Summary            string               `json:"summary,omitempty"`
	Message            string               `json:"message,omitempty"`
	Diagnostic         *diagnostics.Package `json:"diagnostic,omitempty"`
	RecommendationPlan *PlanSummary         `json:"recommendation_plan,omitempty"`
	// Recommendations 记录诊断包中每条可执行推荐的处理结果（建 plan / 执行读 /
	// 跳过+原因）。让操作员看到每条推荐为何落地或未落地，而不是静默丢弃。
	// RecommendationPlan 兼容保留，指向第一个成功建 plan 的推荐。
	Recommendations []RecommendationStatus `json:"recommendations,omitempty"`
	Trace           *AssistantTrace        `json:"trace,omitempty"`
	// Blocks 是结构化响应块，对齐 SxDevOps AIOps 2.0 的 block 协议。
	// 前端按 Block.Type 分发到对应渲染组件（证据时间线、审批表单、风险提示等）。
	// 当为空时 JSON 中省略此字段（omitempty），向后兼容。
	Blocks []Block `json:"blocks,omitempty"`
	// Process 是一次执行的过程证据（思考文本 / 已执行步骤）。executor 流式路径在
	// 终局只落最终 Response，思考与步骤不在其中；挂上 Process 后 persistTurns 会写进
	// turn 的 response_payload，刷新或换设备回放仍能复原生成时的过程内容。
	Process           *ResponseProcess `json:"process,omitempty"`
	ConfirmationToken string           `json:"-"`
	ConversationID    string           `json:"conversation_id,omitempty"`
	TurnID            string           `json:"turn_id,omitempty"`
}

// ResponseProcess 记录一次执行的过程证据。Thinking 是流式过程中实时推给前端的推理
// 文本（与 SSE thinking 事件一致），Steps 是已执行步骤（与 SSE step 事件一致，
// json 字段对齐前端 AssistantStep）。二者都在流式期间累积、随 turn 落库，保证回放
// "所见即所得"，而不是拿最终答复反推过程。仅前端展示用，不回喂给 LLM。
type ResponseProcess struct {
	Thinking       string                `json:"thinking,omitempty"`
	Steps          []StepEvent           `json:"steps,omitempty"`
	ProgressStages []ProgressStageRecord `json:"progress_stages,omitempty"`
}

// PlanSummary is the operator-facing summary of an action plan derived from a
// diagnostic recommendation. The frontend renders it as an "AI suggests
// executing this operation" card. For write operations it carries a pending
// plan_id awaiting human confirmation; for read operations that were executed
// inline the result already lives in Answer, so this stays nil.
type PlanSummary struct {
	PlanID               string `json:"plan_id"`
	Tool                 string `json:"tool"`
	Risk                 string `json:"risk"`
	RequiresConfirmation bool   `json:"requires_confirmation"`
	ExpiresAt            string `json:"expires_at,omitempty"`
}

// RecommendationStatus 记录诊断包中一条可执行推荐的处理结果。
// Status 取值：
//   - "plan_created": 写工具已建 plan，等待人工确认（PlanID 非空）
//   - "read_executed": 读工具已直接执行，结果在 Answer/事实集中
//   - "skipped": 未能落地，Reason 说明原因（工具未注册 / 策略拒绝 / 建 plan 失败）
//
// 未落地的推荐也要如实上报原因，避免"诊断给了建议却静默消失"。
type RecommendationStatus struct {
	Tool      string `json:"tool"`
	Summary   string `json:"summary,omitempty"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	PlanID    string `json:"plan_id,omitempty"`
	Risk      string `json:"risk,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// AssistantTrace exposes the planner's reasoning (which capabilities were
// considered and why one was picked) and, when applicable, the tool invocation
// that was actually executed. It is the operator-facing complement to Answer.
type AssistantTrace struct {
	Selection      *CapabilitySelection `json:"selection,omitempty"`
	ToolInvocation *ToolInvocation      `json:"tool_invocation,omitempty"`
}

// ToolInvocation records what the assistant invoked against a published
// capability. RawResponse is populated for read operations and stays nil for
// write operations that only produce a pending action plan.
type ToolInvocation struct {
	Tool        string         `json:"tool"`
	Input       map[string]any `json:"input,omitempty"`
	RawResponse map[string]any `json:"raw_response,omitempty"`
}

func (s *Service) HandleMessage(ctx context.Context, user identity.CurrentUser, message, conversationID string, pageContext PageContext, attachments []Attachment) (Response, error) {
	ctx, span := tracer().Start(ctx, "assistant.HandleMessage")
	defer span.End()
	// 一次性路径不使用流式 emitter。清空 progress/toolCall 回调，避免之前
	// 流式请求遗留的 emitter 指向已关闭 channel，导致 send on closed channel panic。
	s.progressEmitter = nil
	s.toolCallEmitter = nil
	// LLM 输入与落库统一用"正文+附件块"全文；标题/列表预览仍只用正文
	//（resolveConversation 内部取标题），附件不会刷屏会话列表。
	fullMessage := ComposeMessageWithAttachments(message, attachments)
	// Inject the user message into the context so the diagnostic runner (which
	// may be an orchestrator) can detect multi-domain requests and split them
	// into concurrent sub-diagnostics. A plain diagnostics.Service ignores it.
	// 用正文（message）而非 fullMessage：域检测只应依据用户实际输入判定意图，
	// 附件文件内容是不分层的非结构化文本，日志里顺带提到别的中间件名会把单域
	// 请求误切成并发多域诊断（附件全文仍以 fullMessage 发给 LLM，不受影响）。
	ctx = orchestrator.WithMessage(ctx, message)
	if s.conversations == nil {
		return s.handleStateless(ctx, user, fullMessage, pageContext)
	}

	conv, err := s.resolveConversation(ctx, user, fullMessage, conversationID)
	if err != nil {
		return Response{}, err
	}
	history, err := s.loadHistory(ctx, conv.ID)
	if err != nil {
		return Response{}, err
	}
	response, err := s.handleStatelessWithHistory(ctx, user, fullMessage, history, pageContext)
	if err != nil {
		// 把失败的工具调用落进时间线（用户消息 + 错误 turn），刷新/换会话后
		// 仍能看到"调用了什么、为何失败"，而不是只有一条无历史的会话。
		s.persistFailedTurn(ctx, conv.ID, fullMessage, err)
		return Response{}, err
	}
	assistantTurnID, err := s.persistTurns(ctx, conv.ID, fullMessage, response)
	if err != nil {
		return Response{}, err
	}
	response.ConversationID = conv.ID
	response.TurnID = assistantTurnID
	return response, nil
}

// HandleMessageStream is the streaming variant of HandleMessage. It calls the
// planner's PlanStream (when supported) and forwards each Delta chunk to the
// returned channel. After the planner emits its terminal intent event,
// HandleMessageStream runs the same policy + execution pipeline as
// HandleMessage and sends the final Response as the terminal StreamEvent.
//
// When the planner does not support PlanStream (e.g. DeterministicPlanner),
// the method degrades to one-shot Plan and emits a single terminal event
// carrying the final Response. Conversation history is loaded and persisted
// the same way as HandleMessage, but persistence happens once at the end
// rather than per-chunk.
func (s *Service) HandleMessageStream(ctx context.Context, user identity.CurrentUser, message, conversationID string, pageContext PageContext, attachments []Attachment) (<-chan StreamEvent, error) {
	ctx, span := tracer().Start(ctx, "assistant.HandleMessageStream")
	defer span.End()
	// 与 HandleMessage 相同：LLM 输入与落库统一用"正文+附件块"全文。
	fullMessage := ComposeMessageWithAttachments(message, attachments)
	// Inject the user message so the orchestrator (when wired as the diagnostic
	// runner) can split multi-domain requests. Mirrors HandleMessage.
	// 域判定只看正文（message），附件文件内容不参与（见 HandleMessage 注释）。
	ctx = orchestrator.WithMessage(ctx, message)

	// Resolve conversation (or run stateless when no store is configured),
	// mirroring HandleMessage. History is loaded once up front so the planner
	// can resolve multi-turn references exactly like the one-shot path.
	var (
		history         []Turn
		conv            store.Conversation
		hasConversation bool
	)
	if s.conversations != nil {
		var err error
		conv, err = s.resolveConversation(ctx, user, fullMessage, conversationID)
		if err != nil {
			return nil, err
		}
		history, err = s.loadHistory(ctx, conv.ID)
		if err != nil {
			return nil, err
		}
		hasConversation = true
	}

	events := make(chan StreamEvent, 16)

	// Wire tool call emitter to forward events to SSE channel. recover 兜底：
	// SSE 客户端断开后 channel 关闭，后续工具调用不能 panic。
	s.WithToolCallEmitter(func(tc ToolCallEvent) {
		defer func() { _ = recover() }()
		events <- StreamEvent{ToolCall: &tc}
	})
	// Wire progress emitter to forward stage transitions to SSE channel.
	// Emitted before planner stream (planning), before each tool invocation
	// (tool_executing, handled inside executeFromIntent), and before the
	// formatter runs (formatting, also inside executeFromIntent).
	s.WithProgressEmitter(func(p ProgressEvent) {
		defer func() { _ = recover() }()
		events <- StreamEvent{Progress: &p}
	})
	// Emit the planning stage now: the planner is about to run.
	s.emitProgress(ProgressPlanning, "")

	go func() {
		defer close(events)
		// 流式结束：清空 emitter，避免下个请求（一次性或新的流式）复用指向
		// 已关闭 channel 的回调，导致 send on closed channel panic。
		defer func() {
			s.progressEmitter = nil
			s.toolCallEmitter = nil
		}()
		// 新路径：AgentExecutor（LLM function calling）
		if s.agentExecutor != nil {
			s.runAgentExecutorInStream(ctx, events, user, fullMessage, history, conv.ID, hasConversation)
			return
		}
		if s.agentEnabled() {
			s.runAgentLoopInStream(ctx, events, user, fullMessage, history, pageContext, conv.ID, hasConversation)
			return
		}
		planEvents := s.startPlannerStream(ctx, user, fullMessage, history, pageContext)
		var (
			resolvedIntent Intent
			planErr        error
		)
		// Forward planner deltas; capture the terminal intent/error.
		for ev := range planEvents {
			if ev.Done {
				if ev.Intent != nil {
					resolvedIntent = *ev.Intent
				}
				if ev.Err != nil {
					planErr = ev.Err
				}
				break
			}
			if ev.Delta != "" {
				events <- StreamEvent{Delta: ev.Delta}
			}
			if ev.Thinking != "" {
				events <- StreamEvent{Thinking: ev.Thinking}
			}
		}
		// Run the execution pipeline (policy + plan + execution), reusing the
		// same logic as HandleMessage via executeFromIntent.
		// 整形器摘要叙述以 delta 流式转发（消除诊断/读路径整形期间的空窗等待），
		// 最终 response 事件权威覆盖前端文本。
		resp, err := s.executeFromIntent(ctx, user, fullMessage, resolvedIntent, planErr, func(delta string) {
			events <- StreamEvent{Delta: delta}
		})
		if err != nil {
			events <- StreamEvent{Err: err, Done: true}
			return
		}
		// Persist turns once at the end when a conversation store is
		// configured, matching HandleMessage's persistence semantics.
		if hasConversation {
			assistantTurnID, perr := s.persistTurns(ctx, conv.ID, fullMessage, resp)
			if perr != nil {
				events <- StreamEvent{Err: perr, Done: true}
				return
			}
			resp.ConversationID = conv.ID
			resp.TurnID = assistantTurnID
		}
		intentCopy := resolvedIntent
		events <- StreamEvent{Intent: &intentCopy, Response: &resp, Done: true}
	}()
	return events, nil
}

// agentLoopSequence resolves the product-declared evidence-collection order for
// the current request, when it matches an enabled runbook that carries a
// tool_sequence (② chain skeleton). It drives the loop's conclusion gate so a
// declared multi-step analysis ("告警根因" → alert.query → event.query) is
// followed as a plan rather than improvised, while still letting the model
// insert extra diagnostic steps between declared ones. Returns nil when no
// runbook sequence applies.
func (s *Service) agentLoopSequence(ctx context.Context, message string) []string {
	if s.runbookRouter == nil {
		return nil
	}
	return s.runbookRouter.SequenceForMessage(ctx, message)
}

// agentRunResponse maps an agent loop's terminal outcome to the operator-facing
// Response. TerminalClarification carries ClarifiedFields as an approval_form
// block so the frontend renders a clickable form instead of plain text;
// every other terminal reason maps 1:1 to the previous inline switch.
func agentRunResponse(run *AgentRun) Response {
	switch run.Reason {
	case TerminalDone:
		// A genuine model final_answer is only TerminalDone without Fallback; the
		// repeated-read convergence backstop also lands on TerminalDone but sets
		// Fallback, so it routes to the distinct fallback marker below.
		if run.Fallback {
			answer := stepsAnswer(run)
			return Response{Type: responseTypeFallbackAnswer, Message: answer, Answer: map[string]any{"message": answer}}
		}
		return Response{Type: "answer", Answer: map[string]any{"message": run.FinalAnswer}, Message: run.FinalAnswer}
	case TerminalClarification:
		resp := BuildClarificationResponse(ClarificationError{Message: run.Clarified, Fields: run.ClarifiedFields})
		if strings.TrimSpace(resp.Message) == "" {
			resp.Message = clarificationMessage
		}
		return resp
	case TerminalHandoff:
		return handoffResponse(run.Handoff)
	default: // TerminalMaxSteps
		answer := stepsAnswer(run)
		return Response{Type: responseTypeFallbackAnswer, Message: answer, Answer: map[string]any{"message": answer}}
	}
}

// runAgentLoopInStream drives the autonomous agent loop inside the streaming
// goroutine (used when the planner is agent-capable). It forwards live LLM
// deltas/thinking via the loop's streaming hook, emits one StepEvent per
// executed advisory step, then routes the terminal outcome (done / clarification
// / write handoff / maxSteps / error) to a final StreamEvent — always exactly
// one terminal event, preserving the streaming contract.
func (s *Service) runAgentLoopInStream(ctx context.Context, events chan<- StreamEvent, user identity.CurrentUser, message string, history []Turn, pageContext PageContext, convID string, hasConversation bool) {
	forward := func(ev StreamEvent) {
		defer func() { _ = recover() }()
		events <- ev
	}
	execute := func(intent Intent, step int) (StepOutcome, error) {
		// Attribute each loop step to its conversation + position so read audits
		// carry agent_step / conversation_turn_id identity.
		stepCtx := execution.WithAgentStep(ctx, execution.AgentStep{StepIndex: step, Conversation: convID})
		return s.executeAgentStep(stepCtx, user, message, intent, step)
	}
	loop := NewAgentLoop(s.planner, execute, agentMaxSteps()).
		WithMaxControlSteps(agentMaxControlSteps()).
		WithStreaming(func(ctx context.Context, user identity.CurrentUser, message string, history []Turn, pageContext PageContext) (<-chan StreamEvent, error) {
			return s.startPlannerStream(ctx, user, message, history, pageContext), nil
		}, forward)
	if seq := s.agentLoopSequence(ctx, message); len(seq) > 0 {
		loop.WithRunbookSequence(seq)
	}

	run := loop.Run(ctx, user, message, history, pageContext)

	// Emit each executed advisory step for the frontend step timeline.
	for _, out := range run.Steps {
		if out.Kind == StepAdvisory {
			forward(stepEventFromOutcome(out))
		}
	}
	if run.Err != nil {
		forward(StreamEvent{Err: run.Err, Done: true})
		return
	}
	var resp Response = agentRunResponse(run)
	// 把本轮执行的过程证据（全部 advisory 步骤 + 阶段骨架）聚合到终端 turn。agent-loop
	// 同时把每条工具步存成独立 tool_step turn 做逐步审计，终端 turn 再承载聚合后的执行
	// 进度流，刷新/回放后进度面板仍能重建完整流（阶段骨架 + 全部工具子项），二者互补。
	aggregatedSteps := aggregateStepEvents(run)
	resp = attachProcess(resp, "", aggregatedSteps, progressStagesForRun(aggregatedSteps, true, s.now()))
	if hasConversation {
		// Persist the full step-level audit trail: each tool step as a chained
		// tool_step turn plus the terminal response, so intermediate results are
		// replayable and reusable across turns.
		assistantTurnID, perr := s.persistAgentRun(ctx, convID, message, run, resp)
		if perr != nil {
			forward(StreamEvent{Err: perr, Done: true})
			return
		}
		resp.ConversationID = convID
		resp.TurnID = assistantTurnID
	}
	intentCopy := Intent{}
	if len(run.Steps) > 0 {
		intentCopy = run.Steps[len(run.Steps)-1].Intent
	}
	forward(StreamEvent{Intent: &intentCopy, Response: &resp, Done: true})
}

// startPlannerStream returns a channel of planner stream events. When the
// planner supports PlanStream (e.g. *EinoPlanner), it delegates directly.
// Otherwise (e.g. DeterministicPlanner), it falls back to one-shot Plan and
// emits a single terminal event carrying the resolved intent or error.
func (s *Service) startPlannerStream(ctx context.Context, user identity.CurrentUser, message string, history []Turn, pageContext PageContext) <-chan StreamEvent {
	if streamer, ok := s.planner.(interface {
		PlanStream(context.Context, identity.CurrentUser, string, []Turn, PageContext) (<-chan StreamEvent, error)
	}); ok {
		events, err := streamer.PlanStream(ctx, user, message, history, pageContext)
		if err == nil {
			return events
		}
		// PlanStream failed to start; fall through to one-shot fallback.
	}
	return s.fallbackPlanStream(ctx, user, message, history, pageContext)
}

// fallbackPlanStream runs the one-shot Plan path in a goroutine and emits a
// single terminal StreamEvent. Used when the planner does not support
// PlanStream or PlanStream failed to start, so the streaming contract (always
// one terminal event) is preserved.
func (s *Service) fallbackPlanStream(ctx context.Context, user identity.CurrentUser, message string, history []Turn, pageContext PageContext) <-chan StreamEvent {
	events := make(chan StreamEvent, 1)
	go func() {
		defer close(events)
		planCtx, planSpan := tracer().Start(ctx, "planner.Plan")
		intent, err := s.planner.Plan(planCtx, user, message, history, pageContext)
		planSpan.End()
		if err != nil {
			events <- StreamEvent{Err: err, Done: true}
			return
		}
		intentCopy := intent
		events <- StreamEvent{Intent: &intentCopy, Done: true}
	}()
	return events
}

// runAgentExecutorInStream 用 AgentExecutor（LLM function calling）流式处理请求。
func (s *Service) runAgentExecutorInStream(ctx context.Context, events chan<- StreamEvent, user identity.CurrentUser, message string, history []Turn, convID string, hasConversation bool) {
	// 安全发送：ctx 取消或 channel 满时不再阻塞
	send := func(ev StreamEvent) bool {
		select {
		case events <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}
	// panic 保护：单个请求崩溃不影响进程
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("[agent] stream panic recovered: %v\n", r)
			send(StreamEvent{Err: fmt.Errorf("internal error: %v", r), Done: true})
		}
	}()

	// 不在此重复发 planning：流式入口（HandleMessageStream）已发过一次
	//（见 handleStream 的 emitProgress(ProgressPlanning)）。重复发会产出两条
	// 同毫秒的 planning 阶段，前端时间线显得机械/像编造。

	// 用 RunWithCallback 逐步推送工具调用事件。
	// 关键：把真实请求者身份注入 ctx。缺了它，CapabilityTool 会走到空的
	// fallback 身份并被策略拒绝（RBAC 归属错误 + 审计不可归因 + 缓存串用户）。
	toolCtx := WithToolUser(ctx, user)
	// 最终答案的流式 delta 与推理 token 的实时 thinking：两条都由 executor 承担
	//（终轮 flush / 逐 chunk 转发），共用 send 推给前端，最终 response 事件权威覆盖。
	// 过程证据累计：thinking 逐 chunk 累积、每条已执行步骤记录在案，随 turn 落库
	//（response_payload.process），刷新后回放仍能看到生成时的思考与工具调用。
	var (
		processThinking strings.Builder
		processSteps    []StepEvent
	)
	emitDelta := func(delta string) { send(StreamEvent{Delta: delta}) }
	emitThinking := func(t string) {
		if t != "" {
			processThinking.WriteString(t)
		}
		send(StreamEvent{Thinking: t})
	}
	onStep := func(step AgentStepEvent) {
		se := StepEvent{Tool: step.Tool, StepIndex: step.Step, Status: step.Status, Summary: step.Summary}
		processSteps = append(processSteps, se)
		send(StreamEvent{Step: &se})
	}
	result := s.supervisor.DispatchStream(toolCtx, message, history, onStep, emitDelta, emitThinking)
	if result.Error != nil {
		send(StreamEvent{Err: result.Error, Done: true})
		return
	}
	// 写交接：executor 在写门上停下，已建 pending plan ——渲染 confirmation_required
	//（前端已支持该 shape：待审批卡片 + 确认 token），而不是把"计划已提交待确认"
	// 混进普通 answer。不写 LLM 缓存（计划是一次性的）。
	if result.Handoff != nil {
		response := attachProcess(confirmationResponseFromOutcome(result.Handoff), processThinking.String(), processSteps, progressStagesForRun(processSteps, false, s.now()))
		if hasConversation {
			assistantTurnID, perr := s.persistTurns(ctx, convID, message, response)
			if perr != nil {
				fmt.Printf("[agent] persist turns failed: %v\n", perr)
			} else {
				response.ConversationID = convID
				response.TurnID = assistantTurnID
			}
		}
		send(StreamEvent{Response: &response, Done: true})
		return
	}
	if result.Answer == "" {
		send(StreamEvent{
			Response: &Response{
				Type:    "answer",
				Message: "已执行工具但未生成回复",
			},
			Done: true,
		})
		return
	}
	response := Response{
		Type:    "answer",
		Message: result.Answer,
		Answer:  map[string]any{"message": result.Answer},
	}
	// 二阶段格式化：仅当实际执行了工具时再整形。无工具调用（知识型直接回答）
	// 的答案已是面向用户的完整文本，跳过二次整形可避免改写与泄露内部术语。
	// 工具路径同样不再走 LLM formatter：agent 终轮已流式写出面向用户的最终答复
	//（由 executor 的 streamRound 终轮 flush delta），这里仅用代码兜底即时生成
	// tool_trace/incident_card block。省去 formatter 一次 ~17s 的 LLM 二次生成
	//（诊断类富卡片如 evidence_timeline 是整形器的增值产物，简单的工具查询不再
	// 为其付出重复生成成本）。result.Answer 保留作 Message。
	formatRan := false
	if s.formatter != nil && len(result.ToolCalls) > 0 {
		factSet := make([]ToolFact, 0, len(result.ToolCalls))
		for _, tc := range result.ToolCalls {
			factSet = append(factSet, ToolFact{Tool: tc.Tool, Result: tc.Output})
		}
		req := FormatRequest{Tool: "agent", Answer: map[string]any{"message": result.Answer}, FactSet: factSet}
		if formatted, ferr := NewCodeFallbackFormatter().Format(ctx, req); ferr == nil {
			if len(formatted.Blocks) > 0 {
				response.Blocks = formatted.Blocks
				formatRan = true
			}
		}
	}
	// 挂上过程证据（思考文本 / 已执行步骤 / 阶段骨架），随 turn 的 response_payload
	// 落库，刷新/回放后仍能看到生成时的思考、工具调用与进度阶段（与流式界面所见一致）。
	response = attachProcess(response, processThinking.String(), processSteps, progressStagesForRun(processSteps, formatRan, s.now()))
	// 持久化 turn（流式路径必须在发送 response 前完成）
	if hasConversation {
		assistantTurnID, perr := s.persistTurns(ctx, convID, message, response)
		if perr != nil {
			fmt.Printf("[agent] persist turns failed: %v\n", perr)
		} else {
			response.ConversationID = convID
			response.TurnID = assistantTurnID
		}
	}
	send(StreamEvent{Response: &response, Done: true})
}

// attachProcess 把流式期间累计的过程证据（思考文本 / 已执行步骤 / 进度阶段骨架）挂到
// Response 上，供 persistTurns 写进 turn 的 response_payload。全部为空时原样返回，
// 保持旧 turn 的 response_payload 结构不变（纯知识回答无过程内容，不产生 process 字段）。
func attachProcess(response Response, thinking string, steps []StepEvent, stages []ProgressStageRecord) Response {
	if thinking == "" && len(steps) == 0 && len(stages) == 0 {
		return response
	}
	response.Process = &ResponseProcess{Thinking: thinking, Steps: steps, ProgressStages: stages}
	return response
}

// progressStagesForRun builds the persisted stage skeleton from execution evidence
// available at attach time: planning always (the plan ran), a tool_executing bucket
// whenever steps executed, and formatting when the second-stage formatter produced
// blocks. Order mirrors the live timeline; the frontend rehydrates it to rebuild the
// phase skeleton after refresh (tool sub-items come from the persisted steps).
func progressStagesForRun(steps []StepEvent, formatRan bool, now time.Time) []ProgressStageRecord {
	stages := []ProgressStageRecord{{Stage: ProgressPlanning, ReceivedAt: now}}
	if len(steps) > 0 {
		stages = append(stages, ProgressStageRecord{Stage: ProgressToolExecuting, ReceivedAt: now})
	}
	if formatRan {
		stages = append(stages, ProgressStageRecord{Stage: ProgressFormatting, ReceivedAt: now})
	}
	return stages
}

// aggregateStepEvents maps an agent-loop result to the persisted StepEvent list the
// frontend step timeline / progress stream rehydrates from. It mirrors stepEventFromOutcome
// (advisory steps only, denied/failed surfaced as status + error) so the aggregated terminal
// process matches what live SSE delivered to the frontend.
func aggregateStepEvents(run *AgentRun) []StepEvent {
	out := make([]StepEvent, 0, len(run.Steps))
	for _, s := range run.Steps {
		if s.Kind != StepAdvisory {
			continue
		}
		status := "done"
		if s.Err != "" {
			status = "error"
		}
		out = append(out, StepEvent{Tool: s.Tool, StepIndex: s.StepIndex, Status: status, Summary: s.Summary, Input: s.Input, Output: s.Output, Error: s.Err})
	}
	return out
}

// handleStateless preserves the pre-multiturn behavior: no history, no
// persistence. Used when no conversation store is configured AND as the inner
// planning pipeline for the multiturn path.
func (s *Service) handleStateless(ctx context.Context, user identity.CurrentUser, message string, pageContext PageContext) (Response, error) {
	return s.handleStatelessWithHistory(ctx, user, message, nil, pageContext)
}

func (s *Service) handleStatelessWithHistory(ctx context.Context, user identity.CurrentUser, message string, history []Turn, pageContext PageContext) (Response, error) {
	// 新路径：基于 LLM function calling 的 Agent 执行器
	if s.agentExecutor != nil {
		return s.handleWithAgentExecutor(ctx, user, message, history)
	}
	// 旧路径：planner + capability matching
	planCtx, planSpan := tracer().Start(ctx, "planner.Plan")
	intent, err := s.planner.Plan(planCtx, user, message, history, pageContext)
	planSpan.End()
	return s.executeFromIntent(ctx, user, message, intent, err, nil)
}

// handleWithAgentExecutor 用 AgentExecutor（LLM function calling）处理请求。
func (s *Service) handleWithAgentExecutor(ctx context.Context, user identity.CurrentUser, message string, history []Turn) (Response, error) {
	// 注入真实请求者身份到 ctx（与 runAgentExecutorInStream 一致），
	// 避免工具执行回退到硬编码 admin 身份。
	toolCtx := WithToolUser(ctx, user)
	result := s.supervisor.Dispatch(toolCtx, message, history, nil)
	if result == nil || result.Error != nil {
		if result == nil {
			// 不可静默吞成空 200：supervisor 返回空（如无执行器）时前端会得到
			// 无消息的空白响应且无任何日志/错误，故障被藏起来。与流式路径
			//（发错误事件）一致地报错。
			return Response{}, fmt.Errorf("agent executor returned no result")
		}
		return Response{}, result.Error
	}
	// 写交接：与流式路径一致渲染 confirmation_required（pending plan 已落库）。
	if result.Handoff != nil {
		return confirmationResponseFromOutcome(result.Handoff), nil
	}
	if result.Answer == "" {
		return Response{
			Type:    "answer",
			Message: "已执行工具但未生成回复",
		}, nil
	}
	response := Response{
		Type:    "answer",
		Message: result.Answer,
		Answer:  map[string]any{"message": result.Answer},
	}
	// 二阶段格式化：把 LLM 回复转为结构化 Summary + Blocks。
	// 无工具调用（知识型直接回答）时跳过——答案已是面向用户的完整文本，
	// 二次整形反而会改写或泄露内部术语。工具路径这里也不再走 LLM formatter，
	// 与流式路径一致：agent 终轮答复保留作 Message，仅用代码兜底即时生成
	// tool_trace/incident_card block（省去 formatter 约 17s 的二次生成）。
	if s.formatter != nil && len(result.ToolCalls) > 0 {
		factSet := make([]ToolFact, 0, len(result.ToolCalls))
		for _, tc := range result.ToolCalls {
			factSet = append(factSet, ToolFact{
				Tool:   tc.Tool,
				Result: tc.Output,
			})
		}
		req := FormatRequest{
			Tool:    "agent",
			Answer:  map[string]any{"message": result.Answer},
			FactSet: factSet,
		}
		if formatted, err := NewCodeFallbackFormatter().Format(ctx, req); err == nil {
			if len(formatted.Blocks) > 0 {
				response.Blocks = formatted.Blocks
			}
		}
	}
	return response, nil
}

// executeFromIntent runs the policy + plan + execution pipeline for a
// resolved intent (or a clarification/plan error from the planner). It is the
// execution half of handleStatelessWithHistory, extracted so the streaming
// path (HandleMessageStream) can reuse it after PlanStream produces an intent
// without duplicating the clarification/diagnostic/tool logic.
//
// When planErr is non-nil it is interpreted exactly as in
// handleStatelessWithHistory: a ClarificationError yields a
// clarification_needed Response (no error), ErrClarificationNeeded yields the
// default clarification Response, and any other error is returned as-is.
// When planErr is nil, intent must hold the resolved planner intent and the
// full diagnostic/tool/policy/read/plan pipeline runs.
//
// formatDelta 非 nil 时（流式调用方），二阶段整形走 FormatStream：整形器产出的
// 摘要叙述逐段以 delta 转发给前端增量渲染，消除诊断/读路径整形期间的空窗等待；
// nil 时保持原来的非流式 Format（一次性调用方行为不变）。
func (s *Service) executeFromIntent(ctx context.Context, user identity.CurrentUser, message string, intent Intent, planErr error, formatDelta func(string)) (Response, error) {
	if planErr != nil {
		var clarification ClarificationError
		if errors.As(planErr, &clarification) {
			response := BuildClarificationResponse(clarification)
			if strings.TrimSpace(response.Message) == "" {
				response.Message = clarificationMessage
			}
			return response, nil
		}
		if errors.Is(planErr, ErrClarificationNeeded) {
			return Response{Type: "clarification_needed", Message: clarificationMessage}, nil
		}
		return Response{}, planErr
	}
	// generative intent: return a draft without executing the tool or creating
	// a pending plan. The draft carries the resolved tool + input so the
	// operator can review and modify before converting to an executive action.
	// (借鉴-2: 生成类问题先出草稿，用户可改可弃)
	if ClassifyIntent(intent) == IntentGenerative {
		s.emitProgress(ProgressFormatting, "")
		response := Response{
			Type:    "draft",
			Tool:    intent.ToolName,
			Answer:  intent.Input,
			Summary: summarizePlan(intent.ToolName, intent.Input),
			Trace:   buildAssistantTrace(intent, intent.ToolName, intent.Input, nil),
		}
		// Auto-preview the operation with a simplified dry-run (omits commands)
		// so the operator sees the expected impact while reviewing the draft.
		// Best-effort: failures are silently ignored.
		if s.dryRun != nil {
			if block, ok := s.previewDraftPlan(ctx, intent.ToolName, intent.Input); ok {
				response.Blocks = append(response.Blocks, block)
			}
		}
		return response, nil
	}
	if intent.Diagnostic != nil {
		if s.diagnostics == nil {
			return Response{}, errors.New("diagnostic service is required")
		}
		toolName := resolveDiagnosticToolName(s.diagnostics, *intent.Diagnostic)
		diagRequest := *intent.Diagnostic
		// 参数卫生防线：对用户原始输入做长度/必填校验。
		if verr := diagnostics.ValidateRequestFields(diagRequest); verr != nil {
			return Response{}, verr
		}
		// Emit progress: tool_executing stage with the resolved tool name.
		s.emitProgress(ProgressToolExecuting, toolName)
		// Emit tool call start event for real-time SSE display
		if s.toolCallEmitter != nil {
			s.toolCallEmitter(ToolCallEvent{
				Tool:  toolName,
				Input: map[string]any{"domain": diagRequest.Domain},
				Done:  false,
			})
		}
		diagCtx, diagSpan := tracer().Start(ctx, "diagnostics.Run")
		pkg, err := s.diagnostics.Run(diagCtx, user, diagRequest)
		diagSpan.End()
		if err != nil {
			if errors.Is(err, execution.ErrReadToolDenied) || errors.Is(err, execution.ErrWriteTool) {
				return Response{}, fmt.Errorf("%w: %w", ErrPolicyDenied, err)
			}
			return Response{}, err
		}
		// Emit tool call done event
		if s.toolCallEmitter != nil {
			s.toolCallEmitter(ToolCallEvent{
				Tool:  toolName,
				Input: map[string]any{"domain": diagRequest.Domain},
				Done:  true,
			})
		}
		// 域未接入能力时 diagnostics 返回通用检查框架包（framework=true），
		// Answer 与摘要都如实标注为框架而非"诊断完成"。
		framework := false
		if len(pkg.Observations) > 0 {
			framework, _ = pkg.Observations[0].Data["framework"].(bool)
		}
		diagSummary := fmt.Sprintf("诊断完成：%d 个观察，%d 个发现，%d 个建议", len(pkg.Observations), len(pkg.Findings), len(pkg.Recommendations))
		if framework && len(pkg.Observations) > 0 {
			checkpoints, _ := pkg.Observations[0].Data["checkpoints"].([]string)
			diagSummary = fmt.Sprintf("%s 域未接入精确诊断能力，返回通用检查框架（%d 个检查点，非实测数据）", pkg.Domains, len(checkpoints))
		}
		response := Response{
			Type:       "answer",
			Tool:       toolName,
			Answer:     map[string]any{"message": diagSummary},
			Diagnostic: &pkg,
		}
		// 诊断→修复链路: 把诊断包中所有可执行 recommendation 转成 action plan
		// 或执行读操作。复用现有的写操作 plan 创建链路 (tools.Lookup →
		// policy.Evaluate → plans.CreatePlan) 或直接执行读操作 (reads.ExecuteRead)。
		// 该步骤为 best-effort: 工具未注册、策略拒绝、plan 创建或读执行失败时
		// 不向上抛错，但每条推荐的处理结果如实写入 response.Recommendations
		// （含未落地原因），避免"诊断给了建议却静默消失"。
		var factSet []ToolFact
		for _, rec := range actionableRecommendations(pkg) {
			if fact, executed, status := s.enrichRecommendation(ctx, user, rec, &response); executed {
				factSet = append(factSet, fact)
			} else if status != nil {
				response.Recommendations = append(response.Recommendations, *status)
			}
		}
		// 诊断包事实: 把诊断包的关键摘要作为一个 ToolFact 纳入 FactSet。
		// Result.summary 让 CodeFallbackFormatter 的 extractFactSummary 能提取,
		// 使 Summary 聚合时体现诊断结论而非只显示推荐读结果。
		diagFact := ToolFact{
			Tool:  toolName,
			Input: map[string]any{"domain": diagRequest.Domain},
			Result: map[string]any{
				"summary":         diagSummary,
				"domains":         pkg.Domains,
				"recommendations": len(pkg.Recommendations),
			},
		}
		factSet = append(factSet, diagFact)
		response = s.formatResponse(ctx, response, intent, formatDelta, factSet...)
		return response, nil
	}
	tool, ok := tools.Lookup(intent.ToolName)
	if !ok {
		s.recordDenied(ctx, user, intent.ToolName, policy.ToolNotRegistered)
		return Response{}, fmt.Errorf("%w: %s", ErrPolicyDenied, policy.ToolNotRegistered)
	}
	// 读路径的 input 直接透传（不再做默认环境规约），由策略层校验。
	input := intent.Input
	decision := policy.Evaluate(user, tool, input)
	if !decision.Allowed {
		s.recordDenied(ctx, user, tool.Name, decision.Reason)
		return Response{}, fmt.Errorf("%w: %s", ErrPolicyDenied, decision.Reason)
	}
	if tool.Operation == tools.Read {
		if s.reads == nil {
			return Response{}, errors.New("read service is required")
		}
		// Emit progress: tool_executing stage with the tool name.
		s.emitProgress(ProgressToolExecuting, tool.Name)
		// Emit tool call start event for real-time SSE display
		if s.toolCallEmitter != nil {
			s.toolCallEmitter(ToolCallEvent{
				Tool:  tool.Name,
				Input: input,
				Done:  false,
			})
		}
		readCtx, readSpan := tracer().Start(ctx, "execute_read",
			trace.WithAttributes(attribute.String("tool.name", tool.Name)))
		answer, err := s.reads.ExecuteRead(readCtx, user, tool.Name, input)
		readSpan.End()
		if err != nil {
			return Response{}, err
		}
		// Emit tool call done event
		if s.toolCallEmitter != nil {
			s.toolCallEmitter(ToolCallEvent{
				Tool:        tool.Name,
				Input:       input,
				RawResponse: answer,
				Done:        true,
			})
		}
		return s.formatResponse(ctx, Response{
			Type:   "answer",
			Tool:   tool.Name,
			Answer: answer,
			Trace:  buildAssistantTrace(intent, tool.Name, input, answer),
		}, intent, formatDelta), nil
	}
	if s.plans == nil {
		return Response{}, errors.New("plan service is required")
	}
	// Emit progress: tool_executing stage for write-plan creation.
	s.emitProgress(ProgressToolExecuting, tool.Name)

	// 借鉴-5: Runbook 匹配。低风险 Runbook 创建已确认 plan 并自动执行，
	// 跳过人工确认；medium/high/无命中走原 confirmation_required 路径。
	runbookSlug, runbookRisk := "", ""
	if s.runbookRouter != nil {
		if rb, ok := s.runbookRouter.Match(ctx, message, tool.Name); ok {
			runbookSlug, runbookRisk = rb.Slug, rb.RiskLevel
		}
	}

	planCtx, createPlanSpan := tracer().Start(ctx, "create_plan",
		trace.WithAttributes(attribute.String("tool.name", tool.Name)))

	if runbookSlug != "" && runbookRisk == "low" && s.execution != nil && s.admitAutoExec(ctx, user, tool, decision, autonomy.SourceDirect, boolPtr(runbookRisk == "low")) {
		// 低风险 Runbook 自动执行：创建已确认 plan → 立即执行 → 返回 execution_result。
		// E2: 仅当 Low-Risk Admission Controller 放行才自动执行；否则回落
		// confirmation_required（人工确认），默认 fail-closed。
		plan, err := s.plans.CreateRunbookPlan(planCtx, user, decision, intent.Input, runbookSlug, "low")
		createPlanSpan.End()
		if err != nil {
			return Response{}, err
		}
		executionResult, execErr := s.execution.ExecuteConfirmedStoredPlan(planCtx, plan.ID)
		if execErr != nil {
			return Response{}, execErr
		}
		s.recordAutoExec(planCtx, user) // 自动执行后记一次每日上限计数（不阻塞）
		response := Response{
			Type:   "execution_result",
			Tool:   tool.Name,
			PlanID: plan.ID,
			Status: executionResult.Status,
			Answer: map[string]any{
				"execution_id": executionResult.ID,
				"runbook":      runbookSlug,
				"reused":       executionResult.Reused,
			},
			Trace: buildAssistantTrace(intent, tool.Name, intent.Input, nil),
		}
		if s.dryRun != nil {
			if block, _, ok := s.previewWritePlan(ctx, tool.Name, intent.Input); ok {
				response.Blocks = append(response.Blocks, block)
			}
		}
		return response, nil
	}

	plan, err := s.plans.CreatePlan(planCtx, user, decision, intent.Input)
	createPlanSpan.End()
	if err != nil {
		return Response{}, err
	}
	response := Response{
		Type:              "confirmation_required",
		Tool:              tool.Name,
		PlanID:            plan.ID,
		Status:            string(plan.Status),
		Version:           plan.Version,
		ExpiresAt:         plan.ExpiresAt,
		Summary:           summarizePlan(tool.Name, intent.Input),
		ConfirmationToken: plan.ConfirmationToken,
		Trace:             buildAssistantTrace(intent, tool.Name, intent.Input, nil),
	}
	// Auto-preview the write operation when a dry-run runner is wired. The
	// preview is best-effort: failures (unsupported tool, no handler) are
	// silently ignored so the plan is still returned for confirmation.
	// 结果准 #5: 预览结果持久化到 plan（action_plans.dry_run），供复盘查看
	// 确认时的完整执行计划。
	if s.dryRun != nil {
		if block, result, ok := s.previewWritePlan(ctx, tool.Name, intent.Input); ok {
			response.Blocks = append(response.Blocks, block)
			if encoded, err := json.Marshal(result); err == nil {
				_ = s.plans.AttachDryRun(ctx, plan.ID, encoded)
			}
		}
	}
	return response, nil
}

// previewWritePlan runs the dry-run preview for a write tool and renders the
// result as a risk_notice block. It returns (block, result, true) when a preview
// is available; (zero, zero, false) when dry-run is not supported or fails, so
// the caller can skip attaching a block without propagating the error.
func (s *Service) previewWritePlan(ctx context.Context, toolName string, input map[string]any) (Block, execution.DryRunResult, bool) {
	result, err := s.dryRun.DryRun(ctx, toolName, input)
	if err != nil {
		return Block{}, execution.DryRunResult{}, false
	}
	block := NewBlock(BlockRiskNotice).
		WithTitle("操作预演 (Dry-Run)").
		WithContent(result.Summary)
	payload := map[string]any{}
	if len(result.AffectedResources) > 0 {
		payload["affected_resources"] = result.AffectedResources
	}
	if len(result.Commands) > 0 {
		payload["commands"] = result.Commands
	}
	if len(result.Warnings) > 0 {
		payload["warnings"] = result.Warnings
	}
	if result.SuggestedStrategy != nil {
		payload["suggested_strategy"] = result.SuggestedStrategy
	}
	if len(payload) > 0 {
		block = block.WithPayload(payload)
	}
	return block, result, true
}

// previewDraftPlan runs a simplified dry-run preview for a generative draft.
// Unlike previewWritePlan (executive path), the draft preview omits commands
// — execution detail is not yet decided in the generative stage. The operator
// sees the expected impact (summary + affected_resources + warnings) while
// still in the "review and modify" stage. Returns (block, true) when a preview
// is available; (zero, false) when dry-run is not supported or fails, so the
// draft is still returned without a block.
func (s *Service) previewDraftPlan(ctx context.Context, toolName string, input map[string]any) (Block, bool) {
	result, err := s.dryRun.DryRun(ctx, toolName, input)
	if err != nil {
		return Block{}, false
	}
	block := NewBlock(BlockRiskNotice).
		WithTitle("预期影响").
		WithContent(result.Summary)
	payload := map[string]any{}
	if len(result.AffectedResources) > 0 {
		payload["affected_resources"] = result.AffectedResources
	}
	if len(result.Warnings) > 0 {
		payload["warnings"] = result.Warnings
	}
	if len(payload) > 0 {
		block = block.WithPayload(payload)
	}
	return block, true
}

// formatResponse 在一阶段取证后调用 formatter 进行二阶段整形。
// 当 formatter 为 nil 时直接返回原 response（向后兼容）。
// 整形失败时也返回原 response，不阻塞主链路（二阶段是 best-effort 增强）。
//
// factSet 是多工具场景下的事实集（缺口-4）。为空时走单工具路径
// （response.Tool + response.Answer）；非空时传给 formatter 遍历生成
// 完整草稿。诊断分支收集诊断包工具 + 推荐执行工具的事实。
//
// formatDelta 非 nil 且 formatter 支持流式（StreamingResponseFormatter）时走
// FormatStream：整形器产出的摘要叙述实时转发给前端，消除整形期空窗；流式失败
// 或不支持流式回退非流式 Format，行为与旧路径一致。
func (s *Service) formatResponse(ctx context.Context, response Response, intent Intent, formatDelta func(string), factSet ...ToolFact) Response {
	if s.formatter == nil {
		return response
	}
	// Emit progress: formatting stage. Fires only when a formatter is wired,
	// so non-streaming callers (no emitter) and services without a formatter
	// see no formatting event.
	s.emitProgress(ProgressFormatting, "")
	req := FormatRequest{
		Tool:    response.Tool,
		Answer:  response.Answer,
		FactSet: factSet,
	}
	var result FormatResult
	var err error
	if formatDelta != nil {
		if streamer, ok := s.formatter.(StreamingResponseFormatter); ok {
			result, err = streamer.FormatStream(ctx, req, formatDelta)
		} else {
			result, err = s.formatter.Format(ctx, req)
		}
	} else {
		result, err = s.formatter.Format(ctx, req)
	}
	if err != nil {
		return response
	}
	if strings.TrimSpace(result.Summary) != "" {
		response.Summary = result.Summary
	}
	if len(result.Blocks) > 0 {
		response.Blocks = result.Blocks
	}
	return response
}

// actionableRecommendations 返回诊断包中所有可执行（ToolName 非空）的推荐。
func actionableRecommendations(pkg diagnostics.Package) []diagnostics.Recommendation {
	var out []diagnostics.Recommendation
	for _, rec := range pkg.Recommendations {
		if strings.TrimSpace(rec.ToolName) != "" {
			out = append(out, rec)
		}
	}
	return out
}

// enrichRecommendation turns a single actionable recommendation into either an
// executed read result (factSet) or a pending action plan, and reports its
// status (plan_created / read_executed / skipped+reason). It reuses the existing
// write-plan chain (tools.Lookup → policy.Evaluate → plans.CreatePlan) and the
// read execution path (reads.ExecuteRead).
//
// The method is best-effort: when the recommended tool is not registered, the
// policy denies the operation, or plan creation / read execution fails, it does
// not surface an error — the recommendation is marked skipped with the reason so
// the operator sees why it did not land.
//
// 返回值: (fact, executed, status)。
//   - executed=true 且 fact 非零: 推荐读工具已直接执行, fact 纳入 FactSet
//   - executed=false 且 status 非 nil: 该推荐的处理状态（plan_created 或
//     skipped+reason）, 由调用方写入 response.Recommendations
func (s *Service) enrichRecommendation(ctx context.Context, user identity.CurrentUser, rec diagnostics.Recommendation, response *Response) (ToolFact, bool, *RecommendationStatus) {
	toolName := strings.TrimSpace(rec.ToolName)
	status := &RecommendationStatus{Tool: toolName, Summary: rec.Summary}
	if toolName == "" {
		return ToolFact{}, false, nil
	}
	// ToPlanInput() 等价: 从 recommendation 取 toolName + candidate input.
	tool, ok := tools.Lookup(toolName)
	if !ok {
		status.Status = "skipped"
		status.Reason = "工具未注册，无法执行"
		return ToolFact{}, false, status
	}
	input := rec.CandidateInput
	decision := policy.Evaluate(user, tool, input)
	if !decision.Allowed {
		status.Status = "skipped"
		status.Reason = "策略拒绝：" + string(decision.Reason)
		return ToolFact{}, false, status
	}
	if tool.Operation == tools.Read {
		// risk=low: 直接执行读操作, 结果写入 Answer, 操作员可立即看到结果.
		if s.reads == nil {
			status.Status = "skipped"
			status.Reason = "读执行服务未配置"
			return ToolFact{}, false, status
		}
		readCtx, readSpan := tracer().Start(ctx, "execute_recommendation_read",
			trace.WithAttributes(attribute.String("tool.name", tool.Name)))
		answer, err := s.reads.ExecuteRead(readCtx, user, tool.Name, input)
		readSpan.End()
		if err != nil {
			status.Status = "skipped"
			status.Reason = "读执行失败：" + err.Error()
			return ToolFact{}, false, status
		}
		response.Answer = answer
		status.Status = "read_executed"
		return ToolFact{Tool: tool.Name, Input: input, Result: answer}, true, status
	}
	// risk>=medium: 创建 pending plan, 等待人工确认. 复用现有 plan 创建链路.
	if s.plans == nil {
		status.Status = "skipped"
		status.Reason = "plan 服务未配置"
		return ToolFact{}, false, status
	}
	planCtx, planSpan := tracer().Start(ctx, "create_recommendation_plan",
		trace.WithAttributes(attribute.String("tool.name", tool.Name)))
	plan, err := s.plans.CreatePlan(planCtx, user, decision, input)
	planSpan.End()
	if err != nil {
		status.Status = "skipped"
		status.Reason = "创建 plan 失败：" + err.Error()
		return ToolFact{}, false, status
	}
	status.Status = "plan_created"
	status.PlanID = plan.ID
	status.Risk = string(tool.Risk)
	status.ExpiresAt = plan.ExpiresAt.UTC().Format(time.RFC3339)
	// 兼容：第一个成功建 plan 的推荐仍写入 RecommendationPlan（前端旧契约）。
	if response.RecommendationPlan == nil {
		response.RecommendationPlan = &PlanSummary{
			PlanID:               plan.ID,
			Tool:                 tool.Name,
			Risk:                 string(tool.Risk),
			RequiresConfirmation: decision.RequiresConfirmation,
			ExpiresAt:            plan.ExpiresAt.UTC().Format(time.RFC3339),
		}
	}
	// 写工具创建 plan, 尚未执行, 无结果事实。
	return ToolFact{}, false, status
}

// resolveConversation either fetches an existing conversation (verifying it
// belongs to the current user) or creates a new one when conversationID is
// empty. The title is derived from the first user message, truncated to
// conversationTitleMax runes.
func (s *Service) resolveConversation(ctx context.Context, user identity.CurrentUser, message, conversationID string) (store.Conversation, error) {
	if conversationID != "" {
		conv, err := s.conversations.GetConversation(ctx, conversationID, user.Subject)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return store.Conversation{}, ErrForeignConversation
			}
			return store.Conversation{}, err
		}
		return conv, nil
	}
	now := s.now()
	preview := truncateRunes(message, conversationPreviewMax)
	title := truncateRunes(strings.TrimSpace(message), conversationTitleMax)
	if title == "" {
		title = "新会话"
	}
	return s.conversations.CreateConversation(ctx, user.Subject, title, preview, now)
}

// loadHistory returns the conversation history for the planner. When a
// compactor is configured, it applies rolling summarization: once the
// unsummarized turn count reaches compactThreshold, the oldest batch is
// compacted into a system_summary turn and persisted. The planner then
// receives [summary, ...keepRecentTurns verbatim] in chronological order.
//
// When no compactor is configured, the legacy behavior is preserved: fetch
// historyTurnLimit turns newest-first, reverse to chronological order.
func (s *Service) loadHistory(ctx context.Context, conversationID string) ([]Turn, error) {
	// Fetch existing summary (if compactor is configured).
	var existingSummary store.Turn
	hasSummary := false
	if s.compactor != nil {
		if sum, err := s.conversations.GetSummary(ctx, conversationID); err == nil {
			existingSummary = sum
			hasSummary = true
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}

	// Fetch newest turns. When compactor is configured we fetch
	// compactThreshold + keepRecentTurns turns so the window can contain
	// the summary boundary turn plus enough unsummarized turns to detect
	// when compaction is needed even when persisted turns and the summary
	// itself occupy slots. Without a compactor, use the legacy
	// historyTurnLimit.
	fetchLimit := historyTurnLimit
	if s.compactor != nil {
		fetchLimit = compactThreshold + keepRecentTurns
	}
	page, err := s.conversations.ListTurns(ctx, conversationID, fetchLimit, "")
	if err != nil {
		return nil, err
	}

	// Filter: drop summary turns (tracked separately) and turns already
	// covered by the existing summary (identified by parent_turn_id boundary).
	// page.Turns is newest-first; we break at the boundary so older turns
	// are excluded.
	var unsummarized []store.Turn
	for _, t := range page.Turns {
		if t.Role == store.ConversationRoleSystemSummary {
			continue
		}
		if hasSummary && t.ID == existingSummary.ParentTurnID {
			break
		}
		unsummarized = append(unsummarized, t)
	}

	// Compaction: when unsummarized count reaches compactThreshold, split
	// into [recent (newest keepRecentTurns)] and [excluded (rest)]. Compact
	// only the oldest compactBatchSize of the excluded turns; the remainder
	// of excluded is covered by the summary boundary so they are not
	// re-counted next time.
	if s.compactor != nil && len(unsummarized) >= compactThreshold {
		recent := unsummarized[:keepRecentTurns]
		excluded := unsummarized[keepRecentTurns:]

		// Compact the oldest compactBatchSize of the excluded turns.
		compactCount := compactBatchSize
		if len(excluded) < compactBatchSize {
			compactCount = len(excluded)
		}
		toCompact := excluded[len(excluded)-compactCount:]

		// Build compactor input in chronological order (oldest first):
		// [existing summary, ...toCompact reversed].
		var compactInput []Turn
		if hasSummary {
			compactInput = append(compactInput, toPlannerTurn(existingSummary))
		}
		for i := len(toCompact) - 1; i >= 0; i-- {
			compactInput = append(compactInput, toPlannerTurn(toCompact[i]))
		}

		newSummary, err := s.compactor.Compact(ctx, compactInput)
		if err != nil {
			return nil, err
		}
		// ParentTurnID = newest excluded turn (excluded[0] in newest-first
		// order). This is the boundary: the next loadHistory breaks at this
		// ID and only counts turns newer than the boundary as unsummarized.
		boundaryTurn := excluded[0]
		savedSummary, err := s.conversations.ReplaceSummary(ctx, conversationID, newSummary, boundaryTurn.ID, s.now())
		if err != nil {
			return nil, err
		}

		// Return [new summary, ...recent turns] in chronological order.
		history := []Turn{toPlannerTurn(savedSummary)}
		for i := len(recent) - 1; i >= 0; i-- {
			history = append(history, toPlannerTurn(recent[i]))
		}
		return history, nil
	}

	// No compaction needed. When a summary exists, cap unsummarized at
	// keepRecentTurns (newest-first slice, keep the head) so the planner
	// sees [summary, ...keepRecentTurns]. Without a summary, return all
	// fetched turns (already bounded by fetchLimit) to preserve the
	// pre-compaction behavior.
	recent := unsummarized
	if hasSummary && len(recent) > keepRecentTurns {
		recent = recent[:keepRecentTurns]
	}
	history := make([]Turn, 0, len(recent)+1)
	if hasSummary {
		history = append(history, toPlannerTurn(existingSummary))
	}
	for i := len(recent) - 1; i >= 0; i-- {
		history = append(history, toPlannerTurn(recent[i]))
	}
	return history, nil
}

// persistTurns records the user message and assistant response as two turns.
// Returns the assistant turn ID so the caller can populate Response.TurnID.
// The assistant turn's CreatedAt is nudged by 1ms after the user turn so the
// strict-created-at ordering stays stable in the store.
func (s *Service) persistTurns(ctx context.Context, conversationID, userMessage string, response Response) (string, error) {
	now := s.now()
	if _, err := s.conversations.AppendTurn(ctx, store.Turn{
		ConversationID: conversationID,
		Role:           store.ConversationRoleUser,
		Content:        userMessage,
		CreatedAt:      now,
	}); err != nil {
		return "", err
	}
	assistantTurn, err := s.conversations.AppendTurn(ctx, store.Turn{
		ConversationID:  conversationID,
		Role:            store.ConversationRoleAssistant,
		Content:         assistantTurnContent(response),
		ResponseType:    response.Type,
		ResponsePayload: responsePayload(response),
		CreatedAt:       now.Add(1 * time.Millisecond),
	})
	if err != nil {
		return "", err
	}
	return assistantTurn.ID, nil
}

// persistFailedTurn 把一次失败的工具调用落进会话时间线（用户消息 + 错误助手 turn），
// 让"调用了什么、为何失败"在回放/刷新后仍可见。客户端校验（ErrInvalidRequest）与
// 跨会话归属（ErrForeignConversation）不属于工具调用失败，跳过不记。
func (s *Service) persistFailedTurn(ctx context.Context, conversationID, userMessage string, err error) {
	if s.conversations == nil || !shouldPersistFailure(err) {
		return
	}
	failed := Response{Type: "error", Message: err.Error(), Summary: err.Error()}
	_, perr := s.persistTurns(ctx, conversationID, userMessage, failed)
	if perr != nil {
		return
	}
}

func shouldPersistFailure(err error) bool {
	return !errors.Is(err, diagnostics.ErrInvalidRequest) &&
		!errors.Is(err, ErrForeignConversation) &&
		!errors.Is(err, ErrClarificationNeeded)
}

func (s *Service) now() time.Time {
	if s.clock != nil {
		return s.clock().UTC()
	}
	return time.Now().UTC()
}

// toPlannerTurn converts a stored turn into the planner's Turn type. Assistant
// turns carry their Intent inside ResponsePayload so the planner can merge
// prior clarification selections when the user only supplies missing fields.
func toPlannerTurn(t store.Turn) Turn {
	turn := Turn{
		Role:         t.Role,
		Content:      t.Content,
		ResponseType: t.ResponseType,
	}
	if t.ResponsePayload != nil {
		turn.Result = t.ResponsePayload
	}
	return turn
}

// assistantTurnContent produces a human-readable summary for the persisted
// assistant turn. It prefers the structured fields (Summary, Message) and falls
// back to the Answer payload.
func assistantTurnContent(response Response) string {
	if response.Summary != "" {
		return response.Summary
	}
	if response.Message != "" {
		return response.Message
	}
	if len(response.Answer) > 0 {
		for _, key := range []string{"summary", "message", "status", "text", "result"} {
			if value, ok := response.Answer[key].(string); ok && value != "" {
				return value
			}
		}
		if payload, err := json.Marshal(response.Answer); err == nil {
			return string(payload)
		}
	}
	if response.Status != "" {
		return response.Status
	}
	if response.Type == "answer" {
		return "未返回具体内容"
	}
	return response.Type
}

// responsePayload serializes the Response into the turn's response_payload
// column. The internal ConfirmationToken is intentionally excluded. Returns
// nil when the response has no type (e.g. zero value), which the store treats
// as a NULL payload.
func responsePayload(response Response) map[string]any {
	if response.Type == "" && response.Tool == "" && response.PlanID == "" && response.Message == "" && response.Summary == "" && response.Answer == nil && response.Diagnostic == nil && response.RecommendationPlan == nil && response.Trace == nil && len(response.Blocks) == 0 {
		return nil
	}
	payload := map[string]any{
		"type": response.Type,
	}
	if response.Tool != "" {
		payload["tool"] = response.Tool
	}
	if response.Answer != nil {
		payload["answer"] = response.Answer
	}
	if response.PlanID != "" {
		payload["plan_id"] = response.PlanID
	}
	if response.Status != "" {
		payload["status"] = response.Status
	}
	if response.Version != 0 {
		payload["version"] = response.Version
	}
	if !response.ExpiresAt.IsZero() {
		payload["expires_at"] = response.ExpiresAt
	}
	if response.Summary != "" {
		payload["summary"] = response.Summary
	}
	if response.Message != "" {
		payload["message"] = response.Message
	}
	if response.Diagnostic != nil {
		payload["diagnostic"] = response.Diagnostic
	}
	if response.RecommendationPlan != nil {
		payload["recommendation_plan"] = response.RecommendationPlan
	}
	if response.Trace != nil {
		payload["trace"] = response.Trace
	}
	if len(response.Blocks) > 0 {
		payload["blocks"] = response.Blocks
	}
	if response.Process != nil {
		payload["process"] = response.Process
	}
	return payload
}

// truncateRunes limits s to at most max runes, appending an ellipsis when
// truncation occurs. Non-positive max returns the empty string.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "…"
}

// buildAssistantTrace assembles the operator-facing trace from the planner
// intent and the actual tool invocation. rawResponse may be nil for write
// operations that did not yet execute against the backend.
func buildAssistantTrace(intent Intent, toolName string, input map[string]any, rawResponse map[string]any) *AssistantTrace {
	trace := &AssistantTrace{
		ToolInvocation: &ToolInvocation{Tool: toolName, Input: input, RawResponse: rawResponse},
	}
	if intent.Selection != nil {
		trace.Selection = intent.Selection
	}
	return trace
}

func summarizePlan(toolName string, input map[string]any) string {
	return fmt.Sprintf("Create action plan for %s.", toolName)
}

// ListConversations returns the conversation page scoped to the current user.
// The router layer enforces user.Subject; the store re-checks it for defense
// in depth.
func (s *Service) ListConversations(ctx context.Context, user identity.CurrentUser, filter store.ConversationFilter) (store.ConversationPage, error) {
	if s.conversations == nil {
		return store.ConversationPage{}, errors.New("conversation store is not configured")
	}
	filter.Subject = user.Subject
	return s.conversations.ListConversations(ctx, filter)
}

// GetConversation fetches a single conversation, enforcing subject isolation.
// Returns store.ErrNotFound when the conversation does not exist or belongs to
// another subject; callers should map that to 404 to avoid leaking existence.
func (s *Service) GetConversation(ctx context.Context, id, subject string) (store.Conversation, error) {
	if s.conversations == nil {
		return store.Conversation{}, errors.New("conversation store is not configured")
	}
	return s.conversations.GetConversation(ctx, id, subject)
}

// ListTurns returns turns for a conversation ordered newest-first. Callers
// must have already authorized the conversation via GetConversation; the
// store does not re-check subject here.
func (s *Service) ListTurns(ctx context.Context, conversationID string, limit int, beforeTurnID string) (store.TurnPage, error) {
	if s.conversations == nil {
		return store.TurnPage{}, errors.New("conversation store is not configured")
	}
	return s.conversations.ListTurns(ctx, conversationID, limit, beforeTurnID)
}

// ArchiveConversation soft-deletes a conversation by setting archived_at.
// Returns store.ErrNotFound when the conversation is missing or foreign.
func (s *Service) ArchiveConversation(ctx context.Context, id, subject string) error {
	if s.conversations == nil {
		return errors.New("conversation store is not configured")
	}
	return s.conversations.ArchiveConversation(ctx, id, subject, s.now())
}

// DeleteConversation permanently removes a conversation and all its turns
// (including archived ones). Returns store.ErrNotFound when the conversation
// is missing or belongs to another subject. This is destructive and cannot
// be undone; the HTTP layer is the only sanctioned caller and must gate it
// behind an explicit user intent (confirm dialog / method=DELETE).
func (s *Service) DeleteConversation(ctx context.Context, id, subject string) error {
	if s.conversations == nil {
		return errors.New("conversation store is not configured")
	}
	return s.conversations.DeleteConversation(ctx, id, subject)
}

// RenameConversation updates a conversation's display title after trimming.
// Returns store.ErrInvalidTitle for empty titles and store.ErrNotFound for
// missing or foreign conversations.
func (s *Service) RenameConversation(ctx context.Context, id, subject, title string) error {
	if s.conversations == nil {
		return errors.New("conversation store is not configured")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return store.ErrInvalidTitle
	}
	const maxTitleLength = 120
	if len([]rune(title)) > maxTitleLength {
		runes := []rune(title)
		title = string(runes[:maxTitleLength])
	}
	return s.conversations.RenameConversation(ctx, id, subject, title)
}
