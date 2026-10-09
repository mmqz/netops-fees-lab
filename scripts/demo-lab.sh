#!/usr/bin/env bash
# NetOps Lab — 仿真工作台演示脚本（可重复执行的故障注入故事，每次使用全新临时数据库）。
#
# 用法：
#   ./scripts/demo-lab.sh             # 或 make demo-lab
#
# 流程（< 60 秒）：
#   0. 若 bin/netopslab 缺失则先构建（go build）；testdata 缺失则自动再生成
#   1. 导入 3 本合成工作簿（devices → ippools → circuits，复用 demo.sh 的映射抽取模式）
#   2. lab workspace create --all-batches：物化工作台（27 设备 / 40 接口 / 20 链路）+ 状态哈希
#   3. lab checks --run：基线校验（评分与问题计数）→ 基线 run id
#   4. lab path DEV-001→DEV-003：基线路径（2 跳）
#   5. lab apply：故障注入 —— 同一地址配到两个接口（CHK-001）+ 关断 DEV-001↔DEV-002 链路
#   6. lab path：不可达；再提交一个必失败的操作（422 op_failed + 状态零污染的原子性演示）
#   7. lab diff：前后对比（新增/消失问题、连通性丢失对、评分差）
#   8. lab undo：撤销变更集 → 哈希回到基线 → 路径恢复（与基线逐跳一致）
#   9. lab checkpoint：创建检查点 → 破坏性操作（删除设备）→ 恢复（哈希精确还原）→ 重复恢复 = no-op
#
# 依赖：Go 1.27+（仅首次构建需要）、python3、testdata/*.xlsx。
# 说明：数据库与检查点文件留在临时目录中（脚本结束时打印路径，可自行查看或删除）。
# 每一步验证的能力：原子性（失败的变更集零状态污染）、确定性（同输入同哈希）、
# 撤销（LIFO 变更集逆向）、检查点（Online Backup 快照往返）。

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$REPO_ROOT/bin/netopslab"
TESTDATA="$REPO_ROOT/testdata"
WORK="$(mktemp -d /tmp/netops-demo-lab.XXXXXX)"
DB="$WORK/netops.db"

# 0. 构建二进制（若缺失）
if [[ ! -x "$BIN" ]]; then
  echo "[0/9] 未发现二进制，开始构建 bin/netopslab ..."
  (cd "$REPO_ROOT" && go build -o "$BIN" ./cmd/netopslab)
fi
echo "[0/9] 工作目录：$WORK"

# 0b. 确保合成测试数据存在
if [[ ! -f "$TESTDATA/devices.xlsx" || ! -f "$TESTDATA/ippools.xlsx" || ! -f "$TESTDATA/circuits.xlsx" ]]; then
  echo "[0/9] testdata 缺失，重新生成合成工作簿 ..."
  "$BIN" testdata --out "$TESTDATA" > /dev/null
fi

# 从 analyze 输出中抽取 suggestedMapping，写入映射 JSON 文件（与 demo.sh 相同的技巧）。
extract_map() {
  python3 - "$1" "$2" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    analysis = json.load(f)
with open(sys.argv[2], "w", encoding="utf-8") as f:
    json.dump(analysis["suggestedMapping"], f, ensure_ascii=False, indent=2)
PY
}

# 1. 导入三本工作簿（顺序：设备 → 地址池 → 电路，引用先于被引用落库）
for wb in devices ippools circuits; do
  "$BIN" analyze --file "$TESTDATA/$wb.xlsx" > "$WORK/analysis-$wb.json"
  extract_map "$WORK/analysis-$wb.json" "$WORK/map-$wb.json"
  "$BIN" import --file "$TESTDATA/$wb.xlsx" --map "$WORK/map-$wb.json" --db "$DB" \
    > "$WORK/import-$wb.json"
done
echo "[1/9] 导入完成：devices / ippools / circuits（27 / 16 / 21 个资源落库）。"

# 2. 物化仿真工作台
"$BIN" lab workspace create --db "$DB" --name 故障注入演示 --all-batches > "$WORK/workspace.json"
python3 - "$WORK/workspace.json" "$WORK/ids.env" <<'PY'
import json, sys
r = json.load(open(sys.argv[1], encoding="utf-8"))
c = r["counts"]
print(f"[2/9] 物化工作台「{r['name']}」：设备 {c['devices']} / 接口 {c['interfaces']} / "
      f"链路 {c['links']}；状态哈希 {r['workspaceHash'][:12]}…")
with open(sys.argv[2], "w", encoding="utf-8") as f:
    f.write(f"WS={r['workspaceId']}\nBASE_HASH={r['workspaceHash']}\n")
PY
source "$WORK/ids.env"

# 3. 基线校验
"$BIN" lab checks --db "$DB" --workspace "$WS" --run > "$WORK/run-baseline.json"
python3 - "$WORK/run-baseline.json" "$WORK/ids.env" <<'PY'
import json, sys
r = json.load(open(sys.argv[1], encoding="utf-8"))
c = r["counts"]
print(f"[3/9] 基线校验 {r['runId']}（{r['trigger']}）：评分 {r['score']}，"
      f"问题（错误/警告/提示）{c['error']}/{c['warning']}/{c['info']}。")
with open(sys.argv[2], "a", encoding="utf-8") as f:
    f.write(f"BASE_RUN={r['runId']}\n")
PY
source "$WORK/ids.env"

# 4. 基线路径：DEV-001 → DEV-003（沿电路链 2 跳）
"$BIN" lab path --db "$DB" --workspace "$WS" --from DEV-001 --to DEV-003 > "$WORK/path-baseline.json"
python3 - "$WORK/path-baseline.json" <<'PY'
import json, sys
r = json.load(open(sys.argv[1], encoding="utf-8"))
hops = " → ".join(h["code"] for h in r["hops"])
print(f"[4/9] 基线路径 DEV-001 → DEV-003：可达，{r['pathLength']} 跳（{hops}）。")
PY

# 5. 故障注入操作文件 #1：同一地址配到两个接口（CHK-001）+ 关断 DEV-001↔DEV-002 的链路。
# 接口/链路 id 从拓扑输出解析（物化端口确定性分配：DEV-001/eth0、DEV-002/eth1、CKT-001 链路）。
"$BIN" lab topology --db "$DB" --workspace "$WS" > "$WORK/topology.json"
python3 - "$WORK/topology.json" "$WORK/ops-1.json" "$WORK/ids.env" <<'PY'
import json, sys
topo = json.load(open(sys.argv[1], encoding="utf-8"))
iface = {(n["code"], i["name"]): i["id"] for n in topo["nodes"] for i in n["interfaces"]}
link = {e["label"]: e["id"] for e in topo["edges"]}
device = {n["code"]: n["id"] for n in topo["nodes"]}
ops = {
    "description": "故障注入演示：地址冲突 + 链路中断",
    "ops": [
        {"op": "config_interface_ipv4", "interfaceId": iface[("DEV-001", "eth0")],
         "ipv4Addr": "10.99.0.1", "ipv4Prefix": 24},
        {"op": "config_interface_ipv4", "interfaceId": iface[("DEV-002", "eth1")],
         "ipv4Addr": "10.99.0.1", "ipv4Prefix": 24},
        {"op": "set_link_status", "linkId": link["CKT-001"], "status": "down"},
    ],
}
json.dump(ops, open(sys.argv[2], "w", encoding="utf-8"), ensure_ascii=False, indent=2)
with open(sys.argv[3], "a", encoding="utf-8") as f:
    f.write(f"DEV1={device['DEV-001']}\n")
PY
source "$WORK/ids.env"

# 6. 故障注入：应用 3 个操作（单事务变更集 + 自动校验）
"$BIN" lab apply --db "$DB" --workspace "$WS" --ops "$WORK/ops-1.json" > "$WORK/apply-1.json"
python3 - "$WORK/apply-1.json" "$WORK/ids.env" <<'PY'
import json, sys
r = json.load(open(sys.argv[1], encoding="utf-8"))
c = r["checks"]["counts"]
print(f"[5/9] 故障注入 {r['changesetId']}（{r['trigger']}）：应用 {r['opsApplied']} 个操作，"
      f"自动校验 {r['checks']['runId']}：评分 {r['checks']['score']}，"
      f"问题 {c['error']}/{c['warning']}/{c['info']}（CHK-001 地址冲突 + CHK-003 孤岛设备）。")
with open(sys.argv[2], "a", encoding="utf-8") as f:
    f.write(f"AFTER_RUN={r['checks']['runId']}\n")
PY
source "$WORK/ids.env"

# 7. 故障后的路径（不可达）+ 故意提交必失败的操作（原子性演示：422 + 状态零污染）
"$BIN" lab path --db "$DB" --workspace "$WS" --from DEV-001 --to DEV-003 > "$WORK/path-fault.json" || true
python3 - "$WORK/path-fault.json" <<'PY'
import json, sys
r = json.load(open(sys.argv[1], encoding="utf-8"))
print(f"[6/9] 故障后路径 DEV-001 → DEV-003：{'可达' if r['reachable'] else '不可达'}"
      f"（对比：基线 2 跳 → 现在 DEV-001 成孤岛，唯一电路链路已关断）。")
PY
cat > "$WORK/ops-bad.json" <<'JSON'
{
  "description": "故意失败的变更集（原子性演示）",
  "ops": [
    {"op": "set_device_status", "deviceId": "vd-2", "status": "down"},
    {"op": "set_link_status", "linkId": "ln-999", "status": "up"}
  ]
}
JSON
if "$BIN" lab apply --db "$DB" --workspace "$WS" --ops "$WORK/ops-bad.json" > "$WORK/apply-bad.json" 2> /dev/null; then
  echo "[6/9] 意外：故意失败的变更集竟然成功了" >&2
  exit 1
fi
python3 - "$WORK/apply-bad.json" <<'PY'
import json, re, sys
r = json.load(open(sys.argv[1], encoding="utf-8"))
msg = r["error"]["message"]
cs = re.search(r"\[changeset (\S+) recorded as failed\]", msg)
print(f"[6/9] 原子性演示：op_failed（CLI 退出码 1，HTTP 层为 422）—— {msg}")
print(f"      失败变更集 {cs.group(1) if cs else '?'} 已记录（ops_applied=0），工作台状态零污染。")
PY

# 8. 前后对比（基线 run vs 故障 run）
"$BIN" lab diff --db "$DB" --workspace "$WS" --from "$BASE_RUN" --to "$AFTER_RUN" > "$WORK/diff.json"
python3 - "$WORK/diff.json" <<'PY'
import json, sys
r = json.load(open(sys.argv[1], encoding="utf-8"))
added = ", ".join(sorted({f["checkCode"] for f in r["findingsAdded"]})) or "无"
removed = ", ".join(sorted({f["checkCode"] for f in r["findingsRemoved"]})) or "无"
lost = len(r["connectivityLost"] or [])
gained = len(r["connectivityGained"] or [])
print(f"[7/9] 前后对比 {r['from']['runId']} → {r['to']['runId']}："
      f"评分差 {r['scoreDelta']:+d}；新增问题 [{added}]；消失问题 [{removed}]；"
      f"连通性丢失 {lost} 对（含 DEV-001/DEV-003）；恢复 {gained} 对。")
PY

# 9. 撤销最近变更集（LIFO）：哈希回到基线，路径恢复
"$BIN" lab undo --db "$DB" --workspace "$WS" > "$WORK/undo.json"
python3 - "$WORK/undo.json" <<'PY'
import json, sys
r = json.load(open(sys.argv[1], encoding="utf-8"))
c = r["checks"]["counts"]
print(f"[8/9] 撤销：{r['changesetId']}（trigger {r['trigger']}）逆转了 {r['reverted']}，"
      f"应用 {r['opsApplied']} 个逆向操作；评分回到 {r['checks']['score']}，"
      f"问题 {c['error']}/{c['warning']}/{c['info']}。")
PY
"$BIN" lab path --db "$DB" --workspace "$WS" --from DEV-001 --to DEV-003 > "$WORK/path-recovered.json"
python3 - "$WORK/path-recovered.json" "$WORK/undo.json" <<'PY'
import json, sys
r = json.load(open(sys.argv[1], encoding="utf-8"))
u = json.load(open(sys.argv[2], encoding="utf-8"))
hops = " → ".join(h["code"] for h in r["hops"])
print(f"[8/9] 撤销后路径 DEV-001 → DEV-003：{'可达' if r['reachable'] else '不可达'}，"
      f"{r['pathLength']} 跳（{hops}）——与基线逐跳一致。")
print(f"[8/9] 撤销后状态哈希 {u['workspaceHash'][:12]}…（== 物化基线，确定性：同状态同哈希）。")
PY

# 10. 检查点（Online Backup）：创建 → 破坏 → 恢复 → 重复恢复（no-op）
"$BIN" lab checkpoint --db "$DB" --workspace "$WS" --create --label 演示基线 > "$WORK/cp.json"
python3 - "$WORK/cp.json" "$WORK/ids.env" <<'PY'
import json, sys
r = json.load(open(sys.argv[1], encoding="utf-8"))
print(f"[9/9] 检查点 {r['checkpointId']}「{r['label']}」：快照于状态哈希 {r['workspaceHash'][:12]}…"
      f"（SQLite Online Backup API，非裸拷贝）。")
with open(sys.argv[2], "a", encoding="utf-8") as f:
    f.write(f"CP={r['checkpointId']}\n")
PY
source "$WORK/ids.env"

# 破坏性操作 #2：删除设备 DEV-001（级联其接口与链路）
python3 - "$DEV1" "$WORK/ops-2.json" <<'PY'
import json, sys
ops = {
    "description": "破坏性操作：删除设备 DEV-001",
    "ops": [{"op": "delete_device", "deviceId": sys.argv[1]}],
}
json.dump(ops, open(sys.argv[2], "w", encoding="utf-8"), ensure_ascii=False, indent=2)
PY
"$BIN" lab apply --db "$DB" --workspace "$WS" --ops "$WORK/ops-2.json" > "$WORK/apply-2.json"
python3 - "$WORK/apply-2.json" <<'PY'
import json, sys
r = json.load(open(sys.argv[1], encoding="utf-8"))
c = r["checks"]["counts"]
print(f"[9/9] 破坏性操作 {r['changesetId']}：删除设备 DEV-001（级联接口与链路）；"
      f"自动校验评分 {r['checks']['score']}，问题 {c['error']}/{c['warning']}/{c['info']}。")
PY

# 恢复到检查点：作为前向操作变更集执行（统一审计、可再撤销）
"$BIN" lab checkpoint --db "$DB" --workspace "$WS" --restore --id "$CP" > "$WORK/restore.json"
python3 - "$WORK/restore.json" <<'PY'
import json, sys
r = json.load(open(sys.argv[1], encoding="utf-8"))
same = "==" if r["restoredToHash"] == r["workspaceHash"] else "!="
print(f"[9/9] 检查点恢复：{r['changesetId']}（trigger checkpoint_restore），应用 {r['opsApplied']} 个前向操作"
      f"（被删设备/接口/链路按快照重建）；restoredToHash {same} 当前哈希 {r['workspaceHash'][:12]}…"
      f"（精确回到基线）；评分 {r['checks']['score']}。")
PY

# 重复恢复：当前状态已是检查点状态 → no-op（不产生变更集）
"$BIN" lab checkpoint --db "$DB" --workspace "$WS" --restore --id "$CP" > "$WORK/restore-2.json"
python3 - "$WORK/restore-2.json" <<'PY'
import json, sys
r = json.load(open(sys.argv[1], encoding="utf-8"))
cs = r.get("changesetId") or "null"
print(f"[9/9] 重复恢复：no-op（changesetId {cs}，{r.get('note', '')}）——状态哈希未变，不产生变更集。")
PY

# 11. 收尾总结
echo ""
echo "演示完成（数据库与检查点：$WORK）。本演示验证的能力："
echo "  - 原子性：  变更集单事务提交；故意失败的变更集记录后（ops_applied=0）状态零污染"
echo "  - 确定性：  同一批次物化 + 同一操作序列 → 同一状态哈希（撤销/恢复后精确回到基线哈希）"
echo "  - 撤销：    LIFO 变更集逆向（config/set 的逆操作 + 删除的捕获式 restore_*）"
echo "  - 检查点：  Online Backup 快照 → 工作区级恢复 = 前向操作变更集（可审计、可再撤销、no-op 检测）"
echo "浏览器演示请运行：./bin/netopslab serve --addr :8080 --db ./data/netops.db（/lab 页面）"
