<script setup lang="ts">
import { computed } from 'vue';
import type { MCPServer } from '../types';

const props = defineProps<{
  servers: MCPServer[];
}>();

const emit = defineEmits<{
  (event: 'toggle-enabled', id: string, enabled: boolean): void;
  (event: 'edit', server: MCPServer): void;
  (event: 'delete', id: string): void;
}>();

// 连接方式：优先显示 command（stdio），无 command 时显示 url（SSE）。
function endpoint(server: MCPServer): string {
  if (server.command) return server.command;
  if (server.url) return server.url;
  return '—';
}

const rows = computed(() =>
  props.servers.map((server) => ({
    server,
    connection: endpoint(server),
  })),
);

function onToggle(server: MCPServer, event: Event) {
  const checked = (event.target as HTMLInputElement).checked;
  emit('toggle-enabled', server.id, checked);
}

function onEdit(server: MCPServer) {
  emit('edit', server);
}

function onDelete(server: MCPServer) {
  emit('delete', server.id);
}
</script>

<template>
  <section data-test="mcp-server-list" class="mcp-server-list">
    <div
      v-if="servers.length === 0"
      data-test="mcp-server-empty"
      class="mcp-server-empty"
    >
      暂无 MCP 服务器配置
    </div>
    <table v-else class="mcp-server-table">
      <thead>
        <tr>
          <th>名称</th>
          <th>连接方式</th>
          <th>启用</th>
          <th>更新时间</th>
          <th>操作</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="row in rows"
          :key="row.server.id"
          data-test="mcp-server-row"
          :data-server-id="row.server.id"
        >
          <td class="cell-name">{{ row.server.name }}</td>
          <td class="cell-connection">{{ row.connection }}</td>
          <td class="cell-enabled">
            <label class="toggle-wrap">
              <input
                data-test="mcp-server-toggle"
                type="checkbox"
                :checked="row.server.enabled"
                @change="onToggle(row.server, $event)"
              />
            </label>
          </td>
          <td class="cell-updated">{{ row.server.updated_at }}</td>
          <td class="cell-actions">
            <button
              data-test="mcp-server-edit"
              class="row-action"
              type="button"
              @click="onEdit(row.server)"
            >
              编辑
            </button>
            <button
              data-test="mcp-server-delete"
              class="row-action danger"
              type="button"
              @click="onDelete(row.server)"
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
.mcp-server-list {
  width: 100%;
  overflow-x: auto;
}

.mcp-server-empty {
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

.mcp-server-empty::before {
  content: "";
  display: block;
  width: 40px;
  height: 40px;
  border-radius: 50%;
  background: var(--color-bg-hover);
  margin-bottom: var(--space-2);
}

.mcp-server-table {
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

.mcp-server-table th,
.mcp-server-table td {
  padding: var(--space-3);
  text-align: left;
  border-bottom: 1px solid var(--color-border);
  vertical-align: middle;
}

.mcp-server-table th {
  color: var(--color-text-secondary);
  font-size: var(--font-sm);
  font-weight: 600;
  background: var(--color-bg-hover);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.mcp-server-table th:nth-child(1), .mcp-server-table td:nth-child(1) { width: 20%; }
.mcp-server-table th:nth-child(2), .mcp-server-table td:nth-child(2) { width: 30%; }
.mcp-server-table th:nth-child(3), .mcp-server-table td:nth-child(3) { width: 10%; }
.mcp-server-table th:nth-child(4), .mcp-server-table td:nth-child(4) { width: 22%; }
.mcp-server-table th:nth-child(5), .mcp-server-table td:nth-child(5) { width: 18%; }

.mcp-server-table tbody td {
  word-break: break-word;
  overflow-wrap: anywhere;
}

.mcp-server-table tbody td.cell-name,
.mcp-server-table tbody td.cell-connection,
.mcp-server-table tbody td.cell-updated {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.mcp-server-table tbody tr:last-child td {
  border-bottom: none;
}

.cell-name {
  color: var(--color-text-primary);
  font-weight: 600;
}

.cell-connection,
.cell-updated {
  color: var(--color-text-secondary);
  font-family: var(--font-mono);
  font-size: var(--font-md);
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
