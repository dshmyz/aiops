<script setup lang="ts">
import { computed } from 'vue';
import type { AlertAction, AlertActionRunOverview } from '../../types';

const props = defineProps<{
  rule: AlertAction;
  runs?: AlertActionRunOverview;
}>();

const emit = defineEmits<{
  (event: 'edit', rule: AlertAction): void;
  (event: 'delete', rule: AlertAction): void;
  (event: 'toggle', rule: AlertAction): void;
  (event: 'show-runs', rule: AlertAction): void;
}>();

const matchTags = computed(() => {
  const tags: string[] = [];
  const m = props.rule.alert_match ?? {};
  if (m.alertname) tags.push(`alertname: ${m.alertname}`);
  if (m.severity) tags.push(`severity: ${m.severity}`);
  if (m.domain) tags.push(`domain: ${m.domain}`);
  for (const lm of m.labels ?? []) {
    if (lm.key) tags.push(`${lm.key}${lm.operator && lm.operator !== 'exact' ? ` ${lm.operator}` : ''}: ${lm.value ?? ''}`);
  }
  return tags;
});

const hasAnyOf = computed(() => (props.rule.alert_match?.any_of?.length ?? 0) > 0);
const stats = computed(() => props.runs?.stats);
const successRate = computed(() => {
  const s = stats.value;
  if (!s || s.total === 0) return null;
  return Math.round((s.success / s.total) * 100);
});

function formatTime(iso?: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  return d.toLocaleString('zh-CN', { hour12: false });
}
</script>

<template>
  <article
    class="rule-card"
    :class="{ 'is-disabled': rule.enabled === false }"
    data-test="alert-action-rule"
  >
    <header class="rule-header">
      <div class="rule-title">
        <span class="rule-name">{{ rule.name }}</span>
        <el-tag v-if="rule.enabled === false" type="info" size="small" effect="plain" data-test="rule-disabled-tag">已停用</el-tag>
        <span v-if="rule.description" class="rule-desc">{{ rule.description }}</span>
      </div>
      <div class="rule-actions">
        <label class="toggle" :title="rule.enabled === false ? '启用该规则' : '停用该规则'">
          <el-switch
            :model-value="rule.enabled !== false"
            size="small"
            :aria-label="rule.name + '启停'"
            data-test="rule-enabled-switch"
            @change="emit('toggle', rule)"
          />
        </label>
        <el-button size="small" text @click="emit('show-runs', rule)" data-test="rule-runs-btn">触发历史</el-button>
        <el-button size="small" text type="primary" @click="emit('edit', rule)" data-test="rule-edit-btn">编辑</el-button>
        <el-button size="small" text type="danger" @click="emit('delete', rule)" data-test="rule-delete-btn">删除</el-button>
      </div>
    </header>

    <div class="rule-match">
      <span v-for="(tag, i) in matchTags" :key="i" class="tag">{{ tag }}</span>
      <span v-if="hasAnyOf" class="tag tag-or" title="另有或条件组">+ OR 组</span>
      <span v-if="!rule.enabled" class="tag tag-muted">不参与匹配</span>
    </div>

    <div class="rule-sequence">
      <span v-for="(step, idx) in rule.tool_sequence" :key="idx" class="step-tag">
        <span class="step-idx">{{ idx + 1 }}</span>
        {{ step.tool }}
      </span>
      <span v-if="!rule.execute_last_step" class="tag tag-warn">最后一步 → 待审批</span>
    </div>

    <div v-if="runs" class="rule-runs" data-test="rule-runs-panel">
      <div class="run-stats">
        <span class="stat">触发 <b>{{ stats?.total ?? 0 }}</b> 次</span>
        <span class="stat ok">成功 <b>{{ stats?.success ?? 0 }}</b></span>
        <span class="stat bad">失败 <b>{{ stats?.failure ?? 0 }}</b></span>
        <span class="stat" v-if="successRate !== null">成功率 <b>{{ successRate }}%</b></span>
      </div>
      <div v-if="runs.recent && runs.recent.length > 0" class="run-list">
        <div v-for="run in runs.recent.slice(0, 5)" :key="run.id" class="run-item" :class="`is-${run.status}`">
          <span class="run-status">{{ run.status === 'success' ? '成功' : '失败' }}</span>
          <span class="run-alert" :title="run.alert_title">{{ run.alert_title || run.alert_id || '-' }}</span>
          <span class="run-time">{{ formatTime(run.created_at) }}</span>
        </div>
      </div>
      <p v-else class="run-empty">暂无触发记录。规则被命中后会在此显示执行结果。</p>
    </div>
  </article>
</template>

<style scoped>
.rule-card { background: var(--color-bg-elevated); border: 1px solid var(--color-border); border-radius: var(--radius-lg); padding: var(--space-3); }
.rule-card.is-disabled { opacity: 0.75; }
.rule-header { display: flex; justify-content: space-between; align-items: flex-start; gap: var(--space-2); margin-bottom: var(--space-2); }
.rule-title { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.rule-name { font-weight: 600; }
.rule-desc { font-size: var(--font-xs); color: var(--color-text-secondary); }
.rule-actions { display: flex; align-items: center; gap: 2px; }
.toggle { display: inline-flex; align-items: center; }
.rule-match { display: flex; flex-wrap: wrap; gap: var(--space-1); margin-bottom: var(--space-2); }
.rule-sequence { display: flex; flex-wrap: wrap; gap: var(--space-1); align-items: center; }
.tag { font-size: var(--font-xs); padding: 2px 8px; border-radius: var(--radius-md); background: var(--color-bg); color: var(--color-text-secondary); border: 1px solid var(--color-border); }
.tag-warn { background: var(--color-warning-bg); color: var(--color-warning); border-color: var(--color-warning-border); }
.tag-or { background: var(--color-primary-bg); color: var(--color-primary); border-color: var(--color-primary-border); }
.tag-muted { color: var(--color-text-tertiary); }
.step-tag { font-size: var(--font-xs); padding: 2px 8px; border-radius: var(--radius-md); background: var(--color-primary-bg); color: var(--color-primary); border: 1px solid var(--color-primary-border); }
.step-idx { font-weight: 600; margin-right: 4px; }
.rule-runs { margin-top: var(--space-2); border-top: 1px dashed var(--color-border); padding-top: var(--space-2); }
.run-stats { display: flex; gap: 12px; font-size: var(--font-xs); color: var(--color-text-secondary); margin-bottom: 6px; }
.stat b { color: var(--color-text); }
.stat.ok b { color: var(--color-success); }
.stat.bad b { color: var(--color-danger); }
.run-list { display: flex; flex-direction: column; gap: 4px; }
.run-item { display: flex; gap: 10px; align-items: center; font-size: var(--font-xs); color: var(--color-text-secondary); }
.run-status { flex: none; font-weight: 600; }
.run-item.is-success .run-status { color: var(--color-success); }
.run-item.is-failure .run-status { color: var(--color-danger); }
.run-alert { flex: 1; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.run-time { flex: none; color: var(--color-text-tertiary); }
.run-empty { font-size: var(--font-xs); color: var(--color-text-tertiary); margin: 0; }
</style>
