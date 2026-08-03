<script setup lang="ts">
import type { DiagnosticPackage } from '../types';
import MarkdownContent from './MarkdownContent.vue';
import {
  labelForEnvironment,
  labelForRisk,
  labelForSeverity,
  labelForConfidence,
} from '../labels';

defineProps<{
  diagnostic: DiagnosticPackage | null;
}>();
</script>

<template>
  <div v-if="diagnostic" class="diagnostic-view" data-test="diagnostic-view">
    <div class="diagnostic-header">
      <div>
        <span class="badge tag-info">诊断包</span>
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
