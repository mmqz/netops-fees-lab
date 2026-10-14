#!/usr/bin/env bash
# NetOps Lab — 真实公开数据演示脚本（v0.8.0，scripts/realdata 三件套产出的数据）。
#
# 用法：
#   ./scripts/demo-real.sh            # 全流程（导入 32 工作簿 + PTP 脚本 + 7 日指标 + 检测 + 物化）
#
# 数据构成（诚实披露，详见 testdata/realdata/manifest.json 与 docs/data-sources.md）：
#   真实：局站（OSM 地名/坐标/id）、基站（OpenCelliD 1,198 站/1,851 小区）、
#         光缆走廊 A/Z 局站与锚点链里程（哈大齐/哈牡/哈佳/佳牡/齐加/加漠/哈伊 7 干线）
#   模拟：机房/纤芯占用/波道/电路/客户/业务等台账细节（真实位置 + 示范值）
#
# 流程：
#   1. analyze → suggestedMapping → import --map（31 本工作簿按依赖序）
#   2. ingest 7 日端口性能（哈大齐劣化场景）
#   3. detect 检测运行 → firing 告警（丢包率连续越限）→ run_state 联动
#   4. lab workspace 物化（30 设备真实坐标）+ traverse 影响分析（哈大齐光缆下钻）
#   5. stats 汇总 + 资源/关系类型分布
#
# 每次运行使用全新临时数据库；结束后打印路径与核对数。

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$REPO_ROOT/bin/netopslab"
REALDATA="$REPO_ROOT/testdata/realdata"
WORK="$(mktemp -d /tmp/netops-demo-real.XXXXXX)"
DB="$WORK/netops.db"

if [[ ! -x "$BIN" ]]; then
  echo "[0/5] 未发现二进制，开始构建 bin/netopslab ..."
  (cd "$REPO_ROOT" && go build -o "$BIN" ./cmd/netopslab)
fi
echo "[0/5] 工作目录：$WORK"

# 导入依赖序（ref 先于被引用落库；见 workbooks.py 文档）
# W30（issues #40/#42/#45）：基站表引用设备（上联最近邻）与客户（服务客户），
# 故移至设备之后；端口表引用板卡（mounted_on），板卡先行；保护组在电路后。
ORDER=(
  sites machine_rooms customers vpn_instances rings igp_domains devices boards
  base_stations ports racks odf odf_terminals ducts manholes poles
  optical_cables cable_segments splice_boxes fibers otn_ochs sdh_timeslots
  circuits protection_groups ippools services leased_lines bbus rrus olts pon_ports onus
)

extract_map() {
  python3 - "$1" "$2" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    analysis = json.load(f)
with open(sys.argv[2], "w", encoding="utf-8") as f:
    json.dump(analysis["suggestedMapping"], f, ensure_ascii=False, indent=2)
PY
}

# —— 1. 导入 31 本工作簿 ——
total_imported=0
total_failed=0
for wb in "${ORDER[@]}"; do
  f="$REALDATA/$wb.xlsx"
  [[ -f "$f" ]] || { echo "缺失工作簿：$f（先运行 scripts/realdata/*.py）"; exit 1; }
  "$BIN" analyze -file "$f" > "$WORK/analysis-$wb.json"
  extract_map "$WORK/analysis-$wb.json" "$WORK/map-$wb.json"
  "$BIN" import --file "$f" --map "$WORK/map-$wb.json" --db "$DB" \
    > "$WORK/import-$wb.json" 2> "$WORK/import-$wb.err" || true
  python3 - "$WORK/import-$wb.json" "$wb" <<'PY' >> "$WORK/import-summary.txt"
import json, sys
try:
    d = json.load(open(sys.argv[1], encoding="utf-8"))
    s = d.get("summary", d)
    imp = s.get("rowsImported", s.get("imported", 0))
    skp = s.get("rowsSkipped", s.get("skipped", 0))
    fail = s.get("rowsFailed", s.get("failed", 0))
    print(f"{sys.argv[2]}\t{imp}\t{skp}\t{fail}")
except Exception as e:
    print(f"{sys.argv[2]}\tERR\t0\t0\t{e}")
PY
done
# PTP 配置脚本导入（W30 #40：ptp_port 类型数据缺口——复用 samples 的解析通道，
# 与工作簿同一 analyze → suggestedMapping → import 流程）
"$BIN" analyze -file "$REPO_ROOT/samples/ptp-config-script.txt" > "$WORK/analysis-ptp.json"
extract_map "$WORK/analysis-ptp.json" "$WORK/map-ptp.json"
"$BIN" import --file "$REPO_ROOT/samples/ptp-config-script.txt" \
  --map "$WORK/map-ptp.json" --db "$DB" > "$WORK/import-ptp.json" 2> "$WORK/import-ptp.err" || true
PTP_ROWS=$(python3 -c "import json;print(json.load(open('$WORK/import-ptp.json'))['summary']['rowsImported'])")
echo "      PTP 配置脚本：ptp_port $PTP_ROWS 行（issue #40 数据缺口闭环）"
echo "[1/5] 32 本工作簿 + PTP 脚本导入完成"

total_imported=$(awk -F'\t' '{s+=$2} END {print s}' "$WORK/import-summary.txt")
total_failed=$(awk -F'\t' '{s+=$4} END {print s}' "$WORK/import-summary.txt")
echo "      合计 imported=$total_imported failed=$total_failed"
if [[ "$total_failed" != "0" ]]; then
  echo "      ⚠ 存在失败行，明细："
  awk -F'\t' '$4 != 0' "$WORK/import-summary.txt"
fi

# —— 2. 摄入 7 日端口性能 ——
for day in 01 02 03 04 05 06 07; do
  "$BIN" ingest --db "$DB" --file "$REALDATA/perf-day-$day.xlsx" \
    --map "$REALDATA/ingest-mapping.json" > "$WORK/ingest-$day.json"
done
echo "[2/5] 7 日端口性能摄入完成（30 设备 × 3 端口/日）"

# —— 3. 检测运行 ——
"$BIN" detect --db "$DB" > "$WORK/detect.json"
python3 - "$WORK/detect.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1], encoding="utf-8"))
alerts = d.get("alerts", d.get("summary", {}))
print("[3/5] 检测运行完成：", json.dumps(alerts, ensure_ascii=False)[:300])
PY

# —— 4. 物化工作台 ——
"$BIN" lab workspace create --db "$DB" --name "黑龙江真实公开数据演示" \
  --desc "OSM 局站/走廊 + OpenCelliD 基站；台账细节为示范值（manifest 披露）" \
  > "$WORK/workspace.json"
python3 - "$WORK/workspace.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1], encoding="utf-8"))
c = d.get("counts", {})
print(f"[4/5] 工作台物化：{d.get('workspaceId', '?')} "
      f"设备 {c.get('devices', '?')} 链路 {c.get('links', '?')} 接口 {c.get('interfaces', '?')}")
PY

# —— 5. serve + HTTP 演示（run-state / impact / traverse / stats）——
PORT=$(python3 -c "import socket;s=socket.socket();s.bind(('127.0.0.1',0));print(s.getsockname()[1]);s.close()")
"$BIN" serve --db "$DB" --addr "127.0.0.1:$PORT" > "$WORK/serve.log" 2>&1 &
SERVE_PID=$!
trap 'kill $SERVE_PID 2>/dev/null || true' EXIT
for _ in $(seq 1 50); do
  curl -sf "http://127.0.0.1:$PORT/api/health" > /dev/null 2>&1 && break
  sleep 0.2
done

# 5a. 解析 OC-HDQ（哈大齐光缆）资源 id
OC_ID=$(curl -sf "http://127.0.0.1:$PORT/api/resources?type=optical_cable&q=OC-HDQ" \
  | python3 -c "import json,sys;d=json.load(sys.stdin);items=d.get('items',d.get('data',[]));print(items[0]['id'])")
echo "      哈大齐光缆资源 id：$OC_ID"

# 5b. 置 down（manual 观测——故障注入演示）
curl -sf -X POST "http://127.0.0.1:$PORT/api/resources/$OC_ID/run-state" \
  -H 'Content-Type: application/json' \
  -d '{"runState":"down","source":"manual","reason":"演示：哈大齐光缆割接故障"}' > "$WORK/runstate.json"

# 5c. 影响面（up：波及业务/客户）与根因候选（down）
curl -sf "http://127.0.0.1:$PORT/api/resources/$OC_ID/impact?direction=up" > "$WORK/impact-up.json"
curl -sf "http://127.0.0.1:$PORT/api/resources/$OC_ID/impact?direction=down" > "$WORK/impact-down.json"
python3 - "$WORK/impact-up.json" "$WORK/impact-down.json" <<'PY'
import json, sys
up = json.load(open(sys.argv[1], encoding="utf-8"))
down = json.load(open(sys.argv[2], encoding="utf-8"))
imp = up.get("impacted", [])
cand = down.get("candidates", [])
types = {}
for it in imp:
    t = (it.get("node") or {}).get("type", "?")
    types[t] = types.get(t, 0) + 1
print(f"      影响面(up) {len(imp)} 节点：{types}")
print(f"      根因候选(down) {len(cand)} 节点")
PY

# 5d. 告警清单（哈大齐劣化端口的 firing 告警）
curl -sf "http://127.0.0.1:$PORT/api/alerts?state=firing" > "$WORK/alerts.json"
python3 - "$WORK/alerts.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1], encoding="utf-8"))
items = d.get("items", [])
print(f"      firing 告警 {len(items)} 条" + (f"：{items[0].get('message', '')[:60]}…" if items else ""))
PY

# 5e. 汇总统计
curl -sf "http://127.0.0.1:$PORT/api/stats" > "$WORK/stats.json"
python3 - "$WORK/stats.json" "$WORK/import-summary.txt" <<'PY'
import json, sys
d = json.load(open(sys.argv[1], encoding="utf-8"))
by_type = d.get("byType", {})
total = d.get("totalResources", sum(by_type.values()) if isinstance(by_type, dict) else "?")
print(f"[5/5] 资源总数 {total}；类型分布（前 8）：")
if isinstance(by_type, dict):
    for k in sorted(by_type, key=lambda x: -by_type[x])[:8]:
        print(f"      {k:16s} {by_type[k]}")
brt = d.get("byRelationType", {})
n_rel = sum(brt.values()) if isinstance(brt, dict) else 0
print(f"      关系总数 {n_rel}；byRelationType 前 8：")
if isinstance(brt, dict):
    for k in sorted(brt, key=lambda x: -brt[x])[:8]:
        print(f"      {k:24s} {brt[k]}")
PY

kill $SERVE_PID 2>/dev/null || true
trap - EXIT
echo "完成。数据库：$DB"
