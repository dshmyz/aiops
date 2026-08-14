import { describe, expect, test } from 'vitest';
import { formatAbsoluteTime, formatCompactDateTime, formatDateGroup, formatRelativeTime, formatResponseType } from './conversationFormat';

describe('formatRelativeTime', () => {
  const now = new Date('2026-07-27T10:30:00Z');

  test('returns "刚刚" for less than 60 seconds ago', () => {
    expect(formatRelativeTime('2026-07-27T10:29:45Z', now)).toBe('刚刚');
    expect(formatRelativeTime('2026-07-27T10:30:00Z', now)).toBe('刚刚');
  });

  test('returns minutes ago for less than 60 minutes ago', () => {
    expect(formatRelativeTime('2026-07-27T10:25:00Z', now)).toBe('5 分钟前');
    expect(formatRelativeTime('2026-07-27T09:31:00Z', now)).toBe('59 分钟前');
  });

  test('returns hours ago for less than 24 hours ago', () => {
    expect(formatRelativeTime('2026-07-27T07:30:00Z', now)).toBe('3 小时前');
    expect(formatRelativeTime('2026-07-26T11:30:00Z', now)).toBe('23 小时前');
  });

  test('returns days ago for less than 7 days ago', () => {
    expect(formatRelativeTime('2026-07-25T10:30:00Z', now)).toBe('2 天前');
    expect(formatRelativeTime('2026-07-21T10:30:00Z', now)).toBe('6 天前');
  });

  test('returns month-day for same-year dates 7+ days ago', () => {
    expect(formatRelativeTime('2026-07-15T10:30:00Z', now)).toBe('7月15日');
    expect(formatRelativeTime('2026-06-01T10:30:00Z', now)).toBe('6月1日');
  });

  test('returns full year-month-day for cross-year dates', () => {
    expect(formatRelativeTime('2025-12-31T10:30:00Z', now)).toBe('2025年12月31日');
    expect(formatRelativeTime('2025-01-01T00:00:00Z', now)).toBe('2025年1月1日');
  });

  test('uses current time when now is not provided', () => {
    const iso = new Date(Date.now() - 30 * 1000).toISOString();
    expect(formatRelativeTime(iso)).toBe('刚刚');
  });
});

describe('formatResponseType', () => {
  test('maps answer to 答案 and answer variant', () => {
    expect(formatResponseType('answer')).toEqual({ label: '答案', variant: 'answer' });
  });

  test('maps answer_converged to 兜底总结 and converged variant', () => {
    expect(formatResponseType('answer_converged')).toEqual({
      label: '兜底总结',
      variant: 'converged',
    });
  });

  test('maps clarification_needed to 待补充参数 and clarification variant', () => {
    expect(formatResponseType('clarification_needed')).toEqual({
      label: '待补充参数',
      variant: 'clarification',
    });
  });

  test('maps confirmation_required to 待审批 and confirmation variant', () => {
    expect(formatResponseType('confirmation_required')).toEqual({
      label: '待审批',
      variant: 'confirmation',
    });
  });

  test('maps execution_result to 执行结果 and execution variant', () => {
    expect(formatResponseType('execution_result')).toEqual({
      label: '执行结果',
      variant: 'execution',
    });
  });

  test('falls back to the raw value with default variant for unknown types', () => {
    expect(formatResponseType('custom_type')).toEqual({
      label: 'custom_type',
      variant: 'default',
    });
  });

  test('falls back to default variant for empty string', () => {
    expect(formatResponseType('')).toEqual({ label: '', variant: 'default' });
  });
});

describe('formatDateGroup', () => {
  const now = new Date('2026-07-27T10:30:00Z');

  test('returns "今天" label for the same day', () => {
    const result = formatDateGroup('2026-07-27T08:00:00Z', now);
    expect(result.label).toBe('今天');
    expect(result.key).toBe('2026-07-27');
  });

  test('returns "昨天" label for the previous day', () => {
    // 2026-07-26T10:30:00Z 在 UTC+8 = 2026-07-26T18:30:00 local（昨天）
    const result = formatDateGroup('2026-07-26T10:30:00Z', now);
    expect(result.label).toBe('昨天');
    expect(result.key).toBe('2026-07-26');
  });

  test('returns "M月D日" label for same-year dates 2+ days ago', () => {
    const result = formatDateGroup('2026-07-15T10:30:00Z', now);
    expect(result.label).toBe('7月15日');
    expect(result.key).toBe('2026-07-15');
  });

  test('returns "YYYY年M月D日" label for cross-year dates', () => {
    const result = formatDateGroup('2025-12-31T10:30:00Z', now);
    expect(result.label).toBe('2025年12月31日');
    expect(result.key).toBe('2025-12-31');
  });

  test('same day at different hours returns same key and "今天"', () => {
    // 两个时刻在 UTC+8 都落在 2026-07-27 local
    expect(formatDateGroup('2026-07-27T02:00:00Z', now).key).toBe(
      formatDateGroup('2026-07-27T10:00:00Z', now).key,
    );
  });

  test('respects local timezone for day boundary', () => {
    // 构造与 now 相同本地日期、但时间不同的输入，验证 "今天" 判定只依赖
    // 本地日期而非时刻。用 now 的本地年月日 + 12:00 构造，保证任何时区都
    // 与 now 落在同一天，且远离本地午夜边界。
    const sameDay = new Date(now.getFullYear(), now.getMonth(), now.getDate(), 12, 0, 0, 0).toISOString();
    const result = formatDateGroup(sameDay, now);
    expect(result.label).toBe('今天');
  });
});

describe('formatCompactDateTime', () => {
  test('always includes the year, even when it matches the current year', () => {
    const iso = '2026-07-21T11:29:30Z'; // 本地年份同样为 2026
    expect(formatCompactDateTime(iso)).toMatch(/^2026-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/);
  });

  test('prepends a different year for cross-year dates', () => {
    const iso = '2024-06-01T10:00:00Z';
    expect(formatCompactDateTime(iso)).toMatch(/^2024-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/);
  });

  test('formats in local time with seconds by default', () => {
    const iso = new Date(2026, 6, 21, 19, 29, 30).toISOString(); // local 2026-07-21 19:29:30
    expect(formatCompactDateTime(iso)).toBe('2026-07-21 19:29:30');
  });

  test('omits seconds when withSeconds is false', () => {
    const iso = new Date(2026, 6, 21, 19, 29, 30).toISOString();
    expect(formatCompactDateTime(iso, false)).toBe('2026-07-21 19:29');
  });

  test('returns the input verbatim on invalid dates', () => {
    expect(formatCompactDateTime('not-a-date')).toBe('not-a-date');
  });
});

describe('formatAbsoluteTime', () => {
  test('formats a valid ISO string into full local time', () => {
    // 用本地构造时间，断言与本地时区无关。2026-07-21 为周二。
    const iso = new Date(2026, 6, 21, 19, 29, 30).toISOString();
    expect(formatAbsoluteTime(iso)).toBe('2026年07月21日 周二 19:29:30');
  });

  test('returns the input verbatim on invalid dates', () => {
    expect(formatAbsoluteTime('not-a-date')).toBe('not-a-date');
    expect(formatAbsoluteTime('')).toBe('');
  });
});
