<script setup lang="ts">
import { ElButton, ElAlert } from 'element-plus';
import WorkflowRail from '../components/management/WorkflowRail.vue';
import CapabilityLedgerPanel from '../components/management/CapabilityLedgerPanel.vue';
import SourceStage from '../components/management/SourceStage.vue';
import CapabilityCreationCanvas from '../components/management/CapabilityCreationCanvas.vue';
import WorkOrderHeader from '../components/management/WorkOrderHeader.vue';
import WorkflowSpine from '../components/management/WorkflowSpine.vue';
import CandidatesStage from '../components/management/CandidatesStage.vue';
import ReviewStage from '../components/management/ReviewStage.vue';
import AIStage from '../components/management/AIStage.vue';
import type { UseCapabilities } from '../composables/useCapabilities';
import type { PageContext } from '../composables/useAssistantStream';
import ViewShell from '../components/ViewShell.vue';

const props = defineProps<{ capabilities: UseCapabilities }>();

// 缺口-3：选中 capability 后「问 AI」，携带 domain/resource_type 跳转 assistant 视图。
// 仅当 selected 有 domain 时可用（草稿未填 domain 时禁用）。
const emit = defineEmits<{
  (event: 'ask-ai', pageContext: PageContext): void;
}>();

function askAi() {
  const selected = props.capabilities.selected.value;
  const ctx: PageContext = {
    domain: selected.domain || undefined,
    resource_type: selected.resource_type || undefined,
    resource_name: undefined,
  };
  emit('ask-ai', ctx);
}

// 阶段切换由用户明确点击步骤或主操作完成；不强制修改滚动位置。

// 工单页 → 列表页：review 收起工单开关；接入/验证工单回列表并重置画布。
function closeDetail() {
  props.capabilities.wizardOpen.value = false;
}

function backToList() {
  props.capabilities.managementPhase.value = 'review';
  props.capabilities.wizardOpen.value = false;
}
</script>

<template>
  <ViewShell
    v-show="capabilities.managementPhase.value"
    class="management-entry capability-studio"
    data-test="management-entry"
    data-view="management"
    eyebrow=""
    title=""
    copy=""
  >
    <!-- 只有进入评审/发布阶段（需要挑选草稿）才显示管理动作；创建/验证途中保持零干扰。 -->
    <template v-if="capabilities.managementPhase.value === 'review' || capabilities.managementPhase.value === 'ai'" #actions>
      <el-button
        data-test="toggle-wizard"
        type="primary"
        @click="capabilities.managementPhase.value = 'source'; capabilities.wizardOpen.value = true"
      >+ 接入 AI 工具</el-button>
      <el-button data-test="new-draft" type="primary" @click="capabilities.newDraft(); capabilities.wizardOpen.value = true" plain>新建草稿</el-button>
      <el-button @click="capabilities.loadCapabilities">刷新</el-button>
      <el-button
        data-test="management-ask-ai"
        :disabled="!capabilities.selected.value.domain"
        @click="askAi"
      >
        问 AI
      </el-button>
    </template>

    <el-alert v-if="capabilities.error.value" class="alert" type="error" :title="capabilities.error.value" show-icon />

    <el-alert
      v-if="capabilities.configured.value === false"
      class="alert"
      type="warning"
      show-icon
      data-test="capabilities-not-configured"
      title="能力存储未配置（未设置 COPILOT_CAPABILITIES_DIR，且无数据库）"
      description="当前能力列表为空表示能力存储未启用（配置后列表会自动加载已发现或已发布的能力）。"
      :closable="false"
    />

    <!-- 信息架构（最终形态）：
         review/ai = 列表主界面：纯清单单列全宽 + 顶栏动作；行内「评审」打开工单页。
         source/candidates = 接入/验证工单页（全屏单列）。
         wizardOpen 在 review 阶段兼作"工单页打开"开关。 -->
    <section
      v-if="capabilities.managementPhase.value === 'review' && capabilities.wizardOpen.value"
      data-test="workflow-console"
      class="workflow-console studio-layout is-intake"
      aria-label="Capability 评审工单"
    >
      <div class="studio-wizard">
        <div class="review-topbar">
          <button data-test="back-to-list" class="secondary-inline" type="button" @click="closeDetail">← 返回列表</button>
        </div>
        <WorkOrderHeader :capabilities="capabilities" />
        <WorkflowSpine :capabilities="capabilities" />
        <ReviewStage :capabilities="capabilities" />
      </div>
    </section>
    <section
      v-else-if="capabilities.managementPhase.value === 'ai' && capabilities.wizardOpen.value"
      data-test="workflow-console"
      class="workflow-console studio-layout is-intake"
      aria-label="AI 验证工单"
    >
      <div class="studio-wizard">
        <div class="review-topbar">
          <button data-test="back-to-list" class="secondary-inline" type="button" @click="closeDetail">← 返回列表</button>
        </div>
        <WorkOrderHeader :capabilities="capabilities" />
        <WorkflowSpine :capabilities="capabilities" />
        <AIStage :capabilities="capabilities" />
      </div>
    </section>
    <section
      v-else-if="capabilities.managementPhase.value === 'source' || capabilities.managementPhase.value === 'candidates'"
      data-test="workflow-console"
      class="workflow-console studio-layout is-intake"
      :class="{ 'is-candidate': capabilities.managementPhase.value === 'candidates' }"
      aria-label="Capability 接入"
    >
      <div class="studio-wizard">
        <div class="review-topbar">
          <button data-test="back-to-list" class="secondary-inline" type="button" @click="backToList">← 返回列表</button>
        </div>
        <el-alert v-if="capabilities.error.value" class="alert" type="error" :title="capabilities.error.value" show-icon />
        <WorkOrderHeader :capabilities="capabilities" />
        <WorkflowSpine :capabilities="capabilities" />
        <CapabilityCreationCanvas v-if="capabilities.managementPhase.value === 'source'" :capabilities="capabilities" />
        <CandidatesStage v-else :capabilities="capabilities" />
      </div>
    </section>
    <section
      v-else
      data-test="workflow-console"
      class="workflow-console studio-layout is-intake"
      aria-label="Capability 列表"
    >
      <CapabilityLedgerPanel :capabilities="capabilities" />
    </section>
  </ViewShell>
</template>