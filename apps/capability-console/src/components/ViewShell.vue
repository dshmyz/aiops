<script setup lang="ts">
/**
 * 统一视图骨架：入口容器 + 顶栏 + 正文。
 *
 * 每个"有顶栏的视图"都用本组件包裹，替代过去每个视图各自手写的
 * `<section class="xxx-entry"><header class="topbar">…</header>…</section>`。
 * 水平 24px 边距、flex 纵向排布、入口高度全部归本组件拥有，页面不再自管 padding。
 *
 * 透传约定（利用 attrs fallthrough 落到根 section，不改任何引用即保持生效）：
 * - class：保留视图级入口类（如 audit-entry / admin-page），供视图级 CSS 使用
 * - data-test / data-view：测试选择器与 `[data-view=…]{--view-accent}` 取色继续生效
 * - v-show / v-if：如 ManagementView 按阶段显隐
 *
 * 顶栏水平 padding 已由全局 `.topbar` 归零（见 styles.css），此处不再重复。
 */
defineProps<{
  /** 顶栏小标签（如 "Ops Overview"），可空 */
  eyebrow?: string;
  /** 顶栏主标题 */
  title: string;
  /** 顶栏副标题说明文字，可空 */
  copy?: string;
}>();
</script>

<template>
  <section class="view-shell">
    <header class="topbar">
      <div>
        <p v-if="eyebrow" class="eyebrow">{{ eyebrow }}</p>
        <h1>{{ title }}</h1>
        <p v-if="copy" class="topbar-copy">{{ copy }}</p>
      </div>
      <div v-if="$slots.actions" class="topbar-right">
        <slot name="actions" />
      </div>
    </header>
    <slot />
  </section>
</template>

<style scoped>
.view-shell {
  /* 入口容器唯一一份：水平 24px 收边 + 底部滚动余量。
     垂直方向 flex column 让顶栏与正文自然堆叠，间距由 topbar 的
     padding-bottom(var(--space-4)) 提供，gap 归零。 */
  padding: 0 var(--space-6) var(--space-6);
  display: flex;
  flex-direction: column;
  gap: 0;
  height: 100%;
  min-height: 0;
}
</style>
