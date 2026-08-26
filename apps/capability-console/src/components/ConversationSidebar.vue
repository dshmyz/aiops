<script setup lang="ts">
import type { ConversationSummary } from '../types';
import type { ArchivedView } from '../composables/useConversations';
import { formatRelativeTime, formatAbsoluteTime } from '../conversationFormat';

defineProps<{
  conversations: ConversationSummary[];
  activeConversationID: string | null;
  loading: boolean;
  searchQuery: string;
  archivedView: ArchivedView;
}>();

const emit = defineEmits<{
  (event: 'select', conversationID: string): void;
  (event: 'archive', conversationID: string): void;
  (event: 'new'): void;
  (event: 'update:searchQuery', value: string): void;
  (event: 'update:archivedView', value: ArchivedView): void;
}>();
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

    <div class="conversation-tabs" role="tablist">
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

    <ul v-else class="conversation-list">
      <li
        v-for="conversation in conversations"
        :key="conversation.id"
        data-test="conversation-item"
        :data-conversation-id="conversation.id"
        class="conversation-item"
        :class="{ active: conversation.id === activeConversationID }"
        @click="emit('select', conversation.id)"
      >
        <div class="conversation-item-header">
          <strong class="conversation-title">{{ conversation.title }}</strong>
          <button
            v-if="archivedView === 'active'"
            data-test="conversation-archive"
            class="conversation-archive-button"
            @click.stop="emit('archive', conversation.id)"
          >
            归档
          </button>
        </div>
        <p class="conversation-preview">{{ conversation.last_message_preview }}</p>
        <time class="conversation-time" :title="formatAbsoluteTime(conversation.last_active_at)">{{ formatRelativeTime(conversation.last_active_at) }}</time>
      </li>
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

/* 会话项：苹果风圆角卡片，无 border-bottom，用 margin 分隔 */
.conversation-item {
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

/* 归档按钮：苹果风，无硬边框 */
.conversation-archive-button {
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

.conversation-archive-button:hover {
  color: var(--color-warning);
  background: var(--color-warning-soft);
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
