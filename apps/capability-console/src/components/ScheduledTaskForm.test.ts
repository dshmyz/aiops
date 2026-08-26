import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import ScheduledTaskForm from './ScheduledTaskForm.vue';
import type { ManagedCapability, Runbook, ScheduledTask } from '../types';

// 构造只读 capability 列表，过滤逻辑由父组件传入，这里直接构造已过滤的列表。
function makeCapabilities(): ManagedCapability[] {
  const base = {
    schema_version: 1,
    status: 'published' as const,
    source: 'published' as const,
    risk: 'low' as const,
    backend: {
      adapter: 'http',
      method: 'GET',
      base_url: 'https://middleware.example.com',
      path: '/api/minio/{cluster}/buckets/{bucket}/capacity',
      timeout_ms: 3000,
    },
    input_schema: {
      cluster: { type: 'string' as const, required: true },
      bucket: { type: 'string' as const, required: true },
    },
    output: {
      kind: 'observation',
      severity_path: '$.status',
      summary_template: 'Bucket {bucket} usage is {usage_pct}%',
      fields: { usage_pct: '$.data.usage_pct' },
    },
    auth: { roles: ['viewer', 'operator', 'admin'] },
    ai: { description: 'read minio bucket capacity', examples: [] },
    validation: { valid: true },
  };
  return [
    { ...base, name: 'minio.bucket.capacity.read', domain: 'minio', resource_type: 'bucket', operation: 'read' as const },
    { ...base, name: 'kafka.topic.lag.read', domain: 'kafka', resource_type: 'topic', operation: 'read' as const },
  ];
}

function makeTask(overrides: Partial<ScheduledTask> = {}): ScheduledTask {
  return {
    id: 'task-1',
    name: 'minio 巡检',
    subject: 'admin-1',
    capability_name: 'minio.bucket.capacity.read',
    input: { cluster: 'm1', bucket: 'archive' },
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
    ...overrides,
  };
}

function makeRunbooks(): Runbook[] {
  return [
    {
      id: 'rb-1',
      slug: 'minio-retention-set-low',
      name: 'MinIO 保留期设置（低风险）',
      intent_pattern: ['设置保留期'],
      tool_sequence: ['bucket.retention.set'],
      risk_level: 'low',
      is_builtin: true,
      is_enabled: true,
    },
  ];
}

describe('ScheduledTaskForm', () => {
  test('创建模式：渲染空表单 + 提交按钮 disabled', () => {
    const wrapper = mount(ScheduledTaskForm, {
      props: { capabilities: makeCapabilities() },
    });

    expect(wrapper.find('[data-test="scheduled-task-form"]').exists()).toBe(true);
    expect((wrapper.find('[data-test="scheduled-task-name"]').element as HTMLInputElement).value).toBe('');
    expect((wrapper.find('[data-test="scheduled-task-capability"]').element as HTMLSelectElement).value).toBe('');
    expect((wrapper.find('[data-test="scheduled-task-input"]').element as HTMLTextAreaElement).value).toBe('');
    expect(wrapper.find('[data-test="scheduled-task-submit"]').attributes('disabled')).toBeDefined();
  });

  test('创建模式：填齐 preset 字段后提交按钮 enabled 并 emit submit 正确 payload', async () => {
    const wrapper = mount(ScheduledTaskForm, {
      props: { capabilities: makeCapabilities() },
    });

    await wrapper.find('[data-test="scheduled-task-name"]').setValue('minio 每日巡检');
    await wrapper.find('[data-test="scheduled-task-capability"]').setValue('minio.bucket.capacity.read');
    await wrapper.find('[data-test="scheduled-task-input"]').setValue('{"cluster":"m1","bucket":"archive"}');
    // 默认 schedule_kind=preset，preset=null，需要选一个 preset
    expect(wrapper.find('[data-test="schedule-preset-picker"]').exists()).toBe(true);
    await wrapper.find('[data-test="schedule-preset-option"][data-preset="daily"]').trigger('click');

    expect(wrapper.find('[data-test="scheduled-task-submit"]').attributes('disabled')).toBeUndefined();

    await wrapper.find('[data-test="scheduled-task-submit"]').trigger('click');

    const events = wrapper.emitted('submit');
    expect(events).toBeDefined();
    expect(events?.[0]?.[0]).toEqual({
      name: 'minio 每日巡检',
      capability_name: 'minio.bucket.capacity.read',
      run_kind: 'read',
      runbook_slug: null,
      input: { cluster: 'm1', bucket: 'archive' },
      schedule_kind: 'preset',
      preset: 'daily',
      cron_expr: null,
    });
  });

  test('创建模式：cron 表达式非法时提交按钮 disabled，合法后 enabled', async () => {
    const wrapper = mount(ScheduledTaskForm, {
      props: { capabilities: makeCapabilities() },
    });

    await wrapper.find('[data-test="scheduled-task-name"]').setValue('cron 巡检');
    await wrapper.find('[data-test="scheduled-task-capability"]').setValue('minio.bucket.capacity.read');
    await wrapper.find('[data-test="scheduled-task-input"]').setValue('{"cluster":"m1","bucket":"archive"}');
    // 切换到 cron 模式
    await wrapper.find('[data-test="scheduled-task-schedule-kind"]').setValue('cron');
    expect(wrapper.find('[data-test="schedule-cron-input-wrapper"]').exists()).toBe(true);

    // 默认 cron_expr 为空 → 非法
    expect(wrapper.find('[data-test="scheduled-task-submit"]').attributes('disabled')).toBeDefined();

    // 输入非法 cron
    await wrapper.find('[data-test="schedule-cron-input"]').setValue('not a cron');
    expect(wrapper.find('[data-test="scheduled-task-submit"]').attributes('disabled')).toBeDefined();

    // 输入合法 cron
    await wrapper.find('[data-test="schedule-cron-input"]').setValue('0 2 * * 1-5');
    expect(wrapper.find('[data-test="scheduled-task-submit"]').attributes('disabled')).toBeUndefined();
  });

  test('cron 模式提交 emit 正确 payload（preset=null, cron_expr=表达式）', async () => {
    const wrapper = mount(ScheduledTaskForm, {
      props: { capabilities: makeCapabilities() },
    });

    await wrapper.find('[data-test="scheduled-task-name"]').setValue('cron 巡检');
    await wrapper.find('[data-test="scheduled-task-capability"]').setValue('kafka.topic.lag.read');
    await wrapper.find('[data-test="scheduled-task-input"]').setValue('{"cluster":"m1","bucket":"archive"}');
    await wrapper.find('[data-test="scheduled-task-schedule-kind"]').setValue('cron');
    await wrapper.find('[data-test="schedule-cron-input"]').setValue('0 2 * * 1-5');

    await wrapper.find('[data-test="scheduled-task-submit"]').trigger('click');

    const events = wrapper.emitted('submit');
    expect(events).toBeDefined();
    expect(events?.[0]?.[0]).toEqual({
      name: 'cron 巡检',
      capability_name: 'kafka.topic.lag.read',
      run_kind: 'read',
      runbook_slug: null,
      input: { cluster: 'm1', bucket: 'archive' },
      schedule_kind: 'cron',
      preset: null,
      cron_expr: '0 2 * * 1-5',
    });
  });

  test('编辑模式：传入 task prop 时字段预填', () => {
    const task = makeTask();
    const wrapper = mount(ScheduledTaskForm, {
      props: { capabilities: makeCapabilities(), task },
    });

    expect((wrapper.find('[data-test="scheduled-task-name"]').element as HTMLInputElement).value).toBe('minio 巡检');
    expect((wrapper.find('[data-test="scheduled-task-capability"]').element as HTMLSelectElement).value).toBe('minio.bucket.capacity.read');
    expect((wrapper.find('[data-test="scheduled-task-input"]').element as HTMLTextAreaElement).value).toBe(
      JSON.stringify({ cluster: 'm1', bucket: 'archive' }, null, 2),
    );
    expect((wrapper.find('[data-test="scheduled-task-schedule-kind"]').element as HTMLSelectElement).value).toBe('preset');
    // preset 模式下高亮 daily
    const activePreset = wrapper.find('[data-test="schedule-preset-option"].active');
    expect(activePreset.exists()).toBe(true);
    expect(activePreset.attributes('data-preset')).toBe('daily');
  });

  test('编辑模式：cron 任务预填 cron_expr', () => {
    const task = makeTask({
      schedule_kind: 'cron',
      preset: null,
      cron_expr: '0 2 * * 1-5',
    });
    const wrapper = mount(ScheduledTaskForm, {
      props: { capabilities: makeCapabilities(), task },
    });

    expect((wrapper.find('[data-test="scheduled-task-schedule-kind"]').element as HTMLSelectElement).value).toBe('cron');
    expect(wrapper.find('[data-test="schedule-cron-input-wrapper"]').exists()).toBe(true);
    expect((wrapper.find('[data-test="schedule-cron-input"]').element as HTMLTextAreaElement).value).toBe('0 2 * * 1-5');
  });

  test('schedule_kind 切换：preset → cron，子组件切换', async () => {
    const wrapper = mount(ScheduledTaskForm, {
      props: { capabilities: makeCapabilities() },
    });

    expect(wrapper.find('[data-test="schedule-preset-picker"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="schedule-cron-input-wrapper"]').exists()).toBe(false);

    await wrapper.find('[data-test="scheduled-task-schedule-kind"]').setValue('cron');

    expect(wrapper.find('[data-test="schedule-preset-picker"]').exists()).toBe(false);
    expect(wrapper.find('[data-test="schedule-cron-input-wrapper"]').exists()).toBe(true);
  });

  test('取消按钮 emit cancel 事件', async () => {
    const wrapper = mount(ScheduledTaskForm, {
      props: { capabilities: makeCapabilities() },
    });

    await wrapper.find('[data-test="scheduled-task-cancel"]').trigger('click');

    expect(wrapper.emitted('cancel')).toBeDefined();
    expect(wrapper.emitted('cancel')?.[0]).toEqual([]);
  });

  test('name 为空时提交按钮 disabled', async () => {
    const wrapper = mount(ScheduledTaskForm, {
      props: { capabilities: makeCapabilities() },
    });

    await wrapper.find('[data-test="scheduled-task-capability"]').setValue('minio.bucket.capacity.read');
    await wrapper.find('[data-test="scheduled-task-input"]').setValue('{"cluster":"m1","bucket":"archive"}');
    await wrapper.find('[data-test="schedule-preset-option"][data-preset="daily"]').trigger('click');

    // name 仍为空
    expect(wrapper.find('[data-test="scheduled-task-submit"]').attributes('disabled')).toBeDefined();
  });

  test('capability 为空时提交按钮 disabled', async () => {
    const wrapper = mount(ScheduledTaskForm, {
      props: { capabilities: makeCapabilities() },
    });

    await wrapper.find('[data-test="scheduled-task-name"]').setValue('minio 巡检');
    await wrapper.find('[data-test="scheduled-task-input"]').setValue('{"cluster":"m1","bucket":"archive"}');
    await wrapper.find('[data-test="schedule-preset-option"][data-preset="daily"]').trigger('click');

    // capability 仍为空
    expect(wrapper.find('[data-test="scheduled-task-submit"]').attributes('disabled')).toBeDefined();
  });

  test('preset 模式未选 preset 时提交按钮 disabled', async () => {
    const wrapper = mount(ScheduledTaskForm, {
      props: { capabilities: makeCapabilities() },
    });

    await wrapper.find('[data-test="scheduled-task-name"]').setValue('minio 巡检');
    await wrapper.find('[data-test="scheduled-task-capability"]').setValue('minio.bucket.capacity.read');
    await wrapper.find('[data-test="scheduled-task-input"]').setValue('{"cluster":"m1","bucket":"archive"}');
    // preset 模式但未选 preset
    expect(wrapper.find('[data-test="scheduled-task-submit"]').attributes('disabled')).toBeDefined();
  });

  test('input JSON 非法时提交按钮 disabled', async () => {
    const wrapper = mount(ScheduledTaskForm, {
      props: { capabilities: makeCapabilities() },
    });

    await wrapper.find('[data-test="scheduled-task-name"]').setValue('minio 巡检');
    await wrapper.find('[data-test="scheduled-task-capability"]').setValue('minio.bucket.capacity.read');
    await wrapper.find('[data-test="scheduled-task-input"]').setValue('not a json');
    await wrapper.find('[data-test="schedule-preset-option"][data-preset="daily"]').trigger('click');

    expect(wrapper.find('[data-test="scheduled-task-submit"]').attributes('disabled')).toBeDefined();
  });

  test('capabilities 列表渲染为 select 选项', () => {
    const wrapper = mount(ScheduledTaskForm, {
      props: { capabilities: makeCapabilities() },
    });

    const options = wrapper.find('[data-test="scheduled-task-capability"]').findAll('option');
    // 第一个是占位 "请选择能力"，后两个是 capability
    expect(options).toHaveLength(3);
    expect(options[1].attributes('value')).toBe('minio.bucket.capacity.read');
    expect(options[2].attributes('value')).toBe('kafka.topic.lag.read');
  });

  test('runbook 模式：默认只读，切到 runbook 显示模板下拉并隐藏 capability 下拉', async () => {
    const wrapper = mount(ScheduledTaskForm, {
      props: { capabilities: makeCapabilities(), runbooks: makeRunbooks() },
    });

    // 默认 run_kind=read → capability 下拉可见、runbook 下拉不可见
    expect(wrapper.find('[data-test="scheduled-task-capability"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="scheduled-task-runbook"]').exists()).toBe(false);

    await wrapper.find('[data-test="scheduled-task-run-kind"]').setValue('runbook');

    expect(wrapper.find('[data-test="scheduled-task-capability"]').exists()).toBe(false);
    expect(wrapper.find('[data-test="scheduled-task-runbook"]').exists()).toBe(true);
  });

  test('runbook 模式：选模板后提交 run_kind=runbook + runbook_slug', async () => {
    const wrapper = mount(ScheduledTaskForm, {
      props: { capabilities: makeCapabilities(), runbooks: makeRunbooks() },
    });

    await wrapper.find('[data-test="scheduled-task-name"]').setValue('minio 保留期定时设置');
    await wrapper.find('[data-test="scheduled-task-run-kind"]').setValue('runbook');
    await wrapper.find('[data-test="scheduled-task-runbook"]').setValue('minio-retention-set-low');
    await wrapper.find('[data-test="scheduled-task-input"]').setValue('{"cluster":"m1","bucket":"archive"}');
    await wrapper.find('[data-test="schedule-preset-option"][data-preset="daily"]').trigger('click');

    expect(wrapper.find('[data-test="scheduled-task-submit"]').attributes('disabled')).toBeUndefined();

    await wrapper.find('[data-test="scheduled-task-submit"]').trigger('click');

    const events = wrapper.emitted('submit');
    expect(events).toBeDefined();
    expect(events?.[0]?.[0]).toEqual({
      name: 'minio 保留期定时设置',
      capability_name: '',
      run_kind: 'runbook',
      runbook_slug: 'minio-retention-set-low',
      input: { cluster: 'm1', bucket: 'archive' },
      schedule_kind: 'preset',
      preset: 'daily',
      cron_expr: null,
    });
  });

  test('runbook 模式：未选模板时提交按钮 disabled（切回 read 清空 slug）', async () => {
    const wrapper = mount(ScheduledTaskForm, {
      props: { capabilities: makeCapabilities(), runbooks: makeRunbooks() },
    });

    await wrapper.find('[data-test="scheduled-task-name"]').setValue('minio 保留期定时设置');
    await wrapper.find('[data-test="scheduled-task-run-kind"]').setValue('runbook');
    await wrapper.find('[data-test="scheduled-task-input"]').setValue('{"cluster":"m1","bucket":"archive"}');
    await wrapper.find('[data-test="schedule-preset-option"][data-preset="daily"]').trigger('click');

    // 未选 runbook → disabled
    expect(wrapper.find('[data-test="scheduled-task-submit"]').attributes('disabled')).toBeDefined();

    // 选模板后 enabled，再切回 read 应清空 slug
    await wrapper.find('[data-test="scheduled-task-runbook"]').setValue('minio-retention-set-low');
    expect(wrapper.find('[data-test="scheduled-task-submit"]').attributes('disabled')).toBeUndefined();
    await wrapper.find('[data-test="scheduled-task-run-kind"]').setValue('read');
    expect(wrapper.find('[data-test="scheduled-task-capability"]').exists()).toBe(true);
  });

  test('没有可用 runbook 模板时 run_kind 下拉禁用', () => {
    const wrapper = mount(ScheduledTaskForm, {
      props: { capabilities: makeCapabilities() },
    });

    const runKindSelect = wrapper.find('[data-test="scheduled-task-run-kind"]').element as HTMLSelectElement;
    expect(runKindSelect.disabled).toBe(true);
  });

  test('runbook 模式：列表渲染为模板选项 + 编辑模式预填', () => {
    const task = makeTask({
      capability_name: '',
      run_kind: 'runbook',
      runbook_slug: 'minio-retention-set-low',
    } as Partial<ScheduledTask>);
    const wrapper = mount(ScheduledTaskForm, {
      props: { capabilities: makeCapabilities(), runbooks: makeRunbooks(), task },
    });

    // 编辑模式预填 runbook
    expect(wrapper.find('[data-test="scheduled-task-runbook"]').exists()).toBe(true);
    expect((wrapper.find('[data-test="scheduled-task-runbook"]').element as HTMLSelectElement).value).toBe('minio-retention-set-low');
    expect(wrapper.find('[data-test="scheduled-task-capability"]').exists()).toBe(false);
    const options = wrapper.find('[data-test="scheduled-task-runbook"]').findAll('option');
    // 占位 + 1 个模板
    expect(options).toHaveLength(2);
  });
});
