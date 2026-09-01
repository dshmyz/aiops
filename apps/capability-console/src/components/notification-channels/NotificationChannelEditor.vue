<script setup lang="ts">
import { computed } from 'vue';
import type { NotificationChannel } from '../../types';

const props = defineProps<{
  /** null = 关闭；'new' = 新建；NotificationChannel = 编辑既有 */
  editing: null | 'new' | NotificationChannel;
  form: NotificationChannel;
  saving: boolean;
}>();

const emit = defineEmits<{
  (event: 'close'): void;
  (event: 'save'): void;
}>();

const drawerVisible = computed(() => props.editing !== null);

const title = computed(() => {
  if (props.editing === 'new') return '新建通知通道';
  if (props.editing) return `编辑通道 · ${props.editing.name}`;
  return '';
});

const isWebhook = computed(() => props.form.type === 'webhook');

function emitUpdate(patch: Partial<NotificationChannel>) {
  Object.assign(props.form, patch);
}
</script>

<template>
  <el-drawer
    :model-value="drawerVisible"
    :title="title"
    size="480px"
    :append-to-body="false"
    :destroy-on-close="false"
    @close="emit('close')"
  >
    <div class="editor-body" data-test="notification-channel-editor">
      <div class="field">
        <label class="field-label">通道类型 <span class="req">*</span></label>
        <el-select
          :model-value="form.type"
          style="width: 100%"
          data-test="channel-type-select"
          @update:model-value="(v: NotificationChannel['type']) => emitUpdate({ type: v })"
        >
          <el-option label="通用 Webhook（内网自建端点）" value="webhook" />
          <el-option label="飞书（Lark 群机器人）" value="feishu" />
        </el-select>
      </div>

      <div class="field">
        <label class="field-label">通道名称 <span class="req">*</span></label>
        <el-input
          :model-value="form.name"
          placeholder="如: 内网 IM 网关 / 值班群"
          data-test="channel-name-input"
          @update:model-value="(v: string) => emitUpdate({ name: v })"
        />
      </div>

      <div class="field">
        <label class="field-label">Webhook 地址 <span class="req">*</span></label>
        <el-input
          :model-value="form.url"
          placeholder="https://…"
          data-test="channel-url-input"
          @update:model-value="(v: string) => emitUpdate({ url: v })"
        />
      </div>

      <div v-if="isWebhook" class="field">
        <label class="field-label">HMAC 签名密钥（可选）</label>
        <el-input
          :model-value="form.secret ?? ''"
          type="password"
          show-password
          :placeholder="props.editing !== 'new' ? '留空保持不变' : '接收方验签用，可不填'"
          data-test="channel-secret-input"
          @update:model-value="(v: string) => emitUpdate({ secret: v })"
        />
        <p class="field-hint">设置后出站请求附带 X-Signature（body 的 HMAC-SHA256），接收方可验签。</p>
      </div>

      <div class="enabled-row">
        <el-switch
          :model-value="form.enabled"
          data-test="channel-enabled-switch"
          @update:model-value="(v: boolean) => emitUpdate({ enabled: v })"
        />
        <span>启用该通道（停用后通知不再投递到此通道）</span>
      </div>
    </div>

    <template #footer>
      <div class="editor-actions">
        <el-button :disabled="saving" @click="emit('close')">取消</el-button>
        <el-button type="primary" :loading="saving" @click="emit('save')" data-test="channel-save-btn">保存</el-button>
      </div>
    </template>
  </el-drawer>
</template>

<style scoped>
.editor-body { display: flex; flex-direction: column; gap: var(--space-4); }
.field { display: flex; flex-direction: column; gap: 6px; }
.field-label { font-size: var(--font-sm); font-weight: 600; color: var(--color-text); }
.req { color: var(--color-danger); }
.field-hint { font-size: var(--font-xs); color: var(--color-text-tertiary); margin: 4px 0 0; }
.enabled-row { display: flex; align-items: center; gap: 10px; font-size: var(--font-sm); color: var(--color-text-secondary); }
.editor-actions { display: flex; justify-content: flex-end; gap: var(--space-2); }
</style>
