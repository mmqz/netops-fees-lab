# 测试发现的问题清单

> 来源：2026-10-12 全面测试（Windows + Linux/Docker，v0.6.0 @ 2033de8）；#12–#14 为 2026-10-12 用户现场反馈（v0.6.2 验收后）；#15 为 2026-10-12 用户现场反馈（v0.6.3 验收后）；#16 为 2026-10-12 数据模型评审（v0.6.4 之后，架构级）；#17–#29 为数据模型设计衍生的决策项与缺失项（见 [design/data-model-v1.md](../design/data-model-v1.md) §11）；**#38–#41 为 v0.7.0/v0.8.0 本地审计发现（见 [audit-v0.8.0.md](../audit-v0.8.0.md)）；**#42–#45 为 v0.8.0 真实数据演示的用户现场反馈与复核；**#46 为 v0.8.1 本地验证发现；#48 为 v0.11.0 本地验证发现（见 [audit-v0.11.0.md](../audit-v0.11.0.md)）；#49 为拓扑连线分层的用户现场反馈**
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
