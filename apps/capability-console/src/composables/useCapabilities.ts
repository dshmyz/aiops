import { computed, ref, watch } from 'vue';
import type { ComputedRef, Ref, WritableComputedRef } from 'vue';
import { listCapabilities } from '../api';
import type { ProbeGenerateResult } from '../api';
import { canPublish } from '../capability';
import {
  candidateReasonText,
  candidateVerdictText,
  operationLabel,
  recommendationLabel,
  riskLabel,
  sourceLabel,
} from '../capabilityFormat';
import { filterImportBatchItems } from '../importBatch';
import type { ImportBatch } from '../importBatch';
import { uiStateOf } from '../capabilityState';
import { filterImportCandidates, selectedCandidates } from '../importWizard';
import { useCapabilityEditor } from './useCapabilityEditor';
import { useCapabilityImport } from './useCapabilityImport';
import { useCapabilityPublish } from './useCapabilityPublish';
import type { PublishAllResult } from './useCapabilityPublish';
import type {
  AssistantConsoleResponse,
  Capability,
  ImportCandidate,
  ImportCandidateOverride,
  ImportPreview,
  ImportRecommendation,
  InputField,
  ManagedCapability,
  NormalizedResult,
  ProbeInferResult,
  ValidationResult,
} from '../types';

export type ManagementPhase = 'source' | 'candidates' | 'review' | 'ai';
export type ImportWizardStep = 'source' | 'candidates' | 'adjust' | 'commit';

export interface UseCapabilitiesOptions {
  /**
   * Called when a management action should jump to a different view
   * (e.g. AI preflight jumps to the assistant view).
   */
  onViewChange?: (view: 'assistant' | 'management' | 'plans' | 'audit' | 'scheduled-tasks') => void;
}

export interface UseCapabilities {
  // State
  capabilities: Ref<ManagedCapability[]>;
  selected: Ref<ManagedCapability>;
  validation: Ref<ValidationResult>;
  preview: Ref<NormalizedResult | null>;
  /** 试调探活结果（null 表示尚未试调）。 */
  probeResult: Ref<ProbeInferResult | null>;
  probeLoading: Ref<boolean>;
  error: Ref<string>;
  loading: Ref<boolean>;
  /** 能力存储是否已配置。false 时列表为空是"未启用"而非"零能力"。 */
  configured: Ref<boolean>;
  testInputText: Ref<string>;
  /** 写能力验证用的示例响应体（写接口禁止试调，离线推断输出映射）。 */
  testSampleBody: Ref<string>;
  searchText: Ref<string>;
  statusFilter: Ref<string>;
  /** 只看校验失败项：台账「校验失败」统计卡点击切换。 */
  invalidOnly: Ref<boolean>;
  domainFilter: Ref<string>;
  importOpenAPIURLText: Ref<string>;
  importBackendBaseURL: Ref<string>;
  importMessage: Ref<string>;
  importWizardStep: Ref<ImportWizardStep>;
  managementPhase: Ref<ManagementPhase>;
  /** 接入向导是否展开。清单优先布局下右侧向导可折叠；无能力待处理时默认展开。 */
  wizardOpen: Ref<boolean>;
  importPreview: Ref<ImportPreview | null>;
  importPreviewLoading: Ref<boolean>;
  importPreviewGeneration: Ref<number>;
  importCommitLoading: Ref<boolean>;
  candidateSelections: Ref<Record<string, boolean>>;
  candidateOverrides: Ref<Record<string, ImportCandidateOverride>>;
  candidateFilters: Ref<{ recommendation: ImportRecommendation | 'all'; domain: string; search: string }>;
  importBatch: Ref<ImportBatch | null>;
  importDomainFilter: Ref<string>;
  /** 单接口接入（probe-generate）：填一个后端接口地址自动生成能力草稿。 */
  urlEndpoint: Ref<string>;
  urlMethod: Ref<string>;
  urlDescription: Ref<string>;
  urlSampleBody: Ref<string>;
  urlImportLoading: Ref<boolean>;
  generatedUrlResult: Ref<ProbeGenerateResult | null>;
  aiPromptOverride: Ref<string | null>;
  aiResponse: Ref<AssistantConsoleResponse | null>;
  aiError: Ref<string>;
  aiLoading: Ref<boolean>;

  // Computed
  derivedVariables: ComputedRef<string[]>;
  validationLabel: ComputedRef<string>;
  previewText: ComputedRef<string>;
  requestPreviewText: ComputedRef<string>;
  responsePreviewText: ComputedRef<string>;
  defaultAIPrompt: ComputedRef<string>;
  aiPromptText: WritableComputedRef<string>;
  aiPreflightReady: ComputedRef<boolean>;
  aiPreflightState: ComputedRef<string>;
  aiPreflightResultText: ComputedRef<string>;
  availableDomains: ComputedRef<string[]>;
  visibleImportBatchItems: ComputedRef<ReturnType<typeof filterImportBatchItems>>;
  ignoredImportCapabilityKeys: ComputedRef<Set<string>>;
  visibleImportCandidates: ComputedRef<ReturnType<typeof filterImportCandidates>>;
  importCandidateDomains: ComputedRef<string[]>;
  selectedImportCandidates: ComputedRef<ReturnType<typeof selectedCandidates>>;
  canCommitImportPreview: ComputedRef<boolean>;
  importCommitSummary: ComputedRef<{ selected: number; reads: number; writes: number; highRisk: number }>;
  /** 候选批量试调结果（候选 ID → 结果；ok=false 表示有问题需人工处理）。 */
  candidateProbeResults: Ref<Record<string, ProbeInferResult & { ok: boolean }>>;
  candidateProbing: Ref<boolean>;
  candidateProbedProgress: Ref<number>;
  candidateProbedCount: ComputedRef<number>;
  cleanProbedCandidates: ComputedRef<ImportCandidate[]>;
  problemProbedCandidates: ComputedRef<ImportCandidate[]>;
  publishedCapabilityNames: ComputedRef<Set<string>>;
  stats: ComputedRef<{ published: number; review: number; invalid: number; publishable: number }>;
  filteredCapabilities: ComputedRef<ManagedCapability[]>;
  inputRows: ComputedRef<{ name: string; type: InputField['type']; required: boolean }[]>;
  testInputRows: ComputedRef<{ name: string; type: InputField['type']; required: boolean; source: string; description?: string; examples?: string[]; enum?: string[] }[]>;
  outputRows: ComputedRef<{ name: string; path: string }[]>;
  publishTargetPath: ComputedRef<string>;
  governanceSummary: ComputedRef<string>;
  publishChecks: ComputedRef<import('./useCapabilityEditor').CheckItem[]>;
  publishReady: ComputedRef<boolean>;

  // 分页和批量操作
  paginatedCapabilities: ComputedRef<ManagedCapability[]>;
  pageSize: Ref<number>;
  currentPage: Ref<number>;
  totalPages: ComputedRef<number>;
  groupedStats: ComputedRef<Record<string, number>>;
  publishAll: () => Promise<PublishAllResult | undefined>;

  // Functions
  loadCapabilities: () => Promise<void>;
  selectCapability: (capability: ManagedCapability) => void;
  newDraft: () => void;
  /** 打开一个由手动构造（JSON/表单）生成的 Capability 草稿，进入评审发布阶段。 */
  openManualCapability: (capability: Capability) => void;
  saveSelectedDraft: () => Promise<void>;
  validateSelected: () => Promise<void>;
  testSelected: () => Promise<void>;
  /** 试调真实接口：调一次后端并按真实响应推断输出映射。 */
  probeSelected: () => Promise<void>;
  /** 把试调推断出的输出映射写回当前草稿。 */
  applyInferredOutput: () => void;
  /** 批量试调勾选的候选（仅 GET 读能力），结果内联到候选列表。 */
  probeSelectedCandidates: () => Promise<void>;
  /** 单接口接入：对输入的后端接口地址执行探测并生成能力草稿。 */
  runUrlProbeGenerate: () => Promise<void>;
  clearUrlProbeGenerate: () => void;
  /** 把 probe-generate 生成的能力草稿写回评审区并切到 review 阶段。 */
  commitUrlProbeGenerate: () => void;
  previewSwaggerURL: () => Promise<void>;
  clearImportPreview: () => void;
  commitSwaggerImport: () => Promise<void>;
  /** 发布已验证通过（✓ 低风险读取）的候选；仅在用户显式点击时调用。 */
  publishVerifiedDrafts: () => Promise<{ published: string[]; failed: { name: string; reason: string }[] }>;
  updateCandidateOverride: (id: string, patch: Partial<ImportCandidateOverride>) => void;
  toggleImportIgnored: (name: string, ignored: boolean) => void;
  openImportedCapability: (item: ImportBatch['items'][number]) => void;
  publishSelected: (capability: ManagedCapability) => Promise<void>;
  publishCurrent: () => Promise<void>;
  unpublishSelected: (capability: ManagedCapability) => Promise<void>;
  deleteSelectedDraft: (capability: ManagedCapability) => Promise<void>;
  handleQuickPublished: (capability: ManagedCapability) => void;
  handleQuickPublishError: (message: string) => void;
  enrichSelected: (capability: ManagedCapability) => Promise<void>;
  enrichLoading: Ref<boolean>;
  enrichAllDrafts: () => Promise<number | undefined>;
  enrichAllLoading: Ref<boolean>;
  runAIPreflight: () => Promise<void>;
  addInputField: () => void;
  removeInputField: (name: string) => void;
  renameInputField: (previousName: string, nextName: string) => void;
  setInputType: (name: string, type: InputField['type']) => void;
  setInputRequired: (name: string, required: boolean) => void;
  addOutputField: () => void;
  removeOutputField: (name: string) => void;
  renameOutputField: (previousName: string, nextName: string) => void;
  setOutputPath: (name: string, path: string) => void;
  testInputFieldValue: (name: string) => string | boolean | number;
  setTestInputField: (name: string, rawValue: string | boolean, type: InputField['type']) => void;
  sourceLabel: (source: string) => string;
  capabilityStateLabel: (capability: ManagedCapability) => string;
  operationLabel: (operation: string) => string;
  riskLabel: (risk: string) => string;
  recommendationLabel: (value: ImportRecommendation) => string;
  candidateReasonText: (candidate: ImportCandidate) => string;
  candidateVerdictText: (candidate: ImportCandidate) => string;
  hasPublishedTwin: (capability: ManagedCapability) => boolean;
  hasStaticToolConflict: (capability: ManagedCapability) => boolean;
  isPublishable: (capability: ManagedCapability) => boolean;
  publishActionLabel: (capability: ManagedCapability) => string;
  currentPublishLabel: () => string;
  nextActionLabel: (capability: ManagedCapability) => string;
  resetAIPreflight: () => void;
}

/**
 * useCapabilities 是能力管理模块的组合根：组装 editor（编辑/校验/测试/AI 预检）、
 * import（Swagger 导入向导）、publish（清单/分页/发布动作）三个子 composable，
 * 并补充列表加载、展示标签与发布决策文案。公开接口对调用方保持稳定。
 */
export function useCapabilities(options: UseCapabilitiesOptions = {}): UseCapabilities {
  const { onViewChange } = options;

  // 共享状态
  const capabilities = ref<ManagedCapability[]>([]);
  const error = ref('');
  const loading = ref(false);
  // 能力存储是否已配置（后端 /v1/capabilities 的 configured 字段）。未配置时
  // 列表为空是"未启用"，前端据此展示配置提示而非"零能力"。
  const configured = ref(true);
  // 进入管理页的默认落地阶段：首次为「接入 API」（source），引导用户先接入能力；
  // 已有能力库时由 loadCapabilities 加载后维持，需要评审点向导/清单即可。
  // （loadCapabilities 里 JSON 空列表时的降级逻辑保留，作为无能力场景的一致性兜底。）
  const managementPhase = ref<ManagementPhase>('source');
  // 接入向导是否展开。清单优先布局下，左侧能力清单始终可见，右侧接入向导
  // 默认展开（阶段为 source——默认落在连源）；用户可手动收起以聚焦清单。
  // review 阶段兼作"评审工单页打开"开关：默认 false=列表主界面；接入画布打开时为 true。
  const wizardOpen = ref(false);

  // 子 composable
  const editor = useCapabilityEditor({
    capabilities,
    error,
    managementPhase,
    onSelect: (capability) => selectCapability(capability),
    onViewChange,
  });
  const importWizard = useCapabilityImport({
    capabilities,
    error,
    managementPhase,
    onSelect: (capability) => selectCapability(capability),
    onOpenWorkbench: () => { wizardOpen.value = true; },
  });
  const publish = useCapabilityPublish({
    capabilities,
    error,
    managementPhase,
    selected: editor.selected,
    publishReady: editor.publishReady,
    isPublishable: (capability) => isPublishable(capability),
    onSelect: (capability) => selectCapability(capability),
    onQuickPublished: (message) => {
      importWizard.importMessage.value = message;
    },
    onRefresh: () => loadCapabilities(),
    onPublished: () => { wizardOpen.value = true; },
    ignoredKeys: importWizard.ignoredImportCapabilityKeys,
  });

  // 列表加载
  const selectCapability = editor.selectCapability;

  // 列表是否已完成首次加载。首次加载时按数据落地阶段：存在能力库落在「评审发布」，
  // 能力库为空（含未配置/未启用）落在「接入 API」引导用户先接入；后续刷新保持当前阶段，不打断流程。
  let initialLoaded = false;

  async function loadCapabilities() {
    loading.value = true;
    error.value = '';
    try {
      const result = await listCapabilities();
      capabilities.value = result.capabilities;
      // configured=false 表示能力存储未配置，列表为空是"未启用"而非"零能力"。
      configured.value = result.configured;
      if (capabilities.value.length > 0) {
        selectCapability(capabilities.value[0]);
        // 列表为主界面：有能力库时默认落在评审（列表）阶段；接入是动作，
        // 通过页面内的引导按钮进入，不再默认占据主界面。
        if (!initialLoaded) {
          managementPhase.value = 'review';
        }
      } else if (!initialLoaded) {
        // 无能力时落在接入画布，引导用户先接入。
        managementPhase.value = 'source';
        wizardOpen.value = true;
      }
      initialLoaded = true;
    } catch (err) {
      error.value = err instanceof Error ? err.message : '加载 Capability 失败';
    } finally {
      loading.value = false;
    }
  }

  // 从接入/验证工单切回列表时收起工单开关，保证落在列表主界面。
  watch(managementPhase, (phase, prev) => {
    if (prev && (prev === 'source' || prev === 'candidates') && phase === 'review') {
      wizardOpen.value = false;
    }
  });

  // 展示标签（纯函数）
  const sourceLabelFn = sourceLabel;
  const operationLabelFn = operationLabel;
  const riskLabelFn = riskLabel;
  const recommendationLabelFn = recommendationLabel;
  const candidateReasonTextFn = candidateReasonText;
  const candidateVerdictTextFn = candidateVerdictText;

  // 发布决策标签：依赖 editor 的预检（hasPublishedTwin/hasStaticToolConflict 等）
  function isPublishable(capability: ManagedCapability): boolean {
    return canPublish(capability) && !editor.hasPublishedTwin(capability) && !editor.hasStaticToolConflict(capability);
  }

  function publishActionLabel(capability: ManagedCapability): string {
    if (capability.source === 'published') {
      return '已发布';
    }
    if (editor.hasPublishedTwin(capability)) {
      return '已有已发布版本';
    }
    if (editor.hasStaticToolConflict(capability)) {
      return '改名后发布';
    }
    if (!canPublish(capability)) {
      if (capability.operation === 'write' && !capability.validation.valid) {
        return '补治理';
      }
      return '不可发布';
    }
    return '发布';
  }

  function currentPublishLabel(): string {
    if (editor.selected.value.source === 'published') {
      return '已发布，无需重复发布';
    }
    if (editor.hasPublishedTwin(editor.selected.value)) {
      return '已有已发布版本';
    }
    if (editor.hasStaticToolConflict(editor.selected.value)) {
      return `名称与内置工具冲突，请改名后重试`;
    }
    if (!canPublish(editor.selected.value)) {
      if (editor.selected.value.operation === 'write') {
        return editor.selected.value.governance ? '先校验当前 Capability' : '补齐 governance';
      }
      return '不可发布';
    }
    return '发布当前 Capability';
  }

  function capabilityStateLabel(capability: ManagedCapability): string {
    const state = uiStateOf(capability, isPublishable);
    return {
      draft: '草稿',
      needs_validation: '待验证',
      publishable: '可发布',
      published: '已发布',
    }[state];
  }

  function nextActionLabel(capability: ManagedCapability): string {
    const state = uiStateOf(capability, isPublishable);
    if (state === 'published') {
      return '用 AI 验证';
    }
    if (editor.hasPublishedTwin(capability)) {
      return '已有可用版本';
    }
    if (state === 'publishable') {
      return '发布给 AI';
    }
    if (capability.operation === 'write' && !capability.governance) {
      return '补充执行保护';
    }
    if (state === 'needs_validation') {
      return '开始真实试调';
    }
    return '补充接口信息';
  }

  return {
    // State
    capabilities,
    selected: editor.selected,
    validation: editor.validation,
    preview: editor.preview,
    probeResult: editor.probeResult,
    probeLoading: editor.probeLoading,
    error,
    loading,
    configured,
    testInputText: editor.testInputText,
    testSampleBody: editor.testSampleBody,
    searchText: publish.searchText,
    statusFilter: publish.statusFilter,
    invalidOnly: publish.invalidOnly,
    domainFilter: publish.domainFilter,
    importOpenAPIURLText: importWizard.importOpenAPIURLText,
    importBackendBaseURL: importWizard.importBackendBaseURL,
    importMessage: importWizard.importMessage,
    importWizardStep: importWizard.importWizardStep,
    managementPhase,
    wizardOpen,
    importPreview: importWizard.importPreview,
    importPreviewLoading: importWizard.importPreviewLoading,
    importPreviewGeneration: importWizard.importPreviewGeneration,
    importCommitLoading: importWizard.importCommitLoading,
    candidateSelections: importWizard.candidateSelections,
    candidateOverrides: importWizard.candidateOverrides,
    candidateFilters: importWizard.candidateFilters,
    importBatch: importWizard.importBatch,
    importDomainFilter: importWizard.importDomainFilter,
    urlEndpoint: importWizard.urlEndpoint,
    urlMethod: importWizard.urlMethod,
    urlDescription: importWizard.urlDescription,
    urlSampleBody: importWizard.urlSampleBody,
    urlImportLoading: importWizard.urlImportLoading,
    generatedUrlResult: importWizard.generatedUrlResult,
    aiPromptOverride: editor.aiPromptOverride,
    aiResponse: editor.aiResponse,
    aiError: editor.aiError,
    aiLoading: editor.aiLoading,

    // Computed
    derivedVariables: editor.derivedVariables,
    validationLabel: editor.validationLabel,
    previewText: editor.previewText,
    requestPreviewText: editor.requestPreviewText,
    responsePreviewText: editor.responsePreviewText,
    defaultAIPrompt: editor.defaultAIPrompt,
    aiPromptText: editor.aiPromptText,
    aiPreflightReady: editor.aiPreflightReady,
    aiPreflightState: editor.aiPreflightState,
    aiPreflightResultText: editor.aiPreflightResultText,
    availableDomains: publish.availableDomains,
    visibleImportBatchItems: importWizard.visibleImportBatchItems,
    ignoredImportCapabilityKeys: importWizard.ignoredImportCapabilityKeys,
    visibleImportCandidates: importWizard.visibleImportCandidates,
    importCandidateDomains: importWizard.importCandidateDomains,
    selectedImportCandidates: importWizard.selectedImportCandidates,
    canCommitImportPreview: importWizard.canCommitImportPreview,
    importCommitSummary: importWizard.importCommitSummary,
    candidateProbeResults: importWizard.candidateProbeResults,
    candidateProbing: importWizard.candidateProbing,
    candidateProbedProgress: importWizard.candidateProbedProgress,
    candidateProbedCount: importWizard.candidateProbedCount,
    cleanProbedCandidates: importWizard.cleanProbedCandidates,
    problemProbedCandidates: importWizard.problemProbedCandidates,
    publishedCapabilityNames: publish.publishedCapabilityNames,
    stats: publish.stats,
    filteredCapabilities: publish.filteredCapabilities,
    inputRows: editor.inputRows,
    testInputRows: editor.testInputRows,
    outputRows: editor.outputRows,
    publishTargetPath: editor.publishTargetPath,
    governanceSummary: editor.governanceSummary,
    publishChecks: editor.publishChecks,
    publishReady: editor.publishReady,

    // 分页和批量操作
    paginatedCapabilities: publish.paginatedCapabilities,
    pageSize: publish.pageSize,
    currentPage: publish.currentPage,
    totalPages: publish.totalPages,
    groupedStats: publish.groupedStats,
    publishAll: publish.publishAll,

    // Functions
    loadCapabilities,
    selectCapability: editor.selectCapability,
    newDraft: editor.newDraft,
    openManualCapability: editor.openManualCapability,
    saveSelectedDraft: editor.saveSelectedDraft,
    validateSelected: editor.validateSelected,
    testSelected: editor.testSelected,
    probeSelected: editor.probeSelected,
    applyInferredOutput: editor.applyInferredOutput,
    probeSelectedCandidates: importWizard.probeSelectedCandidates,
    runUrlProbeGenerate: importWizard.runUrlProbeGenerate,
    clearUrlProbeGenerate: importWizard.clearUrlProbeGenerate,
    commitUrlProbeGenerate: importWizard.commitUrlProbeGenerate,
    previewSwaggerURL: importWizard.previewSwaggerURL,
    clearImportPreview: importWizard.clearImportPreview,
    commitSwaggerImport: importWizard.commitSwaggerImport,
    publishVerifiedDrafts: importWizard.publishVerifiedDrafts,
    updateCandidateOverride: importWizard.updateCandidateOverride,
    toggleImportIgnored: importWizard.toggleImportIgnored,
    openImportedCapability: importWizard.openImportedCapability,
    publishSelected: publish.publishSelected,
    publishCurrent: publish.publishCurrent,
    unpublishSelected: publish.unpublishSelected,
    deleteSelectedDraft: publish.deleteSelectedDraft,
    handleQuickPublished: publish.handleQuickPublished,
    handleQuickPublishError: publish.handleQuickPublishError,
    enrichSelected: publish.enrichSelected,
    enrichLoading: publish.enrichLoading,
    enrichAllDrafts: publish.enrichAllDrafts,
    enrichAllLoading: publish.enrichAllLoading,
    runAIPreflight: editor.runAIPreflight,
    addInputField: editor.addInputField,
    removeInputField: editor.removeInputField,
    renameInputField: editor.renameInputField,
    setInputType: editor.setInputType,
    setInputRequired: editor.setInputRequired,
    addOutputField: editor.addOutputField,
    removeOutputField: editor.removeOutputField,
    renameOutputField: editor.renameOutputField,
    setOutputPath: editor.setOutputPath,
    testInputFieldValue: editor.testInputFieldValue,
    setTestInputField: editor.setTestInputField,
    sourceLabel: sourceLabelFn,
    operationLabel: operationLabelFn,
    capabilityStateLabel,
    riskLabel: riskLabelFn,
    recommendationLabel: recommendationLabelFn,
    candidateReasonText: candidateReasonTextFn,
    candidateVerdictText: candidateVerdictTextFn,
    hasPublishedTwin: editor.hasPublishedTwin,
    hasStaticToolConflict: editor.hasStaticToolConflict,
    isPublishable,
    publishActionLabel,
    currentPublishLabel,
    nextActionLabel,
    resetAIPreflight: editor.resetAIPreflight,
  };
}
