<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { marked } from 'marked';
import DOMPurify from 'dompurify';

const props = defineProps<{
  content: string;
  /** 为 true 时按纯文本渲染（转义 HTML，不解析 markdown），用于 user 消息。 */
  raw?: boolean;
}>();

const html = computed(() => {
  if (!props.content) return '';
  const rawHtml = marked.parse(props.content, { async: false }) as string;
  return DOMPurify.sanitize(rawHtml);
});

const containerRef = ref<HTMLElement | null>(null);

async function attachCopyButtons() {
  await nextTick();
  const root = containerRef.value;
  if (!root) return;
  const pres = root.querySelectorAll('pre');
  pres.forEach((pre) => {
    if (pre.querySelector('.markdown-copy-btn')) return;
    pre.style.position = 'relative';
    const btn = document.createElement('button');
    btn.className = 'markdown-copy-btn';
    btn.type = 'button';
    btn.setAttribute('aria-label', '复制代码');
    btn.innerHTML = '<svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true"><path fill="currentColor" d="M5 2a2 2 0 00-2 2v8a2 2 0 002 2h4a2 2 0 002-2V4a2 2 0 00-2-2H5zm0 2h4v8H5V4zm6 0h2a1 1 0 011 1v8a1 1 0 01-1 1H8a1 1 0 01-1-1v-1H6v1a2 2 0 002 2h5a2 2 0 002-2V5a2 2 0 00-2-2h-2v1z"/></svg>';
    btn.addEventListener('click', () => {
      const code = pre.querySelector('code');
      const text = code?.textContent ?? pre.textContent ?? '';
      void navigator.clipboard.writeText(text).then(() => {
        btn.classList.add('copied');
        btn.setAttribute('aria-label', '已复制');
        setTimeout(() => {
          btn.classList.remove('copied');
          btn.setAttribute('aria-label', '复制代码');
        }, 1500);
      });
    });
    pre.appendChild(btn);
  });
}

watch(html, attachCopyButtons, { immediate: true });
</script>

<template>
  <div v-if="raw" class="markdown-content markdown-content--raw">{{ content }}</div>
  <div v-else ref="containerRef" class="markdown-content" v-html="html"></div>
</template>
