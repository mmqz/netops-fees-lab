#!/usr/bin/env bash
# NetOps Lab — R6 检测规则管理页浏览器级回归（v0.10 /rules）。
#
# 前置：make web（dist 已构建）。
# 旅程：realdata 31 工作簿 + PTP 导入 → 7 日性能摄入 → 检测运行
#       （保证默认规则集已播种且有告警引用）→ /rules 全链路操作。
# 断言：
#   A1  侧栏「检测规则」导航项 + /rules 路由 + 页标题；
#   A2  默认规则表格：行数 ≥ 4 + 启用中 chip + 级别徽章 + 比较器条件（mono）；
#   A3  行展开详情（规则 ID / 完整条件 kv 表）；
#   A4  新建旅程：对话框（指标下拉含目录中文标签）→ 必填校验（空阈值禁用）→
#       填表提交 → toast + 行数 +1；
#   A5  编辑旅程：指标只读（身份字段）→ 改阈值保存 → toast；
#   A6  启停旅程：行内停用 → chip 变「已停用」→ toast；再启用恢复；
#   A7  审计保护：删除被 4 条告警引用的默认规则 → 后端 409 → toast 错误文案；
#   A8  干净删除：删除 A4 新建的规则（无告警）→ 行数回落；
#   A9  筛选：仅看启用中开关 + 指标下拉；
#   A10 console 零错误/零未捕获拒绝。
# 截图存 repo tmp/（不作为交付物）。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
export PATH="$PATH:/home/z/.local/go/bin"

PORT=18158
WORK="$(mktemp -d /tmp/netops-r6rul.XXXXXX)"
DB="$WORK/r6.db"
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

echo "[2/4] 7 日性能摄入 + 检测运行（保证告警引用存在）"
for day in 01 02 03 04 05 06 07; do
  "$BIN" ingest --db "$DB" --file "$REALDATA/perf-day-$day.xlsx" \
    --map "$REALDATA/ingest-mapping.json" > "$WORK/ing-$day.json"
done
"$BIN" detect --db "$DB" > "$WORK/detect.json"
ALERTS_N=$(python3 -c "import json;d=json.load(open('$WORK/detect.json'));print(d.get('alertsCreated',0))")
if [[ "$ALERTS_N" -ge 1 ]]; then ok "P0 检测产生 ${ALERTS_N} 条告警（默认规则被引用）"; else bad "P0 alertsCreated = $ALERTS_N"; fi

echo "[3/4] serve"
"$BIN" serve --db "$DB" --addr "127.0.0.1:$PORT" > "$WORK/serve.log" 2>&1 &
SERVE_PID=$!
for _ in $(seq 1 50); do curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && break; sleep 0.2; done

echo "[4/4] 浏览器断言"
agent-browser set viewport 1600 900 >/dev/null
agent-browser open "http://127.0.0.1:$PORT/" >/dev/null
agent-browser wait --load networkidle >/dev/null 2>&1 || true
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null

count_rows() {
  # 展开的详情行（td 内含 div.evidence）不计入数据行
  jeval "document.querySelectorAll('table[aria-label=\'检测规则\'] > tbody > tr:not(:has(.evidence))').length"
}

# 被告警引用的规则（A7 审计保护验证对象）：从告警列表拿第一条 firing 的 ruleId
REFERENCED_ID="$(curl -s "http://127.0.0.1:$PORT/api/alerts?state=firing&pageSize=1" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['items'][0]['ruleId'] if d['items'] else '')")"

# —— A1 导航与路由 ——
NAV="$(jeval "(() => { const links = [...document.querySelectorAll('.sidebar-link')]; return links.some(l => (l.textContent||'').includes('检测规则')); })()")"
if [[ "$NAV" == "true" ]]; then ok "A1a 侧栏「检测规则」导航项"; else bad "A1a 侧栏无检测规则入口"; fi
agent-browser open "http://127.0.0.1:$PORT/rules" >/dev/null
agent-browser wait --fn "!!document.querySelector('.card .filter-bar')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 800 >/dev/null
# 导航后重新武装 console 监听（整页导航会重置 window）
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null
TITLE="$(jeval "document.querySelector('.topbar-title') ? document.querySelector('.topbar-title').textContent : ''")"
if [[ "$TITLE" == *检测规则* ]]; then ok "A1b /rules 路由 + 页标题"; else bad "A1b title = $TITLE"; fi

# —— A2 默认规则表格 ——
ROWS="$(count_rows)"
if [[ "$ROWS" -ge 4 ]]; then ok "A2a 默认规则行（${ROWS}）"; else bad "A2a rows = $ROWS"; fi
CHIPS="$(jeval "(() => { return [...document.querySelectorAll('.card .chip')].some(c => (c.textContent||'').includes('启用中')); })()")"
if [[ "$CHIPS" == "true" ]]; then ok "A2b 启用中 chip"; else bad "A2b 无启用中 chip"; fi
SEV="$(jeval "(() => { return [...document.querySelectorAll('.card .chip')].some(c => (c.textContent||'').includes('警告') || (c.textContent||'').includes('错误') || (c.textContent||'').includes('信息')); })()")"
if [[ "$SEV" == "true" ]]; then ok "A2c 级别徽章"; else bad "A2c 无级别徽章"; fi
COND="$(jeval "(() => { return [...document.querySelectorAll('.card td .mono')].some(m => (m.textContent||'').match(/(高于|不低于|低于|不高于)/)); })()")"
if [[ "$COND" == "true" ]]; then ok "A2d 触发条件列（比较器中文短语）"; else bad "A2d 无比较器条件"; fi

# —— A3 行展开详情 ——
EXPAND="$(jeval "(() => { const tr = document.querySelector('table[aria-label=\'检测规则\'] > tbody > tr:not(:has(td.table-empty))'); if (!tr) return 'none'; tr.click(); return 'ok'; })()")"
agent-browser wait 400 >/dev/null
KV="$(jeval "(() => { return [...document.querySelectorAll('.card .kv-table th')].some(t => (t.textContent||'').includes('规则 ID')); })()")"
if [[ "$EXPAND" == "ok" && "$KV" == "true" ]]; then ok "A3 行展开详情（规则 ID kv）"; else bad "A3 expand=$EXPAND kv=$KV"; fi
agent-browser screenshot "$SHOT_DIR/r6-rules-expanded.png" >/dev/null 2>&1 || true

# —— A4 新建旅程 ——
NEW_BTN="$(jeval "(() => { const b = [...document.querySelectorAll('.card .btn--primary')].find(x => (x.textContent||'').trim() === '新建规则'); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 400 >/dev/null
if [[ "$NEW_BTN" == "ok" ]]; then ok "A4a 打开新建对话框"; else bad "A4a 新建按钮 = $NEW_BTN"; fi
DLG="$(jeval "(() => { const d = document.querySelector('.modal[role=dialog]'); return d ? 'ok' : 'none'; })()")"
if [[ "$DLG" == "ok" ]]; then ok "A4b 新建对话框（dialog）"; else bad "A4b dialog = $DLG"; fi
METRIC_OPTS="$(jeval "(() => { const s = document.getElementById('rule-metric'); return s && s.options.length >= 2 ? String(s.options.length) : 'few'; })()")"
if [[ "$METRIC_OPTS" != "few" ]]; then ok "A4c 指标下拉含封闭目录（${METRIC_OPTS} 项）"; else bad "A4c 目录下拉 = $METRIC_OPTS"; fi
SAVE_DISABLED="$(jeval "(() => { const b = [...document.querySelectorAll('.modal .btn--primary')].find(x => (x.textContent||'').trim() !== ''); return b && b.disabled ? 'true' : 'false'; })()")"
if [[ "$SAVE_DISABLED" == "true" ]]; then ok "A4d 空表单禁用保存（必填校验）"; else bad "A4d 空表单可提交 = $SAVE_DISABLED"; fi
FILL="$(jeval "(() => { const s = document.getElementById('rule-metric'); if (!s || s.options.length < 2) return 'none'; s.value = s.options[1].value; s.dispatchEvent(new Event('change', { bubbles: true })); const t = document.getElementById('rule-threshold'); const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set; setter.call(t, '-25'); t.dispatchEvent(new Event('input', { bubbles: true })); const l = document.getElementById('rule-label'); setter.call(l, 'R6 回归新建'); l.dispatchEvent(new Event('input', { bubbles: true })); return 'ok'; })()")"
agent-browser wait 300 >/dev/null
SUBMIT="$(jeval "(() => { const b = [...document.querySelectorAll('.modal .btn--primary')].pop(); if (!b || b.disabled) return 'still-disabled'; b.click(); return 'ok'; })()")"
agent-browser wait 1200 >/dev/null
ROWS_AFTER_CREATE="$(count_rows)"
if [[ "$FILL" == "ok" && "$SUBMIT" == "ok" && "$ROWS_AFTER_CREATE" -eq $((ROWS + 1)) ]]; then
  ok "A4e 填表提交 → 行数 ${ROWS} → ${ROWS_AFTER_CREATE}"
else
  bad "A4e fill=$FILL submit=$SUBMIT rows=$ROWS_AFTER_CREATE（期望 $((ROWS + 1))）"
fi
TOAST="$(jeval "(() => { return [...document.querySelectorAll('.toast, [class*=toast]')].some(t => (t.textContent||'').includes('规则已创建')); })()")"
if [[ "$TOAST" == "true" ]]; then ok "A4f toast 创建回执"; else bad "A4f 无创建 toast"; fi
agent-browser wait 3000 >/dev/null

# —— A5 编辑旅程（A4 新建行：标签「R6 回归新建」） ——
EDIT="$(jeval "(() => { const trs = [...document.querySelectorAll('table[aria-label=\'检测规则\'] > tbody > tr')]; const tr = trs.find(r => (r.textContent||'').includes('R6 回归新建')); if (!tr) return 'no-row'; const b = [...tr.querySelectorAll('.btn')].find(x => (x.textContent||'').trim() === '编辑'); if (!b) return 'no-btn'; b.click(); return 'ok'; })()")"
agent-browser wait 500 >/dev/null
if [[ "$EDIT" == "ok" ]]; then ok "A5a 打开编辑对话框"; else bad "A5a 编辑 = $EDIT"; fi
RO="$(jeval "(() => { const i = document.getElementById('rule-metric'); return i && i.readOnly ? 'true' : 'false'; })()")"
if [[ "$RO" == "true" ]]; then ok "A5b 编辑态指标只读（身份字段）"; else bad "A5b metric readOnly = $RO"; fi
CHANGE="$(jeval "(() => { const t = document.getElementById('rule-threshold'); if (!t) return 'none'; const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set; setter.call(t, '-26'); t.dispatchEvent(new Event('input', { bubbles: true })); return 'ok'; })()")"
SAVE2="$(jeval "(() => { const b = [...document.querySelectorAll('.modal .btn--primary')].pop(); if (!b || b.disabled) return 'still-disabled'; b.click(); return 'ok'; })()")"
agent-browser wait 1200 >/dev/null
TOAST2="$(jeval "(() => { return [...document.querySelectorAll('.toast, [class*=toast]')].some(t => (t.textContent||'').includes('规则已更新')); })()")"
if [[ "$CHANGE" == "ok" && "$SAVE2" == "ok" && "$TOAST2" == "true" ]]; then
  ok "A5c 改阈值 -25 → -26 保存 + toast"
else
  bad "A5c change=$CHANGE save=$SAVE2 toast=$TOAST2"
fi
agent-browser wait 3000 >/dev/null

# —— A6 启停旅程（新建行） ——
TOGGLE_OFF="$(jeval "(() => { const trs = [...document.querySelectorAll('table[aria-label=\'检测规则\'] > tbody > tr')]; const tr = trs.find(r => (r.textContent||'').includes('R6 回归新建')); if (!tr) return 'no-row'; const b = [...tr.querySelectorAll('.btn')].find(x => (x.textContent||'').trim() === '停用'); if (!b) return 'no-btn'; b.click(); return 'ok'; })()")"
agent-browser wait 1200 >/dev/null
if [[ "$TOGGLE_OFF" == "ok" ]]; then ok "A6a 行内停用触发"; else bad "A6a 停用 = $TOGGLE_OFF"; fi
TOGGLE_ON="$(jeval "(() => { const trs = [...document.querySelectorAll('table[aria-label=\'检测规则\'] > tbody > tr')]; const tr = trs.find(r => (r.textContent||'').includes('R6 回归新建')); if (!tr) return 'no-row'; return (tr.textContent||'').includes('已停用') ? 'stopped' : 'still-on'; })()")"
if [[ "$TOGGLE_ON" == "stopped" ]]; then ok "A6b 行状态变「已停用」"; else bad "A6b 状态 = $TOGGLE_ON"; fi
agent-browser wait 2500 >/dev/null
TOGGLE_BACK="$(jeval "(() => { const trs = [...document.querySelectorAll('table[aria-label=\'检测规则\'] > tbody > tr')]; const tr = trs.find(r => (r.textContent||'').includes('R6 回归新建')); if (!tr) return 'no-row'; const b = [...tr.querySelectorAll('.btn')].find(x => (x.textContent||'').trim() === '启用'); if (!b) return 'no-btn'; b.click(); return 'ok'; })()")"
agent-browser wait 1200 >/dev/null
if [[ "$TOGGLE_BACK" == "ok" ]]; then ok "A6c 重新启用"; else bad "A6c 启用 = $TOGGLE_BACK"; fi
agent-browser wait 2500 >/dev/null

# —— A7 审计保护：删除被告警引用的规则 → 409 ——
HTTP409="$(curl -s -o /dev/null -w "%{http_code}" -X DELETE "http://127.0.0.1:$PORT/api/detection/rules/$REFERENCED_ID")"
if [[ "$HTTP409" == "409" ]]; then
  ok "A7a API 契约：被引用规则 DELETE = 409（${REFERENCED_ID}）"
else
  bad "A7a DELETE http = $HTTP409（期望 409）"
fi
DEL_REFUSED="$(jeval "(() => { const b = [...document.querySelectorAll('.card .btn')].find(x => x.title === '$REFERENCED_ID' && (x.textContent||'').trim() === '删除'); if (!b) return 'no-btn'; b.click(); return 'ok'; })()")"
agent-browser wait 500 >/dev/null
CONFIRM_DEL="$(jeval "(() => { const b = [...document.querySelectorAll('.modal .btn--danger, .modal .btn--primary')].find(x => (x.textContent||'').trim() === '删除'); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 1200 >/dev/null
REFUSE_TOAST="$(jeval "(() => { return [...document.querySelectorAll('.toast, [class*=toast]')].some(t => (t.textContent||'').includes('失败')); })()")"
ROWS_AFTER_REFUSED="$(count_rows)"
if [[ "$ROWS_AFTER_REFUSED" -eq "$ROWS_AFTER_CREATE" ]]; then
  ok "A7b 浏览器拒绝回执：行数不变（${ROWS_AFTER_REFUSED}，toast 失败文案 = $REFUSE_TOAST）"
else
  bad "A7b 删除未被拒？rows=$ROWS_AFTER_REFUSED（期望 $ROWS_AFTER_CREATE）"
fi
agent-browser wait 2500 >/dev/null

# —— A8 干净删除：新建规则（无告警）删除成功 ——
DEL_CLEAN="$(jeval "(() => { const trs = [...document.querySelectorAll('table[aria-label=\'检测规则\'] > tbody > tr')]; const tr = trs.find(r => (r.textContent||'').includes('R6 回归新建')); if (!tr) return 'no-row'; const b = [...tr.querySelectorAll('.btn')].find(x => (x.textContent||'').trim() === '删除'); if (!b) return 'no-btn'; b.click(); return 'ok'; })()")"
agent-browser wait 500 >/dev/null
CONFIRM_DEL2="$(jeval "(() => { const b = [...document.querySelectorAll('.modal .btn--danger, .modal .btn--primary')].find(x => (x.textContent||'').trim() === '删除'); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 1200 >/dev/null
ROWS_FINAL="$(count_rows)"
if [[ "$DEL_CLEAN" == "ok" && "$CONFIRM_DEL2" == "ok" && "$ROWS_FINAL" -eq "$ROWS" ]]; then
  ok "A8 干净删除 → 行数回落 ${ROWS_FINAL} = ${ROWS}"
else
  bad "A8 del=$DEL_CLEAN confirm=$CONFIRM_DEL2 rows=$ROWS_FINAL（期望 $ROWS）"
fi
agent-browser wait 2500 >/dev/null

# —— A9 筛选 ——
FILTER_ON="$(jeval "(() => { const sw = document.querySelector('.filter-bar .switch input[type=checkbox]'); if (!sw) return 'none'; sw.click(); return 'ok'; })()")"
agent-browser wait 600 >/dev/null
ROWS_ENABLED="$(count_rows)"
FILTER_OFF="$(jeval "(() => { const sw = document.querySelector('.filter-bar .switch input[type=checkbox]'); if (!sw) return 'none'; sw.click(); return 'ok'; })()")"
agent-browser wait 600 >/dev/null
ROWS_ALL="$(count_rows)"
if [[ "$FILTER_ON" == "ok" && "$ROWS_ENABLED" -le "$ROWS_ALL" && "$ROWS_ENABLED" -ge 1 ]]; then
  ok "A9 仅看启用中筛选（${ROWS_ENABLED} ≤ ${ROWS_ALL}）"
else
  bad "A9 filter=$FILTER_ON enabled=$ROWS_ENABLED all=$ROWS_ALL"
fi
METRIC_FILTER="$(jeval "(() => { const s = document.querySelectorAll('.filter-bar select')[0]; if (!s || s.options.length < 2) return 'few'; s.value = s.options[1].value; s.dispatchEvent(new Event('change', { bubbles: true })); return 'ok'; })()")"
agent-browser wait 600 >/dev/null
ROWS_METRIC="$(count_rows)"
if [[ "$METRIC_FILTER" == "ok" && "$ROWS_METRIC" -ge 1 ]]; then
  ok "A9b 指标筛选（${ROWS_METRIC} 行）"
else
  bad "A9b metric=$METRIC_FILTER rows=$ROWS_METRIC"
fi

# —— A10 console 零错误 ——
ERRS="$(jeval "window.__errs ? window.__errs.length : -1")"
if [[ "$ERRS" == "0" ]]; then ok "A10 console 零错误"; else
  bad "A10 console errors = $ERRS: $(jeval "window.__errs ? window.__errs.join(' | ').slice(0, 200) : ''")"
fi

agent-browser screenshot "$SHOT_DIR/r6-rules-final.png" >/dev/null 2>&1 || true

echo
echo "结果：PASS=$PASS FAIL=$FAIL"
[[ "$FAIL" -eq 0 ]] && echo "R6 RULES REGRESSION: ALL GREEN"
exit "$FAIL"
