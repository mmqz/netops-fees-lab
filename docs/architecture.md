# NetOps Lab — 架构说明（Architecture）

> 本文描述 Stage 0 + Phase 1（v0.1.0）的实际实现。源码是唯一事实来源：分层、包边界、事务与确定性策略均可在 `internal/` 中逐一对应。中文为主，关键术语保留英文。

## 1. 总体分层

```
cmd/netopslab · cmd/testdatagen        ← 入口（组装参数 → app/CLI）
        │
        ▼
internal/app                           ← 组装层：Config、slog、repo.Open、服务实例、HTTP 生命周期
        │
        ▼
internal/api  ─ internal/mcpserver     ← 出口层（exit）：HTTP API、MCP stdio
（出口层调用服务层，绝不直接写 SQL）
        │
        ▼
internal/importer · internal/validation · internal/query · internal/report · internal/topology
        │        服务层（全部业务规则在这里；只依赖 repo 接口）
        ▼
internal/repo                          ← 存储层：SQLite（modernc.org/sqlite，纯 Go 无 CGO）
        │                                  迁移 + 各聚合仓储（Batches/Resources/Relations/
        ▼                                  SourceRefs/RowResults/Issues）
internal/domain                        ← 领域层：实体/枚举/错误/规则码（零内部依赖，被所有人引用）

internal/workbook · internal/mapping   ← 解析与映射（被 importer 使用；workbook 只管 Excel，
                                          mapping 只管目标 schema 与建议引擎）
internal/webui                         ← go:embed 前端产物（被 api 的 SPA 路由挂载）
web/                                   ← React 源码（构建产物输出到 internal/webui/dist）
```

依赖方向**自上而下、单向**：

- `domain` 不 import 任何内部包（规则码、严重级别、资源/关系类型、领域错误全部在这里，见 `internal/domain/rules.go` 的 `SeverityForCode`）；
- `repo` 只被服务层和出口层的**读路径**使用，服务层写路径一律通过仓储接口；
- 服务层之间不互相 import（`query` 通过 `topology` 组合图查询；`importer` 与 `validation` 互不引用，由 `app` / `api` / CLI 负责先后编排：导入成功后自动触发一次 `validation.Run`）。

## 2. 各模块职责

| 包 | 职责 | 关键文件 |
| --- | --- | --- |
| `domain` | 实体（Resource/Relation/Issue/Batch/SourceRef/RowResult）、枚举、sentinel 错误、规则码→严重级别、ID 生成（`res_`/`rel_`/`src_`/`bat_`/`iss_` 前缀 + UUID） | `entities.go` `enums.go` `rules.go` |
| `repo` | `Open(path)` 应用迁移（v1–v4）；DSN 携带 `busy_timeout(5000)` `journal_mode(WAL)` `foreign_keys(1)`；`DB.Do(ctx, fn)` 事务组合子；六个聚合仓储 | `repo.go` `migrations.go` `migrations_v4.go` |
| `workbook` | excelize 解析：Sheet 列表、表头行、数据行（空行跳过并计数）、类型化单元格读取；`ErrNotXLSX`/`ErrEmptyWorkbook` | `workbook.go` |
| `mapping` | 目标 schema（字段类型 string/ipv4/cidr/int/enum/ref + 取值范围）、映射模型与校验（`*FieldError` 定位 sheet/field）、确定性建议引擎（置信度阶梯 1.0/0.9/0.75/0） | `schema.go` `mapping.go` `suggest.go` |
| `importer` | 分析（建批次、写 workbook_path、产出 Analysis）、执行（单事务导入所有 import sheet、行级规则、行级记账、幂等 skip）、回滚（completed-only + 409 in-use） | `analyze.go` `execute.go` `rollback.go` |
| `validation` | 集合级规则引擎：E005 网段重叠 / E006 地址重复 / I001 孤岛设备；单事务"删旧插新"，幂等可重算 | `engine.go` |
| `topology` | 关系图构建（节点按 `(type, natural_key)` 排序）；全资源图 BFS 邻域（深度 1/2，无向）；设备级拓扑可达性（电路视为设备间链路） | `topology.go` |
| `query` | 检索/统计/详情（含 source_refs 解析与 relationCount）/关系子图/拓扑检查——只读服务 | `query.go` |
| `report` | 质量报告：`Report` 结构（camelCase JSON）、来源解析（resolve.go：source_ref_id → workbook/sheet/row；行级失败行 → 批次+行号；否则 applicationGenerated）、独立 HTML（`html/template` + `//go:embed template.html`，全量自动转义） | `report.go` `resolve.go` `html.go` |
| `mcpserver` | MCP stdio 服务器（go-sdk v1.8.0 IOTransport）；**只注册 `resource_search` 一个只读工具** | `mcpserver.go` |
| `api` | net/http stdlib 路由（Go 1.27 method patterns）、统一 JSON 信封与错误映射（400/404/409/413/422/500）、panic 恢复 + slog 请求日志、10MB 上传限制、SPA 挂载 | `server.go` 等 |
| `app` | 组装 + CLI 子命令实现（退出码 0/1/2、stdout 单 JSON 对象）+ 优雅停机（SIGINT/SIGTERM，5s） | `app.go` `cli.go` |
| `webui` | `//go:embed all:dist` 嵌入前端产物；SPA 文件服务 + index.html 回退 | `webui.go` |

## 3. 隔离边界（isolation boundaries）

1. **无 SQL 泄漏**：SQL 只存在于 `internal/repo`；服务层只见类型化仓储方法。MCP/HTTP 均不暴露任意 SQL/Shell/SSH/文件访问。
2. **无生产写能力**：全仓库不存在任何设备连接代码；MCP 工具面 = `["resource_search"]`（测试 `TestMCPToolRegistry` 断言）。
3. **出口层薄**：`api`/`mcpserver` 只做协议适配（路径参数、错误码映射、JSON 形状），业务规则零重写；CLI 与 HTTP 共用同一服务实例（`app.New` 组装）。
4. **前端只经 API**：React 只调 `/api/*`（vite dev 代理 → :8080；生产走同源嵌入），无直连 DB。
5. **错误传播**：sentinel/包装错误定义在 `domain`/`workbook`/`mapping`/`importer`（如 `importer.ErrBatchInUse` 包装 `domain.ErrConflict`），出口层用 `errors.Is/As` 映射状态码，不解析消息文本。

## 4. 事务策略（`repo.DB.Do`）

- **导入执行**（`importer.Execute`）：所有 import sheet 的**全部写入在同一个 `db.Do` 事务**里——资源、关系、source_refs、row_results、issues、批次计数与状态。任何行级失败**不**中止（行记 `failed`）；任何内部错误（含测试注入的 afterSheet 钩子错误）→ 整体回滚 + 批次置 `failed`（在事务外写状态）。零半写由 `TestImportAtomicRollback` 验证。
- **集合级校验**（`validation.Engine.Run`）：单个 `db.Do`：`DeleteSetLevel`（删 batch_id IS NULL 的集合级问题）→ 计算 → 按 `(rule_code, entityKey)` 排序插入。失败则整体回滚，保留旧问题集。
- **批次回滚**（`importer.Rollback`）：单个 `db.Do` 删除关系 → 资源（FK 级联）→ source_refs → row_results → issues → 批次置 `rolled_back`。
- **迁移**：每个版本一个事务（DDL + `schema_migrations` 记录一起提交）；`Open` 幂等（已应用版本跳过）。
- **边界**：SQLite 单写者；`busy_timeout=5000` + WAL 已足够 Phase 1 负载（导入为 CLI/向导触发，非并发服务）。多工作区并发在范围之外。

## 5. 确定性策略（determinism）

- **排序即规范**：所有列表/插入顺序都有确定排序——topology 节点 `(type, natural_key)`；validation 插入 `(rule_code, entityKey)`；报告问题 severity→rule→entityKey→source→message→id；批次列表 `(started_at, workbook, rowid DESC)`（同秒导入按插入序稳定）。
- **ID/时间戳之外全稳定**：`TestValidationDeterminism`（ACC-4）对两个全新库跑同样导入+校验，把 evidence 中的实体 id 解析回自然键后做规范化 JSON 比较相等。
- **合成数据可再生成**：`cmd/testdatagen` 输出字节级确定（固定文档属性、无随机数、seed 行固定在确切 Excel 行号）。
- **建议引擎纯函数**：同一表头集合永远给同一建议（同义词表 + 置信度阶梯，无学习、无随机）。
- **时钟注入**：importer/report 的 `Now` 是可注入钩子（`Service.Now`/`Generator.Now`），测试可以钉住时间。

## 6. 单二进制与前端嵌入

`make web`（web/ → `vite build --outDir ../internal/webui/dist --emptyOutDir`）→ `make build`（`go build`）时 `internal/webui` 的 `//go:embed all:dist` 把产物打进 `bin/netopslab`（约 27MB）。dist 目录不提交（仅 `.gitkeep` 占位）；未构建前端时 SPA 路由安全降级（非根路径 404，不循环）。详见 ADR-0005。

## 7. 已知边界（诚实清单）

- 并发导入未做乐观锁（Phase 1 无此场景；SQLite 单连接串行化）；
- 报告/统计为全库口径计算，未做增量缓存（Phase 1 数据量级下毫秒级完成）；
- MCP 部署态（TeleAgent 宿主）调用未在本环境验证——见 `docs/verified-capabilities.md`。
