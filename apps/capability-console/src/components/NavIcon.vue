<script setup lang="ts">
/**
 * NavIcon renders an inline SVG icon for a navigation entry.
 *
 * Why a component instead of emoji: emoji glyphs depend on the platform
 * font (Segoe UI Emoji on Windows, Apple Color Emoji on macOS/iOS, Noto
 * on Linux/Android) so the same codepoint renders as a colored 3D shape
 * on one OS and a missing-glyph box on another. Inline SVG is rendered
 * identically everywhere and inherits `currentColor`, so the icon follows
 * the active-state tint defined by --view-accent.
 *
 * Icons are simplified from Lucide (ISC/Ishikawa Masashi, MIT-style license).
 * Each path uses stroke="currentColor" so it tints with the parent color.
 */
type IconName = 'assistant' | 'management' | 'plans' | 'audit' | 'scheduled-tasks' | 'prompts' | 'knowledge' | 'feedback' | 'mcp-servers';

defineProps<{ name: IconName }>();
</script>

<template>
  <svg
    data-test="nav-icon"
    class="nav-icon"
    aria-hidden="true"
    width="1em"
    height="1em"
    viewBox="0 0 24 24"
    fill="currentColor"
    xmlns="http://www.w3.org/2000/svg"
  >
    <!-- assistant: chat bubble -->
    <template v-if="name === 'assistant'">
      <path
        d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>
    <!-- management: settings/sliders -->
    <template v-else-if="name === 'management'">
      <path
        d="M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3M1 14h6M9 8h6M17 16h6"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>
    <!-- plans: hourglass (pending) -->
    <template v-else-if="name === 'plans'">
      <path
        d="M6 2h12M6 22h12M6 2v6a6 6 0 0 0 12 0V2M6 22v-6a6 6 0 0 1 12 0v6"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>
    <!-- audit: clipboard with lines -->
    <template v-else-if="name === 'audit'">
      <path
        d="M9 4h6a1 1 0 0 1 1 1v1H8V5a1 1 0 0 1 1-1zM7 5H5a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2M9 12h6M9 16h6"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>
    <!-- scheduled-tasks: clock -->
    <template v-else-if="name === 'scheduled-tasks'">
      <circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" stroke-width="2" />
      <path
        d="M12 7v5l3 3"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>
    <!-- prompts: file-text -->
    <template v-else-if="name === 'prompts'">
      <path
        d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8zM14 2v6h6M16 13H8M16 17H8M10 9H8"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>
    <!-- knowledge: book-open -->
    <template v-else-if="name === 'knowledge'">
      <path
        d="M2 3h6a4 4 0 0 1 4 4v14a3 3 0 0 0-3-3H2zM22 3h-6a4 4 0 0 0-4 4v14a3 3 0 0 1 3-3h7z"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>
    <!-- feedback: message-square with thumbs -->
    <template v-else-if="name === 'feedback'">
      <path
        d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>
    <!-- mcp-servers: plug (external connection) -->
    <template v-else-if="name === 'mcp-servers'">
      <path
        d="M9 2v6M15 2v6M7 8h10v3a5 5 0 0 1-10 0V8zM12 16v6"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </template>
    <!-- unknown: placeholder dot -->
    <template v-else>
      <circle cx="12" cy="12" r="4" />
    </template>
  </svg>
</template>
