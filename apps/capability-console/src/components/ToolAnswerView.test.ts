import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import ToolAnswerView from './ToolAnswerView.vue';

describe('ToolAnswerView', () => {
  test('renders event.query answer as an events table', () => {
    const wrapper = mount(ToolAnswerView, {
      props: {
        tool: 'event.query',
        answer: {
          events: [
            {
              id: 'evt-1',
              tool_name: 'kafka.topic.retention.set',
              action: 'plan_confirmed',
              decision: 'permitted',
              subject: 'admin-1',
              created_at: '2026-08-01T10:00:00Z',
            },
          ],
          count: 1,
        },
      },
    });

    expect(wrapper.find('[data-test="tool-answer-view"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="tool-answer-count"]').text()).toContain('1');
    const rows = wrapper.findAll('[data-test="tool-answer-row"]');
    expect(rows.length).toBe(1);
    expect(rows[0].text()).toContain('evt-1');
    expect(rows[0].text()).toContain('plan_confirmed');
  });

  test('renders task.query answer as a tasks table', () => {
    const wrapper = mount(ToolAnswerView, {
      props: {
        tool: 'task.query',
        answer: {
          tasks: [
            {
              id: 't-1',
              name: 'minio 巡检',
              capability: 'minio.bucket.capacity.read',
              enabled: true,
              last_status: 'succeeded',
              next_run_at: '2026-08-03T00:00:00Z',
            },
          ],
          count: 1,
        },
      },
    });

    expect(wrapper.find('[data-test="tool-answer-view"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="tool-answer-table"]').exists()).toBe(true);
    const rows = wrapper.findAll('[data-test="tool-answer-row"]');
    expect(rows.length).toBe(1);
    expect(rows[0].text()).toContain('t-1');
    expect(rows[0].text()).toContain('minio 巡检');
  });

  test('renders generic key-value table for non-special tools', () => {
    const wrapper = mount(ToolAnswerView, {
      props: {
        tool: 'glusterfs.volume.health.read',
        answer: { status: 'ok', online_bricks: 3, heal_backlog: 0 },
      },
    });

    expect(wrapper.find('[data-test="tool-answer-view"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="tool-answer-header"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="tool-answer-header"]').text()).toContain('glusterfs.volume.health.read');
    const rows = wrapper.findAll('[data-test="tool-answer-row"]');
    expect(rows.length).toBe(3); // status, online_bricks, heal_backlog
    expect(rows[0].text()).toContain('status');
    expect(rows[0].text()).toContain('ok');
  });
});
