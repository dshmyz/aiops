import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import ScheduledTaskRunHistory from './ScheduledTaskRunHistory.vue';
import type { ScheduledTaskRun } from '../types';

function makeRun(overrides: Partial<ScheduledTaskRun> = {}): ScheduledTaskRun {
  return {
    id: 'run-1',
    task_id: 'task-1',
    started_at: '2026-07-27T10:00:00Z',
    finished_at: '2026-07-27T10:00:05Z',
    status: 'succeeded',
    result_summary: 'Bucket archive usage is 77%',
    result_data: { usage_pct: 77 },
    error: '',
    audit_event_id: 'audit-1',
    ...overrides,
  };
}

describe('ScheduledTaskRunHistory', () => {
  test('渲染执行历史列表（开始时间 / 状态 / 耗时 / 结果摘要）', () => {
    const runs = [
      makeRun(),
      makeRun({
        id: 'run-2',
        started_at: '2026-07-27T11:00:00Z',
        finished_at: '2026-07-27T11:00:10Z',
        result_summary: '另一个结果',
      }),
    ];
    const wrapper = mount(ScheduledTaskRunHistory, { props: { runs } });

    expect(wrapper.find('[data-test="scheduled-task-run-history"]').exists()).toBe(true);
    const rows = wrapper.findAll('[data-test="scheduled-task-run-row"]');
    expect(rows).toHaveLength(2);
    expect(rows[0].text()).toContain('2026-07-27T10:00:00Z');
    expect(rows[0].text()).toContain('succeeded');
    expect(rows[0].text()).toContain('Bucket archive usage is 77%');
  });

  test('计算耗时（finished_at - started_at）', () => {
    const runs = [
      makeRun({
        started_at: '2026-07-27T10:00:00Z',
        finished_at: '2026-07-27T10:00:05Z',
      }),
    ];
    const wrapper = mount(ScheduledTaskRunHistory, { props: { runs } });

    const row = wrapper.find('[data-test="scheduled-task-run-row"]');
    // 5 秒 = 5000 ms
    expect(row.text()).toContain('5');
  });

  test('failed 行红色高亮 + error tooltip', () => {
    const runs = [
      makeRun({
        id: 'run-fail',
        status: 'failed',
        error: 'connection refused',
        result_summary: '',
      }),
    ];
    const wrapper = mount(ScheduledTaskRunHistory, { props: { runs } });

    const row = wrapper.find('[data-test="scheduled-task-run-row"]');
    expect(row.classes()).toContain('run-failed');
    expect(row.text()).toContain('connection refused');
  });

  test('点击行展开 result_data (JSON)', async () => {
    const runs = [
      makeRun({
        result_data: { usage_pct: 77, cluster: 'm1' },
      }),
    ];
    const wrapper = mount(ScheduledTaskRunHistory, { props: { runs } });

    // 初始不展开
    expect(wrapper.find('[data-test="scheduled-task-run-expand"]').exists()).toBe(false);

    await wrapper.find('[data-test="scheduled-task-run-row"]').trigger('click');

    const expand = wrapper.find('[data-test="scheduled-task-run-expand"]');
    expect(expand.exists()).toBe(true);
    expect(expand.text()).toContain('usage_pct');
    expect(expand.text()).toContain('77');
  });

  test('result_data 为 null 时不展开详情', async () => {
    const runs = [
      makeRun({ result_data: null }),
    ];
    const wrapper = mount(ScheduledTaskRunHistory, { props: { runs } });

    await wrapper.find('[data-test="scheduled-task-run-row"]').trigger('click');

    // 没有 result_data 可展开，但仍可点击；展开区显示提示或为空
    const expand = wrapper.find('[data-test="scheduled-task-run-expand"]');
    expect(expand.exists()).toBe(true);
    expect(expand.text()).toContain('无详细数据');
  });

  test('空历史显示「暂无执行记录」', () => {
    const wrapper = mount(ScheduledTaskRunHistory, { props: { runs: [] } });

    expect(wrapper.find('[data-test="scheduled-task-empty"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="scheduled-task-empty"]').text()).toContain('暂无执行记录');
    expect(wrapper.findAll('[data-test="scheduled-task-run-row"]')).toHaveLength(0);
  });

  test('再次点击行折叠展开的详情', async () => {
    const runs = [makeRun({ result_data: { usage_pct: 77 } })];
    const wrapper = mount(ScheduledTaskRunHistory, { props: { runs } });

    await wrapper.find('[data-test="scheduled-task-run-row"]').trigger('click');
    expect(wrapper.find('[data-test="scheduled-task-run-expand"]').exists()).toBe(true);

    await wrapper.find('[data-test="scheduled-task-run-row"]').trigger('click');
    expect(wrapper.find('[data-test="scheduled-task-run-expand"]').exists()).toBe(false);
  });

  test('succeeded 行不带 failed 样式', () => {
    const runs = [makeRun({ status: 'succeeded' })];
    const wrapper = mount(ScheduledTaskRunHistory, { props: { runs } });

    const row = wrapper.find('[data-test="scheduled-task-run-row"]');
    expect(row.classes()).not.toContain('run-failed');
  });
});
