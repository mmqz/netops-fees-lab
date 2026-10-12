# mapdata/ — 离线地图数据

本目录**不存放地图数据文件**，只记录数据来源与下载地址——数据托管在 GitHub Releases，避免撑大仓库。

## 可用数据

### 黑龙江省（2026-10-12）

**Release 页面**：https://github.com/mmqz/netops-lab/releases/tag/mapdata-hlj-2026-10-12

| 文件 | 内容 | 大小 |
| --- | --- | --- |
| `heilongjiang-2026-10-12.osm.pbf` | 全省真全量（所有 OSM 要素与标签） | 35.14 MB |
| `hlj-admin-areas.geojson.gz` | 行政区划面（省/市/县，147 个） | 1.23 MB |
| `hlj-roads-main.geojson.gz` | 主要公路网（22,158 条） | 2.18 MB |
| `hlj-roads-daxinganling-full.geojson.gz` | 大兴安岭全量路网（5,012 条） | 0.55 MB |
| `hlj-railways.geojson.gz` | 铁路（17,257 条） | 1.03 MB |

### 直链下载

```bash
BASE=https://github.com/mmqz/netops-lab/releases/download/mapdata-hlj-2026-10-12

curl -LO $BASE/heilongjiang-2026-10-12.osm.pbf
curl -LO $BASE/hlj-admin-areas.geojson.gz
curl -LO $BASE/hlj-roads-main.geojson.gz
curl -LO $BASE/hlj-roads-daxinganling-full.geojson.gz
curl -LO $BASE/hlj-railways.geojson.gz
```

### 用 gh CLI 下载

```bash
gh release download mapdata-hlj-2026-10-12 --repo mmqz/netops-lab
# 只取单个文件：
gh release download mapdata-hlj-2026-10-12 --repo mmqz/netops-lab -p 'hlj-admin-areas.geojson.gz'
```

## 完整性校验

```
MD5    9d3f9d2156ac1e878e3f498fb663d541
SHA256 4FD56073FFE6A73E548ECAAB982A45B9E452B26B6B8592160ACBA1439079D675
```

（MD5 与 Geofabrik 官方发布值一致）

## 来源与许可

- 数据来自 [OpenStreetMap](https://www.openstreetmap.org/)，经 [Geofabrik](https://download.geofabrik.de/asia/china.html) 导出
- 许可：**ODbL 1.0**
- **使用时必须署名**：`© OpenStreetMap contributors`

## 为什么同时给 PBF 和 GeoJSON

- **PBF**：最完整的原始数据。河流、土地利用、POI、建筑等要素都在里面，使用方可自行提取本项目未提供的图层：

```bash
# 例：提取水系
osmium tags-filter heilongjiang-2026-10-12.osm.pbf w/waterway -o water.osm.pbf
osmium export water.osm.pbf -o water.geojson
```

- **GeoJSON**：已提取的常用图层，解压即用，无需 OSM 工具链。

## 已知限制

- **数据时效**：OSM 由志愿者维护，偏远地区更新可能滞后；适合作为运维参考底图，不宜作为权威测绘数据。
- **坐标系**：WGS84。如需与 GCJ-02 数据叠加，需自行转换。
- **合规**：ODbL 允许存储、缓存、离线与商用（需署名）。若用于对外提供地图服务，请另行确认测绘资质要求。

## 相关 Issue

- [#11 地图底图：仅单一极简英文栅格底图，缺少行政区划/交通/中文，无模式切换](https://github.com/mmqz/netops-lab/issues/11)

## 后续计划

- 接入 `netops-lab` 作为离线底图 provider（配合在线底图的降级链）
- 视需要扩展内蒙古大兴安岭部分（呼伦贝尔根河市、鄂伦春旗等）

## 运行时资产（随二进制分发）

前端离线叠加图层的**运行时数据**不放在本目录，而在 `web/public/mapdata/`（v0.6.1 W16）：
本目录描述的 Release 原始数据经 RDP 简化（admin 0.0006° / rail 0.0005° / roads 0.0005° / dxa 0.0003°）+
属性精简 + 5 位小数圆整后共 ~1.7MB（.geojson.gz），随 vite 构建进 `internal/webui/dist` 并
`go:embed` 进二进制——内网部署无需回 Release 下载。原始 40MB PBF 仍只在 Release（不入库）。

## DEM 地形瓦片（v0.6.2 新增）

`web/public/mapdata/dem/{z}/{x}/{y}.png`：黑龙江 bbox（121.6–128.2E / 45.0–53.6N）
z4–z8 共 97 张 terrarium 编码高程瓦片（≈6.4MB），源为 AWS Terrain Tiles
（Mapzen 数据管线，SRTM/GMTED 融合，公开免密钥，© Mapzen / AWS Open Data）。
随二进制 go:embed 分发，3D 视图地形起伏与 2D 山体阴影共用；重跑/扩 bbox：
`python3 /home/z/my-project/scripts/download_dem_tiles.py`（校验 PNG 256×256
与高程解码范围后落盘，幂等）。
