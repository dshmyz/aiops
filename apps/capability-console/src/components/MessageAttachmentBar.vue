<script setup lang="ts">
import SfSymbol from './SfSymbol.vue';
import type { MessageAttachment } from '../utils/attachments';

/**
 * 输入框上方/下方的附件条：展示待发送的附件（文件名 + 大小 + 移除）。
 * 纯展示组件——增删逻辑由父层通过 add/remove 事件驱动。
 */
defineProps<{
  attachments: MessageAttachment[];
}>();

const emit = defineEmits<{
  remove: [index: number];
}>();

function formatSize(content: string): string {
  const bytes = new TextEncoder().encode(content).length;
  if (bytes >= 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`;
  }
  return `${bytes} B`;
}
</script>

<template>
  <div v-if="attachments.length > 0" data-test="attachment-bar" class="attachment-bar" role="list" aria-label="消息附件">
    <div
      v-for="(att, index) in attachments"
      :key="`${att.name}-${index}`"
      data-test="attachment-chip"
      class="attachment-chip"
      role="listitem"
      :title="att.name"
    >
      <SfSymbol name="doc-text" :size="13" />
      <span class="chip-name">{{ att.name }}</span>
      <span class="chip-size">{{ formatSize(att.content) }}</span>
      <button
        data-test="attachment-remove"
        type="button"
        class="chip-remove"
        :aria-label="`移除附件 ${att.name}`"
        @click="emit('remove', index)"
      >×</button>
    </div>
  </div>
</template>

<style scoped>
.attachment-bar {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 8px;
}

.attachment-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  max-width: 260px;
  padding: 3px 7px;
  border: 1px solid var(--border);
  border-radius: 7px;
  background: var(--surface-secondary);
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.4;
}

.chip-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-primary);
}

.chip-size {
  flex-shrink: 0;
  color: var(--text-tertiary);
  font-size: 11px;
}

.chip-remove {
  flex-shrink: 0;
  border: none;
  background: none;
  padding: 0 1px;
  cursor: pointer;
  color: var(--text-tertiary);
  font-size: 14px;
  line-height: 1;
  border-radius: 4px;
}

.chip-remove:hover,
.chip-remove:focus-visible {
  color: var(--text-primary);
  background: var(--surface-tertiary);
}
</style>
