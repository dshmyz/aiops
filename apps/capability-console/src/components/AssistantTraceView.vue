<script setup lang="ts">
import type { AssistantTrace } from '../types';
import MarkdownContent from './MarkdownContent.vue';

defineProps<{
  trace: AssistantTrace | null | undefined;
}>();
</script>

<template>
  <section
    v-if="trace && (trace.selection || trace.tool_invocation)"
    class="assistant-trace"
    data-test="assistant-trace"
  >
    <header class="assistant-trace-header">
      <span class="badge tag-info">调用链</span>
      <h3>AI 调用链</h3>
      <span class="assistant-trace-hint">展示 AI 如何选择能力并执行</span>
    </header>

    <section v-if="trace.selection" data-test="assistant-trace-selection" class="trace-block">
      <h4>能力选择</h4>
      <dl class="trace-summary">
        <div v-if="trace.selection.selected">
          <dt>选中能力</dt>
          <dd>{{ trace.selection.selected }}</dd>
        </div>
        <div v-if="trace.selection.confidence !== undefined">
          <dt>置信度</dt>
          <dd>{{ Math.round(trace.selection.confidence * 100) }}%</dd>
        </div>
        <div v-if="trace.selection.reason">
          <dt>理由</dt>
          <dd><MarkdownContent :content="trace.selection.reason" /></dd>
        </div>
      </dl>

      <div
        v-if="trace.selection.candidates && trace.selection.candidates.length > 0"
        class="candidate-list"
        data-test="assistant-trace-candidates"
      >
        <h5>候选能力（按 score 降序）</h5>
        <div
          v-for="candidate in trace.selection.candidates"
          :key="candidate.name"
          class="candidate-row"
          :class="{ winner: candidate.name === trace.selection?.selected }"
          :data-test="`assistant-trace-candidate-${candidate.name}`"
        >
          <div class="candidate-head">
            <strong>{{ candidate.name }}</strong>
            <span class="score">score {{ candidate.score }}</span>
            <span v-if="candidate.name === trace.selection?.selected" class="winner-tag">已选中</span>
          </div>
          <ul v-if="candidate.reasons && candidate.reasons.length > 0" class="reason-list">
            <li v-for="(reason, index) in candidate.reasons" :key="index">{{ reason }}</li>
          </ul>
        </div>
      </div>

      <div
        v-if="trace.selection.missing && trace.selection.missing.length > 0"
        class="missing-fields"
        data-test="assistant-trace-missing"
      >
        <strong>缺失字段：</strong>
        <span v-for="field in trace.selection.missing" :key="field" class="missing-pill">{{ field }}</span>
      </div>

      <div
        v-if="trace.selection.extracted && trace.selection.extracted.length > 0"
        class="extracted-list"
        data-test="assistant-trace-extracted"
      >
        <h5>参数提取</h5>
        <div
          v-for="param in trace.selection.extracted"
          :key="param.name"
          class="extracted-row"
          :data-test="`assistant-trace-param-${param.name}`"
        >
          <span class="param-name">{{ param.name }}</span>
          <span class="param-value">{{ param.value === null || param.value === undefined ? 'null' : String(param.value) }}</span>
          <span class="param-source">{{ param.source }}</span>
        </div>
      </div>
    </section>

    <section v-if="trace.tool_invocation" data-test="assistant-trace-invocation" class="trace-block">
      <h4>能力调用</h4>
      <dl class="trace-summary">
        <div>
          <dt>工具</dt>
          <dd>{{ trace.tool_invocation.tool }}</dd>
        </div>
      </dl>
      <div v-if="trace.tool_invocation.input" class="invocation-block">
        <h5>输入参数</h5>
        <pre>{{ JSON.stringify(trace.tool_invocation.input, null, 2) }}</pre>
      </div>
      <div
        v-if="trace.tool_invocation.raw_response"
        class="invocation-block"
        data-test="assistant-trace-raw-response"
      >
        <h5>原始响应</h5>
        <pre>{{ JSON.stringify(trace.tool_invocation.raw_response, null, 2) }}</pre>
      </div>
    </section>
  </section>
</template>
