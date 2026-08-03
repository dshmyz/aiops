<script setup lang="ts">
import { ref } from 'vue';
import { createFeedback } from '../api';

const props = defineProps<{
  turnId: string;
  conversationId: string;
}>();

const emit = defineEmits<{
  (event: 'submitted', rating: 'up' | 'down'): void;
}>();

const selected = ref<'up' | 'down' | null>(null);
const submitting = ref(false);
const showCorrection = ref(false);
const correctionText = ref('');
const errorMessage = ref('');

async function submit(rating: 'up' | 'down') {
  if (submitting.value || selected.value !== null) return;
  if (rating === 'down' && !showCorrection.value) {
    showCorrection.value = true;
    return;
  }
  submitting.value = true;
  errorMessage.value = '';
  try {
    await createFeedback({
      conversation_id: props.conversationId,
      turn_id: props.turnId,
      rating,
      correction: rating === 'down' ? correctionText.value.trim() || undefined : undefined,
    });
    selected.value = rating;
    showCorrection.value = false;
    emit('submitted', rating);
  } catch (e) {
    errorMessage.value = e instanceof Error ? e.message : '提交失败';
  } finally {
    submitting.value = false;
  }
}

function cancelCorrection() {
  showCorrection.value = false;
  correctionText.value = '';
}
</script>

<template>
  <div data-test="message-feedback" class="feedback-buttons">
    <template v-if="!showCorrection">
      <button
        type="button"
        data-test="feedback-up"
        class="feedback-btn"
        :class="{ active: selected === 'up' }"
        :disabled="submitting || selected !== null"
        title="有帮助"
        @click="submit('up')"
      >
        <svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">
          <path fill="currentColor" d="M1 21h4V9H1v12zm22-11c0-1.1-.9-2-2-2h-6.31l.95-4.57.03-.32c0-.41-.17-.79-.44-1.06L14.17 1 7.59 7.59C7.22 7.95 7 8.45 7 9v10c0 1.1.9 2 2 2h9c.83 0 1.54-.5 1.84-1.22l3.02-7.05c.09-.23.14-.47.14-.73v-2z"/>
        </svg>
      </button>
      <button
        type="button"
        data-test="feedback-down"
        class="feedback-btn"
        :class="{ active: selected === 'down' }"
        :disabled="submitting || selected !== null"
        title="需改进"
        @click="submit('down')"
      >
        <svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">
          <path fill="currentColor" d="M15 3H6c-.83 0-1.54.5-1.84 1.22l-3.02 7.05c-.09.23-.14.47-.14.73v2c0 1.1.9 2 2 2h6.31l-.95 4.57-.03.32c0 .41.17.79.44 1.06L9.83 23l6.59-6.59c.36-.36.58-.86.58-1.41V5c0-1.1-.9-2-2-2zm4 0v12h4V3h-4z"/>
        </svg>
      </button>
    </template>

    <div v-if="showCorrection" data-test="feedback-correction" class="feedback-correction">
      <input
        v-model="correctionText"
        class="correction-input"
        placeholder="请描述问题或建议…"
        :disabled="submitting"
        @keydown.enter="submit('down')"
        @keydown.esc="cancelCorrection"
      />
      <button
        type="button"
        data-test="feedback-correction-submit"
        class="correction-submit"
        :disabled="submitting"
        @click="submit('down')"
      >
        {{ submitting ? '提交中' : '提交' }}
      </button>
      <button
        type="button"
        class="correction-cancel"
        :disabled="submitting"
        @click="cancelCorrection"
      >
        取消
      </button>
    </div>

    <span v-if="selected" data-test="feedback-thanks" class="feedback-thanks">
      {{ selected === 'up' ? '感谢反馈' : '已记录' }}
    </span>
    <span v-if="errorMessage" class="feedback-error">{{ errorMessage }}</span>
  </div>
</template>

<style scoped>
.feedback-buttons {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.feedback-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  background: transparent;
  border: 1px solid transparent;
  border-radius: var(--radius-md, 6px);
  color: var(--color-text-tertiary, #a0aec0);
  cursor: pointer;
  transition: background 0.1s, color 0.1s, border-color 0.1s;
}

.feedback-btn:hover:not(:disabled) {
  background: var(--color-bg-hover, #edf2f7);
  color: var(--color-text-secondary, #718096);
  border-color: var(--color-border, #e2e8f0);
}

.feedback-btn.active {
  color: var(--color-accent, #3182ce);
  background: rgba(49, 130, 206, 0.1);
  border-color: rgba(49, 130, 206, 0.3);
}

.feedback-btn:disabled {
  cursor: default;
  opacity: 0.6;
}

.feedback-correction {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.correction-input {
  width: 200px;
  padding: 4px 8px;
  border: 1px solid var(--color-border, #e2e8f0);
  border-radius: var(--radius-md, 6px);
  font-size: var(--font-xs, 12px);
  font-family: inherit;
  color: var(--color-text-primary, #2d3748);
  background: var(--color-bg, #fff);
}

.correction-input:focus {
  outline: 2px solid var(--color-accent, #3182ce);
  outline-offset: 0;
  border-color: transparent;
}

.correction-submit,
.correction-cancel {
  padding: 4px 10px;
  border: 1px solid var(--color-border, #e2e8f0);
  border-radius: var(--radius-md, 6px);
  font-size: var(--font-xs, 12px);
  cursor: pointer;
  background: transparent;
  color: var(--color-text-secondary, #718096);
  transition: background 0.1s, color 0.1s;
}

.correction-submit {
  background: var(--color-accent, #3182ce);
  color: #fff;
  border-color: var(--color-accent, #3182ce);
}

.correction-submit:hover:not(:disabled) {
  opacity: 0.9;
}

.correction-cancel:hover:not(:disabled) {
  background: var(--color-bg-hover, #edf2f7);
}

.correction-submit:disabled,
.correction-cancel:disabled {
  cursor: not-allowed;
  opacity: 0.6;
}

.feedback-thanks {
  font-size: var(--font-xs, 12px);
  color: var(--color-success, #38a169);
}

.feedback-error {
  font-size: var(--font-xs, 12px);
  color: var(--color-danger, #e53e3e);
}
</style>
