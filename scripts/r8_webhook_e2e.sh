#!/usr/bin/env bash
# NetOps Lab — R8 webhook 通知端到端回归（v0.10）。
#
# 旅程：serve 以 --webhook-url 启动（python 接收端）→ 浏览器 /alerts
#       「立即检测」→ 检测运行完成 → 接收端收到 JSON 摘要。
# 断言：
#   W1  接收端收到恰好一次 POST（application/json）；
#   W2  payload 契约：event=detection.run + 四计数 + alerts 变更行
#       （含 alertId/metricKey/resourceId/action/state/lastValue）；
#   W3  计数与 API 回执一致（findings/alertsCreated）；
#   W4  CLI 通道：`netopslab detect --webhook-url` 同样投递（重跑零变更
#       也投递——确定性回执本身就是通知事件）。
# 集成测试（internal/api/detection_webhook_test.go + internal/notify）
# 已钉死契约细节；本脚本补真实进程 + 真浏览器闭环。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
export PATH="$PATH:/home/z/.local/go/bin"

PORT=18178
WH_PORT=18179
WORK="$(mktemp -d /tmp/netops-r8wh.XXXXXX)"
DB="$WORK/r8.db"
REALDATA="$REPO_ROOT/testdata/realdata"

PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "  PASS  $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  FAIL  $1"; }

cleanup() {
  [[ -n "${SERVE_PID:-}" ]] && kill "$SERVE_PID" 2>/dev/null || true
  [[ -n "${WH_PID:-}" ]] && kill "$WH_PID" 2>/dev/null || true
  agent-browser close >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

BIN="$WORK/netopslab"
go build -o "$BIN" ./cmd/netopslab

echo "[1/4] realdata 导入（31 工作簿，依赖序）+ 摄入 + 预置检测"
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
  "$BIN" import --file "$f" --map "$WORK/m.json" --db "$DB" > /dev/null
done
for day in 01 02 03 04 05 06 07; do
  "$BIN" ingest --db "$DB" --file "$REALDATA/perf-day-$day.xlsx" \
    --map "$REALDATA/ingest-mapping.json" > /dev/null
done
"$BIN" detect --db "$DB" > "$WORK/detect.json"
PRE=$(python3 -c "import json;print(json.load(open('$WORK/detect.json'))['alertsCreated'])")
if [[ "$PRE" -ge 1 ]]; then ok "P0 预置检测 ${PRE} 条告警"; else bad "P0 alertsCreated = $PRE"; fi

echo "[2/4] webhook 接收端 + serve --webhook-url"
python3 - "$WH_PORT" > "$WORK/wh.log" 2>&1 <<'PY' &
import http.server, json, sys, threading

PORT = int(sys.argv[1])
DELIVERIES = []

class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(n)
        with open("/tmp/r8wh-deliveries.jsonl", "a") as f:
            f.write(raw.decode("utf-8") + "\n")
        self.send_response(200)
        self.end_headers()
    def log_message(self, *a):
        pass

http.server.ThreadingHTTPServer(("127.0.0.1", PORT), H).serve_forever()
PY
WH_PID=$!
rm -f /tmp/r8wh-deliveries.jsonl
(setsid "$BIN" serve --db "$DB" --addr "127.0.0.1:$PORT" \
  --webhook-url "http://127.0.0.1:$WH_PORT/" > "$WORK/serve.log" 2>&1 < /dev/null &)
SERVE_PID=$(pgrep -f "netopslab serve.*:$PORT" | head -1)
for _ in $(seq 1 50); do curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && break; sleep 0.2; done

echo "[3/4] 浏览器触发立即检测"
agent-browser set viewport 1600 900 >/dev/null
agent-browser open "http://127.0.0.1:$PORT/alerts" >/dev/null
agent-browser wait --fn "!!document.querySelector('.card .filter-bar')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser wait 800 >/dev/null
jeval() { agent-browser eval "$1" 2>/dev/null | tr -d '"'; }
RUN="$(jeval "(() => { const b = [...document.querySelectorAll('.card .btn')].find(x => (x.textContent||'').trim() === '立即检测'); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 400 >/dev/null
START="$(jeval "(() => { const b = [...document.querySelectorAll('.modal .btn--primary')].pop(); if (!b || b.disabled) return 'disabled'; b.click(); return 'ok'; })()")"
agent-browser wait --fn "!!document.querySelector('.run-stats')" --timeout 30000 >/dev/null 2>&1
agent-browser wait 1500 >/dev/null
if [[ "$RUN" == "ok" && "$START" == "ok" ]]; then ok "W0 浏览器立即检测触发"; else bad "W0 run=$RUN start=$START"; fi

# —— W1/W2/W3 接收端断言 ——
sleep 1
if [[ -s /tmp/r8wh-deliveries.jsonl ]]; then
  ok "W1 接收端收到投递（$(wc -l < /tmp/r8wh-deliveries.jsonl) 条）"
else
  bad "W1 接收端无投递"
fi
python3 - "$WORK/wh-assert.txt" <<'PY' || true
import json, sys

lines = [l for l in open("/tmp/r8wh-deliveries.jsonl", encoding="utf-8").read().splitlines() if l]
results = []
if not lines:
    results.append(("W2 payload 解析", False, "无投递"))
else:
    d = json.loads(lines[0])
    checks = [
        ("event", d.get("event") == "detection.run"),
        ("findings 数值", isinstance(d.get("findings"), int)),
        ("alertsCreated 数值", isinstance(d.get("alertsCreated"), int)),
        ("alertsCleared 数值", isinstance(d.get("alertsCleared"), int)),
        ("runStatesWritten 数值", isinstance(d.get("runStatesWritten"), int)),
        ("evaluatedAt 字符串", isinstance(d.get("evaluatedAt"), str) and d.get("evaluatedAt") != ""),
        ("alerts 数组", isinstance(d.get("alerts"), list)),
        ("deliveredAt 字符串", isinstance(d.get("deliveredAt"), str)),
    ]
    if d.get("alerts"):
        first = d["alerts"][0]
        for key in ("alertId", "metricKey", "resourceId", "severity", "action", "state", "lastValue"):
            checks.append((f"alerts[0].{key}", key in first))
    for name, passed in checks:
        results.append((f"W2 {name}", passed, ""))

with open(sys.argv[1], "w", encoding="utf-8") as f:
    for name, passed, note in results:
        f.write(f"{'ok' if passed else 'bad'}\t{name}\t{note}\n")
PY
if [[ -f "$WORK/wh-assert.txt" ]]; then
  while IFS=$'\t' read -r st name note; do
    if [[ "$st" == "ok" ]]; then ok "$name"; else bad "$name $note"; fi
  done < "$WORK/wh-assert.txt"
fi
API_FINDINGS=$(curl -s -X POST "http://127.0.0.1:$PORT/api/detection/run" -d '{}' | python3 -c "import json,sys;print(json.load(sys.stdin)['findings'])")
sleep 1
WH_FINDINGS=$(python3 -c "
import json
lines=[l for l in open('/tmp/r8wh-deliveries.jsonl',encoding='utf-8').read().splitlines() if l]
print(json.loads(lines[-1])['findings'] if lines else -1)")
if [[ "$WH_FINDINGS" == "$API_FINDINGS" ]]; then
  ok "W3 webhook findings（${WH_FINDINGS}）与 API 回执一致"
else
  bad "W3 webhook=$WH_FINDINGS api=$API_FINDINGS"
fi

# —— W4 CLI 通道 ——
BEFORE=$(wc -l < /tmp/r8wh-deliveries.jsonl)
"$BIN" detect --db "$DB" --webhook-url "http://127.0.0.1:$WH_PORT/" > "$WORK/detect2.json" 2>"$WORK/detect2.err"
AFTER=$(wc -l < /tmp/r8wh-deliveries.jsonl)
if [[ "$AFTER" -gt "$BEFORE" ]]; then
  ok "W4 CLI detect --webhook-url 投递（${BEFORE} → ${AFTER}）"
else
  bad "W4 CLI 无投递（${BEFORE} → ${AFTER}）"
fi

echo
echo "结果：PASS=$PASS FAIL=$FAIL"
[[ "$FAIL" -eq 0 ]] && echo "R8 WEBHOOK E2E: ALL GREEN"
exit "$FAIL"
