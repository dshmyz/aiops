import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import ScheduledTaskList from './ScheduledTaskList.vue';
import type { ScheduledTask } from '../types';

function makeTask(overrides: Partial<ScheduledTask> = {}): ScheduledTask {
  return {
    id: 'task-1',
    name: 'minio 巡检',
    subject: 'admin-1',
    capability_name: 'minio.bucket.capacity.read',
    input: { environment: 'prod' },
    schedule_kind: 'preset',
    preset: 'daily',
    cron_expr: null,
    timezone: 'Asia/Shanghai',
    enabled: true,
    last_run_at: '2026-07-27T10:00:00Z',
    last_status: 'succeeded',
    next_run_at: '2026-07-28T00:00:00Z',
    created_at: '2026-07-27T10:00:00Z',
    updated_at: '2026-07-27T10:00:00Z',
    ...overrides,
  };
}

describe('ScheduledTaskList', () => {
  test('渲染任务列表（name / capability / 下次执行 / 上次状态）', () => {
    const tasks = [
      makeTask(),
      makeTask({
        id: 'task-2',
        name: 'kafka 巡检',
        capability_name: 'kafka.topic.lag.read',
        last_status: 'failed',
        next_run_at: '2026-07-28T01:00:00Z',
      }),
    ];
    const wrapper = mount(ScheduledTaskList, { props: { tasks } });

    expect(wrapper.find('[data-test="scheduled-task-list"]').exists()).toBe(true);
    const rows = wrapper.findAll('[data-test="scheduled-task-row"]');
    expect(rows).toHaveLength(2);
    expect(rows[0].text()).toContain('minio 巡检');
    expect(rows[0].text()).toContain('minio.bucket.capacity.read');
    expect(rows[0].text()).toContain('2026-07-28T00:00:00Z');
    expect(rows[0].text()).toContain('succeeded');
  });

  test('上次状态 succeeded 显示绿色，failed 显示红色，空显示灰色', () => {
    const tasks = [
      makeTask({ id: 't1', last_status: 'succeeded' }),
      makeTask({ id: 't2', last_status: 'failed' }),
      makeTask({ id: 't3', last_status: '' }),
    ];
    const wrapper = mount(ScheduledTaskList, { props: { tasks } });

    const rows = wrapper.findAll('[data-test="scheduled-task-row"]');
    const status1 = rows[0].find('[data-test="scheduled-task-status"]');
    const status2 = rows[1].find('[data-test="scheduled-task-status"]');
    const status3 = rows[2].find('[data-test="scheduled-task-status"]');

    expect(status1.classes()).toContain('status-succeeded');
    expect(status2.classes()).toContain('status-failed');
    expect(status3.classes()).toContain('status-empty');
  });

  test('enabled 开关：点击 emit toggle-enabled', async () => {
    const tasks = [makeTask({ id: 'task-1', enabled: true })];
    const wrapper = mount(ScheduledTaskList, { props: { tasks } });

    const toggle = wrapper.find('[data-test="scheduled-task-toggle"]');
    expect((toggle.element as HTMLInputElement).checked).toBe(true);

    await toggle.setValue(false);

    const events = wrapper.emitted('toggle-enabled');
    expect(events).toBeDefined();
    expect(events?.[0]).toEqual(['task-1', false]);
  });

  test('立即运行按钮 emit trigger', async () => {
    const tasks = [makeTask({ id: 'task-1' })];
    const wrapper = mount(ScheduledTaskList, { props: { tasks } });

    await wrapper.find('[data-test="scheduled-task-trigger"]').trigger('click');

    const events = wrapper.emitted('trigger');
    expect(events).toBeDefined();
    expect(events?.[0]).toEqual(['task-1']);
  });

  test('编辑按钮 emit edit', async () => {
    const task = makeTask({ id: 'task-1' });
    const wrapper = mount(ScheduledTaskList, { props: { tasks: [task] } });

    await wrapper.find('[data-test="scheduled-task-edit"]').trigger('click');

    const events = wrapper.emitted('edit');
    expect(events).toBeDefined();
    expect(events?.[0]).toEqual([task]);
  });

  test('删除按钮 emit delete', async () => {
    const tasks = [makeTask({ id: 'task-1' })];
    const wrapper = mount(ScheduledTaskList, { props: { tasks } });

    await wrapper.find('[data-test="scheduled-task-delete"]').trigger('click');

    const events = wrapper.emitted('delete');
    expect(events).toBeDefined();
    expect(events?.[0]).toEqual(['task-1']);
  });

  test('空列表显示「暂无定时任务」', () => {
    const wrapper = mount(ScheduledTaskList, { props: { tasks: [] } });

    expect(wrapper.find('[data-test="scheduled-task-empty"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="scheduled-task-empty"]').text()).toContain('暂无定时任务');
    expect(wrapper.findAll('[data-test="scheduled-task-row"]')).toHaveLength(0);
  });

  test('schedule 描述：preset 模式显示 preset 标签', () => {
    const tasks = [makeTask({ schedule_kind: 'preset', preset: 'daily', cron_expr: null })];
    const wrapper = mount(ScheduledTaskList, { props: { tasks } });

    const row = wrapper.find('[data-test="scheduled-task-row"]');
    expect(row.text()).toContain('每天');
  });

  test('schedule 描述：cron 模式显示 cron 表达式', () => {
    const tasks = [
      makeTask({ schedule_kind: 'cron', preset: null, cron_expr: '0 2 * * 1-5' }),
    ];
    const wrapper = mount(ScheduledTaskList, { props: { tasks } });

    const row = wrapper.find('[data-test="scheduled-task-row"]');
    expect(row.text()).toContain('0 2 * * 1-5');
  });

  test('disabled 任务的开关为关闭状态', () => {
    const tasks = [makeTask({ id: 'task-1', enabled: false })];
    const wrapper = mount(ScheduledTaskList, { props: { tasks } });

    const toggle = wrapper.find('[data-test="scheduled-task-toggle"]');
    expect((toggle.element as HTMLInputElement).checked).toBe(false);
  });
});
