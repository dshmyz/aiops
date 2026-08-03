<script setup lang="ts">
/**
 * 进度事件折叠面板。
 *
 * 渲染 assistant 问答链路的阶段切换时间线（planning → tool_executing →
 * formatting），让用户在等待最终响应时能看到 Agent 当前处于哪个阶段。
 *
 * 设计原则：
 * - 折叠展示：默认收起，点击切换，避免占用过多垂直空间
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

const expanded = ref(false);

const hasStages = computed(() => props.stages.length > 0);

/** 当前最新阶段，用于折叠态显示"进行中"提示 */
const currentStage = computed(() => {
  if (props.stages.length === 0) return null;
  return props.stages[props.stages.length - 1];
});

/** 阶段中文标签映射 */
const stageLabel: Record<ProgressStage['stage'], string> = {
  planning: '模型规划中',
  tool_executing: '工具执行中',
  formatting: '回复整形中',
};

/** 阶段对应的 SF Symbol 图标名 */
const stageIcon: Record<ProgressStage['stage'], 'sparkles' | 'waveform' | 'bubble-left'> = {
  planning: 'sparkles',
  tool_executing: 'waveform',
  formatting: 'bubble-left',
};

function labelFor(stage: ProgressStage['stage']): string {
  return stageLabel[stage] ?? stage;
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
      <SfSymbol :name="currentStage ? iconFor(currentStage.stage) : 'sparkles'" :size="14" />
      <span class="progress-summary">
        <template v-if="streaming && currentStage">
          {{ labelFor(currentStage.stage) }}<template v-if="currentStage.detail"> · {{ currentStage.detail }}</template>
        </template>
        <template v-else>
          已记录 {{ stages.length }} 个阶段事件
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
        :class="`stage-${s.stage}`"
      >
        <span class="progress-item-icon">
          <SfSymbol :name="iconFor(s.stage)" :size="12" />
        </span>
        <span class="progress-item-label">{{ labelFor(s.stage) }}</span>
        <span v-if="s.detail" class="progress-item-detail">{{ s.detail }}</span>
        <span class="progress-item-time">{{ formatTime(s.received_at) }}</span>
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
  padding: 0.25rem 0.75rem 0.6rem;
  list-style: none;
  border-top: 1px solid rgba(32, 36, 42, 0.06);
}

.progress-item {
  display: grid;
  grid-template-columns: 18px minmax(0, 1fr) auto;
  gap: 0.4rem;
  align-items: center;
  padding: 0.3rem 0;
  font-size: 0.75rem;
  color: #68717a;
}

.progress-item + .progress-item {
  border-top: 1px dashed rgba(32, 36, 42, 0.06);
}

.progress-item-icon {
  display: inline-flex;
  justify-content: center;
  color: #4f7ccf;
}

.progress-item-label {
  color: #20242a;
  font-weight: 500;
}

.progress-item-detail {
  color: #68717a;
  font-family: 'JetBrains Mono', Consolas, monospace;
  font-size: 0.7rem;
}

.progress-item-time {
  color: #a0a8b0;
  font-family: 'JetBrains Mono', Consolas, monospace;
  font-size: 0.68rem;
}

/* 阶段配色：planning 紫、tool_executing 蓝、formatting 绿 */
.stage-planning .progress-item-icon { color: #a75bc2; }
.stage-tool_executing .progress-item-icon { color: #4f7ccf; }
.stage-formatting .progress-item-icon { color: #3f9b62; }
</style>
