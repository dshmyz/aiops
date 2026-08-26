import { beforeEach, afterEach, describe, expect, test, vi } from 'vitest';
import {
  countScheduledTaskFailures,
  createScheduledTask,
  deleteScheduledTask,
  getInspectionReport,
  getScheduledTask,
  importOpenAPIURL,
  lastTraceId,
  listExecutions,
  listInspectionReports,
  listScheduledTaskRuns,
  listScheduledTasks,
  publishCapability,
  triggerScheduledTask,
  updateScheduledTask,
} from './api';
import type {
  CreateScheduledTaskPayload,
  ScheduledTask,
  ScheduledTaskRun,
  UpdateScheduledTaskPayload,
} from './types';

function ok(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function noContent(): Response {
  return new Response(null, { status: 204 });
}

const sampleTask: ScheduledTask = {
  id: 'task-1',
  name: 'minio 巡检',
  subject: 'admin-1',
  capability_name: 'minio.bucket.capacity.read',
  input: { cluster: 'm1', bucket: 'archive' },
  schedule_kind: 'preset',
  preset: '5m',
  cron_expr: null,
  timezone: 'Asia/Shanghai',
  enabled: true,
  last_run_at: null,
  last_status: '',
  next_run_at: '2026-07-27T10:05:00Z',
  created_at: '2026-07-27T10:00:00Z',
  updated_at: '2026-07-27T10:00:00Z',
};

const sampleRun: ScheduledTaskRun = {
  id: 'run-1',
  task_id: 'task-1',
  started_at: '2026-07-27T10:00:00Z',
  finished_at: '2026-07-27T10:00:02Z',
  status: 'succeeded',
  result_summary: 'Bucket archive usage is 77%',
  result_data: { usage_pct: 77 },
  error: '',
  audit_event_id: 'audit-1',
};

describe('scheduled tasks API', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  test('listScheduledTasks issues GET /v1/scheduled-tasks without query when no filter', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(ok({ tasks: [sampleTask] }));

    const tasks = await listScheduledTasks();

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [input, init] = fetchMock.mock.calls[0];
    expect(String(input)).toBe('/v1/scheduled-tasks');
    expect(init?.method).toBeUndefined();
    expect(tasks).toEqual([sampleTask]);
  });

  test('listScheduledTasks forwards enabled=true as query parameter', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(ok({ tasks: [sampleTask] }));

    await listScheduledTasks({ enabled: true });

    const [input] = fetchMock.mock.calls[0];
    expect(String(input)).toBe('/v1/scheduled-tasks?enabled=true');
  });

  test('listScheduledTasks forwards enabled=false as query parameter', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(ok({ tasks: [] }));

    await listScheduledTasks({ enabled: false });

    const [input] = fetchMock.mock.calls[0];
    expect(String(input)).toBe('/v1/scheduled-tasks?enabled=false');
  });

  test('getScheduledTask issues GET /v1/scheduled-tasks/{id}', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(ok(sampleTask));

    const task = await getScheduledTask('task-1');

    const [input, init] = fetchMock.mock.calls[0];
    expect(String(input)).toBe('/v1/scheduled-tasks/task-1');
    expect(init?.method).toBeUndefined();
    expect(task).toEqual(sampleTask);
  });

  test('getScheduledTask URL-encodes the task id', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(ok(sampleTask));

    await getScheduledTask('task with space');

    const [input] = fetchMock.mock.calls[0];
    expect(String(input)).toBe('/v1/scheduled-tasks/task%20with%20space');
  });

  test('createScheduledTask issues POST /v1/scheduled-tasks with JSON body', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(ok(sampleTask));

    const payload: CreateScheduledTaskPayload = {
      name: 'minio 巡检',
      capability_name: 'minio.bucket.capacity.read',
      input: { cluster: 'm1', bucket: 'archive' },
      schedule_kind: 'preset',
      preset: '5m',
    };
    const task = await createScheduledTask(payload);

    const [input, init] = fetchMock.mock.calls[0];
    expect(String(input)).toBe('/v1/scheduled-tasks');
    expect(init?.method).toBe('POST');
    expect(JSON.parse(String(init?.body))).toEqual(payload);
    expect(task).toEqual(sampleTask);
  });

  test('updateScheduledTask issues PATCH /v1/scheduled-tasks/{id} with JSON body', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(ok({ ...sampleTask, enabled: false }));

    const payload: UpdateScheduledTaskPayload = {
      name: 'minio 巡检',
      capability_name: 'minio.bucket.capacity.read',
      input: { cluster: 'm1', bucket: 'archive' },
      schedule_kind: 'preset',
      preset: '5m',
      enabled: false,
    };
    const task = await updateScheduledTask('task-1', payload);

    const [input, init] = fetchMock.mock.calls[0];
    expect(String(input)).toBe('/v1/scheduled-tasks/task-1');
    expect(init?.method).toBe('PATCH');
    expect(JSON.parse(String(init?.body))).toEqual(payload);
    expect(task.enabled).toBe(false);
  });

  test('deleteScheduledTask issues DELETE /v1/scheduled-tasks/{id}', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(noContent());

    await deleteScheduledTask('task-1');

    const [input, init] = fetchMock.mock.calls[0];
    expect(String(input)).toBe('/v1/scheduled-tasks/task-1');
    expect(init?.method).toBe('DELETE');
  });

  test('triggerScheduledTask issues POST /v1/scheduled-tasks/{id}/run and returns the run', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(ok(sampleRun));

    const run = await triggerScheduledTask('task-1');

    const [input, init] = fetchMock.mock.calls[0];
    expect(String(input)).toBe('/v1/scheduled-tasks/task-1/run');
    expect(init?.method).toBe('POST');
    expect(run).toEqual(sampleRun);
  });

  test('listScheduledTaskRuns issues GET /v1/scheduled-tasks/{id}/runs', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(ok({ runs: [sampleRun] }));

    const runs = await listScheduledTaskRuns('task-1');

    const [input, init] = fetchMock.mock.calls[0];
    expect(String(input)).toBe('/v1/scheduled-tasks/task-1/runs');
    expect(init?.method).toBeUndefined();
    expect(runs).toEqual([sampleRun]);
  });

  test('listScheduledTaskRuns forwards limit as query parameter', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(ok({ runs: [sampleRun] }));

    await listScheduledTaskRuns('task-1', 10);

    const [input] = fetchMock.mock.calls[0];
    expect(String(input)).toBe('/v1/scheduled-tasks/task-1/runs?limit=10');
  });

  test('countScheduledTaskFailures issues GET /v1/scheduled-tasks/failures/count and returns count', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(ok({ count: 7 }));

    const count = await countScheduledTaskFailures();

    const [input, init] = fetchMock.mock.calls[0];
    expect(String(input)).toBe('/v1/scheduled-tasks/failures/count');
    expect(init?.method).toBeUndefined();
    expect(count).toBe(7);
  });

  test('createScheduledTask surfaces API error message on 400', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'cron_expr is invalid' }), {
        status: 400,
        headers: { 'Content-Type': 'application/json' },
      }),
    );

    await expect(
      createScheduledTask({
        name: 'bad',
        capability_name: 'cap',
        input: {},
        schedule_kind: 'cron',
        cron_expr: 'bad-expr',
      }),
    ).rejects.toThrow('cron_expr is invalid');
  });

  test('listScheduledTasks surfaces API error message on 503', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'service unavailable' }), {
        status: 503,
        headers: { 'Content-Type': 'application/json' },
      }),
    );

    await expect(listScheduledTasks()).rejects.toThrow('service unavailable');
  });
});

describe('publishCapability conflict mapping', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  function conflictResponse(message: string): Response {
    return new Response(JSON.stringify({ error: message }), {
      status: 409,
      headers: { 'Content-Type': 'application/json' },
    });
  }

  test('maps static-tool conflict to friendly Chinese message', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(
      conflictResponse('capability name conflict: "cluster.status.read" conflicts with an existing tool'),
    );

    await expect(publishCapability('cluster.status.read')).rejects.toThrow(
      '能力名称「cluster.status.read」与内置工具冲突，请修改名称后重试',
    );
  });

  test('maps already-published conflict to friendly Chinese message', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(
      conflictResponse('capability name conflict: "redis.cluster.info.read" is already published, unpublish the old version first'),
    );

    await expect(publishCapability('redis.cluster.info.read')).rejects.toThrow(
      '能力「redis.cluster.info.read」已发布，请先下线旧版本',
    );
  });

  test('maps draft-exists conflict to friendly Chinese message', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(
      conflictResponse('capability name conflict: "glusterfs.volume.status.read" already exists as a draft, remove the draft first'),
    );

    await expect(publishCapability('glusterfs.volume.status.read')).rejects.toThrow(
      '能力「glusterfs.volume.status.read」已有草稿，请先删除草稿再下线',
    );
  });

  test('maps unknown 409 conflict to generic Chinese message', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(
      conflictResponse('capability name conflict: some unexpected reason'),
    );

    await expect(publishCapability('weird.name.read')).rejects.toThrow(
      '能力名称「weird.name.read」冲突，请修改名称或下线同名能力',
    );
  });
});

describe('traceparent header extraction', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn());
    lastTraceId.value = null;
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  function okWithTrace(body: unknown, traceparent: string, status = 200): Response {
    return new Response(JSON.stringify(body), {
      status,
      headers: {
        'Content-Type': 'application/json',
        traceparent,
      },
    });
  }

  test('extracts the trace-id from a W3C traceparent header into lastTraceId', async () => {
    const fetchMock = vi.mocked(fetch);
    // W3C format: version-trace_id-span_id-flags
    fetchMock.mockResolvedValueOnce(
      okWithTrace(sampleTask, '00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01'),
    );

    const task = await getScheduledTask('task-1');

    // Response body is still returned unchanged (backward compatible).
    expect(task).toEqual(sampleTask);
    expect(lastTraceId.value).toBe('4bf92f3577b34da6a3ce929d0e0e4736');
  });

  test('falls back to x-trace-id header when traceparent is absent', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify(sampleTask), {
        status: 200,
        headers: {
          'Content-Type': 'application/json',
          'x-trace-id': 'fallback-trace-123',
        },
      }),
    );

    await getScheduledTask('task-1');

    expect(lastTraceId.value).toBe('fallback-trace-123');
  });

  test('leaves lastTraceId null when no trace header is present', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(ok(sampleTask));

    await getScheduledTask('task-1');

    expect(lastTraceId.value).toBeNull();
  });

  test('records trace-id even when the response is an error', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'bad request' }), {
        status: 400,
        headers: {
          'Content-Type': 'application/json',
          traceparent: '00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01',
        },
      }),
    );

    await expect(getScheduledTask('task-1')).rejects.toThrow('bad request');
    expect(lastTraceId.value).toBe('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa');
  });

  test('keeps the existing response body contract alongside trace capture', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(
      okWithTrace(
        { runs: [sampleRun] },
        '00-cccccccccccccccccccccccccccccccc-dddddddddddddddd-01',
      ),
    );

    const runs = await listScheduledTaskRuns('task-1');

    expect(runs).toEqual([sampleRun]);
    expect(lastTraceId.value).toBe('cccccccccccccccccccccccccccccccc');
  });
});

describe('openapi direct import API', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  test('POSTs openapi_url + backend_base_url to the import endpoint and maps capabilities', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(
      ok({
        capabilities: [
          {
            name: 'minio.bucket.capacity.read',
            domain: 'minio',
            resource_type: 'bucket',
            backend: { method: 'GET', path: '/api/minio/{cluster}/buckets/{bucket}/capacity' },
          },
        ],
      }),
    );

    const caps = await importOpenAPIURL({
      openapi_url: 'https://admin.example.com/v3/api-docs',
      backend_base_url: 'https://middleware.example.com',
    });

    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe('/v1/capabilities/import/openapi-url');
    expect(init?.method).toBe('POST');
    expect(JSON.parse(String(init?.body))).toEqual({
      openapi_url: 'https://admin.example.com/v3/api-docs',
      backend_base_url: 'https://middleware.example.com',
    });
    expect(caps.length).toBe(1);
    expect(caps[0].name).toBe('minio.bucket.capacity.read');
    expect(caps[0].domain).toBe('minio');
    // path 在 Capability 里指后端 HTTP 路径（backend.path），不存在顶层
    // openapi-url:// 伪 URI 约定，这里校验归一化后的真实 operation 路径。
    expect(caps[0].backend?.path).toBe('/api/minio/{cluster}/buckets/{bucket}/capacity');
  });

  test('returns an empty list when the response has no capabilities', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(ok({ capabilities: [] }));

    const caps = await importOpenAPIURL({
      openapi_url: 'https://admin.example.com/v3/api-docs',
      backend_base_url: 'https://middleware.example.com',
    });

    expect(caps).toEqual([]);
  });
});

describe('executions API', () => {
  const sampleExecution = {
    id: 'exec-1',
    action_plan_id: 'plan-1',
    status: 'succeeded',
    tool_name: 'kafka.topic.retention.set',
    result_summary: { ok: true },
    started_at: '2026-07-27T10:00:00Z',
    completed_at: '2026-07-27T10:00:02Z',
    created_at: '2026-07-27T10:00:02Z',
  };

  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  test('listExecutions issues GET /v1/executions when no filter', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(ok({ executions: [sampleExecution] }));

    const page = await listExecutions();

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [input, init] = fetchMock.mock.calls[0];
    expect(String(input)).toBe('/v1/executions');
    expect(init?.method).toBeUndefined();
    expect(page.executions).toEqual([sampleExecution]);
  });

  test('listExecutions forwards status/tool/action_plan_id/limit as query parameters', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(ok({ executions: [] }));

    await listExecutions({ status: 'failed', tool: 'minio.bucket.capacity.read', action_plan_id: 'plan-9', limit: 25 });

    const [input] = fetchMock.mock.calls[0];
    expect(String(input)).toBe(
      '/v1/executions?status=failed&action_plan_id=plan-9&tool=minio.bucket.capacity.read&limit=25',
    );
  });

  test('listExecutions forwards time filters and cursor as RFC3339 / params', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(
      ok({
        executions: [sampleExecution],
        next_cursor: { created_at: '2026-07-27T10:00:02Z', id: 'exec-1' },
      }),
    );

    const page = await listExecutions({
      started_after: '2026-07-27T09:00:00Z',
      started_before: '2026-07-27T11:00:00Z',
      cursor_created_at: '2026-07-27T10:00:02Z',
      cursor_id: 'exec-1',
    });

    const [input] = fetchMock.mock.calls[0];
    expect(String(input)).toBe(
      '/v1/executions?started_after=2026-07-27T09%3A00%3A00Z&started_before=2026-07-27T11%3A00%3A00Z&cursor_created_at=2026-07-27T10%3A00%3A02Z&cursor_id=exec-1',
    );
    expect(page.next_cursor).toEqual({ created_at: '2026-07-27T10:00:02Z', id: 'exec-1' });
  });

  test('listExecutions surfaces backend errors', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'unsupported query parameter: foo' }), {
        status: 400,
        headers: { 'Content-Type': 'application/json' },
      }),
    );

    await expect(listExecutions({ tool: 'x' })).rejects.toThrow('unsupported query parameter: foo');
  });
});

describe('inspection reports API', () => {
  const sampleReport = {
    id: 'report-1',
    period: 'daily',
    window_start: '2026-07-27T00:00:00Z',
    window_end: '2026-07-27T23:59:59Z',
    generated_at: '2026-07-28T00:05:00Z',
    total_tasks: 2,
    succeeded_tasks: 1,
    failed_tasks: 1,
    task_summaries: [
      {
        task_id: 'task-1',
        task_name: 'minio 巡检',
        capability_name: 'minio.bucket.capacity.read',
        total_runs: 12,
        succeeded_runs: 11,
        failed_runs: 1,
        last_status: 'failed',
      },
    ],
    html_content: '<p>ok</p>',
  };

  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  test('listInspectionReports issues GET /v1/inspection-reports?limit= and returns a bare array', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(ok([sampleReport]));

    const reports = await listInspectionReports(25);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [input, init] = fetchMock.mock.calls[0];
    expect(String(input)).toBe('/v1/inspection-reports?limit=25');
    expect(init?.method).toBeUndefined();
    expect(reports).toEqual([sampleReport]);
  });

  test('listInspectionReports defaults to 50 when no limit given', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(ok([]));

    await listInspectionReports();

    const [input] = fetchMock.mock.calls[0];
    expect(String(input)).toBe('/v1/inspection-reports?limit=50');
  });

  test('getInspectionReport issues GET /v1/inspection-reports/{id}', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(ok(sampleReport));

    const report = await getInspectionReport('report-1');

    const [input, init] = fetchMock.mock.calls[0];
    expect(String(input)).toBe('/v1/inspection-reports/report-1');
    expect(init?.method).toBeUndefined();
    expect(report.id).toBe('report-1');
  });

  test('getInspectionReport surfaces 404 as APIError with backend message', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'inspection report not found' }), {
        status: 404,
        headers: { 'Content-Type': 'application/json' },
      }),
    );

    await expect(getInspectionReport('nope')).rejects.toThrow('inspection report not found');
  });
});
