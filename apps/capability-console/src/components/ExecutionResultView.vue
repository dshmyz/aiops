<script setup lang="ts">
import type { ExecutionResult } from '../types';
import {
  labelForExecutionStatus,
  verificationStatusLabel,
} from '../labels';

defineProps<{
  result: ExecutionResult | null;
}>();
</script>

<template>
  <div v-if="result" class="execution-result" data-test="execution-result">
    <span class="badge tag-success">已执行</span>
    <h3>{{ result.plan_id }}</h3>
    <dl>
      <div><dt>执行 ID</dt><dd>{{ result.execution_id }}</dd></div>
      <div><dt>状态</dt><dd>{{ labelForExecutionStatus(result.status) }}</dd></div>
      <div><dt>是否复用</dt><dd>{{ result.reused ? '是' : '否' }}</dd></div>
      <div v-if="result.runbook" data-test="execution-runbook"><dt>Runbook</dt><dd>{{ result.runbook }}</dd></div>
      <div v-if="result.confirmed_status"><dt>确认状态</dt><dd>{{ labelForExecutionStatus(result.confirmed_status) }}</dd></div>
    </dl>

    <section
      v-if="result.verification"
      class="verification-block"
      :class="`verification-status-${result.verification.status}`"
      data-test="verification-block"
    >
      <header class="verification-header">
        <span class="badge" :class="`tag-verification-${result.verification.status}`">
          {{ verificationStatusLabel[result.verification.status] }}
        </span>
        <h4>执行后验证</h4>
      </header>

      <dl class="verification-summary">
        <div v-if="result.verification.tool_name" data-test="verification-tool">
          <dt>验证能力</dt>
          <dd>{{ result.verification.tool_name }}</dd>
        </div>
        <div
          v-if="result.verification.status === 'success'"
          data-test="verification-status-success"
        >
          <dt>状态</dt>
          <dd>已通过</dd>
        </div>
        <div
          v-if="result.verification.status === 'failed'"
          data-test="verification-status-failed"
        >
          <dt>状态</dt>
          <dd>失败</dd>
        </div>
        <div
          v-if="result.verification.status === 'denied'"
          data-test="verification-status-denied"
        >
          <dt>状态</dt>
          <dd>被拒绝</dd>
        </div>
        <div
          v-if="result.verification.elapsed_ms !== undefined"
          data-test="verification-elapsed"
        >
          <dt>耗时</dt>
          <dd>{{ result.verification.elapsed_ms }}ms</dd>
        </div>
      </dl>

      <div
        v-if="result.verification.answer"
        class="verification-answer"
        data-test="verification-answer"
      >
        <h5>验证结果</h5>
        <pre>{{ JSON.stringify(result.verification.answer, null, 2) }}</pre>
      </div>

      <div
        v-if="result.verification.error"
        class="verification-error"
        data-test="verification-error"
      >
        <h5>错误信息</h5>
        <pre>{{ result.verification.error }}</pre>
      </div>
    </section>
  </div>
</template>

<style scoped>
.execution-result {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: var(--space-4);
  background: var(--color-bg);
}

.execution-result h3 {
  margin: var(--space-2) 0 var(--space-3);
  font-size: var(--font-lg);
  color: var(--color-text-primary);
}

.execution-result dl {
  margin: 0;
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 6px var(--space-4);
}

.execution-result dl > div {
  display: flex;
  gap: 6px;
}

.execution-result dt {
  color: var(--color-text-tertiary);
  font-size: var(--font-sm);
  min-width: 110px;
}

.execution-result dd {
  margin: 0;
  font-size: var(--font-md);
  color: var(--color-text-primary);
  word-break: break-all;
}

.verification-block {
  margin-top: var(--space-4);
  padding: var(--space-3);
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border);
  background: var(--color-bg-elevated);
}

.verification-block.verification-status-success {
  border-color: var(--color-success);
  background: var(--color-success-soft);
}

.verification-block.verification-status-failed {
  border-color: var(--color-danger);
  background: var(--color-danger-soft);
}

.verification-block.verification-status-denied {
  border-color: var(--color-warning);
  background: var(--color-warning-soft);
}

.verification-header {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: var(--space-2);
}

.verification-header h4 {
  margin: 0;
  font-size: var(--font-base);
  color: var(--color-text-primary);
}

.verification-summary {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 6px var(--space-4);
  margin: 0 0 var(--space-2);
}

.verification-summary dt {
  color: var(--color-text-tertiary);
  font-size: var(--font-sm);
  min-width: 90px;
}

.verification-summary dd {
  margin: 0;
  font-size: var(--font-md);
  color: var(--color-text-primary);
  word-break: break-all;
}

.verification-answer,
.verification-error {
  margin-top: var(--space-2);
}

.verification-answer h5,
.verification-error h5 {
  margin: 0 0 4px;
  font-size: var(--font-sm);
  color: var(--color-text-tertiary);
  font-weight: 600;
}

.verification-answer pre,
.verification-error pre {
  margin: 0;
  padding: var(--space-2);
  background: var(--color-bg);
  border-radius: var(--radius-sm);
  border: 1px solid var(--color-border);
  font-size: var(--font-sm);
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 220px;
  overflow: auto;
}
</style>
