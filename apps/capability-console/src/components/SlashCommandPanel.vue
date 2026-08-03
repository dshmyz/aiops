<script setup lang="ts">
export interface SlashCommand {
  name: string;
  description: string;
}

const props = defineProps<{
  commands: SlashCommand[];
  visible: boolean;
  selectedIndex: number;
}>();

const emit = defineEmits<{
  (event: 'select', command: SlashCommand): void;
  (event: 'close'): void;
}>();

function selectCommand(command: SlashCommand) {
  emit('select', command);
}

function close() {
  emit('close');
}
</script>

<template>
  <div
    v-if="visible"
    data-test="slash-panel"
    class="slash-panel"
  >
    <div v-if="commands.length === 0" data-test="slash-empty" class="slash-empty">
      没有可用指令
    </div>
    <ul v-else class="slash-list">
      <li
        v-for="(command, index) in commands"
        :key="command.name"
        data-test="slash-command"
        class="slash-command"
        :class="{ selected: index === props.selectedIndex }"
        @click="selectCommand(command)"
      >
        <span data-test="slash-command-icon" class="slash-icon">
          <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
            <path
              fill="currentColor"
              d="M7.5 5.6L10 7 8.6 4.5l1.4-2.5-2.5 1.4L5 1l1.4 2.5L4 5l2.5-1.4zM19.5 15.4L17 14l1.4 2.5-1.4 2.5 2.5-1.4L21 21l-1.4-2.5L21 16l-2.5 1.4zM22 2l-3 9h-2l3-9h2zM9.7 13.7L8.3 12.3 5 15.6l-2.3-2.3-1.4 1.4 3.7 3.7 4.7-4.7z"
            />
          </svg>
        </span>
        <div class="slash-command-body">
          <span class="slash-name">{{ command.name }}</span>
          <span class="slash-desc">{{ command.description }}</span>
        </div>
      </li>
    </ul>
    <div class="slash-footer">
      <span><kbd>↑</kbd><kbd>↓</kbd> 导航</span>
      <span><kbd>Enter</kbd> 选择</span>
      <button
        data-test="slash-close"
        class="slash-close"
        @click="close"
      >
        <kbd>Esc</kbd> 关闭
      </button>
    </div>
  </div>
</template>

<style scoped>
.slash-panel {
  position: absolute;
  bottom: 100%;
  left: 0;
  right: 0;
  margin-bottom: var(--space-2);
  background: var(--color-bg-elevated);
  border: none;
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-lg);
  z-index: 10;
  overflow: hidden;
}

.slash-list {
  list-style: none;
  margin: 0;
  padding: var(--space-2);
  max-height: 260px;
  overflow-y: auto;
}

.slash-command {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: background 0.1s, color 0.1s;
}

.slash-command:hover {
  background: var(--color-bg-hover);
}

.slash-command.selected {
  background: var(--color-accent-soft);
}

.slash-icon {
  flex-shrink: 0;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  background: var(--color-bg);
  border: none;
  color: var(--color-text-secondary);
  display: flex;
  align-items: center;
  justify-content: center;
}

.slash-command.selected .slash-icon {
  color: var(--color-accent);
  border-color: var(--color-border-accent);
  background: var(--color-bg);
}

.slash-command-body {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.slash-name {
  font-family: var(--font-mono);
  font-size: var(--font-sm);
  color: var(--color-text-primary);
  font-weight: 500;
}

.slash-desc {
  font-size: var(--font-xs, 12px);
  color: var(--color-text-tertiary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.slash-command.selected .slash-name {
  color: var(--color-accent);
}

.slash-command.selected .slash-desc {
  color: var(--color-text-secondary);
}

.slash-empty {
  padding: var(--space-4);
  text-align: center;
  color: var(--color-text-tertiary);
  font-size: var(--font-sm);
}

.slash-footer {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-2) var(--space-3);
  border-top: 1px solid var(--color-border);
  font-size: var(--font-xs, 12px);
  color: var(--color-text-tertiary);
}

.slash-footer kbd {
  padding: 1px 5px;
  background: var(--color-bg);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  font-family: var(--font-mono);
  font-size: 10px;
}

.slash-close {
  margin-left: auto;
  background: transparent;
  border: none;
  color: inherit;
  cursor: pointer;
  font-size: inherit;
  padding: 2px 4px;
}

.slash-close:hover {
  color: var(--color-text-secondary);
}
</style>
