<script setup lang="ts">
import { computed } from 'vue';
import type { UseCapabilities } from '../../composables/useCapabilities';

// 工单头：任务名（用户目标）+ 接线契约条（METHOD+路径）+ 状态线。
// 路径是本产品被接线的“实物”，等宽放大展示——整页的签名元素。
const props = defineProps<{ capabilities: UseCapabilities }>();

const goal = computed(() => props.capabilities.urlDescription.value.trim());

const contract = computed<{ tag: string; path: string } | null>(() => {
  const phase = props.capabilities.managementPhase.value;
  if (phase === 'review' || phase === 'ai') {
    const backend = props.capabilities.selected.value.backend;
    return backend.path ? { tag: backend.method, path: backend.path } : null;
  }
  if (phase === 'candidates') {
    const url = props.capabilities.importPreview.value?.source.openapi_url;
    if (!url) return null;
    try {
      const parsed = new URL(url);
      return { tag: 'DOC', path: `${parsed.host}${parsed.pathname}` };
    } catch {
      return { tag: 'DOC', path: url };
    }
  }
  const generated = props.capabilities.generatedUrlResult.value?.capability.backend;
  return generated?.path ? { tag: generated.method, path: generated.path } : null;
});

const busy = computed(() =>
  props.capabilities.urlImportLoading.value
  || props.capabilities.importPreviewLoading.value
  || props.capabilities.probeLoading.value
  || props.capabilities.importCommitLoading.value,
);

const statusLine = computed(() => {
  const phase = props.capabilities.managementPhase.value;
  if (busy.value) return '正在连接…';
  if (phase === 'candidates') {
    const stats = props.capabilities.importPreview.value?.stats;
    return stats ? `已识别 ${stats.total} 个接口 · 读取 ${stats.read} · 写入 ${stats.write}` : '等待预览';
  }
  if (phase === 'review' || phase === 'ai') {
    const probe = props.capabilities.probeResult.value;
    if (probe?.probe) return `已验证 · ${probe.probe.summary}`;
    return props.capabilities.validation.value.valid ? '定义完整 · 待验证' : '待完善';
  }
  return contract.value ? '已探测 · 待确认' : '尚未接线';
});
</script>

<template>
  <header class="wo-header" data-test="wo-header">
    <div class="wo-header__row">
      <h2>{{ goal || '让 AI 学会一件事' }}</h2>
      <button
        v-if="capabilities.managementPhase.value === 'source'"
        data-test="existing-capabilities-entry"
        class="wo-header__library"
        type="button"
        @click="capabilities.managementPhase.value = 'review'"
      >工具库 · {{ capabilities.capabilities.value.length }} 个 ↗</button>
    </div>
    <p class="wo-status" data-test="wo-status">
      <span class="wo-dot" :class="{ 'is-live': busy }" aria-hidden="true"></span>
      {{ statusLine }}
    </p>
    <div v-if="contract" class="wo-contract" data-test="wo-contract">
      <span class="method-pill" :class="contract.tag.toLowerCase() === 'get' ? 'method-get' : contract.tag.toLowerCase() === 'post' ? 'method-post' : 'method-doc'">{{ contract.tag }}</span>
      <code class="wo-contract__path">{{ contract.path }}</code>
    </div>
  </header>
</template>
