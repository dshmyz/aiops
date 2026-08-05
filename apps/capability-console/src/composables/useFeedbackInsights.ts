import type { FeedbackEntry } from '../types';

/**
 * 把原始用户反馈（评分 + 纠正文本）聚合为「可观测的改进建议清单」。
 *
 * 当前实现是确定性的关键词归类（frontend heuristic），不调 LLM——它把分散在
 * 各条反馈里的同类问题聚类成若干主题，并为每个主题给出能落到具体改进杠杆上的
 * 建议（planning prompt / runbook 意图与工具序列 / 能力 schema / 权限策略）。
 * 归类不出的纠正落到「待人工阅读」桶，避免凭空臆断。后续可平滑升级为模型驱动。
 */

export interface FeedbackInsight {
  key: string;
  label: string;
  /** 命中该主题的反馈条数 */
  count: number;
  /** 最多 N 条原始纠正/差评作为证据 */
  examples: string[];
  /** 落到具体改进杠杆上的建议 */
  suggestion: string;
}

/** 主题签名：一组关键词，命中即归入该主题。 */
interface ThemeRule {
  key: string;
  label: string;
  keywords: string[];
  suggestion: string;
}

const THEME_RULES: ThemeRule[] = [
  {
    key: 'clarification',
    label: '参数澄清 / 缺参数',
    keywords: ['参数', '缺少参数', '缺失', 'clarify', 'clarification', 'name 缺失', '请补充', '补全参数', '不清楚'],
    suggestion:
      '在 prompts/planning.md 补充对这类请求的参数澄清要求，或在对应能力的 schema 把必填字段标全，减少一轮「缺少参数」的往返。',
  },
  {
    key: 'capability-call',
    label: '工具调用 / 能力不可用',
    keywords: ['工具', '调用失败', '找不到', 'tool', '不可用', '未注册', '失败', '报错', 'error', '超时'],
    suggestion:
      '核查该能力的 published 定义与 HTTP 后端可达性；若为静态工具则确认已注册为可读工具，避免把「不可执行」透传成完整结论。',
  },
  {
    key: 'retention',
    label: '保留策略 / retention',
    keywords: ['保留', 'retention', '留存', '72 小时', '小时'],
    suggestion:
      '保留类请求可配置为低风险 Runbook（IntentPattern + topic.retention.set 工具序列），让常见诉求走受控的声明式链路而非每次让模型自由发挥。',
  },
  {
    key: 'permission',
    label: '权限 / 策略拒绝',
    keywords: ['权限', '无权限', '拒绝', 'denied', '403', 'forbidden', '无权', 'access'],
    suggestion:
      '核查 policy rolePermissions 是否给对应 subject 放行了该读工具；权限不足的探测应在界面上明确归因，而不是模糊失败。',
  },
  {
    key: 'latency',
    label: '响应慢 / 超时',
    keywords: ['慢', '延迟', '超时', 'timeout', '卡', '久'],
    suggestion:
      '检查该请求是否多走了一层非必要的 agent 循环步骤；对单域诊断可确认短路（short-circuit）已生效，或评估是否值得为该主题预置 runbook 路径。',
  },
  {
    key: 'format',
    label: '结果格式 / 可读性',
    keywords: ['格式', '排版', '表格', '不直观', '乱', 'readable', 'summary', '太啰嗦', '太简略', '结构'],
    suggestion:
      '调整该回复的呈现结构（如统一走 event/task 结构化 answer 或诊断包），或在 formatter 里收敛摘要篇幅，让结论一眼可见。',
  },
];

export const UNCLASSIFIED_KEY = 'unclassified';

function normalize(text: string): string {
  return (text || '').toLowerCase();
}

function matchRule(text: string): ThemeRule | null {
  const n = normalize(text);
  if (!n) {
    return null;
  }
  for (const rule of THEME_RULES) {
    if (rule.keywords.some((k) => n.includes(k.toLowerCase()))) {
      return rule;
    }
  }
  return null;
}

/**
 * 从反馈条目聚合出改进建议清单，按条数降序。
 * - 有纠正文本（correction）时按关键词归类为主题；
 * - 无纠正文本的差评归入 generic「差评无纠正」；
 * - 有纠正但归类不出时进入 unclassified 桶，标注需人工阅读。
 */
export function buildFeedbackInsights(feedback: FeedbackEntry[]): FeedbackInsight[] {
  const groups = new Map<string, { rule: ThemeRule | null; examples: string[] }>();
  const ensure = (key: string, rule: ThemeRule | null) => {
    if (!groups.has(key)) {
      groups.set(key, { rule, examples: [] });
    }
    return groups.get(key)!;
  };

  for (const f of feedback || []) {
    const correction = (f.correction || '').trim();
    const isNegative = f.rating < 0;

    if (correction) {
      const rule = matchRule(correction);
      if (rule) {
        ensure(rule.key, rule).examples.push(correction);
      } else {
        ensure(UNCLASSIFIED_KEY, null).examples.push(correction);
      }
    } else if (isNegative) {
      // 只有差评、没有纠正：无力自动归类，如实归入「待看」桶并给出通用建议
      ensure('negative-blank', null).examples.push('（该条仅差评，未填纠正内容）');
    }
  }

  const insights: FeedbackInsight[] = [];
  for (const [key, g] of groups) {
    const examples = [...new Set(g.examples)].slice(0, 4);
    if (key === UNCLASSIFIED_KEY) {
      insights.push({
        key,
        label: '待人工归类',
        count: g.examples.length,
        examples,
        suggestion: '现有纠正文本无法自动归入已知主题，需人工阅读原始反馈，判断是新增能力、runbook 意图还是 prompt 问题。',
      });
    } else if (key === 'negative-blank') {
      insights.push({
        key,
        label: '差评（未填纠正）',
        count: g.examples.length,
        examples,
        suggestion: '只有差评而缺纠正内容，模型无法据此改进。可在对话反馈里引导操作员补充具体不满之处。',
      });
    } else {
      const rule = g.rule!;
      insights.push({
        key,
        label: rule.label,
        count: g.examples.length,
        examples,
        suggestion: rule.suggestion,
      });
    }
  }

  insights.sort((a, b) => b.count - a.count);
  return insights;
}

/** 把建议清单渲染成 Markdown（供导出）。 */
export function insightsToMarkdown(insights: FeedbackInsight[], total: number): string {
  const lines: string[] = [
    '# 用户反馈改进建议',
    '',
    `来源：${total} 条用户反馈（基于纠正文本与差评归类）`,
    '',
  ];
  for (const ins of insights) {
    lines.push(`## ${ins.label}（${ins.count} 条）`);
    lines.push('');
    lines.push(ins.suggestion);
    if (ins.examples.length > 0) {
      lines.push('');
      lines.push('证据示例：');
      for (const ex of ins.examples) {
        lines.push(`- ${ex}`);
      }
    }
    lines.push('');
  }
  return lines.join('\n');
}
