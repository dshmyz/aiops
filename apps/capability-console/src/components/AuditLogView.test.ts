import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import AuditLogView from './AuditLogView.vue';
import type { AuditEvent, AuditEventFilter } from '../types';

const sampleEvents: AuditEvent[] = [
  {
    id: 'audit-1',
    plan_id: 'plan-1',
    subject: 'operator-1',
    tool_name: 'kafka.topic.retention.set',
    action: 'plan_created',
    decision: 'permitted',
    created_at: '2026-07-25T10:00:00Z',
  },
];

describe('AuditLogView', () => {
  test('emits search event with trimmed query when clicking search button', async () => {
    const wrapper = mount(AuditLogView, {
      props: { events: sampleEvents, loading: false },
    });

    await wrapper.find('[data-test="audit-search-query"]').setValue('  上周谁拒绝了 plan  ');
    await wrapper.find('[data-test="audit-search-submit"]').trigger('click');

    const searchEvents = wrapper.emitted('search');
    expect(searchEvents).toBeDefined();
    expect(searchEvents?.[0]).toEqual(['上周谁拒绝了 plan']);
  });

  test('emits search event when pressing enter in the search input', async () => {
    const wrapper = mount(AuditLogView, {
      props: { events: sampleEvents, loading: false },
    });

    await wrapper.find('[data-test="audit-search-query"]').setValue('昨天 admin-1 操作');
    await wrapper.find('[data-test="audit-search-query"]').trigger('keyup.enter');

    const searchEvents = wrapper.emitted('search');
    expect(searchEvents).toBeDefined();
    expect(searchEvents?.[0]).toEqual(['昨天 admin-1 操作']);
  });

  test('does not emit search event when query is empty', async () => {
    const wrapper = mount(AuditLogView, {
      props: { events: sampleEvents, loading: false },
    });

    await wrapper.find('[data-test="audit-search-query"]').setValue('   ');
    await wrapper.find('[data-test="audit-search-submit"]').trigger('click');

    expect(wrapper.emitted('search')).toBeUndefined();
  });

  test('disables search button while loading', () => {
    const wrapper = mount(AuditLogView, {
      props: { events: sampleEvents, loading: true },
    });

    const button = wrapper.find('[data-test="audit-search-submit"]').element as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    expect(wrapper.find('[data-test="audit-search-submit"]').text()).toContain('搜索中');
  });

  test('initializes search input from searchQuery prop', () => {
    const wrapper = mount(AuditLogView, {
      props: { events: sampleEvents, loading: false, searchQuery: '上周拒绝的 plan' },
    });

    const input = wrapper.find('[data-test="audit-search-query"]').element as HTMLInputElement;
    expect(input.value).toBe('上周拒绝的 plan');
  });

  test('keeps search input in sync when searchQuery prop changes', async () => {
    const wrapper = mount(AuditLogView, {
      props: { events: sampleEvents, loading: false, searchQuery: '' },
    });

    await wrapper.setProps({ searchQuery: '今天 admin 操作' });

    const input = wrapper.find('[data-test="audit-search-query"]').element as HTMLInputElement;
    expect(input.value).toBe('今天 admin 操作');
  });

  // --- 借鉴-4: 事件中心"最终结果过滤"切换 ---

  test('renders final-result-only toggle checked by default (借鉴-4)', () => {
    const wrapper = mount(AuditLogView, {
      props: { events: sampleEvents, loading: false },
    });

    const toggle = wrapper.find('[data-test="audit-final-result-only"]');
    expect(toggle.exists()).toBe(true);
    expect((toggle.element as HTMLInputElement).checked).toBe(true);
  });

  test('emits filter with final_result_only=false when toggle is turned off (借鉴-4)', async () => {
    const wrapper = mount(AuditLogView, {
      props: { events: sampleEvents, loading: false },
    });

    await wrapper.find('[data-test="audit-final-result-only"]').setValue(false);

    const filterEvents = wrapper.emitted('filter');
    expect(filterEvents).toBeDefined();
    const lastFilter = filterEvents?.[filterEvents.length - 1]?.[0] as AuditEventFilter;
    expect(lastFilter.final_result_only).toBe(false);
  });

  test('emits filter with final_result_only=true when toggle is turned back on (借鉴-4)', async () => {
    const wrapper = mount(AuditLogView, {
      props: { events: sampleEvents, loading: false, finalResultOnly: false },
    });

    await wrapper.find('[data-test="audit-final-result-only"]').setValue(true);

    const filterEvents = wrapper.emitted('filter');
    expect(filterEvents).toBeDefined();
    const lastFilter = filterEvents?.[filterEvents.length - 1]?.[0] as AuditEventFilter;
    expect(lastFilter.final_result_only).toBe(true);
  });
});
