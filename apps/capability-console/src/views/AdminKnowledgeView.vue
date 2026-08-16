<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { ElInput } from 'element-plus';
import { listKnowledgeDocuments, addKnowledgeDocument, getKnowledgeStatus } from '../api';
import type { KnowledgeDocument } from '../types';
import ViewShell from '../components/ViewShell.vue';

const documents = ref<KnowledgeDocument[]>([]);
const loading = ref(false);
const error = ref('');

// RAG 状态
const embedderConfigured = ref(false);
const knowledgeHint = ref('');
const docCount = ref(0);

// 摄入表单
const showForm = ref(false);
const formTitle = ref('');
const formContent = ref('');
const formSource = ref('manual');
const submitting = ref(false);
const submitNotice = ref('');

async function load() {
  loading.value = true;
  error.value = '';
  try {
    const [docs, status] = await Promise.all([
      listKnowledgeDocuments(),
      getKnowledgeStatus().catch(() => null),
    ]);
    documents.value = docs;
    docCount.value = docs.length;
    if (status) {
      embedderConfigured.value = status.embedder_configured;
      knowledgeHint.value = status.hint ?? '';
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败';
  } finally {
    loading.value = false;
  }
}

function openForm() {
  showForm.value = true;
  formTitle.value = '';
  formContent.value = '';
  formSource.value = 'manual';
  submitNotice.value = '';
}

function closeForm() {
  showForm.value = false;
}

async function submit() {
  if (!formTitle.value.trim() || !formContent.value.trim()) return;
  submitting.value = true;
  submitNotice.value = '';
  try {
    await addKnowledgeDocument({
      title: formTitle.value.trim(),
      content: formContent.value.trim(),
      source: formSource.value || 'manual',
    });
    submitNotice.value = '文档已添加';
    showForm.value = false;
    await load();
  } catch (e) {
    submitNotice.value = e instanceof Error ? e.message : '添加失败';
  } finally {
    submitting.value = false;
  }
}

function formatDate(iso: string): string {
  if (!iso) return '';
  return new Date(iso).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
}

onMounted(load);
</script>

<template>
  <ViewShell
    class="admin-page"
    data-test="admin-knowledge"
    eyebrow="Admin"
    title="运维知识库"
    copy="管理 RAG 检索文档：Runbook、SOP、历史故障。执行记录会自动摄入。"
  >
    <template #actions>
      <button class="secondary-inline" @click="load" :disabled="loading">刷新</button>
      <button class="primary-inline" @click="openForm">+ 添加文档</button>
    </template>

    <p v-if="error" class="error-text" role="alert">{{ error }}</p>
    <p v-if="loading" class="loading-text">加载中…</p>
    <p v-if="submitNotice" class="notice-text">{{ submitNotice }}</p>

    <!-- RAG 状态卡片 -->
    <div
      data-test="knowledge-status"
      class="status-banner"
      :class="embedderConfigured ? 'status-ok' : 'status-warn'"
    >
      <template v-if="embedderConfigured">
        <strong>RAG 已启用</strong>
        <span>当前文档数：{{ docCount }}</span>
      </template>
      <template v-else>
        <strong>RAG 未启用</strong>
        <span>{{ knowledgeHint || '请配置 COPILOT_KNOWLEDGE_EMBEDDER_BASE_URL 和 COPILOT_KNOWLEDGE_EMBEDDER_API_KEY 以启用 RAG。' }}</span>
      </template>
    </div>

    <!-- 摄入表单 -->
    <div v-if="showForm" class="knowledge-form prompt-card">
      <h3>添加知识文档</h3>
      <label class="field">
        <span>标题</span>
        <el-input v-model="formTitle" placeholder="如：Kafka 消费者延迟排查 SOP" />
      </label>
      <label class="field">
        <span>内容</span>
        <el-input v-model="formContent" type="textarea" :rows="8" placeholder="文档正文…" />
      </label>
      <label class="field">
        <span>来源</span>
        <select v-model="formSource" class="filter-select">
          <option value="manual">手动录入</option>
          <option value="runbook">Runbook</option>
          <option value="sop">SOP</option>
          <option value="incident">历史故障</option>
        </select>
      </label>
      <div class="form-actions prompt-edit-actions">
        <button class="primary-inline" :disabled="submitting || !formTitle.trim() || !formContent.trim()" @click="submit">
          {{ submitting ? '提交中…' : '提交' }}
        </button>
        <button class="secondary-inline" @click="closeForm">取消</button>
      </div>
    </div>

    <!-- 文档列表 -->
    <div v-if="!loading && documents.length === 0 && !error" class="empty">
      暂无知识文档。执行记录会在操作完成后自动摄入。
    </div>

    <div class="doc-list">
      <article v-for="doc in documents" :key="doc.id" class="doc-card">
        <header class="doc-header">
          <strong>{{ doc.title || '(无标题)' }}</strong>
          <span class="doc-source">{{ doc.source }}</span>
        </header>
        <p class="doc-content">{{ doc.content }}</p>
        <footer class="doc-footer">
          <span>{{ formatDate(doc.created_at) }}</span>
          <span class="doc-id">{{ doc.id.slice(0, 8) }}</span>
        </footer>
      </article>
    </div>
  </ViewShell>
</template>
