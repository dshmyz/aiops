<script setup lang="ts">
import { computed } from 'vue';
import { ElButton, ElMessage } from 'element-plus';
import type { UseCapabilities } from '../../composables/useCapabilities';

const props = defineProps<{ capabilities: UseCapabilities }>();

const result = computed(() => props.capabilities.probeResult.value);
const loading = computed(() => props.capabilities.probeLoading.value);
const selected = computed(() => props.capabilities.selected.value);

// 读能力都能试调（POST 查询也行，与后端 Test 的治理边界一致）；写入能力不行。
const canProbe = computed(() => {
  const capability = selected.value;
  return Boolean(capability.backend.base_url?.trim()) && capability.operation === 'read';
});
const canProbeHint = computed(() => {
  const capability = selected.value;
  if (!capability.backend.base_url?.trim()) {
    return '先在「编辑 Capability 详情」里填后端 Base URL';
  }
  if (capability.operation !== 'read') {
    return '试调只支持读取类能力；写入能力发布前走审批链路';
  }
  return '';
});
const inferredByLabel = computed(() => {
  if (result.value?.inferred_by === 'llm_sample') {
    return 'AI 按真实响应推断';
  }
  if (result.value?.inferred_by === 'rules') {
    return '规则推断（未配 AI 或 AI 失败）';
  }
  return '';
});
const inferredFields = computed(() => Object.entries(result.value?.inferred?.fields ?? {}));
const probeSummary = computed(() => result.value?.probe?.summary ?? '');

async function runProbe() {
  await props.capabilities.probeSelected();
}

function applyInferred() {
  props.capabilities.applyInferredOutput();
}

function copyRaw() {
  const raw = result.value?.raw_body ?? '';
  if (!raw) {
    return;
  }
  navigator.clipboard?.writeText(raw).catch(() => {
    // 剪贴板不可用时静默失败
  });
  ElMessage.success('已复制响应体');
}
</script>

<template>
  <section class="review-block" data-test="probe-panel">
    <div class="review-block-title">
      <h3>试调真实接口</h3>
      <span>调一次真实后端，AI 按真实响应配置输出映射</span>
    </div>
    <template v-if="canProbe">
      <div class="probe-actions">
        <el-button data-test="probe-run" type="warning" :loading="loading" @click="runProbe">
          {{ loading ? '正在试调…' : '试调真实接口' }}
        </el-button>
        <span class="probe-hint">用下方「测试参数」发起真实请求，验证接口能不能出数据</span>
      </div>
      <template v-if="result">
        <div v-if="result.warnings?.length" data-test="probe-warnings" class="probe-warnings">
          <div v-for="warning in result.warnings" :key="warning" class="probe-warning-row">⚠ {{ warning }}</div>
        </div>
        <div v-if="result.probe" class="probe-summary" data-test="probe-summary">
          <strong>试调成功</strong>
          <span>{{ probeSummary }}</span>
        </div>
        <div v-if="result.inferred" class="probe-inferred" data-test="probe-inferred">
          <div class="probe-inferred__head">
            <span class="probe-inferred__source" :data-test="`probe-source-${result.inferred_by}`">{{ inferredByLabel }}</span>
            <el-button data-test="probe-apply" size="small" type="primary" @click="applyInferred">应用到草稿</el-button>
          </div>
          <div v-if="result.inferred.summary_template" class="probe-inferred__row">
            <em>摘要模板</em><code>{{ result.inferred.summary_template }}</code>
          </div>
          <div v-if="result.inferred.severity_path" class="probe-inferred__row">
            <em>严重级别</em><code>{{ result.inferred.severity_path }}</code>
          </div>
          <table v-if="inferredFields.length" class="probe-inferred__fields">
            <thead><tr><th>字段</th><th>取值路径</th></tr></thead>
            <tbody>
              <tr v-for="[name, path] in inferredFields" :key="name">
                <td>{{ name }}</td><td><code>{{ path }}</code></td>
              </tr>
            </tbody>
          </table>
        </div>
        <details v-if="result.raw_body" class="probe-raw">
          <summary>查看真实响应体</summary>
          <pre data-test="probe-raw-body">{{ result.raw_body }}</pre>
          <el-button size="small" @click="copyRaw">复制</el-button>
        </details>
      </template>
    </template>
    <p v-else class="probe-hint">{{ canProbeHint }}</p>
  </section>
</template>

<style scoped>
.review-block { display: flex; flex-direction: column; gap: 12px; }
.probe-actions { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
.probe-hint { font-size: 12px; color: var(--el-text-color-secondary, #909399); }
.probe-warnings { display: flex; flex-direction: column; gap: 4px; }
.probe-warning-row { font-size: 13px; color: var(--el-color-danger, #f56c6c); }
.probe-summary { display: flex; gap: 8px; align-items: baseline; font-size: 13px; }
.probe-summary strong { color: var(--el-color-success, #67c23a); }
.probe-inferred { border: 1px solid var(--el-border-color-lighter, #ebeef5); border-radius: 6px; padding: 10px 12px; display: flex; flex-direction: column; gap: 8px; }
.probe-inferred__head { display: flex; justify-content: space-between; align-items: center; }
.probe-inferred__source { font-size: 12px; color: var(--el-color-primary, #409eff); }
.probe-inferred__row { display: flex; gap: 8px; font-size: 13px; align-items: baseline; }
.probe-inferred__row em { font-style: normal; color: var(--el-text-color-secondary, #909399); min-width: 60px; }
.probe-inferred__fields { width: 100%; border-collapse: collapse; font-size: 13px; }
.probe-inferred__fields th, .probe-inferred__fields td { text-align: left; padding: 4px 8px; border-bottom: 1px solid var(--el-border-color-extra-light, #f2f6fc); }
.probe-raw pre { max-height: 240px; overflow: auto; background: var(--el-fill-color-light, #f5f7fa); padding: 8px; border-radius: 4px; font-size: 12px; }
</style>
