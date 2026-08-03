import { describe, expect, test } from 'vitest';
import { normalizeCapability } from './capability';
import {
  createImportBatch,
  filterImportBatchItems,
  setImportItemIgnored,
} from './importBatch';
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

describe('import batch', () => {
  test('classifies imported operations and computes summary stats', () => {
    const batch = createImportBatch(
      [
        capability({ name: 'minio.bucket.capacity.read', domain: 'minio', operation: 'read' }),
        capability({
          name: 'kafka.topic.retention.update',
          domain: 'kafka',
          operation: 'write',
          risk: 'medium',
          backend: { adapter: 'http', method: 'POST', timeout_ms: 3000, base_url: 'https://middleware.example.com', path: '/api/kafka/{cluster}/topics/{topic}/retention' },
        }),
        capability({
          name: 'glusterfs.volume.status.read',
          domain: 'glusterfs',
          operation: 'read',
          output: { kind: 'observation', severity_path: '', summary_template: '', fields: {} },
        }),
      ],
      [],
    );

    expect(batch.stats).toEqual({
      total: 3,
      read: 2,
      write: 1,
      selected: 3,
      ignored: 0,
      needsMapping: 1,
      notAIReady: 0,
    });
    expect(batch.items.map((item) => [item.name, item.verdict, item.reason])).toEqual([
      ['minio.bucket.capacity.read', 'draft_ready', '可生成草稿'],
      ['kafka.topic.retention.update', 'draft_ready', '可生成草稿'],
      ['glusterfs.volume.status.read', 'needs_mapping', '需补输出映射'],
    ]);
  });

  test('marks duplicate imported operations against existing capabilities', () => {
    const batch = createImportBatch(
      [capability({ name: 'minio.bucket.capacity.read', domain: 'minio', operation: 'read' })],
      [capability({ name: 'minio.bucket.capacity.read', source: 'published', status: 'published' })],
    );

    expect(batch.items[0].verdict).toBe('duplicate');
    expect(batch.items[0].reason).toBe('已有同名能力');
  });

  test('does not mark repeated discovered imports as duplicate', () => {
    const batch = createImportBatch(
      [capability({ name: 'minio.bucket.capacity.read', domain: 'minio', operation: 'read' })],
      [capability({ name: 'minio.bucket.capacity.read', source: 'discovered', status: 'needs_review' })],
    );

    expect(batch.items[0].verdict).toBe('draft_ready');
    expect(batch.items[0].reason).toBe('可生成草稿');
  });

  test('filters by domain and tracks ignored items immutably', () => {
    const batch = createImportBatch(
      [
        capability({ name: 'minio.bucket.capacity.read', domain: 'minio' }),
        capability({ name: 'kafka.consumer_group.lag.read', domain: 'kafka', resource_type: 'consumer_group' }),
      ],
      [],
    );

    const next = setImportItemIgnored(batch, 'minio.bucket.capacity.read', true);

    expect(filterImportBatchItems(next, 'kafka').map((item) => item.name)).toEqual(['kafka.consumer_group.lag.read']);
    expect(next.stats.selected).toBe(1);
    expect(next.stats.ignored).toBe(1);
    expect(batch.stats.selected).toBe(2);
  });
});
