# [P1] 地图画布渲染缓慢：默认加载 10.6 万要素 / 34 MB，缺简化与降级

来源：用户 2026-10-15 现场反馈（v0.11.0 实际使用）

用户原话：

> **「随着内容变多地图画布的渲染很慢，这个得处理」**

---

## 一、实测：瓶颈在前端，不在后端

### 后端接口（3 次取最小）——**很快，不是瓶颈**

```
/api/workspaces            2.5 ms     0.5 KB
/api/resources?pageSize=200 16.2 ms   92.7 KB
/api/schema                 2.4 ms    81.8 KB
/api/workspaces/{id}/topology 9.3 ms 24.6 KB
/api/alerts                18.0 ms     3.0 KB
```

### 地图图层——**这才是瓶颈**

| 图层 | 要素数 | 解压后 | JSON 解析 |
| --- | --- | --- | --- |
| `hlj-natural` | **29,449** | **16.0 MB** | 309 ms |
| `hlj-contours` | 18,604 | 7.3 MB | 279 ms |
| `hlj-roads-main` | 22,158 | 4.3 MB | 95 ms |
| `hlj-places` | 17,615 | 2.4 MB | 38 ms |
| `hlj-railways` | 17,257 | 2.8 MB | 51 ms |
| `hlj-admin-areas` | 147 | 1.8 MB | 40 ms |
| `hlj-cables` | 14 | 0.27 MB | 9 ms |
| `hlj-basestations` | 1,198 | 0.19 MB | 3 ms |
| **合计** | **106,442** | **34.2 MB** | **~825 ms（纯解析）** |

**`hlj-natural` 单独就有 65 万个坐标点**：

```
几何类型：Polygon 22,399 + MultiPolygon 37 + LineString 7,013
坐标数组元素数：654,654
```

## 二、根因

### 2.1 要素数量级过大

MapLibre 需要：下载 → gunzip → `JSON.parse` → 建索引 → 三角化（面）→ 上传 GPU。
10.6 万要素在浏览器主线程上**至少数百毫秒到数秒**（视设备而定），
且每次 `setStyle`/图层切换会**重做**。

叠加 v0.8.1 已修但同源的问题（#43：选中即全量重建 GeoJSON），
**交互期间的总代价 = 数据重建 + 图层重绘**。

### 2.2 默认几乎全开

`geoBasemap.ts:238-257` `DEFAULT_PREFS`：

```ts
admin: true,    // 1.8 MB
road:  true,    // 4.3 MB
forest:true,    // ┐
water: true,    // ├ 同属 hlj-natural，共 16 MB
green: true,    // ┘
places:true,    // 2.4 MB
cable: true,    // 0.27 MB
terrain:  true, // DEM 瓦片
contours: true, // 7.3 MB
```

**即首次进入地图视图就要加载并渲染约 34 MB / 10.6 万要素。**

### 2.3 无几何简化

`hlj-natural` 的 22,399 个多边形平均每个 29 个顶点——**未做 RDP 简化**。
（对比：`scripts/realdata/extract.py` 对光缆走廊做了 RDP ~150m，效果好得多。）

## 三、影响

| 场景 | 现状 |
| --- | --- |
| 首次打开地图 | 数秒白屏/卡顿 |
| 切换视图（2D↔3D） | 图层重建，再次卡顿 |
| 拖动/缩放 | 视口裁剪缺失，全要素参与 |
| 低端设备 | 可能直接卡死 |

## 四、修复方向

### 方向 1：数据侧——预简化 + 分级（**最优，收益最大**）

对 `hlj-natural` / `hlj-contours` / `hlj-places` / `hlj-railways` 做**缩放级分级**：

```
hlj-natural-z6.geojson.gz    低缩放级：仅大面要素（RDP 500m）
hlj-natural-z9.geojson.gz    中缩放级：RDP 150m
hlj-natural-z12.geojson.gz   高缩放级：原精度
```

前端按当前 zoom 选层加载（MapLibre 惯例做法）。

**预期**：低缩放级要素数与体积可降 **80-95%**。

### 方向 2：默认降级——重图层默认关

```ts
// 建议默认
admin: true,      // 区划是导航基础，保留
cable: true,      // 真实网络数据，是主叙事
places: true,     // 地名，体积小（2.4MB）
road: false,      // 4.3MB，按需开
forest/water/green: false,  // 合计 16MB，按需开
contours: false,  // 7.3MB，按需开
terrain: false,   // DEM 瓦片，按需开
```

**并给「一键恢复完整底图」按钮**，兼顾观感与性能。

### 方向 3：加载策略

- **懒加载**：图层在用户勾选时才 fetch（当前疑似启动即全加载）
- **视口裁剪**：MapLibre 自带，但要素过多时仍需先简化
- **Loading 指示**：解析期间显示进度，避免"卡死了吗"的观感
- **Web Worker 解析**：把 `JSON.parse` 移出主线程

### 方向 4：格式升级（长期）

GeoJSON 对 10 万级要素不合适，可考虑：
- **PMTiles / MBTiles**（矢量瓦片，按需加载）
- **FlatGeobuf**（流式、空间索引）
- 或直接由 PBF 生成矢量瓦片

**这是根治方案**，但工程量大，建议放在方向 1/2 之后。

## 五、验收

- [ ] 首次进入地图视图 **首帧 < 1s**（目标），可交互 < 2s
- [ ] 默认图层组合的解压总量 **< 3 MB**
- [ ] `hlj-natural` 等大图层有缩放级分级（或等效简化）
- [ ] 图层懒加载：未勾选的图层不发网络请求
- [ ] 提供"完整底图"开关，但不作为默认
- [ ] 性能基线纳入回归（记录首帧耗时与要素数，防再次膨胀）

## 附：复现方式

```python
# 统计各图层要素数与解压体积
for f in ['hlj-natural.geojson.gz', 'hlj-contours.geojson.gz', ...]:
    raw = urlopen(f'/mapdata/{f}').read()
    dec = gzip.decompress(raw)
    n = len(json.loads(dec)['features'])
    print(f, n, len(dec)/1024, 'KB')
```