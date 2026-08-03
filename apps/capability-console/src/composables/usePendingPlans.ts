import { ref } from 'vue';
import type { Ref } from 'vue';
import { getPendingPlan, listPendingPlans } from '../api';
import type {
  ExecutionResult,
  PendingPlanDetail as PendingPlanDetailType,
  PendingPlanSummary,
} from '../types';

export interface UsePendingPlansOptions {
  planTokens: Ref<Record<string, string>>;
  onConfirmed?: () => void;
}

export interface UsePendingPlans {
  pendingPlans: Ref<PendingPlanSummary[]>;
  pendingPlansLoading: Ref<boolean>;
  pendingPlansError: Ref<string>;
  selectedPlanID: Ref<string | undefined>;
  selectedPlanDetail: Ref<PendingPlanDetailType | null>;
  selectedPlanLoading: Ref<boolean>;
  latestExecutionResult: Ref<ExecutionResult | null>;
  refresh: () => Promise<void>;
  select: (planID: string) => Promise<void>;
  handleConfirmed: (result: ExecutionResult) => void;
  handleError: (message: string) => void;
}

export function usePendingPlans(options: UsePendingPlansOptions): UsePendingPlans {
  const { planTokens, onConfirmed } = options;

  const pendingPlans = ref<PendingPlanSummary[]>([]);
  const pendingPlansLoading = ref(false);
  const pendingPlansError = ref('');
  const selectedPlanID = ref<string | undefined>(undefined);
  const selectedPlanDetail = ref<PendingPlanDetailType | null>(null);
  const selectedPlanLoading = ref(false);
  const latestExecutionResult = ref<ExecutionResult | null>(null);

  async function refresh() {
    pendingPlansLoading.value = true;
    pendingPlansError.value = '';
    try {
      pendingPlans.value = await listPendingPlans();
    } catch (err) {
      pendingPlansError.value = err instanceof Error ? err.message : '加载待确认计划失败';
    } finally {
      pendingPlansLoading.value = false;
    }
  }

  async function select(planID: string) {
    selectedPlanID.value = planID;
    selectedPlanLoading.value = true;
    try {
      selectedPlanDetail.value = await getPendingPlan(planID);
    } catch (err) {
      pendingPlansError.value = err instanceof Error ? err.message : '加载计划详情失败';
      selectedPlanDetail.value = null;
    } finally {
      selectedPlanLoading.value = false;
    }
  }

  function handleConfirmed(result: ExecutionResult) {
    latestExecutionResult.value = result;
    if (selectedPlanID.value) {
      pendingPlans.value = pendingPlans.value.filter((plan) => plan.id !== selectedPlanID.value);
      const cleared = { ...planTokens.value };
      delete cleared[selectedPlanID.value];
      planTokens.value = cleared;
    }
    selectedPlanDetail.value = null;
    selectedPlanID.value = undefined;
    onConfirmed?.();
  }

  function handleError(message: string) {
    pendingPlansError.value = message;
  }

  return {
    pendingPlans,
    pendingPlansLoading,
    pendingPlansError,
    selectedPlanID,
    selectedPlanDetail,
    selectedPlanLoading,
    latestExecutionResult,
    refresh,
    select,
    handleConfirmed,
    handleError,
  };
}
