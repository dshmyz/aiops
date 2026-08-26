import { describe, expect, test } from 'vitest';
import {
  buildCommitSelections,
  createCandidateOverrides,
  createCandidateSelections,
  filterImportCandidates,
  selectedCandidates,
} from './importWizard';
import type { Capability, ImportPreview } from './types';

function capability(summary: Pick<Capability, 'name' | 'domain' | 'resource_type' | 'operation' | 'risk'>): Capability {
  return {
    schema_version: 1,
    status: 'needs_review',
    backend: {
      adapter: 'http',
      method: summary.operation === 'write' ? 'POST' : 'GET',
      path: '/',
      timeout_ms: 3000,
      base_url: 'https://middleware.example.com',
    },
    input_schema: {},
    output: {
      kind: 'observation',
      severity_path: '',
      summary_template: 'ok',
      fields: {},
    },
    auth: {
      roles: ['viewer'],
    },
    ai: {
      description: '',
      examples: [],
    },
    ...summary,
  };
}

const preview: ImportPreview = {
  source: {
    openapi_url: 'https://admin.example.com/v3/api-docs',
    backend_base_url: 'https://middleware.example.com',
    fingerprint: 'sha256:test',
  },
  stats: {
    total: 3,
    recommended: 1,
    needs_adjustment: 1,
    not_recommended: 1,
    read: 2,
    write: 1,
  },
  candidates: [
    {
      id: 'GET /api/minio/{cluster}/buckets/{bucket}/capacity',
      method: 'GET',
      path: '/api/minio/{cluster}/buckets/{bucket}/capacity',
      operation_id: 'getMinioBucketCapacity',
      capability: capability({
        name: 'minio.bucket.capacity.read',
        domain: 'minio',
        resource_type: 'bucket',
        operation: 'read',
        risk: 'low',
      }),
      recommendation: 'recommended',
      reasons: ['GET read operation'],
      warnings: [],
    },
    {
      id: 'GET /api/unknown/status',
      method: 'GET',
      path: '/api/unknown/status',
      capability: capability({
        name: 'unknown.resource.status.read',
        domain: 'unknown',
        resource_type: 'resource',
        operation: 'read',
        risk: 'low',
      }),
      recommendation: 'needs_adjustment',
      reasons: ['需要调整识别结果'],
      warnings: ['领域或资源类型需要确认'],
    },
    {
      id: 'POST /api/kafka/{cluster}/topics/{topic}/retention',
      method: 'POST',
      path: '/api/kafka/{cluster}/topics/{topic}/retention',
      capability: capability({
        name: 'kafka.topic.retention.update',
        domain: 'kafka',
        resource_type: 'topic',
        operation: 'write',
        risk: 'medium',
      }),
      recommendation: 'not_recommended',
      reasons: ['第一版暂不接入写入能力'],
      warnings: [],
    },
  ],
};

describe('import wizard helpers', () => {
  test('preserves the full backend capability payload', () => {
    expect(preview.candidates[0].capability.backend.path).toBe('/');
  });

  test('selects recommended candidates by default', () => {
    const selections = createCandidateSelections(preview);

    expect(selections['GET /api/minio/{cluster}/buckets/{bucket}/capacity']).toBe(true);
    expect(selections['GET /api/unknown/status']).toBe(false);
    expect(selections['POST /api/kafka/{cluster}/topics/{topic}/retention']).toBe(false);
    expect(selectedCandidates(preview, selections).map((candidate) => candidate.id)).toEqual([
      'GET /api/minio/{cluster}/buckets/{bucket}/capacity',
    ]);
  });

  test('creates editable overrides from candidate summaries', () => {
    const overrides = createCandidateOverrides(preview);

    expect(overrides['GET /api/minio/{cluster}/buckets/{bucket}/capacity']).toEqual({
      name: 'minio.bucket.capacity.read',
      domain: 'minio',
      resource_type: 'bucket',
      operation: 'read',
      risk: 'low',
    });
  });

  test('builds commit selections from selected candidates and overrides', () => {
    const selections = createCandidateSelections(preview);
    selections['GET /api/unknown/status'] = true;
    const overrides = createCandidateOverrides(preview);
    overrides['GET /api/unknown/status'] = {
      name: 'middleware.status.read',
      domain: 'middleware',
      resource_type: 'service',
      operation: 'read',
      risk: 'low',
    };

    expect(buildCommitSelections(preview, selections, overrides)).toEqual([
      {
        candidate_id: 'GET /api/minio/{cluster}/buckets/{bucket}/capacity',
        overrides: {
          name: 'minio.bucket.capacity.read',
          domain: 'minio',
          resource_type: 'bucket',
          operation: 'read',
          risk: 'low',
        },
      },
      {
        candidate_id: 'GET /api/unknown/status',
        overrides: {
          name: 'middleware.status.read',
          domain: 'middleware',
          resource_type: 'service',
          operation: 'read',
          risk: 'low',
        },
      },
    ]);
  });

  test('filters candidates by recommendation domain and search text', () => {
    expect(filterImportCandidates(preview, {
      recommendation: 'recommended',
      domain: 'all',
      search: '',
    }).map((candidate) => candidate.id)).toEqual([
      'GET /api/minio/{cluster}/buckets/{bucket}/capacity',
    ]);

    expect(filterImportCandidates(preview, {
      recommendation: 'all',
      domain: 'kafka',
      search: 'retention',
    }).map((candidate) => candidate.id)).toEqual([
      'POST /api/kafka/{cluster}/topics/{topic}/retention',
    ]);
  });
});
