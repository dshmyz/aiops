<script setup lang="ts">
import { computed, ref } from 'vue';
import DOMPurify from 'dompurify';
import type { InspectionReport } from '../types';
import { useInspectionReports } from '../composables/useInspectionReports';
import { formatCompactDateTime } from '../conversationFormat';
import ViewShell from '../components/ViewShell.vue';

const {
  reports,
  reportsLoading,
  reportsError,
  selectedReport,
  detailLoading,
  detailError,
  refresh,
  select,
  clearSelection,
} = useInspectionReports();

void refresh();

const periodLabel: Record<string, string> = {
  daily: '每日',
  weekly: '每周',
};

function labelForPeriod(period: string): string {
  return periodLabel[period] ?? period;
}

// —— 筛选项：列表卡片按是否含失败任务过滤 ——
type FilterMode = 'all' | 'ok' | 'fail';
const filterMode = ref<FilterMode>('all');

const filters: { value: FilterMode; label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'ok', label: '全部成功' },
  { value: 'fail', label: '含失败' },
];

function hasFailures(report: InspectionReport): boolean {
  return report.failed_tasks > 0;
}

const filteredReports = computed(() => {
  if (filterMode.value === 'all') return reports.value;
  return reports.value.filter((r) =>
    filterMode.value === 'fail' ? hasFailures(r) : !hasFailures(r),
  );
});

const failCount = computed(() => reports.value.filter(hasFailures).length);

// 最新一份报告的健康状态，用于列表顶部的总览。后端已按 generated_at 降序返回，
// 故取数组首元素即为最新；避免对 RFC3339Nano 时间戳做字典序比较（小数秒被省略/去尾零
// 会导致同秒内字符串比较误序）。
const latestReport = computed<InspectionReport | null>(() => reports.value[0] ?? null);

function successRate(report: InspectionReport): number {
  if (report.total_tasks <= 0) return 0;
  return Math.round((report.succeeded_tasks / report.total_tasks) * 100);
}

function failedTasks(report: InspectionReport) {
  return (report.task_summaries ?? []).filter((s) => s.failed_runs > 0);
}

// 健康档位：全部成功 -> ok；有失败但成功率>0 -> warn；全失败 -> danger。
function healthTone(report: InspectionReport): 'ok' | 'warn' | 'danger' {
  if (report.failed_tasks === 0) return 'ok';
  return report.succeeded_tasks > 0 ? 'warn' : 'danger';
}

const healthLabel: Record<string, string> = {
  ok: '正常',
  warn: '部分失败',
  danger: '全部失败',
};

const selectedHTML = computed(() => {
  if (!selectedReport.value?.html_content) return '';
  return DOMPurify.sanitize(selectedReport.value.html_content);
});

function isSelected(report: InspectionReport): boolean {
  return selectedReport.value?.id === report.id;
}

// 把后端的 UTC ISO 时间戳转成本地时间的紧凑展示，避免卡片/详情露出原始 UTC 串。
// 复用共享助手 formatCompactDateTime（含非法输入兜底），再裁掉其年份前缀得到 MM-DD HH:mm。
// 例：2026-08-05T00:00:00Z → 08-05 08:00
function fmtTs(ts: string | null | undefined): string {
  if (!ts) return '';
  // formatCompactDateTime 输出 YYYY-MM-DD HH:mm；slice(5) 去掉 "YYYY-" 保留 MM-DD HH:mm。
  return formatCompactDateTime(ts, false).slice(5);
}

// 时间窗口展示：两侧各自呈现代本地时间 MM-DD HH:mm，形如 "08-05 08:00 → 08-06 07:59"。
function fmtWindow(start: string, end: string): string {
  return `${fmtTs(start)} → ${fmtTs(end)}`;
}
</script>

<template>
  <ViewShell
    class="inspection-entry"
    data-test="inspection-entry"
    data-view="inspection-reports"
    eyebrow="Inspection Reports"
    title="巡检报告"
    copy="查看 by Reporter 按时间窗口聚合生成的巡检报告，展示每个定时任务在窗口内的执行统计与 HTML 详情。"
  >
    <template #actions>
      <button class="mini-button" :disabled="reportsLoading" data-test="inspection-refresh" @click="refresh">
        {{ reportsLoading ? '刷新中' : '刷新' }}
      </button>
    </template>

    <p v-if="reportsError" class="error-text">{{ reportsError }}</p>

    <div class="inspection-workspace">
      <div class="inspection-list">
        <div v-if="reportsLoading" class="list-summary">
          <div class="skeleton skeleton-line"></div>
          <div class="skeleton skeleton-line short"></div>
        </div>
        <div v-else-if="reports.length === 0 && !reportsError" class="empty">暂无巡检报告。</div>
        <div v-else-if="reports.length > 0" class="list-summary">
          <div class="summary-head">
            <strong class="summary-title">共 {{ reports.length }} 份报告</strong>
            <template v-if="latestReport">
              <span
                class="health-chip"
                :class="`health-${healthTone(latestReport)}`"
                data-test="inspection-latest-health"
              >
                {{ healthLabel[healthTone(latestReport)] }}
              </span>
            </template>
          </div>
          <div class="health-strip">
            <span
              class="health-seg health-seg--ok"
              :style="{ flexGrow: latestReport?.succeeded_tasks ?? 0 }"
            ></span>
            <span
              class="health-seg health-seg--fail"
              :style="{ flexGrow: latestReport?.failed_tasks ?? 0 }"
            ></span>
          </div>
          <p v-if="latestReport" class="summary-sub mono">
            最新 {{ labelForPeriod(latestReport.period) }} · {{ latestReport.succeeded_tasks }}/{{
              latestReport.total_tasks
            }} 成功
            <span v-if="failCount > 0"> · {{ failCount }} 份含失败</span>
          </p>
        </div>

        <div v-if="reports.length > 0" class="filter-chips" role="group" aria-label="按健康状态筛选">
          <button
            v-for="f in filters"
            :key="f.value"
            type="button"
            class="filter-chip"
            :class="{ active: filterMode === f.value }"
            :data-test="`inspection-filter-${f.value}`"
            @click="filterMode = f.value"
          >
            {{ f.label }}
          </button>
        </div>

        <div
          v-for="report in filteredReports"
          :key="report.id"
          class="inspection-card"
          :class="{
            active: isSelected(report),
            'has-failures': hasFailures(report),
          }"
          :data-test="`inspection-report-${report.id}`"
          @click="select(report)"
        >
          <header class="inspection-card-header">
            <span class="badge">{{ labelForPeriod(report.period) }}</span>
            <span class="inspection-window mono" :title="`${report.window_start} → ${report.window_end}`">{{ fmtWindow(report.window_start, report.window_end) }}</span>
          </header>
          <div class="inspection-stats">
            <span class="stat stat--ok">{{ report.succeeded_tasks }}/{{ report.total_tasks }} 成功</span>
            <span v-if="hasFailures(report)" class="stat stat--fail">{{ report.failed_tasks }} 失败</span>
            <span v-else class="stat stat--all-ok">全部通过</span>
          </div>
          <div class="rate-track" aria-hidden="true">
            <div
              class="rate-fill"
              :class="`rate-${healthTone(report)}`"
              :style="{ width: `${successRate(report)}%` }"
            ></div>
          </div>
          <p class="inspection-generated mono" :title="report.generated_at">{{ fmtTs(report.generated_at) }}</p>
        </div>
        <p v-if="!reportsLoading && filteredReports.length === 0 && reports.length > 0" class="empty">
          没有符合条件的报告。
        </p>
      </div>

      <aside class="inspection-detail">
        <div v-if="!selectedReport" class="empty">点击左侧某份报告查看详情。</div>
        <div v-else-if="detailError" class="error-text">{{ detailError }}</div>
        <div v-else class="inspection-detail-body">
          <header class="detail-title">
            <h3>{{ labelForPeriod(selectedReport.period) }}巡检报告</h3>
            <button class="mini-button" data-test="inspection-close-detail" @click="clearSelection">关闭</button>
          </header>
          <section class="detail-summary">
            <div class="detail-summary-head">
              <span
                class="health-chip"
                :class="`health-${healthTone(selectedReport)}`"
                data-test="inspection-detail-health"
              >
                {{ healthLabel[healthTone(selectedReport)] }}
              </span>
              <div class="rate-track rate-track--lg" aria-hidden="true">
                <div
                  class="rate-fill"
                  :class="`rate-${healthTone(selectedReport)}`"
                  :style="{ width: `${successRate(selectedReport)}%` }"
                ></div>
              </div>
              <span class="rate-pct">{{ successRate(selectedReport) }}%</span>
            </div>
            <dl>
              <div><dt>生成时间</dt><dd class="mono" :title="selectedReport.generated_at">{{ fmtTs(selectedReport.generated_at) }}</dd></div>
              <div><dt>窗口</dt><dd class="mono" :title="`${selectedReport.window_start} → ${selectedReport.window_end}`">{{ fmtWindow(selectedReport.window_start, selectedReport.window_end) }}</dd></div>
              <div><dt>任务数</dt><dd>{{ selectedReport.total_tasks }}</dd></div>
              <div><dt>成功 / 失败</dt><dd>{{ selectedReport.succeeded_tasks }} / {{ selectedReport.failed_tasks }}</dd></div>
            </dl>
          </section>

          <section v-if="failedTasks(selectedReport).length > 0" class="detail-block detail-block--danger" data-test="inspection-detail-failures">
            <h4>最近的失败任务（{{ failedTasks(selectedReport).length }}）</h4>
            <ul class="fail-list">
              <li v-for="task in failedTasks(selectedReport)" :key="task.task_id">
                <code class="mono">{{ task.task_name }}</code>
                <span class="fail-count">{{ task.failed_runs }} 次失败</span>
                <span v-if="task.last_error" class="fail-err" :title="task.last_error">{{ task.last_error }}</span>
              </li>
            </ul>
          </section>

          <section v-if="selectedReport.task_summaries && selectedReport.task_summaries.length > 0" class="detail-block">
            <h4>任务执行明细</h4>
            <table class="summary-table">
              <thead>
                <tr>
                  <th>任务</th>
                  <th>成功</th>
                  <th>失败</th>
                  <th>最近状态</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="summary in selectedReport.task_summaries" :key="summary.task_id">
                  <td class="mono">{{ summary.task_name }}</td>
                  <td>{{ summary.succeeded_runs }}</td>
                  <td>{{ summary.failed_runs }}</td>
                  <td>{{ summary.last_status || '-' }}</td>
                </tr>
              </tbody>
            </table>
          </section>

          <section v-if="selectedReport.html_content" class="detail-block" data-test="inspection-html">
            <h4>报告正文</h4>
            <div class="inspection-html" v-html="selectedHTML"></div>
          </section>
        </div>
      </aside>
    </div>
  </ViewShell>
</template>

<style scoped>
.inspection-workspace {
  display: grid;
  grid-template-columns: minmax(280px, 380px) minmax(0, 1fr);
  gap: var(--space-4);
  align-items: start;
  flex: 1;
  min-height: 0;
}

@media (max-width: 1100px) {
  .inspection-workspace {
    grid-template-columns: 1fr;
  }
}

.inspection-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  overflow-y: auto;
  max-height: calc(100vh - 260px);
}

/* —— 列表顶部聚合概览 + 筛选 —— */
.list-summary {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border);
  background: var(--color-bg-elevated);
}
.summary-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}
.summary-title { font-size: var(--font-md); color: var(--color-text-primary); }
.summary-sub { margin: 0; font-size: var(--font-xs); color: var(--color-text-tertiary); }
.health-chip {
  padding: 2px 10px;
  border-radius: var(--radius-pill);
  font-size: var(--font-xs);
  font-weight: 600;
  white-space: nowrap;
}
.health-ok { background: var(--color-success-soft); color: var(--color-success); }
.health-warn { background: var(--color-warning-soft); color: var(--color-warning); }
.health-danger { background: var(--color-danger-soft); color: var(--color-danger); }

.health-strip {
  display: flex;
  gap: 2px;
  height: 6px;
  border-radius: var(--radius-pill);
  overflow: hidden;
}
.health-seg--ok { background: var(--color-success); flex-grow: 0; flex-basis: 0; }
.health-seg--fail { background: var(--color-danger); flex-grow: 0; flex-basis: 0; }

.filter-chips {
  display: flex;
  gap: var(--space-2);
  padding: 0 2px;
}
.filter-chip {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-pill);
  background: var(--color-bg-elevated);
  color: var(--color-text-secondary);
  font-size: var(--font-xs);
  font-weight: 600;
  padding: 2px 12px;
  cursor: pointer;
  transition: border-color 0.15s var(--ease-out), color 0.15s var(--ease-out), background 0.15s var(--ease-out);
}
.filter-chip:hover { border-color: var(--color-accent); color: var(--color-accent); }
.filter-chip.active { background: var(--color-accent); border-color: var(--color-accent); color: var(--color-bg); }

/* —— 卡片健康标记与成功率条 —— */
/* 列表卡片基础样式：与右侧详情面板同款「卡片」观感，让内容有呼吸空间。 */
.inspection-card {
  padding: var(--space-4);
  background: var(--color-bg-elevated);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-sm);
  cursor: pointer;
  transition: border-color 0.15s var(--ease-out), background 0.15s var(--ease-out);
}
.inspection-card.has-failures {
  border-left: 3px solid var(--color-danger);
}
.inspection-card:hover { background: var(--color-bg-hover); }
.inspection-card.active {
  border-color: var(--color-accent);
  background: var(--color-bg-active);
}

.rate-track {
  height: 4px;
  margin-top: 8px;
  border-radius: var(--radius-pill);
  background: var(--color-bg-hover);
  overflow: hidden;
}
.rate-track--lg { flex: 1; height: 8px; margin: 0; }
.rate-fill { height: 100%; border-radius: var(--radius-pill); transition: width 0.3s var(--ease-out); }
.rate-ok { background: var(--color-success); }
.rate-warn { background: var(--color-warning); }
.rate-danger { background: var(--color-danger); }

.stat--all-ok { color: var(--color-success); }

/* 加载骨架 */
.skeleton {
  border-radius: var(--radius-md);
  background: linear-gradient(90deg, var(--color-bg-active) 25%, var(--color-bg-hover) 50%, var(--color-bg-active) 75%);
  background-size: 200% 100%;
  animation: skeleton-shimmer 1.2s ease-in-out infinite;
}
.skeleton-line { height: 14px; }
.skeleton-line.short { width: 60%; }
@keyframes skeleton-shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

.inspection-card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  margin-bottom: 6px;
}

.inspection-window {
  font-size: var(--font-xs);
}

.inspection-stats {
  display: flex;
  gap: var(--space-3);
  font-size: var(--font-sm);
}

.stat--ok { color: var(--color-success); }
.stat--fail { color: var(--color-danger); }

.inspection-generated {
  margin: 6px 0 0;
  font-size: var(--font-xs);
  color: var(--color-text-tertiary);
}

.inspection-detail {
  border: none;
  border-radius: var(--radius-xl);
  background: var(--color-bg-elevated);
  box-shadow: var(--shadow-sm);
  padding: var(--space-4);
  overflow-y: auto;
  max-height: calc(100vh - 260px);
}

.detail-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-3);
}

.detail-title h3 {
  margin: 0;
  font-size: var(--font-lg);
  color: var(--color-text-primary);
}

.detail-summary dl {
  margin: var(--space-3) 0 0;
  display: grid;
  grid-template-columns: 1fr;
  gap: 6px;
}

.detail-summary-head {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}
.rate-pct {
  font-size: var(--font-sm);
  font-weight: 600;
  color: var(--color-text-secondary);
  min-width: 40px;
  text-align: right;
}

.fail-list {
  list-style: none;
  margin: 6px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.fail-list li {
  display: flex;
  align-items: baseline;
  gap: var(--space-2);
  flex-wrap: wrap;
  padding: 6px 10px;
  border-radius: var(--radius-md);
  background: var(--color-bg);
  border: 1px solid var(--color-border);
  font-size: var(--font-sm);
}
.fail-count { font-size: var(--font-xs); font-weight: 600; color: var(--color-danger); white-space: nowrap; }
.fail-err {
  flex: 1 1 100%;
  font-size: var(--font-xs);
  color: var(--color-text-tertiary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.detail-summary dl > div {
  display: flex;
  gap: 6px;
}

.detail-summary dt {
  color: var(--color-text-tertiary);
  font-size: var(--font-sm);
  min-width: 90px;
}

.detail-summary dd {
  margin: 0;
  font-size: var(--font-md);
  color: var(--color-text-primary);
  word-break: break-all;
}

.detail-block {
  margin-top: var(--space-4);
}

.detail-block h4 {
  margin: 0 0 6px;
  font-size: var(--font-base);
  color: var(--color-text-primary);
}

/* 放在 .detail-block h4 之后，同特异性下后者胜出，确保危险区块标题用主题红。 */
.detail-block--danger h4 { color: var(--color-danger); }

.summary-table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--font-sm);
}

.summary-table th,
.summary-table td {
  text-align: left;
  padding: 6px var(--space-2);
  border-bottom: 1px solid var(--color-border);
}

.summary-table thead th {
  color: var(--color-text-tertiary);
  font-weight: 600;
  font-size: var(--font-xs);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.inspection-html {
  padding: var(--space-3);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-bg);
  font-size: var(--font-md);
  line-height: 1.6;
}

.mono {
  font-family: var(--font-mono, monospace);
  font-size: 0.8rem;
  word-break: break-all;
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
</style>
