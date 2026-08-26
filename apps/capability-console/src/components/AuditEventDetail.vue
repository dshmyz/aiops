<script setup lang="ts">
import { computed } from 'vue';
import type { AuditEvent } from '../types';
import { formatAbsoluteTime } from '../conversationFormat';

const props = defineProps<{
  event: AuditEvent | null;
}>();

const emit = defineEmits<{
  close: [];
  jumpToPlan: [planID: string];
}>();

const metadataEntries = computed<Array<[string, unknown]>>(() => {
  if (!props.event?.metadata) return [];
  return Object.entries(props.event.metadata).filter(([, value]) => value !== null && value !== undefined);
});

// Build a Jaeger trace URL for the event's trace_id. The Jaeger base URL is
// configurable via the VITE_JAEGER_URL env var (defaults to localhost:16686)
// so different deployments can point at their own tracing backend.
const traceUrl = computed<string>(() => {
  const traceId = props.event?.trace_id;
  if (!traceId) return '';
  const jaegerBase = import.meta.env.VITE_JAEGER_URL || 'http://localhost:16686';
  return `${jaegerBase}/trace/${traceId}`;
});

function formatValue(value: unknown): string {
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}
</script>

<template>
  <aside v-if="event" class="audit-event-detail" data-test="audit-event-detail">
    <header class="detail-header">
      <div>
        <p class="eyebrow">事件详情</p>
        <h3 class="detail-id">{{ event.id }}</h3>
      </div>
      <button class="mini-button" data-test="audit-detail-close" @click="emit('close')">关闭</button>
    </header>

    <dl class="detail-meta">
      <div>
        <dt>工具</dt>
        <dd class="mono">{{ event.tool_name }}</dd>
      </div>
      <div>
        <dt>操作</dt>
        <dd>{{ event.action }}</dd>
      </div>
      <div>
        <dt>决策</dt>
        <dd :class="['decision', `decision-${event.decision}`]">{{ event.decision }}</dd>
      </div>
      <div>
        <dt>提交人</dt>
        <dd>{{ event.subject }}</dd>
      </div>
      <div>
        <dt>时间</dt>
        <dd class="mono" :title="event.created_at">{{ formatAbsoluteTime(event.created_at) }}</dd>
      </div>
      <div v-if="event.request_id">
        <dt>Request ID</dt>
        <dd class="mono">{{ event.request_id }}</dd>
      </div>
      <div v-if="event.trace_id" data-test="audit-detail-trace">
        <dt>Trace ID</dt>
        <dd class="mono trace-row">
          <span class="trace-id">{{ event.trace_id }}</span>
          <a
            :href="traceUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="trace-link"
            data-test="audit-detail-trace-link"
          >查看 Trace</a>
        </dd>
      </div>
      <div v-if="event.execution_id">
        <dt>Execution</dt>
        <dd class="mono">{{ event.execution_id }}</dd>
      </div>
      <div v-if="event.plan_id">
        <dt>Plan ID</dt>
        <dd class="mono">
          <button class="link-button" data-test="audit-detail-jump-plan" @click="emit('jumpToPlan', event.plan_id)">
            {{ event.plan_id }}
          </button>
        </dd>
      </div>
    </dl>

    <section v-if="metadataEntries.length > 0" class="detail-metadata">
      <h4>Metadata</h4>
      <dl>
        <div v-for="[key, value] in metadataEntries" :key="key">
          <dt>{{ key }}</dt>
          <dd class="mono">{{ formatValue(value) }}</dd>
        </div>
      </dl>
    </section>
    <p v-else class="empty-metadata">无附加 metadata。</p>
  </aside>
</template>

<style scoped>
.audit-event-detail {
  display: flex;
  flex-direction: column;
  gap: 1rem;
  padding: 1rem;
  border: 1px solid var(--color-border);
  border-radius: 12px;
  background: var(--color-surface);
  min-width: 280px;
}

.detail-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 0.5rem;
}

.detail-header .eyebrow {
  margin: 0;
  font-size: 0.7rem;
  color: var(--color-text-muted);
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.detail-id {
  margin: 0.25rem 0 0;
  font-size: 0.95rem;
  font-family: var(--font-mono, monospace);
  word-break: break-all;
  color: var(--color-text);
}

.detail-meta {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 0.6rem 1rem;
  margin: 0;
}

.detail-meta div {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}

dt {
  font-size: 0.7rem;
  color: var(--color-text-muted);
  text-transform: uppercase;
  letter-spacing: 0.06em;
}

dd {
  margin: 0;
  font-size: 0.85rem;
  color: var(--color-text);
  word-break: break-all;
}

.mono {
  font-family: var(--font-mono, monospace);
  font-size: 0.8rem;
}

.decision-permitted {
  color: var(--color-success, #2c8a3e);
}

.decision-denied {
  color: var(--color-danger, #d33);
}

.detail-metadata {
  border-top: 1px solid var(--color-border);
  padding-top: 0.75rem;
}

.detail-metadata h4 {
  margin: 0 0 0.5rem;
  font-size: 0.8rem;
  color: var(--color-text-muted);
  text-transform: uppercase;
  letter-spacing: 0.06em;
}

.detail-metadata dl {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 0.5rem 1rem;
  margin: 0;
}

.detail-metadata div {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}

.empty-metadata {
  margin: 0;
  font-size: 0.8rem;
  color: var(--color-text-muted);
  border-top: 1px solid var(--color-border);
  padding-top: 0.75rem;
}

.link-button {
  background: none;
  border: none;
  padding: 0;
  color: var(--color-primary, #3a8ee6);
  cursor: pointer;
  font: inherit;
  text-decoration: underline;
  word-break: break-all;
}

.link-button:hover {
  text-decoration: none;
}

.trace-row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.trace-id {
  word-break: break-all;
}

.trace-link {
  color: var(--color-primary, #3a8ee6);
  font-size: 0.8rem;
  text-decoration: none;
  white-space: nowrap;
}

.trace-link:hover {
  text-decoration: underline;
}
</style>
