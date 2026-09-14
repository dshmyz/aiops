import { describe, expect, it, vi } from 'vitest';
import { ref } from 'vue';
import { useCapabilityImport } from './useCapabilityImport';
import type { ManagementPhase } from './useCapabilities';
import type { ImportPreview, ManagedCapability } from '../types';
import { normalizeCapability } from '../capability';

vi.mock('../api', () => ({
  previewOpenAPIURL: vi.fn(),
  commitOpenAPIURLImport: vi.fn(),
  probeGenerateCapability: vi.fn(),
  probeInferCapability: vi.fn(),
  publishCapability: vi.fn(),
}));

function makeCapability(name: string, path = '/api/items', domain = 'demo', resourceType = 'item'): ManagedCapability {
  return normalizeCapability({
    name,
    source: 'discovered',
    status: 'needs_review',
    domain,
    resource_type: resourceType,
    operation: 'read',
    risk: 'low',
    backend: { adapter: 'http', method: 'GET', path, base_url: 'http://backend', timeout_ms: 3000 },
    input_schema: {},
    output: { kind: 'observation', severity_path: '', summary_template: '查询完成', fields: { id: '$.id' } },
    validation: { valid: true },
  });
}

function makePreview(fingerprint: string, name: string): ImportPreview {
  const capability = makeCapability(name, '/api/items', 'demo', 'item');
  return {
    source: { openapi_url: `http://${fingerprint}/openapi`, backend_base_url: 'http://backend', fingerprint },
    stats: { total: 1, recommended: 1, needs_adjustment: 0, not_recommended: 0, read: 1, write: 0 },
    candidates: [{
      id: 'GET /api/items',
      method: 'GET',
      path: '/api/items',
      operation_id: 'listItems',
      capability,
      summary: { name, domain: 'demo', resource_type: 'item', operation: 'read', risk: 'low' },
      recommendation: 'recommended',
      reasons: [],
      warnings: [],
    }],
  };
}

function setup() {
  const capabilities = ref<ManagedCapability[]>([]);
  const error = ref('');
  const managementPhase = ref<ManagementPhase>('source');
  const wizard = useCapabilityImport({ capabilities, error, managementPhase, onSelect: vi.fn() });
  return { wizard, managementPhase };
}

describe('useCapabilityImport batch transitions', () => {
  it('clears probe and imported batch state when starting a new preview', async () => {
    const { wizard } = setup();
    wizard.importPreview.value = makePreview('first', 'demo.items.first');
    wizard.candidateProbeResults.value = {};
    wizard.candidateProbedProgress.value = 1;
    wizard.importBatch.value = { items: [], domains: [], stats: { total: 0, read: 0, write: 0, selected: 0, ignored: 0, needsMapping: 0, notAIReady: 0 } };

    await wizard.previewSwaggerURL();

    expect(wizard.candidateProbeResults.value).toEqual({});
    expect(wizard.candidateProbedProgress.value).toBe(0);
    expect(wizard.importBatch.value).toBeNull();
  });


});
