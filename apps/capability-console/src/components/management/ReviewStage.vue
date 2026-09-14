<script setup lang="ts">
import { ElTag } from 'element-plus';
import CapabilityEditorPanel from './CapabilityEditorPanel.vue';
import ProbePanel from './ProbePanel.vue';
import PublishChecklistPanel from './PublishChecklistPanel.vue';
import ReviewConfirmCards from './ReviewConfirmCards.vue';
import TestPreviewPanel from './TestPreviewPanel.vue';
import type { UseCapabilities } from '../../composables/useCapabilities';

defineProps<{ capabilities: UseCapabilities }>();
</script>

<template>
  <section data-test="workflow-review" class="workflow-stage workflow-review review-detail-only">
    <section data-test="studio-translator" class="review-detail editor" aria-label="能力评审">
      <!-- 状态行：下一步动作 + 校验状态，一条 hairline。统计数字已移入右侧工具库面板。 -->
      <div class="review-status-line" data-test="review-status-line">
        <span data-test="selected-next-action">下一步：{{ capabilities.nextActionLabel(capabilities.selected.value) }}</span>
        <el-tag data-test="validation-state" :type="capabilities.validation.value.valid ? 'success' : 'danger'" size="small">{{ capabilities.validationLabel.value }}</el-tag>
      </div>
      <!-- 编号章节：工具说明 → 验证 → 发布。技术面板收进发布卡的“高级详情”。 -->
      <ReviewConfirmCards :capabilities="capabilities">
        <CapabilityEditorPanel :capabilities="capabilities" />
        <TestPreviewPanel :capabilities="capabilities" />
        <div class="advanced-editor-stack">
          <PublishChecklistPanel :capabilities="capabilities" />
          <ProbePanel :capabilities="capabilities" />
        </div>
      </ReviewConfirmCards>
    </section>
  </section>
</template>