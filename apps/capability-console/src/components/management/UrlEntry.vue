<script setup lang="ts">
import { computed } from 'vue';
import ThreeWayCompare from './ThreeWayCompare.vue';
import type { UseCapabilities } from '../../composables/useCapabilities';

const props = defineProps<{ capabilities: UseCapabilities }>();

const result = computed(() => props.capabilities.generatedUrlResult.value);
const loading = computed(() => props.capabilities.urlImportLoading.value);
const pathVariables = computed(() => props.capabilities.urlPathVariables.value);

function addQueryParam() {
  props.capabilities.urlQueryParams.value = [
    ...props.capabilities.urlQueryParams.value,
    { key: '', value: '', required: false },
  ];
}

function removeQueryParam(index: number) {
  props.capabilities.urlQueryParams.value = props.capabilities.urlQueryParams.value.filter((_, i) => i !== index);
}
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
          placeholder="https://backend.example.com/api/instances/{instance_id}/metrics"
          @input="capabilities.urlEndpoint.value = ($event.target as HTMLInputElement).value"
        />
        <small class="url-entry__hint">路径参数用 {名称} 标注，例如 /api/instances/{instance_id}/metrics；查询参数可以直接写在 ? 后面，也可以在下方逐行添加</small>
      </label>

      <div v-if="pathVariables.length > 0" class="url-entry__params">
        <div class="url-entry__params-title">路径参数（试调用，成功后作为示例值保存）</div>
        <label v-for="name in pathVariables" :key="name" class="url-entry__param-row">
          <span data-test="url-path-var-name">{{ name }}</span>
          <input
            :data-test="`url-path-value-${name}`"
            :value="capabilities.urlPathValues.value[name] ?? ''"
            class="filter-input"
            :placeholder="`${name} 的真实值`"
            @input="capabilities.urlPathValues.value = { ...capabilities.urlPathValues.value, [name]: ($event.target as HTMLInputElement).value }"
          />
        </label>
      </div>

      <div class="url-entry__params">
        <div class="url-entry__params-title">
          查询参数（可选，作为 AI 可填的输入参数）
          <button type="button" class="url-entry__add" data-test="url-add-query" @click="addQueryParam">+ 添加</button>
        </div>
        <div v-for="(row, index) in capabilities.urlQueryParams.value" :key="index" class="url-entry__query-row" :data-test="`url-query-row-${index}`">
          <input
            :data-test="`url-query-key-${index}`"
            :value="row.key"
            class="filter-input"
            placeholder="参数名，如 limit"
            @input="capabilities.urlQueryParams.value[index] = { ...row, key: ($event.target as HTMLInputElement).value }"
          />
          <input
            :data-test="`url-query-value-${index}`"
            :value="row.value"
            class="filter-input"
            placeholder="试调值，如 10"
            @input="capabilities.urlQueryParams.value[index] = { ...row, value: ($event.target as HTMLInputElement).value }"
          />
          <label class="url-entry__required">
            <input type="checkbox" :checked="row.required" @change="capabilities.urlQueryParams.value[index] = { ...row, required: ($event.target as HTMLInputElement).checked }" />
            必填
          </label>
          <button type="button" class="url-entry__clear" :data-test="`url-query-remove-${index}`" @click="removeQueryParam(index)">删除</button>
        </div>
      </div>

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
        <span>接口说明（告诉 AI 这个工具是干嘛的、何时调用）</span>
        <input
          data-test="url-description-input"
          :value="capabilities.urlDescription.value"
          class="filter-input"
          placeholder="如：查看指定实例的监控指标"
          @input="capabilities.urlDescription.value = ($event.target as HTMLInputElement).value"
        />
      </label>
      <label v-if="capabilities.urlMethod.value !== 'GET'">
        <span>返回示例 JSON（必填：贴一个接口正常返回的例子，系统据此识别返回字段，不会真调后端）</span>
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
      <ul v-if="capabilities.urlProbeWarnings.value.length > 0" data-test="url-probe-warnings" class="url-entry__warnings">
        <li v-for="(warning, index) in capabilities.urlProbeWarnings.value" :key="`${index}-${warning}`">{{ warning }}</li>
      </ul>
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
.url-entry__hint { color: var(--el-text-color-secondary, #909399); font-size: 12px; line-height: 1.5; }
.url-entry__endpoint span,
.url-entry__advanced span { color: var(--el-text-color-secondary, #909399); }
.url-entry__params { display: flex; flex-direction: column; gap: 6px; }
.url-entry__params-title { display: flex; justify-content: space-between; align-items: center; font-size: 12px; color: var(--el-text-color-secondary, #909399); }
.url-entry__params-title .url-entry__add { border: 0; background: none; color: var(--el-color-primary, #409eff); cursor: pointer; font: inherit; font-size: 12px; padding: 0; }
.url-entry__param-row { display: flex; align-items: center; gap: 8px; font-size: 13px; }
.url-entry__param-row > span { min-width: 120px; font-family: var(--el-font-family, monospace); color: var(--el-text-color-primary, #303133); }
.url-entry__query-row { display: flex; align-items: center; gap: 8px; }
.url-entry__query-row .filter-input { flex: 1; }
.url-entry__required { display: inline-flex; align-items: center; gap: 4px; font-size: 12px; color: var(--el-text-color-secondary, #909399); white-space: nowrap; }
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
.url-entry__warnings { margin: 0; padding: 10px 14px; width: 100%; box-sizing: border-box; list-style: none; border: 1px solid var(--el-color-warning, #e6a23c); border-radius: 8px; background: var(--el-color-warning-light-9, #fdf6ec); color: var(--el-color-warning-dark-2, #b88230); font-size: 13px; }
.url-entry__warnings li + li { margin-top: 4px; }
.url-entry__draft { font-size: 14px; }
.url-entry__three-way { width: 100%; }
.url-entry__three-way summary { cursor: pointer; font-size: 13px; color: var(--el-color-primary, #409eff); }
.url-entry__clear { background: none; border: none; padding: 0; font-size: 12px; color: var(--el-text-color-secondary, #909399); cursor: pointer; }
.url-entry__clear:hover { color: var(--el-color-primary, #409eff); }
</style>