<script setup lang="ts">
/**
 * SF Symbol 风格图标
 * 用 SVG 模拟苹果 SF Symbol 的视觉质感：圆角线条、柔和描边、可着色
 * 不依赖外部字体，保证跨平台一致
 */
withDefaults(defineProps<{
  /** 图标名称，对应内置 SF Symbol 风格图形 */
  name: 'bubble-left' | 'sparkles' | 'waveform' | 'exclamationmark-triangle' | 'checkmark-circle' | 'arrow-up-circle'
  /** 尺寸 px */
  size?: number
}>(), {
  size: 24,
});
</script>

<template>
  <svg
    class="sf-symbol"
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.8"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <!-- 对话气泡（左下尖角） -->
    <template v-if="name === 'bubble-left'">
      <path d="M4 6a3 3 0 0 1 3-3h10a3 3 0 0 1 3 3v8a3 3 0 0 1-3 3H9l-4 3v-3a3 3 0 0 1-1-2V6z" />
      <circle cx="9" cy="10" r="0.6" fill="currentColor" stroke="none" />
      <circle cx="12" cy="10" r="0.6" fill="currentColor" stroke="none" />
      <circle cx="15" cy="10" r="0.6" fill="currentColor" stroke="none" />
    </template>

    <!-- 闪光（AI 标识） -->
    <template v-else-if="name === 'sparkles'">
      <path d="M12 3l1.6 4.4L18 9l-4.4 1.6L12 15l-1.6-4.4L6 9l4.4-1.6L12 3z" />
      <path d="M19 14l.7 1.9L21.5 17l-1.8.7L19 19.5l-.7-1.8L16.5 17l1.8-.7L19 14z" />
    </template>

    <!-- 波形（运维监控） -->
    <template v-else-if="name === 'waveform'">
      <path d="M3 12h2M7 6v12M11 9v6M15 4v16M19 10v4M21 12h0" />
    </template>

    <!-- 警告三角 -->
    <template v-else-if="name === 'exclamationmark-triangle'">
      <path d="M12 3.5L2.5 20h19L12 3.5z" />
      <line x1="12" y1="10" x2="12" y2="14" />
      <circle cx="12" cy="17" r="0.6" fill="currentColor" stroke="none" />
    </template>

    <!-- 对勾圆 -->
    <template v-else-if="name === 'checkmark-circle'">
      <circle cx="12" cy="12" r="9" />
      <path d="M8 12.5l2.5 2.5L16 9.5" />
    </template>

    <!-- 上箭头圆（发送） -->
    <template v-else-if="name === 'arrow-up-circle'">
      <circle cx="12" cy="12" r="9" />
      <line x1="12" y1="16" x2="12" y2="8" />
      <polyline points="8,12 12,8 16,12" />
    </template>
  </svg>
</template>

<style scoped>
.sf-symbol {
  display: inline-block;
  vertical-align: middle;
  flex-shrink: 0;
}
</style>
