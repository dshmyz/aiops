import { ref } from 'vue';
import type { Ref } from 'vue';
import {
  listAuditEvents,
  searchAuditEvents,
} from '../api';
import type {
  AuditEvent,
  AuditEventCursor,
  AuditEventFilter,
} from '../types';

export interface UseAuditEvents {
  auditEvents: Ref<AuditEvent[]>;
  auditEventsLoading: Ref<boolean>;
  auditEventsError: Ref<string>;
  auditFilter: Ref<AuditEventFilter>;
  auditNextCursor: Ref<AuditEventCursor | null>;
  auditLoadingMore: Ref<boolean>;
  auditSearchQuery: Ref<string>;
  refresh: () => Promise<void>;
  loadMore: () => Promise<void>;
  applyFilter: (filter: AuditEventFilter) => void;
  search: (query: string) => Promise<void>;
}

export function useAuditEvents(): UseAuditEvents {
  const auditEvents = ref<AuditEvent[]>([]);
  const auditEventsLoading = ref(false);
  const auditEventsError = ref('');
  const auditFilter = ref<AuditEventFilter>({});
  const auditNextCursor = ref<AuditEventCursor | null>(null);
  const auditLoadingMore = ref(false);
  const auditSearchQuery = ref('');

  async function refresh() {
    auditEventsLoading.value = true;
    auditEventsError.value = '';
    try {
      const page = await listAuditEvents(auditFilter.value);
      auditEvents.value = page.events ?? [];
      auditNextCursor.value = page.next_cursor ?? null;
    } catch (err) {
      auditEventsError.value = err instanceof Error ? err.message : '加载审计记录失败';
    } finally {
      auditEventsLoading.value = false;
    }
  }

  async function loadMore() {
    if (!auditNextCursor.value || auditLoadingMore.value) return;
    auditLoadingMore.value = true;
    try {
      const filter: AuditEventFilter = {
        ...auditFilter.value,
        cursor_created_at: auditNextCursor.value.created_at,
        cursor_id: auditNextCursor.value.id,
      };
      const page = await listAuditEvents(filter);
      auditEvents.value = [...auditEvents.value, ...(page.events ?? [])];
      auditNextCursor.value = page.next_cursor ?? null;
    } catch (err) {
      auditEventsError.value = err instanceof Error ? err.message : '加载更多审计记录失败';
    } finally {
      auditLoadingMore.value = false;
    }
  }

  function applyFilter(filter: AuditEventFilter) {
    auditFilter.value = filter;
    auditSearchQuery.value = '';
    void refresh();
  }

  async function search(query: string) {
    auditSearchQuery.value = query;
    auditEventsLoading.value = true;
    auditEventsError.value = '';
    try {
      const page = await searchAuditEvents(query);
      auditEvents.value = page.events ?? [];
      auditNextCursor.value = page.next_cursor ?? null;
      auditFilter.value = {};
    } catch (err) {
      auditEventsError.value = err instanceof Error ? err.message : '搜索审计记录失败';
    } finally {
      auditEventsLoading.value = false;
    }
  }

  return {
    auditEvents,
    auditEventsLoading,
    auditEventsError,
    auditFilter,
    auditNextCursor,
    auditLoadingMore,
    auditSearchQuery,
    refresh,
    loadMore,
    applyFilter,
    search,
  };
}
