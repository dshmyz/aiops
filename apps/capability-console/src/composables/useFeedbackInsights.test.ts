import { describe, expect, it } from 'vitest';
import { buildFeedbackInsights, insightsToMarkdown, UNCLASSIFIED_KEY } from './useFeedbackInsights';
import type { FeedbackEntry } from '../types';

function fb(overrides: Partial<FeedbackEntry>): FeedbackEntry {
  return {
    id: 'x',
    conversation_id: 'c',
    turn_id: 't',
    subject: 'admin-1',
    rating: -1,
    correction: '',
    created_at: '2026-08-01T00:00:00Z',
    ...overrides,
  };
}

describe('buildFeedbackInsights', () => {
  it('groups corrections by keyword theme and orders by count desc', () => {
    const insights = buildFeedbackInsights([
      fb({ rating: -1, correction: '缺少参数: name，需要澄清' }),
      fb({ rating: -1, correction: '参数缺失导致重复往返' }),
      fb({ rating: -1, correction: '调用失败：tool 未注册' }),
    ]);
    expect(insights.length).toBe(2);
    expect(insights[0].key).toBe('clarification');
    expect(insights[0].count).toBe(2);
    expect(insights[0].examples).toContain('缺少参数: name，需要澄清');
    expect(insights[1].key).toBe('capability-call');
  });

  it('sends uncategorized corrections to the unclassified bucket instead of guessing', () => {
    const insights = buildFeedbackInsights([
      fb({ correction: '希望它别回复这么多主观猜测' }),
    ]);
    const unc = insights.find((i) => i.key === UNCLASSIFIED_KEY);
    expect(unc).toBeDefined();
    expect(unc!.count).toBe(1);
  });

  it('buckets a negative rating with no correction into negative-blank with a generic suggestion', () => {
    const insights = buildFeedbackInsights([fb({ rating: -1, correction: '' })]);
    const blank = insights.find((i) => i.key === 'negative-blank');
    expect(blank).toBeDefined();
    expect(blank!.count).toBe(1);
    expect(blank!.suggestion).toContain('补充具体不满');
  });

  it('recommends low-risk runbook for retention-related feedback', () => {
    const insights = buildFeedbackInsights([
      fb({ correction: '把保留改成 72 小时时它确认流程太绕' }),
    ]);
    expect(insights[0].key).toBe('retention');
    expect(insights[0].suggestion).toContain('Runbook');
  });

  it('ignores empty input and empty corrections of positive ratings', () => {
    expect(buildFeedbackInsights([])).toEqual([]);
    const insights = buildFeedbackInsights([fb({ rating: 1, correction: '' })]);
    expect(insights).toEqual([]);
  });

  it('dedupes identical example corrections', () => {
    const insights = buildFeedbackInsights([
      fb({ correction: '权限 denied' }),
      fb({ correction: '权限 denied' }),
    ]);
    expect(insights[0].count).toBe(2);
    expect(insights[0].examples).toEqual(['权限 denied']);
  });
});

describe('insightsToMarkdown', () => {
  it('renders theme, suggestion and evidence', () => {
    const insights = buildFeedbackInsights([fb({ correction: '缺少参数: name' })]);
    const md = insightsToMarkdown(insights, 3);
    expect(md).toContain('# 用户反馈改进建议');
    expect(md).toContain('来源：3 条用户反馈');
    expect(md).toContain('参数澄清');
    expect(md).toContain('缺少参数: name');
  });
});
