import { computed, ref } from 'vue';
import type { Ref } from 'vue';
import { getPendingPlan, sendAssistantMessage } from '../api';
import { streamAssistantMessage } from './useAssistantStream';
import type { PageContext } from './useAssistantStream';
import type {
  AssistantConsoleResponse,
  Block,
  DiagnosticPackage,
  ExecutionResult,
  PendingPlanDetail as PendingPlanDetailType,
} from '../types';
import type { ConversationTurn } from '../types';

export type AssistantDetailStatus =
  | '等待请求'
  | '正在请求'
  | '请求失败'
  | '需要补充参数'
  | '需要审批'
  | '已返回答案'
  | '执行结果'
  | '兜底总结（未完整推理）'
  | '响应详情'
  | '已停止';

export interface AssistantEntryMessage {
  role: 'user' | 'assistant';
  text: string;
  response?: unknown;
}

export interface UseAssistantOptions {
  activeConversationID: Ref<string | null>;
  conversationTurns: Ref<ConversationTurn[]>;
  planTokens: Ref<Record<string, string>>;
  pendingPlans: Ref<{ id: string }[]>;
  refreshConversationTurns: (conversationID: string) => Promise<void>;
  refreshPendingPlans: () => Promise<void>;
  refreshAuditEvents: () => Promise<void>;
  onInlineConfirmed?: (result: ExecutionResult) => void;
}

export interface UseAssistant {
  assistantInput: Ref<string>;
  assistantMessages: Ref<AssistantEntryMessage[]>;
  assistantLatestResponse: Ref<unknown>;
  assistantLatestStatus: Ref<AssistantDetailStatus>;
  assistantEntryLoading: Ref<boolean>;
  assistantPageContext: Ref<PageContext | null>;
  setAssistantPageContext: (ctx: PageContext | null) => void;
  assistantEntryError: Ref<string>;
  assistantDiagnostic: Ref<DiagnosticPackage | null>;
  assistantBlocks: Ref<Block[]>;
  assistantInlinePlan: Ref<PendingPlanDetailType | null>;
  assistantInlinePlanLoading: Ref<boolean>;
  assistantInlineError: Ref<string>;
  lastFailedAssistantMessage: Ref<string>;
  assistantInlineConfirmationToken: Ref<string | undefined>;
  latestDetailText: Ref<string>;
  send: () => Promise<void>;
  retry: () => Promise<void>;
  regenerate: (turn: ConversationTurn) => Promise<void>;
  stop: () => void;
  fillPrompt: (prompt: string) => void;
  loadInlinePlan: (planID: string) => Promise<void>;
  handleInlineConfirmed: (result: ExecutionResult) => void;
  handleInlineError: (message: string) => void;
  clearInlinePlan: () => void;
  resetForNewConversation: () => void;
  resetForSwitchConversation: () => void;
}

function isObjectRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object';
}

function compactJSON(value: unknown): string {
  try {
    return JSON.stringify(value) || '';
  } catch {
    return '无法解析响应';
  }
}

function stringValue(value: unknown): string {
  if (typeof value === 'string') {
    return value.trim();
  }
  if (typeof value === 'number' || typeof value === 'boolean') {
    return String(value);
  }
  return '';
}

function assistantResponseSummary(response: unknown): string {
  const fallback = compactJSON(response);
  if (!isObjectRecord(response)) {
    return fallback;
  }
  if (response.type === 'answer') {
    if (response.answer && typeof response.answer === 'object') {
      return stringValue((response.answer as Record<string, unknown>).summary) || compactJSON(response.answer);
    }
    return fallback;
  }
  if (response.type === 'answer_converged') {
    // 兜底/收敛结论：展示具体 message（由后端 stepsAnswer 生成），并加前缀
    // 让操作员一眼看出这不是模型给出的最终结论。
    const message = stringValue((response as Record<string, unknown>).message);
    return message ? `[兜底总结] ${message}` : fallback;
  }
  if (response.type === 'clarification_needed') {
    return stringValue(response.message) || fallback;
  }
  if (response.type === 'confirmation_required') {
    return `需要审批：${stringValue(response.summary) || fallback}`;
  }
  if (response.type === 'execution_result') {
    return stringValue(response.status) ? `执行状态：${response.status}` : fallback;
  }
  return fallback;
}

function assistantDetailText(response: unknown): string {
  if (response === undefined) {
    return '暂无调用详情';
  }
  if (isObjectRecord(response) && response.type === 'clarification_needed') {
    return `需要补充参数\n${JSON.stringify(response, null, 2)}`;
  }
  return JSON.stringify(response, null, 2);
}

// Feature flag: when true, use SSE streaming for assistant responses so
// the user sees incremental text instead of waiting for the full response.
// Tests can set this to false via setStreamingEnabled(false) to use the
// simpler one-shot fetch path that existing test mocks expect.
let streamingEnabled = true;

/** Override the streaming mode (primarily for tests). */
export function setStreamingEnabled(enabled: boolean): void {
  streamingEnabled = enabled;
}

function assistantResponseStatus(response: unknown): AssistantDetailStatus {
  if (!isObjectRecord(response)) {
    return '响应详情';
  }
  if (response.type === 'answer') {
    return '已返回答案';
  }
  if (response.type === 'answer_converged') {
    return '兜底总结（未完整推理）';
  }
  if (response.type === 'clarification_needed') {
    return '需要补充参数';
  }
  if (response.type === 'confirmation_required') {
    return '需要审批';
  }
  if (response.type === 'execution_result') {
    return '执行结果';
  }
  return '响应详情';
}

export function useAssistant(options: UseAssistantOptions): UseAssistant {
  const {
    activeConversationID,
    conversationTurns,
    planTokens,
    pendingPlans,
    refreshConversationTurns,
    refreshPendingPlans,
    refreshAuditEvents,
    onInlineConfirmed,
  } = options;

  const assistantInput = ref('');
  const assistantMessages = ref<AssistantEntryMessage[]>([]);
  const assistantLatestResponse = ref<unknown>();
  const assistantLatestStatus = ref<AssistantDetailStatus>('等待请求');
  const assistantEntryLoading = ref(false);
  const activeAbortController = ref<AbortController | null>(null);
  // 页面上下文带入。由外部（如 management 视图选中 capability 跳转）通过
  // setAssistantPageContext 设置，send/retry/regenerate 自动携带，无需每个调用点显式传。
  const assistantPageContext = ref<PageContext | null>(null);
  function setAssistantPageContext(ctx: PageContext | null) {
    assistantPageContext.value = ctx;
  }
  const assistantEntryError = ref('');
  const assistantDiagnostic = ref<DiagnosticPackage | null>(null);
  const assistantBlocks = ref<Block[]>([]);
  const assistantInlinePlan = ref<PendingPlanDetailType | null>(null);
  const assistantInlinePlanLoading = ref(false);
  const assistantInlineError = ref('');
  const lastFailedAssistantMessage = ref('');

  const assistantInlineConfirmationToken = computed(() => {
    const plan = assistantInlinePlan.value;
    if (!plan) return undefined;
    return planTokens.value[plan.id];
  });

  const latestDetailText = computed(() => assistantDetailText(assistantLatestResponse.value));

  async function submitAssistantMessage(message: string) {
    if (assistantEntryLoading.value) {
      return;
    }
    // 组装 pageContext：仅当跳转设置了 assistantPageContext 时才启用 page_context。
    const pageContext: PageContext | undefined = assistantPageContext.value
      ? {
          domain: assistantPageContext.value.domain,
          resource_type: assistantPageContext.value.resource_type,
          resource_name: assistantPageContext.value.resource_name,
        }
      : undefined;
    assistantEntryLoading.value = true;
    assistantEntryError.value = '';
    lastFailedAssistantMessage.value = '';
    assistantLatestStatus.value = '正在请求';
    assistantLatestResponse.value = undefined;
    // 发送新消息时清除未决审批（旧计划卡片不应横在新请求之上）
    assistantInlinePlan.value = null;
    assistantInlineError.value = '';
    // 发送新消息时只清理本次会话的瞬时错误气泡（local-* id），保留后端已持久化的
    // 失败 turn——它们是会话历史，刷新后仍应可见（用户无需再翻审计记录）。
    conversationTurns.value = conversationTurns.value.filter((t) => !t.error || t.id.startsWith('local-'));
    assistantMessages.value.push({ role: 'user', text: message });
    const conversationID = activeConversationID.value ?? undefined;
    // Optimistically append a user turn to the transcript so the UI feels
    // responsive while waiting for the assistant response.
    const optimisticUserTurn: ConversationTurn = {
      id: `local-user-${Date.now()}`,
      conversation_id: activeConversationID.value ?? '',
      role: 'user',
      content: message,
      created_at: new Date().toISOString(),
    };
    conversationTurns.value.push(optimisticUserTurn);

    // For streaming: push an empty assistant turn that will be updated with
    // each delta chunk, so the user sees incremental text immediately.
    const streamingTurnId = `local-assistant-${Date.now()}`;
    if (streamingEnabled) {
      conversationTurns.value.push({
        id: streamingTurnId,
        conversation_id: activeConversationID.value ?? '',
        role: 'assistant',
        content: '',
        created_at: new Date().toISOString(),
      });
    }

    const controller = new AbortController();
    activeAbortController.value = controller;

    function applyResponse(response: AssistantConsoleResponse) {
      assistantLatestResponse.value = response;
      assistantLatestStatus.value = assistantResponseStatus(response);
      // 用响应内容填充本地 assistant turn（流式或非流式都走这里）。
      // 只有对象响应才读 message/summary；null/原始值原样交给 detail 面板。
      const content = (response !== null && typeof response === 'object' && (response as any).message != null)
        ? String((response as any).message)
        : ((response !== null && typeof response === 'object' && (response as any).summary != null)
            ? String((response as any).summary)
            : '');
      const localTurn = conversationTurns.value.find((t) => t.id === streamingTurnId);
      if (localTurn) {
        localTurn.content = content;
      } else if (content) {
        // 流式 turn 不存在（可能被刷新掉了），创建一个新的
        const convID = (response !== null && typeof response === 'object' && (response as any).conversation_id != null)
          ? String((response as any).conversation_id)
          : activeConversationID.value || '';
        conversationTurns.value.push({
          id: `local-assistant-${Date.now()}`,
          conversation_id: convID,
          role: 'assistant',
          content,
          created_at: new Date().toISOString(),
        });
      }
      if (isObjectRecord(response) && typeof response.conversation_id === 'string') {
        activeConversationID.value = response.conversation_id;
      }
      if (isObjectRecord(response) && response.type === 'answer' && response.diagnostic) {
        assistantDiagnostic.value = response.diagnostic as DiagnosticPackage;
      } else {
        assistantDiagnostic.value = null;
      }
      // clarification_needed 也可能携带 blocks（缺参澄清的 approval_form 表单），
      // 实时渲染可点选表单而非等刷新后从持久化 payload 读取。
      if (isObjectRecord(response) && (response.type === 'answer' || response.type === 'execution_result' || response.type === 'clarification_needed') && Array.isArray(response.blocks)) {
        assistantBlocks.value = response.blocks as Block[];
      } else {
        assistantBlocks.value = [];
      }
      if (isObjectRecord(response) && response.type === 'execution_result') {
        // Runbook 自动执行（借鉴-5）：把 assistant execution_result 响应映射为
        // ExecutionResult（runbook/execution_id/reused 从 answer 取），交给
        // onInlineConfirmed 渲染 ExecutionResultView。
        const answer = (response.answer ?? {}) as Record<string, unknown>;
        onInlineConfirmed?.({
          type: 'execution_result',
          plan_id: response.plan_id,
          execution_id: typeof answer.execution_id === 'string' ? answer.execution_id : '',
          status: response.status,
          reused: Boolean(answer.reused),
          runbook: typeof answer.runbook === 'string' ? answer.runbook : undefined,
          verification: response.verification,
        });
        assistantInlinePlan.value = null;
        assistantInlineError.value = '';
      }
      if (isObjectRecord(response) && response.type === 'confirmation_required' && response.confirmation_token) {
        planTokens.value = { ...planTokens.value, [response.plan_id]: response.confirmation_token };
        void refreshPendingPlans();
        void loadInlinePlan(response.plan_id as string);
        void refreshAuditEvents();
      } else if (isObjectRecord(response) && response.type === 'confirmation_required' && response.plan_id) {
        void loadInlinePlan(response.plan_id as string);
        void refreshAuditEvents();
      } else {
        assistantInlinePlan.value = null;
        assistantInlineError.value = '';
        void refreshAuditEvents();
      }
      assistantMessages.value.push({
        role: 'assistant',
        text: assistantResponseSummary(response),
        response,
      });
      if (isObjectRecord(response) && typeof response.conversation_id === 'string') {
        void refreshConversationTurns(response.conversation_id);
      }
    }

    function handleStreamError(errMessage: string) {
      assistantEntryError.value = errMessage;
      assistantLatestStatus.value = '请求失败';
      assistantLatestResponse.value = { error: errMessage };
      assistantMessages.value.push({ role: 'assistant', text: errMessage });
      // 保留 optimisticUserTurn，让用户看到「我问了什么→AI 失败了」的上下文。
      // 将 streaming turn 转为错误气泡；非流式模式下新建一个错误 turn。
      const errorContent = `AI 助手请求失败：${errMessage}`;
      const existing = conversationTurns.value.find((t) => t.id === streamingTurnId);
      if (existing) {
        existing.content = errorContent;
        existing.error = true;
      } else {
        conversationTurns.value.push({
          id: streamingTurnId,
          conversation_id: activeConversationID.value ?? '',
          role: 'assistant',
          content: errorContent,
          created_at: new Date().toISOString(),
          error: true,
        });
      }
      lastFailedAssistantMessage.value = message;
    }

    try {
      if (streamingEnabled) {
        await streamAssistantMessage(
          { message, conversationID, pageContext },
          controller.signal,
          {
            onDelta: (chunk) => {
              const turn = conversationTurns.value.find((t) => t.id === streamingTurnId);
              if (turn) {
                turn.content += chunk;
              }
            },
            onThinking: (chunk) => {
              const turn = conversationTurns.value.find((t) => t.id === streamingTurnId);
              if (turn) {
                turn.thinking = (turn.thinking || '') + chunk;
              }
            },
            onToolCall: (info) => {
              const turn = conversationTurns.value.find((t) => t.id === streamingTurnId);
              if (turn) {
                turn.tool_calls = turn.tool_calls || [];
                const existing = turn.tool_calls.find((tc) => tc.tool === info.tool && !tc.done);
                if (existing) {
                  existing.done = info.done;
                  existing.raw_response = info.raw_response;
                } else {
                  turn.tool_calls.push(info);
                }
              }
            },
            onStep: (step) => {
              const turn = conversationTurns.value.find((t) => t.id === streamingTurnId);
              if (turn) {
                turn.steps = turn.steps || [];
                // step_index 消歧同一工具的多步调用：按序号去重，重复到达视为更新。
                const idx = turn.steps.findIndex((s) => s.step_index === step.step_index);
                if (idx >= 0) {
                  turn.steps[idx] = step;
                } else {
                  turn.steps.push(step);
                  turn.steps.sort((a, b) => a.step_index - b.step_index);
                }
              }
            },
            onProgress: (stage) => {
              const turn = conversationTurns.value.find((t) => t.id === streamingTurnId);
              if (turn) {
                turn.progress_stages = turn.progress_stages || [];
                turn.progress_stages.push(stage);
              }
            },
            onFinal: (response) => {
              // Replace the streaming turn with the real response. The
              // refreshConversationTurns call in applyResponse will fetch
              // the persisted turn from the backend, replacing the local one.
              applyResponse(response);
            },
            onError: (msg) => {
              handleStreamError(msg);
            },
          },
        );
      } else {
        const response = await sendAssistantMessage(message, conversationID, controller.signal, pageContext);
        applyResponse(response);
      }
    } catch (err) {
      const aborted = err instanceof DOMException && err.name === 'AbortError';
      if (aborted) {
        assistantLatestStatus.value = '已停止';
        // Keep the partial streaming content so the user can see what was
        // generated before they stopped. Remove the streaming turn marker
        // so it doesn't get confused with a real turn on refresh.
        const turn = conversationTurns.value.find((t) => t.id === streamingTurnId);
        if (turn && !turn.content) {
          conversationTurns.value = conversationTurns.value.filter((t) => t.id !== streamingTurnId);
        }
      } else {
        const errMsg = err instanceof Error ? err.message : 'AI 助手请求失败';
        handleStreamError(errMsg);
      }
    } finally {
      activeAbortController.value = null;
      assistantEntryLoading.value = false;
    }
  }

  function stop() {
    if (activeAbortController.value) {
      activeAbortController.value.abort();
      activeAbortController.value = null;
      assistantEntryLoading.value = false;
      assistantLatestStatus.value = '已停止';
    }
  }

  async function send() {
    const rawMessage = assistantInput.value.trim();
    if (!rawMessage || assistantEntryLoading.value) {
      return;
    }
    assistantInput.value = '';
    await submitAssistantMessage(rawMessage);
  }

  async function retry() {
    const message = lastFailedAssistantMessage.value;
    if (!message || assistantEntryLoading.value) {
      return;
    }
    await submitAssistantMessage(message);
  }

  async function regenerate(turn: ConversationTurn) {
    if (assistantEntryLoading.value) {
      return;
    }
    const turns = conversationTurns.value;
    const index = turns.findIndex((t) => t.id === turn.id);
    if (index <= 0) {
      return;
    }
    const userTurn = turns[index - 1];
    if (userTurn.role !== 'user') {
      return;
    }
    // Drop the target assistant turn, its preceding user turn, and any later
    // turns so the conversation can be replayed from that user message.
    conversationTurns.value = turns.slice(0, index - 1);
    lastFailedAssistantMessage.value = '';
    await submitAssistantMessage(userTurn.content);
  }

  function fillPrompt(prompt: string) {
    assistantInput.value = prompt;
  }

  async function loadInlinePlan(planID: string) {
    assistantInlinePlanLoading.value = true;
    assistantInlineError.value = '';
    try {
      assistantInlinePlan.value = await getPendingPlan(planID);
    } catch (err) {
      assistantInlineError.value = err instanceof Error ? err.message : '加载计划详情失败';
      assistantInlinePlan.value = null;
    } finally {
      assistantInlinePlanLoading.value = false;
    }
  }

  function handleInlineConfirmed(result: ExecutionResult) {
    if (assistantInlinePlan.value) {
      const planID = assistantInlinePlan.value.id;
      pendingPlans.value = pendingPlans.value.filter((plan) => plan.id !== planID);
      const cleared = { ...planTokens.value };
      delete cleared[planID];
      planTokens.value = cleared;
    }
    assistantInlinePlan.value = null;
    onInlineConfirmed?.(result);
    void refreshAuditEvents();
  }

  function handleInlineError(message: string) {
    assistantInlineError.value = message;
  }

  // 编辑上一条消息回炉重发前调用：清掉与该回复绑定的未决审批
  function clearInlinePlan() {
    assistantInlinePlan.value = null;
    assistantInlineError.value = '';
  }

  function resetForNewConversation() {
    assistantMessages.value = [];
    assistantLatestResponse.value = undefined;
    assistantLatestStatus.value = '等待请求';
    assistantDiagnostic.value = null;
    assistantInlinePlan.value = null;
    assistantInlineError.value = '';
    lastFailedAssistantMessage.value = '';
    assistantInput.value = '';
  }

  function resetForSwitchConversation() {
    assistantMessages.value = [];
    assistantLatestResponse.value = undefined;
    assistantLatestStatus.value = '等待请求';
    assistantDiagnostic.value = null;
    assistantInlinePlan.value = null;
    assistantInlineError.value = '';
  }

  return {
    assistantInput,
    assistantMessages,
    assistantLatestResponse,
    assistantLatestStatus,
    assistantEntryLoading,
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
    latestDetailText,
    send,
    retry,
    regenerate,
    stop,
    fillPrompt,
    loadInlinePlan,
    handleInlineConfirmed,
    handleInlineError,
    clearInlinePlan,
    resetForNewConversation,
    resetForSwitchConversation,
  };
}
