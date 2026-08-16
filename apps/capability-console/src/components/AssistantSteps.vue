<script setup lang="ts">
/**
 * 智能体多步执行的"已执行步骤"区块。
 *
 * 渲染自治 agent 循环内每一步只读工具调用的独立清单（工具名、结果摘要、
 * 输入/输出），与最终答复分开展示，让操作员看清"AI 检查了哪些工具、每步
 * 得到什么"后再给出结论。
 *
 * 设计原则：
 * - 独立区块：不复用 progress 折叠，步骤是审计线索，默认展开、可点击收起
 * - 输入/输出默认折叠，点击展开查看详细 JSON
 * - 只读语义：循环内的 advisory 步都已完成（status=done），无进行中态
 * - 不暴露原始 CoT：只展示工具名、摘要与输入/输出，与 thinking/tool_calls
 *   职责互补：steps 看"多步做了什么"、progress 看"阶段时序"、thinking 看 CoT
 */
import { computed, ref, reactive } from 'vue';
import type { AssistantStep } from '../types';
import SfSymbol from './SfSymbol.vue';

const props = defineProps<{
  /** 该 turn 累积的执行步骤（按 step_index 升序） */
  steps: AssistantStep[];
  /** 当前是否仍在流式生成（预留：后续步骤可达 running 态时用） */
  streaming?: boolean;
}>();

const expanded = ref(true);

// 每个步骤的输入/输出折叠状态，key = step_index
const detailsExpanded = reactive<Record<number, { input: boolean; output: boolean }>>({});

function isDetailsExpanded(stepIndex: number, section: 'input' | 'output'): boolean {
  if (!(stepIndex in detailsExpanded)) {
    detailsExpanded[stepIndex] = { input: false, output: false };
  }
  return detailsExpanded[stepIndex][section];
}

function toggleDetails(stepIndex: number, section: 'input' | 'output') {
  if (!(stepIndex in detailsExpanded)) {
    detailsExpanded[stepIndex] = { input: false, output: false };
  }
  detailsExpanded[stepIndex][section] = !detailsExpanded[stepIndex][section];
}

/** 输出 JSON 是否较长（>200 字符） */
function isLongOutput(step: AssistantStep): boolean {
  if (!step.output) return false;
  return JSON.stringify(step.output).length > 200;
}

const hasSteps = computed(() => props.steps.length > 0);

/** 步骤 human-readable 工具名（用 step_index 消歧同一工具的多步调用） */
function toolLabel(step: AssistantStep, index: number): string {
  const sameToolAhead = props.steps.some((s, i) => i < index && s.tool === step.tool);
  return sameToolAhead ? `${step.tool} (${step.step_index + 1})` : step.tool;
}

function stepStatusText(step: AssistantStep): string {
  if (step.status === 'done') return '已完成';
  if (step.status === 'running') return '执行中';
  if (step.status === 'failed') return '失败';
  return step.status;
}

function stepStatusVariant(step: AssistantStep): string {
  if (step.status === 'done') return 'done';
  if (step.status === 'failed') return 'failed';
  return 'running';
}
</script>

<template>
  <div
    v-if="hasSteps"
    data-test="assistant-steps"
    class="assistant-steps"
  >
    <button
      type="button"
      class="steps-toggle"
      :aria-expanded="expanded"
      @click="expanded = !expanded"
    >
      <SfSymbol name="arrow-up-circle" :size="16" class="steps-toggle-icon" />
      <span class="steps-summary">已执行 {{ steps.length }} 个步骤</span>
      <span class="steps-chevron" :class="{ expanded }">
        <svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">
          <path fill="currentColor" d="M7.41 8.59L12 13.17l4.59-4.58L18 10l-6 6-6-6z" />
        </svg>
      </span>
    </button>
    <ol v-show="expanded" class="steps-list">
      <li
        v-for="(step, index) in steps"
        :key="step.step_index"
        class="step-item"
        :class="`status-${stepStatusVariant(step)}`"
        :data-test="`assistant-step-item-${index}`"
      >
        <span class="step-item-icon">
          <SfSymbol name="checkmark-circle" :size="16" />
        </span>
        <span class="step-item-body">
          <span class="step-item-head">
            <span class="step-item-tool">{{ toolLabel(step, index) }}</span>
            <span class="step-item-status">{{ stepStatusText(step) }}</span>
          </span>
          <span v-if="step.summary" class="step-item-summary">{{ step.summary }}</span>
          <span v-if="step.input && Object.keys(step.input).length" class="step-item-toggleable">
            <button type="button" class="step-detail-toggle" @click="toggleDetails(step.step_index, 'input')">
              <svg viewBox="0 0 24 24" width="10" height="10" class="step-detail-chevron" :class="{ open: isDetailsExpanded(step.step_index, 'input') }">
                <path fill="currentColor" d="M7.41 8.59L12 13.17l4.59-4.58L18 10l-6 6-6-6z" />
              </svg>
              <span class="step-item-label">输入</span>
            </button>
            <pre v-show="isDetailsExpanded(step.step_index, 'input')" class="step-item-json">{{ JSON.stringify(step.input, null, 2) }}</pre>
          </span>
          <span v-if="step.output && Object.keys(step.output).length" class="step-item-toggleable">
            <button type="button" class="step-detail-toggle" @click="toggleDetails(step.step_index, 'output')">
              <svg viewBox="0 0 24 24" width="10" height="10" class="step-detail-chevron" :class="{ open: isDetailsExpanded(step.step_index, 'output') }">
                <path fill="currentColor" d="M7.41 8.59L12 13.17l4.59-4.58L18 10l-6 6-6-6z" />
              </svg>
              <span class="step-item-label">输出</span>
            </button>
            <pre v-show="isDetailsExpanded(step.step_index, 'output')" class="step-item-json">{{ JSON.stringify(step.output, null, 2) }}</pre>
          </span>
        </span>
      </li>
    </ol>
  </div>
</template>

<style scoped>
.assistant-steps {
  margin-top: 0.5rem;
  border: 1px solid rgba(32, 36, 42, 0.1);
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.5);
  overflow: hidden;
}

.steps-toggle {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  width: 100%;
  padding: 0.5rem 1rem;
  background: transparent;
  border: none;
  cursor: pointer;
  font-size: 0.95rem;
  color: #20242a;
  text-align: left;
}

.steps-toggle:hover {
  background: rgba(32, 36, 42, 0.04);
}

.steps-toggle-icon {
  color: #4f7ccf;
}

.steps-summary {
  flex: 1;
  font-weight: 600;
}

.steps-chevron {
  display: inline-flex;
  transition: transform 0.2s ease;
  color: #68717a;
}

.steps-chevron.expanded {
  transform: rotate(180deg);
}

.steps-list {
  margin: 0;
  padding: 0.45rem 1rem 0.75rem;
  list-style: none;
  border-top: 1px solid rgba(32, 36, 42, 0.06);
}

.step-item {
  display: grid;
  grid-template-columns: 22px minmax(0, 1fr);
  grid-template-rows: auto auto auto auto;
  column-gap: 0.5rem;
  row-gap: 0.2rem;
  align-items: start;
  padding: 0.5rem 0;
  font-size: 0.85rem;
  color: #68717a;
}

.step-item + .step-item {
  border-top: 1px dashed rgba(32, 36, 42, 0.06);
}

.step-item-icon {
  grid-row: 1;
  grid-column: 1;
  display: inline-flex;
  justify-content: center;
  margin-top: 1px;
}

.step-item.status-done .step-item-icon { color: #3f9b62; }
.step-item.status-running .step-item-icon { color: #4f7ccf; }
.step-item.status-failed .step-item-icon { color: #d33; }

.step-item-body {
  grid-row: 1;
  grid-column: 2;
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
  min-width: 0;
}

.step-item-head {
  display: flex;
  align-items: center;
  gap: 0.4rem;
}

.step-item-tool {
  color: #20242a;
  font-weight: 550;
  font-family: 'JetBrains Mono', Consolas, monospace;
  font-size: 0.84rem;
  word-break: break-word;
}

.step-item-status {
  font-size: 0.74rem;
  padding: 2px 8px;
  border-radius: 999px;
  background: #e8f5ee;
  color: #3f9b62;
  font-weight: 500;
  white-space: nowrap;
}

.step-item.status-running .step-item-status {
  background: #e8eefb;
  color: #4f7ccf;
}

.step-item.status-failed .step-item-status {
  background: #fdeaea;
  color: #d33;
}

.step-item-summary {
  color: #68717a;
  line-height: 1.45;
  word-break: break-word;
}

.step-item-toggleable {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
}

.step-detail-toggle {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  background: none;
  border: none;
  padding: 0;
  cursor: pointer;
  color: #a0a8b0;
  font-size: 0.72rem;
  font-weight: 500;
}

.step-detail-toggle:hover {
  color: #4f7ccf;
}

.step-detail-chevron {
  transition: transform 0.15s ease;
  transform: rotate(-90deg);
}

.step-detail-chevron.open {
  transform: rotate(0deg);
}

.step-item-label {
  font-size: 0.72rem;
  color: #a0a8b0;
  font-weight: 500;
}

.step-item-json {
  margin: 0;
  font-size: 0.76rem;
  color: #4a5159;
  background: rgba(32, 36, 42, 0.04);
  padding: 0.4rem 0.55rem;
  border-radius: 4px;
  overflow-x: auto;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: 'JetBrains Mono', Consolas, monospace;
  line-height: 1.45;
}
</style>
