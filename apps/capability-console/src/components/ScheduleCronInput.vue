<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { CronExpressionParser } from 'cron-parser';

const props = defineProps<{
  modelValue: string;
}>();

const emit = defineEmits<{
  (event: 'update:modelValue', value: string): void;
  (event: 'valid', value: boolean): void;
}>();

// 内部保留一份本地文本，便于在父组件未回写 modelValue 时也能即时反馈校验结果。
const localValue = ref(props.modelValue);

watch(
  () => props.modelValue,
  (value) => {
    if (value !== localValue.value) {
      localValue.value = value;
    }
  },
);

interface PreviewState {
  valid: boolean;
  nextRun: Date | null;
  error: string;
}

// 计算下次执行时间或解析错误。空字符串视为非法（避免提交空 cron）。
function computePreview(expr: string): PreviewState {
  const trimmed = expr.trim();
  if (trimmed === '') {
    return { valid: false, nextRun: null, error: '请输入 cron 表达式' };
  }
  try {
    const interval = CronExpressionParser.parse(trimmed, { currentDate: new Date() });
    const next = interval.next().toDate();
    return { valid: true, nextRun: next, error: '' };
  } catch (err) {
    const message = err instanceof Error ? err.message : '无效的 cron 表达式';
    return { valid: false, nextRun: null, error: message };
  }
}

const preview = computed<PreviewState>(() => computePreview(localValue.value));

const formattedNextRun = computed(() => {
  if (!preview.value.nextRun) return '';
  // 输出格式：YYYY-MM-DD HH:MM（按用户本地时区）。
  const date = preview.value.nextRun;
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
});

// preview.valid 变化时同步 emit valid 事件，避免父组件重复实现校验。
watch(
  () => preview.value.valid,
  (valid) => {
    emit('valid', valid);
  },
  { immediate: true },
);

function onInput(event: Event) {
  const value = (event.target as HTMLTextAreaElement).value;
  localValue.value = value;
  emit('update:modelValue', value);
}
</script>

<template>
  <div data-test="schedule-cron-input-wrapper" class="schedule-cron-input">
    <textarea
      data-test="schedule-cron-input"
      class="schedule-cron-textarea"
      rows="2"
      placeholder="0 2 * * 1-5  # 每个工作日 02:00"
      :value="localValue"
      @input="onInput"
    />
    <p v-if="preview.valid" data-test="schedule-cron-preview" class="schedule-cron-preview">
      下次执行：{{ formattedNextRun }}
    </p>
    <p v-else data-test="schedule-cron-error" class="schedule-cron-error">
      {{ preview.error }}
    </p>
  </div>
</template>

<style scoped>
.schedule-cron-input {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.schedule-cron-textarea {
  width: 100%;
  padding: var(--space-2) var(--space-3);
  background: var(--color-bg-elevated);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  color: var(--color-text-primary);
  font-family: var(--font-mono);
  font-size: var(--font-base);
  resize: vertical;
}

.schedule-cron-textarea:focus {
  outline: none;
  border-color: var(--color-accent);
}

.schedule-cron-preview {
  margin: 0;
  color: var(--color-success);
  font-size: var(--font-sm);
}

.schedule-cron-error {
  margin: 0;
  color: var(--color-danger);
  font-size: var(--font-sm);
}
</style>
