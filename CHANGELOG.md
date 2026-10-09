# Changelog

本项目所有显著变更记录于此。格式参考 Keep a Changelog；版本遵循语义化版本（SemVer）。

## [0.2.0] — 2026-10-09

**Phase 2（仿真工作台）全量交付**：在 Phase 1 资源治理之上，把导入的合成台账一键物化为虚拟拓扑，让人与 AI 在**封闭的类型化操作集**上做可审计、可撤销的仿真变更（含故障注入）。169 个 Go 测试全绿；演示：`./scripts/demo-lab.sh`。

### 新增 — 仿真工作台
- **一键物化**：从导入批次生成虚拟拓扑（设备→虚拟设备、电路→链路、确定性 `eth0/eth1…` 端口分配、自环电路跳过）；演示规模 27 设备 / 40 接口 / 20 链路，状态哈希 `aea27501…`；同批次二次物化结构一致（id 全新）
- **类型化操作引擎**（`internal/sim`）：10 个前向操作（增删设备/接口/链路、启停、配置/清除 IPv4）+ 3 个引擎内部 `restore_*` 逆操作；严格字段校验（未知码/多余字段/坏类型 ⇒ 400）；单事务变更集（op_json + inverse_json 逐操作留档）；**失败零污染**：语义失败整体回滚 + 第二事务记录失败变更集（`ops_failed=0`），HTTP 422 `op_failed` 消息含失败变更集 id
- **确定性校验目录**（rule_version 1.0.0）：CHK-001 重复主机地址 / CHK-002 网段重叠（identical·contains·partial）/ CHK-003 孤岛设备 / CHK-004 部分分区 / CHK-005 未配置接口；发现行 + 恒定基线行（运行自描述）；评分 100−5×错误−1×警告（下限 0）；运行携带规范拓扑快照
- **连通性与前后对比**：up-only BFS 路径查询（邻居编码升序，路径确定）；两次运行的 diff——问题按业务键匹配（id 漂移免疫）、连通性丢失/恢复对、评分差

### 新增 — 故障注入与三层撤销
- **故障注入**：`set_link_status down` / `set_device_status down` / 删除类操作即可注入故障；连通性即时反映（演示：关断 ln-1 ⇒ DEV-001→DEV-003 不可达、连通性丢失 20 对、CHK-003/CHK-004 发现）
- **三层撤销**：
  1. **事务回滚**——失败变更集零状态污染（留档可审计）；
  2. **变更集逆向 LIFO undo**——逆操作按序倒序作为新变更集（trigger `undo`），撤销可再撤销；仅最新已应用前向变更集可撤销（409 `not_latest`）；
  3. **检查点**——SQLite **Online Backup API** 全库快照（`Step(-1)`+`Finish`，经 `(*sql.Conn).Raw` 匿名接口断言，**非裸拷贝**）；**工作区级恢复 = 前向操作变更集**（删除-重建计划、业务键匹配、id 预测），统一审计、可再撤销；状态哈希相等 ⇒ no-op（`changesetId:null`）

### 新增 — 出口层
- **HTTP API §7**（17 条路由）：workspaces CRUD/列表/详情、topology、path、ops、checks run/list/detail、diff、changesets、undo、checkpoints create/list/restore、lab report（HTML/JSON）；英文错误信封（`invalid_request`/`not_found`/`conflict`/`not_latest`/`checkpoint_missing_file`/`no_source_resources`/`op_failed`）
- **MCP 五工具**（SEC-P2）：`resource_search`（P1 不变）+ `lab_apply_ops` / `lab_run_checks` / `lab_diff_undo`（mode 判别）/ `lab_checkpoint`（action 判别）；AI 改仿真态的唯一通道，**无建删工作区工具**；`restore_*` 三边界拒绝（schema + 引擎双重）
- **CLI `lab` 子命令树**：`workspace create / topology / path / apply / checks / diff / undo / checkpoint`；单一 JSON 输出、退出码 0/1/2
- **仿真工作台报告**：`GET /api/lab/report`（HTML 同 P1 视觉风格 + JSON：引擎版本/评分/发现/变更集史/检查点/免责声明）

### 新增 — 前端（/lab）
- 新导航组「仿真工作台」：工作台列表页（新建对话框：批次多选，默认全部已完成）+ 拓扑工作台页（`/lab/:id`）
- **Cytoscape 3.32.0 拓扑画布**（精确版本、无插件）：节点 up/down 分色、链路实线/虚线、力导/网格/环形布局切换、点击联动右侧表格；增量元素同步保留视角
- 右侧六标签页：设备 / 接口（配置地址/清除）/ 链路 / 检查结果（发现+基线+运行历史）/ 变更集（LIFO 撤销按钮）/ 检查点（创建/恢复确认）；页头六动作含前后对比对话框与导出报告
- 操作对话框：11 个中文操作表单（拓扑预填下拉 + 客户端校验）+ 原始 JSON 模式
- 构建产物：js 717.63 kB（gzip 226.73）/ css 22.22 kB（cytoscape 约 +500 kB，单 chunk 嵌入决策见 ADR-0009）

### 新增 — 工程化
- DDL v5（workspaces / 全局 sequences / virtual_devices / interfaces / links / changesets+ops / check_runs+results / checkpoints）+ 8 个仿真仓储 + Online Backup/只读打开（`repo.Backup`/`OpenReadOnly`）
- 169 个 Go 测试（16 包 ok，14 含测试；P1 96 + 73 新增）：ACC-P2-1..8 / SEC-P2 / DET-P2 / E2E-P2 / API-P2 / PERF-P2 全覆盖；物化/操作/校验/undo/检查点均含双库确定性比对
- `scripts/demo-lab.sh`（可重复故障注入演示，双跑输出一致）+ Makefile `demo-lab` 目标
- 文档：`docs/simulation.md`、ADR 0007–0009；更新 README / api（真实捕获示例）/ data-model（DDL v5）/ architecture / teleagent-integration（5 工具 + jsonschema 教训附录）/ verified-capabilities

### 已知限制（诚实声明）
- **单工作区活跃模式**：schema 支持多工作区共存，但多工作区**并发**写不受支持（检查点恢复的 id 预测依赖单写者）；
- 部署态 TeleAgent MCP 调用仍未验证（进程内回环 + 真进程 stdio 冒烟已验证）；真实浏览器 /lab 冒烟待终审；Windows/macOS 未构建（linux/amd64）；详见 docs/verified-capabilities.md。

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
