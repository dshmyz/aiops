<script setup lang="ts">
import { onMounted, ref } from 'vue';
import type { OverviewData, PendingPlanSummary } from '../types';
import {
  fetchOverview,
  listPendingPlans,
  rejectPlan,
} from '../api';
import { labelForRisk, labelForExecutionStatus } from '../labels';
import { formatRelativeTime } from '../conversationFormat';

// 运维总览（D）：首屏聚合待确认计划 / 活动告警 / 定时巡检 / 今日执行等计数。
// 各字段可选：对应 service 未装配或当前用户无权限时后端返回缺省字段，这里优雅降级。

const emit = defineEmits<{
  // 统计卡片下钻：跳转到对应视图（plans / incident / scheduled-tasks / executions）。
  (event: 'navigate', view: string): void;
}>();

const overview = ref<OverviewData>({});
const pendingPlans = ref<PendingPlanSummary[]>([]);
const overviewLoading = ref(false);
const overviewError = ref('');
const rejectingIDs = ref<Set<string>>(new Set());
const rejectError = ref('');
const notice = ref('');

// 统计卡片 → 目标视图映射，供模板绑定（@click 时 emit navigate）。
const statTargets: Record<string, string> = {
  'stat-pending-plans': 'plans',
  'stat-active-alerts': 'incident',
  'stat-enabled-tasks': 'scheduled-tasks',
  'stat-today-executions': 'executions',
};

function onNavigate(dataTest: string) {
  const target = statTargets[dataTest];
  if (target) emit('navigate', target);
}

async function refresh() {
  overviewLoading.value = true;
  overviewError.value = '';
  try {
    const [ov, plans] = await Promise.all([fetchOverview(), listPendingPlans()]);
    overview.value = ov;
    pendingPlans.value = plans;
  } catch (e) {
    overviewError.value = e instanceof Error ? e.message : String(e);
  } finally {
    overviewLoading.value = false;
  }
}

async function handleReject(plan: PendingPlanSummary) {
  rejectError.value = '';
  if (rejectingIDs.value.has(plan.id)) return;
  rejectingIDs.value.add(plan.id);
  try {
    await rejectPlan(plan.id, { expected_version: plan.version });
    notice.value = `已拒绝计划 ${plan.tool}`;
    await refresh();
  } catch (e) {
    rejectError.value = e instanceof Error ? e.message : String(e);
  } finally {
    rejectingIDs.value.delete(plan.id);
  }
}

onMounted(() => {
  void refresh();
});
</script>

<template>
  <section data-test="dashboard-entry" data-view="dashboard" class="dashboard-entry">
    <header class="topbar">
      <div>
        <p class="eyebrow">Ops Overview</p>
        <h1>运维总览</h1>
        <p class="topbar-copy">聚合当前待确认计划、活动告警、定时巡检与今日执行结果，一眼掌握中间件运维态势。</p>
      </div>
      <div class="actions">
        <button class="mini-button" :disabled="overviewLoading" @click="refresh">
          {{ overviewLoading ? '刷新中' : '刷新' }}
        </button>
      </div>
    </header>

    <p v-if="overviewError" data-test="dashboard-error" class="error-text" role="alert">{{ overviewError }}</p>
    <p v-if="rejectError" data-test="dashboard-reject-error" class="error-text">{{ rejectError }}</p>
    <p v-if="notice" data-test="dashboard-notice" class="notice-text">{{ notice }}</p>

    <!-- 顶部统计卡片（可点击下钻到对应视图） -->
    <section class="stat-grid" aria-label="运维统计">
      <button type="button" class="stat-card" data-test="stat-pending-plans" @click="onNavigate('stat-pending-plans')">
        <span class="stat-value" :class="{ 'stat-value--accent': (overview.pending_plans ?? 0) > 0 }">{{ overview.pending_plans ?? 0 }}</span>
        <span class="stat-label">待确认计划</span>
      </button>
      <button type="button" class="stat-card" data-test="stat-active-alerts" @click="onNavigate('stat-active-alerts')">
        <span class="stat-value" :class="{ 'stat-value--alert': (overview.active_alerts ?? 0) > 0 }">{{ overview.active_alerts ?? 0 }}</span>
        <span class="stat-label">活动告警</span>
      </button>
      <button type="button" class="stat-card" data-test="stat-enabled-tasks" @click="onNavigate('stat-enabled-tasks')">
        <span class="stat-value">{{ overview.enabled_tasks ?? 0 }}</span>
        <span class="stat-label">启用的定时巡检</span>
      </button>
      <button type="button" class="stat-card" data-test="stat-today-executions" @click="onNavigate('stat-today-executions')">
        <span class="stat-value">
          <span class="stat-ok">{{ overview.today_executions_succeeded ?? 0 }}</span>
          <span class="stat-sep">/</span>
          <span class="stat-bad">{{ overview.today_executions_failed ?? 0 }}</span>
        </span>
        <span class="stat-label">今日执行 成功/失败</span>
      </button>
    </section>

    <!-- 待确认计划速览 -->
    <section class="dashboard-panel">
      <div class="section-heading">
        <h2>待确认计划</h2>
        <span class="section-hint">需人工确认的高风险写操作</span>
      </div>
      <div v-if="pendingPlans.length === 0" class="empty">当前没有待确认计划</div>
      <ul v-else class="overview-plan-list">
        <li v-for="plan in pendingPlans" :key="plan.id" class="overview-plan-item">
          <div class="overview-plan-info">
            <div class="overview-plan-title">
              <strong>{{ plan.tool }}</strong>
              <span class="risk-badge">{{ labelForRisk(plan.risk) }}</span>
            </div>
            <div class="overview-plan-meta">
              <span>{{ plan.environment || '不指定' }}</span>
              <span>{{ plan.created_by }}</span>
              <span>{{ formatRelativeTime(plan.created_at) }} 创建 · {{ formatRelativeTime(plan.expires_at) }} 过期</span>
            </div>
          </div>
          <div class="overview-plan-status">{{ labelForExecutionStatus(plan.status) }}</div>
          <button
            type="button"
            class="mini-button mini-button--danger"
            :disabled="rejectingIDs.has(plan.id)"
            @click="handleReject(plan)"
          >
            {{ rejectingIDs.has(plan.id) ? '拒绝中' : '拒绝' }}
          </button>
        </li>
      </ul>
    </section>
  </section>
</template>
