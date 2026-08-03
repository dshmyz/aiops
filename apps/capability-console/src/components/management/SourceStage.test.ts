import { mount } from '@vue/test-utils';
import { ref, computed } from 'vue';
import ElementPlus from 'element-plus';
import { describe, expect, test, vi } from 'vitest';
import SourceStage from './SourceStage.vue';
import { makeCapabilities } from './testHelpers';
import type { UseCapabilities } from '../../composables/useCapabilities';

function mountSource(overrides: Partial<UseCapabilities> = {}) {
  const capabilities = makeCapabilities(overrides);
  return {
    wrapper: mount(SourceStage, { props: { capabilities }, global: { plugins: [ElementPlus] } }),
    capabilities,
  };
}

describe('SourceStage', () => {
  test('渲染导入向导与快速发布面板', () => {
    const { wrapper } = mountSource();
    expect(wrapper.find('[data-test="import-wizard"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="quick-publish-panel"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="openapi-url-input"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="backend-base-url-input"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="preview-openapi-url"]').exists()).toBe(true);
  });

  test('渲染统计卡片', () => {
    const { wrapper } = mountSource({
      stats: computed(() => ({ published: 3, review: 2, invalid: 1, publishable: 1 })),
    } as never);
    expect(wrapper.find('[data-test="stat-published"]').text()).toContain('3');
    expect(wrapper.find('[data-test="stat-review"]').text()).toContain('2');
    expect(wrapper.find('[data-test="stat-invalid"]').text()).toContain('1');
    expect(wrapper.find('[data-test="stat-publishable"]').text()).toContain('1');
  });

  test('importMessage 存在时显示 import-result', () => {
    const { wrapper } = mountSource({ importMessage: ref('已生成 2 个草稿') } as never);
    expect(wrapper.find('[data-test="import-result"]').text()).toContain('已生成 2 个草稿');
  });

  test('点击预览按钮调用 previewSwaggerURL', async () => {
    const fn = vi.fn(() => Promise.resolve());
    const { wrapper } = mountSource({ previewSwaggerURL: fn } as never);
    await wrapper.find('[data-test="preview-openapi-url"]').trigger('click');
    expect(fn).toHaveBeenCalledOnce();
  });

  test('输入框 clearImportPreview 在 input 时触发', async () => {
    const fn = vi.fn();
    const { wrapper } = mountSource({ clearImportPreview: fn } as never);
    await wrapper.find('[data-test="openapi-url-input"]').trigger('input');
    expect(fn).toHaveBeenCalled();
  });

  test('点击查看已有能力跳转 review 阶段', async () => {
    const { wrapper, capabilities } = mountSource();
    await wrapper.find('.secondary-wide').trigger('click');
    expect(capabilities.managementPhase.value).toBe('review');
  });
});
