<script setup lang="ts">
import { computed } from 'vue';
import DOMPurify from 'dompurify';
import type { InspectionReport } from '../types';
import { useInspectionReports } from '../composables/useInspectionReports';

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

const selectedHTML = computed(() => {
  if (!selectedReport.value?.html_content) return '';
  return DOMPurify.sanitize(selectedReport.value.html_content);
});

function isSelected(report: InspectionReport): boolean {
  return selectedReport.value?.id === report.id;
}
</script>

<template>
  <section data-test="inspection-entry" data-view="inspection-reports" class="inspection-entry">
    <header class="topbar">
      <div>
        <p class="eyebrow">Inspection Reports</p>
        <h1>巡检报告</h1>
        <p class="topbar-copy">查看 by Reporter 按时间窗口聚合生成的巡检报告，展示每个定时任务在窗口内的执行统计与 HTML 详情。</p>
      </div>
      <div class="actions">
        <button class="mini-button" :disabled="reportsLoading" data-test="inspection-refresh" @click="refresh">
          {{ reportsLoading ? '刷新中' : '刷新' }}
        </button>
      </div>
    </header>

    <p v-if="reportsError" class="error-text">{{ reportsError }}</p>

    <div class="inspection-workspace">
      <div class="inspection-list">
        <div v-if="!reportsLoading && reports.length === 0" class="empty">暂无巡检报告。</div>
        <div
          v-for="report in reports"
          :key="report.id"
          class="inspection-card"
          :class="{ active: isSelected(report) }"
          :data-test="`inspection-report-${report.id}`"
          @click="select(report)"
        >
          <header class="inspection-card-header">
            <span class="badge">{{ labelForPeriod(report.period) }}</span>
            <span class="inspection-window mono">{{ report.window_start }} → {{ report.window_end }}</span>
          </header>
          <div class="inspection-stats">
            <span class="stat stat--ok">{{ report.succeeded_tasks }}/{{ report.total_tasks }} 成功</span>
            <span v-if="report.failed_tasks > 0" class="stat stat--fail">{{ report.failed_tasks }} 失败</span>
          </div>
          <p class="inspection-generated mono">{{ report.generated_at }}</p>
        </div>
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
            <dl>
              <div><dt>生成时间</dt><dd class="mono">{{ selectedReport.generated_at }}</dd></div>
              <div><dt>窗口</dt><dd class="mono">{{ selectedReport.window_start }} → {{ selectedReport.window_end }}</dd></div>
              <div><dt>任务数</dt><dd>{{ selectedReport.total_tasks }}</dd></div>
              <div><dt>成功 / 失败</dt><dd>{{ selectedReport.succeeded_tasks }} / {{ selectedReport.failed_tasks }}</dd></div>
            </dl>
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
  </section>
</template>

<style scoped>
.inspection-entry {
  display: flex;
  flex-direction: column;
  gap: var(--space-3, 0.75rem);
  padding: 0 var(--space-6, 1.5rem) var(--space-6, 1.5rem);
  flex: 1;
  min-height: 0;
}

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

.inspection-card {
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border);
  background: var(--color-bg-elevated);
  cursor: pointer;
  transition: background 0.15s var(--ease-out), border-color 0.15s var(--ease-out);
}

.inspection-card:hover {
  background: var(--color-bg-hover);
}

.inspection-card.active {
  border-color: var(--color-accent);
  background: var(--color-bg-active);
}

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

.stat--ok { color: var(--color-success, #2c8a3e); }
.stat--fail { color: var(--color-danger, #d33); }

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
  margin: 0;
  display: grid;
  grid-template-columns: 1fr;
  gap: 6px;
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
