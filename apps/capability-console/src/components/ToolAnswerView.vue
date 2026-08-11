<script setup lang="ts">
import { computed, ref } from 'vue';

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
const isSpecialTool = computed(() => isEventQuery.value || isTaskQuery.value);

// 通用：排除 count/events/tasks/message，把剩余 key-value 渲染为表格
const genericEntries = computed(() => {
  const skip = new Set(['count', 'events', 'tasks', 'message']);
  return Object.entries(props.answer)
    .filter(([k]) => !skip.has(k))
    .map(([key, value]) => ({ key, value }));
});

// 嵌套对象/数组：用 JSON 折叠
const expandedKeys = ref<Set<string>>(new Set());
function toggleExpand(key: string) {
  if (expandedKeys.value.has(key)) {
    expandedKeys.value.delete(key);
  } else {
    expandedKeys.value.add(key);
  }
}
function isExpandable(value: unknown): boolean {
  return value !== null && typeof value === 'object';
}
function formatValue(value: unknown): string {
  if (value === null || value === undefined) return '—';
  if (typeof value === 'boolean') return value ? '是' : '否';
  if (typeof value === 'number') return String(value);
  if (typeof value === 'string') return value;
  return JSON.stringify(value, null, 2);
}
</script>

<template>
  <div class="tool-answer-view" data-test="tool-answer-view">
    <!-- event.query / task.query 专用表格 -->
    <template v-if="isSpecialTool">
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
    </template>

    <!-- 通用：所有其他工具的原始结果 key-value 表格 -->
    <template v-else>
      <div class="tool-answer-header" data-test="tool-answer-header">
        <span class="tool-answer-tool-name">{{ tool }}</span>
        <span class="tool-answer-hint">原始结果</span>
      </div>
      <table v-if="genericEntries.length > 0" class="tool-answer-table" data-test="tool-answer-table">
        <thead>
          <tr>
            <th>字段</th>
            <th>值</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="entry in genericEntries" :key="entry.key" data-test="tool-answer-row">
            <td class="tool-answer-key">{{ entry.key }}</td>
            <td class="tool-answer-value">
              <template v-if="!isExpandable(entry.value)">{{ formatValue(entry.value) }}</template>
              <template v-else>
                <button class="tool-answer-expand-btn" @click="toggleExpand(entry.key)">
                  {{ expandedKeys.has(entry.key) ? '收起' : '展开' }}
                </button>
                <pre v-if="expandedKeys.has(entry.key)" class="tool-answer-json">{{ formatValue(entry.value) }}</pre>
              </template>
            </td>
          </tr>
        </tbody>
      </table>
      <div v-else class="tool-answer-empty" data-test="tool-answer-empty">无结构化数据</div>
    </template>
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

.tool-answer-header {
  display: flex;
  align-items: baseline;
  gap: var(--space-2);
  margin-bottom: var(--space-2);
}

.tool-answer-tool-name {
  font-size: var(--font-sm);
  font-weight: 600;
  color: var(--color-text-primary);
}

.tool-answer-hint {
  font-size: var(--font-xs);
  color: var(--color-text-tertiary);
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

.tool-answer-key {
  font-weight: 500;
  color: var(--color-text-secondary);
  min-width: 120px;
}

.tool-answer-value {
  white-space: normal;
  word-break: break-word;
}

.tool-answer-expand-btn {
  font-size: var(--font-xs);
  color: var(--color-primary);
  background: none;
  border: none;
  cursor: pointer;
  padding: 0;
}

.tool-answer-expand-btn:hover {
  text-decoration: underline;
}

.tool-answer-json {
  font-family: var(--font-mono);
  font-size: var(--font-xs);
  margin: var(--space-1) 0 0;
  padding: var(--space-2);
  background: var(--color-bg);
  border-radius: var(--radius-md);
  overflow-x: auto;
  white-space: pre;
}

.tool-answer-empty {
  font-size: var(--font-xs);
  color: var(--color-text-tertiary);
  font-style: italic;
}
</style>
