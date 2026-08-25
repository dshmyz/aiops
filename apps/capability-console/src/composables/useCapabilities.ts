import { computed, ref } from 'vue';
import type { ComputedRef, Ref, WritableComputedRef } from 'vue';
import { listCapabilities } from '../api';
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
  error: Ref<string>;
  loading: Ref<boolean>;
  /** 能力存储是否已配置。false 时列表为空是"未启用"而非"零能力"。 */
  configured: Ref<boolean>;
  testInputText: Ref<string>;
  searchText: Ref<string>;
  statusFilter: Ref<string>;
  domainFilter: Ref<string>;
  importOpenAPIURLText: Ref<string>;
  importBackendBaseURL: Ref<string>;
  importMessage: Ref<string>;
  importWizardStep: Ref<ImportWizardStep>;
  managementPhase: Ref<ManagementPhase>;
  importPreview: Ref<ImportPreview | null>;
  importPreviewLoading: Ref<boolean>;
  importPreviewGeneration: Ref<number>;
  importCommitLoading: Ref<boolean>;
  candidateSelections: Ref<Record<string, boolean>>;
  candidateOverrides: Ref<Record<string, ImportCandidateOverride>>;
  candidateFilters: Ref<{ recommendation: ImportRecommendation | 'all'; domain: string; search: string }>;
  importBatch: Ref<ImportBatch | null>;
  importDomainFilter: Ref<string>;
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
  publishedCapabilityNames: ComputedRef<Set<string>>;
  stats: ComputedRef<{ published: number; review: number; invalid: number; publishable: number }>;
  filteredCapabilities: ComputedRef<ManagedCapability[]>;
  inputRows: ComputedRef<{ name: string; type: InputField['type']; required: boolean }[]>;
  testInputRows: ComputedRef<{ name: string; type: InputField['type']; required: boolean; source: string; description?: string; examples?: string[]; enum?: string[] }[]>;
  outputRows: ComputedRef<{ name: string; path: string }[]>;
  publishTargetPath: ComputedRef<string>;
  governanceSummary: ComputedRef<string>;
  publishChecks: ComputedRef<{ label: string; ok: boolean; detail: string }[]>;
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
  previewSwaggerURL: () => Promise<void>;
  clearImportPreview: () => void;
  commitSwaggerImport: () => Promise<void>;
  updateCandidateOverride: (id: string, patch: Partial<ImportCandidateOverride>) => void;
  toggleImportIgnored: (name: string, ignored: boolean) => void;
  openImportedCapability: (item: ImportBatch['items'][number]) => void;
  publishSelected: (capability: ManagedCapability) => Promise<void>;
  publishCurrent: () => Promise<void>;
  unpublishSelected: (capability: ManagedCapability) => Promise<void>;
  handleQuickPublished: (capability: ManagedCapability) => void;
  handleQuickPublishError: (message: string) => void;
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
  // 落地默认落在「能力清单（评审发布）」而非导入向导：返回用户想先看到已接入的
  // 能力库，而不是每次都从 Swagger 收件箱开始。需要导入时点向导第 1 步即可。
  const managementPhase = ref<ManagementPhase>('review');

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
    ignoredKeys: importWizard.ignoredImportCapabilityKeys,
  });

  // 列表加载
  const selectCapability = editor.selectCapability;

  async function loadCapabilities() {
    loading.value = true;
    error.value = '';
    // 零能力（首次/未配置）时，把进入管理页默认落在「接入 API」引导用户，而不是空评审清单。
    const wasEmpty = capabilities.value.length === 0;
    try {
      const result = await listCapabilities();
      capabilities.value = result.capabilities;
      // configured=false 表示能力存储未配置，列表为空是"未启用"而非"零能力"。
      configured.value = result.configured;
      if (capabilities.value.length > 0) {
        selectCapability(capabilities.value[0]);
      } else if (wasEmpty && managementPhase.value === 'review') {
        managementPhase.value = 'source';
      }
    } catch (err) {
      error.value = err instanceof Error ? err.message : '加载 Capability 失败';
    } finally {
      loading.value = false;
    }
  }

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

  function nextActionLabel(capability: ManagedCapability): string {
    if (capability.source === 'published') {
      return '用 AI 试问一次';
    }
    if (editor.hasPublishedTwin(capability)) {
      return '已有 AI 可用版本';
    }
    if (!canPublish(capability)) {
      if (capability.operation === 'write') {
        return capability.governance ? '先校验' : '补齐 governance';
      }
      return '继续评审';
    }
    if (isPublishable(capability)) {
      return '发布给 AI';
    }
    return '继续评审';
  }

  return {
    // State
    capabilities,
    selected: editor.selected,
    validation: editor.validation,
    preview: editor.preview,
    error,
    loading,
    configured,
    testInputText: editor.testInputText,
    searchText: publish.searchText,
    statusFilter: publish.statusFilter,
    domainFilter: publish.domainFilter,
    importOpenAPIURLText: importWizard.importOpenAPIURLText,
    importBackendBaseURL: importWizard.importBackendBaseURL,
    importMessage: importWizard.importMessage,
    importWizardStep: importWizard.importWizardStep,
    managementPhase,
    importPreview: importWizard.importPreview,
    importPreviewLoading: importWizard.importPreviewLoading,
    importPreviewGeneration: importWizard.importPreviewGeneration,
    importCommitLoading: importWizard.importCommitLoading,
    candidateSelections: importWizard.candidateSelections,
    candidateOverrides: importWizard.candidateOverrides,
    candidateFilters: importWizard.candidateFilters,
    importBatch: importWizard.importBatch,
    importDomainFilter: importWizard.importDomainFilter,
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
    previewSwaggerURL: importWizard.previewSwaggerURL,
    clearImportPreview: importWizard.clearImportPreview,
    commitSwaggerImport: importWizard.commitSwaggerImport,
    updateCandidateOverride: importWizard.updateCandidateOverride,
    toggleImportIgnored: importWizard.toggleImportIgnored,
    openImportedCapability: importWizard.openImportedCapability,
    publishSelected: publish.publishSelected,
    publishCurrent: publish.publishCurrent,
    unpublishSelected: publish.unpublishSelected,
    handleQuickPublished: publish.handleQuickPublished,
    handleQuickPublishError: publish.handleQuickPublishError,
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
