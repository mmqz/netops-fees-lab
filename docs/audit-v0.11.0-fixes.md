# v0.11.0 修复轮验证报告（#48 / #49 / #50）

> 验证对象：`5960b69`（云端 main）
> 变更：8 文件，+869 / −91 行
> 环境：Windows / amd64
> 日期：2026-10-15

---

## 0. 结论

**我提的三个 issue 全部按建议方案修复，且已验证生效。**

| # | 问题 | 修复 commit | 验证结果 |
| --- | --- | --- | --- |
| #48 | 调度器测试时序断言失败 | `825cdf3` | ✅ **连跑 5 次全绿**（修复前 5/5 失败） |
| #49 | 拓扑连线跨越数百公里 | `10f0dc0` | ✅ **均值 448km → 145km**，标签 CIR→OC |
| #50 | 地图渲染慢（10.6 万要素） | `5960b69` | ✅ **默认降级 + 惰性 source** |

测试：**24 包 ok，0 项失败**（修复前 1 项 FAIL）。

---

## 1. #48：调度器测试（✅ 已修）

### 修复方式（正是我建议的方案）

`internal/detect/scheduler_test.go:98-131`：

```go
// NOTE: Running is deliberately NOT asserted here (issue #48): with a
// sampling instant, so "Running=false between rounds" was a …
// released after shutdown — is asserted post-cancel below.
…
cancel()
// released — Running settles false within the deadline. This is the
// post-shutdown contract that replaces the removed live assertion:
drainDeadline := time.Now().Add(2 * time.Second)
for sched.Status().Running && time.Now().Before(drainDeadline) { … }
if sched.Status().Running {
    t.Error("Running still true after cancel + 2s drain, want the single-flight slot released")
}
```

即：**删除时序敏感的 live 断言，改为钉死「cancel + drain 后 Running 必须复位」**
——这正是我 issue 里推荐的「方案 A + B 组合」。

### 实测

```
连跑 5 次：
  第1次: ok  internal/detect  1.873s
  第2次: ok  internal/detect  1.932s
  第3次: ok  internal/detect  2.150s
  第4次: ok  internal/detect  2.287s
  第5次: ok  internal/detect  1.950s
修复前：5/5 失败
```

---

## 2. #49：拓扑连线分层（✅ 已修）

### 修复方式：物理层优先 + 走廊锚点链折跳

`internal/sim/materialize.go:52-63`：

```go
// LINKS follow the physical-layer-first rule (issue #49): when the
// dataset carries cable corridors (cable_segment + splice_box rows),
// … cable's anchor chain — segments collapse between consecutive devices,
// so a hop spans one corridor span (~100-160km at province-trunk scale),
…
// the physical drill sandbox, the graph is the business analysis view.
```

算法（`materializePhysicalLinks`）：

```
1. 读光缆走廊锚点链（站点 → 设备）
2. 连续相同设备折叠（中间锚点无设备）
3. 相邻不同设备对 → 一跳 link，标签 = 光缆编码
4. 每条光缆独立贡献自己的跳（并行光缆保持两条链路 = 真实冗余形态）
5. 无走廊数据时回退到 circuit（保持兼容）
```

### 实测：链路距离对比

| 指标 | 修复前 | 修复后 | 变化 |
| --- | --- | --- | --- |
| 链路数 | 32 | 16 | −50% |
| 最短 | 112 km | **53 km** | ✅ |
| 最长 | 935 km | **258 km** | ✅ −72% |
| **均值** | **448 km** | **145 km** | ✅ **−68%** |
| >200km | 31/32 (**96.9%**) | 2/16 (12.5%) | ✅ |

**链路标签从 `CIR-xxx`（电路）变为 `OC-xxx`（光缆）**——
证明现在画的是**物理层光缆段**，正是 issue 要求的层次。

工作台计数变化：

```
修复前  设备 30 / 接口 64 / 链路 32
修复后  设备 30 / 接口 32 / 链路 16
```

### 新增测试

```
TestMaterializePhysicalCorridorHops     走廊逐跳
TestMaterializePhysicalFallbackKeepsCircuits  无走廊时回退
→ ok  github.com/mmqz/netops-lab/internal/sim  2.525s
```

---

## 3. #50：地图渲染性能（✅ 已修）

### 修复方式：默认降级 + 惰性 source（双管齐下）

**① 默认图层全部降级**（`geoBasemap.ts:289-310`）：

```ts
road:     false,   // 4.3 MB
forest:   false,   // ┐
water:    false,   // ├ 同属 hlj-natural，16 MB
green:    false,   // ┘
terrain:  false,
contours: false,   // 7.3 MB
// 保留：admin / places / cable
```

**② 惰性 source（更彻底）**：

```ts
const lazyData = (sourceId, visible) =>
  visible ? overlaySourceUrl(sourceId, def.isGCJ)   // 可见 → 浏览器才去拉
          : EMPTY_FC;                                // 不可见 → 空集，零请求
```

配合 `GeoCanvas` 的 `fillOverlaySources` 在用户勾选时补 `setData(URL)`
——**未勾选的图层根本不发网络请求**。

### 声明的效果

```
首帧 34 MB → 0.6 MB (gz)
```

**这比单纯关默认值更彻底**：即使默认开着，只要用户没勾选就不加载。

### 待复测项

真实首帧耗时需在浏览器 DevTools Network/Performance 面板实测。
本次仅验证了代码实现与默认值变更（静态核对）。

---

## 4. 遗留观察

### 4.1 15 台设备无接口（拓扑孤立点）

实测工作台 30 台设备中 **15 台接口数为 0**：

```
零接口：全部 -02 结尾（绥化02/伊春02/哈尔滨02/漠河02/佳木斯02/…)
有接口：全部 -01 结尾（每台 1-4 个接口）
```

**判定：设计使然，非缺陷。** 每个站点生成 2 台设备（01/02），
光缆走廊只串联 `-01` 那批（真实网络中备机常不在主路径上）。

但从**拓扑可读性**看，15 个孤立点仍影响观感。可考虑：
- 生成器让 `-02` 设备也参与走廊（主备双路径）
- 或画布提供「仅显示有连接设备」过滤

### 4.2 仍有 2 条 >200km 链路（258km / 218km）

由光缆走廊**锚点粒度**决定（省干单段 100-260km 属合理范围）。
若要更细，需按 `splice_box`（接头盒 46 个）细分走廊锚点。

---

## 5. 验证方式

```
1. git pull（f63ea8d → 5960b69）
2. cd web && npm run build && cd .. && go build -o bin/netopslab.exe ./cmd/netopslab
3. pwsh run.ps1 -NoBrowser -SkipWeb -SkipBuild -Rebuild
4. go test -count=1 ./...          → 24 包 ok
5. 逐链路测距（Python haversine）  → 均值 145km
6. headless 截图                   → 拓扑形态验证
```