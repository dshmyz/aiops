import { ref } from 'vue';
import type { Ref } from 'vue';
import { listIncidents, getIncident } from '../api';
import type { AlertIncident, IncidentMemberAlert } from '../types';

export interface UseIncidents {
  incidents: Ref<AlertIncident[]>;
  incidentsLoading: Ref<boolean>;
  incidentsError: Ref<string>;
  statusFilter: Ref<'firing' | 'resolved' | ''>;
  domainFilter: Ref<string>;
  refresh: () => Promise<void>;
  selectedIncident: Ref<AlertIncident | null>;
  memberAlerts: Ref<IncidentMemberAlert[]>;
  detailLoading: Ref<boolean>;
  detailError: Ref<string>;
  select: (incident: AlertIncident) => Promise<void>;
  clearSelection: () => void;
}

export function useIncidents(): UseIncidents {
  const incidents = ref<AlertIncident[]>([]);
  const incidentsLoading = ref(false);
  const incidentsError = ref('');
  const statusFilter = ref<'firing' | 'resolved' | ''>('firing');
  const domainFilter = ref('');
  const selectedIncident = ref<AlertIncident | null>(null);
  const memberAlerts = ref<IncidentMemberAlert[]>([]);
  const detailLoading = ref(false);
  const detailError = ref('');

  async function refresh() {
    incidentsLoading.value = true;
    incidentsError.value = '';
    try {
      const page = await listIncidents({
        status: statusFilter.value || undefined,
        domain: domainFilter.value.trim() || undefined,
        limit: 100,
      });
      incidents.value = page.incidents ?? [];
    } catch (err) {
      incidentsError.value = err instanceof Error ? err.message : '加载 incident 列表失败';
    } finally {
      incidentsLoading.value = false;
    }
  }

  async function select(incident: AlertIncident) {
    selectedIncident.value = incident;
    detailLoading.value = true;
    detailError.value = '';
    memberAlerts.value = [];
    try {
      const detail = await getIncident(incident.id);
      selectedIncident.value = detail.incident;
      memberAlerts.value = detail.alerts ?? [];
    } catch (err) {
      detailError.value = err instanceof Error ? err.message : '加载 incident 详情失败';
    } finally {
      detailLoading.value = false;
    }
  }

  function clearSelection() {
    selectedIncident.value = null;
    memberAlerts.value = [];
    detailError.value = '';
  }

  return {
    incidents,
    incidentsLoading,
    incidentsError,
    statusFilter,
    domainFilter,
    refresh,
    selectedIncident,
    memberAlerts,
    detailLoading,
    detailError,
    select,
    clearSelection,
  };
}
