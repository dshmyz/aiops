import { describe, expect, it, vi, beforeEach } from 'vitest';
import { ref } from 'vue';
import { ElMessage } from 'element-plus';

vi.mock('../api', () => ({
  inferQuickPublish: vi.fn(),
}));

import { useQuickPublishInfer } from './useQuickPublishInfer';
import { inferQuickPublish } from '../api';

const mockedInfer = vi.mocked(inferQuickPublish);

function setup() {
  const baseURL = ref('https://middleware.example.com');
  const path = ref('/api/redis/clusters/{cluster}/info');
  const description = ref('查询 Redis 集群信息');
  const method = ref('GET' as const);
  const name = ref('');
  const domain = ref('');
  const resourceType = ref('');
  const summaryTemplate = ref('');

  const infer = useQuickPublishInfer({
    baseURL,
    path,
    description,
    method,
    name,
    domain,
    resourceType,
    summaryTemplate,
  });

  return { infer, baseURL, path, description, name, domain, resourceType, summaryTemplate };
}

describe('useQuickPublishInfer', () => {
  beforeEach(() => {
    mockedInfer.mockReset();
    vi.spyOn(ElMessage, 'success').mockImplementation(() => ({}) as never);
    vi.spyOn(ElMessage, 'warning').mockImplementation(() => ({}) as never);
  });

  it('推断结果填入用户未编辑的字段', async () => {
    const { infer, name, domain, resourceType } = setup();
    mockedInfer.mockResolvedValue({
      inferred: {
        name: 'redis.cluster.info.read',
        domain: 'redis',
        resource_type: 'cluster',
        backend_base_url: 'https://middleware.example.com',
        method: 'GET',
        path: '/api/redis/clusters/{cluster}/info',
        description: '查询 Redis 集群信息',
      },
    });

    await infer.doInfer();

    expect(name.value).toBe('redis.cluster.info.read');
    expect(domain.value).toBe('redis');
    expect(resourceType.value).toBe('cluster');
    expect(infer.hasInferred.value).toBe(true);
    expect(infer.inferredCount.value).toBe(3);
  });

  it('用户手动编辑过的字段不被推断覆盖', async () => {
    const { infer, name, domain } = setup();
    // 用户手动填了 name 和 domain
    name.value = 'my.custom.name';
    domain.value = 'custom';
    infer.markUserEdited('name');
    infer.markUserEdited('domain');

    mockedInfer.mockResolvedValue({
      inferred: {
        name: 'ai.generated.name',
        domain: 'ai',
        resource_type: 'cluster',
        backend_base_url: 'https://middleware.example.com',
        method: 'GET',
        path: '/api/redis/clusters/{cluster}/info',
        description: '查询 Redis 集群信息',
      },
    });

    await infer.doInfer();

    expect(name.value).toBe('my.custom.name');
    expect(domain.value).toBe('custom');
    // 未编辑的 resource_type 被补全
    expect(infer.inferredCount.value).toBe(1);
  });

  it('推断失败时提示 warning 且不阻塞', async () => {
    const { infer } = setup();
    mockedInfer.mockRejectedValue(new Error('upstream timeout'));

    await expect(infer.doInfer()).resolves.toBeUndefined();

    expect(ElMessage.warning).toHaveBeenCalled();
    expect(infer.hasInferred.value).toBe(false);
  });

  it('reset 清除推断与字段保护标记', async () => {
    const { infer, name } = setup();
    name.value = 'my.custom.name';
    infer.markUserEdited('name');
    mockedInfer.mockResolvedValue({
      inferred: {
        name: 'ai.generated.name',
        domain: 'redis',
        resource_type: 'cluster',
        backend_base_url: 'https://middleware.example.com',
        method: 'GET',
        path: '/api/redis/clusters/{cluster}/info',
        description: '查询 Redis 集群信息',
      },
    });

    // reset 前：已标记的字段不被覆盖
    await infer.doInfer();
    expect(name.value).toBe('my.custom.name');

    // reset 清空字段保护，重新推断可以覆盖
    infer.reset();
    await infer.doInfer();
    expect(name.value).toBe('ai.generated.name');
    expect(infer.hasInferred.value).toBe(true);
  });
});
