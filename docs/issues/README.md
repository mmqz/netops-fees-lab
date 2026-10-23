# 测试发现的问题清单

> 来源：2026-10-12 全面测试（Windows + Linux/Docker，v0.6.0 @ 2033de8）；#12–#14 为 2026-10-12 用户现场反馈（v0.6.2 验收后）；#15 为 2026-10-12 用户现场反馈（v0.6.3 验收后）；#16 为 2026-10-12 数据模型评审（v0.6.4 之后，架构级）；#17–#29 为数据模型设计衍生的决策项与缺失项（见 [design/data-model-v1.md](../design/data-model-v1.md) §11）；**#38–#41 为 v0.7.0/v0.8.0 本地审计发现（见 [audit-v0.8.0.md](../audit-v0.8.0.md)）；**#42–#45 为 v0.8.0 真实数据演示的用户现场反馈与复核；**#46 为 v0.8.1 验证；#48 见 [audit-v0.11.0.md](../audit-v0.11.0.md)；#49–#52 为拓扑/地图/数据的用户现场反馈；#53 为 v0.11.0 图层覆盖层端到端实测（#51 修复后新暴露的缺陷）**
> 完整报告：[test-report-windows-2026-10-12.md](../test-report-windows-2026-10-12.md)

## 按优先级

| # | 优先级 | 标题 | 平台 | 影响 |
| --- | --- | --- | --- | --- |
| [01](issue-01-geo-canvas-collapse.md) | **P0** | 2D/3D 地图不显示：画布高度塌陷为 0 | **所有平台** | 地图功能级失效 |
| [02](issue-02-mjs-mime-windows.md) | **P0** | `.mjs` 返回 `text/plain`，Worker 加载失败 | 仅 Windows | 叠加致地图失效 |
| [03](issue-03-e2e-exe-suffix.md) | P1 | E2E 临时二进制缺 `.exe` 后缀 | Windows | 11 个测试失败 |
| [04](issue-04-suggest-type-sheetname.md) | P1 | 类型识别仅依赖 sheet 名 | 所有平台 | 标准导出整表静默跳过 |
| [09](issue-09-3d-tower-rendering.md) | P1 | 3D 节点巨型光柱（粗细随缩放、高度无限）+ 底图无地形 | 所有平台 | 3D 视图不可读 |
| [11](issue-11-basemap-modes.md) | P1 | 底图仅单一极简英文栅格，无行政/交通/中文，无模式切换 | 所有平台 | 底图信息量不足 |
| [05](issue-05-windows-tempdir-specialchars.md) | P2 | TempDir 清理失败与非法文件名 | Windows | 4 个测试失败 |
| [06](issue-06-report-json-no-output.md) | P2 | `report --format json` 无输出 | 所有平台 | CLI 契约不一致 |
| [07](issue-07-api-negative-pagination.md) | P3 | 分页参数负数未校验 | 所有平台 | API 契约不一致 |
| [08](issue-08-cross-db-silent-mismatch.md) | P3 | CLI 与 serve 用不同 DB 无提示 | 所有平台 | 排查成本高 |
| [10](issue-10-missing-ops-capabilities.md) | 设计 | 异常检测/告警/预测均不存在 | — | 能力差距记录 |
| [12](issue-12-camera-lock-and-fit.md) | **P0** | 2D/3D 相机锁联 + 初始比例不合适（用户反馈） | 所有平台 | 双视角工作流失效 |
| [13](issue-13-elevation-visibility.md) | **P0** | 地理高程支撑不可见（用户反馈） | 所有平台 | 高程数据形同虚设 |
| [14](issue-14-release-pipeline-gap.md) | **P0** | v0.4.0–v0.6.2 发布断档，用户仍在 v0.3.0 | 流程 | 修复从未送达 |
| [15](issue-15-missing-landcover.md) | **P0** | 2D/3D 缺地表覆盖：全是陆地，没有水/森林（用户反馈） | 所有平台 | 地图与真实地貌不符 |
| [16](issue-16-data-model-layers.md) | **P1（架构）** | 数据模型缺层：仅 8 类资源 / 4 种关系，无法表达业务层与保护结构 | 所有平台 | 影响分析只能答连通性 |

---

## 数据模型设计衍生项（#17–#29）

来源：`docs/design/data-model-v1.md` §11。
**这些不是「等人类裁决」的阻塞项，而是给实施方的「测试后择优 / 补设计」工作项**。

统一处置指令：能测的就测出数据择优；不能测的用默认方案 + `TODO(design-v2):` 标记，
并在 issue 记录原因供后续替换。**不允许悬空导致实施卡住。**

> **[W19 处置状态（2026-10-13）]**：§11 十三项 **13/13 已全部裁决**——裁决记录与
> 实测依据见 [design/data-model-v1.md §12](../design/data-model-v1.md)（处置日志），
> A1/A2 架构级裁决另存 [ADR-0014](../adr/0014-relation-semantics-and-metrics-idempotency.md)。
> 关系方向注册表 + 层次/域元数据已随 W19 落地（`internal/mapping/relations.go`）。
> 各项**实施**随 W20-W25 轮次推进，落地后在对应 GitHub issue 附验收证据关闭；
> #17（裁决+对比数据+注册表落地）与 #24（边界裁决+声明+标记，lab 零改动即正确实现）
> 已可关闭，其余待实施轮次。

### A 类：设计决策（有候选方案，需择优）

| # | 标题 | 涉及小节 | 默认方案 |
| --- | --- | --- | --- |
| [17](issue-17-belongs-to-vs-housed-in.md) | `belongs_to` 与 `housed_in` 是否合并 | §4.2 | 保留两个（不破坏 v10 语义） |
| [18](issue-18-metrics-upsert-vs-resource-skip.md) | 时序 upsert 与资源 skip 的语义差异 | §5.4 D3 | 两种并存 + 文档声明边界 |
| [19](issue-19-port-metrics-ownership.md) | 端口性能数据挂哪一层 | §5.4 D5 | 挂 port 资源（保证图可穿透） |
| [20](issue-20-new-types-ui-scope.md) | 新增 30+ 类型的 UI 支持范围 | §3.2 | 只做数据层，复用通用表格 |
| [21](issue-21-placeholder-data-layout.md) | 占位数据目录约定 | §8 | `testdata/placeholder/` |

### B 类：设计缺失（必须补齐，否则实施靠猜）

| # | 标题 | 阻塞原因 | 默认方案 |
| --- | --- | --- | --- |
| [22](issue-22-status-model.md) | **状态模型**：台账状态与运行状态未区分 | §10.5 判据「status 为故障」无法执行（「故障」不在枚举中） | 历史表 + `resources` 快照双写 |
| [23](issue-23-alert-detection-model.md) | **告警与检测规则**未纳入设计 | §10.5「与告警交叉」无表可依 | 按 ADR-0011 D2/D3 落地 |
| [24](issue-24-lab-vs-expanded-model.md) | **lab 与扩容模型的关系**未定义 | 既有 lab 是独立表族，只物化 device/circuit | lab 保持现状 + 明确边界 |
| [25](issue-25-migration-mapping.md) | **既有 8 类数据的迁移映射**未定义 | `归属A` 派生关系与「禁止按名称推断」红线冲突 | 保守迁移，不自动派生新关系 |
| [26](issue-26-resource-type-registry.md) | **资源类型注册表**（单一事实源）未定义 | 45 类元数据散落 7 处必然不一致 | 扩展 `mapping.Specs` 为 registry |
| [27](issue-27-metrics-retention.md) | **时序量级与保留策略**未设计 | 估算约 7.3 亿行，SQLite 承载能力未验证 | 先只存原始点，实测后再定聚合 |
| [28](issue-28-mapping-config-management.md) | **导入映射配置管理**未设计 | 「每天导入即可」需无人值守，现流程每次要人工确认 | 映射模板持久化 + 表头指纹匹配 |
| [29](issue-29-crs-and-coding-conventions.md) | **坐标系与编码规范**未定义 | CRS 不统一致落点偏 ~500m；编码不规范致身份分裂 | CRS 统一 WGS84；编码 trim+upper，无编码则名称兜底+警告 |

### 建议处理顺序

```
第一步（阻塞其他项）    #22 状态模型 / #23 告警检测 / #28 映射无人值守
第二步（架构一致性）    #26 注册表 / #29 CRS与编码 / #24 lab边界 / #25 迁移映射
第三步（性能与容量）    #27 时序量级（合成数据实测后定）
第四步（可并行）        #17–#21 五项决策（各有默认方案，不阻塞）
```
## v0.8.0 本地审计发现（#38–#41）

来源：[docs/audit-v0.8.0.md](../audit-v0.8.0.md)。
审计方式：拉取云端 v0.7.0/v0.8.0 → **独立复现**（自写脚本，不执行项目 demo）→ 库直查 → HTTP 实测 → 隔离复测。

**结论：核心声明全部为真**（33 类型 / 29 关系 / 1,965 行零失败导入 / 7 日劣化 /
检测告警联动 / 跨层穿透 87 节点 / 根因候选正确 / §10.8 七条硬约束落实）。
以下为审计发现的问题。

| # | 优先级 | 标题 | 性质 | 证据 |
| --- | --- | --- | --- | --- |
| [38](issue-38-windows-test-gate.md) | **P1** | Windows 测试门禁不可用（6 包 15 项失败） | TempDir 修复未覆盖新包 + POSIX 断言 | 实跑 go test |
| [39](issue-39-concurrency-retry-budget.md) | P2 | 并发重试预算不足，负载下 SQLITE_BUSY 逃逸 | 负载相关（单测通过 / 全套件失败） | 隔离复测 |
| [40](issue-40-serves-edge-and-coverage-gaps.md) | **P2** | 演示数据质量：serves 边缺失、13 行非法枚举、2 类型 / 5 关系无数据 | 数据质量与覆盖缺口 | 库直查 + 概览页 |
| [41](issue-41-graph-query-cli-channel.md) | P3 | 图查询无 CLI 通道 | 符合设计但离线不可用 | CLI 实测 |

### 运维提示（重要）

```
拉取云端更新后必须「先建前端、再建二进制」（顺序不可颠倒）：
  cd web && npm ci && npm run build                          # ① 更新 dist
  cd .. && go build -o bin/netopslab.exe ./cmd/netopslab     # ② 重新嵌入
原因：internal/webui/dist 不入库（.gitignore），且被 go:embed 在构建时嵌进二进制。
     只重建前端而不重建二进制无效 → 地图新增图层静默缺失
     （GET /mapdata/*.geojson.gz 返回 200 + text/html，控制台无报错）
注：go test 会重新编译故测试通过，而运行时失败——勿以此误判。
```

注：审计中 `TestHandlerRealDistRealNetworkLayers` 曾失败，重建前端后通过——
**属审计环境问题，非产品缺陷**，未开 issue。

---

---

---

## v0.11.0 本地验证（2026-10-15）

来源：[docs/audit-v0.11.0.md](../audit-v0.11.0.md)。云端已迭代到 v0.11.0（含 v0.9.0 / v0.10.0 / v0.11 R11-R14）。

**验证结论：此前提的 4 项（#46 + #47 三诉求）全部按建议方案修复；新发现 1 项确定性缺陷。**

| 我提的 issue | 修复 | 是否按建议 |
| --- | --- | --- |
| #46 测试把 `elapsedMs` 纳入深比较 | ✅ | ✅ 方案 B（`stripClockFields` 精确剔除） |
| #47 按钮顺序 / 工具带换行 / 页头卡片 / 多画布 | ✅ | ✅ 四条全部落地 |

| # | 优先级 | 标题 | 说明 |
| --- | --- | --- | --- |
| [48](issue-48-scheduler-test-race.md) | **P1** | `TestSchedulerFiresAndRecords` 确定性失败 | 亚秒间隔下断言「两轮之间 Running=false」；连跑 5 次 5 败 |

### v0.10.0 新增能力（与用户目标相关）

```
v0.9.0  5 个只读 MCP 分析工具（traverse/impact/context/alerts/metrics）
        ← 落到设计文档 §7.3 的「AI 分析接口」，AI 可经 MCP 直接做根因分析
        4 个运营 UI：/alerts 告警中心、/rules 检测规则、指标时序图、影响分析页签
v0.10.0 R6 规则管理页 / R7 立即检测+CSV 导出 / R8 Webhook / R9 OpenAPI 3 + /dev
```

### git 443 不通时的同步路径

本次 `git fetch/push` 一度全部超时，但 `gh`（GitHub API）通路正常。替代：
`gh auth token` → Python 二进制下载 tarball → robocopy 镜像。

**注意**：PowerShell 的 `>` 重定向会破坏二进制写入
（实测得到 `1F EF BF BD` 而非 gzip magic `1F 8B`）——必须用 Python/curl。

---

## 拓扑连线分层（#49）

来源：用户 2026-10-15 现场反馈。

**问题**：拓扑画布的连线是「业务电路的 A 端 ↔ Z 端」，跨越数百公里。

| 实测项 | 值 |
| --- | --- |
| 连线数 | 32 |
| 最短 / 最长 / 均值 | 112 km / 935 km / 448 km |
| > 200km 占比 | **31 / 32（96.9%）** |

**根因**：`internal/sim/materialize.go:182-233` 只有一条规则——
`circuit.a_end/z_end → link`。即「一条电路一条连线」，
直接连 A 端设备与 Z 端设备，**跳过了中间全部层次**。

**层次错误**：

```
真实分层                      当前画布
L6 业务  客户专线（哈→齐）
L3 电路  CIR-001（哈↔齐）    ← 画布画的是【这层】，所以跨 270km
L3 波道  OCH-HDQ-01（逐段）
L2 光缆  哈-大段 / 大-齐段
L1 设备  相邻站点互联        ← 拓扑图【本应画这层】
```

**中间层数据已存在但未使用**：

```
otn_och 14 / cable_segment 23 / optical_cable 7 / fiber 56
carried_by 32 / routed_over 14 / segment_of 23 / cable_a_end 7 / cable_z_end 7
```

即 v0.7 数据模型扩容已建好 L2/L3，但 materialize 停留在 v0.5 的
「device + circuit」两类型时代（与 #24 的裁决同源，但当时低估了后果——
不是「少几个类型」，而是「拓扑图的物理意义错了」）。

**修复方向**（推荐 B）：
- A 物化物理层连线（按光缆段 A/Z 端站点）
- **B 物化波道逐段路径**（`routed_over` → 相邻站点跳，光缆锚点链天然支持分段）
- C 补充「物理层 / 业务层」视图切换（逻辑飞线只在业务层出现）

| # | 优先级 | 标题 |
| --- | --- | --- |
| [49](issue-49-topology-link-layering.md) | **P1** | 拓扑连线跨越数百公里：画的是业务电路端到端，不是网络逐跳 |

---

## 地图渲染性能（#50）

来源：用户 2026-10-15 现场反馈「随着内容变多地图画布的渲染很慢」。

**实测：后端很快（< 20ms），瓶颈全在前端图层。**

| 图层 | 要素数 | 解压后 |
| --- | --- | --- |
| `hlj-natural` | **29,449** | **16.0 MB** |
| `hlj-roads-main` | 22,158 | 4.3 MB |
| `hlj-contours` | 18,604 | 7.3 MB |
| `hlj-places` | 17,615 | 2.4 MB |
| `hlj-railways` | 17,257 | 2.8 MB |
| 其余 3 个 | 1,359 | 2.3 MB |
| **合计** | **106,442** | **34.2 MB** |

`hlj-natural` 单独 **65 万个坐标点**（22,399 个 Polygon，平均 29 顶点/面，未简化）。

**默认几乎全开**（`geoBasemap.ts:238-257`）：admin/road/forest/water/green/
places/cable/terrain/contours → 首次进地图即加载渲染 34 MB。

**修复方向**：① 缩放级分级预简化（收益最大）② 重图层默认关 + 一键恢复
③ 懒加载/Worker 解析/进度提示 ④ 长期换矢量瓦片（PMTiles）。

| # | 优先级 | 标题 |
| --- | --- | --- |
| [50](issue-50-map-render-performance.md) | **P1** | 地图画布渲染缓慢：默认 10.6 万要素 / 34 MB，缺简化与降级 |

---

## 工作台物化覆盖（#51）

来源：用户 2026-10-15 现场质疑「设备 30 接口 32 链路 16，模拟数据只有这些啊？」

**事实：两层数据量级差 66 倍。**

```
资源库 resources    1,991 条 / 32 类   ← 概览页与 run.ps1 显示的是这个
仿真工作台 virtual_*    30 台设备       ← 拓扑画布头部显示的是这个
```

**根因**：物化输入只有 `device` 一类（实测 `select distinct resource_type from resources
join virtual_devices` 返回 1 类）。31 类 / 1,961 条（98.5%）从不进入拓扑视图：

```
base_station 1198 / port 90 / fiber 56 / splice_box 46 / circuit 32 /
cable_segment 23 / otn_och 14 / customer 12 / service 8 / leased_line 6 …
```

**这不是「数量少」，而是「拓扑缺层」**——当前画布只有 L1（设备），
而用户目标「从表象追溯故障来源」需要业务层→传输层→物理层的可见路径。

**与 #24/#49 同源**：#49 修了「链路怎么连」，但没修「哪些资源该进工作台」。

| # | 优先级 | 标题 |
| --- | --- | --- |
| [51](issue-51-workspace-materialization-coverage.md) | **P1** | 工作台只物化 device：31 类资源闲置，1,961 条被丢弃 |

> **[W33 修复状态（2026-10-15）]**：已修复——工作台升级为「演练沙盒 + 只读图层
> 覆盖层」双层模型（ADR-0011 边界按 #51 裁定扩展为呈现层扩展）：
> - **真实端口物化为接口**：port 资源经 port→board→device 挂靠链物化为
>   399 个真实接口（不再从链路反推 2×N），链路端点优先绑定真实端口、
>   溢出才合成 eth<n>；Phase 1 fixture 无端口时行为字节级不变；
> - **层级覆盖层**（migration v23：virtual_nodes/virtual_layer_links，物化一次
>   只读、不入状态哈希、不受演练操作影响）：L0 空间锚点 21 / L4 接入
>   1,239（1,198 基站按上联设备聚簇）+ PON 子树 / L5 分组 16 / L6 业务 26 +
>   L3 逻辑承载边 221 + L4/L5/L6 语义边 852——**物化输入类型 1 → 17 类**
>   （验收 ≥8）；
> - **读路径**：Topology payload 增 layers 段（聚簇聚合 + 成员样例 + 每层
>   摘要）；新增 `GET /api/workspaces/{id}/layer-members` 簇下钻分页；
> - **画布**：七层开关（L1/L2/L3/L6 默认开，L0/L4/L5 默认关，C.1 组合）、
>   四色语义边（物理实线灰蓝/逻辑虚线琥珀/业务点线绿/上联细灰）、聚簇
>   「接入 ×N」胶囊、布局护栏（可见节点 >800 自动降级 grid，D.2）、
>   图层语义图例、切层 show/hide 不重排不重拉（F.2）；
> - **影响分析画布高亮**（F.4 故障故事闭环）：设备/链路/图层实体右键
>   「影响分析 / 向下找根因」→ 按 resourceId 匹配画布元素琥珀高亮、其余
>   压暗 + 结果面板——哈大齐光缆 down 后上探 200 节点（119 电路/5 客户/
>   3 专线/5 业务）全部在画布可见层上高亮。
>
> 验收对照：物化类型 17 ≥ 8 ✓；业务层节点可见且与设备层有路径 ✓（serves/
> depends_on/provided_by 边 + 锚设备上联边）；分层开关 ✓；1,198 基站聚簇
> （11 簇，默认聚合不逐站渲染）✓；影响分析画布高亮 + 全流程 ≤5 步（选中
> → 右键 → 高亮 → 面板 → Esc）✓。测试：`go test ./...` 24 包全绿
> （migration v23 / 物化 / 聚簇读路径 / 分页 / API 契约 / realdata e2e 重钉
> 400 接口 + 1,286 层节点断言）+ 前端构建通过。

---

## 演示数据被人为限流（#52）

来源：用户 2026-10-15 质疑「按公开数据尽量填充完全，为什么数据这么少？」

**质疑成立：不是没填充，而是生成器硬编码限流。**

```python
DEVICE_SITE_NAMES = [15 个地名]   # scripts/realdata/workbooks.py
for i, s in enumerate(dev_sites): for j in (1,2): …   # → 30 台设备
```

**而 sites 有 83 个**（全部 OSM 真实局站）→ **68 个局站从未分配设备**。

实测覆盖差距：

```
局站 site        83   ← 真实数据，齐全
设备 device      30   ← 仅覆盖 15/83 = 18%
端口 port        90
板卡 board       60
ODF端子         120
电路 circuit     32
```

30 台设备是一切的下游基数（端口 3×/设备、板卡 2×/设备、机架·ODF 1×/局站），
**所以「数据少」不是某一类问题，是基数被锁死在 15 个站点**。

真实对照：黑龙江 13 地市 + 67 县市 ≈ 80 个传输节点，每站至少 1 台设备，
当前 30 台仅为 1/3 ~ 1/5。

**与 #51/#50 必须协同**：
```
本 issue  解除生成限流  → 数据变多
#51       扩展物化覆盖  → 更多类型进工作台
#50       规模保护      → 确保变多后画布不卡
```

| # | 优先级 | 标题 |
| --- | --- | --- |
| [52](issue-52-demo-data-throttled.md) | **P1** | 演示数据被人为限流：DEVICE_SITE_NAMES 硬编码 15 站，68/83 局站无设备 |

> **[W32 修复状态（2026-10-15）]**：已修复——`workbooks.py` 布点规则重写为
> **83 局站全覆盖 + 层级差异化**（枢纽局 13 站×3 台 + 县域局 1~2 台 = **133 台**，
> 覆盖率 100%）；环/IGP 归属改由 OSM 走廊就近关系（core.json reason）派生；
> 电路 32 → **207 条**四族（相邻环段 83 + 跨域长距 60 + 地市网状 52 + 接入主备 11）；
> 端口 399 / 板卡 266 / 机架·ODF 83 / ODF 端子 664 / serves 关系 1,331。
> 总资源 1,991 → **3,464**（含 PTP 4 行 → 3,468）。
> 附带修复：OSM 同名局站（漠河市/塔河县/安达市 各 2 节点）设备名消歧——
> 旧 15 站名单恰好只取同名首个掩盖了 E011 身份冲突，全覆盖后暴露并修复。
> 验收：`TestCLIRealDataImportChain` 全绿（零失败行）；哈大齐光缆影响面上探
> 至 200 节点（119 电路 / 5 客户 / 3 专线 / 5 业务）——影响分析样本量达标。

## v0.11 图层覆盖层实测缺陷（#53）

来源：对 `73e7fa9`（v0.11.0，#51 修复）的端到端实测，2026-10-16。
方式：新建工作台 + headless Chrome + CDP 直读 Cytoscape 实例内部状态。

**先确认 #51 主体修复成功**（同一份数据、同一二进制，仅工作台创建时间不同）：

| 指标 | 旧工作台（v0.8.1 建） | 新工作台（v0.11 建） |
| --- | --- | --- |
| `interfaces` | 46 | **400** |
| `layerNodes` | 0 | **1,286** |
| `layerLinks` | 0 | **1,073** |

接口名从合成 `eth<n>` 变为真实端口 `xgei-1/1/1`。
`go test ./...` **24 包 ok / 0 FAIL**。

**但覆盖层引入后新暴露三个缺陷**（非回归，独立问题）：

```
缺陷 A  L3 图层开关永久禁用（nodes=0 被判"无数据"）→ 207 条电路不可见
缺陷 B  默认视图包围盒塌成 813×1113 细长条 → 内容挤在中间约 436px 竖条
缺陷 C  1,198 个聚簇成员未渲染为节点，仅存于 summary 计数
```

关键实测数据（CDP 读 `_cyreg.cy`）：

```
nodeCount=232  edgeCount=1140
节点坐标范围 2049×1279   vs   boundingBox 813×1113   ← 宽差 2.5 倍
```

| # | 优先级 | 标题 | 状态 |
| --- | --- | --- | --- |
| [53](issue-53-layer-overlay-defects.md) | **P1** | v0.11 图层覆盖层实测缺陷：L3 永久禁用 + 默认视图压成竖条 + 聚簇成员未渲染 | **v0.11.1 已修复**（2026-10-16；验收 1-6 全过，含 `TestLayerOverlayPureEdgeContract` 契约测试——详见 issue 文档「修复记录」节） |

## v0.12.0 复测 #53：缺陷 B 未真正修复（#54）

来源：对 `e258543`（v0.12.0，含 #53 修复轮 `17ceb47`）的独立复测，2026-10-17。
方法：与 #53 首次报告**完全相同的 CDP 方法论**。

| 项 | 云端自述 | 独立实测 | 判定 |
| --- | --- | --- | --- |
| 缺陷 A：L3 纯边开关 | 已修 | `aria-pressed:true` 默认激活、点击可切换、`disabled:[]` | ✅ **确认** |
| 缺陷 B：视图压成竖条 | 已修 72.1% | 利用率 66.1%，**右侧 2/3 仍为空** | ❌ **未修复** |
| 附带：头部口径拆分 | 已修 | 三 chips 清晰（物理链路 23 / 图层节点 1286 / 图层边 1073） | ✅ **确认** |
| 附带：列表图层列 | 已修 | API 返回 `layerNodeCount/layerLinkCount` | ✅ **确认** |

**三项修复的代码全部正确落地**——我逐行核对了 `hasData`（已含 `edges`）
与 `fullGraphBoundingBox()`（全图包围盒 + letterbox）。但缺陷 B 只是换了表现。

### 真实根因：五个图层用五套互不相干的坐标尺度

CDP 逐层测量布局坐标跨度（同一时刻、同一画布）：

| 图层 | 节点数 | 宽度 w | 宽高比 |
| --- | --- | --- | --- |
| body（133 台设备） | 133 | **447** | 0.77 |
| L4 接入 | 36 | 661 | 0.79 |
| **L0 空间** | **21** | **1902** | 1.48 |
| L5 逻辑 | 16 | 600 | 1.14 |
| L6 业务 | 26 | 839 | 0.74 |

**21 个 L0 节点（14 人井 + 7 电杆）铺开 1902 宽，而 133 台真实设备只占 447。**
全图包围盒被 L0 单独撑到 1902 → fit 后真实拓扑被压在左侧 1/4，右侧大片空白。

像素网格印证（8×8，修复后默认态）：

```
[0,   0,   99, 118,  46, 44, 47,  0]
[0,  90,  138,   0,   0,  0,  0,  0]
[0, 446,  819,   0,   0,  0,  0,  0]
[0,1394, 2566,   0,   0,  0,  0,  0]
[0,1556, 2948,  32,   0,  0,  0,  0]   ← 第 2 列 2948，第 3 列仅 32
```

### 为什么"L0 改默认开"方向反了

v0.11.1 的推理：「开 L0 后 bbw 813→1888 → 利用率 33%→72%」。
**但这只证明包围盒变宽，不证明内容铺开**——实际是 L0 撑宽包围盒 → zoom 变小 →
真实内容被缩得更小、挤得更左。而且 72.1% 这个指标本身有误导性：
它只量最左到最右跨度，一根线横贯画布也能拿满分。

| # | 优先级 | 标题 |
| --- | --- | --- |
| [54](issue-54-layout-scale-mismatch.md) | **P1** | v0.12.0 复测 #53：缺陷 B 未真正修复——各图层布局尺度不统一（L0 21 节点撑开 1902 宽，133 台设备仅 447） |

## v0.12.0 维护窗口抑制失效（#55）

来源：对 `e258543`（v0.12.0，R15 维护窗口）的功能级实测，2026-10-17。
方法：**受控对照实验**——删告警 → 建窗口 → 重跑检测，三组交叉验证。

**现象**：维护窗口创建成功、时间范围正确覆盖"现在"，
但新建告警**完全不被抑制**，且回执 `suppressed=0`，**无任何提示**。

| 实验 | 维护窗口 | 数据时间 evaluatedAt | created | suppressed | 判定 |
| --- | --- | --- | --- | --- | --- |
| A | 无 | `2026-10-12T00:00Z` | **1** | 0 | 基线 ✅ |
| B | `10-17T00:53 ~ 06:53`（**墙钟**） | `2026-10-12T00:00Z` | **1** | **0** | ❌ **未抑制** |
| C | `10-11 ~ 10-13`（**覆盖数据**） | `2026-10-12T00:00Z` | **0** | **1** | ✅ 生效 |

**根因**（`internal/detect/engine.go:322`）：匹配用 `f.LastTS`（数据时间戳），
而维护窗口由运维**按墙钟**创建。本库数据时间 `2026-10-12` 比墙钟 `2026-10-17` 早 5 天，
**两个时基不一致 → 窗口永远匹配不上**。

**为什么是缺陷**：
1. 静默失败——`suppressed=0` 与"本来没有需抑制的告警"无法区分；
2. 与确定性公理冲突——系统铁律是"ts 来自数据"，但维护窗口是人类按墙钟做的计划，
   两者必须显式桥接，当前没有任何桥接；
3. **演示/回放场景 100% 踩中**——导入历史数据、按"今天"建窗口、重跑，抑制必然失效。

**建议**：明确时基语义（推荐"维护窗口 = 数据时间语义"，前端展示当前数据时刻并预填）；
即便选其他方案，也**必须**在抑制未触发时给出可观测原因，消除静默失败。

### 本轮同时验证通过（无缺陷）

- **维护窗口参数校验质量很高**：逆序/非法格式/零长/空 label/假资源 ID 五类边界全部正确拒绝，
  错误信息带具体时间和具体资源 ID；
- **确定性公理成立**：连跑两次检测，各项计数与 `evaluatedAt` 完全一致，告警 ID 稳定；
- **迁移完整**：27 个迁移全部应用，v0.12 新表与新索引齐备；
- `go test ./...` **24 包 ok / 0 FAIL**——但**测试全绿而功能实测失效**，
  说明现有测试覆盖数据层契约，没覆盖"墙钟窗口 + 历史数据"这个真实场景。

| # | 优先级 | 标题 |
| --- | --- | --- |
| [55](issue-55-maintenance-window-timebase.md) | **P1** | v0.12.0 维护窗口抑制静默失效：窗口按墙钟建、检测按数据时间评估，时基不一致时 suppression 永不触发 |

## v0.12.0 UI 全面扫描（#56 / #57）

来源：对 `e258543`（v0.12.0）的 UI 全面扫描，2026-10-17。
方法：headless Chrome + CDP（Network 域逐请求捕获 + 进程 CPU 采样）。

**覆盖**：12 个路由全部加载验证。**console 错误 / 未捕获异常 / HTTP 4xx-5xx 全部为 0**，
前端运行时健壮性很好；代码卫生也优秀（`any` 0 处、`console.log` 0 处、TODO 0 处）。

### #56（P0）：概览页无限渲染循环

`web/src/pages/Overview.tsx:381` 把 `getAlertStats()` **裸写在组件函数体里**，
不在任何 `useEffect` 内 → 请求回来 `setAlertStats` → 重渲染 → 再请求 → 无限循环。

CDP 实测（20 秒窗口）：

```
20秒内 /api/alerts/stats 请求次数: 1050
平均间隔: 19 ms          ← 约 52 次/秒，永不停止
```

CPU 实测（首页保持打开）：后端单核 **11.6%**、Chrome 渲染进程 **13%**。
**10 个用户同时在线 = 后端单核打满。**

同文件其它取数点（`load`/`loadGov`）都正确包在 `useCallback`+`useEffect` 里，
**只有这一处漏了**；全站扫描确认是孤例。

### #57（P1×2 + P2×1）：三个 UI/文档一致性问题

1. **告警列表资源列丢设备名**：4 条告警的资源列全是 `xgei-1/1/1`，
   实际分属「大庆市传输设备01」和「齐齐哈尔市传输设备01」两台设备，运维无法区分。
   根因 `Alerts.tsx:706` 优先显示 `name` 而非 `naturalKey`（后端两个字段都返回了）。
2. **质量报告硬编码"合成测试数据"声明**：`i18n.ts:986` 固定文案
   「本报告基于合成测试数据生成」，但系统装的是 3,464 条真实数据，
   用户导入真实台账后报告仍打这个标签——**合规风险**，且在真实数据场景下反向误导。
3. **`docs/api.md` 缺失 v0.12 两个新功能**：`maintenance/windows` 与 `correlationRole`
   在 API 文档中**零覆盖**（`/dev` 页面靠 openapi 自动同步没问题，但人工文档没跟上）。
   这正是 #55 那个缺陷的文档层面成因——**维护窗口的时基语义全系统无一处文档化**。

### 附：`gofmt -l` 报 293 个文件"未格式化"是假阳性

Windows 检出下 `gofmt -l .` 会列出大量文件，实为 **CRLF 行尾**（`gofmt` 只认 LF）。
抽查 `internal/api/auth.go` 确认全是 `^M` 差异，无真实格式问题。
建议 CI 在 Linux 跑 gofmt，或加 `.gitattributes` 归一化，避免误导开发者。

| # | 优先级 | 标题 |
| --- | --- | --- |
| [56](issue-56-overview-render-loop.md) | **P0** | 概览页无限渲染循环：组件体内裸调 getAlertStats，单用户即 52 QPS 持续压测 |
| [57](issue-57-ui-consistency-defects.md) | **P1/P2** | 告警资源列丢设备名 + 质量报告硬编码"合成数据"声明 + v0.12 API 文档缺失 |

## v0.12.1 验证（2026-10-19）

来源：对 `0a5dc98`（v0.12.1，四项修复）的独立复测。
**拉取方式特殊**：`github.com:443` 被阻断（`Test-NetConnection` = False），
但 `api.github.com`/`codeload.github.com` 正常 → 改用 GitHub API 下载 tarball（22.5 MB），
再用 robocopy 同步（注意 `core.autocrlf` 会让 447 个文件显示为修改，
`git add -A` 归一化后真实差异仅 **24 文件 / +742−95**）。

### 四项修复的独立验证

| Issue | 修复前 | 修复后 | 判定 |
| --- | --- | --- | --- |
| **#56** 渲染循环 | 1050 次/20s（52 QPS） | **1 次/25s** | ✅ **彻底修复**（约 1000 倍） |
| **#55** 时基失配 | 墙钟窗口 `created=1, suppressed=0` | **`created=0, suppressed=1`** | ✅ **双时基生效** |
| **#57-A** 资源列 | 只显示 `xgei-1/1/1` | 主行 name + 副行 naturalKey（去重） | ✅ 采纳方案 B |
| **#57-B** 免责声明 | "本报告基于合成测试数据生成" | "本报告基于当前已导入的数据生成，请以实际网络情况为准。" | ✅ 已改中性表述 |
| **#54** 布局尺度 | L0 宽 1902 / 比值 4.3 | **L0 宽 767 / 比值 1.55** | ✅ 尺度达标 |

`#55` 还额外实现了建议的 `suppressionNote`（回执里解释"为何没抑制"），消除静默失败。
`#54` 采纳了建议的方案 2（fit 排除 L0 + 锚链收拢），代码注释明确写「#53-B 的推理链被 #54 实测推翻」。

### #58（P2）：#54 的遗留——letterbox 护栏在目标不可达时静默失效

尺度失衡修好了，但画布横向利用率反而从 66.1% 降到 **34.1%**。手算复现：

```
bb = 869×1081（高瘦，宽高比 0.804），视口 1318×624（宽高比 2.112）
zoomNeeded = 0.72 × 1318 / 869 = 1.092
padNeeded  = (624 - 1.092×1081)/2 = -278.2   ← 负数
Math.max(12, -278.2) = 12                     ← 静默钳掉
实际 zoom = min(1.389, 0.565) = 0.565（纵向约束主导）
横向利用率 = 37.2%（实测 34.1%）
```

**`padNeeded < 0` 说明"横向 72%"物理不可达，但代码只把负数钳到 12，不报错不降级。**
建议让"不可达"成为显式分支，并按内容宽高比分类设定验收指标
（高瘦内容应看纵向利用率——当前其实达 97.8%，问题在"右侧留白"的观感）。

### 环境问题：fsync 被 Kaspersky 拖慢 200–2000 倍

`go test ./...` 有 6 个包超时，但**零个断言失败**，全部 `panic: test timed out after 10m0s`，
堆栈统一停在 `FlushFileBuffers → _winSync → SQLite WAL 提交`。

最小复现（纯 Go，不涉仓库代码）：**50 次 write+fsync 耗时 11.53s（每次 230.6 ms）**——
正常 NVMe 应为 0.1–1 ms。旁证：`repo.test` 运行 395 秒，CPU 仅 4.5 秒（利用率 1.14%），
在等 I/O 而非计算。环境确认 `avp.exe`×4 运行中。

**这是本机环境问题，不是 v0.12.1 回归**：18 个包全部 ok（含 topology 510s、metrics 324s 等重包），
单独跑 `-run TestOpen` 三个测试全部 PASS，`go vet` 全通过。
建议把工作区与 `E:\temp` 加入杀毒排除项后重跑。

| # | 优先级 | 标题 |
| --- | --- | --- |
| [#62](issue-58-letterbox-unreachable-target.md) | **P2** | #54 遗留：letterbox 护栏在「内容纵向装不下」时静默失效，72% 目标数学不可达（GitHub issue 编号 #62——#58–#61 已被费用分析模块占用） |
| [env](env-fsync-antivirus-slowdown.md) | 环境 | 本机 fsync 被 Kaspersky 拖慢 200–2000 倍，导致 go test 大面积超时（非代码缺陷） |

## v0.13.0 费用分析层实测（2026-10-19）

来源：对 `12a46ec`（v0.13.0，费用分析层阶段 1/2/3，migration v29/v30）的独立实测。
方法：读实现 + 最小复现程序量化 + 端到端跑 CLI 导入。

### 实测通过的部分（质量很高）

| 能力 | 实测结果 |
| --- | --- |
| CSV 导入 + 自动识别表头 | ✅ `站址编码/电费(元)/账期` 识别成功 |
| 金额规范化 | ✅ `"1,234.56"` / `¥999.00` / `￥888.88` 全对 |
| **行级账期拆分** | ✅ 4 行拆成 `month=05`(3 行) + `month=06`(1 行) 两个 Parquet |
| 内容寻址快照 | ✅ `fee-imports/electricity/2024/05/<sha256>_*.csv` |
| **幂等重导** | ✅ `skippedDuplicate: true`、`exit 0` |
| **批次共存** | ✅ 第二文件同站同账期 → **两个 batch 文件共存，绝不合并** |
| 默认构建诚实降级 | ✅ 查询答 `501 engine_not_built`，错误信息说明原因 |

幂等 + 共存做得尤其好——后者正是"重复结算检测"的数据基础。

### 三处需要决策的缺陷

**① （P1）金额用 `float64` 存储**

`parquet.go:37` / `domain.go:128` / `normalize.go:50` 全是 `float64`。
最小复现实测：

```
累加 100 万次 0.01 = 10000.0000001719（应为 10000.00），误差 1.7e-7 元
不同求和顺序差异 7.5e-9 元
0.1 + 0.2 = 0.30000000000000004441 ≠ 0.3
```

单笔无害，但**对账取整到分**、**重复结算金额比对**、**双引擎聚合差**三个场景会暴露
（#59 发现的 0.07% 计数差正是这个问题的表现）。

值得注意的反差：同文件 `NormalizeSiteCode` 注释明确写"身份字符串，**绝不数值化**"——
**身份字段刻意不数值化，金额字段却直接 ParseFloat**，两种精度纪律不一致。
建议改整数分（`int64`）存储。

**② （P1）模糊匹配 O(codes × candidates × len²)，万级站址即超时**

`link.go:174-261` 的 `ScanLinks` 对每个 code 遍历全部 candidates 算 Levenshtein。
`LinkableResourceTypes` 含 `base_station`（当前库 **1,198 个基站**）。

最小复现实测：

```
10000 codes × 1364 candidates = 13,640,000 次比较
实测 100 codes: 155 ms → 外推 10000 codes: 15.5 秒
外推 50000 codes: 78 秒（1.3 分钟）
```

若资源库扩到 1 万对象 → 上亿次 Levenshtein → **HTTP `POST /api/fees/links/scan` 必然超时**。
加重因素：`sweepAutoDropouts` 每次全表加载链接。
建议：长度差预筛（能省大部分）、前缀分桶、评估 `base_station` 是否该进 fuzzy 候选、扫描改异步。

**③ （P1）Windows 无法构建 duckdb 版本，费用查询永久 501**

实测 `CGO_ENABLED=1 go build -tags duckdb` 在 Windows 链接失败：

```
ld.exe: cannot find -lrstrtmgr
```

而 `run.ps1` 与 `Makefile` 都**没有 duckdb 构建路径**（grep 为空）。
→ Windows 用户（本项目主要使用环境）**没有官方途径**获得可查询的费用功能。
建议：文档给出 Docker/WSL2 路径、提供 linux 镜像、或评估 duckdb CLI 子进程模式。

**④ （P3）`site_code` 英文表头不在同义词表**

`recognize.go:153` 的 `siteCode` 同义词全中文。实测 `site_code` 表头 → "缺少必需列映射"。
费用表格来自不同系统导出，英文表头很常见，建议补充 `site_code`/`siteCode`/`station_code` 等。

| # | 优先级 | 标题 |
| --- | --- | --- |
| [fees](issue-fees-layer-findings.md) | **P1×3 + P3** | 费用分析层实测：float64 金额、模糊匹配 O(n×m)、Windows 无 duckdb 构建路径、英文表头不识别 |

## v0.15.1 检查更新 + #65 验证（2026-10-20）

来源：从 `6e7e4c0`（v0.13.0）拉到 `ddd9c3e`（v0.15.1），共 17 个提交。

### 云端进展

| 版本 | 内容 |
| --- | --- |
| **v0.13.1** | **#65 修复轮**（整数分金额 + 无损模糊剪枝 + Windows 查询路径） |
| v0.14.0 | 费用风险六场景 + 证据追溯（#64、#58 阶段 4） |
| v0.15.0 | 费用工作台收尾：MCP `fee_risks`（第 11 个工具）+ `/fees` UI + 关联人工通道 |
| v0.15.1 | 关联重提案修复（#69）+ 其他（#68/#70/#71/#72/#73） |

MCP 工具数 5 → **11**。

### #65 三项 P1 的独立验证（不依赖仓库测试）

**✅ P1-a 金额整数分 —— 彻底修复（11/11 边界全过）**

```
"1,234.56" -> 123456    "¥999.00" -> 99900    "￥888.88" -> 88888
".5" -> 50              "-1234.56" -> -123456 "０１２３．４５" -> 12345
"1234.567" -> 拒绝（不静默取整）      "1e3" -> 拒绝
"92233720368547758.07" -> MaxInt64    ".08" -> 拒绝

累加 100 万次 0.01 = 10000.00 元 精确无误差
（v0.13.0 float64 对照：10000.0000001719，误差 1.7e-7 元）
```

实现深度超出建议：逐位定点解析（全程不过 float）+ 第三位小数拒绝 + int64 逐位溢出保护。

**✅ P1-b 模糊匹配性能 —— 彻底修复**

```
TestFuzzyScanPerformance: 10k codes × 1k 同长度类候选 (12 workers): 301.6ms
v0.13.0 我实测外推: 15.5 秒 → v0.15.1: 0.3 秒（约 50 倍）
```

三道剪枝均带**无损证明**，且 `cap` 由朴素表达式求得不做浮点代数截断，
fast path 与原路径逐位相同。额外发现我没想到的坑：
**分块 NOT IN 是错的而不只是慢**（每批只保护自己那片会重删），改用 TEMP keep table。

**⚠️ P1-c Windows 查询路径 —— 绕开而非解决**

`rstrtmgr` 确为 mingw-w64 缺失。改为 Docker 默认带引擎 + `make build-duckdb` + 文档双路径。
**错误消息自引导**（采纳建议 1、4）：501 响应体直接给出 Docker/WSL2 一行命令与文档地址。
仍开放：Windows 用户须额外装 Docker/WSL2，考虑评估 duckdb CLI 子进程模式。

**✅ P3 英文表头** —— `Site Code/siteCode/station_code/cell_code` 可映射，不挤占中文顺序。

### 费用风险模块观察（非缺陷）

六场景落地，措辞纪律值得肯定（`risks.go`）：

> 账单存在 ≠ 认定结论…… **No scenario decides "duplicate payment" or "violation"**

`/fees` 页面实测：加载正常、**零 console 错误**、三功能区齐全。
`RiskResult.Excluded` 显式暴露排除计数 + 每场景 `note` 解释口径。

### 本轮环境问题

**`npm run build` 因本地 `node_modules` 缺 `vitest` 而完全失败**（非仓库缺陷）：
`tsc -b` 报 `Cannot find module 'vitest'` → `vite build` 未执行 → `dist` 保持旧文件
→ `go build` 嵌入过期前端。`npm install`（51 包）后恢复。

值得确认的设计点：`tsconfig.app.json` 把 `.test.ts` 纳入类型检查，
使**测试文件类型错误阻断生产构建**。若非刻意，建议排除 `*.test.ts`。

| 文件 | 内容 |
| --- | --- |
| [v0151](v0151-check-and-issue65-verification.md) | v0.15.1 检查更新 + #65 三项 P1 独立验证 |

## v0.15.2 验证（2026-10-20）

来源：拉取 `80d33a5`（v0.15.2，7 个提交），重点验证 #75 修复。

### #75 跨平台路径契约 —— ✅ 彻底修复

修复方式与建议完全一致，并**额外做了 v31 迁移**处理旧反斜杠数据（正是验收标准第 4 条）。

我的独立验证（不看自述，直接跑）：

```
SnapshotPath 返回值: fee-imports/electricity/2024/05/abc123_bill.csv
含反斜杠: false
以 fee-imports/ 开头: true

TestAPIFeesImportJourney          → PASS (2.70s)   ← 修复前 Windows 上稳定 FAIL
TestSnapshotPathIsLogicalContract → PASS
TestMigrationV31NormalizesFeeSnapshotPaths → PASS
```

实现要点（提交 `3377762`）：
- 写侧 `SnapshotPath` 与分区 rel 改 `path.Join`；磁盘拼接处统一 `filepath.FromSlash` 一次转换；
- **migration v31**：纯 UPDATE 重写旧反斜杠行（`LIKE '%\%'` 守卫，幂等）；
- 顺带修正了 parquet 布局测试的期望值（旧 `filepath.Join` 期望是**同一 bug 的镜像**）。

### 其余三个修复

| 提交 | 内容 | 我的验证 |
| --- | --- | --- |
| `92a94ca` | **Makefile 恢复 TAB 缩进** | ✅ 实测 12 个 TAB、0 个 4 空格缩进 |
| `e97b4be` | workflow 解析失败（步骤名含 `#72:` 被当 YAML 注释） | 未能本地验证（GitHub Actions） |
| `68194a1` | 模糊扫描减少 2000 万次 `[]rune` 转换 | 未能独立量化（未单独跑基准） |

**`92a94ca` 值得记一笔**：`make` 从 **v0.13.1 起就坏了**（`c82bb0e` 重新引入 8 空格缩进回归），
但 CI 用的是 `cd web && npm ci && npm run build` + 裸 go 命令，**从不调用 make**，
所以 CI 全绿而本地 `make build` 直接报 `*** missing separator`。
这与 #72「全包 duckdb 构建是门禁盲区」属同一类问题：**门禁覆盖不到真实入口**。

### 构建与服务验证

```
前端构建: exit 0, 2m7s
二进制:   65,282,048 bytes, v0.15.2
服务:     health=ok
数据:     资源 3464 / 告警 4（v31 迁移后旧数据完好）
```

`/api/stats` 首次请求 5,936ms，复测 5 次为 195/15/11/11/9ms ——
**冷启动 + 本机 fsync 慢所致，非性能退化**（首次后稳定在 10ms 级）。

### v0.15.1 全量测试发现（2026-10-20 追加）

`go test -timeout 25m ./...`：**24 包 ok / 1 包 FAIL / 1 个断言失败，无超时**。

唯一失败是 **Windows 路径分隔符**——`TestAPIFeesImportJourney` 稳定失败（单独跑 2.9 秒必现）：

```
fees_test.go:136: snapshotPath = "fee-imports\\electricity\\2024\\05\\c84b..._may.csv",
                  want the content-addressed tree under the declared period
```

**根因**：`snapshot.go:64` 与 `parquet.go:99` 用 `filepath.Join` 生成**相对路径**，
Windows 产 `\`、Linux 产 `/`。而这个相对路径是**跨平台数据契约**：
存进 SQLite `fee_batches.snapshot_path`、返回 HTTP API、CLI JSON、前端展示。
`domain/fees.go:99` 的注释就写着 "relative to the data dir (content-addressed)"——
**是逻辑路径不是 OS 路径**。

**后果**：Windows 导入 + Linux 读取时库里路径拼不出磁盘；且 CI 跑在 linux 上
**永远看不到这个失败**，Windows 开发者每次都撞。

**修复**：改用 `path.Join` 生成相对路径（实测产出 `fee-imports/electricity/2024/05/...`），
用 `filepath.FromSlash` 在使用前转回 OS 路径；旧反斜杠数据在读取侧归一化。

更详细的见 [issue-66](issue-66-windows-path-separator.md)（GitHub: [#75](https://github.com/mmqz/netops-lab/issues/75)——#66–#74 已被其他 issue 占用）。

## v0.8.1 本地验证（2026-10-14）

来源：[docs/audit-v0.8.1.md](../audit-v0.8.1.md)。v0.8.1 是针对 #38–#45 的修复轮。

**验证结论：8 个 issue 中 7 项完全修复，1 项仅剩测试断言缺陷。**

| # | v0.8.0 | v0.8.1 | 判定 |
| --- | --- | --- | --- |
| 孤立资源 | 1,486 (75.6%) | **197 (9.9%)** | ✅ #42 |
| `uplink_to` / `serves` | 0 / 0 | **1,198 / 1,228** | ✅ #42 |
| 有 status | 40 (2.0%) | **1,826 (91.7%)** | ✅ #45 |
| geo 列 | 无 | **1,288 有值** | ✅ #45 |
| 工作台 接口/链路 | 20 / 10 | **64 / 32** | ✅ #42 |
| 工作簿 / 行数 | 31 / 1,965 | **32 / 1,991** | ✅ |
| 导入失败 | 0 | 0 | ✅ |
| CLI 图查询 | 无 | traverse/impact/context | ✅ #41 |
| 空集合契约 | `null` | `[]` | ✅ §2.6 |
| Windows 测试 | 6 包 15 项失败 | 1 包 1 项 | ✅ #38 |

| # | 优先级 | 标题 | 说明 |
| --- | --- | --- | --- |
| [46](issue-46-cli-http-clock-assert.md) | **P1** | TestCLIGraphQueryChannels 恒失败 | 断言把 `stats.elapsedMs`（运行时钟）纳入深比较，必然不同 |

### 导入顺序（v0.8.1 新增约束）

v0.8.1 把「所属环/上联设备/服务客户」从文本**升级为引用列**，导入必须按依赖序：

```
customers rings igp_domains vpn_instances sites machine_rooms bbus olts
devices boards racks ports pon_ports base_stations rrus onus
optical_cables cable_segments ducts manholes poles odf odf_terminals
splice_boxes fibers otn_ochs sdh_timeslots circuits protection_groups
ippools services leased_lines
```

顺序错误会报 `E003 引用不存在` 并**级联失败**（device 失败 → board/port/base_station 全失败）。
注：`scripts/demo-real.sh` 的 ORDER 数组仍是 v0.8.0 的旧顺序，建议同步更新。
## v0.8.0 真实数据演示：用户现场反馈（#42–#45）

来源：2026-10-13 用户实际操作 v0.8.0 真实数据演示后的反馈 + 本地库直查复核。

| # | 优先级 | 标题 | 用户原话要点 |
| --- | --- | --- | --- |
| [42](issue-42-realdata-topology-depth.md) | **P2** | 真实数据层级不足：75% 资源孤立、1193 基站无上联、链路仅 10 条且是端点对、**物化丢弃 90 端口致 18/30 设备零接口** | 「数据的层级不够啊数据太少」「有点没连上是端点设备」 |
| [43](issue-43-geo-render-performance.md) | **P2** | 地理视图卡顿：点击即全量重建 GeoJSON + 簇标记全量拆建 | 「每次点击画布地图的数据都要重新渲染，卡顿」 |
| [44](issue-44-lab-layout-density.md) | P3 | 拓扑工作台布局：头部三行冗余（实测吃掉 250px 视口）+ 面板默认展开挤占画布 | 计数 chips 移标题行 / 7 按钮与画布工具栏同行 / 面板默认折叠 |
| [45](issue-45-data-field-thinness.md) | **P2** | 演示数据字段厚度不足：98% 资源无状态、基站字段远少于真实表、geo 仅存 JSON 字符串 | 「数据的层级不够啊数据太少」（字段层） |

### 关键实测数据（#42 依据）

```
资源总数 1965   有关系 479 (24.4%)   孤立 1486 (75.6%)
  孤立构成：base_station 1193、port 90、odf_terminal 88、splice_box 46 …
工作台：设备 30 / 接口 20 / 链路 10
  链路性质：links 表 a_interface_id→z_interface_id（label=CIR-xxx）
            = 每条是「电路两端接口对」，非真实网络互联  ← 用户判断正确
```
## 地图相关的三个缺陷（叠加关系）

2D/3D 地图「黑色空白」由**两个独立缺陷叠加**造成，修复需同时处理：

```
issue-01  CSS 高度塌陷  →  所有平台，主因（黑屏）
issue-02  MIME 错误     →  仅 Windows，叠加（黑屏）
issue-09  光柱渲染      →  修复后才可见：3D 节点畸形 + 无地形
issue-11  底图信息量    →  修复后才可见：底图过简 / 无切换
```

- **只修 issue-01**：Linux 地图恢复；Windows 仍黑屏（Worker 失败）
- **只修 issue-02**：地图仍黑屏（高度塌陷）
- **两者都修**：地图正常显示，但 3D 节点呈巨型光柱（issue-09）

## 共同根因

**CI 仅覆盖 `ubuntu-latest`**（`.github/workflows/ci.yml:17` 与 `:38`）。

`.mjs` 的 MIME 映射、`.exe` 后缀需求、Windows 非法文件名字符、
文件句柄释放时序差异——这几类问题在 Linux 上均不复现，故长期未被发现。
但 **issue-01 与 issue-09 与平台无关**，说明 CI 也未覆盖地图交互路径。

建议：
1. 引入 `windows-latest` job（至少覆盖 `go test`）
2. 为 2D/3D 地图增加端到端冒烟测试（断言 canvas 撑满容器、`loaded() === true`）

## 已实测通过的部分

以下能力经实测确认**正确可靠**，不在问题清单内：

- **仿真引擎确定性**：两次物化 hash 完全相同（`aea27501fa59…`）
- **三层撤销**：事务回滚 / 变更集逆向 / 检查点恢复，哈希精确回环
- **MCP 安全边界**：无法创建工作区、无 SQL/Shell 工具、内部操作码被拒、
  schema 严格校验、失败变更集留档不污染状态
- **MCP 全闭环**：注入故障 → 校验 → 影响分析 → 撤销 → 哈希回到基线
- **导入管线**：幂等重导、重复表头跳过、复合自然键、精准错误定位
- **CLI/API 错误处理**：统一 JSON 信封、规范退出码、非法枚举 400
- **拓扑画布**：Cytoscape 渲染、状态着色、图例、迷你地图均正常

## v0.15.2 Windows 测试发现（2026-10-20）

来源：#77 记录声称「25 包 Go 测试 ok / 无可做」，Windows 侧独立复核。

`go test -timeout 25m ./...`：**24 包 ok / 1 包 FAIL / 1 个断言失败**。

```
--- FAIL: TestSnapshotLayer (0.00s)
    parquet_test.go:150: snapshot path = "fee-imports/electricity/2024/05/db5406..._bill.csv",
                         want "fee-imports\\electricity\\2024\\05\\db5406..._bill.csv"
```

**根因**：#75 修复把产品侧路径改为逻辑路径（`/`），大量测试期望同步改成
`filepath.FromSlash` 或字符串常量，**唯独 `parquet_test.go:148` 漏了**：

```go
want := filepath.Join("fee-imports", "electricity", "2024", "05", sha+"_bill.csv")
```

同文件 30 行已改为字符串常量（并加了注释说明），48 行读盘用 `FromSlash` 正确，
`service_test.go:86`、`snapshot_test.go:52` 也都正确 —— **仅此一处遗漏**。
全仓库扫描确认：用于**断言期望值**的 `filepath.Join` 只剩这一个。

**CI 看不见的原因**：CI 跑 linux，`filepath.Join` 与 `path.Join` 产出相同，测试全绿。

**验收建议**：修复后加守卫测试——`TestSnapshotPathIsLogicalContract` 已覆盖
`SnapshotPath`，但**未覆盖 `WriteSnapshot` 的返回值**，而本 issue 恰好走的是这条路径。

### 附：云端声明的独立核实

| 声明 | 核实 |
| --- | --- |
| 25 包 Go 测试 ok | ❌ Windows 上 24 ok / 1 FAIL |
| CI 配额未恢复 | ✅ 属实，GitHub 原文 `recent account payments have failed or your spending limit needs to be increased`（runner_id=0，4-12 秒失败） |
| 仅剩 #76/#78 两个 open issue | ✅ 属实，均为外部阻塞 |
| 浏览器回归 68 断言全绿 | 未独立复跑（需 agent-browser 环境） |

**结论**：云端"无可做"大方向准确（卡在账户计费），但"测试全绿"在 Windows 上不成立。

## #77 浏览器回归脚本校准（2026-10-20）

来源：GitHub issue [#77](https://github.com/mmqz/netops-lab/issues/77)（CI 空转窗口内前端演进而脚本断言未跟）。commit `4f00a71`。

### 三脚本终态

| 脚本 | 校准前 | 校准后 | 本轮改动 |
| --- | --- | --- | --- |
| w17 | 16 PASS / **19 FAIL** | **40 PASS / 0 FAIL** | 全面重校准 + 新增 A0 组 |
| w28 | 12 / 2 | 14 / 0 | 无（`80d33a5` 已修，本环境复跑确认） |
| w47 | 13 / **1 FAIL** | **14 / 0 FAIL** | B12 拆为宽/窄两契约 |

### 19 项失败的根因分类（逐项对照当前产品意图）

| 根因 | 项数 | 校准 |
| --- | --- | --- |
| 偏好存储 **v2→v3 迁移**（#50：重图层默认降级 + 懒加载，v2 键内容被迁移逻辑强制重置为关） | 15 | 预置键升级 v3 显式开重图层（=「恢复完整底图」一键开的存储等价态）；**新增 A0 组钉死 #50 默认最小集**：无偏好时 contours/forest/hillshade=none、区划+光缆（v0.8.0 主叙事）=visible |
| **懒加载空源**（默认关的叠加源是空 FeatureCollection） | 探针层 | 开启后 querySourceFeatures 取到真实特征：等高线 31,839 / natural 22,905 / 地名 38,112 |
| **terrain 异步挂载**（effect 在 sourcesReady 后） | 4 | 固定 sleep 改 wait-fn 轮询；C3 改「夸张与 zoom 分档函数一致」断言（实测 3D fit 落在 zoom 8.15 → 1.3 档，旧「远观 ≥2.5」在当前 fit 语义下无意义）；D2 随之看到 77 个真实 DEM 请求 |
| **maplibre 加载后 data 变解析对象**（GCJ 探针） | 2 | 见下「探针方法论」 |
| **#44 换行收纳**（窄画布两行工具带是有意设计，CSS 注释明言 wrap 严格优于旧 nowrap 的横向逃逸） | 1 | B12 拆两契约：1600 单行 ≤52px；1280 组内不折行（seg 34px/actions 32px）+ 零横向溢出 |

### 探针方法论（GCJ 变体断言的三轮迭代）

1. **网络请求探针 ✗**：maplibre 的 geojson 源由 worker fetch，agent-browser 的 CDP
   网络记录对此失明（同会话 raster-dem 瓦片 77 个可见、hlj-* geojson 0 个）。
2. **特征质心探针 ✗**：querySourceFeatures 的特征子集随视口 tile 化时点漂移，
   质心噪声远大于 GCJ ~0.005° 系统性平移（实测切回后质心差 0.004 系采样噪声）。
3. **setStyle spy ✓**：monkey-patch `map.setStyle` 记录每次样式重建传入的
   natural 源 URL（`buildGeoStyle` 的确定性产物）——无竞速、无采样依赖。

### #76 现状确认（本轮 push 后）

run `39403484430`（`4f00a71`）：workflow 解析正常（go/web/duckdb/windows 四 job 齐全），
但四 job 仍 `runner_id=0` / 3 秒死——**Actions 分钟配额未恢复**（#76 第二段诊断在案，
等 owner 充值或月度配额刷新）。本轮校准验证全部在本地沙箱完成：
25 包 Go 测试 ok / gofmt 空 / vet 0 / vitest 40/40 / tsc 0 / 三浏览器脚本 68 断言全绿。

### #79/#80 修复轮（2026-10-20，第二轮）

接上表（#77 校准轮）。本轮两项闭环 + 一项延伸审计：

| 项 | 内容 | 提交 | 运行态 |
| --- | --- | --- | --- |
| **#79**（P1，测试侧） | `parquet_test.go:148` 期望值入逻辑路径契约（`filepath.Join` → 字符串常量 + `FromSlash` 读盘 + `WriteSnapshot` 返回值反斜杠守卫）——#75 修复的最后一处漏改，Windows 稳定失败、linux CI 不可见 | `ef0262a` | run 39418864077：4 job 壳 + runner_id=0（配额模式，#76 在案） |
| **#80**（P2，产品侧，本轮立案+修复） | #79 复扫发现 `checkpoints.file_path` 以平台原生分隔符持久化（跨平台搬库后 restore 必断）——`path.Join` 写侧 + `FromSlash` 双边界 + **migration v32** 归一存量（v31 孪生）+ 守卫测试；14 处 registry-head 钉随约定 31→32 | `06ce6cd` | run 39420112339：同配额模式 |

**契约闭环**：#75「持久化相对路径 = 跨平台契约」的两个产品侧落点（fee `snapshot_path` → v31 / checkpoint `file_path` → v32）与测试侧镜像（#79）全部收口；持久化路径列普查确认无剩余违规点（`workbook_path` 为调用方本地路径原样记录，不同类）。

**门禁**（两轮同集）：25 包 ok / 0 FAIL · gofmt 空 · vet 0 · -race sim+repo 0 race · tsc 0 · vitest 40/40 · demo.sh + demo-lab.sh 回归绿。镜像文档：[issue-79](issue-79-snapshot-test-missed.md) · [issue-80](issue-80-checkpoint-path-contract.md)。

### #114 修复轮（2026-10-22，v0.15.2 后 main）

v0.15.2 全仓调研定位的费用×指标交叉验证整块缺口：六场景的跨层组合只交叉资源 `status`（retired-billing）与关联面（unlinked），原始需求「停电/无业务量在账」中**费用台账 × 业务量台账**的交叉无场景承载——一个站点可同时"账期计费在流"与"指标面该账期零采集点"而不可见。

| 项 | 内容 | 提交 |
| --- | --- | --- |
| **#114**（新增能力，本轮立案+修复+关闭） | 第七场景 `idle-billing`「有费无量」：引擎 `SitePeriodTotals`（费用面）× confirmed 关联 → 新读原语 `MetricRepo.CountPointsOfResource`（指标面，一次索引 join），**账期窗口内零点触发**、无阈值（零点是确定性信号）；窗口 = 自然月 RFC3339 字典序（与 `ParseTS` 归一约定一致），month=0 整年口径逐 (站址,账期) 开窗；`#85` 双 memo（link 一站一查、计数一 (资源,账期) 一查）；线索 note 恒携带告诫——**采集缺失与空置不可区分，仅供人工核实**（"账单存在≠认定结论"的指标面镜像）；消费面闭集六→七同步（MCP 描述+schema / 路由摘要 / CLI usage 自动派生 / /fees 下拉+i18n 常量 / fees-layer.md §8 场景表+诚实注意义务段 / api.md / facts.md / README） | 本轮 |

**门禁**（本地执行，CI 配额外部阻塞不依赖）：25 包 ok · 0 FAIL（默认 + duckdb 双构建）· gofmt 空 · vet 0（双构建）· vitest 188/188 · tsc 0 · `make web` 重建 dist 后 webui/app/fees 复测 ok。组合逻辑单测（窗口内外/无关联/整年逐月开窗/#93 空锚点非 null）+ 窗口数学单测（平/闰年边界）+ 封闭集三面（fees/mcpserver/e2e）+ duckdb 真实引擎 e2e（RETIRED-SITE 零点触发 vs ACTIVE-SITE 有 6 月点静默 + 单月诚实空）。镜像文档：[issue-114](issue-114-fees-idle-billing.md)。CHANGELOG 入账待发布轮（AppVersion 门禁约束：非发布轮不动 CHANGELOG 头部）。

### #115 修复轮（2026-10-22，v0.15.2 后 main 第二轮）

全仓遗留调研唯一命中的功能欠账：api.md:941 / import-and-validation.md:354 双处声明的「指标批次回滚 CLI 随 W24 一并收口」承诺未兑现——三大导入通道中指标通道是唯一 CLI 无回滚的（资源 `rollback` / 费用 `fees rollback` 齐备，离线操作者导错指标文件无法撤销）。

| 项 | 内容 | 提交 |
| --- | --- | --- |
| **#115**（CLI 通道补全，本轮立案+修复+关闭） | 新增 `metrics` 顶层命令（`list` / `rollback`）：`--db` 前置 + 子命令封闭集派生 usage（#108 教义）+ go/ast 闭集测试双向钉死（dispatch 表登记 + 顶层 19→20）；`list` 输出与 HTTP `GET /api/metrics/batches` 完全同形（分页/字段/字符串 status）；`rollback --batch` 与 HTTP 端点**同引擎同错误码表**（conflict / not_found / usage → exit 1/1/2，零新增映射逻辑）；e2e 全旅程（synthdata→资源导入→ingest→list→rollback→复滚 conflict→未知 id not_found→usage 五形态）；api.md 双处承诺句收口 + CLI 通道小节补「三大导入通道 CLI 回滚齐全」事实 + README 20 命令 | 本轮 |

**门禁**（本地执行）：25 包 ok × 双构建 · 0 FAIL · gofmt 空 · vet 0（双构建）· vitest 188/188 · tsc 0。镜像文档：[issue-115](issue-115-metrics-rollback-cli.md)（含 `runCLIJSON` 首参是标签非命令的踩坑记录）。

### #116 修复轮（2026-10-22，v0.15.2 后 main 第三轮）

#66/#110 家族的收口轮：工具数/路由数漂移靠人眼清扫永远追不上——本轮给「散文契约」补上结构性门禁。

| 项 | 内容 | 提交 |
| --- | --- | --- |
| **#116**（门禁 + 漂移修复，本轮立案+修复+关闭） | ① api.md 补全三条零文档路由小节（openapi.json / alerts/stats / layer-members——全量比对 82 vs 79 确认）；② 新门禁 `TestAPIMDocHeadingsMatchRouteTable`：api.md 方法标题集 ↔ `apiRoutes()` 表**双向集合比对**，零豁免，双向红验证实测（删小节 → missing；幽灵标题 → no longer exist）；③ README 两处 #66 型漂移修复（「MCP 五工具」→「MCP 仿真域工具」+ 指向 11 工具闭集事实；目录树注释 11 工具现实） | 本轮 |

**门禁**（本地执行）：25 包 ok × 双构建 · 0 FAIL · gofmt 空 · vet 0（双构建）。镜像文档：[issue-116](issue-116-prose-contract-gates.md)。

### #117 修复轮（2026-10-22，v0.15.2 后 main 第四轮）

#110「散文冻结了谎言」缺陷类在场景 5 上的实例：标签称「同类站**单位成本**离群」，实现对月度**总额**直接 z-score（无规模归一化——总额口径会把大站误报为离群）。

| 项 | 内容 | 提交 |
| --- | --- | --- |
| **#117**（口径诚实化，本轮立案+修复+关闭） | 裁决**诚实优先**：单位成本归一化需规模基数字段而当前数据面不存在（任何默认基数都是发明数据），且需跨层重构——标签对齐实现（六面同步：Go 标签/MCP 描述/api.md/fees-layer §8/project-overview/前端 i18n）；场景 note 增补规模告诫；`TestRiskOutlierLabelAndNote` 钉死新标签 + note 三要素；归一化登记为显式参数化未来增强（`normBasis`，前置 = 基数字段落地），历史文档按教义不改写 | 本轮 |

**门禁**（本地执行）：25 包 ok × 双构建 · 0 FAIL · gofmt 空 · vet 0（双构建）· vitest + tsc（i18n 常量变更必跑）。镜像文档：[issue-117](issue-117-outlier-label-honesty.md)。
| **#118**（新增能力，本轮立案+修复+关闭） | 第八场景 `missing-billing`「有量无费」：`FeeSiteLinks.List(confirmed)` 关联宇宙（人类裁决过的配对才可测量）→ 退休状态排除（不给退休资源计费是正确形态，场景 1 管反向）→ (资源, 账期) memo 点数（#85 双 memo 纪律）→ 引擎 `SitePeriodTotals` 一次调用的有账集合反查；**点数>0 且无账触发**、无阈值（与场景 7 零点信号对称）；零行证据形态（evidenceTotal 恒 0、空锚点非 null——费用侧零行即异常本体）；note 恒携带告诫——**账期无记录 ≠ 漏收认定**（季度/年度结算、费用类型未导入、挂账他编码全部同相）；消费面闭集八处同步（枚举计数 7→8 / MCP 描述+schema / 路由摘要 / api.md / facts.md / fees-layer §8 / /fees 下拉+i18n / mcpserver 501 循环 / README） | 本轮 |
**门禁**（本地执行）：默认 + duckdb 双构建全绿 · 0 FAIL · gofmt 空 · vet 0（双构建）· tsc 0 · vitest 188/188。镜像文档：[issue-118](issue-118-fees-missing-billing.md)。
| **#119**（新增能力，本轮立案+修复+关闭） | 风险线索核实闭环（migration v33 `risk_clue_decisions`）：线索身份 = (scenario, fee_type, 站址, 年, 月) UNIQUE、无 FK（代码空间事实）、回滚不触碰（W005/W006 教义）、UPSERT 可复议翻转；两状态 verified（属实——保持可见标记 = 已知问题台账）/ dismissed（排除——默认隐藏、信封 `dismissed` 计数，绝不静默丢弃 #83、重跑不再现 #69 PairDecided 教义）；`includeDecided` 审计视图；裁定按 (scenario, feeType) 隔离；写通道仅人类（HTTP POST/GET + CLI `fees clue-decisions`，MCP 无写面），默认构建可裁定（纯审计行不依赖引擎）；42 处 maxV 断言随 v33 前进、路由 82→84 / fees 11→13 facts 门禁同步 | 本轮 |
**门禁**（本地执行）：默认 + duckdb 双构建 25 包全绿 · 0 FAIL · gofmt 空 · vet 0（双构建）· tsc 0 · vitest 188/188 · dist 重建后 webui/app 复验 ok。镜像文档：[issue-119](issue-119-risk-clue-decisions.md)。
