import { flushPromises, mount } from '@vue/test-utils';
import { describe, expect, test, vi, beforeEach } from 'vitest';
import DocsView from './DocsView.vue';

function ok(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

const markdown = '# 使用手册\n\n这是 **手册** 内容。';

// DocsView 通过 getDoc → request → fetch('/v1/docs/OPERATIONS.md') 取数；
// 这里 stub 全局 fetch，返回一段 markdown，验证渲染到 docs-content。
beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.startsWith('/v1/docs/')) {
      return ok({ name: 'OPERATIONS.md', content: markdown });
    }
    return ok({});
  }));
});

describe('DocsView', () => {
  test('挂载后请求文档并渲染 markdown 内容', async () => {
    const wrapper = mount(DocsView);
    await flushPromises();

    // 断言确实请求了 /v1/docs/OPERATIONS.md
    const calls = (fetch as unknown as ReturnType<typeof vi.fn>).mock.calls;
    expect(calls.some((c: unknown[]) => String(c[0]).startsWith('/v1/docs/'))).toBe(true);

    const content = wrapper.find('[data-test="docs-content"]');
    expect(content.exists()).toBe(true);
    expect(content.text()).toContain('使用手册');
    expect(content.text()).toContain('手册');
  });

  test('请求失败时展示错误而非文档', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => {
      throw new Error('boom');
    }));
    const wrapper = mount(DocsView);
    await flushPromises();

    expect(wrapper.find('[data-test="docs-content"]').exists()).toBe(false);
    const alertEl = wrapper.find('[role="alert"]');
    expect(alertEl.exists()).toBe(true);
    // 组件把真实错误信息透出（fetch 抛出的 "boom"）
    expect(alertEl.text()).toContain('boom');
  });
});
