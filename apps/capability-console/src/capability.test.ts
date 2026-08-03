import { describe, expect, test } from 'vitest';
import { BUILTIN_TOOL_NAMES, canPublish, hasStaticToolConflict, normalizeCapability } from './capability';
import type { ManagedCapability } from './types';

function capability(partial: Partial<ManagedCapability>): ManagedCapability {
  return normalizeCapability({
    status: 'needs_review',
    source: 'discovered',
    validation: { valid: true },
    backend: { adapter: 'http', method: 'GET', timeout_ms: 3000, base_url: 'https://middleware.example.com', path: '/api/minio/{cluster}/buckets/{bucket}/capacity' },
    output: {
      kind: 'observation',
      severity_path: '',
      summary_template: 'Bucket {bucket} usage is {usage_pct}%',
      fields: { usage_pct: '$.data.usage_pct' },
    },
    ...partial,
  });
}

describe('canPublish', () => {
  test('allows publishing validated read GET capabilities', () => {
    expect(canPublish(capability({ operation: 'read' }))).toBe(true);
  });

  test('allows publishing validated write capabilities with mutating method', () => {
    expect(
      canPublish(
        capability({
          name: 'minio.bucket.quota.set',
          operation: 'write',
          risk: 'medium',
          backend: { adapter: 'http', method: 'POST', timeout_ms: 3000, base_url: 'https://middleware.example.com', path: '/api/minio/{cluster}/buckets/{bucket}/quota' },
        }),
      ),
    ).toBe(true);
  });

  test('rejects unpublished discovered drafts that have not been validated', () => {
    expect(canPublish(capability({ operation: 'read', validation: { valid: false } }))).toBe(false);
  });

  test('rejects already published capabilities regardless of operation', () => {
    expect(canPublish(capability({ operation: 'read', source: 'published', status: 'published' }))).toBe(false);
    expect(
      canPublish(
        capability({
          name: 'minio.bucket.quota.set',
          operation: 'write',
          source: 'published',
          status: 'published',
          backend: { adapter: 'http', method: 'POST', timeout_ms: 3000, base_url: 'https://middleware.example.com', path: '/api/minio/{cluster}/buckets/{bucket}/quota' },
        }),
      ),
    ).toBe(false);
  });
});

describe('BUILTIN_TOOL_NAMES', () => {
  test('covers all five static tools registered in the backend', () => {
    // 与 internal/tools/registry.go 中的常量保持一致：
    //   ClusterStatusRead, TopicRetentionSet, GlusterVolumeHealthRead,
    //   MinIOBucketHealthRead, KafkaConsumerLagRead
    expect(BUILTIN_TOOL_NAMES).toEqual([
      'cluster.status.read',
      'topic.retention.set',
      'glusterfs.volume.health.read',
      'minio.bucket.health.read',
      'kafka.consumer_lag.read',
    ]);
  });

  test('is frozen at runtime so callers cannot accidentally mutate the shared constant', () => {
    expect(Object.isFrozen(BUILTIN_TOOL_NAMES)).toBe(true);
    expect(BUILTIN_TOOL_NAMES.length).toBe(5);
  });
});

describe('hasStaticToolConflict', () => {
  test('returns true for every name in BUILTIN_TOOL_NAMES', () => {
    for (const name of BUILTIN_TOOL_NAMES) {
      expect(hasStaticToolConflict(name)).toBe(true);
    }
  });

  test('returns false for ordinary discovered capability names', () => {
    // 注意：cluster.status.read / topic.retention.set / glusterfs.volume.health.read
    // / minio.bucket.health.read / kafka.consumer_lag.read 都是内置工具名，会返回 true。
    // 这里挑长得像但并不冲突的名字做反向断言。
    expect(hasStaticToolConflict('minio.bucket.capacity.read')).toBe(false);
    expect(hasStaticToolConflict('kafka.topic.retention.set')).toBe(false);
    expect(hasStaticToolConflict('glusterfs.volume.health.check')).toBe(false);
  });

  test('returns false for empty and unrelated strings', () => {
    expect(hasStaticToolConflict('')).toBe(false);
    expect(hasStaticToolConflict('not.a.tool')).toBe(false);
  });

  test('is case-sensitive to avoid accidental collisions', () => {
    expect(hasStaticToolConflict('CLUSTER.STATUS.READ')).toBe(false);
    expect(hasStaticToolConflict('Cluster.Status.Read')).toBe(false);
  });
});
