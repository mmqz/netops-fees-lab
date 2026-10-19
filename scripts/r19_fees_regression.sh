#!/usr/bin/env bash
# NetOps Lab — R19 费用域端到端回归（v0.15，#69）。
#
# 旅程：发布二进制驱动——sites 资源导入 → 费用 CSV 导入（行级账期）→
#       幂等重导 → 批次共存 → 分级关联扫描（exact/fuzzy/unlinked 计数）→
#       人工 confirm/dismiss → 重扫人类优先 → 默认构建诚实拒绝 →
#       [duckdb 段] 跨批聚合 + duplicate-settlement 候选 + unlinked 线索 →
#       回滚（快照保留/Parquet 删除/键释放）。
# 断言：
#   F1  导入 5 行 completed，rowsImported=5，rowsFailed=0；
#   F2  同文件重导 → skippedDuplicate=true + existingBatch（幂等，exit 0）；
#   F3  第二文件同站同期 → 批次共存（list 3 批 completed）；
#   F4  link-scan：exactLinks=2 / ruleLinks=0 / fuzzyProposed=2（两条近失候选）；
#   F5  pending 列表 2 条 fuzzy → confirm 一条（confirmed）、dismiss 一条
#       （dismissed，审计保留）；
#   F6  重扫 → humanKept=2（人类决策存活，永不覆写）+ 已确认关联不再
#       出现在 pending；
#   F7  默认构建 query totals → exit 1 + engine_not_built 文案（诚实契约）；
#   F8  默认构建 risks 同上；
#   F9  [duckdb] totals 跨批聚合：ST1603990225 = 123456+50000 = 173456 分；
#   F10 [duckdb] risks duplicate-settlement → ST1603990225@2024-05 唯一候选
#       （batches=2，证据 2 行跨 2 批）；
#   F11 [duckdb] risks unlinked → ZZZ-UNKNOWN-999 在线索（零资源计费）；
#   F12 回滚第二批 → list 状态 rolled_back + Parquet 分区文件删除 +
#       快照文件保留 → 同文件重导成功（键释放）。
# duckdb 段需 CGO（本地 linux 或 CI duckdb job）；无 CGO 环境显式 SKIP。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
export PATH="$PATH:/home/z/.local/go/bin"

WORK="$(mktemp -d /tmp/netops-r19fees.XXXXXX)"
DB="$WORK/r19.db"
DATA="$WORK/data"
FEE_IMPORTS="$DATA/fee-imports"
ANALYSIS="$DATA/analysis/fees"
REALDATA="$REPO_ROOT/testdata/realdata"
trap 'rm -rf "$WORK"' EXIT

PASS=0; FAIL=0; SKIP=0
ok()   { PASS=$((PASS+1)); echo "  PASS  $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  FAIL  $1"; }
skip() { SKIP=$((SKIP+1)); echo "  SKIP  $1"; }

# jget <file> <python-expr on d> — 从 JSON stdout 断言
jget() { python3 -c "import json,sys; d=json.load(open(sys.argv[1])); print($2)" "$1"; }

BIN="$WORK/netopslab"
echo "[0/6] 构建默认二进制"
go build -o "$BIN" ./cmd/netopslab

echo "[1/6] sites 资源导入（分级关联的资源候选面）"
"$BIN" analyze -file "$REALDATA/sites.xlsx" > "$WORK/a.json"
python3 - "$WORK/a.json" "$WORK/m.json" <<'PY'
import json, sys
json.dump(json.load(open(sys.argv[1], encoding="utf-8"))["suggestedMapping"], open(sys.argv[2], "w", encoding="utf-8"))
PY
"$BIN" import --file "$REALDATA/sites.xlsx" --map "$WORK/m.json" --db "$DB" > "$WORK/imp_sites.json"
SITE_ROWS="$(jget "$WORK/imp_sites.json" 'd["summary"]["rowsImported"]')"
[[ "$SITE_ROWS" -ge 30 ]] && ok "sites 资源 ${SITE_ROWS} 行入库" || bad "sites 资源行数 ${SITE_ROWS} < 30"

echo "[2/6] 费用导入旅程（F1-F3）"
# CSV-1：2 精确 + 2 模糊近失（ST…226 距 ST…225 编辑距离 1；ST…073 距 ST…072 距离 1）+ 1 零资源
cat > "$WORK/fee1.csv" <<'CSV'
站址编码,电费(元),账期
ST1603990225,1234.56,2024-05
ST1727803072,999.00,2024-05
ST1603990226,100.00,2024-05
ST1727803073,100.00,2024-05
ZZZ-UNKNOWN-999,100.00,2024-05
CSV
"$BIN" fees --db "$DB" --data "$DATA" import --file "$WORK/fee1.csv" --type electricity --year 2024 --month 5 > "$WORK/f1.json"
[[ "$(jget "$WORK/f1.json" 'd["status"]')" == "completed" ]] && ok "F1a 批次 completed" || bad "F1a status=$(jget "$WORK/f1.json" 'd["status"]')"
[[ "$(jget "$WORK/f1.json" 'd["rowsImported"]')" == "5" ]] && ok "F1b rowsImported=5" || bad "F1b rowsImported=$(jget "$WORK/f1.json" 'd["rowsImported"]')"
[[ "$(jget "$WORK/f1.json" 'd["rowsFailed"]')" == "0" ]] && ok "F1c rowsFailed=0" || bad "F1c rowsFailed=$(jget "$WORK/f1.json" 'd["rowsFailed"]')"
BATCH1="$(jget "$WORK/f1.json" 'd["batchId"]')"

# F2 幂等重导
"$BIN" fees --db "$DB" --data "$DATA" import --file "$WORK/fee1.csv" --type electricity --year 2024 --month 5 > "$WORK/f2.json"
[[ "$(jget "$WORK/f2.json" 'd["skippedDuplicate"]')" == "True" ]] && ok "F2a 同文件重导幂等跳过" || bad "F2a skippedDuplicate != true"
[[ "$(jget "$WORK/f2.json" 'd["existingBatch"]')" == "$BATCH1" ]] && ok "F2b existingBatch 指向原批次" || bad "F2b existingBatch 不匹配"

# F3 批次共存：第二文件同站同期
cat > "$WORK/fee2.csv" <<'CSV'
站址编码,电费(元),账期
ST1603990225,500.00,2024-05
ST1933473774,88.88,2024-05
CSV
"$BIN" fees --db "$DB" --data "$DATA" import --file "$WORK/fee2.csv" --type electricity --year 2024 --month 5 > "$WORK/f3.json"
BATCH2="$(jget "$WORK/f3.json" 'd["batchId"]')"
[[ "$(jget "$WORK/f3.json" 'd["status"]')" == "completed" ]] && ok "F3a 第二文件同站同期导入 completed" || bad "F3a 第二文件导入失败"
"$BIN" fees --db "$DB" --data "$DATA" list > "$WORK/fl.json"
[[ "$(jget "$WORK/fl.json" 'len([b for b in d["items"] if b["status"]=="completed"])')" == "2" ]] && ok "F3b 两批共存（不合并）" || bad "F3b completed 批数 != 2"

echo "[3/6] 分级关联旅程（F4-F6）"
"$BIN" fees --db "$DB" --data "$DATA" link-scan > "$WORK/scan1.json"
[[ "$(jget "$WORK/scan1.json" 'd["exactLinks"]')" == "3" ]] && ok "F4a exactLinks=3（ST…225/ST…072/ST1933473774 精确命中，含 CSV-2 新编码）" || bad "F4a exactLinks=$(jget "$WORK/scan1.json" 'd["exactLinks"]')"
[[ "$(jget "$WORK/scan1.json" 'd["ruleLinks"]')" == "0" ]] && ok "F4b ruleLinks=0" || bad "F4b ruleLinks != 0"
[[ "$(jget "$WORK/scan1.json" 'd["fuzzyProposed"]')" == "2" ]] && ok "F4c fuzzyProposed=2（两条近失候选）" || bad "F4c fuzzyProposed=$(jget "$WORK/scan1.json" 'd["fuzzyProposed"]')"

"$BIN" fees --db "$DB" --data "$DATA" links list --status pending > "$WORK/pl.json"
PENDING_N="$(jget "$WORK/pl.json" 'd["total"]')"
[[ "$PENDING_N" == "2" ]] && ok "F5a pending 模糊提案 2 条" || bad "F5a pending=${PENDING_N}"
CONFIRM_ID="$(jget "$WORK/pl.json" '[i["id"] for i in d["items"] if i["feeSiteCode"]=="ST1603990226"][0]')"
DISMISS_ID="$(jget "$WORK/pl.json" '[i["id"] for i in d["items"] if i["feeSiteCode"]=="ST1727803073"][0]')"
"$BIN" fees --db "$DB" --data "$DATA" links confirm --id "$CONFIRM_ID" > "$WORK/cf.json"
[[ "$(jget "$WORK/cf.json" 'd["status"]')" == "confirmed" ]] && ok "F5b confirm → confirmed" || bad "F5b confirm 后状态异常"
"$BIN" fees --db "$DB" --data "$DATA" links dismiss --id "$DISMISS_ID" > "$WORK/dm.json"
[[ "$(jget "$WORK/dm.json" 'd["status"]')" == "dismissed" ]] && ok "F5c dismiss → dismissed（审计保留）" || bad "F5c dismiss 后状态异常"

"$BIN" fees --db "$DB" --data "$DATA" link-scan > "$WORK/scan2.json"
[[ "$(jget "$WORK/scan2.json" 'd["humanKept"]')" == "1" ]] && ok "F6a 重扫 humanKept=1（confirmed 的人工行存活；dismissed 不计入 kept）" || bad "F6a humanKept=$(jget "$WORK/scan2.json" 'd["humanKept"]')"
"$BIN" fees --db "$DB" --data "$DATA" links list --status pending > "$WORK/pl2.json"
[[ "$(jget "$WORK/pl2.json" 'd["total"]')" == "0" ]] && ok "F6b 重扫后 pending=0（confirmed 与 dismissed 配对均不复提——#69 修复前 confirmed 会复提）" || bad "F6b 重扫后仍有 pending"

echo "[4/6] 默认构建诚实契约（F7-F8）"
if "$BIN" fees --db "$DB" --data "$DATA" query totals --type electricity --year 2024 > "$WORK/q1.json" 2>"$WORK/q1.err"; then
  bad "F7 默认构建 query totals 应 exit 1"
else
  RC=$?
  [[ "$RC" == "1" ]] && ok "F7a query totals exit 1" || bad "F7a exit=$RC"
  rg -q "分析查询引擎未编译" "$WORK/q1.json" && ok "F7b 未编译引擎文案（stdout JSON error）" || bad "F7b 文案缺失（stdout）"
fi
if "$BIN" fees --db "$DB" --data "$DATA" risks duplicate-settlement --type electricity --year 2024 > "$WORK/r0.json" 2>"$WORK/r0.err"; then
  bad "F8 默认构建 risks 应 exit 1"
else
  RC=$?
  [[ "$RC" == "1" ]] && ok "F8a risks exit 1" || bad "F8a exit=$RC"
  rg -q "分析查询引擎未编译" "$WORK/r0.json" && ok "F8b 未编译引擎文案" || bad "F8b 文案缺失"
fi

echo "[5/6] duckdb 引擎段（F9-F11，需 CGO）"
DUCKDB_BIN="$WORK/netopslab-duckdb"
if CGO_ENABLED=1 go build -tags duckdb -o "$DUCKDB_BIN" ./cmd/netopslab 2>"$WORK/duckdb-build.err"; then
  "$DUCKDB_BIN" fees --db "$DB" --data "$DATA" query totals --type electricity --year 2024 --month 5 > "$WORK/qt.json"
  GOT="$(jget "$WORK/qt.json" '[i["totalCents"] for i in d["items"] if i["siteCode"]=="ST1603990225"][0]')"
  [[ "$GOT" == "173456" ]] && ok "F9 跨批聚合 ST1603990225 = 173456 分（123456+50000）" || bad "F9 totals=$GOT ≠ 173456"
  "$DUCKDB_BIN" fees --db "$DB" --data "$DATA" risks duplicate-settlement --type electricity --year 2024 > "$WORK/dr.json"
  GOT="$(jget "$WORK/dr.json" 'd["total"]')"
  [[ "$GOT" -ge 1 ]] && ok "F10a duplicate-settlement 线索 ≥1" || bad "F10a total=$GOT"
  GOT="$(jget "$WORK/dr.json" 'len([c for c in d["clues"] if c["siteCode"]=="ST1603990225"])')"
  [[ "$GOT" == "1" ]] && ok "F10b ST1603990225@2024-05 命中唯一候选" || bad "F10b 候选命中数=$GOT"
  GOT="$(jget "$WORK/dr.json" '[(c["evidenceTotal"],c["metricValue"]) for c in d["clues"] if c["siteCode"]=="ST1603990225"][0]')"
  [[ "$GOT" == "(2, 2.0)" || "$GOT" == "(2, 2)" ]] && ok "F10c 证据 2 行跨 2 批（batches=2）" || bad "F10c 证据/批次=$GOT"
  "$DUCKDB_BIN" fees --db "$DB" --data "$DATA" risks unlinked --type electricity --year 2024 > "$WORK/ur.json"
  GOT="$(jget "$WORK/ur.json" 'len([c for c in d["clues"] if c["siteCode"]=="ZZZ-UNKNOWN-999"])')"
  [[ "$GOT" == "1" ]] && ok "F11 unlinked 线索含 ZZZ-UNKNOWN-999（零资源计费）" || bad "F11 unlinked 命中数=$GOT"
else
  skip "F9-F11 duckdb 引擎段（本环境 CGO 构建不可用：$(head -c 120 "$WORK/duckdb-build.err" 2>/dev/null)…）"
fi

echo "[6/6] 回滚旅程（F12）"
"$BIN" fees --db "$DB" --data "$DATA" rollback --batch "$BATCH2" > "$WORK/rb.json"
[[ "$(jget "$WORK/rb.json" 'd["status"]')" == "rolled_back" ]] && ok "F12a 批次 rolled_back" || bad "F12a 回滚后状态异常"
B2_FILES="$(find "$ANALYSIS" -name "batch-${BATCH2}.parquet" 2>/dev/null | wc -l)"
[[ "$B2_FILES" == "0" ]] && ok "F12b 第二批 Parquet 已删除" || bad "F12b Parquet 残留 ${B2_FILES} 个"
SNAP_N="$(find "$FEE_IMPORTS" -type f 2>/dev/null | wc -l)"
[[ "$SNAP_N" -ge 2 ]] && ok "F12c 快照保留（${SNAP_N} 文件——证据寿命 > 批次寿命）" || bad "F12c 快照数 ${SNAP_N} < 2"
"$BIN" fees --db "$DB" --data "$DATA" import --file "$WORK/fee2.csv" --type electricity --year 2024 --month 5 > "$WORK/f4.json"
[[ "$(jget "$WORK/f4.json" 'd["status"]')" == "completed" ]] && ok "F12d 回滚后同文件重导成功（rolled_back 不占幂等键）" || bad "F12d 键未释放"

echo
echo "R19-FEES: PASS=$PASS FAIL=$FAIL SKIP=$SKIP"
[[ "$FAIL" -eq 0 ]] || exit 1
exit 0
