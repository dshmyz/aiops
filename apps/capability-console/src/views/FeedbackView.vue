<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { listFeedback } from '../api';
import type { FeedbackEntry } from '../types';

const items = ref<FeedbackEntry[]>([]);
const total = ref(0);
const loading = ref(false);
const error = ref('');
const offset = ref(0);
const limit = 20;

const thumbsUp = computed(() => items.value.filter((f) => f.rating > 0).length);
const thumbsDown = computed(() => items.value.filter((f) => f.rating < 0).length);
const corrections = computed(() => items.value.filter((f) => f.correction.trim() !== ''));

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

function ratingIcon(rating: number): string {
  if (rating > 0) return '👍';
  if (rating < 0) return '👎';
  return '—';
}

function formatDate(iso: string): string {
  if (!iso) return '';
  return new Date(iso).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
}

defineEmits<{ 'go-to-assistant': [] }>();

onMounted(() => load());
</script>

<template>
  <section data-test="feedback-view" class="admin-page">
    <header class="topbar">
      <div>
        <p class="eyebrow">Admin</p>
        <h1>用户反馈</h1>
        <p class="topbar-copy">查看操作员对 AI 回复的评分与纠正，驱动 Planner 迭代优化。</p>
      </div>
      <button class="mini-button" @click="load(true)" :disabled="loading">刷新</button>
    </header>

    <!-- 统计卡片 -->
    <div class="stats-row">
      <div class="stat-card">
        <span class="stat-value">{{ total }}</span>
        <span class="stat-label">总反馈</span>
      </div>
      <div class="stat-card positive">
        <span class="stat-value">{{ thumbsUp }}</span>
        <span class="stat-label">👍 好评</span>
      </div>
      <div class="stat-card negative">
        <span class="stat-value">{{ thumbsDown }}</span>
        <span class="stat-label">👎 差评</span>
      </div>
      <div class="stat-card">
        <span class="stat-value">{{ corrections.length }}</span>
        <span class="stat-label">纠正</span>
      </div>
    </div>

    <p v-if="error" class="error-text" role="alert">{{ error }}</p>
    <p v-if="loading" class="loading-text">加载中…</p>

    <div v-if="!loading && items.length === 0 && !error" class="empty">
      暂无反馈数据。操作员在对话中点击 👍/👎 后会显示在这里。
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
          <td class="rating-cell">{{ ratingIcon(entry.rating) }}</td>
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
  </section>
</template>
