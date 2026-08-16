import { computed, ref } from 'vue';
import type { ComputedRef, Ref } from 'vue';
import {
  archiveConversation,
  getConversation,
  listConversations,
} from '../api';
import type {
  ConversationRole,
  ConversationSummary,
  ConversationTurn,
  TurnProcess,
} from '../types';

// 流式生成期间只在内存 turn 上累积、后端持久化 turn 不含的瞬时字段。
// refreshTurns 从后端拿回持久化 turn 后会丢失这些过程内容，需在替换时合并回去。
type TransientFields = Pick<ConversationTurn, 'steps' | 'tool_calls' | 'progress_stages' | 'thinking'>;
const TRANSIENT_KEYS: (keyof TransientFields)[] = ['steps', 'tool_calls', 'progress_stages', 'thinking'];

// hasTransientContent 判断 turn 是否携带了值得保留的瞬时过程内容。
function hasTransientContent(turn: ConversationTurn): boolean {
  return TRANSIENT_KEYS.some((key) => {
    const value = turn[key];
    if (Array.isArray(value)) return value.length > 0;
    return typeof value === 'string' ? value.trim() !== '' : value != null;
  });
}

// snapshotTransientByRole 按角色收集当前列表里的瞬时过程内容（同一角色取最后一次）。
function snapshotTransientByRole(
  turns: ConversationTurn[],
): Map<ConversationRole, Partial<TransientFields>> {
  const snapshot = new Map<ConversationRole, Partial<TransientFields>>();
  for (const turn of turns) {
    if (!hasTransientContent(turn)) continue;
    const patch = snapshot.get(turn.role) ?? {};
    for (const key of TRANSIENT_KEYS) {
      const value = turn[key];
      if (value != null) {
        (patch as Record<string, unknown>)[key] = value;
      }
    }
    snapshot.set(turn.role, patch);
  }
  return snapshot;
}

// mergeTransient 把瞬时时序内容合并回刷新后的持久化 turn 列表。
// 瞬时字段只出现在最新一条流式 turn 上，而刷新结果以同一 user+assistant 对结尾，
// 因此从列表末尾按角色合并一次即可命中目标（避免同名角色多处错配）。
function mergeTransient(
  refreshed: ConversationTurn[],
  snapshot: Map<ConversationRole, Partial<TransientFields>>,
): ConversationTurn[] {
  if (snapshot.size === 0) return refreshed;
  const applied = new Set<ConversationRole>();
  return refreshed.map((turn) => {
    if (applied.has(turn.role)) return turn;
    const patch = snapshot.get(turn.role);
    if (!patch) return turn;
    applied.add(turn.role);
    return { ...turn, ...patch };
  });
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return v !== null && typeof v === 'object' && !Array.isArray(v);
}

// processFromPayload 读取后端持久化的过程证据（turn 的 response_payload.process）。
// executor 流式路径把思考文本与已执行步骤随 turn 落库，刷新/换设备后回读据此复原。
function processFromPayload(turn: ConversationTurn): TurnProcess | undefined {
  const payload = turn.response_payload;
  if (!isRecord(payload) || !isRecord(payload.process)) {
    return undefined;
  }
  return payload.process as TurnProcess;
}

// hydrateProcess 把后端持久化的 process 填回瞬态字段（thinking/steps），仅当顶层
// 缺席时补——流式期间内存里的实时值优先，二者内容本就同源。
function hydrateProcess(turn: ConversationTurn): ConversationTurn {
  const process = processFromPayload(turn);
  if (!process) return turn;
  const next: ConversationTurn = { ...turn };
  if (!next.thinking && typeof process.thinking === 'string' && process.thinking.trim()) {
    next.thinking = process.thinking;
  }
  if ((!next.steps || next.steps.length === 0) && Array.isArray(process.steps) && process.steps.length) {
    next.steps = process.steps;
  }
  return next;
}

function hydrateTurns(turns: ConversationTurn[]): ConversationTurn[] {
  return turns.map(hydrateProcess);
}

export type ArchivedView = 'active' | 'archived';

export interface UseConversations {
  conversations: Ref<ConversationSummary[]>;
  filteredConversations: ComputedRef<ConversationSummary[]>;
  searchQuery: Ref<string>;
  archivedView: Ref<ArchivedView>;
  activeConversationID: Ref<string | null>;
  conversationsLoading: Ref<boolean>;
  conversationTurns: Ref<ConversationTurn[]>;
  conversationHasMore: Ref<boolean>;
  conversationLoadingMore: Ref<boolean>;
  conversationOldestTurnID: Ref<string | null>;
  refresh: () => Promise<void>;
  setArchivedView: (view: ArchivedView) => void;
  select: (conversationID: string) => Promise<void>;
  refreshTurns: (conversationID: string) => Promise<void>;
  loadMore: () => Promise<void>;
  startNew: () => void;
  archive: (conversationID: string, onError?: (message: string) => void) => Promise<void>;
}

export function useConversations(): UseConversations {
  const conversations = ref<ConversationSummary[]>([]);
  const activeConversationID = ref<string | null>(null);
  const conversationsLoading = ref(false);
  const conversationTurns = ref<ConversationTurn[]>([]);
  const conversationHasMore = ref(false);
  const conversationLoadingMore = ref(false);
  const conversationOldestTurnID = ref<string | null>(null);
  const searchQuery = ref('');
  const archivedView = ref<ArchivedView>('active');

  const filteredConversations = computed(() => {
    const query = searchQuery.value.trim().toLowerCase();
    if (!query) return conversations.value;
    return conversations.value.filter((conv) => {
      const title = conv.title?.toLowerCase() ?? '';
      const preview = conv.last_message_preview?.toLowerCase() ?? '';
      return title.includes(query) || preview.includes(query);
    });
  });

  async function refresh() {
    conversationsLoading.value = true;
    try {
      const page = await listConversations({
        archived: archivedView.value === 'archived',
      });
      conversations.value = page.conversations ?? [];
    } catch {
      // Silently ignore conversation list errors; the user can still send
      // messages in a new ad-hoc conversation.
      conversations.value = [];
    } finally {
      conversationsLoading.value = false;
    }
  }

  function setArchivedView(view: ArchivedView) {
    if (archivedView.value === view) return;
    archivedView.value = view;
    searchQuery.value = '';
    void refresh();
  }

  async function select(conversationID: string) {
    if (activeConversationID.value === conversationID) {
      return;
    }
    activeConversationID.value = conversationID;
    conversationTurns.value = [];
    conversationHasMore.value = false;
    conversationOldestTurnID.value = null;
    void refreshTurns(conversationID);
  }

  async function refreshTurns(conversationID: string) {
    try {
      // 流式结束后后端返回的持久化 turn 不含 steps/tool_calls/progress_stages/
      // thinking 这些瞬时字段，直接替换会让"输出过程中的东西"消失。先快照再合并，
      // 保证用户在回复完成后仍能看到之前的思考/工具调用/进度过程。
      const transient = snapshotTransientByRole(conversationTurns.value);
      const detail = await getConversation(conversationID);
      const rawTurns = detail.turns ?? [];
      // 后端返回 CreatedAt DESC（最新消息在前），渲染需要正序（user -> assistant）。
      // 先水合后端持久化的过程证据（response_payload.process 的 thinking/steps），
      // 再并入同会话瞬态快照，保证刷新后思考与工具调用步骤不丢。
      conversationTurns.value = mergeTransient(hydrateTurns(rawTurns.slice().reverse()), transient);
      conversationHasMore.value = Boolean(detail.next_turn_cursor) && rawTurns.length > 0;
      conversationOldestTurnID.value = rawTurns[0]?.id ?? null;
    } catch {
      // Keep the existing transcript if the refresh fails; the user can retry.
    }
  }

  async function loadMore() {
    const conversationID = activeConversationID.value;
    const beforeTurnID = conversationOldestTurnID.value;
    if (!conversationID || !beforeTurnID || conversationLoadingMore.value || !conversationHasMore.value) {
      return;
    }
    conversationLoadingMore.value = true;
    try {
      const detail = await getConversation(conversationID, { before_turn_id: beforeTurnID });
      const olderTurns = detail.turns ?? [];
      if (olderTurns.length > 0) {
        // 后端返回倒序；转为正序后拼接到当前正序数组前面（更老的消息在上方）。
        const existingIDs = new Set(conversationTurns.value.map((turn) => turn.id));
        const deduped = olderTurns.filter((turn) => !existingIDs.has(turn.id));
        const oldestFirst = deduped.slice().reverse();
        // 更早的 turn 同样水合持久化的过程证据（thinking/steps）。
        conversationTurns.value = [...hydrateTurns(oldestFirst), ...conversationTurns.value];
        conversationOldestTurnID.value = deduped[0]?.id ?? conversationOldestTurnID.value;
      }
      conversationHasMore.value = Boolean(detail.next_turn_cursor) && olderTurns.length > 0;
    } catch {
      // Leave hasMore as-is so the user can retry by clicking again.
    } finally {
      conversationLoadingMore.value = false;
    }
  }

  function startNew() {
    activeConversationID.value = null;
    conversationTurns.value = [];
    conversationHasMore.value = false;
    conversationLoadingMore.value = false;
    conversationOldestTurnID.value = null;
  }

  async function archive(conversationID: string, onError?: (message: string) => void) {
    try {
      await archiveConversation(conversationID);
      conversations.value = conversations.value.filter((conv) => conv.id !== conversationID);
      if (activeConversationID.value === conversationID) {
        startNew();
      }
    } catch (err) {
      const message = err instanceof Error ? err.message : '归档会话失败';
      onError?.(message);
    }
  }

  return {
    conversations,
    filteredConversations,
    searchQuery,
    archivedView,
    activeConversationID,
    conversationsLoading,
    conversationTurns,
    conversationHasMore,
    conversationLoadingMore,
    conversationOldestTurnID,
    refresh,
    setArchivedView,
    select,
    refreshTurns,
    loadMore,
    startNew,
    archive,
  };
}
