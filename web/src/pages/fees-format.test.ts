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
  selectLinkFilter,
  riskEmptyState,
} from './fees-format';
import type { LinkFilterSelection } from './fees-format';

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

describe('selectLinkFilter — 关联筛选变更必须回到第 1 页（#109-A）', () => {
  // 缺陷场景：pending 全量有 5 页，用户翻到第 5 页后切「已驳回」筛选
  // （仅 12 条 < 1 页）——旧页码 5 越过新总页数，后端返回空 items，
  // 前端渲染「暂无关联」假空态。筛选与页码必须同拍迁移。
  const page5: LinkFilterSelection = { status: 'pending', grade: '', page: 5 };

  it('状态筛选变化：页码从 5 重置为 1（假空态回归钉）', () => {
    expect(selectLinkFilter(page5, { status: 'dismissed' })).toEqual({
      status: 'dismissed',
      grade: '',
      page: 1,
    });
  });
  it('级别筛选变化：页码同样重置为 1（两个 select 同款纪律）', () => {
    expect(selectLinkFilter(page5, { grade: 'exact' })).toEqual({
      status: 'pending',
      grade: 'exact',
      page: 1,
    });
  });
  it('未变更的另一维筛选原样保留（筛选互不覆盖）', () => {
    const next = selectLinkFilter({ status: 'confirmed', grade: 'fuzzy', page: 3 }, { status: 'pending' });
    expect(next.grade).toBe('fuzzy');
    expect(next.status).toBe('pending');
    expect(next.page).toBe(1);
  });
  it('已在第 1 页时保持 1（无跳变），清空筛选（空串）也回第 1 页', () => {
    expect(selectLinkFilter({ status: 'confirmed', grade: '', page: 1 }, { status: '' }).page).toBe(1);
  });
});

describe('riskEmptyState（#21 三态 + #27 第四态空态裁决）', () => {
  it('无数据月 → no-data（引导导入，不是分析结论）', () => {
    expect(riskEmptyState(0, 0, false)).toBe('no-data');
    // 即使有排除数，无数据月仍优先引导导入（排除数来自别的账期口径）
    expect(riskEmptyState(0, 203, false)).toBe('no-data');
  });
  it('0 条线索 + 有排除 → excluded-zero（无法分析 ≠ 无异常，告警色）', () => {
    expect(riskEmptyState(0, 203, true)).toBe('excluded-zero');
  });
  it('0 条线索 + 有告警无排除 → not-analyzed（#27 前置检查拦下，绝不说「无异常」）', () => {
    expect(riskEmptyState(0, 0, true, true)).toBe('not-analyzed');
    // 排除数优先于告警：有排除的 0 条仍是 excluded-zero（更具体的形态）
    expect(riskEmptyState(0, 203, true, true)).toBe('excluded-zero');
    // 无数据月优先于 not-analyzed（引导导入是更根本的动作）
    expect(riskEmptyState(0, 0, false, true)).toBe('no-data');
  });
  it('有告警但有线索 → clean（空态逻辑不介入，告警条照常浮出）', () => {
    expect(riskEmptyState(7, 0, true, true)).toBe('clean');
  });
  it('有数据、无排除、0 条、无告警 → clean（中性的「未发现」）', () => {
    expect(riskEmptyState(0, 0, true)).toBe('clean');
  });
  it('有线索 → clean（空态逻辑不介入）', () => {
    expect(riskEmptyState(7, 0, true)).toBe('clean');
    expect(riskEmptyState(7, 300, true)).toBe('clean');
  });
});
