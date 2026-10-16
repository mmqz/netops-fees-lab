# [P1] v0.11 图层覆盖层实测缺陷：L3 图层永久禁用 + 默认视图被压成竖条 + 聚簇成员未渲染

来源：本地对 `73e7fa9`（v0.11.0，#51 修复）的端到端实测
验证时间：2026-10-16
验证环境：Windows / 构建 `bin/netopslab.exe`（54,033,408 bytes）/ `-addr 127.0.0.1:8088` / `data/netops-real.db`
验证方式：新建工作台 `40faf4d3-08b5-4e39-ab6e-785ab9d4d88b` + headless Chrome + CDP 直读 Cytoscape 实例内部状态

---

## 〇、先说结论：#51 的主体修复是成功的

新建工作台后物化计数对比（**同一份数据、同一个二进制，只是工作台创建时间不同**）：

| 指标 | 旧工作台（v0.8.1 建） | 新工作台（v0.11 建） | 变化 |
| --- | --- | --- | --- |
| `interfaces` | 46 | **400** | 8.7× |
| `layerNodes` | 0 | **1,286** | 从无到有 |
| `layerLinks` | 0 | **1,073** | 从无到有 |

接口名也从合成 `eth<n>` 变成真实端口 `xgei-1/1/1` / `xgei-1/1/2`，说明
`port → mounted_on → board → installed_in → device` 的解析链生效。
**issue #51 提出的「32 类资源中 31 类闲置」问题已经解决。**

图层覆盖层 summary 实测（`GET /api/workspaces/{id}/topology` → `layers.summary.byLayer`）：

| 图层 | nodes | edges | clusters | clusteredNodes |
| --- | --- | --- | --- | --- |
| L0 空间 | 21 | 0 | 0 | 0 |
| **L3 传输** | **0** | **221** | 0 | 0 |
| L4 接入 | 36 | 15 | 11 | **1,198** |
| L5 逻辑 | 16 | 674 | 0 | 0 |
| L6 业务 | 26 | 163 | 0 | 0 |

`golang: go test ./...` 全绿：**24 包 ok / 0 FAIL / exit 0**。所以下面三个缺陷
**不是回归**，而是 #51 引入覆盖层之后**新暴露**的、独立的问题。

---

## 一、缺陷 A（P1）：L3 图层开关永久禁用，207 条电路无法在拓扑上查看

### 现象

画布工具栏的 L0/L1/L2/L3/L4/L5/L6 七个开关里，**L3 点了没有任何反应**。

CDP 实测（依次点击各层开关，记录 `is-active` 类）：

```
BASELINE   active: ["L1","L2","L6"]
click L3 → ok        ← 点击事件送达
AFTER_L3   active: ["L1","L2","L6"]      ← 状态没变
click L4 → ok
AFTER_L4   active: ["L1","L2","L4","L6"] ← 正常
click L5 → ok
AFTER_L5   active: ["L1","L2","L4","L5","L6"]
click L0 → ok
AFTER_L0   active: ["L0","L1","L2","L4","L5","L6"]
```

同时注意：代码里 `DEFAULT_PREFS.layers = { L0:false, L1:true, L2:true, L3:true, L4:false, L5:false, L6:true }`
声明 **L3 默认为 true**，但清空 `sessionStorage` + `localStorage` 后重载，实际激活的只有
**L1/L2/L6 —— L3 连默认态都没生效**。

### 根因（已定位到行）

`web/src/components/lab/TopologyCanvas.tsx:2711`

```ts
const hasData =
  key === 'L1' || key === 'L2'
    ? (topology?.nodes.length ?? 0) > 0
    : (summary?.nodes ?? 0) > 0;      // ← L3 的 nodes 是 0
const active = prefs.layers[key] && hasData;
```

第 2724 行 `disabled={view !== 'topo' || !hasData}`，所以 L3 按钮是 **disabled**。

而 `summary.byLayer.L3 = { nodes: 0, edges: 221 }` —— **L3 是一条纯边图层**。

### 为什么这是个必须修的设计问题

L3 的 221 条边端点类型实测是 **`device -> device`（221/221）**：

```json
{"id":"vl-1","layer":"L3","sem":"logical","label":"CIR-001",
 "aKind":"device","aRef":"vd-134","aCode":"DEV-1603990225-01",
 "zKind":"device","zRef":"vd-137","zCode":"DEV-1933473774-01"}
```

**这些边的两端都是已经在画布上的设备节点**，完全可以正常渲染。
却因为「用节点数判断图层有无数据」而被整体禁用 —— 于是 **207 条电路（`circuit`）的
传输路径在拓扑上完全不可见**。

对 IPRAN 运维来说，L3 传输层恰恰是核心图层。用户最初的原话就是：

> **「各个设备的链接都是按照链路走的啊，逻辑链路在物理之上」**

### 建议修复

`hasData` 应同时考虑边：

```ts
const hasData = key === 'L1' || key === 'L2'
  ? (topology?.nodes.length ?? 0) > 0
  : (summary?.nodes ?? 0) > 0 || (summary?.edges ?? 0) > 0;
```

并补一条断言测试：对每个 `OverlayLayer`，`nodes > 0 || edges > 0` 的图层必须
渲染出**可用（非 disabled）**的开关。

---

## 二、缺陷 B（P1）：默认视图被压成一条竖条，左右各约 440px 全空

### 现象

清空偏好重载后，主画布 1318×624 上**只有中间约 436px 宽的竖条有内容**，
左右两侧完全空白。8×8 网格采样（CDP 读 `getImageData`）：

```
grid = [[0,0,0, 130,   0, 108,0,0],
        [0,0,0, 671, 177,   0,0,0],
        [0,0,164,2781,1187,  0,0,0],
        [0,0,  3,3007,1846,  0,0,0],
        [0,0,  5,2793,1618,  0,0,0],
        [0,0,  0,1196, 749,  0,0,0],
        [0,0,  0, 245,  18,  0,0,0],
        [0,0,  0, 114,   0,  0,0,0]]
```

**第 0/1/2 列和第 5/6/7 列全为 0** —— 内容被挤在中间一根竖条里。

### 根因

对比 Cytoscape 实例内部状态（CDP 经容器 `_cyreg.cy` 读取）：

```
nodeCount = 232, edgeCount = 1140
节点坐标范围   = 2049 × 1279   ← 真实铺开范围
elements().boundingBox() = 813 × 1113   ← fit 实际用的范围，宽只有 813
viewport = 1318 × 624,  zoom = 0.507
```

**坐标范围宽 2049，包围盒宽却只有 813** —— 大量元素处于 `display:none`（图层关闭），
`boundingBox()` 把它们排除，剩下可见元素的包围盒塌成 **813×1113 的细长条**
（宽高比 0.73）。`cy.fit()` 拿这个条去适配 1318×624（宽高比 2.1）的视口，
结果就是内容被压到中间。

验证：**打开 L0 后 `bbw` 从 813 跳到 1888**（接近真实的 2049），说明
包围盒确实是被隐藏图层"截断"的。

`TopologyCanvas.tsx:717` `cy.fit(undefined, FIT_PADDING)` 本身没问题，
问题在于**它 fit 的是"当前可见元素"，而默认可见集合的包围盒极端退化**。

### 建议修复（择一）

1. **按"全部图层都可见"计算 fit 包围盒**，再按当前图层集合裁剪显示 ——
   保证不同图层组合下视图尺度稳定，不会因开关图层而剧烈跳动；
2. 给 fit 加**最小宽高比护栏**：当 `bb.w / bb.h` 超出视口宽高比 2 倍以上时，
   对短边做补偿（相当于 letterbox），避免压成一条线；
3. 图层节点的**布局坐标**应参与整体布局（当前它们似乎只围绕锚设备堆积），
   使包围盒天然接近视口比例。

---

## 三、缺陷 C（P2）：1,198 个聚簇成员没有渲染成任何节点

`summary.byLayer.L4.clusteredNodes = 1198`，但 CDP 读到的实际节点分布：

```
byLayer = { "(none)": 133, "L4": 36, "L0": 21, "L5": 16, "L6": 26 }
总计 = 232
```

**1,198 个基站只体现在 summary 的计数字段里，没有任何一个进入 Cytoscape**，
L4 只有 36 个节点（对应 11 个簇 + 25 个其它）。

这本身可能是**刻意的聚簇设计**（1,198 个基站全画出来确实会卡死画布），
代码里也有 `LAYOUT_GUARD_MAX = 800` 的护栏和 `cluster` 语义。
**但接口 `GET /api/workspaces/{id}/layer-members?cluster=锚设备id` 已经实现了下钻**，
需要确认：

- 画布上是否**真的能点到簇节点并下钻**（本次未验证到该交互）；
- 簇节点上是否显示 `1198` 这样的**成员数徽标**，否则用户看到 11 个孤立小点，
  完全不知道背后有 1,198 个基站。

若下钻已可用且徽标已有，本条可降级为文档说明；否则请补齐。

---

## 四、附带发现（P2）：头部统计条的"链路 23"与实际边数不符

画布头部显示 **`链路 23`**，但 Cytoscape 实例里 `edgeCount = 1140`
（23 条物理链路 + 221 条 L3 + 674 条 L5 + 163 条 L6 + 15 条 L4 ≈ 1096，余量待核对）。

`链路 23` 读的是 `virtual_links`（旧物化表），**没有把覆盖层边计入**。
用户看到的仍然是"链路只有 23 条"这个他最初质疑过的数字。

建议：头部统计拆成 `链路 23（物理） / 图层边 1,073`，或统一口径。
同类问题也出现在**工作台列表卡片**上（卡片显示"链路 23"，与旧工作台完全一样，
用户无法从列表区分修复前后）。

---

## 五、复现步骤

```powershell
# 1. 构建（前端必须先于二进制，go:embed 在编译期嵌入 dist）
cd web; npm run build
cd ..; go build -o bin/netopslab.exe ./cmd/netopslab

# 2. 启动
./bin/netopslab.exe serve -addr 127.0.0.1:8088 -db ./data/netops-real.db

# 3. 新建工作台（旧工作台不会自动回填覆盖层）
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8088/api/workspaces `
  -ContentType 'application/json' `
  -Body '{"name":"v0.11 验证","description":"验证 #51"}'

# 4. 观察 layers.summary
$ws='<上一步返回的 workspaceId>'
(Invoke-RestMethod "http://127.0.0.1:8088/api/workspaces/$ws/topology").layers.summary

# 5. 打开 http://127.0.0.1:8088/lab/$ws
#    → 点 L3 开关无反应（缺陷 A）
#    → 画布内容被挤在中间竖条（缺陷 B）
```

---

## 六、验收标准

1. L3 开关**可点击**，点击后 221 条传输边可见，`aria-pressed="true"`；
2. 默认视图下画布内容**横向利用率 ≥ 70%**（当前中间竖条约占 33%）；
3. 任意图层组合下 `cy.fit()` 后视图尺度稳定，切换图层不产生剧烈缩放跳动；
4. 簇节点显示成员数徽标，且下钻到成员列表可用（或明确文档化为不支持）；
5. 头部统计与实际渲染边数口径一致，或明确标注"物理链路 / 图层边"；
6. 上述 1-3 补断言测试（可在 `internal/sim` 层断言 `L3.nodes==0 && L3.edges>0`
   时仍产出可用图层，避免再次依赖"节点数"判断）。

---

## 七、修复记录（v0.11.1，2026-10-16）

> 修复轮环境：linux/amd64 沙箱，Go 1.27 工具链 / Node 24.21.0。验证方法与
> 本 issue 相同——新建工作台 + headless 浏览器 + 直读 Cytoscape 实例内部状态
> （agent-browser，1600×900 视口，`sessionStorage`/`localStorage` 清空后取默认态）。

### 验收逐条核对

| # | 验收标准 | 实测 | 结论 |
| --- | --- | --- | --- |
| 1 | L3 开关可点击、221 条边可见、`aria-pressed="true"` | 默认态 `activeLayers=[L0,L1,L2,L3,L6]`，L3 `aria-pressed="true"`/`disabled=false`；`cy.edges('edge[layer="L3"]').filter(':visible').length = 221/221` | ✅ |
| 2 | 默认视图横向利用率 ≥ 70% | 清偏好默认态：zoom 0.574，可见内容渲染坐标横向跨度/画布宽 = **72.1%**（修复前 ~33%）；纵向 95.6%（内容完整可见，上下留白 ~15px） | ✅ |
| 3 | 任意图层组合下 fit 尺度稳定、切层无跳动 | 关 L3 → zoom 0.542 不变；按 F（fit）→ zoom 仍 0.542（同一全图包围盒）；开 L0 → zoom 不变 | ✅ |
| 4 | 簇节点成员数徽标 + 下钻可用 | 11 个胶囊显示「接入 ×440/×137/×103/×48/×46…」（`min-zoomed-font-size:0` 不随缩放隐藏）；点击 → LayerInfoTab 成员数 440 + 分页成员表 20 行/页翻页正常 | ✅ |
| 5 | 头部统计口径一致或标注 | 头部 chips：「物理链路 23」「图层节点 1286」「图层边 1073」（悬停 title 注明口径与只读投影属性）；列表新增「图层（节点/边）」列；新建 toast 含图层边计数 | ✅ |
| 6 | 断言测试 | `TestLayerOverlayPureEdgeContract`（internal/sim）：L3 纯边形态钉死 + summary 各层 nodes>0或edges>0 + 全部边端点可解析到画布实体（device/interface/node，无自环）+ 锚设备存在 | ✅ |

### 修复方式与根因对照

**缺陷 A（L3 永久禁用）**：`TopologyCanvas.tsx` 图层开关 hasData 判据改为
`nodes > 0 || edges > 0`。L3 为纯边图层（两端都是画布设备），数据契约由
`TestLayerOverlayPureEdgeContract` 钉死——边可渲染性有测试保障，不再依赖节点数。

**缺陷 B（竖条 + 跳动）**：两层修复——
1. `fitToView` 基准改为**全图包围盒**（`fullGraphBoundingBox` 手工聚合全部节点，
   含隐藏图层节点；Cytoscape 的 `elements().boundingBox()` 在样式启用环境会排除
   display:none 元素——正是 813×1113 塌缩的机制）。全图包围盒与图层开关无关
   → 任意图层组合下 fit 尺度恒定（验收 3 的直接来源）。
2. 默认图层组合 L0 改为默认开：issue 自己的交叉验证（「打开 L0 后 bbw 从 813
   跳到 1888」）指出 L0 空间锚点承载真实地理宽度——默认可见子集（设备+业务，
   宽高比 ~0.8）本身是高瘦簇，任何「看得全」的 fit 都必然竖条；L0 锚点（14px
   无标签暗点）把可见内容横向铺开到 ~74%。实测复验一致。
3. 附带 letterbox 护栏：内容高瘦时纵向留白收窄（40→下限 12px）换横向铺开，
   护栏目标 0.72——这是「L0 关闭、仅剩高瘦可见子集」等一切组合下的兜底。

**缺陷 C（聚簇徽标）**：成员数徽标 = 胶囊标签「接入 ×N」，缺陷实为
`min-zoomed-font-size: 5`（沿用 overlay 基类）在默认 fit 缩放下把标签整体隐藏。
cluster 专项置 0 + 字号 11 + 盒宽 88。下钻（LayerInfoTab 成员表）本已实现，
实测可用——按 issue 的降级条款本条本可文档化收尾，徽标修复后完整达标。

**附带（口径）**：见验收 5。

### 诚实披露

- CI 未运行：run 38857685886（b243714）三 job 均失败于「recent account
  payments have failed」——GitHub Actions 账户计费问题，非代码原因。本地
  六门禁（gofmt/vet/go test 24 包/tsc/vite build/浏览器回归）全绿；billing
  恢复后 CI 预期绿。
- 顺手修复：`internal/domain/sim.go`、`internal/app/e2e_realdata_test.go` 的
  gofmt 缩进漂移（#51 提交 73e7fa9 引入；当时 CI 未运行未拦截）。纯空白变更。
- 已存偏好（prefs v3）的用户默认组合不变（仍按其存档）；仅新会话/清偏好后
  取新默认（L0 开）。
