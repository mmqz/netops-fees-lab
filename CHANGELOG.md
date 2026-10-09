# Changelog

本项目所有显著变更记录于此。格式参考 Keep a Changelog；版本遵循语义化版本（SemVer）。

## [0.1.0] — 2026-10-09

首个交付版本：**Stage 0（技术验证）+ Phase 1（资源治理）** 全量功能。合成数据、无生产写能力、单二进制分发。

### 新增 — 导入管线
- Excel 工作簿解析（多 Sheet、表头行、空行跳过计数、类型化单元格）
- 列映射模型与确定性建议引擎（置信度阶梯 1.0/0.9/0.75/0、同义词表、按 Sheet 类型限定）
- 三类资源 schema（device / ip_pool / circuit，字段级校验含取值范围）
- 用户确认映射后执行导入：全部 Sheet 单事务、行级记账（imported/skipped/failed）、幂等重导（skip-duplicate）、批次回滚（409 in-use 保护）
- 来源引用（source_refs：工作簿/Sheet/1-based 行号 + 原始行 JSON）

### 新增 — 数据质量校验与报告
- 规则目录 v1.0.0：E001–E006 / W001–W003 / I001（行级 7 条 + 集合级 3 条），严重级别映射集中于 domain 层
- 集合级引擎（网段重叠/地址重复使用/孤岛设备）：单事务删旧插新、幂等重算、确定性排序
- 质量报告：JSON + 独立 HTML（内联 CSS、自动转义、页脚合成数据免责声明）；评分 100−5×错误−1×警告（下限 0）；全局与批次两种口径
- 问题来源解析：source_ref / 行号证据 / applicationGenerated 三态

### 新增 — 查询与拓扑
- 资源检索（关键词/类型/状态、分页、详情 + 关系计数 + 来源引用）
- 全库统计（资源/状态/关系/问题计数、最近批次）
- 关系子图（1/2 跳 BFS，节点 + 边，无向遍历，确定性排序）
- 拓扑可达性检查（设备级图、maxHops、路径 hop 列表）

### 新增 — 出口层
- HTTP API（Go 1.27 stdlib 路由、统一错误信封、panic 恢复、slog JSON 请求日志、10MB 上传限制、优雅停机）
- CLI：`serve / analyze / import / validate / report / query / mcp / testdata`；成功输出单一 JSON 对象；退出码 0/1/2
- MCP stdio 服务器（go-sdk v1.8.0）：仅注册一个只读工具 `resource_search`
- React 18 + TypeScript（strict）+ Vite 前端：概览/数据导入三步向导/资源浏览（抽屉+二跳）/问题清单/质量报告，中文 UI，手写设计系统；`go:embed` 嵌入单二进制

### 新增 — 工程化
- SQLite 迁移框架（v1–v4）+ 六个聚合仓储；DSN pragmas（WAL / busy_timeout / foreign_keys）
- 合成测试数据生成器（`cmd/testdatagen`，字节级确定）+ 预期计数固化（`testdata/expected-summary.json`）
- 96 个 Go 测试（13 包）全绿：行级记账、幂等、原子性、确定性、来源证据、拓扑、API 形状回归、CLI 子进程 E2E、MCP stdio 回环、工具面安全断言
- `scripts/demo.sh` 一键演示（全新库 → 导入×3 → 校验 → 报告 → 统计）
- 文档套件：README / architecture / data-model / api / import-and-validation / teleagent-integration / verified-capabilities / ADR 0001–0006
- GitHub Actions CI（go + web 双 job）

### 已知限制（诚实声明）
- 部署态 TeleAgent MCP 调用未验证（进程内回环已验证）；Windows/macOS 未构建（linux/amd64）；真实浏览器渲染待终审；详见 docs/verified-capabilities.md。

## 终审补充（2026-10-09）

- ci：GitHub Actions 三次迭代后全绿（go: fmt/vet/96 测试含前端构建；web: tsc/vite）——run#1 暴露 go job 缺前端构建、run#2 暴露 npm 缓存路径，均已修复

- fix(api)：所有 `/api/*` 响应注入 `Cache-Control: no-store`，杜绝浏览器启发式缓存导致的"幽灵统计"（ADR 0006，含回归测试）
- 终审完成真实浏览器 UI 全流程冒烟（Chrome 155 / agent-browser 隔离会话），截图证据随交付包提供
