<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import type { AlertIncident } from '../types';
import { useIncidents } from '../composables/useIncidents';
import { formatCompactDateTime } from '../conversationFormat';
import ViewShell from '../components/ViewShell.vue';

const {
  incidents,
  incidentsLoading,
  incidentsError,
  statusFilter,
  domainFilter,
  refresh,
  selectedIncident,
  memberAlerts,
  detailLoading,
  detailError,
  select,
  clearSelection,
} = useIncidents();

onMounted(() => {
  void refresh();
});

type StatusMode = 'firing' | 'resolved' | '';
const statusFilters: { value: StatusMode; label: string }[] = [
  { value: 'firing', label: '进行中' },
  { value: 'resolved', label: '已恢复' },
  { value: '', label: '全部' },
];

function applyStatus(mode: StatusMode) {
  statusFilter.value = mode;
  clearSelection();
  void refresh();
}

function applyDomain() {
  clearSelection();
  void refresh();
}

const firingCount = computed(() => incidents.value.filter((i) => i.status === 'firing').length);
const resolvedCount = computed(() => incidents.value.filter((i) => i.status === 'resolved').length);

const severityLabel: Record<string, string> = {
  critical: '严重',
  warning: '警告',
  info: '提示',
};

const severityTone: Record<string, string> = {
  critical: 'danger',
  warning: 'warn',
  info: 'ok',
};

function severityClass(severity: string): string {
  return severityTone[severity] ?? 'ok';
}

function statusClass(status: string): string {
  return status === 'firing' ? 'danger' : 'ok';
}

const statusLabel: Record<string, string> = {
  firing: '进行中',
  resolved: '已恢复',
};

function fmtTs(ts: string | null | undefined): string {
  if (!ts) return '';
  return formatCompactDateTime(ts, false);
}

function isSelected(incident: AlertIncident): boolean {
  return selectedIncident.value?.id === incident.id;
}

// 选中项被列表刷新替换后，详情引用可能与列表脱节：以列表内最新对象为准。
const currentIncident = computed<AlertIncident | null>(() => {
  if (!selectedIncident.value) return null;
  return incidents.value.find((i) => i.id === selectedIncident.value?.id) ?? selectedIncident.value;
});
</script>

<template>
  <ViewShell
    class="incidents-entry"
    data-test="incidents-entry"
    data-view="incidents"
    eyebrow="Alert Incidents"
    title="告警关联"
    copy="同一资源在时间窗内的重复告警归并为一个 incident：一次故障一条记录，只有首条触发自动研判，恢复后自动关闭。"
  >
    <template #actions>
      <button class="mini-button" :disabled="incidentsLoading" data-test="incidents-refresh" @click="refresh">
        {{ incidentsLoading ? '刷新中' : '刷新' }}
      </button>
    </template>

    <p v-if="incidentsError" class="error-text">{{ incidentsError }}</p>

    <div class="incidents-workspace">
      <div class="incidents-list">
        <div class="incidents-toolbar">
          <div class="filter-chips" role="group" aria-label="按状态筛选">
            <button
              v-for="f in statusFilters"
              :key="f.value || 'all'"
              type="button"
              class="filter-chip"
              :class="{ active: statusFilter === f.value }"
              :data-test="`incidents-filter-${f.value || 'all'}`"
              @click="applyStatus(f.value)"
            >
              {{ f.label }}
              <span v-if="f.value === 'firing' && firingCount > 0" class="chip-count">{{ firingCount }}</span>
              <span v-else-if="f.value === 'resolved' && resolvedCount > 0" class="chip-count">{{ resolvedCount }}</span>
            </button>
          </div>
          <input
            v-model="domainFilter"
            class="domain-input"
            type="search"
            placeholder="按域筛选（回车）"
            data-test="incidents-domain-input"
            @keydown.enter="applyDomain"
            @search="applyDomain"
          />
        </div>

        <div v-if="incidentsLoading" class="list-summary">
          <div class="skeleton skeleton-line"></div>
          <div class="skeleton skeleton-line short"></div>
        </div>
        <div v-else-if="incidents.length === 0 && !incidentsError" class="empty">
          暂无 {{ statusFilter === '' ? '' : statusFilter === 'firing' ? '进行中的 ' : '已恢复的 ' }}incident。
        </div>

        <div
          v-for="incident in incidents"
          :key="incident.id"
          class="incident-card"
          :class="{ active: isSelected(incident), 'is-firing': incident.status === 'firing' }"
          :data-test="`incident-${incident.id}`"
          @click="select(incident)"
        >
          <header class="incident-card-header">
            <span class="sev-badge" :class="`sev-${severityClass(incident.severity)}`">
              {{ severityLabel[incident.severity] ?? incident.severity }}
            </span>
            <span class="incident-title" :title="incident.title">{{ incident.title }}</span>
          </header>
          <div class="incident-meta">
            <span class="mono" :title="`${incident.domain} / ${incident.resource_type} / ${incident.resource_name}`">
              {{ incident.domain }}<template v-if="incident.resource_name"> · {{ incident.resource_name }}</template>
            </span>
            <span class="count-chip">{{ incident.alert_count }} 条告警</span>
          </div>
          <footer class="incident-card-footer">
            <span class="status-chip" :class="`status-${statusClass(incident.status)}`">
              {{ statusLabel[incident.status] ?? incident.status }}
            </span>
            <span class="incident-time mono" :title="incident.last_seen_at">
              {{ fmtTs(incident.first_seen_at) }} → {{ fmtTs(incident.last_seen_at) }}
            </span>
          </footer>
        </div>
      </div>

      <aside class="incidents-detail">
        <div v-if="!currentIncident" class="empty">点击左侧某个 incident 查看成员告警。</div>
        <div v-else-if="detailLoading" class="list-summary">
          <div class="skeleton skeleton-line"></div>
        </div>
        <div v-else-if="detailError" class="error-text">{{ detailError }}</div>
        <div v-else class="incidents-detail-body" data-test="incidents-detail">
          <header class="detail-title">
            <h3>{{ currentIncident.title }}</h3>
            <button class="mini-button" data-test="incidents-close-detail" @click="clearSelection">关闭</button>
          </header>

          <section class="detail-summary">
            <div class="detail-summary-head">
              <span class="sev-badge" :class="`sev-${severityClass(currentIncident.severity)}`">
                {{ severityLabel[currentIncident.severity] ?? currentIncident.severity }}
              </span>
              <span class="status-chip" :class="`status-${statusClass(currentIncident.status)}`">
                {{ statusLabel[currentIncident.status] ?? currentIncident.status }}
              </span>
              <span class="count-chip">{{ currentIncident.alert_count }} 条告警</span>
            </div>
            <dl>
              <div><dt>关联键</dt><dd class="mono">{{ currentIncident.domain }} / {{ currentIncident.resource_type || '-' }} / {{ currentIncident.resource_name || '-' }}</dd></div>
              <div><dt>首次出现</dt><dd class="mono" :title="currentIncident.first_seen_at">{{ fmtTs(currentIncident.first_seen_at) }}</dd></div>
              <div><dt>最近活跃</dt><dd class="mono" :title="currentIncident.last_seen_at">{{ fmtTs(currentIncident.last_seen_at) }}</dd></div>
            </dl>
          </section>

          <section class="detail-block">
            <h4>成员告警（{{ memberAlerts.length }}）</h4>
            <div v-if="memberAlerts.length === 0" class="empty">成员告警可能已被清理。</div>
            <ul class="member-list">
              <li v-for="member in memberAlerts" :key="member.id" class="member-item">
                <div class="member-head">
                  <span class="sev-badge" :class="`sev-${severityClass(member.severity)}`">
                    {{ severityLabel[member.severity] ?? member.severity }}
                  </span>
                  <span class="member-title" :title="member.title">{{ member.title }}</span>
                  <span class="status-chip" :class="`status-${statusClass(member.status)}`">
                    {{ statusLabel[member.status] ?? member.status }}
                  </span>
                </div>
                <p v-if="member.description" class="member-desc" :title="member.description">{{ member.description }}</p>
                <p class="member-meta mono">
                  {{ member.source }} · {{ member.external_id }}
                  <template v-if="member.fired_at"> · 触发 {{ fmtTs(member.fired_at) }}</template>
                  <template v-if="member.resolved_at"> · 恢复 {{ fmtTs(member.resolved_at) }}</template>
                </p>
              </li>
            </ul>
          </section>
        </div>
      </aside>
    </div>
  </ViewShell>
</template>

<style scoped>
.incidents-workspace {
  display: grid;
  grid-template-columns: minmax(300px, 420px) minmax(0, 1fr);
  gap: var(--space-4);
  align-items: start;
  flex: 1;
  min-height: 0;
}

@media (max-width: 1100px) {
  .incidents-workspace {
    grid-template-columns: 1fr;
  }
}

.incidents-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  overflow-y: auto;
  max-height: calc(100vh - 260px);
}

.incidents-toolbar {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: 0 2px;
}

.domain-input {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-bg-elevated);
  color: var(--color-text-primary);
  padding: 6px 10px;
  font-size: var(--font-sm);
}
.domain-input:focus {
  outline: none;
  border-color: var(--color-accent);
}

.filter-chips {
  display: flex;
  gap: var(--space-2);
}
.filter-chip {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-pill);
  background: var(--color-bg-elevated);
  color: var(--color-text-secondary);
  font-size: var(--font-xs);
  font-weight: 600;
  padding: 2px 12px;
  cursor: pointer;
  transition: border-color 0.15s var(--ease-out), color 0.15s var(--ease-out), background 0.15s var(--ease-out);
}
.filter-chip:hover { border-color: var(--color-accent); color: var(--color-accent); }
.filter-chip.active { background: var(--color-accent); border-color: var(--color-accent); color: var(--color-bg); }
.chip-count {
  margin-left: 4px;
  font-size: var(--font-xs);
  opacity: 0.8;
}

.incident-card {
  padding: var(--space-3) var(--space-4);
  background: var(--color-bg-elevated);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-sm);
  cursor: pointer;
  transition: border-color 0.15s var(--ease-out), background 0.15s var(--ease-out);
}
.incident-card.is-firing {
  border-left: 3px solid var(--color-danger);
}
.incident-card:hover { background: var(--color-bg-hover); }
.incident-card.active {
  border-color: var(--color-accent);
  background: var(--color-bg-active);
}

.incident-card-header {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: 4px;
  min-width: 0;
}
.incident-title {
  font-size: var(--font-sm);
  font-weight: 600;
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
  min-width: 0;
}

.sev-badge {
  padding: 1px 8px;
  border-radius: var(--radius-pill);
  font-size: var(--font-xs);
  font-weight: 600;
  white-space: nowrap;
}
.sev-danger { background: var(--color-danger-soft); color: var(--color-danger); }
.sev-warn { background: var(--color-warning-soft); color: var(--color-warning); }
.sev-ok { background: var(--color-success-soft); color: var(--color-success); }

.incident-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  font-size: var(--font-xs);
  color: var(--color-text-tertiary);
  margin-bottom: 4px;
}
.incident-meta .mono {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.count-chip {
  white-space: nowrap;
  color: var(--color-text-secondary);
  font-weight: 600;
}

.incident-card-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}
.status-chip {
  padding: 1px 8px;
  border-radius: var(--radius-pill);
  font-size: var(--font-xs);
  font-weight: 600;
  white-space: nowrap;
}
.status-danger { background: var(--color-danger-soft); color: var(--color-danger); }
.status-ok { background: var(--color-success-soft); color: var(--color-success); }
.incident-time {
  font-size: var(--font-xs);
  color: var(--color-text-tertiary);
  white-space: nowrap;
}

.incidents-detail {
  border: none;
  border-radius: var(--radius-xl);
  background: var(--color-bg-elevated);
  box-shadow: var(--shadow-sm);
  padding: var(--space-4);
  overflow-y: auto;
  max-height: calc(100vh - 260px);
}

.detail-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  margin-bottom: var(--space-3);
}
.detail-title h3 {
  margin: 0;
  font-size: var(--font-lg);
  color: var(--color-text-primary);
  word-break: break-all;
}

.detail-summary-head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex-wrap: wrap;
}

.detail-summary dl {
  margin: var(--space-3) 0 0;
  display: grid;
  grid-template-columns: 1fr;
  gap: 6px;
}
.detail-summary dl > div {
  display: flex;
  gap: 6px;
}
.detail-summary dt {
  color: var(--color-text-tertiary);
  font-size: var(--font-sm);
  min-width: 72px;
}
.detail-summary dd {
  margin: 0;
  font-size: var(--font-sm);
  color: var(--color-text-primary);
  word-break: break-all;
}

.detail-block {
  margin-top: var(--space-4);
}
.detail-block h4 {
  margin: 0 0 8px;
  font-size: var(--font-base);
  color: var(--color-text-primary);
}

.member-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
.member-item {
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  background: var(--color-bg);
  border: 1px solid var(--color-border);
}
.member-head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}
.member-title {
  flex: 1;
  min-width: 0;
  font-size: var(--font-sm);
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.member-desc {
  margin: 4px 0 0;
  font-size: var(--font-xs);
  color: var(--color-text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.member-meta {
  margin: 4px 0 0;
  font-size: var(--font-xs);
  color: var(--color-text-tertiary);
}

.skeleton {
  border-radius: var(--radius-md);
  background: linear-gradient(90deg, var(--color-bg-active) 25%, var(--color-bg-hover) 50%, var(--color-bg-active) 75%);
  background-size: 200% 100%;
  animation: skeleton-shimmer 1.2s ease-in-out infinite;
}
.skeleton-line { height: 14px; }
.skeleton-line.short { width: 60%; }
@keyframes skeleton-shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

.mono {
  font-family: var(--font-mono, monospace);
  font-size: 0.8rem;
}

.empty {
  padding: 1.5rem;
  text-align: center;
  color: var(--color-text-muted);
  font-size: 0.85rem;
  margin: 0;
}

.error-text {
  margin: 0;
  font-size: 0.75rem;
  color: var(--color-danger, #d33);
}
</style>
