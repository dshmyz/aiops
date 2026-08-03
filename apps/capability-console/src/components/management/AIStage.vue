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
    <header class="stage-hero">
      <div class="stage-hero__icon" aria-hidden="true">
        <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/>
          <polygon points="10 9 8 12 11 12 9 15" fill="currentColor" stroke="none"/>
        </svg>
      </div>
      <div>
        <p class="eyebrow">第四步</p>
        <h2>让 AI 真的用起来</h2>
        <p>选一个已发布的能力，用自然语言提问，看看 AI 能不能正确调用它。这一步验证的不只是接口通不通，而是 AI 是否理解了你的意图。</p>
      </div>
    </header>

    <aside data-test="studio-ledger" class="review-list inventory" aria-label="已发布能力">
      <div class="section-heading">
        <div class="section-heading__title">
          <span class="section-heading__icon" aria-hidden="true">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/></svg>
          </span>
          <h2>已发布 AI 工具</h2>
        </div>
        <span class="section-heading__count">{{ capabilities.stats.value.published }} 个</span>
      </div>
      <div v-if="capabilities.filteredCapabilities.value.filter((capability) => capability.source === 'published').length === 0" class="empty ai-empty">
        <p>还没有已发布的能力</p>
        <small>回到第三步发布一个，再来这里试问</small>
      </div>
      <div v-else class="capability-card-list" data-test="capability-table-body">
        <article
          v-for="item in capabilities.filteredCapabilities.value.filter((capability) => capability.source === 'published')"
          :key="`${item.source}:${item.name}`"
          class="capability-card"
          :class="{ selected: item.name === capabilities.selected.value.name }"
          :data-test="`capability-row-${item.name}`"
          @click="capabilities.selectCapability(item)"
        >
          <div class="capability-card__head">
            <button class="link-button capability-card__name" :data-test="`edit-${item.name}`" @click.stop="capabilities.selectCapability(item)">
              {{ item.name }}
            </button>
            <span class="status-chip chip-source-published">已发布</span>
          </div>
          <small class="capability-card__meta">{{ item.domain }} / {{ item.resource_type }} / {{ item.backend.method }} {{ item.backend.path || '/' }}</small>
          <div class="capability-card__foot">
            <span class="next-action-chip" :data-test="`next-${item.name}`">{{ capabilities.nextActionLabel(item) }}</span>
          </div>
        </article>
      </div>
    </aside>

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
        <el-input data-test="test-input" v-model="capabilities.testInputText.value" type="textarea" :rows="4" />
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
