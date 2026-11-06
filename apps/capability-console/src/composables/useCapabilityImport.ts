import { computed, nextTick, ref } from 'vue';
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

/** 把"后端接口地址"拆成 base_url + path + query，透明化用户只填一个框的体验。
 *  URL.parse 会把 {var} 百分号编码成 %7Bvar%7D，这里还原，让用户可以直接写模板路径。 */
function splitEndpoint(url: string): { base_url: string; path: string; query: Record<string, string> } {
  const trimmed = url.trim();
  if (!trimmed) {
    return { base_url: '', path: '', query: {} };
  }
  const decodeBraces = (value: string) => value.replace(/%7B/gi, '{').replace(/%7D/gi, '}');
  try {
    const parsed = new URL(trimmed);
    const query: Record<string, string> = {};
    parsed.searchParams.forEach((value, key) => {
      query[key] = value;
    });
    return { base_url: parsed.origin, path: decodeBraces(parsed.pathname), query };
  } catch {
    return { base_url: trimmed, path: '', query: {} };
  }
}

/** 提取路径里显式写的 {var} 模板变量名。 */
function extractPathVariables(path: string): string[] {
  return [...path.matchAll(/\{([^/{}]+)\}/g)].map((match) => match[1]);
}

/** 试调失败的错误分类：把后端/网络的原始报错翻译成下一步该做什么的引导。 */
export function classifyProbeError(message: string): string {
  const missingPath = message.match(/missing path value "([^"]+)"/);
  if (missingPath) {
    return `路径参数 ${missingPath[1]} 缺少试调值：在「参数值」里填入该参数的真实值后重新验证`;
  }
  if (message.includes('missing required input')) {
    return '试调缺少必填参数：请在参数行里补全参数值后重新验证';
  }
  if (/401|403|unauthorized|forbidden/i.test(message)) {
    return '接口返回 401/403：该接口需要鉴权，继续后可在评审页配置 Bearer Token 或 API Key';
  }
  if (/connect|refused|timeout|deadline|dial tcp|no such host|network|unreachable/i.test(message)) {
    return '无法连接后端：请检查接口地址（Base URL）是否可达、网络是否连通';
  }
  return message;
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
      importMessage.value = previewResult.candidates.length === 0
        ? '没有识别到可接入 API：请确认地址指向 OpenAPI v2/v3 文档（如 /v3/api-docs 或 /swagger.json）且可访问'
        : `已预览 ${previewResult.candidates.length} 个候选 API`;
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
  /** 显式 {var} 路径参数的试调值：变量名 → 值（成功后预填进 input examples）。 */
  const urlPathValues = ref<Record<string, string>>({});
  /** 查询参数行：进 input_schema 标 in:"query"，试调时携带实际值。 */
  const urlQueryParams = ref<Array<{ key: string; value: string; required: boolean }>>([]);
  /** 本次试调的本地提示（如查询参数与路径参数同名），与后端 probe warnings 合并展示。 */
  const urlLocalWarnings = ref<string[]>([]);
  /** URL 路径里声明的 {var}（含已知模板还原出的），驱动 UrlEntry 的参数值行。 */
  const urlPathVariables = computed(() => {
    const { path } = splitEndpoint(urlEndpoint.value);
    const explicit = extractPathVariables(path);
    if (explicit.length > 0) {
      return explicit;
    }
    for (const known of KNOWN_PATH_TEMPLATES) {
      if (known.pattern.test(path)) {
        return known.vars;
      }
    }
    return [];
  });

  async function runUrlProbeGenerate() {
    urlImportLoading.value = true;
    error.value = '';
    generatedUrlResult.value = null;
    try {
      const { base_url, path: rawPath, query: urlQuery } = splitEndpoint(urlEndpoint.value);
      // URL 里直接带 ?a=b 的查询参数：自动并入参数行（手动行优先），地址栏还原成纯路径。
      if (Object.keys(urlQuery).length > 0) {
        const manualKeys = new Set(urlQueryParams.value.map((row) => row.key.trim()).filter(Boolean));
        for (const [key, value] of Object.entries(urlQuery)) {
          if (!manualKeys.has(key)) {
            urlQueryParams.value = [...urlQueryParams.value, { key, value, required: false }];
          }
        }
        urlEndpoint.value = `${base_url}${rawPath}`;
      }
      const allRows = urlQueryParams.value.filter((row) => row.key.trim());
      // 显式 {var} 模板优先；其次是已知路径模板还原；都没有按字面量处理。
      const explicitVars = extractPathVariables(rawPath);
      const known = templatizePath(rawPath);
      const path = explicitVars.length > 0 ? rawPath : known.template;
      const pathValues: Record<string, string> = {};
      for (const name of explicitVars) {
        pathValues[name] = urlPathValues.value[name] ?? '';
      }
      if (explicitVars.length === 0) {
        Object.assign(pathValues, known.values);
        // 已知模板同样有可编辑的参数行：用户改过的值优先于 URL 字面量，
        // 否则「参数行里改值」会被静默忽略（探测仍用 URL 里的旧值）。
        for (const name of Object.keys(known.values)) {
          const edited = urlPathValues.value[name];
          if (edited) {
            pathValues[name] = edited;
          }
        }
      }
      // 查询参数行与路径变量同名会互相覆盖：路径参数已在模板里，查询行按同名
      // 处理会劫持路径取值并把 schema 字段改成 in:"query"。路径优先 + 显式提示。
      const pathVarNames = new Set(Object.keys(pathValues));
      const conflictingKeys = [...new Set(
        allRows.map((row) => row.key.trim()).filter((key) => pathVarNames.has(key)),
      )];
      urlLocalWarnings.value = conflictingKeys.map(
        (key) => `查询参数 ${key} 与路径参数同名，本次试调按路径参数处理：请重命名或删除该查询参数行`,
      );
      const rows = allRows.filter((row) => !pathVarNames.has(row.key.trim()));
      const hasInput = Object.keys(pathValues).length > 0 || rows.length > 0;
      const input: Record<string, unknown> = {};
      for (const [name, value] of Object.entries(pathValues)) {
        if (value) {
          input[name] = value;
        }
      }
      for (const row of rows) {
        input[row.key.trim()] = row.value;
      }
      const result = await probeGenerateCapability({
        backend: { adapter: 'http', method: urlMethod.value || 'GET', path, base_url },
        description: urlDescription.value.trim() || undefined,
        sample_body: urlSampleBody.value.trim() || undefined,
        input: hasInput ? input : undefined,
      });
      // 把试调实参写回 input_schema：路径参数进 examples（评审页/发布后 AI 有实参
      // 可参考），查询参数作为 in:"query" 字段落进 schema——后端
      // buildQuickPublishCapability 只解析路径变量，query 字段由前端补齐。
      const seededSchema: Record<string, (typeof result.capability.input_schema)[string]> = {
        ...(result.capability.input_schema ?? {}),
      };
      for (const [name, value] of Object.entries(pathValues)) {
        const field = seededSchema[name];
        if (value && field && !(field.examples?.length)) {
          seededSchema[name] = { ...field, examples: [value] };
        } else if (value && !field) {
          seededSchema[name] = { type: 'string', required: true, examples: [value] };
        }
      }
      for (const row of rows) {
        const key = row.key.trim();
        const existing = seededSchema[key];
        const field: (typeof result.capability.input_schema)[string] = {
          type: existing?.type ?? 'string',
          required: row.required,
          in: 'query',
        };
        if (row.value && !(existing?.examples?.length)) {
          field.examples = [row.value];
        }
        seededSchema[key] = field;
      }
      result.capability.input_schema = seededSchema;
      generatedUrlResult.value = result;
      onSelect(normalizeCapability({ ...result.capability, source: 'discovered', validation: { valid: false } }));
    } catch (err) {
      error.value = err instanceof Error ? classifyProbeError(err.message) : '接入后端接口失败';
    } finally {
      urlImportLoading.value = false;
    }
  }

  function clearUrlProbeGenerate() {
    generatedUrlResult.value = null;
  }

  /** 单接口试调后的非阻断提示（LLM 回退类不算）：连接失败、映射 miss 等给用户看的建议。 */
  const urlProbeWarnings = computed(() => {
    const result = generatedUrlResult.value;
    if (!result) {
      return [];
    }
    return [
      ...urlLocalWarnings.value,
      ...(result.probe?.warnings ?? [])
        .filter((warning) => !warning.includes('LLM 推断失败'))
        .map(classifyProbeError),
    ];
  });

  /** 把 probe-generate 生成的能力草稿写回评审区：选中草稿并打开工单评审页。 */
  function commitUrlProbeGenerate() {
    const result = generatedUrlResult.value;
    if (!result) {
      return;
    }
    onSelect(normalizeCapability({ ...result.capability, source: 'discovered', validation: { valid: false } }));
    managementPhase.value = 'review';
    // useCapabilities 的 watch 会在 source→review 时收起工单页（批量导入回列表
    // 的设计）；单接口提交的意图是直接进工单评审，等 watch 跑完再打开。
    void nextTick(() => onOpenWorkbench?.());
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
    urlPathValues,
    urlPathVariables,
    urlQueryParams,
    urlProbeWarnings,
    runUrlProbeGenerate,
    clearUrlProbeGenerate,
    commitUrlProbeGenerate,
  };
}

export type UseCapabilityImport = ReturnType<typeof useCapabilityImport>;
