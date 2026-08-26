<script setup lang="ts">
import { computed } from 'vue';
import type { AdminTool } from '../../types';

const props = defineProps<{
  tools: AdminTool[];
}>();

const emit = defineEmits<{
  (event: 'select', tool: AdminTool | undefined): void;
}>();

const sorted = computed(() => [...props.tools].sort((a, b) => a.name.localeCompare(b.name)));

function group(t: AdminTool): string {
  return t.domain || (t.operation === 'write' ? '处置' : '其他');
}

const groups = computed(() => {
  const map = new Map<string, AdminTool[]>();
  for (const t of sorted.value) {
    const g = group(t);
    if (!map.has(g)) map.set(g, []);
    map.get(g)!.push(t);
  }
  return map;
});

function pick(t: AdminTool) {
  emit('select', t);
}
</script>

<template>
  <div class="tool-picker" data-test="alert-tool-picker">
    <el-select
      filterable
      clearable
      placeholder="选择可用工具（支持搜索）"
      class="tool-select"
      @change="(v: string | undefined) => emit('select', props.tools.find((t) => t.name === v))"
      data-test="alert-tool-select"
    >
      <el-option-group v-for="[g, items] in groups" :key="g" :label="g">
        <el-option v-for="t in items" :key="t.name" :value="t.name" :label="t.name">
          <span class="opt-name">{{ t.name }}</span>
          <span class="opt-meta">{{ t.operation }} · {{ t.risk }}</span>
        </el-option>
      </el-option-group>
    </el-select>
  </div>
</template>

<style scoped>
.tool-select { width: 100%; }
.opt-name { font-size: var(--font-xs); }
.opt-meta { font-size: 11px; color: var(--color-text-tertiary); margin-left: 8px; }
</style>
