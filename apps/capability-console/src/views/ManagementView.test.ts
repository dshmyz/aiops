import { mount, flushPromises } from '@vue/test-utils';
import { ref } from 'vue';
import ElementPlus from 'element-plus';
import { describe, expect, test } from 'vitest';
import ManagementView from './ManagementView.vue';
import { makeCapabilities } from '../components/management/testHelpers';
import { normalizeCapability } from '../capability';
import type { UseCapabilities } from '../composables/useCapabilities';
import type { ManagedCapability } from '../types';

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

function mountView(overrides: Partial<UseCapabilities> = {}) {
  const capabilities = makeCapabilities(overrides);
  const wrapper = mount(ManagementView, {
    props: { capabilities },
    global: { plugins: [ElementPlus] },
  });
  return { wrapper, capabilities };
}

describe('ManagementView（列表为主界面）', () => {
  test('列表页：纯列表单列（review+工单关闭），无步骤脊', () => {
    const cap = makeCapability();
    const { wrapper } = mountView({ capabilities: ref([cap]), managementPhase: ref('review'), wizardOpen: ref(false) });
    expect(wrapper.find('[data-test="studio-ledger"]').exists()).toBe(true);
    expect(wrapper.find('.wo-spine').exists()).toBe(false);
  });

  test('评审工单页：review+工单打开，全屏评审带返回按钮', () => {
    const cap = makeCapability();
    const { wrapper } = mountView({ capabilities: ref([cap]), managementPhase: ref('review'), wizardOpen: ref(true) });
    expect(wrapper.find('[data-test="workflow-review"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="back-to-list"]').exists()).toBe(true);
  });

  test('source 阶段渲染接入画布', () => {
    const cap = makeCapability();
    const { wrapper } = mountView({ capabilities: ref([cap]), managementPhase: ref('source'), wizardOpen: ref(true) });
    expect(wrapper.find('[data-test="creation-canvas"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="back-to-list"]').exists()).toBe(true);
  });

  test('评审页：全屏工单，含返回列表按钮', () => {
    const cap = makeCapability();
    const { wrapper } = mountView({ capabilities: ref([cap]), managementPhase: ref('review'), wizardOpen: ref(true) });
    expect(wrapper.find('[data-test="workflow-review"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="back-to-list"]').exists()).toBe(true);
  });

  test('评审页步骤脊仅三步，无独立发布步骤', () => {
    const cap = makeCapability();
    const { wrapper } = mountView({ capabilities: ref([cap]), managementPhase: ref('review'), wizardOpen: ref(true) });
    const steps = wrapper.findAll('.wo-spine__step');
    expect(steps).toHaveLength(3);
    expect(wrapper.find('[data-test="workflow-step-ai"]').exists()).toBe(false);
  });
});
