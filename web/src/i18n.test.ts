import { describe, expect, it } from 'vitest';
import {
  format,
  formatDateTime,
  formatFileSize,
  formatRelativeTime,
  NO_VALUE,
} from './i18n';

// i18n.test.ts — #96: the formatter layer is consumed by EVERY page; before
// this file the 40-assertion suite covered fees-format + fitGuard only and
// these were exercised solely through browser regression scripts.
// #111-D 追加：死常量探测器——i18n.ts 是全站文案唯一事实源，导出常量必须
// 被 src 内至少一处引用（页面 / 组件 / 本文件内部工具函数 / 测试），
// 否则就是「常量写好但页面硬编码」或「功能下线遗留」的漂移债。

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

// ---------------------------------------------------------------------------
// #111-D：死常量探测器（静态扫描——i18n 导出常量的引用闭包）
// ---------------------------------------------------------------------------

/** 以 ?raw 形式内联全部源码（import.meta.glob 由 vite-node 转换，无需 node:fs，
 *  类型域保持在浏览器侧 tsconfig 内）。键形如 '/src/pages/Fees.tsx'。 */
const RAW_SOURCES = import.meta.glob('/src/**/*.{ts,tsx}', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

/** i18n.ts 本体的扫描键（#113-A 起探测器与收编回归钉共用）。 */
const i18nKey = Object.keys(RAW_SOURCES).find((key) => key.endsWith('/i18n.ts')) ?? '';

describe('i18n 导出常量引用闭包（#111-D：死常量回归钉）', () => {
  const i18nLines = (RAW_SOURCES[i18nKey] ?? '').split('\n');

  /** 声明行之外的出现视为真实引用（formatRelativeTime 消费 TIME_* 一类）。 */
  const exportedNames = i18nLines
    .map((line) => /^export const ([A-Z][A-Z0-9_]*)/.exec(line)?.[1])
    .filter((name): name is string => name !== undefined);

  const otherContents = Object.entries(RAW_SOURCES)
    .filter(([key]) => key !== i18nKey)
    .map(([, content]) => content);

  it('i18n.ts 每个导出常量都被引用（页面/组件/文件内工具/测试至少其一）', () => {
    const dead: string[] = [];
    for (const name of exportedNames) {
      const referencedInFile = i18nLines.some(
        (line) => new RegExp(`\\b${name}\\b`).test(line) && !/^export const /.test(line),
      );
      const referencedElsewhere = otherContents.some((content) =>
        new RegExp(`\\b${name}\\b`).test(content),
      );
      if (!referencedInFile && !referencedElsewhere) dead.push(name);
    }
    expect(
      dead,
      '死 i18n 常量（写好但无人引用）：删掉它，或把页面的硬编码文案改用常量',
    ).toEqual([]);
  });

  it('探测器自检：能枚举导出常量与源码文件（扫描器失效时此断言先红）', () => {
    expect(i18nKey, '扫描器应能找到 i18n.ts 本体').not.toBe('');
    expect(exportedNames.length).toBeGreaterThan(300);
    expect(exportedNames).toContain('NO_VALUE');
    expect(exportedNames).toContain('APP_NAME');
    expect(Object.keys(RAW_SOURCES).length).toBeGreaterThan(40);
  });
});

// ---------------------------------------------------------------------------
// #113-A：硬编码收编回归钉（费用仓形态：App 令牌弹窗接线 + 死常量探测器）
// ---------------------------------------------------------------------------

describe('i18n 第二批收编回归（#113-A）', () => {
  it('App 令牌弹窗的接线（raw 扫描：新常量确实被对应文件消费，防「只被测试引用」的假绿）', () => {
    const WIRINGS: Array<[string, string]> = [
      ['/src/App.tsx', 'BTN_CLOSE'],
    ];
    for (const [file, name] of WIRINGS) {
      expect(RAW_SOURCES[file], `${file} 应消费 ${name}`).toContain(name);
    }
  });
});
