<script setup lang="ts">
import { ref, watch } from 'vue';
import type { PendingPlanDetail as PendingPlanDetailType, ExecutionResult } from '../types';
import { confirmPlan } from '../api';

const props = defineProps<{
  plan: PendingPlanDetailType | null;
  confirmationToken?: string;
}>();

const emit = defineEmits<{
  confirmed: [result: ExecutionResult];
  error: [message: string];
}>();

const confirming = ref(false);
const localError = ref('');

watch(() => props.plan, () => {
  localError.value = '';
});

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
  <div v-if="plan" class="plan-detail" data-test="plan-detail">
    <h3>{{ plan.id }}</h3>
    <dl>
      <div><dt>tool</dt><dd>{{ plan.tool }}</dd></div>
      <div><dt>environment</dt><dd>{{ plan.environment }}</dd></div>
      <div><dt>risk</dt><dd>{{ plan.risk }}</dd></div>
      <div><dt>status</dt><dd>{{ plan.status }}</dd></div>
      <div><dt>version</dt><dd>{{ plan.version }}</dd></div>
      <div><dt>expires_at</dt><dd>{{ plan.expires_at }}</dd></div>
      <div><dt>created_by</dt><dd>{{ plan.created_by }}</dd></div>
      <div><dt>created_at</dt><dd>{{ plan.created_at }}</dd></div>
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
  </div>
  <div v-else class="empty">选择一个待确认计划查看详情。</div>
</template>
