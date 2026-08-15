<script setup lang="ts">
import { computed } from 'vue';
import type { DiagnosticPackage } from '../types';
import MarkdownContent from './MarkdownContent.vue';
import {
  labelForEnvironment,
  labelForRisk,
  labelForSeverity,
  labelForConfidence,
} from '../labels';

const props = defineProps<{
  diagnostic: DiagnosticPackage | null;
}>();

// 通用检查框架包（后端 diagnostics 对未接入能力的域返回，Data.framework=true）：
// 不是实测数据，前端要如实标注，避免用户把"未接入"当成正常诊断结果。
const isFramework = computed(() => {
  const obs = props.diagnostic?.observations?.[0];
  return !!(obs && (obs.data as Record<string, unknown> | undefined)?.framework === true);
});
</script>

<template>
  <div v-if="diagnostic" class="diagnostic-view" data-test="diagnostic-view">
    <div class="diagnostic-header">
      <div>
        <span class="badge tag-info">诊断包</span>
        <span v-if="isFramework" class="badge tag-warning" data-test="framework-badge">通用检查框架（非实测数据）</span>
        <h3>{{ diagnostic.id }}</h3>
      </div>
      <small>{{ labelForEnvironment(diagnostic.environment) }}</small>
    </div>

    <section v-if="diagnostic.resources.length > 0">
      <h4>资源</h4>
      <div v-for="resource in diagnostic.resources" :key="resource.id" class="compact-row">
        <strong>{{ resource.name }}</strong>
        <span>{{ resource.domain }}</span>
        <span>{{ resource.type }}</span>
      </div>
    </section>

    <section v-if="diagnostic.observations.length > 0">
      <h4>证据</h4>
      <article
        v-for="observation in diagnostic.observations"
        :key="observation.id"
        class="diagnostic-item"
        :class="observation.severity"
      >
        <span class="diagnostic-severity">{{ labelForSeverity(observation.severity) }}</span>
        <span class="diagnostic-kind">{{ observation.kind }}</span>
        <MarkdownContent :content="observation.summary" />
        <pre v-if="observation.data">{{ JSON.stringify(observation.data, null, 2) }}</pre>
      </article>
    </section>

    <section v-if="diagnostic.findings.length > 0">
      <h4>结论</h4>
      <article
        v-for="finding in diagnostic.findings"
        :key="finding.id"
        class="diagnostic-item"
        :class="finding.severity"
      >
        <span class="diagnostic-confidence">置信度：{{ labelForConfidence(finding.confidence) }}</span>
        <MarkdownContent :content="finding.summary" />
      </article>
    </section>

    <section v-if="diagnostic.recommendations.length > 0">
      <h4>建议</h4>
      <article
        v-for="recommendation in diagnostic.recommendations"
        :key="recommendation.id"
        class="diagnostic-item"
      >
        <span class="diagnostic-risk">风险：{{ labelForRisk(recommendation.risk) }}</span>
        <MarkdownContent :content="recommendation.summary" />
        <small class="diagnostic-rationale">
          <MarkdownContent :content="recommendation.rationale" />
        </small>
      </article>
    </section>
  </div>
  <div v-else class="empty">结构化诊断会显示在这里。</div>
</template>
