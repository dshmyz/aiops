import { mount } from '@vue/test-utils';
import { ref, computed } from 'vue';
import ElementPlus from 'element-plus';
import { describe, expect, test, vi } from 'vitest';
import CandidatesStage from './CandidatesStage.vue';
import { makeCapabilities } from './testHelpers';
import type { UseCapabilities } from '../../composables/useCapabilities';
import type { ImportCandidate, ImportPreview } from '../../types';

function makeCandidate(overrides: Partial<ImportCandidate> = {}): ImportCandidate {
  return {
    id: 'GET /api/minio/{cluster}/buckets/{bucket}/capacity',
    method: 'GET',
    path: '/api/minio/{cluster}/buckets/{bucket}/capacity',
    operation_id: 'getBucketCapacity',
    capability: {
      schema_version: 1,
      name: 'minio.bucket.capacity.read',
      status: 'needs_review',
      domain: 'minio',
      resource_type: 'bucket',
      operation: 'read',
      risk: 'low',
      backend: { adapter: 'http', method: 'GET', path: '/api/minio/{cluster}/buckets/{bucket}/capacity', timeout_ms: 3000, base_url: 'https://middleware.example.com' },
      input_schema: {},
      output: { kind: 'observation', severity_path: '', summary_template: '', fields: {} },
      auth: { roles: ['viewer'], environment_scoped: true },
      ai: { description: '', examples: [] },
    },
    recommendation: 'recommended',
    reasons: ['读取类接口，适合 AI'],
    warnings: null,
    ...overrides,
  };
}

function makePreview(candidates: ImportCandidate[] = [makeCandidate()]): ImportPreview {
  return {
    source: { openapi_url: 'http://example/v3/api-docs', backend_base_url: 'https://middleware.example.com', fingerprint: 'sha256:test' },
    stats: { total: candidates.length, recommended: 1, needs_adjustment: 0, not_recommended: 0, read: 1, write: 0 },
    candidates,
  };
}

function mountCandidates(overrides: Partial<UseCapabilities> = {}) {
  const capabilities = makeCapabilities(overrides);
  return {
    wrapper: mount(CandidatesStage, { props: { capabilities }, global: { plugins: [ElementPlus] } }),
    capabilities,
  };
}

describe('CandidatesStage', () => {
  test('渲染源输入条与预览按钮', () => {
    const { wrapper } = mountCandidates();
    expect(wrapper.find('[data-test="openapi-url-input"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="backend-base-url-input"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="preview-openapi-url"]').exists()).toBe(true);
  });

  test('换一个 Swagger 按钮跳回 source 阶段', async () => {
    const { wrapper, capabilities } = mountCandidates();
    await wrapper.find('.secondary-inline').trigger('click');
    expect(capabilities.managementPhase.value).toBe('source');
  });

  test('importPreview 存在时显示预览统计与候选列表', () => {
    const preview = makePreview([makeCandidate(), makeCandidate({ id: 'POST /api/kafka/topics', method: 'POST', path: '/api/kafka/topics', recommendation: 'needs_adjustment' })]);
    const { wrapper } = mountCandidates({
      importPreview: ref(preview),
      visibleImportCandidates: computed(() => preview.candidates),
    } as never);
    expect(wrapper.find('[data-test="import-preview"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="import-preview"]').text()).toContain('推荐接入');
  });

  test('候选列表渲染候选行', () => {
    const candidate = makeCandidate();
    const { wrapper } = mountCandidates({
      importPreview: ref(makePreview([candidate])),
      visibleImportCandidates: computed(() => [candidate]),
    } as never);
    expect(wrapper.find(`[data-test="candidate-row-${candidate.id}"]`).exists()).toBe(true);
    expect(wrapper.find(`[data-test="candidate-selected-${candidate.id}"]`).exists()).toBe(true);
  });

  test('无 override 时不渲染 override 元素，有 override 时显示', () => {
    const candidate = makeCandidate();
    const overrides = { [candidate.id]: { name: 'custom.name', domain: 'minio', resource_type: 'bucket', operation: 'read' as const, risk: 'low' as const } };
    // 无 override
    const noOverride = mountCandidates({
      importPreview: ref(makePreview([candidate])),
      visibleImportCandidates: computed(() => [candidate]),
      candidateOverrides: ref({}),
    } as never);
    expect(noOverride.wrapper.find('.candidate-override').exists()).toBe(false);

    // 有 override
    const withOverride = mountCandidates({
      importPreview: ref(makePreview([candidate])),
      visibleImportCandidates: computed(() => [candidate]),
      candidateOverrides: ref(overrides),
    } as never);
    expect(withOverride.wrapper.find('.candidate-override').exists()).toBe(true);
    expect(withOverride.wrapper.find('.candidate-override').text()).toBe('custom.name');
  });

  test('无 reason 时不渲染 reason 元素', () => {
    const candidate = makeCandidate();
    const { wrapper } = mountCandidates({
      importPreview: ref(makePreview([candidate])),
      visibleImportCandidates: computed(() => [candidate]),
      candidateReasonText: () => '',
    } as never);
    expect(wrapper.find('.candidate-reason').exists()).toBe(false);
  });

  test('提交摘要显示选中数量', () => {
    const { wrapper } = mountCandidates({
      importPreview: ref(makePreview()),
      importCommitSummary: computed(() => ({ selected: 2, reads: 1, writes: 1, highRisk: 0 })),
    } as never);
    expect(wrapper.find('[data-test="import-commit-summary"]').text()).toContain('已选择 2 个候选 API');
  });

  test('canCommitImportPreview 为 false 时提交按钮禁用', () => {
    const { wrapper } = mountCandidates({
      importPreview: ref(makePreview()),
      canCommitImportPreview: computed(() => false),
    } as never);
    expect(wrapper.find('[data-test="commit-openapi-import"]').attributes('disabled')).toBeDefined();
  });

  test('点击提交按钮调用 commitSwaggerImport', async () => {
    const fn = vi.fn(() => Promise.resolve());
    const { wrapper } = mountCandidates({
      importPreview: ref(makePreview()),
      canCommitImportPreview: computed(() => true),
      commitSwaggerImport: fn,
    } as never);
    await wrapper.find('[data-test="commit-openapi-import"]').trigger('click');
    expect(fn).toHaveBeenCalledOnce();
  });
});
