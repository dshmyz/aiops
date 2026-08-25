<script setup lang="ts">
import { ElButton, ElInput } from 'element-plus';
import type { UseCapabilities } from '../../composables/useCapabilities';

const props = defineProps<{ capabilities: UseCapabilities }>();
</script>

<template>
  <section class="review-block">
    <div class="review-block-title">
      <h3>测试与预览</h3>
      <span>校验 · 测试 · AI 试问 · 归一化预览</span>
    </div>
    <section data-test="test-input-form" class="editor-group test-input-panel">
      <div class="group-title"><h3>测试参数</h3><span>根据输入参数和路径变量生成</span></div>
      <div class="test-param-grid">
        <label v-for="row in capabilities.testInputRows.value" :key="row.name" class="test-param">
          <span>{{ row.name }}<small>{{ row.type }}{{ row.required ? ' / 必填' : '' }}{{ row.source === 'path' ? ' / 路径变量' : '' }}</small></span>
          <select v-if="row.enum && row.enum.length > 0" :data-test="`test-param-${row.name}`" class="filter-select" :value="capabilities.testInputFieldValue(row.name) ? String(capabilities.testInputFieldValue(row.name)) : ''" @change="capabilities.setTestInputField(row.name, ($event.target as HTMLSelectElement).value, row.type)">
            <option value="">未设置</option>
            <option v-for="opt in row.enum" :key="opt" :value="opt">{{ opt }}</option>
          </select>
          <select v-else-if="row.type === 'boolean'" :data-test="`test-param-${row.name}`" class="filter-select" :value="String(capabilities.testInputFieldValue(row.name))" @change="capabilities.setTestInputField(row.name, ($event.target as HTMLSelectElement).value === 'true', row.type)"><option value="">未设置</option><option value="true">true</option><option value="false">false</option></select>
          <input v-else :data-test="`test-param-${row.name}`" class="filter-input" :type="row.type === 'integer' || row.type === 'number' ? 'number' : 'text'" :value="capabilities.testInputFieldValue(row.name)" :placeholder="row.name === 'environment' ? 'prod' : (row.examples && row.examples[0]) ? `例如 ${row.examples[0]}` : `填写 ${row.name}`" @input="capabilities.setTestInputField(row.name, ($event.target as HTMLInputElement).value, row.type)" />
          <em v-if="row.description" class="test-param-hint">{{ row.description }}</em>
        </label>
      </div>
    </section>
    <label class="block-label">测试输入 JSON<el-input data-test="test-input" v-model="capabilities.testInputText.value" type="textarea" :rows="4" /></label>
    <div class="actions">
      <el-button data-test="save-draft" @click="capabilities.saveSelectedDraft">保存草稿</el-button>
      <el-button data-test="validate-capability" type="primary" @click="capabilities.validateSelected">校验</el-button>
      <el-button data-test="test-capability" type="success" @click="capabilities.testSelected">测试</el-button>
    </div>
    <section data-test="ai-preflight" class="editor-group ai-preflight-panel">
      <div class="group-title">
        <h3>用 AI 试问一次</h3>
        <span data-test="ai-preflight-state" :class="capabilities.aiPreflightReady.value ? 'ready-text' : 'blocked-text'">{{ capabilities.aiPreflightState.value }}</span>
      </div>
      <label class="block-label tight">
        自然语言请求
        <el-input data-test="ai-prompt" v-model="capabilities.aiPromptText.value" type="textarea" :rows="3" />
      </label>
      <div class="ai-preflight-actions">
        <button data-test="run-ai-preflight" class="primary-inline" :disabled="!capabilities.aiPreflightReady.value || capabilities.aiLoading.value" @click="capabilities.runAIPreflight">
          发送到 AI 助手
        </button>
        <span>
          {{
            capabilities.aiPreflightReady.value
              ? capabilities.hasPublishedTwin(capabilities.selected.value)
                ? '使用同名已发布版本验证 assistant 链路'
                : '通过已发布能力验证 assistant 链路'
              : '发布后再运行预检'
          }}
        </span>
      </div>
      <pre data-test="ai-preflight-result" class="ai-result">{{ capabilities.aiPreflightResultText.value }}</pre>
    </section>
    <section class="preview-panel">
      <h3>归一化预览</h3>
      <div class="preview-grid">
        <div><strong>请求</strong><pre data-test="request-preview">{{ capabilities.requestPreviewText.value }}</pre></div>
        <div><strong>响应</strong><pre data-test="response-preview">{{ capabilities.responsePreviewText.value }}</pre></div>
        <div><strong>归一化结果</strong><pre data-test="preview">{{ capabilities.previewText.value }}</pre></div>
      </div>
    </section>
  </section>
</template>