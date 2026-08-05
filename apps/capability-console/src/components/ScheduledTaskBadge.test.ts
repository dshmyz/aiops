import { mount } from '@vue/test-utils';
import { describe, expect, test, vi } from 'vitest';
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

  test('暴露根元素点击，供外部绑定跳转事件', () => {
    // App.vue 在 badge 上用 @click.stop 绑定"跳到失败列表"。根元素可通过
    // attrs 接收该监听器，这里验证点击监听器确实落到根 <span> 上。
    const onJump = vi.fn();
    const wrapper = mount(ScheduledTaskBadge, {
      props: { count: 2 },
      attrs: { onClick: onJump },
    });

    const badge = wrapper.find('[data-test="scheduled-task-badge"]');
    badge.trigger('click');
    expect(onJump).toHaveBeenCalledTimes(1);
  });
});
