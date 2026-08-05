import { flushPromises, mount } from '@vue/test-utils';
import { describe, expect, test, vi, beforeEach } from 'vitest';
import ExecutionsView from './ExecutionsView.vue';

function ok(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

const records = [
  {
    id: 'exec-1',
    action_plan_id: 'plan-abc',
    status: 'succeeded',
    tool_name: 'kafka.read',
    created_at: '2026-08-04T12:00:00Z',
    started_at: '2026-08-04T12:00:00Z',
    completed_at: '2026-08-04T12:00:10Z',
  },
];

// ExecutionsView 通过 useExecutions → listExecutions(fetch) 取数；这里 stub 全局
// fetch，返回一页执行记录，验证 plan 跳转按钮渲染并 emit jump-to-audit。
beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.startsWith('/v1/executions')) {
      return ok({ executions: records });
    }
    return ok({});
  }));
});

describe('ExecutionsView', () => {
  test('渲染可点击的 plan 跳转按钮', async () => {
    const wrapper = mount(ExecutionsView);
    await flushPromises();

    const jump = wrapper.find('[data-test="executions-plan-jump-exec-1"]');
    expect(jump.exists()).toBe(true);
    expect(jump.text()).toContain('plan-abc');
  });

  test('点击 plan 跳转按钮 emit jump-to-audit(planID, tool)', async () => {
    const wrapper = mount(ExecutionsView);
    await flushPromises();

    await wrapper.find('[data-test="executions-plan-jump-exec-1"]').trigger('click');

    const emitted = wrapper.emitted('jump-to-audit');
    expect(emitted).toBeTruthy();
    expect(emitted?.[0]).toEqual(['plan-abc', 'kafka.read']);
  });

  test('详情面板里也能触发 plan 跳转', async () => {
    const wrapper = mount(ExecutionsView);
    await flushPromises();

    // 先选中一条记录显示详情
    await wrapper.find('[data-test="executions-row-exec-1"]').trigger('click');
    await flushPromises();

    const panelJump = wrapper.find('[data-test="executions-detail-plan-jump"]');
    expect(panelJump.exists()).toBe(true);
    await panelJump.trigger('click');

    const emitted = wrapper.emitted('jump-to-audit');
    expect(emitted).toBeTruthy();
    expect(emitted?.[0]).toEqual(['plan-abc', 'kafka.read']);
  });
});
