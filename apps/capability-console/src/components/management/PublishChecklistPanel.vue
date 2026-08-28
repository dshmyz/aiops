<script setup lang="ts">
import type { UseCapabilities } from '../../composables/useCapabilities';

defineProps<{ capabilities: UseCapabilities }>();

// 一键修复：check 项自带 fix 动作（写入合理默认值），修完自动校验会刷新检查表。
function applyFix(fix: (() => void) | undefined) {
  fix?.();
}
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
          <button
            v-if="!check.ok && check.fix"
            class="check-fix"
            :data-test="`check-fix-${check.label}`"
            @click="applyFix(check.fix)"
          >{{ check.fixLabel ?? '修复' }}</button>
        </div>
      </div>
      <button data-test="publish-current" class="primary-inline" :disabled="!capabilities.publishReady.value" @click="capabilities.publishCurrent">
        {{ capabilities.currentPublishLabel() }}
      </button>
    </div>
  </section>
</template>

<style scoped>
.check-fix {
  font-size: 12px;
  padding: 2px 10px;
  border-radius: 999px;
  border: 1px solid var(--el-color-primary, #409eff);
  color: var(--el-color-primary, #409eff);
  background: transparent;
  cursor: pointer;
  white-space: nowrap;
}
.check-fix:hover {
  background: var(--el-color-primary-light-9, #ecf5ff);
}
</style>