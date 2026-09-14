import { mount } from '@vue/test-utils';
import ElementPlus from 'element-plus';
import { beforeEach, describe, expect, test } from 'vitest';
import CapabilityCreationCanvas from './CapabilityCreationCanvas.vue';
import { resetCreationCanvasState } from './creationCanvasState';
import { makeCapabilities } from './testHelpers';

describe('CapabilityCreationCanvas', () => {
  beforeEach(() => {
    // 画布状态是模块级（跨组件挂载持久），测试间必须重置。
    resetCreationCanvasState();
  });

  test('starts from the AI task instead of technical connection fields', () => {
    const wrapper = mount(CapabilityCreationCanvas, {
      props: { capabilities: makeCapabilities() },
      global: { plugins: [ElementPlus] },
    });

    expect(wrapper.find('[data-test="capability-goal-input"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('用一句话描述任务');
    expect(wrapper.find('[data-test="url-endpoint-input"]').exists()).toBe(false);
    expect(wrapper.find('[data-test="preview-openapi-url"]').exists()).toBe(false);
    expect(wrapper.text()).not.toContain('HTTP 方法');
  });

  test('reveals source choices only after a goal is confirmed', async () => {
    const capabilities = makeCapabilities();
    const wrapper = mount(CapabilityCreationCanvas, {
      props: { capabilities },
      global: { plugins: [ElementPlus] },
    });

    const continueButton = wrapper.find('[data-test="confirm-capability-goal"]');
    expect((continueButton.element as HTMLButtonElement).disabled).toBe(true);

    await wrapper.find('[data-test="capability-goal-input"]').setValue('查询 MinIO 桶容量');
    await continueButton.trigger('click');

    expect(wrapper.find('[data-test="source-single-api"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="source-openapi"]').exists()).toBe(true);
    expect(capabilities.urlDescription.value).toBe('查询 MinIO 桶容量');
  });

  test('shows one API connection only after choosing the single API source', async () => {
    const wrapper = mount(CapabilityCreationCanvas, {
      props: { capabilities: makeCapabilities() },
      global: { plugins: [ElementPlus] },
    });

    await wrapper.find('[data-test="capability-goal-input"]').setValue('查询 MinIO 桶容量');
    await wrapper.find('[data-test="confirm-capability-goal"]').trigger('click');
    await wrapper.find('[data-test="source-single-api"]').trigger('click');

    expect(wrapper.find('[data-test="url-endpoint-input"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="preview-openapi-url"]').exists()).toBe(false);
    expect(wrapper.text()).toContain('连接这个工具的数据来源');
  });
});
