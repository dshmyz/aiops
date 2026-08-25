<script setup lang="ts">
import { ElButton, ElMessage, ElMessageBox } from 'element-plus';
import type { UseCapabilities } from '../../composables/useCapabilities';

const props = defineProps<{ capabilities: UseCapabilities }>();

async function handlePublishAll() {
  const result = await props.capabilities.publishAll();
  if (!result) {
    return;
  }
  if (result.failed === 0) {
    ElMessage.success(`已发布 ${result.success} 个能力`);
    return;
  }
  // 有失败项：用明细弹窗替代 alert，逐条列出失败原因，不再吞掉错误。
  const detailHtml = result.failures
    .map((failure) => `<p class="publish-fail"><strong>${escapeHtml(failure.name)}</strong><span>${escapeHtml(failure.reason)}</span></p>`)
    .join('');
  await ElMessageBox.confirm(
    detailHtml,
    `批量发布完成：成功 ${result.success} / ${result.total}`,
    {
      type: result.failed > result.success ? 'warning' : 'info',
      confirmButtonText: '知道了',
      showCancelButton: false,
      dangerouslyUseHTMLString: true,
      customClass: 'publish-result-dialog',
    },
  );
}

function escapeHtml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}
</script>

<template>
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
      <!-- 状态分组标签 -->
      <div class="status-tabs">
        <button :class="{ active: capabilities.statusFilter.value === 'all' }" @click="capabilities.statusFilter.value = 'all'">全部 ({{ capabilities.capabilities.value.length }})</button>
        <button :class="{ active: capabilities.statusFilter.value === 'discovered' }" @click="capabilities.statusFilter.value = 'discovered'">草稿 ({{ capabilities.groupedStats.value.draft }})</button>
        <button :class="{ active: capabilities.statusFilter.value === 'needs_review' }" @click="capabilities.statusFilter.value = 'needs_review'">待评审 ({{ capabilities.groupedStats.value.review }})</button>
        <button :class="{ active: capabilities.statusFilter.value === 'published' }" @click="capabilities.statusFilter.value = 'published'">已发布 ({{ capabilities.groupedStats.value.published }})</button>
      </div>

      <!-- 批量操作栏 -->
      <div class="batch-actions">
        <span>已选 {{ capabilities.filteredCapabilities.value.filter(c => capabilities.isPublishable(c)).length }} 个可发布</span>
        <el-button size="small" type="primary" :disabled="capabilities.filteredCapabilities.value.filter(c => capabilities.isPublishable(c)).length === 0" @click="handlePublishAll">
          一键发布全部可发布
        </el-button>
      </div>

      <article
        v-for="item in capabilities.paginatedCapabilities.value"
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
      <!-- 分页控制 -->
      <div v-if="capabilities.totalPages.value > 1" class="pagination-controls">
        <button :disabled="capabilities.currentPage.value <= 1" @click="capabilities.currentPage.value--">上一页</button>
        <span>{{ capabilities.currentPage.value }} / {{ capabilities.totalPages.value }}</span>
        <button :disabled="capabilities.currentPage.value >= capabilities.totalPages.value" @click="capabilities.currentPage.value++">下一页</button>
      </div>
    </div>
  </aside>
</template>

<style scoped>
/* 状态筛选 tab（全部/草稿/待评审/已发布）：底部指示条 + active 高亮。 */
.status-tabs {
  display: flex;
  gap: 4px;
  flex-wrap: wrap;
  border-bottom: 1px solid var(--color-border, #d0d7de);
  margin-bottom: var(--space-3, 12px);
}

.status-tabs button {
  appearance: none;
  background: none;
  border: none;
  border-bottom: 2px solid transparent;
  padding: 6px 14px;
  margin-bottom: -1px;
  cursor: pointer;
  font-size: 0.85rem;
  color: var(--color-text-secondary, #57606a);
  border-radius: 0;
  transition: color 0.15s ease, border-color 0.15s ease;
}

.status-tabs button:hover {
  color: var(--color-text, #1f2328);
}

.status-tabs button.active {
  color: var(--color-primary, #0969da);
  border-bottom-color: var(--color-primary, #0969da);
  font-weight: 600;
}

.batch-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3, 12px);
  margin-bottom: var(--space-3, 12px);
  font-size: 0.85rem;
  color: var(--color-text-secondary, #57606a);
}
</style>

<style>
/* 批量发布失败明细弹窗：逐条列出失败能力名 + 原因。 */
.publish-result-dialog .publish-fail {
  margin: 6px 0;
  padding: 6px 10px;
  border-left: 3px solid var(--el-color-danger, #f56c6c);
  background: var(--el-fill-color-light, #f5f7fa);
  border-radius: 4px;
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 0.85rem;
}

.publish-result-dialog .publish-fail strong {
  color: var(--el-text-color-primary, #303133);
}

.publish-result-dialog .publish-fail span {
  color: var(--el-text-color-secondary, #909399);
  word-break: break-all;
}
</style>