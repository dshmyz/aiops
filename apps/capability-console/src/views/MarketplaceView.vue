<script setup lang="ts">
import { computed, reactive, ref } from 'vue';
import { ElMessage } from 'element-plus';
import {
  APIError,
  marketplaceDownload,
  marketplaceListRatings,
  marketplaceListVersions,
  marketplacePublish,
  marketplaceRate,
  marketplaceSearch,
  marketplaceSemanticSearch,
  marketplaceStats,
} from '../api';
import type {
  MarketplacePublishPayload,
  MarketplaceRating,
  MarketplaceRegistry,
  MarketplaceStats,
  MarketplaceVersion,
} from '../types';
import ViewShell from '../components/ViewShell.vue';

type Mode = 'browse' | 'publish';
const mode = ref<Mode>('browse');

// ===== Browse / search state =====
const capabilities = ref<MarketplaceRegistry[]>([]);
const total = ref(0);
const loading = ref(false);
const error = ref('');
const semantic = ref(false);
// 语义搜索未配置（后端 503）时的友好提示；区别于普通搜索失败。
const semanticUnavailable = ref(false);

const searchForm = reactive({
  naturalLanguage: '',
  query: '',
  domain: '',
  riskLevel: '',
  visibility: '',
});

const riskOptions = ['low', 'medium', 'high'];
const visibilityOptions = ['private', 'team', 'public'];

const limit = 20;
const offset = ref(0);

const nextOffsetShown = computed(() => capabilities.value.length < total.value);

async function runSearch(semanticSearch: boolean, reset: boolean) {
  loading.value = true;
  error.value = '';
  semanticUnavailable.value = false;
  try {
    const page = semanticSearch
      ? await marketplaceSemanticSearch(searchForm.naturalLanguage)
      : await marketplaceSearch({
          query: searchForm.query || undefined,
          domain: searchForm.domain || undefined,
          risk_level: searchForm.riskLevel || undefined,
          visibility: searchForm.visibility || undefined,
          limit,
          offset: offset.value,
        });
    // 重置时直接替换列表；加载更多时追加，避免丢掉已加载的条目。
    capabilities.value = reset
      ? (page.capabilities ?? [])
      : [...capabilities.value, ...(page.capabilities ?? [])];
    total.value = page.total ?? capabilities.value.length;
    semantic.value = page.semantic ?? false;
  } catch (err) {
    // 语义搜索未配置（后端 503）时给可操作的引导，而不是笼统的"搜索失败"。
    if (semanticSearch && err instanceof APIError && err.status === 503) {
      semanticUnavailable.value = true;
      error.value = '';
    } else {
      error.value = err instanceof Error ? err.message : '搜索失败';
    }
  } finally {
    loading.value = false;
  }
}

function keywordSearch() {
  if (loading.value) return;
  offset.value = 0;
  void runSearch(false, true);
}

function doSemanticSearch() {
  if (!searchForm.naturalLanguage.trim() || loading.value) return;
  capabilities.value = [];
  void runSearch(true, true);
}

// 加载更多：仅在非语义关键字模式下推进 offset 并追加结果。
async function loadMoreResults() {
  if (loading.value || !nextOffsetShown.value) return;
  offset.value += limit;
  await runSearch(false, false);
}

// ===== Detail state =====
const selected = ref<MarketplaceRegistry | null>(null);
const versions = ref<MarketplaceVersion[]>([]);
const ratings = ref<MarketplaceRating[]>([]);
const stats = ref<MarketplaceStats | null>(null);
const detailLoading = ref(false);
const detailError = ref('');

const downloadForm = reactive({ versionID: '', environment: '' });
const downloaded = ref<{ version: string; yaml: string } | null>(null);

async function openDetail(registry: MarketplaceRegistry) {
  selected.value = registry;
  versions.value = [];
  ratings.value = [];
  stats.value = null;
  downloaded.value = null;
  downloadForm.versionID = '';
  detailLoading.value = true;
  detailError.value = '';
  try {
    const [vers, ra, st] = await Promise.all([
      marketplaceListVersions(registry.id),
      marketplaceListRatings(registry.id, 20, 0),
      marketplaceStats(registry.id),
    ]);
    stats.value = st;
    versions.value = vers;
    ratings.value = ra.ratings;
    if (vers.length > 0) downloadForm.versionID = vers[0].id;
  } catch (err) {
    detailError.value = err instanceof Error ? err.message : '加载详情失败';
  } finally {
    detailLoading.value = false;
  }
}

function closeDetail() {
  selected.value = null;
}

async function doDownload() {
  if (!selected.value || !downloadForm.versionID) return;
  try {
    const result = await marketplaceDownload(selected.value.id, downloadForm.versionID);
    downloaded.value = { version: result.version, yaml: result.yaml_content };
  } catch (err) {
    detailError.value = err instanceof Error ? err.message : '下载失败';
  }
}

// ===== Rating =====
const rateForm = reactive({ rating: 5, review: '' });
const rateError = ref('');
const rateSuccess = ref('');

async function submitRate() {
  if (!selected.value) return;
  rateError.value = '';
  rateSuccess.value = '';
  try {
    await marketplaceRate(selected.value.id, {
      rating: rateForm.rating,
      review: rateForm.review.trim() || undefined,
    });
    rateSuccess.value = '已记录评分';
    await openDetail(selected.value);
  } catch (err) {
    rateError.value = err instanceof Error ? err.message : '评分失败';
  }
}

// ===== Publish =====
const publishForm = reactive<MarketplacePublishPayload>({
  yaml_content: '',
  version: '',
  visibility: 'private',
  tags: [],
});
const publishTagsInput = ref('');
const publishError = ref('');
const publishing = ref(false);

async function submitPublish() {
  publishError.value = '';
  if (!publishForm.yaml_content.trim() || !publishForm.version.trim()) {
    publishError.value = '请填写 YAML 内容与版本号';
    return;
  }
  publishing.value = true;
  try {
    const tags = publishTagsInput.value
      .split(',')
      .map((t) => t.trim())
      .filter(Boolean);
    const result = await marketplacePublish({
      ...publishForm,
      tags,
    });
    ElMessage.success(`已发布 ${result.capability.name}@${result.version.version}`);
    publishForm.yaml_content = '';
    publishForm.version = '';
    publishTagsInput.value = '';
    void runSearch(false, true);
  } catch (err) {
    publishError.value = err instanceof Error ? err.message : '发布失败';
  } finally {
    publishing.value = false;
  }
}
</script>

<template>
  <ViewShell
    class="marketplace-entry"
    data-test="marketplace-entry"
    data-view="marketplace"
    eyebrow="Capability Marketplace"
    title="能力市场"
    copy="浏览、搜索与发布可复用能力。自然语言搜索会触发语义召回，按相似度返回最相关能力。"
  >
    <template #actions>
      <nav class="mode-tabs" aria-label="市场模式">
        <button
          class="mode-tab"
          :class="{ active: mode === 'browse' }"
          data-test="marketplace-tab-browse"
          @click="mode = 'browse'"
        >
          浏览
        </button>
        <button
          class="mode-tab"
          :class="{ active: mode === 'publish' }"
          data-test="marketplace-tab-publish"
          @click="mode = 'publish'"
        >
          发布
        </button>
      </nav>
    </template>

    <!-- ================= Browse ================= -->
    <div v-if="mode === 'browse'">
      <div class="semantic-search">
        <input
          v-model="searchForm.naturalLanguage"
          class="semantic-input"
          placeholder="自然语言搜索，如：能查看 kafka 消费组堆积的能力"
          data-test="marketplace-semantic-input"
          @keyup.enter="doSemanticSearch"
        />
        <button class="mini-button" :disabled="loading" data-test="marketplace-semantic-submit" @click="doSemanticSearch">
          {{ loading ? '搜索中' : '语义搜索' }}
        </button>
      </div>

      <div class="market-filters">
        <input
          v-model="searchForm.query"
          placeholder="关键词（按名称/描述）"
          data-test="marketplace-filter-query"
          @keyup.enter="keywordSearch"
        />
        <input v-model="searchForm.domain" placeholder="域（如 kafka）" data-test="marketplace-filter-domain" @keyup.enter="keywordSearch" />
        <select v-model="searchForm.riskLevel" data-test="marketplace-filter-risk">
          <option value="">全部风险</option>
          <option v-for="risk in riskOptions" :key="risk" :value="risk">{{ risk }}</option>
        </select>
        <select v-model="searchForm.visibility" data-test="marketplace-filter-visibility">
          <option value="">全部可见性</option>
          <option v-for="vis in visibilityOptions" :key="vis" :value="vis">{{ vis }}</option>
        </select>
        <button class="mini-button" :disabled="loading" data-test="marketplace-filter-apply" @click="keywordSearch">筛选</button>
      </div>

      <p v-if="error" class="error-text">{{ error }}</p>
      <div
        v-else-if="semanticUnavailable"
        class="config-banner"
        data-test="semantic-unavailable"
      >
        语义搜索未配置：未启用向量库（embedder）。当前可用关键字搜索；要启用语义召回，
        请配置向量库后端后重试。
      </div>
      <p v-if="semantic" class="hint-text">语义搜索模式：已按查询与能力的相关度召回 {{ total }} 个能力。</p>

      <div v-if="!loading && capabilities.length === 0" class="empty">没有匹配的能力。试试语义搜索或调整筛选条件。</div>

      <div class="market-grid">
        <article
          v-for="item in capabilities"
          :key="item.id"
          class="market-card"
          :class="{ active: selected?.id === item.id }"
          :data-test="`marketplace-card-${item.id}`"
          @click="openDetail(item)"
        >
          <header class="market-card-header">
            <span class="badge">{{ item.domain }}</span>
            <span v-if="item.avg_rating !== undefined && item.avg_rating !== null" class="rating">
              ★ {{ item.avg_rating.toFixed(1) }} ({{ item.rating_count }})
            </span>
          </header>
          <h3 class="market-card-name mono">{{ item.name }}</h3>
          <p class="market-card-desc">{{ item.description || '-' }}</p>
          <footer class="market-card-footer">
            <span class="risk" :class="`risk-${item.risk_level}`">{{ item.risk_level }}</span>
            <span class="stat">{{ item.download_count }} 下载 · {{ item.usage_count }} 使用</span>
          </footer>
        </article>
      </div>

      <div v-if="nextOffsetShown" class="market-pagination">
        <button
          class="mini-button"
          :disabled="loading"
          data-test="marketplace-load-more"
          @click="loadMoreResults"
        >
          {{ loading ? '加载中…' : `加载更多（${capabilities.length}/${total}）` }}
        </button>
      </div>
    </div>

    <!-- ================= Publish ================= -->
    <div v-else class="publish-panel">
      <section class="publish-form">
        <label>
          版本号
          <input v-model="publishForm.version" placeholder="如 1.0.0" data-test="marketplace-publish-version" />
        </label>
        <label>
          可见性
          <select v-model="publishForm.visibility" data-test="marketplace-publish-visibility">
            <option v-for="vis in visibilityOptions" :key="vis" :value="vis">{{ vis }}</option>
          </select>
        </label>
        <label>
          Tags（逗号分隔）
          <input v-model="publishTagsInput" placeholder="如 capacity,read-only" data-test="marketplace-publish-tags" />
        </label>
        <label>
          Capability YAML
          <textarea
            v-model="publishForm.yaml_content"
            rows="12"
            placeholder="粘贴能力 YAML 内容"
            data-test="marketplace-publish-yaml"
          ></textarea>
        </label>
        <p v-if="publishError" class="error-text">{{ publishError }}</p>
        <button
          class="mini-button"
          :disabled="publishing"
          data-test="marketplace-publish-submit"
          @click="submitPublish"
        >
          {{ publishing ? '发布中…' : '发布' }}
        </button>
      </section>
    </div>

    <!-- ================= Detail drawer ================= -->
    <div v-if="selected && mode === 'browse'" class="market-detail" data-test="marketplace-detail">
      <header class="detail-title">
        <div>
          <h3 class="mono">{{ selected.name }}</h3>
          <span class="detail-sub">{{ selected.domain }} / {{ selected.resource_type }} · {{ selected.operation }}</span>
        </div>
        <button class="mini-button" data-test="marketplace-close-detail" @click="closeDetail">关闭</button>
      </header>

      <p v-if="detailError" class="error-text">{{ detailError }}</p>
      <div v-if="detailLoading" class="empty">加载详情…</div>

      <template v-else>
        <section class="detail-block">
          <h4>概览</h4>
          <dl class="detail-dl">
            <div><dt>描述</dt><dd>{{ selected.description || '-' }}</dd></div>
            <div><dt>所有者</dt><dd class="mono">{{ selected.owner_id }}</dd></div>
            <div><dt>可见性</dt><dd>{{ selected.visibility }}</dd></div>
            <div><dt>状态</dt><dd>{{ selected.status }}</dd></div>
          </dl>
          <dl v-if="stats" class="detail-dl">
            <div><dt>下载/使用</dt><dd>{{ stats.total_downloads }} / {{ stats.total_executions }}</dd></div>
            <div><dt>成功率</dt><dd>{{ (stats.success_rate * 100).toFixed(1) }}%</dd></div>
            <div v-if="stats.avg_duration_ms !== undefined && stats.avg_duration_ms !== null">
              <dt>平均耗时</dt><dd>{{ stats.avg_duration_ms }}ms</dd>
            </div>
          </dl>
        </section>

        <section class="detail-block">
          <h4>下载</h4>
          <div class="download-row">
            <select v-model="downloadForm.versionID" data-test="marketplace-download-version">
              <option v-for="v in versions" :key="v.id" :value="v.id">{{ v.version }}</option>
            </select>
            <button class="mini-button" data-test="marketplace-download" @click="doDownload">下载 YAML</button>
          </div>
          <pre v-if="downloaded" class="yaml-pre" data-test="marketplace-downloaded">{{ downloaded.yaml }}</pre>
        </section>

        <section class="detail-block">
          <h4>评分</h4>
          <div class="rate-row">
            <select v-model.number="rateForm.rating" data-test="marketplace-rate-value">
              <option v-for="n in 5" :key="n" :value="n">{{ n }} 星</option>
            </select>
            <button class="mini-button" data-test="marketplace-rate-submit" @click="submitRate">提交评分</button>
          </div>
          <p v-if="rateError" class="error-text">{{ rateError }}</p>
          <p v-if="rateSuccess" class="hint-text">{{ rateSuccess }}</p>
          <ul v-if="ratings.length > 0" class="rating-list">
            <li v-for="rating in ratings" :key="rating.id" class="rating-item">
              <span class="rating-stars">★ {{ rating.rating }}</span>
              <span class="rating-user mono">{{ rating.user_id }}</span>
              <p v-if="rating.review">{{ rating.review }}</p>
            </li>
          </ul>
          <p v-else class="empty">暂无评分。</p>
        </section>
      </template>
    </div>
  </ViewShell>
</template>

<style scoped>
.mode-tabs {
  display: flex;
  gap: 4px;
  padding: 3px;
  border-radius: var(--radius-lg);
  background: var(--color-bg-elevated);
  border: 1px solid var(--color-border);
}

.mode-tab {
  padding: var(--space-2) var(--space-4);
  border: none;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--color-text-secondary);
  font-size: var(--font-sm);
  cursor: pointer;
}

.mode-tab.active {
  background: var(--color-accent);
  color: #fff;
}

.semantic-search {
  display: flex;
  gap: var(--space-2);
  align-items: center;
}

.semantic-input {
  flex: 1;
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-xl);
  background: var(--color-bg);
  color: var(--color-text-primary);
  font-size: var(--font-base);
  outline: none;
  box-shadow: var(--shadow-sm);
}

.semantic-input:focus {
  border-color: var(--color-accent);
}

.market-filters {
  display: grid;
  grid-template-columns: repeat(4, 1fr) auto;
  gap: var(--space-2);
  align-items: center;
  padding: var(--space-3);
  background: var(--color-bg-elevated);
  border: none;
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-sm);
}

.market-filters input,
.market-filters select {
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-bg);
  color: var(--color-text-primary);
  font-size: var(--font-sm);
  outline: none;
}

@media (max-width: 1100px) {
  .market-filters {
    grid-template-columns: repeat(2, 1fr);
  }
}

.market-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: var(--space-3);
}

.market-card {
  padding: var(--space-4);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border);
  background: var(--color-bg-elevated);
  cursor: pointer;
  transition: border-color 0.15s var(--ease-out), transform 0.15s var(--ease-out);
}

.market-card:hover {
  border-color: var(--color-accent);
  transform: translateY(-1px);
}

.market-card.active {
  border-color: var(--color-accent);
  background: var(--color-bg-active);
}

.market-card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  margin-bottom: var(--space-2);
}

.rating {
  font-size: var(--font-sm);
  color: var(--color-warning, #b8860b);
}

.market-card-name {
  margin: 0 0 6px;
  font-size: var(--font-md);
  color: var(--color-text-primary);
}

.market-card-desc {
  margin: 0 0 var(--space-2);
  font-size: var(--font-sm);
  color: var(--color-text-secondary);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.market-card-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.risk {
  font-size: var(--font-xs);
  text-transform: capitalize;
}
.risk-low { color: var(--color-success, #2c8a3e); }
.risk-medium { color: var(--color-warning, #b8860b); }
.risk-high { color: var(--color-danger, #d33); }

.stat {
  font-size: var(--font-xs);
  color: var(--color-text-tertiary);
}

.market-pagination {
  display: flex;
  justify-content: center;
  padding: var(--space-2);
}

.publish-panel {
  display: flex;
  justify-content: center;
  padding-top: var(--space-2);
}

.publish-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  width: 640px;
  max-width: 100%;
  padding: var(--space-4);
  border-radius: var(--radius-xl);
  background: var(--color-bg-elevated);
  box-shadow: var(--shadow-sm);
}

.publish-form label {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: var(--font-sm);
  color: var(--color-text-secondary);
}

.publish-form input,
.publish-form select,
.publish-form textarea {
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-bg);
  color: var(--color-text-primary);
  font-size: var(--font-sm);
  outline: none;
  font-family: inherit;
}

.publish-form textarea {
  resize: vertical;
}

.market-detail {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  width: min(440px, 100vw);
  background: var(--color-bg-elevated);
  box-shadow: var(--shadow-lg);
  border-left: 1px solid var(--color-border);
  padding: var(--space-5);
  overflow-y: auto;
  z-index: 40;
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.detail-title {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-2);
}

.detail-title h3 {
  margin: 0;
  font-size: var(--font-lg);
  color: var(--color-text-primary);
}

.detail-sub {
  font-size: var(--font-sm);
  color: var(--color-text-tertiary);
}

.detail-block h4 {
  margin: 0 0 8px;
  font-size: var(--font-base);
  color: var(--color-text-primary);
}

.detail-dl {
  margin: 0;
  display: grid;
  grid-template-columns: 1fr;
  gap: 6px;
}

.detail-dl > div {
  display: flex;
  gap: 6px;
}

.detail-dl dt {
  color: var(--color-text-tertiary);
  font-size: var(--font-sm);
  min-width: 90px;
}

.detail-dl dd {
  margin: 0;
  font-size: var(--font-md);
  color: var(--color-text-primary);
  word-break: break-all;
}

.download-row,
.rate-row {
  display: flex;
  gap: var(--space-2);
  align-items: center;
}

.download-row select,
.rate-row select {
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-bg);
  color: var(--color-text-primary);
}

.yaml-pre {
  margin: var(--space-2) 0 0;
  padding: var(--space-2);
  background: var(--color-bg);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  font-size: var(--font-sm);
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 240px;
  overflow: auto;
}

.rating-list {
  list-style: none;
  margin: var(--space-2) 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.rating-item {
  padding: var(--space-2);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-bg);
}

.rating-item p {
  margin: 6px 0 0;
  font-size: var(--font-sm);
  color: var(--color-text-secondary);
}

.rating-stars {
  color: var(--color-warning, #b8860b);
  font-size: var(--font-sm);
}

.rating-user {
  margin-left: var(--space-2);
  font-size: var(--font-xs);
  color: var(--color-text-tertiary);
}

.mono {
  font-family: var(--font-mono, monospace);
  font-size: 0.8rem;
  word-break: break-all;
}

.empty {
  padding: 1.5rem;
  text-align: center;
  color: var(--color-text-muted);
  font-size: 0.85rem;
  margin: 0;
}

.error-text {
  margin: 0;
  font-size: 0.75rem;
  color: var(--color-danger, #d33);
}

.config-banner {
  margin: 0 0 var(--space-2);
  padding: var(--space-3);
  background: var(--color-bg-elevated, #f6f8fa);
  border: 1px solid var(--color-warning-border, #d4a72c);
  border-radius: var(--radius-lg, 8px);
  font-size: 0.85rem;
  color: var(--color-text-secondary);
}

.hint-text {
  margin: 0;
  font-size: 0.75rem;
  color: var(--color-text-secondary);
}
</style>
