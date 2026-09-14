import { mount } from '@vue/test-utils';
import { ref, computed } from 'vue';
import { describe, expect, test } from 'vitest';
import WorkflowRail from './WorkflowRail.vue';
import { makeCapabilities } from './testHelpers';
import type { UseCapabilities } from '../../composables/useCapabilities';

function mountRail(overrides: Partial<UseCapabilities> = {}) {
  const capabilities = makeCapabilities(overrides);
  return { wrapper: mount(WorkflowRail, { props: { capabilities } }), capabilities };
}

describe('WorkflowRail', () => {
  test('渲染 3 个步骤按钮，含序号、标题、说明', () => {
    const { wrapper } = mountRail();
    const steps = wrapper.findAll('.workflow-step');
    expect(steps).toHaveLength(3);

    expect(wrapper.find('[data-test="workflow-step-source"]').text()).toContain('连接接口');
    expect(wrapper.find('[data-test="workflow-step-candidates"]').text()).toContain('验证接口');
    expect(wrapper.find('[data-test="workflow-step-review"]').text()).toContain('确认并发布');
    // 方案①：发布从步骤降为动作，不再作为独立第 4 步
    expect(wrapper.find('[data-test="workflow-step-ai"]').exists()).toBe(false);
  });

  test('当前阶段对应的步骤标记为 active', () => {
    const { wrapper } = mountRail({ managementPhase: ref('review') } as never);
    expect(wrapper.find('[data-test="workflow-step-review"]').classes()).toContain('active');
    expect(wrapper.find('[data-test="workflow-step-source"]').classes()).not.toContain('active');
  });

  test('当前阶段步骤带 aria-current="step" 语义', () => {
    const { wrapper } = mountRail({ managementPhase: ref('candidates') } as never);
    expect(wrapper.find('[data-test="workflow-step-candidates"]').attributes('aria-current')).toBe('step');
    expect(wrapper.find('[data-test="workflow-step-source"]').attributes('aria-current')).toBeUndefined();
    expect(wrapper.find('[data-test="workflow-step-review"]').attributes('aria-current')).toBeUndefined();
  });

  test('点击步骤切换 managementPhase', async () => {
    const { wrapper, capabilities } = mountRail({
      capabilities: ref([
        { name: 'c1', source: 'discovered', domain: 'd', resource_type: 'r', operation: 'read', risk: 'low', input_schema: {}, output: { kind: 'table', fields: [] }, backend: { adapter: 'http', method: 'GET', base_url: 'http://x', path: '/a' } },
      ] as never),
      stats: computed(() => ({ published: 1, review: 0, invalid: 0, publishable: 0 })),
    } as never);
    await wrapper.find('[data-test="workflow-step-review"]').trigger('click');
    expect(capabilities.managementPhase.value).toBe('review');
  });

  test('importPreview 存在时 source 步骤标记 done', () => {
    const { wrapper } = mountRail({
      importPreview: ref({
        source: { openapi_url: '', backend_base_url: '', fingerprint: '' },
        stats: { total: 1, recommended: 1, needs_adjustment: 0, not_recommended: 0, read: 1, write: 0 },
        candidates: [],
      }),
    } as never);
    expect(wrapper.find('[data-test="workflow-step-source"]').classes()).toContain('done');
  });

  test('无能力时 review（试调验收）步骤禁用', () => {
    const { wrapper } = mountRail({ capabilities: ref([]) } as never);
    expect(wrapper.find('[data-test="workflow-step-review"]').attributes('disabled')).toBeDefined();
  });

  test('有能力时 review（试调验收）步骤可用', () => {
    const { wrapper } = mountRail({
      capabilities: ref([
        {
          name: 'c1',
          source: 'discovered',
          domain: 'storage',
          resource_type: 'bucket',
          operation: 'read',
          risk: 'low',
          input_schema: {},
          output: { kind: 'table', fields: [] },
          backend: { adapter: 'http', method: 'GET', base_url: 'http://x', path: '/a' },
        },
      ] as never),
    } as never);
    expect(wrapper.find('[data-test="workflow-step-review"]').attributes('disabled')).toBeUndefined();
  });
});
