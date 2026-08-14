<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { ElMessage } from 'element-plus';

interface AlertMatch {
  alertname?: string;
  severity?: string;
  domain?: string;
}
interface AlertActionStep {
  tool: string;
  input: Record<string, string>;
}
interface AlertAction {
  name: string;
  alert_match: AlertMatch;
  tool_sequence: AlertActionStep[];
  execute_last_step?: boolean;
  description?: string;
}

const rules = ref<AlertAction[]>([]);
const loading = ref(false);
const error = ref('');
const configured = ref(true);
const editing = ref(false);
const editForm = ref<AlertAction>({
  name: '',
  alert_match: {},
  tool_sequence: [{ tool: '', input: {} }],
  execute_last_step: false,
  description: '',
});

async function load() {
  loading.value = true;
  error.value = '';
  try {
    const resp = await fetch('/v1/admin/alert-actions', { headers: { 'Content-Type': 'application/json' } });
    const body = await resp.json();
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
  editing.value = true;
  editForm.value = {
    name: '',
    alert_match: { severity: 'critical' },
    tool_sequence: [{ tool: '', input: { environment: '{environment}' } }],
    execute_last_step: false,
    description: '',
  };
}

function addStep() {
  editForm.value.tool_sequence.push({ tool: '', input: { environment: '{environment}' } });
}

function removeStep(idx: number) {
  editForm.value.tool_sequence.splice(idx, 1);
}

async function save() {
  if (!editForm.value.name) {
    ElMessage.error('规则名称不能为空');
    return;
  }
  try {
    const resp = await fetch('/v1/admin/alert-actions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(editForm.value),
    });
    if (!resp.ok) throw new Error('保存失败');
    ElMessage.success('已保存');
    editing.value = false;
    await load();
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : '保存失败');
  }
}

async function remove(name: string) {
  if (!confirm(`确定删除规则 "${name}" 吗？`)) return;
  try {
    const resp = await fetch(`/v1/admin/alert-actions/${name}`, { method: 'DELETE' });
    if (!resp.ok) throw new Error('删除失败');
    ElMessage.success('已删除');
    await load();
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : '删除失败');
  }
}

onMounted(load);
</script>

<template>
  <section data-test="admin-alert-actions" class="admin-page">
    <header class="topbar">
      <div>
        <p class="eyebrow">Admin</p>
        <h1>告警响应编排</h1>
        <p class="topbar-copy">配置告警→动作的自动响应规则。告警到达时自动执行工具序列（诊断+处置）。</p>
      </div>
      <div class="topbar-actions">
        <button class="mini-button" @click="load" :disabled="loading">刷新</button>
        <button class="primary-mini-button" @click="startCreate">新建规则</button>
      </div>
    </header>

    <p v-if="error" class="error-text" role="alert">{{ error }}</p>
    <p v-if="loading" class="loading-text">加载中…</p>

    <div v-if="!loading && !configured" class="config-banner">
      <strong>告警响应编排未启用</strong>
      <p>请在数据库中配置告警动作规则。</p>
    </div>

    <div v-if="editing" data-test="alert-action-editor" class="editor-panel">
      <h3>{{ editForm.name ? '编辑规则' : '新建规则' }}</h3>
      <label class="field">
        <span>规则名称</span>
        <el-input v-model="editForm.name" placeholder="如: kafka-high-lag" />
      </label>
      <label class="field">
        <span>描述</span>
        <el-input v-model="editForm.description" placeholder="可选" />
      </label>

      <div class="match-section">
        <h4>匹配条件（AND）</h4>
        <label class="field-inline">
          <span>alertname</span>
          <el-input v-model="editForm.alert_match.alertname" placeholder="可选" />
        </label>
        <label class="field-inline">
          <span>severity</span>
          <el-input v-model="editForm.alert_match.severity" placeholder="critical/warning" />
        </label>
        <label class="field-inline">
          <span>domain</span>
          <el-input v-model="editForm.alert_match.domain" placeholder="可选" />
        </label>
      </div>

      <div class="sequence-section">
        <h4>工具序列（按序执行）</h4>
        <div v-for="(step, idx) in editForm.tool_sequence" :key="idx" class="step-row">
          <span class="step-num">{{ idx + 1 }}</span>
          <el-input v-model="step.tool" placeholder="工具名" class="step-tool" />
          <el-input v-model="step.input.environment" placeholder="{environment}" class="step-input" />
          <button class="danger-inline" @click="removeStep(idx)" v-if="editForm.tool_sequence.length > 1">删</button>
        </div>
        <button class="secondary-inline" @click="addStep">+ 添加步骤</button>
      </div>

      <label class="field-inline">
        <input type="checkbox" v-model="editForm.execute_last_step" />
        <span>最后一步直接执行（默认：创建待审批 plan）</span>
      </label>

      <div class="editor-actions">
        <button class="primary-inline" @click="save">保存</button>
        <button class="secondary-inline" @click="editing = false">取消</button>
      </div>
    </div>

    <div v-if="!loading && !editing && rules.length === 0 && !error" class="empty">
      暂无告警响应规则。点击"新建规则"添加。
    </div>

    <div v-if="!editing" class="rules-list">
      <article v-for="rule in rules" :key="rule.name" class="rule-card" data-test="alert-action-rule">
        <header class="rule-header">
          <div>
            <strong class="rule-name">{{ rule.name }}</strong>
            <span v-if="rule.description" class="rule-desc">{{ rule.description }}</span>
          </div>
          <div class="rule-actions">
            <button class="secondary-inline" @click="editForm = { ...rule, tool_sequence: [...rule.tool_sequence] }; editing = true">编辑</button>
            <button class="danger-inline" @click="remove(rule.name)">删除</button>
          </div>
        </header>

        <div class="rule-match">
          <span v-if="rule.alert_match.alertname" class="tag">alertname: {{ rule.alert_match.alertname }}</span>
          <span v-if="rule.alert_match.severity" class="tag">severity: {{ rule.alert_match.severity }}</span>
          <span v-if="rule.alert_match.domain" class="tag">domain: {{ rule.alert_match.domain }}</span>
        </div>

        <div class="rule-sequence">
          <span v-for="(step, idx) in rule.tool_sequence" :key="idx" class="step-tag">
            <span class="step-idx">{{ idx + 1 }}</span>
            {{ step.tool }}
          </span>
          <span v-if="!rule.execute_last_step" class="tag tag-warn">最后一步 → 待审批</span>
        </div>
      </article>
    </div>
  </section>
</template>

<style scoped>
.admin-page { padding: var(--space-4); }
.topbar { display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: var(--space-4); }
.topbar-actions { display: flex; gap: var(--space-2); }
.eyebrow { font-size: var(--font-xs); color: var(--color-text-tertiary); text-transform: uppercase; letter-spacing: 0.05em; }
.topbar-copy { color: var(--color-text-secondary); font-size: var(--font-sm); }
.error-text { color: var(--color-danger); }
.loading-text { color: var(--color-text-tertiary); }
.config-banner { padding: var(--space-3); background: var(--color-bg-elevated); border-radius: var(--radius-lg); border: 1px solid var(--color-border); margin-bottom: var(--space-3); }
.empty { color: var(--color-text-tertiary); padding: var(--space-4); text-align: center; }

.editor-panel { background: var(--color-bg-elevated); border: 1px solid var(--color-border); border-radius: var(--radius-lg); padding: var(--space-4); margin-bottom: var(--space-4); }
.editor-panel h3 { margin-bottom: var(--space-3); }
.editor-panel h4 { margin: var(--space-3) 0 var(--space-2); font-size: var(--font-sm); color: var(--color-text-secondary); }
.field { display: block; margin-bottom: var(--space-2); }
.field span { display: block; font-size: var(--font-xs); color: var(--color-text-tertiary); margin-bottom: 4px; }
.field-inline { display: inline-flex; align-items: center; gap: var(--space-1); margin-right: var(--space-3); margin-bottom: var(--space-2); }
.field-inline span { font-size: var(--font-xs); color: var(--color-text-tertiary); white-space: nowrap; }
.match-section, .sequence-section { margin-bottom: var(--space-3); }
.step-row { display: flex; align-items: center; gap: var(--space-2); margin-bottom: var(--space-1); }
.step-num { font-size: var(--font-xs); color: var(--color-text-tertiary); width: 20px; text-align: center; }
.step-tool { flex: 0 0 200px; }
.step-input { flex: 1; }
.editor-actions { display: flex; gap: var(--space-2); margin-top: var(--space-3); }

.rules-list { display: flex; flex-direction: column; gap: var(--space-3); }
.rule-card { background: var(--color-bg-elevated); border: 1px solid var(--color-border); border-radius: var(--radius-lg); padding: var(--space-3); }
.rule-header { display: flex; justify-content: space-between; align-items: flex-start; margin-bottom: var(--space-2); }
.rule-name { font-weight: 600; }
.rule-desc { font-size: var(--font-xs); color: var(--color-text-secondary); margin-left: var(--space-2); }
.rule-actions { display: flex; gap: var(--space-1); }
.rule-match { display: flex; flex-wrap: wrap; gap: var(--space-1); margin-bottom: var(--space-2); }
.tag { font-size: var(--font-xs); padding: 2px 8px; border-radius: var(--radius-md); background: var(--color-bg); color: var(--color-text-secondary); border: 1px solid var(--color-border); }
.tag-warn { background: #fff3e0; color: #e65100; border-color: #ffcc80; }
.rule-sequence { display: flex; flex-wrap: wrap; gap: var(--space-1); align-items: center; }
.step-tag { font-size: var(--font-xs); padding: 2px 8px; border-radius: var(--radius-md); background: var(--color-primary-bg); color: var(--color-primary); border: 1px solid var(--color-primary-border); }
.step-idx { font-weight: 600; margin-right: 4px; }
.primary-mini-button { background: var(--color-primary); color: white; border: none; padding: 6px 12px; border-radius: var(--radius-md); font-size: var(--font-xs); cursor: pointer; }
.mini-button { padding: 6px 12px; border: 1px solid var(--color-border); border-radius: var(--radius-md); font-size: var(--font-xs); cursor: pointer; background: var(--color-bg); }
.primary-inline { background: var(--color-primary); color: white; border: none; padding: 4px 10px; border-radius: var(--radius-md); font-size: var(--font-xs); cursor: pointer; }
.secondary-inline { border: 1px solid var(--color-border); background: var(--color-bg); padding: 4px 10px; border-radius: var(--radius-md); font-size: var(--font-xs); cursor: pointer; }
.danger-inline { border: 1px solid var(--color-danger-border); color: var(--color-danger); background: none; padding: 4px 10px; border-radius: var(--radius-md); font-size: var(--font-xs); cursor: pointer; }
</style>
