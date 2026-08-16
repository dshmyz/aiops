<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { ElAlert } from 'element-plus';
import { listRunbooks } from '../api';
import type { Runbook } from '../types';
import ScheduledTaskForm from '../components/ScheduledTaskForm.vue';
import ScheduledTaskList from '../components/ScheduledTaskList.vue';
import ScheduledTaskRunHistory from '../components/ScheduledTaskRunHistory.vue';
import ViewShell from '../components/ViewShell.vue';
import type { UseScheduledTasks } from '../composables/useScheduledTasks';

defineProps<{ scheduledTasks: UseScheduledTasks }>();

// 可调度的低风险 runbook 模板（run_kind=runbook 下拉数据源，来自 GET /v1/runbooks）。
const schedulableRunbooks = ref<Runbook[]>([]);

onMounted(async () => {
  try {
    const res = await listRunbooks();
    if (res.configured && Array.isArray(res.runbooks)) {
      schedulableRunbooks.value = res.runbooks;
    }
  } catch {
    // 静默失败：无 runbook 模板则仅支持只读巡检（表单会禁用 runbook 类型）。
  }
});
</script>

<template>
  <ViewShell
    class="scheduled-tasks-entry"
    data-test="scheduled-tasks-entry"
    data-view="scheduled-tasks"
    eyebrow="Scheduled Inspections"
    title="定时巡检任务"
    copy="配置定时巡检任务，自动执行只读能力并追踪执行历史与失败情况。"
  >
    <template #actions>
      <button class="mini-button" :disabled="scheduledTasks.scheduledTasksLoading.value" @click="scheduledTasks.refresh">
        {{ scheduledTasks.scheduledTasksLoading.value ? '刷新中' : '刷新' }}
      </button>
      <button data-test="scheduled-task-new" class="primary-button" @click="scheduledTasks.openForm">+ 新建定时任务</button>
    </template>

    <el-alert v-if="scheduledTasks.error.value" class="alert" type="error" :title="scheduledTasks.error.value" show-icon />

    <ScheduledTaskList
      :tasks="scheduledTasks.scheduledTasks.value"
      @toggle-enabled="scheduledTasks.toggleEnabled"
      @trigger="scheduledTasks.triggerNow"
      @edit="scheduledTasks.editTask"
      @delete="scheduledTasks.remove"
    />

    <div v-if="scheduledTasks.scheduledTaskFormOpen.value" data-test="scheduled-task-form-modal" class="form-modal">
      <ScheduledTaskForm
        :task="scheduledTasks.scheduledTaskEditing.value"
        :capabilities="scheduledTasks.readCapabilities.value"
        :runbooks="schedulableRunbooks"
        @submit="scheduledTasks.save"
        @cancel="scheduledTasks.closeForm"
      />
    </div>

    <div v-if="scheduledTasks.scheduledTaskViewingRunsFor.value" data-test="scheduled-task-run-history-panel" class="run-history-panel">
      <div class="run-history-header">
        <h3>执行历史</h3>
        <button class="mini-button" @click="scheduledTasks.closeRuns">关闭</button>
      </div>
      <ScheduledTaskRunHistory :runs="scheduledTasks.scheduledTaskRuns.value" />
    </div>
  </ViewShell>
</template>
