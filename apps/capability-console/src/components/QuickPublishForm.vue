<script setup lang="ts">
import { computed, ref } from 'vue';
import { ElTag } from 'element-plus';
import { quickPublishCapability } from '../api';
import type { ManagedCapability, QuickPublishPayload } from '../types';

const emit = defineEmits<{
  published: [capability: ManagedCapability];
  error: [message: string];
}>();

const name = ref('');
const domain = ref('');
const resourceType = ref('');
const backendBaseURL = ref('https://middleware.example.com');
const path = ref('');
const description = ref('');
const summaryTemplate = ref('');
const submitting = ref(false);
const validationError = ref('');

const pathVariablePreview = computed(() => {
  const matches = path.value.match(/\{([a-zA-Z0-9_]+)\}/g) ?? [];
  return matches.map((match) => match.slice(1, -1));
});

const canSubmit = computed(() => {
  return (
    !submitting.value &&
    name.value.trim() !== '' &&
    domain.value.trim() !== '' &&
    resourceType.value.trim() !== '' &&
    backendBaseURL.value.trim() !== '' &&
    path.value.trim() !== '' &&
    description.value.trim() !== ''
  );
});

function buildPayload(): QuickPublishPayload {
  const payload: QuickPublishPayload = {
    name: name.value.trim(),
    domain: domain.value.trim(),
    resource_type: resourceType.value.trim(),
    backend_base_url: backendBaseURL.value.trim(),
    method: 'GET',
    path: path.value.trim(),
    description: description.value.trim(),
  };
  if (summaryTemplate.value.trim() !== '') {
    payload.summary_template = summaryTemplate.value.trim();
  }
  return payload;
}

async function submit() {
  if (!canSubmit.value) {
    return;
  }
  submitting.value = true;
  validationError.value = '';
  try {
    const published = await quickPublishCapability(buildPayload());
    emit('published', published);
    name.value = '';
    domain.value = '';
    resourceType.value = '';
    path.value = '';
    description.value = '';
    summaryTemplate.value = '';
  } catch (err) {
    const message = err instanceof Error ? err.message : '快速发布失败';
    validationError.value = message;
    emit('error', message);
  } finally {
    submitting.value = false;
  }
}
</script>

<template>
  <section data-test="quick-publish-form" class="quick-publish-form">
    <div class="quick-publish-heading">
      <h3>快速发布读能力</h3>
      <span>只需 URL、方法和描述，跳过 Swagger 导入和草稿评审</span>
    </div>
    <div class="quick-publish-grid">
      <label>
        <span>能力名称</span>
        <input data-test="quick-publish-name" v-model="name" class="filter-input" placeholder="redis.cluster.info.read" />
      </label>
      <label>
        <span>领域</span>
        <input data-test="quick-publish-domain" v-model="domain" class="filter-input" placeholder="redis" />
      </label>
      <label>
        <span>资源类型</span>
        <input data-test="quick-publish-resource" v-model="resourceType" class="filter-input" placeholder="cluster" />
      </label>
      <label class="wide">
        <span>中间件后台 Base URL</span>
        <input data-test="quick-publish-base-url" v-model="backendBaseURL" class="filter-input" placeholder="https://middleware.example.com" />
      </label>
      <label class="wide">
        <span>接口路径</span>
        <input data-test="quick-publish-path" v-model="path" class="filter-input" placeholder="/api/redis/clusters/{cluster}/info" />
      </label>
      <label class="wide">
        <span>AI 描述</span>
        <input data-test="quick-publish-description" v-model="description" class="filter-input" placeholder="Read Redis cluster info" />
      </label>
      <label class="wide">
        <span>摘要模板（可选）</span>
        <input data-test="quick-publish-summary" v-model="summaryTemplate" class="filter-input" placeholder="Cluster {cluster} info retrieved" />
      </label>
    </div>
    <div v-if="pathVariablePreview.length > 0" data-test="quick-publish-path-variables" class="quick-publish-derived">
      <span>识别到的路径变量</span>
      <el-tag v-for="variable in pathVariablePreview" :key="variable" size="small">{{ variable }}</el-tag>
    </div>
    <p v-if="validationError" data-test="quick-publish-error" class="quick-publish-error">{{ validationError }}</p>
    <button data-test="quick-publish-submit" class="primary-inline" :disabled="!canSubmit" @click="submit">
      {{ submitting ? '发布中' : '快速发布' }}
    </button>
  </section>
</template>
