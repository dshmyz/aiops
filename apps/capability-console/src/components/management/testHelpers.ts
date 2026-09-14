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
  ProbeInferResult,
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

  // selectCapability 走真实链路：更新 selected、validation，并按新能力重置测试
  // 输入。依赖 testInputRows（评审确认卡参数区）的组件测试经此装载能力。
  function selectCapability(capability: ManagedCapability) {
    selected.value = normalizeCapability(JSON.parse(JSON.stringify(capability)) as Partial<ManagedCapability>);
    validation.value = selected.value.validation;
  }

  // 测试输入 JSON 的可变引用：testInputFieldValue/setTestInputField 读写它，
  // 模拟编辑器里 testInputText 的行为。
  const testInputTextRef = ref('{}');

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
    probeResult: ref<ProbeInferResult | null>(null),
    probeLoading: ref(false),
    error: ref(''),
    loading: ref(false),
    configured: ref(true),
    testInputText: testInputTextRef,
    testSampleBody: ref(''),
    searchText: ref(''),
    statusFilter: ref('all'),
    domainFilter: ref('all'),
    invalidOnly: ref(false),
    importOpenAPIURLText: ref('http://example/v3/api-docs'),
    importBackendBaseURL: ref('https://middleware.example.com'),
    importMessage: ref(''),
    importWizardStep: ref<'source' | 'candidates' | 'adjust' | 'commit'>('source'),
    managementPhase: ref<'source' | 'candidates' | 'review' | 'ai'>('source'),
    wizardOpen: ref(true),
    importPreview,
    importPreviewLoading: ref(false),
    importPreviewGeneration: ref(0),
    importCommitLoading: ref(false),
    candidateSelections: ref<Record<string, boolean>>({}),
    candidateOverrides: ref<Record<string, ImportCandidateOverride>>({}),
    candidateFilters: ref({ search: '', recommendation: 'all', domain: 'all' }),
    importBatch,
    importDomainFilter: ref('all'),
    urlEndpoint: ref(''),
    urlMethod: ref('GET'),
    urlDescription: ref(''),
    urlSampleBody: ref(''),
    urlImportLoading: ref(false),
    generatedUrlResult: ref(null),
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
    candidateProbeResults: ref({}),
    candidateProbing: ref(false),
    candidateProbedProgress: ref(0),
    candidateProbedCount: computed(() => 0),
    cleanProbedCandidates: computed(() => []),
    problemProbedCandidates: computed(() => []),
    publishedCapabilityNames: computed(() => new Set<string>()),
    stats,
    filteredCapabilities,
    inputRows: computed(() => Object.entries(selected.value.input_schema).map(([name, field]) => ({ name, type: field.type, required: field.required }))),
    testInputRows: computed(() => {
      const rows = Object.entries(selected.value.input_schema).map(([name, field]) => ({ name, type: field.type, required: field.required, source: 'schema', description: field.description, examples: field.examples, enum: field.enum }));
      // 路径变量也参与测试输入（与编辑器派生逻辑一致）。
      const seen = new Set(rows.map((row) => row.name));
      for (const match of selected.value.backend.path.matchAll(/\{([a-zA-Z0-9_]+)\}/g)) {
        if (!seen.has(match[1])) {
          rows.push({ name: match[1], type: 'string' as const, required: true, source: 'path', description: undefined, examples: undefined, enum: undefined });
        }
      }
      return rows;
    }),
    outputRows: computed(() => []),
    publishTargetPath: computed(() => ''),
    governanceSummary: computed(() => ''),
    publishChecks: computed(() => []),
    publishReady: computed(() => false),

    loadCapabilities: () => Promise.resolve(),
    selectCapability,
    newDraft: () => {},
    openManualCapability: () => {},
    saveSelectedDraft: () => Promise.resolve(),
    validateSelected: () => Promise.resolve(),
    testSelected: () => Promise.resolve(),
    probeSelected: () => Promise.resolve(),
    applyInferredOutput: () => {},
    probeSelectedCandidates: () => Promise.resolve(),
    runUrlProbeGenerate: () => Promise.resolve(),
    clearUrlProbeGenerate: () => {},
    commitUrlProbeGenerate: () => {},
    previewSwaggerURL: () => Promise.resolve(),
    clearImportPreview: () => {},
    loadBuiltinExample: () => Promise.resolve(),
    builtinExampleActive: computed(() => false),
    commitSwaggerImport: () => Promise.resolve(),
    publishVerifiedDrafts: () => Promise.resolve({ published: [], failed: [] }),
    updateCandidateOverride: () => {},
    toggleImportIgnored: () => {},
    openImportedCapability: () => {},
    publishSelected: () => Promise.resolve(),
    publishCurrent: () => Promise.resolve(),
    unpublishSelected: () => Promise.resolve(),
    deleteSelectedDraft: () => Promise.resolve(),
    handleQuickPublished: () => {},
    handleQuickPublishError: () => {},
    enrichSelected: () => Promise.resolve(),
    enrichLoading: ref(false),
    enrichAllDrafts: () => Promise.resolve(0),
    enrichAllLoading: ref(false),
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
    testInputFieldValue: (name: string) => {
      try {
        const parsed = JSON.parse(testInputTextRef.value) as Record<string, unknown>;
        const value = parsed[name];
        return typeof value === 'string' || typeof value === 'boolean' || typeof value === 'number' ? value : '';
      } catch {
        return '';
      }
    },
    setTestInputField: (name: string, rawValue: string | boolean) => {
      let parsed: Record<string, unknown> = {};
      try {
        parsed = JSON.parse(testInputTextRef.value) as Record<string, unknown>;
      } catch { /* 保持空对象 */ }
      if (rawValue === '') {
        delete parsed[name];
      } else {
        parsed[name] = rawValue;
      }
      testInputTextRef.value = JSON.stringify(parsed);
    },
    sourceLabel: (source: string) => source,
    capabilityStateLabel: () => '草稿',
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
