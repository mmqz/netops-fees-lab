#!/usr/bin/env bash
# NetOps Lab — #47 工作台 UI 四诉求浏览器级回归（v0.9）。
#
# 前置：make web（dist 已构建）。
# 断言（对应 issue #47 验收清单）：
#   1. /lab 画布概览：.lab-grid 网格 + .lab-tile 卡片 + MiniTopology SVG
#      （真实拓扑的 circle 布局点/弦）；多工作台多卡片；
#   2. 视图切换：概览 ↔ 列表模式（表格回到 .table-wrap），localStorage 持久；
#   3. /lab/:id 页头卡片移除：.lab-pagehead 不存在，.lab-pagebar 单行存在；
#   4. 工具带顺序：画布固有控件（.canvas-seg）在 .lab-canvas-actions 之前；
#      动作组 7 个图标钮（.btn--ico）；
#   5. 工具带布局契约（#77 校准）：1600 宽画布单行 ≤52px；1280 窄画布按 #44
#      换行收纳——组内不折行（组高 ≤42px）+ 零横向溢出；
#   6. 全程 console error / unhandledrejection 零。
# 截图存 repo tmp/（不进 download/ —— 截屏不作为交付物）。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
export PATH="$PATH:/home/z/.local/go/bin"

PORT=18147
WORK="$(mktemp -d /tmp/netops-w47.XXXXXX)"
DB="$WORK/w47.db"
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

echo "[1/3] 经典工作簿导入 + 两个工作台"
for f in devices ippools circuits; do
  "$BIN" analyze -file "testdata/$f.xlsx" > "$WORK/a.json"
  python3 - "$WORK/a.json" "$WORK/m.json" <<'PY'
import json, sys
json.dump(json.load(open(sys.argv[1], encoding="utf-8"))["suggestedMapping"], open(sys.argv[2], "w", encoding="utf-8"))
PY
  "$BIN" import --file "testdata/$f.xlsx" --map "$WORK/m.json" --db "$DB" > "$WORK/i.json"
done
"$BIN" lab workspace create --db "$DB" --name "回归甲" --desc "w47 canvas overview A" > "$WORK/w1.json" 2> "$WORK/w1.err" || true
"$BIN" lab workspace create --db "$DB" --name "回归乙" --desc "w47 canvas overview B" > "$WORK/w2.json" || true
WS=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1], encoding='utf-8'))['workspaceId'])" "$WORK/w1.json")

echo "[2/3] serve"
"$BIN" serve --db "$DB" --addr "127.0.0.1:$PORT" > "$WORK/serve.log" 2>&1 &
SERVE_PID=$!
for _ in $(seq 1 50); do curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && break; sleep 0.2; done

echo "[3/3] 浏览器断言"
agent-browser set viewport 1600 900 >/dev/null
agent-browser open "http://127.0.0.1:$PORT/lab" >/dev/null
agent-browser wait --load networkidle >/dev/null 2>&1 || true
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null

# —— 1. 画布概览默认态 ——
agent-browser wait --fn "!!document.querySelector('.lab-grid')" --timeout 20000 >/dev/null 2>&1 || true
GRID="$(jeval "document.querySelector('.lab-grid') ? 'yes' : 'no'")"
if [[ "$GRID" == "yes" ]]; then ok "B1 /lab 默认画布概览（.lab-grid）"; else bad "B1 .lab-grid = $GRID"; fi
TILES="$(jeval "document.querySelectorAll('.lab-tile').length")"
if [[ "$TILES" -ge 2 ]]; then ok "B2 多工作台多卡片（$TILES 张）"; else bad "B2 tiles = $TILES"; fi
SVG="$(jeval "(document.querySelector('.lab-tile-preview svg') ? document.querySelectorAll('.lab-tile-preview svg circle').length : 0)")"
if [[ "$SVG" -gt 5 ]]; then ok "B3 缩略图真实拓扑渲染（$SVG 个节点圆）"; else bad "B3 circles = $SVG"; fi
agent-browser wait 800 >/dev/null

# —— 2. 视图切换 ——
TABLE_BTN="$(jeval "(() => { const b=[...document.querySelectorAll('.canvas-seg button')].find(x => (x.getAttribute('aria-label')||'') === '列表模式'); if(!b) return 'NO-BTN'; b.click(); return 'ok'; })()")"
agent-browser wait 400 >/dev/null
TABLE_MODE="$(jeval "document.querySelector('.card .table-wrap table') ? 'yes' : 'no'")"
if [[ "$TABLE_MODE" == "yes" && "$TABLE_BTN" == "ok" ]]; then ok "B4 切到列表模式（表格恢复）"; else bad "B4 table=$TABLE_MODE btn=$TABLE_BTN"; fi
PERSIST="$(jeval "localStorage.getItem('netops-lab-list-view')")"
if [[ "$PERSIST" == "table" ]]; then ok "B5 视图偏好持久化"; else bad "B5 persist = $PERSIST"; fi

# —— 3. 工作台页头 ——
agent-browser open "http://127.0.0.1:$PORT/lab/$WS" >/dev/null
agent-browser wait --fn "!!document.querySelector('.lab-split')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 1200 >/dev/null
OLDHEAD="$(jeval "document.querySelector('.lab-pagehead') ? 'yes' : 'no'")"
if [[ "$OLDHEAD" == "no" ]]; then ok "B6 页头卡片已移除（.lab-pagehead 不存在）"; else bad "B6 页头卡片仍在"; fi
NEWBAR="$(jeval "document.querySelector('.lab-pagebar .lab-head-name') ? 'yes' : 'no'")"
if [[ "$NEWBAR" == "yes" ]]; then ok "B7 无边框单行页头条（.lab-pagebar）"; else bad "B7 pagebar = $NEWBAR"; fi
BARLINES="$(jeval "(() => { const bar = document.querySelector('.lab-pagebar'); return bar ? Math.round(bar.getBoundingClientRect().height) : 0; })()")"
if [[ "$BARLINES" -le 48 ]]; then ok "B8 页头条单行（高度 ${BARLINES}px）"; else bad "B8 页头条高度 ${BARLINES}px"; fi

# —— 4. 工具带顺序与图标化 ——
ORDER="$(jeval "(() => { const tb = document.querySelector('.lab-canvas-toolbar'); const seg = tb.querySelector('.canvas-seg'); const act = tb.querySelector('.lab-canvas-actions'); if (!seg || !act) return 'missing'; return seg.compareDocumentPosition(act) & Node.DOCUMENT_POSITION_FOLLOWING ? 'canvas-first' : 'actions-first'; })()")"
if [[ "$ORDER" == "canvas-first" ]]; then ok "B9 画布控件在前，动作按钮殿后"; else bad "B9 order = $ORDER"; fi
ICOBTNS="$(jeval "document.querySelectorAll('.lab-canvas-actions .btn--ico').length")"
if [[ "$ICOBTNS" -eq 7 ]]; then ok "B10 七个动作按钮图标化（32px 方形）"; else bad "B10 icon buttons = $ICOBTNS"; fi
ACTNOWRAP="$(jeval "(() => { const g = document.querySelector('.lab-canvas-actions'); const cs = getComputedStyle(g); return cs.flexWrap; })()")"
if [[ "$ACTNOWRAP" == "nowrap" ]]; then ok "B11 动作组 nowrap（不出现两行按钮）"; else bad "B11 flexWrap = $ACTNOWRAP"; fi

# —— 5. 工具带布局契约（#77 校准）：#44 并入动作按钮组后，窄画布单行放
#    不下整条工具带——设计意图改为「换行收纳」：分组（canvas-seg /
#    lab-canvas-actions）内部不折行，组与组在行间流动，且不再横向逃逸
#    容器（旧 nowrap 时代真溢出直接横向逃逸，wrap 严格更优——见
#    global.css .lab-canvas-toolbar 注释）。校准后的断言：
#    宽画布（1600）仍承诺单行 ≤52px；窄画布（1280）允许第二行，但
#    每组自身单行（组高 ≤42px）且工具带零横向溢出。 ——
check_toolbar_layout() {
  local VP="$1" WANT_SINGLE="$2"
  agent-browser set viewport "$VP" 900 >/dev/null
  agent-browser wait 500 >/dev/null
  local M="$(jeval "(() => { const tb = document.querySelector('.lab-canvas-toolbar'); const seg = tb && tb.querySelector('.canvas-seg'); const act = tb && tb.querySelector('.lab-canvas-actions'); if (!tb || !seg || !act) return 'missing'; return [Math.round(tb.getBoundingClientRect().height), Math.round(seg.getBoundingClientRect().height), Math.round(act.getBoundingClientRect().height), tb.scrollWidth > tb.clientWidth + 1 ? 1 : 0].join(','); })()")"
  local TBH SEGHT ACTH OVERFLOW
  IFS=',' read -r TBH SEGHT ACTH OVERFLOW <<< "$M"
  if [[ "$WANT_SINGLE" == "1" ]]; then
    if [[ "$TBH" -le 52 && "$OVERFLOW" == "0" ]]; then ok "B12 ${VP}px 宽画布工具带单行（${TBH}px）"; else bad "B12 ${VP}px 工具带高 ${TBH}px / 溢出 ${OVERFLOW}（宽画布应单行）"; fi
  else
    if [[ "$SEGHT" -le 42 && "$ACTH" -le 42 && "$OVERFLOW" == "0" ]]; then
      ok "B12 ${VP}px 窄画布换行收纳：组内不折行（seg ${SEGHT}px / actions ${ACTH}px）、零横向溢出（总高 ${TBH}px）"
    else
      bad "B12 ${VP}px 组内折行或溢出：seg=${SEGHT} actions=${ACTH} overflow=${OVERFLOW} 总高=${TBH}"
    fi
  fi
}
check_toolbar_layout 1600 1
check_toolbar_layout 1280 0

agent-browser screenshot "$SHOT_DIR/w47-workspace.png" >/dev/null 2>&1 || true
agent-browser set viewport 1600 900 >/dev/null

# —— 6. console 零错误（进入 /lab/:id 后重新布防——导航会清掉 /lab 页的
#    window 态；__errs 统计的是工作台页自身的错误面） ——
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'rearmed'" >/dev/null
agent-browser wait 800 >/dev/null
ERRS="$(jeval "window.__errs ? String(window.__errs.length) : 'NA'")"
if [[ "$ERRS" == "0" ]]; then ok "B13 console 零错误/零未捕获拒绝"; else bad "B13 console 错误 ${ERRS} 条"; fi

echo
echo "结果：PASS=$PASS FAIL=$FAIL"
[[ "$FAIL" -eq 0 ]]
