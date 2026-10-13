#!/usr/bin/env python3
"""extract.py — W26：真实公开数据提取（v0.8.0 真实数据里程碑）。

输入（外部，不入库，见 manifest 记录的来源与许可）：
- /home/z/my-project/mapdata/heilongjiang-2026-10-12.osm.pbf
    Geofabrik 黑龙江 extract（© OpenStreetMap contributors，ODbL 1.0）
- /home/z/my-project/mapdata/cell_towers.csv.gz
    OpenCelliD 全球快照 2026-10-06（© OpenCelliD / Unwired Labs，CC BY-SA 4.0）
    经 HuggingFace 镜像 yuiseki/opencellid 获取（含 provenance.yaml）
- web/public/mapdata/hlj-roads-main.geojson.gz 等道路/铁路图层（同 PBF 派生）

产出：
- web/public/mapdata/hlj-cables.geojson.gz (+gcj)         干线走廊（真实公路/铁路几何）
- web/public/mapdata/hlj-basestations.geojson.gz (+gcj)   无线基站（真实 OpenCelliD 位置）
- testdata/realdata/realdata-core.json                     真实数据核心（站点/干线/基站）

走廊口径（诚实声明）：光缆沿公路/铁路敷设是公开常识；本管线按**线路名**
（如 绥满高速/滨洲线/嫩林线/京漠线）选取 OSM 真实道路/铁路几何并做 RDP 简化
（容差 ≈ 150m），代表「干线走廊」，不是逐米光缆路由。走廊长度为沿线
haversine 累计（真实几何量测）。

确定性：坐标 5 位量化 + 全序排序 + gzip mtime=0 → 同输入字节级同输出。
"""

from __future__ import annotations

import csv
import io
import json
import subprocess
import sys
from collections import Counter, defaultdict
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from common import (  # noqa: E402
    CORE_JSON,
    MAPDATA,
    PUBLIC_MAPDATA,
    bbox_of,
    haversine_m,
    load_gz_json,
    path_length_m,
    q,
    rdp_np,
    seg_intersects_bbox,
    write_geojson_gz,
)

import numpy as np  # noqa: E402

# ---------------------------------------------------------------------------
# 13 地市（行署）+ 干线走廊定义：全部为真实地理走廊（公路名/铁路名选自 OSM）
# ---------------------------------------------------------------------------

PREFECTURES = [
    "哈尔滨", "齐齐哈尔", "牡丹江", "佳木斯", "大庆", "鸡西", "双鸭山",
    "伊春", "七台河", "鹤岗", "绥化", "黑河", "加格达奇",  # 加格达奇 = 大兴安岭地区首府
]

# waypoints：沿线真实局站锚点（地名匹配 OSM place 节点）。
# road_sel / rail_sel：OSM 道路/铁路 name 精确或包含匹配（多选一命中即选）。
TRUNKS = [
    {"code": "TRK-HDQ", "name": "哈大齐干线", "a": "哈尔滨", "z": "齐齐哈尔",
     "waypoints": ["哈尔滨", "肇东", "安达", "大庆", "齐齐哈尔"],
     "road_sel": ["绥满高速", "绥满线", "哈大齐高速"], "rail_sel": ["滨洲线"]},
    {"code": "TRK-HM", "name": "哈牡干线", "a": "哈尔滨", "z": "牡丹江",
     "waypoints": ["哈尔滨", "阿城", "尚志", "牡丹江"],
     "road_sel": ["绥满高速", "哈牡高速", "绥满线"], "rail_sel": ["滨绥线", "哈牡客专线"]},
    {"code": "TRK-HJ", "name": "哈佳干线", "a": "哈尔滨", "z": "佳木斯",
     "waypoints": ["哈尔滨", "宾县", "方正", "依兰", "佳木斯"],
     "road_sel": ["哈同高速", "哈同线"], "rail_sel": ["哈佳线", "绥佳线", "滨北线"]},
    {"code": "TRK-JM", "name": "佳牡干线", "a": "佳木斯", "z": "牡丹江",
     "waypoints": ["佳木斯", "桦南", "七台河", "鸡西", "牡丹江"],
     "road_sel": ["鹤大高速", "鹤大线"], "rail_sel": ["图佳线"]},
    {"code": "TRK-QJ", "name": "齐加干线", "a": "齐齐哈尔", "z": "加格达奇",
     "waypoints": ["齐齐哈尔", "讷河", "嫩江", "加格达奇"],
     "road_sel": ["双嫩高速", "京漠线", "双嫩线"], "rail_sel": ["齐北线", "富嫩线", "嫩林线"]},
    {"code": "TRK-JM2", "name": "加漠干线", "a": "加格达奇", "z": "漠河",
     "waypoints": ["加格达奇", "塔河", "漠河"],
     "road_sel": ["京漠线", "加漠公路"], "rail_sel": ["嫩林线"]},
    {"code": "TRK-HY", "name": "哈伊干线", "a": "哈尔滨", "z": "伊春",
     "waypoints": ["哈尔滨", "绥化", "铁力", "伊春"],
     "road_sel": ["鹤哈高速", "鹤哈线"], "rail_sel": ["哈伊高速线", "滨北线"]},
]

ROAD_FILES = ["hlj-roads-main.geojson.gz", "hlj-roads-daxinganling-full.geojson.gz"]
RAIL_FILES = ["hlj-railways.geojson.gz"]

MNC_OPERATOR = {0: "中国移动", 1: "中国联通", 11: "中国电信", 15: "中国广电"}

OSMCONF = """[general]
closed_ways_are_polygons=place

[points]
report_uninteresting=no
osm_id=yes
attributes=place,name,population
[lines]
report_uninteresting=no
attributes=name
[multipolygons]
report_uninteresting=no
attributes=place,name,population
"""


# ---------------------------------------------------------------------------
# 1. OSM 地名 → 局站（真实名称 + 真实坐标 + OSM id 稳定身份）
# ---------------------------------------------------------------------------

def extract_places() -> list[dict]:
    conf = Path("/tmp/osmconf-places-realdata.ini")
    conf.write_text(OSMCONF, encoding="utf-8")
    out = Path("/tmp/hlj-places-realdata.geojson")
    if out.exists():
        out.unlink()
    subprocess.run(
        ["ogr2ogr", "-f", "GeoJSON", str(out), str(MAPDATA / "heilongjiang-2026-10-12.osm.pbf"),
         "points", "--config", "OSM_CONFIG_FILE", str(conf)],
        check=True, capture_output=True,
    )
    fc = json.loads(out.read_text(encoding="utf-8"))
    places = []
    for f in fc["features"]:
        p = f.get("properties", {})
        place = p.get("place")
        name = p.get("name")
        if not place or not name or place not in ("city", "county", "district", "town"):
            continue
        osm_id_raw = p.get("osm_id") or p.get("id") or f.get("id")
        try:
            osm_id = int(float(osm_id_raw)) if osm_id_raw is not None else None
        except (TypeError, ValueError):
            osm_id = None
        lng, lat = f["geometry"]["coordinates"]
        places.append({
            "osm_id": osm_id, "name": name, "place": place,
            "lng": q(lng), "lat": q(lat),
            "population": p.get("population") or 0,
        })
    # 人口字符串→数字容错
    for pl in places:
        try:
            pl["population"] = int(float(pl["population"]))
        except (TypeError, ValueError):
            pl["population"] = 0
    places.sort(key=lambda x: (x["lat"], x["lng"], x["name"]))
    return places


def match_place(places: list[dict], want: str) -> dict | None:
    """地名匹配：精确 → 去后缀（市/县/区）精确 → startswith（city/county/district 优先）。"""
    for pl in places:
        if pl["name"] == want:
            return pl
    for pl in places:
        if pl["name"].rstrip("市县区") == want and pl["place"] in ("city", "county", "district"):
            return pl
    for pl in places:
        if pl["name"].startswith(want) and pl["place"] in ("city", "county", "district"):
            return pl
    return None


def build_sites(places: list[dict]) -> tuple[list[dict], dict]:
    """选定演示局站：13 地市 + 全部走廊锚点 + 走廊范围内的县/镇（人口排序截断）。"""
    sites: list[dict] = []
    by_key: dict[str, dict] = {}
    trunks_bbox = []

    def add(pl: dict, reason: str):
        osm_id = pl.get("osm_id")
        sid = f"ST{int(osm_id):08d}" if isinstance(osm_id, (int, float)) else None
        if sid is None or sid in by_key:
            return
        s = {
            "site_id": sid, "site_name": pl["name"], "place": pl["place"],
            "lng": pl["lng"], "lat": pl["lat"], "population": pl["population"],
            "reason": reason,
        }
        sites.append(s)
        by_key[sid] = s

    # 走廊锚点 + bbox
    wp_places: dict[str, dict] = {}
    for t in TRUNKS:
        wps = []
        for w in t["waypoints"]:
            pl = match_place(places, w)
            if pl is None:
                raise SystemExit(f"waypoint place not found: {w} ({t['code']})")
            wp_places[f"{t['code']}:{w}"] = pl
            wps.append(pl)
        t["_wp_places"] = wps
        x0 = min(p["lng"] for p in wps); x1 = max(p["lng"] for p in wps)
        y0 = min(p["lat"] for p in wps); y1 = max(p["lat"] for p in wps)
        trunks_bbox.append((x0, y0, x1, y1))
        for p in wps:
            add(p, f"wp:{t['code']}")

    # 13 地市
    for city in PREFECTURES:
        pl = wp_places.get(f"TRK-HDQ:{city}") or match_place(places, city)
        if pl is None:
            pl = match_place(places, city)
        if pl is not None:
            add(pl, "prefecture")

    # 走廊范围内县/镇：county 全收，town 人口前 6
    for t, bb in zip(TRUNKS, trunks_bbox):
        inb = [p for p in places if seg_intersects_bbox([[p["lng"], p["lat"]]], bb, 0.22)]
        for p in inb:
            if p["place"] in ("county", "district"):
                add(p, f"near:{t['code']}")
        towns = [p for p in inb if p["place"] == "town" and p["population"] >= 30000]
        towns.sort(key=lambda x: (-x["population"], x["name"]))
        for p in towns[:6]:
            add(p, f"near:{t['code']}")

    # 每站归属地市（最近地市）+ 区县（site 自身若 county/district 则取己名）
    pref_pts = []
    for city in PREFECTURES:
        pl = match_place(places, city)
        if pl is not None:
            pref_pts.append((city, pl["lng"], pl["lat"]))
    for s in sites:
        best = min(pref_pts, key=lambda c: haversine_m(s["lng"], s["lat"], c[1], c[2]))
        s["city"] = best[0]
        s["district"] = s["site_name"].rstrip("市县区") if s["place"] in ("county", "district") else ""
    sites.sort(key=lambda x: x["site_id"])
    return sites, by_key


# ---------------------------------------------------------------------------
# 2. 干线走廊：按线路名选真实道路/铁路几何 → RDP 简化 → 真实里程
# ---------------------------------------------------------------------------

RDP_TOL = 0.0015  # ≈150m 简化容差（走廊级）


def name_matches(name: str, sels: list[str]) -> bool:
    if not name:
        return False
    return any(sel in name for sel in sels)


def _dedup_key(r: dict) -> tuple:
    """道路去重键：名称 + 首尾顶点量化（文件无 osm id 时的稳定身份）。"""
    c = r["coords"]
    return (r["name"], round(c[0][0], 5), round(c[0][1], 5), round(c[-1][0], 5), round(c[-1][1], 5))


def extract_corridors() -> list[dict]:
    roads: list[dict] = []   # {name, id, coords}
    rails: list[dict] = []
    for fn in ROAD_FILES:
        fc = load_gz_json(PUBLIC_MAPDATA / fn)
        for f in fc["features"]:
            p = f.get("properties", {})
            roads.append({"name": p.get("name") or "", "id": p.get("id"),
                          "coords": f["geometry"]["coordinates"]})
    for fn in RAIL_FILES:
        fc = load_gz_json(PUBLIC_MAPDATA / fn)
        for f in fc["features"]:
            p = f.get("properties", {})
            rails.append({"name": p.get("name") or "", "id": p.get("id"),
                          "coords": f["geometry"]["coordinates"]})

    out = []
    for t in TRUNKS:
        wps = t["_wp_places"]
        bb = (min(p["lng"] for p in wps), min(p["lat"] for p in wps),
              max(p["lng"] for p in wps), max(p["lat"] for p in wps))
        seen = set()
        segs = {"road": [], "rail": []}
        for kind, src in (("road", roads), ("rail", rails)):
            for r in src:
                if not name_matches(r["name"], t[f"{kind}_sel"]):
                    continue
                fid = _dedup_key(r)
                if fid in seen:
                    continue
                if not seg_intersects_bbox(r["coords"], bb, 0.15):
                    continue
                seen.add(fid)
                arr = np.asarray(r["coords"], dtype=float).reshape(-1, 2)
                simp = rdp_np(arr, RDP_TOL)
                segs[kind].append([[q(c[0]), q(c[1])] for c in simp.tolist()])
        # 逐段真实长度（去重后累计）
        km = {}
        for kind in ("road", "rail"):
            total = sum(path_length_m(c) for c in segs[kind])
            km[kind] = round(total / 1000.0, 1)
        # 走廊几何按「距起点最近」排序稳定化（确定性）
        w0 = (wps[0]["lng"], wps[0]["lat"])
        for kind in ("road", "rail"):
            segs[kind].sort(key=lambda c: (round(haversine_m(c[0][0], c[0][1], w0[0], w0[1]), 0),
                                           round(c[0][0], 5), round(c[0][1], 5)))
        # 锚点链测地里程（光缆 length_km 的口径：沿走廊锚点局的测地距离累计；
        # 并集几何长度 road_km/rail_km 含平行车道/复线重复计数，只作图层参考）
        anchor_m = 0.0
        for i in range(len(wps) - 1):
            anchor_m += haversine_m(wps[i]["lng"], wps[i]["lat"], wps[i + 1]["lng"], wps[i + 1]["lat"])
        out.append({
            "code": t["code"], "name": t["name"], "a": t["a"], "z": t["z"],
            "waypoints": [p["name"] for p in wps],
            "waypoint_ids": [f"ST{int(p['osm_id']):08d}" for p in wps],
            "road_km": km["road"], "rail_km": km["rail"],
            "anchor_km": round(anchor_m / 1000.0, 1),
            "road_fragments": len(segs["road"]), "rail_fragments": len(segs["rail"]),
            "geom": {"road": segs["road"], "rail": segs["rail"]},
        })
    return out


# ---------------------------------------------------------------------------
# 3. OpenCelliD → 真实基站（~110m 网格聚类，主小区 CGI 为稳定身份）
# ---------------------------------------------------------------------------

def extract_stations() -> list[dict]:
    import gzip as gz
    cells = []
    with gz.open(str(MAPDATA / "cell_towers.csv.gz"), "rt", encoding="utf-8") as fh:
        for r in csv.DictReader(fh):  # 流式：全球快照 ~5,000 万行不物化
            if r["mcc"] != "460":
                continue
            lng, lat = float(r["lon"]), float(r["lat"])
            if 121.0 <= lng <= 135.5 and 43.2 <= lat <= 53.7:
                cells.append(r)
    grid: dict[tuple, list] = defaultdict(list)
    for r in cells:
        lat, lng = float(r["lat"]), float(r["lon"])
        grid[(r["net"], round(lat, 3), round(lng, 3))].append(r)
    stations = []
    for (net, glat, glng), members in grid.items():
        primary = max(members, key=lambda r: (int(r["samples"]), int(r["cell"])))
        radios = sorted({r["radio"] for r in members})
        mean_lng = round(sum(float(r["lon"]) for r in members) / len(members), 5)
        mean_lat = round(sum(float(r["lat"]) for r in members) / len(members), 5)
        stations.append({
            "mnc": int(net),
            "operator": MNC_OPERATOR.get(int(net), f"MNC{net}"),
            "lac": int(primary["area"]), "cell": int(primary["cell"]),
            "code": f"BS-{int(net):02d}-{primary['area']}-{primary['cell']}",
            "radios": radios, "cell_count": len(members),
            "samples": sum(int(r["samples"]) for r in members),
            "lng": mean_lng, "lat": mean_lat,
            "updated": max(int(r["updated"]) for r in members),
        })
    stations.sort(key=lambda s: s["code"])
    return stations


def bind_stations(stations: list[dict], sites: list[dict]) -> None:
    """基站 → 最近局站（命名归属）+ 归属地市。"""
    for st in stations:
        best = min(sites, key=lambda s: haversine_m(st["lng"], st["lat"], s["lng"], s["lat"]))
        st["near_site"] = best["site_id"]
        st["near_name"] = best["site_name"]
        st["near_km"] = round(haversine_m(st["lng"], st["lat"], best["lng"], best["lat"]) / 1000.0, 1)
        st["city"] = best["city"]
    # 基站名：基站·<局站名>·<序号>（同局站内按 code 序）
    per_site: dict[str, int] = defaultdict(int)
    for st in stations:  # 已按 code 排序 → 序号确定
        per_site[st["near_site"]] += 1
        st["name"] = f"基站·{st['near_name']}·{per_site[st['near_site']]:03d}"


# ---------------------------------------------------------------------------
# 4. 地图图层输出 + 真实数据核心
# ---------------------------------------------------------------------------

def write_layers(corridors: list[dict], stations: list[dict]) -> dict[str, int]:
    cable_features = []
    for t in corridors:
        for kind, kmkey in (("road", "road_km"), ("rail", "rail_km")):
            if not t["geom"][kind]:
                continue
            code = t["code"] if kind == "road" else t["code"] + "R"
            cable_features.append({
                "type": "Feature",
                "properties": {"code": code, "n": t["name"], "km": t[kmkey], "k": kind},
                "geometry": {"type": "MultiLineString", "coordinates": t["geom"][kind]},
            })
    written = write_geojson_gz(PUBLIC_MAPDATA / "hlj-cables.geojson.gz", cable_features, dual_gcj=True)

    bs_features = [{
        "type": "Feature",
        "properties": {"o": st["operator"], "r": ",".join(st["radios"]), "c": st["cell_count"],
                       "s": st["samples"]},
        "geometry": {"type": "Point", "coordinates": [st["lng"], st["lat"]]},
    } for st in stations]
    written.update(write_geojson_gz(PUBLIC_MAPDATA / "hlj-basestations.geojson.gz", bs_features, dual_gcj=True))
    return written


def main() -> None:
    print("== 1/4 OSM places ==")
    places = extract_places()
    print(f"   place nodes (city/county/district/town): {len(places)}")

    print("== 2/4 sites ==")
    sites, _by_key = build_sites(places)
    print(f"   sites: {len(sites)} (prefectures+waypoints+corridor county/town)")

    print("== 3/4 corridors ==")
    corridors = extract_corridors()
    for t in corridors:
        print(f"   {t['code']} {t['name']}: road {t['road_km']}km/{t['road_fragments']}段"
              f" rail {t['rail_km']}km/{t['rail_fragments']}段  wp={t['waypoints']}")

    print("== 4/4 stations ==")
    stations = extract_stations()
    bind_stations(stations, sites)
    print(f"   stations: {len(stations)} ({sum(s['cell_count'] for s in stations)} cells)"
          f"  operators={dict(Counter(s['operator'] for s in stations))}")

    written = write_layers(corridors, stations)
    for p, n in sorted(written.items()):
        print(f"   layer: {Path(p).name}  ({n/1024:.0f} KB uncompressed)")

    CORE_JSON.parent.mkdir(parents=True, exist_ok=True)
    core = {
        "meta": {
            "generated_by": "scripts/realdata/extract.py",
            "pbf": "heilongjiang-2026-10-12.osm.pbf (Geofabrik HLJ extract, ODbL)",
            "opencellid": "cell_towers.csv.gz 2026-10-06 snapshot (CC BY-SA 4.0)",
            "notes": "corridor = OSM road/rail geometry selected by line name; RDP tol ~150m; "
                     "length = haversine along geometry (real measurement of the corridor)",
        },
        "sites": [{k: v for k, v in s.items()} for s in sites],
        "trunks": [{k: v for k, v in t.items() if k != "geom"} for t in corridors],
        "stations": stations,
    }
    CORE_JSON.write_text(json.dumps(core, ensure_ascii=False, separators=(",", ":"), sort_keys=True),
                         encoding="utf-8")
    print(f"   core: {CORE_JSON} ({CORE_JSON.stat().st_size/1024:.0f} KB)")


if __name__ == "__main__":
    main()
