<script setup lang="ts">
import { ref, watch } from 'vue';
import type { Capability } from '../../types';

const props = defineProps<{
  capability: Capability;
}>();
const emit = defineEmits<{
  update: [capability: Capability];
}>();

// 只暴露高频字段，复杂/进阶字段（governance、output.fields 等）留给评审阶段细调。
function assign(name: string, value: unknown) {
  emit('update', { ...props.capability, [name]: value });
}
</script>

<template>
  <div class="manual-form">
    <div class="manual-form__grid">
      <label class="wide">
        <span class="required">能力名称</span>
        <input
          data-test="manual-form-name"
          class="filter-input"
          :value="capability.name"
          placeholder="如 redis.cluster.info.read"
          @input="assign('name', ($event.target as HTMLInputElement).value)"
        />
      </label>

      <label>
        <span class="required">领域</span>
        <input
          data-test="manual-form-domain"
          class="filter-input"
          :value="capability.domain"
          placeholder="如 redis"
          @input="assign('domain', ($event.target as HTMLInputElement).value)"
        />
      </label>
      <label>
        <span class="required">资源类型</span>
        <input
          data-test="manual-form-resource"
          class="filter-input"
          :value="capability.resource_type"
          placeholder="如 cluster"
          @input="assign('resource_type', ($event.target as HTMLInputElement).value)"
        />
      </label>

      <label>
        <span class="required">操作类型</span>
        <select
          data-test="manual-form-operation"
          class="filter-input"
          :value="capability.operation"
          @change="assign('operation', ($event.target as HTMLSelectElement).value)"
        >
          <option value="read">读取（read）</option>
          <option value="write">写入（write）</option>
        </select>
      </label>
      <label>
        <span class="required">风险等级</span>
        <select
          data-test="manual-form-risk"
          class="filter-input"
          :value="capability.risk"
          @change="assign('risk', ($event.target as HTMLSelectElement).value)"
        >
          <option value="low">低</option>
          <option value="medium">中</option>
          <option value="high">高</option>
        </select>
      </label>

      <label>
        <span class="required">HTTP 方法</span>
        <select
          data-test="manual-form-method"
          class="filter-input"
          :value="capability.backend.method"
          @change="assign('backend', { ...capability.backend, method: ($event.target as HTMLSelectElement).value })"
        >
          <option value="GET">GET</option>
          <option value="POST">POST</option>
          <option value="PUT">PUT</option>
          <option value="PATCH">PATCH</option>
          <option value="DELETE">DELETE</option>
        </select>
      </label>

      <label class="wide">
        <span class="required">接口路径</span>
        <input
          data-test="manual-form-path"
          class="filter-input"
          :value="capability.backend.path"
          placeholder="/api/redis/clusters/{cluster}/info"
          @input="assign('backend', { ...capability.backend, path: ($event.target as HTMLInputElement).value })"
        />
      </label>

      <label class="wide">
        <span class="required">后端 Base URL</span>
        <input
          data-test="manual-form-base-url"
          class="filter-input"
          :value="capability.backend.base_url"
          placeholder="https://middleware.example.com"
          @input="assign('backend', { ...capability.backend, base_url: ($event.target as HTMLInputElement).value })"
        />
      </label>

      <label class="wide">
        <span class="required">AI 描述</span>
        <input
          data-test="manual-form-description"
          class="filter-input"
          :value="capability.ai.description"
          placeholder="用一句话描述这个接口的作用"
          @input="assign('ai', { ...capability.ai, description: ($event.target as HTMLInputElement).value })"
        />
      </label>
    </div>
  </div>
</template>

<style scoped>
.required::after {
  content: ' *';
  color: var(--color-danger, #f56c6c);
}
.manual-form__grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}
.manual-form__grid .wide {
  grid-column: 1 / -1;
}
</style>