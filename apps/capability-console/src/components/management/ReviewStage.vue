<script setup lang="ts">
import { ElButton, ElInput, ElOption, ElSelect, ElTag } from 'element-plus';
import StatsGrid from './StatsGrid.vue';
import type { UseCapabilities } from '../../composables/useCapabilities';
import type { InputField } from '../../types';

defineProps<{ capabilities: UseCapabilities }>();
</script>

<template>
  <section data-test="workflow-review" class="workflow-stage workflow-review">
    <aside data-test="studio-ledger" class="review-list inventory" aria-label="能力清单">
      <div class="section-heading">
        <h2>待处理能力</h2>
        <span>{{ capabilities.filteredCapabilities.value.length }} / {{ capabilities.capabilities.value.length }} 项</span>
      </div>
      <details v-if="capabilities.importBatch.value" data-test="import-batch" class="import-batch-panel compact-batch" aria-label="本次 Swagger 导入">
        <summary class="import-batch-summary">
          <span>本次导入</span>
          <small>{{ capabilities.importBatch.value.stats.total }} 项 · 保留 {{ capabilities.importBatch.value.stats.selected }} · 忽略 {{ capabilities.importBatch.value.stats.ignored }}</small>
        </summary>
        <div class="import-batch-body">
          <div class="section-heading compact">
            <div>
              <h2>本次导入</h2>
              <span>先保留要评审的能力</span>
            </div>
            <select data-test="import-domain-filter" v-model="capabilities.importDomainFilter.value" class="filter-select">
              <option value="all">全部领域</option>
              <option v-for="domain in capabilities.importBatch.value.domains" :key="domain" :value="domain">{{ domain }}</option>
            </select>
          </div>
          <strong v-if="capabilities.importMessage.value" data-test="import-result" class="import-message">{{ capabilities.importMessage.value }}</strong>
          <div class="import-batch-stats">
            <div data-test="import-batch-stat-total"><span>导入</span><strong>{{ capabilities.importBatch.value.stats.total }}</strong></div>
            <div data-test="import-batch-stat-selected"><span>保留</span><strong>{{ capabilities.importBatch.value.stats.selected }}</strong></div>
            <div data-test="import-batch-stat-ignored"><span>忽略</span><strong>{{ capabilities.importBatch.value.stats.ignored }}</strong></div>
          </div>
          <div class="import-batch-list">
            <article v-for="item in capabilities.visibleImportBatchItems.value" :key="item.name" class="import-batch-row" :class="{ ignored: item.ignored }">
              <div class="import-batch-main">
                <button class="link-button" :data-test="`open-import-${item.name}`" @click="capabilities.openImportedCapability(item)">
                  {{ item.name }}
                </button>
                <small>{{ item.domain }} / {{ item.operation }} / {{ item.path }}</small>
              </div>
              <label class="keep-toggle">
                <input
                  :data-test="`ignore-import-${item.name}`"
                  type="checkbox"
                  :checked="item.ignored"
                  @change="capabilities.toggleImportIgnored(item.name, ($event.target as HTMLInputElement).checked)"
                />
                忽略
              </label>
            </article>
          </div>
        </div>
      </details>
      <div class="filters">
        <input data-test="capability-search" v-model="capabilities.searchText.value" class="filter-input" placeholder="搜索名称、领域、接口路径" />
        <select data-test="status-filter" v-model="capabilities.statusFilter.value" class="filter-select">
          <option value="all">全部状态</option>
          <option value="discovered">草稿</option>
          <option value="published">已发布</option>
          <option value="needs_review">待评审</option>
          <option value="deprecated">已废弃</option>
        </select>
        <select v-model="capabilities.domainFilter.value" class="filter-select">
          <option value="all">全部领域</option>
          <option v-for="domain in capabilities.availableDomains.value" :key="domain" :value="domain">{{ domain }}</option>
        </select>
      </div>
      <div v-if="capabilities.loading.value" class="empty">正在加载 AI 运维能力...</div>
      <div v-else class="capability-card-list" data-test="capability-table-body">
        <article
          v-for="item in capabilities.filteredCapabilities.value"
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
            <div class="capability-card__chips">
              <span class="status-chip" :class="`chip-source-${item.source}`">{{ capabilities.sourceLabel(item.source) }}</span>
              <span class="status-chip" :class="`chip-op-risk`">{{ capabilities.operationLabel(item.operation) }} · 风险{{ capabilities.riskLabel(item.risk) }}</span>
            </div>
          </div>
          <small class="capability-card__meta">{{ item.domain }} / {{ item.resource_type }} / {{ item.backend.method }} {{ item.backend.path }}</small>
          <div class="capability-card__foot">
            <span class="next-action-chip" :data-test="`next-${item.name}`">{{ capabilities.nextActionLabel(item) }}</span>
            <div class="capability-card__actions">
              <el-button size="small" :data-test="`publish-${item.name}`" :disabled="!capabilities.isPublishable(item)" @click.stop="capabilities.publishSelected(item)">
                {{ capabilities.publishActionLabel(item) }}
              </el-button>
              <el-button size="small" :disabled="item.source !== 'published'" @click.stop="capabilities.unpublishSelected(item)">下线</el-button>
            </div>
          </div>
        </article>
      </div>
    </aside>

    <section data-test="studio-translator" class="review-detail editor" aria-label="能力评审">
      <StatsGrid class="review-kpis" :items="[
        { label: 'AI 可用', value: capabilities.stats.value.published, testId: 'stat-published' },
        { label: '待评审', value: capabilities.stats.value.review, testId: 'stat-review' },
        { label: '校验失败', value: capabilities.stats.value.invalid, testId: 'stat-invalid' },
        { label: '可发布', value: capabilities.stats.value.publishable, testId: 'stat-publishable' },
      ]" />
      <div class="section-heading">
        <h2>评审发布</h2>
        <div class="heading-status">
          <span data-test="selected-next-action">下一步：{{ capabilities.nextActionLabel(capabilities.selected.value) }}</span>
          <el-tag data-test="validation-state" :type="capabilities.validation.value.valid ? 'success' : 'danger'">{{ capabilities.validationLabel.value }}</el-tag>
        </div>
      </div>
      <section class="editor-group">
        <div class="group-title">
          <h3>先看是否能发布</h3>
          <span :class="capabilities.publishReady.value ? 'ready-text' : 'blocked-text'">{{ capabilities.publishReady.value ? '可以发布' : '需要处理' }}</span>
        </div>
        <div data-test="publish-checklist" class="publish-panel slim">
          <div class="target-path"><span>目标文件</span><code>{{ capabilities.publishTargetPath.value }}</code></div>
          <div class="check-list">
            <div v-for="check in capabilities.publishChecks.value" :key="check.label" class="check-row" :class="{ failed: !check.ok }">
              <strong>{{ check.ok ? '通过' : '阻塞' }}</strong>
              <span>{{ check.label }}</span>
              <small>{{ check.detail }}</small>
            </div>
          </div>
          <button data-test="publish-current" class="primary-inline" :disabled="!capabilities.publishReady.value" @click="capabilities.publishCurrent">
            {{ capabilities.currentPublishLabel() }}
          </button>
        </div>
      </section>
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
              <select v-if="row.type === 'boolean'" :data-test="`test-param-${row.name}`" class="filter-select" :value="String(capabilities.testInputFieldValue(row.name))" @change="capabilities.setTestInputField(row.name, ($event.target as HTMLSelectElement).value === 'true', row.type)"><option value="">未设置</option><option value="true">true</option><option value="false">false</option></select>
              <input v-else :data-test="`test-param-${row.name}`" class="filter-input" :type="row.type === 'integer' || row.type === 'number' ? 'number' : 'text'" :value="capabilities.testInputFieldValue(row.name)" :placeholder="row.name === 'environment' ? 'prod' : `填写 ${row.name}`" @input="capabilities.setTestInputField(row.name, ($event.target as HTMLInputElement).value, row.type)" />
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
    </section>
  </section>
</template>
