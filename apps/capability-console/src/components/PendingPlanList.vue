<script setup lang="ts">
import type { PendingPlanSummary } from '../types';

defineProps<{
  plans: PendingPlanSummary[];
  loading: boolean;
  selectedId?: string;
}>();

const emit = defineEmits<{
  select: [planID: string];
  refresh: [];
}>();
</script>

<template>
  <div class="section-heading">
    <div>
      <h2>待确认计划</h2>
      <span>{{ plans.length }} 项等待确认</span>
    </div>
  </div>
  <div class="plan-list">
    <button
      v-for="plan in plans"
      :key="plan.id"
      class="plan-row"
      :class="{ active: plan.id === selectedId }"
      :data-test="`plan-row-${plan.id}`"
      @click="emit('select', plan.id)"
    >
      <strong>{{ plan.tool }}</strong>
      <span class="plan-id">{{ plan.id }}</span>
      <small>
        <span>{{ plan.environment }}</span> · <span>{{ plan.risk }}</span> · <span>{{ plan.status }}</span>
      </small>
      <small>
        <span>{{ plan.created_by }}</span> · <span>{{ plan.expires_at }}</span>
      </small>
    </button>
    <p v-if="plans.length === 0" class="empty">暂无待确认计划。</p>
  </div>
</template>
