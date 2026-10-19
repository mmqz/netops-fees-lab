import { describe, expect, it } from 'vitest';

import {
  computeFitPaddings,
  fitUtilization,
  FIT_PAD_V_MIN,
  FIT_PADDING,
  FIT_UTILIZATION_TARGET,
  recordFitMode,
} from './fitGuard';

/**
 * #62 的前端单测（验收标准 4）：用 issue 手算复现的精确数字钉死
 * 「不可达分支」是显式的，而不是 Math.max 静默钳制。
 */

describe('computeFitPaddings', () => {
  it('宽扁内容走 plain：统一留白，无护栏动作', () => {
    // 内容 1600×600，视口 1318×624：bb.w/bb.h(2.67) ≥ vw/vh(2.11)。
    const r = computeFitPaddings({ w: 1600, h: 600 }, 1318, 624);
    expect(r.mode).toBe('plain');
    expect(r.padX).toBe(FIT_PADDING);
    expect(r.padY).toBe(FIT_PADDING);
  });

  it('#62 实测场景：869×1081 高瘦内容在 1318×624 走 vertical-bound（不可达）', () => {
    // issue 手算：zoomNeeded=1.092，padNeeded=(624−1.092·1081)/2=−278.2 < 0
    // → 旧实现 Math.max(12, −278.2)=12 静默钳制；新实现显式不可达分支。
    const r = computeFitPaddings({ w: 869, h: 1081 }, 1318, 624);
    expect(r.mode).toBe('vertical-bound');
    expect(r.padY).toBe(FIT_PAD_V_MIN);
    // 不可达的数学判据钉死：达成 72% 所需 zoom 令内容高度超画布。
    const zoomNeeded = (FIT_UTILIZATION_TARGET * 1318) / 869;
    expect((vh => (624 - zoomNeeded * vh) / 2)(1081)).toBeLessThan(0);
    // 该分支下纵向才是验收主指标：zoom=624/(1081+2·12) 纵向利用率 ≥ 0.97。
    const zoom = 624 / (1081 + FIT_PAD_V_MIN * 2);
    const u = fitUtilization({ w: 869, h: 1081 }, 1318, 624, zoom);
    expect(u.vertical).toBeGreaterThan(0.9);
    expect(u.horizontal).toBeLessThan(FIT_UTILIZATION_TARGET);
  });

  it('高瘦但可达：护栏精确收窄纵向留白，72% 横向利用率在真实 fit 语义下成立', () => {
    // 模型单位口径（fitGuard 模块注释的公式）：
    //   zoomTarget = u·vw/bb.w；padMax = (vh/zoomTarget − bb.h)/2。
    // 统一留白不够、收窄后精确达标的中段用例：
    //   bb.w=500, bb.h=380, vw=1000, vh=600 → zoomTarget=1.44；
    //   padMax = (600/1.44 − 380)/2 = (416.67−380)/2 = 18.33 ∈ (12,40)。
    const mid = computeFitPaddings({ w: 500, h: 380 }, 1000, 600);
    expect(mid.mode).toBe('utilization');
    expect(mid.padY).toBeCloseTo(18.333333, 4);
    // 真实 fit 语义验证：zoom = vh/(bb.h+2·padY)，横向利用率恰达 0.72。
    const zoom = 600 / (380 + mid.padY * 2);
    const u = fitUtilization({ w: 500, h: 380 }, 1000, 600, zoom);
    expect(u.horizontal).toBeCloseTo(FIT_UTILIZATION_TARGET, 4);
    // 收窄封顶段（高瘦且余量充足，padMax 超过统一留白则封顶 40）：
    //   bb.w=800, bb.h=500, vw=1200, vh=700 → 1.6 < 1.714 高瘦分支；
    //   zoomTarget=0.72·1200/800=1.08；padMax=(700/1.08−500)/2=74.1 > 40
    //   → 留白封顶统一值 40；zoom=700/580=1.207，横向利用率 0.805 ≥ 0.72。
    const capped = computeFitPaddings({ w: 800, h: 500 }, 1200, 700);
    expect(capped.mode).toBe('utilization');
    expect(capped.padY).toBe(FIT_PADDING);
    const cappedZoom = 700 / (500 + capped.padY * 2);
    expect(fitUtilization({ w: 800, h: 500 }, 1200, 700, cappedZoom).horizontal).toBeGreaterThanOrEqual(
      FIT_UTILIZATION_TARGET - 1e-9,
    );
  });

  it('可达性边界：padMax 恰等于下限时仍走 utilization（含边界）', () => {
    // 模型单位口径解出 padMax = 12 的 bb.h：zoomTarget=1.44 →
    //   12 = (600/1.44 − h)/2 → h = 600/1.44 − 24 = 392.67。
    const r = computeFitPaddings({ w: 500, h: 600 / 1.44 - 24 }, 1000, 600);
    expect(r.mode).toBe('utilization');
    expect(r.padY).toBeCloseTo(FIT_PAD_V_MIN, 9);
    // 再高瘦一丝（h +1）即滑入不可达分支。
    const over = computeFitPaddings({ w: 500, h: 600 / 1.44 - 24 + 1 }, 1000, 600);
    expect(over.mode).toBe('vertical-bound');
  });

  it('非法输入（零/负/NaN）回退 plain 统一留白', () => {
    for (const [bb, vw, vh] of [
      [{ w: 0, h: 100 }, 1000, 600],
      [{ w: 100, h: 0 }, 1000, 600],
      [{ w: 100, h: 100 }, 0, 600],
      [{ w: 100, h: 100 }, 1000, 0],
      [{ w: Number.NaN, h: 100 }, 1000, 600],
    ] as Array<[{ w: number; h: number }, number, number]>) {
      const r = computeFitPaddings(bb, vw, vh);
      expect(r.mode).toBe('plain');
      expect(r.padY).toBe(FIT_PADDING);
    }
  });
});

// @vitest-environment jsdom
describe('recordFitMode', () => {
  it('把分支写到容器 data-fit-mode；null 容器为 no-op', () => {
    const el = document.createElement('div');
    recordFitMode('vertical-bound', el);
    expect(el.getAttribute('data-fit-mode')).toBe('vertical-bound');
    recordFitMode('utilization', null); // 不抛
  });
});
