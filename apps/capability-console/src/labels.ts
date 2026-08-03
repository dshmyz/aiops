import type {
  CapabilityRisk,
  CapabilityStatus,
  DiagnosticConfidence,
  DiagnosticSeverity,
  VerificationStatus,
} from './types';

export const riskLabel: Record<CapabilityRisk, string> = {
  low: '低',
  medium: '中',
  high: '高',
};

export const severityLabel: Record<DiagnosticSeverity, string> = {
  ok: '正常',
  info: '提示',
  warning: '警告',
  critical: '严重',
};

export const confidenceLabel: Record<DiagnosticConfidence, string> = {
  low: '低',
  medium: '中',
  high: '高',
};

export const capabilityStatusLabel: Record<CapabilityStatus, string> = {
  discovered: '已发现',
  needs_review: '待审核',
  published: '已发布',
  deprecated: '已废弃',
};

export const verificationStatusLabel: Record<VerificationStatus, string> = {
  success: '已通过',
  failed: '失败',
  denied: '被拒绝',
};

export const executionStatusLabel: Record<string, string> = {
  succeeded: '成功',
  failed: '失败',
  denied: '被拒绝',
  pending: '待确认',
  confirmed: '已确认',
};

export const planStatusLabel: Record<string, string> = {
  pending: '待确认',
  confirmed: '已确认',
  executing: '执行中',
  succeeded: '成功',
  failed: '失败',
  denied: '被拒绝',
  expired: '已过期',
};

export const environmentLabel: Record<string, string> = {
  prod: '生产环境',
  staging: '预发环境',
  dev: '开发环境',
  none: '不指定',
};

export function labelForEnvironment(value: string | undefined): string {
  if (!value) return '-';
  return environmentLabel[value] ?? value;
}

export function labelForRisk(value: string | undefined): string {
  if (!value) return '-';
  return riskLabel[value as CapabilityRisk] ?? value;
}

export function labelForSeverity(value: string | undefined): string {
  if (!value) return '-';
  return severityLabel[value as DiagnosticSeverity] ?? value;
}

export function labelForConfidence(value: string | undefined): string {
  if (!value) return '-';
  return confidenceLabel[value as DiagnosticConfidence] ?? value;
}

export function labelForExecutionStatus(value: string | undefined): string {
  if (!value) return '-';
  return executionStatusLabel[value] ?? value;
}

export function labelForPlanStatus(value: string | undefined): string {
  if (!value) return '-';
  return planStatusLabel[value] ?? value;
}
