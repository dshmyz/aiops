<script setup lang="ts">
import { computed, ref } from 'vue';
import type { ConversationSummary } from '../types';
import type { ArchivedView } from '../composables/useConversations';
import { formatRelativeTime, formatAbsoluteTime, formatDateGroup } from '../conversationFormat';

const props = defineProps<{
  conversations: ConversationSummary[];
  activeConversationID: string | null;
  loading: boolean;
  searchQuery: string;
  archivedView: ArchivedView;
}>();

const emit = defineEmits<{
  (event: 'select', conversationID: string): void;
  (event: 'archive', conversationID: string): void;
  (event: 'restore', conversationID: string): void;
  (event: 'delete', conversationID: string): void;
  (event: 'rename', conversationID: string, title: string): void;
  (event: 'new'): void;
  (event: 'update:searchQuery', value: string): void;
  (event: 'update:archivedView', value: ArchivedView): void;
}>();

/* ---- 日期分组：按 last_active_at 的日期给列表加分段标题 ---- */
interface GroupedRow {
  kind: 'header';
  key: string;
  label: string;
}
interface ConversationRow {
  kind: 'item';
  conversation: ConversationSummary;
  index: number; // 会话在扁平列表中的下标，供键盘导航使用
}
type SidebarRow = GroupedRow | ConversationRow;

const groupedRows = computed<SidebarRow[]>(() => {
  const rows: SidebarRow[] = [];
  let lastKey = '';
  props.conversations.forEach((conversation, index) => {
    const group = formatDateGroup(conversation.last_active_at);
    if (group.key !== lastKey) {
      rows.push({ kind: 'header', key: group.key, label: group.label });
      lastKey = group.key;
    }
    rows.push({ kind: 'item', conversation, index });
  });
  return rows;
});

/* ---- 键盘导航：方向键在会话间移动，Enter 打开 ---- */
const listEl = ref<HTMLUListElement | null>(null);
const keyboardIndex = ref(0);

const itemEls = computed<HTMLElement[]>(() =>
  Array.from(listEl.value?.querySelectorAll<HTMLElement>('[data-test="conversation-item"]') ?? []),
);

function focusItem(index: number) {
  const els = itemEls.value;
  if (!els.length) return;
  const next = Math.min(Math.max(index, 0), els.length - 1);
  keyboardIndex.value = next;
  els[next]?.focus();
}

function handleListKeydown(event: KeyboardEvent) {
  const key = event.key;
  if (key !== 'ArrowDown' && key !== 'ArrowUp' && key !== 'ArrowLeft' && key !== 'ArrowRight') return;
  event.preventDefault();
  const dir = key === 'ArrowDown' || key === 'ArrowRight' ? 1 : -1;
  const target = keyboardIndex.value + dir;
  focusItem(target < 0 ? props.conversations.length - 1 : target % Math.max(props.conversations.length, 1));
}

function handleItemKeydown(event: KeyboardEvent, conversationID: string, index: number) {
  keyboardIndex.value = index;
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault();
    emit('select', conversationID);
  }
}

function handleTabKeydown(event: KeyboardEvent) {
  // Tab 组左右键切换（符合 WAI-ARIA tabs 模式）
  if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
  event.preventDefault();
  emit('update:archivedView', archivedView.value === 'active' ? 'archived' : 'active');
}

const archivedView = computed(() => props.archivedView);

/* 行内重命名输入框自动聚焦的轻量指令 */
const vFocus = { mounted: (el: HTMLElement) => el.focus() };

/* ---- 行内重命名：点铅笔进入编辑态，Enter/失焦提交、Esc 取消 ---- */
const renamingID = ref<string | null>(null);
const renamingDraft = ref('');

function startRename(conversation: ConversationSummary) {
  renamingID.value = conversation.id;
  renamingDraft.value = conversation.title;
}

function cancelRename() {
  renamingID.value = null;
  renamingDraft.value = '';
}

function commitRename(conversationID: string) {
  const draft = renamingDraft.value.trim();
  if (renamingID.value !== conversationID || !draft) {
    cancelRename();
    return;
  }
  emit('rename', conversationID, draft);
  cancelRename();
}

function handleRenameKeydown(event: KeyboardEvent, conversationID: string) {
  if (event.key === 'Enter') {
    event.preventDefault();
    event.stopPropagation();
    commitRename(conversationID);
  } else if (event.key === 'Escape') {
    event.preventDefault();
    event.stopPropagation();
    cancelRename();
  }
}
</script>

<template>
  <aside class="conversation-sidebar" data-test="conversation-sidebar">
    <header class="conversation-sidebar-header">
      <h2>会话历史</h2>
      <button
        data-test="conversation-new"
        class="conversation-new-button"
        @click="emit('new')"
      >
        + 新会话
      </button>
    </header>

    <div class="conversation-tabs" role="tablist" @keydown="handleTabKeydown">
      <button
        data-test="conversation-tab-active"
        role="tab"
        class="conversation-tab"
        :class="{ active: archivedView === 'active' }"
        :aria-selected="archivedView === 'active'"
        @click="emit('update:archivedView', 'active')"
      >
        活跃
      </button>
      <button
        data-test="conversation-tab-archived"
        role="tab"
        class="conversation-tab"
        :class="{ active: archivedView === 'archived' }"
        :aria-selected="archivedView === 'archived'"
        @click="emit('update:archivedView', 'archived')"
      >
        已归档
      </button>
    </div>

    <div class="conversation-search-wrap">
      <svg class="conversation-search-icon" viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
        <path fill="currentColor" d="M11 7a4 4 0 11-8 0 4 4 0 018 0zm-.7 4.3a6 6 0 111.4-1.4l3 3a1 1 0 01-1.4 1.4l-3-3z"/>
      </svg>
      <input
        data-test="conversation-search"
        type="text"
        class="conversation-search"
        placeholder="搜索会话..."
        :value="searchQuery"
        @input="emit('update:searchQuery', ($event.target as HTMLInputElement).value)"
      />
      <button
        v-if="searchQuery"
        data-test="conversation-search-clear"
        class="conversation-search-clear"
        @click="emit('update:searchQuery', '')"
        aria-label="清除搜索"
      >
        <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true">
          <path fill="currentColor" d="M8 8.6L3.3 13.3a1 1 0 01-1.4-1.4L6.6 7.2 1.9 2.5a1 1 0 011.4-1.4L8 5.8l4.7-4.7a1 1 0 011.4 1.4L9.4 7.2l4.7 4.7a1 1 0 01-1.4 1.4L8 8.6z"/>
        </svg>
      </button>
    </div>

    <div
      v-if="loading"
      data-test="conversation-sidebar-loading"
      class="conversation-sidebar-loading"
    >
      加载中...
    </div>

    <div
      v-else-if="conversations.length === 0"
      data-test="conversation-sidebar-empty"
      class="conversation-sidebar-empty"
    >
      {{ searchQuery ? '没有匹配的会话' : '还没有会话记录' }}
    </div>

    <ul
      v-else
      ref="listEl"
      class="conversation-list"
      role="listbox"
      aria-label="会话列表"
      @keydown="handleListKeydown"
    >
      <template v-for="row in groupedRows" :key="row.kind === 'header' ? `h-${row.key}` : row.conversation.id">
        <li
          v-if="row.kind === 'header'"
          :data-test="`conversation-date-header`"
          class="conversation-date-header"
          role="presentation"
          aria-hidden="true"
        >
          {{ row.label }}
        </li>
        <li
          v-else
          :data-conversation-id="row.conversation.id"
          data-test="conversation-item"
          class="conversation-item"
          :class="{ active: row.conversation.id === activeConversationID }"
          role="option"
          :aria-selected="row.conversation.id === activeConversationID"
          tabindex="0"
          @click="emit('select', row.conversation.id)"
          @focusin="keyboardIndex = row.index"
          @keydown="handleItemKeydown($event, row.conversation.id, row.index)"
        >
          <div class="conversation-item-header">
            <!-- 重命名编辑态 -->
            <input
              v-if="renamingID === row.conversation.id"
              data-test="conversation-rename-input"
              v-model="renamingDraft"
              class="conversation-rename-input"
              type="text"
              maxlength="120"
              :aria-label="'重命名会话'"
              @click.stop
              @keydown="handleRenameKeydown($event, row.conversation.id)"
              @blur="commitRename(row.conversation.id)"
              v-focus
            />
            <strong v-else class="conversation-title">{{ row.conversation.title }}</strong>
            <span v-if="renamingID !== row.conversation.id" class="conversation-item-actions">
              <button
                v-if="archivedView === 'active'"
                data-test="conversation-archive"
                class="conversation-action-button"
                title="归档"
                aria-label="归档会话"
                @click.stop="emit('archive', row.conversation.id)"
              >
                归档
              </button>
              <button
                v-if="archivedView === 'archived'"
                data-test="conversation-restore"
                class="conversation-action-button"
                title="恢复到活跃列表"
                aria-label="恢复会话"
                @click.stop="emit('restore', row.conversation.id)"
              >
                恢复
              </button>
              <button
                data-test="conversation-rename"
                class="conversation-action-button icon-only"
                title="重命名"
                aria-label="重命名会话"
                @click.stop="startRename(row.conversation)"
              >
                <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true">
                  <path fill="currentColor" d="M11.5 1.6a2 2 0 012.9 2.9l-8.3 8.3-3.7.8.8-3.7 8.3-8.3z"/>
                </svg>
              </button>
              <button
                data-test="conversation-delete"
                class="conversation-action-button icon-only danger"
                title="永久删除"
                aria-label="删除会话"
                @click.stop="emit('delete', row.conversation.id)"
              >
                <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true">
                  <path fill="currentColor" d="M6.5 1.5h3a1 1 0 011 1V3h3a.75.75 0 010 1.5h-.6l-.5 8.6A1.8 1.8 0 0110.6 15H5.4a1.8 1.8 0 01-1.8-1.9L3.1 4.5H2.5a.75.75 0 010-1.5h3v-.5a1 1 0 011-1zm.5 1.5v.5h2V3H7zm-2.4 1.5l.5 8.5c0 .2.1.3.3.3h5.2c.2 0 .3-.1.3-.3l.5-8.5H4.6zM6.5 6a.55.55 0 01.55.5l.2 4.5a.55.55 0 01-1.1.05L6 6.55A.55.55 0 016.5 6zm3 0a.55.55 0 01.55.55l-.15 4.5a.55.55 0 01-1.1-.05l.2-4.5A.55.55 0 019.5 6z"/>
                </svg>
              </button>
            </span>
          </div>
          <p class="conversation-preview">{{ row.conversation.last_message_preview }}</p>
          <time class="conversation-time" :title="formatAbsoluteTime(row.conversation.last_active_at)">{{ formatRelativeTime(row.conversation.last_active_at) }}</time>
        </li>
      </template>
    </ul>
  </aside>
</template>

<style scoped>
/* sidebar 容器：苹果风卡片，由全局 styles.css 提供基础样式（border:none + shadow）。
   scoped 这里只补充布局约束，不覆盖背景/边框/圆角，避免回退到硬边框。 */
.conversation-sidebar {
  min-width: 0;
  height: 100%;
}

/* header：用 padding + margin 分隔，不用 border-bottom */
.conversation-sidebar-header {
  padding: var(--space-4) var(--space-4) var(--space-2);
}

.conversation-sidebar-header h2 {
  font-size: var(--font-base);
  font-weight: 600;
  color: var(--color-text-primary);
  letter-spacing: -0.01em;
}

/* 新会话按钮：苹果风 pill，无硬边框 */
.conversation-new-button {
  padding: 5px 12px;
  background: var(--color-accent-soft);
  border: none;
  border-radius: var(--radius-pill);
  color: var(--color-accent);
  font-size: var(--font-sm);
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s var(--ease-out);
}

.conversation-new-button:hover {
  background: var(--color-accent);
  color: #fff;
  transform: translateY(-1px);
}

.conversation-new-button:active {
  transform: translateY(0) scale(0.97);
}

/* Tabs：苹果风 segmented control，用背景色区分，无 border-bottom */
.conversation-tabs {
  display: flex;
  gap: var(--space-1);
  padding: 0 var(--space-3) var(--space-2);
  margin: 0 var(--space-2);
  background: var(--color-bg);
  border-radius: var(--radius-md);
  padding: var(--space-1);
}

.conversation-tab {
  flex: 1;
  padding: 5px 0;
  background: transparent;
  border: none;
  border-radius: var(--radius-sm);
  color: var(--color-text-tertiary);
  font-size: var(--font-sm);
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s var(--ease-out);
}

.conversation-tab:hover {
  color: var(--color-text-secondary);
}

.conversation-tab.active {
  background: var(--color-bg-elevated);
  color: var(--color-text-primary);
  box-shadow: var(--shadow-sm);
}

/* 搜索框：苹果风，无硬边框，用背景色 + focus 光晕 */
.conversation-search-wrap {
  position: relative;
  margin: var(--space-2) var(--space-3) var(--space-2);
  display: flex;
  align-items: center;
}

.conversation-search-icon {
  position: absolute;
  left: 10px;
  color: var(--color-text-muted);
  pointer-events: none;
}

.conversation-search {
  width: 100%;
  padding: 7px 28px 7px 30px;
  background: var(--color-bg);
  border: none;
  border-radius: var(--radius-md);
  color: var(--color-text-primary);
  font-size: var(--font-sm);
  outline: none;
  transition: box-shadow 0.2s var(--ease-out), background 0.2s var(--ease-out);
}

.conversation-search:focus {
  background: var(--color-bg-elevated);
  box-shadow: var(--shadow-focus);
}

.conversation-search::placeholder {
  color: var(--color-text-muted);
}

.conversation-search-clear {
  position: absolute;
  right: 6px;
  background: transparent;
  border: none;
  color: var(--color-text-muted);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 2px;
  border-radius: var(--radius-sm);
  transition: color 0.15s;
}

.conversation-search-clear:hover {
  color: var(--color-text-primary);
}

/* 会话列表：无 border-bottom，用间距分隔 */
.conversation-list {
  list-style: none;
  margin: 0;
  padding: var(--space-2);
  overflow-y: auto;
  flex: 1;
  min-height: 0;
}

/* 日期分组标题：sticky 头，滚动时悬浮在分组顶部。
   背景继承 sidebar 的 elevated 底色，避免条目从标题下透出。 */
.conversation-date-header {
  position: sticky;
  top: calc(-1 * var(--space-2)); /* 抵消 list 顶部 padding，贴住滚动容器顶端 */
  z-index: 1;
  padding: var(--space-2) var(--space-1) var(--space-1);
  margin: var(--space-1) 0 2px;
  background: var(--color-bg-elevated);
  font-size: var(--font-xs, 12px);
  font-weight: 600;
  color: var(--color-text-tertiary);
  letter-spacing: 0.03em;
  user-select: none;
}

/* 会话项：苹果风圆角卡片，无 border-bottom，用 margin 分隔 */
.conversation-item {
  position: relative; /* 操作区绝对定位于行尾，需要它做包含块 */
  padding: var(--space-3);
  border-radius: var(--radius-lg);
  cursor: pointer;
  transition: background 0.15s var(--ease-out);
  margin-bottom: var(--space-1);
  border: none;
}

.conversation-item:hover {
  background: var(--color-bg-hover);
}

.conversation-item.active {
  background: var(--color-bg-active);
}

/* 键盘焦点：复用全局 focus-visible 蓝色描边；鼠标点击不显示圆圈 */
.conversation-item:focus {
  outline: none;
}

.conversation-item:focus-visible {
  outline: 2px solid var(--color-accent);
  outline-offset: -2px;
  background: var(--color-bg-hover);
}

.conversation-item-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  margin-bottom: 2px;
}

.conversation-title {
  font-size: var(--font-sm);
  font-weight: 600;
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
}

/* 归档按钮：苹果风，无硬边框。
   操作区整组悬浮于行尾（absolute），不占标题行布局空间——
   之前 opacity:0 隐藏会留下隐形占位，把"归档"顶离右边缘、
   行尾出现一截空白；改为悬浮后空白消失，hover 时浮现。 */
.conversation-item-actions {
  position: absolute;
  top: var(--space-3);
  right: var(--space-3);
  display: inline-flex;
  align-items: center;
  gap: 2px;
  background: inherit; /* 覆盖行尾内容，避免与长标题文字重叠时透字 */
  border-radius: var(--radius-pill);
  padding: 0 2px;
}

/* 行内重命名输入框：与标题同字号，浅底融入卡片 */
.conversation-rename-input {
  flex: 1;
  min-width: 0;
  padding: 2px 8px;
  font-size: var(--font-sm);
  font-weight: 600;
  color: var(--color-text-primary);
  background: var(--color-bg-inset, rgba(127, 127, 127, 0.08));
  border: 1px solid var(--color-accent);
  border-radius: var(--radius-sm);
  outline: none;
}

.conversation-action-button {
  flex-shrink: 0;
  padding: 3px 10px;
  background: transparent;
  border: none;
  border-radius: var(--radius-pill);
  color: var(--color-text-tertiary);
  font-size: var(--font-xs, 12px);
  font-weight: 500;
  cursor: pointer;
  transition: all 0.15s var(--ease-out);
}

.conversation-action-button:hover {
  color: var(--color-warning);
  background: var(--color-warning-soft);
}

/* 图标按钮（重命名/删除）：hover 才显现，避免列表视觉噪音。
   注意：整组操作按钮 hover 前若用 display:none 会导致归档按钮位置
   跳动；opacity 悬浮方案下它们不占布局，所以只影响自身可见性。 */
.conversation-action-button.icon-only {
  width: 22px;
  height: 22px;
  padding: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.conversation-item:not(:hover) .conversation-item-actions {
  /* 非悬浮态：文字/图标按钮整体淡出但不释放空间（absolute 下无占位成本），
     保持"归档"贴右；行内重命名输入框不受此规则影响 */
  opacity: 0;
  pointer-events: none;
}

.conversation-item:focus-within .conversation-item-actions {
  /* 键盘导航时保持整组可见（置于上条规则之后，同优先级靠后覆盖） */
  opacity: 1;
  pointer-events: auto;
}

.conversation-action-button.icon-only.danger:hover {
  color: var(--color-danger);
  background: var(--color-danger-soft, rgba(255, 59, 48, 0.12));
}

.conversation-preview {
  font-size: var(--font-sm);
  color: var(--color-text-tertiary);
  margin: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.conversation-time {
  /* 原用 text-muted（对比度 ~1.8:1，几乎不可见）+ 10px；升为 tertiary 12px 达 AA */
  font-size: var(--font-sm);
  color: var(--color-text-tertiary);
  display: block;
  margin-top: 2px;
}

.conversation-sidebar-loading,
.conversation-sidebar-empty {
  padding: var(--space-6) var(--space-4);
  text-align: center;
  color: var(--color-text-tertiary);
  font-size: var(--font-sm);
}
</style>
