<script setup lang="ts">
import AuditLogView from '../components/AuditLogView.vue';
import ViewShell from '../components/ViewShell.vue';
import type { UseAuditEvents } from '../composables/useAuditEvents';

defineProps<{ audit: UseAuditEvents }>();

const emit = defineEmits<{
  'jump-to-plan': [planID: string];
}>();
</script>

<template>
  <ViewShell
    class="audit-entry"
    data-test="audit-entry"
    data-view="audit"
    eyebrow="Audit Log"
    title="审计记录"
    copy="追踪每一次计划创建、确认和执行，按工具/操作/决策过滤定位事件。"
  >

    <AuditLogView
      :events="audit.auditEvents.value"
      :loading="audit.auditEventsLoading.value"
      :loading-more="audit.auditLoadingMore.value"
      :error="audit.auditEventsError.value"
      :next-cursor="audit.auditNextCursor.value"
      :search-query="audit.auditSearchQuery.value"
      @refresh="audit.refresh"
      @filter="audit.applyFilter"
      @load-more="audit.loadMore"
      @jump-to-plan="(id: string) => emit('jump-to-plan', id)"
      @search="audit.search"
    />
  </ViewShell>
</template>
