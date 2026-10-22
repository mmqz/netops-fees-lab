# #116 散文契约最后一公里：api.md 三路由无文档 + README 工具数漂移，落门禁钉死

> **状态**：已修复（2026-10-22，v0.15.2 后 main）。GitHub issue：https://github.com/mmqz/netops-lab/issues/116

## 1. 发现背景

v0.15.2 后的质量薄弱点调研（只读子代理）确认三个事实：

1. **README 两处 #66 型漂移**：README:97（Phase 2 特性节）「**MCP 五工具**：resource_search + 4 个 lab 工具」与 README:342「恰好 11 个工具」自相矛盾；README:269（目录树注释）「resource_search + 4 个 lab 工具」——#66 清 14 处、#110 再清 4 处后仍漏（纯人眼/grep 清扫追不上新增）；
2. **api.md 三条路由零文档**（全量比对：路由表 82 条 vs api.md 方法标题 79 条，零文档孤儿）：`GET /api/openapi.json`（v0.10-R9 落地，api.md 全文零次提及）、`GET /api/alerts/stats`（v0.11-R11）、`GET /api/workspaces/{id}/layer-members`（#51 簇）；
3. **根因**：facts.md 只钉数量（api-routes:82）不钉散文覆盖；api.md ↔ 路由表之间无任何门禁——自称「契约基线 v0.15.2」但基线无断言。

## 2. 修复

### 2.1 api.md 三节补全（按既有小节格式）

| 小节 | 内容要点 |
| --- | --- |
| `GET /api/openapi.json` | 路由表机械生成零漂移声明 + `TestOpenAPISpecMatchesLiveServer` 双向钉死 + 机器消费入口定位（散文契约 = 本文档，两道防线） |
| `GET /api/alerts/stats` | 闭集零填充形状（byState 三态 + activeBySeverity 级别矩阵，空库全零）、cleared 是历史不是工作的口径 |
| `GET /api/workspaces/{id}/layer-members` | cluster 必填（anchorDeviceId）、分页契约、簇→成员的 #51 影响分析下钻定位 |

### 2.2 散文契约门禁测试（facts_gate_test.go 同族）

`TestAPIMDocHeadingsMatchRouteTable`：解析 api.md 全部 `^#{2,4} METHOD /path` 标题（查询串与「 — 标题」后缀剥离），与 `apiRoutes()` 表**双向集合比对**：

- 路由新增不写文档 → 红（`missing`，附路由键列表）；
- 文档写死路由 → 红（`no longer exist`，#66 教义）；
- 零豁免——api.md 文档化的就是全量路由表。

**双向红验证**（临时破坏实测）：删 layer-members 小节标题 → 红「missing sections for 1 live route(s): [GET /api/workspaces/{id}/layer-members]」；加 `GET /api/ghost/endpoint` 幽灵标题 → 红「documents 1 route(s) that no longer exist: [GET /api/ghost/endpoint]」；恢复后绿。

### 2.3 README 两处漂移修复

- Phase 2 节：「MCP 五工具」→「MCP 仿真域工具」——如实描述仿真域 4+1 工具的 Phase 2 范围，并指向 11 工具全量事实（`TestMCPToolRegistry` 钉死）；
- 目录树注释：更新为「11 工具：resource_search + 4 仿真 + 5 图分析 + 1 费用风险」。

## 3. 门禁（本地执行）

25 包 ok × 双构建（默认 + `-tags duckdb`）/ gofmt 空 / vet 0 × 双构建（前端零改动，vitest/tsc 沿用 #114 轮绿态）。

## 4. 遗留说明

- facts.md 刻意不钉 api.md 小节数——新门禁钉的是**集合相等**而非计数（计数钉死只会添乱，#91/#108 教义）；
- README 的「五工具」句式属历史特性节的范围声明，本轮改为「仿真域工具」后，工具数主张仅存一处（11 工具，有闭集测试钉死）。
