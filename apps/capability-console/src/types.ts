export type CapabilityOperation = 'read' | 'write';
export type CapabilityRisk = 'low' | 'medium' | 'high';
export type CapabilityStatus = 'discovered' | 'needs_review' | 'published' | 'deprecated';
export type CapabilitySource = 'discovered' | 'published';

export interface BackendSpec {
  adapter: string;
  method: string;
  path: string;
  timeout_ms: number;
  base_url?: string;
}

export interface InputField {
  type: 'string' | 'integer' | 'number' | 'boolean';
  required: boolean;
}

export interface OutputSpec {
  kind: string;
  severity_path: string;
  summary_template: string;
  fields: Record<string, string>;
}

export interface GovernanceSpec {
  requires_action_plan: boolean;
  requires_approval: boolean;
  precheck_tools: string[];
  rollback: {
    strategy: string;
  };
}

export interface Capability {
  schema_version: number;
  name: string;
  status: CapabilityStatus;
  domain: string;
  resource_type: string;
  operation: CapabilityOperation;
  risk: CapabilityRisk;
  backend: BackendSpec;
  input_schema: Record<string, InputField>;
  output: OutputSpec;
  auth: {
    roles: string[];
    environment_scoped: boolean;
  };
  ai: {
    description: string;
    examples: string[];
  };
  governance?: GovernanceSpec;
}

export interface ValidationResult {
  valid: boolean;
  error?: string;
  fields?: Record<string, string>;
}

export interface ManagedCapability extends Capability {
  source: CapabilitySource;
  path?: string;
  modified_at?: string;
  validation: ValidationResult;
}

export type ImportRecommendation = 'recommended' | 'needs_adjustment' | 'not_recommended';

export interface ImportPreviewSource {
  openapi_url: string;
  backend_base_url: string;
  fingerprint: string;
}

export interface ImportPreviewStats {
  total: number;
  recommended: number;
  needs_adjustment: number;
  not_recommended: number;
  read: number;
  write: number;
}

export interface ImportCandidateSummary {
  name: string;
  domain: string;
  resource_type: string;
  operation: CapabilityOperation;
  risk: CapabilityRisk;
}

export interface ImportCandidate {
  id: string;
  method: string;
  path: string;
  operation_id?: string;
  capability: Capability;
  summary?: ImportCandidateSummary;
  recommendation: ImportRecommendation;
  reasons: string[] | null;
  warnings: string[] | null;
}

export interface ImportPreview {
  source: ImportPreviewSource;
  stats: ImportPreviewStats;
  candidates: ImportCandidate[];
}

export interface ImportCandidateOverride {
  name: string;
  domain: string;
  resource_type: string;
  operation: CapabilityOperation;
  risk: CapabilityRisk;
}

export interface ImportCommitSelection {
  candidate_id: string;
  overrides: ImportCandidateOverride;
}

export interface OpenAPIURLCommitPayload {
  openapi_url: string;
  backend_base_url: string;
  fingerprint: string;
  selections: ImportCommitSelection[];
}

export interface OpenAPIURLCommitResult {
  capabilities: ManagedCapability[];
  skipped: Array<{ candidate_id: string; reason: string }>;
}

export interface NormalizedResult {
  kind: string;
  resource: {
    domain: string;
    type: string;
    name: string;
    environment: string;
  };
  severity: string;
  summary: string;
  data: Record<string, unknown>;
}

export type DiagnosticSeverity = 'ok' | 'info' | 'warning' | 'critical';
export type DiagnosticConfidence = 'low' | 'medium' | 'high';

export interface ResourceRef {
  domain: string;
  type: string;
  id: string;
  name: string;
  environment: string;
  labels?: Record<string, string>;
}

export interface Observation {
  id: string;
  resource_id: string;
  kind: string;
  severity: DiagnosticSeverity;
  summary: string;
  data?: Record<string, unknown>;
  collected_at: string;
}

export interface Finding {
  id: string;
  severity: DiagnosticSeverity;
  summary: string;
  evidence_ids: string[];
  confidence: DiagnosticConfidence;
}

export interface Recommendation {
  id: string;
  summary: string;
  rationale: string;
  risk: CapabilityRisk;
  actionable: boolean;
  tool_name?: string;
  candidate_input?: Record<string, unknown>;
}

export interface DiagnosticPackage {
  id: string;
  environment: string;
  domains: string[];
  resources: ResourceRef[];
  observations: Observation[];
  findings: Finding[];
  recommendations: Recommendation[];
  plan_ids?: string[];
  created_at: string;
}

export interface PendingPlanSummary {
  id: string;
  tool: string;
  environment: string;
  risk: string;
  status: string;
  version: number;
  expires_at: string;
  created_by: string;
  created_at: string;
}

export interface PendingPlanDetail extends PendingPlanSummary {
  input: Record<string, unknown>;
}

export interface ConfirmPlanPayload {
  expected_version: number;
  confirmation_token?: string;
}

export type VerificationStatus = 'success' | 'failed' | 'denied';

export interface VerificationResult {
  tool_name?: string;
  status: VerificationStatus;
  answer?: Record<string, unknown>;
  error?: string;
  elapsed_ms?: number;
}

export interface SuggestedStrategy {
  /** 超时（纳秒，后端 time.Duration JSON 序列化） */
  timeout?: number;
  retry?: number;
  concurrency?: number;
  target_hosts?: string[];
  risk_level?: string;
}

export interface ExecutionResult {
  type: 'execution_result';
  plan_id: string;
  execution_id: string;
  status: string;
  reused: boolean;
  confirmed_status?: string;
  /** Runbook 自动执行时携带命中的 Runbook slug */
  runbook?: string;
  verification?: VerificationResult;
}

export type AssistantConsoleResponse =
  | { type: 'answer'; tool: string; answer: Record<string, unknown>; diagnostic?: DiagnosticPackage; trace?: AssistantTrace; blocks?: Block[]; conversation_id?: string; turn_id?: string }
  | {
      /** 兜底/收敛结论：agent 循环未得到模型 final_answer，由系统根据已执行步骤合成。
       *  message 为面向操作员的中文兜底总结；后端持久化为 answer_converged，前端据此打标。 */
      type: 'answer_converged';
      message: string;
      answer?: Record<string, unknown>;
      tool?: string;
      trace?: AssistantTrace;
      blocks?: Block[];
      conversation_id?: string;
      turn_id?: string;
    }
  | {
      type: 'confirmation_required';
      tool: string;
      plan_id: string;
      status: string;
      version: number;
      expires_at: string;
      summary: string;
      confirmation_token?: string;
      trace?: AssistantTrace;
      conversation_id?: string;
      turn_id?: string;
    }
  | { type: 'clarification_needed'; message: string; trace?: AssistantTrace; conversation_id?: string; turn_id?: string }
  | {
      type: 'execution_result';
      plan_id: string;
      execution_id: string;
      status: string;
      reused: boolean;
      /** Runbook 自动执行的 answer：{execution_id, runbook, reused} */
      answer?: Record<string, unknown>;
      runbook?: string;
      verification?: VerificationResult;
      blocks?: Block[];
      conversation_id?: string;
      turn_id?: string;
    };

export type ConversationRole = 'user' | 'assistant';

export interface ToolCall {
  tool: string;
  input?: Record<string, unknown>;
  raw_response?: Record<string, unknown>;
  done: boolean;
}

/**
 * 进度阶段事件，来自 SSE progress 事件。
 * 对应后端 assistant.ProgressEvent，反映 plan→policy→execution 链路的阶段切换。
 * - planning: 模型规划中（LLM 解析 intent）
 * - tool_executing: 工具执行中（诊断/读/写），detail 携带工具名
 * - formatting: 二阶段整形中（仅当 formatter 接入时触发）
 */
export interface ProgressStage {
  stage: 'planning' | 'tool_executing' | 'formatting';
  detail?: string;
  /** 接收到该阶段事件的时间戳，用于时间线展示 */
  received_at: string;
}

/**
 * 智能体循环执行的单步，来自 SSE step 事件（后端 assistant.StepEvent）。
 * 反映自治 agent 多工具链式执行中的一次只读工具调用（advisory step）。
 * 前端将其展示为独立"已执行步骤"区块，与最终答复分开。
 */
export interface AssistantStep {
  tool: string;
  /** 零基步骤序号，用于消歧同一工具的多步调用 */
  step_index: number;
  /** 状态：done（当前仅支持完成态，进行中态预留） */
  status: string;
  /** 人类可读的结果摘要 */
  summary?: string;
  /** 工具输入参数 */
  input?: Record<string, unknown>;
  /** 工具原始返回结果 */
  output?: Record<string, unknown>;
}

export interface ConversationTurn {
  id: string;
  conversation_id: string;
  parent_turn_id?: string;
  role: ConversationRole;
  content: string;
  response_type?: string;
  response_payload?: Record<string, unknown>;
  created_at: string;
  /** 标记该 turn 为错误气泡（流式请求失败时保留上下文用） */
  error?: boolean;
  /** 模型推理过程文本（chain-of-thought），来自 SSE thinking 事件 */
  thinking?: string;
  /** 工具调用信息，来自 response.trace.tool_invocation */
  tool_invocation?: {
    tool: string;
    input?: Record<string, unknown>;
    raw_response?: Record<string, unknown>;
  };
  /** 实时工具调用列表，来自 SSE tool_call 事件 */
  tool_calls?: ToolCall[];
  /**
   * 自治 agent 循环的多步执行步骤，来自 SSE step 事件。
   * 每次只读工具调用一条，独立于最终答复展示为"已执行步骤"区块。
   */
  steps?: AssistantStep[];
  /**
   * 进度阶段时间线，来自 SSE progress 事件。
   * 前端折叠展示为"进度事件折叠"面板，让用户看到 Agent 当前处于哪个阶段。
   * 仅流式生成期间累积；最终响应返回后保留历史用于复盘。
   */
  progress_stages?: ProgressStage[];
}

export interface ConversationSummary {
  id: string;
  subject: string;
  title: string;
  last_message_preview: string;
  created_at: string;
  last_active_at: string;
  archived_at?: string | null;
}

export interface ConversationDetail extends ConversationSummary {
  turns: ConversationTurn[];
  next_turn_cursor?: string | null;
}

export interface ConversationPage {
  conversations: ConversationSummary[];
  next_cursor: string;
}

export interface ConversationListFilter {
  limit?: number;
  archived?: boolean;
  cursor?: string;
}

export interface ConversationTurnsFilter {
  limit?: number;
  before_turn_id?: string;
}

export interface CapabilityCandidate {
  name: string;
  score: number;
  reasons?: string[];
}

export interface ExtractedParameter {
  name: string;
  value: unknown;
  source: string;
}

export interface CapabilitySelection {
  selected: string;
  confidence: number;
  reason?: string;
  candidates?: CapabilityCandidate[];
  extracted?: ExtractedParameter[];
  missing?: string[];
}

export interface ToolInvocation {
  tool: string;
  input?: Record<string, unknown>;
  raw_response?: Record<string, unknown>;
}

export interface AssistantTrace {
  selection?: CapabilitySelection;
  tool_invocation?: ToolInvocation;
}

export interface AuditEvent {
  id: string;
  plan_id: string;
  execution_id?: string;
  request_id?: string;
  trace_id?: string;
  subject: string;
  tool_name: string;
  action: string;
  decision: string;
  metadata?: Record<string, unknown>;
  created_at: string;
}

export interface AuditEventFilter {
  tool?: string;
  action?: string;
  decision?: string;
  subject?: string;
  after?: string;
  before?: string;
  limit?: number;
  cursor_created_at?: string;
  cursor_id?: string;
  /** 借鉴-4: 仅显示最终执行结果，隐藏驳回/未执行的审批流 */
  final_result_only?: boolean;
}

export interface AuditEventCursor {
  created_at: string;
  id: string;
}

export interface AuditEventPage {
  events: AuditEvent[];
  next_cursor?: AuditEventCursor | null;
}

export interface QuickPublishPayload {
  name: string;
  domain: string;
  resource_type: string;
  backend_base_url: string;
  method: 'GET';
  path: string;
  description: string;
  summary_template?: string;
  examples?: string[];
}

// ===== 定时巡检任务 =====
export type ScheduleKind = 'preset' | 'cron';
export type SchedulePreset = '5m' | '1h' | 'daily' | 'weekly';
export type ScheduledTaskStatus = 'succeeded' | 'failed' | '';

export interface ScheduledTask {
  id: string;
  name: string;
  subject: string;
  capability_name: string;
  input: Record<string, unknown>;
  schedule_kind: ScheduleKind;
  preset: SchedulePreset | null;
  cron_expr: string | null;
  timezone: string;
  enabled: boolean;
  last_run_at: string | null;
  last_status: ScheduledTaskStatus;
  next_run_at: string;
  created_at: string;
  updated_at: string;
}

export interface ScheduledTaskRun {
  id: string;
  task_id: string;
  started_at: string;
  finished_at: string;
  status: 'succeeded' | 'failed';
  result_summary: string;
  result_data: Record<string, unknown> | null;
  error: string;
  audit_event_id: string;
}

export interface CreateScheduledTaskPayload {
  name: string;
  capability_name: string;
  input: Record<string, unknown>;
  schedule_kind: ScheduleKind;
  preset?: SchedulePreset | null;
  cron_expr?: string | null;
  timezone?: string;
  enabled?: boolean;
}

export interface UpdateScheduledTaskPayload {
  name: string;
  capability_name: string;
  input: Record<string, unknown>;
  schedule_kind: ScheduleKind;
  preset?: SchedulePreset | null;
  cron_expr?: string | null;
  timezone?: string;
  enabled?: boolean;
}

// ===== Admin: Prompt 管理 =====

export interface AdminPrompt {
  name: string;
  version: number;
  description: string;
  content: string;
}

export interface AdminPromptListResponse {
  prompts: AdminPrompt[];
}

export interface UpdatePromptPayload {
  content: string;
  description?: string;
}

// ===== Admin: 知识库 =====

export interface KnowledgeDocument {
  id: string;
  title: string;
  content: string;
  source: string;
  created_at: string;
}

export interface KnowledgeListResponse {
  documents: KnowledgeDocument[];
}

export interface AddKnowledgePayload {
  title: string;
  content: string;
  source?: string;
}

// ===== Admin: 用户反馈 =====

export interface FeedbackEntry {
  id: string;
  conversation_id: string;
  turn_id: string;
  subject: string;
  rating: number;
  correction: string;
  created_at: string;
}

export interface FeedbackPage {
  items: FeedbackEntry[];
  total: number;
  limit: number;
  offset: number;
}

// ===== MCP 服务器热配置 =====

export interface MCPServer {
  id: string;
  name: string;
  command: string;
  args: string[];
  env: Record<string, string>;
  url: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface SaveMCPServerPayload {
  name: string;
  command: string;
  args: string[];
  env: Record<string, string>;
  url: string;
  enabled: boolean;
}

// ===== AIOps 结构化响应 block 协议（对齐 SxDevOps AIOps 2.0） =====

export type BlockType =
  | 'incident_card'
  | 'evidence_timeline'
  | 'query_suggestion'
  | 'chart_query'
  | 'alert_rule_draft'
  | 'dashboard_draft'
  | 'change_candidate'
  | 'rollback_plan'
  | 'k8s_action'
  | 'self_heal_recommendation'
  | 'approval_form'
  | 'tool_trace'
  | 'risk_notice';

export interface Block {
  type: BlockType;
  title?: string;
  content?: string;
  payload?: Record<string, unknown>;
}
