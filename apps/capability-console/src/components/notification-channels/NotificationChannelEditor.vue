<script setup lang="ts">
import { computed, ref, watch } from 'vue';
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

// 自定义请求体开关：template 非空即视为开启。
const customBody = ref(!!props.form.template);
watch(
  () => props.editing,
  () => {
    customBody.value = !!props.form.template;
  },
);

const TEMPLATE_HINTS = [
  '{{.PlanID}}', '{{.ConfirmationToken}}', '{{.ToolName}}', '{{.Risk}}',
  '{{.Subject}}', '{{.InputJSON}}', '{{.SentAt}}', '{{.ExpiresAt}}',
];

function emitUpdate(patch: Partial<NotificationChannel>) {
  Object.assign(props.form, patch);
}

function onCustomBodyChange(on: boolean) {
  customBody.value = on;
  if (!on) {
    emitUpdate({ template: '' });
  } else if (!props.form.template) {
    emitUpdate({
      template: '{\n  "action": "approve",\n  "plan": "{{.PlanID}}",\n  "token": "{{.ConfirmationToken}}"\n}',
    });
  }
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

      <div v-if="isWebhook" class="template-section">
        <div class="enabled-row">
          <el-switch
            :model-value="customBody"
            data-test="channel-template-switch"
            @update:model-value="onCustomBodyChange"
          />
          <span>自定义请求体模板（默认发送固定 JSON 信封）</span>
        </div>
        <template v-if="customBody">
          <el-input
            :model-value="form.template ?? ''"
            type="textarea"
            :rows="6"
            class="template-editor"
            placeholder='{"action":"approve","plan":"{{.PlanID}}","token":"{{.ConfirmationToken}}"}'
            data-test="channel-template-input"
            @update:model-value="(v: string) => emitUpdate({ template: v })"
          />
          <div class="hints">
            <span>可用变量：</span>
            <code v-for="h in TEMPLATE_HINTS" :key="h">{{ h }}</code>
          </div>
        </template>
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
.template-section { display: flex; flex-direction: column; gap: var(--space-2); }
.template-editor { font-family: var(--font-mono, ui-monospace, SFMono-Regular, Menlo, monospace); font-size: var(--font-xs); }
.hints { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; font-size: var(--font-xs); color: var(--color-text-tertiary); }
.hints code { padding: 1px 6px; border-radius: var(--radius-sm, 4px); background: var(--color-bg); border: 1px solid var(--color-border); color: var(--color-text-secondary); }
.editor-actions { display: flex; justify-content: flex-end; gap: var(--space-2); }
</style>
