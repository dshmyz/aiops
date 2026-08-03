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
  test('渲染 4 个步骤按钮，含序号、标题、说明', () => {
    const { wrapper } = mountRail();
    const steps = wrapper.findAll('.workflow-step');
    expect(steps).toHaveLength(4);

    expect(wrapper.find('[data-test="workflow-step-source"]').text()).toContain('接入 API');
    expect(wrapper.find('[data-test="workflow-step-candidates"]').text()).toContain('选择能力');
    expect(wrapper.find('[data-test="workflow-step-review"]').text()).toContain('评审发布');
    expect(wrapper.find('[data-test="workflow-step-ai"]').text()).toContain('AI 试问');
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
      stats: computed(() => ({ published: 1, review: 0, invalid: 0, publishable: 0 })),
    } as never);
    await wrapper.find('[data-test="workflow-step-ai"]').trigger('click');
    expect(capabilities.managementPhase.value).toBe('ai');
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

  test('已发布能力为 0 时 AI 步骤禁用', () => {
    const { wrapper } = mountRail({ stats: computed(() => ({ published: 0, review: 0, invalid: 0, publishable: 0 })) } as never);
    expect(wrapper.find('[data-test="workflow-step-ai"]').attributes('disabled')).toBeDefined();
  });

  test('已发布能力 > 0 时 AI 步骤可用', () => {
    const { wrapper } = mountRail({ stats: computed(() => ({ published: 1, review: 0, invalid: 0, publishable: 0 })) } as never);
    expect(wrapper.find('[data-test="workflow-step-ai"]').attributes('disabled')).toBeUndefined();
  });
});
