import { computed, ref } from 'vue';
import type { Ref } from 'vue';
import { commitOpenAPIURLImport, previewOpenAPIURL } from '../api';
import { createImportBatch, filterImportBatchItems, setImportItemIgnored } from '../importBatch';
import {
  buildCommitSelections,
  createCandidateOverrides,
  createCandidateSelections,
  filterImportCandidates,
  importPreviewDomains,
  selectedCandidates,
} from '../importWizard';
import type { ImportBatch } from '../importBatch';
import type { ImportCandidateFilters } from '../importWizard';
import type {
  ImportCandidateOverride,
  ImportPreview,
  ManagedCapability,
} from '../types';
import { capabilityKey, upsert } from '../capabilityFormat';
import type { ManagementPhase } from './useCapabilities';

export type ImportWizardStep = 'source' | 'candidates' | 'adjust' | 'commit';

export interface UseCapabilityImportOptions {
  /** 能力列表（导入成功后 upsert 进去）。 */
  capabilities: Ref<ManagedCapability[]>;
  /** 共享错误提示，导入失败时写入。 */
  error: Ref<string>;
  /** 共享阶段，导入向导会切到 source / candidates / commit。 */
  managementPhase: Ref<ManagementPhase>;
  /** 导入生成草稿后选中首条，供评审区展示。 */
  onSelect: (capability: ManagedCapability) => void;
}

/**
 * useCapabilityImport 封装 Swagger 导入向导：URL 预览 → 候选筛选/调整 → 批量生成草稿。
 * 状态与逻辑自洽，通过 options 注入共享的能力列表与阶段，供 useCapabilities 组合。
 */
export function useCapabilityImport(options: UseCapabilityImportOptions) {
  const { capabilities, error, managementPhase, onSelect } = options;

  const importOpenAPIURLText = ref('http://你的后台/v3/api-docs');
  const importBackendBaseURL = ref('https://middleware.example.com');
  const importMessage = ref('');
  const importWizardStep = ref<ImportWizardStep>('source');
  const importPreview = ref<ImportPreview | null>(null);
  const importPreviewLoading = ref(false);
  const importPreviewGeneration = ref(0);
  const importCommitLoading = ref(false);
  const candidateSelections = ref<Record<string, boolean>>({});
  const candidateOverrides = ref<Record<string, ImportCandidateOverride>>({});
  const candidateFilters = ref<ImportCandidateFilters>({
    recommendation: 'all',
    domain: 'all',
    search: '',
  });
  const importBatch = ref<ImportBatch | null>(null);
  const importDomainFilter = ref('all');

  const visibleImportBatchItems = computed(() => {
    if (!importBatch.value) {
      return [];
    }
    return filterImportBatchItems(importBatch.value, importDomainFilter.value);
  });
  const ignoredImportCapabilityKeys = computed(() =>
    new Set(
      importBatch.value?.items
        .filter((item) => item.ignored)
        .map((item) => capabilityKey(item.capability)) ?? [],
    ),
  );
  const visibleImportCandidates = computed(() =>
    importPreview.value ? filterImportCandidates(importPreview.value, candidateFilters.value) : [],
  );
  const importCandidateDomains = computed(() => (importPreview.value ? importPreviewDomains(importPreview.value) : []));
  const selectedImportCandidates = computed(() =>
    importPreview.value ? selectedCandidates(importPreview.value, candidateSelections.value) : [],
  );
  const canCommitImportPreview = computed(() => selectedImportCandidates.value.length > 0 && !importCommitLoading.value);
  const importCommitSummary = computed(() => {
    const candidates = selectedImportCandidates.value;
    const effectiveCandidates = candidates.map((candidate) => candidateOverrides.value[candidate.id] ?? candidate.summary ?? candidate.capability);
    const reads = effectiveCandidates.filter((candidate) => candidate.operation === 'read').length;
    const writes = candidates.length - reads;
    const highRisk = effectiveCandidates.filter((candidate) => candidate.risk === 'high').length;
    return { selected: candidates.length, reads, writes, highRisk };
  });

  async function previewSwaggerURL() {
    error.value = '';
    importMessage.value = '';
    clearImportPreview();
    const generation = importPreviewGeneration.value;
    importPreviewLoading.value = true;
    try {
      const previewResult = await previewOpenAPIURL({
        openapi_url: importOpenAPIURLText.value,
        backend_base_url: importBackendBaseURL.value,
      });
      if (generation !== importPreviewGeneration.value) {
        return;
      }
      importPreview.value = previewResult;
      candidateSelections.value = createCandidateSelections(previewResult);
      candidateOverrides.value = createCandidateOverrides(previewResult);
      candidateFilters.value = { recommendation: 'all', domain: 'all', search: '' };
      importWizardStep.value = previewResult.candidates.length === 0 ? 'source' : 'candidates';
      managementPhase.value = previewResult.candidates.length === 0 ? 'source' : 'candidates';
      importMessage.value = previewResult.candidates.length === 0 ? '没有识别到可接入 API' : `已预览 ${previewResult.candidates.length} 个候选 API`;
    } catch (err) {
      if (generation !== importPreviewGeneration.value) {
        return;
      }
      error.value = err instanceof Error ? err.message : '预览 Swagger URL 失败';
    } finally {
      if (generation === importPreviewGeneration.value) {
        importPreviewLoading.value = false;
      }
    }
  }

  function clearImportPreview() {
    importPreviewGeneration.value += 1;
    importPreview.value = null;
    candidateSelections.value = {};
    candidateOverrides.value = {};
    candidateFilters.value = { recommendation: 'all', domain: 'all', search: '' };
    importWizardStep.value = 'source';
    managementPhase.value = 'source';
  }

  async function commitSwaggerImport() {
    if (!importPreview.value || !canCommitImportPreview.value) {
      return;
    }
    error.value = '';
    importCommitLoading.value = true;
    try {
      const result = await commitOpenAPIURLImport({
        openapi_url: importPreview.value.source.openapi_url || importOpenAPIURLText.value,
        backend_base_url: importPreview.value.source.backend_base_url || importBackendBaseURL.value,
        fingerprint: importPreview.value.source.fingerprint,
        selections: buildCommitSelections(importPreview.value, candidateSelections.value, candidateOverrides.value),
      });
      importBatch.value = createImportBatch(result.capabilities, capabilities.value);
      importDomainFilter.value = 'all';
      for (const item of result.capabilities) {
        upsert(capabilities, item);
      }
      if (result.capabilities.length > 0) {
        onSelect(result.capabilities[0]);
      }
      importWizardStep.value = 'commit';
      managementPhase.value = 'review';
      importMessage.value = result.capabilities.length === 0 ? '没有生成草稿' : `已生成 ${result.capabilities.length} 个待评审草稿`;
    } catch (err) {
      error.value = err instanceof Error ? err.message : '生成 Capability 草稿失败';
    } finally {
      importCommitLoading.value = false;
    }
  }

  function updateCandidateOverride(id: string, patch: Partial<ImportCandidateOverride>) {
    candidateOverrides.value = {
      ...candidateOverrides.value,
      [id]: { ...candidateOverrides.value[id], ...patch },
    };
  }

  function toggleImportIgnored(name: string, ignored: boolean) {
    if (!importBatch.value) {
      return;
    }
    importBatch.value = setImportItemIgnored(importBatch.value, name, ignored);
  }

  function openImportedCapability(item: ImportBatch['items'][number]) {
    const target = item.verdict === 'duplicate'
      ? capabilities.value.find((capability) => capability.name === item.name && capability.source === 'published')
      : capabilities.value.find((capability) => capabilityKey(capability) === capabilityKey(item.capability));
    if (target) {
      onSelect(target);
    }
  }

  return {
    importOpenAPIURLText,
    importBackendBaseURL,
    importMessage,
    importWizardStep,
    importPreview,
    importPreviewLoading,
    importPreviewGeneration,
    importCommitLoading,
    candidateSelections,
    candidateOverrides,
    candidateFilters,
    importBatch,
    importDomainFilter,
    visibleImportBatchItems,
    ignoredImportCapabilityKeys,
    visibleImportCandidates,
    importCandidateDomains,
    selectedImportCandidates,
    canCommitImportPreview,
    importCommitSummary,
    previewSwaggerURL,
    clearImportPreview,
    commitSwaggerImport,
    updateCandidateOverride,
    toggleImportIgnored,
    openImportedCapability,
  };
}

export type UseCapabilityImport = ReturnType<typeof useCapabilityImport>;
