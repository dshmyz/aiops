<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { ElMessage, ElMessageBox } from 'element-plus';
import ViewShell from '../components/ViewShell.vue';
import AlertActionCard from '../components/alert-actions/AlertActionCard.vue';
import AlertActionEditor from '../components/alert-actions/AlertActionEditor.vue';
import { useAlertActions } from '../composables/useAlertActions';
import { listAdminTools } from '../api';
import type { AdminTool, AlertAction } from '../types';

const {
  rules,
  filteredRules,
  loading,
  saving,
  error,
  configured,
  search,
  editing,
  editForm,
  runsByRule,
  startCreate,
  startEdit,
  cancelEdit,
  save,
  remove,
  toggleEnabled,
  loadRuns,
  load,
} = useAlertActions();

const tools = ref<AdminTool[]>([]);
const toolsLoading = ref(false);

async function loadTools() {
  if (tools.value.length > 0) return;
  toolsLoading.value = true;
  try {
    tools.value = await listAdminTools();
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : '加载可用工具失败');
  } finally {
    toolsLoading.value = false;
  }
}

function onEdit(rule: AlertAction) {
  startEdit(rule);
  void loadTools();
}
function onCreate() {
  startCreate();
  void loadTools();
}
function onShowRuns(rule: AlertAction) {
  void loadRuns(rule);
}
async function onDelete(rule: AlertAction) {
  try {
    await ElMessageBox.confirm(`确定删除规则 "${rule.name}" 吗？删除后不可恢复。`, '删除规则', {
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      type: 'warning',
      confirmButtonClass: 'el-button--danger',
    });
    await remove(rule.name);
  } catch {
    // 用户取消删除，不提示
  }
}
function onToggle(rule: AlertAction) {
  void toggleEnabled(rule);
}

// 编辑中刷新/关页提示：防止未保存的内容静默丢失。
function beforeUnloadHandler(event: BeforeUnloadEvent) {
  if (editing.value) {
    event.preventDefault();
  }
}
watch(editing, (val) => {
  if (val) window.addEventListener('beforeunload', beforeUnloadHandler);
  else window.removeEventListener('beforeunload', beforeUnloadHandler);
});

onMounted(() => {
  void load();
  void loadTools();
});
onBeforeUnmount(() => {
  window.removeEventListener('beforeunload', beforeUnloadHandler);
});
</script>

<template>
  <ViewShell
    class="admin-page"
    data-test="admin-alert-actions"
    eyebrow="Admin"
    title="告警响应编排"
    copy="配置告警→动作的自动响应规则。告警到达时自动执行工具序列（诊断+处置）。"
  >
    <template #actions>
      <el-button :loading="loading" @click="load">刷新</el-button>
      <el-button type="primary" @click="onCreate" data-test="alert-create-btn">新建规则</el-button>
    </template>

    <p v-if="error" class="error-text" role="alert">{{ error }}</p>

    <div v-if="!loading && !configured" class="config-banner">
      <strong>告警响应编排未启用</strong>
      <p>请在数据库中配置告警动作规则。</p>
    </div>

    <!-- 骨架屏 -->
    <div v-if="loading" class="skeleton-list" data-test="alert-skeleton">
      <div v-for="i in 3" :key="i" class="skeleton-card">
        <div class="skeleton-line w40"></div>
        <div class="skeleton-line w70"></div>
        <div class="skeleton-line w55"></div>
      </div>
    </div>

    <template v-else-if="!editing">
      <div v-if="rules.length > 1" class="toolbar">
        <el-input
          v-model="search"
          placeholder="按名称或描述搜索规则…"
          clearable
          class="search-input"
          data-test="alert-search"
        />
        <span class="count">{{ filteredRules.length }} / {{ rules.length }} 条</span>
      </div>

      <!-- 空态 -->
      <div v-if="filteredRules.length === 0 && !error" class="empty" data-test="alert-empty">
        <p class="empty-title">{{ rules.length === 0 ? '还没有告警响应规则' : '没有匹配的规则' }}</p>
        <p class="empty-copy">
          {{ rules.length === 0 ? '告警到达时自动执行诊断和处置。点击「新建规则」编排第一条响应。' : '换个关键词试试。' }}
        </p>
      </div>

      <div v-else class="rules-list">
        <AlertActionCard
          v-for="rule in filteredRules"
          :key="rule.name"
          :rule="rule"
          :runs="runsByRule[rule.name]"
          @edit="onEdit"
          @delete="onDelete"
          @toggle="onToggle"
          @show-runs="onShowRuns"
        />
      </div>
    </template>

    <AlertActionEditor
      :editing="editing"
      :form="editForm"
      :tools="tools"
      :saving="saving"
      @close="cancelEdit"
      @save="save"
    />
  </ViewShell>
</template>

<style scoped>
.error-text { color: var(--color-danger); }
.config-banner { padding: var(--space-3); background: var(--color-bg-elevated); border-radius: var(--radius-lg); border: 1px solid var(--color-border); margin-bottom: var(--space-3); }
.empty { color: var(--color-text-tertiary); padding: var(--space-6) var(--space-4); text-align: center; }
.empty-title { font-size: 14px; font-weight: 600; color: var(--color-text-secondary); margin: 0 0 6px; }
.empty-copy { font-size: 12px; margin: 0; }

.toolbar { display: flex; align-items: center; gap: var(--space-2); margin-bottom: var(--space-3); }
.search-input { max-width: 320px; }
.count { font-size: var(--font-xs); color: var(--color-text-tertiary); }

.rules-list { display: flex; flex-direction: column; gap: var(--space-3); }

.skeleton-list { display: flex; flex-direction: column; gap: var(--space-3); }
.skeleton-card { background: var(--color-bg-elevated); border: 1px solid var(--color-border); border-radius: var(--radius-lg); padding: var(--space-3); display: flex; flex-direction: column; gap: 10px; }
.skeleton-line { height: 12px; border-radius: 4px; background: linear-gradient(90deg, var(--color-bg) 25%, var(--color-border) 50%, var(--color-bg) 75%); background-size: 200% 100%; animation: shimmer 1.2s infinite; }
.w40 { width: 40%; }
.w70 { width: 70%; }
.w55 { width: 55%; }
@keyframes shimmer { from { background-position: 200% 0; } to { background-position: -200% 0; } }
</style>
