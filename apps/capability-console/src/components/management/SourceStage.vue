<script setup lang="ts">
import { ElButton } from 'element-plus';
import QuickPublishForm from '../QuickPublishForm.vue';
import ManualCreatePanel from '../manual/ManualCreatePanel.vue';
import StatsGrid from './StatsGrid.vue';
import SwaggerSourceStrip from './SwaggerSourceStrip.vue';
import type { UseCapabilities } from '../../composables/useCapabilities';

defineProps<{ capabilities: UseCapabilities }>();
</script>

<template>
  <section data-test="workflow-start" class="workflow-stage workflow-start">
    <div class="stage-main">
      <header class="stage-hero">
        <div class="stage-hero__icon" aria-hidden="true">
          <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/><polyline points="3.27 6.96 12 12.01 20.73 6.96"/><line x1="12" y1="22.08" x2="12" y2="12"/></svg>
        </div>
        <div>
          <p class="eyebrow">第一步</p>
          <h2>先接入一批后台 API</h2>
          <p>现在只做一件事：把 Swagger 拉进来，系统会先生成候选能力。没有真实环境时，先用内置示例跑通 MinIO、Kafka、GlusterFS。</p>
        </div>
      </header>
      <section data-test="import-wizard" class="import-wizard compact-import" aria-label="Swagger 接入">
        <div class="import-wizard__label">
          <span class="import-wizard__dot"></span>
          Swagger 源
        </div>
        <SwaggerSourceStrip
          class="import-strip"
          :url="capabilities.importOpenAPIURLText.value"
          :base-url="capabilities.importBackendBaseURL.value"
          :loading="capabilities.importPreviewLoading.value"
          @update:url="capabilities.importOpenAPIURLText.value = $event"
          @update:base-url="capabilities.importBackendBaseURL.value = $event"
          @preview="capabilities.previewSwaggerURL"
          @clear-preview="capabilities.clearImportPreview"
        />
        <strong v-if="capabilities.importMessage.value" data-test="import-result" class="import-message">{{ capabilities.importMessage.value }}</strong>
        <div data-test="import-commit-summary" class="commit-summary commit-summary-empty">
          <span class="commit-hint">先预览 API，再到下一步选择候选能力</span>
          <el-button data-test="commit-openapi-import" type="primary" disabled>生成 Capability 草稿</el-button>
        </div>
      </section>
      <details data-test="quick-publish-panel" class="quick-publish-panel">
        <summary>
          <span class="quick-publish-panel__plus" aria-hidden="true">+</span>
          没有 Swagger？快速发布单个能力
        </summary>
        <QuickPublishForm
          @published="capabilities.handleQuickPublished"
          @error="capabilities.handleQuickPublishError"
        />
      </details>
      <details data-test="manual-create-panel" class="quick-publish-panel">
        <summary>
          <span class="quick-publish-panel__plus" aria-hidden="true">+</span>
          手写 Capability 草稿（表单 / 粘贴 JSON）
        </summary>
        <ManualCreatePanel
          @created="capabilities.openManualCapability"
        />
      </details>
    </div>
    <aside class="stage-side">
      <h3>当前能力状态</h3>
      <StatsGrid :items="[
        { label: 'AI 可用', value: capabilities.stats.value.published, testId: 'stat-published' },
        { label: '待评审', value: capabilities.stats.value.review, testId: 'stat-review' },
        { label: '校验失败', value: capabilities.stats.value.invalid, testId: 'stat-invalid' },
        { label: '可发布', value: capabilities.stats.value.publishable, testId: 'stat-publishable' },
      ]" />
      <button data-test="view-existing-capabilities" class="secondary-wide" @click="capabilities.managementPhase.value = 'review'">查看已有能力</button>
    </aside>
  </section>
</template>
