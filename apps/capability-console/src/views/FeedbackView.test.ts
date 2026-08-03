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
});
