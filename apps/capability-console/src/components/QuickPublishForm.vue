<script setup lang="ts">
import { computed, ref } from 'vue';
import { ElTag, ElButton } from 'element-plus';
import type { TagProps } from 'element-plus';
import { quickPublishCapability } from '../api';
import { useQuickPublishInfer } from '../composables/useQuickPublishInfer';
import type { HttpMethod, ManagedCapability } from '../types';

const emit = defineEmits<{
  published: [capability: ManagedCapability];
  error: [message: string];
}>();

// 核心必填字段（3个）
const backendBaseURL = ref('https://middleware.example.com');
const path = ref('');
const description = ref('');
const method = ref<HttpMethod>('GET');

// 自动推断字段（可手动调整）
const name = ref('');
const domain = ref('');
const resourceType = ref('');
const summaryTemplate = ref('');

// 状态
const submitting = ref(false);
const validationError = ref('');
const showAdvanced = ref(false);

const methodOptions: { value: HttpMethod; label: string }[] = [
  { value: 'GET', label: 'GET（查询）' },
  { value: 'POST', label: 'POST（创建）' },
  { value: 'PUT', label: 'PUT（更新）' },
  { value: 'PATCH', label: 'PATCH（修改）' },
  { value: 'DELETE', label: 'DELETE（删除）' },
];

// AI 补全：只覆盖用户未手动编辑过的字段；推断后可重新补全。
const infer = useQuickPublishInfer({
  baseURL: backendBaseURL,
  path,
  description,
  method,
  name,
  domain,
  resourceType,
  summaryTemplate,
});

const pathVariablePreview = computed(() => {
  const matches = path.value.match(/\{([a-zA-Z0-9_]+)\}/g) ?? [];
  return matches.map((match) => match.slice(1, -1));
});

const canSubmit = computed(() => {
  return (
    !submitting.value &&
    backendBaseURL.value.trim() !== '' &&
    path.value.trim() !== '' &&
    description.value.trim() !== ''
  );
});

const riskLevel = computed(() => {
  if (method.value === 'DELETE') return 'high';
  if (method.value === 'GET') return 'low';
  return 'medium';
});

const riskLabel = computed(() => {
  const map: Record<string, string> = {
    low: '低风险',
    medium: '中风险',
    high: '高风险',
  };
  return map[riskLevel.value] || '未知';
});

const riskTagType = computed<TagProps['type']>(() => {
  const map: Record<string, TagProps['type']> = {
    low: 'success',
    medium: 'warning',
    high: 'danger',
  };
  return map[riskLevel.value] || 'info';
});

function buildPayload() {
  return {
    name: name.value.trim(),
    domain: domain.value.trim(),
    resource_type: resourceType.value.trim(),
    backend_base_url: backendBaseURL.value.trim(),
    method: method.value,
    path: path.value.trim(),
    description: description.value.trim(),
    ...(summaryTemplate.value.trim() !== '' ? { summary_template: summaryTemplate.value.trim() } : {}),
  };
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
    // 重置表单与推断状态
    path.value = '';
    description.value = '';
    name.value = '';
    domain.value = '';
    resourceType.value = '';
    summaryTemplate.value = '';
    showAdvanced.value = false;
    infer.reset();
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
      <h3>快速发布能力</h3>
      <span>只需填写 3 个必填项，AI 自动推断其余配置</span>
    </div>

    <!-- 必填字段（核心3项） -->
    <div class="quick-publish-grid">
      <label class="wide">
        <span class="required">后端 Base URL</span>
        <input
          data-test="quick-publish-base-url"
          v-model="backendBaseURL"
          class="filter-input"
          placeholder="https://middleware.example.com"
        />
      </label>

      <label>
        <span class="required">HTTP 方法</span>
        <select data-test="quick-publish-method" v-model="method" class="filter-input">
          <option v-for="opt in methodOptions" :key="opt.value" :value="opt.value">
            {{ opt.label }}
          </option>
        </select>
      </label>

      <label class="wide">
        <span class="required">接口路径</span>
        <input
          data-test="quick-publish-path"
          v-model="path"
          class="filter-input"
          placeholder="/api/redis/clusters/{cluster}/info"
        />
      </label>

      <label class="wide">
        <span class="required">AI 描述</span>
        <input
          data-test="quick-publish-description"
          v-model="description"
          class="filter-input"
          placeholder="用一句话描述这个接口的作用，如：查询 Redis 集群信息"
        />
      </label>
    </div>

    <!-- 路径变量提示 -->
    <div v-if="pathVariablePreview.length > 0" data-test="quick-publish-path-variables" class="quick-publish-derived">
      <span>识别到的路径变量</span>
      <el-tag v-for="variable in pathVariablePreview" :key="variable" size="small">{{ variable }}</el-tag>
    </div>

    <!-- 智能推断状态 -->
    <div v-if="infer.inferring.value" class="quick-publish-inferring">
      <span class="inferring-spinner"></span>
      <span>AI 正在自动推断配置...</span>
    </div>
    <!-- 已补全提示 -->
    <div v-else-if="infer.hasInferred.value" data-test="quick-publish-inferred" class="quick-publish-inferred">
      <span v-if="infer.inferredCount.value > 0">已补全 {{ infer.inferredCount.value }} 个字段</span>
      <span v-else>字段已由手动填写，AI 未覆盖</span>
    </div>

    <!-- 风险等级提示 -->
    <div class="quick-publish-risk">
      <span>风险等级：</span>
      <el-tag :type="riskTagType" size="small">{{ riskLabel }}</el-tag>
      <span v-if="riskLevel === 'high'" class="risk-hint">高风险操作需要管理员审批</span>
    </div>

    <!-- 高级选项（可折叠） -->
    <div class="quick-publish-advanced">
      <button class="text-btn" @click="showAdvanced = !showAdvanced">
        {{ showAdvanced ? '收起高级选项' : '展开高级选项' }}
        <span class="arrow">{{ showAdvanced ? '▲' : '▼' }}</span>
      </button>

      <div v-show="showAdvanced" class="advanced-fields">
        <div class="quick-publish-grid">
          <label>
            <span>能力名称 {{ infer.hasInferred.value ? '（自动推断）' : '' }}</span>
            <input
              data-test="quick-publish-name"
              v-model="name"
              class="filter-input"
              :placeholder="infer.hasInferred.value ? name : '自动推断'"
              @input="infer.markUserEdited('name')"
            />
          </label>
          <label>
            <span>领域 {{ infer.hasInferred.value ? '（自动推断）' : '' }}</span>
            <input
              data-test="quick-publish-domain"
              v-model="domain"
              class="filter-input"
              :placeholder="infer.hasInferred.value ? domain : '自动推断'"
              @input="infer.markUserEdited('domain')"
            />
          </label>
          <label>
            <span>资源类型 {{ infer.hasInferred.value ? '（自动推断）' : '' }}</span>
            <input
              data-test="quick-publish-resource"
              v-model="resourceType"
              class="filter-input"
              :placeholder="infer.hasInferred.value ? resourceType : '自动推断'"
              @input="infer.markUserEdited('resource_type')"
            />
          </label>
          <label class="wide">
            <span>摘要模板（可选）</span>
            <input
              data-test="quick-publish-summary"
              v-model="summaryTemplate"
              class="filter-input"
              :placeholder="infer.hasInferred.value ? summaryTemplate : '自动生成'"
              @input="infer.markUserEdited('summary_template')"
            />
          </label>
        </div>
      </div>
    </div>

    <!-- AI 补全按钮：未推断显示"一键补全"，已推断显示"重新补全" -->
    <div class="quick-publish-actions">
      <el-button
        v-if="infer.canInfer.value"
        :loading="infer.inferring.value"
        size="small"
        @click="infer.doInfer()"
      >
        {{ infer.hasInferred.value ? 'AI 重新补全' : 'AI 一键补全' }}
      </el-button>
    </div>

    <p v-if="validationError" data-test="quick-publish-error" class="quick-publish-error">{{ validationError }}</p>

    <button
      data-test="quick-publish-submit"
      class="primary-inline"
      :disabled="!canSubmit"
      @click="submit"
    >
      {{ submitting ? '发布中' : '快速发布' }}
    </button>
  </section>
</template>

<style scoped>
.required::after {
  content: ' *';
  color: var(--color-danger, #f56c6c);
}

.quick-publish-inferring {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 0;
  color: var(--color-text-secondary, #909399);
  font-size: 13px;
}

.quick-publish-inferred {
  padding: 8px 0;
  color: var(--color-success, #67c23a);
  font-size: 13px;
}

.inferring-spinner {
  width: 14px;
  height: 14px;
  border: 2px solid var(--color-border, #dcdfe6);
  border-top-color: var(--color-primary, #409eff);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.quick-publish-risk {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 0;
  font-size: 13px;
  color: var(--color-text-secondary, #909399);
}

.risk-hint {
  color: var(--color-warning, #e6a23c);
  font-size: 12px;
}

.quick-publish-advanced {
  margin: 12px 0;
  padding-top: 8px;
  border-top: 1px solid var(--color-border-lighter, #ebeef5);
}

.text-btn {
  background: none;
  border: none;
  color: var(--color-primary, #409eff);
  cursor: pointer;
  font-size: 13px;
  padding: 4px 0;
  display: flex;
  align-items: center;
  gap: 4px;
}

.text-btn:hover {
  opacity: 0.8;
}

.arrow {
  font-size: 10px;
}

.advanced-fields {
  margin-top: 12px;
  padding: 12px;
  background: var(--bg-color-page, #f5f7fa);
  border-radius: 4px;
}

.quick-publish-actions {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 8px;
}
</style>
