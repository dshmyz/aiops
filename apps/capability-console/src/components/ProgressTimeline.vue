<script setup lang="ts">
/**
 * 进度事件清单。
 *
 * 渲染 assistant 问答链路的阶段切换时间线（planning → tool_executing →
 * formatting），让用户在等待最终响应时能看到 Agent 当前处于哪个阶段。
 *
 * 设计原则：
 * - 默认展开：进度是用户等待期间最需要的信息，直接可见；仍可点击收起
 * - 完成态语义：已结束的阶段显示绿色 ✓ 与完成时态文案，仅当前阶段用进行时态
 * - 不暴露原始 CoT：只显示阶段标签和工具名（如有），不展示 LLM 思考内容
 * - 与 thinking-section / tool-calls-section 并列，三者职责互补：
 *   thinking 看 CoT、tool_calls 看工具 IO、progress 看阶段时序
 */
import { computed, ref } from 'vue';
import type { ProgressStage } from '../types';
import SfSymbol from './SfSymbol.vue';

const props = defineProps<{
  /** 该 turn 累积的进度阶段列表（按接收顺序） */
  stages: ProgressStage[];
  /** 当前是否仍在流式生成（控制"进行中"标记） */
  streaming?: boolean;
}>();

const expanded = ref(true);

const hasStages = computed(() => props.stages.length > 0);

/** 当前最新阶段，用于折叠态显示"进行中"提示 */
const currentStage = computed(() => {
  if (props.stages.length === 0) return null;
  return props.stages[props.stages.length - 1];
});

/** 阶段完成后的中文标签（完成时态） */
const stageLabelDone: Record<ProgressStage['stage'], string> = {
  planning: '已规划任务',
  tool_executing: '已执行任务',
  formatting: '已生成回复',
};

/** 阶段进行中的中文标签（进行时态） */
const stageLabelActive: Record<ProgressStage['stage'], string> = {
  planning: '模型规划中',
  tool_executing: '工具执行中',
  formatting: '回复整形中',
};

/** 阶段对应的 SF Symbol 图标名（进行中时使用） */
const stageIcon: Record<ProgressStage['stage'], 'sparkles' | 'waveform' | 'bubble-left'> = {
  planning: 'sparkles',
  tool_executing: 'waveform',
  formatting: 'bubble-left',
};

/**
 * 判断第 idx 个阶段是否已完成。
 *
 * 流式结束后所有阶段都算完成；流式期间只有最后一个阶段是进行中，
 * 因为后端按顺序推送、新阶段到达即意味着前一阶段已结束。
 */
function isDone(idx: number): boolean {
  if (!props.streaming) return true;
  return idx < props.stages.length - 1;
}

function labelFor(stage: ProgressStage['stage'], done: boolean): string {
  const table = done ? stageLabelDone : stageLabelActive;
  return table[stage] ?? stage;
}

function iconFor(stage: ProgressStage['stage']): 'sparkles' | 'waveform' | 'bubble-left' {
  return stageIcon[stage] ?? 'sparkles';
}

/** 格式化相对时间显示（HH:MM:SS.mmm），用于时间线节点 */
function formatTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const pad = (n: number, len = 2) => String(n).padStart(len, '0');
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`;
}
</script>

<template>
  <div
    v-if="hasStages"
    data-test="progress-timeline"
    class="progress-timeline"
  >
    <button
      type="button"
      class="progress-toggle"
      :aria-expanded="expanded"
      @click="expanded = !expanded"
    >
      <SfSymbol
        :name="streaming && currentStage ? iconFor(currentStage.stage) : 'checkmark-circle'"
        :size="14"
        :class="streaming ? 'progress-toggle-icon-active' : 'progress-toggle-icon-done'"
      />
      <span class="progress-summary">
        <template v-if="streaming && currentStage">
          {{ labelFor(currentStage.stage, false) }}<template v-if="currentStage.detail"> · {{ currentStage.detail }}</template>
        </template>
        <template v-else>
          已完成 {{ stages.length }} 个阶段
        </template>
      </span>
      <span v-if="streaming" class="progress-pulse" aria-hidden="true"></span>
      <span class="progress-chevron" :class="{ expanded }">
        <svg viewBox="0 0 24 24" width="12" height="12" aria-hidden="true">
          <path fill="currentColor" d="M7.41 8.59L12 13.17l4.59-4.58L18 10l-6 6-6-6z" />
        </svg>
      </span>
    </button>
    <ol v-show="expanded" class="progress-list">
      <li
        v-for="(s, idx) in stages"
        :key="idx"
        class="progress-item"
        :class="[`stage-${s.stage}`, isDone(idx) ? 'is-done' : 'is-active']"
        :data-test="`progress-item-${isDone(idx) ? 'done' : 'active'}`"
      >
        <span class="progress-item-icon">
          <SfSymbol :name="isDone(idx) ? 'checkmark-circle' : iconFor(s.stage)" :size="14" />
        </span>
        <span class="progress-item-label">{{ labelFor(s.stage, isDone(idx)) }}</span>
        <span class="progress-item-time">{{ formatTime(s.received_at) }}</span>
        <span v-if="s.detail" class="progress-item-detail">{{ s.detail }}</span>
      </li>
    </ol>
  </div>
</template>

<style scoped>
.progress-timeline {
  margin-top: 0.5rem;
  border: 1px solid rgba(32, 36, 42, 0.1);
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.5);
  overflow: hidden;
}

.progress-toggle {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  width: 100%;
  padding: 0.5rem 0.75rem;
  background: transparent;
  border: none;
  cursor: pointer;
  font-size: 0.8rem;
  color: #20242a;
  text-align: left;
}

.progress-toggle:hover {
  background: rgba(32, 36, 42, 0.04);
}

.progress-toggle-icon-done {
  color: #3f9b62;
}

.progress-toggle-icon-active {
  color: #4f7ccf;
}

.progress-summary {
  flex: 1;
  font-weight: 500;
}

.progress-pulse {
  width: 0.5rem;
  height: 0.5rem;
  border-radius: 50%;
  background: #4f7ccf;
  animation: progress-pulse 1.2s ease-in-out infinite;
}

@keyframes progress-pulse {
  0%, 100% { opacity: 0.4; transform: scale(0.85); }
  50% { opacity: 1; transform: scale(1.15); }
}

.progress-chevron {
  display: inline-flex;
  transition: transform 0.2s ease;
  color: #68717a;
}

.progress-chevron.expanded {
  transform: rotate(180deg);
}

.progress-list {
  margin: 0;
  padding: 0.35rem 0.75rem 0.6rem;
  list-style: none;
  border-top: 1px solid rgba(32, 36, 42, 0.06);
}

/*
 * 两行网格：第一行 图标 + 标签 + 时间，第二行 detail 说明文字（与标签左对齐缩进）。
 * 图标列固定 18px，detail 从第 2 列开始，形成"标题 + 缩进描述"的清单观感。
 */
.progress-item {
  display: grid;
  grid-template-columns: 18px minmax(0, 1fr) auto;
  grid-template-rows: auto auto;
  column-gap: 0.4rem;
  row-gap: 0.15rem;
  align-items: center;
  padding: 0.35rem 0;
  font-size: 0.75rem;
  color: #68717a;
}

.progress-item + .progress-item {
  border-top: 1px dashed rgba(32, 36, 42, 0.06);
}

.progress-item-icon {
  grid-row: 1;
  grid-column: 1;
  display: inline-flex;
  justify-content: center;
}

.progress-item-label {
  grid-row: 1;
  grid-column: 2;
  color: #20242a;
  font-weight: 500;
}

.progress-item-time {
  grid-row: 1;
  grid-column: 3;
  color: #a0a8b0;
  font-family: 'JetBrains Mono', Consolas, monospace;
  font-size: 0.68rem;
}

.progress-item-detail {
  grid-row: 2;
  grid-column: 2 / -1;
  color: #68717a;
  font-size: 0.72rem;
  line-height: 1.45;
  word-break: break-word;
}

/* 完成态统一绿色 ✓；进行中按阶段配色：planning 紫、tool_executing 蓝、formatting 绿 */
.progress-item.is-done .progress-item-icon { color: #3f9b62; }
.progress-item.is-active.stage-planning .progress-item-icon { color: #a75bc2; }
.progress-item.is-active.stage-tool_executing .progress-item-icon { color: #4f7ccf; }
.progress-item.is-active.stage-formatting .progress-item-icon { color: #3f9b62; }

/* 进行中的阶段图标脉冲，与折叠态的 progress-pulse 保持一致的等待语义 */
.progress-item.is-active .progress-item-icon {
  animation: progress-pulse 1.2s ease-in-out infinite;
}
</style>
