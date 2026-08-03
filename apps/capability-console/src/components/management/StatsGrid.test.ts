import { mount } from '@vue/test-utils';
import { describe, expect, test } from 'vitest';
import StatsGrid from './StatsGrid.vue';

describe('StatsGrid', () => {
  test('渲染所有传入的统计项', () => {
    const items = [
      { label: 'AI 可用', value: 3, testId: 'stat-published' },
      { label: '待评审', value: 2, testId: 'stat-review' },
      { label: '校验失败', value: 1, testId: 'stat-invalid' },
      { label: '可发布', value: 1, testId: 'stat-publishable' },
    ];
    const wrapper = mount(StatsGrid, { props: { items } });
    expect(wrapper.find('[data-test="stat-published"]').text()).toContain('3');
    expect(wrapper.find('[data-test="stat-review"]').text()).toContain('2');
    expect(wrapper.find('[data-test="stat-invalid"]').text()).toContain('1');
    expect(wrapper.find('[data-test="stat-publishable"]').text()).toContain('1');
  });

  test('每项渲染 label 和 value', () => {
    const wrapper = mount(StatsGrid, {
      props: { items: [{ label: 'AI 可用', value: 5, testId: 'stat-published' }] },
    });
    expect(wrapper.find('[data-test="stat-published"]').text()).toContain('AI 可用');
    expect(wrapper.find('[data-test="stat-published"]').text()).toContain('5');
  });

  test('空数组渲染空容器', () => {
    const wrapper = mount(StatsGrid, { props: { items: [] } });
    expect(wrapper.findAll('.stats-grid-item')).toHaveLength(0);
  });

  test('每项按顺序应用语义颜色', () => {
    const items = [
      { label: 'a', value: 1, testId: 'a' },
      { label: 'b', value: 2, testId: 'b' },
      { label: 'c', value: 3, testId: 'c' },
      { label: 'd', value: 4, testId: 'd' },
    ];
    const wrapper = mount(StatsGrid, { props: { items } });
    // 第 1 项 success，第 2 项 warning，第 3 项 danger，第 4 项 accent
    expect(wrapper.find('[data-test="a"]').classes()).toContain('stat-success');
    expect(wrapper.find('[data-test="b"]').classes()).toContain('stat-warning');
    expect(wrapper.find('[data-test="c"]').classes()).toContain('stat-danger');
    expect(wrapper.find('[data-test="d"]').classes()).toContain('stat-accent');
  });
});
