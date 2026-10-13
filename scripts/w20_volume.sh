#!/bin/bash
# W20 volume measurement (issue #27 / design §12-B6): ingest + query + size
# at scale through the REAL CLI, with honest numbers for the disposition.
# Run: bash scripts/w20_volume.sh [devices] [ports] [days] [metrics]
set -euo pipefail
export PATH=$PATH:/home/z/.local/go/bin

DEVICES=${1:-500}
PORTS=${2:-20}
DAYS=${3:-30}
METRICS=${4:-8}

cd "$(dirname "$0")/.."
go build -o /tmp/netopslab-vol ./cmd/netopslab

WORK=$(mktemp -d /tmp/w20vol.XXXXXX)
DB="$WORK/vol.db"
MAPPING="$WORK/map.json"
trap 'rm -rf "$WORK"' EXIT

echo "=== synthdata: $DEVICES devices × $PORTS ports × $DAYS days × $METRICS metrics ==="
SYNTH_START=$(date +%s)
/tmp/netopslab-vol synthdata --out "$WORK/data" --devices "$DEVICES" --ports "$PORTS" --days "$DAYS" --scenario degrade --seed 2026 --metrics "$METRICS" > "$WORK/synth.json"
SYNTH_SEC=$(( $(date +%s) - SYNTH_START ))
echo "synthdata wall-clock: ${SYNTH_SEC}s"

# Import the resource workbook first (devices + ports).
/tmp/netopslab-vol analyze --file "$WORK/data/resources.xlsx" > "$WORK/analysis.json"
python3 - "$WORK/analysis.json" "$WORK/resmap.json" << 'PYEOF'
import json, sys
a = json.load(open(sys.argv[1]))
sm = a.get('suggestedMapping') or {}
sheets = sm.get('sheets') or []
if not sheets:
    print('no suggested mapping sheets found', file=sys.stderr); sys.exit(1)
json.dump(sm, open(sys.argv[2], 'w', encoding='utf-8'))
PYEOF
/tmp/netopslab-vol import --file "$WORK/data/resources.xlsx" --map "$WORK/resmap.json" --db "$DB" > "$WORK/import.json"
python3 -c "
import json
s = json.load(open('$WORK/import.json'))
print('resource import:', s.get('summary', s))"

# Suggest the metric mapping once from day 1, reuse for every day.
/tmp/netopslab-vol ingest --file "$WORK/data/perf-day-01.xlsx" --suggest > "$WORK/ingest-analysis.json"
python3 - "$WORK/ingest-analysis.json" "$MAPPING" << 'PYEOF'
import json, sys
a = json.load(open(sys.argv[1]))
json.dump(a['suggestedMapping'], open(sys.argv[2], 'w', encoding='utf-8'))
PYEOF

echo "=== ingest $DAYS daily files (the REAL channel) ==="
INGEST_START=$(date +%s%3N)
PER_FILE_MS=()
for d in $(seq 1 "$DAYS"); do
    DAY=$(printf "perf-day-%02d.xlsx" "$d")
    T0=$(date +%s%3N)
    /tmp/netopslab-vol ingest --file "$WORK/data/$DAY" --map "$MAPPING" --db "$DB" > "$WORK/ingest-$d.json"
    T1=$(date +%s%3N)
    PER_FILE_MS+=($((T1 - T0)))
done
INGEST_MS=$(( $(date +%s%3N) - INGEST_START ))

# Per-file P95 (nearest-rank on sorted values).
P95=$(printf '%s\n' "${PER_FILE_MS[@]}" | sort -n | awk -v n="$DAYS" 'BEGIN{r=int((n*95+99)/100); if(r<1)r=1} NR==r{print; exit}')
POINTS=$(python3 -c "
import json
total=0
for d in range(1, $DAYS+1):
    s=json.load(open('$WORK/ingest-%d.json'%d))
    total += s.get('pointsWritten', 0)
print(total)")
ROWS=$(python3 -c "
import json
total=0
for d in range(1, $DAYS+1):
    s=json.load(open('$WORK/ingest-%d.json'%d))
    total += s.get('rowsTotal', 0)
print(total)")

echo "=== anomaly scan (the §7.3 daily-inspection core call) ==="
SCAN1=$(date +%s%3N)
curl -s "localhost:1" > /dev/null 2>&1 || true
# Start a real server for the scan (the endpoint path under test).
/tmp/netopslab-vol serve --db "$DB" --addr 127.0.0.1:18920 --data-dir "$WORK/data-dir" > "$WORK/serve.log" 2>&1 &
SERVE_PID=$!
sleep 2
SCAN_TIMES=()
for i in 1 2 3; do
    T0=$(date +%s%3N)
    curl -s "http://127.0.0.1:18920/api/metrics/anomalies?metricKey=optical_power_rx_avg_dbm&threshold=0.5" > "$WORK/scan-$i.json"
    T1=$(date +%s%3N)
    SCAN_TIMES+=($((T1 - T0)))
done
ANOM=$(python3 -c "import json; print(json.load(open('$WORK/scan-1.json'))['total'])")
kill $SERVE_PID 2>/dev/null || true

DB_BYTES=$(stat -c %s "$DB")

echo
echo "===== W20 VOLUME RESULTS ====="
echo "scenario:        $DEVICES devices × $PORTS ports × $DAYS days × $METRICS metrics (degrade seed 2026)"
echo "rows ingested:   $ROWS"
echo "points written:  $POINTS"
echo "ingest total:    ${INGEST_MS}ms  ($(( INGEST_MS / 1000 ))s)"
echo "ingest rows/s:   $(( ROWS * 1000 / (INGEST_MS + 1) ))"
echo "per-file P95:    ${P95}ms"
echo "scan latencies:  ${SCAN_TIMES[*]}ms (3 runs, optical_power_rx_avg_dbm threshold=0.5)"
echo "anomaly items:   $ANOM"
echo "DB size:         $DB_BYTES bytes ($(( DB_BYTES / 1024 / 1024 ))MB)"
echo "bytes/point:     $(python3 -c "print(round($DB_BYTES / max($POINTS,1), 1))")"
echo
echo "===== extrapolation to §12-B6 full scale (200k ports × 10 metrics × 365 days = 730M points) ====="
python3 - "$ROWS" "$INGEST_MS" "$DB_BYTES" "$POINTS" << 'PYEX'
import sys
rows, ingest_ms, db_bytes, points = (int(x) for x in sys.argv[1:5])
if points == 0:
    sys.exit(0)
full_rows = 200_000          # rows per day at full scale
full_points = 730_000_000
day_ms = ingest_ms / (rows / full_rows) if rows else 0
gb = db_bytes / points * full_points / 1e9
print(f"import/day extrapolated: {day_ms/1000:.0f}s (threshold 30s -> {'TRIGGERS stage 2' if day_ms > 30_000 else 'ok'})")
print(f"DB size extrapolated:    {gb:.0f} GB (single-file positioning -> {'TRIGGERS stage 2' if gb > 10 else 'ok'})")
print("(linear extrapolation, labeled as such — honest, not measured)")
PYEX
echo "================================"
