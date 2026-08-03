import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import type { ProgressStage } from '../types';
import ProgressTimeline from './ProgressTimeline.vue';

const stages: ProgressStage[] = [
  { stage: 'planning', received_at: '2026-08-03T10:00:00.100Z' },
  { stage: 'tool_executing', detail: 'kafka.topic.retention.set', received_at: '2026-08-03T10:00:01.200Z' },
  { stage: 'formatting', received_at: '2026-08-03T10:00:02.300Z' },
];

describe('ProgressTimeline', () => {
  test('renders nothing when there are no stages', () => {
    const wrapper = mount(ProgressTimeline, { props: { stages: [] } });
    expect(wrapper.find('[data-test="progress-timeline"]').exists()).toBe(false);
  });

  test('is expanded by default so the checklist is visible without a click', () => {
    const wrapper = mount(ProgressTimeline, { props: { stages } });
    expect(wrapper.find('[aria-expanded="true"]').exists()).toBe(true);
    expect(wrapper.findAll('.progress-item').length).toBe(3);
  });

  test('collapses on toggle click', async () => {
    const wrapper = mount(ProgressTimeline, { props: { stages } });
    await wrapper.find('.progress-toggle').trigger('click');
    expect(wrapper.find('[aria-expanded="false"]').exists()).toBe(true);
  });

  test('marks every stage done with past-tense labels once streaming has finished', () => {
    const wrapper = mount(ProgressTimeline, { props: { stages, streaming: false } });
    expect(wrapper.findAll('[data-test="progress-item-done"]').length).toBe(3);
    expect(wrapper.findAll('[data-test="progress-item-active"]').length).toBe(0);
    const text = wrapper.text();
    expect(text).toContain('已规划任务');
    expect(text).toContain('已执行任务');
    expect(text).toContain('已生成回复');
    expect(wrapper.find('.progress-summary').text()).toContain('已完成 3 个阶段');
  });

  test('keeps only the latest stage active while streaming', () => {
    const wrapper = mount(ProgressTimeline, { props: { stages, streaming: true } });
    expect(wrapper.findAll('[data-test="progress-item-done"]').length).toBe(2);
    const active = wrapper.findAll('[data-test="progress-item-active"]');
    expect(active.length).toBe(1);
    // 最后一个阶段是 formatting，进行中应显示进行时态
    expect(active[0].text()).toContain('回复整形中');
  });

  test('shows the tool name detail on its own line', () => {
    const wrapper = mount(ProgressTimeline, { props: { stages, streaming: false } });
    const details = wrapper.findAll('.progress-item-detail');
    expect(details.length).toBe(1);
    expect(details[0].text()).toBe('kafka.topic.retention.set');
  });

  test('falls back to the raw stage name for unknown stages', () => {
    const wrapper = mount(ProgressTimeline, {
      props: {
        stages: [{ stage: 'unknown_stage' as ProgressStage['stage'], received_at: '2026-08-03T10:00:00Z' }],
        streaming: false,
      },
    });
    expect(wrapper.find('.progress-item-label').text()).toBe('unknown_stage');
  });
});
