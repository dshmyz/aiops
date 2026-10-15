<script setup lang="ts">
import { computed } from 'vue';
import ThreeWayCompare from './ThreeWayCompare.vue';
import type { UseCapabilities } from '../../composables/useCapabilities';

const props = defineProps<{ capabilities: UseCapabilities }>();

const result = computed(() => props.capabilities.generatedUrlResult.value);
const loading = computed(() => props.capabilities.urlImportLoading.value);
</script>

<template>
  <section data-test="url-entry" class="url-entry" aria-label="后端接口地址接入">
    <div class="url-entry__main">
      <label class="url-entry__endpoint">
        <span>后端接口地址</span>
        <input
          data-test="url-endpoint-input"
          :value="capabilities.urlEndpoint.value"
          class="filter-input"
          placeholder="https://middleware.example.com/api/glusterfs/m1/volumes/vol1/status"
          @input="capabilities.urlEndpoint.value = ($event.target as HTMLInputElement).value"
        />
      </label>
      <button
        data-test="url-probe-generate"
        class="url-entry__submit"
        type="button"
        :disabled="!capabilities.urlEndpoint.value.trim() || loading"
        @click="capabilities.runUrlProbeGenerate"
      >
        {{ loading ? '正在连接并验证…' : '接入并验证' }}
      </button>
    </div>

    <div class="url-entry__advanced">
      <label>
        <span>HTTP 方法</span>
        <select
          data-test="url-method-select"
          :value="capabilities.urlMethod.value"
          class="filter-input"
          @change="capabilities.urlMethod.value = ($event.target as HTMLSelectElement).value"
        >
          <option value="GET">GET（只读，真实探测）</option>
          <option value="POST">POST（写入，离线推断）</option>
          <option value="PUT">PUT（写入，离线推断）</option>
        </select>
      </label>
      <label>
        <span>接口说明（可选）</span>
        <input
          data-test="url-description-input"
          :value="capabilities.urlDescription.value"
          class="filter-input"
          placeholder="如：查看卷健康状态"
          @input="capabilities.urlDescription.value = ($event.target as HTMLInputElement).value"
        />
      </label>
      <label v-if="capabilities.urlMethod.value !== 'GET'">
        <span>示例响应体（写入能力离线推断输出映射）</span>
        <textarea
          data-test="url-sample-body-input"
          :value="capabilities.urlSampleBody.value"
          class="filter-input url-entry__sample"
          rows="6"
          placeholder='{"name":"orders","status":"ok"}'
          @input="capabilities.urlSampleBody.value = ($event.target as HTMLTextAreaElement).value"
        />
      </label>
    </div>

    <div v-if="result && capabilities.managementPhase.value === 'source'" data-test="url-generate-result" class="url-entry__result">
      <details data-test="url-three-way" class="url-entry__three-way">
        <summary>查看验证详情</summary>
        <ThreeWayCompare :probe="result.probe" />
      </details>
      <button data-test="url-clear-result" class="url-entry__clear" type="button" @click="capabilities.clearUrlProbeGenerate">
        清除结果
      </button>
    </div>
  </section>
</template>

<style scoped>
.url-entry { display: flex; flex-direction: column; gap: 10px; }
.url-entry__main { display: flex; flex-direction: column; gap: 10px; }
.url-entry__endpoint { flex: 1; display: flex; flex-direction: column; gap: 6px; font-size: 13px; }
.url-entry__main .url-entry__submit { width: 100%; }
.url-entry__endpoint span,
.url-entry__advanced span { color: var(--el-text-color-secondary, #909399); }
.url-entry__advanced { display: flex; gap: 12px; flex-wrap: wrap; }
.url-entry__advanced label { display: flex; flex-direction: column; gap: 6px; font-size: 13px; flex: 1; min-width: 200px; }
.url-entry__sample { resize: vertical; font-family: var(--el-font-family, monospace); }
.url-entry__submit {
  padding: 12px 20px;
  border: 0;
  border-radius: 10px;
  background: var(--gradient-brand);
  color: #fff;
  font: inherit;
  font-size: 14px;
  font-weight: 650;
  cursor: pointer;
  transition: opacity .15s;
}

.url-entry__submit:hover { opacity: .9; }
.url-entry__submit:disabled { opacity: .45; cursor: not-allowed; }

.url-entry__result { display: flex; flex-direction: column; gap: 10px; align-items: flex-start; }
.url-entry__draft { font-size: 14px; }
.url-entry__three-way { width: 100%; }
.url-entry__three-way summary { cursor: pointer; font-size: 13px; color: var(--el-color-primary, #409eff); }
.url-entry__clear { background: none; border: none; padding: 0; font-size: 12px; color: var(--el-text-color-secondary, #909399); cursor: pointer; }
.url-entry__clear:hover { color: var(--el-color-primary, #409eff); }
</style>