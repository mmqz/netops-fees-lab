/**
 * geoCluster —— 2D 地标聚簇（W17，issue「2D 图锁在一起/默认比例不合适」）。
 *
 * 问题：初始 fitBounds 是全省视野（数据跨 7° 纬度），同城设备在屏幕上仅
 * 相距 1–2px——地标与编码牌堆叠成不可读的黑块（「锁在一起」）。
 *
 * 方案：贪心 leader 聚簇。按输入顺序遍历投影后的屏幕点：
 * - 与某个已放置 leader 的屏幕距离 < thresholdPx → 并入该 leader 的簇；
 * - 否则自己成为新 leader（单例）。
 *
 * 不变量（decluttering 语义）：任意两个「可见项」（簇心或单例地标）的屏幕
 * 距离 ≥ thresholdPx，簇内每个成员与簇心的距离 < thresholdPx。确定性：
 * 同一输入顺序恒得同一结果。链式漂移被天然抑制（成员只吸附 leader，
 * 不互相吸附），不会横跨全省滚雪球。
 *
 * 纯函数模块（无 maplibre 依赖）：屏幕投影由 GeoCanvas 提供
 * （map.project，含 GCJ-02 投影后的坐标）；簇心 = leader 的投影坐标，
 * 簇 marker 挂在 leader 的 lng/lat 上，无需反投影。
 */

/** 聚簇输入：一台设备 + 当前底图坐标系下的经纬度 + 屏幕投影。 */
export interface ClusterPoint {
  id: string;
  code: string;
  name: string;
  status: string;
  /** 当前底图坐标系（GCJ 底图下已是 GCJ-02）。 */
  lng: number;
  lat: number;
  /** map.project 结果（CSS 像素）。 */
  x: number;
  y: number;
}

/** 一簇（≥2 成员）：簇心即 leader 的位置。 */
export interface DeviceCluster {
  members: ClusterPoint[];
  center: ClusterPoint;
}

export interface ClusterResult {
  clusters: DeviceCluster[];
  singletons: ClusterPoint[];
  /** 被并入簇而隐藏独立地标的所有设备 id（GeoCanvas 用于切换显隐）。 */
  clusteredIds: Set<string>;
}

/**
 * 贪心 leader 聚簇。thresholdPx 为可见项之间的最小屏幕间距（地标牌
 * 宽 ~85px、高 ~18px，56px 阈值即「标签必然重叠」的经验值）。
 */
export function clusterDevices(points: ClusterPoint[], thresholdPx: number): ClusterResult {
  const leaders: Array<{ center: ClusterPoint; members: ClusterPoint[] }> = [];
  const t2 = thresholdPx * thresholdPx;
  for (const p of points) {
    let best: (typeof leaders)[number] | null = null;
    let bestD2 = t2;
    for (const rep of leaders) {
      const dx = p.x - rep.center.x;
      const dy = p.y - rep.center.y;
      const d2 = dx * dx + dy * dy;
      if (d2 < bestD2) {
        bestD2 = d2;
        best = rep;
      }
    }
    if (best !== null) {
      best.members.push(p);
    } else {
      leaders.push({ center: p, members: [p] });
    }
  }
  const clusters: DeviceCluster[] = [];
  const singletons: ClusterPoint[] = [];
  const clusteredIds = new Set<string>();
  for (const rep of leaders) {
    if (rep.members.length >= 2) {
      clusters.push({ members: rep.members, center: rep.center });
      for (const m of rep.members) clusteredIds.add(m.id);
    } else {
      singletons.push(rep.center);
    }
  }
  return { clusters, singletons, clusteredIds };
}

/**
 * 簇的成员包围盒（WGS84 经纬度；GCJ 投影下的 lng/lat 与 WGS84 差
 * ~500m，fitBounds 语义不受影响）。单成员 / 重合点退化为点。
 */
export function clusterBounds(members: ClusterPoint[]): [[number, number], [number, number]] {
  let minLng = Number.POSITIVE_INFINITY;
  let minLat = Number.POSITIVE_INFINITY;
  let maxLng = Number.NEGATIVE_INFINITY;
  let maxLat = Number.NEGATIVE_INFINITY;
  for (const m of members) {
    minLng = Math.min(minLng, m.lng);
    minLat = Math.min(minLat, m.lat);
    maxLng = Math.max(maxLng, m.lng);
    maxLat = Math.max(maxLat, m.lat);
  }
  return [
    [minLng, minLat],
    [maxLng, maxLat],
  ];
}
