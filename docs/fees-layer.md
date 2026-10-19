# NetOps Lab — 费用分析层（Fee Analysis Layer）

> Issues #58/#60/#61（阶段 1/2 垂直切片）。架构决策依据：2026-10-18 与维护者的两层存储讨论 + #59 的 DuckDB 技术验证（引擎基准 ×2.5–×53、go-duckdb CGO 集成、Parquet 分区裁剪 ×2.4）。**实现即规范**：本文与 `internal/fees/`、`internal/repo/migrations_v29.go` 同步维护。

## 1. 两层架构（讨论裁决）

```
状态层 SQLite（既有，不动）         fee_batches 状态机、幂等键、回滚记账（migration v29）
快照层 文件系统（不可变）           <dataDir>/fee-imports/<type>/<year>/<month>/<sha256>_<原名>
分析层 Parquet（分区大表）          <dataDir>/analysis/fees/fee_type=…/year=…/month=…/batch-<id>.parquet
                                    查询引擎 = DuckDB（go-duckdb，CGO，-tags duckdb 构建）
```

裁决要点：

- **SQLite 边界**：行存 + 单查询单核 + AI 即席查询无法预建索引 → 多年跨表 JOIN 退化为全表扫描；状态层的 WAL 并发读无压力，继续承载任务/审计/确认流（#59 并发实测）。
- **不做几百张物理表**：原始文件几百个（每月/每区域/每费用类型一个），分析层**合并为一张分区大表**——SQL 不拼表名，分区裁剪替代索引（#59 实测 ×2.4）。
- **文件即存储**：分析层没有 .duckdb 数据库文件——Parquet 目录就是表；DuckDB 按查询打开 `:memory:` 引擎 `read_parquet(hive_partitioning=true)` 零拷贝读。无写锁、无 WAL、批次文件不可变追加。
- **Agent 红线延续**：查询面是**固定预设 + 参数**（`query.go`），调用方永远传不了 SQL——与 MCP 五工具只读参数化的同一纪律。

## 2. 快照层（证据链）

- **内容寻址**：`fee-imports/<fee_type>/<year>/<month>/<sha256>_<原始文件名>.<ext>`。目录成分 = 批次声明的账期（操作者对「这文件覆盖什么」的陈述）；同内容 → 同路径（幂等，临时文件 + 原子 rename 落盘）。
- **永不修改**：快照是每条分析行的回读源（`source_file + source_row` 行号级对位）；原始文件改名/重导不改变证据锚点。
- **回滚保留**：批次回滚删 Parquet（分析行消失）但**快照保留**——与资源导入器回滚保留工作簿的同一不对称语义（证据寿命 > 批次寿命）。

## 3. 分析层（分区大表）

**物理布局**（hive 分区，分区列只在路径、不在文件内）：

```
analysis/fees/fee_type=electricity/year=2024/month=05/batch-fee_<uuid>.parquet
```

**文件 schema**（`internal/fees/parquet.go`）：

| 列 | 类型 | 语义 |
| --- | --- | --- |
| `site_code` | string | 站址编码（规范化：去空白、**前导零保留**——身份字符串，绝不数值化） |
| `amount_cents` | int64 | 金额（**整数分**，#65 P1-a）：规范化（¥/￥/，/，/全角数字→半角、元后缀剥离）后**定点逐位解析**，不经 float64；至多两位小数（第三位 = 行失败，绝不静默取整）；全链路（解析/存储/sum 聚合/JSON `totalCents`）int64 分，对账到分零误差、比对与求和顺序无关。v0.13.0 及以前导入的批次文件列名为 float `amount`——升级后回滚旧批次重导（快照层保有原件） |
| `source_file` | string | 快照基名（哈希+原名）——证据锚点 |
| `source_row` | int32 | 原始文件 1 起行号（Excel 行 2+/CSV 数据行）——行号级对位 |
| `import_batch` | string | 批次 id（血缘） |
| `fee_type` / `year` / `month` | —（路径列） | hive 分区值，DuckDB 从目录名读取 |

**行级账期**：每行的 `period` 列（或 `date` 列）优先于批次声明账期——跨月文件按行拆分区（`2024-05` 行落 `month=05`，`2024-06` 行落 `month=06`）。账期列映射且非空时**必须**解析成功（垃圾账期 = 行失败，不是静默回退——分区粒度是整个分析层的前提）。

**批次共存（#60 核心裁决）**：同一 `(site, period)` 的多文件导入 = 同分区目录下**多个 batch 文件共存**，绝不合并、绝不去重——重复结算检测（阶段 4 风险场景）就是查它们（`HAVING count(DISTINCT import_batch) > 1`）。#59 的意外发现写入设计：重复行令窗口函数非确定——**重复行必须在标准化阶段显式共存**，本层照办。

## 4. 状态层（migration v29）

`fee_batches`：`id / workbook_name / sha256 / fee_type / fee_year / fee_month / snapshot_path / mapping / status / rows_total / rows_imported / rows_failed / error / created_at / completed_at`。

- **幂等键**：`(sha256, fee_type, fee_year, fee_month, mapping)` 的**部分唯一索引**（`WHERE status='completed'`）——同文件+同类型+同账期+同映射至多一个 completed 批次；failed/rolled_back **不占键**（重试通道构造性开放，#61「转换失败的批处理重试不产生半写状态」）。
- **提交点**：`MarkCompleted`（唯一索引在此触发）。导入顺序 = 崩溃安全设计：哈希读文件 → 幂等预检 → 快照（幂等）→ 规范化（行失败计数不致命）→ Parquet **staging 写入 + 原子 rename** → 批次行 flip completed。提交点之前的崩溃最多留下 GC 孤儿文件 + 一个 failed 批次行，重试永远干净。
- **GC**（`GCAnalysis`）：keep-set = completed 批次 id；`.staging` 下非 completed 批次的暂存目录、`batch-*.parquet` 中无 completed 行背书的文件 = 孤儿，删除。方向保守：completed 引用的一律保留。
- 与 `import_batches` **无外键、零耦合**：费用管道与资源管道是两个世界（资源导入器看不到 fee 批次，费用导入器不创建资源）。

## 5. 构建形态（CGO 隔离）

| 构建 | 命令 | 引擎 |
| --- | --- | --- |
| 默认（全平台，含 Windows/CI） | `go build` | `engine_std.go`：查询答 `ErrEngineNotBuilt`；**导入/快照/Parquet 写入全功能**（纯 Go，parquet-go） |
| 发布（linux/WSL2/macOS/docker） | `CGO_ENABLED=1 go build -tags duckdb`（或 `make build-duckdb`） | `engine_duckdb.go`：go-duckdb v2（#59 验证：预编译平台绑定、17s 构建、~68MB） |

CI 四个 job：go（默认全量）+ web + **duckdb（linux CGO `-tags duckdb` 跑引擎 e2e）** + windows（默认构建，验证 CGO 不泄漏）。

### 5.1 Windows 查询路径（#65 P1-c）

go-duckdb 静态绑定需要 Windows Restart Manager 库（`rstrtmgr`），mingw-w64 不提供——`CGO_ENABLED=1 go build -tags duckdb` 在 Windows 链接失败（`ld.exe: cannot find -lrstrtmgr`，#65 实测）。因此 **Windows 原生构建没有 duckdb 路径**，费用查询固定答 `501 engine_not_built`（错误体即指引）；两条官方替代路径：

```powershell
# ① Docker（推荐）：镜像默认含 duckdb 引擎（bookworm + libstdc++6）
docker build -t netops-lab:local .
docker run --rm -p 8080:8080 -v netops-data:/data netops-lab:local
# 退化纯 Go 构建（不要查询引擎）：docker build --build-arg BUILD_TAGS="" .

# ② WSL2 内构建（Ubuntu/Debian 自带 gcc）
wsl -e bash -lc "cd /mnt/c/<repo> && make build-duckdb"
```

导入/快照/Parquet 写入与关联扫描在 Windows 默认构建上**全功能可用**（纯 Go）——只有查询预设需要上述路径。`run.ps1` 头部注释与 `ErrEngineNotBuilt` 错误文案同步携带该指引。

## 6. CLI 面

```
netopslab fees --db <path> --data <dir> import  --file <f> --type <t> --year <Y> --month <M> [--map <json>]
netopslab fees --db <path> --data <dir> list    [--status completed|failed|rolled_back]
netopslab fees --db <path> --data <dir> rollback --batch <id>
netopslab fees --db <path> --data <dir> query   totals|duplicates --type <t> --year <Y> [--month <M>] [--limit <n>]
```

- 全部单 JSON stdout 契约；usage 错误 exit 2；幂等重复导入 **informational（exit 0 + `skippedDuplicate`）**——与资源导入器重复跳过同严重级。
- `--map` 缺省时自动识别（表头同义词：站址编码/电费(元)/账期…；类型标记：电量/塔租/工单/综维…）；识别不到 siteCode 或 amount → 拒绝（人工映射是回退，不是猜测）。
- `query` 两个预设：`totals`（站址汇总，分区裁剪验证面）+ `duplicates`（跨批次重复结算候选——**风险线索，非结论**：账单存在 ≠ 重复付款认定，输出待人工核实的差异行）。

## 7. HTTP API 面（与 CLI 同语义）

| 端点 | 语义 |
| --- | --- |
| `POST /api/fees/imports` | multipart（file + feeType + year + month [+ map]）；201 新导入 / 200 幂等重复（`skippedDuplicate`）/ 422 行全失败或不可映射 |
| `GET /api/fees/batches?status=` | 批次列表（最新在前，中文类型标签 + 账期渲染） |
| `POST /api/fees/batches/{id}/rollback` | 回滚（删分析行保快照；409/404 守卫） |
| `GET /api/fees/query/totals?feeType=&year=&month=&limit=` | 站址汇总预设 |
| `GET /api/fees/query/duplicates?feeType=&year=&month=` | 跨批次重复结算候选预设 |
| `POST /api/fees/links/scan` | 分级关联扫描（exact/rule 自动确认，fuzzy 提案） |
| `GET /api/fees/links?status=&grade=&code=&resource_id=` | 关联列表（资源侧 join） |
| `POST /api/fees/links/{id}/confirm \| dismiss` | 人工确认/驳回模糊提案（人类优先于扫描） |

写路由过 v0.11-R13 令牌写门禁（POST 一律 Bearer）；查询预设默认构建答 **501 `engine_not_built`**（诚实契约）。五端点进 OpenAPI 文档（`fees` tag，路由表生成零漂移）。实测：duckdb 二进制上 `query/totals` 跨批聚合 2233.56 与 `query/duplicates` 唯一候选命中——与 CLI 旅程逐字一致。

## 8. 路线（#58 阶段表）

| 阶段 | 状态 |
| --- | --- |
| 0 技术验证（#59） | ✅ 关闭（双引擎基准 + CGO + Parquet 实测） |
| 1 分层设计（#60） | ✅ 本切片落地（快照层 + 分区大表 + 血缘 + 批次共存） |
| 2 导入管道（#61） | ✅ 本切片落地（CSV/Excel → 快照 → Parquet，幂等/重试/回滚/GC） |
| 3 实体分级关联（站址编码精确 → 规则 → 模糊人工确认） | ✅ 落地（#63，migration v30：exact/rule 自动确认 + fuzzy 人工提案流 + dropout 清扫 + 人类优先） |
| 4 六大风险场景规则 + 证据追溯 | 待开（`duplicates` 预设是首个场景的雏形） |
| 5 MCP 工具面扩展 + 统一工作台入口 | 待开（HTTP API 面待阶段 3 前） |

## 9. 相关文档

- [data-model.md](data-model.md) —— v29 DDL（fee_batches）；
- [api.md](api.md) —— CLI 契约示例（HTTP 面待后续轮次）；
- #59 关闭评论 —— 引擎基准数字与设计纠偏（重复行共存裁决的来源）。
