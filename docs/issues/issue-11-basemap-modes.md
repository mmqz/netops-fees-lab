# 地图底图：仅单一极简英文栅格底图，缺少多模式切换（无行政区划/交通/中文）

## 环境
- 平台：**所有平台**
- 版本：v0.6.0 @ 2033de8
- 视图：工作台 →「2D 地图」/「3D 地图」

## 现象
地图底图被**硬编码为单一图层**，无法切换，且信息量过低：

1. **标注为英文**：仅显示 `Nenjiang` 等英文地名，无中文（目标用户为中文运维场景）
2. **无行政区划**：省市县边界、行政中心名称均缺失
3. **无交通要素**：公路、铁路、桥梁、国道编号全部缺失
4. **无模式切换**：界面上只有「拓扑图 / 2D 地图 / 3D 地图」三个**视图**切换，
   没有「底图/图层」维度的选择（卫星图、街道图、极简图等）

实测（放大至 20km 比例尺）：底图仅剩一条英文标注「Nenjiang」与水系轮廓线，
无任何道路或行政边界，无法用于判断设备所处行政区域或交通沿线位置。

## 根因
`web/src/components/lab/GeoCanvas.tsx` 中底图**硬编码**，仅注册两个 raster source：
```ts
// :68-71 —— 常量写死，无备选
const ESRI_DARK_BASE_TILES =
  'https://server.arcgisonline.com/ArcGIS/rest/services/Canvas/World_Dark_Gray_Base/MapServer/tile/{z}/{y}/{x}';
const ESRI_DARK_REF_TILES =
  'https://server.arcgisonline.com/ArcGIS/rest/services/Canvas/World_Dark_Gray_Reference/MapServer/tile/{z}/{y}/{x}';
```
```ts
// :372-384 —— 直接写入 style，无用户可选维度
sources[BASEMAP_SOURCE_ID] = { type: 'raster', tiles: [ESRI_DARK_BASE_TILES], ... };
sources[BASEMAP_REF_SOURCE_ID] = { type: 'raster', tiles: [ESRI_DARK_REF_TILES], ... };
```

代码注释（:65-67）表明当初选型是**为了规避 CARTO 的水印限制**，
选择标准是「免密钥可用」而非「信息丰富度」，因此落到了
`Canvas/World_Dark_Gray` —— ESRI 的**极简画布底图**（设计定位即「近乎无标注的背景」）。

这与运维场景的实际需求不符：运维需要辨认设备所在的行政区、
是否沿铁路/公路分布，而这些正是极简底图**故意省略**的信息。

## 候选方案（已实测可用性）

| 方案 | 类型 | 中文 | 行政区划 | 交通 | 密钥 | 实测 |
| --- | --- | --- | --- | --- | --- | --- |
| 当前 ESRI Dark Gray | raster | ❌ | ❌ | ❌ | 免 | — |
| **OpenFreeMap** | **vector** | ✅ | ✅ | ✅ | **免** | **HTTP 200，111–119 图层** |
| ESRI Light Gray | raster | ❌ | 部分 | ❌ | 免 | HTTP 200 |
| ESRI World Imagery | raster | ❌ | ❌ | ❌ | 免 | HTTP 200（卫星） |

### OpenFreeMap 实测结果
```
styles/liberty   → 200, 111 图层
styles/bright    → 200, 119 图层
styles/positron  → 200,  55 图层
```
标注层使用 `name:nonlatin` 字段（原生支持中文）：
```
['case', ['has','name:nonlatin'],
  ['concat', ['get','name:latin'], ' ', ['get','name:nonlatin']],
  ['coalesce', ['get','name_en'], ['get','name']]]
```
实际渲染确认：
- 中英双语地名（嫩江市 / 临江乡 / 塔温敖宝镇）
- 公路与国道编号（G4512）
- 河流、湖泊、山体阴影
- 多级行政地名（省 / 市 / 镇 / 乡）

## 影响
- 底图**信息量不足**，运维时无法定位设备所属行政区或交通沿线
- 英文标注与中文运维场景不匹配
- **无切换能力**：偏好极简（当前）或丰富（街道/卫星）的用户都无法调整
- 3D 视图同样受限于此（无地形，见 issue-09）

## 建议修复方向（讨论用）

引入**底图模式选择**，至少提供：
- **极简**（保留当前 ESRI 深色，适合只看设备分布）
- **街道/标准**（vector，含行政区划 + 道路 + 中文标注）
- **卫星**（影像底图）

实现要点：
1. 把底图来源抽成配置数组，而非硬编码常量
2. vector 底图需引入 style URL 而非 tiles 数组（OpenFreeMap 提供现成 style）
3. 保持深色主题一致性：OpenFreeMap 仅有浅色主题，
   若需深色 + 中文，需自建样式或另寻 provider
4. 与 issue-09 的 3D 地形需求一并考虑（terrain source 可与底图绑定）

## 注意事项

- **外部依赖**：OpenFreeMap 是公网服务。若最终部署在内网（如网管所在环境），
  需评估可达性，或自建瓦片服务 / 离线底图
- 免费服务的**可用性无 SLA**，生产使用需评估
- 切换 provider 会改变 attribution 文案（合规要求，必须同步更新）

## 验收标准
- [ ] 底图可在至少 2 种模式间切换（极简 / 信息丰富）
- [ ] 信息丰富模式下显示中文地名
- [ ] 信息丰富模式下显示行政区划边界
- [ ] 信息丰富模式下显示道路（含编号）
- [ ] 切换底图时设备、链路等业务图层不受影响
- [ ] attribution 随底图 provider 正确更新
- [ ] 底图加载失败时有降级表现（不白屏）
