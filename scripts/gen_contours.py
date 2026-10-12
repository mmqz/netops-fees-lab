#!/usr/bin/env python3
"""gen_contours.py — 从内嵌 terrarium DEM 瓦片生成黑龙江等高线 GeoJSON（W17/#13）。

数据源：web/public/mapdata/dem/8/{x}/{y}.png（AWS Terrain Tiles / Mapzen，
terrarium 编码，z8 ≈610m/px）。输出两个变体：
- web/public/mapdata/hlj-contours.geojson.gz     （WGS84）
- web/public/mapdata/hlj-contours-gcj.geojson.gz （GCJ-02 预投影，高德底图用）
LineString FeatureCollection，properties: {level, major}。

- 首曲线 200m（100/300/…/1500），计曲线 500m 整倍（500/1000/1500）；
- matplotlib contour（C 实现 marching squares）→ 折线 → RDP 简化（1.5e-3°）
  → 短于 ~1km 的碎线丢弃（610m/px 分辨率下的噪声）；
- 缺瓦片 → NaN，不参与构线（边界自然截断）；
- 坐标取 4 位小数（~11m），gzip 静态分发（go:embed 随二进制）；
- GCJ 变体：逐顶点 wgs84ToGcj02（标准算法，国界外原样），等高线在
  高德底图上不偏移（~500m 系统偏移逐点补偿）。

可重跑：python3 scripts/gen_contours.py [--dry-run]
"""

from __future__ import annotations

import gzip
import json
import math
import sys
from pathlib import Path

import numpy as np
from PIL import Image

REPO = Path(__file__).resolve().parent.parent
DEM_DIR = REPO / "web" / "public" / "mapdata" / "dem"
OUT = REPO / "web" / "public" / "mapdata" / "hlj-contours.geojson.gz"
OUT_GCJ = REPO / "web" / "public" / "mapdata" / "hlj-contours-gcj.geojson.gz"

Z = 8
X0, X1 = 214, 219  # inclusive
Y0, Y1 = 82, 92  # inclusive
MINOR_STEP = 200
MAJOR_STEP = 500
LEVEL_MIN, LEVEL_MAX = 100, 1600
RDP_TOL_DEG = 0.0015
MIN_SEG_LEN_DEG = 0.01  # ~1km
COORD_DECIMALS = 4


def merc_y_to_lat(my: float) -> float:
    return math.degrees(math.atan(math.sinh(math.pi * (1 - 2 * my))))


def tile_edge_lng(x_tile: float) -> float:
    return x_tile / 2**Z * 360.0 - 180.0


def tile_edge_lat(y_tile: float) -> float:
    return merc_y_to_lat(y_tile / 2**Z)


def load_grid() -> tuple[np.ndarray, np.ndarray, np.ndarray]:
    """加载 z8 瓦片拼接为 (lng, lat, elev) 网格；缺瓦片为 NaN。"""
    nx = (X1 - X0 + 1) * 256
    ny = (Y1 - Y0 + 1) * 256
    elev = np.full((ny, nx), np.nan, dtype=np.float64)
    missing: list[str] = []
    for tx in range(X0, X1 + 1):
        for ty in range(Y0, Y1 + 1):
            p = DEM_DIR / str(Z) / str(tx) / f"{ty}.png"
            if not p.exists():
                missing.append(f"{Z}/{tx}/{ty}")
                continue
            im = np.asarray(Image.open(p).convert("RGB"), dtype=np.float64)
            # terrarium: h = R*256 + G + B/256 - 32768
            h = im[:, :, 0] * 256.0 + im[:, :, 1] + im[:, :, 2] / 256.0 - 32768.0
            r0 = (ty - Y0) * 256
            c0 = (tx - X0) * 256
            elev[r0 : r0 + 256, c0 : c0 + 256] = h
    if missing:
        print(f"  缺瓦片 {len(missing)} 块（NaN 处理）: {missing[:6]}{'…' if len(missing) > 6 else ''}")
    # 像素中心地理坐标（等值线取样点与像素对齐）
    lngs = np.array(
        [tile_edge_lng(X0 + (c + 0.5) / 256) for c in range(nx)], dtype=np.float64
    )
    lats = np.array(
        [tile_edge_lat(Y0 + (r + 0.5) / 256) for r in range(ny)], dtype=np.float64
    )
    # lats 从瓦片 y 增序 = 纬度递减，contour 无所谓方向，但输出几何需真实经纬度
    return np.meshgrid(lngs, lats), elev


# —— GCJ-02 转换（与 web/src/components/lab/geoBasemap.ts 同源算法）——

GCJ_A = 6378245.0
GCJ_EE = 0.00669342162296594323


def _gcj_transform_lat(x: float, y: float) -> float:
    ret = -100.0 + 2.0 * x + 3.0 * y + 0.2 * y * y + 0.1 * x * y + 0.2 * math.sqrt(abs(x))
    ret += (20.0 * math.sin(6.0 * x * math.pi) + 20.0 * math.sin(2.0 * x * math.pi)) * 2.0 / 3.0
    ret += (20.0 * math.sin(y * math.pi) + 40.0 * math.sin(y / 3.0 * math.pi)) * 2.0 / 3.0
    ret += (160.0 * math.sin(y / 12.0 * math.pi) + 320.0 * math.sin(y * math.pi / 30.0)) * 2.0 / 3.0
    return ret


def _gcj_transform_lng(x: float, y: float) -> float:
    ret = 300.0 + x + 2.0 * y + 0.1 * x * x + 0.1 * x * y + 0.1 * math.sqrt(abs(x))
    ret += (20.0 * math.sin(6.0 * x * math.pi) + 20.0 * math.sin(2.0 * x * math.pi)) * 2.0 / 3.0
    ret += (20.0 * math.sin(x * math.pi) + 40.0 * math.sin(x / 3.0 * math.pi)) * 2.0 / 3.0
    ret += (150.0 * math.sin(x / 12.0 * math.pi) + 300.0 * math.sin(x / 30.0 * math.pi)) * 2.0 / 3.0
    return ret


def _out_of_china(lng: float, lat: float) -> bool:
    return lng < 72.004 or lng > 137.8347 or lat < 0.8293 or lat > 55.8271


def wgs84_to_gcj02(lng: float, lat: float) -> tuple[float, float]:
    if _out_of_china(lng, lat):
        return lng, lat
    d_lat = _gcj_transform_lat(lng - 105.0, lat - 35.0)
    d_lng = _gcj_transform_lng(lng - 105.0, lat - 35.0)
    rad_lat = lat / 180.0 * math.pi
    magic = math.sin(rad_lat)
    magic = 1 - GCJ_EE * magic * magic
    sqrt_magic = math.sqrt(magic)
    d_lat = (d_lat * 180.0) / ((GCJ_A * (1 - GCJ_EE)) / (magic * sqrt_magic) * math.pi)
    d_lng = (d_lng * 180.0) / (GCJ_A / sqrt_magic * math.pi)
    return lng + d_lng, lat + d_lat


def rdp(points: list[list[float]], tol: float) -> list[list[float]]:
    """Douglas-Peucker 简化（迭代实现，输入/输出 [lng, lat]）。"""
    if len(points) < 3:
        return points
    keep = [False] * len(points)
    keep[0] = keep[-1] = True
    stack = [(0, len(points) - 1)]
    while stack:
        i, j = stack.pop()
        if j - i < 2:
            continue
        ax, ay = points[i]
        bx, by = points[j]
        dx, dy = bx - ax, by - ay
        norm = math.hypot(dx, dy)
        best_d, best_k = -1.0, -1
        for k in range(i + 1, j):
            px, py = points[k]
            if norm < 1e-12:
                d = math.hypot(px - ax, py - ay)
            else:
                # 点到直线距离（经纬小范围内平面近似成立）
                d = abs(dy * (px - ax) - dx * (py - ay)) / norm
            if d > best_d:
                best_d, best_k = d, k
        if best_d > tol:
            keep[best_k] = True
            stack.append((i, best_k))
            stack.append((best_k, j))
    return [p for p, k in zip(points, keep) if k]


def gcj_feature(f: dict) -> dict:
    """WGS84 折线 → GCJ-02 折线（逐顶点；properties 原样）。"""
    return {
        "type": "Feature",
        "geometry": {
            "type": "LineString",
            "coordinates": [
                [round(gx, COORD_DECIMALS), round(gy, COORD_DECIMALS)]
                for gx, gy in (
                    wgs84_to_gcj02(p[0], p[1]) for p in f["geometry"]["coordinates"]
                )
            ],
        },
        "properties": f["properties"],
    }


def main() -> int:
    dry = "--dry-run" in sys.argv
    print(f"[1/4] 加载 z{Z} 瓦片（x {X0}-{X1}, y {Y0}-{Y1}）")
    (LNG, LAT), elev = load_grid()
    valid = np.isfinite(elev)
    print(
        f"  网格 {elev.shape[1]}×{elev.shape[0]}，有效 {valid.sum() / elev.size:.1%}，"
        f"高程 {np.nanmin(elev):.0f}–{np.nanmax(elev):.0f} m"
    )

    minor_levels = list(range(LEVEL_MIN, LEVEL_MAX + 1, MINOR_STEP))
    major_levels = set(range(MAJOR_STEP, LEVEL_MAX + 1, MAJOR_STEP))
    levels = sorted(set(minor_levels) | major_levels)
    print(f"[2/4] 等值线 {levels}（计曲线 {sorted(major_levels)}）")

    import matplotlib

    matplotlib.use("Agg")
    import matplotlib.pyplot as plt

    # NaN 网格：masked 数组保证 contour 跳过缺数据区
    cs = plt.contour(LNG, LAT, np.ma.masked_invalid(elev), levels=levels)
    plt.close()

    print("[3/4] 抽取折线 → RDP 简化 → 过滤碎线")
    features: list[dict] = []
    dropped = 0
    for li, level in enumerate(levels):
        major = level in major_levels
        for seg in cs.allsegs[li]:
            pts = [[round(float(x), 6), round(float(y), 6)] for x, y in seg]
            if len(pts) < 2:
                continue
            # 长度过滤（首点累计）
            length = sum(
                math.hypot(pts[i + 1][0] - pts[i][0], pts[i + 1][1] - pts[i][1])
                for i in range(len(pts) - 1)
            )
            if length < MIN_SEG_LEN_DEG and len(pts) < 4:
                dropped += 1
                continue
            simplified = rdp(pts, RDP_TOL_DEG)
            if len(simplified) < 2:
                dropped += 1
                continue
            features.append(
                {
                    "type": "Feature",
                    "geometry": {
                        "type": "LineString",
                        "coordinates": [
                            [round(p[0], COORD_DECIMALS), round(p[1], COORD_DECIMALS)]
                            for p in simplified
                        ],
                    },
                    "properties": {"level": level, "major": major},
                }
            )
    n_major = sum(1 for f in features if f["properties"]["major"])
    total_pts = sum(len(f["geometry"]["coordinates"]) for f in features)
    print(f"  折线 {len(features)} 条（计曲线 {n_major}），共 {total_pts} 点，丢弃碎线 {dropped} 条")

    print("[4/4] 写出 GeoJSON.gz（WGS84 + GCJ-02 变体）")
    fc = {"type": "FeatureCollection", "features": features}
    raw = json.dumps(fc, separators=(",", ":"), ensure_ascii=True).encode()
    fc_gcj = {"type": "FeatureCollection", "features": [gcj_feature(f) for f in features]}
    raw_gcj = json.dumps(fc_gcj, separators=(",", ":"), ensure_ascii=True).encode()
    if not dry:
        OUT.write_bytes(gzip.compress(raw, compresslevel=9, mtime=0))
        OUT_GCJ.write_bytes(gzip.compress(raw_gcj, compresslevel=9, mtime=0))
        print(
            f"  WGS84: 原始 {len(raw) / 1e6:.2f} MB → gzip {OUT.stat().st_size / 1e6:.2f} MB\n"
            f"  GCJ-02: 原始 {len(raw_gcj) / 1e6:.2f} MB → gzip {OUT_GCJ.stat().st_size / 1e6:.2f} MB"
        )
    else:
        print(f"  dry-run：WGS84 原始 {len(raw) / 1e6:.2f} MB，GCJ 原始 {len(raw_gcj) / 1e6:.2f} MB")
    return 0


if __name__ == "__main__":
    sys.exit(main())
