import { describe, expect, it, vi, beforeEach } from 'vitest';
import { ref } from 'vue';

vi.mock('../api', () => ({
  previewOpenAPIURL: vi.fn(),
  commitOpenAPIURLImport: vi.fn(),
  probeGenerateCapability: vi.fn(),
}));

import { useCapabilityImport } from './useCapabilityImport';
import type { ManagementPhase } from './useCapabilities';
import { previewOpenAPIURL, probeGenerateCapability } from '../api';
import { normalizeCapability } from '../capability';
import type { ImportPreview, ManagedCapability, OutputSpec } from '../types';

const mockedPreview = vi.mocked(previewOpenAPIURL);
const mockedProbeGenerate = vi.mocked(probeGenerateCapability);

const MOCK_OPENAPI_URL = 'http://127.0.0.1:19090/v3/api-docs';
const MOCK_BASE_URL = 'http://127.0.0.1:19090';

function makePreview(overrides: Partial<ImportPreview> = {}): ImportPreview {
  return {
    source: { openapi_url: MOCK_OPENAPI_URL, backend_base_url: MOCK_BASE_URL, fingerprint: 'fp-1' },
    stats: { total: 1, recommended: 1, needs_adjustment: 0, not_recommended: 0, read: 1, write: 0 },
    candidates: [
      {
        id: 'c1',
        method: 'GET',
        path: '/api/minio/{cluster}/buckets/{bucket}/capacity',
        operation_id: 'getCapacity',
        capability: normalizeCapability({
          name: 'minio.bucket.capacity.read',
          domain: 'minio',
          resource_type: 'bucket',
          operation: 'read',
          risk: 'low',
          source: 'discovered',
          backend: { adapter: 'http', method: 'GET', path: '/api/minio/{cluster}/buckets/{bucket}/capacity', timeout_ms: 3000, base_url: MOCK_BASE_URL },
          input_schema: {
            cluster: { type: 'string', required: true },
          },
          output: { kind: 'observation', severity_path: '', summary_template: '', fields: { usage_pct: '$.usage_pct' } },
        }),
        summary: { name: 'minio.bucket.capacity.read', domain: 'minio', resource_type: 'bucket', operation: 'read', risk: 'low' },
        recommendation: 'recommended',
        reasons: [],
        warnings: [],
      },
    ],
    ...overrides,
  };
}

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

  it('runUrlProbeGenerate 失败时写入共享 error', async () => {
    mockedProbeGenerate.mockRejectedValue(new Error('backend unreachable'));
    const { wizard, error } = setup();
    wizard.urlEndpoint.value = 'https://middleware.example.com/api/x';

    await wizard.runUrlProbeGenerate();

    expect(error.value).toBe('backend unreachable');
    expect(wizard.generatedUrlResult.value).toBeNull();
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
