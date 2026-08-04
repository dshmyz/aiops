import { mount, flushPromises } from '@vue/test-utils';
import ElementPlus from 'element-plus';
import { readFileSync } from 'node:fs';
import { describe, expect, test, vi, beforeEach, afterEach } from 'vitest';
import App from './App.vue';
import ManagementView from './views/ManagementView.vue';
import { setStreamingEnabled } from './composables/useAssistant';

function ok(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((next) => {
    resolve = next;
  });
  return { promise, resolve };
}

function mountApp() {
  return mount(App, {
    global: {
      plugins: [ElementPlus],
    },
  });
}

async function openManagement(wrapper: ReturnType<typeof mountApp>) {
  await wrapper.find('[data-test="nav-management"]').trigger('click');
  await flushPromises();
  await flushPromises();
}

/** Navigate to a view and wait for async component resolution. */
async function navigateTo(wrapper: ReturnType<typeof mountApp>, selector: string) {
  await wrapper.find(selector).trigger('click');
  await flushPromises();
  // defineAsyncComponent resolves via dynamic import; poll until the target
  // view section appears in the DOM.
  const viewMap: Record<string, string> = {
    '[data-test="nav-audit"]': '[data-test="audit-entry"]',
    '[data-test="nav-plans"]': '[data-test="plans-entry"]',
    '[data-test="nav-scheduled-tasks"]': '[data-test="scheduled-tasks-entry"]',
    '[data-test="nav-management"]': '[data-test="management-entry"]',
  };
  const targetSelector = viewMap[selector];
  if (targetSelector) {
    await vi.waitFor(() => {
      if (!wrapper.find(targetSelector).exists()) {
        throw new Error(`${targetSelector} not yet rendered`);
      }
    }, { timeout: 3000, interval: 20 });
  }
  await flushPromises();
}

async function openReview(wrapper: ReturnType<typeof mountApp>) {
  await openManagement(wrapper);
  await wrapper.find('[data-test="workflow-step-review"]').trigger('click');
  await flushPromises();
}

// The management view now lands on the capability list (review). These helpers
// reach the sibling phases from there.
async function openSource(wrapper: ReturnType<typeof mountApp>) {
  await openManagement(wrapper);
  await wrapper.find('[data-test="workflow-step-source"]').trigger('click');
  await flushPromises();
}

describe('Capability Console', () => {
  beforeEach(() => {
    // Disable SSE streaming in tests so existing fetch mocks for
    // /v1/assistant/messages continue to work without SSE mocking.
    setStreamingEnabled(false);
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({
          capabilities: [
            {
              name: 'minio.bucket.capacity.read',
              status: 'needs_review',
              source: 'discovered',
              domain: 'minio',
              resource_type: 'bucket',
              operation: 'read',
              risk: 'low',
              backend: { method: 'GET', base_url: 'https://middleware.example.com', path: '/api/minio/{cluster}/buckets/{bucket}/capacity' },
              validation: { valid: true },
            },
            {
              name: 'kafka.topic.retention.set',
              status: 'needs_review',
              source: 'discovered',
              domain: 'kafka',
              resource_type: 'topic',
              operation: 'write',
              risk: 'medium',
              backend: { method: 'POST', base_url: 'https://middleware.example.com', path: '/api/kafka/{cluster}/topics/{topic}/retention' },
              governance: {
                requires_action_plan: true,
                requires_approval: true,
                precheck_tools: ['kafka.topic.retention.read'],
                rollback: { strategy: 'restore_previous' },
              },
              validation: { valid: true },
            },
            {
              name: 'glusterfs.volume.health.read',
              status: 'published',
              source: 'published',
              domain: 'glusterfs',
              resource_type: 'volume',
              operation: 'read',
              risk: 'low',
              backend: { method: 'GET', base_url: 'https://middleware.example.com', path: '/api/glusterfs/{cluster}/volumes/{name}/health' },
              validation: { valid: false, error: 'output fields are required' },
            },
          ],
        });
      }
      if (url === '/v1/capabilities/import/openapi-url/preview') {
        return ok({
          source: {
            openapi_url: 'https://admin.example.com/v3/api-docs',
            backend_base_url: 'https://middleware.example.com',
            fingerprint: 'sha256:test',
          },
          stats: {
            total: 2,
            recommended: 1,
            needs_adjustment: 0,
            not_recommended: 1,
            read: 1,
            write: 1,
          },
          candidates: [
            {
              id: 'GET /api/minio/{cluster}/buckets/{bucket}/capacity',
              method: 'GET',
              path: '/api/minio/{cluster}/buckets/{bucket}/capacity',
              operation_id: 'getMinioBucketCapacity',
              capability: {
                name: 'minio.bucket.capacity.read.imported',
                domain: 'minio',
                resource_type: 'bucket',
                operation: 'read',
                risk: 'low',
              },
              recommendation: 'recommended',
              reasons: ['GET read operation'],
              warnings: null,
            },
            {
              id: 'POST /api/kafka/{cluster}/topics/{topic}/retention',
              method: 'POST',
              path: '/api/kafka/{cluster}/topics/{topic}/retention',
              operation_id: 'setKafkaTopicRetention',
              capability: {
                name: 'kafka.topic.retention.update',
                domain: 'kafka',
                resource_type: 'topic',
                operation: 'write',
                risk: 'medium',
              },
              recommendation: 'not_recommended',
              reasons: null,
              warnings: null,
            },
          ],
        });
      }
      if (url === '/v1/capabilities/import/openapi-url/commit') {
        return ok({
          capabilities: [
            {
              name: 'minio.bucket.capacity.read.imported',
              status: 'needs_review',
              source: 'discovered',
              domain: 'minio',
              resource_type: 'bucket',
              operation: 'read',
              risk: 'low',
              backend: { method: 'GET', base_url: 'https://middleware.example.com', path: '/api/minio/{cluster}/buckets/{bucket}/capacity' },
              validation: { valid: true },
            },
          ],
          skipped: [{ candidate_id: 'POST /api/kafka/{cluster}/topics/{topic}/retention', reason: 'not selected' }],
        });
      }
      if (url === '/v1/assistant/messages') {
        const body = JSON.parse(String(_init?.body ?? '{}')) as { message?: string };
        if (body.message === '查询 prod glusterfs') {
          return ok({ type: 'clarification_needed', message: '缺少参数: cluster, name' });
        }
        if (body.message === '把 orders retention 调成 72 小时') {
          return ok({
            type: 'execution_result',
            tool: 'kafka.topic.retention.set',
            plan_id: 'plan-rb-1',
            status: 'succeeded',
            conversation_id: 'conv-rb',
            turn_id: 'turn-rb',
            answer: { execution_id: 'exec-rb-1', runbook: 'retention-tweak', reused: false },
            blocks: [{ type: 'risk_notice', title: '操作预演 (Dry-Run)', content: '保留 72h' }],
          });
        }
        if (body.message === '查看 prod 事件') {
          return ok({
            type: 'answer',
            tool: 'event.query',
            answer: {
              events: [
                { id: 'evt-a1', tool_name: 'kafka.topic.retention.set', action: 'plan_confirmed', decision: 'denied', subject: 'admin-1', created_at: '2026-08-01T10:00:00Z' },
              ],
              count: 1,
            },
          });
        }
        if (body.message === '查看 prod 任务') {
          return ok({
            type: 'answer',
            tool: 'task.query',
            answer: {
              tasks: [
                { id: 'task-a1', name: 'minio 巡检', capability: 'minio.bucket.capacity.read', enabled: true, last_status: 'succeeded', next_run_at: '2026-08-03T00:00:00Z' },
              ],
              count: 1,
            },
          });
        }
        return ok({
          type: 'answer',
          tool: 'glusterfs.volume.health.read',
          answer: {
            summary: 'Volume data is healthy',
            severity: 'ok',
          },
        });
      }
      if (url === '/v1/capabilities/validate') {
        return ok({ validation: { valid: true } });
      }
      if (url === '/v1/capabilities/test') {
        return ok({
          result: {
            kind: 'observation',
            severity: 'info',
            summary: 'Bucket archive usage is 77%',
            data: { usage_pct: 77 },
            resource: { domain: 'minio', type: 'bucket', name: 'archive', environment: 'prod' },
          },
        });
      }
      if (url === '/v1/action-plans?status=pending_confirmation') {
        return ok({
          plans: [
            {
              id: 'plan-1',
              tool: 'kafka.topic.retention.set',
              environment: 'prod',
              risk: 'medium',
              status: 'pending_confirmation',
              version: 1,
              expires_at: '2026-07-25T12:00:00Z',
              created_by: 'operator-1',
              created_at: '2026-07-25T10:00:00Z',
            },
          ],
        });
      }
      if (url === '/v1/action-plans/plan-1') {
        return ok({
          id: 'plan-1',
          tool: 'kafka.topic.retention.set',
          environment: 'prod',
          risk: 'medium',
          status: 'pending_confirmation',
          version: 1,
          expires_at: '2026-07-25T12:00:00Z',
          created_by: 'operator-1',
          created_at: '2026-07-25T10:00:00Z',
          input: { environment: 'prod', cluster: 'k1', topic: 'orders', retention_hours: 72 },
        });
      }
      if (url === '/v1/audit-events' || url.startsWith('/v1/audit-events?')) {
        return ok({
          events: [
            {
              id: 'audit-1',
              plan_id: 'plan-1',
              subject: 'operator-1',
              tool_name: 'kafka.topic.retention.set',
              action: 'plan_created',
              decision: 'permitted',
              metadata: { source: 'assistant', risk: 'medium' },
              created_at: '2026-07-25T10:00:00Z',
            },
            {
              id: 'audit-2',
              plan_id: 'plan-1',
              subject: 'admin-1',
              tool_name: 'kafka.topic.retention.set',
              action: 'plan_confirmed',
              decision: 'permitted',
              metadata: { idempotency_key: 'plan:plan-1:hash' },
              created_at: '2026-07-25T10:05:00Z',
            },
          ],
          next_cursor: { created_at: '2026-07-25T10:05:00Z', id: 'audit-2' },
        });
      }
      if (url.startsWith('/v1/audit-events/search')) {
        return ok({
          events: [
            {
              id: 'audit-search-1',
              plan_id: 'plan-2',
              subject: 'admin-1',
              tool_name: 'kafka.topic.retention.set',
              action: 'plan_confirmed',
              decision: 'denied',
              metadata: { source: 'search' },
              created_at: '2026-07-25T11:00:00Z',
            },
          ],
          next_cursor: null,
        });
      }
      return ok({});
    }));
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  test('opens on the AI assistant entry and keeps capability management behind navigation', async () => {
    const wrapper = mountApp();
    await flushPromises();

    expect(wrapper.find('[data-test="assistant-entry"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="assistant-entry"]').text()).toContain('AI 运维助手');
    // management-entry uses v-show (keeps DOM but hides via display:none),
    // so we assert the inline display style instead of existence.
    const managementEl = wrapper.find('[data-test="management-entry"]').element as HTMLElement;
    expect(managementEl.style.display).toBe('none');

    await openManagement(wrapper);

    expect(wrapper.find('[data-test="assistant-entry"]').exists()).toBe(false);
    expect(managementEl.style.display).not.toBe('none');
    expect(wrapper.find('[data-test="management-entry"]').text()).toContain('把后台 API 翻译成 AI 工具');
    // 落地落在能力清单（review）而非导入向导
    expect(wrapper.find('[data-test="workflow-review"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="capability-table-body"]').text()).toContain('minio.bucket.capacity.read');
  });

  test('renders quick-start example prompts when transcript is empty', async () => {
    const wrapper = mountApp();
    await flushPromises();

    // 空对话时应展示可点击的示例提问
    expect(wrapper.find('[data-test="assistant-suggestions"]').exists()).toBe(true);
    const suggestions = wrapper.findAll('[data-test="assistant-suggestion-item"]');
    expect(suggestions.length).toBeGreaterThan(0);

    // 点击示例后填入输入框（取 .suggestion-text 文本，排除装饰图标 ›）
    const firstSuggestion = suggestions[0];
    const promptText = firstSuggestion.find('.suggestion-text').text();
    await firstSuggestion.trigger('click');
    expect((wrapper.find('[data-test="assistant-input"]').element as HTMLTextAreaElement).value).toBe(promptText);
  });

  test('hides quick-start example prompts after a message is sent', async () => {
    const wrapper = mountApp();
    await flushPromises();

    expect(wrapper.find('[data-test="assistant-suggestions"]').exists()).toBe(true);

    await wrapper.find('[data-test="assistant-input"]').setValue('检查 prod glusterfs data volume 状态');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-test="assistant-suggestions"]').exists()).toBe(false);
  });

  test('shows published capability count on the assistant entry', async () => {
    const wrapper = mountApp();
    await flushPromises();

    // 默认 mock 数据包含 1 个已发布能力 (glusterfs.volume.health.read)
    expect(wrapper.find('[data-test="assistant-capability-status"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="assistant-capability-status"]').text()).toContain('1');
    expect(wrapper.find('[data-test="assistant-capability-status"]').text()).toContain('AI 工具可用');
  });

  test('shows guidance to publish capabilities when none are published', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      return ok({});
    }));

    const wrapper = mountApp();
    await flushPromises();

    expect(wrapper.find('[data-test="assistant-capability-empty"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="assistant-capability-empty"]').text()).toContain('没有可用的 AI 工具');

    await wrapper.find('[data-test="assistant-capability-empty-action"]').trigger('click');
    expect(wrapper.find('[data-test="management-entry"]').element).toBeDefined();
  });

  test('shows capability guidance card in transcript area when no capabilities and no turns', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      return ok({});
    }));

    const wrapper = mountApp();
    await flushPromises();

    // 对话区显示未发布能力引导卡片
    expect(wrapper.find('[data-test="assistant-capability-guidance"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="assistant-capability-guidance"]').text()).toContain('没有可用的 AI 工具');
    // 不显示示例引导（因为点了也没用）
    expect(wrapper.find('[data-test="assistant-suggestions"]').exists()).toBe(false);
  });

  test('Enter key sends message, Shift+Enter inserts newline', async () => {
    const wrapper = mountApp();
    await flushPromises();

    const textarea = wrapper.find('[data-test="assistant-input"]');
    await textarea.setValue('测试消息');

    // Enter without Shift triggers send
    await textarea.trigger('keydown', { key: 'Enter', shiftKey: false });
    await flushPromises();

    const assistantCall = vi.mocked(fetch).mock.calls.find(([input]) => String(input) === '/v1/assistant/messages');
    expect(assistantCall).toBeDefined();
    expect(JSON.parse(String(assistantCall?.[1]?.body))).toEqual({ message: '测试消息', environment: 'prod' });
  });

  // 缺口-3：management 选中 capability 后「问 AI」跳转，发送时携带 page_context
  test('问 AI 跳转携带 pageContext，发送时带 page_context 而非 legacy environment', async () => {
    const wrapper = mountApp();
    await flushPromises();
    await openManagement(wrapper);

    // 直接对 ManagementView emit ask-ai，绕过 capability 选中流程
    const mgmt = wrapper.findComponent(ManagementView);
    expect(mgmt.exists()).toBe(true);
    await mgmt.vm.$emit('ask-ai', { domain: 'minio', resource_type: 'bucket' });
    await flushPromises();

    // 切到 assistant 视图，上下文 badge 显示
    expect(wrapper.find('[data-test="assistant-page-context-badge"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="assistant-page-context-badge"]').text()).toContain('minio');
    expect(wrapper.find('[data-test="assistant-page-context-badge"]').text()).toContain('bucket');

    // 输入消息发送
    await wrapper.find('[data-test="assistant-input"]').setValue('这个健康吗');
    await wrapper.find('[data-test="assistant-input"]').trigger('keydown', { key: 'Enter', shiftKey: false });
    await flushPromises();

    const call = vi.mocked(fetch).mock.calls.find(([u]) => String(u) === '/v1/assistant/messages');
    expect(call).toBeDefined();
    const body = JSON.parse(String(call?.[1]?.body));
    expect(body.page_context).toEqual({
      domain: 'minio',
      resource_type: 'bucket',
      resource_name: undefined,
      environment: 'prod',
    });
    // page_context 非空时不发 legacy environment 字段
    expect(body.environment).toBeUndefined();
  });

  // 缺口-3：清除上下文 badge 后，发送回到 legacy environment 行为
  test('清除页面上下文后发送回到 legacy environment', async () => {
    const wrapper = mountApp();
    await flushPromises();
    await openManagement(wrapper);

    const mgmt = wrapper.findComponent(ManagementView);
    await mgmt.vm.$emit('ask-ai', { domain: 'minio', resource_type: 'bucket' });
    await flushPromises();
    expect(wrapper.find('[data-test="assistant-page-context-badge"]').exists()).toBe(true);

    // 点 × 清除
    await wrapper.find('[data-test="assistant-page-context-clear"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-test="assistant-page-context-badge"]').exists()).toBe(false);

    // 发送消息，应回到 legacy environment
    await wrapper.find('[data-test="assistant-input"]').setValue('检查集群状态');
    await wrapper.find('[data-test="assistant-input"]').trigger('keydown', { key: 'Enter', shiftKey: false });
    await flushPromises();

    const call = vi.mocked(fetch).mock.calls.find(([u]) => String(u) === '/v1/assistant/messages');
    expect(call).toBeDefined();
    const body = JSON.parse(String(call?.[1]?.body));
    expect(body.environment).toBe('prod');
    expect(body.page_context).toBeUndefined();
  });

  test('groups nav items into operations and admin sections', async () => {
    const wrapper = mountApp();
    await flushPromises();

    const sections = wrapper.findAll('[data-test="nav-section"]');
    expect(sections.length).toBe(2);
    expect(sections[0].find('[data-test="nav-section-label"]').text()).toBe('运维');
    expect(sections[1].find('[data-test="nav-section-label"]').text()).toBe('管理配置');
    // 运维组包含 4 个 nav-item
    expect(sections[0].findAll('.nav-item').length).toBe(4);
    // 管理配置组包含 5 个 nav-item（audit/prompts/knowledge/feedback/mcp-servers）
    expect(sections[1].findAll('.nav-item').length).toBe(5);
  });

  test('switches views via Cmd+number shortcut', async () => {
    const wrapper = mountApp();
    await flushPromises();

    // Cmd+2 切换到能力接入管理（assistant-entry 用 v-if，切换后应不存在）
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '2', metaKey: true }));
    await flushPromises();
    expect(wrapper.find('[data-test="assistant-entry"]').exists()).toBe(false);

    // Cmd+1 切换回助手
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '1', metaKey: true }));
    await flushPromises();
    expect(wrapper.find('[data-test="assistant-entry"]').exists()).toBe(true);
  });

  test('ignores shortcut when typing in input/textarea', async () => {
    const wrapper = mount(App, {
      global: { plugins: [ElementPlus] },
      attachTo: document.body,
    });
    await flushPromises();

    const textarea = wrapper.find('[data-test="assistant-input"]');
    (textarea.element as HTMLTextAreaElement).focus();
    expect(document.activeElement).toBe(textarea.element);

    // 在 textarea focus 时按 Cmd+2 不应切换视图（assistant-entry 应仍存在）
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '2', metaKey: true }));
    await flushPromises();
    expect(wrapper.find('[data-test="assistant-entry"]').exists()).toBe(true);

    wrapper.unmount();
  });

  test('collapses sidebar when toggle button is clicked', async () => {
    const wrapper = mountApp();
    await flushPromises();

    const toggle = wrapper.find('[data-test="nav-collapse-toggle"]');
    expect(toggle.exists()).toBe(true);
    expect(wrapper.find('.app-shell').classes()).not.toContain('app-shell--collapsed');

    await toggle.trigger('click');
    expect(wrapper.find('.app-shell').classes()).toContain('app-shell--collapsed');

    await toggle.trigger('click');
    expect(wrapper.find('.app-shell').classes()).not.toContain('app-shell--collapsed');
  });

  test('Shift+Enter inserts newline without sending', async () => {
    const wrapper = mountApp();
    await flushPromises();

    const textarea = wrapper.find('[data-test="assistant-input"]');
    await textarea.setValue('测试');

    // Shift+Enter should NOT send
    await textarea.trigger('keydown', { key: 'Enter', shiftKey: true });
    await flushPromises();

    const assistantCall = vi.mocked(fetch).mock.calls.find(([input]) => String(input) === '/v1/assistant/messages');
    expect(assistantCall).toBeUndefined();
  });

  test('shows stop button while loading and aborts on click', async () => {
    const { promise, resolve } = deferred<unknown>();
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/v1/capabilities') return ok({ capabilities: [] });
      if (url === '/v1/assistant/messages') {
        await promise;
        return ok({ type: 'answer', tool: 'test', answer: {} });
      }
      return ok({});
    }));

    const wrapper = mountApp();
    await flushPromises();

    await wrapper.find('[data-test="assistant-input"]').setValue('测试');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    // loading 时展示停止按钮
    expect(wrapper.find('[data-test="assistant-stop"]').exists()).toBe(true);

    await wrapper.find('[data-test="assistant-stop"]').trigger('click');
    await flushPromises();

    // 点击停止后恢复发送按钮
    expect(wrapper.find('[data-test="assistant-stop"]').exists()).toBe(false);
    expect(wrapper.find('[data-test="assistant-send"]').exists()).toBe(true);

    resolve(undefined);
    await flushPromises();
  });

  test('shows keyboard shortcut hint below input', async () => {
    const wrapper = mountApp();
    await flushPromises();

    expect(wrapper.find('[data-test="assistant-input-hint"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="assistant-input-hint"]').text()).toContain('Enter');
    expect(wrapper.find('[data-test="assistant-input-hint"]').text()).toContain('Shift+Enter');
  });

  test('renders environment selector with default prod option', async () => {
    const wrapper = mountApp();
    await flushPromises();

    expect(wrapper.find('[data-test="assistant-env-selector"]').exists()).toBe(true);
    expect((wrapper.find('[data-test="assistant-env-selector"]').element as HTMLSelectElement).value).toBe('prod');
  });

  test('includes selected environment as metadata field when sending', async () => {
    const wrapper = mountApp();
    await flushPromises();

    // 选择 staging 环境
    await wrapper.find('[data-test="assistant-env-selector"]').setValue('staging');
    await wrapper.find('[data-test="assistant-input"]').setValue('检查 minio bucket 容量');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    const assistantCall = vi.mocked(fetch).mock.calls.find(([input]) => String(input) === '/v1/assistant/messages');
    expect(assistantCall).toBeDefined();
    const body = JSON.parse(String(assistantCall?.[1]?.body));
    // 消息原文不变
    expect(body.message).toBe('检查 minio bucket 容量');
    // 环境作为独立字段
    expect(body.environment).toBe('staging');
  });

  test('omits environment field when set to none', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await wrapper.find('[data-test="assistant-env-selector"]').setValue('none');
    await wrapper.find('[data-test="assistant-input"]').setValue('检查 minio bucket 容量');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    const assistantCall = vi.mocked(fetch).mock.calls.find(([input]) => String(input) === '/v1/assistant/messages');
    expect(assistantCall).toBeDefined();
    const body = JSON.parse(String(assistantCall?.[1]?.body));
    expect(body.message).toBe('检查 minio bucket 容量');
    expect(body.environment).toBeUndefined();
  });

  test('lays out capability management as a guided workflow', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await openManagement(wrapper);

    expect(wrapper.find('[data-test="workflow-console"]').exists()).toBe(true);
    // 落地默认落在能力清单（评审发布）
    expect(wrapper.find('[data-test="workflow-step-review"]').text()).toContain('评审发布');
    expect(wrapper.find('[data-test="workflow-step-ai"]').text()).toContain('AI 试问');
    expect(wrapper.find('[data-test="workflow-step-source"]').text()).toContain('接入 API');
  });

  test('starts capability management as a guided workflow instead of an always-on three-column console', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await openManagement(wrapper);

    expect(wrapper.find('[data-test="workflow-console"]').exists()).toBe(true);
    // 落地默认落在能力清单（评审发布）而非导入向导；评审编辑区随清单出现，但 AI 试问不显示
    expect(wrapper.find('[data-test="workflow-step-review"]').classes()).toContain('active');
    expect(wrapper.find('[data-test="workflow-review"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="workflow-start"]').exists()).toBe(false);
    expect(wrapper.find('[data-test="studio-ai-runner"]').exists()).toBe(false);

    // 点向导第 1 步进入打开 API 导入
    await wrapper.find('[data-test="workflow-step-source"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-test="workflow-step-source"]').classes()).toContain('active');
    expect(wrapper.find('[data-test="workflow-start"]').text()).toContain('先接入一批后台 API');

    await wrapper.find('[data-test="preview-openapi-url"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-test="workflow-step-candidates"]').classes()).toContain('active');
    expect(wrapper.find('[data-test="workflow-candidates"]').text()).toContain('选择要变成 AI 工具的 API');
    expect(wrapper.find('[data-test="workflow-candidates"]').text()).toContain('minio.bucket.capacity.read.imported');
  });

  test('sends a user question through the assistant entry and renders an answer', async () => {
    const fetchMock = vi.mocked(fetch);
    const wrapper = mountApp();
    await flushPromises();

    await wrapper.find('[data-test="assistant-input"]').setValue('检查 prod glusterfs data volume 状态');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    const assistantCall = fetchMock.mock.calls.find(([input]) => String(input) === '/v1/assistant/messages');
    expect(assistantCall).toBeDefined();
    expect(JSON.parse(String(assistantCall?.[1]?.body))).toEqual({
      message: '检查 prod glusterfs data volume 状态',
      environment: 'prod',
    });
    expect(wrapper.find('[data-test="assistant-transcript"]').text()).toContain('检查 prod glusterfs data volume 状态');
    expect(wrapper.find('[data-test="assistant-latest-detail"]').text()).toContain('glusterfs.volume.health.read');
    expect(wrapper.find('[data-test="assistant-latest-detail"]').text()).toContain('Volume data is healthy');
  });

  test('renders assistant clarification in the user entry', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await wrapper.find('[data-test="assistant-input"]').setValue('查询 prod glusterfs');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-test="assistant-latest-detail"]').text()).toContain('需要补充参数');
    expect(wrapper.find('[data-test="assistant-latest-detail"]').text()).toContain('缺少参数: cluster, name');
  });

  test('keeps assistant request failures in the transcript across retries', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      if (url === '/v1/assistant/messages') {
        const body = JSON.parse(String(_init?.body ?? '{}')) as { message?: string };
        if (body.message === '第一次失败') {
          return new Response(JSON.stringify({ error: 'assistant gateway timeout' }), {
            status: 503,
            headers: { 'Content-Type': 'application/json' },
          });
        }
        return ok({
          type: 'answer',
          tool: 'minio.bucket.capacity.read',
          answer: { summary: 'Bucket archive usage is 77%', severity: 'info' },
        });
      }
      return ok({});
    }));

    const wrapper = mountApp();
    await flushPromises();

    await wrapper.find('[data-test="assistant-input"]').setValue('第一次失败');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    // On failure the user message is preserved and the error surfaces both
    // in the latest-detail panel and as an error bubble in the transcript.
    expect(wrapper.find('[data-test="assistant-latest-detail"]').text()).toContain('assistant gateway timeout');
    expect(wrapper.find('[data-test="conversation-turn-error"]').exists()).toBe(true);

    await wrapper.find('[data-test="assistant-input"]').setValue('第二次成功');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-test="assistant-transcript"]').text()).toContain('第二次成功');
    expect(wrapper.find('[data-test="assistant-latest-detail"]').text()).toContain('Bucket archive usage is 77%');
  });

  test('preserves user message and shows error bubble when assistant request fails', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      if (url === '/v1/assistant/messages') {
        return new Response(JSON.stringify({ error: 'assistant gateway timeout' }), {
          status: 503,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      return ok({});
    }));

    const wrapper = mountApp();
    await flushPromises();

    await wrapper.find('[data-test="assistant-input"]').setValue('为什么会失败');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    // 新行为：用户消息保留在 transcript 中
    expect(wrapper.find('[data-test="assistant-transcript"]').text()).toContain('为什么会失败');
    // 错误气泡出现，带 error 标记和重试按钮
    expect(wrapper.find('[data-test="conversation-turn-error"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="conversation-turn-error"]').text()).toContain('assistant gateway timeout');
    expect(wrapper.find('[data-test="conversation-turn-retry"]').exists()).toBe(true);
  });

  test('renders confirmation-required responses with inline confirm UI', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      if (url === '/v1/assistant/messages') {
        return ok({
          type: 'confirmation_required',
          tool: 'kafka.topic.retention.set',
          plan_id: 'plan-inline-1',
          status: 'pending_confirmation',
          version: 1,
          expires_at: '2026-07-25T12:00:00Z',
          summary: '准备调整 kafka topic retention',
          confirmation_token: 'tok-1',
        });
      }
      if (url === '/v1/action-plans/plan-inline-1') {
        return ok({
          id: 'plan-inline-1',
          tool: 'kafka.topic.retention.set',
          environment: 'prod',
          risk: 'medium',
          status: 'pending_confirmation',
          version: 1,
          expires_at: '2026-07-25T12:00:00Z',
          created_by: 'operator-1',
          created_at: '2026-07-25T10:00:00Z',
          input: { environment: 'prod', cluster: 'k1', topic: 'orders', retention_hours: 72 },
        });
      }
      return ok({});
    }));

    const wrapper = mountApp();
    await flushPromises();

    await wrapper.find('[data-test="assistant-input"]').setValue('把 orders retention 调成 7 天');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-test="assistant-detail-status"]').text()).toBe('需要审批');
    // Inline confirm UI should appear within the assistant entry.
    const inline = wrapper.find('[data-test="assistant-inline-confirm"]');
    expect(inline.exists()).toBe(true);
    expect(inline.text()).toContain('kafka.topic.retention.set');
    expect(inline.text()).toContain('retention_hours');
    expect(inline.find('[data-test="confirm-plan"]').exists()).toBe(true);
    expect(inline.find('[data-test="confirm-plan"]').attributes('disabled')).toBeUndefined();
  });

  // 借鉴-5：Runbook 自动执行 → execution_result 渲染 ExecutionResultView + risk_notice block
  test('runbook auto-execution renders ExecutionResultView and risk_notice block', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await wrapper.find('[data-test="assistant-input"]').setValue('把 orders retention 调成 72 小时');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    // ExecutionResultView 显示 plan_id + runbook
    const execView = wrapper.find('[data-test="execution-result"]');
    expect(execView.exists()).toBe(true);
    expect(execView.text()).toContain('plan-rb-1');
    expect(wrapper.find('[data-test="execution-runbook"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="execution-runbook"]').text()).toContain('retention-tweak');
    // risk_notice block 渲染（assistantBlocks 现支持 execution_result）
    expect(wrapper.find('[data-test="block-risk_notice"]').exists()).toBe(true);
    // 状态徽章为执行结果
    expect(wrapper.find('[data-test="assistant-detail-status"]').text()).toBe('执行结果');
  });

  // §4 工具生态：event.query answer 结构化渲染
  test('event.query answer renders structured event table', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await wrapper.find('[data-test="assistant-input"]').setValue('查看 prod 事件');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-test="tool-answer-view"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="tool-answer-count"]').text()).toContain('1');
    const rows = wrapper.findAll('[data-test="tool-answer-row"]');
    expect(rows.length).toBe(1);
    expect(rows[0].text()).toContain('evt-a1');
    expect(rows[0].text()).toContain('plan_confirmed');
  });

  // §4 工具生态：task.query answer 结构化渲染
  test('task.query answer renders structured task table', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await wrapper.find('[data-test="assistant-input"]').setValue('查看 prod 任务');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-test="tool-answer-view"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="tool-answer-table"]').exists()).toBe(true);
    const rows = wrapper.findAll('[data-test="tool-answer-row"]');
    expect(rows.length).toBe(1);
    expect(rows[0].text()).toContain('task-a1');
    expect(rows[0].text()).toContain('minio 巡检');
  });

  test('falls back when confirmation-required response lacks confirmation_token', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      if (url === '/v1/assistant/messages') {
        return ok({
          type: 'confirmation_required',
          tool: 'kafka.topic.retention.set',
          plan_id: 'plan-inline-2',
          status: 'pending_confirmation',
          version: 1,
          expires_at: '2026-07-25T12:00:00Z',
          summary: '需要外部审批',
        });
      }
      if (url === '/v1/action-plans/plan-inline-2') {
        return ok({
          id: 'plan-inline-2',
          tool: 'kafka.topic.retention.set',
          environment: 'prod',
          risk: 'medium',
          status: 'pending_confirmation',
          version: 1,
          expires_at: '2026-07-25T12:00:00Z',
          created_by: 'operator-1',
          created_at: '2026-07-25T10:00:00Z',
          input: { topic: 'orders' },
        });
      }
      return ok({});
    }));

    const wrapper = mountApp();
    await flushPromises();

    await wrapper.find('[data-test="assistant-input"]').setValue('把 orders retention 调成 7 天');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    // Confirm button should be present but disabled without token.
    const inline = wrapper.find('[data-test="assistant-inline-confirm"]');
    expect(inline.exists()).toBe(true);
    expect(inline.find('[data-test="confirm-plan"]').attributes('disabled')).toBeDefined();
  });

  test('falls back to compact JSON for malformed assistant variants', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      if (String(input) === '/v1/assistant/messages') {
        return ok({ type: 'answer', tool: 'minio.bucket.capacity.read' });
      }
      return ok({});
    }));

    const wrapper = mountApp();
    await flushPromises();

    await wrapper.find('[data-test="assistant-input"]').setValue('查询 minio');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    // assistantDetailText renders pretty JSON (JSON.stringify with 2-space
    // indentation), so the detail panel should contain the type and tool.
    expect(wrapper.find('[data-test="assistant-latest-detail"]').text()).toContain('"type": "answer"');
    expect(wrapper.find('[data-test="assistant-latest-detail"]').text()).toContain('minio.bucket.capacity.read');
  });

  test('clears stale latest-call details when a later assistant request fails', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      if (url === '/v1/assistant/messages') {
        const body = JSON.parse(String(_init?.body ?? '{}')) as { message?: string };
        if (body.message === '后一次失败') {
          return new Response(JSON.stringify({ error: 'assistant gateway timeout' }), {
            status: 503,
            headers: { 'Content-Type': 'application/json' },
          });
        }
        return ok({
          type: 'answer',
          tool: 'minio.bucket.capacity.read',
          answer: { summary: 'Bucket archive usage is 77%', severity: 'info' },
        });
      }
      return ok({});
    }));

    const wrapper = mountApp();
    await flushPromises();

    await wrapper.find('[data-test="assistant-input"]').setValue('先成功');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-test="assistant-latest-detail"]').text()).toContain('minio.bucket.capacity.read');

    await wrapper.find('[data-test="assistant-input"]').setValue('后一次失败');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-test="assistant-detail-status"]').text()).toBe('请求失败');
    expect(wrapper.find('[data-test="assistant-latest-detail"]').text()).toContain('assistant gateway timeout');
    expect(wrapper.find('[data-test="assistant-latest-detail"]').text()).not.toContain('minio.bucket.capacity.read');
  });

  test('falls back to compact JSON when the assistant returns null', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      if (String(input) === '/v1/assistant/messages') {
        return ok(null);
      }
      return ok({});
    }));

    const wrapper = mountApp();
    await flushPromises();

    await wrapper.find('[data-test="assistant-input"]').setValue('查询未知响应');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    // The transcript now reflects conversationTurns (optimistic user turn),
    // so it contains the user's message rather than the null response. The
    // null fallback lives in the detail panel and status.
    expect(wrapper.find('[data-test="assistant-transcript"]').text()).toContain('查询未知响应');
    expect(wrapper.find('[data-test="assistant-detail-status"]').text()).toBe('响应详情');
    expect(wrapper.find('[data-test="assistant-latest-detail"]').text()).toBe('null');
  });

  test('collapses the assistant workspace before small laptop widths', () => {
    const styles = readFileSync('src/styles.css', 'utf8');

    expect(styles).toMatch(/@media \(max-width: 768px\)[\s\S]*\.assistant-workspace[\s\S]*grid-template-columns: 1fr;/);
    expect(styles).toMatch(/\* \{[\s\S]*box-sizing: border-box;/);
  });

  test('keeps the management workbench readable on laptop widths', () => {
    const styles = readFileSync('src/styles.css', 'utf8');

    expect(styles).toMatch(/\.workflow-review \{[\s\S]*grid-template-columns: var\(--left-panel-width\) 1fr;/);
    expect(styles).toMatch(/@media \(max-width: 768px\)[\s\S]*\.workflow-review,[\s\S]*grid-template-columns: 1fr;/);
  });

  test('styles capability management as a guided workflow workbench', () => {
    const styles = readFileSync('src/styles.css', 'utf8');

    expect(styles).toContain('/* ============================================\n   Workflow Stepper\n   ============================================ */');
    expect(styles).toMatch(/\.workflow-step\.active \{[\s\S]*background: var\(--color-accent-soft\);/);
    expect(styles).toMatch(/\.import-wizard \{[\s\S]*background: var\(--color-bg-elevated\);/);
    expect(styles).toMatch(/\.workflow-stage \{[\s\S]*overflow: hidden;/);
  });

  test('does not use the retired API orchestration canvas as the management shell', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await openManagement(wrapper);

    expect(wrapper.find('[data-test="api-orchestration-canvas"]').exists()).toBe(false);
    expect(wrapper.find('[data-test="workflow-console"]').exists()).toBe(true);
  });

  test('rebuilds capability management as a step-by-step workflow console', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await openManagement(wrapper);

    expect(wrapper.find('[data-test="workflow-console"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="workflow-step-source"]').text()).toContain('接入 API');
    expect(wrapper.find('[data-test="workflow-step-candidates"]').text()).toContain('选择能力');
    expect(wrapper.find('[data-test="workflow-step-review"]').text()).toContain('评审发布');
    expect(wrapper.find('[data-test="workflow-step-ai"]').text()).toContain('AI 试问');
    expect(wrapper.find('[data-test="api-orchestration-canvas"]').exists()).toBe(false);
    expect(wrapper.find('[data-test="ops-workbench"]').exists()).toBe(false);
  });

  test('renders capability inventory and enables publishing for writes', async () => {
    const wrapper = mountApp();
    await flushPromises();
    await openReview(wrapper);

    expect(wrapper.text()).toContain('把后台 API 翻译成 AI 工具');
    expect(wrapper.text()).toContain('从 Swagger 收件箱选择接口，补齐参数和摘要，然后直接在右侧用自然语言试运行。');
    expect(wrapper.find('[data-test="workflow-review"]').text()).toContain('待处理能力');
    expect(wrapper.find('[data-test="workflow-review"]').text()).toContain('评审发布');
    expect(wrapper.text()).toContain('minio.bucket.capacity.read');
    expect(wrapper.text()).toContain('kafka.topic.retention.set');
    expect(wrapper.find('[data-test="next-minio.bucket.capacity.read"]').text()).toContain('发布给 AI');
    expect(wrapper.find('[data-test="next-kafka.topic.retention.set"]').text()).toContain('发布给 AI');
    expect(wrapper.find('[data-test="next-glusterfs.volume.health.read"]').text()).toContain('用 AI 试问一次');
    expect(wrapper.find('[data-test="publish-kafka.topic.retention.set"]').text()).toContain('发布');
    expect(wrapper.find('[data-test="publish-kafka.topic.retention.set"]').attributes('disabled')).toBeUndefined();
  });

  test('shows operational summary and filters inventory', async () => {
    const wrapper = mountApp();
    await flushPromises();
    await openManagement(wrapper);

    expect(wrapper.find('[data-test="stat-published"]').text()).toContain('1');
    expect(wrapper.find('[data-test="stat-review"]').text()).toContain('2');
    expect(wrapper.find('[data-test="stat-invalid"]').text()).toContain('1');
    expect(wrapper.find('[data-test="stat-publishable"]').text()).toContain('2');

    await wrapper.find('[data-test="workflow-step-review"]').trigger('click');
    await flushPromises();
    await wrapper.find('[data-test="capability-search"]').setValue('kafka');
    expect(wrapper.find('[data-test="capability-table-body"]').text()).toContain('kafka.topic.retention.set');
    expect(wrapper.find('[data-test="capability-table-body"]').text()).not.toContain('minio.bucket.capacity.read');

    await wrapper.find('[data-test="capability-search"]').setValue('');
    await wrapper.find('[data-test="status-filter"]').setValue('published');
    expect(wrapper.find('[data-test="capability-table-body"]').text()).toContain('glusterfs.volume.health.read');
    expect(wrapper.find('[data-test="capability-table-body"]').text()).not.toContain('kafka.topic.retention.set');
  });

  test('uses local preview data when the API is not connected in development', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('<!doctype html>', {
      status: 200,
      headers: { 'Content-Type': 'text/html' },
    })));

    const wrapper = mountApp();
    await flushPromises();
    await openReview(wrapper);

    expect(wrapper.text()).toContain('minio.bucket.capacity.read');
    expect(wrapper.find('[data-test="stat-publishable"]').text()).toContain('2');
  });

  test('previews Swagger candidates before generating selected drafts', async () => {
    const fetchMock = vi.mocked(fetch);
    const wrapper = mountApp();
    await flushPromises();
    await openSource(wrapper);

    expect(wrapper.find('[data-test="import-wizard"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="commit-openapi-import"]').attributes('disabled')).toBeDefined();

    await wrapper.find('[data-test="openapi-url-input"]').setValue('https://admin.example.com/v3/api-docs');
    await wrapper.find('[data-test="backend-base-url-input"]').setValue('https://middleware.example.com');
    await wrapper.find('[data-test="preview-openapi-url"]').trigger('click');
    await flushPromises();

    const previewCall = fetchMock.mock.calls.find(([input]) => String(input) === '/v1/capabilities/import/openapi-url/preview');
    expect(previewCall).toBeDefined();
    expect(JSON.parse(String(previewCall?.[1]?.body))).toEqual({
      openapi_url: 'https://admin.example.com/v3/api-docs',
      backend_base_url: 'https://middleware.example.com',
    });
    expect(wrapper.find('[data-test="import-preview"]').text()).toContain('推荐接入');
    expect(wrapper.find('[data-test="import-preview"]').text()).toContain('minio.bucket.capacity.read.imported');
    expect(wrapper.find('[data-test="import-preview"]').text()).toContain('kafka.topic.retention.update');
    expect(wrapper.find('[data-test="candidate-adjust-GET /api/minio/{cluster}/buckets/{bucket}/capacity"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="candidate-adjust-GET /api/minio/{cluster}/buckets/{bucket}/capacity"]').text()).toContain('调整字段');
    expect((wrapper.find('[data-test="candidate-selected-GET /api/minio/{cluster}/buckets/{bucket}/capacity"]').element as HTMLInputElement).checked).toBe(true);
    expect((wrapper.find('[data-test="candidate-selected-POST /api/kafka/{cluster}/topics/{topic}/retention"]').element as HTMLInputElement).checked).toBe(false);
    expect(wrapper.find('[data-test="workflow-review"]').exists()).toBe(false);

    await wrapper.find('[data-test="candidate-name-GET /api/minio/{cluster}/buckets/{bucket}/capacity"]').setValue('minio.bucket.capacity.read.custom');
    await wrapper.find('[data-test="commit-openapi-import"]').trigger('click');
    await flushPromises();

    const commitCall = fetchMock.mock.calls.find(([input]) => String(input) === '/v1/capabilities/import/openapi-url/commit');
    expect(commitCall).toBeDefined();
    expect(fetchMock.mock.calls.some(([input]) => String(input) === '/v1/capabilities/import/openapi-url')).toBe(false);
    expect(JSON.parse(String(commitCall?.[1]?.body))).toEqual({
      openapi_url: 'https://admin.example.com/v3/api-docs',
      backend_base_url: 'https://middleware.example.com',
      fingerprint: 'sha256:test',
      selections: [{
        candidate_id: 'GET /api/minio/{cluster}/buckets/{bucket}/capacity',
        overrides: {
          name: 'minio.bucket.capacity.read.custom',
          domain: 'minio',
          resource_type: 'bucket',
          operation: 'read',
          risk: 'low',
        },
      }],
    });
    expect(wrapper.find('[data-test="import-result"]').text()).toContain('已生成 1 个待评审草稿');
    expect(wrapper.find('[data-test="import-batch"]').text()).toContain('本次导入');
    expect(wrapper.find('[data-test="import-batch"]').text()).toContain('minio.bucket.capacity.read.imported');
    expect(wrapper.find('[data-test="import-batch-stat-total"]').text()).toContain('1');
    expect(wrapper.find('[data-test="import-batch-stat-selected"]').text()).toContain('1');
    expect(wrapper.find('[data-test="capability-table-body"]').text()).toContain('minio.bucket.capacity.read.imported');

    await wrapper.find('[data-test="ignore-import-minio.bucket.capacity.read.imported"]').setValue(true);
    expect(wrapper.find('[data-test="import-batch-stat-selected"]').text()).toContain('0');
    expect(wrapper.find('[data-test="import-batch-stat-ignored"]').text()).toContain('1');
    expect(wrapper.find('[data-test="capability-table-body"]').text()).not.toContain('minio.bucket.capacity.read.imported');

    await wrapper.find('[data-test="ignore-import-minio.bucket.capacity.read.imported"]').setValue(false);
    expect(wrapper.find('[data-test="capability-table-body"]').text()).toContain('minio.bucket.capacity.read.imported');
    await wrapper.find('[data-test="open-import-minio.bucket.capacity.read.imported"]').trigger('click');
    expect((wrapper.find('[data-test="capability-name"]').element as HTMLInputElement).value).toBe('minio.bucket.capacity.read.imported');
  });

  test('disables Swagger commit when no candidates are selected', async () => {
    const wrapper = mountApp();
    await flushPromises();
    await openSource(wrapper);

    await wrapper.find('[data-test="preview-openapi-url"]').trigger('click');
    await flushPromises();
    await wrapper.find('[data-test="candidate-selected-GET /api/minio/{cluster}/buckets/{bucket}/capacity"]').setValue(false);

    expect(wrapper.find('[data-test="commit-openapi-import"]').attributes('disabled')).toBeDefined();
    expect(wrapper.find('[data-test="import-commit-summary"]').text()).toContain('已选择 0 个候选 API');
  });

  test('clears Swagger preview when source fields change or preview retry fails', async () => {
    const fetchMock = vi.mocked(fetch);
    const wrapper = mountApp();
    await flushPromises();
    await openSource(wrapper);

    await wrapper.find('[data-test="preview-openapi-url"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-test="import-preview"]').exists()).toBe(true);

    await wrapper.find('[data-test="openapi-url-input"]').setValue('https://admin.example.com/changed-api-docs');
    expect(wrapper.find('[data-test="import-preview"]').exists()).toBe(false);
    expect(wrapper.find('[data-test="commit-openapi-import"]').attributes('disabled')).toBeDefined();

    fetchMock.mockImplementationOnce(async () =>
      new Response(JSON.stringify({ error: 'Swagger URL 不可访问' }), {
        status: 502,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    await wrapper.find('[data-test="preview-openapi-url"]').trigger('click');
    await flushPromises();

    expect(wrapper.text()).toContain('Swagger URL 不可访问');
    expect(wrapper.find('[data-test="import-preview"]').exists()).toBe(false);
    expect(wrapper.find('[data-test="commit-openapi-import"]').attributes('disabled')).toBeDefined();
  });

  test('ignores stale Swagger preview responses after source changes', async () => {
    const pendingPreview = deferred<Response>();
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      if (url === '/v1/capabilities/import/openapi-url/preview') {
        return pendingPreview.promise;
      }
      return ok({});
    }));
    const wrapper = mountApp();
    await flushPromises();
    await openSource(wrapper);

    await wrapper.find('[data-test="openapi-url-input"]').setValue('https://admin.example.com/v3/api-docs');
    await wrapper.find('[data-test="preview-openapi-url"]').trigger('click');
    await wrapper.find('[data-test="openapi-url-input"]').setValue('https://admin.example.com/changed-api-docs');
    pendingPreview.resolve(ok({
      source: {
        openapi_url: 'https://admin.example.com/v3/api-docs',
        backend_base_url: 'https://middleware.example.com',
        fingerprint: 'sha256:old',
      },
      stats: { total: 1, recommended: 1, needs_adjustment: 0, not_recommended: 0, read: 1, write: 0 },
      candidates: [{
        id: 'GET /api/minio/{cluster}/buckets/{bucket}/capacity',
        method: 'GET',
        path: '/api/minio/{cluster}/buckets/{bucket}/capacity',
        capability: {
          schema_version: 1,
          name: 'minio.bucket.capacity.read.imported',
          status: 'needs_review',
          domain: 'minio',
          resource_type: 'bucket',
          operation: 'read',
          risk: 'low',
          backend: { adapter: 'http', method: 'GET', path: '/api/minio/{cluster}/buckets/{bucket}/capacity', timeout_ms: 3000 },
          input_schema: {},
          output: { kind: 'observation', severity_path: '', summary_template: '', fields: {} },
          auth: { roles: [], environment_scoped: true },
          ai: { description: '', examples: [] },
        },
        recommendation: 'recommended',
        reasons: [],
        warnings: [],
      }],
    }));
    await flushPromises();

    expect(wrapper.find('[data-test="import-preview"]').exists()).toBe(false);
    expect(wrapper.find('[data-test="commit-openapi-import"]').attributes('disabled')).toBeDefined();
    expect(wrapper.find('[data-test="workflow-review"]').exists()).toBe(false);
  });

  test('updates commit summary when a selected candidate operation is overridden', async () => {
    const wrapper = mountApp();
    await flushPromises();
    await openSource(wrapper);

    await wrapper.find('[data-test="preview-openapi-url"]').trigger('click');
    await flushPromises();
    await wrapper.find('[data-test="candidate-operation-GET /api/minio/{cluster}/buckets/{bucket}/capacity"]').setValue('write');

    const summary = wrapper.find('[data-test="import-commit-summary"]').text();
    expect(summary).toContain('读取 0 个');
    expect(summary).toContain('写入 1 个');
  });

  test('keeps generated drafts available to the existing import review batch', async () => {
    const wrapper = mountApp();
    await flushPromises();
    await openSource(wrapper);

    await wrapper.find('[data-test="preview-openapi-url"]').trigger('click');
    await flushPromises();
    await wrapper.find('[data-test="commit-openapi-import"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-test="import-batch"]').text()).toContain('minio.bucket.capacity.read.imported');
    await wrapper.find('[data-test="open-import-minio.bucket.capacity.read.imported"]').trigger('click');
    expect((wrapper.find('[data-test="capability-name"]').element as HTMLInputElement).value).toBe('minio.bucket.capacity.read.imported');
  });

  test('filters Swagger preview candidates by domain', async () => {
    const wrapper = mountApp();
    await flushPromises();
    await openSource(wrapper);

    await wrapper.find('[data-test="preview-openapi-url"]').trigger('click');
    await flushPromises();
    await wrapper.find('[data-test="candidate-domain-filter"]').setValue('kafka');

    const candidates = wrapper.find('[data-test="import-preview"]').text();
    expect(candidates).toContain('kafka.topic.retention.update');
    expect(candidates).not.toContain('minio.bucket.capacity.read.imported');
  });

  test('derives path variables while editing a draft', async () => {
    const wrapper = mountApp();
    await flushPromises();
    await openManagement(wrapper);

    await wrapper.find('[data-test="new-draft"]').trigger('click');
    await wrapper.find('[data-test="backend-path"]').setValue('/api/minio/{cluster}/buckets/{bucket}/capacity');

    expect(wrapper.text()).toContain('识别结果');
    expect(wrapper.text()).toContain('后端接口');
    expect(wrapper.text()).toContain('输入参数');
    expect(wrapper.text()).toContain('AI 摘要字段');
    expect(wrapper.text()).toContain('权限与治理');
    expect(wrapper.find('[data-test="path-variables"]').text()).toContain('cluster');
    expect(wrapper.find('[data-test="path-variables"]').text()).toContain('bucket');
  });

  test('edits input parameters and output mappings before saving a draft', async () => {
    const fetchMock = vi.mocked(fetch);
    const wrapper = mountApp();
    await flushPromises();
    await openManagement(wrapper);

    await wrapper.find('[data-test="new-draft"]').trigger('click');
    await wrapper.find('[data-test="capability-name"]').setValue('minio.bucket.capacity.read');
    await wrapper.find('[data-test="add-input-field"]').trigger('click');
    await wrapper.find('[data-test="input-name-1"]').setValue('bucket');
    await wrapper.find('[data-test="input-type-1"]').setValue('string');
    await wrapper.find('[data-test="input-required-1"]').setValue(true);

    await wrapper.find('[data-test="add-output-field"]').trigger('click');
    await wrapper.find('[data-test="output-name-0"]').setValue('usage_pct');
    await wrapper.find('[data-test="output-path-0"]').setValue('$.data.usage_pct');
    await wrapper.find('[data-test="summary-template"]').setValue('Bucket {bucket} usage is {usage_pct}%');

    await wrapper.find('[data-test="save-draft"]').trigger('click');
    await flushPromises();

    const draftCall = fetchMock.mock.calls.find(([input]) => String(input) === '/v1/capabilities/drafts');
    expect(draftCall).toBeDefined();
    const body = JSON.parse(String(draftCall?.[1]?.body));
    expect(body.input_schema.bucket).toEqual({ type: 'string', required: true });
    expect(body.output.fields.usage_pct).toBe('$.data.usage_pct');
    expect(body.output.summary_template).toBe('Bucket {bucket} usage is {usage_pct}%');
  });

  test('shows publish readiness checklist for safe and unsafe capabilities', async () => {
    const wrapper = mountApp();
    await flushPromises();
    await openReview(wrapper);

    await wrapper.find('[data-test="edit-minio.bucket.capacity.read"]').trigger('click');
    const safeChecklist = wrapper.find('[data-test="publish-checklist"]').text();
    expect(safeChecklist).toContain('目标文件');
    expect(safeChecklist).toContain('可以发布');
    expect(safeChecklist).toContain('目标文件');
    expect(safeChecklist).toContain('capabilities/published/minio.bucket.capacity.read.yaml');
    expect(safeChecklist).toContain('读取类能力');
    expect(safeChecklist).toContain('GET 请求');
    expect(safeChecklist).toContain('校验通过');

    await wrapper.find('[data-test="edit-kafka.topic.retention.set"]').trigger('click');
    const unsafeChecklist = wrapper.find('[data-test="publish-checklist"]').text();
    expect(unsafeChecklist).toContain('写入类能力');
    expect(unsafeChecklist).toContain('POST 请求');
    expect(unsafeChecklist).toContain('需执行计划');
    expect(unsafeChecklist).toContain('需审批');
    expect(unsafeChecklist).toContain('预检能力');
    expect(unsafeChecklist).toContain('回滚策略');
  });

  test('renders governance summary per capability operation', async () => {
    const wrapper = mountApp();
    await flushPromises();
    await openReview(wrapper);

    await wrapper.find('[data-test="edit-minio.bucket.capacity.read"]').trigger('click');
    expect(wrapper.find('[data-test="governance-summary"]').text()).toContain('读取能力');

    await wrapper.find('[data-test="edit-kafka.topic.retention.set"]').trigger('click');
    expect(wrapper.find('[data-test="governance-summary"]').text()).toContain('写入能力');
    expect(wrapper.find('[data-test="governance-summary"]').text()).toContain('需执行计划');
  });

  test('explains why published capabilities cannot be published again', async () => {
    const wrapper = mountApp();
    await flushPromises();
    await openReview(wrapper);

    const publishedRowAction = wrapper.find('[data-test="publish-glusterfs.volume.health.read"]');
    expect(publishedRowAction.text()).toContain('已发布');
    expect(publishedRowAction.attributes('disabled')).toBeDefined();

    await wrapper.find('[data-test="edit-glusterfs.volume.health.read"]').trigger('click');
    expect(wrapper.find('[data-test="publish-current"]').text()).toContain('已发布，无需重复发布');
    expect(wrapper.find('[data-test="publish-current"]').attributes('disabled')).toBeDefined();
  });

  test('publishes a draft when the publish endpoint returns an empty body', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({
          capabilities: [
            {
              name: 'minio.bucket.capacity.read',
              status: 'needs_review',
              source: 'discovered',
              domain: 'minio',
              resource_type: 'bucket',
              operation: 'read',
              risk: 'low',
              backend: { method: 'GET', base_url: 'https://middleware.example.com', path: '/api/minio/{cluster}/buckets/{bucket}/capacity' },
              validation: { valid: true },
            },
          ],
        });
      }
      if (url === '/v1/capabilities/minio.bucket.capacity.read/publish') {
        return new Response(null, {
          status: 204,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      return ok({});
    }));
    const wrapper = mountApp();
    await flushPromises();
    await openReview(wrapper);

    await wrapper.find('[data-test="publish-minio.bucket.capacity.read"]').trigger('click');
    await flushPromises();

    expect(wrapper.text()).not.toContain('Unexpected end of JSON input');
    expect(wrapper.find('[data-test="workflow-ai"]').text()).toContain('minio.bucket.capacity.read');
    expect(wrapper.find('[data-test="workflow-step-ai"]').attributes('disabled')).toBeUndefined();
  });

  test('blocks publishing a draft but allows AI preflight when the same capability is already published', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
      if (String(input) === '/v1/capabilities') {
        return ok({
          capabilities: [
            {
              name: 'minio.bucket.capacity.read',
              status: 'needs_review',
              source: 'discovered',
              domain: 'minio',
              resource_type: 'bucket',
              operation: 'read',
              risk: 'low',
              backend: { method: 'GET', base_url: 'https://middleware.example.com', path: '/api/minio/{cluster}/buckets/{bucket}/capacity' },
              validation: { valid: true },
            },
            {
              name: 'minio.bucket.capacity.read',
              status: 'published',
              source: 'published',
              domain: 'minio',
              resource_type: 'bucket',
              operation: 'read',
              risk: 'low',
              backend: { method: 'GET', base_url: 'https://middleware.example.com', path: '/api/minio/{cluster}/buckets/{bucket}/capacity' },
              validation: { valid: true },
            },
          ],
        });
      }
      if (String(input) === '/v1/assistant/messages') {
        return ok({
          type: 'answer',
          tool: 'minio.bucket.capacity.read',
          answer: {
            summary: 'Bucket archive usage is 77%',
            severity: 'warning',
          },
        });
      }
      return ok({});
    }));
    const fetchMock = vi.mocked(fetch);
    const wrapper = mountApp();
    await flushPromises();
    await openReview(wrapper);

    const draftPublish = wrapper.find('[data-test="publish-minio.bucket.capacity.read"]');
    expect(draftPublish.text()).toContain('已有已发布版本');
    expect(draftPublish.attributes('disabled')).toBeDefined();

    await wrapper.find('[data-test="edit-minio.bucket.capacity.read"]').trigger('click');
    expect(wrapper.find('[data-test="publish-checklist"]').text()).toContain('同名发布');
    expect(wrapper.find('[data-test="publish-current"]').text()).toContain('已有已发布版本');
    expect(wrapper.find('[data-test="publish-current"]').attributes('disabled')).toBeDefined();
    await wrapper.find('[data-test="test-input"]').setValue('{"environment":"prod","cluster":"m1","bucket":"archive"}');
    expect(wrapper.find('[data-test="ai-preflight"]').text()).toContain('使用同名已发布版本');
    expect(wrapper.find('[data-test="run-ai-preflight"]').attributes('disabled')).toBeUndefined();

    await wrapper.find('[data-test="run-ai-preflight"]').trigger('click');
    await flushPromises();

    const assistantCall = fetchMock.mock.calls.find(([input]) => String(input) === '/v1/assistant/messages');
    expect(assistantCall).toBeDefined();
    expect(JSON.parse(String(assistantCall?.[1]?.body))).toEqual({
      message: '查询 prod m1 archive bucket 的 minio 容量',
    });
    expect(wrapper.find('[data-test="ai-preflight-state"]').text()).toContain('已返回答案');
  });

  test('prepares an AI preflight prompt and gates drafts from assistant calls', async () => {
    const wrapper = mountApp();
    await flushPromises();
    await openReview(wrapper);

    await wrapper.find('[data-test="edit-minio.bucket.capacity.read"]').trigger('click');
    await wrapper.find('[data-test="test-param-cluster"]').setValue('m1');
    await wrapper.find('[data-test="test-param-bucket"]').setValue('archive');

    const panel = wrapper.find('[data-test="ai-preflight"]');
    expect(panel.text()).toContain('用 AI 试问一次');
    expect(JSON.parse((wrapper.find('[data-test="test-input"]').element as HTMLTextAreaElement).value)).toEqual({
      environment: 'prod',
      cluster: 'm1',
      bucket: 'archive',
    });
    expect(wrapper.find('[data-test="ai-prompt"]').element).toHaveProperty('value', '查询 prod m1 archive bucket 的 minio 容量');
    expect(wrapper.find('[data-test="run-ai-preflight"]').attributes('disabled')).toBeDefined();
    expect(panel.text()).toContain('发布后可运行');
  });

  test('renders the quick publish form on the source step', async () => {
    const wrapper = mountApp();
    await flushPromises();
    await openSource(wrapper);

    expect(wrapper.find('[data-test="quick-publish-form"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="quick-publish-name"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="quick-publish-submit"]').attributes('disabled')).toBeDefined();
  });

  test('quick publishes a read capability and jumps to the AI step', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      if (url === '/v1/capabilities/quick-publish') {
        const body = JSON.parse(String(init?.body ?? '{}'));
        return ok({
          name: body.name,
          status: 'published',
          source: 'published',
          domain: body.domain,
          resource_type: body.resource_type,
          operation: 'read',
          risk: 'low',
          backend: { method: 'GET', base_url: body.backend_base_url, path: body.path, timeout_ms: 3000 },
          input_schema: {
            environment: { type: 'string', required: true },
            cluster: { type: 'string', required: true },
          },
          output: { kind: 'observation', summary_template: 'Read cluster', fields: {} },
          auth: { roles: ['viewer', 'operator', 'admin'], environment_scoped: true },
          ai: { description: body.description, examples: [] },
          validation: { valid: true },
        });
      }
      return ok({});
    });
    vi.stubGlobal('fetch', fetchMock);
    const wrapper = mountApp();
    await flushPromises();
    await openSource(wrapper);

    await wrapper.find('[data-test="quick-publish-name"]').setValue('redis.cluster.info.read');
    await wrapper.find('[data-test="quick-publish-domain"]').setValue('redis');
    await wrapper.find('[data-test="quick-publish-resource"]').setValue('cluster');
    await wrapper.find('[data-test="quick-publish-base-url"]').setValue('https://middleware.example.com');
    await wrapper.find('[data-test="quick-publish-path"]').setValue('/api/redis/clusters/{cluster}/info');
    await wrapper.find('[data-test="quick-publish-description"]').setValue('Read Redis cluster info');

    await wrapper.find('[data-test="quick-publish-submit"]').trigger('click');
    await flushPromises();

    const quickPublishCall = fetchMock.mock.calls.find(([input]) => String(input) === '/v1/capabilities/quick-publish');
    expect(quickPublishCall).toBeDefined();
    const sentBody = JSON.parse(String(quickPublishCall?.[1]?.body));
    expect(sentBody).toEqual({
      name: 'redis.cluster.info.read',
      domain: 'redis',
      resource_type: 'cluster',
      backend_base_url: 'https://middleware.example.com',
      method: 'GET',
      path: '/api/redis/clusters/{cluster}/info',
      description: 'Read Redis cluster info',
    });

    expect(wrapper.find('[data-test="workflow-ai"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('redis.cluster.info.read');
    expect(wrapper.find('[data-test="workflow-step-ai"]').attributes('disabled')).toBeUndefined();
  });

  test('shows an error when quick publish fails with a conflict', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      if (url === '/v1/capabilities/quick-publish') {
        return new Response(JSON.stringify({ error: 'capability name conflict' }), {
          status: 409,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      return ok({});
    }));
    const wrapper = mountApp();
    await flushPromises();
    await openSource(wrapper);

    await wrapper.find('[data-test="quick-publish-name"]').setValue('redis.cluster.info.read');
    await wrapper.find('[data-test="quick-publish-domain"]').setValue('redis');
    await wrapper.find('[data-test="quick-publish-resource"]').setValue('cluster');
    await wrapper.find('[data-test="quick-publish-base-url"]').setValue('https://middleware.example.com');
    await wrapper.find('[data-test="quick-publish-path"]').setValue('/api/redis/clusters/{cluster}/info');
    await wrapper.find('[data-test="quick-publish-description"]').setValue('Read Redis cluster info');

    await wrapper.find('[data-test="quick-publish-submit"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-test="quick-publish-error"]').text()).toContain('capability name conflict');
    expect(wrapper.find('[data-test="workflow-ai"]').exists()).toBe(false);
  });

  test('runs a published read capability through the assistant endpoint', async () => {
    const fetchMock = vi.mocked(fetch);
    const wrapper = mountApp();
    await flushPromises();
    await openReview(wrapper);

    await wrapper.find('[data-test="edit-glusterfs.volume.health.read"]').trigger('click');
    await wrapper.find('[data-test="test-input"]').setValue('{"environment":"prod","cluster":"g1","name":"data"}');
    await wrapper.find('[data-test="run-ai-preflight"]').trigger('click');
    await flushPromises();

    const assistantCall = fetchMock.mock.calls.find(([input]) => String(input) === '/v1/assistant/messages');
    expect(assistantCall).toBeDefined();
    expect(JSON.parse(String(assistantCall?.[1]?.body))).toEqual({
      message: '查询 prod g1 data volume 的 glusterfs 健康',
    });
    expect(wrapper.find('[data-test="ai-preflight-state"]').text()).toContain('已返回答案');
    expect(wrapper.find('[data-test="ai-preflight-result"]').text()).toContain('glusterfs.volume.health.read');
    expect(wrapper.find('[data-test="ai-preflight-result"]').text()).toContain('Volume data is healthy');
  });

  test('renders assistant clarification as an AI preflight result', async () => {
    const wrapper = mountApp();
    await flushPromises();
    await openReview(wrapper);

    await wrapper.find('[data-test="edit-glusterfs.volume.health.read"]').trigger('click');
    await wrapper.find('[data-test="ai-prompt"]').setValue('查询 prod glusterfs');
    await wrapper.find('[data-test="run-ai-preflight"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-test="ai-preflight-state"]').text()).toContain('需要补充参数');
    expect(wrapper.find('[data-test="ai-preflight-result"]').text()).toContain('缺少参数: cluster, name');
  });

  test('validates and previews normalized output', async () => {
    const wrapper = mountApp();
    await flushPromises();
    await openReview(wrapper);

    await wrapper.find('[data-test="edit-minio.bucket.capacity.read"]').trigger('click');
    await wrapper.find('[data-test="test-input"]').setValue('{"environment":"prod","cluster":"m1","bucket":"archive"}');
    await wrapper.find('[data-test="validate-capability"]').trigger('click');
    await wrapper.find('[data-test="test-capability"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-test="validation-state"]').text()).toContain('校验通过');
    expect(wrapper.find('[data-test="request-preview"]').text()).toContain('GET');
    expect(wrapper.find('[data-test="response-preview"]').text()).toContain('usage_pct');
    expect(wrapper.find('[data-test="preview"]').text()).toContain('Bucket archive usage is 77%');
    expect(wrapper.find('[data-test="preview"]').text()).toContain('usage_pct');
  });

  test('loads audit events on mount and shows them on the audit log view', async () => {
    const wrapper = mountApp();
    await flushPromises();

    expect(wrapper.find('[data-test="nav-audit"]').exists()).toBe(true);

    await navigateTo(wrapper, '[data-test="nav-audit"]');

    expect(wrapper.find('[data-test="audit-entry"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="audit-log-view"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="audit-row-audit-1"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="audit-row-audit-2"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="audit-log-view"]').text()).toContain('kafka.topic.retention.set');
    expect(wrapper.find('[data-test="audit-log-view"]').text()).toContain('plan_confirmed');
  });

  test('applies audit filter by refreshing audit events with query params', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await navigateTo(wrapper, '[data-test="nav-audit"]');

    await wrapper.find('[data-test="audit-filter-tool"]').setValue('kafka.topic.retention.set');
    await wrapper.find('[data-test="audit-filter-action"]').setValue('plan_confirmed');
    await wrapper.find('[data-test="audit-filter-limit"]').setValue('5');
    await wrapper.find('[data-test="audit-filter-apply"]').trigger('click');
    await flushPromises();

    const calls = vi.mocked(fetch).mock.calls.map(([input]) => String(input));
    expect(calls.some((path) => path.includes('/v1/audit-events?'))).toBe(true);
    expect(calls.some((path) => path.includes('tool=kafka.topic.retention.set'))).toBe(true);
    expect(calls.some((path) => path.includes('action=plan_confirmed'))).toBe(true);
    expect(calls.some((path) => path.includes('limit=5'))).toBe(true);
  });

  test('submits natural language search query to the search endpoint and renders results', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await navigateTo(wrapper, '[data-test="nav-audit"]');

    await wrapper.find('[data-test="audit-search-query"]').setValue('上周谁拒绝了 plan');
    await wrapper.find('[data-test="audit-search-submit"]').trigger('click');
    await flushPromises();

    const calls = vi.mocked(fetch).mock.calls.map(([input]) => String(input));
    expect(calls.some((path) => path.startsWith('/v1/audit-events/search'))).toBe(true);
    const searchCall = calls.find((path) => path.startsWith('/v1/audit-events/search')) ?? '';
    const searchParams = new URLSearchParams(searchCall.split('?')[1] ?? '');
    expect(searchParams.get('q')).toBe('上周谁拒绝了 plan');

    expect(wrapper.find('[data-test="audit-row-audit-search-1"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="audit-log-view"]').text()).toContain('denied');
  });

  test('refreshes audit events when the refresh button is clicked', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await navigateTo(wrapper, '[data-test="nav-audit"]');

    vi.mocked(fetch).mockClear();
    await wrapper.find('[data-test="audit-refresh"]').trigger('click');
    await flushPromises();

    const calls = vi.mocked(fetch).mock.calls.map(([input]) => String(input));
    expect(calls.some((path) => path === '/v1/audit-events')).toBe(true);
  });

  test('loads more audit events using the next cursor when load more is clicked', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await navigateTo(wrapper, '[data-test="nav-audit"]');

    expect(wrapper.find('[data-test="audit-load-more"]').exists()).toBe(true);

    // Second page: cursor set, returns older events, no further cursor.
    vi.mocked(fetch).mockImplementationOnce((async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('cursor_created_at=')) {
        return ok({
          events: [
            {
              id: 'audit-3',
              plan_id: 'plan-2',
              subject: 'admin-1',
              tool_name: 'kafka.topic.retention.set',
              action: 'plan_created',
              decision: 'permitted',
              metadata: {},
              created_at: '2026-07-25T09:00:00Z',
            },
          ],
          next_cursor: null,
        });
      }
      return ok({ events: [], next_cursor: null });
    }));

    await wrapper.find('[data-test="audit-load-more"]').trigger('click');
    await flushPromises();

    const calls = vi.mocked(fetch).mock.calls.map(([input]) => String(input));
    expect(calls.some((path) => path.includes('cursor_created_at=') && path.includes('cursor_id=audit-2'))).toBe(true);
    expect(wrapper.find('[data-test="audit-row-audit-3"]').exists()).toBe(true);
    // First page events remain in the list.
    expect(wrapper.find('[data-test="audit-row-audit-1"]').exists()).toBe(true);
    // Last page: no more load-more button.
    expect(wrapper.find('[data-test="audit-load-more"]').exists()).toBe(false);
  });

  test('applies subject filter when subject field is set', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await navigateTo(wrapper, '[data-test="nav-audit"]');

    await wrapper.find('[data-test="audit-filter-subject"]').setValue('admin-1');
    await wrapper.find('[data-test="audit-filter-apply"]').trigger('click');
    await flushPromises();

    const calls = vi.mocked(fetch).mock.calls.map(([input]) => String(input));
    expect(calls.some((path) => path.includes('subject=admin-1'))).toBe(true);
  });

  test('opens event detail sidebar with metadata when a row is clicked', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await navigateTo(wrapper, '[data-test="nav-audit"]');

    expect(wrapper.find('[data-test="audit-event-detail"]').exists()).toBe(false);

    await wrapper.find('[data-test="audit-row-audit-1"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-test="audit-event-detail"]').exists()).toBe(true);
    const detail = wrapper.find('[data-test="audit-event-detail"]').text();
    expect(detail).toContain('audit-1');
    expect(detail).toContain('kafka.topic.retention.set');
    expect(detail).toContain('plan_created');
    expect(detail).toContain('source');
    expect(detail).toContain('assistant');
    expect(detail).toContain('risk');
  });

  test('closes the event detail sidebar via the close button', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await navigateTo(wrapper, '[data-test="nav-audit"]');
    await wrapper.find('[data-test="audit-row-audit-1"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-test="audit-event-detail"]').exists()).toBe(true);

    await wrapper.find('[data-test="audit-detail-close"]').trigger('click');

    expect(wrapper.find('[data-test="audit-event-detail"]').exists()).toBe(false);
  });

  test('applies time range filter to the audit query', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await navigateTo(wrapper, '[data-test="nav-audit"]');

    await wrapper.find('[data-test="audit-filter-after"]').setValue('2026-07-25T09:00');
    await wrapper.find('[data-test="audit-filter-before"]').setValue('2026-07-25T11:00');
    await wrapper.find('[data-test="audit-filter-apply"]').trigger('click');
    await flushPromises();

    const calls = vi.mocked(fetch).mock.calls.map(([input]) => String(input));
    expect(calls.some((path) => path.includes('after='))).toBe(true);
    expect(calls.some((path) => path.includes('before='))).toBe(true);
  });

  test('jumps to the plans view and selects the related plan', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await navigateTo(wrapper, '[data-test="nav-audit"]');
    await wrapper.find('[data-test="audit-row-audit-1"]').trigger('click');
    await flushPromises();

    await wrapper.find('[data-test="audit-detail-jump-plan"]').trigger('click');
    await flushPromises();
    await vi.waitFor(() => {
      if (!wrapper.find('[data-test="plans-entry"]').exists()) {
        throw new Error('plans-entry not yet rendered');
      }
    }, { timeout: 3000, interval: 20 });
    await flushPromises();

    expect(wrapper.find('[data-test="plans-entry"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="audit-entry"]').exists()).toBe(false);
  });

  test('exports current audit events as CSV', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await navigateTo(wrapper, '[data-test="nav-audit"]');

    const createObjectURL = vi.fn(() => 'blob:csv');
    const revokeObjectURL = vi.fn();
    vi.stubGlobal('URL', { createObjectURL, revokeObjectURL });
    const clickSpy = vi.fn();
    const fakeLink = { href: '', download: '', click: clickSpy } as unknown as HTMLAnchorElement;
    const createElementSpy = vi.fn(() => fakeLink);
    const fakeBody = {
      appendChild: vi.fn(),
      removeChild: vi.fn(),
    } as unknown as HTMLElement;
    vi.stubGlobal('document', { createElement: createElementSpy, body: fakeBody });
    vi.stubGlobal('Blob', vi.fn());

    await wrapper.find('[data-test="audit-export-csv"]').trigger('click');

    expect(createElementSpy).toHaveBeenCalledWith('a');
    expect(clickSpy).toHaveBeenCalledOnce();
    expect(fakeLink.download).toMatch(/^audit-events-.*\.csv$/);

    vi.unstubAllGlobals();
  });

  test('nav items carry identity icons for each view', async () => {
    const wrapper = mountApp();
    await flushPromises();

    const assistantNav = wrapper.find('[data-test="nav-assistant"]');
    const managementNav = wrapper.find('[data-test="nav-management"]');
    const plansNav = wrapper.find('[data-test="nav-plans"]');
    const auditNav = wrapper.find('[data-test="nav-audit"]');

    expect(assistantNav.attributes('data-view')).toBe('assistant');
    expect(managementNav.attributes('data-view')).toBe('management');
    expect(plansNav.attributes('data-view')).toBe('plans');
    expect(auditNav.attributes('data-view')).toBe('audit');

    expect(assistantNav.find('[data-test="nav-icon"]').exists()).toBe(true);
    expect(managementNav.find('[data-test="nav-icon"]').exists()).toBe(true);
    expect(plansNav.find('[data-test="nav-icon"]').exists()).toBe(true);
    expect(auditNav.find('[data-test="nav-icon"]').exists()).toBe(true);
  });

  test('each view section exposes its identity for accent coloring', async () => {
    const wrapper = mountApp();
    await flushPromises();

    await wrapper.find('[data-test="nav-assistant"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-test="assistant-entry"]').attributes('data-view')).toBe('assistant');

    await wrapper.find('[data-test="nav-management"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-test="management-entry"]').attributes('data-view')).toBe('management');

    await navigateTo(wrapper, '[data-test="nav-plans"]');
    expect(wrapper.find('[data-test="plans-entry"]').attributes('data-view')).toBe('plans');

    await navigateTo(wrapper, '[data-test="nav-audit"]');
    expect(wrapper.find('[data-test="audit-entry"]').attributes('data-view')).toBe('audit');
  });

  test('refreshes audit events after an assistant answer response', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/v1/capabilities') return ok({ capabilities: [] });
      if (url === '/v1/assistant/messages') return ok({ type: 'answer', tool: 'minio.bucket.capacity.read' });
      if (url === '/v1/audit-events' || url.startsWith('/v1/audit-events?')) {
        return ok({ events: [{ id: 'audit-after-answer', plan_id: '', subject: 'assistant', tool_name: 'minio.bucket.capacity.read', action: 'tool_executed', decision: 'permitted', created_at: '2026-07-26T00:00:00Z' }] });
      }
      return ok({});
    }));

    const wrapper = mountApp();
    await flushPromises();
    vi.mocked(fetch).mockClear();

    await wrapper.find('[data-test="assistant-input"]').setValue('查询 minio');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    const calls = vi.mocked(fetch).mock.calls.map(([input]) => String(input));
    expect(calls.some((path) => path === '/v1/audit-events' || path.startsWith('/v1/audit-events?'))).toBe(true);
  });

  test('audit nav badge shows event count', async () => {
    const wrapper = mountApp();
    await flushPromises();

    const badge = wrapper.find('[data-test="nav-audit"] [data-test="nav-badge"]');
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toBe('2');
  });

  test('loads conversation history into the sidebar on mount (cross-refresh persistence)', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      if (url === '/v1/assistant/conversations') {
        return ok({
          conversations: [
            {
              id: 'conv-1',
              subject: 'operator-1',
              title: '查询 minio 容量',
              last_message_preview: 'Bucket archive usage is 77%',
              created_at: '2026-07-25T10:00:00Z',
              last_active_at: '2026-07-25T10:05:00Z',
            },
          ],
          next_cursor: '',
        });
      }
      return ok({});
    }));

    const wrapper = mountApp();
    await flushPromises();

    const items = wrapper.findAll('[data-test="conversation-item"]');
    expect(items).toHaveLength(1);
    expect(items[0].attributes('data-conversation-id')).toBe('conv-1');
    expect(items[0].text()).toContain('查询 minio 容量');
    expect(items[0].text()).toContain('Bucket archive usage is 77%');
  });

  test('switching conversations loads the turn history into the transcript', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      if (url === '/v1/assistant/conversations') {
        return ok({
          conversations: [
            {
              id: 'conv-1',
              subject: 'operator-1',
              title: '历史会话',
              last_message_preview: '之前的消息',
              created_at: '2026-07-25T10:00:00Z',
              last_active_at: '2026-07-25T10:05:00Z',
            },
          ],
          next_cursor: '',
        });
      }
      if (url === '/v1/assistant/conversations/conv-1') {
        return ok({
          conversation: {
            id: 'conv-1',
            subject: 'operator-1',
            title: '历史会话',
            last_message_preview: '之前的消息',
            created_at: '2026-07-25T10:00:00Z',
            last_active_at: '2026-07-25T10:05:00Z',
          },
          turns: [
            {
              id: 'turn-1',
              conversation_id: 'conv-1',
              role: 'user',
              content: '之前的消息',
              created_at: '2026-07-25T10:00:00Z',
            },
            {
              id: 'turn-2',
              conversation_id: 'conv-1',
              role: 'assistant',
              content: 'Bucket archive usage is 77%',
              response_type: 'answer',
              created_at: '2026-07-25T10:01:00Z',
            },
          ],
          next_cursor: '',
        });
      }
      return ok({});
    }));

    const wrapper = mountApp();
    await flushPromises();

    // Initially the transcript should be empty (no active conversation).
    // capabilities 为空时显示 guidance 引导（替代 transcript 内部空状态）。
    expect(wrapper.find('[data-test="assistant-capability-guidance"]').exists()).toBe(true);

    // Click the conversation item to switch.
    await wrapper.find('[data-test="conversation-item"]').trigger('click');
    await flushPromises();

    // The transcript should now show the loaded turns.
    const transcript = wrapper.find('[data-test="assistant-transcript"]');
    expect(transcript.text()).toContain('之前的消息');
    expect(transcript.text()).toContain('Bucket archive usage is 77%');
    // The active conversation item should be highlighted.
    expect(wrapper.find('[data-test="conversation-item"]').classes()).toContain('active');
  });

  test('archiving a conversation removes it from the sidebar and resets the workspace', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      if (url === '/v1/assistant/conversations') {
        return ok({
          conversations: [
            {
              id: 'conv-1',
              subject: 'operator-1',
              title: '待归档会话',
              last_message_preview: '最后一条消息',
              created_at: '2026-07-25T10:00:00Z',
              last_active_at: '2026-07-25T10:05:00Z',
            },
          ],
          next_cursor: '',
        });
      }
      if (url === '/v1/assistant/conversations/conv-1') {
        return ok({
          conversation: {
            id: 'conv-1',
            subject: 'operator-1',
            title: '待归档会话',
            last_message_preview: '最后一条消息',
            created_at: '2026-07-25T10:00:00Z',
            last_active_at: '2026-07-25T10:05:00Z',
          },
          turns: [],
          next_cursor: '',
        });
      }
      if (url === '/v1/assistant/conversations/conv-1/archive') {
        return ok({});
      }
      return ok({});
    });
    vi.stubGlobal('fetch', fetchMock);

    const wrapper = mountApp();
    await flushPromises();

    // Select the conversation first so it becomes active.
    await wrapper.find('[data-test="conversation-item"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-test="conversation-item"]').classes()).toContain('active');

    // Archive it.
    await wrapper.find('[data-test="conversation-archive"]').trigger('click');
    await flushPromises();

    // The conversation should be removed from the sidebar.
    expect(wrapper.find('[data-test="conversation-sidebar-empty"]').exists()).toBe(true);
    expect(wrapper.findAll('[data-test="conversation-item"]')).toHaveLength(0);
    // The archive endpoint should have been called with POST.
    const archiveCall = fetchMock.mock.calls.find(
      ([input, init]) =>
        String(input) === '/v1/assistant/conversations/conv-1/archive' && init?.method === 'POST',
    );
    expect(archiveCall).toBeDefined();
  });

  test('starting a new conversation clears the active conversation and transcript', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      if (url === '/v1/assistant/conversations') {
        return ok({
          conversations: [
            {
              id: 'conv-1',
              subject: 'operator-1',
              title: '历史会话',
              last_message_preview: '之前的消息',
              created_at: '2026-07-25T10:00:00Z',
              last_active_at: '2026-07-25T10:05:00Z',
            },
          ],
          next_cursor: '',
        });
      }
      if (url === '/v1/assistant/conversations/conv-1') {
        return ok({
          conversation: {
            id: 'conv-1',
            subject: 'operator-1',
            title: '历史会话',
            last_message_preview: '之前的消息',
            created_at: '2026-07-25T10:00:00Z',
            last_active_at: '2026-07-25T10:05:00Z',
          },
          turns: [
            {
              id: 'turn-1',
              conversation_id: 'conv-1',
              role: 'user',
              content: '之前的消息',
              created_at: '2026-07-25T10:00:00Z',
            },
          ],
          next_cursor: '',
        });
      }
      return ok({});
    }));

    const wrapper = mountApp();
    await flushPromises();

    // Select the existing conversation.
    await wrapper.find('[data-test="conversation-item"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-test="assistant-transcript"]').text()).toContain('之前的消息');

    // Click "新会话".
    await wrapper.find('[data-test="conversation-new"]').trigger('click');
    await flushPromises();

    // The transcript should be cleared and the empty state shown.
    // capabilities 为空时显示 guidance 引导（替代 transcript 内部空状态）。
    expect(wrapper.find('[data-test="assistant-capability-guidance"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="assistant-transcript"]').text()).not.toContain('之前的消息');
    // No conversation should be marked active.
    expect(wrapper.findAll('[data-test="conversation-item"].active')).toHaveLength(0);
  });

  test('sending a message with an active conversation includes conversation_id in the payload', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      if (url === '/v1/assistant/conversations') {
        return ok({
          conversations: [
            {
              id: 'conv-active',
              subject: 'operator-1',
              title: '进行中的会话',
              last_message_preview: '继续对话',
              created_at: '2026-07-25T10:00:00Z',
              last_active_at: '2026-07-25T10:05:00Z',
            },
          ],
          next_cursor: '',
        });
      }
      if (url === '/v1/assistant/conversations/conv-active') {
        return ok({
          conversation: {
            id: 'conv-active',
            subject: 'operator-1',
            title: '进行中的会话',
            last_message_preview: '继续对话',
            created_at: '2026-07-25T10:00:00Z',
            last_active_at: '2026-07-25T10:05:00Z',
          },
          turns: [
            {
              id: 'turn-1',
              conversation_id: 'conv-active',
              role: 'user',
              content: '继续对话',
              created_at: '2026-07-25T10:00:00Z',
            },
          ],
          next_cursor: '',
        });
      }
      if (url === '/v1/assistant/messages') {
        return ok({
          type: 'answer',
          conversation_id: 'conv-active',
          tool: 'minio.bucket.capacity.read',
          answer: { summary: 'Bucket archive usage is 77%', severity: 'info' },
        });
      }
      return ok({});
    });
    vi.stubGlobal('fetch', fetchMock);

    const wrapper = mountApp();
    await flushPromises();

    // Select the existing conversation so it becomes active.
    await wrapper.find('[data-test="conversation-item"]').trigger('click');
    await flushPromises();

    // Clear mock calls to isolate the assistant message call.
    fetchMock.mockClear();
    await wrapper.find('[data-test="assistant-input"]').setValue('再查一次 minio');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    const assistantCall = fetchMock.mock.calls.find(([input]) => String(input) === '/v1/assistant/messages');
    expect(assistantCall).toBeDefined();
    expect(JSON.parse(String(assistantCall?.[1]?.body))).toEqual({
      message: '再查一次 minio',
      conversation_id: 'conv-active',
      environment: 'prod',
    });
  });

  test('assistant response with conversation_id refreshes turns from the server', async () => {
    const detailCalls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      if (url === '/v1/assistant/conversations') {
        return ok({ conversations: [], next_cursor: '' });
      }
      if (url.startsWith('/v1/assistant/conversations/conv-new')) {
        detailCalls.push(url);
        return ok({
          conversation: {
            id: 'conv-new',
            subject: 'operator-1',
            title: '新创建的会话',
            last_message_preview: '查询 minio',
            created_at: '2026-07-25T10:00:00Z',
            last_active_at: '2026-07-25T10:05:00Z',
          },
          turns: [
            {
              id: 'turn-1',
              conversation_id: 'conv-new',
              role: 'user',
              content: '查询 minio',
              created_at: '2026-07-25T10:00:00Z',
            },
            {
              id: 'turn-2',
              conversation_id: 'conv-new',
              role: 'assistant',
              content: 'Bucket archive usage is 77%',
              response_type: 'answer',
              created_at: '2026-07-25T10:01:00Z',
            },
          ],
          next_cursor: '',
        });
      }
      if (url === '/v1/assistant/messages') {
        return ok({
          type: 'answer',
          conversation_id: 'conv-new',
          tool: 'minio.bucket.capacity.read',
          answer: { summary: 'Bucket archive usage is 77%', severity: 'info' },
        });
      }
      return ok({});
    }));

    const wrapper = mountApp();
    await flushPromises();

    // Initially the sidebar is empty.
    expect(wrapper.find('[data-test="conversation-sidebar-empty"]').exists()).toBe(true);

    // Send a message; the response carries a new conversation_id.
    await wrapper.find('[data-test="assistant-input"]').setValue('查询 minio');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    // The transcript should reflect the server-persisted turns after refresh.
    const transcript = wrapper.find('[data-test="assistant-transcript"]');
    expect(transcript.text()).toContain('查询 minio');
    expect(transcript.text()).toContain('Bucket archive usage is 77%');
    // The conversation detail endpoint should have been called to refresh turns.
    expect(detailCalls.some((path) => path.includes('/v1/assistant/conversations/conv-new'))).toBe(true);
  });

  test('shows load-more button when conversation has more historical turns', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      if (url === '/v1/assistant/conversations') {
        return ok({
          conversations: [
            {
              id: 'conv-1',
              subject: 'operator-1',
              title: '长会话',
              last_message_preview: '最新消息',
              created_at: '2026-07-25T10:00:00Z',
              last_active_at: '2026-07-25T10:05:00Z',
            },
          ],
          next_cursor: '',
        });
      }
      // First page: returns next_cursor indicating more history exists.
      if (url === '/v1/assistant/conversations/conv-1') {
        return ok({
          conversation: {
            id: 'conv-1',
            subject: 'operator-1',
            title: '长会话',
            last_message_preview: '最新消息',
            created_at: '2026-07-25T10:00:00Z',
            last_active_at: '2026-07-25T10:05:00Z',
          },
          turns: [
            {
              id: 'turn-recent',
              conversation_id: 'conv-1',
              role: 'user',
              content: '最新消息',
              created_at: '2026-07-25T10:05:00Z',
            },
          ],
          next_cursor: 'cursor-older',
        });
      }
      return ok({});
    }));

    const wrapper = mountApp();
    await flushPromises();

    // Initially no load-more (no active conversation).
    expect(wrapper.find('[data-test="assistant-load-more"]').exists()).toBe(false);

    // Select the conversation; load-more should appear since next_cursor is set.
    await wrapper.find('[data-test="conversation-item"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-test="assistant-load-more"]').exists()).toBe(true);
  });

  test('clicking load-more prepends older turns to the transcript', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      if (url === '/v1/assistant/conversations') {
        return ok({
          conversations: [
            {
              id: 'conv-1',
              subject: 'operator-1',
              title: '长会话',
              last_message_preview: '最新消息',
              created_at: '2026-07-25T10:00:00Z',
              last_active_at: '2026-07-25T10:05:00Z',
            },
          ],
          next_cursor: '',
        });
      }
      // First page: only the latest turn, with next_cursor pointing to older history.
      if (url === '/v1/assistant/conversations/conv-1') {
        return ok({
          conversation: {
            id: 'conv-1',
            subject: 'operator-1',
            title: '长会话',
            last_message_preview: '最新消息',
            created_at: '2026-07-25T10:00:00Z',
            last_active_at: '2026-07-25T10:05:00Z',
          },
          turns: [
            {
              id: 'turn-recent',
              conversation_id: 'conv-1',
              role: 'user',
              content: '最新消息',
              created_at: '2026-07-25T10:05:00Z',
            },
          ],
          next_cursor: 'cursor-older',
        });
      }
      // Second page: older turns, before_turn_id=turn-recent.
      if (url.startsWith('/v1/assistant/conversations/conv-1?') && url.includes('before_turn_id=turn-recent')) {
        return ok({
          conversation: {
            id: 'conv-1',
            subject: 'operator-1',
            title: '长会话',
            last_message_preview: '最新消息',
            created_at: '2026-07-25T10:00:00Z',
            last_active_at: '2026-07-25T10:05:00Z',
          },
          turns: [
            {
              id: 'turn-old',
              conversation_id: 'conv-1',
              role: 'assistant',
              content: '更早的回复',
              response_type: 'answer',
              created_at: '2026-07-25T09:00:00Z',
            },
          ],
          next_cursor: '',
        });
      }
      return ok({});
    }));

    const wrapper = mountApp();
    await flushPromises();

    // Select the conversation.
    await wrapper.find('[data-test="conversation-item"]').trigger('click');
    await flushPromises();

    // Initial transcript only has the latest turn.
    expect(wrapper.find('[data-test="assistant-transcript"]').text()).toContain('最新消息');
    expect(wrapper.find('[data-test="assistant-transcript"]').text()).not.toContain('更早的回复');

    // Click load-more.
    await wrapper.find('[data-test="assistant-load-more"]').trigger('click');
    await flushPromises();

    // Older turn should now be prepended.
    const transcript = wrapper.find('[data-test="assistant-transcript"]');
    expect(transcript.text()).toContain('更早的回复');
    expect(transcript.text()).toContain('最新消息');
    // load-more should disappear since next_cursor is now empty.
    expect(wrapper.find('[data-test="assistant-load-more"]').exists()).toBe(false);
  });

  test('shows a retry button on failure and re-sends the same message when clicked', async () => {
    let failedOnce = false;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      if (url === '/v1/assistant/messages') {
        const body = JSON.parse(String(_init?.body ?? '{}')) as { message?: string };
        if (body.message === '会失败的消息' && !failedOnce) {
          failedOnce = true;
          return new Response(JSON.stringify({ error: 'assistant gateway timeout' }), {
            status: 503,
            headers: { 'Content-Type': 'application/json' },
          });
        }
        return ok({
          type: 'answer',
          tool: 'minio.bucket.capacity.read',
          answer: { summary: '重试成功', severity: 'info' },
        });
      }
      return ok({});
    });
    vi.stubGlobal('fetch', fetchMock);

    const wrapper = mountApp();
    await flushPromises();

    // Send a message that fails on the first attempt.
    await wrapper.find('[data-test="assistant-input"]').setValue('会失败的消息');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    // The error block and retry button should be visible.
    expect(wrapper.find('[data-test="assistant-error"]').exists()).toBe(true);
    // 错误详情在对话气泡中，红条只显示简短提示
    expect(wrapper.find('[data-test="conversation-turn-error"]').text()).toContain('assistant gateway timeout');
    const retryButton = wrapper.find('[data-test="assistant-retry"]');
    expect(retryButton.exists()).toBe(true);
    expect(retryButton.attributes('disabled')).toBeUndefined();

    // Input should be cleared after the failed send (retry preserves the
    // original message internally, not via the input field).
    expect((wrapper.find('[data-test="assistant-input"]').element as HTMLTextAreaElement).value).toBe('');

    // Click retry; the same message should be re-sent and succeed this time.
    fetchMock.mockClear();
    await retryButton.trigger('click');
    await flushPromises();

    const retryCall = fetchMock.mock.calls.find(([input]) => String(input) === '/v1/assistant/messages');
    expect(retryCall).toBeDefined();
    expect(JSON.parse(String(retryCall?.[1]?.body))).toEqual({ message: '会失败的消息', environment: 'prod' });
    // The retry succeeded, so the error should clear and the answer should appear.
    expect(wrapper.find('[data-test="assistant-error"]').exists()).toBe(false);
    expect(wrapper.find('[data-test="assistant-latest-detail"]').text()).toContain('重试成功');
  });

  test('hides retry button when there is no failed message yet', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({ capabilities: [] });
      }
      if (url === '/v1/assistant/messages') {
        return ok({
          type: 'answer',
          tool: 'minio.bucket.capacity.read',
          answer: { summary: '成功', severity: 'info' },
        });
      }
      return ok({});
    }));

    const wrapper = mountApp();
    await flushPromises();

    // Successful send: no error block, no retry button.
    await wrapper.find('[data-test="assistant-input"]').setValue('正常消息');
    await wrapper.find('[data-test="assistant-send"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-test="assistant-error"]').exists()).toBe(false);
    expect(wrapper.find('[data-test="assistant-retry"]').exists()).toBe(false);
  });

  // ===== 定时巡检任务 E2E =====

  test('admin creates a scheduled task and sees it in the list', async () => {
    let createdTask: Record<string, unknown> | null = null;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/v1/capabilities') {
        return ok({
          capabilities: [
            {
              name: 'minio.bucket.capacity.read',
              status: 'published',
              source: 'published',
              domain: 'minio',
              resource_type: 'bucket',
              operation: 'read',
              risk: 'low',
              backend: { method: 'GET', base_url: 'https://middleware.example.com', path: '/api/minio/{cluster}/buckets/{bucket}/capacity' },
              validation: { valid: true },
            },
          ],
        });
      }
      if (url === '/v1/scheduled-tasks') {
        if (init?.method === 'POST') {
          const body = JSON.parse(String(init.body));
          createdTask = {
            id: 'task-1',
            name: body.name,
            subject: 'admin-1',
            capability_name: body.capability_name,
            input: body.input,
            schedule_kind: body.schedule_kind,
            preset: body.preset ?? null,
            cron_expr: body.cron_expr ?? null,
            timezone: 'Asia/Shanghai',
            enabled: true,
            last_run_at: null,
            last_status: '',
            next_run_at: '2026-07-28T00:00:00Z',
            created_at: '2026-07-27T10:00:00Z',
            updated_at: '2026-07-27T10:00:00Z',
          };
          return ok(createdTask);
        }
        // GET list: return created task if it exists
        return ok({ tasks: createdTask ? [createdTask] : [] });
      }
      if (url === '/v1/scheduled-tasks/failures/count') {
        return ok({ count: 0 });
      }
      return ok({});
    });
    vi.stubGlobal('fetch', fetchMock);

    const wrapper = mountApp();
    await flushPromises();

    // Navigate to scheduled tasks view
    await navigateTo(wrapper, '[data-test="nav-scheduled-tasks"]');

    expect(wrapper.find('[data-test="scheduled-tasks-entry"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="scheduled-task-empty"]').exists()).toBe(true);

    // Click new task button
    await wrapper.find('[data-test="scheduled-task-new"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-test="scheduled-task-form-modal"]').exists()).toBe(true);

    // Fill the form
    await wrapper.find('[data-test="scheduled-task-name"]').setValue('minio 每日巡检');
    await wrapper.find('[data-test="scheduled-task-capability"]').setValue('minio.bucket.capacity.read');
    await wrapper.find('[data-test="scheduled-task-input"]').setValue('{"environment":"prod","cluster":"m1","bucket":"archive"}');
    await wrapper.find('[data-test="schedule-preset-option"][data-preset="daily"]').trigger('click');

    // Submit
    await wrapper.find('[data-test="scheduled-task-submit"]').trigger('click');
    await flushPromises();

    // Verify POST was called with correct payload
    const createCall = fetchMock.mock.calls.find(
      ([input, init]) => String(input) === '/v1/scheduled-tasks' && init?.method === 'POST',
    );
    expect(createCall).toBeDefined();
    expect(JSON.parse(String(createCall?.[1]?.body))).toEqual({
      name: 'minio 每日巡检',
      capability_name: 'minio.bucket.capacity.read',
      input: { environment: 'prod', cluster: 'm1', bucket: 'archive' },
      schedule_kind: 'preset',
      preset: 'daily',
      cron_expr: null,
    });

    // List should refresh and show the task
    expect(wrapper.find('[data-test="scheduled-task-row"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="scheduled-task-row"]').text()).toContain('minio 每日巡检');
  });

  test('triggering a scheduled task shows the run in the history panel', async () => {
    const task = {
      id: 'task-1',
      name: 'minio 每日巡检',
      subject: 'admin-1',
      capability_name: 'minio.bucket.capacity.read',
      input: { environment: 'prod' },
      schedule_kind: 'preset',
      preset: 'daily',
      cron_expr: null,
      timezone: 'Asia/Shanghai',
      enabled: true,
      last_run_at: null,
      last_status: '',
      next_run_at: '2026-07-28T00:00:00Z',
      created_at: '2026-07-27T10:00:00Z',
      updated_at: '2026-07-27T10:00:00Z',
    };
    const run = {
      id: 'run-1',
      task_id: 'task-1',
      started_at: '2026-07-27T10:05:00Z',
      finished_at: '2026-07-27T10:05:05Z',
      status: 'succeeded',
      result_summary: 'Bucket archive usage is 77%',
      result_data: { usage_pct: 77 },
      error: '',
      audit_event_id: 'audit-1',
    };

    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/v1/capabilities') return ok({ capabilities: [] });
      if (url === '/v1/scheduled-tasks') return ok({ tasks: [task] });
      if (url === '/v1/scheduled-tasks/failures/count') return ok({ count: 0 });
      if (url === '/v1/scheduled-tasks/task-1/run' && init?.method === 'POST') return ok(run);
      if (url === '/v1/scheduled-tasks/task-1/runs') return ok({ runs: [run] });
      return ok({});
    }));

    const wrapper = mountApp();
    await flushPromises();

    await navigateTo(wrapper, '[data-test="nav-scheduled-tasks"]');

    expect(wrapper.find('[data-test="scheduled-task-row"]').exists()).toBe(true);

    // Click trigger button
    await wrapper.find('[data-test="scheduled-task-trigger"]').trigger('click');
    await flushPromises();

    // Run history panel should appear with the run
    expect(wrapper.find('[data-test="scheduled-task-run-history-panel"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="scheduled-task-run-row"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="scheduled-task-run-row"]').text()).toContain('succeeded');
  });

  test('scheduled task failure count badge appears in the sidebar', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/v1/capabilities') return ok({ capabilities: [] });
      if (url === '/v1/scheduled-tasks/failures/count') return ok({ count: 3 });
      return ok({});
    }));

    const wrapper = mountApp();
    await flushPromises();

    const badge = wrapper.find('[data-test="nav-scheduled-tasks"] [data-test="scheduled-task-badge"]');
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toBe('3');
  });

  test('toggling a scheduled task enabled state sends PATCH with the new enabled value', async () => {
    const task = {
      id: 'task-1',
      name: 'minio 每日巡检',
      subject: 'admin-1',
      capability_name: 'minio.bucket.capacity.read',
      input: { environment: 'prod' },
      schedule_kind: 'preset',
      preset: 'daily',
      cron_expr: null,
      timezone: 'Asia/Shanghai',
      enabled: true,
      last_run_at: null,
      last_status: '',
      next_run_at: '2026-07-28T00:00:00Z',
      created_at: '2026-07-27T10:00:00Z',
      updated_at: '2026-07-27T10:00:00Z',
    };

    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/v1/capabilities') return ok({ capabilities: [] });
      if (url === '/v1/scheduled-tasks') return ok({ tasks: [task] });
      if (url === '/v1/scheduled-tasks/failures/count') return ok({ count: 0 });
      if (url === '/v1/scheduled-tasks/task-1' && init?.method === 'PATCH') {
        const body = JSON.parse(String(init.body));
        return ok({ ...task, enabled: body.enabled });
      }
      return ok({});
    });
    vi.stubGlobal('fetch', fetchMock);

    const wrapper = mountApp();
    await flushPromises();

    await navigateTo(wrapper, '[data-test="nav-scheduled-tasks"]');

    const toggle = wrapper.find('[data-test="scheduled-task-toggle"]');
    expect((toggle.element as HTMLInputElement).checked).toBe(true);

    // Uncheck the toggle
    await toggle.setValue(false);
    await flushPromises();

    const patchCall = fetchMock.mock.calls.find(
      ([input, init]) => String(input) === '/v1/scheduled-tasks/task-1' && init?.method === 'PATCH',
    );
    expect(patchCall).toBeDefined();
    const body = JSON.parse(String(patchCall?.[1]?.body));
    expect(body.enabled).toBe(false);
  });
});
