import { computed, ref, watch } from 'vue';
import type { ComputedRef, Ref } from 'vue';
import { ElMessage } from 'element-plus';
import { saveDraft, testCapability, validateCapability, sendAssistantMessage, probeInferCapability } from '../api';
import { emptyCapability, normalizeCapability, pathVariables } from '../capability';
import { buildAIPrompt, parseTestInput } from '../capabilityPrompt';
import type {
  AssistantConsoleResponse,
  Capability,
  GovernanceSpec,
  InputField,
  ManagedCapability,
  NormalizedResult,
  ProbeInferResult,
  ValidationResult,
} from '../types';
import { capabilityKey, toCapability, upsert } from '../capabilityFormat';
import { hasStaticToolConflict as hasStaticToolConflictName } from '../capability';
import type { ManagementPhase } from './useCapabilities';

/** 发布检查表条目。fix 提供一键修复动作（写入合理默认值），fixLabel 是按钮文案。 */
export interface CheckItem {
  label: string;
  ok: boolean;
  detail: string;
  fix?: () => void;
  fixLabel?: string;
}

export interface UseCapabilityEditorOptions {
  /** 能力列表（保存草稿后 upsert 进去）。 */
  capabilities: Ref<ManagedCapability[]>;
  /** 共享错误提示，编辑/校验/测试失败时写入。 */
  error: Ref<string>;
  /** 共享阶段，新建/打开草稿时切到 review 阶段。 */
  managementPhase: Ref<ManagementPhase>;
  /** 保存草稿后选中最新版本。 */
  onSelect: (capability: ManagedCapability) => void;
  /** AI 预检完成后跳转 assistant 视图。 */
  onViewChange?: (view: 'assistant' | 'management' | 'plans' | 'audit' | 'scheduled-tasks') => void;
}

/**
 * useCapabilityEditor 封装评审发布阶段的能力编辑：表单字段编辑、路径变量推导、
 * 校验/测试/AI 预检。通过 options 注入共享的能力列表，供 useCapabilities 组合。
 */
export function useCapabilityEditor(options: UseCapabilityEditorOptions) {
  const { capabilities, error, managementPhase, onSelect, onViewChange } = options;

  const selected = ref<ManagedCapability>(normalizeCapability({}));
  const validation = ref<ValidationResult>({ valid: false, error: '未校验' });
  const preview = ref<NormalizedResult | null>(null);
  const testInputText = ref('{}');
  // 试调探活：真实调一次后端 + 按真实响应推断输出映射。
  const probeResult = ref<ProbeInferResult | null>(null);
  const probeLoading = ref(false);
  const aiPromptOverride = ref<string | null>(null);
  const aiResponse = ref<AssistantConsoleResponse | null>(null);
  const aiError = ref('');
  const aiLoading = ref(false);

  const derivedVariables = computed(() => pathVariables(selected.value.backend.path));
  // 当前能力合法的测试字段名集合：schema 字段 ∪ 路径变量。
  // testInputRows 与 pruneTestInput 都基于它，保证"字段是否合法"唯一出处。
  const testInputFieldNames = computed(() => {
    const names = new Set<string>(Object.keys(selected.value.input_schema));
    for (const name of derivedVariables.value) {
      names.add(name);
    }
    return names;
  });
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

  const inputRows = computed(() =>
    Object.entries(selected.value.input_schema).map(([name, field]) => ({
      name,
      type: field.type,
      required: field.required,
    })),
  );
  const testInputRows = computed(() => {
    const rows = new Map<string, { name: string; type: InputField['type']; required: boolean; source: string; description?: string; examples?: string[]; enum?: string[] }>();
    for (const [name, field] of Object.entries(selected.value.input_schema)) {
      rows.set(name, { name, type: field.type, required: field.required, source: 'schema', description: field.description, examples: field.examples, enum: field.enum });
    }
    for (const name of derivedVariables.value) {
      if (!rows.has(name)) {
        rows.set(name, { name, type: 'string', required: true, source: 'path' });
      }
    }
    return Array.from(rows.values());
  });
  // 将 testInputText 中已不属于当前能力合法字段的键清理掉（保留仍在的字段值）。
  // 用于同 key 重选路径：能力 schema 字段被增删/改名后，旧字段名残留会污染
  // 测试请求与 AI 预检提示词；裁剪只删失效键，不丢用户仍有效的输入。
  function pruneTestInput() {
    const input = parseTestInput(testInputText.value);
    const names = testInputFieldNames.value;
    const next: Record<string, unknown> = {};
    let changed = false;
    for (const [name, value] of Object.entries(input)) {
      if (names.has(name)) {
        next[name] = value;
      } else {
        changed = true;
      }
    }
    if (changed) {
      testInputText.value = JSON.stringify(next);
      resetAIPreflight();
    }
  }
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
  // governance 补全：patch 只写变更字段，其余用安全默认值补齐必填项。
  function withGovernanceDefaults(patch: Partial<GovernanceSpec>): GovernanceSpec {
    const current = selected.value.governance;
    return {
      requires_action_plan: patch.requires_action_plan ?? current?.requires_action_plan ?? true,
      requires_approval: patch.requires_approval ?? current?.requires_approval ?? true,
      precheck_tools: patch.precheck_tools ?? current?.precheck_tools ?? [],
      rollback: patch.rollback ?? current?.rollback ?? { strategy: 'restore_previous' },
    };
  }

  // 后端校验错误 → 人话。后端返回的是面向开发者的英文 schema 错误，
  // 检查表面向运维，按错误模式翻译；未匹配的原样透出（保底不误导）。
  function humanizeValidationError(err: string): string {
    const rules: [RegExp, string][] = [
      [/path variable .+? is missing from input_schema/, '接口路径里的参数需要在输入参数里补上对应字段'],
      [/read capability backend method must be/, '查询类接口的请求方法要选 GET/POST/PUT/PATCH/DELETE 之一'],
      [/read risk must be low or medium/, '查询类能力风险只能是低或中'],
      [/read capability requires output/, '查询类能力要配置输出映射或摘要模板'],
      [/write capability backend method must be/, '变更类接口的请求方法要选 POST/PUT/PATCH/DELETE'],
      [/write risk must be medium or high/, '变更类能力风险要选中或高'],
      [/write capability requires action plan and approval/, '写入能力要开启「执行前生成计划」和「执行前需审批」'],
      [/write capability requires precheck_tools/, '写入能力要指定执行前的预检项（选一个只读能力）'],
      [/write capability requires rollback strategy/, '写入能力要声明回滚方式'],
      [/requires an absolute http or https/, '后端地址要以 http:// 或 https:// 开头'],
      [/input \".+?\" has unsupported type/, '输入参数类型只支持 string/integer/boolean'],
    ];
    for (const [pattern, message] of rules) {
      if (pattern.test(err)) {
        return message;
      }
    }
    return err;
  }

  const publishChecks = computed<CheckItem[]>(() => {
    const baseURL = selected.value.backend.base_url?.trim() ?? '';
    const operation = selected.value.operation;
    const method = selected.value.backend.method;
    const operationMatchesMethod =
      (operation === 'read' && ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].includes(method)) ||
      (operation === 'write' && ['POST', 'PUT', 'PATCH', 'DELETE'].includes(method));
    return [
      {
        label: operation === 'read' ? '读取类能力' : '写入类能力',
        ok: operationMatchesMethod,
        detail: operationMatchesMethod
          ? `${operation === 'read' ? '读取' : '写入'}接口与请求方法匹配`
          : operation === 'read'
          ? '查询类接口要选 GET 方法'
          : '变更类接口要选 POST/PUT/PATCH/DELETE',
        fix: operationMatchesMethod ? undefined : () => {
          selected.value.backend.method = operation === 'read' ? 'GET' : 'POST';
        },
        fixLabel: operationMatchesMethod ? undefined : '改为匹配的方法',
      },
      {
        label: `${method === 'GET' ? 'GET' : method} 请求`,
        ok: operationMatchesMethod,
        detail: `请求方法 ${method}（读取类接口支持 GET/POST 等查询方法）`,
      },
      {
        label: '后端地址',
        ok: /^https?:\/\/[^/]+/.test(baseURL),
        detail: baseURL || '填上后台服务的地址（http/https 开头）才能调用',
      },
      {
        label: '格式检查',
        ok: validation.value.valid,
        detail: validation.value.valid ? '能力定义完整可用' : humanizeValidationError(validation.value.error ?? '正在检查…'),
      },
      ...(operation === 'write'
        ? [
            {
              label: '执行前生成计划',
              ok: Boolean(selected.value.governance?.requires_action_plan),
              detail: selected.value.governance?.requires_action_plan
                ? '执行前会先生成操作计划'
                : '写入操作执行前需要生成操作计划供确认',
              fix: selected.value.governance?.requires_action_plan ? undefined : () => {
                selected.value.governance = withGovernanceDefaults({ requires_action_plan: true });
              },
              fixLabel: '开启',
            },
            {
              label: '执行前需审批',
              ok: Boolean(selected.value.governance?.requires_approval),
              detail: selected.value.governance?.requires_approval
                ? '需要人工确认后才会执行'
                : '写入操作需要人工确认后才会执行',
              fix: selected.value.governance?.requires_approval ? undefined : () => {
                selected.value.governance = withGovernanceDefaults({ requires_approval: true });
              },
              fixLabel: '开启',
            },
            {
              label: '执行前预检',
              ok: (selected.value.governance?.precheck_tools?.length ?? 0) > 0,
              detail:
                (selected.value.governance?.precheck_tools?.length ?? 0) > 0
                  ? `预检项：${selected.value.governance!.precheck_tools.join('、')}`
                  : '执行前需要指定一个只读能力做预检（如先查询当前状态）',
              fix: (selected.value.governance?.precheck_tools?.length ?? 0) > 0 ? undefined : () => {
                // 自动挑同域已发布读能力做预检；没有同域的就挑任意一个已发布读能力。
                const candidates = capabilities.value.filter(
                  (item) => item.source === 'published' && item.operation === 'read' && item.name !== selected.value.name,
                );
                const sameDomain = candidates.find((item) => item.domain === selected.value.domain);
                const pick = sameDomain ?? candidates[0];
                if (pick) {
                  selected.value.governance = withGovernanceDefaults({ precheck_tools: [pick.name] });
                  ElMessage.success(`已选「${pick.name}」做预检`);
                } else {
                  // 没有任何已发布读能力时给出明确指引，不再静默无反应。
                  ElMessage.warning('还没有已发布的读取能力做预检。请先发布一个读取类能力（如查询接口），再回来配置预检');
                }
              },
              fixLabel: '自动选择预检项',
            },
            {
              label: '回滚方式',
              ok: Boolean(selected.value.governance?.rollback?.strategy),
              detail: selected.value.governance?.rollback?.strategy
                ? `出问题时的回滚方式：${selected.value.governance.rollback.strategy}`
                : '需要声明出问题后怎么恢复（如 restore_previous 恢复原配置）',
              fix: selected.value.governance?.rollback?.strategy ? undefined : () => {
                selected.value.governance = withGovernanceDefaults({
                  rollback: { ...selected.value.governance?.rollback, strategy: 'restore_previous' },
                });
              },
              fixLabel: '用默认（恢复原配置）',
            },
          ]
        : []),
      {
        label: '同名发布',
        ok: !hasPublishedTwin(selected.value),
        detail: hasPublishedTwin(selected.value) ? '已有同名能力在线上运行，先下线旧版再发布' : '没有同名冲突',
      },
      {
        label: '名称不冲突',
        ok: !hasStaticToolConflict(selected.value),
        detail: hasStaticToolConflict(selected.value)
          ? `名称「${selected.value.name}」被系统内置功能占用，改个名就能发布`
          : '名称可用',
      },
      {
        label: '发布状态',
        ok: selected.value.source === 'discovered',
        detail: selected.value.source === 'discovered' ? '当前是草稿，可以发布' : '已发布的能力不能重复发布',
      },
    ];
  });
  const publishReady = computed(() => publishChecks.value.every((check) => check.ok));

  // 依赖：publishChecks 需要 publishedCapabilityNames 判断同名发布
  const publishedCapabilityNames: ComputedRef<Set<string>> = computed(
    () => new Set(capabilities.value.filter((item) => item.source === 'published').map((item) => item.name)),
  );
  function hasPublishedTwin(capability: ManagedCapability): boolean {
    return capability.source !== 'published' && publishedCapabilityNames.value.has(capability.name);
  }
  function hasStaticToolConflict(capability: ManagedCapability): boolean {
    // 与后端 internal/tools/registry.go 中的静态工具名单对齐；发布时这些名字会被
    // tools.Lookup 命中并返回 409，前端在发布前先做预检给出友好提示。
    return hasStaticToolConflictName(capability.name);
  }

  function selectCapability(capability: ManagedCapability) {
    // 能力切换的唯一入口（列表点击 / 导入 / 发布 / 下架 / 刷新自动选中都经过这里）。
    // 切到另一个能力时重置测试参数，避免上一个能力的参数残留导致误测；
    // 同一能力重新选中（如保存后重选）则保留用户已填的测试输入，
    // 但清理 schema 已不存在的字段（能力编辑中增删/改名后旧字段名不再合法）。
    const switching = capabilityKey(capability) !== capabilityKey(selected.value);
    selected.value = normalizeCapability(JSON.parse(JSON.stringify(capability)) as Partial<ManagedCapability>);
    validation.value = selected.value.validation;
    preview.value = null;
    probeResult.value = null;
    resetAIPreflight();
    if (switching) {
      testInputText.value = '{}';
    } else {
      // 先更新 selected 再裁剪，pruneTestInput 基于新能力的合法字段集合。
      pruneTestInput();
    }
  }

  function newDraft() {
    selected.value = normalizeCapability({ ...emptyCapability(), source: 'discovered', validation: { valid: false } });
    validation.value = { valid: false, error: '未校验' };
    preview.value = null;
    testInputText.value = '{}';
    resetAIPreflight();
    managementPhase.value = 'review';
  }

  function openManualCapability(capability: Capability) {
    selected.value = normalizeCapability({ ...capability, source: 'discovered', validation: { valid: false } });
    validation.value = { valid: false, error: '未校验' };
    preview.value = null;
    testInputText.value = '{}';
    resetAIPreflight();
    managementPhase.value = 'review';
  }

  async function saveSelectedDraft() {
    error.value = '';
    try {
      const saved = await saveDraft(toCapability(selected.value));
      upsert(capabilities, saved);
      onSelect(saved);
      ElMessage.success(`草稿已保存：${saved.name || '未命名'}`);
    } catch (err) {
      const msg = err instanceof Error ? err.message : '保存草稿失败';
      error.value = msg;
      ElMessage.error(msg);
    }
  }

  async function validateSelected(showToast = true): Promise<void> {
    error.value = '';
    try {
      validation.value = await validateCapability(toCapability(selected.value));
      selected.value.validation = validation.value;
      if (validation.value.valid) {
        if (showToast) {
          ElMessage.success('校验通过');
        }
      } else if (showToast) {
        ElMessage.error(validation.value.error ?? '校验失败');
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : '校验 Capability 失败';
      error.value = msg;
      if (showToast) {
        ElMessage.error(msg);
      }
    }
  }

  // 编辑时自动校验：debounce 800ms，静默（不弹 toast），结果驱动发布检查表。
  let autoValidateTimer: ReturnType<typeof setTimeout> | null = null;
  function scheduleAutoValidate() {
    if (autoValidateTimer) {
      clearTimeout(autoValidateTimer);
    }
    autoValidateTimer = setTimeout(() => {
      autoValidateTimer = null;
      if (selected.value.name.trim() !== '') {
        void validateSelected(false);
      }
    }, 800);
  }

  async function testSelected() {
    error.value = '';
    try {
      const input = JSON.parse(testInputText.value) as Record<string, unknown>;
      preview.value = await testCapability(toCapability(selected.value), input);
    } catch (err) {
      const msg = err instanceof Error ? err.message : '测试 Capability 失败';
      error.value = msg;
      ElMessage.error(msg);
    }
  }

  // 试调探活：真实调用后端一次，返回真实响应 + AI/规则推断的输出映射。
  // 与 testSelected 共用测试参数（testInputText），结果由 ProbePanel 展示。
  async function probeSelected() {
    error.value = '';
    probeLoading.value = true;
    try {
      const input = JSON.parse(testInputText.value) as Record<string, unknown>;
      probeResult.value = await probeInferCapability(toCapability(selected.value), input);
    } catch (err) {
      const msg = err instanceof Error ? err.message : '试调失败';
      error.value = msg;
      ElMessage.error(msg);
    } finally {
      probeLoading.value = false;
    }
  }

  // 把试调推断出的输出映射写回当前草稿（severity_path/summary_template/fields），
  // 用户随后可继续微调并发布。
  function applyInferredOutput() {
    const inferred = probeResult.value?.inferred;
    if (!inferred) {
      return;
    }
    if (inferred.summary_template) {
      selected.value.output.summary_template = inferred.summary_template;
    }
    if (inferred.severity_path) {
      selected.value.output.severity_path = inferred.severity_path;
    }
    if (inferred.status_mapping && Object.keys(inferred.status_mapping).length > 0) {
      selected.value.output.status_mapping = inferred.status_mapping;
    }
    if (inferred.fields && Object.keys(inferred.fields).length > 0) {
      selected.value.output.fields = { ...inferred.fields };
    }
    ElMessage.success('已把推断的映射写入草稿，记得保存');
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

  // 编辑时自动校验：能力内容变化（改名/改参数/改映射等）后 debounce 触发静默校验，
  // 发布检查表实时更新，用户不再需要手动点「校验」。
  watch(selected, () => scheduleAutoValidate(), { deep: true });

  function resetAIPreflight() {
    aiPromptOverride.value = null;
    aiResponse.value = null;
    aiError.value = '';
  }

  return {
    selected,
    validation,
    preview,
    testInputText,
    probeResult,
    probeLoading,
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
    inputRows,
    testInputRows,
    outputRows,
    publishTargetPath,
    governanceSummary,
    publishChecks,
    publishReady,
    hasPublishedTwin,
    hasStaticToolConflict,
    selectCapability,
    newDraft,
    openManualCapability,
    saveSelectedDraft,
    validateSelected,
    testSelected,
    probeSelected,
    applyInferredOutput,
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
    resetAIPreflight,
  };
}

export type UseCapabilityEditor = ReturnType<typeof useCapabilityEditor>;
