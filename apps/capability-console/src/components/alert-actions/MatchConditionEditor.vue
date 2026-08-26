<script setup lang="ts">
import type { AlertLabelMatch, AlertMatch, AlertMatchOperator } from '../../types';

const props = defineProps<{
  match: AlertMatch;
}>();

const emit = defineEmits<{
  (event: 'update', match: AlertMatch): void;
}>();

const OPERATOR_OPTIONS: Array<{ value: AlertMatchOperator; label: string }> = [
  { value: 'exact', label: '精确' },
  { value: 'contains', label: '包含' },
  { value: 'regex', label: '正则' },
];

const SEVERITY_OPTIONS = ['critical', 'warning', 'info', 'unknown'];

function update(patch: Partial<AlertMatch>) {
  emit('update', { ...props.match, ...patch });
}

function updateLabel(idx: number, patch: Partial<AlertLabelMatch>) {
  const labels = [...(props.match.labels ?? [])];
  labels[idx] = { ...labels[idx], ...patch };
  update({ labels });
}

function addLabel() {
  update({ labels: [...(props.match.labels ?? []), { key: '', value: '', operator: 'exact' }] });
}

function removeLabel(idx: number) {
  update({ labels: (props.match.labels ?? []).filter((_, i) => i !== idx) });
}

function updateAnyOf(idx: number, patch: AlertMatch) {
  const anyOf = [...(props.match.any_of ?? [])];
  anyOf[idx] = patch;
  update({ any_of: anyOf });
}

function addAnyOf() {
  update({ any_of: [...(props.match.any_of ?? []), { severity: 'critical' }] });
}

function removeAnyOf(idx: number) {
  update({ any_of: (props.match.any_of ?? []).filter((_, i) => i !== idx) });
}
</script>

<template>
  <div class="match-editor" data-test="alert-match-editor">
    <div class="and-row">
      <label class="field">
        <span>alertname</span>
        <el-input :model-value="match.alertname ?? ''" placeholder="可选，精确匹配" @update:model-value="(v: string) => update({ alertname: v || undefined })" />
      </label>
      <label class="field">
        <span>severity</span>
        <el-select
          :model-value="match.severity ?? ''"
          clearable
          placeholder="可选"
          @update:model-value="(v: string) => update({ severity: v || undefined })"
        >
          <el-option v-for="s in SEVERITY_OPTIONS" :key="s" :value="s" :label="s" />
        </el-select>
      </label>
      <label class="field">
        <span>domain</span>
        <el-input :model-value="match.domain ?? ''" placeholder="可选，如 kafka" @update:model-value="(v: string) => update({ domain: v || undefined })" />
      </label>
    </div>

    <div class="labels-section">
      <div class="section-label">标签匹配（AND，缺失标签即不匹配）</div>
      <div v-for="(lm, idx) in match.labels ?? []" :key="idx" class="label-row" data-test="label-match-row">
        <el-input :model-value="lm.key" placeholder="标签键" class="label-key" @update:model-value="(v: string) => updateLabel(idx, { key: v })" />
        <el-select :model-value="lm.operator ?? 'exact'" class="label-op" @update:model-value="(v: AlertMatchOperator) => updateLabel(idx, { operator: v })">
          <el-option v-for="op in OPERATOR_OPTIONS" :key="op.value" :value="op.value" :label="op.label" />
        </el-select>
        <el-input :model-value="lm.value ?? ''" placeholder="匹配值" class="label-val" @update:model-value="(v: string) => updateLabel(idx, { value: v })" />
        <button type="button" class="remove-btn" title="移除该标签条件" @click="removeLabel(idx)">×</button>
      </div>
      <button type="button" class="add-btn" @click="addLabel">+ 标签条件</button>
    </div>

    <div class="anyof-section" v-if="(match.any_of ?? []).length > 0">
      <div class="section-label">或条件组（任一子条件匹配即命中）</div>
      <div v-for="(sub, idx) in match.any_of" :key="idx" class="anyof-row">
        <div class="anyof-fields">
          <label class="field">
            <span>severity</span>
            <el-select
              :model-value="sub.severity ?? ''"
              clearable
              placeholder="可选"
              @update:model-value="(v: string) => updateAnyOf(idx, { ...sub, severity: v || undefined })"
            >
              <el-option v-for="s in SEVERITY_OPTIONS" :key="s" :value="s" :label="s" />
            </el-select>
          </label>
          <label class="field">
            <span>alertname</span>
            <el-input :model-value="sub.alertname ?? ''" placeholder="可选" @update:model-value="(v: string) => updateAnyOf(idx, { ...sub, alertname: v || undefined })" />
          </label>
          <label class="field">
            <span>domain</span>
            <el-input :model-value="sub.domain ?? ''" placeholder="可选" @update:model-value="(v: string) => updateAnyOf(idx, { ...sub, domain: v || undefined })" />
          </label>
        </div>
        <button type="button" class="remove-btn" @click="removeAnyOf(idx)">×</button>
      </div>
    </div>
    <button type="button" class="add-btn" @click="addAnyOf">+ 或条件组</button>
  </div>
</template>

<style scoped>
.match-editor { display: flex; flex-direction: column; gap: 12px; }
.and-row { display: flex; gap: 12px; }
.and-row .field { flex: 1; }
.field span { display: block; font-size: 11px; color: var(--color-text-tertiary); margin-bottom: 4px; }
.labels-section, .anyof-section { border-top: 1px dashed var(--color-border); padding-top: 10px; display: flex; flex-direction: column; gap: 8px; }
.section-label { font-size: 11px; color: var(--color-text-tertiary); }
.label-row { display: flex; gap: 8px; align-items: center; }
.label-key { flex: 1; }
.label-op { flex: 0 0 90px; }
.label-val { flex: 1.5; }
.anyof-row { border: 1px solid var(--color-border); border-radius: var(--radius-md); padding: 10px; display: flex; gap: 8px; align-items: flex-start; background: var(--color-bg); }
.anyof-fields { flex: 1; display: flex; gap: 12px; }
.anyof-fields .field { flex: 1; }
.remove-btn { border: none; background: none; color: var(--color-text-tertiary); font-size: 16px; cursor: pointer; }
.add-btn { align-self: flex-start; border: 1px dashed var(--color-border); background: none; color: var(--color-text-secondary); padding: 4px 12px; border-radius: 4px; cursor: pointer; font-size: 12px; }
</style>
