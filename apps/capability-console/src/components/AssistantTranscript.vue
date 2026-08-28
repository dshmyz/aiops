<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import type { ConversationTurn } from '../types';
import { formatDateGroup } from '../conversationFormat';
import ConversationTurnItem from './ConversationTurnItem.vue';
import SfSymbol from './SfSymbol.vue';

const props = defineProps<{
  turns: ConversationTurn[];
  loading: boolean;
  hasMore?: boolean;
  loadingMore?: boolean;
  /** 当外部已经提供了空状态引导（如建议问题卡片）时，隐藏 transcript 内部的空状态，避免重复 */
  hideEmpty?: boolean;
}>();

const emit = defineEmits<{
  (event: 'load-more'): void;
  (event: 'copy', content: string): void;
  (event: 'regenerate', turn: ConversationTurn): void;
  (event: 'retry'): void;
  (event: 'edit', turn: ConversationTurn): void;
  (event: 'quote', turn: ConversationTurn): void;
}>();

const transcriptRef = ref<HTMLElement | null>(null);

/** 用户是否贴近底部：贴近时流式内容自动跟随，上翻看历史时不拽底 */
const nearBottom = ref(true);

// 距底多少像素内视为"贴近底部"，避免滚动条差几个像素就疯狂追底
const NEAR_BOTTOM_THRESHOLD = 64;

function onScroll() {
  const el = transcriptRef.value;
  if (!el) return;
  nearBottom.value = el.scrollHeight - el.scrollTop - el.clientHeight < NEAR_BOTTOM_THRESHOLD;
}

function scrollToBottom() {
  const el = transcriptRef.value;
  if (!el) return;
  el.scrollTop = el.scrollHeight;
  nearBottom.value = true;
}

// 新消息到达（turns 数量变化）时，仅在用户贴近底部时跟随。
// 用户上翻看历史、流式生成中浏览旧内容时绝不强制拽底。
watch(
  () => props.turns.length,
  () => {
    if (nearBottom.value) void nextTick(scrollToBottom);
  },
);

// 请求结束（loading 由 true → false）：若用户仍在底部则回正到最终结果
watch(
  () => props.loading,
  (loading, prev) => {
    if (!loading && prev && nearBottom.value) void nextTick(scrollToBottom);
  },
);

// 流式生成中内容持续增长：只要用户仍贴近底部就跟随光标逐字滚动
watch(
  () => {
    if (!props.loading) return 0;
    return props.turns[props.turns.length - 1]?.content?.length ?? 0;
  },
  () => {
    if (nearBottom.value) void nextTick(scrollToBottom);
  },
);

// 最后一个 turn 是否是流式生成中的 assistant turn（id 以 local-assistant- 开头）。
// 此时由 ConversationTurnItem 内部显示 typing/光标，不再渲染独立 typing 行。
const lastTurnIsStreaming = computed(() => {
  if (props.turns.length === 0 || !props.loading) return false;
  const last = props.turns[props.turns.length - 1];
  return last.role === 'assistant' && last.id.startsWith('local-assistant-');
});

// 最后一条用户消息的索引：编辑回炉重发的目标。无论其后是否已有助手回复都可编辑，
// 但流式生成中（loading）不允许截断对话。
const lastUserTurnIndex = computed(() => {
  for (let i = props.turns.length - 1; i >= 0; i--) {
    if (props.turns[i].role === 'user') return i;
  }
  return -1;
});

function canEditTurn(index: number): boolean {
  return !props.loading && index === lastUserTurnIndex.value && !props.turns[index].error;
}

// 判断某个 turn 是否需要在其前面插入日期分隔线（首条或跨天时插入）
function shouldShowDivider(index: number): boolean {
  if (index === 0) return true;
  const cur = formatDateGroup(props.turns[index].created_at).key;
  const prev = formatDateGroup(props.turns[index - 1].created_at).key;
  return cur !== prev;
}

function dividerLabel(index: number): string {
  return formatDateGroup(props.turns[index].created_at).label;
}
</script>

<template>
  <div
    ref="transcriptRef"
    data-test="assistant-transcript"
    class="assistant-transcript"
    :class="{ 'assistant-transcript--collapsed': turns.length === 0 && !loading && hideEmpty }"
    @scroll="onScroll"
  >
    <button
      v-if="!nearBottom && turns.length > 0"
      data-test="assistant-scroll-bottom"
      type="button"
      class="assistant-scroll-bottom"
      @click="scrollToBottom"
    >
      <svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">
        <path d="M12 5v14m0 0-6-6m6 6 6-6" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
      最新回复
    </button>

    <div
      v-if="turns.length === 0 && !loading && !hideEmpty"
      data-test="assistant-transcript-empty"
      class="assistant-transcript-empty"
    >
      <div data-test="assistant-empty-icon" class="assistant-empty-icon">
        <SfSymbol name="bubble-left" :size="40" />
      </div>
      <p class="assistant-empty-title">开始对话</p>
      <p class="assistant-empty-subtitle">输入一个中间件问题，AI 助手会调用已发布能力帮你排查。</p>
    </div>

    <button
      v-if="hasMore"
      data-test="assistant-load-more"
      type="button"
      class="assistant-load-more"
      :disabled="loadingMore"
      @click="emit('load-more')"
    >
      <svg v-if="loadingMore" class="load-more-spinner" viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">
        <circle cx="12" cy="12" r="10" fill="none" stroke="currentColor" stroke-width="3" stroke-dasharray="31.4 31.4" stroke-linecap="round" />
      </svg>
      {{ loadingMore ? '加载中...' : '加载更多历史' }}
    </button>

    <template v-for="(turn, index) in turns" :key="turn.id">
      <div
        v-if="shouldShowDivider(index)"
        data-test="conversation-date-divider"
        class="conversation-date-divider"
      >
        <span class="conversation-date-pill">{{ dividerLabel(index) }}</span>
      </div>
      <ConversationTurnItem
        :turn="turn"
        :is-last="index === turns.length - 1"
        :streaming="lastTurnIsStreaming && index === turns.length - 1"
        :can-edit="canEditTurn(index)"
        @copy="emit('copy', $event)"
        @regenerate="emit('regenerate', $event)"
        @retry="emit('retry')"
        @edit="emit('edit', $event)"
        @quote="emit('quote', $event)"
      />
    </template>

    <div
      v-if="loading && !lastTurnIsStreaming"
      data-test="assistant-transcript-loading"
      class="assistant-typing-row"
    >
      <div data-test="assistant-loading-avatar" class="assistant-loading-avatar">
        <SfSymbol name="sparkles" :size="18" />
      </div>
      <div class="assistant-typing-bubble">
        <span class="typing-dots">
          <span class="typing-dot"></span>
          <span class="typing-dot"></span>
          <span class="typing-dot"></span>
        </span>
        <span class="typing-text">正在思考</span>
      </div>
    </div>

    <!-- 追加在消息流末尾的内容（如内联批准确认卡片），随消息流一起滚动 -->
    <slot name="footer" />
  </div>
</template>

<style scoped>
.assistant-transcript {
  position: relative;
  display: flex;
  flex-direction: column;
  flex: 1;
  overflow-y: auto;
  padding: var(--space-5) var(--space-4);
}

/* 用户上翻后出现的"回到底部"悬浮胶囊，跟随滚动容器底部吸底 */
.assistant-scroll-bottom {
  position: sticky;
  bottom: var(--space-4);
  z-index: 2;
  align-self: center;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin: calc(-1 * var(--space-4)) auto var(--space-2);
  padding: 6px 14px;
  background: var(--material-regular);
  -webkit-backdrop-filter: var(--material-blur);
  backdrop-filter: var(--material-blur);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-pill, 9999px);
  box-shadow: var(--shadow-md);
  color: var(--color-text-secondary);
  font-size: var(--font-sm);
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s var(--ease-out);
  animation: scroll-bottom-enter 0.25s var(--ease-out) both;
}

.assistant-scroll-bottom:hover {
  color: var(--color-accent);
  border-color: var(--color-accent);
  transform: translateY(-1px);
}

@keyframes scroll-bottom-enter {
  0% { transform: translateY(6px); opacity: 0; }
  100% { transform: translateY(0); opacity: 1; }
}

/* 当外部提供空状态引导（suggestions）时，transcript 塌缩不占空间 */
.assistant-transcript--collapsed {
  flex: 0 1 0;
  padding: 0;
  overflow: visible;
}

.assistant-transcript-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
  padding: var(--space-10) var(--space-4);
  color: var(--color-text-tertiary);
  animation: empty-enter 0.5s var(--ease-out) both;
}

@keyframes empty-enter {
  0% { transform: translateY(8px); opacity: 0; }
  100% { transform: translateY(0); opacity: 1; }
}

.assistant-empty-icon {
  width: 72px;
  height: 72px;
  border-radius: 22px;
  background: var(--gradient-brand);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-bottom: var(--space-5);
  box-shadow: 0 12px 28px rgba(10, 132, 255, 0.28), 0 0 0 1px rgba(255, 255, 255, 0.06) inset;
}

.assistant-empty-title {
  font-size: var(--font-xl);
  font-weight: 600;
  color: var(--color-text-primary);
  margin-bottom: var(--space-2);
  letter-spacing: -0.01em;
}

.assistant-empty-subtitle {
  font-size: var(--font-sm);
  color: var(--color-text-secondary);
  max-width: 320px;
  line-height: 1.5;
}

.assistant-load-more {
  align-self: center;
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: var(--space-4);
  padding: 6px 14px;
  background: var(--material-thin);
  -webkit-backdrop-filter: var(--material-blur);
  backdrop-filter: var(--material-blur);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-pill, 9999px);
  color: var(--color-text-secondary);
  font-size: var(--font-sm);
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s var(--ease-out);
}

.assistant-load-more:hover:not(:disabled) {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
  border-color: var(--color-border-strong);
  transform: translateY(-1px);
}

.assistant-load-more:disabled {
  cursor: not-allowed;
  opacity: 0.6;
}

.conversation-date-divider {
  position: sticky;
  top: 0;
  z-index: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  margin: var(--space-3) 0 var(--space-2);
  pointer-events: none;
}

.conversation-date-pill {
  background: var(--material-regular);
  -webkit-backdrop-filter: var(--material-blur);
  backdrop-filter: var(--material-blur);
  color: var(--color-text-secondary);
  font-size: var(--font-xs, 12px);
  font-weight: 600;
  padding: 4px 12px;
  border-radius: var(--radius-pill, 9999px);
  border: 1px solid var(--color-border);
  pointer-events: auto;
  letter-spacing: 0.02em;
}

.load-more-spinner {
  animation: load-more-spin 0.8s linear infinite;
}

@keyframes load-more-spin {
  to { transform: rotate(360deg); }
}

.assistant-typing-row {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  margin-bottom: var(--space-5);
  max-width: 80%;
  margin-right: auto;
  animation: typing-row-enter 0.3s var(--ease-out) both;
}

@keyframes typing-row-enter {
  0% { transform: translateY(4px); opacity: 0; }
  100% { transform: translateY(0); opacity: 1; }
}

.assistant-loading-avatar {
  flex-shrink: 0;
  width: 32px;
  height: 32px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  background: var(--gradient-brand);
  box-shadow: 0 2px 8px rgba(10, 132, 255, 0.28);
}

.assistant-typing-bubble {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  background: var(--color-bg-elevated);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  border-bottom-left-radius: 4px;
  box-shadow: var(--shadow-sm);
}

.typing-dots {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.typing-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--color-accent);
  opacity: 0.4;
  animation: typing-bounce 1.2s infinite ease-in-out;
}

.typing-dot:nth-child(2) {
  animation-delay: 0.15s;
}

.typing-dot:nth-child(3) {
  animation-delay: 0.3s;
}

.typing-text {
  font-size: var(--font-sm);
  color: var(--color-text-secondary);
  font-weight: 500;
}

@keyframes typing-bounce {
  0%, 60%, 100% {
    opacity: 0.4;
    transform: scale(0.8);
  }
  30% {
    opacity: 1;
    transform: scale(1.1);
  }
}
</style>
