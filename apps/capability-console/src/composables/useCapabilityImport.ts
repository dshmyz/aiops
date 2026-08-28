import { computed, ref } from 'vue';
import type { Ref } from 'vue';
import { commitOpenAPIURLImport, previewOpenAPIURL, probeInferCapability, publishCapability } from '../api';
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
  Capability,
  ImportCandidateOverride,
  ImportPreview,
  ManagedCapability,
  ProbeInferResult,
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

  /** 内置示例：本地 mock 中间件（examples/mock-middleware-api.js），与 examples/README.md 保持一致。 */
  const BUILTIN_EXAMPLE_OPENAPI_URL = 'http://127.0.0.1:19090/v3/api-docs';
  const BUILTIN_EXAMPLE_BASE_URL = 'http://127.0.0.1:19090';

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

  async function runPreview(url: string, baseUrl: string) {
    error.value = '';
    importMessage.value = '';
    clearImportPreview();
    const generation = importPreviewGeneration.value;
    importPreviewLoading.value = true;
    try {
      const previewResult = await previewOpenAPIURL({
        openapi_url: url,
        backend_base_url: baseUrl,
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
      throw err;
    } finally {
      if (generation === importPreviewGeneration.value) {
        importPreviewLoading.value = false;
      }
    }
  }

  async function previewSwaggerURL() {
    try {
      await runPreview(importOpenAPIURLText.value, importBackendBaseURL.value);
    } catch (err) {
      error.value = err instanceof Error ? err.message : '预览 Swagger URL 失败';
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

  /** 当前 Swagger 地址是否正是内置示例地址。 */
  const builtinExampleActive = computed(
    () => importOpenAPIURLText.value === BUILTIN_EXAMPLE_OPENAPI_URL && importBackendBaseURL.value === BUILTIN_EXAMPLE_BASE_URL,
  );

  /** 载入内置示例：一键填充 mock 地址并触发预览。mock 未启动时给出启动命令提示。 */
  async function loadBuiltinExample() {
    importOpenAPIURLText.value = BUILTIN_EXAMPLE_OPENAPI_URL;
    importBackendBaseURL.value = BUILTIN_EXAMPLE_BASE_URL;
    try {
      await runPreview(BUILTIN_EXAMPLE_OPENAPI_URL, BUILTIN_EXAMPLE_BASE_URL);
    } catch {
      error.value = '载入内置示例失败：请先启动 mock 服务（node examples/mock-middleware-api.js），或确认端口 19090 未被占用。';
    }
  }

  async function commitSwaggerImport(options?: { autoPublishClean?: boolean }) {
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
      // 快速通道：批量试调干净的低风险读能力跳过人工评审，直接发布。
      const cleanNames = new Set(cleanProbedCandidates.value.map((candidate) => candidate.capability.name));
      const autoPublished: string[] = [];
      const autoPublishFailed: { name: string; reason: string }[] = [];
      if (options?.autoPublishClean && cleanNames.size > 0) {
        for (const item of result.capabilities) {
          if (cleanNames.has(item.name) && item.risk === 'low' && item.operation === 'read') {
            try {
              const published = await publishCapability(item.name);
              upsert(capabilities, published);
              autoPublished.push(published.name);
            } catch (err) {
              autoPublishFailed.push({ name: item.name, reason: err instanceof Error ? err.message : '发布失败' });
            }
          }
        }
      }
      if (result.capabilities.length > 0) {
        const firstRemaining = result.capabilities.find((item) => !autoPublished.includes(item.name));
        if (firstRemaining) {
          onSelect(firstRemaining);
        }
      }
      importWizardStep.value = 'commit';
      managementPhase.value = 'review';
      const parts: string[] = [];
      if (autoPublished.length > 0) {
        parts.push(`已试调验证并直接发布 ${autoPublished.length} 个`);
      }
      if (autoPublishFailed.length > 0) {
        parts.push(`${autoPublishFailed.length} 个自动发布失败（${autoPublishFailed[0].reason}）`);
      }
      const remaining = result.capabilities.length - autoPublished.length;
      if (remaining > 0) {
        parts.push(`${remaining} 个待评审草稿`);
      }
      importMessage.value = parts.length === 0 ? '没有生成草稿' : parts.join('，');
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

  // ===== 批量试调快速通道 =====
  // 选中候选后先逐个试调真实后端：无警告的低风险读能力标记"可直接发布"，
  // 有问题的标记原因，让用户只对麻烦的少数候选做人工评审。

  /** 候选 ID → 试调结果。 */
  const candidateProbeResults = ref<Record<string, ProbeInferResult & { ok: boolean }>>({});
  const candidateProbing = ref(false);
  /** 已完成试调的候选数量，用于进度显示。 */
  const candidateProbedCount = computed(() => Object.keys(candidateProbeResults.value).length);

  /**
   * 判断候选试调结果是否"干净"：试调成功、有推断出的映射、且没有"阻断性"问题。
   * LLM 回退警告（如"LLM 推断失败，回退规则"）不算失败——规则推断成功即可用。
   * 只有后端连不通/HTTP 错误这类阻断问题才标记 ⚠。
   */
  function isCleanProbe(result: ProbeInferResult & { ok: boolean }): boolean {
    return result.ok;
  }

  /** 试调用的示例参数：优先用 Swagger 声明的 examples，其余字段一律用通用
   *  占位值（字段名不参与猜测）。目的只是让请求结构合法、能打到后端，
   *  探测是否有数据由后端响应决定。 */
  function buildProbeInput(capability: Capability): Record<string, unknown> {
    const input: Record<string, unknown> = {};
    for (const [name, field] of Object.entries(capability.input_schema)) {
      input[name] = field.examples && field.examples.length > 0 ? field.examples[0] : 'demo';
    }
    return input;
  }

  /** 对当前勾选的候选并发试调（上限 5 并发，避免打爆后端）。 */
  async function probeSelectedCandidates() {
    const preview = importPreview.value;
    if (!preview) {
      return;
    }
    const selected = selectedCandidates(preview, candidateSelections.value);
    // 只试调读能力：写能力走审批链路，试调会真的写后端。POST 查询接口同样可试调。
    const probeable = selected.filter(
      (candidate) => candidate.capability.operation === 'read',
    );
    if (probeable.length === 0) {
      importMessage.value = '所选候选里没有可试调的读取接口（写入能力走审批链路，不做试调）';
      return;
    }
    error.value = '';
    candidateProbing.value = true;
    candidateProbeResults.value = {};
    const baseURL = preview.source.backend_base_url || importBackendBaseURL.value;
    let done = 0;
    const workers = Array.from({ length: Math.min(5, probeable.length) }, async () => {
      while (probeable.length > 0) {
        const candidate = probeable.shift();
        if (!candidate) {
          break;
        }
        const capability = { ...candidate.capability, backend: { ...candidate.capability.backend, base_url: baseURL } };
        let entry: ProbeInferResult & { ok: boolean };
        try {
          const result = await probeInferCapability(capability, buildProbeInput(candidate.capability));
          // ok 的标准：后端真实响应拿到了（probe 存在）且推断出了字段映射。
          // LLM 回退类警告不阻断——规则推断成功即可用。
          const hasData = Boolean(result.probe) && Object.keys(result.inferred?.fields ?? {}).length > 0;
          const blocking = (result.warnings ?? []).some((w) => !w.includes('LLM 推断失败'));
          entry = { ...result, ok: hasData && !blocking };
        } catch (err) {
          entry = { ok: false, warnings: [err instanceof Error ? err.message : '试调请求失败'] };
        }
        candidateProbeResults.value = { ...candidateProbeResults.value, [candidate.id]: entry };
        done += 1;
        candidateProbedProgress.value = done;
      }
    });
    await Promise.all(workers);
    candidateProbing.value = false;
  }

  const candidateProbedProgress = ref(0);

  /** 试调干净的候选（可直接批量发布的低风险读能力）。 */
  const cleanProbedCandidates = computed(() => {
    const preview = importPreview.value;
    if (!preview) {
      return [];
    }
    return selectedCandidates(preview, candidateSelections.value).filter((candidate) => {
      const probe = candidateProbeResults.value[candidate.id];
      return probe !== undefined && isCleanProbe(probe)
        && candidate.capability.operation === 'read' && candidate.capability.risk === 'low';
    });
  });

  /** 试调发现问题的候选（需要人工评审）。 */
  const problemProbedCandidates = computed(() => {
    const preview = importPreview.value;
    if (!preview) {
      return [];
    }
    return selectedCandidates(preview, candidateSelections.value).filter((candidate) => {
      const probe = candidateProbeResults.value[candidate.id];
      return probe !== undefined && !isCleanProbe(probe);
    });
  });

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
    candidateProbeResults,
    candidateProbing,
    candidateProbedProgress,
    candidateProbedCount,
    cleanProbedCandidates,
    problemProbedCandidates,
    previewSwaggerURL,
    clearImportPreview,
    loadBuiltinExample,
    builtinExampleActive,
    commitSwaggerImport,
    probeSelectedCandidates,
    updateCandidateOverride,
    toggleImportIgnored,
    openImportedCapability,
  };
}

export type UseCapabilityImport = ReturnType<typeof useCapabilityImport>;
