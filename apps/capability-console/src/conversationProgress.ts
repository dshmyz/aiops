/**
 * 执行进度流的数据汇聚。
 *
 * 把 SSE 推送的三类进度证据（阶段 progress_stages、工具调用 tool_calls）
 * 合并成一条 Trae 式连贯的执行进度时间线，供 ProgressTimeline 渲染。
 *
 * 排序策略是确定性的：progress_stages 提供"阶段围栏"骨架，工具调用作为缩进
 * 子项锚定在 tool_executing 阶段之下展开（诊断/读路径的工具调用都发生在这个
 * 阶段）。ToolCall 没有时间戳，因此不依赖事件到达时间排序，避免错位。
 */

import type { AssistantStep, ProgressStage, ToolCall } from './types';

export type ProgressEntryKind = 'phase' | 'tool';

export interface ProgressEntry {
  /** 条目类型：phase=阶段围栏，tool=阶段下的工具动作 */
  kind: ProgressEntryKind;
  /** 来源阶段，用于样式与 icon 分类 */
  stage: ProgressStage['stage'];
  /** 完成态：true=已结束，false=进行中 */
  done: boolean;
  /** 人话化动作文案（工具条目用）或阶段文案（阶段条目） */
  label: string;
  /** 附加说明（工具名原文，悬停/辅助阅读用） */
  detail?: string;
  /** 时间戳（阶段条目来自事件，工具条目沿用其锚定阶段的时间） */
  time: string;
}

/** 阶段完成文案（完成时态） */
const PHASE_DONE_LABEL: Record<ProgressStage['stage'], string> = {
  planning: '识别并拆解任务',
  tool_executing: '查询平台事实',
  formatting: '整合输出',
};

/** 阶段进行中文案（进行时态） */
const PHASE_ACTIVE_LABEL: Record<ProgressStage['stage'], string> = {
  planning: '正在识别并拆解任务',
  tool_executing: '正在查询平台事实',
  formatting: '正在整合输出',
};

/**
 * 工具名可读化，如 cluster.status.read → 查询 cluster.status.read。
 * 不引入硬编码领域词表，只在工具名可信时拼一个动作前缀，其余保留原文。
 */
function humanizeTool(tool: string): string {
  const name = tool.replace(/\.read$/, '');
  return `查询 ${name}`;
}

/**
 * 判断是否有任何阶段处于进行中（用于 exercise 是否进入"进行中"语义）。
 * 流式期间最后一个阶段是进行中；非流式全部结束。
 */
function phaseDoneAt(stages: ProgressStage[], idx: number, streaming: boolean): boolean {
  if (!streaming) return true;
  return idx < stages.length - 1;
}

/**
 * 把阶段与工具调用合并为一条执行进度流。
 *
 * 规则：
 * - 阶段按事件顺序成为骨架条目；
 * - tool_executing 阶段之下的工具调用作为缩进子项紧跟其后；此时工具名以子项
 *   体现，阶段条目不重复携带 detail；
 * - 没有工具列表兜底时，阶段自身的 detail（工具名）直接作为阶段说明展示；
 * - 非流式或工具 done 时条目视为完成，否则视为进行中。
 */
export function mergeExecutionProgress(
  stages: ProgressStage[],
  tools: ToolCall[],
  streaming: boolean,
): ProgressEntry[] {
  const entries: ProgressEntry[] = [];

  for (const [idx, stage] of stages.entries()) {
    const done = phaseDoneAt(stages, idx, streaming);
    const hasToolsUnder = stage.stage === 'tool_executing' && tools.length > 0;
    const phaseEntry: ProgressEntry = {
      kind: 'phase',
      stage: stage.stage,
      done,
      label: done ? PHASE_DONE_LABEL[stage.stage] : PHASE_ACTIVE_LABEL[stage.stage],
      // 有完整工具列表时由子项展示工具名；否则兜底阶段自身的 detail。
      detail: hasToolsUnder ? undefined : stage.detail,
      time: stage.received_at,
    };
    entries.push(phaseEntry);

    if (hasToolsUnder) {
      for (const tool of tools) {
        entries.push({
          kind: 'tool',
          stage: 'tool_executing',
          // 回放时工具调用已全部结束，一律视为完成；流式中按各自 done 判定。
          done: !streaming || tool.done,
          label: humanizeTool(tool.tool),
          detail: tool.tool,
          time: stage.received_at,
        });
      }
    }
  }

  return entries;
}

/**
 * 把回放的历史步骤转换为工具执行流条目。
 *
 * 用于页面刷新/回放后 `progress_stages` 已不在 turn 上、但后端已持久化
 * `steps`（executor 的 process.steps 水合，或 agentLoop 的独立 tool_step turn）
 * 的场景：让执行进度面板在刷新后仍能重建，展示 Agent 实际执行/查询了哪些工具。
 *
 * 每条 advisory 工具步生成一个"查询 xxx"工具条目；detail 优先取结果摘要
 * （failed 时取错误），使条目信息量与步骤详情一致。历史步一律视为完成。
 */
export function mergeStepToolEntries(steps: AssistantStep[]): ProgressEntry[] {
  return steps
    .filter((s) => s.tool)
    .map((s) => ({
      kind: 'tool' as const,
      stage: 'tool_executing' as const,
      done: s.status !== 'running',
      label: humanizeTool(s.tool),
      detail: s.status === 'failed' && s.error ? s.error : (s.summary || s.tool),
      time: '',
    }));
}

/** 供折叠态使用的阶段图标名（仅取已知阶段） */
export function stageIconName(stage: ProgressEntry['stage']): 'sparkles' | 'waveform' | 'bubble-left' {
  if (stage === 'planning') return 'sparkles';
  if (stage === 'tool_executing') return 'waveform';
  return 'bubble-left';
}