<script setup lang="ts">
import { ElInput } from 'element-plus';
import type { UseCapabilities } from '../../composables/useCapabilities';

defineProps<{ capabilities: UseCapabilities }>();

// 重置自然语言请求为默认生成的 prompt
function resetPrompt(capabilities: UseCapabilities) {
  capabilities.aiPromptOverride.value = null;
}

// 复制 AI 响应结果到剪贴板
async function copyResult(text: string) {
  if (!text) return;
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    // 剪贴板不可用时静默失败，不阻塞主流程
  }
}
</script>

<template>
  <section data-test="workflow-ai" class="workflow-stage workflow-ai">
    <!-- 已发布列表只在右侧工具库面板（评审/AI 共用侧栏）；本页专注试运行，单列全宽。 -->
    <aside data-test="studio-ai-runner" class="ai-runner-focus" aria-label="AI 试运行">
      <div class="runner-header">
        <div class="runner-header__title">
          <span class="runner-header__eyebrow">AI 试运行</span>
          <strong class="runner-header__name">{{ capabilities.selected.value.name }}</strong>
          <small class="runner-header__meta">{{ capabilities.selected.value.backend.method }} {{ capabilities.selected.value.backend.path || '/' }}</small>
        </div>
        <span data-test="ai-preflight-state" class="runner-header__state" :class="capabilities.aiPreflightReady.value ? 'is-ready' : 'is-blocked'">{{ capabilities.aiPreflightState.value }}</span>
      </div>

      <label class="block-label">
        测试输入 JSON
        <el-input data-test="test-input" v-model="capabilities.testInputText.value" type="textarea" :rows="6" />
      </label>

      <label class="runner-prompt">
        <span>自然语言请求</span>
        <textarea v-model="capabilities.aiPromptText.value" rows="5" data-test="ai-prompt" />
      </label>

      <div class="runner-actions">
        <button data-test="run-ai-preflight" class="runner-primary" :disabled="!capabilities.aiPreflightReady.value || capabilities.aiLoading.value" @click="capabilities.runAIPreflight">
          {{ capabilities.aiLoading.value ? '请求中' : '发送到 AI 助手' }}
        </button>
        <button class="runner-secondary" :disabled="capabilities.aiLoading.value" @click="resetPrompt(capabilities)">
          重置提示
        </button>
      </div>

      <div class="runner-result-wrap">
        <div class="runner-result-head">
          <span class="runner-result-label">AI 响应</span>
          <button
            v-if="capabilities.aiPreflightResultText.value"
            class="runner-result-copy"
            @click="copyResult(capabilities.aiPreflightResultText.value)"
          >
            复制
          </button>
        </div>
        <pre data-test="ai-preflight-result" class="runner-result" :class="{ 'runner-result--empty': !capabilities.aiPreflightResultText.value }">{{ capabilities.aiPreflightResultText.value || '运行后这里会显示 AI 的响应结果，包括它调用了哪个能力、传了什么参数、得到什么返回。' }}</pre>
      </div>
    </aside>
  </section>
</template>
