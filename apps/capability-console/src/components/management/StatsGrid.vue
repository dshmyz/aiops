<script setup lang="ts">
export interface StatsItem {
  label: string;
  value: number;
  testId?: string;
  /** 提供后该项可点击（如点击统计数字筛选对应列表）。 */
  onClick?: () => void;
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
        item.onClick ? 'is-clickable' : '',
      ]"
      :data-test="item.testId"
      :title="item.onClick ? '点击筛选' : undefined"
      @click="item.onClick"
    >
      <span>{{ item.label }}</span>
      <strong>{{ item.value }}</strong>
    </div>
  </div>
</template>
