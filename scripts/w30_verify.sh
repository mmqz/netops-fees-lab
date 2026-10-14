#!/usr/bin/env bash
# W30 verification: import the regenerated realdata dataset and measure the
# issue #40/#42/#45 acceptance numbers (isolation %, status coverage, W002,
# serves/uplink_to/mounted_on/protects edges, workbench links).
set -euo pipefail
cd /home/z/my-project/netops-lab
export PATH=$PATH:/home/z/.local/go/bin
BIN=bin/netopslab
go build -o $BIN ./cmd/netopslab
WORK=$(mktemp -d /tmp/netops-w30.XXXXXX)
DB="$WORK/netops.db"
REALDATA=testdata/realdata

ORDER=(
  sites machine_rooms customers vpn_instances rings igp_domains devices boards
  base_stations ports racks odf odf_terminals ducts manholes poles
  optical_cables cable_segments splice_boxes fibers otn_ochs sdh_timeslots
  circuits protection_groups ippools services leased_lines bbus rrus olts pon_ports onus
)

total=0; failed=0
for wb in "${ORDER[@]}"; do
  f="$REALDATA/$wb.xlsx"
  $BIN analyze -file "$f" > "$WORK/a.json" 2>/dev/null
  python3 -c "import json,sys; json.dump(json.load(open('$WORK/a.json'))['suggestedMapping'], open('$WORK/m.json','w'), ensure_ascii=False)"
  $BIN import --file "$f" --map "$WORK/m.json" --db "$DB" > "$WORK/i.json" 2> "$WORK/i.err" || true
  python3 - "$WORK/i.json" "$wb" <<'PY' >> "$WORK/sum.txt"
import json, sys
d = json.load(open(sys.argv[1], encoding="utf-8"))
s = d.get("summary", d)
print(f"{sys.argv[2]}\t{s.get('rowsImported',0)}\t{s.get('rowsFailed',0)}")
PY
done
echo "=== import summary ==="
cat "$WORK/sum.txt"
total=$(awk -F'\t' '{s+=$2} END {print s}' "$WORK/sum.txt")
failed=$(awk -F'\t' '{s+=$3} END {print s}' "$WORK/sum.txt")
echo "TOTAL imported=$total failed=$failed"

echo "=== ptp script import (issue #40: ptp_port via samples) ==="
$BIN analyze -file samples/ptp-config-script.txt > "$WORK/ptp.json" 2>/dev/null || true
head -c 400 "$WORK/ptp.json"; echo
python3 -c "import json; d=json.load(open('$WORK/ptp.json')); json.dump(d['suggestedMapping'], open('$WORK/ptp-map.json','w'), ensure_ascii=False)" 2>/dev/null &&   $BIN import --file samples/ptp-config-script.txt --map "$WORK/ptp-map.json" --db "$DB" 2>&1 | head -5 || true

echo "=== quality issues (W001/W002 counts) ==="
$BIN analyze -file "$REALDATA/devices.xlsx" >/dev/null 2>&1 || true
python3 - "$DB" <<'PY'
import sqlite3, sys, json
db = sqlite3.connect(sys.argv[1])
q = lambda sql, *a: db.execute(sql, a).fetchall()
print("resources:", q("SELECT COUNT(*) FROM resources")[0][0])
print("by status:", dict(q("SELECT COALESCE(status,'(null)'), COUNT(*) FROM resources GROUP BY status")))
nulls = q("SELECT COUNT(*) FROM resources WHERE status IS NULL")[0][0]
total = q("SELECT COUNT(*) FROM resources")[0][0]
print(f"no-status: {nulls}/{total} = {100.0*nulls/total:.1f}%  (target < 20%)")
print("geo columns populated:", q("SELECT COUNT(*) FROM resources WHERE geo_lat IS NOT NULL")[0][0])
rels = q("SELECT relation_type, COUNT(*) FROM relations GROUP BY relation_type ORDER BY 2 DESC")
for r in rels: print(f"  {r[0]:22s} {r[1]}")
iso = q("""
SELECT COUNT(*) FROM resources r WHERE NOT EXISTS (
  SELECT 1 FROM relations rel WHERE rel.from_resource_id = r.id OR rel.to_resource_id = r.id
)""")[0][0]
print(f"isolated: {iso}/{total} = {100.0*iso/total:.1f}%  (target < 30%)")
print("issues by code:", dict(q("SELECT rule_code, COUNT(*) FROM issues GROUP BY rule_code")))
# base_station attribute thickness
bs = q("SELECT attributes FROM resources WHERE resource_type='base_station' LIMIT 1")
if bs:
    attrs = json.loads(bs[0][0])
    print("base_station attribute count:", len(attrs), sorted(attrs.keys()))
PY
echo "WORK=$WORK"
