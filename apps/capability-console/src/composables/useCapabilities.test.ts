import { describe, expect, it, vi, beforeEach } from 'vitest';

vi.mock('../api', () => ({
  listCapabilities: vi.fn(),
  saveDraft: vi.fn(),
  testCapability: vi.fn(),
  validateCapability: vi.fn(),
  publishCapability: vi.fn(),
  unpublishCapability: vi.fn(),
  quickPublishCapability: vi.fn(),
  sendAssistantMessage: vi.fn(),
}));

import { useCapabilities } from './useCapabilities';
import { listCapabilities } from '../api';
import { normalizeCapability } from '../capability';
import type { ManagedCapability } from '../types';

const mockedList = vi.mocked(listCapabilities);

function makeCapability(overrides: Partial<ManagedCapability> = {}): ManagedCapability {
  return normalizeCapability({
    name: 'minio.bucket.capacity.read',
    domain: 'minio',
    resource_type: 'bucket',
    operation: 'read',
    risk: 'low',
    source: 'discovered',
    backend: { adapter: 'http', method: 'GET', path: '/api/minio/{cluster}/buckets/{bucket}/capacity', timeout_ms: 3000, base_url: 'https://middleware.example.com' },
    input_schema: {
      environment: { type: 'string', required: true },
      cluster: { type: 'string', required: true },
    },
    output: { kind: 'observation', severity_path: '', summary_template: '', fields: { usage_pct: '$.usage_pct' } },
    ...overrides,
  });
}

function makePublished(overrides: Partial<ManagedCapability> = {}): ManagedCapability {
  return makeCapability({ source: 'published', ...overrides });
}

describe('useCapabilities — testInputText 残留', () => {
  beforeEach(() => {
    mockedList.mockReset();
    mockedList.mockResolvedValue({ configured: true, capabilities: [] });
  });

  it('跨能力切换时重置 testInputText 为默认', async () => {
    const caps = useCapabilities();
    const a = makeCapability();
    const b = makePublished({ name: 'kafka.topic.retention.read', domain: 'kafka' });

    await caps.loadCapabilities();
    caps.selectCapability(a);
    caps.testInputText.value = '{"environment":"prod","cluster":"c1"}';

    // 切到另一个能力（source 或 name 不同 → key 不同）应重置
    caps.selectCapability(b);
    expect(caps.testInputText.value).toBe('{"environment":"prod"}');
  });

  it('同能力重选保留有效字段值', async () => {
    const caps = useCapabilities();
    const a = makeCapability();

    caps.selectCapability(a);
    caps.testInputText.value = '{"environment":"prod","cluster":"c1"}';

    // 同 key 重选（如保存草稿后）应保留用户输入
    caps.selectCapability(makeCapability());
    expect(caps.testInputText.value).toBe('{"environment":"prod","cluster":"c1"}');
  });

  it('同能力重选时清理 schema 已不存在的字段', async () => {
    const caps = useCapabilities();
    const original = makeCapability();

    caps.selectCapability(original);
    caps.testInputText.value = '{"environment":"prod","region":"cn-east"}';

    // 同一能力但 schema 已变化：region 字段被移除
    const evolved = makeCapability({
      input_schema: {
        environment: { type: 'string', required: true },
        cluster: { type: 'string', required: true },
      },
    });
    caps.selectCapability(evolved);

    // 残留的旧字段 region 被清理（不在 schema，也不在路径变量），environment 保留
    expect(caps.testInputText.value).toBe('{"environment":"prod"}');
  });

  it('同能力重选时保留仍存在的字段值', async () => {
    const caps = useCapabilities();
    const original = makeCapability();

    caps.selectCapability(original);
    caps.testInputText.value = '{"environment":"staging","region":"cn-east"}';

    // schema 去掉 region，但保留 environment
    const evolved = makeCapability({
      input_schema: { environment: { type: 'string', required: true }, cluster: { type: 'string', required: true } },
    });
    caps.selectCapability(evolved);

    // environment 仍在合法字段集合中，值保留；region 被清理
    expect(caps.testInputText.value).toBe('{"environment":"staging"}');
  });
});

describe('useCapabilities — 加载落地阶段', () => {
  it('能力库为空时首次加载落在 source（接入 API）', async () => {
    mockedList.mockResolvedValue({ configured: true, capabilities: [] });
    const caps = useCapabilities();
    await caps.loadCapabilities();
    expect(caps.managementPhase.value).toBe('source');
  });

  it('存在能力库时首次加载落在 review（评审发布）', async () => {
    mockedList.mockResolvedValue({ configured: true, capabilities: [makeCapability()] });
    const caps = useCapabilities();
    await caps.loadCapabilities();
    expect(caps.managementPhase.value).toBe('review');
  });

  it('首次加载定稿后，后续刷新保持当前阶段不打断', async () => {
    mockedList.mockResolvedValue({ configured: true, capabilities: [] });
    const caps = useCapabilities();
    await caps.loadCapabilities();
    expect(caps.managementPhase.value).toBe('source');

    // 用户已进入评审阶段后刷新，即使能力库仍为空也不打回 source
    caps.managementPhase.value = 'review';
    mockedList.mockResolvedValue({ configured: true, capabilities: [] });
    await caps.loadCapabilities();
    expect(caps.managementPhase.value).toBe('review');
  });
});
