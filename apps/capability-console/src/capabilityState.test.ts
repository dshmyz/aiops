import { describe, expect, it } from 'vitest';
import { normalizeCapability } from './capability';
import { uiStateOf } from './capabilityState';
import type { ManagedCapability } from './types';

function makeCapability(overrides: Partial<ManagedCapability> = {}): ManagedCapability {
  return normalizeCapability({
    name: 'minio.bucket.capacity.read',
    domain: 'minio',
    resource_type: 'bucket',
    operation: 'read',
    risk: 'low',
    source: 'discovered',
    status: 'needs_review',
    backend: {
      adapter: 'http',
      method: 'GET',
      path: '/api/minio/{cluster}/buckets/{bucket}/capacity',
      timeout_ms: 3000,
      base_url: 'https://middleware.example.com',
    },
    input_schema: { cluster: { type: 'string', required: true } },
    output: { kind: 'observation', severity_path: '', summary_template: '查询完成', fields: {} },
    validation: { valid: false, error: '未校验' },
    ...overrides,
  });
}

describe('uiStateOf', () => {
  it('把未完成配置归为草稿', () => {
    expect(uiStateOf(makeCapability(), () => false)).toBe('draft');
  });

  it('把定义完整但尚未发布的能力归为待验证', () => {
    expect(uiStateOf(makeCapability({ validation: { valid: true } }), () => false)).toBe('needs_validation');
  });

  it('把通过发布检查的能力归为可发布', () => {
    expect(uiStateOf(makeCapability({ validation: { valid: true } }), () => true)).toBe('publishable');
  });

  it('已发布能力优先归为已发布', () => {
    expect(uiStateOf(makeCapability({ source: 'published', status: 'published', validation: { valid: true } }), () => false)).toBe('published');
  });
});
