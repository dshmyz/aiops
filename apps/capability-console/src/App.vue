<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onMounted, onUnmounted, ref } from 'vue';
import { useTheme } from './composables/useTheme';
import { useAuditEvents } from './composables/useAuditEvents';
import { usePendingPlans } from './composables/usePendingPlans';
import { useConversations } from './composables/useConversations';
import { useAssistant } from './composables/useAssistant';
import { useScheduledTasks } from './composables/useScheduledTasks';
import { useMCPServers } from './composables/useMCPServers';
import { useCapabilities } from './composables/useCapabilities';
import PendingPlanDetail from './components/PendingPlanDetail.vue';
import AssistantInlineConfirm from './components/AssistantInlineConfirm.vue';
import AssistantTranscript from './components/AssistantTranscript.vue';
import AssistantTraceView from './components/AssistantTraceView.vue';
const AuditView = defineAsyncComponent(() => import('./views/AuditView.vue'));
const PlansView = defineAsyncComponent(() => import('./views/PlansView.vue'));
const ScheduledTasksView = defineAsyncComponent(() => import('./views/ScheduledTasksView.vue'));
const McpServersView = defineAsyncComponent(() => import('./views/McpServersView.vue'));
import ManagementView from './views/ManagementView.vue';
const AdminPromptsView = defineAsyncComponent(() => import('./views/AdminPromptsView.vue'));
const AdminKnowledgeView = defineAsyncComponent(() => import('./views/AdminKnowledgeView.vue'));
const FeedbackView = defineAsyncComponent(() => import('./views/FeedbackView.vue'));
import AssistantSuggestions from './components/AssistantSuggestions.vue';
import CapabilityStatusBadge from './components/CapabilityStatusBadge.vue';
import BlockRenderer from './components/BlockRenderer.vue';
import ConversationSidebar from './components/ConversationSidebar.vue';
import DiagnosticView from './components/DiagnosticView.vue';
import ExecutionResultView from './components/ExecutionResultView.vue';
import ToolAnswerView from './components/ToolAnswerView.vue';
import ScheduledTaskBadge from './components/ScheduledTaskBadge.vue';
import NavIcon from './components/NavIcon.vue';
import SfSymbol from './components/SfSymbol.vue';
import SlashCommandPanel from './components/SlashCommandPanel.vue';
import type { SlashCommand } from './components/SlashCommandPanel.vue';
import type {
  AssistantTrace,
  ExecutionResult,
} from './types';

type ActiveView = 'assistant' | 'management' | 'plans' | 'audit' | 'scheduled-tasks' | 'prompts' | 'knowledge' | 'feedback' | 'mcp-servers';

const activeView = ref<ActiveView>('assistant');

// 侧栏折叠状态
const sidebarCollapsed = ref(false);

// 视图顺序与快捷键映射（Cmd/Ctrl+1..9），顺序与侧栏视觉分组一致
const viewOrder: ActiveView[] = ['assistant', 'management', 'plans', 'scheduled-tasks', 'audit', 'prompts', 'knowledge', 'feedback', 'mcp-servers'];

function handleGlobalKeydown(event: KeyboardEvent) {
  // Cmd/Ctrl + 数字 切换视图
  if ((event.metaKey || event.ctrlKey) && /^[1-9]$/.test(event.key)) {
    const active = document.activeElement;
    // 在输入框/文本域中不拦截快捷键
    if (active && (active.tagName === 'INPUT' || active.tagName === 'TEXTAREA' || (active as HTMLElement).isContentEditable)) {
      return;
    }
    event.preventDefault();
    const index = Number(event.key) - 1;
    if (viewOrder[index]) {
      activeView.value = viewOrder[index];
    }
  }
}

const capabilitiesComposable = useCapabilities({
  onViewChange: (view) => {
    activeView.value = view;
  },
});

const { isDarkTheme, toggle: toggleTheme, init: initTheme } = useTheme();

// 1. audit composable（无依赖）
const auditComposable = useAuditEvents();
const {
  auditEvents,
  auditEventsLoading,
  auditEventsError,
  auditFilter,
  auditNextCursor,
  auditLoadingMore,
  auditSearchQuery,
  refresh: refreshAuditEvents,
  loadMore: loadMoreAuditEvents,
  applyFilter: applyAuditFilter,
  search: searchAuditLog,
} = auditComposable;

// 2. planTokens（跨视图共享，App.vue 持有）+ plans composable
const planTokens = ref<Record<string, string>>({});
const plansComposable = usePendingPlans({
  planTokens,
  onConfirmed: () => refreshAuditEvents(),
});
const {
  pendingPlans,
  pendingPlansLoading,
  pendingPlansError,
  selectedPlanID,
  selectedPlanDetail,
  selectedPlanLoading,
  latestExecutionResult,
  refresh: refreshPendingPlans,
  select: selectPendingPlan,
  handleConfirmed: handlePlanConfirmed,
  handleError: handlePlanError,
} = plansComposable;

// 3. conversations composable
const conversationsComposable = useConversations();
const {
  filteredConversations,
  searchQuery,
  archivedView,
  activeConversationID,
  conversationsLoading,
  conversationTurns,
  conversationHasMore,
  conversationLoadingMore,
  conversationOldestTurnID,
  refresh: refreshConversations,
  setArchivedView,
  refreshTurns: refreshConversationTurns,
  loadMore: loadMoreConversationTurns,
  archive: archiveConversationEntry,
} = conversationsComposable;

// 4. assistant composable（依赖 conversations + plans + audit）
const assistantComposable = useAssistant({
  activeConversationID,
  conversationTurns,
  planTokens,
  pendingPlans,
  refreshConversationTurns,
  refreshPendingPlans,
  refreshAuditEvents,
  onInlineConfirmed: (result) => {
    latestExecutionResult.value = result;
  },
});
const {
  assistantInput,
  assistantMessages,
  assistantLatestResponse,
  assistantLatestStatus,
  assistantEntryLoading,
  assistantEnvironment,
  assistantPageContext,
  setAssistantPageContext,
  assistantEntryError,
  assistantDiagnostic,
  assistantBlocks,
  assistantInlinePlan,
  assistantInlinePlanLoading,
  assistantInlineError,
  lastFailedAssistantMessage,
  assistantInlineConfirmationToken,
  latestDetailText: assistantLatestDetailText,
  send: sendAssistantEntryMessage,
  retry: retryLastAssistantMessage,
  regenerate: regenerateAssistantMessage,
  stop: stopAssistantEntry,
  fillPrompt: fillAssistantPrompt,
  loadInlinePlan: loadAssistantInlinePlan,
  handleInlineConfirmed: handleAssistantInlineConfirmed,
  handleInlineError: handleAssistantInlineError,
  resetForNewConversation: resetAssistantForNewConversation,
  resetForSwitchConversation: resetAssistantForSwitchConversation,
} = assistantComposable;

// 缺口-3：从 management 视图携带 capability 上下文跳转 assistant。
// 设置 pageContext（domain/resource_type）后切到 assistant 视图，用户发送时自动携带。
function handleAskAi(pageContext: { domain?: string; resource_type?: string; resource_name?: string }) {
  setAssistantPageContext(pageContext);
  activeView.value = 'assistant';
}
function clearAssistantPageContext() {
  setAssistantPageContext(null);
}

// 定时巡检任务相关状态
const scheduledTasksComposable = useScheduledTasks({
  readCapabilities: capabilitiesComposable.capabilities,
});
const {
  scheduledTasks,
  scheduledTaskFailures,
  scheduledTaskFormOpen,
  scheduledTaskEditing,
  scheduledTaskViewingRunsFor,
  scheduledTaskRuns,
  scheduledTasksLoading,
  refresh: refreshScheduledTasks,
  refreshFailures: refreshScheduledTaskFailures,
  openForm: openScheduledTaskForm,
  editTask: editScheduledTask,
  closeForm: closeScheduledTaskForm,
  save: saveScheduledTask,
  remove: deleteScheduledTaskById,
  triggerNow: triggerScheduledTaskById,
  toggleEnabled: toggleScheduledTaskEnabled,
  viewRuns: viewRunHistory,
  closeRuns: closeRunHistory,
} = scheduledTasksComposable;

// MCP 服务器热配置：onMounted 自动加载列表，视图切换无需额外触发
const mcpServersComposable = useMCPServers();

const assistantTrace = computed<AssistantTrace | null | undefined>(() => {
  const response = assistantLatestResponse.value;
  if (!isObjectRecord(response)) {
    return null;
  }
  const trace = response.trace;
  if (!isObjectRecord(trace)) {
    return null;
  }
  return trace as AssistantTrace;
});

// event.query / task.query 的结构化 answer，供 ToolAnswerView 渲染表格。
const assistantToolAnswer = computed<{ tool: string; answer: Record<string, unknown> } | null>(() => {
  const response = assistantLatestResponse.value;
  if (!isObjectRecord(response) || response.type !== 'answer') {
    return null;
  }
  const tool = response.tool;
  if (tool !== 'event.query' && tool !== 'task.query') {
    return null;
  }
  return { tool, answer: (response.answer ?? {}) as Record<string, unknown> };
});

async function selectConversation(conversationID: string) {
  // 先重置 assistant 视图状态，再委托 composable 处理 conversation 状态
  resetAssistantForSwitchConversation();
  await conversationsComposable.select(conversationID);
}

function startNewConversation() {
  conversationsComposable.startNew();
  resetAssistantForNewConversation();
}

async function handleArchiveConversation(conversationID: string) {
  await archiveConversationEntry(conversationID, (message) => {
    assistantEntryError.value = message;
  });
}

const copyNotice = ref('');
let copyNoticeTimer: ReturnType<typeof setTimeout> | null = null;

async function handleCopyTurn(content: string) {
  try {
    await navigator.clipboard.writeText(content);
    copyNotice.value = '已复制到剪贴板';
  } catch {
    copyNotice.value = '复制失败';
  }
  if (copyNoticeTimer) {
    clearTimeout(copyNoticeTimer);
  }
  copyNoticeTimer = setTimeout(() => {
    copyNotice.value = '';
  }, 2000);
}

async function jumpToPlanFromAudit(planID: string) {
  activeView.value = 'plans';
  await refreshPendingPlans();
  await selectPendingPlan(planID);
}

function isObjectRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object';
}

// 快捷指令面板：从已发布能力提取 domain，生成指令列表
const slashVisible = ref(false);
const slashIndex = ref(0);
const assistantTextareaRef = ref<HTMLTextAreaElement | null>(null);

// 所有可用指令（全量）
const allSlashCommands = computed<SlashCommand[]>(() => {
  const domains = new Map<string, number>();
  for (const cap of capabilitiesComposable.capabilities.value) {
    if (cap.source === 'published' && cap.domain) {
      domains.set(cap.domain, (domains.get(cap.domain) ?? 0) + 1);
    }
  }
  const labels: Record<string, string> = {
    minio: '查询 MinIO 存储状态',
    kafka: '查询 Kafka 消费延迟',
    glusterfs: '查询 GlusterFS 卷健康',
  };
  return Array.from(domains.keys()).map((domain) => ({
    name: `/${domain}`,
    description: labels[domain] ?? `查询 ${domain} 状态`,
  }));
});

// 当前输入的 `/xxx` 前缀（不含 /）
const slashQuery = computed(() => {
  const m = assistantInput.value.match(/\/([a-z]*)$/);
  return m ? m[1] : '';
});

// 按前缀过滤后的指令列表
const slashCommands = computed<SlashCommand[]>(() => {
  const q = slashQuery.value;
  if (!q) return allSlashCommands.value;
  return allSlashCommands.value.filter((cmd) => cmd.name.slice(1).startsWith(q));
});

function openSlashPanel() {
  slashVisible.value = true;
  slashIndex.value = 0;
}

function closeSlashPanel() {
  slashVisible.value = false;
  slashIndex.value = 0;
}

function focusTextareaEnd() {
  void nextTick(() => {
    const ta = assistantTextareaRef.value;
    if (!ta) return;
    ta.focus();
    const end = ta.value.length;
    ta.setSelectionRange(end, end);
  });
}

function selectSlashCommand(command: SlashCommand) {
  const domain = command.name.slice(1);
  // 把当前输入中的 `/xxx` 替换为完整模板
  assistantInput.value = assistantInput.value.replace(/\/[a-z]*$/, `检查 ${domain} `);
  closeSlashPanel();
  focusTextareaEnd();
}

function handleAssistantKeydown(event: KeyboardEvent) {
  // 指令面板打开时：方向键导航，Enter 选择，Escape 关闭
  if (slashVisible.value) {
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      slashIndex.value = (slashIndex.value + 1) % Math.max(slashCommands.value.length, 1);
      return;
    }
    if (event.key === 'ArrowUp') {
      event.preventDefault();
      slashIndex.value = (slashIndex.value - 1 + slashCommands.value.length) % Math.max(slashCommands.value.length, 1);
      return;
    }
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      const command = slashCommands.value[slashIndex.value];
      if (command) {
        selectSlashCommand(command);
      }
      return;
    }
    if (event.key === 'Escape') {
      event.preventDefault();
      closeSlashPanel();
      return;
    }
  }

  // 输入 `/` 在行首或空输入时触发指令面板（无已发布能力则跳过）
  if (
    event.key === '/' &&
    !slashVisible.value &&
    allSlashCommands.value.length > 0 &&
    (assistantInput.value === '' || assistantInput.value.endsWith('\n'))
  ) {
    // 不阻止默认行为，让 `/` 输入到 textarea，下一帧打开面板
    setTimeout(() => {
      if (assistantInput.value === '/' || assistantInput.value.endsWith('\n/')) {
        openSlashPanel();
      }
    }, 0);
    return;
  }

  // 面板打开时输入字符：检查是否仍是 `/xxx` 模式，否则关闭
  if (slashVisible.value && event.key.length === 1 && event.key !== '/') {
    setTimeout(() => {
      const m = assistantInput.value.match(/\/[a-z]*$/);
      if (!m) {
        closeSlashPanel();
      } else {
        // 重置索引避免越界
        if (slashIndex.value >= slashCommands.value.length) {
          slashIndex.value = 0;
        }
        // 如果过滤后无指令，也关闭
        if (slashCommands.value.length === 0) {
          closeSlashPanel();
        }
      }
    }, 0);
    return;
  }

  if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
    event.preventDefault();
    if (!assistantEntryLoading.value && assistantInput.value.trim() !== '') {
      void sendAssistantEntryMessage();
    }
  }
}

// textarea 失焦时关闭面板（点击外部即关闭）
function handleTextareaBlur() {
  // 延迟一帧，避免与面板点击事件冲突
  setTimeout(() => {
    if (slashVisible.value) {
      closeSlashPanel();
    }
  }, 150);
}

// 面板上的 mousedown 阻止 textarea 失焦
function handleSlashPanelMousedown(event: MouseEvent) {
  // 点击指令项时阻止默认行为，避免 textarea 失焦
  event.preventDefault();
}

onMounted(() => {
  initTheme();
  capabilitiesComposable.loadCapabilities();
  refreshPendingPlans();
  refreshAuditEvents();
  refreshConversations();
  // 定时巡检：加载任务列表（失败 badge 轮询由 useScheduledTasks 内部管理）
  void refreshScheduledTasks();
  window.addEventListener('keydown', handleGlobalKeydown);
});

onUnmounted(() => {
  window.removeEventListener('keydown', handleGlobalKeydown);
});
</script>

<template>
  <main :class="['capability-console', 'app-shell', activeView === 'management' ? 'studio-mode' : 'assistant-mode', { 'app-shell--collapsed': sidebarCollapsed }]">
    <aside class="app-nav" aria-label="产品入口">
      <div class="app-brand">
        <strong v-if="!sidebarCollapsed">AI 运维 Copilot</strong>
        <span v-if="!sidebarCollapsed">中间件智能运维</span>
        <button
          data-test="nav-collapse-toggle"
          class="nav-collapse-toggle"
          :title="sidebarCollapsed ? '展开侧栏' : '折叠侧栏'"
          @click="sidebarCollapsed = !sidebarCollapsed"
        >
          <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
            <path fill="currentColor" d="M3 6h18v2H3V6zm0 5h18v2H3v-2zm0 5h18v2H3v-2z" />
          </svg>
        </button>
      </div>

      <div data-test="nav-section" class="nav-section">
        <p data-test="nav-section-label" class="nav-section-label" v-if="!sidebarCollapsed">运维</p>
        <button
          data-test="nav-assistant"
          data-view="assistant"
          class="nav-item"
          :class="{ active: activeView === 'assistant' }"
          :title="'AI 运维助手 (Cmd+1)'"
          @click="activeView = 'assistant'"
        >
          <NavIcon name="assistant" />
          <span v-if="!sidebarCollapsed">AI 运维助手</span>
        </button>
        <button
          data-test="nav-management"
          data-view="management"
          class="nav-item"
          :class="{ active: activeView === 'management' }"
          :title="'能力接入管理 (Cmd+2)'"
          @click="activeView = 'management'"
        >
          <NavIcon name="management" />
          <span v-if="!sidebarCollapsed">能力接入管理</span>
        </button>
        <button
          data-test="nav-plans"
          data-view="plans"
          class="nav-item"
          :class="{ active: activeView === 'plans' }"
          :title="'待确认计划 (Cmd+3)'"
          @click="activeView = 'plans'"
        >
          <NavIcon name="plans" />
          <span v-if="!sidebarCollapsed">待确认计划</span>
          <span v-if="pendingPlans.length > 0 && !sidebarCollapsed" data-test="nav-badge" class="nav-badge">{{ pendingPlans.length }}</span>
        </button>
        <button
          data-test="nav-scheduled-tasks"
          data-view="scheduled-tasks"
          class="nav-item"
          :class="{ active: activeView === 'scheduled-tasks' }"
          :title="'定时巡检 (Cmd+4)'"
          @click="activeView = 'scheduled-tasks'"
        >
          <NavIcon name="scheduled-tasks" />
          <span v-if="!sidebarCollapsed">定时巡检</span>
          <ScheduledTaskBadge v-if="!sidebarCollapsed" :count="scheduledTaskFailures" />
        </button>
      </div>

      <div data-test="nav-section" class="nav-section">
        <p data-test="nav-section-label" class="nav-section-label" v-if="!sidebarCollapsed">管理配置</p>
        <button
          data-test="nav-audit"
          data-view="audit"
          class="nav-item"
          :class="{ active: activeView === 'audit' }"
          :title="'审计记录 (Cmd+5)'"
          @click="activeView = 'audit'"
        >
          <NavIcon name="audit" />
          <span v-if="!sidebarCollapsed">审计记录</span>
          <span v-if="auditEvents.length > 0 && !sidebarCollapsed" data-test="nav-badge" class="nav-badge">{{ auditEvents.length }}</span>
        </button>
        <button
          data-test="nav-prompts"
          data-view="prompts"
          class="nav-item"
          :class="{ active: activeView === 'prompts' }"
          :title="'Prompt 管理 (Cmd+6)'"
          @click="activeView = 'prompts'"
        >
          <NavIcon name="prompts" />
          <span v-if="!sidebarCollapsed">Prompt 管理</span>
        </button>
        <button
          data-test="nav-knowledge"
          data-view="knowledge"
          class="nav-item"
          :class="{ active: activeView === 'knowledge' }"
          :title="'知识库 (Cmd+7)'"
          @click="activeView = 'knowledge'"
        >
          <NavIcon name="knowledge" />
          <span v-if="!sidebarCollapsed">知识库</span>
        </button>
        <button
          data-test="nav-feedback"
          data-view="feedback"
          class="nav-item"
          :class="{ active: activeView === 'feedback' }"
          :title="'用户反馈 (Cmd+8)'"
          @click="activeView = 'feedback'"
        >
          <NavIcon name="feedback" />
          <span v-if="!sidebarCollapsed">用户反馈</span>
        </button>
        <button
          data-test="nav-mcp-servers"
          data-view="mcp-servers"
          class="nav-item"
          :class="{ active: activeView === 'mcp-servers' }"
          :title="'MCP 服务器管理 (Cmd+9)'"
          @click="activeView = 'mcp-servers'"
        >
          <NavIcon name="mcp-servers" />
          <span v-if="!sidebarCollapsed">MCP 服务器</span>
        </button>
      </div>

      <div style="flex: 1"></div>
      <button
        data-test="theme-toggle-sidebar"
        class="nav-item"
        :title="isDarkTheme ? '切换到浅色模式' : '切换到深色模式'"
        @click="toggleTheme"
      >
        {{ isDarkTheme ? '☀️' : '🌙' }}
      </button>
    </aside>

    <section class="app-main">
      <section v-if="activeView === 'assistant'" data-test="assistant-entry" data-view="assistant" class="assistant-entry">
        <header class="assistant-hero">
          <div class="assistant-hero__icon" aria-hidden="true">
            <SfSymbol name="sparkles" :size="28" />
          </div>
          <p class="eyebrow">AI 运维助手</p>
          <h1>问 AI 排查中间件状态</h1>
          <p>用自然语言查询 MinIO、Kafka、GlusterFS，AI 会通过已发布能力调用现有后台 API。</p>
        </header>

        <CapabilityStatusBadge
          :published-count="capabilitiesComposable.stats.value.published"
          @go-to-management="activeView = 'management'"
        />

        <section class="assistant-workspace">
          <ConversationSidebar
            :conversations="filteredConversations"
            :activeConversationID="activeConversationID"
            :loading="conversationsLoading"
            v-model:searchQuery="searchQuery"
            :archivedView="archivedView"
            @update:archivedView="setArchivedView"
            @select="selectConversation"
            @archive="handleArchiveConversation"
            @new="startNewConversation"
          />

          <section class="assistant-chat" aria-label="AI 运维对话">
            <AssistantTranscript
              :turns="conversationTurns"
              :loading="assistantEntryLoading"
              :hasMore="conversationHasMore"
              :loadingMore="conversationLoadingMore"
              :hide-empty="conversationTurns.length === 0 && !assistantEntryLoading"
              @load-more="loadMoreConversationTurns"
              @copy="handleCopyTurn"
              @regenerate="regenerateAssistantMessage"
              @retry="retryLastAssistantMessage"
            />
            <div
              v-if="copyNotice"
              class="copy-notice"
              role="status"
              aria-live="polite"
            >
              {{ copyNotice }}
            </div>
            <div
              v-if="conversationTurns.length === 0 && !assistantEntryLoading && capabilitiesComposable.stats.value.published === 0"
              data-test="assistant-capability-guidance"
              class="assistant-capability-guidance"
            >
              <span class="guidance-icon" aria-hidden="true">⚠</span>
              <div class="guidance-body">
                <strong>没有可用的 AI 工具</strong>
                <small>需要先在能力管理中发布至少一个能力，AI 才能响应提问</small>
              </div>
              <button
                data-test="assistant-capability-guidance-action"
                class="guidance-action"
                @click="activeView = 'management'"
              >
                去发布能力
              </button>
            </div>
            <AssistantSuggestions
              v-else-if="conversationTurns.length === 0 && !assistantEntryLoading"
              @pick="fillAssistantPrompt"
            />
            <div v-if="assistantEntryError" class="assistant-error" data-test="assistant-error" role="alert">
            <svg class="assistant-error-icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
              <path fill="currentColor" d="M8 1a7 7 0 100 14A7 7 0 008 1zm0 3a1 1 0 110 2 1 1 0 010-2zm0 3a1 1 0 011 1v4a1 1 0 11-2 0V8a1 1 0 011-1z"/>
            </svg>
            <span class="assistant-error-text">AI 响应失败，详见对话</span>
              <button
                v-if="lastFailedAssistantMessage"
                data-test="assistant-retry"
                type="button"
                class="assistant-retry-button"
                :disabled="assistantEntryLoading"
                @click="retryLastAssistantMessage"
              >
                重试
              </button>
            </div>
            <label class="assistant-input-label">
              <span>自然语言请求</span>
              <div class="assistant-input-wrap">
                <SlashCommandPanel
                  :commands="slashCommands"
                  :visible="slashVisible"
                  :selectedIndex="slashIndex"
                  @select="selectSlashCommand"
                  @close="closeSlashPanel"
                  @mousedown="handleSlashPanelMousedown"
                />
                <textarea
                  ref="assistantTextareaRef"
                  data-test="assistant-input"
                  v-model="assistantInput"
                  class="assistant-input"
                  rows="4"
                  placeholder="输入中间件问题，如「检查 minio prod 容量」。输入 / 快速选择能力"
                  @keydown="handleAssistantKeydown"
                  @blur="handleTextareaBlur"
                />
              </div>
            </label>
            <div class="assistant-input-toolbar">
              <div
                v-if="assistantPageContext && (assistantPageContext.domain || assistantPageContext.resource_type)"
                data-test="assistant-page-context-badge"
                class="assistant-page-context-badge"
              >
                <span class="ctx-label">上下文</span>
                <span class="ctx-value">
                  {{ assistantPageContext.domain }}<template v-if="assistantPageContext.resource_type">/{{ assistantPageContext.resource_type }}</template>
                </span>
                <button
                  data-test="assistant-page-context-clear"
                  type="button"
                  class="ctx-clear"
                  aria-label="清除页面上下文"
                  @click="clearAssistantPageContext"
                >×</button>
              </div>
              <div class="assistant-env-field">
                <span class="assistant-env-label">目标环境</span>
                <select
                  data-test="assistant-env-selector"
                  v-model="assistantEnvironment"
                  class="env-selector"
                  aria-label="目标环境"
                >
                  <option value="prod">prod</option>
                  <option value="staging">staging</option>
                  <option value="dev">dev</option>
                  <option value="none">不指定</option>
                </select>
              </div>
              <div class="assistant-input-actions">
                <span data-test="assistant-input-hint" class="input-hint">
                  <kbd>Enter</kbd> 发送 · <kbd>Shift+Enter</kbd> 换行
                </span>
                <button
                  v-if="!assistantEntryLoading"
                  data-test="assistant-send"
                  class="primary-inline"
                  :disabled="assistantInput.trim() === ''"
                  @click="sendAssistantEntryMessage"
                >
                  发送
                </button>
                <button
                  v-else
                  data-test="assistant-stop"
                  class="stop-button"
                  @click="stopAssistantEntry"
                >
                  停止生成
                </button>
              </div>
            </div>
          </section>

          <aside class="assistant-detail" aria-label="本次能力调用">
            <div class="group-title">
              <h2>本次调用</h2>
              <span data-test="assistant-detail-status">{{ assistantLatestStatus }}</span>
            </div>
            <AssistantInlineConfirm
              v-if="assistantInlinePlan"
              :plan="assistantInlinePlan"
              :confirmationToken="assistantInlineConfirmationToken"
              :loading="assistantInlinePlanLoading"
              @confirmed="handleAssistantInlineConfirmed"
              @error="handleAssistantInlineError"
            />
            <p v-if="assistantInlineError" class="error-text">{{ assistantInlineError }}</p>
            <ExecutionResultView v-if="latestExecutionResult" :result="latestExecutionResult" />
            <DiagnosticView v-if="assistantDiagnostic" :diagnostic="assistantDiagnostic" />
            <BlockRenderer v-if="assistantBlocks.length > 0" :blocks="assistantBlocks" />
            <ToolAnswerView
              v-if="assistantToolAnswer"
              :tool="assistantToolAnswer.tool"
              :answer="assistantToolAnswer.answer"
            />
            <AssistantTraceView :trace="assistantTrace" />
            <pre data-test="assistant-latest-detail">{{ assistantLatestDetailText }}</pre>
          </aside>
        </section>
      </section>

      <ManagementView
        v-show="activeView === 'management'"
        :capabilities="capabilitiesComposable"
        @ask-ai="handleAskAi"
      />

      <PlansView
        v-if="activeView === 'plans'"
        :plans="plansComposable"
        :plan-tokens="planTokens"
      />

      <AuditView
        v-if="activeView === 'audit'"
        :audit="auditComposable"
        @jump-to-plan="jumpToPlanFromAudit"
      />

      <ScheduledTasksView
        v-if="activeView === 'scheduled-tasks'"
        :scheduled-tasks="scheduledTasksComposable"
      />

      <AdminPromptsView v-if="activeView === 'prompts'" />

      <AdminKnowledgeView v-if="activeView === 'knowledge'" />

      <FeedbackView v-if="activeView === 'feedback'" @go-to-assistant="activeView = 'assistant'" />

      <McpServersView
        v-if="activeView === 'mcp-servers'"
        :mcp-servers="mcpServersComposable"
      />
    </section>

  </main>
</template>
