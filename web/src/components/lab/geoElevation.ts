/**
 * geoElevation —— 光标处实时海拔采样（W17/#13：地理高程支撑）。
 *
 * 数据源：与 3D 地形/hillshade 同一批离线 terrarium DEM 瓦片
 * （/mapdata/dem/8/{x}/{y}.png，z8 ≈610m/px，仅覆盖黑龙江 bbox）。
 * 客户端按需取瓦片 → ImageBitmap → OffscreenCanvas 像素解码 → 双线性采样：
 * - 瓦片按 (x,y) 缓存（Map，容量上限 LRU 淘汰，全部本地 0 网络往返）；
 * - 覆盖外/取瓦片失败 → null（UI 显示「—」）；
 * - GCJ 底图（高德）下光标 lngLat 为火星坐标：先 gcj02ToWgs84 逆偏移再采样
 *   （单步近似逆变换，误差 <2m，对海拔读数无影响）；
 * - 采样节流由调用方负责（rAF）。
 *
 * terrarium 编码：h = R·256 + G + B/256 − 32768（米）。
 */

import { wgs84ToGcj02 } from './geoBasemap';

/** 采样层（本地瓦片最高分辨率层）。 */
const DEM_Z = 8;
/** 本地瓦片覆盖范围（与 scripts/gen_contours.py / 下载脚本一致）。 */
const DEM_X0 = 214;
const DEM_X1 = 219;
const DEM_Y0 = 82;
const DEM_Y1 = 92;
/** 瓦片缓存上限（6×11=66 块全量也仅 66 张 256² PNG；上限给余量）。 */
const CACHE_LIMIT = 80;

interface TileEntry {
  grid: Float32Array | null;
}

const tileCache = new Map<string, TileEntry>();
const inflight = new Map<string, Promise<TileEntry>>();

function tileKey(x: number, y: number): string {
  return `${DEM_Z}/${x}/${y}`;
}

function inCoverage(x: number, y: number): boolean {
  return x >= DEM_X0 && x <= DEM_X1 && y >= DEM_Y0 && y <= DEM_Y1;
}

function lngLatToTilePixel(lng: number, lat: number): { x: number; y: number; fx: number; fy: number } {
  const n = 2 ** DEM_Z;
  const fx = ((lng + 180) / 360) * n;
  const latRad = (lat * Math.PI) / 180;
  const mercY = Math.log(Math.tan(Math.PI / 4 + latRad / 2)) / Math.PI;
  const fy = (1 - mercY) / 2 * n;
  return {
    x: Math.floor(fx),
    y: Math.floor(fy),
    fx: fx - Math.floor(fx),
    fy: fy - Math.floor(fy),
  };
}

/** terrarium PNG → 256×256 高程网格。失败（404/解码）返回 null grid。 */
async function loadTile(x: number, y: number): Promise<TileEntry> {
  const key = tileKey(x, y);
  const cached = tileCache.get(key);
  if (cached !== undefined) return cached;
  const pending = inflight.get(key);
  if (pending !== undefined) return pending;
  const task = (async (): Promise<TileEntry> => {
    let entry: TileEntry = { grid: null };
    try {
      const res = await fetch(`/mapdata/dem/${key}.png`);
      if (res.ok) {
        const blob = await res.blob();
        const bmp = await createImageBitmap(blob);
        const size = 256;
        const ctx = new OffscreenCanvas(size, size).getContext('2d');
        if (ctx !== null) {
          ctx.drawImage(bmp, 0, 0, size, size);
          const { data } = ctx.getImageData(0, 0, size, size);
          const grid = new Float32Array(size * size);
          for (let i = 0; i < size * size; i += 1) {
            // terrarium：h = R·256 + G + B/256 − 32768
            grid[i] = data[i * 4] * 256 + data[i * 4 + 1] + data[i * 4 + 2] / 256 - 32768;
          }
          entry = { grid };
        }
        bmp.close();
      }
    } catch {
      entry = { grid: null };
    }
    tileCache.set(key, entry);
    if (tileCache.size > CACHE_LIMIT) {
      const oldest = tileCache.keys().next().value;
      if (oldest !== undefined) tileCache.delete(oldest);
    }
    return entry;
  })();
  inflight.set(key, task);
  try {
    return await task;
  } finally {
    inflight.delete(key);
  }
}

/** 双线性采样瓦片内 (fx, fy) 处高程（米）。 */
function bilinear(grid: Float32Array, fx: number, fy: number): number {
  const size = 256;
  const x0 = Math.min(size - 2, Math.max(0, Math.floor(fx * size)));
  const y0 = Math.min(size - 2, Math.max(0, Math.floor(fy * size)));
  const tx = Math.min(1, Math.max(0, fx * size - x0));
  const ty = Math.min(1, Math.max(0, fy * size - y0));
  const at = (x: number, y: number): number => grid[y * size + x];
  const top = at(x0, y0) * (1 - tx) + at(x0 + 1, y0) * tx;
  const bottom = at(x0, y0 + 1) * (1 - tx) + at(x0 + 1, y0 + 1) * tx;
  return top * (1 - ty) + bottom * ty;
}

/**
 * GCJ-02 → WGS84 单步近似逆变换（g − (T(g) − g)）。
 * 高德底图光标坐标回到 WGS84 瓦片网格；误差 <2m。
 */
export function gcj02ToWgs84(lng: number, lat: number): [number, number] {
  const [glng, glat] = wgs84ToGcj02(lng, lat);
  return [lng * 2 - glng, lat * 2 - glat];
}

/**
 * 采样 (lng, lat) 处海拔（米）。toGCJ=true 时先逆偏移。
 * 覆盖外/瓦片不可用 → null。
 */
export async function sampleElevation(lng: number, lat: number, fromGCJ: boolean): Promise<number | null> {
  const [wlng, wlat] = fromGCJ ? gcj02ToWgs84(lng, lat) : [lng, lat];
  const { x, y, fx, fy } = lngLatToTilePixel(wlng, wlat);
  if (!inCoverage(x, y)) return null;
  const tile = await loadTile(x, y);
  if (tile.grid === null) return null;
  return bilinear(tile.grid, fx, fy);
}

/** 清空瓦片缓存（组件卸载时调用，防止长会话驻留）。 */
export function clearElevationCache(): void {
  tileCache.clear();
  inflight.clear();
}

// —— 测试探针（w17 回归断言用）——
export const __demCoverage = { z: DEM_Z, x0: DEM_X0, x1: DEM_X1, y0: DEM_Y0, y1: DEM_Y1 };
