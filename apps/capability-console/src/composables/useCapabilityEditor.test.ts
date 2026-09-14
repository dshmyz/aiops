import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { nextTick, ref } from 'vue';
import type { ManagedCapability } from '../types';

vi.mock('../api', () => ({
  saveDraft: vi.fn(),
  testCapability: vi.fn(),
  validateCapability: vi.fn(),
  sendAssistantMessage: vi.fn(),
  probeInferCapability: vi.fn(),
}));

import { useCapabilityEditor } from './useCapabilityEditor';
import { validateCapability } from '../api';
import { normalizeCapability } from '../capability';
import type { ManagementPhase } from './useCapabilities';

const mockedValidate = vi.mocked(validateCapability);

function makeCapability(name = 'minio.bucket.capacity.read'): ManagedCapability {
  return normalizeCapability({
    name,
    source: 'discovered',
    domain: 'minio',
    resource_type: 'bucket',
    operation: 'read',
    risk: 'low',
    backend: {
      adapter: 'http',
      method: 'GET',
      path: '/api/minio/{cluster}/buckets/{bucket}/capacity',
      base_url: 'https://middleware.example.com',
      timeout_ms: 3000,
    },
    input_schema: { cluster: { type: 'string', required: true } },
    output: { kind: 'table', fields: {}, summary_template: '', severity_path: '' },
  });
}

function setup() {
  const capabilities = ref<ManagedCapability[]>([]);
  const error = ref('');
  const managementPhase = ref<ManagementPhase>('review');
  const onSelect = vi.fn();
  const onViewChange = vi.fn();
  const editor = useCapabilityEditor({ capabilities, error, managementPhase, onSelect, onViewChange });
  return { editor, capabilities, error, managementPhase, onSelect, onViewChange };
}

describe('useCapabilityEditor 自动校验', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    mockedValidate.mockReset();
    mockedValidate.mockResolvedValue({ valid: true, error: '' });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('编辑触发一次自动校验后不会因写回结果而再次自循环调用', async () => {
    const { editor } = setup();
    // 选中一个能力（这本身也是一次深度变更，会被 debounce 收敛）
    editor.selectCapability(makeCapability());

    // 模拟用户编辑：触发 800ms 防抖自动校验
    editor.selected.value.name = 'minio.bucket.capacity.read.v2';
    await nextTick(); // 刷新 Vue 的 pre-flush watch，让 scheduleAutoValidate 排队
    await vi.advanceTimersByTimeAsync(800);

    // 第一次自动校验应发生一次
    expect(mockedValidate).toHaveBeenCalledTimes(1);

    // 关键回归：即使校验成功写回 selected，也不得再次自循环触发校验。
    await nextTick();
    await vi.advanceTimersByTimeAsync(2400);
    await nextTick();
    expect(mockedValidate).toHaveBeenCalledTimes(1);
  });
});