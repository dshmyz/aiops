<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { listFeedback, inferRunbookDraft, activateRunbookDraft } from '../api';
import type { FeedbackEntry, RunbookDraft } from '../types';
import { buildFeedbackInsights, insightsToMarkdown } from '../composables/useFeedbackInsights';
import type { FeedbackInsight } from '../composables/useFeedbackInsights';
import ViewShell from '../components/ViewShell.vue';
import SfSymbol from '../components/SfSymbol.vue';

const items = ref<FeedbackEntry[]>([]);
const total = ref(0);
const loading = ref(false);
const error = ref('');
const offset = ref(0);
const limit = 20;

// 改进建议基于更大样本（最多 200 条），与表格自己的分页解耦。
const insightSourceCount = ref(0);
const insights = ref<FeedbackInsight[]>([]);
const insightsLoading = ref(false);
const insightsError = ref('');

const thumbsUp = computed(() => items.value.filter((f) => f.rating > 0).length);
const thumbsDown = computed(() => items.value.filter((f) => f.rating < 0).length);
const corrections = computed(() => items.value.filter((f) => (f.correction || '').trim() !== ''));

async function load(reset = false) {
  if (reset) offset.value = 0;
  loading.value = true;
  error.value = '';
  try {
    const page = await listFeedback({ limit, offset: offset.value });
    items.value = page.items ?? [];
    total.value = page.total ?? 0;
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败';
  } finally {
    loading.value = false;
  }
}

// 拉取全量反馈（上限 200）生成改进建议清单
async function loadInsights() {
  insightsLoading.value = true;
  insightsError.value = '';
  try {
    const page = await listFeedback({ limit: 200, offset: 0 });
    insightSourceCount.value = page.total ?? page.items?.length ?? 0;
    insights.value = buildFeedbackInsights(page.items ?? []);
  } catch (e) {
    insightsError.value = e instanceof Error ? e.message : '生成改进建议失败';
  } finally {
    insightsLoading.value = false;
  }
}

function exportInsights() {
  const blob = new Blob([insightsToMarkdown(insights.value, insightSourceCount.value)], {
    type: 'text/markdown;charset=utf-8',
  });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `feedback-建议-${new Date().toISOString().slice(0, 10)}.md`;
  a.click();
  URL.revokeObjectURL(url);
}

// ===== runbook 草稿：把可落 runbook 的主题"生成 → 确认启用" =====
// 生成是确定性的（后端关键词→工具序列映射）；前端只触发生成、展示字段并确认启用。
// 单个主题项一条状态：draft 携带草稿、error 携带失败原因、activated 标记已启用。
const draftState = ref<Record<string, { loading: boolean; draft?: RunbookDraft; activating?: boolean; activated?: boolean; error?: string }>>({});

async function generateDraft(ins: FeedbackInsight) {
  const store = draftState.value;
  if (!store[ins.key]) {
    store[ins.key] = { loading: false };
  }
  const s = store[ins.key];
  s.loading = true;
  s.error = undefined;
  try {
    s.draft = await inferRunbookDraft({ topic_key: ins.key, examples: ins.examples });
  } catch (e) {
    s.error = e instanceof Error ? e.message : '生成草稿失败';
  } finally {
    s.loading = false;
  }
}

async function activateDraft(ins: FeedbackInsight) {
  const s = draftState.value[ins.key];
  if (!s?.draft) return;
  s.activating = true;
  s.error = undefined;
  try {
    await activateRunbookDraft(s.draft.id);
    s.draft = undefined;
    s.activated = true;
  } catch (e) {
    s.error = e instanceof Error ? e.message : '启用失败';
  } finally {
    s.activating = false;
  }
}

function prevPage() {
  if (offset.value > 0) {
    offset.value = Math.max(0, offset.value - limit);
    void load();
  }
}

function nextPage() {
  if (offset.value + limit < total.value) {
    offset.value += limit;
    void load();
  }
}

function formatDate(iso: string): string {
  if (!iso) return '';
  return new Date(iso).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
}

defineEmits<{ 'go-to-assistant': [] }>();

onMounted(() => {
  void load();
  void loadInsights();
});
</script>

<template>
  <ViewShell
    class="admin-page"
    data-test="feedback-view"
    eyebrow="Admin"
    title="用户反馈"
    copy="查看操作员对 AI 回复的评分与纠正，驱动 Planner 迭代优化。"
  >
    <template #actions>
      <button class="mini-button" @click="load(true)" :disabled="loading">刷新</button>
    </template>

    <!-- 统计卡片 -->
    <div class="stats-row">
      <div class="stat-card">
        <span class="stat-value">{{ total }}</span>
        <span class="stat-label">总反馈</span>
      </div>
      <div class="stat-card positive">
        <span class="stat-value">{{ thumbsUp }}</span>
        <span class="stat-label"><SfSymbol name="thumbs-up" :size="14" /> 好评</span>
      </div>
      <div class="stat-card negative">
        <span class="stat-value">{{ thumbsDown }}</span>
        <span class="stat-label"><SfSymbol name="thumbs-down" :size="14" /> 差评</span>
      </div>
      <div class="stat-card">
        <span class="stat-value">{{ corrections.length }}</span>
        <span class="stat-label">纠正</span>
      </div>
    </div>

    <p v-if="error" class="error-text" role="alert">{{ error }}</p>
    <p v-if="loading" class="loading-text">加载中…</p>

    <!-- 改进建议：把好/坏反馈沉淀成可落地的下一步动作 -->
    <section data-test="feedback-insights" class="insights-panel">
      <div class="insights-header">
        <h2>驱动迭代的改进建议</h2>
        <button
          data-test="feedback-insights-export"
          class="mini-button"
          :disabled="insightsLoading || insights.length === 0"
          @click="exportInsights"
        >
          导出建议 (.md)
        </button>
      </div>
      <p class="insights-note">
        基于 {{ insightSourceCount }} 条反馈聚合出待处理主题，按命中条数排序，供人工确认后落到 prompt / runbook / 能力 / 策略改进。
      </p>
      <p v-if="insightsLoading" class="loading-text">正在聚合反馈…</p>
      <p v-if="insightsError" class="error-text" role="alert">{{ insightsError }}</p>

      <div v-if="!insightsLoading && insights.length === 0 && !insightsError" class="insights-empty">
        暂无需要处理的负向反馈。好评与无纠正内容不会被列入改进清单。
      </div>

      <ul v-else class="insights-list">
        <li v-for="ins in insights" :key="ins.key" data-test="feedback-insight-item" class="insight-item">
          <div class="insight-head">
            <strong>{{ ins.label }}</strong>
            <span class="insight-count" :class="{ negative: ins.count > 0 }">{{ ins.count }} 条</span>
          </div>
          <p class="insight-suggestion">{{ ins.suggestion }}</p>
          <details v-if="ins.examples.length" class="insight-evidence">
            <summary>证据示例</summary>
            <ul>
              <li v-for="(ex, i) in ins.examples" :key="i">{{ ex }}</li>
            </ul>
          </details>

          <!-- runbook 草稿：可落 runbook 的主题可一键生成 → 人工确认启用 -->
          <div class="draft-area">
            <button
              v-if="!draftState[ins.key]?.draft && !draftState[ins.key]?.activated"
              data-test="feedback-draft-runbook"
              class="mini-button"
              :disabled="draftState[ins.key]?.loading || insightsLoading"
              @click="generateDraft(ins)"
            >
              {{ draftState[ins.key]?.loading ? '生成中…' : '生成 runbook 草稿' }}
            </button>
            <button
              v-if="draftState[ins.key]?.activated"
              data-test="feedback-draft-activated"
              class="mini-button"
              disabled
            >
              已启用 ✓
            </button>

            <p v-if="draftState[ins.key]?.error" class="draft-error" role="alert">{{ draftState[ins.key]?.error }}</p>

            <!-- 已生成草稿：展示推断字段 + 确认启用 -->
            <div v-if="draftState[ins.key]?.draft" data-test="feedback-draft-preview" class="draft-preview">
              <template v-if="draftState[ins.key]?.draft?.missing_reason">
                <p class="draft-missing">{{ draftState[ins.key]?.draft?.missing_reason }}</p>
              </template>
              <template v-else>
                <div class="draft-fields">
                  <span class="draft-field"><span class="draft-key">意图匹配</span>{{ draftState[ins.key]?.draft?.intent_pattern.join('、') }}</span>
                  <span class="draft-field"><span class="draft-key">工具序列</span>{{ draftState[ins.key]?.draft?.tool_sequence.join(' → ') }}</span>
                  <span class="draft-field"><span class="draft-key">风险</span>{{ draftState[ins.key]?.draft?.risk_level }}</span>
                </div>
                <button
                  data-test="feedback-draft-activate"
                  class="mini-button"
                  :disabled="draftState[ins.key]?.activating"
                  @click="activateDraft(ins)"
                >
                  {{ draftState[ins.key]?.activating ? '启用中…' : '确认启用' }}
                </button>
              </template>
            </div>
          </div>
        </li>
      </ul>
    </section>

    <div v-if="!loading && items.length === 0 && !error" class="empty">
      暂无反馈数据。操作员在对话中点好评/差评后会显示在这里。
      <button
        data-test="feedback-empty-action"
        class="empty-action-btn"
        @click="$emit('go-to-assistant')"
      >
        去对话反馈
      </button>
    </div>

    <!-- 反馈列表 -->
    <table v-if="items.length > 0" class="feedback-table">
      <thead>
        <tr>
          <th>评分</th>
          <th>操作人</th>
          <th>纠正内容</th>
          <th>时间</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="entry in items" :key="entry.id">
          <td class="rating-cell">
              <SfSymbol v-if="entry.rating > 0" name="thumbs-up" :size="18" />
              <SfSymbol v-else-if="entry.rating < 0" name="thumbs-down" :size="18" />
              <template v-else>—</template>
            </td>
          <td>{{ entry.subject }}</td>
          <td class="correction-cell">{{ entry.correction || '—' }}</td>
          <td class="time-cell">{{ formatDate(entry.created_at) }}</td>
        </tr>
      </tbody>
    </table>

    <!-- 分页 -->
    <div v-if="total > limit" class="pagination">
      <button class="secondary-inline" :disabled="offset === 0" @click="prevPage">上一页</button>
      <span class="page-info">{{ offset + 1 }}–{{ Math.min(offset + limit, total) }} / {{ total }}</span>
      <button class="secondary-inline" :disabled="offset + limit >= total" @click="nextPage">下一页</button>
    </div>
  </ViewShell>
</template>

<style scoped>
.insights-panel {
  margin-bottom: var(--space-5);
  padding: var(--space-4);
  background: var(--color-bg-elevated);
  border-radius: var(--radius-lg);
}

.insights-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}

.insights-header h2 {
  margin: 0;
  font-size: var(--font-lg);
}

.insights-note {
  margin: var(--space-2) 0 0;
  font-size: var(--font-sm);
  color: var(--color-text-secondary);
}

.insights-empty {
  margin-top: var(--space-3);
  padding: var(--space-4);
  border: 1px dashed var(--color-border);
  border-radius: var(--radius-md);
  text-align: center;
  color: var(--color-text-tertiary);
  font-size: var(--font-sm);
}

.insights-list {
  list-style: none;
  margin: var(--space-3) 0 0;
  padding: 0;
  display: grid;
  gap: var(--space-3);
}

.insight-item {
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-bg);
}

.insight-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.insight-count {
  font-size: var(--font-xs);
  padding: 2px 8px;
  border-radius: var(--radius-pill);
  background: var(--color-bg-hover);
  color: var(--color-text-secondary);
}

.insight-count.negative {
  background: var(--color-danger-soft);
  color: var(--color-danger);
}

.insight-suggestion {
  margin: var(--space-2) 0 0;
  font-size: var(--font-sm);
  color: var(--color-text-primary);
  line-height: 1.6;
}

.insight-evidence {
  margin-top: var(--space-2);
  font-size: var(--font-sm);
  color: var(--color-text-secondary);
}

.insight-evidence summary {
  cursor: pointer;
  color: var(--color-text-tertiary);
}

.insight-evidence ul {
  margin: var(--space-2) 0 0;
  padding-left: var(--space-4);
  display: grid;
  gap: var(--space-1);
}

.draft-area {
  margin-top: var(--space-3);
  padding-top: var(--space-3);
  border-top: 1px dashed var(--color-border);
  display: grid;
  gap: var(--space-2);
}

.draft-preview {
  padding: var(--space-3);
  background: var(--color-bg-elevated);
  border-radius: var(--radius-md);
  display: grid;
  gap: var(--space-2);
}

.draft-fields {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.draft-field {
  font-size: var(--font-xs);
  color: var(--color-text-secondary);
  padding: 2px 8px;
  border-radius: var(--radius-sm);
  background: var(--color-bg-hover);
}

.draft-key {
  font-weight: 600;
  margin-right: 4px;
  color: var(--color-text-primary);
}

.draft-missing {
  margin: 0;
  font-size: var(--font-sm);
  color: var(--color-text-secondary);
}

.draft-error {
  margin: 0;
  font-size: var(--font-sm);
  color: var(--color-danger);
}
</style>
