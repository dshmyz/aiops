<script setup lang="ts">
import type { AdminTool, AlertActionStep } from '../../types';
import ToolPicker from './ToolPicker.vue';
import StepParamEditor from './StepParamEditor.vue';

const props = defineProps<{
  steps: AlertActionStep[];
  tools: AdminTool[];
}>();

const emit = defineEmits<{
  (event: 'update', steps: AlertActionStep[]): void;
}>();

function toolByName(name: string): AdminTool | undefined {
  return props.tools.find((t) => t.name === name);
}

function updateStep(idx: number, step: AlertActionStep) {
  const next = props.steps.map((s, i) => (i === idx ? step : s));
  emit('update', next);
}

function addStep() {
  emit('update', [...props.steps, { tool: '', input: {} }]);
}

function removeStep(idx: number) {
  emit('update', props.steps.filter((_, i) => i !== idx));
}

function moveStep(idx: number, delta: number) {
  const target = idx + delta;
  if (target < 0 || target >= props.steps.length) return;
  const next = [...props.steps];
  const [item] = next.splice(idx, 1);
  next.splice(target, 0, item);
  emit('update', next);
}

function copyStep(idx: number) {
  const src = props.steps[idx];
  const copy = { ...src, input: { ...src.input } };
  const next = [...props.steps];
  next.splice(idx + 1, 0, copy);
  emit('update', next);
}
</script>

<template>
  <div class="sequence-editor" data-test="tool-sequence-editor">
    <ol class="step-list">
      <li v-for="(step, idx) in steps" :key="idx" class="step-item" data-test="sequence-step">
        <div class="step-head">
          <span class="step-num">{{ idx + 1 }}</span>
          <span class="step-arrow" v-if="idx < steps.length - 1">↓</span>
          <div class="step-tool">
            <ToolPicker
              :tools="tools"
              :model-value="step.tool"
              @select="(t) => updateStep(idx, { tool: t?.name ?? '', input: {} })"
            />
          </div>
          <span class="step-toolname" v-if="step.tool">{{ step.tool }}</span>
          <div class="step-ops">
            <button type="button" class="op-btn" title="上移" :disabled="idx === 0" @click="moveStep(idx, -1)">↑</button>
            <button type="button" class="op-btn" title="下移" :disabled="idx === steps.length - 1" @click="moveStep(idx, 1)">↓</button>
            <button type="button" class="op-btn" title="复制该步" @click="copyStep(idx)">⧉</button>
            <button
              type="button"
              class="op-btn danger"
              title="删除该步"
              :disabled="steps.length === 1"
              @click="removeStep(idx)"
            >×</button>
          </div>
        </div>
        <div class="step-body">
          <StepParamEditor :step="step" :tool="toolByName(step.tool)" @update="(s) => updateStep(idx, s)" />
        </div>
      </li>
    </ol>
    <button type="button" class="add-step" @click="addStep">+ 添加步骤</button>
  </div>
</template>

<style scoped>
.step-list { list-style: none; margin: 0 0 10px; padding: 0; display: flex; flex-direction: column; gap: 10px; }
.step-item { border: 1px solid var(--color-border); border-radius: var(--radius-md); padding: 10px; background: var(--color-bg); }
.step-head { display: flex; align-items: center; gap: 8px; }
.step-num { flex: none; width: 22px; height: 22px; border-radius: 50%; background: var(--color-primary-bg); color: var(--color-primary); font-size: 12px; font-weight: 600; display: inline-flex; align-items: center; justify-content: center; }
.step-arrow { color: var(--color-text-tertiary); font-size: 12px; flex: none; }
.step-tool { flex: 1; }
.step-toolname { font-size: 12px; color: var(--color-text-secondary); white-space: nowrap; max-width: 180px; overflow: hidden; text-overflow: ellipsis; }
.step-ops { display: flex; gap: 4px; flex: none; }
.op-btn { border: 1px solid var(--color-border); background: var(--color-bg-elevated); border-radius: 4px; width: 24px; height: 24px; cursor: pointer; color: var(--color-text-secondary); font-size: 12px; }
.op-btn:disabled { opacity: 0.35; cursor: not-allowed; }
.op-btn.danger { color: var(--color-danger); border-color: var(--color-danger-border); }
.step-body { margin-top: 8px; padding-left: 30px; }
.add-step { border: 1px dashed var(--color-border); background: none; color: var(--color-text-secondary); padding: 6px 14px; border-radius: var(--radius-md); cursor: pointer; font-size: 12px; }
</style>
