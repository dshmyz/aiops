<script setup lang="ts">
import type { SchedulePreset } from '../types';

defineProps<{
  modelValue: SchedulePreset | null;
}>();

const emit = defineEmits<{
  (event: 'update:modelValue', value: SchedulePreset): void;
}>();

interface PresetOption {
  value: SchedulePreset;
  title: string;
  description: string;
}

const options: PresetOption[] = [
  { value: '5m', title: '每 5 分钟', description: '高频巡检，适合热点资源' },
  { value: '1h', title: '每小时', description: '常规健康检查' },
  { value: 'daily', title: '每天 00:00', description: '夜间巡检，避开业务高峰' },
  { value: 'weekly', title: '每周一 00:00', description: '周度基线巡检' },
];

function select(value: SchedulePreset) {
  emit('update:modelValue', value);
}
</script>

<template>
  <div data-test="schedule-preset-picker" class="schedule-preset-picker">
    <button
      v-for="option in options"
      :key="option.value"
      type="button"
      data-test="schedule-preset-option"
      :data-preset="option.value"
      class="schedule-preset-option"
      :class="{ active: modelValue === option.value }"
      @click="select(option.value)"
    >
      <strong>{{ option.title }}</strong>
      <small>{{ option.description }}</small>
    </button>
  </div>
</template>

<style scoped>
.schedule-preset-picker {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-2);
}

.schedule-preset-option {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-1);
  padding: var(--space-3);
  background: var(--color-bg-elevated);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  color: var(--color-text-secondary);
  font-size: var(--font-base);
  text-align: left;
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s, color 0.15s;
}

.schedule-preset-option strong {
  font-size: var(--font-lg);
  color: var(--color-text-primary);
}

.schedule-preset-option small {
  color: var(--color-text-tertiary);
  font-size: var(--font-sm);
}

.schedule-preset-option:hover {
  border-color: var(--color-border-accent);
  background: var(--color-bg-hover);
}

.schedule-preset-option.active {
  border-color: var(--color-accent);
  background: var(--color-accent-soft);
  color: var(--color-text-primary);
}

.schedule-preset-option.active strong {
  color: var(--color-accent);
}
</style>
