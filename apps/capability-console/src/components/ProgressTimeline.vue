<script setup lang="ts">
/**
 * 执行进度流（参考 Trae / Claude 的"实时工具调用流"体验）。
 *
 * 渲染 assistant 问答链路的一条连贯执行进度时间线：阶段作骨架（planning →
 * tool_executing → formatting），工具调用作为缩进子项锚定在 tool_executing
 * 之下。让用户在等待最终响应时能实时看到 Agent 正在做什么、已经完成什么。
 *
 * 设计原则：
 * - 默认展开：进度是等待期间最需要的信息，直接可见；仍可点击收起
 * - 完成态语义：已结束的阶段/工具显示绿色 ✓ 与完成时态文案，仅当前进行中的
 *   变体用进行时态 + 脉冲
 * - 不暴露原始 CoT：只显示阶段标签与工具名，不展示 LLM 思考内容
 * - 输入是已合并的执行流条目（conversationProgress.mergeExecutionProgress 的
 *   输出），组件本身不关心三源数据如何汇聚，保持单一只管渲染
 */
import { computed, ref } from 'vue';
import type { ProgressEntry } from '../conversationProgress';
import SfSymbol from './SfSymbol.vue';

const props = defineProps<{
  /** 合并后的执行进度条目流（阶段 + 工具子项，按顺序） */
  entries: ProgressEntry[];
  /** 当前是否仍在流式生成（控制"进行中"标记） */
  streaming?: boolean;
}>();

const expanded = ref(true);

const hasEntries = computed(() => props.entries.length > 0);

/** 当前进行中的条目（最后一个未完成），用于折叠态提示 */
const currentEntry = computed(() => {
  for (let i = props.entries.length - 1; i >= 0; i -= 1) {
    if (!props.entries[i].done) return props.entries[i];
  }
  return null;
});

/** 已完成条目数量（含工具子项） */
const doneCount = computed(() => props.entries.filter((e) => e.done).length);

/**
 * 工具条目判断是否进行中。工具阶段下，done=true 即完成；
 * 流式且工具未 done 才视为进行中。
 */
function isToolActive(entry: ProgressEntry): boolean {
  return entry.kind === 'tool' && props.streaming && !entry.done;
}

/** 格式化相对时间显示（HH:MM:SS）。不显示毫秒：阶段与工具几乎同时到达，
 *  毫秒会让时间线显得机械。 */
function formatTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}
</script>

<template>
  <div v-if="hasEntries" data-test="progress-timeline" class="progress-timeline">
    <button
      type="button"
      class="progress-toggle"
      :aria-expanded="expanded"
      @click="expanded = !expanded"
    >
      <SfSymbol :name="'checkmark-circle'" :size="16" :class="currentEntry ? 'progress-toggle-icon-active' : 'progress-toggle-icon-done'" />
      <span class="progress-summary">
        <template v-if="currentEntry">
          {{ currentEntry.label }}<template v-if="currentEntry.detail"> · {{ currentEntry.detail }}</template>
        </template>
        <template v-else>
          已完成 {{ doneCount }} 个步骤
        </template>
      </span>
      <span v-if="currentEntry" class="progress-pulse" aria-hidden="true"></span>
      <span class="progress-chevron" :class="{ expanded }">
        <svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">
          <path fill="currentColor" d="M7.41 8.59L12 13.17l4.59-4.58L18 10l-6 6-6-6z" />
        </svg>
      </span>
    </button>
    <ol v-show="expanded" class="progress-list">
      <li
        v-for="(e, idx) in entries"
        :key="idx"
        class="progress-item"
        :class="[`stage-${e.stage}`, e.kind === 'tool' ? 'is-tool' : 'is-phase', isToolActive(e) ? 'is-active' : 'is-done']"
        :data-test="isToolActive(e) ? 'progress-item-active' : 'progress-item-done'"
      >
        <span class="progress-item-icon">
          <SfSymbol
            :name="isToolActive(e) ? 'waveform' : 'checkmark-circle'"
            :size="isToolActive(e) ? 14 : 16"
          />
        </span>
        <span class="progress-item-label">{{ e.label }}</span>
        <span class="progress-item-time">{{ formatTime(e.time) }}</span>
        <span v-if="e.detail" class="progress-item-detail">{{ e.detail }}</span>
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
  font-weight: 600;
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
  padding: 0.45rem 1rem 0.75rem;
  list-style: none;
  border-top: 1px solid rgba(32, 36, 42, 0.06);
}

/*
 * 两行网格：第一行 图标 + 标签 + 时间，第二行 detail 说明文字（与标签左对齐缩进）。
 * 图标列固定 22px，detail 从第 2 列开始，形成"标题 + 缩进描述"的清单观感。
 * 工具子项额外左缩进，体现"阶段下的动作"，形成阶段 → 动作的树状层级。
 */
.progress-item {
  display: grid;
  grid-template-columns: 22px minmax(0, 1fr) auto;
  grid-template-rows: auto auto;
  column-gap: 0.5rem;
  row-gap: 0.2rem;
  align-items: center;
  padding: 0.5rem 0;
  font-size: 0.85rem;
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
  font-size: 0.76rem;
}

.progress-item-detail {
  grid-row: 2;
  grid-column: 2 / -1;
  color: #68717a;
  font-size: 0.8rem;
  line-height: 1.45;
  word-break: break-word;
}

/* 工具子项缩进：阶段 → 动作的层级 */
.progress-item.is-tool {
  padding-left: 1.25rem;
  font-size: 0.82rem;
}

.progress-item.is-tool .progress-item-label {
  font-weight: 400;
  color: #4a525b;
}

/* 完成态统一绿色 ✓；进行中的工具子项蓝色波形 + 脉冲 */
.progress-item.is-done .progress-item-icon { color: #3f9b62; }
.progress-item.is-active .progress-item-icon { color: #4f7ccf; }

/* 进行中的工具子项脉冲，与折叠态的 progress-pulse 保持一致的等待语义 */
.progress-item.is-active .progress-item-icon {
  animation: progress-pulse 1.2s ease-in-out infinite;
}
</style>