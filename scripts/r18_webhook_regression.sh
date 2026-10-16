#!/usr/bin/env bash
# NetOps Lab — R18 webhook 投递硬化（有界重试 + 审计）浏览器级回归（v0.12 /alerts）。
#
# 前置：make web（dist 已构建）。
# 旅程：导入最小资源集（devices+ports）→ 起一个「先 503 两次再 200」的
#       抖动接收器（带 ?token= 查询串——顺带验证审计脱敏）→ serve
#       --webhook-url → POST /api/detection/run（异步通知）→ 轮询
#       GET /api/detection/runs 直到裁决落库 → 断言 delivered + attempts=3
#       → 接收器切换为永久 503 → 再跑一轮 → failed + error 原因 →
#       浏览器：运行历史对话框「通知」列 chip（已送达/投递失败）。
# 断言：
#   P0  瞬时故障被重试吸收：verdict=delivered，attempts=3（接收器恰被打 3 次）；
#   P1  审计 URL 脱敏：webhook_deliveries.url 不含 token 查询串；
#   P2  死接收器：verdict=failed，webhookError 非空，回执本身 error 为空
#       （通知失败绝不拖垮检测运行——best-effort 契约）；
#   A1  运行历史对话框：「通知」列显示已送达 + 投递失败；
#   A2  console 零错误/零未捕获拒绝。
# 截图存 repo tmp/（不作为交付物）。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
export PATH="$PATH:/home/z/.local/go/bin"

PORT=18176
RECV_PORT=18177
WORK="$(mktemp -d /tmp/netops-r18wh.XXXXXX)"
DB="$WORK/r18.db"
REALDATA="$REPO_ROOT/testdata/realdata"
SHOT_DIR="$REPO_ROOT/tmp"
mkdir -p "$SHOT_DIR"

PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "  PASS  $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  FAIL  $1"; }
jeval() { agent-browser eval "$1" 2>/dev/null | tr -d '"'; }

RECV_STATE="$WORK/recv-state"   # 0 = flaky (503x2 then 200), 1 = always 503
RECV_LOG="$WORK/recv-hits"
cleanup() {
  [[ -n "${SERVE_PID:-}" ]] && kill "$SERVE_PID" 2>/dev/null || true
  [[ -n "${RECV_PID:-}" ]] && kill "$RECV_PID" 2>/dev/null || true
  agent-browser close >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

BIN="$WORK/netopslab"
go build -o "$BIN" ./cmd/netopslab

echo "[1/5] 抖动接收器（先 503×2 再 200；状态文件可切换为永久 503）"
echo 0 > "$RECV_STATE"
python3 - "$RECV_PORT" "$RECV_STATE" "$RECV_LOG" <<'PY' &
import http.server, sys, threading

port, state_path, log_path = int(sys.argv[1]), sys.argv[2], sys.argv[3]
hits = {"n": 0}
lock = threading.Lock()

class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        with lock:
            hits["n"] += 1
            n = hits["n"]
            with open(log_path, "w") as f:
                f.write(str(n))
        mode = open(state_path).read().strip()
        if mode == "0" and n <= 2:
            self.send_response(503)
            self.end_headers()
            return
        if mode == "1":
            self.send_response(503)
            self.end_headers()
            return
        self.send_response(200)
        self.end_headers()
    def log_message(self, *a):
        pass

http.server.ThreadingHTTPServer(("127.0.0.1", port), H).serve_forever()
PY
RECV_PID=$!
for _ in $(seq 1 50); do curl -s -o /dev/null "http://127.0.0.1:$RECV_PORT/" 2>/dev/null && break; sleep 0.1; done

echo "[2/5] 最小导入 + serve（webhook 指向抖动接收器，带 token 查询串）"
for wb in devices ports; do
  f="$REALDATA/$wb.xlsx"
  "$BIN" analyze -file "$f" > "$WORK/a.json"
  python3 - "$WORK/a.json" "$WORK/m.json" <<'PY'
import json, sys
json.dump(json.load(open(sys.argv[1], encoding="utf-8"))["suggestedMapping"], open(sys.argv[2], "w", encoding="utf-8"))
PY
  "$BIN" import --file "$f" --map "$WORK/m.json" --db "$DB" > "$WORK/i-$wb.json"
done
"$BIN" serve --db "$DB" --addr "127.0.0.1:$PORT" \
  --webhook-url "http://127.0.0.1:$RECV_PORT/hook?token=demo-secret" \
  > "$WORK/serve.log" 2>&1 &
SERVE_PID=$!
for _ in $(seq 1 50); do curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && break; sleep 0.2; done

echo "[3/5] 第一轮：瞬时故障被重试吸收"
RUN1=$(curl -s -X POST "http://127.0.0.1:$PORT/api/detection/run" -H 'Content-Type: application/json' -d '{}')
RUN1_ID=$(python3 -c "import json; print(json.loads('''$RUN1''')['runId'])")
# 轮询 runs 列表直到裁决落库（异步通知 + 默认 1s/2s 退避 ≈ 3s）
VERDICT1=""
for _ in $(seq 1 150); do
  V=$(curl -s "http://127.0.0.1:$PORT/api/detection/runs?page=1&pageSize=5" | python3 -c "
import json, sys
d = json.load(sys.stdin)
for it in d['items']:
    if it['runId'] == '$RUN1_ID':
        print(f\"{it['webhookStatus']}|{it['webhookAttempts']}\")
        break
")
  if [[ -n "$V" && "$(echo "$V" | cut -d'|' -f1)" != "" ]]; then VERDICT1="$V"; break; fi
  sleep 0.1
done
if [[ "$VERDICT1" == "delivered|3" ]]; then
  ok "P0 瞬时故障被吸收：delivered，attempts=3"
else
  bad "P0 verdict=[$VERDICT1] (want delivered|3)"
fi
RECV_HITS=$(cat "$RECV_LOG" 2>/dev/null || echo 0)
if [[ "$RECV_HITS" == "3" ]]; then ok "P0 接收器恰被打 3 次（有界重试）"; else bad "P0 hits=$RECV_HITS"; fi

# P1 审计 URL 脱敏
AUDIT_URL=$(python3 - "$DB" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
row = db.execute("SELECT url FROM webhook_deliveries ORDER BY delivered_at DESC LIMIT 1").fetchone()
print(row[0] if row else "")
PY
)
if [[ "$AUDIT_URL" == "http://127.0.0.1:$RECV_PORT/hook" ]]; then
  ok "P1 审计 URL 已脱敏（token 查询串不落库）：$AUDIT_URL"
else
  bad "P1 auditUrl=$AUDIT_URL"
fi

echo "[4/5] 第二轮：死接收器 → failed（检测运行本身不受影响）"
echo 1 > "$RECV_STATE"
RUN2=$(curl -s -X POST "http://127.0.0.1:$PORT/api/detection/run" -H 'Content-Type: application/json' -d '{}')
RUN2_ID=$(python3 -c "import json; print(json.loads('''$RUN2''')['runId'])")
VERDICT2=""
for _ in $(seq 1 150); do
  V=$(curl -s "http://127.0.0.1:$PORT/api/detection/runs?page=1&pageSize=5" | python3 -c "
import json, sys
d = json.load(sys.stdin)
for it in d['items']:
    if it['runId'] == '$RUN2_ID':
        print(f\"{it['webhookStatus']}|{it['webhookError']}|{it['error']}\")
        break
")
  if [[ -n "$V" && "$(echo "$V" | cut -d'|' -f1)" != "" ]]; then VERDICT2="$V"; break; fi
  sleep 0.1
done
if [[ "$VERDICT2" == failed* && "$(echo "$VERDICT2" | cut -d'|' -f2)" != "" && "$(echo "$VERDICT2" | cut -d'|' -f3)" == "" ]]; then
  ok "P2 死接收器：failed + 失败原因非空 + 回执无错误（best-effort 契约）"
else
  bad "P2 verdict=[$VERDICT2]"
fi

echo "[5/5] 浏览器断言（运行历史「通知」列）"
agent-browser set viewport 1600 900 >/dev/null
agent-browser open "http://127.0.0.1:$PORT/alerts" >/dev/null
agent-browser wait --fn "!!document.querySelector('.patrol-bar')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 1000 >/dev/null
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null

HIST_BTN="$(jeval "(() => { const b = [...document.querySelectorAll('.patrol-bar button')].find(x => (x.title||'').includes('运行历史') || (x.textContent||'').includes('历史')); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait --fn "!!document.querySelector('.modal--wide table')" --timeout 15000 >/dev/null 2>&1 || true
agent-browser wait 600 >/dev/null
DELIV="$(jeval "(() => { return [...document.querySelectorAll('.modal--wide .patrol-status--ok')].some(s => (s.textContent||'').includes('已送达')); })()")"
FAILED="$(jeval "(() => { return [...document.querySelectorAll('.modal--wide .patrol-status--failed')].some(s => (s.textContent||'').includes('投递失败')); })()")"
if [[ "$HIST_BTN" == "ok" && "$DELIV" == "true" ]]; then
  ok "A1a 运行历史「通知」列显示已送达"
else
  bad "A1a hist=$HIST_BTN delivered=$DELIV"
fi
if [[ "$FAILED" == "true" ]]; then
  ok "A1b 运行历史「通知」列显示投递失败"
else
  bad "A1b failed=$FAILED"
fi
agent-browser screenshot "$SHOT_DIR/r18-run-history.png" >/dev/null 2>&1 || true

ERRS="$(jeval "window.__errs.length")"
if [[ "$ERRS" == "0" ]]; then ok "A2 console 零错误/零未捕获拒绝"; else bad "A2 console 错误 ${ERRS} 条：$(jeval "window.__errs.join(' | ')")"; fi

echo
echo "汇总：PASS=$PASS FAIL=$FAIL"
if [[ "$FAIL" -gt 0 ]]; then exit 1; fi
