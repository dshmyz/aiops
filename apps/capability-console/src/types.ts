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
  in?: string;
  min?: number;
  max?: number;
  description?: string;
  examples?: string[];
  enum?: string[];
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

/**
 * PlanSummary 是诊断推荐建出的 action plan 的操作员侧摘要（后端
 * Response.RecommendationPlan，兼容字段，指向首个成功建 plan 的推荐）。
 */
export interface PlanSummary {
  plan_id: string;
  tool: string;
  risk: string;
  requires_confirmation: boolean;
  expires_at?: string;
}

/**
 * RecommendationStatus 记录诊断包中一条可执行推荐的处理结果（后端
 * Response.Recommendations）：
 *  - plan_created：写工具已建 plan 等待确认（plan_id 非空）
 *  - read_executed：读工具已直接执行
 *  - skipped：未能落地，reason 说明原因（工具未注册/策略拒绝/建 plan 失败）
 */
export interface RecommendationStatus {
  tool: string;
  summary?: string;
  status: 'plan_created' | 'read_executed' | 'skipped';
  reason?: string;
  plan_id?: string;
  risk?: string;
  expires_at?: string;
}

export interface PendingPlanDetail extends PendingPlanSummary {
  input: Record<string, unknown>;
}

export interface ConfirmPlanPayload {
  expected_version: number;
  confirmation_token?: string;
}

/** RejectPlanPayload 显式拒绝一个 pending action plan（POST /reject）。 */
export interface RejectPlanPayload {
  expected_version: number;
}

/** RejectPlanResult 是 reject 端点的响应。 */
export interface RejectPlanResult {
  type: 'plan_rejected';
  plan_id: string;
  status: string;
  version: number;
}

/** OverviewData 是 GET /v1/overview 的顶部统计计数（运维总览首屏）。缺失字段表示
 *  对应 service 未装配或当前用户无权限查看（执行数仅 admin）。 */
export interface OverviewData {
  pending_plans?: number;
  active_alerts?: number;
  enabled_tasks?: number;
  today_executions_succeeded?: number;
  today_executions_failed?: number;
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
  | { type: 'answer'; tool: string; answer: Record<string, unknown>; diagnostic?: DiagnosticPackage; recommendation_plan?: PlanSummary; recommendations?: RecommendationStatus[]; trace?: AssistantTrace; blocks?: Block[]; conversation_id?: string; turn_id?: string }
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
  /** 状态：done / running / failed（denied 归入 failed 展示），执行器路径可能传其他值 */
  status: string;
  /** 人类可读的结果摘要 */
  summary?: string;
  /** 工具输入参数 */
  input?: Record<string, unknown>;
  /** 工具原始返回结果 */
  output?: Record<string, unknown>;
  /** 失败/被拒时的原始错误或策略拒绝原因 */
  error?: string;
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

/**
 * 后端持久化的过程证据（turn 的 response_payload.process）。executor 流式路径在
 * 终局只落最终答复，思考文本与已执行步骤由后端流式期间累积、随 turn 落库；
 * 刷新或换设备回放后前端据此水合回瞬态字段，保证"所见即所得"。仅前端展示用，
 * 不会被回喂给 LLM。
 */
export interface TurnProcess {
  thinking?: string;
  steps?: AssistantStep[];
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

// ===== Executions 执行历史 =====
// 对 GET /v1/executions 的字面映射。此端点仅 admin 可见（内部含敏感错误/入参）。

export interface ExecutionRecord {
  id: string;
  action_plan_id: string;
  status: string;
  tool_name?: string;
  result_summary?: unknown;
  error_summary?: string;
  verification?: unknown;
  started_at?: string;
  completed_at?: string;
  created_at: string;
}

export interface ExecutionFilter {
  status?: string;
  action_plan_id?: string;
  tool?: string;
  started_after?: string;
  started_before?: string;
  limit?: number;
  cursor_created_at?: string;
  cursor_id?: string;
}

export interface ExecutionPage {
  executions: ExecutionRecord[];
  next_cursor?: AuditEventCursor | null;
}

// ===== Inspection Reports 巡检报告 =====
// 对 GET /v1/inspection-reports 及 /{id} 的字面映射。任意登录用户可见。

export interface InspectionTaskSummary {
  task_id: string;
  task_name: string;
  capability_name: string;
  total_runs: number;
  succeeded_runs: number;
  failed_runs: number;
  last_status?: string;
  last_result_summary?: string;
  last_error?: string;
  last_run_at?: string;
}

export interface InspectionReport {
  id: string;
  period: string;
  window_start: string;
  window_end: string;
  generated_at: string;
  total_tasks: number;
  succeeded_tasks: number;
  failed_tasks: number;
  task_summaries?: InspectionTaskSummary[];
  html_content: string;
}

// ===== Capability Marketplace 能力市场 =====
// 对 /v1/marketplace/capabilities* 的字面映射。读操作对 viewer/operator/admin
// 开放；发布需 admin。SemanticSearch 用自然语言查询召回能力（语义检索的入口）。

export interface MarketplaceRegistry {
  id: string;
  name: string;
  domain: string;
  resource_type: string;
  operation: string;
  risk_level: string;
  owner_id: string;
  visibility: string;
  organization_id?: string;
  description: string;
  tags?: string[];
  category?: string;
  download_count: number;
  usage_count: number;
  avg_rating?: number;
  rating_count: number;
  status: string;
  published_at?: string;
  deprecated_at?: string;
  created_at: string;
  updated_at: string;
}

export interface MarketplaceVersion {
  id: string;
  capability_id: string;
  version: string;
  yaml_content: string;
  yaml_hash: string;
  schema_version: number;
  backend_adapter: string;
  input_schema?: unknown;
  output_schema?: unknown;
  governance?: unknown;
  changelog?: string;
  breaking_changes?: string;
  status: string;
  published_at?: string;
  published_by?: string;
  created_at: string;
}

export interface MarketplaceRating {
  id: string;
  capability_id: string;
  user_id: string;
  rating: number;
  review?: string;
  version_used?: string;
  environment?: string;
  created_at: string;
  updated_at: string;
}

export interface MarketplaceStats {
  capability_id: string;
  total_downloads: number;
  total_executions: number;
  success_rate: number;
  avg_duration_ms?: number;
  executions_by_environment: Record<string, number>;
}

export interface MarketplaceSearchFilter {
  query?: string;
  domain?: string;
  category?: string;
  risk_level?: string;
  min_rating?: number;
  visibility?: string;
  status?: string;
  sort_by?: string;
  limit?: number;
  offset?: number;
}

export interface MarketplaceSearchPage {
  capabilities: MarketplaceRegistry[];
  total: number;
  semantic?: boolean;
  next_offset?: number;
}

export interface MarketplacePublishPayload {
  yaml_content: string;
  version: string;
  visibility: string;
  organization_id?: string;
  tags?: string[];
  category?: string;
  changelog?: string;
}

export interface MarketplacePublishResult {
  capability: MarketplaceRegistry;
  version: MarketplaceVersion;
}

export interface MarketplaceDownload {
  version: string;
  yaml_content: string;
  yaml_hash: string;
}

export type HttpMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';

export interface QuickPublishPayload {
  name: string;
  domain: string;
  resource_type: string;
  backend_base_url: string;
  method: HttpMethod;
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
  run_kind?: 'read' | 'runbook';
  runbook_slug?: string | null;
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
  run_kind?: 'read' | 'runbook';
  runbook_slug?: string | null;
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
  run_kind?: 'read' | 'runbook';
  runbook_slug?: string | null;
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
  /** 后端对无纠正的条目返回 null */
  correction: string | null;
  created_at: string;
}

export interface FeedbackPage {
  items: FeedbackEntry[];
  total: number;
  limit: number;
  offset: number;
}

// ===== Runbook 草稿（反馈 → 可确认启用的 runbook） =====
// 后端 /v1/admin/runbook-drafts* 的字面映射。反馈页为可落 runbook 的主题生成
// 草稿，操作员确认后 activate 写入注册表并被 RunbookRouter 即时命中。

export interface RunbookDraft {
  id: string;
  slug: string;
  name: string;
  intent_pattern: string[];
  tool_sequence: string[];
  risk_level: string;
  topic_key: string;
  /** 非空表示该主题无法落成 runbook（草稿不可启用，仅人工判断）。 */
  missing_reason?: string;
  status: string;
  created_at: string;
  activated_at?: string | null;
}

export interface RunbookDraftListResponse {
  drafts: RunbookDraft[];
  configured?: boolean;
  hint?: string;
}

// ===== Runbook 模板（E2 Phase 3：定时任务 run_kind=runbook 可调度的模板） =====
// 后端 GET /v1/runbooks 只返回 enabled + risk_level===low 的模板（定时写安全边界）。
// 定时任务表单「任务类型 = runbook」时下拉其 slug/name。

export interface Runbook {
  id: string;
  slug: string;
  name: string;
  intent_pattern: string[];
  tool_sequence: string[];
  risk_level: string;
  is_builtin?: boolean;
  is_enabled: boolean;
}

export interface RunbookListResponse {
  configured?: boolean;
  runbooks: Runbook[];
  hint?: string;
}

export interface InferRunbookDraftPayload {
  topic_key: string;
  examples: string[];
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

// ===== incident.view 告警全景（后端 incidentViewReadRunner 输出契约） =====

export interface IncidentViewPivot {
  domain?: string;
  resource_type?: string;
  resource_name?: string;
  environment?: string;
  since?: string;
  until?: string;
}

export interface IncidentTimelineItem {
  id: string;
  tool_name: string;
  action: string;
  decision: string;
  subject?: string;
  created_at: string;
  action_plan_id?: string | null;
  tool_execution_id?: string | null;
  trace_id?: string | null;
}

export interface IncidentRun {
  id: string;
  task_id: string;
  status: string;
  started_at: string;
  finished_at: string;
  audit_event_id?: string;
}

export interface IncidentProbe {
  tool_name: string;
  operation: string;
  input: Record<string, unknown>;
}

export interface IncidentRunbook {
  slug: string;
  name?: string;
  risk_level?: string;
  confidence: number;
  tool_sequence?: string[];
}

export interface IncidentRecentWrites {
  count: number;
  events: IncidentTimelineItem[];
}

export interface IncidentCounts {
  audit: number;
  scheduled_runs: number;
  probes: number;
  runbooks: number;
  recent_writes: number;
}

export interface IncidentViewResult {
  tool?: string;
  incident_id?: string;
  pivot?: Partial<IncidentViewPivot>;
  // alert 是后端 alert.Alert 的 JSON 序列化，字段松散，按需弱类型访问。
  alert?: Record<string, unknown> | null;
  timeline: IncidentTimelineItem[];
  scheduled_runs: IncidentRun[];
  probes: IncidentProbe[];
  runbooks: IncidentRunbook[];
  recent_writes: IncidentRecentWrites;
  counts: IncidentCounts;
}
