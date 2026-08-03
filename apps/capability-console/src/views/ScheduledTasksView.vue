<script setup lang="ts">
import { ElAlert } from 'element-plus';
import ScheduledTaskForm from '../components/ScheduledTaskForm.vue';
import ScheduledTaskList from '../components/ScheduledTaskList.vue';
import ScheduledTaskRunHistory from '../components/ScheduledTaskRunHistory.vue';
import type { UseScheduledTasks } from '../composables/useScheduledTasks';

defineProps<{ scheduledTasks: UseScheduledTasks }>();
</script>

<template>
  <section data-test="scheduled-tasks-entry" data-view="scheduled-tasks" class="scheduled-tasks-entry">
    <header class="topbar">
      <div>
        <p class="eyebrow">Scheduled Inspections</p>
        <h1>定时巡检任务</h1>
        <p class="topbar-copy">配置定时巡检任务，自动执行只读能力并追踪执行历史与失败情况。</p>
      </div>
      <div class="actions">
        <button class="mini-button" :disabled="scheduledTasks.scheduledTasksLoading.value" @click="scheduledTasks.refresh">
          {{ scheduledTasks.scheduledTasksLoading.value ? '刷新中' : '刷新' }}
        </button>
        <button data-test="scheduled-task-new" class="primary-button" @click="scheduledTasks.openForm">+ 新建定时任务</button>
      </div>
    </header>

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
  </section>
</template>
