<script setup lang="ts">
import { computed } from 'vue';
import type { ProbeInferResult } from '../../types';

interface Props {
  probe: ProbeInferResult;
}

const props = defineProps<Props>();

const rawBody = computed(() => props.probe.raw_body ?? '');
const normalizedRows = computed(() => Object.entries(props.probe.probe?.data ?? {}));
const severity = computed(() => props.probe.probe?.severity ?? '');
const summary = computed(() => props.probe.probe?.summary ?? '');
const inferredFields = computed(() => Object.entries(props.probe.inferred?.fields ?? {}));
const inferredByLabel = computed(() => {
  if (props.probe.inferred_by === 'llm_sample') {
    return 'AI 按真实响应推断';
  }
  if (props.probe.inferred_by === 'rules') {
    return '规则推断（未配 AI 或 AI 失败）';
  }
  return '未推断';
});
const hasInferred = computed(() => Boolean(props.probe.inferred));
</script>

<template>
  <section data-test="three-way-compare" class="three-way">
    <div class="three-way__head">
      <span class="three-way__source" :data-test="`twc-inferred-by-${props.probe.inferred_by}`">{{ inferredByLabel }}</span>
      <span v-if="severity" class="three-way__severity" :data-test="`twc-severity-${severity}`">severity: {{ severity }}</span>
    </div>

    <div class="three-way__grid">
      <div class="three-way__col" data-test="twc-raw">
        <h4>后端原始响应</h4>
        <pre>{{ rawBody || '（无原始响应）' }}</pre>
      </div>

      <div class="three-way__col" data-test="twc-normalized">
        <h4>归一化 data</h4>
        <p v-if="summary" class="three-way__summary" data-test="twc-summary">{{ summary }}</p>
        <table v-if="normalizedRows.length" class="three-way__table">
          <tbody>
            <tr v-for="[name, value] in normalizedRows" :key="name">
              <td class="three-way__key">{{ name }}</td>
              <td><code>{{ String(value) }}</code></td>
            </tr>
          </tbody>
        </table>
        <p v-else-if="hasInferred" class="three-way__sentinel">写操作离线推断，无真实归一化结果</p>
        <p v-else class="three-way__sentinel">无归一化结果</p>
      </div>

      <div class="three-way__col" data-test="twc-inferred">
        <h4>LLM 视角（output 映射）</h4>
        <template v-if="hasInferred">
          <div class="three-way__row"><em>摘要模板</em><code>{{ props.probe.inferred?.summary_template || '—' }}</code></div>
          <div class="three-way__row"><em>严重级别</em><code>{{ props.probe.inferred?.severity_path || '—' }}</code></div>
          <table v-if="inferredFields.length" class="three-way__table">
            <tbody>
              <tr v-for="[name, path] in inferredFields" :key="name">
                <td class="three-way__key">{{ name }}</td>
                <td><code>{{ path }}</code></td>
              </tr>
            </tbody>
          </table>
        </template>
        <p v-else class="three-way__sentinel">无推断映射</p>
      </div>
    </div>
  </section>
</template>

<style scoped>
.three-way { display: flex; flex-direction: column; gap: 10px; }
.three-way__head { display: flex; gap: 12px; align-items: center; font-size: 12px; }
.three-way__source { color: var(--el-color-primary, #409eff); }
.three-way__severity { color: var(--el-color-warning, #e6a23c); }
.three-way__grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 12px; }
.three-way__col { border: 1px solid var(--el-border-color-lighter, #ebeef5); border-radius: 6px; padding: 10px; display: flex; flex-direction: column; gap: 8px; min-width: 0; }
.three-way__col h4 { margin: 0; font-size: 12px; color: var(--el-text-color-secondary, #909399); }
.three-way__col pre { margin: 0; max-height: 260px; overflow: auto; background: var(--el-fill-color-light, #f5f7fa); padding: 8px; border-radius: 4px; font-size: 12px; white-space: pre-wrap; word-break: break-all; }
.three-way__summary { margin: 0; font-size: 13px; }
.three-way__table { width: 100%; border-collapse: collapse; font-size: 13px; }
.three-way__table td { text-align: left; padding: 3px 8px; border-bottom: 1px solid var(--el-border-color-extra-light, #f2f6fc); vertical-align: top; }
.three-way__key { color: var(--el-text-color-secondary, #909399); width: 34%; white-space: nowrap; }
.three-way__row { display: flex; gap: 8px; font-size: 13px; align-items: baseline; }
.three-way__row em { font-style: normal; color: var(--el-text-color-secondary, #909399); min-width: 60px; }
.three-way__sentinel { margin: 0; font-size: 12px; color: var(--el-text-color-secondary, #909399); }
</style>