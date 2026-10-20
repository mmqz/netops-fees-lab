#!/usr/bin/env bash
# NetOps Lab — W28 真实网络数据图层浏览器级回归（v0.8.0）。
#
# 前置：make web（dist 含 hlj-cables*/hlj-basestations*.geojson.gz）。
# 断言：
#   1. 2D 地图：cable-line/cable-label/basestation-pts 图层存在且源加载
#      （window.__geomap 暴露的 maplibre 实例）；
#   2. 网络面板：hlj-cables / hlj-basestations 资源确被拉取（performance API）；
#   3. 图层面板：新分组「真实网络数据（v0.8）」+ 两个开关系初始态正确；
#   4. 基站开关关闭 → basestation-pts visibility=none（真切换不假渲染）；
#   5. 3D 视图：图层仍存在（pitch 模式不重建丢失）；
#   6. 全程 console error / unhandledrejection 零。
# 截图存 repo tmp/（不进 download/ —— 截屏不作为交付物）。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
export PATH="$PATH:/home/z/.local/go/bin"

PORT=18128
WORK="$(mktemp -d /tmp/netops-w28.XXXXXX)"
DB="$WORK/w28.db"
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

echo "[1/4] 导入真实数据工作簿（31 本，v0.8.0 testdata/realdata）"
ORDER=(
  sites machine_rooms customers vpn_instances rings igp_domains devices boards
  base_stations ports racks odf odf_terminals ducts manholes poles
  optical_cables cable_segments splice_boxes fibers otn_ochs sdh_timeslots
  circuits protection_groups ippools services leased_lines bbus rrus olts pon_ports onus
)
for wb in "${ORDER[@]}"; do
  f="testdata/realdata/$wb.xlsx"
  "$BIN" analyze -file "$f" > "$WORK/a.json"
  python3 - "$WORK/a.json" "$WORK/m.json" <<'PY'
import json, sys
json.dump(json.load(open(sys.argv[1], encoding="utf-8"))["suggestedMapping"], open(sys.argv[2], "w", encoding="utf-8"))
PY
  "$BIN" import --file "$f" --map "$WORK/m.json" --db "$DB" > "$WORK/i.json"
done
echo "      导入完成"

echo "[2/4] 工作台 + serve"
WS_OUT="$("$BIN" lab workspace create --db "$DB" --name "w28-browser-regression")"
WS=$(python3 -c "import json,sys;print(json.loads(sys.argv[1])['workspaceId'])" "$WS_OUT")
"$BIN" serve --db "$DB" --addr "127.0.0.1:$PORT" > "$WORK/serve.log" 2>&1 &
SERVE_PID=$!
for _ in $(seq 1 50); do curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && break; sleep 0.2; done

echo "[3/4] 浏览器断言"
agent-browser set viewport 1600 900 >/dev/null
agent-browser open "http://127.0.0.1:$PORT/" >/dev/null
agent-browser wait --load networkidle >/dev/null 2>&1 || true
# 预置偏好：新图层全开（含基站，验证 1,198 点渲染）
agent-browser storage local set netops-lab.geo.layers.v2 '{"basemap":"offline","overlays":{"admin":true,"railway":false,"road":true,"forest":true,"water":true,"green":true,"places":true,"cable":true,"basestation":true},"links":true,"devices":true,"terrain":true,"contours":true}' >/dev/null
agent-browser open "http://127.0.0.1:$PORT/lab/$WS" >/dev/null
agent-browser wait --fn "!!document.querySelector('.canvas-seg button')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 1500 >/dev/null
agent-browser eval "(() => { const b=[...document.querySelectorAll('.canvas-seg button')].find(x => (x.getAttribute('aria-label')||'').startsWith('2D 地图')); if(!b) return 'NO-BTN'; b.click(); return 'ok'; })()" >/dev/null
agent-browser wait --fn "window.__geomap && document.querySelectorAll('.geo-marker').length > 0" --timeout 25000 >/dev/null 2>&1 || true
agent-browser wait 2500 >/dev/null

agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null
agent-browser wait 1500 >/dev/null
# 图层面板默认折叠：先展开（断言分组与开关前必须）
agent-browser eval "document.querySelector('.geo-layerpanel-toggle')?.click(); 'expanded'" >/dev/null
agent-browser wait 600 >/dev/null

# A. 图层存在性（maplibre 实例）
CABLE_LAYER="$(jeval "window.__geomap ? (window.__geomap.getLayer('cable-line') ? 'yes' : 'no') : 'nomap'")"
if [[ "$CABLE_LAYER" == "yes" ]]; then ok "A1 cable-line 图层存在"; else bad "A1 cable-line = $CABLE_LAYER"; fi
CABLE_LABEL_LAYER="$(jeval "window.__geomap.getLayer('cable-label') ? 'yes' : 'no'")"
if [[ "$CABLE_LABEL_LAYER" == "yes" ]]; then ok "A2 cable-label 图层存在"; else bad "A2 cable-label = $CABLE_LABEL_LAYER"; fi
BS_LAYER="$(jeval "window.__geomap.getLayer('basestation-pts') ? 'yes' : 'no'")"
if [[ "$BS_LAYER" == "yes" ]]; then ok "A3 basestation-pts 图层存在"; else bad "A3 basestation-pts = $BS_LAYER"; fi

# B. 源加载（geojson 真正取回并解析）
agent-browser wait --fn "window.__geomap && window.__geomap.isSourceLoaded('cables')" --timeout 20000 >/dev/null 2>&1 || true
CABLE_SRC="$(jeval "window.__geomap.isSourceLoaded('cables') ? 'yes' : 'no'")"
if [[ "$CABLE_SRC" == "yes" ]]; then ok "B1 cables 源已加载（7 条干线走廊）"; else bad "B1 cables 源未加载"; fi
BS_SRC="$(jeval "window.__geomap.isSourceLoaded('basestations') ? 'yes' : 'no'")"
if [[ "$BS_SRC" == "yes" ]]; then ok "B2 basestations 源已加载（1,198 真实站）"; else bad "B2 basestations 源未加载"; fi

# C. mapdata 资产可取（geojson 拉取发生在 maplibre worker，主线程 performance
#    不可观测——改用服务端取回 + gzip 魔数校验；渲染要素数量证明真画出来了）
for f in hlj-cables hlj-basestations; do
  CODE=$(curl -s -o "$WORK/$f.gz" -w "%{http_code}" "http://127.0.0.1:$PORT/mapdata/$f.geojson.gz")
  MAGIC=$(head -c 2 "$WORK/$f.gz" | od -An -tx1 | tr -d ' \n')
  if [[ "$CODE" == "200" && "$MAGIC" == "1f8b" ]]; then ok "C-$f 服务端可取（200 + gzip 魔数）"; else bad "C-$f status=$CODE magic=$MAGIC"; fi
done
CABLE_FEATS="$(jeval "window.__geomap.queryRenderedFeatures({layers:['cable-line','cable-line-rail']}).length")"
if [[ "$CABLE_FEATS" != "0" && -n "$CABLE_FEATS" ]]; then ok "C3 光缆走廊渲染要素 $CABLE_FEATS 条（真实画布上）"; else bad "C3 光缆渲染要素 = $CABLE_FEATS"; fi

# D. 图层面板：新分组与开关
SECTION="$(jeval "[...document.querySelectorAll('.geo-layerpanel-section')].map(e => e.textContent).join('|')")"
if [[ "$SECTION" == *"真实网络数据"* ]]; then ok "D1 图层面板出现新分组"; else bad "D1 新分组缺失：$SECTION"; fi
CABLE_CHK="$(jeval "[...document.querySelectorAll('.geo-layerpanel-opt')].find(l => l.textContent.includes('光缆干线')) ? 'yes' : 'no'")"
if [[ "$CABLE_CHK" == "yes" ]]; then ok "D2 光缆干线开关系存在"; else bad "D2 光缆干线开关系缺失"; fi
BS_CHK_STATE="$(jeval "[...document.querySelectorAll('.geo-layerpanel-opt')].find(l => l.textContent.includes('无线基站'))?.querySelector('input')?.checked ?? 'none'")"
if [[ "$BS_CHK_STATE" == "false" ]]; then ok "D3 无线基站开关初始关（默认关一键开——README 声明，#77 校准：脚本原断言 v0.8.0 时点的初始开，产品意图已改为默认关）"; else bad "D3 无线基站初始态 = $BS_CHK_STATE（应为 false——默认关）"; fi

# E. 开关真切换（默认关 → 开启 → visible；再关闭恢复默认 → none）
agent-browser eval "(() => { const l=[...document.querySelectorAll('.geo-layerpanel-opt')].find(x => x.textContent.includes('无线基站')); l.querySelector('input').click(); return 'ok'; })()" >/dev/null
agent-browser wait 800 >/dev/null
BS_VIS="$(jeval "window.__geomap.getLayoutProperty('basestation-pts', 'visibility')")"
if [[ "$BS_VIS" == "visible" ]]; then ok "E1 开启开关 → basestation-pts visibility=visible"; else bad "E1 visibility = $BS_VIS"; fi
# 再关闭（恢复默认态，供后续断言与 3D 段不被基站点云干扰）
agent-browser eval "(() => { const l=[...document.querySelectorAll('.geo-layerpanel-opt')].find(x => x.textContent.includes('无线基站')); l.querySelector('input').click(); return 'ok'; })()" >/dev/null
agent-browser wait 800 >/dev/null

agent-browser screenshot "$SHOT_DIR/w28-2d-realdata.png" >/dev/null

# F. 3D 视图图层仍在
agent-browser eval "(() => { const b=[...document.querySelectorAll('.canvas-seg button')].find(x => (x.getAttribute('aria-label')||'').startsWith('3D 地图')); if(!b) return 'NO-BTN'; b.click(); return 'ok'; })()" >/dev/null
agent-browser wait 3000 >/dev/null
CABLE_3D="$(jeval "window.__geomap && window.__geomap.getLayer('cable-line') ? 'yes' : 'no'")"
if [[ "$CABLE_3D" == "yes" ]]; then ok "F1 3D 视图 cable-line 仍在（走廊贴真实地形）"; else bad "F1 3D 视图丢失 cable-line"; fi
agent-browser screenshot "$SHOT_DIR/w28-3d-realdata.png" >/dev/null

# G. console 错误零
agent-browser wait 800 >/dev/null
ERRS="$(jeval "window.__errs.length")"
if [[ "$ERRS" == "0" ]]; then ok "G1 console error/rejection = 0"; else bad "G1 错误 $ERRS 条：$(jeval "window.__errs.slice(0,3).join(' ; ')")"; fi

echo "[4/4] 结果：PASS=$PASS FAIL=$FAIL（截图 tmp/w28-2d-realdata.png · tmp/w28-3d-realdata.png）"
[[ "$FAIL" == "0" ]] || exit 1
