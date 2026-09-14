import { describe, expect, it } from 'vitest';
import { ref } from 'vue';
import { useCapabilityEditor } from './useCapabilityEditor';
import { normalizeCapability } from '../capability';
import type { ManagedCapability } from '../types';

function makeCapability(overrides: Partial<ManagedCapability> = {}): ManagedCapability {
  return normalizeCapability({
    name: 'demo.search.read',
    source: 'discovered',
    status: 'needs_review',
    domain: 'demo',
    resource_type: 'item',
    operation: 'read',
    risk: 'low',
    backend: { adapter: 'http', method: 'POST', path: '/search', base_url: 'http://backend', timeout_ms: 3000 },
    input_schema: {},
    output: { kind: 'observation', severity_path: '', summary_template: '查询完成', fields: { id: '$.id' } },
    validation: { valid: true },
    ...overrides,
  });
}

describe('useCapabilityEditor publish consistency', () => {
  it('does not mark a non-GET read capability as publish-ready', () => {
    const selected = ref(makeCapability());
    const editor = useCapabilityEditor({
      capabilities: ref([selected.value]),
      error: ref(''),
      managementPhase: ref('review'),
      onSelect: () => {},
    });
    editor.selectCapability(selected.value);
    expect(editor.publishReady.value).toBe(false);
    expect(editor.publishChecks.value.find((check) => check.label === '读取类能力')?.ok).toBe(false);
  });
});
