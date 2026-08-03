<script setup lang="ts">
import { computed } from 'vue';
import type { SchedulePreset, ScheduledTask } from '../types';

const props = defineProps<{
  tasks: ScheduledTask[];
}>();

const emit = defineEmits<{
  (event: 'toggle-enabled', id: string, enabled: boolean): void;
  (event: 'trigger', id: string): void;
  (event: 'edit', task: ScheduledTask): void;
  (event: 'delete', id: string): void;
}>();

// preset 模式给出中文标签；cron 模式直接显示表达式。
const presetLabels: Record<SchedulePreset, string> = {
  '5m': '每 5 分钟',
  '1h': '每小时',
  daily: '每天 00:00',
  weekly: '每周一 00:00',
};

function scheduleDescription(task: ScheduledTask): string {
  if (task.schedule_kind === 'preset') {
    return task.preset ? presetLabels[task.preset] : '未设置';
  }
  return task.cron_expr ?? '未设置';
}

// 表格行数据，便于模板渲染。
const rows = computed(() =>
  props.tasks.map((task) => ({
    task,
    schedule: scheduleDescription(task),
  })),
);

function onToggle(task: ScheduledTask, event: Event) {
  const checked = (event.target as HTMLInputElement).checked;
  emit('toggle-enabled', task.id, checked);
}

function onTrigger(task: ScheduledTask) {
  emit('trigger', task.id);
}

function onEdit(task: ScheduledTask) {
  emit('edit', task);
}

function onDelete(task: ScheduledTask) {
  emit('delete', task.id);
}
</script>

<template>
  <section data-test="scheduled-task-list" class="scheduled-task-list">
    <div
      v-if="tasks.length === 0"
      data-test="scheduled-task-empty"
      class="scheduled-task-empty"
    >
      暂无定时任务
    </div>
    <table v-else class="scheduled-task-table">
      <thead>
        <tr>
          <th>名称</th>
          <th>能力</th>
          <th>调度</th>
          <th>下次执行</th>
          <th>上次状态</th>
          <th>启用</th>
          <th>操作</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="row in rows"
          :key="row.task.id"
          data-test="scheduled-task-row"
          :data-task-id="row.task.id"
        >
          <td class="cell-name">{{ row.task.name }}</td>
          <td class="cell-capability">{{ row.task.capability_name }}</td>
          <td class="cell-schedule">{{ row.schedule }}</td>
          <td class="cell-next">{{ row.task.next_run_at }}</td>
          <td class="cell-status">
            <span
              data-test="scheduled-task-status"
              class="task-status"
              :class="{
                'status-succeeded': row.task.last_status === 'succeeded',
                'status-failed': row.task.last_status === 'failed',
                'status-empty': row.task.last_status === '',
              }"
            >
              {{ row.task.last_status || '—' }}
            </span>
          </td>
          <td class="cell-enabled">
            <label class="toggle-wrap">
              <input
                data-test="scheduled-task-toggle"
                type="checkbox"
                :checked="row.task.enabled"
                @change="onToggle(row.task, $event)"
              />
            </label>
          </td>
          <td class="cell-actions">
            <button
              data-test="scheduled-task-trigger"
              class="row-action"
              type="button"
              @click="onTrigger(row.task)"
            >
              立即运行
            </button>
            <button
              data-test="scheduled-task-edit"
              class="row-action"
              type="button"
              @click="onEdit(row.task)"
            >
              编辑
            </button>
            <button
              data-test="scheduled-task-delete"
              class="row-action danger"
              type="button"
              @click="onDelete(row.task)"
            >
              删除
            </button>
          </td>
        </tr>
      </tbody>
    </table>
  </section>
</template>

<style scoped>
.scheduled-task-list {
  width: 100%;
  overflow-x: auto;
  padding: 0 var(--space-6, 1.5rem) var(--space-6, 1.5rem);
}

.scheduled-task-empty {
  padding: var(--space-8) var(--space-4);
  color: var(--color-text-tertiary);
  text-align: center;
  font-size: var(--font-base);
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-2);
  background: var(--color-bg-elevated);
  border: none;
  border-radius: var(--radius-xl);
  margin: var(--space-4) 0;
  box-shadow: var(--shadow-sm);
}

.scheduled-task-empty::before {
  content: "";
  display: block;
  width: 40px;
  height: 40px;
  border-radius: 50%;
  background: var(--color-bg-hover);
  margin-bottom: var(--space-2);
}

.scheduled-task-table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--font-base);
  background: var(--color-bg-elevated);
  border: none;
  border-radius: var(--radius-xl);
  overflow: hidden;
  table-layout: fixed;
  box-shadow: var(--shadow-sm);
}

.scheduled-task-table th,
.scheduled-task-table td {
  padding: var(--space-3);
  text-align: left;
  border-bottom: 1px solid var(--color-border);
  vertical-align: middle;
}

.scheduled-task-table th {
  color: var(--color-text-secondary);
  font-size: var(--font-sm);
  font-weight: 600;
  background: var(--color-bg-hover);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

/* Column widths: name/capability wider, status/actions narrower */
.scheduled-task-table th:nth-child(1), .scheduled-task-table td:nth-child(1) { width: 18%; }
.scheduled-task-table th:nth-child(2), .scheduled-task-table td:nth-child(2) { width: 20%; }
.scheduled-task-table th:nth-child(3), .scheduled-task-table td:nth-child(3) { width: 14%; }
.scheduled-task-table th:nth-child(4), .scheduled-task-table td:nth-child(4) { width: 16%; }
.scheduled-task-table th:nth-child(5), .scheduled-task-table td:nth-child(5) { width: 10%; }
.scheduled-task-table th:nth-child(6), .scheduled-task-table td:nth-child(6) { width: 8%; }
.scheduled-task-table th:nth-child(7), .scheduled-task-table td:nth-child(7) { width: 14%; }

.scheduled-task-table tbody td {
  /* Allow action button columns to wrap to a second line on narrow
     viewports instead of clipping the buttons. Long schedule/cron
     expressions and timestamps still benefit from word-break. */
  word-break: break-word;
  overflow-wrap: anywhere;
}

/* Preserve ellipsis for monospace data columns that should not wrap. */
.scheduled-task-table tbody td.cell-name,
.scheduled-task-table tbody td.cell-capability,
.scheduled-task-table tbody td.cell-schedule,
.scheduled-task-table tbody td.cell-next {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.scheduled-task-table tbody tr:last-child td {
  border-bottom: none;
}

.cell-name {
  color: var(--color-text-primary);
  font-weight: 600;
}

.cell-capability,
.cell-schedule,
.cell-next {
  color: var(--color-text-secondary);
  font-family: var(--font-mono);
  font-size: var(--font-md);
}

.task-status {
  display: inline-block;
  padding: 2px 8px;
  border-radius: var(--radius-pill);
  font-size: var(--font-sm);
  font-weight: 600;
}

.status-succeeded {
  background: var(--color-success-soft);
  color: var(--color-success);
}

.status-failed {
  background: var(--color-danger-soft);
  color: var(--color-danger);
}

.status-empty {
  background: var(--color-bg-hover);
  color: var(--color-text-tertiary);
}

.toggle-wrap {
  display: inline-flex;
  cursor: pointer;
}

.cell-actions {
  display: flex;
  gap: var(--space-2);
  flex-wrap: wrap;
}

/* row-action mirrors the global .mini-button visual language so every
   in-row action looks consistent across views. */
.row-action {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 5px 10px;
  background: var(--color-bg-hover);
  color: var(--color-text-secondary);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  font-size: var(--font-sm);
  font-weight: 500;
  line-height: 1.2;
  cursor: pointer;
  transition: all 0.15s ease;
  white-space: nowrap;
  font-family: inherit;
}

.row-action:hover:not(:disabled) {
  border-color: var(--color-border-strong);
  color: var(--color-text-primary);
  background: var(--color-bg-elevated);
}

.row-action:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.row-action.danger:hover:not(:disabled) {
  border-color: var(--color-danger);
  color: var(--color-danger);
  background: var(--color-danger-soft);
}
</style>
