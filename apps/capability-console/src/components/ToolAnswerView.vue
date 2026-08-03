<script setup lang="ts">
import { computed } from 'vue';

const props = defineProps<{
  tool: string;
  answer: Record<string, unknown>;
}>();

// event.query: answer = {events: [...], count}
const events = computed<Array<Record<string, unknown>>>(() =>
  Array.isArray(props.answer.events) ? (props.answer.events as Array<Record<string, unknown>>) : [],
);
// task.query: answer = {tasks: [...], count}
const tasks = computed<Array<Record<string, unknown>>>(() =>
  Array.isArray(props.answer.tasks) ? (props.answer.tasks as Array<Record<string, unknown>>) : [],
);
const count = computed<number>(() => {
  const raw = props.answer.count;
  return typeof raw === 'number' ? raw : 0;
});

const isEventQuery = computed(() => props.tool === 'event.query');
const isTaskQuery = computed(() => props.tool === 'task.query');
</script>

<template>
  <div v-if="isEventQuery || isTaskQuery" class="tool-answer-view" data-test="tool-answer-view">
    <div class="tool-answer-count" data-test="tool-answer-count">
      {{ isEventQuery ? '事件' : '任务' }}数量：{{ count }}
    </div>

    <table v-if="isEventQuery" class="tool-answer-table" data-test="tool-answer-table">
      <thead>
        <tr>
          <th>ID</th>
          <th>工具</th>
          <th>动作</th>
          <th>决策</th>
          <th>主体</th>
          <th>时间</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="event in events" :key="String(event.id)" data-test="tool-answer-row">
          <td>{{ event.id }}</td>
          <td>{{ event.tool_name }}</td>
          <td>{{ event.action }}</td>
          <td>{{ event.decision }}</td>
          <td>{{ event.subject }}</td>
          <td>{{ event.created_at }}</td>
        </tr>
      </tbody>
    </table>

    <table v-else-if="isTaskQuery" class="tool-answer-table" data-test="tool-answer-table">
      <thead>
        <tr>
          <th>ID</th>
          <th>名称</th>
          <th>能力</th>
          <th>启用</th>
          <th>最近状态</th>
          <th>下次执行</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="task in tasks" :key="String(task.id)" data-test="tool-answer-row">
          <td>{{ task.id }}</td>
          <td>{{ task.name }}</td>
          <td>{{ task.capability }}</td>
          <td>{{ task.enabled ? '是' : '否' }}</td>
          <td>{{ task.last_status }}</td>
          <td>{{ task.next_run_at }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.tool-answer-view {
  margin-top: var(--space-2);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: var(--space-3);
  background: var(--color-bg-elevated);
  overflow-x: auto;
}

.tool-answer-count {
  font-size: var(--font-sm);
  color: var(--color-text-secondary);
  margin-bottom: var(--space-2);
  font-weight: 600;
}

.tool-answer-table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--font-xs);
}

.tool-answer-table th,
.tool-answer-table td {
  padding: 6px 8px;
  border-bottom: 1px solid var(--color-border);
  text-align: left;
  color: var(--color-text-primary);
  white-space: nowrap;
}

.tool-answer-table th {
  color: var(--color-text-tertiary);
  font-weight: 600;
  background: var(--color-bg);
}

.tool-answer-table tbody tr:hover {
  background: var(--color-bg-hover);
}
</style>
