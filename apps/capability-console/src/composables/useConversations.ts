import { computed, ref } from 'vue';
import type { ComputedRef, Ref } from 'vue';
import {
  archiveConversation,
  getConversation,
  listConversations,
} from '../api';
import type {
  ConversationSummary,
  ConversationTurn,
} from '../types';

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
      const detail = await getConversation(conversationID);
      const rawTurns = detail.turns ?? [];
      // 后端返回 CreatedAt DESC（最新消息在前），渲染需要正序（user -> assistant）。
      conversationTurns.value = rawTurns.slice().reverse();
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
        conversationTurns.value = [...oldestFirst, ...conversationTurns.value];
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
