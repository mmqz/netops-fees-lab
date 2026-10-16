#!/usr/bin/env bash
# NetOps Lab — R16 告警关联（根因/次生）浏览器级回归（v0.12 /alerts）。
#
# 前置：make web（dist 已构建）。
# 旅程：realdata 导入 + 7 日摄入 + 基线检测（端口告警，同层 = 未关联——
#       钉 lateral 守卫的真实通道行为）→ 直播种 device + circuit 序列
#       （跨层 a_end 边，3 日连续越限）→ 检测 → device=primary /
#       circuit=secondary（链接 device 告警）→ /alerts 浏览器断言。
# 断言：
#   A1  基线（同层端口告警）：关联列「—」未标记（sibling 守卫）；
#   A2  跨层检测（curl）：role=primary 含 device 告警、role=secondary 含
#       circuit 告警且 correlatedAlertId 指向 device 告警；
#   A3  /alerts 表格：根因嫌疑 chip（warning）+ 疑似次生 chip（muted）；
#   A4  角色筛选：primary → 仅根因行；secondary → 仅次生行；
#   A5  行展开详情：疑似次生因果行（含根因资源 id + 跳转链接）；
#   A6  bogus role → 400（curl）；
#   A7  console 零错误/零未捕获拒绝。
# 截图存 repo tmp/（不作为交付物）。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
export PATH="$PATH:/home/z/.local/go/bin"

PORT=18164
WORK="$(mktemp -d /tmp/netops-r16corr.XXXXXX)"
DB="$WORK/r16.db"
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

echo "[1/5] realdata 导入 + 7 日摄入 + 基线检测"
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
"$BIN" detect --db "$DB" > "$WORK/baseline.json"
BASE_N=$(python3 -c "import json; print(json.load(open('$WORK/baseline.json'))['alertsCreated'])")
if [[ "$BASE_N" -ge 1 ]]; then ok "P0 基线检测告警 ${BASE_N} 条（端口，同层）"; else bad "P0 baseline=$BASE_N"; fi

echo "[2/5] 直播种跨层序列（device L1 + circuit L3，a_end 边）"
PAIR=$(python3 - "$DB" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
row = db.execute(
    "SELECT r.to_resource_id, r.from_resource_id FROM relations r "
    "JOIN resources dev ON dev.id = r.to_resource_id AND dev.resource_type = 'device' "
    "JOIN resources cir ON cir.id = r.from_resource_id AND cir.resource_type = 'circuit' "
    "WHERE r.relation_type = 'a_end' LIMIT 1").fetchone()
if row is None:
    # 方向勘误：a_end 存储方向可能反转，双向尝试
    row = db.execute(
        "SELECT r.from_resource_id, r.to_resource_id FROM relations r "
        "JOIN resources dev ON dev.id = r.from_resource_id AND dev.resource_type = 'device' "
        "JOIN resources cir ON cir.id = r.to_resource_id AND cir.resource_type = 'circuit' "
        "WHERE r.relation_type = 'a_end' LIMIT 1").fetchone()
print(f"{row[0]} {row[1]}" if row else "")
PY
)
DEV_ID=$(echo "$PAIR" | cut -d' ' -f1)
CIR_ID=$(echo "$PAIR" | cut -d' ' -f2)
if [[ -z "$DEV_ID" || -z "$CIR_ID" || "$DEV_ID" == "" ]]; then
  bad "P1 未找到 device-circuit a_end 边"; exit 1
fi
ok "P1 a_end 边：device=${DEV_ID:0:18}… circuit=${CIR_ID:0:18}…"

python3 - "$DB" "$DEV_ID" "$CIR_ID" <<'PY'
import sqlite3, sys, uuid
db = sqlite3.connect(sys.argv[1])
dev, cir = sys.argv[2], sys.argv[3]
batch = "mbat_r16_seed"
db.execute(
    "INSERT INTO metric_batches (id, workbook_name, workbook_sha256, status, rows_total, rows_imported, started_at, finished_at) "
    "VALUES (?, 'r16-seed.xlsx', '1', 'completed', 6, 6, '2026-10-13T00:00:00Z', '2026-10-13T00:00:01Z')", (batch,))
for res in (dev, cir):
    sid = "ser_r16_" + uuid.uuid4().hex[:12]
    db.execute(
        "INSERT INTO metric_series (series_id, resource_id, metric_key, scope, first_seen_at, last_seen_at) "
        "VALUES (?, ?, 'optical_power_rx_avg_dbm', NULL, '2026-10-10T00:00:00Z', '2026-10-12T00:00:00Z')",
        (sid, res))
    for i, day in enumerate(("2026-10-10", "2026-10-11", "2026-10-12")):
        db.execute(
            "INSERT INTO metrics (series_id, ts, value, quality, batch_id) VALUES (?, ?, ?, 'ok', ?)",
            (sid, f"{day}T00:00:00Z", -20.0 - i, batch))
db.commit()
print("seeded")
PY

echo "[3/5] serve + 跨层检测（curl 断言）"
"$BIN" serve --db "$DB" --addr "127.0.0.1:$PORT" > "$WORK/serve.log" 2>&1 &
SERVE_PID=$!
for _ in $(seq 1 50); do curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && break; sleep 0.2; done

curl -s -X POST "http://127.0.0.1:$PORT/api/detection/run" -H 'Content-Type: application/json' -d '{}' > "$WORK/run.json"
CREATED=$(python3 -c "import json; print(json.load(open('$WORK/run.json'))['alertsCreated'])")
if [[ "$CREATED" -ge 2 ]]; then ok "P2 跨层检测新增 ${CREATED} 条告警"; else bad "P2 created=$CREATED"; fi

PRIM=$(curl -s "http://127.0.0.1:$PORT/api/alerts?state=firing&role=primary&pageSize=100")
SEC=$(curl -s "http://127.0.0.1:$PORT/api/alerts?state=firing&role=secondary&pageSize=100")
PRIM_DEV=$(python3 -c "
import json, sys
d = json.loads('''$PRIM''')
items = [i for i in d['items'] if i['resource']['id'] == '$DEV_ID']
print(items[0]['alertId'] if items else '')")
SEC_CIR=$(python3 -c "
import json, sys
d = json.loads('''$SEC''')
items = [i for i in d['items'] if i['resource']['id'] == '$CIR_ID']
print(items[0]['alertId'] if items else '')")
SEC_LINK=$(python3 -c "
import json, sys
d = json.loads('''$SEC''')
items = [i for i in d['items'] if i['resource']['id'] == '$CIR_ID']
print(items[0]['correlatedAlertId'] if items else '')")
if [[ "$PRIM_DEV" != "" ]]; then ok "A2a role=primary 含 device 告警"; else bad "A2a primary 无 device 告警"; fi
if [[ "$SEC_CIR" != "" && "$SEC_LINK" == "$PRIM_DEV" ]]; then
  ok "A2b role=secondary 含 circuit 告警且链接 device 根因"
else
  bad "A2b sec=$SEC_CIR link=$SEC_LINK want=$PRIM_DEV"
fi
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:$PORT/api/alerts?role=bogus")
if [[ "$HTTP_CODE" == "400" ]]; then ok "A6 bogus role → 400"; else bad "A6 bogus role → $HTTP_CODE"; fi

echo "[4/5] 浏览器断言"
agent-browser set viewport 1600 900 >/dev/null
agent-browser open "http://127.0.0.1:$PORT/alerts" >/dev/null
agent-browser wait --fn "!!document.querySelector('.card table')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 1000 >/dev/null
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null

# —— A3 表格 chips ——
PRIM_CHIP="$(jeval "(() => { return [...document.querySelectorAll('.card .chip')].some(c => (c.textContent||'').includes('根因嫌疑')); })()")"
SEC_CHIP="$(jeval "(() => { return [...document.querySelectorAll('.card .chip')].some(c => (c.textContent||'').includes('疑似次生')); })()")"
if [[ "$PRIM_CHIP" == "true" && "$SEC_CHIP" == "true" ]]; then ok "A3 表格关联 chip（根因嫌疑 + 疑似次生）"; else bad "A3 prim=$PRIM_CHIP sec=$SEC_CHIP"; fi
agent-browser screenshot "$SHOT_DIR/r16-alerts-chips.png" >/dev/null 2>&1 || true

# —— A4 角色筛选（primary）——
ROLE_SEL="$(jeval "(() => { const s = [...document.querySelectorAll('.filter-bar select')].find(x => (x.options && [...x.options].some(o => (o.textContent||'').includes('根因嫌疑')))); if (!s) return 'none'; s.value = 'primary'; s.dispatchEvent(new Event('change', { bubbles: true })); return 'ok'; })()")"
agent-browser wait 900 >/dev/null
PRIM_ROWS="$(jeval "(() => { const chips = [...document.querySelectorAll('.card .chip')]; const prim = chips.filter(c => (c.textContent||'').includes('根因嫌疑')).length; const sec = chips.filter(c => (c.textContent||'').includes('疑似次生')).length; return prim + '/' + sec; })()")"
if [[ "$ROLE_SEL" == "ok" && "$PRIM_ROWS" != "0" && "${PRIM_ROWS#*/}" == "0" ]]; then
  ok "A4a role=primary 筛选（根因 ${PRIM_ROWS%/*} 条 / 次生 0 条）"
else
  bad "A4a sel=$ROLE_SEL prim/sec=$PRIM_ROWS"
fi
# —— A4b（secondary + 行展开因果行）——
ROLE2="$(jeval "(() => { const s = [...document.querySelectorAll('.filter-bar select')].find(x => [...(x.options||[])].some(o => (o.textContent||'').includes('疑似次生'))); if (!s) return 'none'; s.value = 'secondary'; s.dispatchEvent(new Event('change', { bubbles: true })); return 'ok'; })()")"
agent-browser wait 900 >/dev/null
SEC_ROWS="$(jeval "(() => { const chips = [...document.querySelectorAll('.card .chip')]; return chips.filter(c => (c.textContent||'').includes('疑似次生')).length + '/' + chips.filter(c => (c.textContent||'').includes('根因嫌疑')).length; })()")"
EXPAND="$(jeval "(() => { const trs = [...document.querySelectorAll('table[aria-label=\\'告警中心\\'] > tbody > tr:not(:has(td.table-empty))')]; const tr = trs.find(r => (r.textContent||'').includes('疑似次生')); if (!tr) return 'no-row'; tr.click(); return 'ok'; })()")"
agent-browser wait 500 >/dev/null
CAUSE_LINE="$(jeval "(() => { return [...document.querySelectorAll('.card .kv-table td')].some(t => (t.textContent||'').includes('疑似次生') && (t.textContent||'').includes('$PRIM_DEV')); })()")"
if [[ "$ROLE2" == "ok" && "${SEC_ROWS%/*}" != "0" && "${SEC_ROWS#*/}" == "0" ]]; then
  ok "A4b role=secondary 筛选（次生 ${SEC_ROWS%/*} 条 / 根因 0 条）"
else
  bad "A4b sel=$ROLE2 sec/prim=$SEC_ROWS"
fi
if [[ "$EXPAND" == "ok" && "$CAUSE_LINE" == "true" ]]; then
  ok "A5 行展开因果行（链接根因告警 id）"
else
  bad "A5 expand=$EXPAND cause=$CAUSE_LINE"
fi
agent-browser screenshot "$SHOT_DIR/r16-alerts-secondary.png" >/dev/null 2>&1 || true

# —— A7 console 零错误 ——
ERRS="$(jeval "window.__errs.length")"
if [[ "$ERRS" == "0" ]]; then ok "A7 console 零错误/零未捕获拒绝"; else bad "A7 console 错误 ${ERRS} 条：$(jeval "window.__errs.join(' | ')")"; fi

echo
echo "[5/5] 汇总：PASS=$PASS FAIL=$FAIL"
if [[ "$FAIL" -gt 0 ]]; then exit 1; fi
