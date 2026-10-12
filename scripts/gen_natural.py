#!/usr/bin/env python3
"""gen_natural.py — 从黑龙江 OSM PBF 提取地表覆盖（森林/水域/绿地）与地名（v0.6.4/#15）。

数据源：mapdata/heilongjiang-2026-10-12.osm.pbf（Release mapdata-hlj-2026-10-12）。
输出（web/public/mapdata → go:embed 随二进制分发，gzip 静态）：
- hlj-natural.geojson.gz / hlj-natural-gcj.geojson.gz
  面要素（Polygon/MultiPolygon）+ 线要素（LineString）混合 FC，
  properties: {c: 类别} / {c, n: 名称}（水系与部分水域带名称）。
  类别 c：wood|forest|grass|scrub|farm|wetland|water|riverbank|river|stream|canal
- hlj-places.geojson.gz / hlj-places-gcj.geojson.gz
  地名点（OSM place=*），properties: {c: place 值, n: 名称}。

处理管线（ogr2ogr OSM 驱动 + shapely）：
- 自定义 osmconf.ini 只暴露需要的标签字段（natural/landuse/waterway/place/name）；
- -where 预过滤（建筑等无关要素不导出）；
- 分类白名单 → shapely simplify（类内分级容差）→ 最小面积/长度过滤
  → 坐标量化（面 5 位/线 5 位小数）→ 紧凑 GeoJSON（gzip -9）；
- GCJ-02 变体：逐顶点 wgs84_to_gcj02（与 geoBasemap.ts 同源算法，
  numpy 向量化一次变换全部坐标），高德底图上自然要素不偏移；
- QA 渲染：matplotlib 出一张全省渲染图（tmp/，仅自检，不进交付物）。

可重跑：python3 scripts/gen_natural.py [--dry-run]
"""

from __future__ import annotations

import gzip
import json
import math
import subprocess
import sys
from pathlib import Path

import numpy as np

REPO = Path(__file__).resolve().parent.parent
MAPDATA = Path("/home/z/my-project/mapdata")
PBF = MAPDATA / "heilongjiang-2026-10-12.osm.pbf"
TMP = Path("/home/z/my-project/tmp/natural")
OUT_DIR = REPO / "web" / "public" / "mapdata"
OUT_NAT = OUT_DIR / "hlj-natural.geojson.gz"
OUT_NAT_GCJ = OUT_DIR / "hlj-natural-gcj.geojson.gz"
OUT_PLA = OUT_DIR / "hlj-places.geojson.gz"
OUT_PLA_GCJ = OUT_DIR / "hlj-places-gcj.geojson.gz"
QA_PNG = Path("/home/z/my-project/tmp/natural/qa-render.png")

OSMCONF = REPO / "scripts" / "osmconf-natural.ini"

# —— 分类白名单（OSM 标签 → 类别 c）——
# natural=*
NATURAL_CLASS = {
    "wood": "wood",
    "forest": "wood",  # 罕见：natural=forest 视作 wood
    "scrub": "scrub",
    "grassland": "grass",
    "meadow": "grass",
    "wetland": "wetland",
    "water": "water",
    "riverbank": "riverbank",
}
# landuse=*
LANDUSE_CLASS = {
    "forest": "forest",
    "farmland": "farm",
    "orchard": "farm",
    "grass": "grass",
    "meadow": "grass",
    "reservoir": "water",
    "basin": "water",
}
# waterway=*（线）
WATERWAY_LINE_CLASS = {
    "river": "river",
    "stream": "stream",
    "canal": "canal",
    "ditch": "canal",
    "drain": "canal",
}
# waterway=*（面：riverbank 老式标注）
WATERWAY_AREA_CLASS = {"riverbank": "riverbank"}

# —— 简化与过滤参数（度）——
# 面积单位说明：1 deg² ≈ 12,392·cos(lat) km² ≈ 8,290 km²（纬度 ~48°N），
# 即 1e-6 deg² ≈ 0.83 ha。阈值按“省级视野不可见的最小斑”取值。
SIMPLIFY_TOL = {
    "wood": 0.0008,  # ~90m：连片林区，边缘毛刺可弃
    "forest": 0.0008,
    "grass": 0.0008,
    "scrub": 0.0008,
    "farm": 0.0012,  # 农田地块大而规整
    "wetland": 0.0008,
    "water": 0.0005,  # 湖岸保真度略高
    "riverbank": 0.0006,
    "river": 0.00028,  # 河流：树枝状水系保真（~30m）
    "stream": 0.00022,  # 溪流最细
    "canal": 0.0005,
}
MIN_AREA_DEG2 = {
    "wood": 1.2e-6,  # ~1 ha
    "forest": 1.2e-6,
    "grass": 1.5e-6,
    "scrub": 1.5e-6,
    "farm": 2e-6,  # ~1.7 ha
    "wetland": 1e-6,
    "water": 6e-7,  # ~0.5 ha
    "riverbank": 6e-7,
}
MIN_LINE_LEN_DEG = {"river": 0.002, "stream": 0.0012, "canal": 0.001}
COORD_DECIMALS = 5

# 地名白名单（place=* → 显示层级由前端 minzoom 控制）
PLACE_KEEP = {"province", "city", "county", "town", "village", "hamlet", "district"}


def write_osmconf() -> None:
    # [general].closed_ways_are_polygons 是关键路由指令：带这些标签的闭合
    # way 会被提升为面（缺失时 natural=wood 闭合面会掉进 lines 层被丢弃）。
    OSMCONF.write_text(
        "[general]\n"
        "closed_ways_are_polygons=natural,landuse,waterway,place\n"
        "\n"
        "[points]\n"
        "report_uninteresting=no\n"
        "attributes=place,name\n"
        "\n"
        "[lines]\n"
        "report_uninteresting=no\n"
        "attributes=waterway,name\n"
        "\n"
        "[multipolygons]\n"
        "report_uninteresting=no\n"
        "attributes=natural,landuse,waterway,name\n"
    )


def run_ogr() -> None:
    TMP.mkdir(parents=True, exist_ok=True)
    cfg = ["--config", "OSM_CONFIG_FILE", str(OSMCONF)]
    jobs = [
        (
            TMP / "points.geojson",
            "points",
            'place IS NOT NULL AND "name" IS NOT NULL',
        ),
        (
            TMP / "lines.geojson",
            "lines",
            "waterway IS NOT NULL",
        ),
        (
            TMP / "areas.geojson",
            "multipolygons",
            # 值白名单直接在导出端拦截（residential/industrial 等不进文件）
            '"natural" IN (\'wood\',\'forest\',\'water\',\'wetland\',\'grassland\',\'scrub\',\'riverbank\')'
            " OR landuse IN ('forest','farmland','meadow','grass','orchard','reservoir','basin')"
            " OR waterway = 'riverbank'",
        ),
    ]
    for out, layer, where in jobs:
        cmd = [
            "ogr2ogr",
            "-f",
            "GeoJSON",
            str(out),
            str(PBF),
            layer,
            *cfg,
            "-where",
            where,
        ]
        print(f"  ogr2ogr {layer} -> {out.name}")
        subprocess.run(cmd, check=True, capture_output=True, text=True)


# —— GCJ-02（与 geoBasemap.ts / gen_contours.py 同源，numpy 向量化）——
GCJ_A = 6378245.0
GCJ_EE = 0.00669342162296594323


def _tlat(x: np.ndarray, y: np.ndarray) -> np.ndarray:
    ret = -100.0 + 2.0 * x + 3.0 * y + 0.2 * y * y + 0.1 * x * y + 0.2 * np.sqrt(np.abs(x))
    ret += (20.0 * np.sin(6.0 * x * np.pi) + 20.0 * np.sin(2.0 * x * np.pi)) * 2.0 / 3.0
    ret += (20.0 * np.sin(y * np.pi) + 40.0 * np.sin(y / 3.0 * np.pi)) * 2.0 / 3.0
    ret += (160.0 * np.sin(y / 12.0 * np.pi) + 320.0 * np.sin(y * np.pi / 30.0)) * 2.0 / 3.0
    return ret


def _tlng(x: np.ndarray, y: np.ndarray) -> np.ndarray:
    ret = 300.0 + x + 2.0 * y + 0.1 * x * x + 0.1 * x * y + 0.1 * np.sqrt(np.abs(x))
    ret += (20.0 * np.sin(6.0 * x * np.pi) + 20.0 * np.sin(2.0 * x * np.pi)) * 2.0 / 3.0
    ret += (20.0 * np.sin(x * np.pi) + 40.0 * np.sin(x / 3.0 * np.pi)) * 2.0 / 3.0
    ret += (150.0 * np.sin(x / 12.0 * np.pi) + 300.0 * np.sin(x / 30.0 * np.pi)) * 2.0 / 3.0
    return ret


def wgs84_to_gcj02_np(lng: np.ndarray, lat: np.ndarray) -> tuple[np.ndarray, np.ndarray]:
    out = (
        (lng < 72.004) | (lng > 137.8347) | (lat < 0.8293) | (lat > 55.8271)
    )
    d_lat = _tlat(lng - 105.0, lat - 35.0)
    d_lng = _tlng(lng - 105.0, lat - 35.0)
    rad_lat = lat / 180.0 * np.pi
    magic = np.sin(rad_lat)
    magic = 1 - GCJ_EE * magic * magic
    sqrt_magic = np.sqrt(magic)
    d_lat = (d_lat * 180.0) / ((GCJ_A * (1 - GCJ_EE)) / (magic * sqrt_magic) * np.pi)
    d_lng = (d_lng * 180.0) / ((GCJ_A / sqrt_magic) * np.pi)
    return np.where(out, lng, lng + d_lng), np.where(out, lat, lat + d_lat)


def q(v: float) -> float:
    return round(v, COORD_DECIMALS)


def coord_seq(geom: dict) -> list[list[list[float]]]:
    """几何 → 外层环列表（面：每个环一个坐标序列；线：单一序列）。"""
    t = geom["type"]
    if t == "LineString":
        return [geom["coordinates"]]
    if t == "MultiLineString":
        return [c for c in geom["coordinates"] if len(c) >= 2]
    if t == "Polygon":
        return [ring for ring in geom["coordinates"] if len(ring) >= 4]
    if t == "MultiPolygon":
        out: list[list[list[float]]] = []
        for poly in geom["coordinates"]:
            for ring in poly:
                if len(ring) >= 4:
                    out.append(ring)
        return out
    return []


def line_length(pts: list[list[float]]) -> float:
    return sum(
        math.hypot(pts[i + 1][0] - pts[i][0], pts[i + 1][1] - pts[i][1])
        for i in range(len(pts) - 1)
    )


def rdp_np(pts: np.ndarray, tol: float) -> np.ndarray:
    """Douglas-Peucker（numpy 点数组 N×2）。"""
    n = len(pts)
    if n < 3:
        return pts
    keep = np.zeros(n, dtype=bool)
    keep[0] = keep[-1] = True
    stack = [(0, n - 1)]
    while stack:
        i, j = stack.pop()
        if j - i < 2:
            continue
        a = pts[i]
        b = pts[j]
        seg = b - a
        norm = float(np.hypot(seg[0], seg[1]))
        sub = pts[i + 1 : j]
        if norm < 1e-12:
            d = np.hypot(sub[:, 0] - a[0], sub[:, 1] - a[1])
        else:
            d = np.abs(seg[1] * (sub[:, 0] - a[0]) - seg[0] * (sub[:, 1] - a[1])) / norm
        k = int(np.argmax(d))
        if float(d[k]) > tol:
            kk = i + 1 + k
            keep[kk] = True
            stack.append((i, kk))
            stack.append((kk, j))
    return pts[keep]


def simplify_rings(rings: list[list[list[float]]], tol: float) -> list[list[list[float]]]:
    out: list[list[list[float]]] = []
    for ring in rings:
        arr = np.asarray(ring, dtype=np.float64)
        simp = rdp_np(arr, tol)
        if len(simp) >= 4:
            # 环闭合
            if simp[0][0] != simp[-1][0] or simp[0][1] != simp[-1][1]:
                simp = np.vstack([simp, simp[:1]])
            out.append([[q(x), q(y)] for x, y in simp])
    return out


def build_natural() -> list[dict]:
    import shapely.geometry as sg

    features: list[dict] = []
    stats: dict[str, int] = {}

    areas = json.loads((TMP / "areas.geojson").read_text())
    for f in areas["features"]:
        props = f.get("properties") or {}
        natural = props.get("natural")
        landuse = props.get("landuse")
        waterway = props.get("waterway")
        cls = None
        if natural in NATURAL_CLASS:
            cls = NATURAL_CLASS[natural]
        elif landuse in LANDUSE_CLASS:
            cls = LANDUSE_CLASS[landuse]
        elif waterway in WATERWAY_AREA_CLASS:
            cls = WATERWAY_AREA_CLASS[waterway]
        if cls is None:
            continue
        try:
            geom = sg.shape(f["geometry"])
        except Exception:
            continue
        if not geom.is_valid:
            geom = geom.buffer(0)
        if geom.is_empty:
            continue
        min_area = MIN_AREA_DEG2.get(cls, 0.00005)
        if geom.area < min_area:
            continue
        geom = geom.simplify(SIMPLIFY_TOL[cls], preserve_topology=True)
        if geom.is_empty or geom.area < min_area * 0.5:
            continue
        name = props.get("name")
        p = {"c": cls}
        if name and cls in ("water", "riverbank"):
            p["n"] = name  # 湖库名称（前端 z≥8 显示）
        feats_out = []
        geoms = geom.geoms if geom.geom_type == "MultiPolygon" else [geom]
        for g in geoms:
            if g.geom_type != "Polygon" or g.is_empty:
                continue
            # shapely simplify 后顶点为输入子集（全精度），统一量化到 5 位小数
            rings = [
                [[q(x), q(y)] for x, y in g.exterior.coords]
            ] + [
                [[q(x), q(y)] for x, y in r.coords] for r in g.interiors
            ]
            rings = [r for r in rings if len(r) >= 4]
            if not rings:
                continue
            feats_out.append(
                {
                    "type": "Feature",
                    "geometry": {"type": "Polygon", "coordinates": rings},
                    "properties": p,
                }
            )
        # MultiPolygon 输出为单 Feature（MapLibre 兼容更好且省字节）
        if feats_out:
            if len(feats_out) == 1:
                features.append(feats_out[0])
            else:
                features.append(
                    {
                        "type": "Feature",
                        "geometry": {
                            "type": "MultiPolygon",
                            "coordinates": [f["geometry"]["coordinates"] for f in feats_out],
                        },
                        "properties": p,
                    }
                )
            stats[cls] = stats.get(cls, 0) + 1

    lines = json.loads((TMP / "lines.geojson").read_text())
    for f in lines["features"]:
        props = f.get("properties") or {}
        cls = WATERWAY_LINE_CLASS.get(props.get("waterway"))
        if cls is None:
            continue
        rings = coord_seq(f["geometry"])
        if not rings:
            continue
        total_len = sum(line_length(r) for r in rings)
        if total_len < MIN_LINE_LEN_DEG.get(cls, 0.001):
            continue
        name = props.get("name")
        p = {"c": cls}
        if name:
            p["n"] = name
        kept: list[list[list[float]]] = []
        for r in rings:
            simp = rdp_np(np.asarray(r, dtype=np.float64), SIMPLIFY_TOL[cls])
            if len(simp) >= 2:
                kept.append([[q(x), q(y)] for x, y in simp])
        if not kept:
            continue
        geom = (
            {"type": "LineString", "coordinates": kept[0]}
            if len(kept) == 1
            else {"type": "MultiLineString", "coordinates": kept}
        )
        features.append({"type": "Feature", "geometry": geom, "properties": p})
        stats[cls] = stats.get(cls, 0) + 1

    print(f"  自然要素 {len(features)} 条：{json.dumps(stats, ensure_ascii=False)}")
    return features


def build_places() -> list[dict]:
    points = json.loads((TMP / "points.geojson").read_text())
    features: list[dict] = []
    stats: dict[str, int] = {}
    for f in points["features"]:
        props = f.get("properties") or {}
        place = props.get("place")
        name = props.get("name")
        if place not in PLACE_KEEP or not name:
            continue
        coords = f["geometry"]["coordinates"]
        features.append(
            {
                "type": "Feature",
                "geometry": {"type": "Point", "coordinates": [q(coords[0]), q(coords[1])]},
                "properties": {"c": place, "n": name},
            }
        )
        stats[place] = stats.get(place, 0) + 1
    print(f"  地名 {len(features)} 个：{json.dumps(stats, ensure_ascii=False)}")
    return features


# —— GCJ 变体：摊平全部坐标一次变换，再按遍历序回填 ——


def gcj_variant(fc: dict) -> dict:
    flat: list[float] = []
    lat: list[float] = []

    def is_coord(node: list) -> bool:
        return (
            len(node) == 2
            and isinstance(node[0], (int, float))
            and isinstance(node[1], (int, float))
            and not isinstance(node[0], bool)
        )

    def walk(node) -> None:
        if isinstance(node, list):
            if is_coord(node):
                flat.append(node[0])
                lat.append(node[1])
            else:
                for x in node:
                    walk(x)
        elif isinstance(node, dict):
            # 只下钻几何容器：properties 的值都是标量/字符串，不会被误当坐标
            for key in ("coordinates", "geometry", "features"):
                if key in node:
                    walk(node[key])

    walk(fc)
    assert len(flat) == len(lat) and len(flat) > 0
    glng, glat = wgs84_to_gcj02_np(np.asarray(flat), np.asarray(lat))
    it = iter(zip((q(v) for v in glng.tolist()), (q(v) for v in glat.tolist())))

    def rebuild(node):
        if isinstance(node, list):
            if is_coord(node):
                a, b = next(it)
                return [a, b]
            return [rebuild(x) for x in node]
        if isinstance(node, dict):
            return {
                k: (rebuild(v) if k in ("coordinates", "geometry", "features") else v)
                for k, v in node.items()
            }
        return node

    out = rebuild(fc)

    # 自检：首坐标确实发生偏移（防再次静默 no-op）
    def first_xy(geom: dict) -> tuple[float, float]:
        c: object = geom["coordinates"]
        while isinstance(c, (list, tuple)) and len(c) > 0 and isinstance(c[0], (list, tuple)):
            c = c[0]
        assert isinstance(c, (list, tuple)) and len(c) >= 2
        return float(c[0]), float(c[1])

    x0, _ = first_xy(fc["features"][0]["geometry"])
    x1, _ = first_xy(out["features"][0]["geometry"])
    assert abs(x1 - x0) > 1e-4, "GCJ 变体未生效（偏移为零）"
    return out


def write_gz(fc: dict, out: Path) -> None:
    raw = json.dumps(fc, separators=(",", ":"), ensure_ascii=True).encode()
    out.write_bytes(gzip.compress(raw, compresslevel=9, mtime=0))
    print(f"  {out.name}: 原始 {len(raw) / 1e6:.2f} MB → gzip {out.stat().st_size / 1e6:.2f} MB")


def qa_render(natural: list[dict], places: list[dict]) -> None:
    import matplotlib

    matplotlib.use("Agg")
    import matplotlib.font_manager as fm

    for f in [
        "/usr/share/fonts/truetype/chinese/NotoSansSC-Regular.ttf",
        "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
    ]:
        try:
            fm.fontManager.addfont(f)
        except Exception:
            pass
    import matplotlib.pyplot as plt

    plt.rcParams["font.sans-serif"] = ["Noto Sans SC", "DejaVu Sans"]
    plt.rcParams["axes.unicode_minus"] = False
    fig, ax = plt.subplots(figsize=(12, 10), dpi=110, constrained_layout=True)
    ax.set_facecolor("#F5F3F0")
    fill_colors = {
        "wood": "#C9E3C3",
        "forest": "#C9E3C3",
        "grass": "#DCEED3",
        "scrub": "#D5E8D2",
        "farm": "#EFE9CB",
        "wetland": "#C9E4E0",
        "water": "#A8CFF0",
        "riverbank": "#A8CFF0",
    }
    line_colors = {"river": "#8FBEE8", "stream": "#A8CFF0", "canal": "#96C4EA"}
    for f in natural:
        geom = f["geometry"]
        c = f["properties"]["c"]
        if geom["type"] in ("Polygon", "MultiPolygon"):
            polys = [geom["coordinates"]] if geom["type"] == "Polygon" else geom["coordinates"]
            for poly in polys:
                for ring in poly:
                    xs, ys = zip(*ring)
                    ax.fill(xs, ys, color=fill_colors.get(c, "#DDD"), lw=0)
        else:
            segs = (
                [geom["coordinates"]]
                if geom["type"] == "LineString"
                else geom["coordinates"]
            )
            for seg in segs:
                xs, ys = zip(*seg)
                ax.plot(xs, ys, color=line_colors.get(c, "#99C"), lw=0.5 if c == "stream" else 0.9)
    shown = set()
    for f in places:
        if f["properties"]["c"] in ("city", "province") :
            x, y = f["geometry"]["coordinates"]
            if f"{x:.1f},{y:.1f}" in shown:
                continue
            shown.add(f"{x:.1f},{y:.1f}")
            ax.text(x, y, f["properties"]["n"], fontsize=8, ha="center", color="#444")
    ax.set_xlim(121, 135)
    ax.set_ylim(43, 54)
    ax.set_title("HLJ natural QA render (tmp only)")
    fig.savefig(QA_PNG)
    plt.close(fig)
    print(f"  QA 渲染 → {QA_PNG}")


def main() -> int:
    dry = "--dry-run" in sys.argv
    if not PBF.exists():
        print(f"PBF 不存在：{PBF}", file=sys.stderr)
        return 1
    print("[1/4] osmconf + ogr2ogr 导出（points/lines/areas）")
    write_osmconf()
    run_ogr()
    print("[2/4] 分类 + 简化 + 过滤")
    natural = build_natural()
    places = build_places()
    print("[3/4] GCJ-02 变体（numpy 向量化）")
    nat_fc = {"type": "FeatureCollection", "features": natural}
    pla_fc = {"type": "FeatureCollection", "features": places}
    nat_gcj = gcj_variant(nat_fc)
    pla_gcj = gcj_variant(pla_fc)
    print("[4/4] 写出 gzip 资产 + QA 渲染")
    if not dry:
        OUT_DIR.mkdir(parents=True, exist_ok=True)
        write_gz(nat_fc, OUT_NAT)
        write_gz(nat_gcj, OUT_NAT_GCJ)
        write_gz(pla_fc, OUT_PLA)
        write_gz(pla_gcj, OUT_PLA_GCJ)
        qa_render(natural, places)
    return 0


if __name__ == "__main__":
    sys.exit(main())
