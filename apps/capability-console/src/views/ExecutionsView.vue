<script setup lang="ts">
import { reactive, ref } from 'vue';
import type { ExecutionFilter, ExecutionRecord } from '../types';
import { labelForExecutionStatus } from '../labels';
import { formatAbsoluteTime, formatCompactDateTime } from '../conversationFormat';
import { useExecutions } from '../composables/useExecutions';

const {
  executions,
  executionsLoading,
  executionsError,
  nextCursor,
  loadingMore,
  refresh,
  loadMore,
  applyFilter,
} = useExecutions();

const emit = defineEmits<{
  'jump-to-audit': [planID: string, toolName: string];
}>();

// 从执行记录跳到该 plan 的审计记录：携带 planID 与执行的工具名，App.vue 负责
// 切到审计视图并按工具过滤，定位这条执行的确认/执行审计链。
function jumpToAudit(record: ExecutionRecord) {
  if (!record.action_plan_id) return;
  emit('jump-to-audit', record.action_plan_id, record.tool_name ?? '');
}

// 初始加载由 App.vue 在视图激活时触发 refresh；这里保持空起始。
void refresh();

const localFilter = reactive({
  status: '',
  tool: '',
  actionPlanID: '',
  startedAfter: '',
  startedBefore: '',
  limit: '',
});

// 与后端 executionStatusLabel 对齐的可过滤状态集合。
const statusOptions = ['succeeded', 'failed', 'denied', 'pending', 'confirmed', 'executing'];

const selectedID = ref<string | null>(null);

function buildFilter(): ExecutionFilter {
  const filter: ExecutionFilter = {};
  if (localFilter.status) filter.status = localFilter.status;
  if (localFilter.tool) filter.tool = localFilter.tool;
  if (localFilter.actionPlanID) filter.action_plan_id = localFilter.actionPlanID;
  if (localFilter.startedAfter) filter.started_after = toRFC3339(localFilter.startedAfter);
  if (localFilter.startedBefore) filter.started_before = toRFC3339(localFilter.startedBefore);
  if (localFilter.limit) {
    const limit = Number(localFilter.limit);
    if (Number.isFinite(limit) && limit > 0) filter.limit = limit;
  }
  return filter;
}

function apply() {
  selectedID.value = null;
  applyFilter(buildFilter());
}

// datetime-local 值 (YYYY-MM-DDTHH:mm) 转 RFC3339（后端要求），本地时区无偏移即可。
function toRFC3339(datetimeLocal: string): string {
  if (!datetimeLocal) return '';
  return new Date(datetimeLocal).toISOString();
}

function isSelected(record: ExecutionRecord): boolean {
  return record.id === selectedID.value;
}

function prettyJSON(value: unknown): string {
  if (value === undefined || value === null) return '';
  return JSON.stringify(value, null, 2);
}
</script>

<template>
  <section data-test="executions-entry" data-view="executions" class="executions-entry">
    <header class="topbar">
      <div>
        <p class="eyebrow">Execution History</p>
        <h1>执行历史</h1>
        <p class="topbar-copy">查看每次写操作计划的执行记录（仅管理员可见），按状态/工具过滤定位执行情况。</p>
      </div>
      <div class="actions">
        <button class="mini-button" :disabled="executionsLoading" data-test="executions-refresh" @click="refresh">
          {{ executionsLoading ? '刷新中' : '刷新' }}
        </button>
      </div>
    </header>

    <div class="executions-filters">
      <select v-model="localFilter.status" data-test="executions-filter-status">
        <option value="">全部状态</option>
        <option v-for="status in statusOptions" :key="status" :value="status">
          {{ labelForExecutionStatus(status) }}
        </option>
      </select>
      <input v-model="localFilter.tool" placeholder="按工具过滤" data-test="executions-filter-tool" />
      <input v-model="localFilter.actionPlanID" placeholder="按 Plan ID 过滤" data-test="executions-filter-plan" />
      <input v-model="localFilter.startedAfter" type="datetime-local" title="起始时间" data-test="executions-filter-after" />
      <input v-model="localFilter.startedBefore" type="datetime-local" title="结束时间" data-test="executions-filter-before" />
      <input v-model="localFilter.limit" type="number" min="1" placeholder="每页 N 条" data-test="executions-filter-limit" />
      <button class="mini-button" data-test="executions-filter-apply" @click="apply">应用</button>
    </div>
    <p v-if="executionsError" class="error-text">{{ executionsError }}</p>

    <div class="executions-workspace">
      <div class="executions-table-wrap">
        <table class="executions-table">
          <thead>
            <tr>
              <th>时间</th>
              <th>状态</th>
              <th>工具</th>
              <th>Plan ID</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="record in executions"
              :key="record.id"
              :data-test="`executions-row-${record.id}`"
              :class="{ active: isSelected(record) }"
              @click="selectedID = record.id"
            >
              <td class="mono" :title="record.created_at">{{ formatCompactDateTime(record.created_at) }}</td>
              <td :class="['execution-status', `execution-status-${record.status}`]">
                {{ labelForExecutionStatus(record.status) }}
              </td>
              <td class="mono">{{ record.tool_name || '-' }}</td>
              <td class="mono">
                <button
                  v-if="record.action_plan_id"
                  type="button"
                  class="plan-jump"
                  :data-test="`executions-plan-jump-${record.id}`"
                  :title="`在审计记录中查看该 plan（${record.tool_name || ''}）`"
                  @click.stop="jumpToAudit(record)"
                >
                  {{ record.action_plan_id }} ↗
                </button>
                <span v-else>-</span>
              </td>
            </tr>
          </tbody>
        </table>
        <p v-if="!executionsLoading && executions.length === 0" class="empty">暂无执行记录。</p>
        <div v-if="nextCursor" class="executions-load-more">
          <button
            class="mini-button"
            :disabled="loadingMore"
            data-test="executions-load-more"
            @click="loadMore"
          >
            {{ loadingMore ? '加载中…' : '加载更多' }}
          </button>
        </div>
      </div>

      <aside class="executions-detail">
        <div v-if="!selectedID" class="empty">点击左侧某条记录查看详情。</div>
        <div v-else v-for="record in executions.filter(isSelected)" :key="record.id" class="execution-detail-card">
          <header class="detail-header">
            <span class="badge" :class="`tag-execution-${record.status}`">
              {{ labelForExecutionStatus(record.status) }}
            </span>
            <h3 class="mono">{{ record.tool_name || record.id }}</h3>
          </header>
          <dl>
            <div><dt>执行 ID</dt><dd class="mono">{{ record.id }}</dd></div>
            <div>
              <dt>Plan ID</dt>
              <dd class="mono">
                <button
                  v-if="record.action_plan_id"
                  type="button"
                  class="plan-jump"
                  data-test="executions-detail-plan-jump"
                  :title="`在审计记录中查看该 plan（${record.tool_name || ''}）`"
                  @click="jumpToAudit(record)"
                >
                  {{ record.action_plan_id }} ↗
                </button>
                <span v-else>-</span>
              </dd>
            </div>
            <div><dt>开始时间</dt><dd class="mono" :title="record.started_at || undefined">{{ record.started_at ? formatAbsoluteTime(record.started_at) : '-' }}</dd></div>
            <div><dt>完成时间</dt><dd class="mono" :title="record.completed_at || undefined">{{ record.completed_at ? formatAbsoluteTime(record.completed_at) : '-' }}</dd></div>
          </dl>

          <section v-if="record.result_summary" class="detail-block">
            <h5>结果摘要</h5>
            <pre>{{ prettyJSON(record.result_summary) }}</pre>
          </section>
          <section v-if="record.error_summary" class="detail-block detail-block--error">
            <h5>错误信息</h5>
            <pre>{{ record.error_summary }}</pre>
          </section>
          <section v-if="record.verification" class="detail-block">
            <h5>执行后验证</h5>
            <pre>{{ prettyJSON(record.verification) }}</pre>
          </section>
        </div>
      </aside>
    </div>
  </section>
</template>

<style scoped>
.executions-entry {
  display: flex;
  flex-direction: column;
  gap: var(--space-3, 0.75rem);
  padding: 0 var(--space-6, 1.5rem) var(--space-6, 1.5rem);
  flex: 1;
  min-height: 0;
}

/* 与其它带顶栏视图一致：entry 已提供 24px 左右内边距，顶栏自身不再叠加左边距。 */
.executions-entry .topbar {
  padding: var(--space-5, 1.25rem) 0 var(--space-4, 1rem);
}

.executions-filters {
  display: grid;
  grid-template-columns: repeat(6, 1fr);
  gap: var(--space-2);
  align-items: center;
  padding: var(--space-3);
  background: var(--color-bg-elevated);
  border: none;
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-sm);
}

.executions-filters input,
.executions-filters select {
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-bg);
  color: var(--color-text-primary);
  font-size: var(--font-sm);
  outline: none;
  transition: border-color 0.15s;
}

.executions-filters input:focus,
.executions-filters select:focus {
  border-color: var(--color-accent);
}

@media (max-width: 1200px) {
  .executions-filters {
    grid-template-columns: repeat(3, 1fr);
  }
}

.executions-workspace {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(320px, 400px);
  gap: var(--space-4);
  align-items: start;
  flex: 1;
  min-height: 0;
}

@media (max-width: 1100px) {
  .executions-workspace {
    grid-template-columns: 1fr;
  }
}

.executions-table-wrap {
  overflow-x: auto;
  border: none;
  border-radius: var(--radius-xl);
  background: var(--color-bg-elevated);
  box-shadow: var(--shadow-sm);
}

.executions-table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--font-md);
  cursor: pointer;
  table-layout: fixed;
}

.executions-table thead th {
  text-align: left;
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--color-border);
  color: var(--color-text-tertiary);
  font-weight: 600;
  font-size: var(--font-xs);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.executions-table th:nth-child(1), .executions-table td:nth-child(1) { width: 28%; }
.executions-table th:nth-child(2), .executions-table td:nth-child(2) { width: 14%; }
.executions-table th:nth-child(3), .executions-table td:nth-child(3) { width: 20%; }
.executions-table th:nth-child(4), .executions-table td:nth-child(4) { width: 38%; }

.executions-table tbody td {
  padding: var(--space-2) var(--space-4);
  border-bottom: 1px solid var(--color-border);
  color: var(--color-text-primary);
  vertical-align: top;
  word-break: break-word;
  overflow-wrap: anywhere;
}

.executions-table tbody tr {
  transition: background 0.15s var(--ease-out);
}

.executions-table tbody tr:hover {
  background: var(--color-bg-hover);
}

.executions-table tbody tr.active {
  background: var(--color-bg-active);
}

.executions-table tbody tr:last-child td {
  border-bottom: none;
}

.mono {
  font-family: var(--font-mono, monospace);
  font-size: 0.8rem;
  word-break: break-all;
}

.execution-status-succeeded { color: var(--color-success, #2c8a3e); }
.execution-status-failed { color: var(--color-danger, #d33); }
.execution-status-denied { color: var(--color-warning, #b8860b); }

.executions-load-more {
  display: flex;
  justify-content: center;
  padding: 0.75rem;
  border-top: 1px solid var(--color-border);
}

.plan-jump {
  background: none;
  border: none;
  padding: 0;
  margin: 0;
  font-family: var(--font-mono, monospace);
  font-size: 0.8rem;
  color: var(--color-accent, #2f6fed);
  cursor: pointer;
  word-break: break-all;
  text-align: left;
}

.plan-jump:hover {
  text-decoration: underline;
}

.empty {
  padding: 1.5rem;
  text-align: center;
  color: var(--color-text-muted);
  font-size: 0.85rem;
  margin: 0;
}

.error-text {
  margin: 0;
  font-size: 0.75rem;
  color: var(--color-danger, #d33);
}

.executions-detail {
  border: none;
  border-radius: var(--radius-xl);
  background: var(--color-bg-elevated);
  box-shadow: var(--shadow-sm);
  padding: var(--space-4);
}

.execution-detail-card {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.detail-header {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.detail-header h3 {
  margin: 0;
  font-size: var(--font-base);
  color: var(--color-text-primary);
}

.execution-detail-card dl {
  margin: 0;
  display: grid;
  grid-template-columns: 1fr;
  gap: 6px;
}

.execution-detail-card dl > div {
  display: flex;
  gap: 6px;
}

.execution-detail-card dt {
  color: var(--color-text-tertiary);
  font-size: var(--font-sm);
  min-width: 90px;
}

.execution-detail-card dd {
  margin: 0;
  font-size: var(--font-md);
  color: var(--color-text-primary);
  word-break: break-all;
}

.detail-block {
  margin-top: var(--space-2);
}

.detail-block h5 {
  margin: 0 0 4px;
  font-size: var(--font-sm);
  color: var(--color-text-tertiary);
  font-weight: 600;
}

.detail-block pre {
  margin: 0;
  padding: var(--space-2);
  background: var(--color-bg);
  border-radius: var(--radius-sm);
  border: 1px solid var(--color-border);
  font-size: var(--font-sm);
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 260px;
  overflow: auto;
}

.detail-block--error pre {
  border-color: var(--color-danger, #d33);
  color: var(--color-danger, #d33);
}

.tag-execution-succeeded { background: var(--color-success, #2c8a3e); color: #fff; }
.tag-execution-failed { background: var(--color-danger, #d33); color: #fff; }
.tag-execution-denied { background: var(--color-warning, #b8860b); color: #fff; }
</style>
