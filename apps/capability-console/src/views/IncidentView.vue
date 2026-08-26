<script setup lang="ts">
import { ref } from 'vue';
import { useIncident } from '../composables/useIncident';
import { executeRead } from '../api';
import type { IncidentProbe, IncidentTimelineItem } from '../types';
import ViewShell from '../components/ViewShell.vue';

const emit = defineEmits<{
  'jump-to-audit': [toolName: string];
}>();

const {
  pivot,
  result,
  loading,
  error,
  hasSearched,
  run,
} = useIncident();

// 表单字段与 pivot 直接绑定；触发一次 run 时才提交。
const domain = ref('');
const resourceType = ref('');
const resourceName = ref('');

async function submit() {
  await run({
    domain: domain.value.trim() || undefined,
    resource_type: resourceType.value.trim() || undefined,
    resource_name: resourceName.value.trim() || undefined,
  });
}

function jumpToAudit(item: IncidentTimelineItem) {
  if (!item.action_plan_id) return;
  emit('jump-to-audit', item.tool_name || '');
}

// 每条只读探测的执行状态（per-tool）：loading / result / error。
const probeState = ref<Record<string, { loading: boolean; result?: unknown; error?: string }>>({});

// 执行一条只读探测（经通用工具读端点，服务端自带权限与审计）。工具不可执行时
// 捕获错误落到该 probe 的 error，不影响其余探测或整页。
async function runProbe(probe: IncidentProbe) {
  const key = probe.tool_name;
  const store = probeState.value;
  if (!store[key]) {
    store[key] = { loading: false };
  }
  const s = store[key]; // 经 ref 读回，拿到的是 reactive proxy，后续写入才能触发渲染
  s.loading = true;
  s.error = undefined;
  s.result = undefined;
  try {
    s.result = await executeRead(probe.tool_name, probe.input ?? {});
  } catch (err) {
    s.error = err instanceof Error ? err.message : '探测失败';
  } finally {
    s.loading = false;
  }
}

// 探测结果形态异构（能力读 / 静态元工具）。优先取可读字段，兜底 JSON。
function formatProbeResult(state: { result?: unknown }): string {
  const r = state.result;
  if (r && typeof r === 'object') {
    const o = r as Record<string, unknown>;
    if (typeof o.summary === 'string' && o.summary) return o.summary;
    const statusBits = [o.severity, o.status].filter((v): v is string => typeof v === 'string' && v !== '');
    const where = [o.kind, o.resource].filter((v): v is string => typeof v === 'string' && v !== '');
    if (statusBits.length) return where.length ? `${where.join(' / ')} · ${statusBits.join(' · ')}` : statusBits.join(' · ');
    if (typeof o.data !== 'undefined') return JSON.stringify(o.data);
  }
  return JSON.stringify(r);
}

function fmt(ts: string | undefined | null): string {
  if (!ts) return '';
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return ts;
  return d.toLocaleString();
}

const severityClass: Record<string, string> = {
  critical: 'sev-critical',
  warning: 'sev-warning',
  ok: 'sev-ok',
  info: 'sev-info',
};
</script>

<template>
  <ViewShell
    class="incident-entry"
    data-test="incident-entry"
    data-view="incident"
    eyebrow="Incident View"
    title="告警全景"
    copy="输入资源身份（域 / 资源类型 / 资源名），把告警与其相关审计、定时巡检、只读探测与匹配 Runbook 串成一张可回链的全景。"
  >

    <form class="incident-form" @submit.prevent="submit">
      <label class="field">
        <span>Domain</span>
        <input v-model="domain" placeholder="如 minio / kafka" data-test="incident-domain" />
      </label>
      <label class="field">
        <span>资源类型</span>
        <input v-model="resourceType" placeholder="如 bucket / consumer_group" data-test="incident-resource-type" />
      </label>
      <label class="field">
        <span>资源名</span>
        <input v-model="resourceName" placeholder="如 archive" data-test="incident-resource-name" />
      </label>
      <button class="primary-button" type="submit" :disabled="loading" data-test="incident-run">
        {{ loading ? '加载中…' : '查看全景' }}
      </button>
    </form>

    <p v-if="error" class="error-text" data-test="incident-error">{{ error }}</p>
    <div v-if="!loading && hasSearched && result && result.counts" class="counts-bar" data-test="incident-counts">
      <span class="count-chip">审计事件 {{ result.counts.audit }}</span>
      <span class="count-chip">巡检 run {{ result.counts.scheduled_runs }}</span>
      <span class="count-chip">只读探测 {{ result.counts.probes }}</span>
      <span class="count-chip">Runbook {{ result.counts.runbooks }}</span>
      <span class="count-chip count-chip--danger">近期写操作 {{ result.counts.recent_writes }}</span>
    </div>

    <div v-if="!loading && hasSearched && result" class="incident-body">
      <!-- 资源为空：没有找到任何证据 -->
      <div v-if="result.counts && result.counts.audit === 0 && result.counts.runbooks === 0 && result.counts.recent_writes === 0" class="empty" data-test="incident-empty">
        未找到该资源的告警证据。请检查 Domain / 资源类型 / 资源名，或先经 webhook 吸入一条告警。
      </div>

      <template v-else>
        <!-- 告警本体摘要 -->
        <section v-if="result.alert" class="incident-card alert-card">
          <div class="card-title">
            <h3>告警</h3>
            <span v-if="result.alert!.severity" class="severity-badge" :class="severityClass[String(result.alert!.severity)] ?? ''">
              {{ result.alert!.severity }}
            </span>
          </div>
          <p class="alert-title">{{ result.alert!.title }}</p>
          <dl class="kv">
            <div><dt>来源</dt><dd>{{ result.alert!.source }}</dd></div>
            <div><dt>状态</dt><dd>{{ result.alert!.status }}</dd></div>
            <div><dt>触发时间</dt><dd>{{ fmt(result.alert!.fired_at as string) }}</dd></div>
            <div v-if="result.alert!.description"><dt>描述</dt><dd>{{ result.alert!.description }}</dd></div>
          </dl>
        </section>

        <!-- Timeline: 相关审计事件 -->
        <section class="incident-card">
          <div class="card-title"><h3>证据时间线</h3><span class="muted">相关审计事件</span></div>
          <ul v-if="result.timeline && result.timeline.length > 0" class="timeline-list" data-test="incident-timeline">
            <li v-for="item in result.timeline" :key="item.id" class="timeline-item">
              <div class="tl-head">
                <code class="tool-name">{{ item.tool_name }}</code>
                <span class="muted">{{ fmt(item.created_at) }}</span>
              </div>
              <div class="tl-meta">
                <span>决策：{{ item.decision || '—' }}</span>
                <button
                  v-if="item.action_plan_id"
                  class="plan-jump"
                  data-test="incident-plan-jump"
                  @click="jumpToAudit(item)"
                >
                  {{ item.action_plan_id }} ↗
                </button>
                <span v-else-if="item.trace_id" class="muted muted-block">trace {{ item.trace_id }}</span>
              </div>
            </li>
          </ul>
          <p v-else class="empty-inline">无相关审计事件。</p>
        </section>

        <!-- 定时巡检 run -->
        <section class="incident-card">
          <div class="card-title"><h3>相关巡检 run</h3></div>
          <ul v-if="result.scheduled_runs && result.scheduled_runs.length > 0" class="kv-list" data-test="incident-runs">
            <li v-for="run in result.scheduled_runs" :key="run.id">
              <code>{{ run.task_id }}</code>
              <span class="run-status" :class="run.status === 'succeeded' ? 'ok' : 'bad'">{{ run.status }}</span>
              <span class="muted">{{ fmt(run.started_at) }}</span>
            </li>
          </ul>
          <p v-else class="empty-inline">无相关巡检 run。</p>
        </section>

        <!-- 只读探测：点击执行真实只读探测 -->
        <section class="incident-card">
          <div class="card-title"><h3>可跑只读探测</h3><span class="muted note">点击执行只读探测</span></div>
          <ul v-if="result.probes && result.probes.length > 0" class="probe-list" data-test="incident-probes">
            <li v-for="probe in result.probes" :key="probe.tool_name" class="probe-item">
              <div class="probe-main">
                <div class="probe-head">
                  <code class="tool-name">{{ probe.tool_name }}</code>
                  <button
                    type="button"
                    class="probe-run"
                    data-test="incident-probe-run"
                    :disabled="probeState[probe.tool_name]?.loading"
                    @click="runProbe(probe)"
                  >
                    {{ probeState[probe.tool_name]?.loading ? '执行中…' : '执行' }}
                  </button>
                </div>
                <span v-if="probe.input && Object.keys(probe.input).length" class="muted probe-input">
                  {{ JSON.stringify(probe.input) }}
                </span>
                <p v-if="probeState[probe.tool_name]?.result !== undefined" class="probe-result" data-test="incident-probe-result">
                  {{ formatProbeResult(probeState[probe.tool_name]!) }}
                </p>
                <p v-else-if="probeState[probe.tool_name]?.error" class="probe-error" data-test="incident-probe-error">
                  该探测当前不可执行：{{ probeState[probe.tool_name]!.error }}
                </p>
              </div>
            </li>
          </ul>
          <p v-else class="empty-inline">无匹配的只读探测。</p>
        </section>

        <!-- 匹配 Runbook -->
        <section class="incident-card">
          <div class="card-title"><h3>匹配 Runbook</h3></div>
          <ul v-if="result.runbooks && result.runbooks.length > 0" class="runbook-list" data-test="incident-runbooks">
            <li v-for="rb in result.runbooks" :key="rb.slug">
              <code>{{ rb.slug }}</code>
              <span class="confidence">{{ Math.round(rb.confidence * 100) }}%</span>
              <span class="muted">{{ rb.tool_sequence?.join(' → ') }}</span>
            </li>
          </ul>
          <p v-else class="empty-inline">无匹配 Runbook。</p>
        </section>

        <!-- 近期写操作（判断力提示） -->
        <section v-if="result.recent_writes && result.recent_writes.count > 0" class="incident-card alert-card--danger">
          <div class="card-title"><h3>近期写操作</h3><span class="muted">该资源最近被改过，排查根因时须留意</span></div>
          <ul class="kv-list" data-test="incident-writes">
            <li v-for="w in result.recent_writes.events" :key="w.id">
              <code>{{ w.tool_name }}</code>
              <span class="muted">{{ fmt(w.created_at) }}</span>
            </li>
          </ul>
        </section>
      </template>
    </div>
  </ViewShell>
</template>

<style scoped>
.incident-form {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr)) auto;
  gap: var(--space-3);
  align-items: end;
  padding: var(--space-3);
  background: var(--color-bg-elevated);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-sm);
}
.field { display: flex; flex-direction: column; gap: 4px; }
.field span { font-size: var(--font-sm); color: var(--color-text-tertiary); }
.field input,
.incident-form input {
  padding: 8px 10px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-bg);
  color: var(--color-text-primary);
  font-size: var(--font-md);
}
.primary-button {
  padding: 8px 16px;
  border: none;
  border-radius: var(--radius-md);
  background: var(--color-accent);
  color: var(--color-bg);
  font-weight: 600;
  cursor: pointer;
}
.primary-button:disabled { opacity: 0.6; cursor: default; }

.counts-bar { display: flex; flex-wrap: wrap; gap: var(--space-2); }
.count-chip {
  padding: 4px 12px;
  border-radius: var(--radius-pill);
  background: var(--color-bg-elevated);
  border: 1px solid var(--color-border);
  font-size: var(--font-sm);
  color: var(--color-text-secondary);
}
.count-chip--danger { color: var(--color-danger); border-color: var(--color-danger); }

.incident-body { display: flex; flex-direction: column; gap: var(--space-3); }
.incident-card {
  background: var(--color-bg-elevated);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-sm);
  padding: var(--space-4);
}
.alert-card { border-left: 4px solid var(--color-danger); }
.alert-card--danger { border-left: 4px solid var(--color-danger); }
.card-title {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  margin-bottom: var(--space-2);
}
.card-title h3 { margin: 0; font-size: var(--font-md); color: var(--color-text-primary); }
.muted { color: var(--color-text-tertiary); font-size: var(--font-sm); }
.note { font-style: italic; }

.alert-title { margin: 0 0 var(--space-2); font-size: var(--font-lg); color: var(--color-text-primary); }
.severity-badge {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: var(--radius-pill);
  font-size: var(--font-sm);
  font-weight: 600;
  line-height: 1.6;
  letter-spacing: 0.02em;
  white-space: nowrap;
  background: var(--color-accent-soft);
  color: var(--color-accent);
}
.sev-critical { background: var(--color-danger-soft); color: var(--color-danger); }
.sev-warning  { background: var(--color-warning-soft); color: var(--color-warning); }
.sev-ok       { background: var(--color-success-soft); color: var(--color-success); }

.kv { margin: 0; display: grid; grid-template-columns: 1fr; gap: 4px; }
.kv > div { display: flex; gap: 8px; }
.kv dt { color: var(--color-text-tertiary); font-size: var(--font-sm); min-width: 70px; }
.kv dd { margin: 0; font-size: var(--font-sm); color: var(--color-text-primary); word-break: break-all; }

.timeline-list, .kv-list, .probe-list, .runbook-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
.timeline-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 8px 10px;
  border-radius: var(--radius-md);
  background: var(--color-bg);
  border: 1px solid var(--color-border);
}
.tl-head, .tl-meta { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.tool-name { color: var(--color-accent); font-size: var(--font-sm); }
.plan-jump {
  border: none;
  background: none;
  color: var(--color-accent);
  font-size: var(--font-sm);
  cursor: pointer;
  text-decoration: underline;
  padding: 0;
}
.muted-block { overflow: hidden; text-overflow: ellipsis; max-width: 260px; white-space: nowrap; }

.kv-list li, .runbook-list li { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.probe-item {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 8px 10px;
  border-radius: var(--radius-md);
  background: var(--color-bg);
  border: 1px solid var(--color-border);
}
.probe-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.probe-run {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-bg-elevated);
  color: var(--color-text-primary);
  font-size: var(--font-xs);
  font-weight: 600;
  padding: 2px 10px;
  cursor: pointer;
}
.probe-run:hover:not(:disabled) { border-color: var(--color-accent); color: var(--color-accent); }
.probe-run:disabled { opacity: 0.6; cursor: default; }
.probe-input { word-break: break-all; }
.probe-result { margin: 0; font-size: var(--font-sm); color: var(--color-text-primary); word-break: break-all; }
.probe-error { margin: 0; font-size: var(--font-sm); color: var(--color-danger); word-break: break-all; }
.run-status { font-size: var(--font-xs); font-weight: 600; }
.run-status.ok { color: #15803d; }
.run-status.bad { color: var(--color-danger); }
.confidence { font-size: var(--font-xs); font-weight: 600; color: var(--color-accent); }

.empty, .empty-inline { color: var(--color-text-tertiary); font-size: var(--font-sm); }
.empty { padding: var(--space-4); text-align: center; }
.error-text { color: var(--color-danger); font-size: var(--font-sm); }
</style>
