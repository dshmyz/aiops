import { mount, flushPromises } from '@vue/test-utils';
import { describe, expect, test, vi, beforeEach } from 'vitest';
import FeedbackView from './FeedbackView.vue';

function ok(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('FeedbackView', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith('/v1/assistant/feedback')) {
        return ok({ items: [], total: 0, limit: 20, offset: 0 });
      }
      return ok({});
    }));
  });

  test('renders empty state with action button when no feedback exists', async () => {
    const wrapper = mount(FeedbackView);
    await flushPromises();

    // 空状态使用统一的 .empty 类
    expect(wrapper.find('.empty').exists()).toBe(true);
    // 行动引导按钮存在
    const action = wrapper.find('[data-test="feedback-empty-action"]');
    expect(action.exists()).toBe(true);
    expect(action.text()).toContain('去对话反馈');
  });

  test('emits go-to-assistant event when action button is clicked', async () => {
    const wrapper = mount(FeedbackView);
    await flushPromises();

    await wrapper.find('[data-test="feedback-empty-action"]').trigger('click');

    expect(wrapper.emitted('go-to-assistant')).toBeTruthy();
    expect(wrapper.emitted('go-to-assistant')?.length).toBe(1);
  });

  test('renders improvement insights grouped from correction feedback', async () => {
    const feedback = [
      { id: '1', conversation_id: 'c', turn_id: 't1', subject: 'admin-1', rating: -1, correction: '缺少参数: name', created_at: '2026-08-01T00:00:00Z' },
      { id: '2', conversation_id: 'c', turn_id: 't2', subject: 'admin-1', rating: -1, correction: '参数缺失导致往返', created_at: '2026-08-01T00:00:00Z' },
      { id: '3', conversation_id: 'c', turn_id: 't3', subject: 'admin-1', rating: 1, correction: '', created_at: '2026-08-01T00:00:00Z' },
    ];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith('/v1/assistant/feedback')) {
        return ok({ items: feedback, total: feedback.length, limit: 200, offset: 0 });
      }
      return ok({});
    }));
    const wrapper = mount(FeedbackView);
    await flushPromises();

    const insights = wrapper.findAll('[data-test="feedback-insight-item"]');
    expect(insights.length).toBe(1);
    expect(insights[0].text()).toContain('参数澄清');
    expect(insights[0].text()).toContain('2 条');

    // 导出按钮存在，且在有建议时可点
    const exportBtn = wrapper.find('[data-test="feedback-insights-export"]');
    expect(exportBtn.exists()).toBe(true);
    expect((exportBtn.attributes('disabled'))).toBeUndefined();
  });

  test('export button is disabled when no actionable insight exists', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith('/v1/assistant/feedback')) {
        return ok({ items: [], total: 0, limit: 200, offset: 0 });
      }
      return ok({});
    }));
    const wrapper = mount(FeedbackView);
    await flushPromises();

    const exportBtn = wrapper.find('[data-test="feedback-insights-export"]');
    expect(exportBtn.attributes('disabled')).toBeDefined();
  });

  test('generates runbook draft and shows inferred fields with confirm button', async () => {
    const feedback = [
      { id: '1', conversation_id: 'c', turn_id: 't1', subject: 'admin-1', rating: -1, correction: '把保留改成 72 小时时确认流程太绕', created_at: '2026-08-01T00:00:00Z' },
    ];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.startsWith('/v1/assistant/feedback')) {
        return ok({ items: feedback, total: 1, limit: 200, offset: 0 });
      }
      if (url.startsWith('/v1/admin/runbook-drafts/infer')) {
        return ok({
          id: 'draft-1',
          slug: 'fb-retention-fb-retention-把保留改成',
          name: '资源保留策略调整',
          intent_pattern: ['保留', 'retention', '留存', '72 小时', '小时'],
          tool_sequence: ['topic.retention.set'],
          risk_level: 'low',
          topic_key: 'retention',
          status: 'draft',
          created_at: '2026-08-01T00:00:00Z',
        });
      }
      return ok({});
    }));
    const wrapper = mount(FeedbackView);
    await flushPromises();

    const genBtn = wrapper.find('[data-test="feedback-draft-runbook"]');
    expect(genBtn.exists()).toBe(true);
    await genBtn.trigger('click');
    await flushPromises();

    const preview = wrapper.find('[data-test="feedback-draft-preview"]');
    expect(preview.exists()).toBe(true);
    expect(preview.text()).toContain('topic.retention.set');
    const activate = wrapper.find('[data-test="feedback-draft-activate"]');
    expect(activate.exists()).toBe(true);
    expect(activate.text()).toContain('确认启用');
  });

  test('activating a draft marks it enabled and hides the preview', async () => {
    const feedback = [
      { id: '1', conversation_id: 'c', turn_id: 't1', subject: 'admin-1', rating: -1, correction: '把保留改成 72 小时时确认流程太绕', created_at: '2026-08-01T00:00:00Z' },
    ];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.startsWith('/v1/assistant/feedback')) {
        return ok({ items: feedback, total: 1, limit: 200, offset: 0 });
      }
      if (url.startsWith('/v1/admin/runbook-drafts/infer')) {
        return ok({
          id: 'draft-1', slug: 'fb-retention', name: '资源保留策略调整',
          intent_pattern: ['保留'], tool_sequence: ['topic.retention.set'],
          risk_level: 'low', topic_key: 'retention', status: 'draft', created_at: '2026-08-01T00:00:00Z',
        });
      }
      if (url.startsWith('/v1/admin/runbook-drafts/draft-1/activate')) {
        return ok({
          id: 'draft-1', slug: 'fb-retention', name: '资源保留策略调整',
          intent_pattern: ['保留'], tool_sequence: ['topic.retention.set'],
          risk_level: 'low', topic_key: 'retention', status: 'activated',
          activated_at: '2026-08-01T00:00:00Z', created_at: '2026-08-01T00:00:00Z',
        });
      }
      return ok({});
    }));
    const wrapper = mount(FeedbackView);
    await flushPromises();

    await wrapper.find('[data-test="feedback-draft-runbook"]').trigger('click');
    await flushPromises();
    await wrapper.find('[data-test="feedback-draft-activate"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-test="feedback-draft-preview"]').exists()).toBe(false);
    const done = wrapper.find('[data-test="feedback-draft-activated"]');
    expect(done.exists()).toBe(true);
    expect(done.text()).toContain('已启用');
  });

  test('non-runbookable topic shows missing reason and no confirm button', async () => {
    const feedback = [
      { id: '1', conversation_id: 'c', turn_id: 't1', subject: 'admin-1', rating: -1, correction: '结果太啰嗦，希望更格式', created_at: '2026-08-01T00:00:00Z' },
    ];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith('/v1/assistant/feedback')) {
        return ok({ items: feedback, total: 1, limit: 200, offset: 0 });
      }
      if (url.startsWith('/v1/admin/runbook-drafts/infer')) {
        return ok({
          id: 'draft-2', slug: '', name: '', intent_pattern: [], tool_sequence: [],
          risk_level: '', topic_key: 'format', status: 'draft',
          missing_reason: '该主题无法落成 runbook，需人工判断', created_at: '2026-08-01T00:00:00Z',
        });
      }
      return ok({});
    }));
    const wrapper = mount(FeedbackView);
    await flushPromises();

    await wrapper.find('[data-test="feedback-draft-runbook"]').trigger('click');
    await flushPromises();

    const preview = wrapper.find('[data-test="feedback-draft-preview"]');
    expect(preview.exists()).toBe(true);
    expect(preview.text()).toContain('无法落成 runbook');
    expect(wrapper.find('[data-test="feedback-draft-activate"]').exists()).toBe(false);
  });
});
