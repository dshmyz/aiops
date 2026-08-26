import { flushPromises, mount } from '@vue/test-utils';
import { describe, expect, test, vi, beforeEach } from 'vitest';
import DashboardView from './DashboardView.vue';

function ok(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

const overview = {
  pending_plans: 1,
  active_alerts: 2,
  enabled_tasks: 3,
  today_executions_succeeded: 5,
  today_executions_failed: 1,
};

const plans = [
  {
    id: 'plan-1',
    tool: 'kafka.topic.retention.set',
    risk: 'medium',
    status: 'pending_confirmation',
    version: 1,
    expires_at: '2026-08-07T09:00:00Z',
    created_by: 'operator-1',
    created_at: '2026-08-07T08:00:00Z',
  },
];

let lastBody: { call: string; body: any; auth?: boolean } | null = null;

// DashboardView 通过 fetchOverview + listPendingPlans 取数；stub 全局 fetch，
// 覆盖 overview 计数渲染、空态、以及 reject 端点调用与刷新。
beforeEach(() => {
  lastBody = null;
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url === '/v1/overview') {
      return ok(overview);
    }
    if (url.startsWith('/v1/action-plans?status=pending_confirmation')) {
      return ok({ plans });
    }
    if (url.endsWith('/reject')) {
      lastBody = { call: 'reject', body: init?.body ? JSON.parse(String(init.body)) : null };
      return ok({ type: 'plan_rejected', plan_id: 'plan-1', status: 'rejected', version: 2 });
    }
    return ok({ plans: [] });
  }));
});

describe('DashboardView', () => {
  test('渲染顶部统计卡片', async () => {
    const wrapper = mount(DashboardView);
    await flushPromises();

    expect(wrapper.find('[data-test="stat-pending-plans"]').text()).toContain('1');
    expect(wrapper.find('[data-test="stat-active-alerts"]').text()).toContain('2');
    expect(wrapper.find('[data-test="stat-enabled-tasks"]').text()).toContain('3');
    expect(wrapper.find('[data-test="stat-today-executions"]').text()).toContain('5');
    expect(wrapper.find('[data-test="stat-today-executions"]').text()).toContain('1');
  });

  test('点击统计卡片 emit navigate 下钻到对应视图', async () => {
    const wrapper = mount(DashboardView);
    await flushPromises();

    const cases: Array<[string, string]> = [
      ['stat-pending-plans', 'plans'],
      ['stat-active-alerts', 'incident'],
      ['stat-enabled-tasks', 'scheduled-tasks'],
      ['stat-today-executions', 'executions'],
    ];
    for (const [dataTest, view] of cases) {
      await wrapper.find(`[data-test="${dataTest}"]`).trigger('click');
    }
    const emitted = wrapper.emitted('navigate');
    expect(emitted?.map((e) => e[0])).toEqual(cases.map(([, view]) => view));
  });

  test('渲染待确认计划列表并可拒绝', async () => {
    const wrapper = mount(DashboardView);
    await flushPromises();

    expect(wrapper.text()).toContain('kafka.topic.retention.set');
    await wrapper.find('[data-test="dashboard-entry"] .mini-button--danger').trigger('click');
    await flushPromises();

    expect(lastBody).toEqual({ call: 'reject', body: { expected_version: 1 } });
  });

  test('无待确认计划时显示空态', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/v1/overview') {
        return ok({ pending_plans: 0 });
      }
      return ok({ plans: [] });
    }));
    const wrapper = mount(DashboardView);
    await flushPromises();

    expect(wrapper.find('.overview-plan-list').exists()).toBe(false);
    expect(wrapper.text()).toContain('当前没有待确认计划');
  });
});
