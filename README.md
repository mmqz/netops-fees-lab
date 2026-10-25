# NetOps Fees Lab — 站点费用风险分析系统（费用域 fork）

> **本仓库是 [mmqz/netops-lab](https://github.com/mmqz/netops-lab) 的费用域 fork**（fork 时点：v0.16.1 / 2223101）。
> 拆分决策与裁剪清单见 [docs/fees-repo-split-plan.md](docs/fees-repo-split-plan.md)（决策）、
> [docs/fees-split-review.md](docs/fees-split-review.md)（评审，A–E 五项修订）与
> [docs/fees-code-removal-plan.md](docs/fees-code-removal-plan.md)（主仓侧三条红线）。
> IPRAN 网络域（仿真/拓扑/告警/检测/治理）留在主仓演进；本仓只为**费用对账与风险发现**而存在。

```
                    ┌──────────────────────────────────────────────┐
   费用台账          │              netopsfees 单二进制               │
 (电费/铁塔租费/     │                                              │
  修理费/综维 .xlsx  │  fees 快照层（内容寻址 · 不可变证据 · 幂等键）    │
        │           │      │                                       │
        ▼           │      ▼                                       │
   费用导入          │  Parquet 分区表（DuckDB 分析引擎 · 可选构建）    │
   资源台账 ────────►  资源宇宙（SQLite · 导入+校验+审计底座）         │
   指标台账 ────────►  指标时序（SQLite · 双账本交叉的另一半）          │
                    │      │                                       │
                    │      ▼                                       │
                    │  8 个风险场景（线索 + 行级证据锚点 + 人工裁定）    │
                    │      │                                       │
                    │  ┌── API（86 → 40 条费用面路由 · OpenAPI 同源）─┐│
                    │  ├── 嵌入式 React UI（/fees 费用工作台）        ││
                    │  ├── CLI（fees/metrics/import/backup …）        ││
                    │  └── MCP（4 工具：resource_search + 费用三件）    ││
                    └──────────────────────────────────────────────┘┘
```

> ⚠️ **合规声明**：本平台**不具备生产设备写能力**，Agent 不直连 SQL/Shell。**账单只能证明存在，不能定罪**——
> 全部风险场景输出为「线索 + 证据锚点」三级证据结构，结论由人工经 HTTP/CLI 裁定（verify 留档 / dismiss 永不再现）。
> 仓库内数据为工程验证用样例；`samples/` 含项目所有者授权的电信运营导出样例，仅限本仓库内验证、不得外发（见
> [samples/README.md](samples/README.md)）。

## 1. 项目定位

NetOps Fees Lab 是一个本地优先（local-first）的**站点费用风险分析系统**：把费用台账（电费 / 铁塔租费 / 修理费 / 综维费用，单 sheet 或多 sheet 整本）经**快照 + 幂等键**导入 DuckDB Parquet 分区表，把资源台账与指标台账导入 SQLite 构成关联宇宙；再以 **8 个风险场景**（退用仍在账 / 跨批次重复结算 / 台账金额差异 / 环比突增 / 离群站址 / 未关联 / 有费无量 / 有量无费）对账出**带行级证据锚点的风险线索**，经**分级关联**（精确/规则自动 + 模糊人工）挂到资源宇宙，最终由人（HTTP/CLI）或 AI（MCP 只读面）消费与裁定。

## 2. 能力清单（fork 自 v0.16.1 的费用线全量）

- **费用导入**：单 sheet（`fees import`）与多 sheet 台账整本（`fees import-sheets`，#125——每 sheet 按表名/表头自判类型、独立批次独立证据链）；快照内容寻址（#88），回滚删分析行、保快照证据（#107 单提交点）；uploads GC；.xls 旧格式诚实拒绝。
- **8 个风险场景 + 三级证据**：线索 → 站址汇总/明细 → 快照行原文回读（`GET /api/fees/batches/{id}/evidence`，#86）；`z-score`/环比阈值全部显式参数化；空结果诚实空（#83）。
- **人工裁定闭环**（#119）：verify 属实留档为「已知问题台账」/ dismiss 排除且**永不再现**（dismissed 计数入信封）；可复议翻转；MCP 无写面。
- **分级关联**（#63/#58-S3）：`link-scan` 精确/规则自动确认 + 模糊出提案，人工 confirm/dismiss；关联列表含资源侧 join。
- **双账本交叉**：指标摄入独立通道（`metrics ingest`）+ 资源宇宙导入；`idle-billing`（有费无量，#114）与 `missing-billing`（有量无费，#118）跨账本互证。
- **AI 消费面（MCP 4 工具）**：`resource_search`（宇宙检索）+ `fee_risks` + `fee_totals` + `fee_duplicates`——闭集只读，TestMCPToolRegistry 钉死。
- **HTTP 40 条路由**：system 3 / imports 9 / resources 2 / metrics 8 / fees 14 / 治理与校验 4；OpenAPI 文档与路由表同源生成（零漂移）。
- **React 前端**：/fees 费用工作台（风险面板 + 阈值化 + 分页明细 + 批次管理 + 证据下钻 + 关联确认）、概览 / 导入 / 资源 / 问题 / 报告 / API 参考。
- **底座**：SQLite（modernc.org/sqlite 纯 Go 无 CGO，WAL，事务 + 行级记账）+ 迁移链 v1–v34 **完整保留**（fork 红线 3）；确定性校验规则 E/W/I；HTML/JSON 质量报告；备份与 uploads GC。

## 3. 快速开始

前置要求：**Go 1.27+**（后端）、**Node 18+**（前端；验证于 Node 24 / npm 11）。测试全部本地执行（本仓库不配置 CI）。

```bash
# 1) 后端测试（16 包全绿；std 构建不含 DuckDB 引擎）
make test            # go test -count=1 -timeout 15m ./...

# 2) 前端构建（产物嵌入 internal/webui/dist）
make web             # cd web && npm ci && npm run build

# 3) 单二进制（默认构建：CGO-free，费用查询答 501 engine_not_built）
make build           # bin/netopsfees

# 3') DuckDB 构建可选（Linux/WSL2/macOS：CGO_ENABLED=1 -tags duckdb，含分析引擎）
make build-duckdb    # bin/netopsfees-duckdb

# 4) 一键演示：资源宇宙（全新临时库：导入×3 → 校验 → 报告 → 统计）
./scripts/demo.sh    # 或 make demo

# 5) 费用闭环（示例）
./bin/netopsfees-duckdb fees --db ./data/netops.db --data ./data \
     import --file 台账.xlsx --type electricity --year 2024 --month 5
./bin/netopsfees-duckdb fees --db ./data/netops.db --data ./data \
     risks spike --type electricity --year 2024

# 6) 启动服务并打开界面
./bin/netopsfees serve --addr :8080 --db ./data/netops.db --data ./data
#    浏览器打开 http://localhost:8080（费用工作台入口：/fees）
#    可选写保护：--auth-token <T>（所有 POST/PATCH/DELETE 需 Bearer）
```

Docker（含 DuckDB 引擎）：`docker build -t netops-fees-lab:local .`

## 4. 技术栈

Go 1.27 · SQLite（modernc.org/sqlite，纯 Go）· DuckDB（go-duckdb，CGO，分析层可选构建）· Parquet（parquet-go）· React 19 + Vite · go:embed 单二进制 · MCP（modelcontextprotocol go-sdk）。无框架、无 ORM，标准库优先。

## 5. 仓库结构

```
cmd/netopsfees/        单二进制入口（serve/fees/metrics/import/…）
internal/fees/         费用核心：快照/Parquet/8 场景/分级关联/裁定（27 文件）
internal/metrics/      指标时序：摄入/查询/趋势预测/异常点（双账本另一半）
internal/repo/         SQLite 存储层 + 迁移链 v1–v34（红线 3：完整保留）
internal/importer/     资源宇宙导入管道（列映射确认 + 行级记账）
internal/api/          HTTP 面（40 路由 · OpenAPI 同源）
internal/app/          CLI 骨架（fees/metrics/serve/gc/backup/…）
internal/mcpserver/    MCP 面（4 工具闭集）
internal/{domain,mapping,workbook,validation,query,report,services,webui,testsupport,testfixtures}/
web/src/               React SPA（/fees 工作台 + 概览/导入/资源/问题/报告）
docs/                  契约与决策文档（api.md · fees-layer.md · facts.md · 拆分三文档）
samples/               电信运营导出样例（授权范围见 samples/README.md）
```

## 6. 风险场景目录（8 个，#64/#114/#118）

| 场景 | code | 判定口径 |
| --- | --- | --- |
| 退用仍在账 | `retired-billing` | 关联资源状态 ∈ 退用/停用集合（可参数化）而费用仍在 |
| 跨批次重复结算 | `duplicate-settlement` | 同（站址, 账期）跨批次成对出现 |
| 台账金额差异 | `ledger-mismatch` | 各批次合计差额 ≥ 阈值（默认 0 分） |
| 环比突增 | `spike` | 站址月度合计环比涨幅 ≥ 阈值（默认 30%） |
| 离群站址 | `outlier` | 同期同类站 |z| ≥ 3.0（最小样本 8） |
| 未关联费用 | `unlinked` | 站址编码未挂上资源宇宙 |
| 有费无量 | `idle-billing` | 费用在账而指标点数为 0（指标摄入通道） |
| 有量无费 | `missing-billing` | 指标有量而账期为空（指标摄入通道） |

## 7. CLI 与 MCP

- **CLI**（`netopsfees <子命令>`，stdout 单 JSON 契约）：`serve` / `analyze` / `import` / `ingest` / `metrics` / `validate` / `report` / `fees`（import · import-sheets · list · rollback · evidence · query · link-scan · links · risks · clue-decisions）/ `templates` / `mcp` / `gc` / `backup` / `testdata`。
- **MCP**（`netopsfees mcp`，stdio JSON-RPC）：`resource_search` / `fee_risks` / `fee_totals` / `fee_duplicates` —— 全部只读，闭集由 TestMCPToolRegistry + facts.md 门禁双向钉死。

## 8. 常见问题

**Q：新克隆后直接 `make test`，为什么有 3 个包报 404 失败？**

`internal/webui/dist` 里只有 `.gitkeep`（构建产物不入库），Go 二进制嵌入的是空目录，
SPA 路由 `GET /` 返回 404，三个 SPA 冒烟测试（`TestAuthTokenWriteGate` / `TestAPIHealthAndSPA` /
`TestHandlerRealDistSmoke`）因此失败。**这不是代码缺陷，是构建顺序问题。**
自 issue #13 起，`make test` 已带前置检查：dist 未构建时直接给出明确提示并退出，
不再跑出费解的 404 红灯。执行 `make web`（需要 Node 18+）后重跑即可 16 包全绿。

**Q：`make build` 出的二进制打开页面 404？**

同因：`make build` 只编译 Go 二进制，嵌入的是当时的 `internal/webui/dist`。
要带真实前端请用 `make all`（= 先 `web` 后 `build`）。

## 9. 免责声明

本系统输出的是**风险线索**而非定论：账单数据只能证明费用存在（「账单只能证明存在，不能定罪」），一切裁定权在人工通道。分析结果仅供对账核查使用。

## 文档索引

- [docs/api.md](docs/api.md) — HTTP 契约基线（40 路由双向钉死）
- [docs/fees-layer.md](docs/fees-layer.md) — 费用分析层设计（快照/Parquet/场景/证据链）
- [docs/facts.md](docs/facts.md) — 机读事实源（路由/工具/迁移计数门禁）
- [docs/fees-repo-split-plan.md](docs/fees-repo-split-plan.md) / [docs/fees-split-review.md](docs/fees-split-review.md) — 拆分决策与评审
- [docs/import-and-validation.md](docs/import-and-validation.md) — 导入与校验契约
- [docs/issues/](docs/issues/) — 历史问题登记（继承自主仓 v0.16.1 及之前）
