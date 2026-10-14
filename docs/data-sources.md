# 数据来源与许可披露（v0.8.0 真实公开数据里程碑）

> 诚实为本：本文档逐项披露仓库内每类数据的来源、真实性口径与许可约束。
> v0.8.0 起，演示数据集以**互联网公开数据**为骨架（用户指令：「数据用互联网
> 公开数据填充，完全找不到的用模拟数据」），模拟数据仅填充公开渠道不存在的
> 台账细节，且在 `testdata/realdata/manifest.json` 与本文档双处披露。
> 生成管线：`scripts/realdata/`（extract.py / workbooks.py / metrics.py，
> 全部确定性——同输入字节级同输出）。

## 1. 真实公开数据（可溯源、可复算）

| 数据 | 来源 | 许可 | 仓库位置 | 真实性口径 |
| --- | --- | --- | --- | --- |
| 局站（83 个） | OSM place 节点（Geofabrik 黑龙江 extract 2026-10-12） | ODbL 1.0 | `testdata/realdata/sites.xlsx` + `realdata-core.json` | 名称/坐标真实；**局站ID = OSM node id 派生**（`ST` + 8 位零填充）；地市=最近地市归属 |
| 无线基站（1,198 站 / 1,851 小区） | OpenCelliD 全球快照 2026-10-06（经 HuggingFace 镜像 `yuiseki/opencellid`，其 provenance.yaml 记录原始下载链路与许可） | **CC BY-SA 4.0** | `testdata/realdata/base_stations.xlsx` + `web/public/mapdata/hlj-basestations*.geojson.gz` | 位置/制式（GSM/UMTS/LTE/NR）/运营商（MNC 映射）/地市真实；**基站编码 = 主小区 CGI**（MCC-MNC-LAC-CID，站按 ~110m 网格聚类）；名称=最近局站派生；W30 起经纬度随表入库（REAL 列，migration v20，issue #45） |
| 干线走廊（7 条） | OSM 道路/铁路几何**按线路名选取**（绥满高速 G10/滨洲线/滨绥线/哈佳线/图佳线/齐北线+富嫩线/嫩林线/京漠线 G111 等） | ODbL 1.0 | `web/public/mapdata/hlj-cables*.geojson.gz` | 走廊=真实公路/铁路几何（RDP 简化 ~150m）；**走廊并集长度含平行车道/复线重复计数**，仅作图层参考 |
| 光缆（7 条） | 走廊锚点局 + 锚点链测地里程 | 派生（ODbL） | `testdata/realdata/optical_cables.xlsx` | A/Z 端局站真实；**长度 = 走廊锚点链 haversine 累计**（真实测地里程；实际公路里程通常再高 5–15%，因迂回） |
| 光缆段（23 段） | 走廊锚点局对 | 派生 | `cable_segments.xlsx` | 段端点=真实锚点局；**段界是走廊锚点，不是运营商实际接头位置** |
| 接头盒/人井坐标 | 锚点局坐标 / 走廊线性内插 | 派生 | `splice_boxes.xlsx` `manholes.xlsx` | 坐标真实（内插点在真实坐标系）；设施本身为示意 |
| 地表覆盖/地名/等高线/DEM | OSM + AWS Terrain Tiles（SRTM·GMTED，terrarium） | ODbL / Mapzen·AWS Open Data | `web/public/mapdata/*`（v0.6.4/W17 既有） | 详见各自生成脚本头注释 |

**MNC → 运营商映射**（460 网号，公开分配表）：00 移动 / 01 联通 / 11 电信 /
15 广电。OpenCelliD 为众源数据——黑龙江覆盖集中于哈尔滨（672 站）与
大庆（520 站），**不代表市场份额**，是众源采样密度的现实（如实披露，不粉饰）。

## 2. 模拟数据（公开渠道不存在，逐项披露）

| 数据 | 披露位置 | 说明 |
| --- | --- | --- |
| 机房（83） | manifest `simulated` | 编号/名称模拟；归属局站真实 |
| 纤芯占用/使用情况/局向（56×8 芯） | manifest | 示范值；波长为 ITU 100GHz 网格真实值但占用关系为示范 |
| 波道/时隙/环网/IGP域/VPN（保护与路由结构） | manifest | 工程示意结构，锚点真实 |
| 电路（32）/地址池（5） | manifest | A/Z 设备为真实布点；编号/带宽/CIDR 模拟；W30 三族结构（相邻环段 15 + 跨域长距 14 + 接入层 3，issues #42） |
| 基站上联（1,198 条 uplink_to） | manifest `base_station_uplink` | **站坐标→15 个设备局站坐标的 haversine 最近邻**（#42 裁定：坐标派生，非名称匹配，不违反「禁止名称推断」红线）；归属A IP/名称/端口 = 该设备属性 |
| 基站服务客户（1,198 条 serves） | manifest `base_station_serves` | 地市→客户确定性映射（哈尔滨双客户轮转/大庆/黑河单客户）；真实客户关系涉密，全为虚构客户 |
| 设备/基站服务客户（30 条 serves） | manifest | 设备地市→客户映射（#40：设备→客户直连边） |
| 端口所属板卡（90 条 mounted_on） | manifest | 每设备 3 端口挂 2 板卡的确定性回绕（#40） |
| 保护组（4 组 + protects 边） | manifest `protection_groups` | 1+1/1:1/环网三类型示范；工作/保护路径=干线名（#40） |
| 各台账 状态 列 | manifest `status_columns` | 确定性示范值（在网为主，少量停用/在建/退网）；W30 前的 ip_pool「使用中」/service「开通」为封闭枚举外非法值（13 条 W002），已修正（#40） |
| ptp_port（4 行） | `samples/ptp-config-script.txt` | v0.5 既有解析通道，demo-real.sh W30 起纳入（#40：2 类无数据 → 1 类） |
| 客户（12）/业务（8）/专线（6） | manifest | **全部虚构**（名称含虚构标记或明显虚构词）；专线装机地址=真实局站名+「演示装机地址」标记 |
| 设备（30）/端口/板卡/机架/ODF/PON 家族 | manifest | 所在局站/机房/**坐标真实**；编码/IP（私网演示段）/型号（真实产品族名作示范值）模拟 |
| 端口性能 7 日（90 序列） | `metrics.py` 头注释 | 模拟形态：正常日稳定 + 哈大齐两站光功率 7 日 -8→-24 dBm 线性劣化 + 丢包率 0→4.8%（触发种子检测规则 → firing 告警 → run_state 联动）；数值钳制在指标目录 advisory 界内 |

## 3. 明确不采用的数据源（与理由）

- **高德 POI**：Web 服务 API 的条款限制数据缓存/再分发。实测可达（营业厅等
  真实 POI 可查询），但**不将其数据嵌入仓库**——高德在产品中的角色限于
  在线底图（既有底图四模式之一）。这是与「内嵌数据必须可合规再分发」
  原则一致的设计裁决（ADR-0015）。
- **RIPE Atlas**：可达，但黑龙江无公共探针，无法提供本省真实时延——
  性能数据继续用确定性模拟（上表）。

## 4. 许可合规要点

- **OpenStreetMap（ODbL 1.0）**：随二进制/仓库分发的派生数据须署名
  © OpenStreetMap contributors；地图图层 attribution 已在 UI 呈现，本文件
  为文档级署名。
- **OpenCelliD（CC BY-SA 4.0）**：署名 + 相同方式共享。`base_stations.xlsx`
  与 `hlj-basestations*.geojson.gz` 是 OpenCelliD 数据的**改编**（过滤/
  聚类/派生字段）；私有仓库内使用合规，**若仓库转为公开发布，这些文件
  须以 CC BY-SA 4.0 提供并注明已作修改**（本节即为该声明；地图图层
  attribution 同步呈现「基站: OpenCelliD (CC BY-SA 4.0)」）。
- **AWS Terrain Tiles / Mapzen**：既有 v0.6 处理不变（DEM 瓦片 attribution）。

## 5. 重现管线

```
# 输入（仓库外，mapdata/ 目录）：
#   heilongjiang-2026-10-12.osm.pbf   ← Geofabrik HLJ extract（ODbL）
#   cell_towers.csv.gz                ← OpenCelliD 快照（CC BY-SA 4.0）
python3 scripts/realdata/extract.py     # → 地图图层 + realdata-core.json
python3 scripts/realdata/workbooks.py  # → 31 本工作簿 + manifest.json
python3 scripts/realdata/metrics.py    # → perf-day-01..07 + ingest-mapping.json
./scripts/demo-real.sh                 # → 一键全链路演示
./scripts/w28_browser_regression.sh    # → 浏览器级图层回归（14 断言）
```

管线确定性已验证：连续两次运行 md5 一致（gzip mtime=0、坐标 5 位量化、
全序排序）。`internal/app/e2e_realdata_test.go` 钉死 31 本工作簿的导入
计数与跨层影响链，防止数据集漂移。
