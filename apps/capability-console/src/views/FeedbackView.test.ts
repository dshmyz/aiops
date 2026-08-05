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
});
