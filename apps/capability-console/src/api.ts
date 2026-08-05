import type {
  AdminPrompt,
  AssistantConsoleResponse,
  AuditEvent,
  AuditEventFilter,
  AuditEventPage,
  Capability,
  ConfirmPlanPayload,
  ConversationDetail,
  ConversationListFilter,
  ConversationPage,
  ConversationTurnsFilter,
  CreateScheduledTaskPayload,
  ExecutionFilter,
  ExecutionPage,
  ExecutionRecord,
  ExecutionResult,
  FeedbackEntry,
  FeedbackPage,
  ImportPreview,
  IncidentViewPivot,
  IncidentViewResult,
  InspectionReport,
  KnowledgeDocument,
  KnowledgeListResponse,
  ManagedCapability,
  MarketplaceDownload,
  MarketplacePublishPayload,
  MarketplacePublishResult,
  MarketplaceRating,
  MarketplaceRegistry,
  MarketplaceSearchFilter,
  MarketplaceSearchPage,
  MarketplaceStats,
  MarketplaceVersion,
  MCPServer,
  NormalizedResult,
  OpenAPIURLCommitPayload,
  OpenAPIURLCommitResult,
  PendingPlanDetail,
  PendingPlanSummary,
  QuickPublishPayload,
  RunbookDraft,
  SaveMCPServerPayload,
  ScheduledTask,
  ScheduledTaskRun,
  UpdateScheduledTaskPayload,
  ValidationResult,
} from './types';
import { normalizeCapability } from './capability';
import { ref } from 'vue';

class APIError extends Error {
  status: number;
  path: string;

  constructor(message: string, status: number, path: string) {
    super(message);
    this.name = 'APIError';
    this.status = status;
    this.path = path;
  }
}

// ===== CAS SSO support =====

export interface AuthConfig {
  mode: 'jwt' | 'cas' | 'both';
  cas_login_url: string;
}

let cachedAuthConfig: AuthConfig | null = null;

/** Fetch (and cache) the backend auth configuration. */
export async function getAuthConfig(): Promise<AuthConfig> {
  if (cachedAuthConfig) return cachedAuthConfig;
  try {
    const resp = await fetch('/v1/auth/config');
    if (resp.ok) {
      cachedAuthConfig = (await resp.json()) as AuthConfig;
      return cachedAuthConfig;
    }
  } catch { /* ignore */ }
  return { mode: 'jwt', cas_login_url: '' };
}

/**
 * Handle 401 responses: when auth mode includes CAS, redirect the browser
 * to the CAS login page. Returns true if a redirect was initiated.
 */
async function handleUnauthorized(): Promise<boolean> {
  const config = await getAuthConfig();
  if ((config.mode === 'cas' || config.mode === 'both') && config.cas_login_url) {
    window.location.href = config.cas_login_url;
    return true;
  }
  return false;
}

/**
 * Holds the trace-id extracted from the most recent API response's
 * `traceparent` (or `x-trace-id`) header. Components can read this reactive
 * ref to surface the current request's trace for debugging / Jaeger links.
 */
export const lastTraceId = ref<string | null>(null);

/**
 * Parse the trace-id out of a response's `traceparent` (W3C format
 * `00-{trace-id}-{span-id}-{flags}`) or, when that header is absent, fall back
 * to the `x-trace-id` header (used verbatim, since it is already a bare
 * trace-id rather than W3C format). Returns null when no usable trace-id is
 * present.
 */
export function extractTraceId(response: Response): string | null {
  const traceparent = response.headers.get('traceparent');
  if (traceparent) {
    const parts = traceparent.split('-');
    // W3C traceparent: version-trace_id-span_id-flags (4 parts); the trace-id
    // is the second segment.
    if (parts.length >= 2 && parts[1]) {
      return parts[1];
    }
    // Malformed traceparent — fall through to the x-trace-id fallback below.
  }
  const xTraceId = response.headers.get('x-trace-id');
  if (xTraceId && xTraceId.trim()) {
    return xTraceId.trim();
  }
  return null;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(init?.headers ?? {}),
    },
  });
  // Capture the trace-id from the response headers so other components can
  // reference it (e.g. for Jaeger lookups). Done before error handling so a
  // failed request still records its trace-id for debugging.
  const traceId = extractTraceId(response);
  if (traceId) {
    lastTraceId.value = traceId;
  }
  const text = await response.text();
  // Guard against non-JSON responses (e.g. plain-text 404 from Go's
  // http.NotFound). Attempt to parse, but fall back to null when the body
  // is not valid JSON so the error branch below can surface a useful message.
  let body: unknown = null;
  if (text.trim() !== '') {
    try {
      body = JSON.parse(text);
    } catch {
      body = null;
    }
  }
  if (!response.ok) {
    // On 401, attempt CAS redirect when configured.
    if (response.status === 401) {
      await handleUnauthorized();
    }
    const message = (body as Record<string, string>)?.error ?? `请求失败 (${response.status})：${path}`;
    throw new APIError(message, response.status, path);
  }
  return body as T;
}

export async function listCapabilities(): Promise<ManagedCapability[]> {
  const response = await fetch('/v1/capabilities', {
    headers: { 'Content-Type': 'application/json' },
  });
  if (import.meta.env.DEV && !response.headers.get('Content-Type')?.includes('application/json')) {
    return localPreviewCapabilities();
  }
  const body = (await response.json()) as { capabilities?: Partial<ManagedCapability>[]; error?: string };
  if (!response.ok) {
    throw new Error(body.error ?? '加载 Capability 失败');
  }
  if (!body.capabilities) {
    throw new Error('Capability 列表响应格式不正确');
  }
  return body.capabilities.map(normalizeCapability);
}

export interface OpenAPIURLImportPayload {
  openapi_url: string;
  backend_base_url: string;
}

export async function previewOpenAPIURL(payload: OpenAPIURLImportPayload): Promise<ImportPreview> {
  return request<ImportPreview>('/v1/capabilities/import/openapi-url/preview', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

export async function commitOpenAPIURLImport(payload: OpenAPIURLCommitPayload): Promise<OpenAPIURLCommitResult> {
  const body = await request<{ capabilities?: Partial<ManagedCapability>[]; skipped?: OpenAPIURLCommitResult['skipped'] }>('/v1/capabilities/import/openapi-url/commit', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
  return {
    capabilities: (body.capabilities ?? []).map(normalizeCapability),
    skipped: body.skipped ?? [],
  };
}

// importOpenAPIURL performs a one-shot OpenAPI import: fetch the spec from
// the URL and import every selected operation as a draft capability in a single
// request, bypassing the two-phase preview/commit flow. The payload mirrors
// capabilities.OpenAPIURLImportRequest in the backend. Returns the imported
// (draft) capabilities.
export async function importOpenAPIURL(payload: OpenAPIURLImportPayload): Promise<ManagedCapability[]> {
  const body = await request<{ capabilities?: Partial<ManagedCapability>[] }>('/v1/capabilities/import/openapi-url', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
  return (body.capabilities ?? []).map(normalizeCapability);
}

export async function saveDraft(capability: Capability): Promise<ManagedCapability> {
  const body = await request<Partial<ManagedCapability>>('/v1/capabilities/drafts', {
    method: 'POST',
    body: JSON.stringify(capability),
  });
  return normalizeCapability(body);
}

export async function validateCapability(capability: Capability): Promise<ValidationResult> {
  const body = await request<{ validation: ValidationResult }>('/v1/capabilities/validate', {
    method: 'POST',
    body: JSON.stringify(capability),
  });
  return body.validation;
}

export async function testCapability(capability: Capability, input: Record<string, unknown>): Promise<NormalizedResult> {
  const body = await request<{ result: NormalizedResult }>('/v1/capabilities/test', {
    method: 'POST',
    body: JSON.stringify({ capability, input }),
  });
  return body.result;
}

export async function sendAssistantMessage(
  message: string,
  conversationID?: string,
  signal?: AbortSignal,
  environment?: string,
  pageContext?: { domain?: string; environment?: string; resource_type?: string; resource_name?: string },
): Promise<AssistantConsoleResponse> {
  const payload: Record<string, unknown> = { message };
  if (conversationID) {
    payload.conversation_id = conversationID;
  }
  if (pageContext && (pageContext.domain || pageContext.environment || pageContext.resource_type || pageContext.resource_name)) {
    payload.page_context = pageContext;
  } else if (environment && environment !== 'none') {
    payload.environment = environment;
  }
  return request<AssistantConsoleResponse>('/v1/assistant/messages', {
    method: 'POST',
    body: JSON.stringify(payload),
    signal,
  });
}

export async function listConversations(filter: ConversationListFilter = {}): Promise<ConversationPage> {
  const params = new URLSearchParams();
  if (filter.limit !== undefined) params.set('limit', String(filter.limit));
  if (filter.archived) params.set('archived', 'true');
  if (filter.cursor) params.set('cursor', filter.cursor);
  const query = params.toString();
  const path = query ? `/v1/assistant/conversations?${query}` : '/v1/assistant/conversations';
  return request<ConversationPage>(path);
}

export async function getConversation(conversationID: string, filter: ConversationTurnsFilter = {}): Promise<ConversationDetail> {
  const params = new URLSearchParams();
  if (filter.limit !== undefined) params.set('limit', String(filter.limit));
  if (filter.before_turn_id) params.set('before_turn_id', filter.before_turn_id);
  const query = params.toString();
  const path = query
    ? `/v1/assistant/conversations/${encodeURIComponent(conversationID)}?${query}`
    : `/v1/assistant/conversations/${encodeURIComponent(conversationID)}`;
  const body = await request<{
    conversation: ConversationDetail;
    turns: ConversationDetail['turns'];
    next_cursor: string;
  }>(path);
  // The API returns conversation and turns separately; merge them for the
  // frontend so ConversationDetail is self-contained. Preserve next_cursor
  // as next_turn_cursor so callers can decide whether to load more history.
  return {
    ...body.conversation,
    turns: body.turns ?? [],
    next_turn_cursor: body.next_cursor ?? null,
  };
}

export async function archiveConversation(conversationID: string): Promise<void> {
  await request<void>(`/v1/assistant/conversations/${encodeURIComponent(conversationID)}/archive`, {
    method: 'POST',
  });
}

export async function publishCapability(name: string): Promise<ManagedCapability> {
  try {
    const body = await request<Partial<ManagedCapability> | null>(`/v1/capabilities/${encodeURIComponent(name)}/publish`, {
      method: 'POST',
      body: '{}',
    });
    return normalizeCapability({ name, ...(body ?? {}), status: 'published', source: 'published' });
  } catch (err) {
    if (err instanceof APIError && err.status === 409) {
      // 后端返回的 409 错误信息（见 internal/capabilities/manage.go）：
      //   - "... conflicts with an existing tool"
      //   - "... is already published, unpublish the old version first"
      //   - "... already exists as a draft, remove the draft first"
      //   - "... selected more than once in the same batch" / "... multiple candidates in the same batch"
      // 映射成中文友好提示
      const raw = err.message;
      if (raw.includes('tool')) {
        throw new APIError(`能力名称「${name}」与内置工具冲突，请修改名称后重试`, err.status, err.path);
      }
      if (raw.includes('already published')) {
        throw new APIError(`能力「${name}」已发布，请先下线旧版本`, err.status, err.path);
      }
      if (raw.includes('draft')) {
        throw new APIError(`能力「${name}」已有草稿，请先删除草稿再下线`, err.status, err.path);
      }
      throw new APIError(`能力名称「${name}」冲突，请修改名称或下线同名能力`, err.status, err.path);
    }
    throw err;
  }
}

export async function unpublishCapability(name: string): Promise<ManagedCapability> {
  const body = await request<Partial<ManagedCapability> | null>(`/v1/capabilities/${encodeURIComponent(name)}/unpublish`, {
    method: 'POST',
    body: '{}',
  });
  return normalizeCapability({ name, ...(body ?? {}), status: 'needs_review', source: 'discovered' });
}

export async function quickPublishCapability(payload: QuickPublishPayload): Promise<ManagedCapability> {
  const body = await request<Partial<ManagedCapability>>('/v1/capabilities/quick-publish', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
  return normalizeCapability(body);
}

export async function listPendingPlans(): Promise<PendingPlanSummary[]> {
  const body = await request<{ plans: PendingPlanSummary[] }>('/v1/action-plans?status=pending_confirmation');
  return body.plans ?? [];
}

export async function getPendingPlan(planID: string): Promise<PendingPlanDetail> {
  return request<PendingPlanDetail>(`/v1/action-plans/${encodeURIComponent(planID)}`);
}

export async function confirmPlan(planID: string, payload: ConfirmPlanPayload): Promise<ExecutionResult> {
  return request<ExecutionResult>(`/v1/action-plans/${encodeURIComponent(planID)}/confirm`, {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

export async function listAuditEvents(filter: AuditEventFilter = {}): Promise<AuditEventPage> {
  const params = new URLSearchParams();
  if (filter.tool) params.set('tool', filter.tool);
  if (filter.action) params.set('action', filter.action);
  if (filter.decision) params.set('decision', filter.decision);
  if (filter.subject) params.set('subject', filter.subject);
  if (filter.after) params.set('after', filter.after);
  if (filter.before) params.set('before', filter.before);
  if (filter.limit !== undefined) params.set('limit', String(filter.limit));
  if (filter.cursor_created_at) params.set('cursor_created_at', filter.cursor_created_at);
  if (filter.cursor_id) params.set('cursor_id', filter.cursor_id);
  if (filter.final_result_only) params.set('final_result_only', 'true');
  const query = params.toString();
  const path = query ? `/v1/audit-events?${query}` : '/v1/audit-events';
  return await request<AuditEventPage>(path);
}

export async function searchAuditEvents(query: string): Promise<AuditEventPage> {
  const params = new URLSearchParams();
  params.set('q', query);
  return request<AuditEventPage>(`/v1/audit-events/search?${params.toString()}`);
}

// ===== Executions 执行历史 =====
// 对应 GET /v1/executions（admin-only，内部含敏感错误/入参，operator/viewer 用审计记录）。
// 支持 status / action_plan_id / tool / started_after / started_before / limit /
// cursor_created_at / cursor_id 过滤与游标分页；时间参数需 RFC3339。

export async function listExecutions(filter: ExecutionFilter = {}): Promise<ExecutionPage> {
  const params = new URLSearchParams();
  if (filter.status) params.set('status', filter.status);
  if (filter.action_plan_id) params.set('action_plan_id', filter.action_plan_id);
  if (filter.tool) params.set('tool', filter.tool);
  if (filter.started_after) params.set('started_after', filter.started_after);
  if (filter.started_before) params.set('started_before', filter.started_before);
  if (filter.limit !== undefined) params.set('limit', String(filter.limit));
  if (filter.cursor_created_at) params.set('cursor_created_at', filter.cursor_created_at);
  if (filter.cursor_id) params.set('cursor_id', filter.cursor_id);
  const query = params.toString();
  const path = query ? `/v1/executions?${query}` : '/v1/executions';
  return request<ExecutionPage>(path);
}

// ===== Inspection Reports 巡检报告 =====
// 对应 GET /v1/inspection-reports（列表，任意登录用户，仅 limit 参数，由
// Reporter 按时间窗口聚合生成）与 GET /v1/inspection-reports/{id}（详情）。
// 列表返回裸数组（非 {reports: [...]} 包装）。

export async function listInspectionReports(limit = 50): Promise<InspectionReport[]> {
  const params = new URLSearchParams();
  if (limit > 0) params.set('limit', String(limit));
  return request<InspectionReport[]>(`/v1/inspection-reports?${params.toString()}`);
}

export async function getInspectionReport(id: string): Promise<InspectionReport> {
  return request<InspectionReport>(`/v1/inspection-reports/${encodeURIComponent(id)}`);
}

// ===== Capability Marketplace 能力市场 =====

export async function marketplaceSearch(filter: MarketplaceSearchFilter = {}): Promise<MarketplaceSearchPage> {
  const params = new URLSearchParams();
  if (filter.query) params.set('query', filter.query);
  if (filter.domain) params.set('domain', filter.domain);
  if (filter.category) params.set('category', filter.category);
  if (filter.risk_level) params.set('risk_level', filter.risk_level);
  if (filter.min_rating !== undefined) params.set('min_rating', String(filter.min_rating));
  if (filter.visibility) params.set('visibility', filter.visibility);
  if (filter.status) params.set('status', filter.status);
  if (filter.sort_by) params.set('sort_by', filter.sort_by);
  if (filter.limit !== undefined) params.set('limit', String(filter.limit));
  if (filter.offset !== undefined) params.set('offset', String(filter.offset));
  const query = params.toString();
  const path = query ? `/v1/marketplace/capabilities?${query}` : '/v1/marketplace/capabilities';
  return request<MarketplaceSearchPage>(path);
}

// 语义搜索：自然语言查询召回能力。当后端未配置向量检索时返回 503。
export async function marketplaceSemanticSearch(query: string): Promise<MarketplaceSearchPage> {
  const params = new URLSearchParams();
  params.set('semantic', 'true');
  if (query.trim()) params.set('query', query.trim());
  return request<MarketplaceSearchPage>(`/v1/marketplace/capabilities?${params.toString()}`);
}

export async function marketplaceGet(id: string): Promise<MarketplaceRegistry> {
  return request<MarketplaceRegistry>(`/v1/marketplace/capabilities/${encodeURIComponent(id)}`);
}

export async function marketplaceListVersions(id: string): Promise<MarketplaceVersion[]> {
  const body = await request<{ versions: MarketplaceVersion[] }>(
    `/v1/marketplace/capabilities/${encodeURIComponent(id)}/versions`,
  );
  return body.versions ?? [];
}

export async function marketplaceListRatings(id: string, limit = 20, offset = 0): Promise<{ ratings: MarketplaceRating[]; total: number }> {
  const params = new URLSearchParams();
  params.set('limit', String(limit));
  params.set('offset', String(offset));
  const body = await request<{ ratings: MarketplaceRating[]; total: number }>(
    `/v1/marketplace/capabilities/${encodeURIComponent(id)}/ratings?${params.toString()}`,
  );
  return { ratings: body.ratings ?? [], total: body.total ?? 0 };
}

export async function marketplaceStats(id: string): Promise<MarketplaceStats> {
  return request<MarketplaceStats>(`/v1/marketplace/capabilities/${encodeURIComponent(id)}/stats`);
}

export async function marketplacePublish(payload: MarketplacePublishPayload): Promise<MarketplacePublishResult> {
  return request<MarketplacePublishResult>('/v1/marketplace/capabilities', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

// 下载（含版本 YAML）并记录一次 download 统计。
export async function marketplaceDownload(id: string, versionID: string): Promise<MarketplaceDownload> {
  return request<MarketplaceDownload>(
    `/v1/marketplace/capabilities/${encodeURIComponent(id)}/download/${encodeURIComponent(versionID)}`,
  );
}

export async function marketplaceRate(
  id: string,
  payload: { rating: number; review?: string; version_used?: string; environment?: string },
): Promise<void> {
  await request<void>(`/v1/marketplace/capabilities/${encodeURIComponent(id)}/ratings`, {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

function localPreviewCapabilities(): ManagedCapability[] {
  return [
    normalizeCapability({
      name: 'minio.bucket.capacity.read',
      status: 'needs_review',
      source: 'discovered',
      domain: 'minio',
      resource_type: 'bucket',
      operation: 'read',
      risk: 'low',
      backend: {
        adapter: 'http',
        method: 'GET',
        base_url: 'https://middleware.example.com',
        path: '/api/minio/{cluster}/buckets/{bucket}/capacity',
        timeout_ms: 3000,
      },
      input_schema: {
        environment: { type: 'string', required: true },
        cluster: { type: 'string', required: true },
        bucket: { type: 'string', required: true },
      },
      output: {
        kind: 'observation',
        severity_path: '$.status',
        summary_template: 'Bucket {bucket} usage is {usage_pct}%',
        fields: { usage_pct: '$.data.usage_pct' },
      },
      validation: { valid: true },
    }),
    normalizeCapability({
      name: 'glusterfs.volume.health.read',
      status: 'published',
      source: 'published',
      domain: 'glusterfs',
      resource_type: 'volume',
      operation: 'read',
      risk: 'low',
      backend: {
        adapter: 'http',
        method: 'GET',
        base_url: 'https://middleware.example.com',
        path: '/api/glusterfs/{cluster}/volumes/{name}/health',
        timeout_ms: 3000,
      },
      validation: { valid: true },
    }),
    normalizeCapability({
      name: 'kafka.topic.retention.set',
      status: 'needs_review',
      source: 'discovered',
      domain: 'kafka',
      resource_type: 'topic',
      operation: 'write',
      risk: 'medium',
      backend: {
        adapter: 'http',
        method: 'POST',
        base_url: 'https://middleware.example.com',
        path: '/api/kafka/{cluster}/topics/{topic}/retention',
        timeout_ms: 3000,
      },
      governance: {
        requires_action_plan: true,
        requires_approval: true,
        precheck_tools: ['kafka.topic.retention.read'],
        rollback: { strategy: 'restore_previous' },
      },
      validation: { valid: true },
    }),
  ];
}

// ===== 定时巡检任务 =====

export async function listScheduledTasks(filter?: { enabled?: boolean }): Promise<ScheduledTask[]> {
  const params = new URLSearchParams();
  if (filter?.enabled === true) params.set('enabled', 'true');
  else if (filter?.enabled === false) params.set('enabled', 'false');
  const query = params.toString();
  const path = query ? `/v1/scheduled-tasks?${query}` : '/v1/scheduled-tasks';
  const body = await request<{ tasks: ScheduledTask[] }>(path);
  return body.tasks ?? [];
}

export async function getScheduledTask(id: string): Promise<ScheduledTask> {
  return request<ScheduledTask>(`/v1/scheduled-tasks/${encodeURIComponent(id)}`);
}

export async function createScheduledTask(payload: CreateScheduledTaskPayload): Promise<ScheduledTask> {
  return request<ScheduledTask>('/v1/scheduled-tasks', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

export async function updateScheduledTask(id: string, payload: UpdateScheduledTaskPayload): Promise<ScheduledTask> {
  return request<ScheduledTask>(`/v1/scheduled-tasks/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: JSON.stringify(payload),
  });
}

export async function deleteScheduledTask(id: string): Promise<void> {
  await request<void>(`/v1/scheduled-tasks/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });
}

export async function triggerScheduledTask(id: string): Promise<ScheduledTaskRun> {
  return request<ScheduledTaskRun>(`/v1/scheduled-tasks/${encodeURIComponent(id)}/run`, {
    method: 'POST',
  });
}

export async function listScheduledTaskRuns(id: string, limit?: number): Promise<ScheduledTaskRun[]> {
  const params = new URLSearchParams();
  if (limit !== undefined) params.set('limit', String(limit));
  const query = params.toString();
  const path = query
    ? `/v1/scheduled-tasks/${encodeURIComponent(id)}/runs?${query}`
    : `/v1/scheduled-tasks/${encodeURIComponent(id)}/runs`;
  const body = await request<{ runs: ScheduledTaskRun[] }>(path);
  return body.runs ?? [];
}

export async function countScheduledTaskFailures(): Promise<number> {
  const body = await request<{ count: number }>('/v1/scheduled-tasks/failures/count');
  return body.count ?? 0;
}

// ===== Admin: Prompt 管理 =====

export async function updateAdminPrompt(name: string, payload: { content: string; description?: string }): Promise<AdminPrompt> {
  return request<AdminPrompt>(`/v1/admin/prompts/${encodeURIComponent(name)}`, {
    method: 'PUT',
    body: JSON.stringify(payload),
  });
}

// ===== Admin: 知识库 =====

export async function listKnowledgeDocuments(): Promise<KnowledgeDocument[]> {
  const body = await request<KnowledgeListResponse>('/v1/admin/knowledge/documents');
  return body.documents ?? [];
}

export async function addKnowledgeDocument(payload: { title: string; content: string; source?: string }): Promise<KnowledgeDocument> {
  return request<KnowledgeDocument>('/v1/admin/knowledge/documents', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

export interface KnowledgeStatus {
  embedder_configured: boolean;
  documents_count: number;
  hint?: string;
}

export async function getKnowledgeStatus(): Promise<KnowledgeStatus> {
  return request<KnowledgeStatus>('/v1/admin/knowledge/status');
}

// ===== 用户反馈 =====

export async function createFeedback(payload: {
  conversation_id: string;
  turn_id: string;
  rating: 'up' | 'down';
  correction?: string;
}): Promise<FeedbackEntry> {
  const body: Record<string, unknown> = {
    conversation_id: payload.conversation_id,
    turn_id: payload.turn_id,
    rating: payload.rating === 'up' ? 1 : -1,
  };
  if (payload.correction) {
    body.correction = payload.correction;
  }
  return request<FeedbackEntry>('/v1/assistant/feedback', {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

export async function listFeedback(filter?: { limit?: number; offset?: number }): Promise<FeedbackPage> {
  const params = new URLSearchParams();
  if (filter?.limit !== undefined) params.set('limit', String(filter.limit));
  if (filter?.offset !== undefined) params.set('offset', String(filter.offset));
  const query = params.toString();
  const path = query ? `/v1/assistant/feedback?${query}` : '/v1/assistant/feedback';
  return request<FeedbackPage>(path);
}

// ===== Runbook 草稿（反馈 → 可确认启用的 runbook） =====
// 生成是确定性的（后端关键词→工具序列映射），前端只展示 + 确认启用。

export interface RunbookDraftListResponse {
  drafts: RunbookDraft[];
  configured?: boolean;
  hint?: string;
}

export interface InferRunbookDraftPayload {
  topic_key: string;
  examples: string[];
}

/** 为某个已聚合的反馈主题生成 runbook 草稿（未启用，内存暂存）。 */
export async function inferRunbookDraft(payload: InferRunbookDraftPayload): Promise<RunbookDraft> {
  return request<RunbookDraft>('/v1/admin/runbook-drafts/infer', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

/** 列出全部 runbook 草稿（draft + 已启用记录）。 */
export async function listRunbookDrafts(): Promise<RunbookDraftListResponse> {
  return request<RunbookDraftListResponse>('/v1/admin/runbook-drafts');
}

/** 确认启用：把草稿写入 runbook 注册表并即时生效，成功后草稿从列表移除。 */
export async function activateRunbookDraft(id: string): Promise<RunbookDraft> {
  return request<RunbookDraft>(`/v1/admin/runbook-drafts/${encodeURIComponent(id)}/activate`, {
    method: 'POST',
  });
}

// ===== MCP 服务器热配置 =====
// 后端 /v1/mcp/servers CRUD + /v1/mcp/servers/reload 触发增量注册/注销工具。
// 注意：list 返回直接数组（非 {servers: [...]} 包装）；update 用 PUT（非 PATCH）。

export async function listMCPServers(): Promise<MCPServer[]> {
  return request<MCPServer[]>('/v1/mcp/servers');
}

export async function createMCPServer(payload: SaveMCPServerPayload): Promise<MCPServer> {
  return request<MCPServer>('/v1/mcp/servers', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

export async function getMCPServer(id: string): Promise<MCPServer> {
  return request<MCPServer>(`/v1/mcp/servers/${encodeURIComponent(id)}`);
}

export async function updateMCPServer(id: string, payload: SaveMCPServerPayload): Promise<MCPServer> {
  return request<MCPServer>(`/v1/mcp/servers/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify(payload),
  });
}

export async function deleteMCPServer(id: string): Promise<void> {
  await request<void>(`/v1/mcp/servers/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });
}

export async function reloadMCPServers(): Promise<{ status: string }> {
  return request<{ status: string }>('/v1/mcp/servers/reload', {
    method: 'POST',
  });
}

// ===== incident.view 告警全景（Phase 3） =====
// 对应 POST /v1/tools/incident.view/read（通用工具读端点）。后端 incidentViewReadRunner
// 按 (domain, resource_type, resource_name, environment) 软匹配各证据源，返回
// { result: {...} }；此处解包 result。
export async function viewIncident(pivot: IncidentViewPivot): Promise<IncidentViewResult> {
  const data = await request<{ result: IncidentViewResult }>('/v1/tools/incident.view/read', {
    method: 'POST',
    body: JSON.stringify(pivot),
  });
  return data.result;
}

/**
 * executeRead 通过通用工具读端点执行一次只读探测（如 incident 全景里列出的 probe）。
 * 调用后端 `ReadOnlyService.ExecuteRead`，自动获得 policy 鉴权 + 审计记录。
 * 结果形态异构（能力读 / 静态元工具），返回宽松 unknown 由调用方格式化。
 */
export async function executeRead(toolName: string, input: Record<string, unknown>): Promise<unknown> {
  const data = await request<{ result: unknown }>(`/v1/tools/${toolName}/read`, {
    method: 'POST',
    body: JSON.stringify(input ?? {}),
  });
  return data.result;
}
