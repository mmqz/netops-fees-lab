#!/usr/bin/env bash
# NetOps Lab — 地理坐标演示脚本（黑龙江真实网络孪生，geo pipeline v6）。
#
# 用法：
#   ./scripts/demo-geo-lab.sh
#
# 流程（< 30 秒，可重复执行，每次使用全新临时数据库）：
#   0. 若 bin/netopslab 缺失则先构建；testdata/geodevices.xlsx + geocircuits.xlsx
#      缺失则用 cmd/testdatagen -geo 补齐（既有 4 个合成工件不受影响）
#   1. 导入 2 本地理工作簿（geodevices → geocircuits，复用 demo-lab.sh 的映射抽取模式；
#      设备表带 经度/纬度 分列 → 资源属性 geo_lat/geo_lng）
#   2. lab workspace create --desc：物化工作台「黑龙江-地理孪生演示」
#      （10 设备 / 10 链路 / 20 接口 + 状态哈希；漠河站停用 → down）
#   3. lab apply：为 GD-202366 基站归属电路两端配置 IPv4（10.61.0.1/30 与 10.61.0.2/30，
#      接口 id 从拓扑动态解析）
#   4. lab apply：注入一处中途干线故障 —— CKT-QQH-JGDC-10G（齐齐哈尔→加格达奇）
#      set_link_status down，地图中段出现红色故障链路
#   5. 启动 serve（随机空闲端口）→ curl 拓扑 API → 打印携带 geoLat/geoLng 的节点样例
#      （哈尔滨枢纽 / 漠河站）+ workspaceHash → 关闭服务
#
# 依赖：Go 1.27+（仅构建需要）、python3、curl。
# 说明：坐标为真实站点的演示近似值（文档与 fixtures 注释中均有说明）；
#       数据库留在临时目录（脚本结束时打印路径）。
# 红线：geo 是展示元数据 —— 绝不进入状态哈希（哈希与无坐标物化逐字节一致，
#       见 internal/sim/geo_test.go 的 TestWorkspaceHashIgnoresGeo）。

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$REPO_ROOT/bin/netopslab"
TESTDATA="$REPO_ROOT/testdata"
WORK="$(mktemp -d /tmp/netops-demo-geo.XXXXXX)"
DB="$WORK/netops.db"

# 0. 构建二进制（若缺失）
if [[ ! -x "$BIN" ]]; then
  echo "[0/5] 未发现二进制，开始构建 bin/netopslab ..."
  (cd "$REPO_ROOT" && go build -o "$BIN" ./cmd/netopslab)
fi

# 0b. 确保地理演示工作簿存在（只补新增的两个文件；既有 4 个工件不动）
if [[ ! -f "$TESTDATA/geodevices.xlsx" || ! -f "$TESTDATA/geocircuits.xlsx" ]]; then
  echo "[0/5] testdata 地理工作簿缺失，用 testdatagen -geo 生成 ..."
  (cd "$REPO_ROOT" && go run ./cmd/testdatagen -out "$TESTDATA" -geo > /dev/null)
fi
echo "[0/5] 工作目录：$WORK"

# 从 analyze 输出中抽取 suggestedMapping（与 demo-lab.sh 相同的技巧）。
extract_map() {
  python3 - "$1" "$2" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    analysis = json.load(f)
with open(sys.argv[2], "w", encoding="utf-8") as f:
    json.dump(analysis["suggestedMapping"], f, ensure_ascii=False, indent=2)
PY
}

# 1. 导入两本地理工作簿（顺序：设备 → 电路，引用先于被引用落库）
for wb in geodevices geocircuits; do
  "$BIN" analyze --file "$TESTDATA/$wb.xlsx" > "$WORK/analysis-$wb.json"
  extract_map "$WORK/analysis-$wb.json" "$WORK/map-$wb.json"
  "$BIN" import --file "$TESTDATA/$wb.xlsx" --map "$WORK/map-$wb.json" --db "$DB" \
    > "$WORK/import-$wb.json"
done
python3 - "$WORK/import-geodevices.json" "$WORK/import-geocircuits.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1], encoding="utf-8"))
c = json.load(open(sys.argv[2], encoding="utf-8"))
ds, cs = d["summary"], c["summary"]
print(f"[1/5] 导入完成：geodevices {ds['rowsImported']}/{ds['rowsTotal']} 设备（含经度/纬度列，"
      f"零失败）；geocircuits {cs['rowsImported']}/{cs['rowsTotal']} 电路。")
PY

# 2. 物化地理孪生工作台（描述注明坐标为演示近似值）
"$BIN" lab workspace create --db "$DB" --name 黑龙江-地理孪生演示 \
  --desc "哈尔滨→漠河北南干线 + 地震局专线孪生演示（坐标为演示近似值，非工程坐标）" \
  --all-batches > "$WORK/workspace.json"
python3 - "$WORK/workspace.json" "$WORK/ids.env" <<'PY'
import json, sys
r = json.load(open(sys.argv[1], encoding="utf-8"))
c = r["counts"]
print(f"[2/5] 物化工作台「{r['name']}」：设备 {c['devices']} / 接口 {c['interfaces']} / "
      f"链路 {c['links']}；状态哈希 {r['workspaceHash'][:12]}…（geo 不入哈希）。")
with open(sys.argv[2], "w", encoding="utf-8") as f:
    f.write(f"WS={r['workspaceId']}\nBASE_HASH={r['workspaceHash']}\n")
PY
source "$WORK/ids.env"

# 3+4. 操作文件：先解析拓扑中的接口/链路 id，再一次性应用
#      （IPv4 两端配置 + 中途干线故障注入）。
"$BIN" lab topology --db "$DB" --workspace "$WS" > "$WORK/topology.json"
python3 - "$WORK/topology.json" "$WORK/ops-geo.json" <<'PY'
import json, sys
topo = json.load(open(sys.argv[1], encoding="utf-8"))
iface = {(n["code"], i["name"]): i["id"] for n in topo["nodes"] for i in n["interfaces"]}
link = {e["label"]: e["id"] for e in topo["edges"]}
# GD-202366（动力机房2 基站归属电路）两端：HLB-A4 的第二个端口 + HLB-DLZS2 的首端口
# —— 物化端口确定性分配后即 HLB-A4/eth1 与 HLB-DLZS2/eth0，这里仍动态解析。
ops = {
    "description": "基站归属电路两端配置 + 中途干线故障注入",
    "ops": [
        {"op": "config_interface_ipv4", "interfaceId": iface[("HLB-A4", "eth1")],
         "ipv4Addr": "10.61.0.1", "ipv4Prefix": 30},
        {"op": "config_interface_ipv4", "interfaceId": iface[("HLB-DLZS2", "eth0")],
         "ipv4Addr": "10.61.0.2", "ipv4Prefix": 30},
        {"op": "set_link_status", "linkId": link["CKT-QQH-JGDC-10G"], "status": "down"},
    ],
}
json.dump(ops, open(sys.argv[2], "w", encoding="utf-8"), ensure_ascii=False, indent=2)
PY
"$BIN" lab apply --db "$DB" --workspace "$WS" --ops "$WORK/ops-geo.json" > "$WORK/apply-geo.json"
python3 - "$WORK/apply-geo.json" <<'PY'
import json, sys
r = json.load(open(sys.argv[1], encoding="utf-8"))
c = r["checks"]["counts"]
print(f"[3/5] 变更集 {r['changesetId']}：GD-202366 两端配置 10.61.0.1/30 ↔ 10.61.0.2/30；"
      f"CKT-QQH-JGDC-10G（齐齐哈尔→加格达奇）已关断 —— 干线中段红色故障。")
print(f"      自动校验 {r['checks']['runId']}：评分 {r['checks']['score']}，"
      f"问题（错误/警告/提示）{c['error']}/{c['warning']}/{c['info']}。")
PY

# 5. 启动 serve（空闲端口）→ curl 拓扑 API：节点携带 geoLat/geoLng
PORT="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')"
"$BIN" serve --addr "127.0.0.1:$PORT" --db "$DB" --data-dir "$WORK/data" > "$WORK/server.log" 2>&1 &
SERVER_PID=$!
trap 'kill "$SERVER_PID" 2>/dev/null || true' EXIT
for _ in $(seq 1 50); do
  if curl -fsS "http://127.0.0.1:$PORT/api/health" > "$WORK/health.json" 2>/dev/null; then
    break
  fi
  sleep 0.1
done
curl -fsS "http://127.0.0.1:$PORT/api/workspaces/$WS/topology" > "$WORK/topology-api.json"
python3 - "$WORK/topology-api.json" <<'PY'
import json, sys
topo = json.load(open(sys.argv[1], encoding="utf-8"))
nodes = {n["code"]: n for n in topo["nodes"]}
h = nodes["HLB-A4"]
m = nodes["HLB-MH-BS1"]
edges = {e["label"]: e for e in topo["edges"]}
print(f"[4/5] 拓扑 API（curl /api/workspaces/<id>/topology）：{len(topo['nodes'])} 节点 / "
      f"{len(topo['edges'])} 边，workspaceHash {topo['workspaceHash'][:12]}…")
print(f"      HLB-A4（哈尔滨枢纽楼）  geoLat={h['geoLat']}, geoLng={h['geoLng']}, status={h['status']}")
print(f"      HLB-MH-BS1（漠河基站1） geoLat={m['geoLat']}, geoLng={m['geoLng']}, status={m['status']}（停用→down）")
print(f"      CKT-QQH-JGDC-10G 状态 = {edges['CKT-QQH-JGDC-10G']['status']}（干线中段故障）")
missing = [c for c, n in nodes.items() if "geoLat" not in n or "geoLng" not in n]
print(f"      携带坐标节点 {len(topo['nodes']) - len(missing)}/{len(topo['nodes'])}"
      + (f"（缺坐标：{missing}）" if missing else "（全部携带）"))
PY
kill "$SERVER_PID" 2>/dev/null || true
trap - EXIT

# 6. 收尾总结
echo ""
echo "[5/5] 演示完成（数据库与产物：$WORK）。本演示验证的能力："
echo "  - 导入列扩展：设备表 经度/纬度（geo_lat/geo_lng）直接落库资源属性；组合列 经纬度 由"
echo "    中国边界启发式自动拆分（高德序/纬经序/中文逗号/空格分隔均可）"
echo "  - 物化 → 拓扑：位置随虚拟设备物化，GET /api/workspaces/{id}/topology 节点携带 geoLat/geoLng"
echo "    （无坐标节点省略该字段，omitempty）"
echo "  - 红线：geo 为展示元数据 —— 状态哈希与无坐标物化逐字节一致（见 TestWorkspaceHashIgnoresGeo）"
echo "  - 故障可视化：漠河站 停用→down、齐齐哈尔→加格达奇 10G 关断 → 地图中的停用节点与红色故障链路"
echo "浏览器演示请运行：./bin/netopslab serve --addr :8080 --db ./data/netops.db（/lab 页面）"
