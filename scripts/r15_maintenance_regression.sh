#!/usr/bin/env bash
# NetOps Lab — R15 维护窗口浏览器级回归（v0.12 /maintenance + 抑制链路）。
#
# 前置：make web（dist 已构建）。
# 旅程：realdata 导入 + 7 日性能摄入（不先跑检测——保留全新告警面）→ serve →
#       curl 建两个窗口（全局抑制窗 Oct12-13 + 2027 范围窗）→ /maintenance 浏览器断言 →
#       检测运行 e2e 抑制（created=0 / suppressed>0 / 告警零行）→ UI 删除抑制窗 →
#       检测运行恢复触发 → /alerts 巡检状态条抑制计数 → /rules 回执对话框第五格 →
#       UI 新建窗口旅程。
# 断言：
#   A1  侧栏「维护窗口」导航项 + /maintenance 路由 + 页标题；
#   A2  窗口表格：curl 两窗（全局/范围）+ 状态 chip（已结束/计划中）+ 范围 chip；
#   A3  行展开详情（ID / UTC 区间 / 范围 id kv 表）；
#   A4  抑制 e2e（curl）：POST /detection/run → created=0 suppressed>0；告警 0 行；
#       回执 suppression 计数落库（GET /detection/runs）；
#   A5  UI 删除旅程：行内删除 → ConfirmDialog → toast → 行数回落；
#   A6  恢复触发（curl）：created>0 suppressed=0；firing 告警 ≥1；
#   A7  /alerts 巡检状态条含「抑制」计数；
#   A8  /rules 立即检测回执对话框第五格「维护抑制」；
#   A9  UI 新建旅程：对话框（事由 + datetime-local）→ 提交 → toast + 行数 +1 + 计划中 chip；
#   A10 console 零错误/零未捕获拒绝。
# 截图存 repo tmp/（不作为交付物）。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
export PATH="$PATH:/home/z/.local/go/bin"

PORT=18163
WORK="$(mktemp -d /tmp/netops-r15mnt.XXXXXX)"
DB="$WORK/r15.db"
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

echo "[1/4] realdata 导入（31 工作簿，依赖序）+ 7 日性能摄入"
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
RES_IDS=$(curl -s --get --data-urlencode "pageSize=2" "http://127.0.0.1:1/api/resources" 2>/dev/null || true) # 占位（serve 后取）
echo "  realdata 就绪（未先跑检测——全新告警面）"

echo "[2/4] serve + curl 建窗（全局抑制窗 Oct12-13 + 2027 范围窗）"
"$BIN" serve --db "$DB" --addr "127.0.0.1:$PORT" > "$WORK/serve.log" 2>&1 &
SERVE_PID=$!
for _ in $(seq 1 50); do curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && break; sleep 0.2; done

# 两个真实资源 id（范围窗 scope）
R1=$(curl -s "http://127.0.0.1:$PORT/api/resources?pageSize=2" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['items'][0]['id'])")
R2=$(curl -s "http://127.0.0.1:$PORT/api/resources?pageSize=2" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['items'][1]['id'])")
curl -s -X POST "http://127.0.0.1:$PORT/api/maintenance/windows" \
  -H 'Content-Type: application/json' \
  -d '{"label":"割接演练（抑制）","startsAt":"2026-10-12T00:00:00Z","endsAt":"2026-10-13T00:00:00Z","note":"R15 回归：覆盖第 7 日劣化证据"}' > "$WORK/w1.json"
W1=$(python3 -c "import json; print(json.load(open('$WORK/w1.json'))['windowId'])")
curl -s -X POST "http://127.0.0.1:$PORT/api/maintenance/windows" \
  -H 'Content-Type: application/json' \
  -d "{\"label\":\"2027 扩容窗口\",\"startsAt\":\"2027-03-01T00:00:00Z\",\"endsAt\":\"2027-03-02T00:00:00Z\",\"resourceIds\":[\"$R1\",\"$R2\"]}" > "$WORK/w2.json"
W2=$(python3 -c "import json; print(json.load(open('$WORK/w2.json'))['windowId'])")
LIST_N=$(curl -s "http://127.0.0.1:$PORT/api/maintenance/windows" | python3 -c "import json,sys; print(json.load(sys.stdin)['total'])")
if [[ "$LIST_N" == "2" ]]; then ok "P0 curl 两窗就位（total=2）"; else bad "P0 windows total=$LIST_N"; fi

echo "[3/4] 浏览器断言"
agent-browser set viewport 1600 900 >/dev/null
agent-browser open "http://127.0.0.1:$PORT/" >/dev/null
agent-browser wait --load networkidle >/dev/null 2>&1 || true
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null

count_rows() {
  jeval "document.querySelectorAll('table[aria-label=\'维护窗口\'] > tbody > tr:not(:has(.evidence))').length"
}

# —— A1 导航与路由 ——
NAV="$(jeval "(() => { const links = [...document.querySelectorAll('.sidebar-link')]; return links.some(l => (l.textContent||'').includes('维护窗口')); })()")"
if [[ "$NAV" == "true" ]]; then ok "A1a 侧栏「维护窗口」导航项"; else bad "A1a 侧栏无维护窗口入口"; fi
agent-browser open "http://127.0.0.1:$PORT/maintenance" >/dev/null
agent-browser wait --fn "!!document.querySelector('.card .filter-bar')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 800 >/dev/null
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null
TITLE="$(jeval "document.querySelector('.topbar-title') ? document.querySelector('.topbar-title').textContent : ''")"
if [[ "$TITLE" == *维护窗口* ]]; then ok "A1b /maintenance 路由 + 页标题"; else bad "A1b title = $TITLE"; fi

# —— A2 窗口表格（2027 范围窗在前 = 最新开始优先；Oct12 全局窗在后 = 已结束）——
ROWS="$(count_rows)"
if [[ "$ROWS" == "2" ]]; then ok "A2a 窗口行（${ROWS}，最新开始优先）"; else bad "A2a rows = $ROWS"; fi
SCOPED="$(jeval "(() => { return [...document.querySelectorAll('.card .chip')].some(c => (c.textContent||'').includes('2 个资源')); })()")"
if [[ "$SCOPED" == "true" ]]; then ok "A2b 范围 chip「2 个资源」"; else bad "A2b 无范围 chip"; fi
GLOBAL="$(jeval "(() => { return [...document.querySelectorAll('.card .chip')].some(c => (c.textContent||'').includes('全部资源')); })()")"
if [[ "$GLOBAL" == "true" ]]; then ok "A2c 全局范围 chip"; else bad "A2c 无全局 chip"; fi
PLANNED="$(jeval "(() => { return [...document.querySelectorAll('.card .chip')].some(c => (c.textContent||'').includes('计划中')); })()")"
ENDED="$(jeval "(() => { return [...document.querySelectorAll('.card .chip')].some(c => (c.textContent||'').includes('已结束')); })()")"
if [[ "$PLANNED" == "true" && "$ENDED" == "true" ]]; then ok "A2d 状态 chip（计划中 + 已结束）"; else bad "A2d planned=$PLANNED ended=$ENDED"; fi
agent-browser screenshot "$SHOT_DIR/r15-maintenance-list.png" >/dev/null 2>&1 || true

# —— A3 行展开详情 ——
EXPAND="$(jeval "(() => { const trs = [...document.querySelectorAll('table[aria-label=\'维护窗口\'] > tbody > tr:not(:has(td.table-empty))')]; const tr = trs.find(r => (r.textContent||'').includes('割接演练')); if (!tr) return 'none'; tr.click(); return 'ok'; })()")"
agent-browser wait 400 >/dev/null
KV="$(jeval "(() => { return [...document.querySelectorAll('.card .kv-table th')].some(t => (t.textContent||'') === 'UTC'); })()")"
if [[ "$EXPAND" == "ok" && "$KV" == "true" ]]; then ok "A3 行展开详情（UTC 区间 kv）"; else bad "A3 expand=$EXPAND kv=$KV"; fi
agent-browser screenshot "$SHOT_DIR/r15-maintenance-expanded.png" >/dev/null 2>&1 || true

# —— A4 抑制 e2e（curl，全新告警面：首个检测全部被抑制）——
curl -s -X POST "http://127.0.0.1:$PORT/api/detection/run" -H 'Content-Type: application/json' -d '{}' > "$WORK/run1.json"
CREATED1=$(python3 -c "import json; print(json.load(open('$WORK/run1.json'))['alertsCreated'])")
SUPP1=$(python3 -c "import json; print(json.load(open('$WORK/run1.json'))['suppressed'])")
FIND1=$(python3 -c "import json; print(json.load(open('$WORK/run1.json'))['findings'])")
if [[ "$CREATED1" == "0" && "$SUPP1" -ge 1 && "$FIND1" -ge 1 ]]; then
  ok "A4a 检测运行被抑制（findings=$FIND1 created=0 suppressed=$SUPP1）"
else
  bad "A4a findings=$FIND1 created=$CREATED1 suppressed=$SUPP1"
fi
FIRING_N=$(curl -s "http://127.0.0.1:$PORT/api/alerts?state=firing" | python3 -c "import json,sys; print(json.load(sys.stdin)['total'])")
if [[ "$FIRING_N" == "0" ]]; then ok "A4b 抑制期间告警零行"; else bad "A4b firing=$FIRING_N"; fi
RECEIPT_SUP=$(curl -s "http://127.0.0.1:$PORT/api/detection/runs?pageSize=1" | python3 -c "import json,sys; print(json.load(sys.stdin)['items'][0]['suppressed'])")
if [[ "$RECEIPT_SUP" == "$SUPP1" ]]; then ok "A4c 回执落库 suppressed=$RECEIPT_SUP"; else bad "A4c receipt suppressed=$RECEIPT_SUP ≠ $SUPP1"; fi

# —— A5 UI 删除旅程（删抑制窗 W1）——
DEL_BTN="$(jeval "(() => { const trs = [...document.querySelectorAll('table[aria-label=\'维护窗口\'] > tbody > tr')]; const tr = trs.find(r => (r.textContent||'').includes('割接演练')); if (!tr) return 'no-row'; const b = [...tr.querySelectorAll('.btn')].find(x => (x.textContent||'').trim() === '删除'); if (!b) return 'no-btn'; b.click(); return 'ok'; })()")"
agent-browser wait 500 >/dev/null
CONFIRM="$(jeval "(() => { const d = document.querySelector('.modal[role=alertdialog]'); return d && (d.textContent||'').includes('将删除窗口') ? 'ok' : 'none'; })()")"
CONFIRMED="$(jeval "(() => { const d = document.querySelector('.modal[role=alertdialog]'); if (!d) return 'no-dlg'; const b = [...d.querySelectorAll('.btn--danger')].find(x => (x.textContent||'').trim() === '删除'); if (!b) return 'no-btn'; b.click(); return 'ok'; })()")"
agent-browser wait 1200 >/dev/null
ROWS_AFTER_DEL="$(count_rows)"
TOAST_DEL="$(jeval "(() => { return [...document.querySelectorAll('.toast, [class*=toast]')].some(t => (t.textContent||'').includes('窗口已删除')); })()")"
if [[ "$DEL_BTN" == "ok" && "$CONFIRM" == "ok" && "$CONFIRMED" == "ok" && "$ROWS_AFTER_DEL" == "1" && "$TOAST_DEL" == "true" ]]; then
  ok "A5 UI 删除旅程（确认框 + toast + 行数 2 → 1）"
else
  bad "A5 del=$DEL_BTN confirm=$CONFIRM confirmed=$CONFIRMED rows=$ROWS_AFTER_DEL toast=$TOAST_DEL"
fi
agent-browser wait 2500 >/dev/null

# —— A6 恢复触发（curl：窗口删除后同样数据全部触发）——
curl -s -X POST "http://127.0.0.1:$PORT/api/detection/run" -H 'Content-Type: application/json' -d '{}' > "$WORK/run2.json"
CREATED2=$(python3 -c "import json; print(json.load(open('$WORK/run2.json'))['alertsCreated'])")
SUPP2=$(python3 -c "import json; print(json.load(open('$WORK/run2.json'))['suppressed'])")
if [[ "$CREATED2" -ge 1 && "$SUPP2" == "0" ]]; then
  ok "A6 恢复触发（created=$CREATED2 suppressed=0）"
else
  bad "A6 created=$CREATED2 suppressed=$SUPP2"
fi
FIRING2=$(curl -s "http://127.0.0.1:$PORT/api/alerts?state=firing" | python3 -c "import json,sys; print(json.load(sys.stdin)['total'])")
if [[ "$FIRING2" -ge 1 ]]; then ok "A6b firing 告警 ${FIRING2} 条"; else bad "A6b firing=$FIRING2"; fi

# —— A7 /alerts 巡检状态条抑制计数 ——
agent-browser open "http://127.0.0.1:$PORT/alerts" >/dev/null
agent-browser wait --fn "!!document.querySelector('.patrol-bar')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 800 >/dev/null
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null
PATROL="$(jeval "(() => { const p = document.querySelector('.patrol-bar'); return p && (p.textContent||'').includes('抑制') ? 'true' : 'false'; })()")"
if [[ "$PATROL" == "true" ]]; then ok "A7 巡检状态条含抑制计数"; else bad "A7 patrol 无抑制字样"; fi
agent-browser screenshot "$SHOT_DIR/r15-alerts-patrol.png" >/dev/null 2>&1 || true

# —— A8 /rules 立即检测回执对话框第五格 ——
agent-browser open "http://127.0.0.1:$PORT/rules" >/dev/null
agent-browser wait --fn "!!document.querySelector('.card .filter-bar')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 800 >/dev/null
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null
RUN_CLICK="$(jeval "(() => { const b = [...document.querySelectorAll('.card .btn--secondary')].find(x => (x.textContent||'').trim() === '立即检测'); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 400 >/dev/null
RUN_GO="$(jeval "(() => { const d = document.querySelector('.modal[role=dialog]'); if (!d) return 'no-dlg'; const b = [...d.querySelectorAll('.btn--primary')].pop(); if (!b || b.disabled) return 'no-btn'; b.click(); return 'ok'; })()")"
agent-browser wait --fn "!!document.querySelector('.run-stats')" --timeout 30000 >/dev/null 2>&1 || true
agent-browser wait 400 >/dev/null
FIFTH="$(jeval "(() => { return [...document.querySelectorAll('.run-stat-label')].some(t => (t.textContent||'').includes('维护抑制')); })()")"
if [[ "$RUN_CLICK" == "ok" && "$RUN_GO" == "ok" && "$FIFTH" == "true" ]]; then
  ok "A8 回执对话框第五格「维护抑制」"
else
  bad "A8 click=$RUN_CLICK go=$RUN_GO fifth=$FIFTH"
fi
agent-browser screenshot "$SHOT_DIR/r15-receipt-dialog.png" >/dev/null 2>&1 || true
jeval "(() => { const b = [...document.querySelectorAll('.modal .btn--primary')].find(x => (x.textContent||'').trim() === '完成'); if (b) b.click(); return 'closed'; })()" >/dev/null
agent-browser wait 500 >/dev/null

# —— A9 UI 新建旅程（未来时间 → 计划中）——
agent-browser open "http://127.0.0.1:$PORT/maintenance" >/dev/null
agent-browser wait --fn "!!document.querySelector('.card .filter-bar')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 800 >/dev/null
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null
NEW_BTN="$(jeval "(() => { const b = [...document.querySelectorAll('.card .btn--primary')].find(x => (x.textContent||'').trim() === '新建窗口'); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 400 >/dev/null
FILL="$(jeval "(() => { const l = document.getElementById('mnt-label'); if (!l) return 'none'; const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set; setter.call(l, 'R15 回归新建'); l.dispatchEvent(new Event('input', { bubbles: true })); const s = document.getElementById('mnt-start'); setter.call(s, '2027-06-01T09:00'); s.dispatchEvent(new Event('input', { bubbles: true })); const e = document.getElementById('mnt-end'); setter.call(e, '2027-06-01T13:00'); e.dispatchEvent(new Event('input', { bubbles: true })); return 'ok'; })()")"
agent-browser wait 300 >/dev/null
SUBMIT="$(jeval "(() => { const b = [...document.querySelectorAll('.modal .btn--primary')].pop(); if (!b || b.disabled) return 'still-disabled'; b.click(); return 'ok'; })()")"
agent-browser wait 1200 >/dev/null
ROWS_AFTER_CREATE="$(count_rows)"
TOAST_NEW="$(jeval "(() => { return [...document.querySelectorAll('.toast, [class*=toast]')].some(t => (t.textContent||'').includes('窗口已创建')); })()")"
if [[ "$NEW_BTN" == "ok" && "$FILL" == "ok" && "$SUBMIT" == "ok" && "$ROWS_AFTER_CREATE" == "2" && "$TOAST_NEW" == "true" ]]; then
  ok "A9 UI 新建旅程（行数 1 → 2 + toast）"
else
  bad "A9 new=$NEW_BTN fill=$FILL submit=$SUBMIT rows=$ROWS_AFTER_CREATE toast=$TOAST_NEW"
fi
agent-browser wait 1500 >/dev/null
PLANNED2="$(jeval "(() => { const trs = [...document.querySelectorAll('table[aria-label=\'维护窗口\'] > tbody > tr')]; return trs.some(r => (r.textContent||'').includes('R15 回归新建') && (r.textContent||'').includes('计划中')); })()")"
if [[ "$PLANNED2" == "true" ]]; then ok "A9b 新建行状态「计划中」"; else bad "A9b 计划中 chip = $PLANNED2"; fi
agent-browser screenshot "$SHOT_DIR/r15-maintenance-created.png" >/dev/null 2>&1 || true

# —— A10 console 零错误 ——
ERRS="$(jeval "window.__errs.length")"
if [[ "$ERRS" == "0" ]]; then ok "A10 console 零错误/零未捕获拒绝"; else bad "A10 console 错误 ${ERRS} 条：$(jeval "window.__errs.join(' | ')")"; fi

echo
echo "[4/4] 汇总：PASS=$PASS FAIL=$FAIL"
if [[ "$FAIL" -gt 0 ]]; then exit 1; fi
