<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { MCPServer, SaveMCPServerPayload } from '../types';

const props = defineProps<{
  server?: MCPServer | null;
}>();

const emit = defineEmits<{
  (event: 'submit', payload: SaveMCPServerPayload): void;
  (event: 'cancel'): void;
}>();

// 内部状态：name / command / url / argsText (JSON 字符串) / envText (JSON 字符串) / enabled
const name = ref('');
const command = ref('');
const url = ref('');
const argsText = ref('');
const envText = ref('');
const enabled = ref(true);

// 编辑模式：传入 server prop 时字段预填。watch 以便 server 变化时重新填充。
function applyServer(server: MCPServer | null | undefined) {
  if (!server) {
    name.value = '';
    command.value = '';
    url.value = '';
    argsText.value = '';
    envText.value = '';
    enabled.value = true;
    return;
  }
  name.value = server.name;
  command.value = server.command;
  url.value = server.url;
  argsText.value = server.args && server.args.length > 0 ? JSON.stringify(server.args, null, 2) : '';
  envText.value = server.env && Object.keys(server.env).length > 0 ? JSON.stringify(server.env, null, 2) : '';
  enabled.value = server.enabled;
}

watch(
  () => props.server,
  (server) => applyServer(server),
  { immediate: true },
);

// args JSON 解析：空字符串 → []，非法 → null
function parseArgs(text: string): string[] | null {
  const trimmed = text.trim();
  if (trimmed === '') return [];
  try {
    const value = JSON.parse(trimmed) as unknown;
    if (Array.isArray(value) && value.every((item) => typeof item === 'string')) {
      return value;
    }
    return null;
  } catch {
    return null;
  }
}

// env JSON 解析：空字符串 → {}，非法 → null
function parseEnv(text: string): Record<string, string> | null {
  const trimmed = text.trim();
  if (trimmed === '') return {};
  try {
    const value = JSON.parse(trimmed) as unknown;
    if (value && typeof value === 'object' && !Array.isArray(value)) {
      const record = value as Record<string, unknown>;
      // 校验所有值为 string
      for (const v of Object.values(record)) {
        if (typeof v !== 'string') return null;
      }
      return record as Record<string, string>;
    }
    return null;
  } catch {
    return null;
  }
}

const parsedArgs = computed(() => parseArgs(argsText.value));
const parsedEnv = computed(() => parseEnv(envText.value));

// 表单校验：
//  - name 非空
//  - command 或 url 至少一项非空（后端要求二选一）
//  - args / env JSON 合法
const canSubmit = computed(() => {
  if (name.value.trim() === '') return false;
  if (command.value.trim() === '' && url.value.trim() === '') return false;
  if (parsedArgs.value === null) return false;
  if (parsedEnv.value === null) return false;
  return true;
});

function onSubmit() {
  if (!canSubmit.value) return;
  emit('submit', {
    name: name.value.trim(),
    command: command.value.trim(),
    args: parsedArgs.value ?? [],
    env: parsedEnv.value ?? {},
    url: url.value.trim(),
    enabled: enabled.value,
  });
}

function onCancel() {
  emit('cancel');
}
</script>

<template>
  <form data-test="mcp-server-form" class="mcp-server-form" @submit.prevent="onSubmit">
    <label class="form-field">
      <span class="form-label">服务器名称</span>
      <input
        data-test="mcp-server-form-name"
        v-model="name"
        class="form-input"
        type="text"
        placeholder="例如：grafana（用作工具命名前缀，唯一）"
      />
    </label>

    <label class="form-field">
      <span class="form-label">Command（stdio 进程，与 URL 二选一）</span>
      <input
        data-test="mcp-server-form-command"
        v-model="command"
        class="form-input"
        type="text"
        placeholder="例如：mcp-grafana"
      />
    </label>

    <label class="form-field">
      <span class="form-label">URL（SSE 端点，与 Command 二选一）</span>
      <input
        data-test="mcp-server-form-url"
        v-model="url"
        class="form-input"
        type="text"
        placeholder="例如：http://grafana:3000/mcp"
      />
    </label>

    <label class="form-field">
      <span class="form-label">Args（JSON 数组，stdio 模式下传给 Command 的参数）</span>
      <textarea
        data-test="mcp-server-form-args"
        v-model="argsText"
        class="form-textarea"
        rows="3"
        placeholder='["--port", "3000"]'
      />
    </label>

    <label class="form-field">
      <span class="form-label">Env（JSON 对象，传给进程的环境变量）</span>
      <textarea
        data-test="mcp-server-form-env"
        v-model="envText"
        class="form-textarea"
        rows="3"
        placeholder='{"API_KEY":"xxx"}'
      />
    </label>

    <label class="form-field form-field-inline">
      <input
        data-test="mcp-server-form-enabled"
        v-model="enabled"
        type="checkbox"
      />
      <span class="form-label">启用（禁用后配置保留，Reload 时跳过工具注册）</span>
    </label>

    <div class="form-actions">
      <button data-test="mcp-server-form-submit" type="button" class="form-submit" :disabled="!canSubmit" @click="onSubmit">
        保存
      </button>
      <button data-test="mcp-server-form-cancel" type="button" class="form-cancel" @click="onCancel">
        取消
      </button>
    </div>
  </form>
</template>

<style scoped>
.mcp-server-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.form-field {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.form-field-inline {
  flex-direction: row;
  align-items: center;
  gap: var(--space-2);
}

.form-label {
  color: var(--color-text-secondary);
  font-size: var(--font-sm);
}

.form-input,
.form-textarea {
  padding: var(--space-2) var(--space-3);
  background: var(--color-bg-elevated);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  color: var(--color-text-primary);
  font-family: var(--font-ui);
  font-size: var(--font-base);
}

.form-textarea {
  font-family: var(--font-mono);
  resize: vertical;
}

.form-input:focus,
.form-textarea:focus {
  outline: none;
  border-color: var(--color-accent);
}

.form-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-3);
  margin-top: var(--space-2);
}

.form-submit {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 9px 16px;
  background: var(--gradient-brand);
  color: #fff;
  border: none;
  border-radius: var(--radius-md);
  font-size: var(--font-md);
  font-weight: 500;
  cursor: pointer;
  transition: all 0.15s ease;
  white-space: nowrap;
  box-shadow: 0 2px 8px rgba(56, 189, 248, 0.22);
}

.form-submit:hover:not(:disabled) {
  box-shadow: 0 4px 14px rgba(56, 189, 248, 0.38);
  transform: translateY(-1px);
}

.form-submit:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.form-cancel {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 8px 14px;
  background: transparent;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-md);
  color: var(--color-text-secondary);
  font-size: var(--font-md);
  font-weight: 500;
  cursor: pointer;
  transition: all 0.15s ease;
  white-space: nowrap;
}

.form-cancel:hover {
  border-color: var(--color-text-muted);
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
}
</style>
