<script setup lang="ts">
/**
 * 列宽拖拽手柄：一条可拖拽的垂直分隔条，用于调整相邻列宽。
 *
 * 通用设计：v-model 绑定被它控制的那一列的宽度（px）。拖拽公式统一为
 * `newValue = start ± delta`，方向由 `anchor` 决定——
 *   - anchor="left"（默认）：被控列在手柄左侧，向右拖 → 值增大（左列变宽）
 *   - anchor="right"：被控列在手柄右侧，向右拖 → 值减小（右列变窄）
 * 两条分隔条用同一个组件即可，无需两套拖拽逻辑。
 *
 * 手柄自身占据一条固定宽度的网格轨道（宽由外部 grid-template-columns 决定），
 * 因此拖拽后列宽变化时手柄会随被控列边缘自动移动。
 */
import { onBeforeUnmount, ref } from 'vue';

const props = withDefaults(
  defineProps<{
    modelValue: number;
    min?: number;
    max?: number;
    label?: string;
    /** 被控列在手柄的哪一侧。见文件头注释的拖拽方向说明。 */
    anchor?: 'left' | 'right';
    /** 视口窄于该值（如 '1100px'）时隐藏本手柄。用 v-show 内联 display:none，
        避免 scoped `display:flex` 与全局断点规则的同特异性冲突。 */
    hideBelow?: string;
  }>(),
  { min: 120, max: 800, label: '调整列宽', anchor: 'left' },
);

const emit = defineEmits<{
  (e: 'update:modelValue', value: number): void;
  (e: 'drag-start'): void;
  (e: 'drag-end'): void;
}>();

const dragging = ref(false);

// —— hideBelow：监听视口断点，窄屏时从网格中隐藏（v-show）。
//    jsdom 等环境无 matchMedia，此时退化为不隐藏（媒体查询本就不生效）。 ——
const collapsedMedia =
  props.hideBelow && typeof window.matchMedia === 'function'
    ? window.matchMedia(`(max-width: ${props.hideBelow})`)
    : null;
const hidden = ref(collapsedMedia?.matches ?? false);
function onCollapsedChange(e: MediaQueryListEvent) {
  hidden.value = e.matches;
}
if (collapsedMedia) collapsedMedia.addEventListener('change', onCollapsedChange);

// —— 拖拽状态：只记起点，增量随 pointermove 换算，列宽夹取到 [min, max] ——
let startX = 0;
let startValue = 0;

function clamp(v: number): number {
  return Math.min(props.max, Math.max(props.min, Math.round(v)));
}

function onPointerDown(e: PointerEvent) {
  e.preventDefault();
  dragging.value = true;
  startX = e.clientX;
  startValue = props.modelValue;
  document.body.style.cursor = 'col-resize';
  document.body.style.userSelect = 'none';
  emit('drag-start');
  window.addEventListener('pointermove', onPointerMove);
  window.addEventListener('pointerup', onPointerUp);
}

function onPointerMove(e: PointerEvent) {
  if (!dragging.value) return;
  const delta = e.clientX - startX;
  const next = props.anchor === 'right' ? startValue - delta : startValue + delta;
  emit('update:modelValue', clamp(next));
}

function onPointerUp() {
  if (!dragging.value) return;
  dragging.value = false;
  document.body.style.cursor = '';
  document.body.style.userSelect = '';
  emit('drag-end');
  window.removeEventListener('pointermove', onPointerMove);
  window.removeEventListener('pointerup', onPointerUp);
}

// 键盘无障碍：方向键以 10px 步进调宽，方向与 anchor 一致。
function onKeydown(e: KeyboardEvent) {
  const dir = props.anchor === 'right' ? -1 : 1;
  if (e.key === 'ArrowLeft') {
    e.preventDefault();
    emit('update:modelValue', clamp(props.modelValue - 10 * dir));
  } else if (e.key === 'ArrowRight') {
    e.preventDefault();
    emit('update:modelValue', clamp(props.modelValue + 10 * dir));
  }
}

onBeforeUnmount(() => {
  window.removeEventListener('pointermove', onPointerMove);
  window.removeEventListener('pointerup', onPointerUp);
  collapsedMedia?.removeEventListener('change', onCollapsedChange);
});
</script>

<template>
  <div
    v-show="!hidden"
    class="split-handle"
    :class="{ dragging }"
    role="separator"
    aria-orientation="vertical"
    :aria-label="label"
    :aria-valuenow="modelValue"
    :aria-valuemin="min"
    :aria-valuemax="max"
    tabindex="0"
    @pointerdown="onPointerDown"
    @keydown="onKeydown"
  >
    <span class="split-handle__grip" aria-hidden="true" />
  </div>
</template>

<style scoped>
.split-handle {
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: col-resize;
  touch-action: none; /* 触屏拖拽必需：否则 pointermove 会被浏览器接管做滚动 */
  user-select: none;
  outline: none;
  min-height: 0;
}

/* 默认淡显提示可拖拽，hover / 键盘聚焦 / 拖拽中高亮。 */
.split-handle__grip {
  width: 3px;
  height: 34px;
  border-radius: 9999px;
  background: var(--color-border-strong);
  opacity: 0.35;
  transition: opacity 0.15s var(--ease-out), background 0.15s var(--ease-out);
}
.split-handle:hover .split-handle__grip,
.split-handle:focus-visible .split-handle__grip,
.split-handle.dragging .split-handle__grip {
  opacity: 1;
  background: var(--color-accent);
}
</style>
