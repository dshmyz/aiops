import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import type { ProgressEntry } from '../conversationProgress';
import ProgressTimeline from './ProgressTimeline.vue';

const entries: ProgressEntry[] = [
  { kind: 'phase', stage: 'planning', done: true, label: '识别并拆解任务', time: '2026-08-03T10:00:00.100Z' },
  { kind: 'phase', stage: 'tool_executing', done: true, label: '查询平台事实', time: '2026-08-03T10:00:01.200Z' },
  { kind: 'tool', stage: 'tool_executing', done: true, label: '查询 kafka.topic.retention.set', detail: 'kafka.topic.retention.set', time: '2026-08-03T10:00:01.200Z' },
  { kind: 'phase', stage: 'formatting', done: true, label: '整合输出', time: '2026-08-03T10:00:02.300Z' },
];

describe('ProgressTimeline', () => {
  test('renders nothing when there are no entries', () => {
    const wrapper = mount(ProgressTimeline, { props: { entries: [] } });
    expect(wrapper.find('[data-test="progress-timeline"]').exists()).toBe(false);
  });

  test('is expanded by default so the checklist is visible without a click', () => {
    const wrapper = mount(ProgressTimeline, { props: { entries } });
    expect(wrapper.find('[aria-expanded="true"]').exists()).toBe(true);
    expect(wrapper.findAll('.progress-item').length).toBe(4);
  });

  test('collapses on toggle click', async () => {
    const wrapper = mount(ProgressTimeline, { props: { entries } });
    await wrapper.find('.progress-toggle').trigger('click');
    expect(wrapper.find('[aria-expanded="false"]').exists()).toBe(true);
  });

  test('marks every entry done with past-tense labels once streaming has finished', () => {
    const wrapper = mount(ProgressTimeline, { props: { entries, streaming: false } });
    expect(wrapper.findAll('[data-test="progress-item-done"]').length).toBe(4);
    expect(wrapper.findAll('[data-test="progress-item-active"]').length).toBe(0);
    const text = wrapper.text();
    expect(text).toContain('识别并拆解任务');
    expect(text).toContain('查询平台事实');
    expect(text).toContain('整合输出');
    expect(wrapper.find('.progress-summary').text()).toContain('已完成 4 个步骤');
  });

  test('keeps only the latest active entry while streaming', () => {
    const streamingEntries: ProgressEntry[] = [
      { kind: 'phase', stage: 'planning', done: true, label: '识别并拆解任务', time: '2026-08-03T10:00:00Z' },
      { kind: 'phase', stage: 'tool_executing', done: true, label: '查询平台事实', time: '2026-08-03T10:00:01Z' },
      { kind: 'tool', stage: 'tool_executing', done: false, label: '查询 kafka.topic.retention.set', detail: 'kafka.topic.retention.set', time: '2026-08-03T10:00:01Z' },
    ];
    const wrapper = mount(ProgressTimeline, { props: { entries: streamingEntries, streaming: true } });
    expect(wrapper.findAll('[data-test="progress-item-done"]').length).toBe(2);
    const active = wrapper.findAll('[data-test="progress-item-active"]');
    expect(active.length).toBe(1);
    expect(active[0].text()).toContain('kafka.topic.retention.set');
  });

  test('shows the tool name detail on its own line', () => {
    const wrapper = mount(ProgressTimeline, { props: { entries, streaming: false } });
    const details = wrapper.findAll('.progress-item-detail');
    expect(details.length).toBe(1);
    expect(details[0].text()).toBe('kafka.topic.retention.set');
  });
});