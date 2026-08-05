<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { getDoc } from '../api';
import MarkdownContent from '../components/MarkdownContent.vue';

const name = ref('OPERATIONS.md');
const content = ref('');
const loading = ref(false);
const error = ref('');

async function load() {
  loading.value = true;
  error.value = '';
  try {
    const doc = await getDoc(name.value);
    content.value = doc.content ?? '';
  } catch (e) {
    content.value = '';
    error.value = e instanceof Error ? e.message : '加载文档失败';
  } finally {
    loading.value = false;
  }
}

onMounted(() => {
  void load();
});
</script>

<template>
  <section data-test="docs-view" data-view="docs" class="admin-page docs-page">
    <header class="topbar docs-topbar">
      <div>
        <p class="eyebrow">Admin</p>
        <h1>使用手册</h1>
        <p class="topbar-copy">项目使用手册，来自后端 docs/ 目录（admin 只读）。</p>
      </div>
      <button class="mini-button" @click="load" :disabled="loading">刷新</button>
    </header>

    <p v-if="error" class="error-text" role="alert">{{ error }}</p>
    <p v-if="loading" class="loading-text">加载中…</p>

    <p v-if="!loading && !error && !content" class="empty">
      暂无手册内容（可能后端未配置 COPILOT_DOCS_DIR 或文档不在白名单内）。
    </p>

    <article v-else-if="content" data-test="docs-content" class="docs-article">
      <MarkdownContent :content="content" />
    </article>
  </section>
</template>

<style scoped>
.docs-page {
  max-width: 980px;
}

.docs-article {
  padding: var(--space-4);
  background: var(--color-bg-elevated);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border);
}

/* 让 MarkdownContent 里的排版在文档页里更可读 */
.docs-article :deep(.markdown-content) {
  line-height: 1.7;
  font-size: var(--font-sm, 14px);
}

.docs-article :deep(h1) {
  margin-top: 0;
}
</style>
