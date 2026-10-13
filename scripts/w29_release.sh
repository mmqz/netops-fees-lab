#!/usr/bin/env bash
# w29_release.sh — v0.8.0 GitHub Release（source tarball + linux 二进制双资产）。
# PAT 运行时从 ~/.git-credentials 读取（600 权限，不入库）。
set -euo pipefail

REPO_ROOT="/home/z/my-project/netops-lab"
cd "$REPO_ROOT"
TAG="v0.8.0"
REL="$(mktemp -d /tmp/netops-rel-v08.XXXXXX)"

TOKEN=$(python3 -c "print(open('/home/z/.git-credentials').read().strip().split(':')[2].split('@')[0])")
AUTH=(-H "Authorization: token $TOKEN" -H "Accept: application/vnd.github+json")

# 1. source tarball from the tagged tree
git archive --format=tar.gz --prefix="netops-lab-$TAG/" -o "$REL/netops-lab-$TAG-source.tar.gz" "$TAG"

# 2. binary tarball（bin/netopslab 已构建含最新 dist）
tar -czf "$REL/netops-lab-$TAG-linux-amd64.tar.gz" -C "$REPO_ROOT" --transform "s|^bin/netopslab|netopslab|" bin/netopslab

# 3. create release
cat > "$REL/body.json" << 'EOF'
{
  "tag_name": "v0.8.0",
  "name": "v0.8.0 — 真实公开数据里程碑",
  "body": "演示数据集以互联网公开数据为骨架（用户指令：数据用互联网公开数据填充，完全找不到的用模拟数据）：\n\n**真实数据（可溯源）**\n- 83 个真实局站 — OSM place 节点（OSM node id 派生身份）\n- 1,198 个真实基站（1,851 小区，含 844 个 NR 5G 小区）— OpenCelliD 2026-10-06 快照（© OpenCelliD，CC BY-SA 4.0），主小区 CGI 为站编码\n- 7 条干线走廊真实几何 — 哈大齐/哈牡/哈佳/佳牡/齐加/加漠/哈伊（绥满高速 G10·滨洲线·嫩林线·京漠线 G111 等按线路名选取，© OpenStreetMap contributors）\n- 光缆 A/Z 端局站与锚点链测地里程（哈大齐 273.6km 等真实量测）\n\n**模拟数据（逐字段披露）**：机房/纤芯占用/波道/电路/IP/客户/业务等台账细节——`docs/data-sources.md` + `testdata/realdata/manifest.json` 双处披露；高德 POI 因再分发限制不内嵌（ADR-0015）。\n\n**新能力**\n- 地理视图两个离线真实数据图层（双坐标变体）：光缆干线（走廊+里程标注，默认开）/ 无线基站（运营商配色 1,198 点，默认关一键开）\n- `scripts/demo-real.sh` 一键全链路：31 工作簿导入（1,965 行零失败）→ 7 日指标 → 哈大齐劣化 4 条 firing 告警 → 物化 → 光缆置 down → 80 节点跨层影响面\n- 管线确定性：`scripts/realdata/` 三件套字节级可重现（md5 双跑一致）\n\n**质量口径**：429 顶层测试 23 包全绿 · 浏览器级回归 14/14 · 四演示脚本零回归 · e2e 数据集漂移防护。详阅 CHANGELOG 与 docs/verified-capabilities.md v0.8.0 段。",
  "draft": false,
  "prerelease": false
}
EOF
ID=$(curl -s "${AUTH[@]}" -H "Content-Type: application/json" --data @"$REL/body.json" \
  "https://api.github.com/repos/mmqz/netops-lab/releases" | python3 -c "import json,sys; print(json.load(sys.stdin)['id'])")
echo "release id=$ID"

# 4. upload assets
for f in "netops-lab-$TAG-source.tar.gz" "netops-lab-$TAG-linux-amd64.tar.gz"; do
  curl -s "${AUTH[@]}" -H "Content-Type: application/octet-stream" \
    --data-binary "@$REL/$f" \
    "https://uploads.github.com/repos/mmqz/netops-lab/releases/$ID/assets?name=$f" | python3 -c "import json,sys; d=json.load(sys.stdin); print('uploaded:', d.get('name'), d.get('state'), d.get('size'))"
done
echo "done: https://github.com/mmqz/netops-lab/releases/tag/$TAG"
