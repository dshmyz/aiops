<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { ElInput } from 'element-plus';
import { updateAdminPrompt } from '../api';
import type { AdminPrompt, AdminPromptListResponse } from '../types';
import ViewShell from '../components/ViewShell.vue';

const prompts = ref<AdminPrompt[]>([]);
const loading = ref(false);
const error = ref('');
const editingName = ref<string | null>(null);
const editContent = ref('');
const editDescription = ref('');
const saving = ref(false);
const saveNotice = ref('');
const configured = ref(true);
const hint = ref('');

async function load() {
  loading.value = true;
  error.value = '';
  try {
    const body = await listAdminPromptsRaw();
    if ('configured' in body && body.configured === false) {
      configured.value = false;
      hint.value = body.hint ?? '';
      prompts.value = [];
    } else {
      configured.value = true;
      prompts.value = (body as AdminPromptListResponse).prompts ?? [];
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败';
  } finally {
    loading.value = false;
  }
}

// Raw fetch that returns the unparsed body so we can detect the
// configured:false sentinel returned by the backend.
async function listAdminPromptsRaw(): Promise<AdminPromptListResponse | { configured: false; hint: string }> {
  const resp = await fetch('/v1/admin/prompts', { headers: { 'Content-Type': 'application/json' } });
  const text = await resp.text();
  let body: unknown = null;
  if (text.trim()) {
    try { body = JSON.parse(text); } catch { body = null; }
  }
  if (!resp.ok) {
    const msg = (body as Record<string, string>)?.error ?? `请求失败 (${resp.status})`;
    throw new Error(msg);
  }
  return body as AdminPromptListResponse | { configured: false; hint: string };
}

function startEdit(prompt: AdminPrompt) {
  editingName.value = prompt.name;
  editContent.value = prompt.content;
  editDescription.value = prompt.description;
  saveNotice.value = '';
}

function cancelEdit() {
  editingName.value = null;
}

async function saveEdit() {
  if (!editingName.value) return;
  saving.value = true;
  try {
    const updated = await updateAdminPrompt(editingName.value, {
      content: editContent.value,
      description: editDescription.value,
    });
    const idx = prompts.value.findIndex((p) => p.name === updated.name);
    if (idx >= 0) prompts.value[idx] = updated;
    saveNotice.value = `已保存 v${updated.version}`;
    editingName.value = null;
  } catch (e) {
    saveNotice.value = e instanceof Error ? e.message : '保存失败';
  } finally {
    saving.value = false;
  }
}

onMounted(load);
</script>

<template>
  <ViewShell
    class="admin-page"
    data-test="admin-prompts"
    eyebrow="Admin"
    title="Prompt 版本管理"
    copy="查看和编辑系统提示词，修改后自动递增版本号并热加载到 Planner。"
  >
    <template #actions>
      <button class="mini-button" @click="load" :disabled="loading">刷新</button>
    </template>

    <p v-if="error" class="error-text" role="alert">{{ error }}</p>
    <p v-if="loading" class="loading-text">加载中…</p>

    <div v-if="!loading && !configured" data-test="prompts-not-configured" class="config-banner">
      <strong>Prompt 版本管理未启用</strong>
      <p>{{ hint || '请配置 COPILOT_PROMPTS_DIR 环境变量以启用。' }}</p>
    </div>

    <div v-if="!loading && configured && prompts.length === 0 && !error" class="empty">
      暂无已注册的 Prompt。请确认后端配置了 COPILOT_PROMPTS_DIR。
    </div>

    <div class="prompt-list">
      <article v-for="prompt in prompts" :key="prompt.name" class="prompt-card">
        <header class="prompt-card-header">
          <div>
            <strong class="prompt-name">{{ prompt.name }}</strong>
            <span class="prompt-version">v{{ prompt.version }}</span>
          </div>
          <button
            v-if="editingName !== prompt.name"
            class="secondary-inline"
            @click="startEdit(prompt)"
          >
            编辑
          </button>
        </header>
        <p v-if="prompt.description" class="prompt-desc">{{ prompt.description }}</p>

        <div v-if="editingName === prompt.name" class="prompt-edit">
          <label class="field">
            <span>描述</span>
            <el-input v-model="editDescription" placeholder="可选描述" />
          </label>
          <label class="field">
            <span>内容</span>
            <el-input v-model="editContent" type="textarea" :rows="16" />
          </label>
          <div class="prompt-edit-actions">
            <button class="primary-inline" :disabled="saving" @click="saveEdit">
              {{ saving ? '保存中…' : '保存' }}
            </button>
            <button class="secondary-inline" @click="cancelEdit">取消</button>
            <span v-if="saveNotice" class="save-notice">{{ saveNotice }}</span>
          </div>
        </div>

        <pre v-else class="prompt-content">{{ prompt.content }}</pre>
      </article>
    </div>
  </ViewShell>
</template>
