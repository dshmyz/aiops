import { mount } from '@vue/test-utils';
import ElementPlus from 'element-plus';
import { describe, expect, test } from 'vitest';
import ThreeWayCompare from './ThreeWayCompare.vue';
import type { ProbeInferResult } from '../../types';

function mountCompare(probe: ProbeInferResult) {
  return mount(ThreeWayCompare, { props: { probe }, global: { plugins: [ElementPlus] } });
}

const sampleProbe: ProbeInferResult = {
  raw_body: '{"data":{"usage_pct":88,"severity":"info"}}',
  probe: {
    kind: 'observation',
    resource: { domain: 'minio', type: 'bucket', name: '' },
    severity: 'info',
    summary: 'usage 88%',
    data: { 'data.usage_pct': '88', 'data.severity': 'info' },
  },
  inferred_by: 'rules',
  inferred: {
    kind: 'observation',
    severity_path: '$.data.severity',
    summary_template: '{{data.usage_pct}}% 已用',
    fields: { usage_pct: '$.data.usage_pct' },
  },
};

describe('ThreeWayCompare', () => {
  test('渲染三段对比：原始响应 / 归一化 data / LLM 视角', () => {
    const wrapper = mountCompare(sampleProbe);
    expect(wrapper.find('[data-test="three-way-compare"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="twc-raw"]').text()).toContain('usage_pct');
    expect(wrapper.find('[data-test="twc-normalized"]').text()).toContain('88');
    expect(wrapper.find('[data-test="twc-inferred"]').text()).toContain('$.data.severity');
  });

  test('展示 inferred_by 标签与 severity', () => {
    const wrapper = mountCompare(sampleProbe);
    expect(wrapper.find('[data-test="twc-inferred-by-rules"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="twc-severity-info"]').exists()).toBe(true);
  });

  test('写操作无 probe（离线推断）时给出哨兵提示', () => {
    const wrapper = mountCompare({ inferred_by: 'rules', inferred: sampleProbe.inferred });
    expect(wrapper.find('[data-test="twc-normalized"]').text()).toContain('写操作离线推断');
  });

  test('无推断映射时给出哨兵提示', () => {
    const wrapper = mountCompare({ probe: sampleProbe.probe, inferred_by: undefined });
    expect(wrapper.find('[data-test="twc-inferred"]').text()).toContain('无推断映射');
  });
});