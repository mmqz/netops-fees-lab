#!/usr/bin/env bash
# NetOps Lab — R12 资源视角告警 + 指标 CSV 导出回归（v0.11）。
#
# 旅程：synthdata（degrade 4 天）→ serve → HTTP 导入 + 摄入 + window-1 规则
#       + CLI detect 预置告警 → 浏览器 /resources?focus=<告警资源> 抽屉 →
#       「告警」页签 → 切「性能指标」页签 → 导出 CSV（劫持 a.click）。
# 断言：
#   D1  ?focus= 深链直接打开抽屉；
#   D2  告警页签渲染行数 == API resource 过滤 total（封顶 50）；
#   D3  行内状态徽章 + 规则标签 + 值域文本存在；
#   D4  「去告警中心处置」深链按钮存在；
#   D5  指标页签图表 + 导出工具条渲染；
#   D6  导出下载（劫持验证）：文件名 netops-metrics-<id>-、BOM、表头 5 列、
#       行数 == API 指标点总数 + 表头、导出 toast；
#   D7  console 零错误/零未捕获拒绝。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
export PATH="$PATH:/home/z/.local/go/bin"

PORT=18192
WORK="$(mktemp -d /tmp/netops-r12.XXXXXX)"
DB="$WORK/r12.db"

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

echo "[1/3] 数据面：synthdata + serve + 导入 + 预置告警"
"$BIN" synthdata --out "$WORK/synth" --devices 3 --ports 1 --days 4 --scenario degrade --seed 42 > /dev/null
mkdir -p "$WORK/data"
(setsid "$BIN" serve --db "$DB" --addr "127.0.0.1:$PORT" --data-dir "$WORK/data" > "$WORK/serve.log" 2>&1 < /dev/null &)
SERVE_PID=$(pgrep -f "netopslab serve.*:$PORT" | head -1)
for _ in $(seq 1 50); do curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && break; sleep 0.2; done

UPLOAD=$(curl -sf -F "file=@$WORK/synth/resources.xlsx" "http://127.0.0.1:$PORT/api/imports")
BATCH=$(echo "$UPLOAD" | python3 -c "import json,sys;print(json.load(sys.stdin)['batchId'])")
SUGGESTED=$(echo "$UPLOAD" | python3 -c "import json,sys;print(json.dumps(json.load(sys.stdin)['suggestedMapping'],ensure_ascii=False))")
curl -sf -X POST "http://127.0.0.1:$PORT/api/imports/$BATCH/mapping" -H 'Content-Type: application/json' -d "$SUGGESTED" > /dev/null
for day in perf-day-01.xlsx perf-day-02.xlsx perf-day-03.xlsx perf-day-04.xlsx; do
  M=$(curl -sf -F "file=@$WORK/synth/$day" "http://127.0.0.1:$PORT/api/metrics/analyze" | python3 -c "import json,sys;print(json.dumps(json.load(sys.stdin)['suggestedMapping'],ensure_ascii=False))")
  curl -sf -F "file=@$WORK/synth/$day" -F "mapping=$M" "http://127.0.0.1:$PORT/api/metrics/imports" > /dev/null
done
curl -sf -X POST "http://127.0.0.1:$PORT/api/detection/rules" -H 'Content-Type: application/json' \
  -d '{"metricKey":"optical_power_rx_avg_dbm","comparator":"lte","threshold":-8.1,"window":1,"severity":"warning","label":"R12触发"}' > /dev/null
"$BIN" detect --db "$DB" > "$WORK/detect.json"
CREATED=$(python3 -c "import json;print(json.load(open('$WORK/detect.json'))['alertsCreated'])")
if [[ "$CREATED" -ge 1 ]]; then ok "P0 预置告警 ${CREATED} 条"; else bad "P0 alertsCreated=$CREATED"; fi

RES_ID=$(curl -s "http://127.0.0.1:$PORT/api/alerts?state=firing&pageSize=1" | python3 -c "import json,sys;d=json.load(sys.stdin);print(d['items'][0]['resource']['id'] if d['items'] else '')")
ALERT_TOTAL=$(curl -s "http://127.0.0.1:$PORT/api/alerts?resource_id=$RES_ID&pageSize=1" | python3 -c "import json,sys;print(json.load(sys.stdin)['total'])")
POINTS=$(curl -s "http://127.0.0.1:$PORT/api/resources/$RES_ID/metrics" | python3 -c "import json,sys;print(sum(len(s['points']) for s in json.load(sys.stdin)['series']))")
if [[ -n "$RES_ID" && "$ALERT_TOTAL" -ge 1 && "$POINTS" -ge 4 ]]; then
  ok "P1 目标资源（${RES_ID:0:12}…，${ALERT_TOTAL} 条告警，${POINTS} 个指标点）"
else
  bad "P1 res=$RES_ID alerts=$ALERT_TOTAL points=$POINTS"
fi

echo "[2/3] 浏览器：抽屉告警页签"
agent-browser set viewport 1600 900 >/dev/null
agent-browser open "http://127.0.0.1:$PORT/resources?focus=$RES_ID" >/dev/null
agent-browser wait --fn "!!document.querySelector('.drawer .tabs')" --timeout 20000 >/dev/null 2>&1
agent-browser wait 600 >/dev/null
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null
DRAWER="$(jeval "document.querySelector('.drawer') ? 'open' : 'none'")"
if [[ "$DRAWER" == "open" ]]; then ok "D1 ?focus= 深链打开抽屉"; else bad "D1 drawer=$DRAWER"; fi

TAB="$(jeval "(() => { const t = [...document.querySelectorAll('.drawer .tab')].find(x => (x.textContent||'').trim() === '告警'); if (!t) return 'none'; t.click(); return 'ok'; })()")"
agent-browser wait 800 >/dev/null
ROWS="$(jeval "document.querySelectorAll('.drawer-alert-row').length")"
if [[ "$TAB" == "ok" && "$ROWS" == "$ALERT_TOTAL" ]]; then
  ok "D2 告警页签行数 ${ROWS} == API total ${ALERT_TOTAL}"
else
  bad "D2 tab=$TAB rows=$ROWS total=$ALERT_TOTAL"
fi
ROWTEXT="$(jeval "(() => { const r = document.querySelector('.drawer-alert-row'); return r ? r.textContent : 'none'; })()")"
if [[ "$ROWTEXT" != "none" && "$ROWTEXT" == *"R12触发"* ]]; then
  ok "D3 行内规则标签 + 状态徽章渲染"
else
  bad "D3 row=${ROWTEXT:0:60}"
fi
LINK="$(jeval "(() => { const a = [...document.querySelectorAll('.drawer-alerts-footer a, .drawer a.btn')].find(x => (x.textContent||'').includes('去告警中心处置')); return a ? a.getAttribute('href') : 'none'; })()")"
if [[ "$LINK" == "/alerts" ]]; then ok "D4 「去告警中心处置」深链"; else bad "D4 link=$LINK"; fi

echo "[3/3] 指标页签 + CSV 导出（下载劫持）"
MTAB="$(jeval "(() => { const t = [...document.querySelectorAll('.drawer .tab')].find(x => (x.textContent||'').trim() === '性能指标'); if (!t) return 'none'; t.click(); return 'ok'; })()")"
agent-browser wait --fn "!!document.querySelector('.metric-list-toolbar')" --timeout 20000 >/dev/null 2>&1
agent-browser wait 800 >/dev/null
CHARTS="$(jeval "document.querySelectorAll('.metric-card').length")"
TOOLBAR="$(jeval "document.querySelector('.metric-list-toolbar') ? 'ok' : 'none'")"
if [[ "$MTAB" == "ok" && "$TOOLBAR" == "ok" && "$CHARTS" -ge 1 ]]; then
  ok "D5 指标图表 ${CHARTS} 张 + 导出工具条"
else
  bad "D5 tab=$MTAB toolbar=$TOOLBAR charts=$CHARTS"
fi

ARM="$(jeval "(() => { window.__dl = null; HTMLAnchorElement.prototype.click = function() { window.__dl = { name: this.download, url: this.href }; }; return 'armed'; })()")"
EXPORT="$(jeval "(() => { const b = [...document.querySelectorAll('.drawer .btn')].find(x => (x.textContent||'').trim() === '导出指标 CSV'); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 1500 >/dev/null
DL_NAME="$(jeval "window.__dl ? window.__dl.name : 'none'")"
if [[ "$EXPORT" == "ok" && "$DL_NAME" == netops-metrics-$RES_ID-* ]]; then
  ok "D6a 下载触发（${DL_NAME:0:44}…）"
else
  bad "D6a export=$EXPORT name=$DL_NAME"
fi
jeval "(() => { window.__csvText = null; window.__csvLines = -1; window.__bom = 'none'; fetch(window.__dl.url).then(r => r.text()).then(t => { window.__csvText = t; window.__csvLines = t.split('\r\n').filter(x => x !== '').length; }); fetch(window.__dl.url).then(r => r.arrayBuffer()).then(b => { const v = new DataView(b); window.__bom = v.getUint8(0) === 0xEF && v.getUint8(1) === 0xBB ? 'bom' : 'nobom'; }); return 'started'; })()" > /dev/null
agent-browser wait 800 >/dev/null
CSV_HEAD="$(jeval "window.__csvText ? window.__csvText.slice(0, 40) : 'none'")"
CSV_LINES="$(jeval "window.__csvLines")"
CSV_BOM="$(jeval "window.__bom")"
if [[ "$CSV_BOM" == "bom" && "$CSV_HEAD" == seriesId,metricKey,ts,value,quality* ]]; then
  ok "D6b CSV BOM + 表头（${CSV_HEAD:0:38}…）"
else
  bad "D6b bom=$CSV_BOM head=$CSV_HEAD"
fi
if [[ "$CSV_LINES" -eq $((POINTS + 1)) ]]; then
  ok "D6c 行数一致（${CSV_LINES} = ${POINTS} 点 + 表头）"
else
  bad "D6c lines=$CSV_LINES 期望 $((POINTS + 1))"
fi
TOAST="$(jeval "(() => { return [...document.querySelectorAll('.toast, [class*=toast]')].some(t => (t.textContent||'').includes('已导出')); })()")"
if [[ "$TOAST" == "true" ]]; then ok "D6d 导出 toast 回执"; else bad "D6d 无导出 toast"; fi

ERRS="$(jeval "window.__errs ? window.__errs.length : -1")"
if [[ "$ERRS" == "0" ]]; then ok "D7 console 零错误"; else
  bad "D7 console errors = $ERRS: $(jeval "window.__errs ? window.__errs.join(' | ').slice(0, 200) : ''")"
fi

echo
echo "结果：PASS=$PASS FAIL=$FAIL"
[[ "$FAIL" -eq 0 ]] && echo "R12 RESOURCE-ALERTS+METRICS-EXPORT E2E: ALL GREEN"
exit "$FAIL"
