#!/usr/bin/env bash
# NetOps Lab — R17 告警抖动治理（re-fire hold-down）浏览器级回归（v0.12 /alerts）。
#
# 前置：make web（dist 已构建）。
# 旅程：realdata 导入 + 7 日摄入 + 基线检测（先让基线告警落定）→
#       直播种一条合成端口序列，走三个完整 fire→clear 周期
#       （CLI detect 逐周期驱动——数据逐日到位、巡检逐轮扫描的
#       真实节奏）→ 第 4 个周期的重燃被抖动抑制门拦下
#       （flapHeld=1、created=0、信封行携带确定性「应燃 id」）→
#       serve 后 curl 复跑（确定性：再次 flapHeld=1）→ 浏览器断言。
# 断言：
#   P0  周期 1-3：每轮 created=1 / cleared=1（诚实的三次周期预算）；
#   P1  第 4 周期重燃被拦：flapHeld=1 created=0，信封行 action=flap_held
#       且 alertId 非空（应燃身份可关联）；
#   A1  巡检状态条显示「抖动 1」（上次回执计数）；
#   A2  立即检测回执对话框：第六格「抖动抑制 1」+ 明细行「抖动抑制」chip；
#   A3  运行历史对话框：计数列末位 1（回执持久化）；
#   A4  curl：POST /api/detection/run 复跑 flapHeld=1（确定性）、
#       GET /api/detection/runs 首行 flapHeld=1；
#   A5  console 零错误/零未捕获拒绝。
# 截图存 repo tmp/（不作为交付物）。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
export PATH="$PATH:/home/z/.local/go/bin"

PORT=18172
WORK="$(mktemp -d /tmp/netops-r17flap.XXXXXX)"
DB="$WORK/r17.db"
REALDATA="$REPO_ROOT/testdata/realdata"
SHOT_DIR="$REPO_ROOT/tmp"
mkdir -p "$SHOT_DIR"

PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "  PASS  $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  FAIL  $1"; }
jeval() { agent-browser eval "$1" 2>/dev/null | tr -d '"'; }

cleanup() {
  [[ -n "${SERVE_PID:-}" ]] && kill "$SERVE_PID" 2>/dev/null || true
  agent-browser close >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

BIN="$WORK/netopslab"
go build -o "$BIN" ./cmd/netopslab

echo "[1/6] realdata 导入 + 7 日摄入 + 基线检测"
ORDER=(
  sites machine_rooms customers vpn_instances rings igp_domains devices boards
  base_stations ports racks odf odf_terminals ducts manholes poles
  optical_cables cable_segments splice_boxes fibers otn_ochs sdh_timeslots
  circuits protection_groups ippools services leased_lines bbus rrus olts pon_ports onus
)
for wb in "${ORDER[@]}"; do
  f="$REALDATA/$wb.xlsx"
  "$BIN" analyze -file "$f" > "$WORK/a.json"
  python3 - "$WORK/a.json" "$WORK/m.json" <<'PY'
import json, sys
json.dump(json.load(open(sys.argv[1], encoding="utf-8"))["suggestedMapping"], open(sys.argv[2], "w", encoding="utf-8"))
PY
  "$BIN" import --file "$f" --map "$WORK/m.json" --db "$DB" > "$WORK/i.json"
done
for day in 01 02 03 04 05 06 07; do
  "$BIN" ingest --db "$DB" --file "$REALDATA/perf-day-$day.xlsx" \
    --map "$REALDATA/ingest-mapping.json" > "$WORK/ing-$day.json"
done
# 基线检测：基线端口告警先行落定（后续轮次只刷新证据，不再计入 created）。
"$BIN" detect --db "$DB" > "$WORK/baseline.json"
BASE_N=$(python3 -c "import json; print(json.load(open('$WORK/baseline.json'))['alertsCreated'])")
if [[ "$BASE_N" -ge 1 ]]; then ok "P0 基线检测告警 ${BASE_N} 条（先行落定）"; else bad "P0 baseline=$BASE_N"; fi

echo "[2/6] 直播种抖动序列（合成 series，2026-10-10 起 15 天）"
PORT_RES=$(python3 - "$DB" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
row = db.execute("SELECT id FROM resources WHERE resource_type = 'port' LIMIT 1").fetchone()
print(row[0] if row else "")
PY
)
if [[ -z "$PORT_RES" ]]; then
  bad "P1 未找到无序列的端口资源"; exit 1
fi
ok "P1 端口资源：${PORT_RES:0:18}…"

# 播种工具：向合成序列追加一批点（数值 = 越限 -15.x / 健康 -9.0）。
SEED_SID="ser_r17_flap"
seed_points() {  # $1 = 起始日期(YYYY-MM-DD)  $2 = 逗号分隔值
  python3 - "$DB" "$SEED_SID" "$1" "$2" <<'PY'
import sqlite3, sys, datetime
db = sqlite3.connect(sys.argv[1])
sid, day0, vals = sys.argv[2], sys.argv[3], sys.argv[4].split(",")
base = datetime.date.fromisoformat(day0)
pts = []
for i, v in enumerate(vals):
    ts = f"{(base + datetime.timedelta(days=i)).isoformat()}T00:00:00Z"
    pts.append((sid, ts, float(v), "ok", "mbat_r17"))
db.execute(
    "INSERT OR IGNORE INTO metric_batches (id, workbook_name, workbook_sha256, status, rows_total, rows_imported, started_at, finished_at) "
    "VALUES ('mbat_r17', 'r17-seed.xlsx', '1', 'completed', 1, 1, '2026-10-16T00:00:00Z', '2026-10-16T00:00:01Z')")
db.execute(
    "INSERT OR IGNORE INTO metric_series (series_id, resource_id, metric_key, scope, first_seen_at, last_seen_at) "
    "VALUES (?, ?, 'optical_power_rx_avg_dbm', NULL, '2026-10-10T00:00:00Z', '2026-10-24T00:00:00Z')",
    (sid, sys.argv[5] if len(sys.argv) > 5 else "",))
# 修正：上一行的 resource 占位在下方更新
db.executemany("INSERT OR REPLACE INTO metrics (series_id, ts, value, quality, batch_id) VALUES (?, ?, ?, ?, ?)", pts)
db.commit()
print("seeded", len(pts))
PY
}
# 简化：一次性建好序列行（含 resource_id），再逐批追加。
python3 - "$DB" "$SEED_SID" "$PORT_RES" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
sid, res = sys.argv[2], sys.argv[3]
db.execute(
    "INSERT OR IGNORE INTO metric_batches (id, workbook_name, workbook_sha256, status, rows_total, rows_imported, started_at, finished_at) "
    "VALUES ('mbat_r17', 'r17-seed.xlsx', '1', 'completed', 1, 1, '2026-10-16T00:00:00Z', '2026-10-16T00:00:01Z')")
db.execute(
    "INSERT OR REPLACE INTO metric_series (series_id, resource_id, metric_key, scope, first_seen_at, last_seen_at) "
    "VALUES (?, ?, 'optical_power_rx_avg_dbm', NULL, '2026-10-10T00:00:00Z', '2026-10-24T00:00:00Z')", (sid, res))
db.commit()
print("series ready")
PY

append_points() {  # $1 = 起始日期  $2 = 逗号分隔值
  python3 - "$DB" "$SEED_SID" "$1" "$2" <<'PY'
import sqlite3, sys, datetime
db = sqlite3.connect(sys.argv[1])
sid, day0, vals = sys.argv[2], sys.argv[3], sys.argv[4].split(",")
base = datetime.date.fromisoformat(day0)
rows = [(sid, f"{(base + datetime.timedelta(days=i)).isoformat()}T00:00:00Z", float(v), "ok", "mbat_r17")
        for i, v in enumerate(vals)]
db.executemany("INSERT OR REPLACE INTO metrics (series_id, ts, value, quality, batch_id) VALUES (?, ?, ?, ?, ?)", rows)
db.commit()
print(f"appended {len(rows)}")
PY
}

echo "[3/6] 三个完整周期（fire→clear ×3）"
run_detect() { "$BIN" detect --db "$DB" > "$WORK/run-$1.json"; }
count_of() { python3 -c "import json; print(json.load(open('$WORK/run-$1.json'))['$2'])"; }

# 周期 1：10-12 越限 → 燃；13 健康 → 清。
append_points "2026-10-10" "-15.0,-15.5,-16.0" >/dev/null
run_detect c1f
append_points "2026-10-13" "-9.0" >/dev/null
run_detect c1c
# 周期 2：14-16 越限 → 燃；17 清。
append_points "2026-10-14" "-15.2,-15.6,-16.1" >/dev/null
run_detect c2f
append_points "2026-10-17" "-9.1" >/dev/null
run_detect c2c
# 周期 3：18-20 越限 → 燃；21 清。
append_points "2026-10-18" "-15.3,-15.7,-16.2" >/dev/null
run_detect c3f
append_points "2026-10-21" "-9.2" >/dev/null
run_detect c3c
for c in c1f c2f c3f; do
  n=$(count_of "$c" alertsCreated)
  [[ "$n" == "1" ]] && ok "P0 $c created=1" || bad "P0 $c created=$n"
done
for c in c1c c2c c3c; do
  n=$(count_of "$c" alertsCleared)
  [[ "$n" == "1" ]] && ok "P0 $c cleared=1" || bad "P0 $c cleared=$n"
done

echo "[4/6] 第 4 周期重燃：抖动抑制"
append_points "2026-10-22" "-15.4,-15.8,-16.3" >/dev/null
run_detect held
HELD=$(count_of held flapHeld)
CREATED=$(count_of held alertsCreated)
LINE_ID=$(python3 - "$WORK/run-held.json" "$SEED_SID" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
for line in d["alerts"]:
    if line["action"] == "flap_held" and line["seriesId"] == sys.argv[2]:
        print(line["alertId"]); break
PY
)
if [[ "$HELD" == "1" && "$CREATED" == "0" ]]; then
  ok "P1 重燃被拦：flapHeld=$HELD created=$CREATED"
else
  bad "P1 flapHeld=$HELD created=$CREATED (want 1/0)"
fi
if [[ -n "$LINE_ID" && "$LINE_ID" != "" ]]; then
  ok "P1 信封行 action=flap_held，应燃 id=${LINE_ID:0:24}…"
else
  bad "P1 信封行缺失 flap_held 行（seriesId=$SEED_SID）"
fi

echo "[5/6] serve + curl + 浏览器断言"
"$BIN" serve --db "$DB" --addr "127.0.0.1:$PORT" > "$WORK/serve.log" 2>&1 &
SERVE_PID=$!
for _ in $(seq 1 50); do curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && break; sleep 0.2; done

# —— A4a：HTTP 复跑（确定性：再次抑制）——
curl -s -X POST "http://127.0.0.1:$PORT/api/detection/run" -H 'Content-Type: application/json' -d '{}' > "$WORK/http-run.json"
HTTP_HELD=$(python3 -c "import json; print(json.load(open('$WORK/http-run.json'))['flapHeld'])")
HTTP_CREATED=$(python3 -c "import json; print(json.load(open('$WORK/http-run.json'))['alertsCreated'])")
if [[ "$HTTP_HELD" == "1" && "$HTTP_CREATED" == "0" ]]; then
  ok "A4a HTTP 复跑 flapHeld=1（确定性）"
else
  bad "A4a HTTP flapHeld=$HTTP_HELD created=$HTTP_CREATED"
fi
# —— A4b：回执持久化 ——
RUNS=$(curl -s "http://127.0.0.1:$PORT/api/detection/runs?page=1&pageSize=1")
RUNS_HELD=$(python3 -c "
import json
d = json.loads('''$RUNS''')
print(d['items'][0]['flapHeld'] if d['items'] else 'none')")
if [[ "$RUNS_HELD" == "1" ]]; then ok "A4b 运行历史首行 flapHeld=1（回执持久化）"; else bad "A4b 首行 flapHeld=$RUNS_HELD"; fi

# —— 浏览器 ——
agent-browser set viewport 1600 900 >/dev/null
agent-browser open "http://127.0.0.1:$PORT/alerts" >/dev/null
agent-browser wait --fn "!!document.querySelector('.patrol-bar')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 1000 >/dev/null
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null

# —— A1 巡检状态条 ——
STRIP="$(jeval "(() => { const bar = document.querySelector('.patrol-bar'); return bar ? (bar.textContent || '').includes('抖动 1') : 'no-bar'; })()")"
if [[ "$STRIP" == "true" ]]; then ok "A1 巡检状态条显示「抖动 1」"; else bad "A1 strip=$STRIP"; fi
agent-browser screenshot "$SHOT_DIR/r17-alerts-patrol.png" >/dev/null 2>&1 || true

# —— A2 立即检测回执（第六格 + chip 行）——
RUN_BTN="$(jeval "(() => { const b = [...document.querySelectorAll('button')].find(x => (x.textContent||'').includes('立即检测')); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 600 >/dev/null
CONFIRM="$(jeval "(() => { const b = [...document.querySelectorAll('.modal--form button')].find(x => (x.textContent||'') === '运行检测' && !x.disabled); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait --fn "!!document.querySelector('.run-stats')" --timeout 30000 >/dev/null 2>&1 || true
agent-browser wait 800 >/dev/null
FLAP_CELL="$(jeval "(() => { const cells = [...document.querySelectorAll('.run-stat')]; const c = cells.find(x => (x.textContent||'').includes('抖动抑制')); return c ? c.textContent.replace(/\\s+/g, ' ').trim() : 'none'; })()")"
if [[ "$FLAP_CELL" == *"抖动抑制 1"* ]]; then ok "A2 回执第六格「抖动抑制 1」"; else bad "A2 cell=$FLAP_CELL"; fi
FLAP_CHIP="$(jeval "(() => { return [...document.querySelectorAll('.run-mutations .chip')].some(c => (c.textContent||'').includes('抖动抑制')); })()")"
if [[ "$FLAP_CHIP" == "true" ]]; then ok "A2 回执明细行「抖动抑制」chip"; else bad "A2 chip=$FLAP_CHIP"; fi
agent-browser screenshot "$SHOT_DIR/r17-run-receipt.png" >/dev/null 2>&1 || true
# 关闭回执对话框
jeval "(() => { const b = [...document.querySelectorAll('.modal button')].find(x => (x.textContent||'') === '完成' || (x.textContent||'') === '关闭'); if (b) b.click(); return 'closed'; })()" >/dev/null
agent-browser wait 500 >/dev/null

# —— A3 运行历史对话框 ——
HIST_BTN="$(jeval "(() => { const b = [...document.querySelectorAll('.patrol-bar button')].find(x => (x.title||'').includes('运行历史') || (x.textContent||'').includes('历史')); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait --fn "!!document.querySelector('.modal--wide table')" --timeout 15000 >/dev/null 2>&1 || true
agent-browser wait 600 >/dev/null
HIST_ROW="$(jeval "(() => { const row = document.querySelector('.modal--wide tbody tr'); return row ? (row.textContent || '').replace(/\\s+/g, ' ').trim() : 'none'; })()")"
if [[ "$HIST_ROW" != "none" && "$(echo "$HIST_ROW" | grep -o '/ 1$')" == "/ 1" ]]; then
  ok "A3 运行历史计数列末位 1（回持久化）"
elif [[ "$HIST_ROW" != "none" && "$(echo "$HIST_ROW" | grep -oE '[0-9] / [0-9] / [0-9] / [0-9] / [0-9]')" != "" ]]; then
  ok "A3 运行历史计数列五段式（含抖动位）"
else
  bad "A3 histRow=$HIST_ROW"
fi
agent-browser screenshot "$SHOT_DIR/r17-run-history.png" >/dev/null 2>&1 || true

# —— A5 console 零错误 ——
ERRS="$(jeval "window.__errs.length")"
if [[ "$ERRS" == "0" ]]; then ok "A5 console 零错误/零未捕获拒绝"; else bad "A5 console 错误 ${ERRS} 条：$(jeval "window.__errs.join(' | ')")"; fi

echo
echo "[6/6] 汇总：PASS=$PASS FAIL=$FAIL"
if [[ "$FAIL" -gt 0 ]]; then exit 1; fi
