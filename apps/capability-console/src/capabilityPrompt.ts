import type { ManagedCapability } from './types';

/**
 * 将一段测试输入文本解析为结构化对象，供 AI 预检提示词与测试表单使用。
 * - 只有 JSON 对象被接受；数组 / 原始类型 / 非法 JSON 一律返回空对象。
 */

export function parseTestInput(text: string): Record<string, unknown> {
  try {
    const input = JSON.parse(text) as unknown;
    if (input && typeof input === 'object' && !Array.isArray(input)) {
      return input as Record<string, unknown>;
    }
  } catch (_err) {
    return {};
  }
  return {};
}

function stringValue(value: unknown): string {
  if (typeof value === 'string') {
    return value.trim();
  }
  if (typeof value === 'number' || typeof value === 'boolean') {
    return String(value);
  }
  return '';
}

function operationKeyword(name: string): string {
  const aliases: Record<string, string> = {
    capacity: '容量',
    health: '健康',
    lag: '延迟',
    lifecycle: '生命周期',
    quota: '配额',
    retention: '保留',
    status: '状态',
  };
  for (const token of name.split('.')) {
    if (aliases[token]) {
      return aliases[token];
    }
  }
  return '状态';
}

/**
 * 根据 Capability 与用户输入的测试参数，拼一句话形式的 AI 预检提示词。
 * 环境默认取 prod，非 environment 字段值按顺序参与拼接。
 */
export function buildAIPrompt(capability: ManagedCapability, input: Record<string, unknown>): string {
  const environment = stringValue(input.environment) || 'prod';
  const values = Object.entries(input)
    .filter(([name]) => name !== 'environment')
    .map(([_name, value]) => stringValue(value))
    .filter((value) => value !== '');
  const resource = capability.resource_type || 'resource';
  const domain = capability.domain || 'middleware';
  const keyword = operationKeyword(capability.name);
  return ['查询', environment, ...values, resource, '的', domain, keyword].filter(Boolean).join(' ');
}