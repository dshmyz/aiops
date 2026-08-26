import { flushPromises, mount } from '@vue/test-utils';
import { describe, expect, test, vi, beforeEach } from 'vitest';
import IncidentView from './IncidentView.vue';

function ok(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

// incident.view 通过 useIncident → viewIncident(fetch) 取数；这里 stub 全局
// fetch，返回一个完整的 incident 全景（告警 + timeline + 巡检 + 探测 + runbook +
// 写操作），验证提交后渲染各证据块并 emit jump-to-audit。
const panorama = {
  result: {
    incident_id: 'alert-1',
    pivot: { domain: 'minio', resource_type: 'bucket', resource_name: 'archive' },
    alert: {
      title: 'bucket capacity over 85%',
      source: 'prometheus',
      status: 'firing',
      severity: 'critical',
      fired_at: '2026-08-04T12:00:00Z',
      description: 'capacity high',
    },
    timeline: [
      {
        id: 'audit-1',
        tool_name: 'minio.bucket.retention.set',
        action: 'plan_created',
        decision: 'apply',
        created_at: '2026-08-04T12:05:00Z',
        action_plan_id: 'plan-archive',
        trace_id: 'trace-1',
      },
    ],
    scheduled_runs: [
      { id: 'run-1', task_id: 'task-1', status: 'succeeded', started_at: '2026-08-04T12:00:00Z' },
    ],
    probes: [
      { tool_name: 'minio.bucket.capacity.read', operation: 'read', input: { bucket: 'archive' } },
    ],
    runbooks: [
      { slug: 'minio-bucket-capacity', confidence: 0.9, tool_sequence: ['minio.bucket.capacity.read'] },
    ],
    recent_writes: { count: 1, events: [{ id: 'audit-1', tool_name: 'minio.bucket.retention.set', created_at: '2026-08-04T12:05:00Z' }] },
    counts: { audit: 1, scheduled_runs: 1, probes: 1, runbooks: 1, recent_writes: 1 },
  },
};

// 无证据的空全景（异资源）：counts 全 0，alert 缺失。
const emptyPanorama = {
  result: {
    incident_id: '',
    pivot: { domain: 'kafka', resource_type: 'consumer_group', resource_name: 'nope' },
    alert: null,
    timeline: [],
    scheduled_runs: [],
    probes: [],
    runbooks: [],
    recent_writes: { count: 0, events: [] },
    counts: { audit: 0, scheduled_runs: 0, probes: 0, runbooks: 0, recent_writes: 0 },
  },
};

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.startsWith('/v1/tools/incident.view/read')) {
      // 按提交的资源名决定返回全景还是空全景
      return ok(panorama);
    }
    if (url === '/v1/tools/minio.bucket.capacity.read/read') {
      // 只读探测执行：返回一个能力读形态的结果。
      return ok({ result: { kind: 'read', resource: 'minio/archive', severity: 'ok', summary: 'capacity 42%' } });
    }
    return ok({});
  }));
});

async function mountAndRun(fields: Record<string, string>) {
  const wrapper = mount(IncidentView);
  const setField = (selector: string, value: string | undefined) => {
    const el = wrapper.find(selector);
    if (value !== undefined && el.exists()) {
      (el.element as HTMLInputElement).value = value;
      el.trigger('input');
    }
  };
  setField('[data-test="incident-domain"]', fields.domain);
  setField('[data-test="incident-resource-type"]', fields.resource_type);
  setField('[data-test="incident-resource-name"]', fields.resource_name);
  await wrapper.find('[data-test="incident-run"]').trigger('submit');
  await flushPromises();
  return wrapper;
}

describe('IncidentView', () => {
  test('提交后渲染 counts 徽章与各证据块', async () => {
    const wrapper = await mountAndRun({
      domain: 'minio',
      resource_type: 'bucket',
      resource_name: 'archive',
    });

    const counts = wrapper.find('[data-test="incident-counts"]').text();
    expect(counts).toContain('审计事件 1');
    expect(counts).toContain('近期写操作 1');

    // 告警摘要
    expect(wrapper.text()).toContain('bucket capacity over 85%');
    // timeline 项带可跳 plan
    expect(wrapper.find('[data-test="incident-timeline"]').text()).toContain('plan-archive');
    // 巡检 / 探测 / runbook
    expect(wrapper.find('[data-test="incident-runs"]').text()).toContain('task-1');
    expect(wrapper.find('[data-test="incident-probes"]').text()).toContain('minio.bucket.capacity.read');
    expect(wrapper.find('[data-test="incident-runbooks"]').text()).toContain('90%');
    // 近期写操作
    expect(wrapper.find('[data-test="incident-writes"]').text()).toContain('minio.bucket.retention.set');
  });

  test('点击 timeline 的 plan emit jump-to-audit(tool_name)', async () => {
    const wrapper = await mountAndRun({
      domain: 'minio',
      resource_type: 'bucket',
      resource_name: 'archive',
    });

    await wrapper.find('[data-test="incident-plan-jump"]').trigger('click');

    const emitted = wrapper.emitted('jump-to-audit');
    expect(emitted).toBeTruthy();
    expect(emitted?.[0]).toEqual(['minio.bucket.retention.set']);
  });

  test('无证据资源显示空态', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith('/v1/tools/incident.view/read')) {
        return ok(emptyPanorama);
      }
      return ok({});
    }));

    const wrapper = await mountAndRun({ domain: 'kafka', resource_type: 'consumer_group', resource_name: 'nope' });

    const empty = wrapper.find('[data-test="incident-empty"]');
    expect(empty.exists()).toBe(true);
    expect(empty.text()).toContain('未找到该资源的告警证据');
  });

  test('点击探测「执行」内联渲染真实只读结果', async () => {
    const wrapper = await mountAndRun({ domain: 'minio', resource_type: 'bucket', resource_name: 'archive' });

    const runBtn = wrapper.find('[data-test="incident-probe-run"]');
    expect(runBtn.exists()).toBe(true);
    expect(runBtn.text()).toBe('执行');

    await runBtn.trigger('click');
    await flushPromises();

    // 请求打到通用工具读端点
    const calls = (vi.mocked(fetch).mock.calls as [RequestInfo | URL][]).filter((c) =>
      String(c[0]).startsWith('/v1/tools/'),
    );
    expect(calls.some((c) => String(c[0]) === '/v1/tools/minio.bucket.capacity.read/read')).toBe(true);

    // 内联渲染 summary（能力读形态优先）
    const result = wrapper.find('[data-test="incident-probe-result"]');
    expect(result.exists()).toBe(true);
    expect(result.text()).toContain('capacity 42%');
  });

  test('探测请求失败时显示失败文案且不影响整页', async () => {
    // 第二条探测：kafka.consumer_group.lag.read 未注册 → 后端返回 403。
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith('/v1/tools/incident.view/read')) {
        return ok(panorama);
      }
      if (url.startsWith('/v1/tools/')) {
        return new Response(JSON.stringify({ error: 'tool not allowed' }), { status: 403 });
      }
      return ok({});
    }));

    const wrapper = await mountAndRun({ domain: 'minio', resource_type: 'bucket', resource_name: 'archive' });

    await wrapper.find('[data-test="incident-probe-run"]').trigger('click');
    await flushPromises();

    const err = wrapper.find('[data-test="incident-probe-error"]');
    expect(err.exists()).toBe(true);
    expect(err.text()).toContain('该探测当前不可执行');

    // 整页视图仍在（无未捕获错误导致卸载）
    expect(wrapper.find('[data-test="incident-entry"]').exists()).toBe(true);
  });
});
