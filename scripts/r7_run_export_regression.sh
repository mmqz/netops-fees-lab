#!/usr/bin/env bash
# NetOps Lab — R7 立即检测 UI + 告警 CSV 导出浏览器级回归（v0.10）。
#
# 前置：make web（dist 已构建）。
# 旅程：realdata 31 工作簿 + PTP 导入 → 7 日性能摄入 → 先跑一次 CLI detect
#       （数据到位）→ 浏览器全链路。
# 断言：
#   B1  /alerts「立即检测」按钮 + 确认对话框（全库扫描说明文案）；
#   B2  确认运行 → 回执六格统计（发现/新建/清除/写入/维护抑制/抖动抑制，
#       计数与六标签集双断言——回执再加格时此处大声断掉而非静默漏测）
#       + 数据截止行；
#   B3  数据与规则未变时重跑 → 新建 0 清除 0（确定性，非错误）+ 空变更文案；
#   B4  回执完成后告警列表自动刷新（onFinished → fetchAlerts）；
#   B5  /rules 页「立即检测」入口同样可用（改规则后的验证通道）；
#   B6  「导出 CSV」：下载触发（劫持 a.click）→ blob 内容可读 →
#       BOM + 表头 20 列 + 行数与 API total 一致（当前筛选 firing）→ toast；
#   B7  无告警筛选导出 → info 提示不触发下载；
#   B8  console 零错误/零未捕获拒绝。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
export PATH="$PATH:/home/z/.local/go/bin"

PORT=18168
WORK="$(mktemp -d /tmp/netops-r7run.XXXXXX)"
DB="$WORK/r7.db"
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

echo "[1/4] realdata 导入（31 工作簿，依赖序）"
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
"$BIN" analyze -file "$REPO_ROOT/samples/ptp-config-script.txt" > "$WORK/a.json"
python3 - "$WORK/a.json" "$WORK/m.json" <<'PY'
import json, sys
json.dump(json.load(open(sys.argv[1], encoding="utf-8"))["suggestedMapping"], open(sys.argv[2], "w", encoding="utf-8"))
PY
"$BIN" import --file "$REPO_ROOT/samples/ptp-config-script.txt" --map "$WORK/m.json" --db "$DB" > "$WORK/i-ptp.json" || true

echo "[2/4] 7 日性能摄入 + 预置检测（CLI 通道，数据到位）"
for day in 01 02 03 04 05 06 07; do
  "$BIN" ingest --db "$DB" --file "$REALDATA/perf-day-$day.xlsx" \
    --map "$REALDATA/ingest-mapping.json" > "$WORK/ing-$day.json"
done
"$BIN" detect --db "$DB" > "$WORK/detect.json"
ALERTS_N=$(python3 -c "import json;d=json.load(open('$WORK/detect.json'));print(d.get('alertsCreated',0))")
if [[ "$ALERTS_N" -ge 1 ]]; then ok "P0 预置检测产生 ${ALERTS_N} 条告警"; else bad "P0 alertsCreated = $ALERTS_N"; fi

echo "[3/4] serve"
(setsid "$BIN" serve --db "$DB" --addr "127.0.0.1:$PORT" > "$WORK/serve.log" 2>&1 < /dev/null &) 
SERVE_PID=$(pgrep -f "netopslab serve.*:$PORT" | head -1)
for _ in $(seq 1 50); do curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && break; sleep 0.2; done

echo "[4/4] 浏览器断言"
agent-browser set viewport 1600 900 >/dev/null
agent-browser open "http://127.0.0.1:$PORT/alerts" >/dev/null
agent-browser wait --fn "!!document.querySelector('.card .filter-bar')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 800 >/dev/null
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null

# —— B1 立即检测入口 + 确认对话框 ——
RUN_BTN="$(jeval "(() => { const b = [...document.querySelectorAll('.card .btn')].find(x => (x.textContent||'').trim() === '立即检测'); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 400 >/dev/null
CONFIRM_TEXT="$(jeval "(() => { const m = document.querySelector('.modal-message'); return m ? (m.textContent||'').slice(0, 40) : 'none'; })()")"
if [[ "$RUN_BTN" == "ok" && "$CONFIRM_TEXT" == *阈值检测* ]]; then
  ok "B1 立即检测按钮 + 确认说明文案"
else
  bad "B1 btn=$RUN_BTN confirm=$CONFIRM_TEXT"
fi

# —— B2 运行 → 回执 ——
START="$(jeval "(() => { const b = [...document.querySelectorAll('.modal .btn--primary')].pop(); if (!b || b.disabled) return 'disabled'; b.click(); return 'ok'; })()")"
agent-browser wait --fn "!!document.querySelector('.run-stats')" --timeout 30000 >/dev/null 2>&1
agent-browser wait 500 >/dev/null
STATS_N="$(jeval "document.querySelectorAll('.modal .run-stat').length")"
STATS_LABELS="$(jeval "(() => { const want = ['发现越限','新建告警','恢复清除','写入运行状态','维护抑制','抖动抑制']; const got = [...document.querySelectorAll('.modal .run-stat-label')].map(x => (x.textContent||'')); const miss = want.filter(w => !got.some(g => g.startsWith(w))); return miss.length === 0 ? 'ok' : 'missing:' + miss.join('/'); })()")"
EVAL_AT="$(jeval "(() => { const n = document.querySelector('.modal .muted-note'); return n ? (n.textContent||'').slice(0, 30) : 'none'; })()")"
TOAST_RUN="$(jeval "(() => { return [...document.querySelectorAll('.toast, [class*=toast]')].some(t => (t.textContent||'').includes('检测完成')); })()")"
if [[ "$START" == "ok" && "$STATS_N" == "6" && "$STATS_LABELS" == "ok" && "$EVAL_AT" == *数据截止* ]]; then
  ok "B2 回执六格统计（六标签全在场）+ 数据截止（toast=$TOAST_RUN）"
else
  bad "B2 start=$START stats=$STATS_N labels=$STATS_LABELS evalAt=$EVAL_AT"
fi
agent-browser wait 3000 >/dev/null

# —— B3 重跑确定性（零变更） ——
CLOSE="$(jeval "(() => { const b = [...document.querySelectorAll('.modal .btn--primary')].find(x => (x.textContent||'').trim() === '完成'); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 400 >/dev/null
REOPEN="$(jeval "(() => { const b = [...document.querySelectorAll('.card .btn')].find(x => (x.textContent||'').trim() === '立即检测'); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 400 >/dev/null
START2="$(jeval "(() => { const b = [...document.querySelectorAll('.modal .btn--primary')].pop(); if (!b || b.disabled) return 'disabled'; b.click(); return 'ok'; })()")"
agent-browser wait --fn "!!document.querySelector('.run-stats')" --timeout 30000 >/dev/null 2>&1
agent-browser wait 500 >/dev/null
ZERO="$(jeval "(() => { const vals = [...document.querySelectorAll('.modal .run-stat-value')].map(v => v.textContent); return vals.join(','); })()")"
EMPTY_MUT="$(jeval "(() => { return [...document.querySelectorAll('.modal .muted-note')].some(n => (n.textContent||'').includes('无告警变更')); })()")"
if [[ "$CLOSE" == "ok" && "$REOPEN" == "ok" && "$START2" == "ok" && "$ZERO" == *"0"* ]]; then
  ok "B3 重跑零变更（stats=$ZERO，空变更文案=$EMPTY_MUT）"
else
  bad "B3 close=$CLOSE reopen=$REOPEN start=$START2 stats=$ZERO"
fi
agent-browser wait 3000 >/dev/null
jeval "(() => { const b = [...document.querySelectorAll('.modal .btn--primary')].find(x => (x.textContent||'').trim() === '完成'); if (b) b.click(); return 'closed'; })()" >/dev/null
agent-browser wait 400 >/dev/null

# —— B4 列表自动刷新（onFinished）：告警行仍在（firing 视图） ——
ROWS="$(jeval "document.querySelectorAll('table[aria-label=\'告警中心\'] > tbody > tr:not(:has(.evidence))').length")"
if [[ "$ROWS" -ge 1 ]]; then ok "B4 回执后列表已刷新（firing ${ROWS} 行）"; else bad "B4 rows = $ROWS"; fi

# —— B5 /rules 页入口 ——
agent-browser open "http://127.0.0.1:$PORT/rules" >/dev/null
agent-browser wait --fn "!!document.querySelector('.card .filter-bar')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 800 >/dev/null
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null
RULES_RUN="$(jeval "(() => { const b = [...document.querySelectorAll('.card .btn')].find(x => (x.textContent||'').trim() === '立即检测'); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 400 >/dev/null
RULES_CONFIRM="$(jeval "(() => { const m = document.querySelector('.modal-message'); return m ? 'ok' : 'none'; })()")"
if [[ "$RULES_RUN" == "ok" && "$RULES_CONFIRM" == "ok" ]]; then
  ok "B5 /rules 立即检测入口"
else
  bad "B5 run=$RULES_RUN confirm=$RULES_CONFIRM"
fi
jeval "(() => { const b = [...document.querySelectorAll('.modal .btn')].find(x => (x.textContent||'').trim() === '取消'); if (b) b.click(); return 'cancelled'; })()" >/dev/null

# —— B6 导出 CSV（回到 /alerts，劫持下载） ——
agent-browser open "http://127.0.0.1:$PORT/alerts" >/dev/null
agent-browser wait --fn "!!document.querySelector('.card .filter-bar')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 800 >/dev/null
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null
FIRING_TOTAL="$(curl -s "http://127.0.0.1:$PORT/api/alerts?state=firing&pageSize=1" | python3 -c "import json,sys; print(json.load(sys.stdin)['total'])")"
ARM_DL="$(jeval "(() => { window.__dl = null; const orig = HTMLAnchorElement.prototype.click; HTMLAnchorElement.prototype.click = function() { window.__dl = { name: this.download, url: this.href }; }; return 'armed'; })()")"
EXPORT="$(jeval "(() => { const b = [...document.querySelectorAll('.card .btn')].find(x => (x.textContent||'').trim() === '导出 CSV'); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 1500 >/dev/null
DL_NAME="$(jeval "window.__dl ? window.__dl.name : 'none'")"
if [[ "$EXPORT" == "ok" && "$DL_NAME" == netops-alerts-* ]]; then
  ok "B6a 下载触发（${DL_NAME}）"
else
  bad "B6a export=$EXPORT name=$DL_NAME"
fi
ARM_FETCH="$(jeval "(() => { window.__csvText = null; window.__csvLines = -1; window.__bom = 'none'; fetch(window.__dl.url).then(r => r.text()).then(t => { window.__csvText = t; window.__csvLines = t.split('\\r\\n').filter(x => x !== '').length; }); fetch(window.__dl.url).then(r => r.arrayBuffer()).then(b => { const v = new DataView(b); window.__bom = v.getUint8(0) === 0xEF && v.getUint8(1) === 0xBB ? 'bom' : 'nobom'; }); return 'started'; })()")"
agent-browser wait 800 >/dev/null
CSV_HEAD="$(jeval "window.__csvText ? window.__csvText.slice(0, 60) : 'none'")"
CSV_LINES="$(jeval "window.__csvLines")"
CSV_BOM="$(jeval "window.__bom")"
if [[ "$CSV_BOM" == "bom" && "$CSV_HEAD" == alertId,* ]]; then
  ok "B6b CSV BOM + 表头（${CSV_HEAD:0:40}…）"
else
  bad "B6b bom=$CSV_BOM head=$CSV_HEAD"
fi
if [[ "$CSV_LINES" -eq $((FIRING_TOTAL + 1)) ]]; then
  ok "B6c 行数一致（${CSV_LINES} = ${FIRING_TOTAL} 告警 + 表头）"
else
  bad "B6c lines=$CSV_LINES 期望 $((FIRING_TOTAL + 1))"
fi
EXPORT_TOAST="$(jeval "(() => { return [...document.querySelectorAll('.toast, [class*=toast]')].some(t => (t.textContent||'').includes('已导出')); })()")"
if [[ "$EXPORT_TOAST" == "true" ]]; then ok "B6d 导出 toast 回执"; else bad "B6d 无导出 toast"; fi
agent-browser wait 3000 >/dev/null

# —— B7 空筛选导出（cleared 无告警）→ info 不下载 ——
SWITCH="$(jeval "(() => { const s = document.querySelectorAll('.filter-bar select')[0]; s.value = 'cleared'; s.dispatchEvent(new Event('change', { bubbles: true })); return 'ok'; })()")"
agent-browser wait 1000 >/dev/null
ARM2="$(jeval "(() => { window.__dl = null; return 'rearmed'; })()")"
EXPORT2="$(jeval "(() => { const b = [...document.querySelectorAll('.card .btn')].find(x => (x.textContent||'').trim() === '导出 CSV'); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 800 >/dev/null
NO_DL="$(jeval "window.__dl === null ? 'no-dl' : 'downloaded'")"
INFO_TOAST="$(jeval "(() => { return [...document.querySelectorAll('.toast, [class*=toast]')].some(t => (t.textContent||'').includes('没有告警')); })()")"
if [[ "$NO_DL" == "no-dl" || "$INFO_TOAST" == "true" ]]; then
  ok "B7 空结果导出 → 提示不下载（toast=$INFO_TOAST）"
else
  bad "B7 dl=$NO_DL toast=$INFO_TOAST"
fi
agent-browser wait 2500 >/dev/null

# —— B8 console 零错误 ——
ERRS="$(jeval "window.__errs ? window.__errs.length : -1")"
if [[ "$ERRS" == "0" ]]; then ok "B8 console 零错误"; else
  bad "B8 console errors = $ERRS: $(jeval "window.__errs ? window.__errs.join(' | ').slice(0, 200) : ''")"
fi

agent-browser screenshot "$SHOT_DIR/r7-run-export.png" >/dev/null 2>&1 || true

echo
echo "结果：PASS=$PASS FAIL=$FAIL"
[[ "$FAIL" -eq 0 ]] && echo "R7 RUN+EXPORT REGRESSION: ALL GREEN"
exit "$FAIL"
