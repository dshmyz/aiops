<script setup lang="ts">
import type { UseCapabilities } from '../../composables/useCapabilities';

defineProps<{ capabilities: UseCapabilities }>();
</script>

<template>
  <nav class="workflow-rail" aria-label="接入步骤">
    <button
      data-test="workflow-step-source"
      class="workflow-step"
      :class="{ active: capabilities.managementPhase.value === 'source', done: capabilities.importPreview.value || capabilities.importBatch.value }"
      :aria-current="capabilities.managementPhase.value === 'source' ? 'step' : undefined"
      @click="capabilities.managementPhase.value = 'source'"
    >
      <span>1</span>
      <strong>连接接口</strong>
      <small>输入接口地址或导入文档</small>
    </button>
    <button
      data-test="workflow-step-candidates"
      class="workflow-step"
      :class="{ active: capabilities.managementPhase.value === 'candidates', done: capabilities.importBatch.value }"
      :disabled="!capabilities.importPreview.value"
      :aria-current="capabilities.managementPhase.value === 'candidates' ? 'step' : undefined"
      @click="capabilities.managementPhase.value = 'candidates'"
    >
      <span>2</span>
      <strong>验证接口</strong>
      <small>选择接口并确认测试结果</small>
    </button>
    <button
      data-test="workflow-step-review"
      class="workflow-step"
      :class="{ active: capabilities.managementPhase.value === 'review' }"
      :disabled="capabilities.capabilities.value.length === 0"
      :aria-current="capabilities.managementPhase.value === 'review' ? 'step' : undefined"
      @click="capabilities.managementPhase.value = 'review'"
    >
      <span>3</span>
      <strong>确认并发布</strong>
      <small>确认工具信息并发布给 AI</small>
    </button>
  </nav>
</template>
