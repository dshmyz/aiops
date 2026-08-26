import { computed, ref } from 'vue';
import type { ComputedRef, Ref } from 'vue';
import { ElMessage } from 'element-plus';
import { saveDraft, testCapability, validateCapability, sendAssistantMessage } from '../api';
import { emptyCapability, normalizeCapability, pathVariables } from '../capability';
import { buildAIPrompt, parseTestInput } from '../capabilityPrompt';
import type {
  AssistantConsoleResponse,
  Capability,
  InputField,
  ManagedCapability,
  NormalizedResult,
  ValidationResult,
} from '../types';
import { capabilityKey, toCapability, upsert } from '../capabilityFormat';
import { hasStaticToolConflict as hasStaticToolConflictName } from '../capability';
import type { ManagementPhase } from './useCapabilities';

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

  async function validateSelected() {
    error.value = '';
    try {
      validation.value = await validateCapability(toCapability(selected.value));
      selected.value.validation = validation.value;
      if (validation.value.valid) {
        ElMessage.success('校验通过');
      } else {
        ElMessage.error(validation.value.error ?? '校验失败');
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : '校验 Capability 失败';
      error.value = msg;
      ElMessage.error(msg);
    }
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
