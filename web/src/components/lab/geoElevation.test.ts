import { describe, expect, it } from 'vitest';
import { gcj02ToWgs84 } from './geoElevation';
import { wgs84ToGcj02 } from './geoBasemap';

// geoElevation.test.ts — #96: the coordinate-pair transforms are the reason
// the 3D terrain cursor reads the right DEM cell and why switching the
// basemap to 高德 picks the pre-projected GCJ variant (w17 N10/N11). A
// sign error here moves the cursor ~500m — these unit pins catch it before
// the weekly browser run.

const WGS = [116.397428, 39.90923] as const; // 北京（中国境内 → 有偏移）

describe('wgs84ToGcj02 / gcj02ToWgs84', () => {
  it('境内坐标：GCJ 偏移量在公认知别域内（数百米量级）', () => {
    const [glng, glat] = wgs84ToGcj02(WGS[0], WGS[1]);
    expect(Math.abs(glng - WGS[0])).toBeGreaterThan(0.001);
    expect(Math.abs(glng - WGS[0])).toBeLessThan(0.01);
    expect(Math.abs(glat - WGS[1])).toBeGreaterThan(0.001);
    expect(Math.abs(glat - WGS[1])).toBeLessThan(0.01);
  });
  it('镜像逆变换的往返误差 <1e-4 度（≈10m 级，双线性采样的容差内）', () => {
    const [glng, glat] = wgs84ToGcj02(WGS[0], WGS[1]);
    const [wlng, wlat] = gcj02ToWgs84(glng, glat);
    expect(Math.abs(wlng - WGS[0])).toBeLessThan(1e-4);
    expect(Math.abs(wlat - WGS[1])).toBeLessThan(1e-4);
  });
  it('境外坐标恒等（outOfChina 直通，无伪偏移）', () => {
    const [lng, lat] = wgs84ToGcj02(-0.1276, 51.5072); // 伦敦
    expect([lng, lat]).toEqual([-0.1276, 51.5072]);
    const [rlng, rlat] = gcj02ToWgs84(-0.1276, 51.5072);
    expect(rlng).toBeCloseTo(-0.1276, 10);
    expect(rlat).toBeCloseTo(51.5072, 10);
  });
});
