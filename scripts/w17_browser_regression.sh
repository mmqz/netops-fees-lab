#!/usr/bin/env bash
# NetOps Lab — W17 浏览器级回归：2D 聚簇（「锁在一起」修复）+ 离线 DEM 地形。
#
# 前置：dist 已构建（含 web/public/mapdata/dem 瓦片 + hlj-contours*.geojson.gz）。
# 本脚本：
#   0. 构建 netopslab 二进制
#   1. testdata -geo → 导入 geodevices/geocircuits → 物化地理工作台
#   2. serve（18099）→ agent-browser（1600×900）断言：
#      A. 2D 初始视野：≥2 个聚簇（哈尔滨 3 台 + 大兴安岭 2 台同城扎堆）
#         且可见单例地标数 < 总设备数（标签不再糊成黑块）
#      B. 点击聚簇 → 相机放大、簇展开为单例地标（一次到位，cameraForBounds）
#      C. 3D 视图：getTerrain() 绑定离线 dem 源；hillshade 图层存在且可见；
#         夸张系数随 zoom 分档自适应（低 zoom 远观档 ≠ 近观档）
#      D. /mapdata/dem/*.png 同源可取（本地瓦片不受外网封禁影响）；
#         等高线资产（WGS84 + GCJ 变体）同源可取
#      E. 图层面板「地形高程」开关 → terrain 卸载 / hillshade 隐藏；
#         「等高线」开关 → contours-* 图层隐藏
#      G. 相机独立性（#12）：2D 平移 → 切 3D → 平移 3D → 切回 2D，
#         2D 相机恢复（center/zoom 回到离开时快照，不再锁联）
#      H. 海拔读数（#13）：大兴安岭坐标派发 mousemove → .geo-elev chip
#         显示非 — 海拔值
#      F. 全程零页面错误
#   截图存 repo tmp/（不进 download/ —— 用户要求截屏不作为交付物）。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
export PATH="$PATH:/home/z/.local/go/bin"

PORT=18099
WORK="$(mktemp -d /tmp/netops-w17.XXXXXX)"
DB="$WORK/geo.db"
SHOT_DIR="$REPO_ROOT/tmp"
mkdir -p "$SHOT_DIR"

PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "  PASS  $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  FAIL  $1"; }
# eval 返回 JSON 字面量（带引号），剥引号后再比较
jeval() { agent-browser eval "$1" 2>/dev/null | tr -d '"'; }

cleanup() {
  [[ -n "${SERVE_PID:-}" ]] && kill "$SERVE_PID" 2>/dev/null || true
  agent-browser close >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

echo "[0/4] 构建二进制（内嵌 dist 含 DEM 瓦片）"
go build -o "$WORK/netopslab" ./cmd/netopslab

echo "[1/4] 准备地理工作台数据"
if [[ ! -f testdata/geodevices.xlsx || ! -f testdata/geocircuits.xlsx ]]; then
  go run ./cmd/testdatagen -out testdata -geo > /dev/null
fi
for wb in geodevices geocircuits; do
  analysis="$($WORK/netopslab analyze --file "testdata/$wb.xlsx")"
  python3 -c "import json,sys; print(json.dumps(json.loads(sys.argv[1])['suggestedMapping']))" "$analysis" > "$WORK/map-$wb.json"
  $WORK/netopslab import --db "$DB" --file "testdata/$wb.xlsx" --map "$WORK/map-$wb.json" > /dev/null
done
WS_JSON="$($WORK/netopslab lab workspace create --db "$DB" --name w17-regression --all-batches)"
WS="$(python3 -c "import json,sys; print(json.loads(sys.argv[1])['workspaceId'])" "$WS_JSON")"
echo "  workspace: $WS"

echo "[2/4] serve :$PORT"
$WORK/netopslab serve --db "$DB" --addr ":$PORT" > "$WORK/serve.log" 2>&1 &
SERVE_PID=$!
for i in $(seq 1 30); do
  curl -sf "http://127.0.0.1:$PORT/api/health" > /dev/null 2>&1 && break
  sleep 0.3
done
curl -sf "http://127.0.0.1:$PORT/api/health" > /dev/null || { echo "serve 未就绪"; exit 1; }

echo "[3/4] 浏览器断言"
agent-browser set viewport 1600 900 >/dev/null
agent-browser open "http://127.0.0.1:$PORT/" >/dev/null
agent-browser wait --load networkidle >/dev/null 2>&1 || true
# 预置离线底图偏好：沙箱页面世界封禁 autonavi，避免 4s 看门狗降级的等待与噪声
agent-browser storage local set netops-lab.geo.layers '{"basemap":"offline","overlays":{"admin":true,"railway":false,"road":false},"links":true,"devices":true,"terrain":true}' >/dev/null
agent-browser open "http://127.0.0.1:$PORT/lab/$WS" >/dev/null
# 工具带就绪门：首次运行浏览器冷启动慢，无条件等待会点到不存在的按钮
agent-browser wait --fn "!!document.querySelector('.canvas-seg button')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 1500 >/dev/null

# 切到 2D 地图视图（工具带第一分段的第 2 个按钮）
agent-browser eval "(() => { const b=[...document.querySelectorAll('.canvas-seg button')].find(x => (x.getAttribute('aria-label')||'').startsWith('2D 地图')); if(!b) return 'NO-BTN'; b.click(); return 'clicked'; })()" >/dev/null
# 地图就绪门：__geomap 挂载且设备地标已建
agent-browser wait --fn "window.__geomap && document.querySelectorAll('.geo-marker').length > 0" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 2000 >/dev/null

# —— A. 初始聚簇 ——
CLUSTERS="$(agent-browser eval "document.querySelectorAll('.geo-cluster').length")"
VISIBLE_MARKERS="$(agent-browser eval "document.querySelectorAll('.geo-marker:not(.geo-marker--clustered)').length")"
TOTAL_MARKERS="$(agent-browser eval "document.querySelectorAll('.geo-marker').length")"
if [[ "$CLUSTERS" -ge 2 ]]; then ok "A1 初始视野聚簇数 = $CLUSTERS（≥2）"; else bad "A1 初始视野聚簇数 = $CLUSTERS（<2，聚簇失效）"; fi
if [[ "$VISIBLE_MARKERS" -lt "$TOTAL_MARKERS" && "$VISIBLE_MARKERS" -gt 0 ]]; then
  ok "A2 可见单例 $VISIBLE_MARKERS / 总设备 $TOTAL_MARKERS（扎堆设备收进簇，标签不再叠成黑块）"
else
  bad "A2 可见单例 $VISIBLE_MARKERS / 总设备 $TOTAL_MARKERS"
fi
# 等高线图层加载与默认可见（#13）
agent-browser wait --fn "window.__geomap && window.__geomap.isSourceLoaded('contours')" --timeout 20000 >/dev/null 2>&1 || true
CONTOURS_VIS="$(jeval "window.__geomap && window.__geomap.getLayer('contours-major') ? String(window.__geomap.getLayoutProperty('contours-major','visibility')) : 'NO-LAYER'")"
CONTOUR_LOADED="$(jeval "window.__geomap ? String(window.__geomap.isSourceLoaded('contours')) : 'false'")"
CONTOUR_FEATS="$(jeval "(() => { const s = window.__geomap && window.__geomap.getSource('contours'); const d = s && s._data; return d && d.features ? d.features.length : 0; })()")"
if [[ "$CONTOURS_VIS" == "visible" ]]; then ok "A3 等高线图层默认可见"; else bad "A3 contours-major = $CONTOURS_VIS"; fi
if [[ "$CONTOUR_LOADED" == "true" ]]; then ok "A4 等高线源加载完成（$CONTOUR_FEATS 条折线）"; else bad "A4 等高线源未加载（$CONTOUR_FEATS 条）"; fi
ZOOM_BEFORE="$(jeval "window.__geomap ? window.__geomap.getZoom().toFixed(2) : 'NO-MAP'")"
agent-browser screenshot "$SHOT_DIR/w17-2d-clusters.png" >/dev/null

# —— B. 点击聚簇展开 ——
agent-browser eval "document.querySelector('.geo-cluster')?.click() || 'NO-CLUSTER'" >/dev/null
agent-browser wait 1500 >/dev/null
ZOOM_AFTER="$(jeval "window.__geomap.getZoom().toFixed(2)")"
CLUSTERS_AFTER="$(agent-browser eval "document.querySelectorAll('.geo-cluster').length")"
if python3 -c "import sys; sys.exit(0 if float('$ZOOM_AFTER') > float('$ZOOM_BEFORE') + 0.5 else 1)"; then
  ok "B1 点击聚簇 zoom $ZOOM_BEFORE → $ZOOM_AFTER（放大展开）"
else
  bad "B1 点击聚簇 zoom $ZOOM_BEFORE → $ZOOM_AFTER（未放大）"
fi
if [[ "$CLUSTERS_AFTER" -lt "$CLUSTERS" ]]; then
  ok "B2 聚簇数 $CLUSTERS → $CLUSTERS_AFTER（簇随放大拆分）"
else
  bad "B2 聚簇数 $CLUSTERS → $CLUSTERS_AFTER（未拆分）"
fi

# —— C. 3D 视图：离线 DEM 地形（含自适应夸张 #13）——
agent-browser eval "(() => { const b=[...document.querySelectorAll('.canvas-seg button')].find(x => (x.getAttribute('aria-label')||'').startsWith('3D 地图')); b?.click(); return 'ok'; })()" >/dev/null
agent-browser wait --fn "window.__geomap && window.__geomap.getPitch() > 40" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 2500 >/dev/null
TERRAIN="$(jeval "window.__geomap && window.__geomap.getTerrain() ? window.__geomap.getTerrain().source : 'NULL'")"
HILLSHADE="$(jeval "window.__geomap && window.__geomap.getLayer('hillshade') ? String(window.__geomap.getLayoutProperty('hillshade','visibility')) : 'NO-LAYER'")"
EXAG_FAR="$(jeval "window.__geomap && window.__geomap.getTerrain() ? String(window.__geomap.getTerrain().exaggeration) : '0'")"
if [[ "$TERRAIN" == "dem" ]]; then ok "C1 3D terrain 绑定离线 dem 源"; else bad "C1 3D terrain = $TERRAIN（应为 dem）"; fi
if [[ "$HILLSHADE" == "visible" ]]; then ok "C2 hillshade 图层可见"; else bad "C2 hillshade = $HILLSHADE"; fi
if python3 -c "import sys; sys.exit(0 if float('$EXAG_FAR') >= 2.5 else 1)"; then
  ok "C3 远观档夸张 $EXAG_FAR（≥2.5，整省起伏可见）"
else
  bad "C3 远观档夸张 $EXAG_FAR（<2.5，整省视觉仍平）"
fi
agent-browser screenshot "$SHOT_DIR/w17-3d-terrain.png" >/dev/null

# —— C4. 近观档（zoom 放大后夸张回落）——
agent-browser eval "(() => { window.__geomap.setZoom(9.2); return 'ok'; })()" >/dev/null
agent-browser wait 1200 >/dev/null
EXAG_NEAR="$(jeval "window.__geomap && window.__geomap.getTerrain() ? String(window.__geomap.getTerrain().exaggeration) : '0'")"
if python3 -c "import sys; sys.exit(0 if float('$EXAG_NEAR') <= 1.5 else 1)"; then
  ok "C4 近观档夸张 $EXAG_NEAR（≤1.5，回到真实比例）"
else
  bad "C4 近观档夸张 $EXAG_NEAR（>1.5，未自适应）"
fi

# —— D. DEM 瓦片 + 等高线资产本地分发 ——
DEM_HTTP="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/mapdata/dem/5/27/10.png")"
DEM_TYPE="$(curl -s -o /dev/null -w '%{content_type}' "http://127.0.0.1:$PORT/mapdata/dem/5/27/10.png")"
if [[ "$DEM_HTTP" == "200" && "$DEM_TYPE" == "image/png" ]]; then ok "D1 /mapdata/dem 瓦片 200 image/png"; else bad "D1 DEM 瓦片 $DEM_HTTP $DEM_TYPE"; fi
DEM_REQS="$(agent-browser network requests --filter 'mapdata/dem' 2>/dev/null | grep -c 'dem/' || true)"
if [[ "$DEM_REQS" -gt 0 ]]; then ok "D2 页面实际拉取 DEM 瓦片请求数 = $DEM_REQS"; else bad "D2 页面未请求任何 DEM 瓦片"; fi
CONTOUR_HTTP="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/mapdata/hlj-contours.geojson.gz")"
CONTOUR_GCJ_HTTP="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/mapdata/hlj-contours-gcj.geojson.gz")"
if [[ "$CONTOUR_HTTP" == "200" && "$CONTOUR_GCJ_HTTP" == "200" ]]; then ok "D3 等高线资产 WGS84/GCJ 双变体 200"; else bad "D3 等高线资产 $CONTOUR_HTTP/$CONTOUR_GCJ_HTTP"; fi

# —— E. 图层面板地形/等高线开关 ——
agent-browser eval "document.querySelector('.geo-layerpanel-toggle')?.click() || 'NO-PANEL'" >/dev/null
agent-browser wait 400 >/dev/null
agent-browser eval "(() => { const labels=[...document.querySelectorAll('.geo-layerpanel-opt')]; const row=labels.find(l => l.textContent.includes('地形高程')); const cb=row?.querySelector('input[type=checkbox]'); if(!cb) return 'NO-CB'; cb.click(); return 'toggled'; })()" >/dev/null
agent-browser wait 1200 >/dev/null
TERRAIN_OFF="$(jeval "window.__geomap && window.__geomap.getTerrain() === null ? 'NULL' : (window.__geomap.getTerrain() ? 'ON' : 'NULL')")"
HILLSHADE_OFF="$(jeval "String(window.__geomap.getLayoutProperty('hillshade','visibility'))")"
if [[ "$TERRAIN_OFF" == "NULL" ]]; then ok "E1 关闭地形后 terrain 卸载"; else bad "E1 terrain 关闭后仍 $TERRAIN_OFF"; fi
if [[ "$HILLSHADE_OFF" == "none" ]]; then ok "E2 hillshade 随开关隐藏"; else bad "E2 hillshade = $HILLSHADE_OFF"; fi
agent-browser eval "(() => { const labels=[...document.querySelectorAll('.geo-layerpanel-opt')]; const row=labels.find(l => l.textContent.includes('等高线')); const cb=row?.querySelector('input[type=checkbox]'); if(!cb) return 'NO-CB'; cb.click(); return 'toggled'; })()" >/dev/null
agent-browser wait 800 >/dev/null
CONTOURS_OFF="$(jeval "String(window.__geomap.getLayoutProperty('contours-major','visibility'))")"
if [[ "$CONTOURS_OFF" == "none" ]]; then ok "E3 等高线开关隐藏 contours-*"; else bad "E3 contours-major = $CONTOURS_OFF"; fi
agent-browser eval "(() => { const labels=[...document.querySelectorAll('.geo-layerpanel-opt')]; const row=labels.find(l => l.textContent.includes('等高线')); const cb=row?.querySelector('input[type=checkbox]'); if(!cb) return 'NO-CB'; cb.click(); return 'restored'; })()" >/dev/null
agent-browser wait 400 >/dev/null

# —— G. 相机独立性（#12：2D/3D 解锁）——
switch_view() {
  agent-browser eval "(() => { const b=[...document.querySelectorAll('.canvas-seg button')].find(x => (x.getAttribute('aria-label')||'').startsWith('$1')); b?.click(); return 'ok'; })()" >/dev/null
  agent-browser wait 2600 >/dev/null
}
switch_view '2D 地图'
# 2D 相机 → 哈尔滨近观 + 自定义 bearing 20°（旧版切回会强制归零，新版应恢复）
agent-browser eval "(() => { window.__geomap.jumpTo({center:[126.63, 45.75], zoom: 9.4, bearing: 20, pitch: 0}); return 'ok'; })()" >/dev/null
agent-browser wait 400 >/dev/null
CAM2D="$(jeval "window.__geomap.getCenter().lng.toFixed(3) + ',' + window.__geomap.getCenter().lat.toFixed(3) + ',' + window.__geomap.getZoom().toFixed(2) + ',' + window.__geomap.getBearing().toFixed(0)")"
switch_view '3D 地图'
agent-browser eval "(() => { window.__geomap.jumpTo({center:[124.7, 52.38], zoom: 7.5}); return 'ok'; })()" >/dev/null
agent-browser wait 400 >/dev/null
CAM3D="$(jeval "window.__geomap.getCenter().lng.toFixed(3) + ',' + window.__geomap.getCenter().lat.toFixed(3) + ',' + window.__geomap.getZoom().toFixed(2)")"
if [[ "$CAM3D" == "124.700,52.380,7.50" ]]; then ok "G1 3D 会话相机独立设置（$CAM3D）"; else bad "G1 3D 相机 = $CAM3D（预期 124.700,52.380,7.50）"; fi
switch_view '2D 地图'
CAM2D_BACK="$(jeval "window.__geomap.getCenter().lng.toFixed(3) + ',' + window.__geomap.getCenter().lat.toFixed(3) + ',' + window.__geomap.getZoom().toFixed(2) + ',' + window.__geomap.getBearing().toFixed(0)")"
if [[ "$CAM2D_BACK" == "$CAM2D" ]]; then
  ok "G2 2D 相机恢复 $CAM2D_BACK（含自定义 bearing，解锁生效）"
else
  bad "G2 2D 相机 $CAM2D_BACK ≠ 离开时 $CAM2D（仍锁联）"
fi

# —— H. 光标海拔读数（#13）——
# 大兴安岭主脉：必为正海拔；再跳覆盖外验证 — 降级
agent-browser eval "(() => { window.__geomap.jumpTo({center:[124.1, 51.5], zoom: 7}); return 'ok'; })()" >/dev/null
agent-browser wait 600 >/dev/null
agent-browser eval "(() => { const cv=document.querySelector('.geo-map canvas'); if(!cv) return 'NO-CV'; const r=cv.getBoundingClientRect(); cv.dispatchEvent(new MouseEvent('mousemove', {clientX: r.left + r.width/2, clientY: r.top + r.height/2, bubbles: true})); return 'moved'; })()" >/dev/null
agent-browser wait 1500 >/dev/null
ELEV_TEXT="$(agent-browser eval "document.querySelector('.geo-elev') ? document.querySelector('.geo-elev').textContent : 'NO-CHIP'")"
ELEV_NUM="$(jeval "(() => { const m = String(document.querySelector('.geo-elev')?.textContent || '').match(/-?\\d+/); return m ? m[0] : 'NaN'; })()")"
if [[ "$ELEV_NUM" != "NaN" ]] && python3 -c "import sys; sys.exit(0 if float('$ELEV_NUM') >= 100 else 1)"; then
  ok "H1 大兴安岭光标海拔读数 = $ELEV_TEXT（≥100m）"
else
  bad "H1 海拔读数 = $ELEV_TEXT（应为 ≥100m 数值）"
fi
agent-browser eval "(() => { window.__geomap.jumpTo({center:[118.5, 40.0], zoom: 5}); return 'ok'; })()" >/dev/null
agent-browser wait 600 >/dev/null
agent-browser eval "(() => { const cv=document.querySelector('.geo-map canvas'); const r=cv.getBoundingClientRect(); cv.dispatchEvent(new MouseEvent('mousemove', {clientX: r.left + r.width/2, clientY: r.top + r.height/2, bubbles: true})); return 'moved'; })()" >/dev/null
agent-browser wait 1500 >/dev/null
ELEV_OUT="$(agent-browser eval "document.querySelector('.geo-elev') ? document.querySelector('.geo-elev').textContent : 'NO-CHIP'")"
if [[ "$ELEV_OUT" == *"超出高程覆盖"* ]]; then ok "H2 覆盖外降级 —（$ELEV_OUT）"; else bad "H2 覆盖外读数 = $ELEV_OUT"; fi

# —— F. 页面错误 ——
PERR="$(agent-browser errors 2>/dev/null | grep -cv '^$' || true)"
if [[ "$PERR" == "0" ]]; then ok "F1 页面错误 0"; else bad "F1 页面错误 $PERR 条：$(agent-browser errors 2>/dev/null | head -3)"; fi

echo
echo "[4/4] 结果：$PASS PASS / $FAIL FAIL（截图：tmp/w17-2d-clusters.png、tmp/w17-3d-terrain.png）"
[[ "$FAIL" -eq 0 ]] && exit 0 || exit 1
