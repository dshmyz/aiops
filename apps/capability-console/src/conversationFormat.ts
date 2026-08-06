export type ResponseTypeVariant = 'answer' | 'converged' | 'clarification' | 'confirmation' | 'execution' | 'default';

export interface ResponseTypeDisplay {
  label: string;
  variant: ResponseTypeVariant;
}

const RESPONSE_TYPE_MAP: Record<string, ResponseTypeDisplay> = {
  answer: { label: '答案', variant: 'answer' },
  answer_converged: { label: '兜底总结', variant: 'converged' },
  clarification_needed: { label: '待补充参数', variant: 'clarification' },
  confirmation_required: { label: '待审批', variant: 'confirmation' },
  execution_result: { label: '执行结果', variant: 'execution' },
};

export function formatResponseType(type: string): ResponseTypeDisplay {
  return RESPONSE_TYPE_MAP[type] ?? { label: type, variant: 'default' };
}

const MINUTE_SECONDS = 60;
const HOUR_SECONDS = 60 * MINUTE_SECONDS;
const DAY_SECONDS = 24 * HOUR_SECONDS;
const WEEK_SECONDS = 7 * DAY_SECONDS;

const WEEKDAY_NAMES = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];

export function formatRelativeTime(iso: string, now: Date = new Date()): string {
  const then = new Date(iso);
  const diffMs = now.getTime() - then.getTime();
  const diffSeconds = Math.max(0, Math.floor(diffMs / 1000));

  if (diffSeconds < MINUTE_SECONDS) {
    return '刚刚';
  }
  if (diffSeconds < HOUR_SECONDS) {
    return `${Math.floor(diffSeconds / MINUTE_SECONDS)} 分钟前`;
  }
  if (diffSeconds < DAY_SECONDS) {
    return `${Math.floor(diffSeconds / HOUR_SECONDS)} 小时前`;
  }
  if (diffSeconds < WEEK_SECONDS) {
    return `${Math.floor(diffSeconds / DAY_SECONDS)} 天前`;
  }

  const sameYear = now.getFullYear() === then.getFullYear();
  const month = then.getMonth() + 1;
  const day = then.getDate();
  return sameYear ? `${month}月${day}日` : `${then.getFullYear()}年${month}月${day}日`;
}

/**
 * formatAbsoluteTime 把 UTC ISO 时间转成完整本地时间。
 * 例：2026-07-21T11:29:30Z → "2026年07月21日 周二 19:29:30"（本地时区）。
 * 解析失败（非法输入）时原样返回，避免把坏数据渲染成 NaN 串。
 */
export function formatAbsoluteTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const year = d.getFullYear();
  const month = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  const hours = String(d.getHours()).padStart(2, '0');
  const minutes = String(d.getMinutes()).padStart(2, '0');
  const seconds = String(d.getSeconds()).padStart(2, '0');
  const weekday = WEEKDAY_NAMES[d.getDay()];
  return `${year}年${month}月${day}日 ${weekday} ${hours}:${minutes}:${seconds}`;
}

/**
 * formatCompactDateTime 把 UTC ISO 时间转成适合密集表格的紧凑本地时间。
 * 例：2026-07-21T11:29:30.339659Z → "2026-07-21 19:29:30"（本地时区）。
 * - 秒数可选（withSeconds=false 时省略 `:ss`），用于卡片/概览等次要展示。
 * - 始终包含完整年份，避免跨年数据歧义（即使与当前年份相同也不省略）。
 * - 解析失败（非法输入）时原样返回，避免把坏数据吞成空串。
 */
export function formatCompactDateTime(iso: string, withSeconds = true): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const pad = (n: number) => String(n).padStart(2, '0');
  const year = d.getFullYear();
  const month = pad(d.getMonth() + 1);
  const day = pad(d.getDate());
  const hh = pad(d.getHours());
  const mm = pad(d.getMinutes());
  const sec = withSeconds ? `:${pad(d.getSeconds())}` : '';
  return `${year}-${month}-${day} ${hh}:${mm}${sec}`;
}

export interface DateGroup {
  /** YYYY-MM-DD，用于跨条目去重 */
  key: string;
  /** 显示文案：今天 / 昨天 / M月D日 / YYYY年M月D日 */
  label: string;
}

const DAY_MS = 24 * 60 * 60 * 1000;

function startOfDay(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate());
}

/**
 * formatDateGroup 计算一条消息所属的日期分组。
 * 标签规则（基于本地时区）：
 * - 同一天 → 「今天」
 * - 前一天 → 「昨天」
 * - 同年 → 「M月D日」
 * - 跨年 → 「YYYY年M月D日」
 *
 * key 始终是 YYYY-MM-DD 格式，用于检测分组边界。
 */
export function formatDateGroup(iso: string, now: Date = new Date()): DateGroup {
  const then = new Date(iso);
  const thenDay = startOfDay(then);
  const nowDay = startOfDay(now);
  const diffDays = Math.round((nowDay.getTime() - thenDay.getTime()) / DAY_MS);

  const year = then.getFullYear();
  const month = then.getMonth() + 1;
  const day = then.getDate();
  const key = `${year}-${String(month).padStart(2, '0')}-${String(day).padStart(2, '0')}`;

  let label: string;
  if (diffDays === 0) {
    label = '今天';
  } else if (diffDays === 1) {
    label = '昨天';
  } else if (now.getFullYear() === year) {
    label = `${month}月${day}日`;
  } else {
    label = `${year}年${month}月${day}日`;
  }

  return { key, label };
}
