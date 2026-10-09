# NetOps Lab — 云网资源治理工作台（Stage 0 + Phase 1）

```
                    ┌──────────────────────────────────────────────┐
   Excel 工作簿      │                netopslab 单二进制              │
 (devices/ippools/  │                                              │
  circuits .xlsx)   │  importer ──► SQLite（modernc.org/sqlite，    │
        │           │  列映射确认     纯 Go 无 CGO，事务 + 行级记账）   │
        ▼           │      │                                       │
   analyze/导入      │      ▼                                       │
                    │  validation（规则 E/W/I，确定性、可重算）        │
                    │      │                                       │
                    │      ▼                                       │
                    │  ┌── API（net/http stdlib）── 嵌入式 React UI    │
                    │  ├── CLI（结构化 JSON 输出）                     │
                    │  └── MCP（stdio，仅 1 个只读工具 resource_search）│
                    │      │                                       │
                    │      ▼                                       │
                    │  query/topology/report（检索·二跳关系·质量报告）  │
                    └──────────────────────────────────────────────┘
```

> ⚠️ **合规声明**：本平台**不具备生产设备写能力**。AI 仅可操作本地实验室工作区；生产操作必须由人员通过公司批准的外部工具执行。仓库内所有数据均为**合成测试数据**，仅用于工程方法验证，不代表真实网络运行情况。
>
> ⚠️ **Compliance**: This platform has **NO production-device write capability**. AI may only operate the local lab workspace; production operations must be performed by humans through company-approved external tooling. All data in this repository is **synthetic** and used solely for engineering-method validation.

## 1. 项目定位

NetOps Lab 是一个本地优先（local-first）的云网资源治理工作台：把多份 Excel 资源台账（设备 / 地址池 / 电路）经**用户确认的列映射**导入 SQLite，完成行级记账的标准化与确定性质量校验，提供资源检索、一跳/二跳关系查询、拓扑可达性检查与质量报告，并通过 HTTP API、嵌入式 React 界面、CLI 与 **MCP 单工具（只读 `resource_search`）** 四个出口对外服务。它是后续虚拟仿真能力的工程基座（Phase 2），本仓库交付 **Stage 0（技术验证）+ Phase 1（资源治理）**。

## 2. 当前阶段能力清单（Stage 0 + Phase 1 已交付）

- **Excel 导入管线**：多工作表解析、列映射建议（确定性建议引擎，置信度 1.0/0.9/0.75）、用户确认后执行；导入全程单事务，任何内部错误整体回滚（零半写）。
- **行级记账**：每一数据行必有 `imported / skipped / failed` 之一；跳过即幂等（重复导入 0 新增），失败行携带规则码与消息。
- **数据质量校验**：10 条版本化规则（E001–E006 / W001–W003 / I001），行级规则随导入触发，集合级规则（网段重叠、地址重复、孤岛设备）导入后自动重算且幂等；每条问题携带来源证据（工作簿/Sheet/行号）或显式 `applicationGenerated` 标记。
- **资源检索与统计**：类型/状态/关键词过滤、分页、资源详情（原始行回溯）、全库统计。
- **关系查询**：assigned_to / a_end / z_end 三类关系，1 跳与 2 跳 BFS 子图（节点 + 边）。
- **拓扑可达性检查**（Stage 0）：按设备编码的两端连通性判定 + 路径（如 DEV-001→DEV-003 路径长 2）。
- **质量报告**：JSON 与独立 HTML 两版（评分 = 100 − 5×错误 − 1×警告，下限 0），支持全局与单批次两个口径。
- **批次管理**：批次列表/详情/行结果查询；已完成批次可回滚（被其他批次引用时报 409 `batch_in_use`）。
- **HTTP API**：`/api/...` 全套 REST 风格接口（错误统一 `{"error":{"code","message"}}` 信封）。
- **嵌入式 React 前端**：概览 / 数据导入（三步向导）/ 资源浏览（抽屉 + 二跳）/ 问题清单 / 质量报告 五页中文界面，`go:embed` 打进单二进制。
- **MCP 服务器**（Stage 0）：stdio JSON-RPC，**仅注册一个只读工具 `resource_search`**；无 SQL/Shell/文件类工具。
- **结构化 CLI**：`analyze / import / validate / report / query / serve / mcp / testdata`，成功输出单一 JSON 对象，退出码 0/1/2（TeleAgent 的 CLI 回退路径）。
- **工程化**：96 个 Go 测试（13 个含测试的包）全绿；gofmt/vet 干净；确定性导入/校验；可重复的一键演示脚本。

## 3. 快速开始

前置要求：**Go 1.27+**（编译后端；无 CGO，纯 Go）、**Node 18+**（构建前端；本仓库验证于 Node 24 / npm 11）、python3（演示脚本用）。

```bash
# 1) 测试
make test            # go test -count=1 -timeout 15m ./...（96 个测试 / 13 个包 ok）

# 2) 构建前端（产物嵌入 internal/webui/dist，供 go:embed 使用）
make web             # cd web && npm ci && npm run build

# 3) 构建单二进制（约 27MB，含嵌入式前端）
make build           # bin/netopslab

# 4) 一键演示（全新临时库：导入×3 → 校验 → 报告 → 统计，< 1 秒）
./scripts/demo.sh    # 或 make demo

# 5) 启动服务并打开界面
./bin/netopslab serve --addr :8080 --db ./data/netops.db
#    浏览器打开 http://localhost:8080
```

说明：`make web` 会把 `web/` 构建到 `internal/webui/dist`（该目录不提交，仅保留 `.gitkeep` 占位）；`make build` 嵌入的是**当时 dist 里的内容**——先 `make web` 再 `make build` 才会得到带真实前端的二进制。`make all` = `web + build`。

## 4. 10 分钟演示路径（浏览器操作）

前提：`make web && make build`，然后 `./bin/netopslab serve`（默认 `:8080`，数据库 `./data/netops.db`），浏览器打开 http://localhost:8080。

1. **数据导入（/import）** — 依次上传 `testdata/devices.xlsx`、`testdata/ippools.xlsx`、`testdata/circuits.xlsx`（顺序即依赖顺序：设备 → 地址池 → 电路）。
   - 第 ① 步上传工作簿（.xlsx，≤10MB）；
   - 第 ② 步确认列映射：系统已按表头建议预填（含置信度徽章、必填字段标记），直接“确认导入”；
   - 第 ③ 步查看执行结果：27/0/3、16/0/4、21/0/4（总/成功/跳过/失败），失败行可按状态过滤并查看规则码、消息与来源行号。
2. **问题清单（/issues）** — 按严重级别/规则码/批次过滤；每条问题显示 severity 徽章、证据来源（工作簿/Sheet/行号）或“系统生成”标记。集合级规则示例：E005 网段重叠（POOL-15/POOL-16）、E006 地址重复（10.10.10.5）、I001 孤岛设备（DEV-030）。
3. **质量报告（/report）** — 选择批次或全局口径，页面内嵌 HTML 报告预览，可下载 HTML / JSON；顶部有显著的合成数据免责声明横幅；全局口径评分 32（E13/W3/I1），设备单批次口径评分 74。
4. **资源浏览（/resources）** — 类型/状态过滤 + 关键词搜索（如 `DEV-001`）；点击行打开右侧抽屉：属性 JSON、来源引用（工作簿/Sheet/行号 + 原始行）、关系页签（1 跳列表，“查看二跳”展开 2 跳子图）。
5. **概览（/）** — 资源/问题/评分统计卡片、最近批次表。
6. **回滚演示（可选）** — 重复上传同一工作簿可见“跳过”幂等行为；在批次管理中回滚 circuits 批次后统计中电路清零，尝试回滚 devices 批次将得到 409（被地址池/电路引用）。

无浏览器环境时，等价演示：`./scripts/demo.sh`（见上）。

## 5. 技术栈

| 层 | 技术 | 说明 |
| --- | --- | --- |
| 后端 | Go 1.27（无 CGO） | 仅 `net/http` stdlib 路由；无 Web 框架 |
| 存储 | SQLite（modernc.org/sqlite v1.60.1） | 纯 Go 驱动；WAL + busy_timeout + foreign_keys |
| Excel | xuri/excelize/v2 v2.11.0 | 多 Sheet 解析、类型化单元格读取 |
| MCP | modelcontextprotocol/go-sdk v1.8.0 | stdio JSON-RPC；单只读工具 |
| 前端 | React 18 + TypeScript 5.6（strict）+ Vite 6 | 手写设计系统 CSS，无 UI 框架/图表库 |
| 嵌入 | go:embed all:dist | 前端产物打进二进制，单文件分发 |
| 测试 | go test（table-driven） | 96 个测试 / 13 包；含 CLI 子进程 E2E 与 MCP stdio 回环 |

## 6. 仓库结构

```
netops-lab/
├── cmd/netopslab/        # 单二进制入口：serve|analyze|import|validate|report|query|mcp|testdata
├── cmd/testdatagen/      # 生成 3 本合成工作簿（确定性）
├── internal/
│   ├── domain/           # 实体、枚举、错误、规则码（零依赖）
│   ├── repo/             # SQLite：Open/迁移 + 各聚合仓储
│   ├── workbook/         # excelize 解析：Sheet/表头/行/空行
│   ├── mapping/          # 目标 schema、映射模型、确定性建议引擎
│   ├── importer/         # 分析/执行/回滚；行级记账；幂等
│   ├── validation/       # 集合级规则引擎（E005/E006/I001）
│   ├── topology/         # 关系图 + BFS 可达性
│   ├── query/            # 检索/统计/关系子图/拓扑检查
│   ├── report/           # 质量报告 JSON + HTML（html/template）
│   ├── mcpserver/        # MCP stdio 服务器（仅 resource_search）
│   ├── api/              # HTTP 处理器、错误信封、中间件
│   ├── app/              # 组装（配置/日志/DB/服务）+ CLI 实现
│   └── webui/            # go:embed 前端产物（dist/.gitkeep 占位）
├── web/                  # React 源码（npm；构建产物出 internal/webui/dist）
├── testdata/             # 3 本合成工作簿 + expected-summary.json
├── scripts/demo.sh       # 一键演示
├── docs/                 # 文档（见下方索引）
└── Makefile
```

## 7. 数据质量规则目录（rule_version 1.0.0）

| 规则码 | 名称（中文） | 严重级别 | 阶段 | 触发条件 |
| --- | --- | --- | --- | --- |
| E001 | 重复键 | 错误 | 行级 | 自然键与本批次其他行或库中既有资源重复（重复导入 → 行状态 `skipped`） |
| E002 | 必填字段缺失 | 错误 | 行级 | 必填字段为空或未映射 |
| E003 | 引用无效 | 错误 | 行级 | 引用字段非空但目标资源不存在（批内 + 全库检查） |
| E004 | 格式无效 | 错误 | 行级 | ipv4/cidr/int/enum 解析失败或超界（如 VLAN 1–4094） |
| E005 | 网段重叠 | 错误 | 集合级 | 两个地址池 CIDR 重叠（证据含重叠网段） |
| E006 | 地址重复使用 | 错误 | 集合级 | 同一 mgmt_ip / gateway 被 >1 个资源使用 |
| W001 | 孤儿记录 | 警告 | 行级 | 可选关系字段留空（assigned_device） |
| W002 | 状态值未知 | 警告 | 行级 | status 非空但不在枚举内（行仍导入，状态丢弃） |
| W003 | 电路自环 | 警告 | 行级 | a_end == z_end（两端相同） |
| I001 | 孤岛设备 | 提示 | 集合级 | 设备零关系（未关联任何地址池/电路） |

行级 E 规则 → 该行 `failed`（不落库）；W/I 规则 → 行仍导入并记录问题。评分公式：`100 − 5×错误数 − 1×警告数`（下限 0，按报告口径）。详见 [docs/import-and-validation.md](docs/import-and-validation.md)。

## 8. CLI 与 MCP

**CLI**（所有成功输出 = stdout 单一 JSON 对象；退出码 0 成功 / 1 失败 / 2 用法错误）：

```bash
netopslab analyze  --file devices.xlsx                      # 离线解析 + 映射建议（不写库）
netopslab import   --file devices.xlsx --map map.json --db ./data/netops.db
netopslab validate --db ./data/netops.db [--batch <id>]
netopslab report   --db ./data/netops.db --format html --out report.html
netopslab query    --db ./data/netops.db resources|stats|relations …
netopslab serve    --addr :8080 --db ./data/netops.db
netopslab mcp      [--db ./data/netops.db]                  # 或环境变量 NETOPS_DB
netopslab testdata --out testdata
```

**MCP**：`netopslab mcp` 启动 stdio JSON-RPC 服务器（server name `netops-lab`，version `v0.1.0`），**只注册一个只读工具 `resource_search`**（输入 `q/type/limit/offset`，输出 `total + items[]`，每项含来源 workbook/sheet/rowNumber）。

TeleAgent 集成：MCP 注册方式与 CLI 回退路径（以结构化 JSON 供 Skills 解析）见 **[docs/teleagent-integration.md](docs/teleagent-integration.md)** —— 注意：**部署态 TeleAgent 的 MCP 调用未在本开发环境验证**，接入时请按该文档操作并反馈。

## 9. 免责声明（再次强调）

- 本项目**没有任何生产网络写能力**，MCP/HTTP API 均不暴露 SQL、Shell、SSH 或通用文件访问；
- 仓库内全部资源数据为**合成测试数据**（`testdata/`，可由 `netopslab testdata` 确定性再生成），**不代表真实网络运行情况**；
- 质量报告页脚固定携带免责声明：“本报告基于合成测试数据生成，仅用于工程方法验证，不代表真实网络运行情况。”
- 能力边界与已验证/未验证事项的诚实清单见 [docs/verified-capabilities.md](docs/verified-capabilities.md)。

## 10. English Summary

NetOps Lab is a local-first cloud-network resource governance workbench delivering Stage 0 + Phase 1. It imports three synthetic Excel workbook types (devices, IP pools, circuits) through a user-confirmed column mapping into SQLite (pure Go, no CGO), with single-transaction atomicity, row-level accounting (imported/skipped/failed), idempotent re-import, and batch rollback. A deterministic, versioned validation engine applies 10 quality rules (E001–E006, W001–W003, I001) with source evidence (workbook/sheet/row) or an explicit application-generated label. Read-side services provide resource search, statistics, 1/2-hop relation queries, topology reachability, and JSON/HTML quality reports. Exits: stdlib HTTP API, embedded React 18 UI (go:embed single binary), structured-JSON CLI (exit 0/1/2), and an MCP stdio server exposing exactly one read-only tool, resource_search. Verified: 96 Go tests green, full-workflow smoke, CLI e2e, in-process MCP round-trip, determinism, atomicity, idempotency, rollback 409. Unverified: deployed-TeleAgent MCP invocation, Windows/macOS builds (linux/amd64 only), real-browser rendering. No production write capability anywhere; all data is synthetic.

## 文档索引

| 文档 | 内容 |
| --- | --- |
| [docs/architecture.md](docs/architecture.md) | 架构分层、模块边界、事务与确定性策略 |
| [docs/data-model.md](docs/data-model.md) | DDL v1、实体说明、ID/时间戳约定、迁移机制 |
| [docs/api.md](docs/api.md) | HTTP API 参考（含真实请求/响应示例） |
| [docs/import-and-validation.md](docs/import-and-validation.md) | 导入管线、行级记账、规则目录、评分、幂等与回滚 |
| [docs/teleagent-integration.md](docs/teleagent-integration.md) | TeleAgent 的 MCP 注册与 CLI 回退 |
| [docs/verified-capabilities.md](docs/verified-capabilities.md) | 已验证 / 未验证能力清单（诚实性文档） |
| [docs/adr/](docs/adr/) | 架构决策记录（ADR 0001–0006） |
| [CHANGELOG.md](CHANGELOG.md) | 版本变更记录 |
