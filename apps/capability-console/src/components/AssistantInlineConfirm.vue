<script setup lang="ts">
import { ref, watch } from 'vue';
import type { PendingPlanDetail, ExecutionResult } from '../types';
import { confirmPlan } from '../api';
import {
  labelForEnvironment,
  labelForRisk,
  labelForPlanStatus,
} from '../labels';

const props = defineProps<{
  plan: PendingPlanDetail;
  confirmationToken?: string;
  loading?: boolean;
}>();

const emit = defineEmits<{
  confirmed: [result: ExecutionResult];
  error: [message: string];
}>();

const confirming = ref(false);
const localError = ref('');

watch(
  () => props.plan.id,
  () => {
    localError.value = '';
  },
);

async function handleConfirm() {
  if (!props.plan) return;
  const token = props.confirmationToken;
  if (!token) {
    localError.value = '生产环境需要外部审批 token；本地演示请用 COPILOT_DEV_EXPOSE_CONFIRMATION_TOKEN=1 创建计划。';
    return;
  }
  confirming.value = true;
  localError.value = '';
  try {
    const result = await confirmPlan(props.plan.id, {
      expected_version: props.plan.version,
      confirmation_token: token,
    });
    emit('confirmed', result);
  } catch (err) {
    const message = err instanceof Error ? err.message : '确认执行失败';
    localError.value = message;
    emit('error', message);
  } finally {
    confirming.value = false;
  }
}
</script>

<template>
  <section v-if="loading" class="assistant-inline-confirm" data-test="assistant-inline-confirm">
    <p class="hint">正在加载计划详情…</p>
  </section>
  <section v-else class="assistant-inline-confirm" data-test="assistant-inline-confirm">
    <header class="inline-header">
      <p class="eyebrow">需要审批</p>
      <h4>{{ plan.tool }}</h4>
    </header>
    <dl class="inline-meta">
      <div><dt>环境</dt><dd>{{ labelForEnvironment(plan.environment) }}</dd></div>
      <div><dt>风险</dt><dd>{{ labelForRisk(plan.risk) }}</dd></div>
      <div><dt>状态</dt><dd>{{ labelForPlanStatus(plan.status) }}</dd></div>
      <div><dt>版本</dt><dd>{{ plan.version }}</dd></div>
      <div><dt>过期时间</dt><dd>{{ plan.expires_at }}</dd></div>
      <div><dt>提交人</dt><dd>{{ plan.created_by }}</dd></div>
    </dl>
    <dl class="inline-input">
      <div v-for="(value, key) in plan.input" :key="key">
        <dt>{{ key }}</dt><dd>{{ String(value) }}</dd>
      </div>
    </dl>
    <p v-if="!confirmationToken" class="hint">生产环境需要外部审批 token。</p>
    <button
      data-test="confirm-plan"
      class="primary-inline"
      :disabled="confirming || !confirmationToken"
      @click="handleConfirm"
    >
      {{ confirming ? '确认中' : '确认执行' }}
    </button>
    <p v-if="localError" class="error-text">{{ localError }}</p>
  </section>
</template>

<style scoped>
.assistant-inline-confirm {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  padding: 1rem;
  border: 1px solid var(--color-border);
  border-radius: 12px;
  background: var(--color-surface);
}

.inline-header .eyebrow {
  margin: 0;
  font-size: 0.75rem;
  color: var(--color-text-muted);
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.inline-header h4 {
  margin: 0.25rem 0 0;
  font-size: 1rem;
  color: var(--color-text);
}

.inline-meta,
.inline-input {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
  gap: 0.5rem 1rem;
  margin: 0;
}

.inline-meta div,
.inline-input div {
  display: flex;
  flex-direction: column;
  gap: 0.125rem;
}

dt {
  font-size: 0.7rem;
  color: var(--color-text-muted);
  text-transform: uppercase;
  letter-spacing: 0.06em;
}

dd {
  margin: 0;
  font-size: 0.875rem;
  color: var(--color-text);
  font-family: var(--font-mono, monospace);
  word-break: break-all;
}

.hint {
  margin: 0;
  font-size: 0.75rem;
  color: var(--color-text-muted);
}

.error-text {
  margin: 0;
  font-size: 0.75rem;
  color: var(--color-danger, #d33);
}
</style>
