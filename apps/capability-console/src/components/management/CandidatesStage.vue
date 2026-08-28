<script setup lang="ts">
import { ElButton } from 'element-plus';
import SwaggerSourceStrip from './SwaggerSourceStrip.vue';
import type { UseCapabilities } from '../../composables/useCapabilities';
import type { CapabilityOperation, CapabilityRisk } from '../../types';

const props = defineProps<{ capabilities: UseCapabilities }>();

// 试调结果列的提示文案：优先显示阻断问题；干净但走了规则推断的说明来源。
function candidateProbeCellText(candidateId: string): string {
  const probe = props.capabilities.candidateProbeResults.value[candidateId];
  if (!probe) {
    return '';
  }
  const blocking = (probe.warnings ?? []).find((w) => !w.includes('LLM 推断失败'));
  if (blocking) {
    return blocking;
  }
  if (probe.ok) {
    return probe.inferred_by === 'rules' ? '试调成功（规则推断映射）' : '试调成功，AI 已配置映射';
  }
  return (probe.warnings ?? [])[0] ?? '试调失败';
}
</script>

<template>
  <section data-test="workflow-candidates" class="workflow-stage workflow-candidates">
    <header class="stage-hero">
      <div class="stage-hero__icon" aria-hidden="true">
        <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="9 11 12 14 22 4"/><path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/></svg>
      </div>
      <div>
        <p class="eyebrow">第二步</p>
        <h2>选择要变成 AI 工具的 API</h2>
        <p>默认只勾选读取类、低风险、能被自然语言描述清楚的接口。写入能力需补齐 governance 字段后才能发布。</p>
      </div>
      <button class="secondary-inline stage-hero__action" @click="capabilities.managementPhase.value = 'source'">换一个 Swagger</button>
    </header>
    <section data-test="import-wizard" class="import-wizard candidate-stage" aria-label="候选 API">
      <SwaggerSourceStrip
        class="candidate-source-strip"
        :url="capabilities.importOpenAPIURLText.value"
        :base-url="capabilities.importBackendBaseURL.value"
        :loading="capabilities.importPreviewLoading.value"
        button-text="重新预览"
        button-type="default"
        :show-example-link="false"
        @update:url="capabilities.importOpenAPIURLText.value = $event"
        @update:base-url="capabilities.importBackendBaseURL.value = $event"
        @preview="capabilities.previewSwaggerURL"
        @clear-preview="capabilities.clearImportPreview"
      />
      <section v-if="capabilities.importPreview.value" data-test="import-preview" class="import-preview">
        <div class="candidate-stats">
          <div class="candidate-stat-card">
            <span>全部接口</span>
            <strong>{{ capabilities.importPreview.value.stats.total }}</strong>
          </div>
          <div class="candidate-stat-card stat-success">
            <span>推荐接入</span>
            <strong>{{ capabilities.importPreview.value.stats.recommended }}</strong>
          </div>
          <div class="candidate-stat-card stat-warning">
            <span>需要调整</span>
            <strong>{{ capabilities.importPreview.value.stats.needs_adjustment }}</strong>
          </div>
          <div class="candidate-stat-card stat-danger">
            <span>暂不接入</span>
            <strong>{{ capabilities.importPreview.value.stats.not_recommended }}</strong>
          </div>
          <div class="candidate-stat-card">
            <span>读取</span>
            <strong>{{ capabilities.importPreview.value.stats.read }}</strong>
          </div>
          <div class="candidate-stat-card">
            <span>写入</span>
            <strong>{{ capabilities.importPreview.value.stats.write }}</strong>
          </div>
        </div>
        <div class="filters">
          <input data-test="candidate-search" v-model="capabilities.candidateFilters.value.search" class="filter-input" placeholder="搜索候选 API" />
          <select data-test="candidate-recommendation-filter" v-model="capabilities.candidateFilters.value.recommendation" class="filter-select">
            <option value="all">全部建议</option>
            <option value="recommended">推荐接入</option>
            <option value="needs_adjustment">需要调整</option>
            <option value="not_recommended">暂不接入</option>
          </select>
          <select data-test="candidate-domain-filter" v-model="capabilities.candidateFilters.value.domain" class="filter-select">
            <option value="all">全部领域</option>
            <option v-for="domain in capabilities.importCandidateDomains.value" :key="domain" :value="domain">{{ domain }}</option>
          </select>
        </div>
        <div class="candidate-list">
          <article v-for="candidate in capabilities.visibleImportCandidates.value" :key="candidate.id" class="candidate-row candidate-row--compact" :data-test="`candidate-row-${candidate.id}`">
            <label class="candidate-check">
              <input :data-test="`candidate-selected-${candidate.id}`" type="checkbox" v-model="capabilities.candidateSelections.value[candidate.id]" />
              <span class="candidate-check__mark" aria-hidden="true"></span>
            </label>
            <div class="method-pill">{{ candidate.method }}</div>
            <div class="candidate-main candidate-main--compact" :title="candidate.operation_id || candidate.id">
              <strong class="candidate-path">{{ candidate.path }}</strong>
              <small v-if="capabilities.candidateOverrides.value[candidate.id]?.name" class="candidate-override">{{ capabilities.candidateOverrides.value[candidate.id]?.name }}</small>
              <small v-else-if="capabilities.candidateReasonText(candidate)" class="candidate-reason" :title="capabilities.candidateReasonText(candidate)">{{ capabilities.candidateReasonText(candidate) }}</small>
            </div>
            <div class="verdict-cell verdict-cell--compact" :data-verdict-cell="candidate.recommendation" :title="capabilities.candidateVerdictText(candidate)">
              <strong :data-verdict="candidate.recommendation">{{ capabilities.recommendationLabel(candidate.recommendation) }}</strong>
            </div>
            <div v-if="capabilities.candidateProbeResults.value[candidate.id]" class="probe-cell probe-cell--compact" :data-test="`candidate-probe-${candidate.id}`" :data-probe-ok="capabilities.candidateProbeResults.value[candidate.id].ok" :title="candidateProbeCellText(candidate.id)">
              <strong v-if="capabilities.candidateProbeResults.value[candidate.id].ok">✓ 可用</strong>
              <strong v-else>⚠ 有问题</strong>
              <small>{{ candidateProbeCellText(candidate.id) }}</small>
            </div>
            <details class="candidate-adjust candidate-adjust--compact" :data-test="`candidate-adjust-${candidate.id}`">
              <summary title="调整命名、领域、风险等级"><span class="candidate-adjust__chevron" aria-hidden="true"></span>调整</summary>
              <div class="candidate-edit-grid">
                <input :data-test="`candidate-name-${candidate.id}`" class="mini-input" :value="capabilities.candidateOverrides.value[candidate.id]?.name" @input="capabilities.updateCandidateOverride(candidate.id, { name: ($event.target as HTMLInputElement).value })" />
                <input :data-test="`candidate-domain-${candidate.id}`" class="mini-input" :value="capabilities.candidateOverrides.value[candidate.id]?.domain" @input="capabilities.updateCandidateOverride(candidate.id, { domain: ($event.target as HTMLInputElement).value })" />
                <input :data-test="`candidate-resource-${candidate.id}`" class="mini-input" :value="capabilities.candidateOverrides.value[candidate.id]?.resource_type" @input="capabilities.updateCandidateOverride(candidate.id, { resource_type: ($event.target as HTMLInputElement).value })" />
                <select :data-test="`candidate-operation-${candidate.id}`" class="mini-select" :value="capabilities.candidateOverrides.value[candidate.id]?.operation" @change="capabilities.updateCandidateOverride(candidate.id, { operation: ($event.target as HTMLSelectElement).value as CapabilityOperation })">
                  <option value="read">读取</option>
                  <option value="write">写入</option>
                </select>
                <select :data-test="`candidate-risk-${candidate.id}`" class="mini-select" :value="capabilities.candidateOverrides.value[candidate.id]?.risk" @change="capabilities.updateCandidateOverride(candidate.id, { risk: ($event.target as HTMLSelectElement).value as CapabilityRisk })">
                  <option value="low">低</option>
                  <option value="medium">中</option>
                  <option value="high">高</option>
                </select>
              </div>
            </details>
          </article>
        </div>
        <div data-test="import-commit-summary" class="commit-summary sticky-commit">
          <span class="commit-summary__text">已选择 <strong>{{ capabilities.importCommitSummary.value.selected }}</strong> 个候选 API，其中读取 {{ capabilities.importCommitSummary.value.reads }} 个，写入 {{ capabilities.importCommitSummary.value.writes }} 个，高风险 {{ capabilities.importCommitSummary.value.highRisk }} 个。</span>
          <span v-if="capabilities.candidateProbing.value" data-test="probe-batch-progress" class="commit-hint">试调中… {{ capabilities.candidateProbedProgress.value }}</span>
          <template v-else-if="capabilities.candidateProbedCount.value > 0">
            <span data-test="probe-batch-result" class="commit-hint">试调完成：{{ capabilities.cleanProbedCandidates.value.length }} 个接口可用，{{ capabilities.problemProbedCandidates.value.length }} 个需人工处理</span>
          </template>
          <el-button data-test="probe-batch" :disabled="capabilities.importCommitSummary.value.selected === 0" :loading="capabilities.candidateProbing.value" @click="capabilities.probeSelectedCandidates">试调所选接口</el-button>
          <el-button
            v-if="capabilities.cleanProbedCandidates.value.length > 0"
            data-test="commit-and-publish-clean"
            type="success"
            :loading="capabilities.importCommitLoading.value"
            @click="capabilities.commitSwaggerImport({ autoPublishClean: true })"
          >试调通过的 {{ capabilities.cleanProbedCandidates.value.length }} 个直接发布</el-button>
          <el-button data-test="commit-openapi-import" type="primary" :disabled="!capabilities.canCommitImportPreview.value" :loading="capabilities.importCommitLoading.value" @click="capabilities.commitSwaggerImport()">生成 Capability 草稿</el-button>
        </div>
      </section>
    </section>
  </section>
</template>
