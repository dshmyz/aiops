<script setup lang="ts">
import { computed, ref } from 'vue';
import { ElButton } from 'element-plus';
import type { UseCapabilities } from '../../composables/useCapabilities';

const props = defineProps<{ capabilities: UseCapabilities }>();

// 三张确认卡：工具说明 → 验证 → 发布。用户用业务语言确认，技术细节
// （JSONPath、输出映射）收进“高级详情”，不再是评审主内容。

const toolName = computed({
  get: () => props.capabilities.selected.value.name,
  set: (value: string) => {
    props.capabilities.selected.value.name = value.trim();
  },
});

const toolDescription = computed({
  get: () => props.capabilities.selected.value.ai.description,
  set: (value: string) => {
    props.capabilities.selected.value.ai.description = value;
  },
});

const verifyQuestion = ref('');
const verifyRan = ref(false);

// 写能力（POST/PUT/PATCH/DELETE）禁止试调（治理边界）：验证卡不发起真实调用，
// 改为粘贴示例响应体做离线推断，见后端 ProbeAndInfer 的写分支。
const isWrite = computed(() => props.capabilities.selected.value.operation === 'write');

const params = computed(() =>
  props.capabilities.testInputRows.value.map((row) => ({
    name: row.name,
    type: row.type,
    required: row.required,
    example: row.examples?.[0] ?? '',
    value: props.capabilities.testInputFieldValue(row.name),
  })),
);

function setParam(name: string, value: string) {
  props.capabilities.setTestInputField(name, value, 'string');
}

async function runVerify() {
  await props.capabilities.probeSelected();
  verifyRan.value = true;
}

const verifyOk = computed(() => Boolean(props.capabilities.probeResult.value?.probe));

const publishReady = computed(() => props.capabilities.publishReady.value);

const publishBlockedReason = computed(() => {
  if (!verifyRan.value) {
    return isWrite.value
      ? '先粘贴一次真实调用的示例响应体，校验输出映射'
      : '先完成验证，确认接口能返回数据';
  }
  if (!verifyOk.value) {
    return isWrite.value
      ? '示例响应体推断未通过，请检查是否为合法 JSON 且包含结果字段'
      : '验证未通过，先修正参数或地址再发布';
  }
  if (!publishReady.value) {
    return isWrite.value
      ? '发布检查表还有未通过项（写入能力需补齐治理配置：执行计划、审批、预检、回滚）'
      : '工具说明还不完整，先补齐名称和描述';
  }
  return '';
});

const verifySummaryLabel = computed(() => {
  if (!verifyRan.value) {
    return '未验证';
  }
  if (!verifyOk.value) {
    return '未通过';
  }
  return isWrite.value ? '已通过（样例推断）' : '已通过';
});

// 写能力验证卡的示例响应体，读写到编辑器共享状态（probeSelected 会带上）。
const sampleBody = computed({
  get: () => props.capabilities.testSampleBody.value,
  set: (value: string) => {
    props.capabilities.testSampleBody.value = value;
  },
});

async function publish() {
  await props.capabilities.publishCurrent();
}
</script>

<template>
  <div class="confirm-cards">
    <section data-test="confirm-card-tool" class="confirm-card">
      <header class="confirm-card__head">
        <span class="confirm-card__step">1</span>
        <h3>这个工具叫什么、做什么</h3>
      </header>
      <div class="confirm-card__body">
        <label class="confirm-card__field">
          <span>工具名称</span>
          <input data-test="confirm-card-tool-name" v-model="toolName" class="filter-input" />
        </label>
        <label class="confirm-card__field">
          <span>AI 什么时候使用它（用途说明）</span>
          <textarea data-test="confirm-card-tool-desc" v-model="toolDescription" rows="2" class="filter-input" placeholder="例如：当用户询问 MinIO 桶容量、使用率时使用" />
        </label>
        <p class="confirm-card__meta">
          接口：{{ props.capabilities.selected.value.backend.method }} {{ props.capabilities.selected.value.backend.path }}
        </p>
      </div>
    </section>

    <section data-test="confirm-card-verify" class="confirm-card">
      <header class="confirm-card__head">
        <span class="confirm-card__step">2</span>
        <h3>{{ isWrite ? '用示例响应体确认输出映射' : '用一个问题验证它能拿到数据' }}</h3>
      </header>
      <div class="confirm-card__body">
        <template v-if="isWrite">
          <p class="confirm-card__hint">
            变更类接口不发起试调（避免产生真实变更）。粘贴一次真实调用的示例响应体，
            AI 会用它推断输出映射；发布后执行仍走「计划 → 审批」流程。
          </p>
          <label class="confirm-card__field">
            <span>示例响应体（JSON）</span>
            <textarea
              data-test="verify-sample-body"
              v-model="sampleBody"
              rows="6"
              class="filter-input filter-input--mono"
              placeholder='例如：{"code":0,"data":{"topic":"orders","status":"ok"}}'
            />
          </label>
        </template>
        <template v-else>
          <label class="confirm-card__field">
            <span>你会怎么问 AI？</span>
            <input data-test="verify-question-input" v-model="verifyQuestion" class="filter-input" placeholder="例如：archive 桶现在用了多少空间？" />
          </label>
          <div v-if="params.length > 0" class="confirm-card__params">
            <p class="confirm-card__hint">验证时使用的资源（已从你的输入里带出，可修改）：</p>
            <label v-for="param in params" :key="param.name" class="confirm-card__param">
              <span>{{ param.name }}<small v-if="param.example">，例如 {{ param.example }}</small></span>
              <input :data-test="`verify-param-${param.name}`" class="filter-input" :value="String(param.value ?? '')" :placeholder="param.example || `填写 ${param.name}`" @input="setParam(param.name, ($event.target as HTMLInputElement).value)" />
            </label>
          </div>
        </template>
        <div class="confirm-card__actions">
          <el-button data-test="verify-run" type="primary" :loading="props.capabilities.probeLoading.value" @click="runVerify">
            {{ props.capabilities.probeLoading.value ? '正在验证…' : '开始验证' }}
          </el-button>
        </div>
        <div v-if="verifyRan" data-test="verify-result" class="confirm-card__result" :class="verifyOk ? 'is-ok' : 'is-bad'">
          <template v-if="verifyOk">
            <strong>{{ isWrite ? '样例推断通过' : '验证通过' }}</strong>
            <span>{{ props.capabilities.probeResult.value?.probe?.summary || (isWrite ? '已从示例响应体推断出输出映射' : '') }}</span>
          </template>
          <template v-else>
            <strong>验证未通过</strong>
            <span>{{ props.capabilities.error.value || (isWrite ? '示例响应体推断未通过，请检查 JSON 格式' : '接口没有返回可用的数据，请检查参数或地址') }}</span>
          </template>
        </div>
      </div>
    </section>

    <section data-test="confirm-card-publish" class="confirm-card">
      <header class="confirm-card__head">
        <span class="confirm-card__step">3</span>
        <h3>发布给 AI</h3>
      </header>
      <div class="confirm-card__body">
        <ul class="confirm-card__summary">
          <li><span>名称</span><strong>{{ props.capabilities.selected.value.name }}</strong></li>
          <li><span>接口</span><strong>{{ props.capabilities.selected.value.backend.method }} {{ props.capabilities.selected.value.backend.path }}</strong></li>
          <li><span>验证</span><strong>{{ verifySummaryLabel }}</strong></li>
        </ul>
        <p v-if="publishBlockedReason" data-test="publish-blocked-reason" class="confirm-card__blocked">{{ publishBlockedReason }}</p>
        <el-button data-test="publish-confirm" type="primary" :disabled="!publishReady || !verifyRan || !verifyOk" :loading="props.capabilities.enrichLoading.value" @click="publish">
          发布给 AI
        </el-button>
        <details data-test="confirm-card-advanced" class="confirm-card__advanced">
          <summary>高级详情（参数定义、发布检查）</summary>
          <slot />
        </details>
      </div>
    </section>
  </div>
</template>

<style scoped>
/* 工单 hairline 语言：无阴影、无圆角卡壳，编号章节用 hairline 分隔，
   与 WorkOrderHeader / WorkflowSpine 同一套视觉词汇。 */
.confirm-cards { display: flex; flex-direction: column; }
.confirm-card { border: none; border-bottom: 1px solid var(--color-border); background: none; box-shadow: none; overflow: visible; }
.confirm-card:last-child { border-bottom: none; }
.confirm-card__head { display: flex; align-items: baseline; gap: 12px; padding: 18px 0 10px; border-bottom: none; }
.confirm-card__step { display: inline-block; width: auto; height: auto; border-radius: 0; background: none; color: var(--view-accent, var(--color-text-primary)); font-weight: 800; font-size: 14px; font-family: var(--font-mono, monospace); }
.confirm-card__head h3 { margin: 0; font-size: 16px; font-weight: 650; letter-spacing: -0.01em; color: var(--color-text-primary); }
.confirm-card__body { display: flex; flex-direction: column; gap: 12px; padding: 2px 0 22px 26px; }
.confirm-card__field { display: flex; flex-direction: column; gap: 6px; }
.confirm-card__field span { font-size: 13px; color: var(--color-text-secondary); }
.confirm-card__field textarea { resize: vertical; padding: 10px 12px; border: 1px solid var(--color-border-strong); border-radius: var(--radius-md); font: inherit; background: var(--color-bg); color: var(--color-text-primary); }
.confirm-card__meta { margin: 0; font-size: 12px; color: var(--color-text-tertiary); font-family: var(--font-mono, monospace); overflow-wrap: anywhere; }
.confirm-card__params { display: flex; flex-direction: column; gap: 10px; }
.confirm-card__hint { margin: 0; font-size: 13px; color: var(--color-text-secondary); }
.confirm-card__param { display: flex; flex-direction: column; gap: 4px; }
.confirm-card__param span { font-size: 13px; color: var(--color-text-primary); }
.confirm-card__param small { color: var(--color-text-tertiary); }
.confirm-card__actions { display: flex; gap: 10px; }
.confirm-card__result { display: flex; flex-direction: column; gap: 4px; padding: 12px 14px; border-left: 2px solid currentColor; border-radius: 0; background: var(--color-bg-elevated); }
.confirm-card__result.is-ok { background: var(--color-success-soft, #e8f8ee); color: var(--color-success, #1a7f37); }
.confirm-card__result.is-bad { background: var(--color-danger-soft, #fdecec); color: var(--color-danger, #c62828); }
.confirm-card__summary { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
.confirm-card__summary li { display: flex; gap: 12px; font-size: 14px; }
.confirm-card__summary span { width: 42px; flex-shrink: 0; color: var(--color-text-tertiary); }
.confirm-card__summary strong { color: var(--color-text-primary); font-weight: 600; overflow-wrap: anywhere; }
.confirm-card__blocked { margin: 0; font-size: 13px; color: var(--color-text-tertiary); }
.confirm-card__advanced summary { cursor: pointer; font-size: 13px; color: var(--color-text-secondary); }
</style>
