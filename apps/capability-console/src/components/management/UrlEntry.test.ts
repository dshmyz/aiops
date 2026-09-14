import { mount } from '@vue/test-utils';
import { ref, computed } from 'vue';
import ElementPlus from 'element-plus';
import { describe, expect, test, vi } from 'vitest';
import UrlEntry from './UrlEntry.vue';
import { makeCapabilities } from './testHelpers';
import { normalizeCapability } from '../../capability';
import type { UseCapabilities } from '../../composables/useCapabilities';
import type { ProbeGenerateResult } from '../../api';

function mountEntry(overrides: Partial<UseCapabilities> = {}) {
  const capabilities = makeCapabilities(overrides);
  const wrapper = mount(UrlEntry, { props: { capabilities }, global: { plugins: [ElementPlus] } });
  return { wrapper, capabilities };
}

function makeGeneratedResult(overrides: Partial<ProbeGenerateResult> = {}): ProbeGenerateResult {
  return {
    capability: normalizeCapability({
      name: 'minio.bucket.capacity.read',
      backend: { adapter: 'http', method: 'GET', path: '/api/minio/m1/capacity', base_url: 'https://middleware.example.com', timeout_ms: 3000 },
    }),
    probe: {
      raw_body: '{"data":{"usage_pct":88}}',
      probe: { kind: 'observation', resource: { domain: 'minio', type: 'bucket', name: '' }, severity: 'info', summary: 'usage 88%', data: { 'data.usage_pct': '88' } },
      inferred_by: 'rules',
      inferred: { kind: 'observation', severity_path: '$.data.severity', summary_template: '{{data.usage_pct}}% 已用', fields: { usage_pct: '$.data.usage_pct' } },
    },
    ...overrides,
  };
}

describe('UrlEntry', () => {
  test('渲染单一后端接口地址输入框 + 接入按钮', () => {
    const { wrapper } = mountEntry();
    expect(wrapper.find('[data-test="url-entry"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="url-endpoint-input"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="url-probe-generate"]').text()).toContain('接入');
  });

  test('无地址时接入按钮禁用', () => {
    const { wrapper } = mountEntry({ urlEndpoint: ref('') });
    const button = wrapper.find('[data-test="url-probe-generate"]').element as HTMLButtonElement;
    expect(button.disabled).toBe(true);
  });

  test('点击接入并验证后由单一动作完成探测和进入评审', async () => {
    const fn = vi.fn(() => Promise.resolve());
    const { wrapper } = mountEntry({ urlEndpoint: ref('https://m/api/x'), runUrlProbeGenerate: fn });
    const button = wrapper.find('[data-test="url-probe-generate"]');
    expect(button.text()).toContain('接入并验证');
    await button.trigger('click');
    expect(fn).toHaveBeenCalledOnce();
    expect(wrapper.find('[data-test="url-commit-draft"]').exists()).toBe(false);
  });

  test('POST 方法展示示例响应体输入框', () => {
    const { wrapper } = mountEntry({ urlMethod: ref('POST') });
    expect(wrapper.find('[data-test="url-sample-body-input"]').exists()).toBe(true);
  });

  test('GET 方法不展示示例响应体输入框', () => {
    const { wrapper } = mountEntry({ urlMethod: ref('GET') });
    expect(wrapper.find('[data-test="url-sample-body-input"]').exists()).toBe(false);
  });

  test('接入结果由评审页承接，不在入口重复展示提交动作', () => {
    const { wrapper } = mountEntry({ generatedUrlResult: ref(makeGeneratedResult()) });
    expect(wrapper.find('[data-test="url-commit-draft"]').exists()).toBe(false);
  });

  test('点击清除结果调用 clearUrlProbeGenerate', async () => {
    const fn = vi.fn();
    const { wrapper } = mountEntry({ generatedUrlResult: ref(makeGeneratedResult()), clearUrlProbeGenerate: fn });
    await wrapper.find('[data-test="url-clear-result"]').trigger('click');
    expect(fn).toHaveBeenCalledOnce();
  });
});