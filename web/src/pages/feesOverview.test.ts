/**
 * 费用展示层确定性核心的对照测试（#3 二期——同数据同输出）。
 *
 * fees-format.test.ts 钉线索键与金额展示；这里钉：月度补位（空月不造零）、
 * 环比（0 基期诚实 null）、趋势几何（确定性坐标 + 断月断线 + 异常点）、
 * 跨场景合并 / 排序 / CSV 导出（口径列随行）。
 */

import { describe, expect, it } from 'vitest';
import type { RiskResult } from '../api/types';
import {
  centsToWan,
  cluesCsv,
  clueStateLabel,
  mergeClues,
  momRatio,
  padMonthly,
  seriesLatestMonth,
  slotOf,
  sortMergedClues,
  trendGeometry,
} from './feesOverview';

describe('padMonthly', () => {
  it('pads sparse months with null — absent is a statement, never a zero', () => {
    const slots = padMonthly([
      { month: 5, rows: 3, sites: 2, totalCents: 45000 },
      { month: 8, rows: 1, sites: 1, totalCents: 2000 },
    ]);
    expect(slots).toHaveLength(12);
    expect(slots[0]).toEqual({ month: 1, point: null });
    expect(slots[4]).toEqual({ month: 5, point: { month: 5, rows: 3, sites: 2, totalCents: 45000 } });
    expect(slots[7].point?.totalCents).toBe(2000);
    expect(slots[11]).toEqual({ month: 12, point: null });
  });

  it('empty input answers twelve null slots', () => {
    expect(padMonthly([]).every((s) => s.point === null)).toBe(true);
  });
});

describe('momRatio', () => {
  it('computes the honest MoM delta', () => {
    expect(momRatio(110_000, 100_000)).toBeCloseTo(0.1);
    expect(momRatio(50_000, 100_000)).toBe(-0.5);
  });

  it('null on absent months and zero base — no fake 0%/∞%', () => {
    expect(momRatio(null, 100)).toBeNull();
    expect(momRatio(100, null)).toBeNull();
    expect(momRatio(100, 0)).toBeNull();
  });
});

describe('seriesLatestMonth / slotOf', () => {
  it('finds the last month carrying data', () => {
    const slots = padMonthly([
      { month: 2, rows: 1, sites: 1, totalCents: 100 },
      { month: 9, rows: 1, sites: 1, totalCents: 200 },
    ]);
    expect(seriesLatestMonth(slots)).toBe(9);
    expect(seriesLatestMonth(padMonthly([]))).toBe(0);
    expect(slotOf(slots, 9)?.point?.totalCents).toBe(200);
    expect(slotOf(slots, 13)).toBeNull();
  });
});

describe('trendGeometry', () => {
  it('is deterministic: same input, same coordinates (byte-equal points)', () => {
    const slots = padMonthly([
      { month: 1, rows: 1, sites: 1, totalCents: 1_000_000 },
      { month: 2, rows: 1, sites: 1, totalCents: 2_000_000 },
    ]);
    const series = [{ key: 'electricity', label: '电费', color: '#2f5fe0', slots }];
    const a = JSON.stringify(trendGeometry(series));
    const b = JSON.stringify(trendGeometry(series));
    expect(a).toBe(b);
  });

  it('breaks the line across absent months instead of inventing zeros', () => {
    const slots = padMonthly([
      { month: 1, rows: 1, sites: 1, totalCents: 1_000_000 },
      { month: 3, rows: 1, sites: 1, totalCents: 3_000_000 },
    ]);
    const geo = trendGeometry([{ key: 'x', label: 'x', color: '#000', slots }]);
    // two separate segments (1→gap, gap→3): the polyline carries both.
    expect(geo.paths[0].points.split(' ').length).toBe(2);
    expect(geo.paths[0].dots).toHaveLength(2);
  });

  it('marks ≥2× jumps as spike months on the primary series only', () => {
    const primary = padMonthly([
      { month: 1, rows: 1, sites: 1, totalCents: 1_000_000 },
      { month: 2, rows: 1, sites: 1, totalCents: 2_500_000 }, // 2.5× → spike
    ]);
    const other = padMonthly([
      { month: 1, rows: 1, sites: 1, totalCents: 1_000_000 },
      { month: 2, rows: 1, sites: 1, totalCents: 9_000_000 }, // 9× but NOT primary
    ]);
    const geo = trendGeometry([
      { key: 'p', label: 'p', color: '#111', slots: primary },
      { key: 'o', label: 'o', color: '#222', slots: other },
    ]);
    expect(geo.spikeMonths).toEqual([2]);
  });

  it('all-empty series answers zero max and no paths points', () => {
    const geo = trendGeometry([{ key: 'x', label: 'x', color: '#000', slots: padMonthly([]) }]);
    expect(geo.maxCents).toBe(0);
    expect(geo.paths[0].points).toBe('');
    expect(geo.spikeMonths).toEqual([]);
  });
});

describe('centsToWan', () => {
  it('renders integral cents as 万元 without trailing zeros', () => {
    expect(centsToWan(33_990_000)).toBe('33.99');
    expect(centsToWan(1_000_000)).toBe('1');
    expect(centsToWan(0)).toBe('0');
  });
});

describe('mergeClues / sortMergedClues', () => {
  const r1: RiskResult = {
    scenario: 'spike',
    scenarioCn: '电费环比异常上涨',
    feeType: 'electricity',
    year: 2026,
    month: 8,
    total: 1,
    excluded: 0,
    dismissed: 0,
    note: '',
    clues: [
      {
        siteCode: 'S1',
        year: 2026,
        month: 8,
        metricValue: 4.25,
        metricLabel: '环比涨幅',
        threshold: 0.3,
        thresholdLabel: '环比 ≥ 30%',
        evidence: [{ sourceFile: 'a.csv', sourceRow: 3, importBatch: 'b1', amountCents: 46_517 }],
        evidenceTotal: 1,
      },
    ],
  };
  const r2: RiskResult = {
    scenario: 'duplicate-settlement',
    scenarioCn: '跨批次重复结算',
    feeType: 'electricity',
    year: 2026,
    month: 8,
    total: 1,
    excluded: 0,
    dismissed: 0,
    note: '',
    clues: [
      {
        siteCode: 'S0',
        recordKey: 'DD-01',
        year: 2026,
        month: 8,
        metricValue: 2,
        metricLabel: '批次数',
        threshold: 1,
        thresholdLabel: '批次数 > 1',
        evidence: [
          { sourceFile: 'a.csv', sourceRow: 3, importBatch: 'b1', amountCents: 9_000 },
          { sourceFile: 'b.csv', sourceRow: 9, importBatch: 'b2', amountCents: 1_000 },
        ],
        evidenceTotal: 2,
      },
    ],
  };

  it('flattens envelopes into one cross-scenario list carrying the scenario tags', () => {
    const merged = mergeClues([r1, r2]);
    expect(merged).toHaveLength(2);
    expect(merged[0].scenarioCn).toBe('电费环比异常上涨');
    expect(merged[1].clue.recordKey).toBe('DD-01');
  });

  it("'amount' sorts by the evidence money total — the 金额排序 acceptance", () => {
    const sorted = sortMergedClues(mergeClues([r1, r2]), 'amount');
    expect(sorted[0].clue.siteCode).toBe('S1'); // 465.17 元 > 100 元
  });

  it("'metric' sorts by the raw signal strength with deterministic tiebreaks", () => {
    const sorted = sortMergedClues(mergeClues([r1, r2]), 'metric');
    expect(sorted[0].clue.siteCode).toBe('S1'); // 4.25 > 2
  });

  it('same input + same key → byte-equal order (determinism)', () => {
    const a = sortMergedClues(mergeClues([r1, r2]), 'amount');
    const b = sortMergedClues(mergeClues([r1, r2]), 'amount');
    expect(JSON.stringify(a)).toBe(JSON.stringify(b));
  });
});

describe('clueStateLabel', () => {
  it('maps the #119 decision tri-state', () => {
    expect(clueStateLabel(undefined)).toBe('pending');
    expect(clueStateLabel('verified')).toBe('verified');
    expect(clueStateLabel('dismissed')).toBe('dismissed');
  });
});

describe('cluesCsv', () => {
  it('carries the 口径 note column — the doctrine leaves the building with the file', () => {
    const merged = sortMergedClues(
      mergeClues([
        {
          ...r1(),
        },
      ]),
      'amount',
    );
    const csv = cluesCsv(merged);
    const head = csv.split('\n')[0];
    expect(head).toContain('场景');
    expect(head).toContain('备注');
  });

  it('escapes commas inside note cells', () => {
    const csv = cluesCsv(
      mergeClues([
        {
          scenario: 'spike',
          scenarioCn: '带,逗号的场景',
          feeType: 'electricity',
          year: 2026,
          month: 8,
          total: 1,
          excluded: 0,
          dismissed: 0,
          note: '',
          clues: [
            {
              siteCode: 'S1',
              year: 2026,
              month: 8,
              metricValue: 1,
              metricLabel: 'm',
              threshold: 1,
              thresholdLabel: 't',
              evidence: [],
              evidenceTotal: 0,
            },
          ],
        },
      ]),
    );
    expect(csv).toContain('"带,逗号的场景"');
  });
});

/** r1 的独立构造器（describe 作用域里复用时的可变副本来源）。 */
function r1(): RiskResult {
  return {
    scenario: 'spike',
    scenarioCn: '电费环比异常上涨',
    feeType: 'electricity',
    year: 2026,
    month: 8,
    total: 1,
    excluded: 0,
    dismissed: 0,
    note: '',
    clues: [
      {
        siteCode: 'S1',
        year: 2026,
        month: 8,
        metricValue: 4.25,
        metricLabel: '环比涨幅',
        threshold: 0.3,
        thresholdLabel: '环比 ≥ 30%',
        evidence: [{ sourceFile: 'a.csv', sourceRow: 3, importBatch: 'b1', amountCents: 46_517 }],
        evidenceTotal: 1,
        note: '口径：跨批次合计',
      },
    ],
  };
}
