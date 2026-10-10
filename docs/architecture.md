# NetOps Lab — 架构说明（Architecture）

> 本文描述 Stage 0 + Phase 1 + Phase 2（v0.2.0）的实际实现。源码是唯一事实来源：分层、包边界、事务与确定性策略均可在 `internal/` 中逐一对应。中文为主，关键术语保留英文。

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
internal/sim                           ← 仿真引擎（Phase 2）：类型化操作 + 逆操作、物化、
        │         CHK-001…005、连通性、状态哈希、diff、undo、检查点——
        │         独占全部仿真表写路径（handlers/tools/CLI 只能经它写）
        ▼
internal/importer · internal/validation · internal/query · internal/report · internal/topology
        │        服务层（全部业务规则在这里；只依赖 repo 接口）
        ▼
internal/repo                          ← 存储层：SQLite（modernc.org/sqlite，纯 Go 无 CGO）
        │                                  迁移 v1–v5 + 各聚合仓储 + Online Backup（backup.go）
        ▼
internal/domain                        ← 领域层：实体/枚举/错误/规则码 + 操作码/检查码（零内部依赖）

internal/workbook · internal/mapping   ← 解析与映射（被 importer 使用；workbook 只管 Excel，
                                          mapping 只管目标 schema 与建议引擎）
internal/webui                         ← go:embed 前端产物（被 api 的 SPA 路由挂载）
web/                                   ← React 源码（构建产物输出到 internal/webui/dist，
                                          Phase 2 增 Cytoscape 拓扑画布，见 ADR-0009）
```

依赖方向**自上而下、单向**：

- `domain` 不 import 任何内部包（P1 规则码/严重级别 + P2 操作码/检查码/sentinel（`ErrInvalidOp`/`ErrNotLatest`/`ErrOpFailed`/`ErrCheckpointMissing`）全部在这里）；
- `repo` 只被服务层和出口层的**读路径**使用；`sim` 引擎独占仿真表（v5 表）写路径——**处理器 / MCP 工具 / CLI 没有任何仿真的 SQL 直写**；
- 服务层之间不互相 import（`query` 通过 `topology` 组合图查询；`importer` 与 `validation` 互不引用；`report` 通过 `WorkspaceHasher` 接口消费 `sim` 的哈希能力而不 import 引擎）；`api`/`mcpserver`/CLI 各自绑定同一服务实例（`app.New` / `api.New` 组装，包括 `sim.New`）。

## 2. 各模块职责

| 包 | 职责 | 关键文件 |
| --- | --- | --- |
| `domain` | 实体（Resource/Relation/Issue/Batch/SourceRef/RowResult）、枚举、sentinel 错误、规则码→严重级别、ID 生成（`res_`/`rel_`/`src_`/`bat_`/`iss_` 前缀 + UUID） | `entities.go` `enums.go` `rules.go` |
| `repo` | `Open(path)` 应用迁移（v1–v5）；DSN 携带 `busy_timeout(5000)` `journal_mode(WAL)` `foreign_keys(1)`；`DB.Do(ctx, fn)` 事务组合子；P1 六个聚合仓储 + P2 八个仿真仓储；`DB.Backup`（Online Backup API，ADR-0008）与 `OpenReadOnly`（快照只读打开） | `repo.go` `migrations*.go` `backup.go` |
| `workbook` | excelize 解析：Sheet 列表、表头行、数据行（空行跳过并计数）、类型化单元格读取；`ErrNotXLSX`/`ErrEmptyWorkbook` | `workbook.go` |
| `mapping` | 目标 schema（字段类型 string/ipv4/cidr/int/enum/ref + 取值范围）、映射模型与校验（`*FieldError` 定位 sheet/field）、确定性建议引擎（置信度阶梯 1.0/0.9/0.75/0） | `schema.go` `mapping.go` `suggest.go` |
| `importer` | 分析（建批次、写 workbook_path、产出 Analysis）、执行（单事务导入所有 import sheet、行级规则、行级记账、幂等 skip）、回滚（completed-only + 409 in-use） | `analyze.go` `execute.go` `rollback.go` |
| `validation` | 集合级规则引擎：E005 网段重叠 / E006 地址重复 / I001 孤岛设备；单事务"删旧插新"，幂等可重算 | `engine.go` |
| `topology` | 关系图构建（节点按 `(type, natural_key)` 排序）；全资源图 BFS 邻域（深度 1/2，无向）；设备级拓扑可达性（电路视为设备间链路） | `topology.go` |
| `query` | 检索/统计/详情（含 source_refs 解析与 relationCount）/关系子图/拓扑检查——只读服务 | `query.go` |
| `report` | 质量报告：`Report` 结构（camelCase JSON）、来源解析（resolve.go：source_ref_id → workbook/sheet/row；行级失败行 → 批次+行号；否则 applicationGenerated）、独立 HTML（`html/template` + `//go:embed template.html`，全量自动转义）；Phase 2 增仿真工作台报告（labreport.go + labtemplate.html：引擎版本/评分/发现/变更集/检查点 + 同一免责声明） | `report.go` `resolve.go` `html.go` `labreport.go` |
| `sim` | **仿真引擎**（Phase 2）：类型化操作集（10 前向 + 3 内部 restore）与严格校验、物化（确定性端口分配）、CHK-001..005 确定性校验、连通性 BFS、规范状态哈希、run diff、LIFO undo、检查点创建/恢复（ADR-0007/0008）；**独占仿真表写路径** | `ops.go` `apply.go` `materialize.go` `checks.go` `state.go` `diff.go` `undo.go` `checkpoint.go` |
| `mcpserver` | MCP stdio 服务器（go-sdk v1.8.0 IOTransport）；注册 **5 个工具**：只读 `resource_search` + 仿真写面 `lab_apply_ops` / `lab_run_checks` / `lab_diff_undo` / `lab_checkpoint`（全部绑定 sim 引擎；restore_* 双重拒绝；**无建删工作区工具**） | `mcpserver.go` `labtools.go` |
| `api` | net/http stdlib 路由（Go 1.27 method patterns）、统一 JSON 信封与错误映射（400/404/409/413/422/500）、panic 恢复 + slog 请求日志、10MB 上传限制、`no-store` 缓存中间件、SPA 挂载；Phase 2 增 17 条 §7 仿真路由（workspaces.go，英文错误消息） | `server.go` `workspaces.go` 等 |
| `app` | 组装 + CLI 子命令实现（退出码 0/1/2、stdout 单 JSON 对象）+ 优雅停机（SIGINT/SIGTERM，5s）；Phase 2 增 `lab` 子命令树（workspace/topology/path/apply/checks/diff/undo/checkpoint） | `app.go` `cli.go` |
| `webui` | `//go:embed all:dist` 嵌入前端产物；SPA 文件服务 + index.html 回退 | `webui.go` |

## 3. 隔离边界（isolation boundaries）

1. **无 SQL 泄漏**：SQL 只存在于 `internal/repo`；服务层只见类型化仓储方法。MCP/HTTP 均不暴露任意 SQL/Shell/SSH/文件访问。
2. **无生产写能力**：全仓库不存在任何设备连接代码；仿真的"写"也只落在本地 SQLite 的仿真表。MCP 工具面 = `[resource_search, lab_apply_ops, lab_run_checks, lab_diff_undo, lab_checkpoint]` **恰好 5 个**（测试 `TestMCPToolRegistry` 断言）。
3. **AI 边界（Phase 2）**：AI 改仿真态只能通过 4 个类型化 lab 工具（每个变更显式携带 workspaceId、走引擎校验/变更集/自动校验）；**MCP 不能创建/删除工作区**（工作区创建是人工/API 动作）；`restore_*` 内部操作码在 HTTP/MCP/CLI 三边界均被拒。
4. **引擎独占仿真写路径**：`internal/sim` 是唯一写 v5 仿真表的包（ADR-0007 决策 6）——出口层只做协议适配。
5. **出口层薄**：`api`/`mcpserver` 只做协议适配（路径参数、错误码映射、JSON 形状），业务规则零重写；CLI 与 HTTP 共用同一服务实例（`app.New` 组装）。P2 路由错误消息为英文、P1 路由保持中文（§7 契约，信封同构）。
6. **前端只经 API**：React 只调 `/api/*`（vite dev 代理 → :8080；生产走同源嵌入），无直连 DB；`/lab` 页面同样只消费 §7 路由。
7. **错误传播**：sentinel/包装错误定义在 `domain`/`workbook`/`mapping`/`importer`（如 `importer.ErrBatchInUse` 包装 `domain.ErrConflict`、`domain.ErrCheckpointMissing` 包装 `ErrConflict`），出口层用 `errors.Is/As` 映射状态码，不解析消息文本。

## 4. 事务策略（`repo.DB.Do`）

- **导入执行**（`importer.Execute`）：所有 import sheet 的**全部写入在同一个 `db.Do` 事务**里——资源、关系、source_refs、row_results、issues、批次计数与状态。任何行级失败**不**中止（行记 `failed`）；任何内部错误（含测试注入的 afterSheet 钩子错误）→ 整体回滚 + 批次置 `failed`（在事务外写状态）。零半写由 `TestImportAtomicRollback` 验证。
- **集合级校验**（`validation.Engine.Run`）：单个 `db.Do`：`DeleteSetLevel`（删 batch_id IS NULL 的集合级问题）→ 计算 → 按 `(rule_code, entityKey)` 排序插入。失败则整体回滚，保留旧问题集。
- **批次回滚**（`importer.Rollback`）：单个 `db.Do` 删除关系 → 资源（FK 级联）→ source_refs → row_results → issues → 批次置 `rolled_back`。
- **仿真操作/撤销/恢复**（`sim.applyInternal`，Phase 2）：变更集行 + 逐操作迁移 + 逐操作逆操作记录**同一事务**；语义失败 → 整体回滚 → 第二个事务记录失败变更集（ADR-0007）。undo 的目标标记 `reverted` 在撤销变更集提交后的独立小事务中（崩溃窗口分析见 `sim/undo.go` 注释）。
- **校验运行**（`sim.RunChecks`）：单事务（状态读取 + 发现计算 + run/results/topology 快照写入）。
- **物化**：单事务（workspace + 序列 + 设备 + 接口 + 链路 + 哈希）。
- **检查点**：三步非事务化（id 分配 → Online Backup 文件写 → 标记行插入，崩溃安全分析见 ADR-0008）；恢复 = 前向操作变更集（引擎路径）。
- **迁移**：每个版本一个事务（DDL + `schema_migrations` 记录一起提交）；`Open` 幂等（已应用版本跳过）。
- **边界**：SQLite 单写者；`busy_timeout=5000` + WAL 已足够负载（导入为 CLI/向导触发，仿真是单用户交互式变更）。多工作区**并发**写显式 out-of-scope（schema 支持共存，单活跃工作区是受支持模式）。

## 5. 确定性策略（determinism）

- **排序即规范**：所有列表/插入顺序都有确定排序——topology 节点 `(type, natural_key)`；validation 插入 `(rule_code, entityKey)`；报告问题 severity→rule→entityKey→source→message→id；批次列表 `(started_at, workbook, rowid DESC)`（同秒导入按插入序稳定）；仿真拓扑节点按 code、接口按 name、边按数值 id、校验发现按 (check_code, 实体键, detail JSON)、BFS 邻居按设备编码升序。
- **状态哈希**（Phase 2）：仿真状态 → 规范 JSON → sha256（[simulation.md](simulation.md) §6）；同状态必同哈希，撤销/恢复用它验证"精确回到基线"。
- **ID/时间戳之外全稳定**：`TestValidationDeterminism`（ACC-4）与 `TestSimulationDeterminism`（DET-P2）对两个全新库跑同样流程，把 evidence/实体 id 解析回自然键后做规范化 JSON 比较相等（仿真含 id 预测路径：序列确定性使检查点恢复的 create 计划可预测引擎将分配的 id）。
- **合成数据可再生成**：`cmd/testdatagen` 输出字节级确定（固定文档属性、无随机数、seed 行固定在确切 Excel 行号）。
- **建议引擎纯函数**：同一表头集合永远给同一建议（同义词表 + 置信度阶梯，无学习、无随机）。
- **时钟注入**：importer/report/sim 的 `Now` 是可注入钩子，测试可以钉住时间。

## 6. 单二进制与前端嵌入

`make web`（web/ → `vite build --outDir ../internal/webui/dist --emptyOutDir`）→ `make build`（`go build`）时 `internal/webui` 的 `//go:embed all:dist` 把产物打进 `bin/netopslab`（约 28MB；Phase 2 前端含 Cytoscape，js 717.63 kB / gzip 226.73 kB——单 chunk 不分割的决策见 ADR-0009）。dist 目录不提交（仅 `.gitkeep` 占位）；未构建前端时 SPA 路由安全降级（非根路径 404，不循环）。详见 ADR-0005。

## 7. 已知边界（诚实清单）

- 并发导入未做乐观锁（无此场景；SQLite 单连接串行化）；
- 报告/统计为全库口径计算，未做增量缓存（Phase 1 数据量级下毫秒级完成）；
- **多工作区并发写不支持**（schema 允许多工作区共存；检查点恢复的 id 预测依赖单写者模式）；
- MCP 部署态（TeleAgent 宿主）调用未在本环境验证——见 `docs/verified-capabilities.md`；
- `/lab` 的 Cytoscape 视觉效果未经真实浏览器人工确认（结构断言通过，终审待做）。

## 8. 地理坐标元数据路径（geo pipeline，schema v6）

设备坐标走一条**独立于业务规则的展示元数据管道**：导入列（`纬度`/`经度` 分列，或组合列 `经纬度` 经中国边界启发式拆分，`internal/mapping` 的 `SplitGeoLatLng`）→ 资源属性 `geo_lat`/`geo_lng`（纯十进制串）→ 物化时解析进 `domain.VirtualDevice.GeoLat/GeoLng`（v6 两列可空 `REAL`，`internal/repo/migrations_v6.go`）→ 拓扑负载节点携带 `geoLat`/`geoLng`（`omitempty`）。红线：**geo 绝不进入规范状态哈希**（`hashDevice` 不含 geo，回归测试证明含/不含坐标的同业务状态哈希逐字节一致）——坐标是给前端 2D/3D 地图视图消费的呈现数据，挪动地图上的设备不允许改变哈希、校验或 diff。撤销路径的 `DeviceSnap` 携带坐标（delete→undo 位置不丢）；已知边界：检查点恢复重建被删设备走 `create_device`（封闭操作面无 geo 字段），重建设备无坐标（哈希不受影响）。演示数据 `testdata/geodevices.xlsx` + `geocircuits.xlsx`（`testdatagen -geo`，黑龙江真实网络孪生，坐标为演示近似值），演示脚本 `scripts/demo-geo-lab.sh`。

前端消费端为 `web/src/components/lab/GeoCanvas.tsx`（v0.3.0）：画布三视图切换（拓扑图 / 2D 地图 / 3D 地图，MapLibre GL 懒加载）。底图 ESRI `Canvas/World_Dark_Gray_Base + Reference` 双层栅格（免密钥匿名可用；曾评估 CARTO dark_all，2026-10 实测对匿名 IP 统一返回「API KEY REQUIRED」水印砖已弃用）；瓦片连续失败自动重建无栅格样式降级（设备/链路本地 GeoJSON 恒可渲染）。设备为 HTML Marker（键盘可聚焦、与拓扑 tap 同一选中语义联动表格），链路为 GeoJSON line 图层（up 实线 / down 红虚线 marching-ants），3D 塔为 fill-extrusion（像素高 18 + 连接数 × 10，高度经顶层 `interpolate exponential-2` 双停止点按 zoom 几何换算为米——MapLibre 规范要求 paint 属性中 `zoom` 只能作顶层 interpolate 输入；塔 footprint 按当前 zoom 恒定 7px 宽重建，规避亚像素多边形无法光栅化的问题）。
