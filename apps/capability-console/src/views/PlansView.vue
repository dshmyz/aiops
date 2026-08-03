<script setup lang="ts">
import { ElAlert } from 'element-plus';
import PendingPlanList from '../components/PendingPlanList.vue';
import PendingPlanDetail from '../components/PendingPlanDetail.vue';
import ExecutionResultView from '../components/ExecutionResultView.vue';
import type { UsePendingPlans } from '../composables/usePendingPlans';

defineProps<{
  plans: UsePendingPlans;
  planTokens: Record<string, string>;
}>();
</script>

<template>
  <section data-test="plans-entry" data-view="plans" class="plans-entry">
    <header class="topbar">
      <div>
        <p class="eyebrow">Action Plan Approval</p>
        <h1>待确认计划</h1>
        <p class="topbar-copy">查看待审批的写入操作计划，确认后执行。生产环境需要外部审批 token。</p>
      </div>
      <div class="actions">
        <button class="mini-button" :disabled="plans.pendingPlansLoading.value" @click="plans.refresh">
          {{ plans.pendingPlansLoading.value ? '刷新中' : '刷新' }}
        </button>
      </div>
    </header>

    <el-alert v-if="plans.pendingPlansError.value" class="alert" type="error" :title="plans.pendingPlansError.value" show-icon />

    <section class="plans-workspace">
      <aside class="plans-list-panel">
        <PendingPlanList
          :plans="plans.pendingPlans.value"
          :loading="plans.pendingPlansLoading.value"
          :selected-id="plans.selectedPlanID.value"
          @select="plans.select"
          @refresh="plans.refresh"
        />
      </aside>
      <section class="plans-main-panel">
        <div class="plans-main-scroll">
          <div class="plans-detail-section">
            <div class="section-heading">
              <h2>计划详情</h2>
            </div>
            <div v-if="plans.selectedPlanLoading.value" class="empty">加载中...</div>
            <PendingPlanDetail
              v-else
              :plan="plans.selectedPlanDetail.value"
              :confirmation-token="plans.selectedPlanID.value ? planTokens[plans.selectedPlanID.value] : undefined"
              @confirmed="plans.handleConfirmed"
              @error="plans.handleError"
            />
          </div>
          <div class="plans-result-section">
            <div class="section-heading">
              <h2>执行结果</h2>
            </div>
            <ExecutionResultView v-if="plans.latestExecutionResult.value" :result="plans.latestExecutionResult.value" />
            <div v-else class="empty">确认执行后，结果会显示在这里。</div>
          </div>
        </div>
      </section>
    </section>
  </section>
</template>
