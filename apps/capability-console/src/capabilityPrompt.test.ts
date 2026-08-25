import { describe, expect, test } from 'vitest';
import type { ManagedCapability } from './types';
import { buildAIPrompt, parseTestInput } from './capabilityPrompt';

function makeCapability(overrides: Partial<ManagedCapability> = {}): ManagedCapability {
  return {
    schema_version: 1,
    name: 'kafka.consumer_lag.read',
    status: 'discovered',
    domain: 'middleware',
    resource_type: 'kafka',
    operation: 'read',
    risk: 'low',
    source: 'discovered',
    validation: { valid: false, error: '未校验' },
    input_schema: {},
    output: { fields: {} },
    backend: { adapter: 'http', method: 'GET', path: '/consumer/groups/lag' },
    ...overrides,
  } as ManagedCapability;
}

describe('parseTestInput', () => {
  test('解析合法的 JSON 对象', () => {
    expect(parseTestInput('{"environment":"prod","group":"g1"}')).toEqual({
      environment: 'prod',
      group: 'g1',
    });
  });

  test('JSON 数组按非法对象处理返回空对象', () => {
    expect(parseTestInput('[1,2,3]')).toEqual({});
  });

  test('JSON 原始类型按非法对象处理返回空对象', () => {
    expect(parseTestInput('"hello"')).toEqual({});
  });

  test('非法 JSON 返回空对象而不是抛错', () => {
    expect(parseTestInput('not-json')).toEqual({});
    expect(parseTestInput('')).toEqual({});
    expect(parseTestInput('{broken')).toEqual({});
  });
});

describe('buildAIPrompt', () => {
  test('默认环境取 prod，resource/domain 取默认值', () => {
    const prompt = buildAIPrompt(makeCapability(), {});
    expect(prompt).toContain('查询');
    expect(prompt).toContain('prod');
    expect(prompt).toContain('kafka');
    expect(prompt).toContain('middleware');
  });

  test('环境来自输入且参与拼接', () => {
    const prompt = buildAIPrompt(makeCapability(), { environment: 'staging' });
    expect(prompt).toContain('staging');
    expect(prompt).not.toContain('prod');
  });

  test('非 environment 字段的字符串值按顺序参与拼接', () => {
    const prompt = buildAIPrompt(
      makeCapability({ name: 'consumer_group.lag.read', resource_type: 'kafka', domain: 'middleware' }),
      { environment: 'prod', group: 'g1', topic: 'orders' },
    );
    expect(prompt).toBe('查询 prod g1 orders kafka 的 middleware 延迟');
  });

  test('空白字符串值被过滤，不影响其余拼接', () => {
    const prompt = buildAIPrompt(
      makeCapability(),
      { environment: 'prod', group: '  ' },
    );
    expect(prompt).not.toContain('group');
    expect(prompt).toContain('kafka');
  });

  test('数字与布尔值转为字符串参与拼接', () => {
    const prompt = buildAIPrompt(
      makeCapability(),
      { environment: 'prod', replicas: 3, force: true },
    );
    expect(prompt).toContain('3');
    expect(prompt).toContain('true');
  });

  test('名称中的已知 token 映射为中文关键词', () => {
    expect(buildAIPrompt(makeCapability({ name: 'minio.bucket.health.read' }), {})).toContain(
      '健康',
    );
  });

  test('未知 token 回退为「状态」', () => {
    expect(buildAIPrompt(makeCapability({ name: 'foo.bar.baz' }), {})).toContain('状态');
  });
});