<script setup lang="ts">
import { ElInput, ElSelect, ElOption, ElTag } from 'element-plus';
import type { UseCapabilities } from '../../composables/useCapabilities';
import type { InputField } from '../../types';

const props = defineProps<{ capabilities: UseCapabilities }>();
</script>

<template>
  <details class="advanced-editor">
    <summary>编辑 Capability 详情</summary>
    <section class="editor-group">
      <div class="group-title"><h3>识别结果</h3><span>{{ capabilities.sourceLabel(capabilities.selected.value.source) }} / {{ capabilities.operationLabel(capabilities.selected.value.operation) }}</span></div>
      <div class="form-grid">
        <label>名称<el-input data-test="capability-name" v-model="capabilities.selected.value.name" placeholder="minio.bucket.capacity.read" /></label>
        <label>领域<el-input v-model="capabilities.selected.value.domain" placeholder="minio" /></label>
        <label>资源类型<el-input v-model="capabilities.selected.value.resource_type" placeholder="bucket" /></label>
        <label>操作类型<el-select v-model="capabilities.selected.value.operation"><el-option label="读取" value="read" /><el-option label="写入" value="write" /></el-select></label>
        <label>风险等级<el-select v-model="capabilities.selected.value.risk"><el-option label="低" value="low" /><el-option label="中" value="medium" /><el-option label="高" value="high" /></el-select></label>
        <label class="wide">AI 描述<el-input data-test="ai-description" v-model="capabilities.selected.value.ai.description" type="textarea" :rows="2" placeholder="描述该能力的作用，供 AI 判断何时调用" /></label>
      </div>
    </section>
    <section class="editor-group">
      <div class="group-title"><h3>后端接口</h3><span>{{ capabilities.selected.value.backend.method }} {{ capabilities.selected.value.backend.path || '/' }}</span></div>
      <div class="form-grid">
        <label>后端 Base URL<el-input v-model="capabilities.selected.value.backend.base_url" placeholder="https://middleware.example.com" /></label>
        <label>请求方法<el-select v-model="capabilities.selected.value.backend.method"><el-option label="GET" value="GET" /><el-option label="POST" value="POST" /><el-option label="PUT" value="PUT" /><el-option label="PATCH" value="PATCH" /><el-option label="DELETE" value="DELETE" /></el-select></label>
        <label class="wide">接口路径<el-input data-test="backend-path" v-model="capabilities.selected.value.backend.path" placeholder="/api/minio/{cluster}/buckets/{bucket}" /></label>
      </div>
    </section>
    <section class="editor-group two-column">
      <div>
        <div class="group-title compact"><h3>输入参数</h3><span>{{ Object.keys(capabilities.selected.value.input_schema).length }} 个字段</span></div>
        <div class="derived"><strong>路径变量</strong><div data-test="path-variables" class="variable-list"><el-tag v-for="name in capabilities.derivedVariables.value" :key="name" size="small">{{ name }}</el-tag><span v-if="capabilities.derivedVariables.value.length === 0">没有路径变量</span></div></div>
        <div class="field-table">
          <div class="field-row header"><span>字段名</span><span>类型</span><span>必填</span><span></span></div>
          <div v-for="(row, index) in capabilities.inputRows.value" :key="row.name" class="field-row">
            <input :data-test="`input-name-${index}`" class="mini-input" :value="row.name" :disabled="row.name === 'environment'" @change="capabilities.renameInputField(row.name, ($event.target as HTMLInputElement).value)" />
            <select :data-test="`input-type-${index}`" class="mini-select" :value="row.type" @change="capabilities.setInputType(row.name, ($event.target as HTMLSelectElement).value as InputField['type'])"><option value="string">string</option><option value="integer">integer</option><option value="number">number</option><option value="boolean">boolean</option></select>
            <input :data-test="`input-required-${index}`" type="checkbox" :checked="row.required" @change="capabilities.setInputRequired(row.name, ($event.target as HTMLInputElement).checked)" />
            <button class="mini-button" :disabled="row.name === 'environment'" @click="capabilities.removeInputField(row.name)">删除</button>
          </div>
          <button data-test="add-input-field" class="inline-add" @click="capabilities.addInputField">添加参数</button>
        </div>
      </div>
      <div>
        <div class="group-title compact"><h3>AI 摘要字段</h3><span>{{ Object.keys(capabilities.selected.value.output.fields).length }} 个字段</span></div>
        <label class="block-label tight">摘要模板<input data-test="summary-template" v-model="capabilities.selected.value.output.summary_template" class="filter-input" placeholder="Bucket {bucket} usage is {usage_pct}%" /></label>
        <div class="mapping-list">
          <div class="field-row header output-header"><span>字段名</span><span>JSONPath</span><span></span></div>
          <div v-for="(row, index) in capabilities.outputRows.value" :key="row.name" class="field-row output-row">
            <input :data-test="`output-name-${index}`" class="mini-input" :value="row.name" @change="capabilities.renameOutputField(row.name, ($event.target as HTMLInputElement).value)" />
            <input :data-test="`output-path-${index}`" class="mini-input" :value="row.path" @input="capabilities.setOutputPath(row.name, ($event.target as HTMLInputElement).value)" />
            <button class="mini-button" @click="capabilities.removeOutputField(row.name)">删除</button>
          </div>
          <span v-if="Object.keys(capabilities.selected.value.output.fields).length === 0">还没有输出字段</span>
          <button data-test="add-output-field" class="inline-add" @click="capabilities.addOutputField">添加映射</button>
        </div>
      </div>
    </section>
    <section class="editor-group">
      <div class="group-title">
        <h3>权限与治理</h3>
        <span>{{ capabilities.selected.value.auth.roles.join(' / ') || '未配置角色' }}</span>
      </div>
      <div class="policy-line">
        <span>环境隔离：{{ capabilities.selected.value.auth.environment_scoped ? '开启' : '关闭' }}</span>
        <span data-test="governance-summary">{{ capabilities.governanceSummary.value }}</span>
      </div>
    </section>
  </details>
</template>