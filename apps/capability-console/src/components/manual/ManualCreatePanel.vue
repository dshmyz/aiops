<script setup lang="ts">
import { computed, ref } from 'vue';
import { emptyCapability } from '../../capability';
import type { Capability } from '../../types';
import ManualFormBuilder from './ManualFormBuilder.vue';
import ManualJsonPaster from './ManualJsonPaster.vue';

const emit = defineEmits<{
  created: [capability: Capability];
}>();

type ManualTab = 'form' | 'json';
const tab = ref<ManualTab>('form');

// 内部维护正在编辑的 Capability 草稿。JSON / 表单两个子组件都通过 update 事件回写。
const capability = ref<Capability>(emptyCapability());

const canOpen = computed(() => {
  const c = capability.value;
  return Boolean(c.name.trim() && (c.backend.base_url ?? '').trim() && (c.backend.path ?? '').trim());
});

const operationLabel = computed(() => (capability.value.operation === 'write' ? '写入' : '读取'));

function onUpdate(updated: Capability) {
  capability.value = updated;
}

function openAsDraft() {
  if (!canOpen.value) {
    return;
  }
  // 深拷贝，避免与表单绑定互相引用
  emit('created', JSON.parse(JSON.stringify(capability.value)) as Capability);
}

function reset() {
  capability.value = emptyCapability();
}
</script>

<template>
  <section data-test="manual-create-panel" class="manual-create">
    <div class="manual-create__tabs" role="tablist" aria-label="手动创建方式">
      <button
        data-test="manual-tab-form"
        :class="['manual-create__tab', { 'manual-create__tab--active': tab === 'form' }]"
        role="tab"
        :aria-selected="tab === 'form'"
        @click="tab = 'form'"
      >
        表单填写
      </button>
      <button
        data-test="manual-tab-json"
        :class="['manual-create__tab', { 'manual-create__tab--active': tab === 'json' }]"
        role="tab"
        :aria-selected="tab === 'json'"
        @click="tab = 'json'"
      >
        粘贴 JSON
      </button>
    </div>

    <div class="manual-create__body">
      <ManualFormBuilder
        v-if="tab === 'form'"
        :capability="capability"
        @update="onUpdate"
      />
      <ManualJsonPaster
        v-else
        :capability="capability"
        @update="onUpdate"
      />
    </div>

    <div class="manual-create__meta">
      <span v-if="capability.name">
        草稿：{{ capability.name }}（{{ operationLabel }} / {{ capability.risk }} 风险）
      </span>
      <span v-else class="manual-create__meta--empty">填写后可继续在评审阶段细化参数与输出字段</span>
    </div>

    <div class="manual-create__actions">
      <button class="secondary-wide" @click="reset">重置</button>
      <button
        data-test="manual-open-draft"
        class="primary-inline"
        :disabled="!canOpen"
        @click="openAsDraft"
      >
        打开为草稿并按 AI 能力评审
      </button>
    </div>
  </section>
</template>

<style scoped>
.manual-create__tabs {
  display: flex;
  gap: 4px;
  margin-bottom: 14px;
  border-bottom: 1px solid var(--color-border, rgba(255, 255, 255, 0.1));
}
.manual-create__tab {
  background: none;
  border: none;
  padding: 8px 14px;
  cursor: pointer;
  color: var(--color-text-secondary, rgba(235, 235, 245, 0.6));
  font-size: 13px;
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
}
.manual-create__tab--active {
  color: var(--color-accent, #0a84ff);
  border-bottom-color: var(--color-accent, #0a84ff);
}
.manual-create__meta {
  font-size: 12px;
  color: var(--color-text-secondary, rgba(235, 235, 245, 0.6));
  margin: 12px 0;
  min-height: 18px;
}
.manual-create__meta--empty {
  color: var(--color-text-tertiary, rgba(235, 235, 245, 0.3));
}
.manual-create__actions {
  display: flex;
  gap: 10px;
  justify-content: flex-end;
}
</style>