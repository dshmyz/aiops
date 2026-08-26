import { mount } from '@vue/test-utils';
import { ref, computed } from 'vue';
import ElementPlus from 'element-plus';
import { describe, expect, test, vi } from 'vitest';
import ReviewStage from './ReviewStage.vue';
import { makeCapabilities } from './testHelpers';
import { normalizeCapability } from '../../capability';
import type { UseCapabilities } from '../../composables/useCapabilities';
import type { ManagedCapability } from '../../types';
import type { ImportBatch } from '../../importBatch';

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
  test('渲染能力清单与评审详情', () => {
    const { wrapper } = mountReview();
    expect(wrapper.find('[data-test="studio-ledger"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="studio-translator"]').exists()).toBe(true);
  });

  test('渲染搜索与筛选器', () => {
    const { wrapper } = mountReview();
    expect(wrapper.find('[data-test="capability-search"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="status-filter"]').exists()).toBe(true);
  });

  test('能力表格渲染能力行', () => {
    const cap = makeCapability();
    const { wrapper } = mountReview({
      capabilities: ref([cap]),
    });
    expect(wrapper.find('[data-test="edit-minio.bucket.capacity.read"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="next-minio.bucket.capacity.read"]').exists()).toBe(true);
  });

  test('点击能力名调用 selectCapability', async () => {
    const fn = vi.fn();
    const cap = makeCapability();
    const { wrapper } = mountReview({
      capabilities: ref([cap]),
      selectCapability: fn,
    });
    await wrapper.find('[data-test="edit-minio.bucket.capacity.read"]').trigger('click');
    expect(fn).toHaveBeenCalledWith(cap);
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

  test('importBatch 存在时显示导入批次面板', () => {
    const batch: ImportBatch = {
      items: [],
      domains: ['minio'],
      stats: { total: 1, read: 1, write: 0, selected: 1, ignored: 0, needsMapping: 0, notAIReady: 0 },
    };
    const { wrapper } = mountReview({ importBatch: ref(batch) } as never);
    expect(wrapper.find('[data-test="import-batch"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="import-batch-stat-total"]').text()).toContain('1');
  });
});
