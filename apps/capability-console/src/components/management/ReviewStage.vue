<script setup lang="ts">
import { ElTag } from 'element-plus';
import CapabilityEditorPanel from './CapabilityEditorPanel.vue';
import CapabilityLedgerPanel from './CapabilityLedgerPanel.vue';
import PublishChecklistPanel from './PublishChecklistPanel.vue';
import StatsGrid from './StatsGrid.vue';
import TestPreviewPanel from './TestPreviewPanel.vue';
import type { UseCapabilities } from '../../composables/useCapabilities';

defineProps<{ capabilities: UseCapabilities }>();
</script>

<template>
  <section data-test="workflow-review" class="workflow-stage workflow-review">
    <CapabilityLedgerPanel :capabilities="capabilities" />
    <section data-test="studio-translator" class="review-detail editor" aria-label="能力评审">
      <StatsGrid class="review-kpis" :items="[
        { label: 'AI 可用', value: capabilities.stats.value.published, testId: 'stat-published' },
        { label: '待评审', value: capabilities.stats.value.review, testId: 'stat-review' },
        { label: '校验失败', value: capabilities.stats.value.invalid, testId: 'stat-invalid' },
        { label: '可发布', value: capabilities.stats.value.publishable, testId: 'stat-publishable' },
      ]" />
      <div class="section-heading">
        <h2>评审发布</h2>
        <div class="heading-status">
          <span data-test="selected-next-action">下一步：{{ capabilities.nextActionLabel(capabilities.selected.value) }}</span>
          <el-tag data-test="validation-state" :type="capabilities.validation.value.valid ? 'success' : 'danger'">{{ capabilities.validationLabel.value }}</el-tag>
        </div>
      </div>
      <PublishChecklistPanel :capabilities="capabilities" />
      <CapabilityEditorPanel :capabilities="capabilities" />
      <TestPreviewPanel :capabilities="capabilities" />
    </section>
  </section>
</template>