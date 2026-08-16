<script setup lang="ts">
import { reactive, ref, watch } from 'vue';
import type { AuditEvent, AuditEventCursor, AuditEventFilter } from '../types';
import { downloadAuditCSV } from '../auditExport';
import { formatCompactDateTime } from '../conversationFormat';
import AuditEventDetail from './AuditEventDetail.vue';

const props = withDefaults(
  defineProps<{
    events: AuditEvent[];
    loading: boolean;
    loadingMore?: boolean;
    error?: string;
    nextCursor?: AuditEventCursor | null;
    searchQuery?: string;
    // 借鉴-4: 事件中心"最终结果过滤"切换的初始状态。默认 true（仅最终结果），
    // 与文档"默认过滤未执行的驳回审批流"对齐。
    finalResultOnly?: boolean;
  }>(),
  {
    // 用 withDefaults 显式声明默认值：Vue 对 Boolean 类型 prop 做隐式转换，
    // 未传入时会变成 false 而非 undefined，导致 `?? true` 兜底失效。
    finalResultOnly: true,
  },
);

const emit = defineEmits<{
  refresh: [];
  filter: [filter: AuditEventFilter];
  loadMore: [];
  jumpToPlan: [planID: string];
  search: [query: string];
}>();

const localFilter = reactive({
  tool: '',
  action: '',
  decision: '',
  subject: '',
  after: '',
  before: '',
  limit: '',
});

const decisionOptions = ['permitted', 'denied'];
const selectedEventID = ref<string | undefined>(undefined);
const selectedEvent = ref<AuditEvent | null>(null);
const searchQuery = ref(props.searchQuery ?? '');
// 借鉴-4: 最终结果过滤开关。默认开启（仅最终结果），可切换为"显示全部"。
// 默认值由 withDefaults 提供，这里直接读取 prop。
const finalResultOnly = ref(props.finalResultOnly);

watch(
  () => props.searchQuery,
  (value) => {
    searchQuery.value = value ?? '';
  },
);

// buildFilter 把 localFilter + finalResultOnly 合并成一个 AuditEventFilter。
// applyFilter（点"应用"按钮）和 toggle 切换共用此函数，保证两端过滤条件一致。
function buildFilter(): AuditEventFilter {
  const filter: AuditEventFilter = {};
  if (localFilter.tool) filter.tool = localFilter.tool;
  if (localFilter.action) filter.action = localFilter.action;
  if (localFilter.decision) filter.decision = localFilter.decision;
  if (localFilter.subject) filter.subject = localFilter.subject;
  if (localFilter.after) filter.after = localFilter.after;
  if (localFilter.before) filter.before = localFilter.before;
  if (localFilter.limit) {
    const limit = Number(localFilter.limit);
    if (Number.isFinite(limit) && limit > 0) filter.limit = limit;
  }
  // 始终写入 final_result_only 的当前布尔值：true 表示仅最终结果，
  // false 表示显示全部。api.ts 仅在为 true 时才追加查询参数，故 false 不会
  // 产生副作用，但显式写入便于父组件区分"未设置"与"主动关闭"。
  filter.final_result_only = finalResultOnly.value;
  return filter;
}

function applyFilter() {
  emit('filter', buildFilter());
}

// 借鉴-4: toggle 即时切换视图模式，无需点"应用"按钮。切换时立即 emit filter。
watch(finalResultOnly, () => {
  emit('filter', buildFilter());
});

function selectEvent(event: AuditEvent) {
  selectedEventID.value = event.id;
  selectedEvent.value = event;
}

function closeDetail() {
  selectedEventID.value = undefined;
  selectedEvent.value = null;
}

function handleJumpToPlan(planID: string) {
  emit('jumpToPlan', planID);
}

function exportCSV() {
  if (props.events.length === 0) return;
  const stamp = new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-');
  downloadAuditCSV(props.events, `audit-events-${stamp}.csv`);
}

function submitSearch() {
  const query = searchQuery.value.trim();
  if (!query) return;
  emit('search', query);
}
</script>

<template>
  <section data-test="audit-log-view" class="audit-log-view">
    <header class="section-heading">
      <div>
        <h2>审计记录</h2>
        <span>{{ events.length }} 条事件</span>
      </div>
      <div class="audit-actions">
        <button class="mini-button" :disabled="loading" data-test="audit-refresh" @click="emit('refresh')">
          {{ loading ? '刷新中' : '刷新' }}
        </button>
        <button
          class="mini-button"
          :disabled="events.length === 0"
          data-test="audit-export-csv"
          @click="exportCSV"
        >
          导出 CSV
        </button>
      </div>
    </header>
    <div class="audit-search">
      <input
        v-model="searchQuery"
        class="audit-search-input"
        placeholder="自然语言搜索，如：上周谁拒绝了 plan"
        data-test="audit-search-query"
        @keyup.enter="submitSearch"
      />
      <button
        class="mini-button"
        :disabled="loading"
        data-test="audit-search-submit"
        @click="submitSearch"
      >
        {{ loading ? '搜索中' : '搜索' }}
      </button>
    </div>
    <div class="audit-filters">
      <input v-model="localFilter.tool" placeholder="按工具过滤" data-test="audit-filter-tool" />
      <input v-model="localFilter.action" placeholder="按操作过滤" data-test="audit-filter-action" />
      <select v-model="localFilter.decision" data-test="audit-filter-decision">
        <option value="">全部决策</option>
        <option v-for="option in decisionOptions" :key="option" :value="option">{{ option }}</option>
      </select>
      <input v-model="localFilter.subject" placeholder="按提交人过滤" data-test="audit-filter-subject" />
      <input v-model="localFilter.after" type="datetime-local" data-test="audit-filter-after" title="起始时间" />
      <input v-model="localFilter.before" type="datetime-local" data-test="audit-filter-before" title="结束时间" />
      <input v-model="localFilter.limit" type="number" min="1" placeholder="每页 N 条" data-test="audit-filter-limit" />
      <button class="mini-button" data-test="audit-filter-apply" @click="applyFilter">应用</button>
      <label class="final-result-toggle" title="开启后隐藏驳回/未执行事件，聚焦真正发生的结果">
        <input
          v-model="finalResultOnly"
          type="checkbox"
          data-test="audit-final-result-only"
        />
        <span>仅显示最终结果</span>
      </label>
    </div>
    <p v-if="error" class="error-text">{{ error }}</p>
    <div class="audit-workspace">
      <div class="audit-table-wrap">
        <table class="audit-table">
          <thead>
            <tr>
              <th>时间</th>
              <th>工具</th>
              <th>操作</th>
              <th>决策</th>
              <th>提交人</th>
              <th>Plan</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="event in props.events"
              :key="event.id"
              :data-test="`audit-row-${event.id}`"
              :class="{ active: event.id === selectedEventID }"
              @click="selectEvent(event)"
            >
              <td class="mono" :title="event.created_at">{{ formatCompactDateTime(event.created_at) }}</td>
              <td class="mono">{{ event.tool_name }}</td>
              <td>{{ event.action }}</td>
              <td :class="['decision', `decision-${event.decision}`]">{{ event.decision }}</td>
              <td>{{ event.subject }}</td>
              <td class="mono">{{ event.plan_id || '-' }}</td>
            </tr>
          </tbody>
        </table>
        <p v-if="events.length === 0" class="empty">暂无审计事件。</p>
        <div v-if="nextCursor" class="audit-load-more">
          <button
            class="mini-button"
            :disabled="loadingMore"
            data-test="audit-load-more"
            @click="emit('loadMore')"
          >
            {{ loadingMore ? '加载中…' : '加载更多' }}
          </button>
        </div>
      </div>
      <AuditEventDetail
        :event="selectedEvent"
        @close="closeDetail"
        @jump-to-plan="handleJumpToPlan"
      />
    </div>
  </section>
</template>

<style scoped>
.audit-log-view {
  display: flex;
  flex-direction: column;
  gap: var(--space-3, 0.75rem);
  padding: 0;
  flex: 1;
  min-height: 0;
}

.section-heading {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.section-heading .audit-actions {
  display: flex;
  gap: 0.5rem;
}

.audit-filters {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: var(--space-2);
  align-items: center;
  padding: var(--space-3);
  background: var(--color-bg-elevated);
  border: none;
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-sm);
}

.audit-search {
  display: flex;
  gap: var(--space-2);
  align-items: center;
}

.audit-search-input {
  flex: 1;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-bg);
  color: var(--color-text-primary);
  font-size: var(--font-base);
  outline: none;
  transition: border-color 0.15s;
}

.audit-search-input:focus {
  border-color: var(--color-accent);
}

.audit-filters input,
.audit-filters select {
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-bg);
  color: var(--color-text-primary);
  font-size: var(--font-sm);
  outline: none;
  transition: border-color 0.15s;
}

.audit-filters input:focus,
.audit-filters select:focus {
  border-color: var(--color-accent);
}

/* 借鉴-4: 最终结果过滤 toggle。横跨整行，避免在 4 列网格里被挤压。 */
.final-result-toggle {
  grid-column: 1 / -1;
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--font-sm);
  color: var(--color-text-secondary);
  cursor: pointer;
  user-select: none;
}

.final-result-toggle input[type='checkbox'] {
  width: 1rem;
  height: 1rem;
  accent-color: var(--color-accent);
  cursor: pointer;
}

.audit-workspace {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(280px, 320px);
  gap: var(--space-4);
  align-items: start;
  flex: 1;
  min-height: 0;
}

@media (max-width: 1100px) {
  .audit-workspace {
    grid-template-columns: 1fr;
  }
  .audit-filters {
    grid-template-columns: repeat(2, 1fr);
  }
}

.audit-table-wrap {
  overflow-x: auto;
  border: none;
  border-radius: var(--radius-xl);
  background: var(--color-bg-elevated);
  box-shadow: var(--shadow-sm);
}

.audit-table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--font-md);
  cursor: pointer;
  table-layout: fixed;
}

.audit-table thead th {
  text-align: left;
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--color-border);
  color: var(--color-text-tertiary);
  font-weight: 600;
  font-size: var(--font-xs);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

/* Column width allocation: time/tool/subject need more space, action/decision less */
.audit-table th:nth-child(1), .audit-table td:nth-child(1) { width: 22%; }
.audit-table th:nth-child(2), .audit-table td:nth-child(2) { width: 20%; }
.audit-table th:nth-child(3), .audit-table td:nth-child(3) { width: 12%; }
.audit-table th:nth-child(4), .audit-table td:nth-child(4) { width: 12%; }
.audit-table th:nth-child(5), .audit-table td:nth-child(5) { width: 18%; }
.audit-table th:nth-child(6), .audit-table td:nth-child(6) { width: 16%; }

.audit-table tbody td {
  padding: var(--space-2) var(--space-4);
  border-bottom: 1px solid var(--color-border);
  color: var(--color-text-primary);
  vertical-align: top;
  /* Allow long tool names and plan IDs to wrap instead of being clipped,
     so the audit table is fully readable on narrow viewports. */
  word-break: break-word;
  overflow-wrap: anywhere;
}

.audit-table tbody tr {
  transition: background 0.15s var(--ease-out);
}

.audit-table tbody tr:hover {
  background: var(--color-bg-hover);
}

.audit-table tbody tr.active {
  background: var(--color-bg-active);
}

.audit-table tbody tr:last-child td {
  border-bottom: none;
}

.mono {
  font-family: var(--font-mono, monospace);
  font-size: 0.8rem;
  word-break: break-all;
}

.decision-permitted {
  color: var(--color-success, #2c8a3e);
}

.decision-denied {
  color: var(--color-danger, #d33);
}

.empty {
  padding: 1.5rem;
  text-align: center;
  color: var(--color-text-muted);
  font-size: 0.85rem;
  margin: 0;
}

.audit-load-more {
  display: flex;
  justify-content: center;
  padding: 0.75rem;
  border-top: 1px solid var(--color-border);
}

.error-text {
  margin: 0;
  font-size: 0.75rem;
  color: var(--color-danger, #d33);
}
</style>
