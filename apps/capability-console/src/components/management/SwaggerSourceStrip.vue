<script setup lang="ts">
import { ElButton } from 'element-plus';

interface Props {
  url: string;
  baseUrl: string;
  loading?: boolean;
  buttonText?: string;
  buttonType?: 'primary' | 'default';
  showExampleLink?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  loading: false,
  buttonText: '预览 API',
  buttonType: 'primary',
  showExampleLink: true,
});

const emit = defineEmits<{
  'update:url': [value: string];
  'update:baseUrl': [value: string];
  'preview': [];
  'clear-preview': [];
  'load-example': [];
}>();

function onUrlInput(event: Event) {
  emit('update:url', (event.target as HTMLInputElement).value);
  emit('clear-preview');
}

function onBaseUrlInput(event: Event) {
  emit('update:baseUrl', (event.target as HTMLInputElement).value);
  emit('clear-preview');
}
</script>

<template>
  <div data-test="import-step-source" class="swagger-strip">
    <label>
      <span>Swagger / OpenAPI 地址</span>
      <input
        data-test="openapi-url-input"
        :value="props.url"
        class="filter-input"
        placeholder="http://你的后台/v3/api-docs"
        @input="onUrlInput"
      />
    </label>
    <label>
      <span>中间件后台 Base URL</span>
      <input
        data-test="backend-base-url-input"
        :value="props.baseUrl"
        class="filter-input"
        placeholder="https://middleware.example.com"
        @input="onBaseUrlInput"
      />
    </label>
    <div class="swagger-strip__actions">
      <el-button
        data-test="preview-openapi-url"
        :type="props.buttonType"
        :loading="props.loading"
        @click="emit('preview')"
      >
        {{ props.buttonText }}
      </el-button>
      <button
        v-if="props.showExampleLink"
        data-test="load-builtin-example"
        class="example-link"
        type="button"
        :disabled="props.loading"
        @click="emit('load-example')"
      >
        载入内置示例（MinIO / Kafka / GlusterFS）
      </button>
    </div>
  </div>
</template>

<style scoped>
.swagger-strip { display: flex; flex-direction: column; gap: 12px; }
.swagger-strip label { display: flex; flex-direction: column; gap: 6px; font-size: 13px; }
.swagger-strip label span { color: var(--el-text-color-secondary, #909399); }
.swagger-strip__actions { display: flex; align-items: center; gap: 12px; padding-top: 2px; }
.swagger-strip__actions .el-button { height: 42px; padding: 0 22px; font-size: 14px; }
.example-link { background: none; border: none; padding: 4px 0; font: inherit; font-size: 13px; color: var(--el-color-primary, #409eff); cursor: pointer; }
.example-link:disabled { opacity: .5; cursor: not-allowed; }
</style>
