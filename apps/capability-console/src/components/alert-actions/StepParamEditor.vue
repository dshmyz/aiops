<script setup lang="ts">
import { computed } from 'vue';
import type { AdminTool, AdminToolField, AlertActionStep } from '../../types';

const props = defineProps<{
  step: AlertActionStep;
  /** 当前步骤绑定的工具；无 schema 时退化为 key-value 自由输入 */
  tool?: AdminTool;
}>();

const emit = defineEmits<{
  (event: 'update', step: AlertActionStep): void;
}>();

/** 按 schema 顺序展开字段；无 schema 时返回 null（走自由 key-value 模式）。 */
const fields = computed<Array<{ key: string; field: AdminToolField }> | null>(() => {
  const schema = props.tool?.input_schema;
  if (!schema || Object.keys(schema).length === 0) return null;
  const entries = Object.entries(schema);
  // 必填在前，其余保持 schema 顺序。
  entries.sort((a, b) => Number(b[1].required ?? false) - Number(a[1].required ?? false));
  return entries.map(([key, field]) => ({ key, field }));
});

function setValue(key: string, value: string) {
  const input = { ...props.step.input, [key]: value };
  emit('update', { ...props.step, input });
}

function removeKey(key: string) {
  const input = { ...props.step.input };
  delete input[key];
  emit('update', { ...props.step, input });
}
</script>

<template>
  <div class="step-params" data-test="step-param-editor">
    <!-- schema 驱动 -->
    <template v-if="fields">
      <div v-for="f in fields" :key="f.key" class="param-row">
        <label class="param-label" :title="f.field.description">
          {{ f.key }}
          <span v-if="f.field.required" class="required-star">*</span>
          <span v-if="f.field.type" class="param-type">{{ f.field.type }}</span>
        </label>
        <el-select
          v-if="f.field.enum && f.field.enum.length > 0"
          :model-value="step.input[f.key] ?? ''"
          filterable
          allow-create
          clearable
          :placeholder="f.field.examples?.[0] ?? f.key"
          @update:model-value="(v: string) => setValue(f.key, v)"
        >
          <el-option v-for="opt in f.field.enum" :key="opt" :value="opt" :label="opt" />
        </el-select>
        <el-input
          v-else
          :model-value="step.input[f.key] ?? ''"
          :placeholder="f.field.examples?.[0] ?? f.key"
          @update:model-value="(v: string) => setValue(f.key, v)"
        />
      </div>
      <p v-if="fields.length === 0" class="param-hint">该工具无参数</p>
    </template>

    <!-- 无 schema：自由 key-value -->
    <template v-else>
      <div v-for="(val, key) in step.input" :key="key" class="param-row">
        <span class="param-key">{{ key }}</span>
        <el-input :model-value="val" @update:model-value="(v: string) => setValue(key, v)" />
        <button type="button" class="param-remove" title="移除参数" @click="removeKey(key)">×</button>
      </div>
      <button type="button" class="param-add" @click="setValue('param', '')">+ 参数</button>
      <p class="param-hint">该工具未提供参数定义，可自由填写键值对（支持 {labels.xxx} 模板）</p>
    </template>
  </div>
</template>

<style scoped>
.step-params { display: flex; flex-direction: column; gap: 8px; }
.param-row { display: flex; align-items: center; gap: 8px; }
.param-row .el-input, .param-row .el-select { flex: 1; }
.param-label { width: 160px; flex: none; font-size: 12px; color: var(--color-text-secondary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.param-type { color: var(--color-text-tertiary); font-size: 11px; margin-left: 4px; }
.required-star { color: var(--color-danger); }
.param-key { font-size: 12px; color: var(--color-text-secondary); min-width: 80px; }
.param-remove { border: none; background: none; color: var(--color-text-tertiary); cursor: pointer; font-size: 14px; }
.param-add { align-self: flex-start; border: 1px dashed var(--color-border); background: none; padding: 2px 10px; border-radius: 4px; font-size: 12px; cursor: pointer; color: var(--color-text-secondary); }
.param-hint { font-size: 11px; color: var(--color-text-tertiary); margin: 0; }
</style>
