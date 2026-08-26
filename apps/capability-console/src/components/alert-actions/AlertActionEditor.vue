<script setup lang="ts">
import { computed } from 'vue';
import type { AdminTool, AlertAction } from '../../types';
import MatchConditionEditor from './MatchConditionEditor.vue';
import SequenceEditor from './SequenceEditor.vue';

const props = defineProps<{
  /** null = 关闭；'new' = 新建；AlertAction = 编辑既有 */
  editing: null | 'new' | AlertAction;
  form: AlertAction;
  tools: AdminTool[];
  saving: boolean;
}>();

const emit = defineEmits<{
  (event: 'close'): void;
  (event: 'save'): void;
}>();

const drawerVisible = computed(() => props.editing !== null);

const title = computed(() => {
  if (props.editing === 'new') return '新建规则';
  if (props.editing) return `编辑规则 · ${props.editing.name}`;
  return '';
});

function emitUpdate(patch: Partial<AlertAction>) {
  // props.form 即父级 editForm 的响应式引用；原地 Object.assign 会触发父组件同步。
  Object.assign(props.form, patch);
}

function onMatchUpdate(match: AlertAction['alert_match']) {
  emitUpdate({ alert_match: match });
}
function onStepsUpdate(steps: AlertAction['tool_sequence']) {
  emitUpdate({ tool_sequence: steps });
}
</script>

<template>
  <el-drawer
    :model-value="drawerVisible"
    :title="title"
    size="640px"
    :append-to-body="false"
    :destroy-on-close="false"
    @close="emit('close')"
  >
    <div class="editor-body" data-test="alert-action-editor">
      <div class="field">
        <label class="field-label">规则名称 <span class="req">*</span></label>
        <el-input :model-value="form.name" placeholder="如: kafka-high-lag" data-test="alert-rule-name" @update:model-value="(v: string) => emitUpdate({ name: v })" />
      </div>

      <div class="field">
        <label class="field-label">描述</label>
        <el-input :model-value="form.description ?? ''" placeholder="可选，说明该规则用途" @update:model-value="(v: string) => emitUpdate({ description: v })" />
      </div>

      <section class="editor-section">
        <h4>匹配条件</h4>
        <MatchConditionEditor :match="form.alert_match ?? {}" @update="onMatchUpdate" />
      </section>

      <section class="editor-section">
        <h4>工具序列（按序执行）</h4>
        <SequenceEditor :steps="form.tool_sequence" :tools="tools" @update="onStepsUpdate" />
      </section>

      <div class="execute-last">
        <el-switch :model-value="form.execute_last_step ?? false" @update:model-value="(v: boolean) => emitUpdate({ execute_last_step: v })" />
        <span>最后一步直接执行（默认关闭：处置步骤创建待审批 plan）</span>
      </div>
    </div>

    <template #footer>
      <div class="editor-actions">
        <el-button :disabled="saving" @click="emit('close')">取消</el-button>
        <el-button type="primary" :loading="saving" @click="emit('save')" data-test="alert-save-btn">保存</el-button>
      </div>
    </template>
  </el-drawer>
</template>

<style scoped>
.editor-body { display: flex; flex-direction: column; gap: 16px; }
.field-label { display: block; font-size: 12px; color: var(--color-text-secondary); margin-bottom: 4px; }
.req { color: var(--color-danger); }
.editor-section h4 { margin: 0 0 10px; font-size: 13px; color: var(--color-text); }
.execute-last { display: flex; align-items: center; gap: 8px; font-size: 12px; color: var(--color-text-secondary); }
.editor-actions { display: flex; justify-content: flex-end; gap: 8px; }
</style>
