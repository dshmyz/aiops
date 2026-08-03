<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { ManagedCapability, ScheduleKind, SchedulePreset, ScheduledTask } from '../types';
import SchedulePresetPicker from './SchedulePresetPicker.vue';
import ScheduleCronInput from './ScheduleCronInput.vue';

const props = defineProps<{
  task?: ScheduledTask | null;
  capabilities: ManagedCapability[];
}>();

const emit = defineEmits<{
  (event: 'submit', payload: {
    name: string;
    capability_name: string;
    input: Record<string, unknown>;
    schedule_kind: ScheduleKind;
    preset: SchedulePreset | null;
    cron_expr: string | null;
  }): void;
  (event: 'cancel'): void;
}>();

// 内部状态：name / capability_name / inputText (JSON 字符串) / schedule_kind / preset / cron_expr
const name = ref('');
const capabilityName = ref('');
const inputText = ref('');
const scheduleKind = ref<ScheduleKind>('preset');
const preset = ref<SchedulePreset | null>(null);
const cronExpr = ref('');
// cron 表达式的合法性由子组件实时校验后回传，避免父组件重复实现 cron 解析。
const cronValid = ref(false);

// 编辑模式：传入 task prop 时字段预填。使用 watch 以便 task 变化时重新填充。
function applyTask(task: ScheduledTask | null | undefined) {
  if (!task) {
    name.value = '';
    capabilityName.value = '';
    inputText.value = '';
    scheduleKind.value = 'preset';
    preset.value = null;
    cronExpr.value = '';
    return;
  }
  name.value = task.name;
  capabilityName.value = task.capability_name;
  inputText.value = JSON.stringify(task.input ?? {}, null, 2);
  scheduleKind.value = task.schedule_kind;
  preset.value = task.preset;
  cronExpr.value = task.cron_expr ?? '';
}

watch(
  () => props.task,
  (task) => applyTask(task),
  { immediate: true },
);

// input JSON 解析：非法时返回 null，便于校验逻辑判断。
function parseInput(text: string): Record<string, unknown> | null {
  const trimmed = text.trim();
  if (trimmed === '') {
    return {};
  }
  try {
    const value = JSON.parse(trimmed) as unknown;
    if (value && typeof value === 'object' && !Array.isArray(value)) {
      return value as Record<string, unknown>;
    }
    return null;
  } catch {
    return null;
  }
}

const parsedInput = computed(() => parseInput(inputText.value));

// 表单校验：name 非空 / capability 非空 / preset 模式 preset 非空 / cron 模式 cron_expr 合法 / input JSON 合法
const canSubmit = computed(() => {
  if (name.value.trim() === '') return false;
  if (capabilityName.value === '') return false;
  if (parsedInput.value === null) return false;
  if (scheduleKind.value === 'preset') {
    return preset.value !== null;
  }
  if (scheduleKind.value === 'cron') {
    return cronValid.value;
  }
  return false;
});

function onPresetUpdate(value: SchedulePreset) {
  preset.value = value;
}

function onCronUpdate(value: string) {
  cronExpr.value = value;
}

function onCronValid(value: boolean) {
  cronValid.value = value;
}

function onSubmit() {
  if (!canSubmit.value) return;
  // input 字段从 JSON 字符串解析；canSubmit 已保证 parsedInput 非 null
  const input = parsedInput.value ?? {};
  emit('submit', {
    name: name.value.trim(),
    capability_name: capabilityName.value,
    input,
    schedule_kind: scheduleKind.value,
    preset: scheduleKind.value === 'preset' ? preset.value : null,
    cron_expr: scheduleKind.value === 'cron' ? cronExpr.value : null,
  });
}

function onCancel() {
  emit('cancel');
}
</script>

<template>
  <form data-test="scheduled-task-form" class="scheduled-task-form" @submit.prevent="onSubmit">
    <label class="form-field">
      <span class="form-label">任务名称</span>
      <input
        data-test="scheduled-task-name"
        v-model="name"
        class="form-input"
        type="text"
        placeholder="例如：minio 每日巡检"
      />
    </label>

    <label class="form-field">
      <span class="form-label">能力</span>
      <select data-test="scheduled-task-capability" v-model="capabilityName" class="form-select">
        <option value="">请选择能力</option>
        <option v-for="capability in capabilities" :key="capability.name" :value="capability.name">
          {{ capability.name }}
        </option>
      </select>
    </label>

    <label class="form-field">
      <span class="form-label">输入参数 (JSON)</span>
      <textarea
        data-test="scheduled-task-input"
        v-model="inputText"
        class="form-textarea"
        rows="5"
        placeholder='{"environment":"prod","cluster":"m1","bucket":"archive"}'
      />
    </label>

    <label class="form-field">
      <span class="form-label">调度类型</span>
      <select data-test="scheduled-task-schedule-kind" v-model="scheduleKind" class="form-select">
        <option value="preset">预设模板</option>
        <option value="cron">Cron 表达式</option>
      </select>
    </label>

    <div v-if="scheduleKind === 'preset'" class="form-field">
      <span class="form-label">频率</span>
      <SchedulePresetPicker :modelValue="preset" @update:modelValue="onPresetUpdate" />
    </div>

    <div v-else class="form-field">
      <span class="form-label">Cron 表达式</span>
      <ScheduleCronInput
        :modelValue="cronExpr"
        @update:modelValue="onCronUpdate"
        @valid="onCronValid"
      />
    </div>

    <div class="form-actions">
      <button data-test="scheduled-task-submit" type="button" class="form-submit" :disabled="!canSubmit" @click="onSubmit">
        保存
      </button>
      <button data-test="scheduled-task-cancel" type="button" class="form-cancel" @click="onCancel">
        取消
      </button>
    </div>
  </form>
</template>

<style scoped>
.scheduled-task-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.form-field {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.form-label {
  color: var(--color-text-secondary);
  font-size: var(--font-sm);
}

.form-input,
.form-select,
.form-textarea {
  padding: var(--space-2) var(--space-3);
  background: var(--color-bg-elevated);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  color: var(--color-text-primary);
  font-family: var(--font-ui);
  font-size: var(--font-base);
}

.form-textarea {
  font-family: var(--font-mono);
  resize: vertical;
}

.form-input:focus,
.form-select:focus,
.form-textarea:focus {
  outline: none;
  border-color: var(--color-accent);
}

.form-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-3);
  margin-top: var(--space-2);
}

/* Submit / cancel buttons mirror the global .btn-primary / .btn-ghost
   variants so the form is visually consistent with the rest of the app. */
.form-submit {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 9px 16px;
  background: var(--gradient-brand);
  color: #fff;
  border: none;
  border-radius: var(--radius-md);
  font-size: var(--font-md);
  font-weight: 500;
  cursor: pointer;
  transition: all 0.15s ease;
  white-space: nowrap;
  box-shadow: 0 2px 8px rgba(56, 189, 248, 0.22);
}

.form-submit:hover:not(:disabled) {
  box-shadow: 0 4px 14px rgba(56, 189, 248, 0.38);
  transform: translateY(-1px);
}

.form-submit:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.form-cancel {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 8px 14px;
  background: transparent;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-md);
  color: var(--color-text-secondary);
  font-size: var(--font-md);
  font-weight: 500;
  cursor: pointer;
  transition: all 0.15s ease;
  white-space: nowrap;
}

.form-cancel:hover {
  border-color: var(--color-text-muted);
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
}
</style>
