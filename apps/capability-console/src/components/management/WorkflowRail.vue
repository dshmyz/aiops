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
      <strong>接入 API</strong>
      <small>Swagger 或示例数据</small>
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
      <strong>选择能力</strong>
      <small>只挑适合 AI 的接口</small>
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
      <strong>评审发布</strong>
      <small>补参数、校验、发布</small>
    </button>
    <button
      data-test="workflow-step-ai"
      class="workflow-step"
      :class="{ active: capabilities.managementPhase.value === 'ai' }"
      :disabled="capabilities.stats.value.published === 0"
      :aria-current="capabilities.managementPhase.value === 'ai' ? 'step' : undefined"
      @click="capabilities.managementPhase.value = 'ai'"
    >
      <span>4</span>
      <strong>AI 试问</strong>
      <small>验证自然语言调用</small>
    </button>
  </nav>
</template>
