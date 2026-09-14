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
    <header class="candidate-workbench-head">
      <div>
        <p class="eyebrow">接口选择</p>
        <h2>选择要接入的接口</h2>
        <p>读取接口会先验证；写入接口只保存草稿，不会自动执行。</p>
      </div>
      <button class="secondary-inline" @click="capabilities.managementPhase.value = 'source'">更换来源</button>
    </header>
    <section data-test="import-wizard" class="import-wizard candidate-stage" aria-label="候选 API">
      <details class="candidate-source-details">
        <summary>
          <span>来源</span>
          <strong>{{ capabilities.importPreview.value?.source.openapi_url || capabilities.importOpenAPIURLText.value }}</strong>
          <small>修改</small>
        </summary>
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
      </details>
      <section v-if="capabilities.importPreview.value" data-test="import-preview" class="import-preview">
        <div class="candidate-summary-line">
          <strong>{{ capabilities.importPreview.value.stats.total }} 个接口</strong>
          <span class="is-success">推荐 {{ capabilities.importPreview.value.stats.recommended }}</span>
          <span class="is-warning">需调整 {{ capabilities.importPreview.value.stats.needs_adjustment }}</span>
          <span>读取 {{ capabilities.importPreview.value.stats.read }}</span>
          <span>写入 {{ capabilities.importPreview.value.stats.write }}</span>
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
                  <summary title="调整命名、领域、风险等级、描述"><span class="candidate-adjust__chevron" aria-hidden="true"></span>调整</summary>
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
                    <textarea :data-test="`candidate-description-${candidate.id}`" class="mini-input candidate-description" rows="3" :value="capabilities.candidateOverrides.value[candidate.id]?.description" @input="capabilities.updateCandidateOverride(candidate.id, { description: ($event.target as HTMLInputElement).value })" placeholder="AI 描述：告诉大模型这个工具是干嘛的、何时调用"></textarea>
                  </div>
                </details>
              </article>
        </div>        <div data-test="import-commit-summary" class="commit-summary sticky-commit">
          <span class="commit-summary__text">已选择 <strong>{{ capabilities.importCommitSummary.value.selected }}</strong> 个接口，其中读取 {{ capabilities.importCommitSummary.value.reads }} 个，写入 {{ capabilities.importCommitSummary.value.writes }} 个，高风险 {{ capabilities.importCommitSummary.value.highRisk }} 个。</span>
          <span v-if="capabilities.candidateProbing.value" data-test="probe-batch-progress" class="commit-hint">试调中… {{ capabilities.candidateProbedProgress.value }}</span>
          <template v-else-if="capabilities.candidateProbedCount.value > 0">
            <span data-test="probe-batch-result" class="commit-hint">已验证 {{ capabilities.cleanProbedCandidates.value.length }} 个，需人工处理 {{ capabilities.problemProbedCandidates.value.length }} 个；写入接口不会自动调用</span>
          </template>
          <el-button data-test="probe-batch" :disabled="capabilities.importCommitSummary.value.selected === 0" :loading="capabilities.candidateProbing.value" @click="capabilities.probeSelectedCandidates">先验证读取接口</el-button>
          <el-button
            v-if="capabilities.cleanProbedCandidates.value.length > 0"
            data-test="publish-verified"
            type="success"
            :loading="capabilities.importCommitLoading.value"
            @click="capabilities.publishVerifiedDrafts()"
          >发布已验证的 {{ capabilities.cleanProbedCandidates.value.length }} 个</el-button>
          <el-button data-test="commit-openapi-import" type="primary" :disabled="!capabilities.canCommitImportPreview.value" :loading="capabilities.importCommitLoading.value" @click="capabilities.commitSwaggerImport()">
            导入 {{ capabilities.importCommitSummary.value.selected }} 个接口，保存为草稿
          </el-button>
        </div>
      </section>
    </section>
  </section>
</template>
