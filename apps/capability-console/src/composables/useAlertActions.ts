import { computed, onMounted, ref } from 'vue';
import { ElMessage } from 'element-plus';
import type { AlertAction, AlertActionRunOverview } from '../types';
import {
  deleteAlertAction,
  listAlertActionRuns,
  listAlertActions,
  saveAlertAction,
  setAlertActionEnabled,
} from '../api';

/** 新建/编辑用的空表单。 */
export function blankAlertAction(): AlertAction {
  return {
    name: '',
    alert_match: { severity: 'critical' },
    tool_sequence: [{ tool: '', input: {} }],
    execute_last_step: false,
    description: '',
    enabled: true,
  };
}

/** 告警→动作编排的状态 + 数据流（列表/搜索/启停/增删改/触发历史）。 */
export function useAlertActions() {
  const rules = ref<AlertAction[]>([]);
  const loading = ref(false);
  const saving = ref(false);
  const error = ref('');
  const configured = ref(true);
  const search = ref('');

  // 编辑态：null = 未在编辑；'new' = 新建；AlertAction = 编辑既有规则。
  const editing = ref<null | 'new' | AlertAction>(null);
  const editForm = ref<AlertAction>(blankAlertAction());

  // 每条规则的触发历史（懒加载，卡片展开时取）。
  const runsByRule = ref<Record<string, AlertActionRunOverview>>({});

  const filteredRules = computed(() => {
    const q = search.value.trim().toLowerCase();
    if (!q) return rules.value;
    return rules.value.filter(
      (r) =>
        r.name.toLowerCase().includes(q) ||
        (r.description ?? '').toLowerCase().includes(q),
    );
  });

  async function load() {
    loading.value = true;
    error.value = '';
    try {
      const body = await listAlertActions();
      if ('configured' in body && body.configured === false) {
        configured.value = false;
        rules.value = [];
      } else {
        configured.value = true;
        rules.value = body.rules ?? [];
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : '加载失败';
    } finally {
      loading.value = false;
    }
  }

  function startCreate() {
    editForm.value = blankAlertAction();
    editing.value = 'new';
  }

  function startEdit(rule: AlertAction) {
    // 深拷贝，避免编辑过程污染列表里的原对象。
    editForm.value = {
      ...rule,
      alert_match: rule.alert_match ? { ...rule.alert_match, labels: [...(rule.alert_match.labels ?? [])] } : {},
      tool_sequence: rule.tool_sequence.map((s) => ({ ...s, input: { ...s.input } })),
    };
    editing.value = rule;
  }

  function cancelEdit() {
    editing.value = null;
  }

  async function save() {
    const form = editForm.value;
    if (!form.name) {
      ElMessage.error('规则名称不能为空');
      return;
    }
    if (form.tool_sequence.length === 0 || !form.tool_sequence.some((s) => s.tool.trim())) {
      ElMessage.error('至少需要一个有效的工具步骤');
      return;
    }
    saving.value = true;
    try {
      await saveAlertAction(form);
      ElMessage.success('已保存');
      editing.value = null;
      await load();
    } catch (e) {
      ElMessage.error(e instanceof Error ? e.message : '保存失败');
    } finally {
      saving.value = false;
    }
  }

  async function remove(name: string) {
    try {
      await deleteAlertAction(name);
      ElMessage.success('已删除');
      await load();
    } catch (e) {
      ElMessage.error(e instanceof Error ? e.message : '删除失败');
    }
  }

  async function toggleEnabled(rule: AlertAction) {
    const next = !rule.enabled;
    try {
      await setAlertActionEnabled(rule.name, next);
      rule.enabled = next;
      ElMessage.success(next ? `已启用 "${rule.name}"` : `已停用 "${rule.name}"`);
    } catch (e) {
      ElMessage.error(e instanceof Error ? e.message : '启停失败');
    }
  }

  async function loadRuns(rule: AlertAction) {
    if (runsByRule.value[rule.name]) return; // 已加载
    try {
      const overview = await listAlertActionRuns(rule.name);
      runsByRule.value = { ...runsByRule.value, [rule.name]: overview };
    } catch (e) {
      ElMessage.error(e instanceof Error ? e.message : '加载触发历史失败');
    }
  }

  function clearRuns(name: string) {
    const next = { ...runsByRule.value };
    delete next[name];
    runsByRule.value = next;
  }

  onMounted(load);

  return {
    rules,
    filteredRules,
    loading,
    saving,
    error,
    configured,
    search,
    editing,
    editForm,
    runsByRule,
    load,
    startCreate,
    startEdit,
    cancelEdit,
    save,
    remove,
    toggleEnabled,
    loadRuns,
    clearRuns,
  };
}
