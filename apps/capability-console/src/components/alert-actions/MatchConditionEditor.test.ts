import { describe, expect, test } from 'vitest';
import { mount } from '@vue/test-utils';
import ElementPlus from 'element-plus';
import MatchConditionEditor from './MatchConditionEditor.vue';

describe('MatchConditionEditor', () => {
  test('renders top-level fields and empty label row when no labels yet', () => {
    const wrapper = mount(MatchConditionEditor, {
      props: { match: { severity: 'critical' } },
      global: { plugins: [ElementPlus] },
    });
    expect(wrapper.find('[data-test="alert-match-editor"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('标签匹配');
  });

  test('renders label rows with operator options', () => {
    const wrapper = mount(MatchConditionEditor, {
      props: { match: { labels: [{ key: 'cluster', value: 'm1', operator: 'exact' }] } },
      global: { plugins: [ElementPlus] },
    });
    const rows = wrapper.findAll('[data-test="label-match-row"]');
    expect(rows).toHaveLength(1);
    // el-input 的值在内部 input 元素上，不在 .text() 里
    const keyInput = rows[0].find('input');
    expect((keyInput.element as HTMLInputElement).value).toBe('cluster');
  });

  test('adds a label condition via button', async () => {
    const wrapper = mount(MatchConditionEditor, {
      props: { match: {} },
      global: { plugins: [ElementPlus] },
    });
    await wrapper.find('.add-btn').trigger('click');
    const emitted = wrapper.emitted('update')!;
    expect(emitted).toBeTruthy();
    const match = emitted[emitted.length - 1][0] as { labels: unknown[] };
    expect(match.labels).toHaveLength(1);
  });

  test('adds an OR group via button', async () => {
    const wrapper = mount(MatchConditionEditor, {
      props: { match: {} },
      global: { plugins: [ElementPlus] },
    });
    // 第一个 add-btn 是"标签条件"，第二个是"或条件组"
    const addBtns = wrapper.findAll('.add-btn');
    await addBtns[addBtns.length - 1].trigger('click');
    const emitted = wrapper.emitted('update')!;
    const match = emitted[emitted.length - 1][0] as { any_of: unknown[] };
    expect(match.any_of).toHaveLength(1);
  });

  test('shows OR group rows when any_of present', () => {
    const wrapper = mount(MatchConditionEditor, {
      props: { match: { any_of: [{ severity: 'warning' }] } },
      global: { plugins: [ElementPlus] },
    });
    expect(wrapper.text()).toContain('或条件组');
  });
});
