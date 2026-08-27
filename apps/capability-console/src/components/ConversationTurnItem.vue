<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import type { ConversationTurn, Block, DiagnosticPackage, AssistantStep, PlanSummary, RecommendationStatus, ToolCall } from '../types';
import { formatRelativeTime, formatAbsoluteTime, formatResponseType } from '../conversationFormat';
import AssistantSteps from './AssistantSteps.vue';
import BlockRenderer from './BlockRenderer.vue';
import ToolAnswerView from './ToolAnswerView.vue';
import DiagnosticView from './DiagnosticView.vue';
import MarkdownContent from './MarkdownContent.vue';
import MessageFeedbackButtons from './MessageFeedbackButtons.vue';
import ProgressTimeline from './ProgressTimeline.vue';
import SfSymbol from './SfSymbol.vue';
import { mergeExecutionProgress, mergeStepToolEntries } from '../conversationProgress';

const props = defineProps<{
  turn: ConversationTurn;
  isLast?: boolean;
  /** 当该 turn 正在流式生成时为 true，用于显示 typing 动画或闪烁光标 */
  streaming?: boolean;
  /** 该 turn 是否为最后一条用户消息（可编辑回炉重发）；由父级计算，流式期间禁用 */
  canEdit?: boolean;
}>();

const emit = defineEmits<{
  (event: 'copy', content: string): void;
  (event: 'regenerate', turn: ConversationTurn): void;
  (event: 'retry'): void;
  (event: 'edit', turn: ConversationTurn): void;
}>();

const isAssistant = computed(() => props.turn.role === 'assistant');
const canRegenerate = computed(() => isAssistant.value && props.isLast && !props.turn.error);

const roleLabel = computed(() => (isAssistant.value ? 'AI 助手' : '你'));

const relativeTime = computed(() => formatRelativeTime(props.turn.created_at));

const absoluteTime = computed(() => formatAbsoluteTime(props.turn.created_at));

const responseTypeDisplay = computed(() =>
  props.turn.response_type ? formatResponseType(props.turn.response_type) : null,
);

// 回放的 tool_step 持久化 turn：单独以步骤区块呈现，不重复渲染文字气泡
// （其 content 就是步骤摘要，与 AssistantSteps 的 summary 冗余）。
const isReplayedToolStep = computed(() => props.turn.response_type === 'tool_step');

// 流式生成中且尚无内容：显示三点 typing 动画
const showTypingDots = computed(() => Boolean(props.streaming && props.turn.content === ''));

// 流式生成中且已有内容：末尾追加闪烁光标
const showStreamingCursor = computed(() => Boolean(props.streaming && props.turn.content !== ''));

// 思考过程折叠状态：流式生成中 thinking 到达时自动展开（边到边看推理过程），
// 用户手动点击后进入手动偏好；非流式回放保持默认收起，不打扰阅读。
const thinkingExpanded = ref(false);
let thinkingManual = false;
const hasThinking = computed(() => Boolean(props.turn.thinking && props.turn.thinking.trim()));
watch(
  () => [props.streaming, props.turn.thinking],
  ([streaming, thinking]) => {
    if (streaming && thinking && !thinkingManual) {
      thinkingExpanded.value = true;
    }
  },
  { immediate: true },
);
function toggleThinking() {
  thinkingManual = true;
  thinkingExpanded.value = !thinkingExpanded.value;
}

// 工具调用折叠状态：默认展开（实时显示调用进度）
const toolCallsExpanded = ref(true);
const hasToolCalls = computed(() => Boolean(props.turn.tool_calls && props.turn.tool_calls.length > 0));

/* ---- 长回复折叠：非流式渲染完成后，若气泡高度超过阈值则默认折叠 ---- */
const LONG_REPLY_COLLAPSE_HEIGHT = 600; // px；约相当于 30+ 行正文
const bodyRef = ref<HTMLElement | null>(null);
const bodyOverflowing = ref(false);
const longReplyExpanded = ref(false);

// 只对"已完成"（非流式）的 assistant 正文测量折叠；流式期间保持完整展开，
// 否则会出现刚生成完就被截断的体验。turn 内容变化（重试/重新生成）后重新测量。
watch(
  () => [props.streaming, props.turn.content] as const,
  ([streaming]) => {
    if (streaming) {
      // 流式中：清掉旧测量，强制完整展示
      bodyOverflowing.value = false;
      return;
    }
    void nextTick(() => {
      const el = bodyRef.value;
      if (!el) {
        bodyOverflowing.value = false;
        return;
      }
      bodyOverflowing.value = el.scrollHeight > LONG_REPLY_COLLAPSE_HEIGHT;
      if (!bodyOverflowing.value) {
        longReplyExpanded.value = false;
      }
    });
  },
  { immediate: true },
);

// 进度阶段时间线：当有阶段（流式）或有回放步骤（刷新后持久化的执行证据）时显示
const hasProgress = computed(() =>
  Boolean(
    (props.turn.progress_stages && props.turn.progress_stages.length > 0) ||
    replayedSteps.value.length > 0,
  ),
);

// 回放步骤来源：刷新后 progress_stages/tool_calls 不在 turn 上，但后端持久化了
// steps（executor / agentLoop 终端 turn 的 process.steps 水合）或单步 tool_step
// （agentLoop 独立 turn），据此重建执行进度流，让进度面板在回放后仍可见。
const replayedSteps = computed<AssistantStep[]>(() => {
  const fromSteps = props.turn.steps ?? [];
  return fromSteps.length > 0 ? fromSteps : persistedStep.value ? [persistedStep.value] : [];
});

// 工具子项的单一证据来源：优先实时 tool_calls（旧 planner 路径），否则用 steps
//（executor / agentLoop 主路径，流式累积与回放水合同源）。这修复了进度面板此前
// 只从 tool_calls 取子项、导致主路径（只产 steps）显示不出 Trae 式工具流的问题。
const toolActions = computed<ToolCall[]>(() => {
  const calls = props.turn.tool_calls;
  if (calls && calls.length > 0) {
    return calls;
  }
  return (props.turn.steps ?? [])
    .filter((s) => s.tool)
    .map((s) => ({ tool: s.tool, done: s.status !== 'running' }));
});

// 合并阶段与工具执行证据为一条 Trae 式执行进度流（阶段骨架 + 工具子项）；
// 无阶段（回放单步 tool_step）时用已持久化的步骤重建工具执行流。
const progressEntries = computed(() => {
  const stages = props.turn.progress_stages ?? [];
  if (stages.length > 0) {
    return mergeExecutionProgress(stages, toolActions.value, Boolean(props.streaming));
  }
  return mergeStepToolEntries(replayedSteps.value);
});

// 已执行步骤（agent 循环）：优先用实时 SSE 累积的 steps；回放时该 turn 本身是
// 持久化的 tool_step（response_payload 含 tool/input/result/step_index/summary），
// 从 payload 重建，确保切换对话/刷新后步骤区块仍然可见。
const persistedStep = computed<AssistantStep | null>(() => {
  const payload = props.turn.response_payload;
  if (!payload || props.turn.response_type !== 'tool_step') {
    return null;
  }
  return {
    tool: typeof payload.tool === 'string' ? payload.tool : '',
    step_index: typeof payload.step_index === 'number' ? payload.step_index : 0,
    status: typeof payload.status === 'string' ? payload.status : 'done',
    summary: typeof payload.summary === 'string' ? payload.summary : undefined,
    input: isRecord(payload.input) ? payload.input as Record<string, unknown> : undefined,
    output: isRecord(payload.result) ? payload.result as Record<string, unknown> : undefined,
    error: typeof payload.error === 'string' ? payload.error : undefined,
  };
});
const hasSteps = computed(() =>
  Boolean(
    (props.turn.steps && props.turn.steps.length > 0) ||
    persistedStep.value,
  ),
);

function isRecord(v: unknown): v is Record<string, unknown> {
  return v !== null && typeof v === 'object' && !Array.isArray(v);
}

// 从持久化的 response_payload 读取 blocks（BlockRenderer 渲染 risk_notice 等）
const turnBlocks = computed<Block[]>(() => {
  const payload = props.turn.response_payload;
  if (!payload || !Array.isArray(payload.blocks)) {
    return [];
  }
  return payload.blocks as Block[];
});

// event.query / task.query 的结构化 answer（ToolAnswerView 渲染表格）
const toolAnswer = computed<{ tool: string; answer: Record<string, unknown> } | null>(() => {
  const payload = props.turn.response_payload;
  if (!payload || payload.type !== 'answer') {
    return null;
  }
  const tool = payload.tool;
  if (tool !== 'event.query' && tool !== 'task.query') {
    return null;
  }
  return { tool, answer: (payload.answer ?? {}) as Record<string, unknown> };
});

// 诊断包（DiagnosticView 渲染观察、发现、建议）
const diagnostic = computed<DiagnosticPackage | null>(() => {
  const payload = props.turn.response_payload;
  if (!payload || !payload.diagnostic) {
    return null;
  }
  return payload.diagnostic as DiagnosticPackage;
});

// 推荐落地状态（后端 Response.recommendation_plan / recommendations）：
// 展示"AI 建议操作"卡片（已建 plan 等待确认）与每条推荐的处理结果/未落地原因。
const recommendationPlan = computed<PlanSummary | null>(() => {
  const payload = props.turn.response_payload;
  if (!payload || !payload.recommendation_plan) {
    return null;
  }
  return payload.recommendation_plan as PlanSummary;
});

const recommendationStatuses = computed<RecommendationStatus[]>(() => {
  const payload = props.turn.response_payload;
  if (!payload || !Array.isArray(payload.recommendations)) {
    return [];
  }
  return payload.recommendations as RecommendationStatus[];
});
</script>

<template>
  <article
    :data-test="turn.error ? 'conversation-turn-error' : 'conversation-turn-item'"
    class="conversation-turn-item"
    :class="[turn.role, { error: turn.error }]"
  >
    <div class="conversation-turn-avatar" data-test="conversation-turn-avatar" :class="turn.role">
      <SfSymbol v-if="isAssistant" name="sparkles" :size="18" />
      <svg v-else viewBox="0 0 24 24" width="18" height="18" aria-hidden="true">
        <path
          fill="currentColor"
          d="M12 12c2.21 0 4-1.79 4-4s-1.79-4-4-4-4 1.79-4 4 1.79 4 4 4zm0 2c-2.67 0-8 1.34-8 4v2h16v-2c0-2.66-5.33-4-8-4z"
        />
      </svg>
    </div>

    <div class="conversation-turn-body">
      <header class="conversation-turn-header">
        <div class="conversation-turn-meta">
          <strong data-test="conversation-turn-role">{{ roleLabel }}</strong>
          <span
            v-if="responseTypeDisplay"
            data-test="conversation-turn-response-type"
            class="conversation-turn-badge"
            :class="`variant-${responseTypeDisplay.variant}`"
          >
            {{ responseTypeDisplay.label }}
          </span>
        </div>
        <time
          data-test="conversation-turn-time"
          class="conversation-turn-time"
          :datetime="turn.created_at"
          :title="absoluteTime"
        >
          {{ relativeTime }}
        </time>
      </header>
      <!-- 回放的 tool_step turn 没有正文：其步骤摘要以独立步骤区块呈现，这里完全
           隐藏气泡外壳，避免在顶部渲染出一个空白的对话气泡。 -->
      <div
        v-if="!isReplayedToolStep"
        data-test="conversation-turn-content"
        class="conversation-turn-content"
      >
        <SfSymbol v-if="turn.error" name="exclamationmark-triangle" :size="13" class="error-icon" />
        <template v-if="showTypingDots">
          <span class="typing-dots" aria-label="正在生成">
            <span class="typing-dot"></span>
            <span class="typing-dot"></span>
            <span class="typing-dot"></span>
          </span>
        </template>
        <template v-else>
          <!-- 长回复折叠容器：仅非流式且超高时收起，流式中始终完整展示 -->
          <div
            ref="bodyRef"
            class="turn-body-collapse"
            :class="{ collapsed: isAssistant && bodyOverflowing && !longReplyExpanded && !streaming }"
          >
            <MarkdownContent :content="turn.content" :raw="!isAssistant" />
          </div>
          <button
            v-if="isAssistant && bodyOverflowing && !streaming"
            type="button"
            data-test="long-reply-toggle"
            class="long-reply-toggle"
            @click="longReplyExpanded = !longReplyExpanded"
          >
            {{ longReplyExpanded ? '收起' : '展开全文' }}
            <svg viewBox="0 0 24 24" width="13" height="13" aria-hidden="true" :class="{ flip: longReplyExpanded }">
              <path fill="currentColor" d="M7.41 8.59L12 13.17l4.59-4.58L18 10l-6 6-6-6z" />
            </svg>
          </button>
          <span v-if="showStreamingCursor" class="streaming-cursor" aria-hidden="true">▌</span>
        </template>
      </div>

      <!-- 思考过程折叠区：仅在 assistant turn 有 thinking 内容时显示 -->
      <div
        v-if="isAssistant && hasThinking"
        data-test="conversation-turn-thinking"
        class="thinking-section"
      >
        <button
          type="button"
          class="thinking-toggle"
          :aria-expanded="thinkingExpanded"
          @click="toggleThinking"
        >
          <SfSymbol name="sparkles" :size="16" />
          <span>思考过程</span>
          <span class="thinking-chevron" :class="{ expanded: thinkingExpanded }">
            <svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">
              <path fill="currentColor" d="M7.41 8.59L12 13.17l4.59-4.58L18 10l-6 6-6-6z" />
            </svg>
          </span>
        </button>
        <div v-show="thinkingExpanded" class="thinking-content">
          {{ turn.thinking }}
        </div>
      </div>

      <!-- 工具调用追踪区：实时显示工具调用进度 -->
      <div
        v-if="isAssistant && hasToolCalls"
        data-test="conversation-turn-tool-calls"
        class="tool-calls-section"
      >
        <button
          type="button"
          class="tool-calls-toggle"
          :aria-expanded="toolCallsExpanded"
          @click="toolCallsExpanded = !toolCallsExpanded"
        >
          <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
          </svg>
          <span>已调用 {{ turn.tool_calls?.length || 0 }} 个受控工具获取平台事实</span>
          <span class="tool-calls-chevron" :class="{ expanded: toolCallsExpanded }">
            <svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">
              <path fill="currentColor" d="M7.41 8.59L12 13.17l4.59-4.58L18 10l-6 6-6-6z" />
            </svg>
          </span>
        </button>
        <div v-show="toolCallsExpanded" class="tool-calls-content">
          <div
            v-for="(tc, index) in turn.tool_calls"
            :key="index"
            class="tool-call-item"
            :class="{ done: tc.done }"
          >
            <div class="tool-call-header">
              <span class="tool-call-name">{{ tc.tool }}</span>
              <span v-if="!tc.done" class="tool-call-status calling">调用中...</span>
              <span v-else class="tool-call-status done">已完成</span>
            </div>
            <div v-if="tc.input" class="tool-call-input">
              <div class="tool-call-label">输入:</div>
              <pre class="tool-call-json">{{ JSON.stringify(tc.input, null, 2) }}</pre>
            </div>
            <div v-if="tc.done && tc.raw_response" class="tool-call-output">
              <div class="tool-call-label">输出:</div>
              <pre class="tool-call-json">{{ JSON.stringify(tc.raw_response, null, 2) }}</pre>
            </div>
          </div>
        </div>
      </div>

      <!-- 执行进度流：阶段骨架 + 工具子项（参考 Trae 的实时工具调用流） -->
      <ProgressTimeline
        v-if="isAssistant && hasProgress"
        :entries="progressEntries"
        :streaming="streaming"
      />

      <!-- 已执行步骤区块：智能体自治循环多步执行（实时 SSE 或回放的 tool_step 持久化 turn） -->
      <AssistantSteps
        v-if="isAssistant && hasSteps"
        :steps="turn.steps && turn.steps.length ? turn.steps : [persistedStep!]"
        :streaming="streaming"
      />

      <!-- 结构化 blocks（如 Runbook 自动执行的 risk_notice）与 event/task 表格 -->
      <div v-if="isAssistant && turnBlocks.length" data-test="conversation-turn-blocks">
        <BlockRenderer :blocks="turnBlocks" />
      </div>
      <ToolAnswerView v-if="isAssistant && toolAnswer" :tool="toolAnswer.tool" :answer="toolAnswer.answer" />
      <DiagnosticView v-if="isAssistant && diagnostic" :diagnostic="diagnostic" />

      <!-- 推荐落地状态：AI 建议的操作卡片 + 每条推荐处理结果/未落地原因 -->
      <div v-if="isAssistant && (recommendationPlan || recommendationStatuses.length)" class="recommendation-status" data-test="recommendation-status">
        <article v-if="recommendationPlan" class="recommendation-plan-card">
          <h4>AI 建议操作（待确认）</h4>
          <p>工具：<code>{{ recommendationPlan.tool }}</code>（风险 {{ recommendationPlan.risk }}）
            <template v-if="recommendationPlan.plan_id"> · plan {{ recommendationPlan.plan_id }}</template>
          </p>
        </article>
        <ul v-if="recommendationStatuses.length" class="recommendation-list">
          <li
            v-for="r in recommendationStatuses"
            :key="r.tool + r.status"
            class="recommendation-item"
            :class="`status-${r.status}`"
          >
            <code>{{ r.tool }}</code>
            <span v-if="r.summary">：{{ r.summary }}</span>
            <template v-if="r.status === 'plan_created'">
              <span class="badge tag-success">已建 plan 待确认{{ r.plan_id ? '（' + r.plan_id + '）' : '' }}</span>
            </template>
            <template v-else-if="r.status === 'read_executed'">
              <span class="badge tag-info">已执行</span>
            </template>
            <template v-else>
              <span class="badge tag-error">未落地</span>
              <span v-if="r.reason" class="recommendation-reason">{{ r.reason }}</span>
            </template>
          </li>
        </ul>
      </div>

      <div class="conversation-turn-actions">
        <button
          v-if="turn.error"
          type="button"
          data-test="conversation-turn-retry"
          class="action-button retry-button"
          :title="'重试'"
          @click="emit('retry')"
        >
          <svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">
            <path
              fill="currentColor"
              d="M17.65 6.35A7.95 7.95 0 0012 4c-4.42 0-7.99 3.58-7.99 8s3.57 8 7.99 8c3.73 0 6.84-2.55 7.73-6h-2.08A5.99 5.99 0 0112 18c-3.31 0-6-2.69-6-6s2.69-6 6-6c1.66 0 3.14.69 4.22 1.78L13 11h7V4l-2.35 2.35z"
            />
          </svg>
          <span>重试</span>
        </button>

        <button
          type="button"
          data-test="conversation-turn-copy"
          class="action-button"
          :title="'复制内容'"
          @click="emit('copy', turn.content)"
        >
          <svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">
            <path
              fill="currentColor"
              d="M16 1H4c-1.1 0-2 .9-2 2v14h2V3h12V1zm3 4H8c-1.1 0-2 .9-2 2v14c0 1.1.9 2 2 2h11c1.1 0-2-.9-2-2V7c0-1.1-.9-2-2-2zm0 16H8V7h11v14z"
            />
          </svg>
          <span>复制</span>
        </button>

        <button
          v-if="canRegenerate"
          type="button"
          data-test="conversation-turn-regenerate"
          class="action-button"
          :title="'重新生成'"
          @click="emit('regenerate', turn)"
        >
          <svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">
            <path
              fill="currentColor"
              d="M17.65 6.35A7.95 7.95 0 0012 4c-4.42 0-7.99 3.58-7.99 8s3.57 8 7.99 8c3.73 0 6.84-2.55 7.73-6h-2.08A5.99 5.99 0 0112 18c-3.31 0-6-2.69-6-6s2.69-6 6-6c1.66 0 3.14.69 4.22 1.78L13 11h7V4l-2.35 2.35z"
            />
          </svg>
          <span>重新生成</span>
        </button>

        <button
          v-if="canEdit"
          type="button"
          data-test="conversation-turn-edit"
          class="action-button"
          :title="'编辑本条'"
          @click="emit('edit', turn)"
        >
          <svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">
            <path
              fill="currentColor"
              d="M3 17.25V21h3.75L17.81 9.94l-3.75-3.75L3 17.25zM20.71 7.04a1 1 0 000-1.41l-2.34-2.34a1 1 0 00-1.41 0l-1.83 1.83 3.75 3.75 1.83-1.83z"
            />
          </svg>
          <span>编辑</span>
        </button>

        <MessageFeedbackButtons
          v-if="isAssistant && turn.id && turn.conversation_id && !turn.error"
          :turn-id="turn.id"
          :conversation-id="turn.conversation_id"
        />
      </div>
    </div>
  </article>
</template>

<style scoped>
/* ---- 长回复折叠：收起时限高 + 底部渐隐，提示还有更多内容 ---- */
.turn-body-collapse.collapsed {
  max-height: 600px;
  overflow: hidden;
  position: relative;
}

.turn-body-collapse.collapsed::after {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 72px;
  background: linear-gradient(transparent, var(--turn-bubble-bg, transparent) 85%);
  pointer-events: none;
}

.long-reply-toggle {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-top: var(--space-2);
  padding: 2px 10px;
  background: transparent;
  border: none;
  border-radius: var(--radius-pill);
  color: var(--color-accent);
  font-size: var(--font-sm);
  font-weight: 500;
  cursor: pointer;
  transition: background 0.15s var(--ease-out);
}

.long-reply-toggle:hover {
  background: color-mix(in srgb, var(--color-accent) 10%, transparent);
}

.long-reply-toggle svg {
  transition: transform 0.15s var(--ease-out);
}

.long-reply-toggle svg.flip {
  transform: rotate(180deg);
}
</style>

<style scoped>
.recommendation-status {
  margin-top: var(--space-3);
  display: grid;
  gap: var(--space-2);
}

.recommendation-plan-card {
  border: 1px solid var(--color-border, #d0d7de);
  border-radius: 8px;
  padding: var(--space-3);
  background: var(--color-surface-2, #f6f8fa);
}

.recommendation-plan-card h4 {
  margin: 0 0 var(--space-2);
  font-size: 0.95rem;
}

.recommendation-plan-card p {
  margin: 0;
  font-size: 0.9rem;
}

.recommendation-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: var(--space-1);
}

.recommendation-item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: 0.88rem;
  padding: var(--space-1) 0;
}

.recommendation-item code {
  font-size: 0.85em;
}

.recommendation-reason {
  color: var(--color-danger, #cf222e);
  font-size: 0.82em;
}

.conversation-turn-item {
  display: flex;
  gap: var(--space-3);
  margin-bottom: var(--space-4);
  max-width: 86%;
  animation: turn-enter 0.3s var(--ease-out) both;
}

@keyframes turn-enter {
  0% { transform: translateY(4px); opacity: 0; }
  100% { transform: translateY(0); opacity: 1; }
}

.conversation-turn-item.user {
  flex-direction: row-reverse;
  margin-left: auto;
}

.conversation-turn-item.assistant {
  margin-right: auto;
  /* 助手轮封顶 800px（略宽于正文气泡 720px，但不再铺满整列 882px——满宽太冲）。
     width:100% 必须显式写死：transcript 的 align-items:normal(stretch) 会被本轮的
     margin-right:auto（交叉轴 auto 外边距）禁用，届时整轮退化为随内容收缩的
     fit-content，流式初期内容少时步骤/时间线块会窄成一列。width:100% + max-width
     封顶 = 流式全程恒定 800px，不收缩也不铺满。 */
  max-width: min(800px, 100%);
  width: 100%;
}

/* 助手轮的"宽数据"执行块（步骤清单、已调用工具、结构化答案/诊断卡、推荐落地、
   Runbook 块）撑满整轮宽度；正文气泡由 .conversation-turn-content 单独封顶
   min(720px, 100%) 保持可读，不受此限。 */
.conversation-turn-item.assistant .assistant-steps,
.conversation-turn-item.assistant .tool-calls-section,
.conversation-turn-item.assistant .thinking-section,
.conversation-turn-item.assistant .conversation-turn-blocks,
.conversation-turn-item.assistant .tool-answer-view,
.conversation-turn-item.assistant .diagnostic-view,
.conversation-turn-item.assistant .progress-timeline,
.conversation-turn-item.assistant .recommendation-status {
  align-self: stretch;
}

.conversation-turn-avatar {
  flex-shrink: 0;
  width: 32px;
  height: 32px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  box-shadow: var(--shadow-sm);
}

.conversation-turn-avatar.user {
  background: var(--color-bg-hover);
  color: var(--color-text-secondary);
}

.conversation-turn-avatar.assistant {
  background: var(--gradient-brand);
  color: #fff;
}

.conversation-turn-body {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.conversation-turn-item.user .conversation-turn-body {
  align-items: flex-end;
}

.conversation-turn-item.assistant .conversation-turn-body {
  align-items: flex-start;
  /* 填满整轮宽度：turn 已 width:100%，若 body 仍按内容收缩（flex 默认
     basis:auto），步骤/时间线块 align-self:stretch 只能撑到 body 的内容宽，
     依旧窄一列。flex:1 让 body 吃掉整轮剩余宽度，执行块才有稳定全宽舞台。 */
  flex: 1 1 auto;
}

.conversation-turn-header {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: 4px;
  font-size: var(--font-sm);
  width: 100%;
}

.conversation-turn-item.user .conversation-turn-header {
  flex-direction: row-reverse;
}

.conversation-turn-meta {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.conversation-turn-header strong {
  font-weight: 600;
  color: var(--color-text-secondary);
  font-size: var(--font-sm);
}

.conversation-turn-time {
  color: var(--color-text-tertiary);
  font-size: var(--font-xs, 12px);
}

.conversation-turn-badge {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: var(--radius-pill);
  font-size: var(--font-sm);
  font-weight: 600;
  background: var(--color-bg-elevated);
  color: var(--color-text-secondary);
  border: none;
  letter-spacing: 0.02em;
  white-space: nowrap;
}

.conversation-turn-badge.variant-answer {
  background: var(--color-success-soft);
  color: var(--color-success);
}

.conversation-turn-badge.variant-converged {
  background: var(--color-warning-soft);
  color: var(--color-warning);
}

.conversation-turn-badge.variant-clarification {
  background: var(--color-warning-soft);
  color: var(--color-warning);
}

.conversation-turn-badge.variant-confirmation {
  background: var(--color-danger-soft);
  color: var(--color-danger);
}

.conversation-turn-badge.variant-execution {
  background: var(--color-accent-soft);
  color: var(--color-accent);
}

.conversation-turn-badge.variant-error {
  background: var(--color-danger-soft);
  color: var(--color-danger);
}

.conversation-turn-badge.variant-default {
  background: var(--color-bg-hover);
  color: var(--color-text-secondary);
  border-color: var(--color-border);
}

/* iMessage 风格气泡 */
.conversation-turn-content {
  font-size: var(--font-base);
  color: var(--color-text-primary);
  line-height: 1.6;
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius-xl);
  background: var(--color-bg-elevated);
  border: 1px solid var(--color-border);
  word-break: break-word;
  overflow-wrap: anywhere;
  box-shadow: var(--shadow-sm);
  transition: transform 0.15s var(--ease-out);
  /* 防止代码块/长行把气泡撑出容器，导致整页出现横向滚动条 */
  min-width: 0;
  /* 正文封顶 ~720px，保证大屏下长句不拉过宽；步骤/工具执行块不受此限 */
  max-width: min(720px, 100%);
}

/* markdown 代码块：超宽时块内横向滚动，而不是撑开消息气泡 */
.conversation-turn-content :deep(pre) {
  max-width: 100%;
  overflow-x: auto;
  white-space: pre;
}

.conversation-turn-content :deep(pre code) {
  white-space: pre;
}

.conversation-turn-item.user .conversation-turn-content {
  background: var(--color-accent-soft);
  color: var(--color-text-primary);
  border-color: transparent;
  border-bottom-right-radius: 4px;
}

.conversation-turn-item.assistant .conversation-turn-content {
  background: var(--color-bg-elevated);
  border-color: transparent;
  border-bottom-left-radius: 4px;
}

.conversation-turn-item.error .conversation-turn-content {
  background: var(--color-danger-soft);
  border-color: rgba(255, 69, 58, 0.3);
  color: var(--color-danger);
}

/* user 气泡内的链接和代码保持可读 */
.conversation-turn-item.user .conversation-turn-content :deep(a) {
  color: var(--color-accent);
  text-decoration: underline;
}

.conversation-turn-item.user .conversation-turn-content :deep(code) {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.error-icon {
  margin-right: 6px;
  font-weight: 700;
}

.retry-button {
  color: var(--color-danger);
}

.retry-button:hover {
  background: var(--color-danger-soft);
  border-color: rgba(255, 69, 58, 0.3);
  color: var(--color-danger);
}

.typing-dots {
  display: inline-flex;
  gap: 4px;
  align-items: center;
  padding: 2px 0;
}

.typing-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--color-accent);
  animation: typing-bounce 1.2s infinite ease-in-out;
}

.typing-dot:nth-child(2) {
  animation-delay: 0.15s;
}

.typing-dot:nth-child(3) {
  animation-delay: 0.3s;
}

@keyframes typing-bounce {
  0%, 60%, 100% {
    transform: scale(0.8);
    opacity: 0.4;
  }
  30% {
    transform: scale(1.1);
    opacity: 1;
  }
}

.streaming-cursor {
  display: inline-block;
  margin-left: 2px;
  color: var(--color-accent);
  font-weight: 600;
  animation: streaming-blink 0.8s steps(2) infinite;
}

@keyframes streaming-blink {
  0%, 50% { opacity: 1; }
  51%, 100% { opacity: 0; }
}

.conversation-turn-actions {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  margin-top: 4px;
  opacity: 0;
  transition: opacity 0.2s var(--ease-out);
}

.conversation-turn-item:hover .conversation-turn-actions,
.conversation-turn-actions:focus-within {
  opacity: 1;
}

.conversation-turn-item.user .conversation-turn-actions {
  justify-content: flex-end;
}

.conversation-turn-item.assistant .conversation-turn-actions {
  justify-content: flex-start;
}

.action-button {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 10px;
  background: var(--material-thin);
  -webkit-backdrop-filter: var(--material-blur);
  backdrop-filter: var(--material-blur);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-pill, 9999px);
  color: var(--color-text-tertiary);
  font-size: var(--font-xs, 12px);
  font-weight: 500;
  cursor: pointer;
  transition: all 0.15s var(--ease-out);
}

.action-button:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
  border-color: var(--color-border-strong);
}

.action-button:active {
  transform: scale(0.96);
}

.action-button:focus-visible {
  outline: 2px solid var(--color-accent);
  outline-offset: 2px;
}

/* 工具调用追踪区 */
.tool-calls-section {
  margin-top: var(--space-2);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  background: var(--color-bg);
  overflow: hidden;
}

.tool-calls-toggle {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 0.5rem 1rem;
  background: transparent;
  border: none;
  color: var(--color-text-secondary);
  font-size: var(--font-base);
  font-weight: 600;
  cursor: pointer;
  transition: color 0.15s var(--ease-out);
}

.tool-calls-toggle:hover {
  color: var(--color-text-primary);
}

.tool-calls-chevron {
  margin-left: auto;
  display: inline-flex;
  transition: transform 0.2s var(--ease-out);
}

.tool-calls-chevron.expanded {
  transform: rotate(180deg);
}

.tool-calls-content {
  padding: 0 12px 12px;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.tool-call-item {
  padding: 10px 14px;
  background: var(--color-bg-elevated);
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border);
}

.tool-call-item.done {
  border-color: var(--color-success);
  background: var(--color-success-soft);
}

.tool-call-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 6px;
}

.tool-call-name {
  font-weight: 600;
  color: var(--color-text-primary);
  font-size: var(--font-base);
}

.tool-call-status {
  font-size: var(--font-sm);
  padding: 2px 10px;
  border-radius: var(--radius-pill);
  font-weight: 500;
}

.tool-call-status.calling {
  background: var(--color-warning-soft);
  color: var(--color-warning);
  animation: pulse 1.5s ease-in-out infinite;
}

.tool-call-status.done {
  background: var(--color-success-soft);
  color: var(--color-success);
}

@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.6; }
}

.tool-call-input,
.tool-call-output {
  margin-top: 6px;
}

.tool-call-label {
  font-size: var(--font-sm);
  color: var(--color-text-tertiary);
  margin-bottom: 4px;
  font-weight: 500;
}

.tool-call-json {
  font-size: var(--font-sm);
  color: var(--color-text-secondary);
  background: var(--color-bg);
  padding: 10px 12px;
  border-radius: var(--radius-sm);
  overflow-x: auto;
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
  font-family: var(--font-mono);
  line-height: 1.5;
}

/* 思考过程折叠区 */
.thinking-section {
  margin-top: var(--space-2);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  background: var(--color-bg);
  overflow: hidden;
}

.thinking-toggle {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 0.5rem 1rem;
  background: transparent;
  border: none;
  color: var(--color-text-secondary);
  font-size: var(--font-base);
  font-weight: 600;
  cursor: pointer;
  transition: color 0.15s var(--ease-out);
}

.thinking-toggle:hover {
  color: var(--color-text-primary);
}

.thinking-chevron {
  margin-left: auto;
  display: inline-flex;
  transition: transform 0.2s var(--ease-out);
}

.thinking-chevron.expanded {
  transform: rotate(180deg);
}

.thinking-content {
  padding: 0 1rem 1rem;
  font-size: var(--font-base);
  color: var(--color-text-secondary);
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
