import type { ManagedCapability } from './types';

export type ImportVerdict = 'draft_ready' | 'needs_mapping' | 'not_ai_ready' | 'duplicate';

export interface ImportBatchItem {
  name: string;
  domain: string;
  method: string;
  path: string;
  operation: string;
  risk: string;
  capability: ManagedCapability;
  verdict: ImportVerdict;
  verdictLabel: string;
  reason: string;
  ignored: boolean;
}

export interface ImportBatchStats {
  total: number;
  read: number;
  write: number;
  selected: number;
  ignored: number;
  needsMapping: number;
  notAIReady: number;
}

export interface ImportBatch {
  items: ImportBatchItem[];
  domains: string[];
  stats: ImportBatchStats;
}

export function createImportBatch(items: ManagedCapability[], existing: ManagedCapability[]): ImportBatch {
  const existingPublishedNames = new Set(existing.filter((item) => item.source === 'published').map((item) => item.name));
  return buildBatch(
    items.map((capability) => {
      const verdict = classifyCapability(capability, existingPublishedNames);
      return {
        name: capability.name,
        domain: capability.domain || 'other',
        method: capability.backend.method || 'GET',
        path: capability.backend.path || '/',
        operation: capability.operation,
        risk: capability.risk,
        capability,
        verdict: verdict.verdict,
        verdictLabel: verdict.label,
        reason: verdict.reason,
        ignored: false,
      };
    }),
  );
}

export function setImportItemIgnored(batch: ImportBatch, name: string, ignored: boolean): ImportBatch {
  return buildBatch(batch.items.map((item) => (item.name === name ? { ...item, ignored } : item)));
}

export function filterImportBatchItems(batch: ImportBatch, domain: string): ImportBatchItem[] {
  if (domain === 'all') {
    return batch.items;
  }
  return batch.items.filter((item) => item.domain === domain);
}

function buildBatch(items: ImportBatchItem[]): ImportBatch {
  const domains = Array.from(new Set(items.map((item) => item.domain).filter(Boolean))).sort();
  const stats = {
    total: items.length,
    read: items.filter((item) => item.operation === 'read').length,
    write: items.filter((item) => item.operation === 'write').length,
    selected: items.filter((item) => !item.ignored).length,
    ignored: items.filter((item) => item.ignored).length,
    needsMapping: items.filter((item) => item.verdict === 'needs_mapping').length,
    notAIReady: items.filter((item) => item.verdict === 'not_ai_ready').length,
  };
  return { items, domains, stats };
}

function classifyCapability(
  capability: ManagedCapability,
  existingPublishedNames: Set<string>,
): { verdict: ImportVerdict; label: string; reason: string } {
  if (existingPublishedNames.has(capability.name)) {
    return { verdict: 'duplicate', label: '已有同名能力', reason: '已有同名能力' };
  }
  if (!capability.output.summary_template && Object.keys(capability.output.fields).length === 0) {
    return { verdict: 'needs_mapping', label: '需补映射', reason: '需补输出映射' };
  }
  return { verdict: 'draft_ready', label: '可生成草稿', reason: '可生成草稿' };
}
