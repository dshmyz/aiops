<script setup lang="ts">
// 建议提问完全由父级从已发布能力的数据生成（ai.examples / domain 退回），
// 本组件只负责展示，不写死任何具体中间件。
const props = withDefaults(defineProps<{
  suggestions?: string[];
}>(), {
  suggestions: () => [],
});

defineEmits<{
  'pick': [prompt: string];
}>();
</script>

<template>
  <div v-if="suggestions.length > 0" data-test="assistant-suggestions" class="assistant-suggestions">
    <p class="suggestions-title">试试这些问题</p>
    <div class="suggestions-grid">
      <button
        v-for="prompt in suggestions"
        :key="prompt"
        data-test="assistant-suggestion-item"
        class="suggestion-item"
        @click="$emit('pick', prompt)"
      >
        <span class="suggestion-icon" aria-hidden="true">›</span>
        <span class="suggestion-text">{{ prompt }}</span>
      </button>
    </div>
  </div>
</template>

<style scoped>
.assistant-suggestions {
  flex: 1;
  display: flex;
  flex-direction: column;
  justify-content: center;
  padding: var(--space-4);
}

.suggestions-title {
  font-size: var(--font-xs);
  color: var(--color-text-tertiary);
  margin-bottom: var(--space-3);
  text-transform: uppercase;
  letter-spacing: 0.06em;
  font-weight: 600;
}

.suggestions-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: var(--space-2);
}

.suggestion-item {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  background: var(--color-bg-elevated);
  border: none;
  border-radius: var(--radius-xl);
  color: var(--color-text-secondary);
  font-size: var(--font-sm);
  text-align: left;
  cursor: pointer;
  transition: all 0.2s var(--ease-out);
  line-height: 1.4;
  box-shadow: var(--shadow-sm);
}

.suggestion-item:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
  transform: translateY(-1px);
  box-shadow: var(--shadow-md);
}

.suggestion-item:active {
  transform: translateY(0) scale(0.99);
}

.suggestion-icon {
  flex-shrink: 0;
  color: var(--color-accent);
  font-weight: 700;
  font-size: var(--font-base);
  line-height: 1.3;
}

.suggestion-text {
  flex: 1;
  min-width: 0;
}
</style>
