import { mount } from '@vue/test-utils';
import ElementPlus from 'element-plus';
import { computed } from 'vue';
import { describe, expect, test } from 'vitest';
import ReviewConfirmCards from './ReviewConfirmCards.vue';
import { makeCapabilities } from './testHelpers';
import { normalizeCapability } from '../../capability';
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
    input_schema: { cluster: { type: 'string', required: true, examples: ['demo'] }, bucket: { type: 'string', required: true, examples: ['archive'] } },
    output: { kind: 'observation', severity_path: '', summary_template: 'Bucket {bucket} usage is {usage_pct}%', fields: { usage_pct: '$.usage_pct' } },
    ...overrides,
  });
}

describe('ReviewConfirmCards', () => {
  test('确认卡按顺序展示工具说明、验证问题、发布，不出现 JSONPath', () => {
    const wrapper = mount(ReviewConfirmCards, {
      props: { capabilities: makeCapabilities() },
      global: { plugins: [ElementPlus] },
    });

    expect(wrapper.find('[data-test="confirm-card-tool"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="confirm-card-verify"]').exists()).toBe(true);
    expect(wrapper.find('[data-test="confirm-card-publish"]').exists()).toBe(true);
    expect(wrapper.text()).not.toContain('JSONPath');
    expect(wrapper.text()).not.toContain('输出映射');
  });

  test('工具说明卡展示名称、用途和参数，并允许编辑名称', async () => {
    const capabilities = makeCapabilities();
    capabilities.selectCapability(makeCapability());
    const wrapper = mount(ReviewConfirmCards, {
      props: { capabilities },
      global: { plugins: [ElementPlus] },
    });

    expect(wrapper.text()).toContain('查询 MinIO 桶容量'.length > 0 ? 'minio.bucket.capacity.read' : '');
    const nameInput = wrapper.find('[data-test="confirm-card-tool-name"]');
    expect(nameInput.exists()).toBe(true);
    await nameInput.setValue('minio.bucket.usage.read');
    expect(capabilities.selected.value.name).toBe('minio.bucket.usage.read');
  });

  test('验证卡把参数展示为资源问题并支持逐项填写', async () => {
    const capabilities = makeCapabilities();
    // 走真实选中链路：testInputRows 依赖 editor 内部的 selectCapability 装载。
    capabilities.selectCapability(makeCapability());
    capabilities.probeResult.value = {
      probe: { kind: 'observation', resource: { domain: 'minio', type: 'bucket', name: 'archive' }, severity: 'ok', summary: 'usage 88%', data: { 'data.usage_pct': '88' } },
      inferred: { kind: 'observation', severity_path: '$.status', summary_template: 'Bucket {bucket} usage is {usage_pct}%', fields: { usage_pct: '$.data.usage_pct' } },
      inferred_by: 'rules',
    };
    const wrapper = mount(ReviewConfirmCards, {
      props: { capabilities },
      global: { plugins: [ElementPlus] },
    });

    await wrapper.find('[data-test="verify-question-input"]').setValue('archive 桶现在用了多少空间？');
    const clusterParam = wrapper.find('[data-test="verify-param-cluster"]');
    expect(clusterParam.exists()).toBe(true);
    await clusterParam.setValue('demo');
    await wrapper.find('[data-test="verify-run"]').trigger('click');

    expect(capabilities.testInputFieldValue('cluster')).toBe('demo');
    expect(wrapper.find('[data-test="verify-result"]').exists()).toBe(true);
  });

  test('发布卡在验证通过前保持禁用并说明原因', () => {
    const capabilities = makeCapabilities();
    capabilities.selectCapability(makeCapability());
    const wrapper = mount(ReviewConfirmCards, {
      props: { capabilities },
      global: { plugins: [ElementPlus] },
    });

    const publishButton = wrapper.find('[data-test="publish-confirm"]');
    expect((publishButton.element as HTMLButtonElement).disabled).toBe(true);
    expect(wrapper.text()).toContain('先完成验证');
  });

  test('写能力验证卡不出现实调参数，改为粘贴示例响应体并说明治理边界', async () => {
    const capabilities = makeCapabilities({ publishReady: computed(() => true) });
    capabilities.selectCapability(
      makeCapability({
        name: 'kafka.topic.retention.update',
        operation: 'write',
        risk: 'medium',
        backend: { adapter: 'http', method: 'POST', path: '/api/kafka/topics/{topic}/retention', timeout_ms: 3000, base_url: 'https://middleware.example.com' },
      }),
    );
    const wrapper = mount(ReviewConfirmCards, {
      props: { capabilities },
      global: { plugins: [ElementPlus] },
    });

    // 读模式的问题输入与参数框不出现，换成示例响应体输入。
    expect(wrapper.find('[data-test="verify-question-input"]').exists()).toBe(false);
    expect(wrapper.find('[data-test="verify-param-cluster"]').exists()).toBe(false);
    const sampleInput = wrapper.find('[data-test="verify-sample-body"]');
    expect(sampleInput.exists()).toBe(true);

    await sampleInput.setValue('{"code":0,"data":{"status":"ok"}}');
    expect(capabilities.testSampleBody.value).toBe('{"code":0,"data":{"status":"ok"}}');

    // 未验证时给出写能力专属的阻断原因。
    expect(wrapper.text()).toContain('先粘贴一次真实调用的示例响应体');

    // 验证通过（后端离线推断返回 probe）后发布解锁，摘要标注样例推断。
    capabilities.probeResult.value = {
      probe: { kind: 'observation', resource: { domain: 'kafka', type: 'topic', name: 'orders' }, severity: 'ok', summary: 'retention updated', data: {} },
      inferred_by: 'rules',
    };
    await wrapper.find('[data-test="verify-run"]').trigger('click');
    expect((wrapper.find('[data-test="publish-confirm"]').element as HTMLButtonElement).disabled).toBe(false);
    expect(wrapper.text()).toContain('已通过（样例推断）');
  });
});
