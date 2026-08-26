import { describe, expect, test } from 'vitest';
import type { ProgressStage, ToolCall } from './types';
import { mergeExecutionProgress } from './conversationProgress';

const phaseStages = (): ProgressStage[] => [
  { stage: 'planning', received_at: '2026-08-03T10:00:00Z' },
  { stage: 'tool_executing', received_at: '2026-08-03T10:00:01Z' },
  { stage: 'formatting', received_at: '2026-08-03T10:00:02Z' },
];

const tools = (): ToolCall[] => [
  { tool: 'kafka.topic.retention.set', input: {}, done: true },
  { tool: 'minio.bucket.capacity.read', done: false },
];

describe('mergeExecutionProgress', () => {
  test('阶段为骨架，工具子项锚定在 tool_executing 阶段之下', () => {
    const entries = mergeExecutionProgress(phaseStages(), tools(), false);
    expect(entries.map((e) => e.kind)).toEqual([
      'phase',
      'phase',
      'tool',
      'tool',
      'phase',
    ]);
  });

  test('有工具列表时 tool_executing 阶段不带 detail，工具以子项呈现原文', () => {
    const entries = mergeExecutionProgress(phaseStages(), tools(), false);
    const toolExecPhase = entries[1];
    expect(toolExecPhase.detail).toBeUndefined();
    const toolItems = entries.filter((e) => e.kind === 'tool');
    expect(toolItems.map((e) => e.detail)).toEqual([
      'kafka.topic.retention.set',
      'minio.bucket.capacity.read',
    ]);
    // 人话化：动作前缀 + 工具名
    expect(entries[2].label).toBe('查询 kafka.topic.retention.set');
  });

  test('无工具列表时，tool_executing 阶段保留自身 detail 兜底显示', () => {
    const stages: ProgressStage[] = [
      { stage: 'planning', received_at: '2026-08-03T10:00:00Z' },
      { stage: 'tool_executing', detail: 'kafka.topic.retention.set', received_at: '2026-08-03T10:00:01Z' },
    ];
    const entries = mergeExecutionProgress(stages, [], false);
    expect(entries.length).toBe(2);
    expect(entries[1].detail).toBe('kafka.topic.retention.set');
  });

  test('非流式全部视为完成', () => {
    const entries = mergeExecutionProgress(phaseStages(), tools(), false);
    expect(entries.every((e) => e.done)).toBe(true);
  });

  test('流式时仅最后一个条目进行中，工具是否完成由各自 done 决定', () => {
    const stages: ProgressStage[] = [
      { stage: 'planning', received_at: '2026-08-03T10:00:00Z' },
      { stage: 'tool_executing', received_at: '2026-08-03T10:00:01Z' },
    ];
    const entries = mergeExecutionProgress(stages, tools(), true);
    expect(entries[0].done).toBe(true); // planning 已完成，进入下一阶段
    // tool_executing 阶段本身处于进行中
    expect(entries[1].done).toBe(false);
    // 工具按各自 done 判定：kafka 已 done，minio 仍进行中
    expect(entries[2].done).toBe(true);
    expect(entries[3].done).toBe(false);
  });

  test('空阶段与空工具返回空流', () => {
    expect(mergeExecutionProgress([], [], true)).toEqual([]);
  });
});