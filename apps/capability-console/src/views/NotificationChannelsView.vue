<script setup lang="ts">
import { ElMessageBox } from 'element-plus';
import ViewShell from '../components/ViewShell.vue';
import NotificationChannelCard from '../components/notification-channels/NotificationChannelCard.vue';
import NotificationChannelEditor from '../components/notification-channels/NotificationChannelEditor.vue';
import { useNotificationChannels } from '../composables/useNotificationChannels';
import type { NotificationChannel } from '../types';

const {
  channels,
  loading,
  saving,
  error,
  configured,
  editing,
  editForm,
  startCreate,
  startEdit,
  cancelEdit,
  save,
  remove,
  load,
} = useNotificationChannels();

function onCreate() {
  startCreate();
}
function onEdit(channel: NotificationChannel) {
  startEdit(channel);
}
async function onDelete(channel: NotificationChannel) {
  try {
    await ElMessageBox.confirm(`确定删除通道 "${channel.name}" 吗？删除后通知不再投递到此通道。`, '删除通知通道', {
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      type: 'warning',
      confirmButtonClass: 'el-button--danger',
    });
    await remove(channel);
  } catch {
    // 用户取消删除，不提示
  }
}
</script>

<template>
  <ViewShell
    class="admin-page"
    data-test="admin-notification-channels"
    eyebrow="Admin"
    title="通知通道"
    copy="配置处置确认通知的外发通道（飞书 / 通用 Webhook）。增删改即时生效，无需重启服务。"
  >
    <template #actions>
      <el-button :loading="loading" @click="load">刷新</el-button>
      <el-button type="primary" @click="onCreate" data-test="channel-create-btn">新建通道</el-button>
    </template>

    <p v-if="error" class="error-text" role="alert">{{ error }}</p>

    <div v-if="!loading && !configured" class="config-banner">
      <strong>通知通道管理未启用</strong>
      <p>数据库中无通道配置。</p>
    </div>

    <!-- 骨架屏 -->
    <div v-if="loading" class="skeleton-list" data-test="channel-skeleton">
      <div v-for="i in 2" :key="i" class="skeleton-card">
        <div class="skeleton-line w40"></div>
        <div class="skeleton-line w70"></div>
      </div>
    </div>

    <template v-else>
      <!-- 空态 -->
      <div v-if="channels.length === 0 && !error" class="empty" data-test="channel-empty">
        <p class="empty-title">还没有通知通道</p>
        <p class="empty-copy">处置确认通知目前只会落到日志（最低可见渠道）。点击「新建通道」接入飞书或内网 Webhook。</p>
      </div>

      <div v-else class="channels-list">
        <NotificationChannelCard
          v-for="channel in channels"
          :key="channel.id"
          :channel="channel"
          @edit="onEdit"
          @delete="onDelete"
        />
      </div>
    </template>

    <NotificationChannelEditor
      :editing="editing"
      :form="editForm"
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

.channels-list { display: flex; flex-direction: column; gap: var(--space-3); }

.skeleton-list { display: flex; flex-direction: column; gap: var(--space-3); }
.skeleton-card { background: var(--color-bg-elevated); border: 1px solid var(--color-border); border-radius: var(--radius-lg); padding: var(--space-3); display: flex; flex-direction: column; gap: 10px; }
.skeleton-line { height: 12px; border-radius: 4px; background: linear-gradient(90deg, var(--color-bg) 25%, var(--color-border) 50%, var(--color-bg) 75%); background-size: 200% 100%; animation: shimmer 1.2s infinite; }
.w40 { width: 40%; }
.w70 { width: 70%; }
@keyframes shimmer { from { background-position: 200% 0; } to { background-position: -200% 0; } }
</style>
