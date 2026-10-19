/**
 * fees-format.test.ts — /fees 金额展示纯逻辑表测（#68）。
 *
 * 钉死三类对账级边界：
 * 1. 分→元恒两位小数 + 千分位（zh-CN）；
 * 2. isCentsMetric 闭集判断（金额与比率是两类完全不同的读者决策）；
 * 3. 尾零清理正则的边界（0 / 整数 / 全零小数 / 恰四位小数）。
 */
import { describe, expect, it } from 'vitest';
import {
  centsToYuan,
  clueKey,
  formatRiskMetric,
  isCentsMetric,
  linkGradeLabel,
  linkStatusLabel,
  trimMetricTail,
} from './fees-format';

describe('centsToYuan — 整数分 → 元（zh-CN 千分位 + 恒两位小数）', () => {
  it.each([
    [0, '0.00'],
    [1, '0.01'],
    [100, '1.00'],
    [123456, '1,234.56'],
    [99999, '999.99'],
    [-250, '-2.50'],
    [900719925474, '9,007,199,254.74'], // Number.MAX_SAFE_INTEGER / 100 附近的大额
  ])('%d 分 → %s 元', (cents, want) => {
    expect(centsToYuan(cents)).toBe(want);
  });
});

describe('isCentsMetric — 金额指标闭集（其余一律比率/原值）', () => {
  it.each([
    ['totalCents', true],
    ['deltaCents', true],
    ['ratio', false],
    ['z', false],
    ['batches', false],
    ['totalcents', false], // 大小写敏感——后端契约恒 camelCase
    ['', false],
  ])('isCentsMetric(%s) = %s', (label, want) => {
    expect(isCentsMetric(label)).toBe(want);
  });
});

describe('trimMetricTail — toFixed(4) 尾零清理', () => {
  it.each([
    [0.3, '0.3'],
    [0.5, '0.5'],
    [0.3000, '0.3'],
    [3, '3'],
    [10.0, '10'],
    [0, '0'],
    [12.3456, '12.3456'],
    [-2.5, '-2.5'],
  ])('%p → %s', (v, want) => {
    expect(trimMetricTail(v as number)).toBe(want);
  });
});

describe('formatRiskMetric — 金额 vs 比率双分支（读者决策分叉点）', () => {
  it('金额分支：totalCents 带千分位与 ¥ 前缀', () => {
    expect(formatRiskMetric(123456, 'totalCents')).toBe('¥1,234.56');
  });
  it('金额分支：deltaCents 同走 /100', () => {
    expect(formatRiskMetric(-250, 'deltaCents')).toBe('¥-2.50');
  });
  it('比率分支：ratio 原值展示（不除 100）', () => {
    expect(formatRiskMetric(0.3, 'ratio')).toBe('0.3');
  });
  it('比率分支：z 整数清理尾零', () => {
    expect(formatRiskMetric(3.0, 'z')).toBe('3');
  });
  it('同数值不同标签语义分叉：123456 分 = ¥1,234.56，123456 批次 = 123456', () => {
    expect(formatRiskMetric(123456, 'totalCents')).toBe('¥1,234.56');
    expect(formatRiskMetric(123456, 'batches')).toBe('123456');
  });
});

describe('行键与标签映射（闭集）', () => {
  it('clueKey 三段拼接', () => {
    expect(clueKey({ siteCode: 'S1', year: 2024, month: 5 })).toBe('S1|2024|5');
  });
  it.each([
    ['pending', '待确认'],
    ['confirmed', '已确认'],
    ['dismissed', '已驳回'],
  ])('linkStatusLabel(%s)', (s, want) => {
    expect(linkStatusLabel(s)).toBe(want);
  });
  it.each([
    ['exact', '精确'],
    ['rule', '规则'],
    ['fuzzy', '模糊'],
  ])('linkGradeLabel(%s)', (g, want) => {
    expect(linkGradeLabel(g)).toBe(want);
  });
});
