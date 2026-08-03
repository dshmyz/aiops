import { computed, ref } from 'vue';
import type { ComputedRef, Ref, WritableComputedRef } from 'vue';
import {
  commitOpenAPIURLImport,
  listCapabilities,
  previewOpenAPIURL,
  publishCapability,
  saveDraft,
  testCapability,
  unpublishCapability,
  validateCapability,
} from '../api';
import {
  canPublish,
  emptyCapability,
  hasStaticToolConflict as isStaticToolName,
  normalizeCapability,
  pathVariables,
} from '../capability';
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
  AssistantConsoleResponse,
  Capability,
  CapabilityOperation,
  CapabilityRisk,
  ImportCandidate,
  ImportCandidateOverride,
  ImportPreview,
  ImportRecommendation,
  InputField,
  ManagedCapability,
  NormalizedResult,
  ValidationResult,
} from '../types';
import { sendAssistantMessage } from '../api';

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
  candidateFilters: Ref<ImportCandidateFilters>;
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
  testInputRows: ComputedRef<{ name: string; type: InputField['type']; required: boolean; source: string }[]>;
  outputRows: ComputedRef<{ name: string; path: string }[]>;
  publishTargetPath: ComputedRef<string>;
  governanceSummary: ComputedRef<string>;
  publishChecks: ComputedRef<{ label: string; ok: boolean; detail: string }[]>;
  publishReady: ComputedRef<boolean>;

  // Functions
  loadCapabilities: () => Promise<void>;
  selectCapability: (capability: ManagedCapability) => void;
  newDraft: () => void;
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

export function useCapabilities(options: UseCapabilitiesOptions = {}): UseCapabilities {
  const { onViewChange } = options;

  // State
  const capabilities = ref<ManagedCapability[]>([]);
  const selected = ref<ManagedCapability>(normalizeCapability({}));
  const validation = ref<ValidationResult>({ valid: false, error: '未校验' });
  const preview = ref<NormalizedResult | null>(null);
  const error = ref('');
  const loading = ref(false);
  const testInputText = ref('{"environment":"prod"}');
  const searchText = ref('');
  const statusFilter = ref('all');
  const domainFilter = ref('all');
  const importOpenAPIURLText = ref('http://你的后台/v3/api-docs');
  const importBackendBaseURL = ref('https://middleware.example.com');
  const importMessage = ref('');
  const importWizardStep = ref<ImportWizardStep>('source');
  const managementPhase = ref<ManagementPhase>('source');
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
  const aiPromptOverride = ref<string | null>(null);
  const aiResponse = ref<AssistantConsoleResponse | null>(null);
  const aiError = ref('');
  const aiLoading = ref(false);

  // Computed
  const derivedVariables = computed(() => pathVariables(selected.value.backend.path));
  const validationLabel = computed(() => (validation.value.valid ? '校验通过' : validation.value.error ?? '未校验'));
  const previewText = computed(() => (preview.value ? JSON.stringify(preview.value, null, 2) : '暂无预览'));
  const requestPreviewText = computed(() => `${selected.value.backend.method || 'GET'} ${selected.value.backend.path || '/'}`);
  const responsePreviewText = computed(() => (preview.value ? JSON.stringify(preview.value.data, null, 2) : '暂无测试响应'));
  const defaultAIPrompt = computed(() => buildAIPrompt(selected.value, parseTestInput(testInputText.value)));
  const aiPromptText = computed({
    get: () => aiPromptOverride.value ?? defaultAIPrompt.value,
    set: (value: string) => {
      aiPromptOverride.value = value;
    },
  });
  const aiPreflightReady = computed(() => selected.value.source === 'published' || hasPublishedTwin(selected.value));
  const aiPreflightState = computed(() => {
    if (!aiPreflightReady.value) {
      return '发布后可运行';
    }
    if (aiLoading.value) {
      return '正在请求';
    }
    if (aiError.value) {
      return '请求失败';
    }
    if (aiResponse.value?.type === 'answer') {
      return '已返回答案';
    }
    if (aiResponse.value?.type === 'clarification_needed') {
      return '需要补充参数';
    }
    if (aiResponse.value?.type === 'confirmation_required') {
      return '需要审批';
    }
    if (aiResponse.value?.type === 'execution_result') {
      return '执行结果';
    }
    return '等待预检';
  });
  const aiPreflightResultText = computed(() => {
    if (aiError.value) {
      return aiError.value;
    }
    if (!aiResponse.value) {
      return '暂无 AI 响应';
    }
    return JSON.stringify(aiResponse.value, null, 2);
  });
  const availableDomains = computed(() => {
    const domains = new Set(capabilities.value.map((item) => item.domain).filter(Boolean));
    return Array.from(domains).sort();
  });
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
  const visibleImportCandidates = computed(() => (importPreview.value ? filterImportCandidates(importPreview.value, candidateFilters.value) : []));
  const importCandidateDomains = computed(() => (importPreview.value ? importPreviewDomains(importPreview.value) : []));
  const selectedImportCandidates = computed(() => (importPreview.value ? selectedCandidates(importPreview.value, candidateSelections.value) : []));
  const canCommitImportPreview = computed(() => selectedImportCandidates.value.length > 0 && !importCommitLoading.value);
  const importCommitSummary = computed(() => {
    const candidates = selectedImportCandidates.value;
    const effectiveCandidates = candidates.map((candidate) => candidateOverrides.value[candidate.id] ?? candidate.summary ?? candidate.capability);
    const reads = effectiveCandidates.filter((candidate) => candidate.operation === 'read').length;
    const writes = candidates.length - reads;
    const highRisk = effectiveCandidates.filter((candidate) => candidate.risk === 'high').length;
    return { selected: candidates.length, reads, writes, highRisk };
  });
  const publishedCapabilityNames = computed(() => new Set(capabilities.value.filter((item) => item.source === 'published').map((item) => item.name)));
  const stats = computed(() => {
    const published = capabilities.value.filter((item) => item.source === 'published').length;
    const review = capabilities.value.filter((item) => item.status === 'needs_review' || item.source === 'discovered').length;
    const invalid = capabilities.value.filter((item) => !item.validation.valid).length;
    const publishable = capabilities.value.filter((item) => isPublishable(item)).length;
    return { published, review, invalid, publishable };
  });
  const filteredCapabilities = computed(() => {
    const query = searchText.value.trim().toLowerCase();
    return capabilities.value.filter((item) => {
      if (ignoredImportCapabilityKeys.value.has(capabilityKey(item))) {
        return false;
      }
      const matchesQuery =
        query === '' ||
        [item.name, item.domain, item.resource_type, item.backend.method, item.backend.path]
          .join(' ')
          .toLowerCase()
          .includes(query);
      const matchesStatus = statusFilter.value === 'all' || item.source === statusFilter.value || item.status === statusFilter.value;
      const matchesDomain = domainFilter.value === 'all' || item.domain === domainFilter.value;
      return matchesQuery && matchesStatus && matchesDomain;
    });
  });
  const inputRows = computed(() =>
    Object.entries(selected.value.input_schema).map(([name, field]) => ({
      name,
      type: field.type,
      required: field.required,
    })),
  );
  const testInputRows = computed(() => {
    const rows = new Map<string, { name: string; type: InputField['type']; required: boolean; source: string }>();
    for (const [name, field] of Object.entries(selected.value.input_schema)) {
      rows.set(name, { name, type: field.type, required: field.required, source: 'schema' });
    }
    for (const name of derivedVariables.value) {
      if (!rows.has(name)) {
        rows.set(name, { name, type: 'string', required: true, source: 'path' });
      }
    }
    if (!rows.has('environment')) {
      rows.set('environment', { name: 'environment', type: 'string', required: true, source: 'schema' });
    }
    return Array.from(rows.values());
  });
  const outputRows = computed(() =>
    Object.entries(selected.value.output.fields).map(([name, path]) => ({
      name,
      path,
    })),
  );
  const publishTargetPath = computed(() =>
    selected.value.name.trim() ? `capabilities/published/${selected.value.name.trim()}.yaml` : 'capabilities/published/<name>.yaml',
  );
  const governanceSummary = computed(() => {
    const capability = selected.value;
    if (capability.operation === 'read') {
      return '读取能力：发布后可被 AI 直接调用';
    }
    const governance = capability.governance;
    if (!governance) {
      return '写入能力：需补齐 governance（执行计划 / 审批 / 预检 / 回滚）';
    }
    const parts: string[] = [];
    parts.push(governance.requires_action_plan ? '需执行计划' : '无需执行计划');
    parts.push(governance.requires_approval ? '需审批' : '无需审批');
    parts.push(governance.precheck_tools.length > 0 ? `预检 ${governance.precheck_tools.length} 项` : '未配预检');
    parts.push(governance.rollback.strategy ? `回滚策略：${governance.rollback.strategy}` : '未配回滚');
    return `写入能力治理：${parts.join(' / ')}`;
  });
  const publishChecks = computed(() => {
    const baseURL = selected.value.backend.base_url?.trim() ?? '';
    const operation = selected.value.operation;
    const method = selected.value.backend.method;
    const operationMatchesMethod =
      (operation === 'read' && method === 'GET') ||
      (operation === 'write' && ['POST', 'PUT', 'PATCH', 'DELETE'].includes(method));
    return [
      {
        label: operation === 'read' ? '读取类能力' : '写入类能力',
        ok: operationMatchesMethod,
        detail: operationMatchesMethod
          ? `operation = ${operation} / method = ${method}`
          : operation === 'read'
          ? '读取能力要求 backend.method = GET'
          : '写入能力要求 backend.method ∈ POST/PUT/PATCH/DELETE',
      },
      {
        label: method === 'GET' ? 'GET 请求' : `${method} 请求`,
        ok: operationMatchesMethod,
        detail: `backend.method = ${method}`,
      },
      {
        label: '后端地址',
        ok: /^https?:\/\/[^/]+/.test(baseURL),
        detail: baseURL || '发布前必须配置 http/https Base URL',
      },
      {
        label: '校验通过',
        ok: validation.value.valid,
        detail: validation.value.valid ? 'Capability schema 已通过校验' : validation.value.error ?? '请先运行校验',
      },
      ...(operation === 'write'
        ? [
            {
              label: '需执行计划',
              ok: Boolean(selected.value.governance?.requires_action_plan),
              detail: selected.value.governance?.requires_action_plan
                ? 'governance.requires_action_plan = true'
                : '写入能力必须声明 governance.requires_action_plan',
            },
            {
              label: '需审批',
              ok: Boolean(selected.value.governance?.requires_approval),
              detail: selected.value.governance?.requires_approval
                ? 'governance.requires_approval = true'
                : '写入能力必须声明 governance.requires_approval',
            },
            {
              label: '预检能力',
              ok: (selected.value.governance?.precheck_tools?.length ?? 0) > 0,
              detail:
                (selected.value.governance?.precheck_tools?.length ?? 0) > 0
                  ? `预检能力：${selected.value.governance!.precheck_tools.join(', ')}`
                  : '写入能力必须声明 governance.precheck_tools',
            },
            {
              label: '回滚策略',
              ok: Boolean(selected.value.governance?.rollback?.strategy),
              detail: selected.value.governance?.rollback?.strategy
                ? `governance.rollback.strategy = ${selected.value.governance.rollback.strategy}`
                : '写入能力必须声明 governance.rollback.strategy',
            },
          ]
        : []),
      {
        label: '同名发布',
        ok: !hasPublishedTwin(selected.value),
        detail: hasPublishedTwin(selected.value) ? '已有同名发布能力，请先下线旧版本' : '没有同名已发布能力',
      },
      {
        label: '内置工具冲突',
        ok: !hasStaticToolConflict(selected.value),
        detail: hasStaticToolConflict(selected.value)
          ? `名称「${selected.value.name}」与内置工具冲突，请改名后重试`
          : '名称不与内置工具冲突',
      },
      {
        label: '发布来源',
        ok: selected.value.source === 'discovered',
        detail: selected.value.source === 'discovered' ? '当前是草稿，可以发布' : '已发布能力不能重复发布',
      },
    ];
  });
  const publishReady = computed(() => publishChecks.value.every((check) => check.ok));

  // Functions
  async function loadCapabilities() {
    loading.value = true;
    error.value = '';
    try {
      capabilities.value = await listCapabilities();
      if (capabilities.value.length > 0) {
        selectCapability(capabilities.value[0]);
      }
    } catch (err) {
      error.value = err instanceof Error ? err.message : '加载 Capability 失败';
    } finally {
      loading.value = false;
    }
  }

  function selectCapability(capability: ManagedCapability) {
    selected.value = normalizeCapability(JSON.parse(JSON.stringify(capability)) as Partial<ManagedCapability>);
    validation.value = selected.value.validation;
    preview.value = null;
    resetAIPreflight();
  }

  function newDraft() {
    selected.value = normalizeCapability({ ...emptyCapability(), source: 'discovered', validation: { valid: false } });
    validation.value = { valid: false, error: '未校验' };
    preview.value = null;
    testInputText.value = '{"environment":"prod"}';
    resetAIPreflight();
    managementPhase.value = 'review';
  }

  async function saveSelectedDraft() {
    error.value = '';
    try {
      const saved = await saveDraft(toCapability(selected.value));
      upsert(saved);
      selectCapability(saved);
    } catch (err) {
      error.value = err instanceof Error ? err.message : '保存草稿失败';
    }
  }

  async function validateSelected() {
    error.value = '';
    try {
      validation.value = await validateCapability(toCapability(selected.value));
      selected.value.validation = validation.value;
    } catch (err) {
      error.value = err instanceof Error ? err.message : '校验 Capability 失败';
    }
  }

  async function testSelected() {
    error.value = '';
    try {
      const input = JSON.parse(testInputText.value) as Record<string, unknown>;
      preview.value = await testCapability(toCapability(selected.value), input);
    } catch (err) {
      error.value = err instanceof Error ? err.message : '测试 Capability 失败';
    }
  }

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
        upsert(item);
      }
      if (result.capabilities.length > 0) {
        selectCapability(result.capabilities[0]);
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
      selectCapability(target);
    }
  }

  async function publishSelected(capability: ManagedCapability) {
    if (capability.name === selected.value.name && !publishReady.value) {
      return;
    }
    if (capability.name !== selected.value.name && !isPublishable(capability)) {
      return;
    }
    error.value = '';
    try {
      const published = await publishCapability(capability.name);
      capabilities.value = capabilities.value.filter((item) => item.name !== capability.name);
      capabilities.value.push(published);
      selectCapability(published);
      managementPhase.value = 'ai';
    } catch (err) {
      error.value = err instanceof Error ? err.message : '发布 Capability 失败';
    }
  }

  async function publishCurrent() {
    await publishSelected(selected.value);
  }

  async function unpublishSelected(capability: ManagedCapability) {
    error.value = '';
    try {
      const unpublished = await unpublishCapability(capability.name);
      capabilities.value = capabilities.value.filter((item) => item.name !== capability.name);
      capabilities.value.push(unpublished);
      selectCapability(unpublished);
    } catch (err) {
      error.value = err instanceof Error ? err.message : '下线 Capability 失败';
    }
  }

  function handleQuickPublished(capability: ManagedCapability) {
    upsert(capability);
    selectCapability(capability);
    managementPhase.value = 'ai';
    importMessage.value = `快速发布成功：${capability.name}`;
  }

  function handleQuickPublishError(message: string) {
    error.value = message;
  }

  function upsert(capability: ManagedCapability) {
    const index = capabilities.value.findIndex((item) => capabilityKey(item) === capabilityKey(capability));
    if (index >= 0) {
      capabilities.value[index] = capability;
      return;
    }
    capabilities.value.push(capability);
  }

  function capabilityKey(capability: Pick<ManagedCapability, 'source' | 'name'>): string {
    return `${capability.source}:${capability.name}`;
  }

  function toCapability(value: ManagedCapability): Capability {
    const { source: _source, path: _path, modified_at: _modifiedAt, validation: _validation, ...capability } = value;
    return capability;
  }

  async function runAIPreflight() {
    if (!aiPreflightReady.value) {
      return;
    }
    aiLoading.value = true;
    aiError.value = '';
    aiResponse.value = null;
    try {
      aiResponse.value = await sendAssistantMessage(aiPromptText.value.trim());
      // Jump to assistant view so the user can see the response in context.
      onViewChange?.('assistant');
    } catch (err) {
      aiError.value = err instanceof Error ? err.message : 'AI 调用预检失败';
    } finally {
      aiLoading.value = false;
    }
  }

  function addInputField() {
    const base = 'param';
    let index = 1;
    let name = base;
    while (selected.value.input_schema[name]) {
      index += 1;
      name = `${base}_${index}`;
    }
    selected.value.input_schema[name] = { type: 'string', required: true };
  }

  function removeInputField(name: string) {
    if (name === 'environment') {
      return;
    }
    const next = { ...selected.value.input_schema };
    delete next[name];
    selected.value.input_schema = next;
  }

  function renameInputField(previousName: string, nextName: string) {
    nextName = nextName.trim();
    if (nextName === '' || nextName === previousName) {
      return;
    }
    const next = { ...selected.value.input_schema };
    const value = next[previousName];
    delete next[previousName];
    next[nextName] = value;
    selected.value.input_schema = next;
  }

  function setInputType(name: string, type: InputField['type']) {
    selected.value.input_schema[name] = { ...selected.value.input_schema[name], type };
  }

  function setInputRequired(name: string, required: boolean) {
    selected.value.input_schema[name] = { ...selected.value.input_schema[name], required };
  }

  function addOutputField() {
    const base = 'field';
    let index = 1;
    let name = base;
    while (selected.value.output.fields[name]) {
      index += 1;
      name = `${base}_${index}`;
    }
    selected.value.output.fields[name] = '$.data.value';
  }

  function removeOutputField(name: string) {
    const next = { ...selected.value.output.fields };
    delete next[name];
    selected.value.output.fields = next;
  }

  function renameOutputField(previousName: string, nextName: string) {
    nextName = nextName.trim();
    if (nextName === '' || nextName === previousName) {
      return;
    }
    const next = { ...selected.value.output.fields };
    const value = next[previousName];
    delete next[previousName];
    next[nextName] = value;
    selected.value.output.fields = next;
  }

  function setOutputPath(name: string, path: string) {
    selected.value.output.fields[name] = path;
  }

  function sourceLabel(source: string): string {
    return source === 'published' ? '已发布' : '草稿';
  }

  function operationLabel(operation: string): string {
    return operation === 'write' ? '写入' : '读取';
  }

  function riskLabel(risk: string): string {
    switch (risk) {
      case 'medium':
        return '中';
      case 'high':
        return '高';
      default:
        return '低';
    }
  }

  function recommendationLabel(value: ImportRecommendation): string {
    if (value === 'recommended') {
      return '推荐接入';
    }
    if (value === 'needs_adjustment') {
      return '需要调整';
    }
    return '暂不接入';
  }

  function candidateReasonText(candidate: ImportCandidate): string {
    return (candidate.reasons ?? []).join(' / ');
  }

  function candidateVerdictText(candidate: ImportCandidate): string {
    return (candidate.warnings ?? []).join(' / ') || candidateReasonText(candidate);
  }

  function hasPublishedTwin(capability: ManagedCapability): boolean {
    return capability.source !== 'published' && publishedCapabilityNames.value.has(capability.name);
  }

  function hasStaticToolConflict(capability: ManagedCapability): boolean {
    // 与后端 internal/tools/registry.go 中的静态工具名单对齐；发布时这些名字会被
    // tools.Lookup 命中并返回 409，前端在发布前先做预检给出友好提示。
    return isStaticToolName(capability.name);
  }

  function isPublishable(capability: ManagedCapability): boolean {
    return canPublish(capability) && !hasPublishedTwin(capability) && !hasStaticToolConflict(capability);
  }

  function publishActionLabel(capability: ManagedCapability): string {
    if (capability.source === 'published') {
      return '已发布';
    }
    if (hasPublishedTwin(capability)) {
      return '已有已发布版本';
    }
    if (hasStaticToolConflict(capability)) {
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
    if (selected.value.source === 'published') {
      return '已发布，无需重复发布';
    }
    if (hasPublishedTwin(selected.value)) {
      return '已有已发布版本';
    }
    if (hasStaticToolConflict(selected.value)) {
      return `名称与内置工具冲突，请改名后重试`;
    }
    if (!canPublish(selected.value)) {
      if (selected.value.operation === 'write') {
        return selected.value.governance ? '先校验当前 Capability' : '补齐 governance';
      }
      return '不可发布';
    }
    return '发布当前 Capability';
  }

  function nextActionLabel(capability: ManagedCapability): string {
    if (capability.source === 'published') {
      return '用 AI 试问一次';
    }
    if (hasPublishedTwin(capability)) {
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

  function resetAIPreflight() {
    aiPromptOverride.value = null;
    aiResponse.value = null;
    aiError.value = '';
  }

  function testInputFieldValue(name: string): string | boolean | number {
    const value = parseTestInput(testInputText.value)[name];
    if (typeof value === 'string' || typeof value === 'boolean' || typeof value === 'number') {
      return value;
    }
    return '';
  }

  function setTestInputField(name: string, rawValue: string | boolean, type: InputField['type']) {
    const next = parseTestInput(testInputText.value);
    if (rawValue === '') {
      delete next[name];
    } else if (type === 'boolean') {
      next[name] = Boolean(rawValue);
    } else if (type === 'integer') {
      const parsed = Number.parseInt(String(rawValue), 10);
      if (Number.isFinite(parsed)) {
        next[name] = parsed;
      }
    } else if (type === 'number') {
      const parsed = Number.parseFloat(String(rawValue));
      if (Number.isFinite(parsed)) {
        next[name] = parsed;
      }
    } else {
      next[name] = String(rawValue);
    }
    testInputText.value = JSON.stringify(next);
    resetAIPreflight();
  }

  function parseTestInput(text: string): Record<string, unknown> {
    try {
      const input = JSON.parse(text) as unknown;
      if (input && typeof input === 'object' && !Array.isArray(input)) {
        return input as Record<string, unknown>;
      }
    } catch (_err) {
      return {};
    }
    return {};
  }

  function buildAIPrompt(capability: ManagedCapability, input: Record<string, unknown>): string {
    const environment = stringValue(input.environment) || 'prod';
    const values = Object.entries(input)
      .filter(([name]) => name !== 'environment')
      .map(([_name, value]) => stringValue(value))
      .filter((value) => value !== '');
    const resource = capability.resource_type || 'resource';
    const domain = capability.domain || 'middleware';
    const keyword = operationKeyword(capability.name);
    return ['查询', environment, ...values, resource, '的', domain, keyword].filter(Boolean).join(' ');
  }

  function stringValue(value: unknown): string {
    if (typeof value === 'string') {
      return value.trim();
    }
    if (typeof value === 'number' || typeof value === 'boolean') {
      return String(value);
    }
    return '';
  }

  function operationKeyword(name: string): string {
    const aliases: Record<string, string> = {
      capacity: '容量',
      health: '健康',
      lag: '延迟',
      lifecycle: '生命周期',
      quota: '配额',
      retention: '保留',
      status: '状态',
    };
    for (const token of name.split('.')) {
      if (aliases[token]) {
        return aliases[token];
      }
    }
    return '状态';
  }

  return {
    capabilities,
    selected,
    validation,
    preview,
    error,
    loading,
    testInputText,
    searchText,
    statusFilter,
    domainFilter,
    importOpenAPIURLText,
    importBackendBaseURL,
    importMessage,
    importWizardStep,
    managementPhase,
    importPreview,
    importPreviewLoading,
    importPreviewGeneration,
    importCommitLoading,
    candidateSelections,
    candidateOverrides,
    candidateFilters,
    importBatch,
    importDomainFilter,
    aiPromptOverride,
    aiResponse,
    aiError,
    aiLoading,
    derivedVariables,
    validationLabel,
    previewText,
    requestPreviewText,
    responsePreviewText,
    defaultAIPrompt,
    aiPromptText,
    aiPreflightReady,
    aiPreflightState,
    aiPreflightResultText,
    availableDomains,
    visibleImportBatchItems,
    ignoredImportCapabilityKeys,
    visibleImportCandidates,
    importCandidateDomains,
    selectedImportCandidates,
    canCommitImportPreview,
    importCommitSummary,
    publishedCapabilityNames,
    stats,
    filteredCapabilities,
    inputRows,
    testInputRows,
    outputRows,
    publishTargetPath,
    governanceSummary,
    publishChecks,
    publishReady,
    loadCapabilities,
    selectCapability,
    newDraft,
    saveSelectedDraft,
    validateSelected,
    testSelected,
    previewSwaggerURL,
    clearImportPreview,
    commitSwaggerImport,
    updateCandidateOverride,
    toggleImportIgnored,
    openImportedCapability,
    publishSelected,
    publishCurrent,
    unpublishSelected,
    handleQuickPublished,
    handleQuickPublishError,
    runAIPreflight,
    addInputField,
    removeInputField,
    renameInputField,
    setInputType,
    setInputRequired,
    addOutputField,
    removeOutputField,
    renameOutputField,
    setOutputPath,
    testInputFieldValue,
    setTestInputField,
    sourceLabel,
    operationLabel,
    riskLabel,
    recommendationLabel,
    candidateReasonText,
    candidateVerdictText,
    hasPublishedTwin,
    hasStaticToolConflict,
    isPublishable,
    publishActionLabel,
    currentPublishLabel,
    nextActionLabel,
    resetAIPreflight,
  };
}
