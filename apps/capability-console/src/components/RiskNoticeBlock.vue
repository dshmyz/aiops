<script setup lang="ts">
import type { Block, SuggestedStrategy } from '../types';

defineProps<{
  block: Block;
}>();

// payload 中的数组字段安全提取（后端可能省略 omitempty 字段）
function asArray(value: unknown): string[] {
  return Array.isArray(value) ? (value as string[]) : [];
}

function strategy(block: Block): SuggestedStrategy | null {
  const raw = block.payload?.suggested_strategy;
  if (!raw || typeof raw !== 'object') return null;
  return raw as SuggestedStrategy;
}

// 后端 time.Duration 序列化为纳秒 int64，换算成人类可读的超时文案。
// ≥1s 用秒，否则用毫秒，避免展示 "30000000000" 这种不可读数字。
function formatTimeout(ns: number | undefined): string {
  if (!ns || ns <= 0) return '-';
  const seconds = ns / 1_000_000_000;
  if (seconds >= 1) return `${seconds}s`;
  return `${Math.round(ns / 1_000_000)}ms`;
}
</script>

<template>
  <div class="risk-notice-block">
    <!-- 兼容旧格式：impact 字段（非 dry-run 的风险提示） -->
    <div v-if="block.payload?.impact" class="risk-impact" data-test="risk-impact">
      {{ block.payload.impact }}
    </div>

    <!-- dry-run 预演：影响资源 -->
    <ul
      v-if="asArray(block.payload?.affected_resources).length"
      class="risk-resources"
      data-test="risk-resources"
    >
      <li v-for="(r, i) in asArray(block.payload?.affected_resources)" :key="i" class="resource-chip">
        {{ r }}
      </li>
    </ul>

    <!-- dry-run 预演：将执行的命令 -->
    <div
      v-if="asArray(block.payload?.commands).length"
      class="risk-commands"
      data-test="risk-commands"
    >
      <code v-for="(c, i) in asArray(block.payload?.commands)" :key="i" class="command-line">{{ c }}</code>
    </div>

    <!-- dry-run 预演：风险警告 -->
    <ul
      v-if="asArray(block.payload?.warnings).length"
      class="risk-warnings"
      data-test="risk-warnings"
    >
      <li v-for="(w, i) in asArray(block.payload?.warnings)" :key="i" class="warning-item">{{ w }}</li>
    </ul>

    <!-- 借鉴-3：执行策略（超时/重试/并发度/目标主机/风险等级） -->
    <dl v-if="strategy(block)" class="risk-strategy" data-test="risk-strategy">
      <div class="strategy-row">
        <dt>风险等级</dt>
        <dd>{{ strategy(block)?.risk_level ?? '-' }}</dd>
      </div>
      <div class="strategy-row">
        <dt>超时</dt>
        <dd>{{ formatTimeout(strategy(block)?.timeout) }}</dd>
      </div>
      <div class="strategy-row">
        <dt>并发度</dt>
        <dd>{{ strategy(block)?.concurrency ?? '-' }}</dd>
      </div>
      <div v-if="strategy(block)?.retry" class="strategy-row">
        <dt>重试</dt>
        <dd>{{ strategy(block)?.retry }}</dd>
      </div>
      <div v-if="strategy(block)?.target_hosts?.length" class="strategy-row">
        <dt>目标主机</dt>
        <dd>{{ strategy(block)?.target_hosts?.join(', ') }}</dd>
      </div>
    </dl>
  </div>
</template>

<style scoped>
.risk-notice-block {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 4px;
}

.risk-impact {
  padding: 6px 10px;
  background: var(--color-bg-warning-subtle, #fef3c7);
  border-radius: 6px;
  font-size: 12px;
  color: var(--color-text-warning, #92400e);
}

.risk-resources {
  list-style: none;
  padding: 0;
  margin: 0;
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.resource-chip {
  font-size: 11px;
  font-family: 'SF Mono', Menlo, monospace;
  padding: 2px 8px;
  border-radius: 4px;
  background: var(--color-bg-tag, #e8e8ed);
  color: var(--color-text-primary);
}

.risk-commands {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.command-line {
  font-family: 'SF Mono', Menlo, monospace;
  font-size: 12px;
  padding: 6px 10px;
  background: var(--color-bg-code, #f5f5f7);
  border-radius: 6px;
  color: var(--color-text-primary);
  word-break: break-all;
}

.risk-warnings {
  list-style: none;
  padding: 0;
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.warning-item {
  font-size: 12px;
  padding: 4px 8px;
  border-left: 3px solid var(--color-border-warning, #f59e0b);
  color: var(--color-text-warning, #92400e);
  background: var(--color-bg-warning-subtle, #fef3c7);
  border-radius: 0 4px 4px 0;
}

.risk-strategy {
  margin: 0;
  padding: 8px 10px;
  background: var(--color-bg-info-subtle, #e8f0fe);
  border-radius: 6px;
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
  gap: 4px 12px;
}

.strategy-row {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.strategy-row dt {
  font-size: 11px;
  color: var(--color-text-tertiary);
  font-weight: 500;
}

.strategy-row dd {
  margin: 0;
  font-size: 12px;
  font-weight: 600;
  color: var(--color-text-primary);
}
</style>
