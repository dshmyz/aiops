<script setup lang="ts">
import { ElButton } from 'element-plus';
import QuickPublishForm from '../QuickPublishForm.vue';
import ManualCreatePanel from '../manual/ManualCreatePanel.vue';
import StatsGrid from './StatsGrid.vue';
import SwaggerSourceStrip from './SwaggerSourceStrip.vue';
import UrlEntry from './UrlEntry.vue';
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
          <p class="eyebrow">第一步 · 连接接口</p>
          <h2>把一个 API 接入 AI</h2>
          <p>输入一个接口地址，或批量导入 Swagger/OpenAPI。系统会帮你生成 AI 工具并验证返回结果。写入接口只保存为草稿，不会在这里直接调用。</p>
        </div>
      </header>

      <!-- 双入口并列：单一 URL ‖ 批量 Swagger -->
      <section data-test="import-wizard" class="import-wizard source-entry-grid" aria-label="后端接口接入">
        <article data-test="url-source-card" class="source-entry-card">
          <div class="import-wizard__label">
            <span class="import-wizard__dot"></span>
            填一个后端接口地址
          </div>
          <UrlEntry class="import-strip" :capabilities="capabilities" />
        </article>
        <article data-test="swagger-source-card" class="source-entry-card">
          <div class="import-wizard__label">
            <span class="import-wizard__dot"></span>
            批量接入 Swagger / OpenAPI 文档
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
            @load-example="capabilities.loadBuiltinExample"
          />
          <strong v-if="capabilities.importMessage.value" data-test="import-result" class="import-message">{{ capabilities.importMessage.value }}</strong>
          <div data-test="import-commit-summary" class="commit-summary commit-summary-empty">
            <span class="commit-hint">先预览 API，再选择要接入的接口</span>
          </div>
        </article>
      </section>

      <details data-test="manual-create-panel" class="quick-publish-panel">
        <summary>
          <span class="quick-publish-panel__plus" aria-hidden="true">+</span>
          高级 JSON：快速发布 / 手写草稿
        </summary>
        <QuickPublishForm
          @published="capabilities.handleQuickPublished"
          @error="capabilities.handleQuickPublishError"
        />
        <ManualCreatePanel
          class="manual-inline"
          @created="capabilities.openManualCapability"
        />
      </details>
    </div>
    <aside class="stage-side">
      <h3>当前能力状态</h3>
      <StatsGrid :items="[
        { label: 'AI 可用', value: capabilities.stats.value.published, testId: 'stat-published' },
        { label: '待验证', value: capabilities.stats.value.review, testId: 'stat-review' },
        { label: '校验失败', value: capabilities.stats.value.invalid, testId: 'stat-invalid' },
        { label: '可发布', value: capabilities.stats.value.publishable, testId: 'stat-publishable' },
      ]" />
      <button data-test="view-existing-capabilities" class="secondary-wide" @click="capabilities.managementPhase.value = 'review'">查看已有能力</button>
    </aside>
  </section>
</template>
