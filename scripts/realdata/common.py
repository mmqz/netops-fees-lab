#!/usr/bin/env python3
"""common.py — v0.8.0 真实公开数据管线共享工具（W26/W27）。

被 extract.py（PBF/OpenCelliD/道路铁路 → 真实数据核心 + 地图图层）与
workbooks.py（真实数据核心 → 演示工作簿）共用。全部函数纯确定性：同一输入
字节级同一输出（坐标量化 + 排序钉死）。

坐标与许可：
- OSM 数据 © OpenStreetMap contributors（ODbL 1.0）。
- OpenCelliD 数据 © OpenCelliD / Unwired Labs（CC BY-SA 4.0）。
- DEM 地形 © Mapzen / AWS Open Data（SRTM·GMTED，terrarium）。
- 本管线不嵌入任何高德 POI 数据（其服务条款限制再分发；高德仅作在线底图）。
"""

from __future__ import annotations

import gzip
import json
import math
from pathlib import Path

import numpy as np

REPO = Path(__file__).resolve().parent.parent.parent
MAPDATA = Path("/home/z/my-project/mapdata")          # 外部大输入（不入库）
PUBLIC_MAPDATA = REPO / "web" / "public" / "mapdata"   # 内嵌地图数据（入库）
WORKBOOK_DIR = REPO / "testdata" / "realdata"
CORE_JSON = WORKBOOK_DIR / "realdata-core.json"
MANIFEST = WORKBOOK_DIR / "manifest.json"

GCJ_EE = 0.00669342162296594323
GCJ_A = 6378245.0


# ---------------------------------------------------------------------------
# GCJ-02（火星坐标）投影 —— 与 web/src/components/lab/geoBasemap.ts 同源算法，
# numpy 向量化一次变换全部坐标；境外/越界点原样返回。
# ---------------------------------------------------------------------------

def _tlat(x: np.ndarray, y: np.ndarray) -> np.ndarray:
    ret = -100.0 + 2.0 * x + 3.0 * y + 0.2 * y * y + 0.1 * x * y + 0.2 * np.sqrt(np.abs(x))
    ret += (20.0 * np.sin(6.0 * x * np.pi) + 20.0 * np.sin(2.0 * x * np.pi)) * 2.0 / 3.0
    ret += (20.0 * np.sin(y * np.pi) + 40.0 * np.sin(y / 3.0 * np.pi)) * 2.0 / 3.0
    ret += (160.0 * np.sin(y / 12.0 * np.pi) + 320 * np.sin(y * np.pi / 30.0)) * 2.0 / 3.0
    return ret


def _tlng(x: np.ndarray, y: np.ndarray) -> np.ndarray:
    ret = 300.0 + x + 2.0 * y + 0.1 * x * x + 0.1 * x * y + 0.1 * np.sqrt(np.abs(x))
    ret += (20.0 * np.sin(6.0 * x * np.pi) + 20.0 * np.sin(2.0 * x * np.pi)) * 2.0 / 3.0
    ret += (20.0 * np.sin(x * np.pi) + 40.0 * np.sin(x / 3.0 * np.pi)) * 2.0 / 3.0
    ret += (150.0 * np.sin(x / 12.0 * np.pi) + 300.0 * np.sin(x / 30.0 * np.pi)) * 2.0 / 3.0
    return ret


def wgs84_to_gcj02_np(lng: np.ndarray, lat: np.ndarray) -> tuple[np.ndarray, np.ndarray]:
    """逐点 WGS84 → GCJ-02（numpy 向量化；与 gen_natural.py 同源实现）。"""
    out = (lng < 72.004) | (lng > 137.8347) | (lat < 0.8293) | (lat > 55.8271)
    d_lat = _tlat(lng - 105.0, lat - 35.0)
    d_lng = _tlng(lng - 105.0, lat - 35.0)
    rad_lat = lat / 180.0 * np.pi
    magic = np.sin(rad_lat)
    magic = 1 - GCJ_EE * magic * magic
    sqrt_magic = np.sqrt(magic)
    d_lat = (d_lat * 180.0) / ((GCJ_A * (1 - GCJ_EE)) / (magic * sqrt_magic) * np.pi)
    d_lng = (d_lng * 180.0) / ((GCJ_A / sqrt_magic) * np.pi)
    return np.where(out, lng, lng + d_lng), np.where(out, lat, lat + d_lat)


def coords_to_gcj(coords: list) -> list:
    """递归坐标结构（LineString/MultiLineString/Point 任一嵌套，段长可不等）→ GCJ-02。

    叶子点统一收集一次批量变换（numpy 向量化），再按原嵌套形状重建。
    """
    leaves: list[list[float]] = []

    def collect(node):
        if isinstance(node, (list, tuple)) and len(node) >= 2 \
                and isinstance(node[0], (int, float)) and isinstance(node[1], (int, float)):
            leaves.append([float(node[0]), float(node[1])])
            return
        for child in node:
            collect(child)

    collect(coords)
    if not leaves:
        return coords
    flat = np.asarray(leaves, dtype=float)
    g_lng, g_lat = wgs84_to_gcj02_np(flat[:, 0], flat[:, 1])
    transformed = np.column_stack([g_lng, g_lat])
    index = 0

    def rebuild(node):
        if isinstance(node, (list, tuple)) and len(node) >= 2 \
                and isinstance(node[0], (int, float)) and isinstance(node[1], (int, float)):
            nonlocal index
            pt = [float(transformed[index][0]), float(transformed[index][1])]
            index += 1
            if isinstance(node, tuple):
                return tuple(pt)
            return pt
        out = []
        for child in node:
            out.append(rebuild(child))
        return out

    return rebuild(coords)


# ---------------------------------------------------------------------------
# 几何工具
# ---------------------------------------------------------------------------

def rdp_np(pts: np.ndarray, tol: float) -> np.ndarray:
    """Ramer–Douglas–Peucker 简化（numpy 实现，与 gen_natural.py 同源）。"""
    if pts.shape[0] <= 2:
        return pts
    anchor = pts[0]
    last = pts[-1]
    dx, dy = last[0] - anchor[0], last[1] - anchor[1]
    seg = math.hypot(dx, dy)
    if seg == 0:
        d = np.hypot(pts[:, 0] - anchor[0], pts[:, 1] - anchor[1])
    else:
        d = np.abs(dx * (pts[:, 1] - anchor[1]) - (pts[:, 0] - anchor[0]) * dy) / seg
    i = int(np.argmax(d))
    if d[i] > tol:
        left = rdp_np(pts[: i + 1], tol)
        right = rdp_np(pts[i:], tol)
        return np.vstack([left[:-1], right])
    return np.vstack([anchor, last])


R_EARTH = 6371008.8  # IUGG 平均地球半径（米）


def haversine_m(lng1: float, lat1: float, lng2: float, lat2: float) -> float:
    rlat1, rlat2 = math.radians(lat1), math.radians(lat2)
    dlat, dlng = math.radians(lat2 - lat1), math.radians(lng2 - lng1)
    a = math.sin(dlat / 2) ** 2 + math.cos(rlat1) * math.cos(rlat2) * math.sin(dlng / 2) ** 2
    return 2 * R_EARTH * math.asin(math.sqrt(a))


def path_length_m(coords: list) -> float:
    """折线长度（米）——沿线逐段 haversine。"""
    total = 0.0
    for i in range(len(coords) - 1):
        total += haversine_m(coords[i][0], coords[i][1], coords[i + 1][0], coords[i + 1][1])
    return total


def q(v: float, nd: int = 5) -> float:
    """坐标量化（默认 5 位小数 ≈ 1.1m）——确定性输出。"""
    return round(float(v), nd)


def bbox_of(coords: list) -> tuple[float, float, float, float]:
    arr = np.asarray(coords, dtype=float).reshape(-1, 2)
    return float(arr[:, 0].min()), float(arr[:, 1].min()), float(arr[:, 0].max()), float(arr[:, 1].max())


def seg_intersects_bbox(coords: list, bbox: tuple[float, float, float, float], buf: float) -> bool:
    """折线（任一顶点）落在 bbox+buf 内即视为相交（走廊筛选足够）。"""
    x0, y0, x1, y1 = bbox
    arr = np.asarray(coords, dtype=float).reshape(-1, 2)
    return bool(
        ((arr[:, 0] >= x0 - buf) & (arr[:, 0] <= x1 + buf) & (arr[:, 1] >= y0 - buf) & (arr[:, 1] <= y1 + buf)).any()
    )


# ---------------------------------------------------------------------------
# IO：gzip GeoJSON（紧凑 + 确定性键序）
# ---------------------------------------------------------------------------

def write_geojson_gz(path: Path, features: list, dual_gcj: bool, gzlevel: int = 9) -> dict:
    """写 WGS84 变体（+可选 GCJ-02 变体），返回 {path: uncompressed bytes}。"""
    out: dict[str, int] = {}
    fc = {"type": "FeatureCollection", "features": features}
    raw = json.dumps(fc, ensure_ascii=False, separators=(",", ":"), sort_keys=True).encode("utf-8")
    path.parent.mkdir(parents=True, exist_ok=True)
    with gzip.GzipFile(str(path), "wb", compresslevel=gzlevel, mtime=0) as fh:
        fh.write(raw)
    out[str(path)] = len(raw)
    if dual_gcj:
        gcj_features = []
        for f in features:
            gf = {
                "type": "Feature",
                "properties": f.get("properties", {}),
                "geometry": {
                    "type": f["geometry"]["type"],
                    "coordinates": coords_to_gcj(f["geometry"]["coordinates"]),
                },
            }
            gcj_features.append(gf)
        stem = path.name[: -len(".geojson.gz")]
        gcj_path = path.with_name(stem + "-gcj.geojson.gz")
        gfc = {"type": "FeatureCollection", "features": gcj_features}
        graw = json.dumps(gfc, ensure_ascii=False, separators=(",", ":"), sort_keys=True).encode("utf-8")
        with gzip.GzipFile(str(gcj_path), "wb", compresslevel=gzlevel, mtime=0) as fh:
            fh.write(graw)
        out[str(gcj_path)] = len(graw)
    return out


def load_gz_json(path: Path):
    with gzip.open(str(path), "rt", encoding="utf-8") as fh:
        return json.load(fh)
