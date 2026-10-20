<script setup lang="ts">
import { ElButton } from 'element-plus';

interface Props {
  url: string;
  baseUrl: string;
  loading?: boolean;
  buttonText?: string;
  buttonType?: 'primary' | 'default';
}

const props = withDefaults(defineProps<Props>(), {
  loading: false,
  buttonText: '预览 API',
  buttonType: 'primary',
});

const emit = defineEmits<{
  'update:url': [value: string];
  'update:baseUrl': [value: string];
  'preview': [];
  'clear-preview': [];
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
      <span>Swagger / OpenAPI 文档地址</span>
      <input
        data-test="openapi-url-input"
        :value="props.url"
        class="filter-input"
        placeholder="http://你的后台/v3/api-docs"
        @input="onUrlInput"
      />
    </label>
    <label>
      <span>后端服务 Base URL（实际调用地址，文档里的接口路径会拼在它后面）</span>
      <input
        data-test="backend-base-url-input"
        :value="props.baseUrl"
        class="filter-input"
        placeholder="https://backend.example.com"
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
    </div>
  </div>
</template>

<style scoped>
.swagger-strip { display: flex; flex-direction: column; gap: 12px; }
.swagger-strip label { display: flex; flex-direction: column; gap: 6px; font-size: 13px; }
.swagger-strip label span { color: var(--el-text-color-secondary, #909399); }
.swagger-strip__actions { display: flex; align-items: center; gap: 12px; padding-top: 2px; }
.swagger-strip__actions .el-button { height: 42px; padding: 0 22px; font-size: 14px; }
</style>
