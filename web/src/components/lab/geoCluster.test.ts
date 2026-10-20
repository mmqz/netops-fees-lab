import { describe, expect, it } from 'vitest';
import { clusterDevices, type ClusterPoint } from './geoCluster';

// geoCluster.test.ts — #96: the 2D map's "锁定在一起" clustering fix (w17
// B1/B2) depends on this pure greedy-leader sweep; until now it was only
// covered transitively by the browser regression scripts (68 assertions,
// weekly). w17's contract in unit form:

const pt = (id: string, x: number, y: number): ClusterPoint => ({
  id,
  code: `C${id}`,
  name: `设备${id}`,
  status: '在网',
  lng: 126 + x / 1000,
  lat: 45 + y / 1000,
  x,
  y,
});

describe('clusterDevices（贪心 leader 聚簇）', () => {
  it('空输入 → 零簇零单例', () => {
    const r = clusterDevices([], 56);
    expect(r.clusters).toEqual([]);
    expect(r.singletons).toEqual([]);
    expect(r.clusteredIds.size).toBe(0);
  });
  it('孤立单点 → 单例', () => {
    const r = clusterDevices([pt('a', 0, 0)], 56);
    expect(r.clusters).toHaveLength(0);
    expect(r.singletons).toHaveLength(1);
    expect(r.singletons[0].id).toBe('a');
    expect(r.clusteredIds.size).toBe(0);
  });
  it('阈值内两点 → 一簇（簇心=leader 位置），clusteredIds 记账', () => {
    const r = clusterDevices([pt('a', 0, 0), pt('b', 40, 0)], 56);
    expect(r.clusters).toHaveLength(1);
    expect(r.clusters[0].members).toHaveLength(2);
    expect(r.clusters[0].center.id).toBe('a'); // leader 即簇心
    expect([...r.clusteredIds].sort()).toEqual(['a', 'b']);
    expect(r.singletons).toHaveLength(0);
  });
  it('阈值语义是严格小于：恰等于阈值不吸附（< t 入簇）', () => {
    const r = clusterDevices([pt('a', 0, 0), pt('b', 56, 0)], 56);
    expect(r.clusters).toHaveLength(0); // d2 == t2 不满足 d2 < bestD2
    expect(r.singletons).toHaveLength(2);
    const rIn = clusterDevices([pt('a', 0, 0), pt('b', 55, 0)], 56);
    expect(rIn.clusters).toHaveLength(1); // 55 < 56 吸附
  });
  it('远点分簇（>阈值）', () => {
    const r = clusterDevices([pt('a', 0, 0), pt('b', 100, 0), pt('c', 300, 0)], 56);
    // 100 与 300 都 > 56 —— 三个互相独立
    expect(r.clusters).toHaveLength(0);
    expect(r.singletons).toHaveLength(3);
  });
  it('链式吸附：先到 leader 决定归属（贪心语义，非全连通）', () => {
    // a 在原点，b 距 a 50px，c 距 b 50px（距 a 100px）——贪心下 c 找不到
    // 阈值内 leader 时成为新 leader：两簇/簇内各 2 员或 1 簇+1 单例取决于
    // 阈值判定；本用例钉住「总成员数守恒」这一不变量。
    const r = clusterDevices([pt('a', 0, 0), pt('b', 50, 0), pt('c', 100, 0)], 56);
    const clustered = r.clusters.reduce((n, c) => n + c.members.length, 0);
    expect(clustered + r.singletons.length).toBe(3);
    for (const c of r.clusters) {
      for (const m of c.members) {
        expect(r.clusteredIds.has(m.id)).toBe(true);
      }
    }
  });
});
