# 2D/3D 地图完全不显示（画布高度塌陷为 0）

> **这是地图黑屏的主因，与平台无关。**
> 先前报告将主因归为 `.mjs` MIME 缺陷（issue-02），实测证明顺序相反：
> 即使在 MIME 正常的 Linux 上，地图同样黑屏，根因是本条 CSS 缺陷。

## 环境
- 平台：**所有平台**（Windows 与 Linux/Docker 均复现）
- 版本：v0.6.0 @ 2033de8
- 视图：工作台 →「2D 地图」或「3D 地图」

## 现象
切换 2D/3D 地图后，画布区域一片深色空白，仅剩图例浮层。
MapLibre 实例本身「已加载」（`window.__geomap.loaded() === true`），
底图瓦片也成功下载（HTTP 200），但**画布高度只有 300px 且内容不可见**。

## 关键证据

### 实测尺寸
```
修复前: .geo-map rect = 756 x 0        ← 高度 0
        .maplibregl-canvas = 756x300  ← MapLibre 回退默认值
修复后: .geo-map rect = 756 x 644      ← 撑满父容器
        .maplibregl-canvas = 756x644
```

### 计算样式矛盾
```
匹配到的 CSS 规则: .geo-map { position: absolute; inset: 0 }   ← 期望
实际 computed position: relative                                ← 被覆盖
元素内联样式: null                                              ← 无内联覆盖
```

### 样式表内容
```
index-CnbC3dw0.css       → .geo-map        { position: absolute }   (0,1,0)
vendor-maplibre-*.css    → .maplibregl-map { position: relative }   (0,1,0)
```

## 根因
MapLibre 在初始化时给地图容器**追加 `maplibregl-map` class**
（`web/src/components/lab/GeoCanvas.tsx:935` 的 `className="geo-map"`），
于是元素同时携带两个 class：

| 规则 | 特异性 | 加载顺序 |
| --- | --- | --- |
| `.geo-map { position: absolute; inset: 0 }` | (0,1,0) | index.css 先 |
| `.maplibregl-map { position: relative }` | (0,1,0) | vendor-maplibre.css 后 |

**特异性相同 → 后加载者胜出** → `position: relative` 生效。
容器失去 `position: absolute; inset: 0` 后**高度塌陷为 0**，
MapLibre 读到 0 高度，将 canvas 退回默认 300px，地图内容无法铺满，区域呈空白。

## 已实施的修复（验证用）
`web/src/styles/global.css:2587` —— 提升特异性至 (0,2,0)：
```css
/* 修复前 */
.geo-map { position: absolute; inset: 0; }

/* 修复后 */
.geo-canvas .geo-map { position: absolute; inset: 0; }
```

### 验证结果
```
mapRect:  756x0    →  756x644
canvas:   756x300  →  756x644
加载状态: loaded=true, 控制台零错误
2D 地图:  底图 + 10 台设备按真实经纬度落点正常显示
3D 地图:  pitch=58° 斜视正常，节点与连线显示
```

## 影响
- 2D 地图、3D 地图**功能级失效**（非降级）
- 拓扑图视图不受影响
- **所有平台均受影响**，故 CI 无法发现（CI 不覆盖该交互路径）

## 说明
修复已应用于工作区用于验证；是否合并由维护者决定。
`.geo-canvas` 前导选择器是稳定做法（结构上 `.geo-map` 必为 `.geo-canvas` 子元素，
见 GeoCanvas.tsx:934-935）。

## 验收标准
- [ ] 2D 地图画布撑满容器（不再回退 300px）
- [ ] 底图、设备落点、链路可见
- [ ] 3D 地图斜视正常
- [ ] 拓扑图视图无回归
- [ ] 前端测试全绿

## 相关
- issue-02：`.mjs` MIME 缺陷（**仅 Windows**，独立叠加问题）
- issue-09：3D 节点光柱渲染质量问题（本 issue 修复后才可见）
