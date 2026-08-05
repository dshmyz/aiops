import { ref } from 'vue';
import type { Ref } from 'vue';
import { viewIncident } from '../api';
import type { IncidentViewPivot, IncidentViewResult } from '../types';

export interface UseIncident {
  pivot: Ref<IncidentViewPivot>;
  result: Ref<IncidentViewResult | null>;
  loading: Ref<boolean>;
  error: Ref<string>;
  hasSearched: Ref<boolean>;
  run: (pivot?: IncidentViewPivot) => Promise<void>;
}

/**
 * incident.view 告警全景：operator 输入资源身份（domain / resource_type /
 * resource_name / environment），后端 incidentViewReadRunner 软匹配各证据源，
 * 返回可回链的 incident 全景（timeline / scheduled_runs / probes / runbooks /
 * recent_writes / counts）。
 */
export function useIncident(): UseIncident {
  const pivot = ref<IncidentViewPivot>({});
  const result = ref<IncidentViewResult | null>(null);
  const loading = ref(false);
  const error = ref('');
  const hasSearched = ref(false);

  async function run(next?: IncidentViewPivot) {
    if (next) {
      pivot.value = { ...next };
    }
    loading.value = true;
    error.value = '';
    try {
      result.value = await viewIncident(pivot.value);
    } catch (err) {
      result.value = null;
      error.value = err instanceof Error ? err.message : '查看告警全景失败';
    } finally {
      loading.value = false;
      hasSearched.value = true;
    }
  }

  return {
    pivot,
    result,
    loading,
    error,
    hasSearched,
    run,
  };
}
