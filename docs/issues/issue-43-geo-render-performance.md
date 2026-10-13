# [P2] 地理视图卡顿：每次点击画布都重新渲染

来源：用户 2026-10-13 现场反馈（v0.8.0 真实数据演示）

用户原话：

> **「加载问题，我每次点击画布地图的数据都要重新渲染，卡顿」**

## 一、根因 A：选中态触发全量 GeoJSON 重建

`web/src/components/lab/GeoCanvas.tsx:719`：

```tsx
const geoData = useMemo(() => buildGeoData(topology, selected, gcj), [topology, selected, gcj]);
```

**`selected` 在依赖里** → 每次点击节点/链路，`buildGeoData` **全量重建**，
然后 `applyDataToMap` 对 source 调 `setData`。

`setData` 的性能与 feature 数量成正比，而该 GeoJSON 含：

```
设备点（30）+ 接口（20）+ 链路（10）+ 基站（1198）+ 局站（83）+ ...
```

**即每次选中一个小节点，要重新序列化并下发上千个 feature。**

## 二、根因 B：簇标记全量拆除 + 重建

`GeoCanvas.tsx:1045-1046`（`updateClusters`）：

```tsx
clusterMarkersRef.current.forEach((entry) => entry.marker.remove());  // 全删
clusterMarkersRef.current.clear();
// ... 然后全量重建，O(n·k) 屏幕距离计算
```

注释自称「典型规模 ≤几百设备，O(n·k) 可忽略」，
但真实数据是 **1,198 基站 + 83 局站**，且 `move`/`moveend`/`zoom`/`zoomend`
**四个事件全部** rAF 节流重算（`GeoCanvas.tsx:908-909`）。

**注释的规模假设已被 v0.8.0 的真实数据打破**——这是典型的「假设过时」缺陷。

## 三、根因 C：塔体重建也在 zoom 期间全量跑

`GeoCanvas.tsx:889-890`：`map.on('zoom', scheduleTowerRebuild)` + `zoomend`，
重建 `footprint` + `heightM`（每个塔计算 metersPerPixel）。

与 B 叠加：**缩放一次 = 塔全量重建 + 簇全量拆建 + GeoJSON 可能重下**。

## 四、影响

| 操作 | 现状 | 期望 |
| --- | --- | --- |
| 点击节点 | 全量 GeoJSON setData | 仅更新选中 feature 的 properties |
| 拖拽/缩放 | 簇全量拆建 + 塔全量重建 | 增量更新 / 视口裁剪 |
| 1,198 基站 | 全部参与每次重算 | 视口内 + 聚簇抽样 |

## 五、建议

1. **选中态与几何数据解耦**：
   - `buildGeoData` 的依赖**移除 `selected`**，几何只随 `topology` 变化
   - 选中态改用 MapLibre 的 **feature-state**（`setFeatureState`）
     或独立的「选中高亮」layer（只含 1 个 feature），避免全量 `setData`
2. **簇标记增量化**：复用已有 marker，只增删差集；或改用 MapLibre 原生
   `cluster: true` 的 GeoJSON source（GPU 侧聚簇，无 DOM marker）
3. **视口裁剪**：只对 `map.getBounds()` 内的要素建 DOM marker
4. **真实规模回归**：把「1,198 基站」纳入性能测试基线，
   断言交互帧率/耗时，防止「注释里的规模假设」再次过时

## 验收

- [ ] 点击节点不再触发全量 GeoJSON 重建（可用 DevTools 断言 setData 次数）
- [ ] 1,198 基站下拖拽/缩放无可感卡顿（帧率或耗时量化）
- [ ] 性能基线测试覆盖真实规模