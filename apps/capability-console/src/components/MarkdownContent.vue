<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { marked } from 'marked';
import DOMPurify from 'dompurify';
import hljs from 'highlight.js/lib/core';
import bash from 'highlight.js/lib/languages/bash';
import yaml from 'highlight.js/lib/languages/yaml';
import json from 'highlight.js/lib/languages/json';
import go from 'highlight.js/lib/languages/go';
import sql from 'highlight.js/lib/languages/sql';
import python from 'highlight.js/lib/languages/python';
// 运维场景高频语言按需注册，避免打包全量 hljs（全量约 1MB vs 按需 ~80KB）
hljs.registerLanguage('bash', bash);
hljs.registerLanguage('shell', bash);
hljs.registerLanguage('yaml', yaml);
hljs.registerLanguage('yml', yaml);
hljs.registerLanguage('json', json);
hljs.registerLanguage('go', go);
hljs.registerLanguage('sql', sql);
hljs.registerLanguage('python', python);

const props = defineProps<{
  content: string;
  /** 为 true 时按纯文本渲染（转义 HTML，不解析 markdown），用于 user 消息。 */
  raw?: boolean;
}>();

// marked 渲染时同步做代码高亮：给 <code> 注入 hljs class 与语言角标
marked.use({
  renderer: {
    code({ text, lang }: { text: string; lang?: string }) {
      const language = (lang ?? '').trim().split(/\s+/)[0];
      let highlighted: string;
      let resolvedLang = '';
      if (language && hljs.getLanguage(language)) {
        highlighted = hljs.highlight(text, { language }).value;
        resolvedLang = language;
      } else {
        // 无语言标注或未注册：自动探测（限制子集避免误判）
        const detected = hljs.highlightAuto(text);
        highlighted = detected.value;
        if (!language && detected.language) {
          resolvedLang = detected.language;
        }
      }
      const escapedText = text
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;');
      const label = resolvedLang
        ? `<span class="code-lang-badge" aria-hidden="true">${resolvedLang}</span>`
        : '';
      return `<pre><code class="hljs language-${resolvedLang || 'text'}" data-code="${escapedText}">${highlighted}</code>${label}</pre>`;
    },
  },
});

const html = computed(() => {
  if (!props.content) return '';
  const rawHtml = marked.parse(props.content, { async: false }) as string;
  return DOMPurify.sanitize(rawHtml, {
    // ADD_ATTR 在默认白名单之上追加：只放行高亮所需的自定义属性。
    // 不能用 ALLOWED_ATTR——它会整体替换默认名单，把 <a href> 等基础安全属性一并剥掉。
    ADD_ATTR: ['data-code'],
  });
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
      // 高亮后 textContent 含高亮 span 的文本内容，与原始 code 一致；data-code 作为兜底
      const code = pre.querySelector('code');
      const text = code?.getAttribute('data-code') ?? code?.textContent ?? pre.textContent ?? '';
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
