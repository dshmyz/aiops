<script setup lang="ts">
import { onMounted, ref } from 'vue';
import ViewShell from '../components/ViewShell.vue';
import {
  listSkills,
  createSkill,
  updateSkill,
  deleteSkill,
  type SkillRecord,
} from '../api';

/**
 * 技能（运维 SOP 能力包）管理页：
 * 列出内置/自定义技能，支持新建、编辑、启停；内置技能不可删除（后端校验）。
 * 写操作要求 operator/admin 角色，viewer 只读——越权时展示后端 403 文案。
 */

const skills = ref<SkillRecord[]>([]);
const loading = ref(false);
const error = ref('');
const formOpen = ref(false);
const editing = ref<SkillRecord | null>(null);
const saving = ref(false);

// 表单字段（编辑时回填；新建为空模板）
const form = ref({
  slug: '',
  name: '',
  description: '',
  content: '',
  output_contract: '',
  risk_level: 'low',
  is_enabled: true,
});

async function refresh() {
  loading.value = true;
  error.value = '';
  try {
    skills.value = await listSkills();
  } catch (err) {
    error.value = err instanceof Error ? err.message : '加载技能列表失败';
  } finally {
    loading.value = false;
  }
}

function openCreate() {
  editing.value = null;
  form.value = { slug: '', name: '', description: '', content: '', output_contract: '', risk_level: 'low', is_enabled: true };
  formOpen.value = true;
}

function openEdit(sk: SkillRecord) {
  editing.value = sk;
  form.value = {
    slug: sk.slug,
    name: sk.name,
    description: sk.description ?? '',
    content: sk.content ?? '',
    output_contract: sk.output_contract ?? '',
    risk_level: sk.risk_level || 'low',
    is_enabled: sk.is_enabled,
  };
  formOpen.value = true;
}

async function save() {
  if (!form.value.slug.trim() || !form.value.content.trim()) {
    error.value = 'slug 与 SOP 正文不能为空';
    return;
  }
  saving.value = true;
  error.value = '';
  try {
    if (editing.value) {
      await updateSkill(editing.value.id, { ...form.value });
    } else {
      await createSkill({ ...form.value });
    }
    formOpen.value = false;
    await refresh();
  } catch (err) {
    error.value = err instanceof Error ? err.message : '保存失败';
  } finally {
    saving.value = false;
  }
}

async function toggleEnabled(sk: SkillRecord) {
  error.value = '';
  try {
    await updateSkill(sk.id, {
      slug: sk.slug,
      name: sk.name,
      description: sk.description,
      content: sk.content,
      output_contract: sk.output_contract,
      risk_level: sk.risk_level,
      is_enabled: !sk.is_enabled,
    });
    await refresh();
  } catch (err) {
    error.value = err instanceof Error ? err.message : '更新失败';
  }
}

async function remove(sk: SkillRecord) {
  if (sk.is_builtin) {
    error.value = '内置技能不可删除，可改为停用';
    return;
  }
  if (!window.confirm(`确认删除技能「${sk.name || sk.slug}」？`)) {
    return;
  }
  error.value = '';
  try {
    await deleteSkill(sk.id);
    await refresh();
  } catch (err) {
    error.value = err instanceof Error ? err.message : '删除失败';
  }
}

onMounted(refresh);
</script>

<template>
  <ViewShell
    class="skills-entry"
    data-test="skills-entry"
    data-view="skills"
    eyebrow="AIOps Skills"
    title="技能 / 运维手册管理"
    copy="维护 Agent 可参考的运维 SOP：命中消息主题的技能正文会注入提示词，指导排障步骤与输出约束。"
  >
    <template #actions>
      <button class="mini-button" :disabled="loading" @click="refresh">
        {{ loading ? '刷新中' : '刷新' }}
      </button>
      <button data-test="skill-new" class="mini-button skill-primary" @click="openCreate">
        + 新建技能
      </button>
    </template>

    <div v-if="error" data-test="skills-error" class="skills-error">{{ error }}</div>

    <div v-if="!loading && skills.length === 0 && !error" class="skills-empty">
      暂无技能。点击「+ 新建技能」添加第一条运维手册。
    </div>

    <ul class="skill-list" data-test="skill-list">
      <li
        v-for="sk in skills"
        :key="sk.id"
        class="skill-row"
        :class="{ disabled: !sk.is_enabled }"
      >
        <div class="skill-main">
          <div class="skill-title">
            <strong>{{ sk.name || sk.slug }}</strong>
            <span class="skill-slug">{{ sk.slug }}</span>
            <span v-if="sk.is_builtin" class="skill-badge builtin">内置</span>
            <span v-if="!sk.is_enabled" class="skill-badge off">已停用</span>
          </div>
          <p v-if="sk.description" class="skill-desc">{{ sk.description }}</p>
          <pre class="skill-content">{{ sk.content }}</pre>
          <p v-if="sk.output_contract" class="skill-contract">输出约束：{{ sk.output_contract }}</p>
        </div>
        <div class="skill-actions">
          <button class="mini-button" @click="openEdit(sk)">编辑</button>
          <button class="mini-button" @click="toggleEnabled(sk)">
            {{ sk.is_enabled ? '停用' : '启用' }}
          </button>
          <button v-if="!sk.is_builtin" class="mini-button skill-danger" @click="remove(sk)">删除</button>
        </div>
      </li>
    </ul>

    <div v-if="formOpen" class="form-modal" data-test="skill-form-modal">
      <form class="skill-form" @submit.prevent="save">
        <h3>{{ editing ? '编辑技能' : '新建技能' }}</h3>
        <label>Slug（唯一标识）</label>
        <input v-model="form.slug" type="text" placeholder="如 kafka-consumer-group" required />
        <label>名称</label>
        <input v-model="form.name" type="text" placeholder="如 Kafka 消费组排障" />
        <label>描述</label>
        <input v-model="form.description" type="text" placeholder="什么场景适用（用于相关性匹配）" />
        <label>SOP 正文</label>
        <textarea v-model="form.content" rows="8" placeholder="排障步骤、检查清单…" required></textarea>
        <label>输出约束（可选）</label>
        <input v-model="form.output_contract" type="text" placeholder="要求模型产出什么结论/格式" />
        <div class="skill-form-row">
          <label class="inline">风险等级</label>
          <select v-model="form.risk_level">
            <option value="low">low</option>
            <option value="medium">medium</option>
            <option value="high">high</option>
          </select>
          <label class="inline"><input v-model="form.is_enabled" type="checkbox" /> 启用</label>
        </div>
        <div class="skill-form-actions">
          <button type="button" class="mini-button" @click="formOpen = false">取消</button>
          <button type="submit" class="mini-button skill-primary" :disabled="saving">
            {{ saving ? '保存中…' : '保存' }}
          </button>
        </div>
      </form>
    </div>
  </ViewShell>
</template>

<style scoped>
.skills-error {
  border: 1px solid var(--danger, #c0392b);
  color: var(--danger, #c0392b);
  border-radius: 8px;
  padding: 8px 12px;
  margin-bottom: 12px;
  font-size: 13px;
}

.skills-empty {
  color: var(--text-secondary, #667085);
  padding: 24px 0;
  text-align: center;
  font-size: 13px;
}

.skill-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.skill-row {
  display: flex;
  gap: 16px;
  align-items: flex-start;
  justify-content: space-between;
  border: 1px solid var(--border, #e4e7ec);
  border-radius: 10px;
  padding: 12px 14px;
  background: var(--surface-secondary, #fafbfc);
}

.skill-row.disabled {
  opacity: 0.55;
}

.skill-main {
  min-width: 0;
  flex: 1;
}

.skill-title {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.skill-slug {
  color: var(--text-tertiary, #98a2b3);
  font-size: 12px;
  font-family: ui-monospace, monospace;
}

.skill-badge {
  font-size: 11px;
  border-radius: 999px;
  padding: 1px 8px;
  border: 1px solid currentColor;
}

.skill-badge.builtin {
  color: var(--color-accent, #10a37f);
}

.skill-badge.off {
  color: var(--text-tertiary, #98a2b3);
}

.skill-desc {
  margin: 6px 0 0;
  color: var(--text-secondary, #475467);
  font-size: 13px;
}

.skill-content {
  margin: 8px 0 0;
  white-space: pre-wrap;
  word-break: break-word;
  background: var(--surface-tertiary, #f2f4f7);
  border-radius: 8px;
  padding: 8px 10px;
  font-size: 12px;
  max-height: 180px;
  overflow: auto;
}

.skill-contract {
  margin: 6px 0 0;
  font-size: 12px;
  color: var(--text-secondary, #475467);
}

.skill-actions {
  display: flex;
  gap: 6px;
  flex-shrink: 0;
}

.skill-primary {
  background: var(--color-accent, #10a37f);
  border-color: var(--color-accent, #10a37f);
  color: #fff;
}

.skill-danger:hover:not(:disabled) {
  color: var(--danger, #c0392b);
  border-color: var(--danger, #c0392b);
}

.form-modal {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.35);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 60;
}

.skill-form {
  background: var(--surface, #fff);
  border-radius: 12px;
  padding: 18px 20px;
  width: min(560px, calc(100vw - 48px));
  max-height: calc(100vh - 96px);
  overflow: auto;
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.18);
}

.skill-form h3 {
  margin: 0 0 12px;
}

.skill-form label {
  display: block;
  font-size: 12px;
  color: var(--text-secondary, #475467);
  margin: 10px 0 4px;
}

.skill-form label.inline {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin: 12px 16px 0 0;
}

.skill-form input[type='text'],
.skill-form textarea,
.skill-form select {
  width: 100%;
  box-sizing: border-box;
  border: 1px solid var(--border, #d0d5dd);
  border-radius: 8px;
  padding: 7px 9px;
  font-size: 13px;
  font-family: inherit;
  background: var(--surface, #fff);
  color: var(--text-primary, #101828);
}

.skill-form textarea {
  font-family: ui-monospace, monospace;
  resize: vertical;
}

.skill-form-row {
  display: flex;
  align-items: center;
}

.skill-form-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 14px;
}
</style>
