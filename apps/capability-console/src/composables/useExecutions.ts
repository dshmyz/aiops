import { ref } from 'vue';
import type { Ref } from 'vue';
import { listExecutions } from '../api';
import type {
  AuditEventCursor,
  ExecutionFilter,
  ExecutionRecord,
} from '../types';

export interface UseExecutions {
  executions: Ref<ExecutionRecord[]>;
  executionsLoading: Ref<boolean>;
  executionsError: Ref<string>;
  executionFilter: Ref<ExecutionFilter>;
  nextCursor: Ref<AuditEventCursor | null>;
  loadingMore: Ref<boolean>;
  refresh: () => Promise<void>;
  loadMore: () => Promise<void>;
  applyFilter: (filter: ExecutionFilter) => void;
}

export function useExecutions(): UseExecutions {
  const executions = ref<ExecutionRecord[]>([]);
  const executionsLoading = ref(false);
  const executionsError = ref('');
  const executionFilter = ref<ExecutionFilter>({});
  const nextCursor = ref<AuditEventCursor | null>(null);
  const loadingMore = ref(false);

  async function refresh() {
    executionsLoading.value = true;
    executionsError.value = '';
    try {
      const page = await listExecutions(executionFilter.value);
      executions.value = page.executions ?? [];
      nextCursor.value = page.next_cursor ?? null;
    } catch (err) {
      executionsError.value = err instanceof Error ? err.message : '加载执行历史失败';
    } finally {
      executionsLoading.value = false;
    }
  }

  async function loadMore() {
    if (!nextCursor.value || loadingMore.value) return;
    loadingMore.value = true;
    try {
      const filter: ExecutionFilter = {
        ...executionFilter.value,
        cursor_created_at: nextCursor.value.created_at,
        cursor_id: nextCursor.value.id,
      };
      const page = await listExecutions(filter);
      executions.value = [...executions.value, ...(page.executions ?? [])];
      nextCursor.value = page.next_cursor ?? null;
    } catch (err) {
      executionsError.value = err instanceof Error ? err.message : '加载更多执行记录失败';
    } finally {
      loadingMore.value = false;
    }
  }

  function applyFilter(filter: ExecutionFilter) {
    executionFilter.value = filter;
    void refresh();
  }

  return {
    executions,
    executionsLoading,
    executionsError,
    executionFilter,
    nextCursor,
    loadingMore,
    refresh,
    loadMore,
    applyFilter,
  };
}
