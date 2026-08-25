<script setup lang="ts">
import type { UseCapabilities } from '../../composables/useCapabilities';

const props = defineProps<{ capabilities: UseCapabilities }>();
</script>

<template>
  <section class="editor-group">
    <div class="group-title">
      <h3>先看是否能发布</h3>
      <span :class="capabilities.publishReady.value ? 'ready-text' : 'blocked-text'">{{ capabilities.publishReady.value ? '可以发布' : '需要处理' }}</span>
    </div>
    <div data-test="publish-checklist" class="publish-panel slim">
      <div class="target-path"><span>目标文件</span><code>{{ capabilities.publishTargetPath.value }}</code></div>
      <div class="check-list">
        <div v-for="check in capabilities.publishChecks.value" :key="check.label" class="check-row" :class="{ failed: !check.ok }">
          <strong>{{ check.ok ? '通过' : '阻塞' }}</strong>
          <span>{{ check.label }}</span>
          <small>{{ check.detail }}</small>
        </div>
      </div>
      <button data-test="publish-current" class="primary-inline" :disabled="!capabilities.publishReady.value" @click="capabilities.publishCurrent">
        {{ capabilities.currentPublishLabel() }}
      </button>
    </div>
  </section>
</template>