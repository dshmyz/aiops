<script setup lang="ts">
import { computed, ref } from 'vue';
import type { ScheduledTaskRun } from '../types';

const props = defineProps<{
  runs: ScheduledTaskRun[];
}>();

// 展开的 run id，null 表示无展开。
const expandedId = ref<string | null>(null);

function toggleExpand(runId: string) {
  expandedId.value = expandedId.value === runId ? null : runId;
}

// 计算耗时（毫秒），用于展示。finished_at - started_at
function elapsedMs(run: ScheduledTaskRun): number {
  const started = Date.parse(run.started_at);
  const finished = Date.parse(run.finished_at);
  if (Number.isNaN(started) || Number.isNaN(finished)) return 0;
  return Math.max(0, finished - started);
}

// 友好的耗时展示：毫秒数 → "Xs" 或 "Xms"
function formatElapsed(run: ScheduledTaskRun): string {
  const ms = elapsedMs(run);
  if (ms < 1000) return `${ms}ms`;
  return `${Math.round(ms / 100) / 10}s`;
}

// result_data 的 JSON 字符串展示，null 时给出提示。
function resultDataText(run: ScheduledTaskRun): string {
  if (run.result_data === null) return '无详细数据';
  try {
    return JSON.stringify(run.result_data, null, 2);
  } catch {
    return '无详细数据';
  }
}

const rows = computed(() =>
  props.runs.map((run) => ({
    run,
    elapsed: formatElapsed(run),
    expanded: expandedId.value === run.id,
    resultText: resultDataText(run),
  })),
);
</script>

<template>
  <section data-test="scheduled-task-run-history" class="scheduled-task-run-history">
    <div
      v-if="runs.length === 0"
      data-test="scheduled-task-empty"
      class="scheduled-task-empty"
    >
      暂无执行记录
    </div>
    <ul v-else class="run-list">
      <li
        v-for="row in rows"
        :key="row.run.id"
        data-test="scheduled-task-run-row"
        :data-run-id="row.run.id"
        class="run-row"
        :class="{ 'run-failed': row.run.status === 'failed' }"
        @click="toggleExpand(row.run.id)"
      >
        <div class="run-summary">
          <span class="run-started">{{ row.run.started_at }}</span>
          <span class="run-status" :class="`status-${row.run.status}`">{{ row.run.status }}</span>
          <span class="run-elapsed">{{ row.elapsed }}</span>
          <span class="run-summary-text">{{ row.run.result_summary || row.run.error || '—' }}</span>
        </div>
        <div
          v-if="row.expanded"
          data-test="scheduled-task-run-expand"
          class="run-expand"
          @click.stop
        >
          <pre class="run-result-data">{{ row.resultText }}</pre>
        </div>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.scheduled-task-run-history {
  width: 100%;
  overflow-x: auto;
}

.scheduled-task-empty {
  padding: var(--space-6) 0;
  color: var(--color-text-tertiary);
  text-align: center;
  font-size: var(--font-base);
}

.run-list {
  list-style: none;
  margin: 0;
  padding: 0;
}

.run-row {
  padding: var(--space-3);
  border-bottom: 1px solid var(--color-border);
  cursor: pointer;
  transition: background 0.15s;
}

.run-row:hover {
  background: var(--color-bg-hover);
}

.run-row.run-failed {
  background: var(--color-danger-soft);
}

.run-row.run-failed:hover {
  background: var(--color-danger-soft);
}

.run-summary {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-3);
  align-items: center;
  font-size: var(--font-base);
}

.run-started {
  color: var(--color-text-secondary);
  font-family: var(--font-mono);
  font-size: var(--font-md);
}

.run-status {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: var(--radius-pill);
  font-size: var(--font-sm);
  font-weight: 600;
  line-height: 1.6;
  white-space: nowrap;
}

.status-succeeded {
  background: var(--color-success-soft);
  color: var(--color-success);
}

.status-failed {
  background: var(--color-danger-soft);
  color: var(--color-danger);
}

.run-elapsed {
  color: var(--color-text-tertiary);
  font-family: var(--font-mono);
  font-size: var(--font-sm);
}

.run-summary-text {
  flex: 1;
  color: var(--color-text-primary);
  font-size: var(--font-base);
}

.run-expand {
  margin-top: var(--space-2);
  padding: var(--space-3);
  background: var(--color-bg-elevated);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
}

.run-result-data {
  margin: 0;
  color: var(--color-text-primary);
  font-family: var(--font-mono);
  font-size: var(--font-md);
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
