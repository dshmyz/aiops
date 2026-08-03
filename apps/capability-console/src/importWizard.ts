import type {
  ImportCandidate,
  ImportCandidateOverride,
  ImportCommitSelection,
  ImportPreview,
  ImportRecommendation,
} from './types';

export interface ImportCandidateFilters {
  recommendation: ImportRecommendation | 'all';
  domain: string;
  search: string;
}

export function createCandidateSelections(preview: ImportPreview): Record<string, boolean> {
  return Object.fromEntries(preview.candidates.map((candidate) => [
    candidate.id,
    candidate.recommendation === 'recommended',
  ]));
}

export function createCandidateOverrides(preview: ImportPreview): Record<string, ImportCandidateOverride> {
  return Object.fromEntries(preview.candidates.map((candidate) => {
    const summary = candidate.summary ?? candidate.capability;
    return [candidate.id, {
      name: summary.name,
      domain: summary.domain,
      resource_type: summary.resource_type,
      operation: summary.operation,
      risk: summary.risk,
    }];
  }));
}

export function selectedCandidates(preview: ImportPreview, selections: Record<string, boolean>): ImportCandidate[] {
  return preview.candidates.filter((candidate) => selections[candidate.id]);
}

export function buildCommitSelections(
  preview: ImportPreview,
  selections: Record<string, boolean>,
  overrides: Record<string, ImportCandidateOverride>,
): ImportCommitSelection[] {
  return selectedCandidates(preview, selections).map((candidate) => ({
    candidate_id: candidate.id,
    overrides: overrides[candidate.id] ?? createCandidateOverrides({ ...preview, candidates: [candidate] })[candidate.id],
  }));
}

export function filterImportCandidates(preview: ImportPreview, filters: ImportCandidateFilters): ImportCandidate[] {
  const search = filters.search.trim().toLowerCase();
  return preview.candidates.filter((candidate) => {
    if (filters.recommendation !== 'all' && candidate.recommendation !== filters.recommendation) {
      return false;
    }
    const summary = candidate.summary ?? candidate.capability;
    if (filters.domain !== 'all' && summary.domain !== filters.domain) {
      return false;
    }
    if (search === '') {
      return true;
    }
    return [
      candidate.id,
      candidate.method,
      candidate.path,
      candidate.operation_id ?? '',
      summary.name,
      summary.domain,
      summary.resource_type,
    ].some((value) => value.toLowerCase().includes(search));
  });
}

export function importPreviewDomains(preview: ImportPreview): string[] {
  return Array.from(new Set(preview.candidates.map((candidate) => (candidate.summary ?? candidate.capability).domain || 'other'))).sort();
}
