import { computed, onMounted, onUnmounted, ref } from 'vue';
import type { ComputedRef, Ref } from 'vue';
import {
  countScheduledTaskFailures,
  createScheduledTask,
  deleteScheduledTask,
  listScheduledTaskRuns,
  listScheduledTasks,
  triggerScheduledTask,
  updateScheduledTask,
} from '../api';
import type {
  ManagedCapability,
  ScheduledTask,
  ScheduledTaskRun,
} from '../types';

export interface SaveScheduledTaskPayload {
  name: string;
  capability_name: string;
  input: Record<string, unknown>;
  schedule_kind: 'preset' | 'cron';
  preset: '5m' | '1h' | 'daily' | 'weekly' | null;
  cron_expr: string | null;
}

export interface UseScheduledTasksOptions {
  readCapabilities: ComputedRef<ManagedCapability[]> | Ref<ManagedCapability[]>;
}

export interface UseScheduledTasks {
  scheduledTasks: Ref<ScheduledTask[]>;
  scheduledTaskFailures: Ref<number>;
  scheduledTaskFormOpen: Ref<boolean>;
  scheduledTaskEditing: Ref<ScheduledTask | null>;
  scheduledTaskViewingRunsFor: Ref<string | null>;
  scheduledTaskRuns: Ref<ScheduledTaskRun[]>;
  scheduledTasksLoading: Ref<boolean>;
  error: Ref<string>;
  readCapabilities: ComputedRef<ManagedCapability[]>;
  refresh: () => Promise<void>;
  refreshFailures: () => Promise<void>;
  openForm: () => void;
  editTask: (task: ScheduledTask) => void;
  closeForm: () => void;
  save: (payload: SaveScheduledTaskPayload) => Promise<void>;
  remove: (id: string) => Promise<void>;
  triggerNow: (id: string) => Promise<void>;
  toggleEnabled: (id: string, enabled: boolean) => Promise<void>;
  viewRuns: (task: ScheduledTask) => Promise<void>;
  closeRuns: () => void;
}

const FAILURE_POLL_INTERVAL_MS = 60_000;

export function useScheduledTasks(options: UseScheduledTasksOptions): UseScheduledTasks {
  const readCapabilities = computed(() => {
    const source = options.readCapabilities;
    const value = 'value' in source ? source.value : source;
    return value.filter((capability) => capability.operation === 'read');
  });

  const scheduledTasks = ref<ScheduledTask[]>([]);
  const scheduledTaskFailures = ref(0);
  const scheduledTaskFormOpen = ref(false);
  const scheduledTaskEditing = ref<ScheduledTask | null>(null);
  const scheduledTaskViewingRunsFor = ref<string | null>(null);
  const scheduledTaskRuns = ref<ScheduledTaskRun[]>([]);
  const scheduledTasksLoading = ref(false);
  const error = ref('');

  let failureTimer: ReturnType<typeof setInterval> | null = null;

  async function refresh() {
    scheduledTasksLoading.value = true;
    try {
      scheduledTasks.value = await listScheduledTasks();
    } catch {
      // 静默失败：列表页保留之前的状态，用户可手动重试
    } finally {
      scheduledTasksLoading.value = false;
    }
  }

  async function refreshFailures() {
    try {
      scheduledTaskFailures.value = await countScheduledTaskFailures();
    } catch {
      // 静默失败：badge 数字保留之前的状态
    }
  }

  function openForm() {
    scheduledTaskEditing.value = null;
    scheduledTaskFormOpen.value = true;
  }

  function editTask(task: ScheduledTask) {
    scheduledTaskEditing.value = task;
    scheduledTaskFormOpen.value = true;
  }

  function closeForm() {
    scheduledTaskFormOpen.value = false;
    scheduledTaskEditing.value = null;
  }

  async function save(payload: SaveScheduledTaskPayload) {
    try {
      if (scheduledTaskEditing.value) {
        await updateScheduledTask(scheduledTaskEditing.value.id, payload);
      } else {
        await createScheduledTask(payload);
      }
      closeForm();
      await refresh();
    } catch (err) {
      error.value = err instanceof Error ? err.message : '保存定时任务失败';
    }
  }

  async function remove(id: string) {
    try {
      await deleteScheduledTask(id);
      await refresh();
    } catch (err) {
      error.value = err instanceof Error ? err.message : '删除定时任务失败';
    }
  }

  async function triggerNow(id: string) {
    try {
      await triggerScheduledTask(id);
      // 触发后展示执行历史面板，加载最新 runs
      scheduledTaskViewingRunsFor.value = id;
      scheduledTaskRuns.value = await listScheduledTaskRuns(id);
      await refresh();
    } catch (err) {
      error.value = err instanceof Error ? err.message : '触发定时任务失败';
    }
  }

  async function toggleEnabled(id: string, enabled: boolean) {
    // 组件 emit 的是 (id, enabled)，这里按 id 查找完整 task 以构造更新 payload
    const task = scheduledTasks.value.find((item) => item.id === id);
    if (!task) return;
    try {
      await updateScheduledTask(id, {
        name: task.name,
        capability_name: task.capability_name,
        input: task.input,
        schedule_kind: task.schedule_kind,
        preset: task.preset,
        cron_expr: task.cron_expr,
        enabled,
      });
      await refresh();
    } catch (err) {
      error.value = err instanceof Error ? err.message : '切换定时任务状态失败';
    }
  }

  async function viewRuns(task: ScheduledTask) {
    scheduledTaskViewingRunsFor.value = task.id;
    try {
      scheduledTaskRuns.value = await listScheduledTaskRuns(task.id);
    } catch {
      scheduledTaskRuns.value = [];
    }
  }

  function closeRuns() {
    scheduledTaskViewingRunsFor.value = null;
    scheduledTaskRuns.value = [];
  }

  onMounted(() => {
    void refreshFailures();
    failureTimer = setInterval(() => {
      void refreshFailures();
    }, FAILURE_POLL_INTERVAL_MS);
  });

  onUnmounted(() => {
    if (failureTimer !== null) {
      clearInterval(failureTimer);
      failureTimer = null;
    }
  });

  return {
    scheduledTasks,
    scheduledTaskFailures,
    scheduledTaskFormOpen,
    scheduledTaskEditing,
    scheduledTaskViewingRunsFor,
    scheduledTaskRuns,
    scheduledTasksLoading,
    error,
    readCapabilities,
    refresh,
    refreshFailures,
    openForm,
    editTask,
    closeForm,
    save,
    remove,
    triggerNow,
    toggleEnabled,
    viewRuns,
    closeRuns,
  };
}
