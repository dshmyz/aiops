<script setup lang="ts">
import { ElButton, ElAlert } from 'element-plus';
import WorkflowRail from '../components/management/WorkflowRail.vue';
import SourceStage from '../components/management/SourceStage.vue';
import CandidatesStage from '../components/management/CandidatesStage.vue';
import ReviewStage from '../components/management/ReviewStage.vue';
import AIStage from '../components/management/AIStage.vue';
import type { UseCapabilities } from '../composables/useCapabilities';
import type { PageContext } from '../composables/useAssistantStream';

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
</script>

<template>
  <section v-show="capabilities.managementPhase.value" data-test="management-entry" data-view="management" class="management-entry">
    <header class="topbar">
      <div>
        <p class="eyebrow">AI Capability Studio</p>
        <h1>把后台 API 翻译成 AI 工具</h1>
        <p class="topbar-copy">从 Swagger 收件箱选择接口，补齐参数和摘要，然后直接在右侧用自然语言试运行。</p>
      </div>
      <div class="actions">
        <el-button data-test="new-draft" type="primary" @click="capabilities.newDraft">新建草稿</el-button>
        <el-button @click="capabilities.loadCapabilities">刷新</el-button>
        <el-button
          data-test="management-ask-ai"
          :disabled="!capabilities.selected.value.domain"
          @click="askAi"
        >
          问 AI
        </el-button>
      </div>
    </header>

    <el-alert v-if="capabilities.error.value" class="alert" type="error" :title="capabilities.error.value" show-icon />

    <section data-test="workflow-console" class="workflow-console" aria-label="Capability 接入向导">
      <WorkflowRail :capabilities="capabilities" />
      <SourceStage v-if="capabilities.managementPhase.value === 'source'" :capabilities="capabilities" />
      <CandidatesStage v-else-if="capabilities.managementPhase.value === 'candidates'" :capabilities="capabilities" />
      <ReviewStage v-else-if="capabilities.managementPhase.value === 'review'" :capabilities="capabilities" />
      <AIStage v-else :capabilities="capabilities" />
    </section>
  </section>
</template>
