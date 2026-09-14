import { mount } from '@vue/test-utils';
import { ref, computed } from 'vue';
import ElementPlus from 'element-plus';
import { describe, expect, test, vi } from 'vitest';
import AIStage from './AIStage.vue';
import { makeCapabilities } from './testHelpers';
import { normalizeCapability } from '../../capability';
import type { UseCapabilities } from '../../composables/useCapabilities';
import type { ManagedCapability } from '../../types';

function mountAI(overrides: Partial<UseCapabilities> = {}) {
  const capabilities = makeCapabilities(overrides);
  return {
    wrapper: mount(AIStage, { props: { capabilities }, global: { plugins: [ElementPlus] } }),
    capabilities,
  };
}

describe('AIStage', () => {
  test('渲染已发布能力列表与 AI 运行器', () => {
    const { wrapper } = mountAI();
    // 已发布列表由外层（ManagementView 侧栏）渲染，AI 页只含试运行面板
    expect(wrapper.find('[data-test="studio-ledger"]').exists()).toBe(false);
    expect(wrapper.find('[data-test="studio-ai-runner"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="ai-prompt"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="run-ai-preflight"]').exists()).toBe(true);
  });



  test('run-ai-preflight 按钮在未就绪时禁用', () => {
    const { wrapper } = mountAI({ aiPreflightReady: computed(() => false) } as never);
    expect(wrapper.find('[data-test="run-ai-preflight"]').attributes('disabled')).toBeDefined();
  });

  test('aiLoading 时按钮显示请求中', () => {
    const { wrapper } = mountAI({ aiLoading: ref(true) } as never);
    expect(wrapper.find('[data-test="run-ai-preflight"]').text()).toContain('请求中');
  });

  test('点击运行预检调用 runAIPreflight', async () => {
    const fn = vi.fn(() => Promise.resolve());
    const { wrapper } = mountAI({ aiPreflightReady: computed(() => true), runAIPreflight: fn } as never);
    await wrapper.find('[data-test="run-ai-preflight"]').trigger('click');
    expect(fn).toHaveBeenCalledOnce();
  });
});
