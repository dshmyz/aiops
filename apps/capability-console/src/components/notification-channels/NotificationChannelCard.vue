<script setup lang="ts">
import type { NotificationChannel } from '../../types';

const props = defineProps<{
  channel: NotificationChannel;
}>();

const emit = defineEmits<{
  (event: 'edit', channel: NotificationChannel): void;
  (event: 'delete', channel: NotificationChannel): void;
}>();

const typeLabel = props.channel.type === 'feishu' ? '飞书' : 'Webhook';
</script>

<template>
  <article
    class="channel-card"
    :class="{ 'is-disabled': channel.enabled === false }"
    data-test="notification-channel"
  >
    <header class="channel-header">
      <div class="channel-title">
        <span class="channel-name">{{ channel.name }}</span>
        <el-tag size="small" effect="plain" :type="channel.type === 'feishu' ? 'primary' : 'success'" data-test="channel-type-tag">
          {{ typeLabel }}
        </el-tag>
        <el-tag v-if="channel.template" size="small" effect="plain" type="warning" data-test="channel-template-tag">自定义模板</el-tag>
        <el-tag v-if="channel.enabled === false" type="info" size="small" effect="plain" data-test="channel-disabled-tag">已停用</el-tag>
      </div>
      <div class="channel-actions">
        <el-button size="small" text type="primary" @click="emit('edit', channel)" data-test="channel-edit-btn">编辑</el-button>
        <el-button size="small" text type="danger" @click="emit('delete', channel)" data-test="channel-delete-btn">删除</el-button>
      </div>
    </header>
    <div class="channel-url" :title="channel.url">{{ channel.url }}</div>
  </article>
</template>

<style scoped>
.channel-card { background: var(--color-bg-elevated); border: 1px solid var(--color-border); border-radius: var(--radius-lg); padding: var(--space-3); }
.channel-card.is-disabled { opacity: 0.75; }
.channel-header { display: flex; justify-content: space-between; align-items: center; gap: var(--space-2); margin-bottom: var(--space-2); }
.channel-title { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.channel-name { font-weight: 600; }
.channel-actions { display: flex; align-items: center; gap: 2px; }
.channel-url { font-size: var(--font-xs); color: var(--color-text-secondary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
</style>
