import { ref, computed } from 'vue';
import type { UseCapabilities } from '../../composables/useCapabilities';
import { normalizeCapability } from '../../capability';
import type { ImportBatch } from '../../importBatch';
import type { ImportCandidateOverride } from '../../types';
import type {
  AssistantConsoleResponse,
  ImportCandidate,
  ImportPreview,
  ManagedCapability,
  NormalizedResult,
  ValidationResult,
} from '../../types';

/**
 * 创建一个完整的 UseCapabilities mock 对象，用于 management 子组件测试。
 * 所有参数均可通过 overrides 覆盖，方便各组件按需定制测试场景。
 */
export function makeCapabilities(overrides: Partial<UseCapabilities> = {}): UseCapabilities {
  const capabilities = ref<ManagedCapability[]>([]);
  const selected = ref<ManagedCapability>(normalizeCapability({}));
  const validation = ref<ValidationResult>({ valid: false, error: '未校验' });
  const importPreview = ref<ImportPreview | null>(null);
  const importBatch = ref<ImportBatch | null>(null);

  // 若 overrides 提供 capabilities，注入到局部 ref 上，保证后续 computed
  // （filteredCapabilities / paginatedCapabilities）仍引用同一个数据源。
  if (overrides.capabilities) {
    capabilities.value = overrides.capabilities.value;
    delete overrides.capabilities;
  }

  const stats = computed(() => ({
    published: 0,
    review: 0,
    invalid: 0,
    publishable: 0,
  }));

  const filteredCapabilities = computed(() => capabilities.value);

  return {
    capabilities,
    selected,
    validation,
    preview: ref<NormalizedResult | null>(null),
    error: ref(''),
    loading: ref(false),
    configured: ref(true),
    testInputText: ref('{"environment":"prod"}'),
    searchText: ref(''),
    statusFilter: ref('all'),
    domainFilter: ref('all'),
    importOpenAPIURLText: ref('http://example/v3/api-docs'),
    importBackendBaseURL: ref('https://middleware.example.com'),
    importMessage: ref(''),
    importWizardStep: ref<'source' | 'candidates' | 'adjust' | 'commit'>('source'),
    managementPhase: ref<'source' | 'candidates' | 'review' | 'ai'>('source'),
    importPreview,
    importPreviewLoading: ref(false),
    importPreviewGeneration: ref(0),
    importCommitLoading: ref(false),
    candidateSelections: ref<Record<string, boolean>>({}),
    candidateOverrides: ref<Record<string, ImportCandidateOverride>>({}),
    candidateFilters: ref({ search: '', recommendation: 'all', domain: 'all' }),
    importBatch,
    importDomainFilter: ref('all'),
    aiPromptOverride: ref<string | null>(null),
    aiResponse: ref<AssistantConsoleResponse | null>(null),
    aiError: ref(''),
    aiLoading: ref(false),

    derivedVariables: computed(() => []),
    validationLabel: computed(() => '未校验'),
    previewText: computed(() => ''),
    requestPreviewText: computed(() => ''),
    responsePreviewText: computed(() => ''),
    defaultAIPrompt: computed(() => ''),
    aiPromptText: computed({
      get: () => '',
      set: () => {},
    }),
    aiPreflightReady: computed(() => false),
    aiPreflightState: computed(() => '发布后再运行预检'),
    aiPreflightResultText: computed(() => ''),
    availableDomains: computed(() => []),
    visibleImportBatchItems: computed(() => []),
    ignoredImportCapabilityKeys: computed(() => new Set<string>()),
    visibleImportCandidates: computed<ImportCandidate[]>(() => []),
    importCandidateDomains: computed(() => []),
    selectedImportCandidates: computed(() => []),
    canCommitImportPreview: computed(() => false),
    importCommitSummary: computed(() => ({ selected: 0, reads: 0, writes: 0, highRisk: 0 })),
    publishedCapabilityNames: computed(() => new Set<string>()),
    stats,
    filteredCapabilities,
    inputRows: computed(() => []),
    testInputRows: computed(() => []),
    outputRows: computed(() => []),
    publishTargetPath: computed(() => ''),
    governanceSummary: computed(() => ''),
    publishChecks: computed(() => []),
    publishReady: computed(() => false),

    loadCapabilities: () => Promise.resolve(),
    selectCapability: () => {},
    newDraft: () => {},
    openManualCapability: () => {},
    saveSelectedDraft: () => Promise.resolve(),
    validateSelected: () => Promise.resolve(),
    testSelected: () => Promise.resolve(),
    previewSwaggerURL: () => Promise.resolve(),
    clearImportPreview: () => {},
    loadBuiltinExample: () => Promise.resolve(),
    builtinExampleActive: computed(() => false),
    commitSwaggerImport: () => Promise.resolve(),
    updateCandidateOverride: () => {},
    toggleImportIgnored: () => {},
    openImportedCapability: () => {},
    publishSelected: () => Promise.resolve(),
    publishCurrent: () => Promise.resolve(),
    unpublishSelected: () => Promise.resolve(),
    handleQuickPublished: () => {},
    handleQuickPublishError: () => {},
    runAIPreflight: () => Promise.resolve(),
    addInputField: () => {},
    removeInputField: () => {},
    renameInputField: () => {},
    setInputType: () => {},
    setInputRequired: () => {},
    addOutputField: () => {},
    removeOutputField: () => {},
    renameOutputField: () => {},
    setOutputPath: () => {},
    testInputFieldValue: () => '',
    setTestInputField: () => {},
    sourceLabel: (source: string) => source,
    operationLabel: (operation: string) => operation,
    riskLabel: (risk: string) => risk,
    recommendationLabel: (value: string) => value,
    candidateReasonText: () => '',
    candidateVerdictText: () => '',
    hasPublishedTwin: () => false,
    hasStaticToolConflict: () => false,
    isPublishable: () => false,
    publishActionLabel: () => '发布',
    currentPublishLabel: () => '发布',
    nextActionLabel: () => '评审',
    resetAIPreflight: () => {},
    // 分页和批量操作
    paginatedCapabilities: computed(() => capabilities.value),
    pageSize: ref(20),
    currentPage: ref(1),
    totalPages: computed(() => 1),
    groupedStats: computed(() => ({ draft: 0, review: 0, published: 0, other: 0 })),
    publishAll: () => Promise.resolve(undefined),
    ...overrides,
  };
}
