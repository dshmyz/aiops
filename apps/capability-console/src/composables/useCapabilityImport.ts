import { computed, ref } from 'vue';
import type { Ref } from 'vue';
import { commitOpenAPIURLImport, previewOpenAPIURL, probeGenerateCapability, probeInferCapability, publishCapability } from '../api';
import { APIError } from '../api';
import type { ProbeGenerateResult } from '../api';
import { createImportBatch, filterImportBatchItems, setImportItemIgnored } from '../importBatch';
import { normalizeCapability } from '../capability';
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

/** 把"后端接口地址"拆成 base_url + path，透明化用户只填一个框的体验。 */
function splitEndpoint(url: string): { base_url: string; path: string } {
  const trimmed = url.trim();
  if (!trimmed) {
    return { base_url: '', path: '' };
  }
  try {
    const parsed = new URL(trimmed);
    return { base_url: parsed.origin, path: `${parsed.pathname}${parsed.search}` };
  } catch {
    return { base_url: trimmed, path: '' };
  }
}

/**
 * 把探测 URL 的字面量路径还原成 {var} 模板：用户填的是
 * /api/minio/demo/buckets/archive/capacity，按既有模板路径模板
 * （/api/minio/{cluster}/buckets/{bucket}/…）匹配出变量值并回填模板；
 * 没有匹配到模板时原样返回，由后端按无变量处理。
 * 返回模板路径 + 变量实参（cluster=demo、bucket=archive），供
 * input examples 预填，让评审页参数开箱即用。
 */
const KNOWN_PATH_TEMPLATES: Array<{ pattern: RegExp; template: string; vars: string[] }> = [
  { pattern: /^\/api\/minio\/([^/]+)\/buckets\/([^/]+)\/capacity$/, template: '/api/minio/{cluster}/buckets/{bucket}/capacity', vars: ['cluster', 'bucket'] },
  { pattern: /^\/api\/minio\/([^/]+)\/buckets\/([^/]+)\/health$/, template: '/api/minio/{cluster}/buckets/{bucket}/health', vars: ['cluster', 'bucket'] },
  { pattern: /^\/api\/glusterfs\/([^/]+)\/volumes\/([^/]+)\/status$/, template: '/api/glusterfs/{cluster}/volumes/{volume}/status', vars: ['cluster', 'volume'] },
  { pattern: /^\/api\/glusterfs\/([^/]+)\/volumes\/([^/]+)\/health$/, template: '/api/glusterfs/{cluster}/volumes/{volume}/health', vars: ['cluster', 'volume'] },
  { pattern: /^\/api\/kafka\/([^/]+)\/consumer-groups\/([^/]+)\/lag$/, template: '/api/kafka/{cluster}/consumer-groups/{group}/lag', vars: ['cluster', 'group'] },
  { pattern: /^\/api\/kafka\/([^/]+)\/topics\/([^/]+)\/retention$/, template: '/api/kafka/{cluster}/topics/{topic}/retention', vars: ['cluster', 'topic'] },
  { pattern: /^\/api\/redis\/([^/]+)\/info$/, template: '/api/redis/{cluster}/info', vars: ['cluster'] },
];

function templatizePath(path: string): { template: string; values: Record<string, string> } {
  for (const known of KNOWN_PATH_TEMPLATES) {
    const match = path.match(known.pattern);
    if (match) {
      const values: Record<string, string> = {};
      known.vars.forEach((name, index) => {
        values[name] = decodeURIComponent(match[index + 1] ?? '');
      });
      return { template: known.template, values };
    }
  }
  return { template: path, values: {} };
}

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
  /** 列表主界面：从清单打开导入项时进入工单页（评审区）。 */
  onOpenWorkbench?: () => void;
}

/**
 * useCapabilityImport 封装 Swagger 导入向导：URL 预览 → 候选筛选/调整 → 批量生成草稿。
 * 状态与逻辑自洽，通过 options 注入共享的能力列表与阶段，供 useCapabilities 组合。
 */
export function useCapabilityImport(options: UseCapabilityImportOptions) {
  const { capabilities, error, managementPhase, onSelect, onOpenWorkbench } = options;

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
    candidateProbeResults.value = {};
    candidateProbedProgress.value = 0;
    importBatch.value = null;
    importDomainFilter.value = 'all';
    candidateFilters.value = { recommendation: 'all', domain: 'all', search: '' };
    importWizardStep.value = 'source';
    managementPhase.value = 'source';
  }

  // ===== 单接口快速接入（probe-generate：读真实探测 / 写用 sample_body 离线推断）=====
  const urlEndpoint = ref('');
  const urlMethod = ref('GET');
  const urlDescription = ref('');
  const urlSampleBody = ref('');
  const urlImportLoading = ref(false);
  const generatedUrlResult = ref<ProbeGenerateResult | null>(null);

  async function runUrlProbeGenerate() {
    urlImportLoading.value = true;
    error.value = '';
    generatedUrlResult.value = null;
    try {
      const split = splitEndpoint(urlEndpoint.value);
      const { base_url, path: rawPath } = split;
      // 字面量 URL 还原成模板路径：后端靠 {var} 生成 input_schema，
      // 并把实际值作为探测实参传入，探测记录的资源值得以贯穿全流程。
      const { template, values } = templatizePath(rawPath);
      const hasVars = Object.keys(values).length > 0;
      const path = hasVars ? template : rawPath;
      const result = await probeGenerateCapability({
        backend: { adapter: 'http', method: urlMethod.value || 'GET', path, base_url },
        description: urlDescription.value.trim() || undefined,
        sample_body: urlSampleBody.value.trim() || undefined,
        input: hasVars ? values : undefined,
      });
      // 把探测 URL 里的真实资源值写进 input examples（cluster=demo、
      // bucket=archive），评审页测试参数据此预填，不用再猜一遍。
      if (hasVars) {
        const seededSchema: Record<string, (typeof result.capability.input_schema)[string]> = {};
        for (const [name, field] of Object.entries(result.capability.input_schema ?? {})) {
          const value = values[name];
          seededSchema[name] = value && !(field.examples?.length)
            ? { ...field, examples: [value] }
            : field;
        }
        result.capability.input_schema = seededSchema;
      }
      generatedUrlResult.value = result;
      onSelect(normalizeCapability({ ...result.capability, source: 'discovered', validation: { valid: false } }));
    } catch (err) {
      error.value = err instanceof Error ? err.message : '接入后端接口失败';
    } finally {
      urlImportLoading.value = false;
    }
  }

  function clearUrlProbeGenerate() {
    generatedUrlResult.value = null;
  }

  /** 把 probe-generate 生成的能力草稿写回评审区：选中草稿并切到 review 阶段。 */
  function commitUrlProbeGenerate() {
    const result = generatedUrlResult.value;
    if (!result) {
      return;
    }
    onSelect(normalizeCapability({ ...result.capability, source: 'discovered', validation: { valid: false } }));
    managementPhase.value = 'review';
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
      const autoPublished: string[] = [];
      importMessage.value = result.capabilities.length > 0 ? `已导入 ${result.capabilities.length} 个草稿` : '没有生成草稿';
      // 回列表页：批量导入结果由列表页"本次导入"面板承接；
      // 逐个评审由用户点列表条目进入工单页。
      if (result.capabilities.length > 0) {
        onSelect(result.capabilities[0]);
      }
      importWizardStep.value = 'commit';
      managementPhase.value = 'review';
    } catch (err) {
      error.value = err instanceof Error ? err.message : '生成 Capability 草稿失败';
    } finally {
      importCommitLoading.value = false;
    }
  }

  /**
   * 发布已验证通过的干净候选（✓ 且低风险读取）。只在用户显式点击"发布已验证"时执行；
   * 名称冲突自动加 .imported 后缀重试一次。
   */
  async function publishVerifiedDrafts(): Promise<{ published: string[]; failed: { name: string; reason: string }[] }> {
    const published: string[] = [];
    const failed: { name: string; reason: string }[] = [];
    for (const candidate of cleanProbedCandidates.value) {
      const draft = capabilities.value.find((item) => item.source === 'discovered' && item.backend.path === candidate.capability.backend.path && item.backend.method === candidate.capability.backend.method);
      if (!draft) {
        continue;
      }
      try {
        let item: ManagedCapability;
        try {
          item = await publishCapability(draft.name);
        } catch (firstErr) {
          const conflict = firstErr instanceof APIError && firstErr.status === 409;
          if (!conflict) {
            throw firstErr;
          }
          const renamed = `${draft.name}.imported`;
          item = await publishCapability(renamed);
          draft.name = renamed;
        }
        upsert(capabilities, item);
        published.push(item.name);
      } catch (err) {
        failed.push({ name: draft.name, reason: err instanceof Error ? err.message : '发布失败' });
      }
    }
    const parts: string[] = [];
    if (published.length > 0) {
      parts.push(`已发布 ${published.length} 个`);
    }
    if (failed.length > 0) {
      parts.push(`${failed.length} 个发布失败（${failed[0].reason}）`);
    }
    if (parts.length > 0) {
      importMessage.value = parts.join('，');
    }
    return { published, failed };
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

  /**
   * 试调通过后，把实际使用的探测参数写回候选的 input examples。
   * 批量导入的 OpenAPI 规格常缺 examples（占位值是 'demo'），
   * 不回填则评审页参数框全空、发布后 AI 也没有可参考的实参。
   * 已有声明 examples 的字段尊重规格，不覆盖。
   */
  function backfillProbeExamples(candidateId: string) {
    const preview = importPreview.value;
    if (!preview) {
      return;
    }
    const candidate = preview.candidates.find((item) => item.id === candidateId);
    if (!candidate) {
      return;
    }
    const probeInput = buildProbeInput(candidate.capability);
    // 提交链路上后端会用原始规格重建草稿，前端内存改动带不过去；
    // examples 通过 overrides.inputExamples 随提交落库（后端 ApplyCandidateOverride 支持）。
    const examples: Record<string, string> = {};
    for (const [name, value] of Object.entries(probeInput)) {
      const field = candidate.capability.input_schema[name];
      if (field && !(field.examples?.length) && value !== undefined && value !== '') {
        examples[name] = String(value);
      }
    }
    if (Object.keys(examples).length === 0) {
      return;
    }
    const override = candidateOverrides.value[candidateId];
    candidateOverrides.value = {
      ...candidateOverrides.value,
      [candidateId]: { ...override, input_examples: { ...(override?.input_examples ?? {}), ...examples } },
    };
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
    candidateProbedProgress.value = 0;
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
        if (entry.ok) {
          backfillProbeExamples(candidate.id);
        }
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
      onOpenWorkbench?.();
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
    commitSwaggerImport,
    publishVerifiedDrafts,
    probeSelectedCandidates,
    updateCandidateOverride,
    toggleImportIgnored,
    openImportedCapability,
    // 单接口快速接入
    urlEndpoint,
    urlMethod,
    urlDescription,
    urlSampleBody,
    urlImportLoading,
    generatedUrlResult,
    runUrlProbeGenerate,
    clearUrlProbeGenerate,
    commitUrlProbeGenerate,
  };
}

export type UseCapabilityImport = ReturnType<typeof useCapabilityImport>;
