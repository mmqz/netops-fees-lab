#!/usr/bin/env bash
# NetOps Lab — R13 写操作令牌网关回归（v0.11）。
#
# 旅程：serve --auth-token secret（空库即可——网关是请求层的）→
#       curl 矩阵 → 浏览器：无令牌写操作 401 → 侧栏设置令牌 → 写操作成功。
# 断言：
#   G1  POST 无令牌 → 401 unauthorized；
#   G2  读面全开：GET health/alerts/stats/openapi + SPA 根 → 200；
#   G3  错误令牌 → 401；正确 Bearer → 200 且落库回执；
#   G4  DELETE 无令牌 → 401（先于路由命中——中间件短路）；
#   B1  浏览器外壳加载 + 侧栏「访问令牌」入口存在；
#   B2  无令牌「立即检测」→ 401 错误展示（对话框内）；
#   B3  设置令牌（对话框保存 → localStorage + toast + 徽章）；
#   B4  有令牌「立即检测」→ 运行回执统计渲染；
#   B5  console 零错误（401 是被捕获的 API 错误，非未捕获拒绝）。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
export PATH="$PATH:/home/z/.local/go/bin"

PORT=18200
WORK="$(mktemp -d /tmp/netops-r13.XXXXXX)"
DB="$WORK/r13.db"
TOKEN="r13-secret-token"

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

echo "[1/3] serve --auth-token（空库）"
mkdir -p "$WORK/data"
(setsid "$BIN" serve --db "$DB" --addr "127.0.0.1:$PORT" --data-dir "$WORK/data" \
  --auth-token "$TOKEN" > "$WORK/serve.log" 2>&1 < /dev/null &)
SERVE_PID=$(pgrep -f "netopslab serve.*:$PORT" | head -1)
for _ in $(seq 1 50); do curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && break; sleep 0.2; done

echo "[2/3] curl 矩阵"
CODE401=$(curl -s -o "$WORK/g1.json" -w '%{http_code}' -X POST "http://127.0.0.1:$PORT/api/detection/run" -H 'Content-Type: application/json' -d '{}')
ERRCODE=$(python3 -c "import json;print(json.load(open('$WORK/g1.json'))['error']['code'])" 2>/dev/null || echo none)
if [[ "$CODE401" == "401" && "$ERRCODE" == "unauthorized" ]]; then
  ok "G1 无令牌 POST → 401 unauthorized（标准错误信封）"
else
  bad "G1 status=$CODE401 code=$ERRCODE"
fi

OPEN_OK=1
for path in /api/health /api/alerts /api/alerts/stats /api/openapi.json /; do
  S=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT$path")
  [[ "$S" == "200" ]] || { OPEN_OK=0; bad "G2 $path → $S"; }
done
[[ "$OPEN_OK" == "1" ]] && ok "G2 读面全开（health/alerts/stats/openapi/SPA 全 200）"

WRONG=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:$PORT/api/detection/run" -H 'Content-Type: application/json' -H "Authorization: Bearer wrong-token" -d '{}')
if [[ "$WRONG" == "401" ]]; then ok "G3a 错误令牌 → 401"; else bad "G3a wrong=$WRONG"; fi
RIGHT=$(curl -s -o "$WORK/g3.json" -w '%{http_code}' -X POST "http://127.0.0.1:$PORT/api/detection/run" -H 'Content-Type: application/json' -H "Authorization: Bearer $TOKEN" -d '{}')
RUNID=$(python3 -c "import json;print(json.load(open('$WORK/g3.json')).get('runId',''))" 2>/dev/null || echo '')
if [[ "$RIGHT" == "200" && -n "$RUNID" ]]; then
  ok "G3b 正确 Bearer → 200 + 回执（${RUNID:0:12}…）"
else
  bad "G3b status=$RIGHT runId=$RUNID"
fi
DEL401=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "http://127.0.0.1:$PORT/api/mapping-templates/whatever")
if [[ "$DEL401" == "401" ]]; then
  ok "G4 DELETE 无令牌 → 401（先于路由 404——中间件短路）"
else
  bad "G4 delete=$DEL401"
fi

echo "[3/3] 浏览器令牌旅程"
agent-browser set viewport 1600 900 >/dev/null
agent-browser open "http://127.0.0.1:$PORT/alerts" >/dev/null
agent-browser wait --fn "!!document.querySelector('.patrol-bar')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 600 >/dev/null
# 清掉上一轮可能残留的令牌，然后整页重载（window 重置）
jeval "localStorage.removeItem('netops_auth_token'); 'cleared'" > /dev/null
agent-browser open "http://127.0.0.1:$PORT/alerts" >/dev/null
agent-browser wait --fn "!!document.querySelector('.patrol-bar')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 600 >/dev/null
# 重载后武装 console 监听（r6 惯例——顺序错了监听会被重载清掉）
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null

BTN="$(jeval "(() => { const b = document.querySelector('.sidebar-token-btn'); return b ? 'ok' : 'none'; })()")"
if [[ "$BTN" == "ok" ]]; then ok "B1a 侧栏「访问令牌」入口"; else bad "B1a btn=$BTN"; fi

# 无令牌：立即检测 → 401 错误
RUN="$(jeval "(() => { const b = [...document.querySelectorAll('.card .btn')].find(x => (x.textContent||'').trim() === '立即检测'); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 400 >/dev/null
START="$(jeval "(() => { const b = [...document.querySelectorAll('.modal .btn--primary')].pop(); if (!b || b.disabled) return 'disabled'; b.click(); return 'ok'; })()")"
agent-browser wait 1200 >/dev/null
ERRTEXT="$(jeval "(() => { const m = document.querySelector('.modal'); return m ? (m.textContent||'') : 'none'; })()")"
if [[ "$RUN" == "ok" && "$START" == "ok" && "$ERRTEXT" == *"令牌"* ]]; then
  ok "B2 无令牌立即检测 → 401 错误展示（含令牌提示）"
else
  bad "B2 run=$RUN start=$START err=${ERRTEXT:0:60}"
fi
# 错误态：RunDetectionDialog 保持打开（confirm 视图内联错误）——
# 保存令牌后直接重试，无需关闭重开。

# 设置令牌
OPEN="$(jeval "(() => { const b = document.querySelector('.sidebar-token-btn'); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 300 >/dev/null
FILL="$(jeval "(() => { const i = document.getElementById('auth-token-input'); if (!i) return 'none'; const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set; setter.call(i, '$TOKEN'); i.dispatchEvent(new Event('input', { bubbles: true })); return 'ok'; })()")"
SAVE="$(jeval "(() => { const b = [...document.querySelectorAll('.modal .btn--primary')].find(x => (x.textContent||'').trim() === '保存'); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 500 >/dev/null
STORED="$(jeval "localStorage.getItem('netops_auth_token') || 'none'")"
BADGE="$(jeval "(() => { const b = document.querySelector('.sidebar-token-badge'); return b ? 'ok' : 'none'; })()")"
if [[ "$OPEN" == "ok" && "$FILL" == "ok" && "$SAVE" == "ok" && "$STORED" == "$TOKEN" && "$BADGE" == "ok" ]]; then
  ok "B3 令牌保存（localStorage + 侧栏徽章）"
else
  bad "B3 open=$OPEN fill=$FILL save=$SAVE stored=${STORED:0:8} badge=$BADGE"
fi

# 有令牌：仍在 confirm 视图——直接重试主按钮（现在携带 Bearer）
RETRY="$(jeval "(() => { const m = document.querySelector('.modal'); if (!m) return 'nomodal'; const b = [...m.querySelectorAll('.btn--primary')].pop(); if (!b || b.disabled) return 'disabled'; b.click(); return 'ok'; })()")"
agent-browser wait --fn "!!document.querySelector('.run-stats')" --timeout 30000 >/dev/null 2>&1 || true
agent-browser wait 800 >/dev/null
STATS="$(jeval "(() => { const s = document.querySelector('.run-stats'); return s ? 'ok' : 'none'; })()")"
if [[ "$RETRY" == "ok" && "$STATS" == "ok" ]]; then
  ok "B4 有令牌立即检测 → 运行回执渲染（retry=$RETRY）"
else
  bad "B4 retry=$RETRY stats=$STATS"
fi

ERRS="$(jeval "window.__errs ? window.__errs.length : -1")"
if [[ "$ERRS" == "0" ]]; then ok "B5 console 零错误（401 走被捕获的 API 错误面）"; else
  bad "B5 console errors = $ERRS: $(jeval "window.__errs ? window.__errs.join(' | ').slice(0, 200) : ''")"
fi

echo
echo "结果：PASS=$PASS FAIL=$FAIL"
[[ "$FAIL" -eq 0 ]] && echo "R13 AUTH WRITE-GATE E2E: ALL GREEN"
exit "$FAIL"
