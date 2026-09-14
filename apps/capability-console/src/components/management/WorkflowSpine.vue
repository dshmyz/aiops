<script setup lang="ts">
import type { UseCapabilities } from '../../composables/useCapabilities';

// 细线脊步骤导航：不再是三张大卡。当前步骤用主题色下划线表达，
// done 用信号绿小点。占位极小，把视觉预算让给契约条和阶段内容。
const props = defineProps<{ capabilities: UseCapabilities }>();

interface Step {
  key: 'source' | 'candidates' | 'review';
  num: string;
  label: string;
}

const steps: Step[] = [
  { key: 'source', num: '①', label: '连接' },
  { key: 'candidates', num: '②', label: '验证' },
  { key: 'review', num: '③', label: '发布' },
];

function done(step: Step): boolean {
  const phase = props.capabilities.managementPhase.value;
  if (step.key === 'source') {
    return phase !== 'source' && Boolean(props.capabilities.importPreview.value || props.capabilities.importBatch.value || props.capabilities.generatedUrlResult.value);
  }
  if (step.key === 'candidates') {
    return phase === 'review' || phase === 'ai';
  }
  return false;
}

function canGo(step: Step): boolean {
  const phase = props.capabilities.managementPhase.value;
  if (step.key === 'source') return true;
  if (step.key === 'candidates') return Boolean(props.capabilities.importPreview.value);
  return props.capabilities.capabilities.value.length > 0 && (phase !== 'source' || Boolean(props.capabilities.importBatch.value || props.capabilities.importPreview.value || props.capabilities.generatedUrlResult.value));
}
</script>

<template>
  <nav class="wo-spine" data-test="wo-spine" aria-label="接入步骤">
    <button
      v-for="step in steps"
      :key="step.key"
      :data-test="`workflow-step-${step.key}`"
      class="wo-spine__step"
      :class="{ 'is-current': capabilities.managementPhase.value === step.key, 'is-done': done(step) }"
      :disabled="!canGo(step)"
      :aria-current="capabilities.managementPhase.value === step.key ? 'step' : undefined"
      @click="capabilities.managementPhase.value = step.key"
    >
      <span class="wo-spine__num" aria-hidden="true">{{ done(step) ? '✓' : step.num }}</span>
      {{ step.label }}
    </button>
  </nav>
</template>

<style scoped>
.wo-spine {
  display: flex;
  gap: 4px;
  border-bottom: 1px solid var(--color-border);
  margin-bottom: 24px;
}

.wo-spine__step {
  appearance: none;
  border: none;
  background: none;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 10px 14px 12px;
  margin-bottom: -1px;
  border-bottom: 2px solid transparent;
  color: var(--color-text-tertiary);
  font: inherit;
  font-size: 14px;
  cursor: pointer;
  transition: color 0.15s ease, border-color 0.15s ease;
}

.wo-spine__step:disabled { cursor: not-allowed; opacity: 0.45; }

.wo-spine__step.is-current {
  color: var(--color-text-primary);
  font-weight: 650;
  border-bottom-color: var(--view-accent, var(--color-accent));
}

.wo-spine__step.is-done:not(.is-current) { color: var(--color-text-secondary); }

.wo-spine__num {
  font-size: 13px;
  width: 18px;
  text-align: center;
}

.wo-spine__step.is-done .wo-spine__num { color: var(--color-success); }

.wo-spine__step:focus-visible {
  outline: 2px solid var(--view-accent, var(--color-accent));
  outline-offset: -2px;
  border-radius: 6px;
}
</style>
