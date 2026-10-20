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

  test('直接展示来源选择，不再有前置任务描述步骤', () => {
    const wrapper = mount(CapabilityCreationCanvas, {
      props: { capabilities: makeCapabilities() },
      global: { plugins: [ElementPlus] },
    });

    expect(wrapper.find('[data-test="source-single-api"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="source-openapi"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="url-endpoint-input"]').exists()).toBe(false);
    expect(wrapper.find('[data-test="preview-openapi-url"]').exists()).toBe(false);
    expect(wrapper.text()).toContain('从一个接口开始');
  });

  test('shows one API connection only after choosing the single API source', async () => {
    const wrapper = mount(CapabilityCreationCanvas, {
      props: { capabilities: makeCapabilities() },
      global: { plugins: [ElementPlus] },
    });

    await wrapper.find('[data-test="source-single-api"]').trigger('click');

    expect(wrapper.find('[data-test="url-endpoint-input"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="preview-openapi-url"]').exists()).toBe(false);
    expect(wrapper.text()).toContain('连接这个工具的数据来源');
  });

  test('接口地址支持 {var} 路径参数：出现参数值行与查询参数行', async () => {
    const capabilities = makeCapabilities();
    const wrapper = mount(CapabilityCreationCanvas, {
      props: { capabilities },
      global: { plugins: [ElementPlus] },
    });

    await wrapper.find('[data-test="source-single-api"]').trigger('click');
    await wrapper.find('[data-test="url-endpoint-input"]').setValue('https://backend.example.com/api/instances/{instance_id}/metrics?limit=10');

    expect(wrapper.find('[data-test="url-path-value-instance_id"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="url-add-query"]').exists()).toBe(true);
  });
});
