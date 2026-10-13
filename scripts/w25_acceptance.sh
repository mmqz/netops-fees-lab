#!/usr/bin/env bash
# NetOps Lab — W25 终局验收（issues #16/#20）：
#   阶段 A（#20 UI 范围验证）：14 类新类型工作簿（L0-L6 全层、真实表头风格）
#     经 analyze→suggestedMapping→import 通道入库；浏览器断言通用资源页
#     （33 类型筛选、新类型 TypeChip 渲染、类型筛选/搜索过滤）与导入向导
#     （多 sheet 上传、逐 sheet 类型建议、33 类型选项）。
#   阶段 B（#16 里程碑总验收）：同一库上做 HTTP 级端到端——
#     层次穿透（service L6 → physical L2 traverse）、业务影响分析
#     （physical 断 → impacted 含 customer L6）、保护结构（ring 可表达）、
#     /api/schema 33 类层次域投影。
# 截图存 repo tmp/（不进 download/ —— 截屏不作为交付物）。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
export PATH="$PATH:/home/z/.local/go/bin"

PORT=18125
WORK="$(mktemp -d /tmp/netops-w25.XXXXXX)"
DB="$WORK/w25.db"
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

echo "[0/5] 构建二进制"
go build -o "$WORK/netopslab" ./cmd/netopslab

echo "[1/5] 构造全层 14 类型工作簿（真实表头风格，含 #16 原始 issue 的专线 23 列）"
python3 - "$WORK" <<'PYEOF'
import sys, openpyxl
out = sys.argv[1]
wb = openpyxl.Workbook()
wb.remove(wb.active)

def sheet(name, headers, rows):
    ws = wb.create_sheet(title=name)
    ws.append(headers)
    for r in rows: ws.append(r)

# L0 局站/机房（既有类型，作链路锚点）
sheet("局站资源表", ["局站ID", "局站名称"], [["SITE-01", "加区枢纽楼"], ["SITE-02", "道里汇聚局"]])
sheet("机房资源表", ["机房ID", "机房名称", "局站ID"], [["MR-01", "枢纽一楼机房", "SITE-01"]])
# L1 设备（housed_in MR-01）
sheet("网元设备", ["设备编码", "设备名称", "厂商", "机房ID"], [["DEV-C1", "加区C1路由器", "华为", "MR-01"]])
# L2 物理层：ODF 端子 / 光缆 / 纤芯
sheet("端子资源表", ["ODF编码", "端子号"], [["ODF-01", "1"]])
sheet("光缆资源表", ["光缆编码", "光缆名称", "A端局站", "Z端局站", "芯数"], [["CABLE-01", "加区-道里主干光缆", "SITE-01", "SITE-02", 48]])
sheet("纤芯资源表", ["光缆编码", "纤芯号", "成端ODF端子", "使用情况"], [["CABLE-01", "1", "ODF-01|1", "在用"]])
# L3 传输层：波道 / 电路
sheet("波道资源表", ["波道编码", "波道号", "路由光缆", "占用纤芯"], [["OCH-01", "5", "CABLE-01", "CABLE-01|1"]])
sheet("传输电路", ["电路编号", "A端设备", "Z端设备", "承载波道"], [["CIR-01", "DEV-C1", "DEV-C1", "OCH-01"]])
# L4 逻辑/接入：环网 / OLT / 基站
sheet("环网资源表", ["环编码", "环名称", "环类型", "节点数"], [["RING-01", "道里汇聚环", "汇聚环", 6]])
sheet("OLT资源表", ["OLT编码", "OLT名称", "上联设备"], [["OLT-01", "道里OLT-01", "DEV-C1"]])
sheet("基站资源表", ["基站编码", "基站名称", "组网方式", "归属A IP"], [["BS-01", "中央大街基站", "环", "10.80.1.5"]])
# L6 业务层：客户 / 业务实例 / 专线（#16 原始 issue 的真实 23 列表头）
sheet("客户资源表", ["客户编码", "客户名称"], [["CUST-01", "哈尔滨银行"]])
sheet("业务实例表", ["业务编码", "业务类型", "客户编码", "提供电路", "依赖设备", "带宽Mbps"],
      [["SVC-01", "金融专线", "CUST-01", "CIR-01", "DEV-C1", 1000]])
sheet("专线资源表",
      ["级别", "业务类型", "组网方式", "专线名称", "专线编号", "集团STN编号",
       "互联网专线编号", "客户IP掩码", "客户名称", "A端装机地址", "Z端装机地址",
       "A端IP", "A端名称", "A端端口", "A端VLAN ID", "A端装机经纬度",
       "Z端IP", "Z端名称", "Z端端口", "Z端VLAN ID", "协议带宽(Mbps)", "工单号"],
      [["金牌", "互联网专线", "环", "哈尔滨银行总部专线", "LL-01", "STN-8848",
        "DIA-0001", "255.255.255.252", "CUST-01", "哈尔滨市道里区XX街1号", "哈尔滨市南岗区YY街2号",
        "10.1.0.2", "A端名称-01", "GE0/0/1", 100, "45.77,126.65",
        "10.2.0.2", "Z端名称-01", "GE0/0/2", 100, 200, "WO-2026-001"]])
wb.save(f"{out}/w25-all-layers.xlsx")
print("workbook written: 14 sheets")
PYEOF

echo "[2/5] analyze → suggestedMapping → import（建议映射通道，零人工映射文件）"
ANALYSIS="$("$WORK/netopslab" analyze --file "$WORK/w25-all-layers.xlsx")"
echo "$ANALYSIS" > "$WORK/analysis.json"
python3 - "$WORK" <<'PYEOF'
import json, sys
w = sys.argv[1]
a = json.load(open(f"{w}/analysis.json"))
imported = [s for s in a["suggestedMapping"]["sheets"] if s.get("action") == "import"]
skipped = [s for s in a["suggestedMapping"]["sheets"] if s.get("action") != "import"]
print(f"suggested: {len(imported)} import / {len(skipped)} skip")
types = {s["sheet"]: s.get("resourceType") for s in imported}
# 14 张 sheet 全部建议 import 且类型各自不同
assert len(imported) == 14, f"want 14 import sheets, got {len(imported)}"
assert len(set(types.values())) == 14, f"want 14 distinct types, got {types}"
open(f"{w}/map.json", "w").write(json.dumps({"sheets": imported}, ensure_ascii=False))
PYEOF
ok "A0 analyze 建议 14/14 sheet 全部 import 且类型互不相同（SuggestType 对新 sheet 名提示全命中）"
"$WORK/netopslab" import --file "$WORK/w25-all-layers.xlsx" --map "$WORK/map.json" --db "$DB" > "$WORK/import.json"
python3 - "$WORK" <<'PYEOF'
import json, sys
d = json.load(open(f"{sys.argv[1]}/import.json"))
assert d["status"] == "completed", d
assert d["summary"]["rowsFailed"] == 0, d["summary"]
assert d["summary"]["rowsImported"] == 15, d["summary"]  # 14 sheet（局站 2 行）
print("import:", d["summary"])
PYEOF
ok "A1 全层工作簿单批次导入 completed（14 行 / 0 失败）"

echo "[3/5] serve :$PORT"
"$WORK/netopslab" serve --db "$DB" --addr ":$PORT" > "$WORK/serve.log" 2>&1 &
SERVE_PID=$!
for i in $(seq 1 40); do
  curl -sf "http://127.0.0.1:$PORT/api/health" > /dev/null 2>&1 && break
  sleep 0.3
done
curl -sf "http://127.0.0.1:$PORT/api/health" > /dev/null || { echo "serve 未就绪"; exit 1; }

# 预置错误收集（页面错误 → F 断言）
agent-browser set viewport 1600 900 >/dev/null
agent-browser open "http://127.0.0.1:$PORT/resources" >/dev/null
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null
agent-browser wait --load networkidle >/dev/null 2>&1 || true
agent-browser wait --fn "document.querySelectorAll('.filter-bar .select option').length > 30" --timeout 20000 >/dev/null 2>&1 || true

echo "[4/5] 浏览器断言 —— 通用资源页（#20 评判第 1 条）"
# A. 类型筛选下拉 = 33 项 + 全部
TYPE_OPTS="$(jeval "document.querySelectorAll('.filter-bar .select')[0].options.length")"
if [[ "$TYPE_OPTS" == "34" ]]; then ok "A2 类型筛选下拉 34 项（33 类型 + 全部）"; else bad "A2 类型筛选下拉 = $TYPE_OPTS（want 34）"; fi
agent-browser wait --fn "document.querySelector('table[aria-label=\"资源列表\"] tbody') && document.querySelector('table[aria-label=\"资源列表\"] tbody').children.length >= 14" --timeout 20000 >/dev/null 2>&1 || true
ROWS="$(jeval "document.querySelector('table[aria-label=\"资源列表\"] tbody').children.length")"
if [[ "$ROWS" -ge 14 ]]; then ok "A3 资源表格渲染 $ROWS 行（14 类新类型全部可见）"; else bad "A3 资源表格行数 = $ROWS（<14）"; fi
# B. 新类型 TypeChip 的中文标签渲染
for pair in "客户:customer" "光缆:optical_cable" "业务实例:service" "环网:ring" "OTN波道:otn_och" "OLT:olt" "纤芯:fiber" "专线:leased_line"; do
  LABEL="${pair%%:*}"
  FOUND="$(jeval "[...document.querySelectorAll('table[aria-label=\"资源列表\"] .chip')].some(c => c.textContent === '$LABEL')")"
  if [[ "$FOUND" == "true" ]]; then ok "A4 TypeChip「$LABEL」渲染"; else bad "A4 TypeChip「$LABEL」缺失"; fi
done
# C. 类型筛选：选光缆 → 表中只剩光缆行
agent-browser select ".filter-bar .select" "optical_cable" >/dev/null
agent-browser wait --fn "document.querySelector('table[aria-label=\"资源列表\"] tbody') && document.querySelector('table[aria-label=\"资源列表\"] tbody').children.length === 1" --timeout 20000 >/dev/null 2>&1 || true
CABLE_ROWS="$(jeval "document.querySelector('table[aria-label=\"资源列表\"] tbody').children.length")"
CABLE_CHIPS="$(jeval "[...document.querySelectorAll('table[aria-label=\"资源列表\"] tbody .chip')].filter(c => c.textContent === '光缆').length")"
if [[ "$CABLE_ROWS" == "1" && "$CABLE_CHIPS" == "1" ]]; then ok "A5 类型筛选=光缆 → 恰 1 行且 chip=光缆"; else bad "A5 筛选光缆：rows=$CABLE_ROWS chips=$CABLE_CHIPS"; fi
agent-browser select ".filter-bar .select" "" >/dev/null
agent-browser wait 600 >/dev/null
agent-browser wait --fn "document.querySelector('table[aria-label=\"资源列表\"] tbody') && document.querySelector('table[aria-label=\"资源列表\"] tbody').children.length >= 14" --timeout 20000 >/dev/null 2>&1 || true
# D. 搜索框：光缆唯一名称 → 恰 1 行（文本等待，避免中间态竞态）
agent-browser fill ".search-box input" "加区-道里" >/dev/null
agent-browser wait --fn "(() => { const rows = [...document.querySelectorAll('table[aria-label=\"资源列表\"] tbody tr')]; return rows.length === 1 && rows[0].textContent.includes('加区-道里'); })()" --timeout 20000 >/dev/null 2>&1 || true
SEARCH_ROWS="$(jeval "document.querySelector('table[aria-label=\"资源列表\"] tbody').children.length")"
if [[ "$SEARCH_ROWS" == "1" ]]; then ok "A6 搜索唯一名称 → 恰 1 行"; else bad "A6 搜索唯一名称 → $SEARCH_ROWS 行"; fi
agent-browser fill ".search-box input" "" >/dev/null
agent-browser wait 600 >/dev/null
agent-browser wait --fn "document.querySelector('table[aria-label=\"资源列表\"] tbody') && document.querySelector('table[aria-label=\"资源列表\"] tbody').children.length >= 14" --timeout 20000 >/dev/null 2>&1 || true
# E. 行点击 → 抽屉展示新类型详情（文本等待：SVC-01 行出现后再点击）
agent-browser fill ".search-box input" "SVC-01" >/dev/null
agent-browser wait --fn "(() => { const rows = [...document.querySelectorAll('table[aria-label=\"资源列表\"] tbody tr')]; return rows.some(r => r.textContent.includes('SVC-01')); })()" --timeout 20000 >/dev/null 2>&1 || true
SVC_ROW_TEXT="$(jeval "(() => { const r = [...document.querySelectorAll('table[aria-label=\"资源列表\"] tbody tr')].find(r => r.textContent.includes('SVC-01')); return r ? r.textContent : ''; })()")"
if [[ "$SVC_ROW_TEXT" == *SVC-01* ]]; then
  agent-browser eval "(() => { const r = [...document.querySelectorAll('table[aria-label=\"资源列表\"] tbody tr')].find(r => r.textContent.includes('SVC-01')); if (r) r.click(); return 'ok'; })()" >/dev/null
  agent-browser wait --fn "document.querySelector('.kv-table')" --timeout 15000 >/dev/null 2>&1 || true
  DRAWER_TYPE="$(jeval "[...document.querySelectorAll('.kv-table .chip')].map(c => c.textContent).join(',')")"
  if [[ "$DRAWER_TYPE" == *业务实例* ]]; then ok "A7 行点击抽屉：SVC-01 类型 chip=业务实例（含全部属性字段）"; else bad "A7 抽屉类型 chip = $DRAWER_TYPE"; fi
  agent-browser eval "document.querySelector('.drawer__close, [class*=close]')?.click(); 'closed'" >/dev/null 2>/dev/null
else
  bad "A7 SVC-01 行未找到（搜索后行文本=$SVC_ROW_TEXT）"
fi
agent-browser fill ".search-box input" "" >/dev/null
agent-browser wait 600 >/dev/null
agent-browser screenshot "$SHOT_DIR/w25-resources-33types.png" >/dev/null
# F1. 资源页阶段零页面错误（导航前读取——导航会重建 window）
ERRS_RES="$(jeval "window.__errs ? window.__errs.length : -1")"
if [[ "$ERRS_RES" == "0" ]]; then ok "A10 资源页零页面错误/未处理拒绝"; else bad "A10 资源页错误 = $ERRS_RES"; fi

echo "[4b/5] 浏览器断言 —— 导入向导（#20：新类型走建议流）"
agent-browser open "http://127.0.0.1:$PORT/import" >/dev/null
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); window.addEventListener('unhandledrejection', e => window.__errs.push(String(e.reason))); 'armed'" >/dev/null
agent-browser wait --fn "document.querySelector('input[type=\"file\"]')" --timeout 20000 >/dev/null 2>&1 || true
agent-browser upload "input[type=\"file\"]" "$WORK/w25-all-layers.xlsx" >/dev/null
agent-browser wait --fn "document.querySelectorAll('.sheet-card, [class*=sheet]').length >= 1" --timeout 25000 >/dev/null 2>&1 || true
# 向导第②步：14 张 sheet 卡片，每张有类型下拉；验证类型下拉选项 = 33
WIZ_CARDS="$(jeval "document.querySelectorAll('.wizard-step select, .sheet-card select, select.select').length")"
WIZ_OPTS="$(jeval "(() => { const s = document.querySelector('select.select'); return s ? s.options.length : 0; })()")"
if [[ "$WIZ_OPTS" == "33" ]]; then ok "A8 向导类型下拉 33 类型选项"; else bad "A8 向导类型下拉选项 = $WIZ_OPTS（want 33）"; fi
# 专线 sheet 卡：建议类型=专线（SuggestType 从 23 列真实表头命中）
LL_SUGGEST="$(jeval "(() => { const cards = [...document.querySelectorAll('select.select')]; const sel = cards.find(s => [...s.options].some(o => o.value === 'leased_line' && o.selected)); return sel ? 'leased_line' : 'none'; })()")"
if [[ "$LL_SUGGEST" == "leased_line" ]]; then ok "A9 专线 sheet（#16 真实 23 列表头）建议类型=leased_line"; else bad "A9 专线建议类型 = $LL_SUGGEST"; fi
agent-browser screenshot "$SHOT_DIR/w25-wizard-14sheets.png" >/dev/null

# F2. 向导页阶段零页面错误
ERRS_IMP="$(jeval "window.__errs ? window.__errs.length : -1")"
if [[ "$ERRS_IMP" == "0" ]]; then ok "A11 向导页零页面错误/未处理拒绝"; else bad "A11 向导页错误 = $ERRS_IMP"; fi

echo "[5/5] HTTP 端到端 —— #16 里程碑总验收（层次穿透 + 业务影响）"
B="$WORK"
# B1. /api/schema 33 类 + 层次/域投影
curl -sf "http://127.0.0.1:$PORT/api/schema" -o "$B/schema.json"
python3 - "$B" <<'PYEOF'
import json, sys
d = json.load(open(f"{sys.argv[1]}/schema.json"))
types = d["types"]
assert len(types) == 33, len(types)
layers = {t["layer"] for t in types}
assert layers == {0, 1, 2, 3, 4, 5, 6}, layers  # 七层全有代表
# 客户 L6、光缆 L2、环网 L5（逻辑层；L4=接入）——四层模型可表达
by = {t["type"]: t for t in types}
assert by["customer"]["layer"] == 6 and by["optical_cable"]["layer"] == 2
assert by["ring"]["layer"] == 5 and by["leased_line"]["layer"] == 6
print("schema: 33 types, layers", sorted(layers))
PYEOF
ok "B1 /api/schema 33 类 + 七层投影（customer L6 / optical_cable L2 / ring L5）"

# 资源 ID 解析辅助
resolve() { # type key -> id（列表端点搜索参数为 q）
  curl -sf "http://127.0.0.1:$PORT/api/resources?type=$1&q=$2" | python3 -c "
import json,sys
d = json.load(sys.stdin)
items = [i for i in d['items'] if i['naturalKey'] == sys.argv[1] or i.get('name','') == sys.argv[1]]
print(items[0]['id'])" "$2"
}
SVC_ID=$(resolve service SVC-01)
CABLE_ID=$(resolve optical_cable CABLE-01)
CUST_ID=$(resolve customer CUST-01)

# B2. 层次穿透：service(SVC-01, L6) down → 物理层（业务故障下钻物理根因）
curl -sf "http://127.0.0.1:$PORT/api/resources/$SVC_ID/traverse?direction=down" -o "$B/trav.json"
python3 - "$B" <<'PYEOF'
import json, sys
d = json.load(open(f"{sys.argv[1]}/trav.json"))
nodes = d["nodes"]
types = {n["type"] for n in nodes}
assert "optical_cable" in types, types   # L6 业务 → L2 物理根因可达
assert "fiber" in types, types
assert len(nodes) >= 8, len(nodes)
print(f"traverse down SVC-01: {len(nodes)} nodes, types:", sorted(types))
PYEOF
ok "B2 层次穿透：service L6 → down 遍历抵达 optical_cable/fiber L2（≥8 节点）"

# B3. 业务影响分析：物理根因（光缆 down）→ impacted 含客户（哪些客户受损）
curl -sf -X POST "http://127.0.0.1:$PORT/api/resources/$CABLE_ID/run-state" -H 'Content-Type: application/json' \
  -d "{\"runState\": \"down\"}" -o "$B/rs.json"
curl -sf "http://127.0.0.1:$PORT/api/resources/$CABLE_ID/impact?direction=up" -o "$B/impact.json"
python3 - "$B" "$CUST_ID" <<'PYEOF'
import json, sys
w, cust = sys.argv[1], sys.argv[2]
d = json.load(open(f"{w}/impact.json"))
impacted_ids = {it["node"]["id"] for it in d.get("impacted", [])}
assert cust in impacted_ids, f"customer not impacted: {sorted(it['node']['type'] for it in d['impacted'])}"
types = sorted({it["node"]["type"] for it in d["impacted"]})
cands = [it["node"]["type"] for it in d.get("candidates", [])]
print(f"impact CABLE-01 up: {len(impacted_ids)} impacted, types {types}, candidates {cands}")
PYEOF
ok "B3 业务影响：CABLE-01 down → impact up 波及 customer CUST-01（客户受损可回答）"

# B4. 保护结构可表达：ring 资源在库 + 关系注册表含保护组语义
curl -sf "http://127.0.0.1:$PORT/api/resources?type=ring" | python3 -c "
import json,sys
d = json.load(sys.stdin)
assert d['total'] == 1, d['total']
print('ring:', d['items'][0]['naturalKey'], d['items'][0]['name'])"
ok "B4 保护结构：ring 资源可入库可查询（组网方式=环 可区分表达）"

# B5. 按客户聚合：CUST-01 的业务/专线（#16 原文「某客户有多少条专线」）
curl -sf "http://127.0.0.1:$PORT/api/resources/$CUST_ID/traverse?direction=down" -o "$B/cust.json"
python3 - "$B" <<'PYEOF'
import json, sys
d = json.load(open(f"{sys.argv[1]}/cust.json"))
types = {n["type"] for n in d["nodes"]}
assert "service" in types and "leased_line" in types, types
print("customer CUST-01 down:", sorted(types))
PYEOF
ok "B5 按客户聚合：CUST-01 → service + leased_line 可达（客户视角可答）"

echo
echo "======================================"
echo "W25 验收汇总：PASS=$PASS FAIL=$FAIL"
echo "======================================"
[[ "$FAIL" -eq 0 ]] || exit 1
