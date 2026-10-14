#!/usr/bin/env python3
"""W31: add the resource natural key (id) to the committed basestation map
layers. The layers were generated before the station resources carried
ledger identity (v20 geo columns); the map click-through (station →
resource detail) needs the natural key on the feature. extract.py gains the
same property at the source for future regenerations, but re-running the
full pipeline needs the external OSM/OpenCelliD inputs — this script
patches the COMMITTED artifacts deterministically instead: realdata-core.json
(the committed source of truth both artifacts derive from) supplies the
codes, features match by exact 5-digit-quantized coordinate (verified 1198/
1198), output goes through the same write_geojson_gz helper (gzip mtime=0,
sorted order preserved) so the result is byte-stable.

Run: python3 scripts/realdata/patch_station_ids.py  (idempotent).
"""
from __future__ import annotations

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from common import CORE_JSON, PUBLIC_MAPDATA, load_gz_json, write_geojson_gz  # noqa: E402


def patch(path):
    stations = json.loads(CORE_JSON.read_text(encoding="utf-8"))["stations"]
    by_coord = {(round(st["lng"], 5), round(st["lat"], 5)): st["code"] for st in stations}
    fc = load_gz_json(path)
    patched = missed = 0
    for f in fc["features"]:
        lng, lat = f["geometry"]["coordinates"]
        code = by_coord.get((round(lng, 5), round(lat, 5)))
        if code is None:
            missed += 1
            continue
        f["properties"]["id"] = code
        patched += 1
    if missed:
        raise SystemExit(f"{path.name}: {missed} features without a matching station code")
    return write_geojson_gz(path, fc["features"], dual_gcj=True), patched


def main() -> None:
    for name in ("hlj-basestations.geojson.gz",):
        path = PUBLIC_MAPDATA / name
        # write_geojson_gz writes both the WGS84 file and the -gcj variant
        # (dual_gcj=True) — the GCJ variant's coordinates are re-projected
        # but the PROPERTIES (including the new id) ride along unchanged.
        written, patched = patch(path)
        print(f"{name}: {patched} features id'd; files written: {sorted(written)}")


if __name__ == "__main__":
    main()
