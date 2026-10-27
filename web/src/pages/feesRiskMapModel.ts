/**
 * 站点风险地图的确定性核心（#18 渲染 / #26 空数据与退化修复——纯函数，
 * 同数据同输出，可对照测试；组件只做副作用：建图、上源、挂事件）。
 *
 * 职责：
 * - 坐标过滤（lat/lng 任一为 null 的站址不落点——资源表没给坐标就不上地图，
 *   诚实空态，与后端 geoSites/linkedSites 覆盖计数口径一致）；
 * - 决策态着色（红=已核实 / 黄=待核实 / 绿=无线索——颜色令牌与全局告警色系一致）；
 * - **视口目标计算（#26 的核心教训）**：`Math.min(...[])` === Infinity、
 *   `Math.max(...[])` === -Infinity 是 JS 语言规范行为——空数组直塞 fitBounds
 *   必抛 MapLibre「Invalid LngLat latitude value」。空即「不碰视口」、
 *   单点即「居中定焦而非缩放矩形」（min==max 的退化矩形交给 fitBounds 是
 *   含糊行为），多点才走 fitBounds。视口决策全部收在本函数，组件不得自算。
 *
 * 教义：无法渲染的数据形态（0 点）必须在类型上可表达（'none'），
 * 让「不调用」成为唯一正确动作，而不是依赖调用方记得判空。
 */

import type { RiskMapPoint } from '../api/types';

/** 已过坐标过滤的落点点位（lat/lng 保证非空——组件层拿到即可直接用）。 */
export interface RiskMapGeoPoint {
  siteCode: string;
  lat: number;
  lng: number;
  clueCount: number;
  pending: number;
  verified: number;
  dismissed: number;
  scenarios: string[];
  resourceKey: string;
}

/** 过滤：lat/lng 任一为 null 的站址不落点（#16 边界——坐标来自用户资源表）。 */
export function filterGeoPoints(points: RiskMapPoint[]): RiskMapGeoPoint[] {
  return points.filter(
    (p): p is RiskMapPoint & { lat: number; lng: number } => p.lat !== null && p.lng !== null,
  );
}

/** 着色令牌：红/黄/绿与全局告警色系一致（已核实优先，其次待核实，全无线索为绿）。 */
export const COLOR_VERIFIED = '#d64541';
export const COLOR_PENDING = '#e6a817';
export const COLOR_CLEAN = '#3f9e63';

export function pointColor(p: { verified: number; pending: number }): string {
  if (p.verified > 0) return COLOR_VERIFIED;
  if (p.pending > 0) return COLOR_PENDING;
  return COLOR_CLEAN;
}

/** 视口目标：kind='none' 表示「不碰视口」——绝不允许空数组到达 fitBounds。 */
export type FitTarget =
  | { kind: 'none' }
  | { kind: 'point'; center: [number, number]; zoom: number }
  | { kind: 'bounds'; bounds: [[number, number], [number, number]] };

/** 单点定焦 zoom（issue #26 修复 3：min==max 的退化矩形改为居中）。 */
export const SINGLE_POINT_ZOOM = 10;

export function computeFitTarget(points: Array<{ lat: number; lng: number }>): FitTarget {
  if (points.length === 0) return { kind: 'none' };
  if (points.length === 1) {
    return { kind: 'point', center: [points[0].lng, points[0].lat], zoom: SINGLE_POINT_ZOOM };
  }
  const lons = points.map((p) => p.lng);
  const lats = points.map((p) => p.lat);
  return {
    kind: 'bounds',
    bounds: [
      [Math.min(...lons), Math.min(...lats)],
      [Math.max(...lons), Math.max(...lats)],
    ],
  };
}
