/**
 * 站点风险地图确定性核心的对照测试（#18 / #26）。
 *
 * 钉死的契约：
 * - 坐标过滤：null 坐标（任一侧）不落点，其余字段原样透传；
 * - 着色：已核实 > 待核实 > 绿色优先级，零计数为绿；
 * - 视口目标（#26 的回归线）：
 *   0 点 → 'none'（**绝不**让 Math.min(...[]) = Infinity 有机会到达 MapLibre——
 *         「Invalid LngLat latitude value」异常的根）；
 *   1 点 → 居中定焦（lng/lat 经纬序不颠倒，zoom 为常量）；
 *   多点 → 精确 min/max 外接矩形（乱序输入也得到规范矩形）；
 *   两点同位 → 外接矩形退化为零面积矩形（MapLibre 按 maxZoom 收口，不抛异常）。
 */

import { describe, expect, it } from 'vitest';
import type { RiskMapPoint } from '../api/types';
import {
  COLOR_CLEAN,
  COLOR_PENDING,
  COLOR_VERIFIED,
  SINGLE_POINT_ZOOM,
  computeFitTarget,
  filterGeoPoints,
  pointColor,
} from './feesRiskMapModel';

function point(over: Partial<RiskMapPoint>): RiskMapPoint {
  return {
    siteCode: 'S-1',
    lat: 45.75,
    lng: 126.63,
    clueCount: 1,
    pending: 1,
    verified: 0,
    dismissed: 0,
    scenarios: ['unlinked'],
    resourceKey: 'site|S-1',
    ...over,
  };
}

describe('filterGeoPoints（#16 边界：坐标来自用户资源表，null 不落点）', () => {
  it('null 坐标（任一侧）被过滤，有效点原样透传', () => {
    const out = filterGeoPoints([
      point({ siteCode: 'A' }),
      point({ siteCode: 'B', lat: null }),
      point({ siteCode: 'C', lng: null }),
      point({ siteCode: 'D', lat: null, lng: null }),
    ]);
    expect(out.map((p) => p.siteCode)).toEqual(['A']);
  });

  it('全空坐标 → 空数组（#26 实测形态：geoSites=0 时 points 全 null）', () => {
    expect(filterGeoPoints([point({ lat: null, lng: null }), point({ lat: null, lng: null })])).toEqual([]);
  });
});

describe('pointColor（决策态着色：红=已核实 / 黄=待核实 / 绿=无线索）', () => {
  it('已核实优先于待核实', () => {
    expect(pointColor({ verified: 1, pending: 2 })).toBe(COLOR_VERIFIED);
  });
  it('仅待核实用黄', () => {
    expect(pointColor({ verified: 0, pending: 2 })).toBe(COLOR_PENDING);
  });
  it('全无线索为绿', () => {
    expect(pointColor({ verified: 0, pending: 0 })).toBe(COLOR_CLEAN);
  });
});

describe('computeFitTarget（#26 回归线：视口决策的唯一事实源）', () => {
  it('0 点 → none：空数组绝不接触 fitBounds（Math.min(...[]) === Infinity 是语言规范行为）', () => {
    expect(computeFitTarget([])).toEqual({ kind: 'none' });
  });

  it('1 点 → 居中定焦：center=[lng,lat] 经纬序正确，zoom 为常量', () => {
    expect(computeFitTarget([{ lat: 45.75, lng: 126.63 }])).toEqual({
      kind: 'point',
      center: [126.63, 45.75],
      zoom: SINGLE_POINT_ZOOM,
    });
  });

  it('多点 → 精确外接矩形（乱序输入得到规范 min/max 矩形）', () => {
    const pts = [
      { lat: 30.1, lng: 110.2 },
      { lat: -20.5, lng: 80.9 },
      { lat: 50.0, lng: 130.0 },
    ];
    expect(computeFitTarget(pts)).toEqual({
      kind: 'bounds',
      bounds: [
        [80.9, -20.5],
        [130.0, 50.0],
      ],
    });
  });

  it('两点同位 → 零面积矩形（多点路径按 bounds 走，MapLibre 以 maxZoom 收口）', () => {
    const p = { lat: 45.75, lng: 126.63 };
    expect(computeFitTarget([p, { ...p }])).toEqual({
      kind: 'bounds',
      bounds: [
        [126.63, 45.75],
        [126.63, 45.75],
      ],
    });
  });

  it('负纬度边界值在合法域内正常成矩形', () => {
    expect(computeFitTarget([{ lat: -89.9, lng: -179.9 }, { lat: 89.9, lng: 179.9 }])).toEqual({
      kind: 'bounds',
      bounds: [
        [-179.9, -89.9],
        [179.9, 89.9],
      ],
    });
  });
});
