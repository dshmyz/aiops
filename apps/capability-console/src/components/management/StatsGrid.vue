<script setup lang="ts">
export interface StatsItem {
  label: string;
  value: number;
  testId?: string;
}

defineProps<{ items: StatsItem[] }>();
</script>

<template>
  <div class="stats-grid">
    <div
      v-for="(item, index) in items"
      :key="item.testId ?? item.label"
      class="stats-grid-item"
      :class="[
        index === 0 ? 'stat-success' : index === 1 ? 'stat-warning' : index === 2 ? 'stat-danger' : 'stat-accent',
      ]"
      :data-test="item.testId"
    >
      <span>{{ item.label }}</span>
      <strong>{{ item.value }}</strong>
    </div>
  </div>
</template>
