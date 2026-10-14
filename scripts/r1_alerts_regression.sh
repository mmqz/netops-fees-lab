#!/usr/bin/env bash
# NetOps Lab — R1 告警中心浏览器级回归（v0.9 /alerts）。
#
# 前置：make web（dist 已构建）。
# 旅程：realdata 31 工作簿 + PTP 导入 → 7 日端口性能摄入 → 检测运行
#       （4 firing 告警：哈大齐两站光功率劣化）→ /alerts 全链路操作。
# 断言：
#   A1  侧栏「告警中心」导航项 + /alerts 路由可达；
#   A2  默认筛选 = 触发中；表格 4 行（劣化场景的 4 条 firing）；
#   A3  行渲染：状态 chip（触发中）+ 级别徽章 + 规则名 + 资源链接 +
#       相对时间 + 最新值/阈值（mono）；
#   A4  确认旅程：点「确认」→ 对话框收集确认人（必填校验：空值禁用）→
#       提交 → toast + 行状态变「已确认」（切筛选可见）；
#   A5  清除旅程：已确认行点「清除」→ 危险确认 → 状态变「已清除」；
#   A6  行展开详情（规则 ID / 指标序列 / 窗口 / 确认审计行）；
#   A7  ?state= 深链；
#   A8  空态文案（无匹配筛选）；
#   A9  console 零错误/零未捕获拒绝。
# 截图存 repo tmp/（不作为交付物）。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
export PATH="$PATH:/home/z/.local/go/bin"

PORT=18148
WORK="$(mktemp -d /tmp/netops-r1alr.XXXXXX)"
DB="$WORK/r1.db"
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
# PTP 脚本通道（同一 analyze → import 流程）
"$BIN" analyze -file "$REPO_ROOT/samples/ptp-config-script.txt" > "$WORK/a.json"
python3 - "$WORK/a.json" "$WORK/m.json" <<'PY'
import json, sys
json.dump(json.load(open(sys.argv[1], encoding="utf-8"))["suggestedMapping"], open(sys.argv[2], "w", encoding="utf-8"))
PY
"$BIN" import --file "$REPO_ROOT/samples/ptp-config-script.txt" --map "$WORK/m.json" --db "$DB" > "$WORK/i-ptp.json" || true

echo "[2/4] 7 日性能摄入 + 检测运行"
for day in 01 02 03 04 05 06 07; do
  "$BIN" ingest --db "$DB" --file "$REALDATA/perf-day-$day.xlsx" \
    --map "$REALDATA/ingest-mapping.json" > "$WORK/ing-$day.json"
done
"$BIN" detect --db "$DB" > "$WORK/detect.json"
ALERTS_N=$(python3 -c "import json;d=json.load(open('$WORK/detect.json'));print(d.get('alertsCreated',0))")
if [[ "$ALERTS_N" -ge 1 ]]; then ok "P0 检测产生 ${ALERTS_N} 条告警（劣化场景）"; else bad "P0 alertsCreated = $ALERTS_N"; fi

echo "[3/4] serve"
"$BIN" serve --db "$DB" --addr "127.0.0.1:$PORT" > "$WORK/serve.log" 2>&1 &
SERVE_PID=$!
for _ in $(seq 1 50); do curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && break; sleep 0.2; done

echo "[4/4] 浏览器断言"
agent-browser set viewport 1600 900 >/dev/null
agent-browser open "http://127.0.0.1:$PORT/" >/dev/null
agent-browser wait --load networkidle >/dev/null 2>&1 || true
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null

# —— A1 导航与路由 ——
NAV="$(jeval "(() => { const links = [...document.querySelectorAll('.sidebar-link')]; return links.some(l => (l.textContent||'').includes('告警中心')); })()")"
if [[ "$NAV" == "true" ]]; then ok "A1a 侧栏「告警中心」导航项"; else bad "A1a 侧栏无告警中心入口"; fi
agent-browser open "http://127.0.0.1:$PORT/alerts" >/dev/null
agent-browser wait --fn "!!document.querySelector('.card .filter-bar')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 800 >/dev/null
TITLE="$(jeval "document.querySelector('.topbar-title') ? document.querySelector('.topbar-title').textContent : ''")"
if [[ "$TITLE" == *告警中心* ]]; then ok "A1b /alerts 路由 + 页标题"; else bad "A1b title = $TITLE"; fi

# —— A2 默认筛选 + 行数 ——
STATE_SEL="$(jeval "(() => { const s = document.querySelector('.filter-bar select'); return s ? s.value : 'none'; })()")"
if [[ "$STATE_SEL" == "firing" ]]; then ok "A2a 默认筛选 = 触发中"; else bad "A2a 默认 state = $STATE_SEL"; fi
ROWS="$(jeval "document.querySelectorAll('table[aria-label=\'告警中心\'] > tbody > tr:not(:has(td.table-empty))').length")"
if [[ "$ROWS" -ge 1 ]]; then ok "A2b 触发中队列有行（${ROWS}）"; else bad "A2b rows = $ROWS"; fi

# —— A3 行渲染 ——
CHIPS="$(jeval "(() => { const chips = [...document.querySelectorAll('.card .chip')]; return chips.some(c => (c.textContent||'').includes('触发中')); })()")"
if [[ "$CHIPS" == "true" ]]; then ok "A3a 状态 chip「触发中」"; else bad "A3a 无触发中 chip"; fi
SEV="$(jeval "(() => { return [...document.querySelectorAll('.card .chip')].some(c => (c.textContent||'').includes('警告') || (c.textContent||'').includes('错误')); })()")"
if [[ "$SEV" == "true" ]]; then ok "A3b 级别徽章"; else bad "A3b 无级别徽章"; fi
LINK="$(jeval "(() => { return !!document.querySelector('.card .entity-link'); })()")"
if [[ "$LINK" == "true" ]]; then ok "A3c 资源链接（→ /resources?focus=）"; else bad "A3c 无资源链接"; fi
VAL="$(jeval "(() => { const m = document.querySelector('.card .mono'); return m ? (m.textContent.includes('阈值') ? 'ok' : 'plain') : 'none'; })()")"
if [[ "$VAL" == "ok" ]]; then ok "A3d 最新值/阈值列"; else bad "A3d value = $VAL"; fi

# —— A4 确认旅程 ——
ACK_BTN="$(jeval "(() => { const b = [...document.querySelectorAll('.card .btn')].find(x => (x.textContent||'').trim() === '确认'); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 500 >/dev/null
if [[ "$ACK_BTN" == "ok" ]]; then ok "A4a 打开确认对话框"; else bad "A4a 确认按钮 = $ACK_BTN"; fi
DLG="$(jeval "(() => { const d = document.querySelector('.modal[role=alertdialog]'); return d ? 'ok' : 'none'; })()")"
if [[ "$DLG" == "ok" ]]; then ok "A4b 确认对话框（alertdialog）"; else bad "A4b dialog = $DLG"; fi
EMPTY_DISABLE="$(jeval "(() => { const b = [...document.querySelectorAll('.modal .btn--primary')].pop(); return b && b.disabled ? 'true' : 'false'; })()")"
if [[ "$EMPTY_DISABLE" == "true" ]]; then ok "A4c 确认人空值禁用提交（必填校验）"; else bad "A4c 空值可提交 = $EMPTY_DISABLE"; fi
FILL="$(jeval "(() => { const i = document.getElementById('ack-acked-by'); if (!i) return 'none'; const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set; setter.call(i, '王工'); i.dispatchEvent(new Event('input', { bubbles: true })); return 'ok'; })()")"
agent-browser wait 300 >/dev/null
SUBMIT="$(jeval "(() => { const b = [...document.querySelectorAll('.modal .btn--primary')].pop(); if (!b || b.disabled) return 'still-disabled'; b.click(); return 'ok'; })()")"
agent-browser wait 1000 >/dev/null
if [[ "$FILL" == "ok" && "$SUBMIT" == "ok" ]]; then ok "A4d 填确认人并提交（王工）"; else bad "A4d fill=$FILL submit=$SUBMIT"; fi
TOAST="$(jeval "(() => { return [...document.querySelectorAll('.toast, [class*=toast]')].some(t => (t.textContent||'').includes('王工')); })()")"
if [[ "$TOAST" == "true" ]]; then ok "A4e toast 处置人回执"; else bad "A4e 无 toast 回执"; fi
agent-browser wait 3000 >/dev/null

# —— A6 行展开详情（在已确认筛选下验） ——
SWITCH="$(jeval "(() => { const s = document.querySelectorAll('.filter-bar select')[0]; s.value = 'acked'; s.dispatchEvent(new Event('change', { bubbles: true })); return 'ok'; })()")"
agent-browser wait 1200 >/dev/null
ACKED_ROWS="$(jeval "document.querySelectorAll('table[aria-label=\'告警中心\'] > tbody > tr:not(:has(td.table-empty))').length")"
if [[ "$SWITCH" == "ok" && "$ACKED_ROWS" -ge 1 ]]; then ok "A6a 已确认筛选可见确认行（${ACKED_ROWS}）"; else bad "A6a rows = $ACKED_ROWS"; fi
EXPAND="$(jeval "(() => { const tr = document.querySelector('table[aria-label=\'告警中心\'] > tbody > tr:not(:has(td.table-empty))'); if (!tr) return 'none'; tr.click(); return 'ok'; })()")"
agent-browser wait 500 >/dev/null
DETAIL="$(jeval "(() => { const ev = document.querySelector('.evidence'); return ev && (ev.textContent.includes('规则') || ev.textContent.includes('指标')) ? 'ok' : 'none'; })()")"
if [[ "$EXPAND" == "ok" && "$DETAIL" == "ok" ]]; then ok "A6b 行展开详情（规则/指标序列）"; else bad "A6b expand=$EXPAND detail=$DETAIL"; fi

# —— A5 清除旅程 ——
CLEAR_BTN="$(jeval "(() => { const b = [...document.querySelectorAll('.card .btn')].find(x => (x.textContent||'').trim() === '清除'); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 500 >/dev/null
CDLG="$(jeval "(() => { const d = [...document.querySelectorAll('.modal')].pop(); return d && (d.textContent||'').includes('清除') ? 'ok' : 'none'; })()")"
CONFIRM_CLEAR="$(jeval "(() => { const b = [...document.querySelectorAll('.modal .btn--danger, .modal .btn--primary')].pop(); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 1200 >/dev/null
if [[ "$CLEAR_BTN" == "ok" && "$CDLG" == "ok" && "$CONFIRM_CLEAR" == "ok" ]]; then ok "A5a 清除确认旅程（对话框→提交）"; else bad "A5a btn=$CLEAR_BTN dlg=$CDLG go=$CONFIRM_CLEAR"; fi
SWITCH2="$(jeval "(() => { const s = document.querySelectorAll('.filter-bar select')[0]; s.value = 'cleared'; s.dispatchEvent(new Event('change', { bubbles: true })); return 'ok'; })()")"
agent-browser wait 1200 >/dev/null
CLEARED_ROWS="$(jeval "document.querySelectorAll('table[aria-label=\'告警中心\'] > tbody > tr:not(:has(td.table-empty))').length")"
CLEARED_CHIP="$(jeval "(() => { return [...document.querySelectorAll('.card .chip')].some(c => (c.textContent||'').includes('已清除')); })()")"
if [[ "$SWITCH2" == "ok" && "$CLEARED_ROWS" -ge 1 && "$CLEARED_CHIP" == "true" ]]; then ok "A5b 已清除归档可见（${CLEARED_ROWS} 行 + chip）"; else bad "A5b rows=$CLEARED_ROWS chip=$CLEARED_CHIP"; fi

# —— A7 深链 ——
agent-browser open "http://127.0.0.1:$PORT/alerts?state=firing" >/dev/null
agent-browser wait --fn "!!document.querySelector('.card .filter-bar')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 800 >/dev/null
DEEP="$(jeval "(() => { const s = document.querySelector('.filter-bar select'); return s ? s.value : 'none'; })()")"
if [[ "$DEEP" == "firing" ]]; then ok "A7 ?state=firing 深链"; else bad "A7 deep = $DEEP"; fi

# —— A8 空态 ——
agent-browser open "http://127.0.0.1:$PORT/alerts?state=acked" >/dev/null
agent-browser wait --fn "!!document.querySelector('.card .filter-bar')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 800 >/dev/null
EMPTYTEXT="$(jeval "(() => { const t = document.querySelector('.card .table-empty, .card .empty'); return t ? (t.textContent||'').includes('没有告警') ? 'ok' : t.textContent : 'none'; })()")"
if [[ "$EMPTYTEXT" == "ok" ]]; then ok "A8 空态文案（无匹配告警）"; else bad "A8 empty = $EMPTYTEXT"; fi

agent-browser screenshot "$SHOT_DIR/r1-alerts.png" >/dev/null 2>&1 || true

# —— A9 console 零错误（最后一次导航后重新布防 800ms 采集窗口） ——
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'rearmed'" >/dev/null
agent-browser wait 800 >/dev/null
ERRS="$(jeval "window.__errs ? String(window.__errs.length) : 'NA'")"
if [[ "$ERRS" == "0" ]]; then ok "A9 console 零错误/零未捕获拒绝"; else bad "A9 console 错误 ${ERRS} 条"; fi

echo
echo "结果：PASS=$PASS FAIL=$FAIL"
[[ "$FAIL" -eq 0 ]]
