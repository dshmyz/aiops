import { describe, expect, it, vi, beforeEach } from 'vitest';
import { ref } from 'vue';

vi.mock('../api', () => ({
  previewOpenAPIURL: vi.fn(),
  commitOpenAPIURLImport: vi.fn(),
}));

import { useCapabilityImport } from './useCapabilityImport';
import type { ManagementPhase } from './useCapabilities';
import { previewOpenAPIURL } from '../api';
import { normalizeCapability } from '../capability';
import type { ImportPreview, ManagedCapability } from '../types';

const mockedPreview = vi.mocked(previewOpenAPIURL);

const BUILTIN_OPENAPI_URL = 'http://127.0.0.1:19090/v3/api-docs';
const BUILTIN_BASE_URL = 'http://127.0.0.1:19090';

function makePreview(overrides: Partial<ImportPreview> = {}): ImportPreview {
  return {
    source: { openapi_url: BUILTIN_OPENAPI_URL, backend_base_url: BUILTIN_BASE_URL, fingerprint: 'fp-1' },
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
          backend: { adapter: 'http', method: 'GET', path: '/api/minio/{cluster}/buckets/{bucket}/capacity', timeout_ms: 3000, base_url: BUILTIN_BASE_URL },
          input_schema: {
            environment: { type: 'string', required: true },
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

describe('useCapabilityImport / loadBuiltinExample', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('载入内置示例：填充 mock 地址并触发预览', async () => {
    mockedPreview.mockResolvedValue(makePreview());
    const { error, managementPhase, wizard } = setup();

    await wizard.loadBuiltinExample();

    expect(wizard.importOpenAPIURLText.value).toBe(BUILTIN_OPENAPI_URL);
    expect(wizard.importBackendBaseURL.value).toBe(BUILTIN_BASE_URL);
    expect(mockedPreview).toHaveBeenCalledWith({
      openapi_url: BUILTIN_OPENAPI_URL,
      backend_base_url: BUILTIN_BASE_URL,
    });
    expect(wizard.importPreview.value?.candidates.length).toBe(1);
    expect(wizard.importMessage.value).toContain('1 个候选 API');
    expect(managementPhase.value).toBe('candidates');
    expect(error.value).toBe('');
  });

  it('内置示例地址匹配时 builtinExampleActive 为 true，改掉后为 false', async () => {
    const { wizard } = setup();
    wizard.importOpenAPIURLText.value = BUILTIN_OPENAPI_URL;
    wizard.importBackendBaseURL.value = BUILTIN_BASE_URL;
    expect(wizard.builtinExampleActive.value).toBe(true);

    wizard.importOpenAPIURLText.value = 'http://other/v3/api-docs';
    expect(wizard.builtinExampleActive.value).toBe(false);
  });

  it('mock 未启动（预览失败）时给出启动命令提示', async () => {
    mockedPreview.mockRejectedValue(new Error('Failed to fetch'));
    const { error, wizard } = setup();

    await wizard.loadBuiltinExample();

    expect(error.value).toContain('mock 服务');
    expect(error.value).toContain('node examples/mock-middleware-api.js');
    expect(wizard.importPreview.value).toBeNull();
  });

  it('手动预览（previewSwaggerURL）失败时保留原始错误信息，不被内置示例提示覆盖', async () => {
    mockedPreview.mockRejectedValue(new Error('network down'));
    const { error, wizard } = setup();

    await wizard.previewSwaggerURL();

    expect(error.value).toBe('network down');
    expect(error.value).not.toContain('mock 服务');
  });
});
