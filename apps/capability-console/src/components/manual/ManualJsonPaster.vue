<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { Capability } from '../../types';

const props = defineProps<{
  capability: Capability;
}>();
const emit = defineEmits<{
  update: [capability: Capability];
}>();

const text = ref('');
const error = ref('');

// 判断粘贴的 JSON 是否构成一个可用的 Capability 草稿
const parsed = computed<Capability | null>(() => {
  const t = text.value.trim();
  if (t === '') {
    error.value = '';
    return null;
  }
  let raw: unknown;
  try {
    raw = JSON.parse(t);
  } catch (e) {
    error.value = `JSON 解析失败：${e instanceof Error ? e.message : '格式错误'}`;
    return null;
  }
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    error.value = 'JSON 必须是对象结构';
    return null;
  }
  const obj = raw as Record<string, any>;
  const missing: string[] = [];
  if (!obj.name || typeof obj.name !== 'string') missing.push('name');
  if (typeof obj.backend !== 'object' || obj.backend === null) {
    missing.push('backend');
  } else {
    if (!obj.backend.base_url) missing.push('backend.base_url');
    if (!obj.backend.path) missing.push('backend.path');
  }
  if (missing.length > 0) {
    error.value = `缺少必填字段：${missing.join('、')}`;
    return null;
  }
  error.value = '';
  return mergeIntoCapability(props.capability, obj);
});

// JSON 解析成功后同步到父组件的 capability 草稿。
watch(parsed, (value) => {
  if (value) {
    emit('update', value);
  }
});

function mergeIntoCapability(base: Capability, obj: Record<string, any>): Capability {
  return {
    schema_version: Number(obj.schema_version) || base.schema_version,
    name: obj.name ?? base.name,
    status: (obj.status as Capability['status']) ?? 'needs_review',
    domain: obj.domain ?? base.domain,
    resource_type: obj.resource_type ?? base.resource_type,
    operation: obj.operation === 'write' ? 'write' : 'read',
    risk: (['low', 'medium', 'high'].includes(obj.risk) ? obj.risk : 'low') as Capability['risk'],
    backend: {
      ...base.backend,
      ...(obj.backend ?? {}),
    },
    input_schema: obj.input_schema && typeof obj.input_schema === 'object' ? obj.input_schema : base.input_schema,
    output: {
      ...base.output,
      ...(obj.output ?? {}),
      fields: obj.output?.fields && typeof obj.output.fields === 'object' ? obj.output.fields : (base.output.fields ?? {}),
    },
    auth: { ...base.auth, ...(obj.auth ?? {}) },
    ai: { ...base.ai, ...(obj.ai ?? {}) },
    governance: obj.governance ? { ...base.governance, ...obj.governance } : base.governance,
  };
}

const fieldCount = computed(() => (parsed.value ? Object.keys(parsed.value.input_schema).length : 0));
</script>

<template>
  <div class="manual-json">
    <textarea
      data-test="manual-json-input"
      v-model="text"
      class="manual-json__input"
      rows="10"
      spellcheck="false"
      placeholder='粘贴一个 Capability JSON，例如：&#10;{&#10;  "name": "redis.cluster.info.read",&#10;  "domain": "redis",&#10;  "resource_type": "cluster",&#10;  "operation": "read",&#10;  "risk": "low",&#10;  "backend": {&#10;    "adapter": "http",&#10;    "method": "GET",&#10;    "path": "/api/redis/clusters/{cluster}/info",&#10;    "base_url": "https://middleware.example.com",&#10;    "timeout_ms": 3000&#10;  },&#10;  "output": { "kind": "observation", "summary_template": "查询完成", "fields": { "status": "$.status" } }&#10;}'
    ></textarea>

    <p v-if="fieldCount > 0" data-test="manual-json-ok" class="manual-json__ok">
      已识别草稿：{{ parsed?.name }}（{{ parsed?.domain }}.{{ parsed?.resource_type }}，输入参数 {{ fieldCount }} 个）
    </p>
    <p v-else-if="error" data-test="manual-json-error" class="manual-json__error">{{ error }}</p>
    <p v-else class="manual-json__hint">JSON 解析成功后会自动填充下方草稿</p>
  </div>
</template>

<style scoped>
.manual-json__input {
  width: 100%;
  min-height: 180px;
  resize: vertical;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  line-height: 1.5;
  padding: 10px 12px;
  color: var(--color-text-primary, #fff);
  background: var(--color-bg, #0a0a0c);
  border: 1px solid var(--color-border, rgba(255, 255, 255, 0.1));
  border-radius: 6px;
}
.manual-json__input:focus {
  outline: none;
  border-color: var(--color-accent, #0a84ff);
}
.manual-json__ok {
  color: var(--color-success, #30d158);
  font-size: 12px;
  margin-top: 8px;
}
.manual-json__error {
  color: var(--color-danger, #ff453a);
  font-size: 12px;
  margin-top: 8px;
}
.manual-json__hint {
  color: var(--color-text-tertiary, rgba(235, 235, 245, 0.3));
  font-size: 12px;
  margin-top: 8px;
}
</style>