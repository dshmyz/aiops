import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import AuditEventDetail from './AuditEventDetail.vue';
import type { AuditEvent } from '../types';

const baseEvent: AuditEvent = {
  id: 'audit-1',
  plan_id: 'plan-1',
  subject: 'operator-1',
  tool_name: 'kafka.topic.retention.set',
  action: 'plan_created',
  decision: 'permitted',
  created_at: '2026-07-25T10:00:00Z',
};

describe('AuditEventDetail', () => {
  test('renders trace_id and a Jaeger "查看 Trace" link when trace_id is present', () => {
    const traceId = '4bf92f3577b34da6a3ce929d0e0e4736';
    const wrapper = mount(AuditEventDetail, {
      props: { event: { ...baseEvent, trace_id: traceId } },
    });

    const traceBlock = wrapper.find('[data-test="audit-detail-trace"]');
    expect(traceBlock.exists()).toBe(true);
    expect(traceBlock.text()).toContain(traceId);

    const link = wrapper.find('[data-test="audit-detail-trace-link"]');
    expect(link.exists()).toBe(true);
    expect(link.text()).toContain('查看 Trace');
    // VITE_JAEGER_URL is unset in tests, so the default base applies.
    expect(link.attributes('href')).toBe(`http://localhost:16686/trace/${traceId}`);
    expect(link.attributes('target')).toBe('_blank');
    expect(link.attributes('rel')).toBe('noopener noreferrer');
  });

  test('omits the trace_id row when trace_id is undefined', () => {
    const wrapper = mount(AuditEventDetail, {
      props: { event: baseEvent },
    });

    expect(wrapper.find('[data-test="audit-detail-trace"]').exists()).toBe(false);
    expect(wrapper.find('[data-test="audit-detail-trace-link"]').exists()).toBe(false);
  });

  test('omits the trace_id row when trace_id is an empty string', () => {
    const wrapper = mount(AuditEventDetail, {
      props: { event: { ...baseEvent, trace_id: '' } },
    });

    expect(wrapper.find('[data-test="audit-detail-trace"]').exists()).toBe(false);
  });

  test('still renders the rest of the event detail when trace_id is absent', () => {
    const wrapper = mount(AuditEventDetail, {
      props: { event: baseEvent },
    });

    expect(wrapper.find('[data-test="audit-event-detail"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('kafka.topic.retention.set');
    expect(wrapper.text()).toContain('permitted');
    expect(wrapper.text()).toContain('operator-1');
  });
});
