# 测试发现的问题清单

> 来源：2026-10-12 全面测试（Windows + Linux/Docker，v0.6.0 @ 2033de8）；#12–#14 为 2026-10-12 用户现场反馈（v0.6.2 验收后）；#15 为 2026-10-12 用户现场反馈（v0.6.3 验收后）；#16 为 2026-10-12 数据模型评审（v0.6.4 之后，架构级）；#17–#29 为数据模型设计衍生的决策项与缺失项（见 [design/data-model-v1.md](../design/data-model-v1.md) §11）；**#38–#41 为 v0.7.0/v0.8.0 本地审计发现（见 [audit-v0.8.0.md](../audit-v0.8.0.md)）**
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
| [40](issue-40-serves-edge-and-coverage-gaps.md) | P3 | 演示数据缺 serves 边，2 类型 / 5 关系无数据 | 数据覆盖缺口 | 库直查 |
| [41](issue-41-graph-query-cli-channel.md) | P3 | 图查询无 CLI 通道 | 符合设计但离线不可用 | CLI 实测 |

### 运维提示（重要）

```
拉取云端更新后必须重建前端：
  cd web && npm ci && npm run build
原因：internal/webui/dist 不入库（.gitignore）；
     不重建则新增地图图层静默缺失（SPA 回退返回 200 + text/html）
```

注：审计中 `TestHandlerRealDistRealNetworkLayers` 曾失败，重建前端后通过——
**属审计环境问题，非产品缺陷**，未开 issue。

---

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
