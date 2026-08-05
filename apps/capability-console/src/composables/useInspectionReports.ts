import { ref } from 'vue';
import type { Ref } from 'vue';
import { listInspectionReports, getInspectionReport } from '../api';
import type { InspectionReport } from '../types';

export interface UseInspectionReports {
  reports: Ref<InspectionReport[]>;
  reportsLoading: Ref<boolean>;
  reportsError: Ref<string>;
  selectedReport: Ref<InspectionReport | null>;
  detailLoading: Ref<boolean>;
  detailError: Ref<string>;
  refresh: () => Promise<void>;
  select: (report: InspectionReport) => void;
  clearSelection: () => void;
}

export function useInspectionReports(): UseInspectionReports {
  const reports = ref<InspectionReport[]>([]);
  const reportsLoading = ref(false);
  const reportsError = ref('');
  const selectedReport = ref<InspectionReport | null>(null);
  const detailLoading = ref(false);
  const detailError = ref('');

  async function refresh() {
    reportsLoading.value = true;
    reportsError.value = '';
    try {
      reports.value = await listInspectionReports(50);
    } catch (err) {
      reportsError.value = err instanceof Error ? err.message : '加载巡检报告失败';
    } finally {
      reportsLoading.value = false;
    }
  }

  // 列表返回的 report 已含 task_summaries 与 html_content（非分页摘要），
  // 故默认直接用已加载对象；如需保证最新可再按 id 拉取详情。
  async function select(report: InspectionReport) {
    selectedReport.value = report;
    detailLoading.value = true;
    detailError.value = '';
    try {
      selectedReport.value = await getInspectionReport(report.id);
    } catch (err) {
      detailError.value = err instanceof Error ? err.message : '加载报告详情失败';
    } finally {
      detailLoading.value = false;
    }
  }

  function clearSelection() {
    selectedReport.value = null;
    detailError.value = '';
  }

  return {
    reports,
    reportsLoading,
    reportsError,
    selectedReport,
    detailLoading,
    detailError,
    refresh,
    select,
    clearSelection,
  };
}
