<script setup lang="ts">
import SfSymbol from './SfSymbol.vue';

defineProps<{ publishedCount: number }>();

defineEmits<{ 'go-to-management': [] }>();
</script>

<template>
  <div v-if="publishedCount > 0" data-test="assistant-capability-status" class="capability-status-badge">
    <span class="status-dot" aria-hidden="true"></span>
    <strong>{{ publishedCount }}</strong>
    <span>个 AI 工具可用</span>
  </div>
  <div v-else data-test="assistant-capability-empty" class="capability-status-empty">
    <div class="empty-icon" aria-hidden="true">
      <SfSymbol name="exclamationmark-triangle" :size="22" />
    </div>
    <div class="empty-body">
      <strong>没有可用的 AI 工具</strong>
      <small>需要先在能力管理中发布至少一个能力，AI 才能响应提问</small>
    </div>
    <button data-test="assistant-capability-empty-action" class="empty-action" @click="$emit('go-to-management')">
      去发布能力
    </button>
  </div>
</template>

<style scoped>
.capability-status-badge {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  padding: 6px 14px;
  background: var(--color-success-soft);
  border: none;
  border-radius: var(--radius-pill);
  font-size: var(--font-sm);
  color: var(--color-text-secondary);
  margin: 0 auto var(--space-3);
  font-weight: 500;
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--color-success);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-success) 24%, transparent);
  animation: status-pulse 2s ease-in-out infinite;
}

@keyframes status-pulse {
  0%, 100% { box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-success) 24%, transparent); }
  50% { box-shadow: 0 0 0 6px color-mix(in srgb, var(--color-success) 12%, transparent); }
}

.capability-status-badge strong {
  font-family: var(--font-mono);
  color: var(--color-success);
  font-size: var(--font-base);
  font-weight: 600;
}

.capability-status-empty {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-4);
  background: var(--color-warning-soft);
  border: none;
  border-radius: var(--radius-xl);
  margin: 0 var(--space-6) var(--space-3);
}

.empty-icon {
  flex-shrink: 0;
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-lg);
  background: var(--color-warning-soft);
  color: var(--color-warning);
}

.empty-body {
  display: flex;
  flex-direction: column;
  gap: 2px;
  flex: 1;
}

.empty-body strong {
  font-size: var(--font-base);
  color: var(--color-text-primary);
  font-weight: 600;
}

.empty-body small {
  font-size: var(--font-sm);
  color: var(--color-text-secondary);
}

.empty-action {
  padding: 8px 16px;
  background: var(--view-accent);
  color: white;
  border: none;
  border-radius: var(--radius-pill);
  font-size: var(--font-sm);
  font-weight: 600;
  cursor: pointer;
  white-space: nowrap;
  transition: all 0.2s var(--ease-out);
  box-shadow: 0 2px 8px rgba(10, 132, 255, 0.24);
}

.empty-action:hover {
  transform: translateY(-1px);
  box-shadow: 0 4px 12px rgba(10, 132, 255, 0.32);
}

.empty-action:active {
  transform: translateY(0) scale(0.98);
}
</style>
