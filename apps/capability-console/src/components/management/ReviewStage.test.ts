import { mount } from '@vue/test-utils';
import { ref } from 'vue';
import ElementPlus from 'element-plus';
import { describe, expect, test, vi } from 'vitest';
import ReviewStage from './ReviewStage.vue';
import { makeCapabilities } from './testHelpers';
import { normalizeCapability } from '../../capability';
import type { UseCapabilities } from '../../composables/useCapabilities';
import type { ManagedCapability } from '../../types';

function makeCapability(overrides: Partial<ManagedCapability> = {}): ManagedCapability {
  return normalizeCapability({
    name: 'minio.bucket.capacity.read',
    domain: 'minio',
    resource_type: 'bucket',
    operation: 'read',
    risk: 'low',
    source: 'discovered',
    backend: { adapter: 'http', method: 'GET', path: '/api/minio/{cluster}/buckets/{bucket}/capacity', timeout_ms: 3000, base_url: 'https://middleware.example.com' },
    input_schema: { cluster: { type: 'string', required: true } },
    output: { kind: 'observation', severity_path: '', summary_template: '', fields: { usage_pct: '$.usage_pct' } },
    ...overrides,
  });
}

function mountReview(overrides: Partial<UseCapabilities> = {}) {
  const capabilities = makeCapabilities(overrides);
  return {
    wrapper: mount(ReviewStage, { props: { capabilities }, global: { plugins: [ElementPlus] } }),
    capabilities,
  };
}

describe('ReviewStage', () => {
  // 注：能力清单（studio-ledger）已上提到 ManagementView 层常驻，ReviewStage 只负责
  // 评审详情（编辑器 + 测试 + 折叠的发布清单/探活）。清单自身在 ManagementView.test.ts 覆盖。

  test('渲染评审详情', () => {
    const { wrapper } = mountReview();
    expect(wrapper.find('[data-test="studio-translator"]').exists()).toBe(true);
  });

  test('渲染发布清单与发布按钮', () => {
    const { wrapper } = mountReview();
    expect(wrapper.find('[data-test="publish-checklist"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="publish-current"]').exists()).toBe(true);
  });

  test('渲染能力编辑器表单字段', () => {
    const cap = makeCapability({ ai: { description: '读取 MinIO bucket 容量', examples: [] } });
    const { wrapper } = mountReview({
      selected: ref(cap),
    } as never);
    expect(wrapper.find('[data-test="capability-name"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="backend-path"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="ai-description"]').exists()).toBe(true);
  });

  test('渲染测试与预览区块', () => {
    const { wrapper } = mountReview();
    expect(wrapper.find('[data-test="test-input-form"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="test-input"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="save-draft"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="validate-capability"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="test-capability"]').exists()).toBe(true);
  });

  test('渲染 AI 预检面板', () => {
    const { wrapper } = mountReview();
    expect(wrapper.find('[data-test="ai-preflight"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="ai-prompt"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="run-ai-preflight"]').exists()).toBe(true);
  });

  test('渲染归一化预览', () => {
    const { wrapper } = mountReview();
    expect(wrapper.find('[data-test="request-preview"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="response-preview"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="preview"]').exists()).toBe(true);
  });

  test('点击保存草稿调用 saveSelectedDraft', async () => {
    const fn = vi.fn(() => Promise.resolve());
    const { wrapper } = mountReview({ saveSelectedDraft: fn } as never);
    await wrapper.find('[data-test="save-draft"]').trigger('click');
    expect(fn).toHaveBeenCalledOnce();
  });

  test('点击校验调用 validateSelected', async () => {
    const fn = vi.fn(() => Promise.resolve());
    const { wrapper } = mountReview({ validateSelected: fn } as never);
    await wrapper.find('[data-test="validate-capability"]').trigger('click');
    expect(fn).toHaveBeenCalledOnce();
  });

  test('评审主视是三张确认卡，技术面板收进发布卡高级详情', () => {
    const { wrapper } = mountReview();
    // 三张卡按顺序可见
    expect(wrapper.find('[data-test="confirm-card-tool"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="confirm-card-verify"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="confirm-card-publish"]').exists()).toBe(true);
    // 高级详情仍可达：编辑器、测试、发布检查、试调都在 details 槽内
    const advanced = wrapper.find('[data-test="confirm-card-advanced"]');
    expect(advanced.exists()).toBe(true);
    expect(wrapper.find('[data-test="capability-name"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="test-input-form"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="publish-checklist"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="probe-panel"]').exists()).toBe(true);
  });
});
