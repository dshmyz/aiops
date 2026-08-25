import type { Ref } from 'vue';
import type { Capability, ImportCandidate, ImportRecommendation, ManagedCapability } from './types';

/**
 * capabilityFormat 提供能力管理 UI 共用的纯函数：列表 upsert、展示文案格式化等。
 * 与能力 schema 相关的纯函数（canPublish / normalizeCapability / pathVariables）在 capability.ts。
 */

/** 同一能力在列表中的唯一键（source + name），区分"已发布"与"同名草稿"。 */
export function capabilityKey(capability: Pick<ManagedCapability, 'source' | 'name'>): string {
  return `${capability.source}:${capability.name}`;
}

/** 将 ManagedCapability 还原为可提交的 Capability（去掉管理字段）。 */
export function toCapability(value: ManagedCapability): Capability {
  const { source: _source, path: _path, modified_at: _modifiedAt, validation: _validation, ...capability } = value;
  return capability;
}

/** 按 (source, name) 插入或替换列表中的能力。 */
export function upsert(capabilities: Ref<ManagedCapability[]>, capability: ManagedCapability): void {
  const index = capabilities.value.findIndex((item) => capabilityKey(item) === capabilityKey(capability));
  if (index >= 0) {
    capabilities.value[index] = capability;
    return;
  }
  capabilities.value.push(capability);
}

export function sourceLabel(source: string): string {
  return source === 'published' ? '已发布' : '草稿';
}

export function operationLabel(operation: string): string {
  return operation === 'write' ? '写入' : '读取';
}

export function riskLabel(risk: string): string {
  switch (risk) {
    case 'medium':
      return '中';
    case 'high':
      return '高';
    default:
      return '低';
  }
}

export function recommendationLabel(value: ImportRecommendation): string {
  if (value === 'recommended') {
    return '推荐接入';
  }
  if (value === 'needs_adjustment') {
    return '需要调整';
  }
  return '暂不接入';
}

export function candidateReasonText(candidate: ImportCandidate): string {
  return (candidate.reasons ?? []).join(' / ');
}

export function candidateVerdictText(candidate: ImportCandidate): string {
  return (candidate.warnings ?? []).join(' / ') || candidateReasonText(candidate);
}
