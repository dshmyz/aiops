<script setup lang="ts">
import type { UseCapabilities } from '../../composables/useCapabilities';
import UrlEntry from './UrlEntry.vue';
import SwaggerSourceStrip from './SwaggerSourceStrip.vue';
import QuickPublishForm from '../QuickPublishForm.vue';
import ManualCreatePanel from '../manual/ManualCreatePanel.vue';
import {
  creationSource as source,
  resetCreationCanvasState,
} from './creationCanvasState';

const props = defineProps<{ capabilities: UseCapabilities }>();

function reset() {
  resetCreationCanvasState();
  props.capabilities.clearUrlProbeGenerate();
}
</script>

<template>
  <section data-test="creation-canvas" class="creation-canvas" aria-label="创建 AI 工具">
    <div class="creation-canvas__body">
      <div v-if="!source" class="creation-canvas__sources">
        <p class="creation-canvas__question">你想接入哪种来源？</p>
        <button data-test="source-single-api" type="button" class="source-choice" @click="source = 'single'">
          <strong>从一个接口开始</strong>
          <small>输入接口地址，系统自动连接并验证</small>
        </button>
        <button data-test="source-openapi" type="button" class="source-choice" @click="source = 'openapi'">
          <strong>导入 API 文档</strong>
          <small>已有 Swagger / OpenAPI 时批量接入</small>
        </button>
      </div>

      <div v-else-if="source === 'single'" class="creation-canvas__source-panel">
        <div class="creation-canvas__section-heading">
          <div>
            <span>数据来源</span>
            <h3>连接这个工具的数据来源</h3>
          </div>
          <button type="button" @click="source = null">返回</button>
        </div>
        <UrlEntry :capabilities="capabilities" />
        <button
          v-if="capabilities.generatedUrlResult.value"
          data-test="continue-single-review"
          class="creation-canvas__primary"
          type="button"
          @click="capabilities.commitUrlProbeGenerate"
        >
          继续确认工具
        </button>
      </div>

      <div v-else class="creation-canvas__source-panel">
        <div class="creation-canvas__section-heading">
          <div>
            <span>数据来源</span>
            <h3>导入 API 文档</h3>
          </div>
          <button type="button" @click="source = null">返回</button>
        </div>
        <SwaggerSourceStrip
          :url="capabilities.importOpenAPIURLText.value"
          :base-url="capabilities.importBackendBaseURL.value"
          :loading="capabilities.importPreviewLoading.value"
          @update:url="capabilities.importOpenAPIURLText.value = $event"
          @update:base-url="capabilities.importBackendBaseURL.value = $event"
          @preview="capabilities.previewSwaggerURL"
          @clear-preview="capabilities.clearImportPreview"
        />
        <div v-if="capabilities.importPreview.value" class="creation-canvas__preview-ready">
          <span>已识别 {{ capabilities.importPreview.value.candidates.length }} 个接口</span>
          <button data-test="continue-openapi-candidates" type="button" class="creation-canvas__primary" @click="capabilities.managementPhase.value = 'candidates'">
            查看并选择接口
          </button>
        </div>
      </div>

      <details data-test="creation-advanced" class="creation-canvas__advanced">
        <summary>高级方式：快速发布或手写工具</summary>
        <div class="creation-canvas__advanced-body">
          <section class="creation-canvas__advanced-section">
            <h4>快速发布</h4>
            <QuickPublishForm @published="capabilities.handleQuickPublished" @error="capabilities.handleQuickPublishError" />
          </section>
          <section class="creation-canvas__advanced-section">
            <h4>手写工具</h4>
            <ManualCreatePanel @created="capabilities.openManualCapability" />
          </section>
        </div>
      </details>
    </div>
  </section>
</template>

<style scoped>
.creation-canvas { display: flex; flex-direction: column; gap: 20px; padding: 4px 0 48px; }
.creation-canvas__sources, .creation-canvas__source-panel { display: flex; flex-direction: column; gap: 16px; padding: 0; border: none; background: none; box-shadow: none; }
.creation-canvas__question { margin: 0; font-size: 17px; font-weight: 650; color: var(--color-text-primary); }
.source-choice { display: flex; flex-direction: column; gap: 5px; padding: 16px 18px; text-align: left; border: 1px solid var(--color-border); border-radius: 12px; background: var(--color-bg-elevated); color: var(--color-text-primary); cursor: pointer; transition: border-color .15s, box-shadow .15s; font: inherit; margin-bottom: 12px; }
.source-choice:hover { border-color: var(--view-accent, var(--color-accent)); box-shadow: 0 2px 8px rgba(0, 0, 0, 0.06); }
.source-choice strong { font-size: 15px; }
.source-choice small { color: var(--color-text-secondary); }
.creation-canvas__section-heading { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; }
.creation-canvas__section-heading button, .creation-canvas__section-heading span { border: 0; background: none; color: var(--view-accent, var(--color-accent)); cursor: pointer; font: inherit; }
.creation-canvas__section-heading span { cursor: default; font-size: 12px; }
.creation-canvas__section-heading h3 { margin: 4px 0 0; color: var(--color-text-primary); }
/* 高级方式折叠区：浅底容器把子表单和页面正文隔开；两个子表单各带小节标题。 */
.creation-canvas__advanced { border: 1px solid var(--color-border); border-radius: 12px; background: var(--color-bg); }
.creation-canvas__advanced > summary { padding: 12px 16px; cursor: pointer; font-size: 14px; color: var(--color-text-secondary); }
.creation-canvas__advanced[open] > summary { border-bottom: 1px solid var(--color-border); }
.creation-canvas__advanced-body { display: flex; flex-direction: column; gap: 20px; padding: 16px; }
.creation-canvas__advanced-section > h4 { margin: 0 0 10px; font-size: 13px; font-weight: 700; letter-spacing: 0.04em; color: var(--color-text-tertiary); }
.creation-canvas__preview-ready { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 12px 14px; border-radius: var(--radius-lg); background: var(--color-success-soft); color: var(--color-success); }
.creation-canvas__preview-ready .creation-canvas__primary { margin-left: auto; }
</style>
