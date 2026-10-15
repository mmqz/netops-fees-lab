#!/usr/bin/env bash
# NetOps Lab — R11 定时巡检闭环回归（v0.11）。
#
# 旅程：serve --detect-interval 3s --webhook-url 启动（python 接收端）→
#       HTTP 导入 synthdata（degrade 场景 3 设备 2 天）→ CLI detect 预置
#       告警 → 等待调度器自动巡检 ≥3 轮 → 手动 POST /api/detection/run →
#       浏览器 /alerts 巡检条 + 运行历史。
# 断言：
#   S1  运行历史：cli 来源行 + scheduler 来源行 ≥3，计数/错误列正确；
#   S2  health 调度器块：enabled / intervalSeconds=3 / rounds / nextRunAt；
#   S3  手动触发：响应信封 runId/source=api 与历史行一致；
#   S4  告警统计：闭集键全在，byState 之和 == total == 列表 total；
#   S5  OpenAPI：/api/detection/runs 与 /api/alerts/stats 在文档里；
#   S6  webhook 收到调度轮通知 ≥3 条（event=detection.run）；
#   S7  浏览器：巡检条渲染（上次巡检 + 下次自动巡检）、运行历史对话框
#       打开且有行、console 零错误。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
export PATH="$PATH:/home/z/.local/go/bin"

PORT=18186
WH_PORT=18187
WORK="$(mktemp -d /tmp/netops-r11.XXXXXX)"
DB="$WORK/r11.db"

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

echo "[1/6] synthdata（degrade 场景）+ serve --detect-interval 3s"
"$BIN" synthdata --out "$WORK/synth" --devices 3 --ports 1 --days 4 --scenario degrade --seed 42 > /dev/null

python3 - "$WH_PORT" > "$WORK/wh.log" 2>&1 <<'PY' &
import http.server, sys

PORT = int(sys.argv[1])

class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(n)
        with open("/tmp/r11wh-deliveries.jsonl", "a") as f:
            f.write(raw.decode("utf-8") + "\n")
        self.send_response(200)
        self.end_headers()
    def log_message(self, *a):
        pass

http.server.ThreadingHTTPServer(("127.0.0.1", PORT), H).serve_forever()
PY
WH_PID=$!
rm -f /tmp/r11wh-deliveries.jsonl

(setsid "$BIN" serve --db "$DB" --addr "127.0.0.1:$PORT" --data-dir "$WORK/data" \
  --detect-interval 3s --webhook-url "http://127.0.0.1:$WH_PORT/" > "$WORK/serve.log" 2>&1 < /dev/null &)
SERVE_PID=$(pgrep -f "netopslab serve.*:$PORT" | head -1)
for _ in $(seq 1 50); do curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && break; sleep 0.2; done

# HTTP 导入资源 + 摄入两天劣化指标（api 测试同路径）。
mkdir -p "$WORK/data"
UPLOAD=$(curl -sf -F "file=@$WORK/synth/resources.xlsx" "http://127.0.0.1:$PORT/api/imports")
BATCH=$(echo "$UPLOAD" | python3 -c "import json,sys;print(json.load(sys.stdin)['batchId'])")
SUGGESTED=$(echo "$UPLOAD" | python3 -c "import json,sys;print(json.dumps(json.load(sys.stdin)['suggestedMapping'],ensure_ascii=False))")
curl -sf -X POST "http://127.0.0.1:$PORT/api/imports/$BATCH/mapping" -H 'Content-Type: application/json' -d "$SUGGESTED" > /dev/null
for day in perf-day-01.xlsx perf-day-02.xlsx perf-day-03.xlsx perf-day-04.xlsx; do
  M=$(curl -sf -F "file=@$WORK/synth/$day" "http://127.0.0.1:$PORT/api/metrics/analyze" | python3 -c "import json,sys;print(json.dumps(json.load(sys.stdin)['suggestedMapping'],ensure_ascii=False))")
  curl -sf -F "file=@$WORK/synth/$day" -F "mapping=$M" "http://127.0.0.1:$PORT/api/metrics/imports" > /dev/null
done

# 预置一条 window-1 灵敏规则（api 测试先例：默认规则需 ≥3 天连续违规，
# 4 天劣化曲线只有尾 2 天违规——巡检回归关心的是调度闭环不是阈值语义）。
curl -sf -X POST "http://127.0.0.1:$PORT/api/detection/rules" -H 'Content-Type: application/json' \
  -d '{"metricKey":"optical_power_rx_avg_dbm","comparator":"lte","threshold":-8.1,"window":1,"severity":"warning","label":"巡检回归触发"}' > /dev/null

# CLI 预置检测（cli 来源回执 + 创建告警）。
"$BIN" detect --db "$DB" > "$WORK/detect.json"
CLI_CREATED=$(python3 -c "import json;print(json.load(open('$WORK/detect.json'))['alertsCreated'])")
CLI_RUNID=$(python3 -c "import json;print(json.load(open('$WORK/detect.json'))['runId'])")
if [[ "$CLI_CREATED" -ge 1 ]]; then ok "P0 CLI 预置检测创建 ${CLI_CREATED} 条告警（runId=${CLI_RUNID:0:12}…）"; else bad "P0 CLI alertsCreated=$CLI_CREATED"; fi

echo "[2/6] 等待调度器自动巡检（3s × ≥3 轮）"
SCHED_N=0
for _ in $(seq 1 40); do
  SCHED_N=$(curl -s "http://127.0.0.1:$PORT/api/detection/runs?pageSize=100" | python3 -c "import json,sys;print(sum(1 for r in json.load(sys.stdin)['items'] if r['source']=='scheduler'))")
  [[ "$SCHED_N" -ge 3 ]] && break
  sleep 1
done
if [[ "$SCHED_N" -ge 3 ]]; then ok "P1 调度器完成 ${SCHED_N} 轮自动巡检"; else bad "P1 scheduler 轮数=$SCHED_N"; fi

echo "[3/6] API 契约断言"
RUNS_JSON=$(curl -s "http://127.0.0.1:$PORT/api/detection/runs?pageSize=100")
python3 - "$RUNS_JSON" "$WORK/r11-assert.txt" <<'PY'
import json, sys

runs = json.loads(sys.argv[1])
items = runs.get("items", [])
res = []
sched = [r for r in items if r["source"] == "scheduler"]
cli = [r for r in items if r["source"] == "cli"]
res.append(("S1a 历史含 cli 来源行", len(cli) >= 1, f"cli={len(cli)}"))
res.append(("S1b 历史含 scheduler 来源行 ≥3", len(sched) >= 3, f"sched={len(sched)}"))
res.append(("S1c scheduler 轮零错误", all(r["error"] == "" for r in sched), ""))
res.append(("S1d scheduler 轮 findings≥1（持续违规仍在巡检视野）", all(r["findings"] >= 1 for r in sched) if sched else False, ""))
res.append(("S1e scheduler 轮幂等（created=0，告警已由 CLI 预置）", all(r["alertsCreated"] == 0 for r in sched) if sched else False, ""))
res.append(("S1f 列契约：evaluatedAt/durationMs/from/to 存在", all(("evaluatedAt" in r and "durationMs" in r and "from" in r and "to" in r) for r in items) if items else False, ""))
res.append(("S1g 排序：started_at 降序", items == sorted(items, key=lambda r: r["startedAt"], reverse=True) if items else True, ""))
res.append(("S1h total ≥ cli+sched", runs.get("total", 0) >= len(cli) + len(sched), f"total={runs.get('total')}"))

with open(sys.argv[2], "w", encoding="utf-8") as f:
    for name, passed, note in res:
        f.write(f"{'ok' if passed else 'bad'}\t{name}\t{note}\n")
PY
while IFS=$'\t' read -r st name note; do
  if [[ "$st" == "ok" ]]; then ok "$name"; else bad "$name $note"; fi
done < "$WORK/r11-assert.txt"

# S2 health 调度器块。
HEALTH=$(curl -s "http://127.0.0.1:$PORT/api/health")
python3 - "$HEALTH" "$WORK/r11-assert2.txt" <<'PY'
import json, sys

h = json.loads(sys.argv[1]).get("scheduler", {})
res = [
    ("S2a scheduler.enabled", h.get("enabled") is True, ""),
    ("S2b intervalSeconds=3", h.get("intervalSeconds") == 3, f"got {h.get('intervalSeconds')}"),
    ("S2c rounds ≥3", h.get("rounds", 0) >= 3, f"rounds={h.get('rounds')}"),
    ("S2d nextRunAt 非空", bool(h.get("nextRunAt")), ""),
    ("S2e running 布尔", isinstance(h.get("running"), bool), ""),
]
with open(sys.argv[2], "w", encoding="utf-8") as f:
    for name, passed, note in res:
        f.write(f"{'ok' if passed else 'bad'}\t{name}\t{note}\n")
PY
while IFS=$'\t' read -r st name note; do
  if [[ "$st" == "ok" ]]; then ok "$name"; else bad "$name $note"; fi
done < "$WORK/r11-assert2.txt"

# S3 手动触发信封 + 历史一致性。
MANUAL=$(curl -s -X POST "http://127.0.0.1:$PORT/api/detection/run" -H 'Content-Type: application/json' -d '{}')
MANUAL_RUNID=$(echo "$MANUAL" | python3 -c "import json,sys;print(json.load(sys.stdin)['runId'])")
MANUAL_SRC=$(echo "$MANUAL" | python3 -c "import json,sys;print(json.load(sys.stdin)['source'])")
FOUND=$(curl -s "http://127.0.0.1:$PORT/api/detection/runs?pageSize=100" | python3 -c "import json,sys;print(any(r['runId']=='$MANUAL_RUNID' and r['source']=='$MANUAL_SRC' for r in json.load(sys.stdin)['items']))")
if [[ "$MANUAL_SRC" == "api" && "$FOUND" == "True" ]]; then
  ok "S3 手动触发信封（source=api, runId 落库一致）"
else
  bad "S3 src=$MANUAL_SRC found=$FOUND"
fi

# S4 告警统计闭集 + 与列表一致。
STATS=$(curl -s "http://127.0.0.1:$PORT/api/alerts/stats")
LIST_TOTAL=$(curl -s "http://127.0.0.1:$PORT/api/alerts?pageSize=1" | python3 -c "import json,sys;print(json.load(sys.stdin)['total'])")
python3 - "$STATS" "$LIST_TOTAL" "$WORK/r11-assert3.txt" <<'PY'
import json, sys

s = json.loads(sys.argv[1])
list_total = int(sys.argv[2])
by = s.get("byState", {})
sev = s.get("activeBySeverity", {})
res = [
    ("S4a byState 三键闭集", all(k in by for k in ("firing", "acked", "cleared")), f"{by}"),
    ("S4b activeBySeverity 三键闭集", all(k in sev for k in ("error", "warning", "info")), f"{sev}"),
    ("S4c byState 之和 == total", sum(by.values()) == s.get("total", -1), f"{sum(by.values())} vs {s.get('total')}"),
    ("S4d stats total == 列表 total", s.get("total") == list_total, f"{s.get('total')} vs {list_total}"),
    ("S4e firing ≥1（degrade 场景）", by.get("firing", 0) >= 1, f"firing={by.get('firing')}"),
]
with open(sys.argv[3], "w", encoding="utf-8") as f:
    for name, passed, note in res:
        f.write(f"{'ok' if passed else 'bad'}\t{name}\t{note}\n")
PY
while IFS=$'\t' read -r st name note; do
  if [[ "$st" == "ok" ]]; then ok "$name"; else bad "$name $note"; fi
done < "$WORK/r11-assert3.txt"

# S5 OpenAPI 覆盖。
SPEC=$(curl -s "http://127.0.0.1:$PORT/api/openapi.json")
for path in "/api/detection/runs" "/api/alerts/stats"; do
  if echo "$SPEC" | python3 -c "import json,sys;sys.exit(0 if '$path' in json.load(sys.stdin)['paths'] else 1)"; then
    ok "S5 OpenAPI 含 $path"
  else
    bad "S5 OpenAPI 缺 $path"
  fi
done

# S6 webhook 收到调度轮通知。
WH_N=$(wc -l < /tmp/r11wh-deliveries.jsonl 2>/dev/null || echo 0)
WH_SCHED=$(python3 -c "
import json
try:
    lines = [l for l in open('/tmp/r11wh-deliveries.jsonl', encoding='utf-8').read().splitlines() if l]
    print(sum(1 for l in lines if json.loads(l).get('event') == 'detection.run'))
except FileNotFoundError:
    print(0)")
if [[ "$WH_SCHED" -ge 3 ]]; then
  ok "S6 webhook 调度轮通知 ${WH_SCHED} 条（总投递 ${WH_N}）"
else
  bad "S6 webhook 调度通知=$WH_SCHED"
fi

echo "[4/6] 浏览器：/alerts 巡检条与运行历史"
agent-browser set viewport 1600 900 >/dev/null
agent-browser open "http://127.0.0.1:$PORT/alerts" >/dev/null
agent-browser wait --fn "!!document.querySelector('.patrol-bar')" --timeout 20000 >/dev/null 2>&1
agent-browser wait 800 >/dev/null
# 导航后武装 console 监听（整页导航重置 window——r6 惯例）
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null
jeval() { agent-browser eval "$1" 2>/dev/null | tr -d '"'; }
BAR="$(jeval "(() => { const b = document.querySelector('.patrol-bar'); return b ? (b.textContent||'').trim().slice(0,200) : 'none'; })()")"
if [[ "$BAR" != "none" && "$BAR" == *"上次巡检"* && "$BAR" == *"下次自动巡检"* && "$BAR" == *"运行历史"* ]]; then
  ok "S7a 巡检条渲染（上次回执 + 下次调度 + 历史入口）"
else
  bad "S7a bar=$BAR"
fi
if [[ "$BAR" == *"每 3 秒"* ]]; then
  ok "S7b 调度间隔人话显示（每 3 秒一轮）"
else
  bad "S7b 间隔文案缺失"
fi
HIST="$(jeval "(() => { const b = [...document.querySelectorAll('.patrol-bar .btn')].find(x => (x.textContent||'').trim() === '运行历史'); if (!b) return 'none'; b.click(); return 'ok'; })()")"
agent-browser wait 600 >/dev/null
MODAL_ROWS="$(jeval "(() => { const m = document.querySelector('.modal'); return m ? String(m.querySelectorAll('tbody tr').length) : 'none'; })()")"
MODAL_SRC="$(jeval "(() => { const m = document.querySelector('.modal'); return m && (m.textContent||'').includes('定时调度') ? 'yes' : 'no'; })()")"
if [[ "$HIST" == "ok" && "$MODAL_ROWS" != "none" && "$MODAL_ROWS" != "0" && "$MODAL_SRC" == "yes" ]]; then
  ok "S7c 运行历史对话框（${MODAL_ROWS} 行，含定时调度来源）"
else
  bad "S7c hist=$HIST rows=$MODAL_ROWS src=$MODAL_SRC"
fi
CONSOLE="$(jeval "window.__errs ? window.__errs.length : -1")"
if [[ "$CONSOLE" == "0" ]]; then ok "S7d console 零错误"; else bad "S7d console errors=$CONSOLE"; fi

echo "[5/6] Overview 告警分布卡"
agent-browser open "http://127.0.0.1:$PORT/" >/dev/null
agent-browser wait --fn "!!document.querySelector('.stat-grid')" --timeout 20000 >/dev/null 2>&1
agent-browser wait 800 >/dev/null
OV="$(jeval "(() => { const cards = [...document.querySelectorAll('.stat-grid .stat-label, .stat-grid .stat-card')]; const hit = cards.find(c => (c.textContent||'').includes('触发中告警')); return hit ? (hit.closest('.stat-card') ? (hit.closest('.stat-card').textContent||'') : hit.textContent) : 'none'; })()")"
if [[ "$OV" != "none" && "$OV" == *"待处置"* && "$OV" == *"已确认"* ]]; then
  ok "S8 Overview 告警分布卡（stats 端点驱动：待处置矩阵 + 已确认数）"
else
  bad "S8 ov=$OV"
fi

echo "[6/6] CLI 运行历史回读"
"$BIN" query --db "$DB" stats > /dev/null 2>&1 || true
CLI_ROWS=$(curl -s "http://127.0.0.1:$PORT/api/detection/runs?pageSize=100" | python3 -c "import json,sys;print(sum(1 for r in json.load(sys.stdin)['items'] if r['runId']=='$CLI_RUNID'))")
if [[ "$CLI_ROWS" == "1" ]]; then ok "S9 CLI 回执行在历史中（runId 一致）"; else bad "S9 cli 行数=$CLI_ROWS"; fi

echo
echo "结果：PASS=$PASS FAIL=$FAIL"
[[ "$FAIL" -eq 0 ]] && echo "R11 SCHEDULER E2E: ALL GREEN"
exit "$FAIL"
