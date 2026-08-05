<script setup lang="ts">
import { computed } from 'vue';

const props = defineProps<{
  count: number;
}>();

// count=0 不渲染；count<=99 显示数字；count>99 显示 "99+"
const label = computed(() => {
  if (props.count <= 0) return '';
  if (props.count <= 99) return String(props.count);
  return '99+';
});
</script>

<template>
  <span v-if="label" data-test="scheduled-task-badge" class="scheduled-task-badge">{{ label }}</span>
</template>

<style scoped>
.scheduled-task-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 18px;
  height: 18px;
  padding: 0 6px;
  margin-left: var(--space-2);
  background: var(--color-danger);
  border-radius: var(--radius-pill);
  color: var(--color-bg);
  font-size: var(--font-xs);
  font-weight: 600;
  line-height: 1;
  cursor: pointer;
}
</style>
