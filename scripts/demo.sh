#!/usr/bin/env bash
# NetOps Lab — 一键演示脚本（可重复执行，每次使用全新临时数据库）。
#
# 用法：
#   ./scripts/demo.sh                # 或 make demo
#
# 流程（< 60 秒）：
#   1. 若 bin/netopslab 缺失则先构建（go build）
#   2. 全新临时数据库（mktemp -d，每次运行互不影响）
#   3. netopslab analyze（离线解析 + 列映射建议）→ python3 抽取 suggestedMapping 生成映射 JSON
#   4. netopslab import ×3（devices → ippools → circuits，严格顺序，依赖引用先落库）
#   5. netopslab validate（重算全局规则，输出问题计数与质量评分）
#   6. netopslab report --format html --out（独立 HTML 质量报告）
#   7. netopslab query stats（资源/关系/问题统计）
#
# 依赖：Go 1.27+（仅首次构建需要）、python3、testdata/*.xlsx（缺失时自动再生成）。
# 说明：报告与数据库留在临时目录中（脚本结束时打印路径，可自行查看或删除）。

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$REPO_ROOT/bin/netopslab"
TESTDATA="$REPO_ROOT/testdata"
WORK="$(mktemp -d /tmp/netops-demo.XXXXXX)"
DB="$WORK/netops.db"
REPORT="$WORK/quality-report.html"

# 1. 构建二进制（若缺失）
if [[ ! -x "$BIN" ]]; then
  echo "[0/5] 未发现二进制，开始构建 bin/netopslab ..."
  (cd "$REPO_ROOT" && go build -o "$BIN" ./cmd/netopslab)
fi
echo "[0/5] 工作目录：$WORK"

# 2. 确保合成测试数据存在
if [[ ! -f "$TESTDATA/devices.xlsx" || ! -f "$TESTDATA/ippools.xlsx" || ! -f "$TESTDATA/circuits.xlsx" ]]; then
  echo "[0/5] testdata 缺失，重新生成合成工作簿 ..."
  "$BIN" testdata --out "$TESTDATA" > /dev/null
fi

# 从 analyze 输出中抽取 suggestedMapping，写入映射 JSON 文件。
# 用法：extract_map <analysis.json> <map.json>
extract_map() {
  python3 - "$1" "$2" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    analysis = json.load(f)
with open(sys.argv[2], "w", encoding="utf-8") as f:
    json.dump(analysis["suggestedMapping"], f, ensure_ascii=False, indent=2)
PY
}

# 打印一步导入的中文摘要。用法：print_import <name> <import.json>
print_import() {
  python3 - "$1" "$2" <<'PY'
import json, sys
with open(sys.argv[2], encoding="utf-8") as f:
    r = json.load(f)
s = r["summary"]
i = r["issues"]
print(f"[1/5] 导入 {sys.argv[1]}：总行 {s['rowsTotal']}，"
      f"成功 {s['rowsImported']}，跳过 {s['rowsSkipped']}，失败 {s['rowsFailed']}；"
      f"问题（错误/警告/提示）{i['error']}/{i['warning']}/{i['info']}")
PY
}

# 3+4. 导入三本工作簿（顺序：设备 → 地址池 → 电路，引用先于被引用落库）
for wb in devices ippools circuits; do
  "$BIN" analyze --file "$TESTDATA/$wb.xlsx" > "$WORK/analysis-$wb.json"
  extract_map "$WORK/analysis-$wb.json" "$WORK/map-$wb.json"
  "$BIN" import --file "$TESTDATA/$wb.xlsx" --map "$WORK/map-$wb.json" --db "$DB" \
    > "$WORK/import-$wb.json"
  print_import "$wb.xlsx" "$WORK/import-$wb.json"
done

# 5. 全局质量校验
python3 - "$BIN" "$DB" <<'PY'
import json, subprocess, sys
out = subprocess.run([sys.argv[1], "validate", "--db", sys.argv[2]],
                     capture_output=True, text=True, check=True).stdout
r = json.loads(out)
i = r["issues"]
print(f"[2/5] 校验：问题（错误/警告/提示）{i['error']}/{i['warning']}/{i['info']}，"
      f"质量评分 {r['score']}")
PY

# 6. HTML 质量报告
"$BIN" report --db "$DB" --format html --out "$REPORT" > /dev/null
echo "[3/5] 质量报告：$REPORT"

# 7. 资源统计
python3 - "$BIN" "$DB" <<'PY'
import json, subprocess, sys
out = subprocess.run([sys.argv[1], "query", "--db", sys.argv[2], "stats"],
                     capture_output=True, text=True, check=True).stdout
r = json.loads(out)
b = r["byType"]; br = r["byRelationType"]; i = r["issuesBySeverity"]
print(f"[4/5] 资源统计：总数 {r['totalResources']}（设备 {b.get('device', 0)} / "
      f"地址池 {b.get('ip_pool', 0)} / 电路 {b.get('circuit', 0)}）；"
      f"关系 {br.get('assigned_to', 0) + br.get('a_end', 0) + br.get('z_end', 0)}"
      f"（assigned_to {br.get('assigned_to', 0)} / a_end {br.get('a_end', 0)} / "
      f"z_end {br.get('z_end', 0)}）；问题 E{i.get('error', 0)}/W{i.get('warning', 0)}"
      f"/I{i.get('info', 0)}")
PY

echo "[5/5] 演示完成（数据库：$DB；报告：$REPORT）。"
echo "      浏览器演示请运行：./bin/netopslab serve --addr :8080 --db ./data/netops.db"
