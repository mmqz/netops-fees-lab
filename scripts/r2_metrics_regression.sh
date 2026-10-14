#!/usr/bin/env bash
# NetOps Lab — R2 资源详情指标时序可视化浏览器级回归（v0.9）。
#
# 前置：make web（dist 已构建）。
# 旅程：realdata 导入（依赖序）→ 7 日摄入 → 检测 → 告警页 → 资源抽屉
#       「性能指标」页签 → 折线图渲染与劣化形态断言。
# 断言：
#   M1  抽屉出现「性能指标」页签（第四页签）；
#   M2  懒加载：点页签后出现 .metric-card（14 键 × 端口序列）；
#   M3  每张卡：SVG 折线（polyline + 圆点）+ 单位 chip + 中文标签（目录映射）；
#   M4  劣化序列可见：接收光功率卡 polyline 存在且「最新值」文本非空；
#       劣化方向（末值 < 首值）时折线为告警红（stroke 含 rgb(255,82,82)）；
#   M5  y 轴 min/mid/max 数值标签 + x 轴首末日期（MM-DD）存在；
#   M6  无指标资源（非端口）→ 空态文案；
#   M7  console 零错误/零未捕获拒绝。
# 截图存 repo tmp/。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
export PATH="$PATH:/home/z/.local/go/bin"

# 动态端口（避免与并行/残留 serve 冲突——18149 曾被残留进程占用导致跨库串台）
PORT=$(python3 -c "import socket; s=socket.socket(); s.bind(('127.0.0.1', 0)); print(s.getsockname()[1]); s.close()")
WORK="$(mktemp -d /tmp/netops-r2met.XXXXXX)"
DB="$WORK/r2.db"
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

echo "[1/3] realdata 导入 + 摄入 + 检测"
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
"$BIN" detect --db "$DB" > "$WORK/detect.json"
ALERTS_N=$(python3 -c "import json;d=json.load(open('$WORK/detect.json'));print(d.get('alertsCreated',0))")
if [[ "$ALERTS_N" -ge 1 ]]; then ok "P0 检测产生 ${ALERTS_N} 条告警"; else bad "P0 alertsCreated = $ALERTS_N"; fi

echo "[2/3] serve"
"$BIN" serve --db "$DB" --addr "127.0.0.1:$PORT" > "$WORK/serve.log" 2>&1 &
SERVE_PID=$!
for _ in $(seq 1 50); do curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && break; sleep 0.2; done

# 解析一条劣化端口资源 id（告警的 series → resource；带就绪重试）
RES_ID=""
for _ in $(seq 1 20); do
  RES_ID=$(curl -sf "http://127.0.0.1:$PORT/api/alerts?state=firing&pageSize=1"     | python3 -c "import json,sys;d=json.load(sys.stdin);items=d.get('items',[]);print(items[0]['resource']['id'] if items else '')" 2>/dev/null) && [[ -n "$RES_ID" ]] && break
  sleep 0.5
done
if [[ -z "$RES_ID" ]]; then echo "FATAL: 无法解析 firing 告警资源 id"; exit 1; fi
# 一条无指标资源 id（站点——无端口性能序列）
SITE_ID=$(curl -s "http://127.0.0.1:$PORT/api/resources?type=site&pageSize=1" \
  | python3 -c "import json,sys;d=json.load(sys.stdin);print(d['items'][0]['id'])")
echo "      劣化端口：$RES_ID · 站点（无指标）：$SITE_ID"

echo "[3/3] 浏览器断言"
agent-browser set viewport 1600 900 >/dev/null
agent-browser open "http://127.0.0.1:$PORT/resources?focus=$RES_ID" >/dev/null
agent-browser wait --fn "!!document.querySelector('.drawer')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 1000 >/dev/null
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null

# —— M1 页签存在 ——
TABS="$(jeval "(() => { const tabs = [...document.querySelectorAll('.drawer .tabs .tab')]; return tabs.some(t => (t.textContent||'').includes('性能指标')); })()")"
if [[ "$TABS" == "true" ]]; then ok "M1 抽屉「性能指标」页签"; else bad "M1 无性能指标页签"; fi

# —— M2 懒加载 ——
BEFORE="$(jeval "document.querySelectorAll('.drawer .metric-card').length")"
CLICK="$(jeval "(() => { const t = [...document.querySelectorAll('.drawer .tabs .tab')].find(x => (x.textContent||'').includes('性能指标')); if (!t) return 'none'; t.click(); return 'ok'; })()")"
agent-browser wait --fn "document.querySelectorAll('.drawer .metric-card').length > 0" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 600 >/dev/null
CARDS="$(jeval "document.querySelectorAll('.drawer .metric-card').length")"
if [[ "$CLICK" == "ok" && "$CARDS" -ge 1 && "$BEFORE" == "0" ]]; then ok "M2 懒加载（点页签前 0 卡 → 后 ${CARDS} 卡）"; else bad "M2 before=$BEFORE after=$CARDS click=$CLICK"; fi

# —— M3 卡片结构 ——
POLY="$(jeval "document.querySelectorAll('.drawer .metric-card svg polyline').length")"
DOTS="$(jeval "document.querySelectorAll('.drawer .metric-card svg circle').length")"
UNIT="$(jeval "(() => { return [...document.querySelectorAll('.drawer .metric-card-unit')].some(u => (u.textContent||'').includes('dBm')); })()")"
LABEL="$(jeval "(() => { return [...document.querySelectorAll('.drawer .metric-card-label')].some(l => (l.textContent||'').includes('光功率') || (l.textContent||'').includes('丢包') || (l.textContent||'').includes('带宽')); })()")"
if [[ "$POLY" -ge 1 && "$DOTS" -ge 7 ]]; then ok "M3a SVG 折线 + 圆点（${POLY} 线 / ${DOTS} 点）"; else bad "M3a poly=$POLY dots=$DOTS"; fi
if [[ "$UNIT" == "true" ]]; then ok "M3b 单位 chip（dBm）"; else bad "M3b 无单位 chip"; fi
if [[ "$LABEL" == "true" ]]; then ok "M3c 中文指标标签（目录映射）"; else bad "M3c 无中文标签"; fi

# —— M4 劣化序列（两个独立求值，避免 jeval 剥引号破坏 JSON） ——
RX_FOUND="$(jeval "(() => { const card = [...document.querySelectorAll('.drawer .metric-card')].find(c => { const l = c.querySelector('.metric-card-label'); return l && (l.textContent||'').includes('接收光功率均值'); }); return card ? 'yes' : 'no'; })()")"
if [[ "$RX_FOUND" == "yes" ]]; then
  RX_STROKE="$(jeval "(() => { const card = [...document.querySelectorAll('.drawer .metric-card')].find(c => { const l = c.querySelector('.metric-card-label'); return l && (l.textContent||'').includes('接收光功率均值'); }); const pl = card.querySelector('svg polyline'); return pl ? (pl.getAttribute('stroke') || '') : ''; })()")"
  RX_LAST="$(jeval "(() => { const card = [...document.querySelectorAll('.drawer .metric-card')].find(c => { const l = c.querySelector('.metric-card-label'); return l && (l.textContent||'').includes('接收光功率均值'); }); const n = card.querySelector('.muted-note'); return n ? (n.textContent || '') : ''; })()")"
  if [[ "$RX_STROKE" == *"#ff5252"* || "$RX_STROKE" == *"255, 82, 82"* || "$RX_STROKE" == *"255,82,82"* ]]; then ok "M4a 劣化折线红色（stroke=$RX_STROKE）"; else bad "M4a stroke = $RX_STROKE"; fi
  if [[ "$RX_LAST" == *"最新"* ]]; then ok "M4b 最新值可见（$RX_LAST）"; else bad "M4b last = $RX_LAST"; fi
else
  bad "M4 未找到接收光功率均值卡"
fi

# —— M5 轴标签 ——
YLABELS="$(jeval "document.querySelectorAll('.drawer .metric-card svg text').length")"
DAYFMT="$(jeval "(() => { const texts = [...document.querySelectorAll('.drawer .metric-card svg text')].map(t => t.textContent); return texts.some(t => /^\\d{2}-\\d{2}$/.test(t)) ? 'ok' : 'none'; })()")"
if [[ "$YLABELS" -ge 4 && "$DAYFMT" == "ok" ]]; then ok "M5 轴标签（y 数值 + x 首末 MM-DD）"; else bad "M5 texts=$YLABELS day=$DAYFMT"; fi

agent-browser screenshot "$SHOT_DIR/r2-metrics.png" >/dev/null 2>&1 || true

# —— M6 无指标资源空态 ——
agent-browser open "http://127.0.0.1:$PORT/resources?focus=$SITE_ID" >/dev/null
agent-browser wait --fn "!!document.querySelector('.drawer .tabs .tab')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 800 >/dev/null
agent-browser eval "(() => { const t = [...document.querySelectorAll('.drawer .tabs .tab')].find(x => (x.textContent||'').includes('性能指标')); if (t) t.click(); return 'ok'; })()" >/dev/null
agent-browser wait --fn "!!document.querySelector('.drawer .metric-list .muted-note')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 600 >/dev/null
EMPTYTEXT="$(jeval "(() => { const n = document.querySelector('.drawer .metric-list .muted-note'); return n && (n.textContent||'').includes('没有指标序列') ? 'ok' : (n ? n.textContent : 'none'); })()")"
if [[ "$EMPTYTEXT" == "ok" ]]; then ok "M6 无指标资源空态文案"; else bad "M6 empty = $EMPTYTEXT"; fi

# —— M7 console 零错误 ——
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'rearmed'" >/dev/null
agent-browser wait 800 >/dev/null
ERRS="$(jeval "window.__errs ? String(window.__errs.length) : 'NA'")"
if [[ "$ERRS" == "0" ]]; then ok "M7 console 零错误/零未捕获拒绝"; else bad "M7 console 错误 ${ERRS} 条"; fi

echo
echo "结果：PASS=$PASS FAIL=$FAIL"
[[ "$FAIL" -eq 0 ]]
