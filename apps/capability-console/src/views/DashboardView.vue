<script setup lang="ts">
import { onMounted, ref } from 'vue';
import type { OverviewData, PendingPlanSummary } from '../types';
import {
  fetchOverview,
  listPendingPlans,
  rejectPlan,
  fetchAgentMetrics,
  setAgentEnabled,
  setAgentTrustLevel,
  type AgentMetrics,
} from '../api';
import { labelForRisk, labelForExecutionStatus } from '../labels';
import { formatRelativeTime } from '../conversationFormat';
import ViewShell from '../components/ViewShell.vue';
import SfSymbol from '../components/SfSymbol.vue';

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

// Agent 状态面板
const agentMetrics = ref<AgentMetrics | null>(null);
const agentMetricsError = ref('');
const agentActionLoading = ref(false);
const agentNotice = ref('');
const trustLevel = ref<'readonly' | 'confirm' | 'auto'>('confirm');

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
    const [ov, plans, metrics] = await Promise.all([fetchOverview(), listPendingPlans(), fetchAgentMetrics().catch(() => null)]);
    overview.value = ov;
    pendingPlans.value = plans;
    agentMetrics.value = metrics;
  } catch (e) {
    overviewError.value = e instanceof Error ? e.message : String(e);
  } finally {
    overviewLoading.value = false;
  }
}

async function handleToggleAgent() {
  if (!agentMetrics.value) return;
  agentActionLoading.value = true;
  agentNotice.value = '';
  try {
    const result = await setAgentEnabled(!agentMetrics.value.agent_enabled);
    agentNotice.value = result.message;
    agentMetrics.value = await fetchAgentMetrics();
  } catch (e) {
    agentMetricsError.value = e instanceof Error ? e.message : String(e);
  } finally {
    agentActionLoading.value = false;
  }
}

async function handleTrustLevel(level: 'readonly' | 'confirm' | 'auto') {
  agentActionLoading.value = true;
  agentNotice.value = '';
  try {
    await setAgentTrustLevel(level);
    trustLevel.value = level;
    agentNotice.value = `信任等级已切换为 ${level}`;
  } catch (e) {
    agentMetricsError.value = e instanceof Error ? e.message : String(e);
  } finally {
    agentActionLoading.value = false;
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
  <ViewShell
    class="dashboard-entry"
    data-test="dashboard-entry"
    data-view="dashboard"
    eyebrow="Ops Overview"
    title="运维总览"
    copy="聚合当前待确认计划、活动告警、定时巡检与今日执行结果，一眼掌握中间件运维态势。"
  >
    <template #actions>
      <button class="mini-button" :disabled="overviewLoading" @click="refresh">
        {{ overviewLoading ? '刷新中' : '刷新' }}
      </button>
    </template>

    <p v-if="overviewError" data-test="dashboard-error" class="error-text" role="alert">{{ overviewError }}</p>
    <p v-if="rejectError" data-test="dashboard-reject-error" class="error-text">{{ rejectError }}</p>
    <p v-if="notice" data-test="dashboard-notice" class="notice-text">{{ notice }}</p>

    <!-- 顶部统计卡片（可点击下钻到对应视图）；有「待办」时卡片顶边带警示色条 -->
    <section class="stat-grid" aria-label="运维统计">
      <button type="button" class="stat-card" :class="{ 'has-attention': (overview.pending_plans ?? 0) > 0 }" data-test="stat-pending-plans" @click="onNavigate('stat-pending-plans')">
        <span class="stat-value" :class="{ 'stat-value--accent': (overview.pending_plans ?? 0) > 0 }">{{ overview.pending_plans ?? 0 }}</span>
        <span class="stat-label">待确认计划</span>
      </button>
      <button type="button" class="stat-card" :class="{ 'has-danger': (overview.active_alerts ?? 0) > 0 }" data-test="stat-active-alerts" @click="onNavigate('stat-active-alerts')">
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

    <!-- 下方双栏：宽屏并排（Agent 状态 | 待确认计划），窄屏自动堆叠 -->
    <div class="dashboard-columns">
      <!-- Agent 状态面板：运行状态 + 一键停止 + 信任等级 -->
      <section class="dashboard-panel" data-test="agent-status-panel">
      <div class="section-heading">
        <h2>AI Agent 状态</h2>
        <span class="section-hint">LLM 调用健康度与安全开关</span>
      </div>
      <p v-if="agentMetricsError" class="error-text" role="alert">{{ agentMetricsError }}</p>
      <p v-if="agentNotice" class="notice-text">{{ agentNotice }}</p>
      <div v-if="agentMetrics" class="agent-status-grid">
        <div class="agent-status-item">
          <span class="agent-status-label">运行状态</span>
          <span class="agent-status-value" :class="agentMetrics.agent_enabled ? 'ok' : 'bad'">
            {{ agentMetrics.agent_enabled ? '运行中' : '已停止' }}
          </span>
        </div>
        <div class="agent-status-item">
          <span class="agent-status-label">LLM 调用</span>
          <span class="agent-status-value">
            {{ agentMetrics.llm_calls }} 次 / 失败 {{ agentMetrics.llm_failures }}（{{ (agentMetrics.llm_failure_rate * 100).toFixed(1) }}%）
          </span>
        </div>
        <div class="agent-status-item">
          <span class="agent-status-label">工具调用</span>
          <span class="agent-status-value">
            {{ agentMetrics.tool_calls }} 次 / 失败 {{ agentMetrics.tool_failures }}（{{ (agentMetrics.tool_failure_rate * 100).toFixed(1) }}%）
          </span>
        </div>
        <div class="agent-status-item">
          <span class="agent-status-label">连续失败</span>
          <span class="agent-status-value" :class="agentMetrics.consecutive_errors > 0 ? 'bad' : 'ok'">
            {{ agentMetrics.consecutive_errors }}
          </span>
        </div>
        <div class="agent-status-actions">
          <button
            type="button"
            class="mini-button"
            :class="agentMetrics.agent_enabled ? 'danger' : ''"
            :disabled="agentActionLoading"
            data-test="agent-kill-switch"
            @click="handleToggleAgent"
          >
            <template v-if="agentActionLoading">处理中...</template>
            <template v-else>
              <SfSymbol :name="agentMetrics.agent_enabled ? 'stop' : 'play'" :size="14" />
              {{ agentMetrics.agent_enabled ? '停止 Agent' : '启动 Agent' }}
            </template>
          </button>
          <div class="trust-level-group" role="radiogroup" aria-label="信任等级">
            <span class="agent-status-label">信任等级</span>
            <button
              v-for="level in (['readonly', 'confirm', 'auto'] as const)"
              :key="level"
              type="button"
              class="mini-button trust-button"
              :class="{ active: trustLevel === level }"
              :disabled="agentActionLoading"
              @click="handleTrustLevel(level)"
            >
              {{ { readonly: '只读', confirm: '需确认', auto: '自动' }[level] }}
            </button>
          </div>
        </div>
      </div>
      <div v-else class="empty">Agent 指标暂不可用（可能未启用 LLM 模式）</div>
    </section>

    <!-- 待确认计划速览 -->
    <section class="dashboard-panel" data-test="pending-plans-panel">
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
    </div>
  </ViewShell>
</template>
