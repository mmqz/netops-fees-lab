# 测试发现的问题清单

> 来源：2026-10-12 全面测试（Windows + Linux/Docker，v0.6.0 @ 2033de8）；#12–#14 为 2026-10-12 用户现场反馈（v0.6.2 验收后）；#15 为 2026-10-12 用户现场反馈（v0.6.3 验收后）；#16 为 2026-10-12 数据模型评审（v0.6.4 之后，架构级）
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
