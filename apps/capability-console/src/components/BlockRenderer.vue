<script setup lang="ts">
import type { Block, BlockType } from '../types';
import RiskNoticeBlock from './RiskNoticeBlock.vue';

defineProps<{
  blocks: Block[];
}>();

// 按类型返回展示标签
function blockLabel(type: BlockType): string {
  const labels: Record<BlockType, string> = {
    incident_card: '故障摘要',
    evidence_timeline: '证据时间线',
    query_suggestion: '查询建议',
    chart_query: '指标查询',
    alert_rule_draft: '告警规则草稿',
    dashboard_draft: '仪表盘草稿',
    change_candidate: '关联变更',
    rollback_plan: '回滚计划',
    k8s_action: 'K8s 操作建议',
    self_heal_recommendation: '自愈推荐',
    approval_form: '待确认表单',
    tool_trace: '工具追踪',
    risk_notice: '风险提示',
  };
  return labels[type] ?? type;
}

// 安全提取 payload 中的数组字段
function events(block: Block): Array<Record<string, unknown>> {
  const raw = block.payload?.events;
  return Array.isArray(raw) ? (raw as Array<Record<string, unknown>>) : [];
}

function fields(block: Block): Array<Record<string, unknown>> {
  const raw = block.payload?.fields;
  return Array.isArray(raw) ? (raw as Array<Record<string, unknown>>) : [];
}
</script>

<template>
  <section v-if="blocks.length > 0" class="block-renderer" data-test="block-renderer">
    <div
      v-for="(block, idx) in blocks"
      :key="idx"
      :class="['block-card', `block-${block.type}`]"
      :data-test="`block-${block.type}`"
    >
      <header class="block-header">
        <span class="block-tag">{{ blockLabel(block.type) }}</span>
        <h4 v-if="block.title">{{ block.title }}</h4>
      </header>

      <div class="block-body">
        <!-- 通用内容 -->
        <p v-if="block.content && block.type !== 'query_suggestion'" class="block-content">
          {{ block.content }}
        </p>

        <!-- 查询建议：用 code 块展示 -->
        <div v-if="block.type === 'query_suggestion'" class="query-block">
          <code>{{ block.content }}</code>
          <div v-if="block.payload?.language || block.payload?.time_range" class="query-meta">
            <span v-if="block.payload?.language" class="meta-tag">{{ block.payload.language }}</span>
            <span v-if="block.payload?.time_range" class="meta-tag">{{ block.payload.time_range }}</span>
          </div>
        </div>

        <!-- 证据时间线：按事件列表展示 -->
        <ol v-if="block.type === 'evidence_timeline'" class="timeline-list">
          <li v-for="(evt, i) in events(block)" :key="i" class="timeline-item">
            <span class="timeline-time">{{ evt.time }}</span>
            <span class="timeline-type">{{ evt.type }}</span>
            <span class="timeline-desc">{{ evt.description }}</span>
          </li>
        </ol>

        <!-- 待确认表单：按字段列表展示 -->
        <div v-if="block.type === 'approval_form'" class="approval-fields">
          <div v-for="(field, i) in fields(block)" :key="i" class="approval-field">
            <label>
              <span class="field-name">{{ field.name }}</span>
              <span v-if="field.required" class="field-required">*</span>
            </label>
            <span class="field-type">{{ field.type }}</span>
            <span v-if="field.options" class="field-options">
              {{ Array.isArray(field.options) ? field.options.join(' / ') : field.options }}
            </span>
          </div>
        </div>

        <!-- 风险提示：dry-run 预演 + 执行策略（借鉴-3），抽成子组件维护 -->
        <RiskNoticeBlock v-if="block.type === 'risk_notice'" :block="block" />
      </div>
    </div>
  </section>
</template>

<style scoped>
.block-renderer {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.block-card {
  background: var(--color-bg-elevated);
  border-radius: 12px;
  border: 1px solid var(--color-border-subtle);
  padding: 14px 16px;
  box-shadow: var(--shadow-sm);
}

.block-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}

.block-tag {
  font-size: 11px;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 6px;
  background: var(--color-bg-info-subtle, #e8f0fe);
  color: var(--color-text-info, #1a73e8);
}

.block-header h4 {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: var(--color-text-primary);
}

.block-body {
  font-size: 13px;
  color: var(--color-text-secondary);
  line-height: 1.5;
}

.block-content {
  margin: 0;
}

/* 查询建议 */
.query-block {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.query-block code {
  font-family: 'SF Mono', Menlo, monospace;
  font-size: 12px;
  padding: 8px 10px;
  background: var(--color-bg-code, #f5f5f7);
  border-radius: 6px;
  color: var(--color-text-primary);
  word-break: break-all;
}

.query-meta {
  display: flex;
  gap: 6px;
}

.meta-tag {
  font-size: 11px;
  padding: 1px 6px;
  border-radius: 4px;
  background: var(--color-bg-tag, #e8e8ed);
  color: var(--color-text-tertiary);
}

/* 证据时间线 */
.timeline-list {
  list-style: none;
  padding: 0;
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.timeline-item {
  display: grid;
  grid-template-columns: auto auto 1fr;
  gap: 8px;
  align-items: baseline;
  padding: 6px 0;
  border-bottom: 1px solid var(--color-border-subtle);
}

.timeline-item:last-child {
  border-bottom: none;
}

.timeline-time {
  font-size: 11px;
  font-family: 'SF Mono', Menlo, monospace;
  color: var(--color-text-tertiary);
}

.timeline-type {
  font-size: 11px;
  font-weight: 600;
  padding: 1px 6px;
  border-radius: 4px;
  background: var(--color-bg-tag, #e8e8ed);
}

.timeline-desc {
  font-size: 12px;
  color: var(--color-text-secondary);
}

/* 待确认表单 */
.approval-fields {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.approval-field {
  display: flex;
  align-items: baseline;
  gap: 8px;
  flex-wrap: wrap;
}

.field-name {
  font-weight: 600;
  color: var(--color-text-primary);
}

.field-required {
  color: var(--color-text-danger, #dc2626);
}

.field-type {
  font-size: 11px;
  padding: 1px 6px;
  border-radius: 4px;
  background: var(--color-bg-tag, #e8e8ed);
  color: var(--color-text-tertiary);
}

.field-options {
  font-size: 11px;
  color: var(--color-text-tertiary);
}

/* 风险提示 */
.block-risk_notice {
  border-color: var(--color-border-warning, #f59e0b);
}
</style>
