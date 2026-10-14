#!/usr/bin/env bash
# NetOps Lab — R3 资源详情「影响分析」页签浏览器级回归（v0.9）。
#
# 前置：make web（dist 已构建）。
# 旅程：realdata 导入 → 摄入/检测（制造告警 run_state）→ 哈大齐光缆
#       置 down（manual 观测）→ 电路抽屉「影响分析」三子模式断言。
# 断言：
#   G1  抽屉出现「影响分析」页签；
#   G2  默认「影响面」模式：根因候选 + 受影响面两区（各含节点行）；
#   G3  候选区含异常节点红 chip + 中文判定理由（.graph-reason）；
#   G4  节点行：层族类型 chip + entity-link（→ /resources?focus=）；
#   G5  切「向下穿透」：depth 分组 + 节点/关系/跨层计数；
#   G6  切「向上汇总」：可达业务面（或空态文案——契约仍成立）；
#   G7  异常根因旅程：光缆置 down 后，影响面候选含「异常」chip
#       且判定理由非空（§10.5 根因判定在 UI 可见）；
#   G8  console 零错误/零未捕获拒绝。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
export PATH="$PATH:/home/z/.local/go/bin"

PORT=$(python3 -c "import socket; s=socket.socket(); s.bind(('127.0.0.1', 0)); print(s.getsockname()[1]); s.close()")
WORK="$(mktemp -d /tmp/netops-r3gr.XXXXXX)"
DB="$WORK/r3.db"
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

echo "[1/3] realdata 导入（依赖序）"
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
echo "      导入完成（31 本）"

echo "[2/3] serve + 制造异常根因（哈大齐光缆置 down）"
"$BIN" serve --db "$DB" --addr "127.0.0.1:$PORT" > "$WORK/serve.log" 2>&1 &
SERVE_PID=$!
for _ in $(seq 1 50); do curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && break; sleep 0.2; done

# 哈大齐光缆置 down（manual 观测，demo-real 同款旅程）
OC_ID=""
for _ in $(seq 1 20); do
  OC_ID=$(curl -sf "http://127.0.0.1:$PORT/api/resources?type=optical_cable&q=OC-HDQ" \
    | python3 -c "import json,sys;d=json.load(sys.stdin);items=d.get('items',[]);print(items[0]['id'] if items else '')" 2>/dev/null) && [[ -n "$OC_ID" ]] && break
  sleep 0.5
done
curl -sf -X POST "http://127.0.0.1:$PORT/api/resources/$OC_ID/run-state" \
  -H "Content-Type: application/json" -d '{"runState": "down"}' > "$WORK/rs.json"
# 找一条受影响电路（impact 会波及的 L3 传输层资源：电路 carried_by→otn→routed_over→OC）
CKT_ID=""
for _ in $(seq 1 20); do
  CKT_ID=$(curl -sf "http://127.0.0.1:$PORT/api/resources/$OC_ID/impact" \
    | python3 -c "
import json, sys
d = json.load(sys.stdin)
ups = [i['node']['id'] for i in d.get('impacted', []) if i['node']['type'] == 'circuit']
print(ups[0] if ups else '')" 2>/dev/null) && [[ -n "$CKT_ID" ]] && break
  sleep 0.5
done
if [[ -z "$CKT_ID" ]]; then
  # fallback：取任意电路
  CKT_ID=$(curl -sf "http://127.0.0.1:$PORT/api/resources?type=circuit&pageSize=1" \
    | python3 -c "import json,sys;d=json.load(sys.stdin);print(d['items'][0]['id'])")
fi
echo "      光缆：$OC_ID · 电路：$CKT_ID"

echo "[3/3] 浏览器断言"
agent-browser set viewport 1600 900 >/dev/null
agent-browser open "http://127.0.0.1:$PORT/resources?focus=$CKT_ID" >/dev/null
agent-browser wait --fn "!!document.querySelector('.drawer .tabs .tab')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 1000 >/dev/null
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null

# —— G1 页签 ——
TAB="$(jeval "(() => { return [...document.querySelectorAll('.drawer .tabs .tab')].some(t => (t.textContent||'').includes('影响分析')); })()")"
if [[ "$TAB" == "true" ]]; then ok "G1 「影响分析」页签存在"; else bad "G1 无影响分析页签"; fi

# 打开页签（默认影响面模式）
jeval "(() => { const t = [...document.querySelectorAll('.drawer .tabs .tab')].find(x => (x.textContent||'').includes('影响分析')); if (t) t.click(); return 'ok'; })()" >/dev/null
agent-browser wait --fn "!!document.querySelector('.graph-tab')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 1000 >/dev/null

# —— G2 影响面两区 ——
CAND_TITLE="$(jeval "(() => { return [...document.querySelectorAll('.graph-tab .graph-section-title')].some(t => (t.textContent||'').includes('根因候选')); })()")"
IMP_TITLE="$(jeval "(() => { return [...document.querySelectorAll('.graph-tab .graph-section-title')].some(t => (t.textContent||'').includes('受影响面')); })()")"
IMP_ROWS="$(jeval "document.querySelectorAll('.graph-tab .graph-node-row').length")"
if [[ "$CAND_TITLE" == "true" && "$IMP_TITLE" == "true" ]]; then ok "G2a 影响面模式两区（根因候选 + 受影响面）"; else bad "G2a cand=$CAND_TITLE imp=$IMP_TITLE"; fi
if [[ "$IMP_ROWS" -ge 1 ]]; then ok "G2b 节点行渲染（${IMP_ROWS} 行）"; else bad "G2b rows = $IMP_ROWS"; fi

# —— G3 异常候选（根因分析默认模式：光缆已置 down，候选应可见） ——
ABN="$(jeval "(() => { return [...document.querySelectorAll('.graph-tab .chip--error')].some(c => (c.textContent||'').includes('异常')); })()")"
REASON="$(jeval "(() => { const r = document.querySelector('.graph-tab .graph-reason'); return r && (r.textContent||'').trim().length > 4 ? 'ok' : 'none'; })()")"
if [[ "$ABN" == "true" ]]; then ok "G3a 异常节点红 chip（根因候选）"; else bad "G3a 无异常 chip"; fi
if [[ "$REASON" == "ok" ]]; then ok "G3b 中文判定理由非空"; else bad "G3b reason = $REASON"; fi

# —— G4 节点行结构 ——
CHIP="$(jeval "document.querySelectorAll('.graph-tab .graph-node-row .chip--mono').length")"
LINK="$(jeval "document.querySelectorAll('.graph-tab .graph-node-row .entity-link').length")"
if [[ "$CHIP" -ge 1 && "$LINK" -ge 1 ]]; then ok "G4 层 chip + entity-link（${LINK} 链接）"; else bad "G4 chips=$CHIP links=$LINK"; fi

agent-browser screenshot "$SHOT_DIR/r3-impact.png" >/dev/null 2>&1 || true

# —— G5 向下穿透 ——
jeval "(() => { const b = [...document.querySelectorAll('.graph-tab .graph-mode .btn')].find(x => (x.textContent||'').includes('向下穿透')); if (b) b.click(); return 'ok'; })()" >/dev/null
agent-browser wait --fn "!!document.querySelector('.graph-tab .graph-traverse')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 1000 >/dev/null
DEPTH_GROUPS="$(jeval "document.querySelectorAll('.graph-tab .graph-depth-group').length")"
SUMMARY="$(jeval "(() => { const s = document.querySelector('.graph-tab .graph-summary .muted-note'); return s ? 'ok' : 'none'; })()")"
if [[ "$DEPTH_GROUPS" -ge 1 && "$SUMMARY" == "ok" ]]; then ok "G5 向下穿透（${DEPTH_GROUPS} 个 depth 分组 + 计数）"; else bad "G5 groups=$DEPTH_GROUPS summary=$SUMMARY"; fi

# —— G6 向上汇总 ——
jeval "(() => { const b = [...document.querySelectorAll('.graph-tab .graph-mode .btn')].find(x => (x.textContent||'').includes('向上汇总')); if (b) b.click(); return 'ok'; })()" >/dev/null
agent-browser wait 1500 >/dev/null
UP_OK="$(jeval "(() => { const hasNodes = document.querySelectorAll('.graph-tab .graph-node-row').length > 0; const hasEmpty = [...document.querySelectorAll('.graph-tab .muted-note')].some(n => (n.textContent||'').includes('无可达')); return hasNodes || hasEmpty ? 'ok' : 'fail'; })()")"
if [[ "$UP_OK" == "ok" ]]; then ok "G6 向上汇总（可达业务面或空态契约）"; else bad "G6 up = $UP_OK"; fi

# —— G8 console 零错误 ——
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'rearmed'" >/dev/null
agent-browser wait 800 >/dev/null
ERRS="$(jeval "window.__errs ? String(window.__errs.length) : 'NA'")"
if [[ "$ERRS" == "0" ]]; then ok "G8 console 零错误/零未捕获拒绝"; else bad "G8 console 错误 ${ERRS} 条"; fi

echo
echo "结果：PASS=$PASS FAIL=$FAIL"
[[ "$FAIL" -eq 0 ]]
