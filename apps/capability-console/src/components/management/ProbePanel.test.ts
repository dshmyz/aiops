import { mount } from '@vue/test-utils';
import ElementPlus from 'element-plus';
import { describe, expect, test } from 'vitest';
import ProbePanel from './ProbePanel.vue';
import { makeCapabilities } from './testHelpers';
import { normalizeCapability } from '../../capability';
import type { ManagedCapability } from '../../types';

function makeCapability(overrides: Partial<ManagedCapability> = {}): ManagedCapability {
  return normalizeCapability({
    name: 'kafka.topic.retention.update',
    domain: 'kafka',
    resource_type: 'topic',
    operation: 'write',
    risk: 'medium',
    source: 'discovered',
    backend: { adapter: 'http', method: 'POST', path: '/api/kafka/topics/{topic}/retention', timeout_ms: 3000, base_url: 'https://middleware.example.com' },
    input_schema: { topic: { type: 'string', required: true } },
    output: { kind: 'observation', severity_path: '', summary_template: '', fields: {} },
    ...overrides,
  });
}

describe('ProbePanel — 写能力', () => {
  test('写能力不显示实调入口，改为示例响应体离线推断', async () => {
    const capabilities = makeCapabilities();
    capabilities.selectCapability(makeCapability());
    const wrapper = mount(ProbePanel, {
      props: { capabilities },
      global: { plugins: [ElementPlus] },
    });

    // 读模式按钮不出现，写模式样例框与按钮出现
    expect(wrapper.find('[data-test="probe-run"]').exists()).toBe(false);
    expect(wrapper.find('[data-test="probe-sample-body"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="probe-run-write"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('不发起试调');

    // 粘贴样例写入共享状态，供 probeSelected 携带
    await wrapper.find('[data-test="probe-sample-body"]').setValue('{"code":0,"data":{"status":"ok"}}');
    expect(capabilities.testSampleBody.value).toBe('{"code":0,"data":{"status":"ok"}}');
  });

  test('写能力样例推断通过时展示「样例推断成功」', async () => {
    const capabilities = makeCapabilities();
    capabilities.selectCapability(makeCapability());
    capabilities.probeResult.value = {
      probe: { kind: 'observation', resource: { domain: 'kafka', type: 'topic', name: 'orders' }, severity: 'ok', summary: 'retention updated', data: {} },
      raw_body: '{"code":0,"data":{"status":"ok"}}',
      inferred_by: 'rules',
    };
    const wrapper = mount(ProbePanel, {
      props: { capabilities },
      global: { plugins: [ElementPlus] },
    });

    expect(wrapper.find('[data-test="probe-summary"]').text()).toContain('样例推断成功');
    expect(wrapper.text()).toContain('查看示例响应体');
  });
});
