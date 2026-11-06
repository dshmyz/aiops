import { describe, expect, it, vi, beforeEach } from 'vitest';
import { ref } from 'vue';

vi.mock('../api', () => ({
  previewOpenAPIURL: vi.fn(),
  commitOpenAPIURLImport: vi.fn(),
  probeGenerateCapability: vi.fn(),
}));

import { useCapabilityImport, classifyProbeError } from './useCapabilityImport';
import type { ManagementPhase } from './useCapabilities';
import { previewOpenAPIURL, probeGenerateCapability } from '../api';
import { normalizeCapability } from '../capability';
import type { ManagedCapability, OutputSpec } from '../types';

const mockedPreview = vi.mocked(previewOpenAPIURL);
const mockedProbeGenerate = vi.mocked(probeGenerateCapability);

function setup() {
  const capabilities = ref<ManagedCapability[]>([]);
  const error = ref('');
  const managementPhase = ref<ManagementPhase>('source');
  const onSelect = vi.fn();
  const wizard = useCapabilityImport({ capabilities, error, managementPhase, onSelect });
  return { capabilities, error, managementPhase, onSelect, wizard };
}

describe('useCapabilityImport / Swagger 预览', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('手动预览（previewSwaggerURL）失败时保留原始错误信息', async () => {
    mockedPreview.mockRejectedValue(new Error('network down'));
    const { error, wizard } = setup();

    await wizard.previewSwaggerURL();

    expect(error.value).toBe('network down');
    expect(error.value).not.toContain('mock 服务');
  });
});

describe('useCapabilityImport / 单接口接入（probe-generate）', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  function makeGeneratedOutput(overrides: Partial<OutputSpec> = {}): OutputSpec {
    return {
      kind: 'observation',
      severity_path: '$.data.severity',
      summary_template: '{{data.usage_pct}}% 已用',
      fields: { usage_pct: '$.data.usage_pct' },
      ...overrides,
    };
  }

  it('runUrlProbeGenerate：把整条 URL 拆成 base_url + path 后调用 probe-generate', async () => {
    mockedProbeGenerate.mockResolvedValue({
      capability: { name: 'minio.bucket.capacity.read' } as never,
      probe: { raw_body: '{}', inferred_by: 'rules' },
    });
    const { wizard, error } = setup();
    wizard.urlEndpoint.value = 'https://middleware.example.com/api/minio/m1/buckets/archive/capacity';
    wizard.urlMethod.value = 'GET';
    wizard.urlDescription.value = ' 查看桶容量 ';

    await wizard.runUrlProbeGenerate();

    expect(mockedProbeGenerate).toHaveBeenCalledWith({
      backend: { adapter: 'http', method: 'GET', path: '/api/minio/{cluster}/buckets/{bucket}/capacity', base_url: 'https://middleware.example.com' },
      description: '查看桶容量',
      sample_body: undefined,
      input: { cluster: 'm1', bucket: 'archive' },
    });
    expect(wizard.generatedUrlResult.value).not.toBeNull();
    expect(wizard.urlImportLoading.value).toBe(false);
    expect(error.value).toBe('');
  });

  it('runUrlProbeGenerate：POST 写操作携带 sample_body 离线推断', async () => {
    mockedProbeGenerate.mockResolvedValue({
      capability: { name: 'kafka.topic.create' } as never,
      probe: { inferred_by: 'rules' },
    });
    const { wizard } = setup();
    wizard.urlEndpoint.value = 'https://middleware.example.com/api/kafka/topics';
    wizard.urlMethod.value = 'POST';
    wizard.urlSampleBody.value = '{"name":"orders"}';

    await wizard.runUrlProbeGenerate();

    expect(mockedProbeGenerate).toHaveBeenCalledWith({
      backend: { adapter: 'http', method: 'POST', path: '/api/kafka/topics', base_url: 'https://middleware.example.com' },
      description: undefined,
      sample_body: '{"name":"orders"}',
    });
  });

  it('runUrlProbeGenerate 失败时写入分类后的共享 error', async () => {
    mockedProbeGenerate.mockRejectedValue(new Error('backend unreachable'));
    const { wizard, error } = setup();
    wizard.urlEndpoint.value = 'https://middleware.example.com/api/x';

    await wizard.runUrlProbeGenerate();

    expect(error.value).toContain('无法连接后端');
    expect(wizard.generatedUrlResult.value).toBeNull();
  });

  it('runUrlProbeGenerate：显式 {var} 模板保留变量并把参数值作为探测实参', async () => {
    mockedProbeGenerate.mockResolvedValue({
      capability: { name: 'app.instance.metrics.read' } as never,
      probe: { raw_body: '{}', inferred_by: 'rules' },
    });
    const { wizard } = setup();
    wizard.urlEndpoint.value = 'https://backend.example.com/api/instances/{instance_id}/metrics';
    wizard.urlPathValues.value = { instance_id: 'i-123' };

    await wizard.runUrlProbeGenerate();

    expect(mockedProbeGenerate).toHaveBeenCalledWith({
      backend: { adapter: 'http', method: 'GET', path: '/api/instances/{instance_id}/metrics', base_url: 'https://backend.example.com' },
      description: undefined,
      sample_body: undefined,
      input: { instance_id: 'i-123' },
    });
    // 探测实参预填进 input examples
    const draft = wizard.generatedUrlResult.value!.capability;
    expect(draft.input_schema.instance_id.examples).toEqual(['i-123']);
  });

  it('runUrlProbeGenerate：查询参数行进 input_schema 标 in=query 并参与试调', async () => {
    mockedProbeGenerate.mockResolvedValue({
      capability: { name: 'app.instance.metrics.read' } as never,
      probe: { raw_body: '{}', inferred_by: 'rules' },
    });
    const { wizard } = setup();
    wizard.urlEndpoint.value = 'https://backend.example.com/api/instances';
    wizard.urlQueryParams.value = [{ key: 'limit', value: '10', required: false }];

    await wizard.runUrlProbeGenerate();

    expect(mockedProbeGenerate).toHaveBeenCalledWith(expect.objectContaining({
      input: { limit: '10' },
    }));
    const draft = wizard.generatedUrlResult.value!.capability;
    expect(draft.input_schema.limit).toMatchObject({ type: 'string', required: false, in: 'query', examples: ['10'] });
  });

  it('runUrlProbeGenerate：URL 自带的查询串自动并入参数行且地址栏还原成纯路径', async () => {
    mockedProbeGenerate.mockResolvedValue({
      capability: { name: 'app.list.read' } as never,
      probe: { raw_body: '{}', inferred_by: 'rules' },
    });
    const { wizard } = setup();
    wizard.urlEndpoint.value = 'https://backend.example.com/api/instances?status=ok';

    await wizard.runUrlProbeGenerate();

    expect(wizard.urlEndpoint.value).toBe('https://backend.example.com/api/instances');
    expect(wizard.urlQueryParams.value).toEqual([{ key: 'status', value: 'ok', required: false }]);
    expect(mockedProbeGenerate).toHaveBeenCalledWith(expect.objectContaining({
      backend: expect.objectContaining({ path: '/api/instances' }),
      input: { status: 'ok' },
    }));
  });

  it('runUrlProbeGenerate：已知模板路径的参数行编辑优先于 URL 字面量', async () => {
    mockedProbeGenerate.mockResolvedValue({
      capability: { name: 'minio.bucket.capacity.read' } as never,
      probe: { raw_body: '{}', inferred_by: 'rules' },
    });
    const { wizard } = setup();
    wizard.urlEndpoint.value = 'https://backend.example.com/api/minio/demo/buckets/archive/capacity';
    wizard.urlPathValues.value = { cluster: 'demo', bucket: 'logs' };

    await wizard.runUrlProbeGenerate();

    expect(mockedProbeGenerate).toHaveBeenCalledWith(expect.objectContaining({
      backend: expect.objectContaining({ path: '/api/minio/{cluster}/buckets/{bucket}/capacity' }),
      input: { cluster: 'demo', bucket: 'logs' },
    }));
  });

  it('runUrlProbeGenerate：查询参数行与路径变量同名时路径优先并给出提示', async () => {
    mockedProbeGenerate.mockResolvedValue({
      capability: { name: 'app.instance.metrics.read' } as never,
      probe: { raw_body: '{}', inferred_by: 'rules' },
    });
    const { wizard } = setup();
    wizard.urlEndpoint.value = 'https://backend.example.com/api/instances/{instance_id}/metrics';
    wizard.urlPathValues.value = { instance_id: 'i-123' };
    wizard.urlQueryParams.value = [{ key: 'instance_id', value: 'hijack', required: false }];

    await wizard.runUrlProbeGenerate();

    // 路径取值不被查询行劫持；schema 中该字段仍按路径变量处理（不标 in=query）。
    expect(mockedProbeGenerate).toHaveBeenCalledWith(expect.objectContaining({
      input: { instance_id: 'i-123' },
    }));
    const draft = wizard.generatedUrlResult.value!.capability;
    expect(draft.input_schema.instance_id).not.toMatchObject({ in: 'query' });
    expect(draft.input_schema.instance_id.examples).toEqual(['i-123']);
    expect(wizard.urlProbeWarnings.value.some((warning) => warning.includes('instance_id'))).toBe(true);
  });

  it('runUrlProbeGenerate 失败时按错误类型给引导', async () => {
    mockedProbeGenerate.mockRejectedValue(new Error('missing path value "instance_id"'));
    const { wizard, error } = setup();
    wizard.urlEndpoint.value = 'https://backend.example.com/api/instances/{instance_id}';

    await wizard.runUrlProbeGenerate();

    expect(error.value).toContain('路径参数 instance_id');
    expect(error.value).toContain('参数值');
  });

  it('classifyProbeError：401/403 指引去评审页配鉴权，连接失败指引查地址', async () => {
    expect(classifyProbeError('request failed: HTTP 401')).toContain('鉴权');
    expect(classifyProbeError('dial tcp 10.0.0.1:8080: connect: connection refused')).toContain('Base URL');
  });

  it('commitUrlProbeGenerate：把生成能力写回评审区并切到 review', () => {
    const { wizard, onSelect, managementPhase } = setup();
    wizard.generatedUrlResult.value = {
      capability: normalizeCapability({
        name: 'minio.bucket.capacity.read',
        backend: { adapter: 'http', method: 'GET', path: '/api/minio/m1/capacity', base_url: 'https://middleware.example.com', timeout_ms: 3000 },
        output: makeGeneratedOutput(),
      }),
      probe: { inferred_by: 'rules' },
    };

    wizard.commitUrlProbeGenerate();

    expect(onSelect).toHaveBeenCalledTimes(1);
    const selected = onSelect.mock.calls[0][0];
    expect(selected.name).toBe('minio.bucket.capacity.read');
    expect(selected.source).toBe('discovered');
    expect(managementPhase.value).toBe('review');
  });

  it('commitUrlProbeGenerate：无生成结果时静默跳过', () => {
    const { wizard, onSelect, managementPhase } = setup();

    wizard.commitUrlProbeGenerate();

    expect(onSelect).not.toHaveBeenCalled();
    expect(managementPhase.value).toBe('source');
  });
});
