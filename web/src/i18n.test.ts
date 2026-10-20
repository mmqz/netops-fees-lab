import { describe, expect, it } from 'vitest';
import { format, formatDateTime, formatFileSize, formatRelativeTime, NO_VALUE } from './i18n';

// i18n.test.ts — #96: the formatter layer is consumed by EVERY page; before
// this file the 40-assertion suite covered fees-format + fitGuard only and
// these were exercised solely through browser regression scripts.

describe('format（模板填充）', () => {
  it('填充提供的占位符', () => {
    expect(format('{n} 条', { n: 42 })).toBe('42 条');
    expect(format('{a}/{b}', { a: 1, b: 2 })).toBe('1/2');
  });
  it('未提供的占位符原样保留（不静默丢信息）', () => {
    expect(format('{n} 条 / {total} 条', { n: 3 })).toBe('3 条 / {total} 条');
    expect(format('无占位符', {})).toBe('无占位符');
  });
});

describe('formatDateTime（RFC3339 → 本地显示）', () => {
  it('本地时间的「YYYY-MM-DD HH:mm」形态', () => {
    // 无时区后缀的 ISO 串按本地时间解释 → 断言值与时区无关
    expect(formatDateTime('2026-10-20T12:34')).toBe('2026-10-20 12:34');
  });
  it('空值/缺省 → 占位符', () => {
    expect(formatDateTime(undefined)).toBe(NO_VALUE);
    expect(formatDateTime(null)).toBe(NO_VALUE);
    expect(formatDateTime('')).toBe(NO_VALUE);
  });
  it('非法串原样返回（不吞错）', () => {
    expect(formatDateTime('not-a-date')).toBe('not-a-date');
  });
  it('月/日/时/分补零', () => {
    expect(formatDateTime('2026-01-02T03:04')).toBe('2026-01-02 03:04');
  });
});

describe('formatFileSize', () => {
  it('字节/KB/MB 分档', () => {
    expect(formatFileSize(512)).toBe('512 B');
    expect(formatFileSize(2048)).toBe('2.0 KB');
    expect(formatFileSize(3 * 1024 * 1024)).toBe('3.0 MB');
  });
  it('分档边界（1023B / 1MB）', () => {
    expect(formatFileSize(1023)).toBe('1023 B');
    expect(formatFileSize(1024)).toBe('1.0 KB');
    expect(formatFileSize(1024 * 1024)).toBe('1.0 MB');
  });
});

describe('formatRelativeTime', () => {
  it('空值 → 占位符；非法串原样返回', () => {
    expect(formatRelativeTime(null)).toBe(NO_VALUE);
    expect(formatRelativeTime(undefined)).toBe(NO_VALUE);
    expect(formatRelativeTime('junk')).toBe('junk');
  });
  it('分/时/天梯度与 7 天回退绝对时间', () => {
    const now = Date.now();
    expect(formatRelativeTime(new Date(now - 30_000).toISOString())).toBe('刚刚');
    expect(formatRelativeTime(new Date(now - 5 * 60_000).toISOString())).toBe('5 分钟前');
    expect(formatRelativeTime(new Date(now - 3 * 3600_000).toISOString())).toBe('3 小时前');
    expect(formatRelativeTime(new Date(now - 5 * 24 * 3600_000).toISOString())).toBe('5 天前');
    // 超过 7 天 → 回退 formatDateTime 的本地绝对形态（无时区后缀构造 + 本地 ISO 解析闭环）
    const old = new Date(now - 8 * 24 * 3600_000);
    expect(formatRelativeTime(old.toISOString())).toBe(formatDateTime(old.toISOString()));
  });
});
