import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import ScheduledTaskBadge from './ScheduledTaskBadge.vue';

describe('ScheduledTaskBadge', () => {
  test('count=0 时不渲染', () => {
    const wrapper = mount(ScheduledTaskBadge, { props: { count: 0 } });

    expect(wrapper.find('[data-test="scheduled-task-badge"]').exists()).toBe(false);
  });

  test('count=3 时显示数字 3', () => {
    const wrapper = mount(ScheduledTaskBadge, { props: { count: 3 } });

    const badge = wrapper.find('[data-test="scheduled-task-badge"]');
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toBe('3');
  });

  test('count=1 时显示数字 1', () => {
    const wrapper = mount(ScheduledTaskBadge, { props: { count: 1 } });

    const badge = wrapper.find('[data-test="scheduled-task-badge"]');
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toBe('1');
  });

  test('count=99 时显示数字 99', () => {
    const wrapper = mount(ScheduledTaskBadge, { props: { count: 99 } });

    const badge = wrapper.find('[data-test="scheduled-task-badge"]');
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toBe('99');
  });

  test('count=100 时显示 99+', () => {
    const wrapper = mount(ScheduledTaskBadge, { props: { count: 100 } });

    const badge = wrapper.find('[data-test="scheduled-task-badge"]');
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toBe('99+');
  });

  test('count=1000 时显示 99+', () => {
    const wrapper = mount(ScheduledTaskBadge, { props: { count: 1000 } });

    const badge = wrapper.find('[data-test="scheduled-task-badge"]');
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toBe('99+');
  });
});
