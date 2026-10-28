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
- **永不修改**：快照是每条分析行的回读源（`source_file + source_row` 行号级对位）；原始文件改名/重导不改变证据锚点。回读通道（#86）：`GET /api/fees/batches/{id}/evidence?row=N` 与 `netopslab fees evidence --batch <id> --row <n>`（行号 = 源文件自身编号，表头 = 1；回滚批次仍可读——快照按设计存活）。
- **回滚保留**：批次回滚删 Parquet（分析行消失）但**快照保留**——与资源导入器回滚保留工作簿的同一不对称语义（证据寿命 > 批次寿命）。回滚本身是**原子事务（#107）**：先把删除集物化成显式清单（该批次的每个分区文件 → 隔离槽），逐文件以**可逆 rename** 移入 `analysis/fees/.rollback/<批次id>/`（同文件系统、纯元数据操作——失败时文件原地不动），**最后一个文件移完才**把批次行 flip `rolled_back`（单一提交点，与导入的第 7 步同构）。提交点之前的任何故障（IO 错误、DB 故障）→ 隔离整体 UNDO，批次保持 completed、**每一行都还在——回滚要么整体发生要么整体不发生**；UNDO 本身也被阻断的极端双重故障 → 批次行如实标 `failed`（隔离区滞留位置写入 error 文本，绝不假扮 completed/rolled_back；`failed` 释放幂等键，重导快照原件即恢复分析行，滞留隔离区与残留文件过里程碑后由 GC 收走）。隔离区对一切读者不可见（镜像布局在 `fees/` 下五层深，查询 glob 恰三层）；崩溃在隔离中途留下的残局由下一次回滚先收养（恢复隔离文件后重新执行）。

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
- **回滚原子性（#107）**：回滚不再逐文件 unlink 后 flip——那个窗口里中途 IO 错误会留下「completed 批次 + Parquet 行已删」的中间态（既非完整回滚也非完整保留，审计口径直接损坏）。现行为：物化删除清单 → 逐文件可逆 rename 进 `.rollback` 隔离区 → **flip `rolled_back` 为唯一提交点** → 提交后清隔离区 + 修剪空分区目录。故障语义：提交前故障 = 整体 UNDO（批次保持 completed，行数不变）；UNDO 也故障 = 批次行标 `failed`（既有诚实态：不占幂等键 → 重导即恢复；error 文本记录隔离区滞留位置）；翻转失败（含并发赢家先行 flip）= UNDO 恢复全部分析行后如实报错。隔离区不可见性（五层深 vs 查询 glob 三层）与 GC 接管见下。
- **GC**（`GCAnalysis`，#106 接线）：keep-set = completed 批次 id；`.staging` 与 `.rollback`（#107 隔离区）下非 completed 批次的工作目录、`batch-*.parquet` 中无 completed 行背书的文件 = 孤儿，删除（隔离区整体按**目录粒度**判定——残局是全有或全无，文件级删除会剥离可收养证据并留空镜像目录垃圾；completed 批次的隔离区是撕裂回滚的收养证据，keep-set 永远赢过里程碑）。方向保守：completed 引用的一律保留。**触发点（cmd 层，与 uploads GC 同款二处）**：①`serve` 启动时——HTTP 监听启动**前**串行执行（本进程不可能有在途导入）；②CLI `netopslab gc`（与 uploads 清理同一次运行，stdout 单 JSON 计数对象多 `analysisFiles`/`analysisStaging` 两字段——`analysisStaging` 覆盖暂存与隔离两工作目录）。MCP 进程零触发、HTTP 无 GC 路由。**并发里程碑（mtime 防御）**：Parquet 文件在批次行 flip completed **之前**就已 rename 进分区目录——跨进程（`fees import` CLI 与 serve 启动重叠）在途导入的文件会短暂呈现「无 completed 行背书」形态；无差别清理会静默丢行。孤儿仅在**最后写入时间超过 1 小时宽限**后才可删：在途导入的文件总是秒级新鲜，真实崩溃孤儿在被扫到时早已过期，宽限只延迟「一小时内崩溃的碎片」的收集。`.staging` 与 `.rollback` 子树 walk 整体跳过（`filepath.SkipDir`），只由专属工作目录清扫按同一里程碑判定。
- 与 `import_batches` **无外键、零耦合**：费用管道与资源管道是两个世界（资源导入器看不到 fee 批次，费用导入器不创建资源）。

## 5. 构建形态（CGO 隔离）

| 构建 | 命令 | 引擎 |
| --- | --- | --- |
| 默认（全平台，含 Windows/CI） | `go build` | `engine_std.go`：查询答 `ErrEngineNotBuilt`；**导入/快照/Parquet 写入全功能**（纯 Go，parquet-go） |
| 发布（linux/WSL2/macOS/docker） | `CGO_ENABLED=1 go build -tags duckdb`（或 `make build-duckdb`） | `engine_duckdb.go`：go-duckdb v2（#59 验证：预编译平台绑定、17s 构建、~68MB） |

CI 四个 job：go（默认全量 + **staticcheck W3 门禁三面**：默认构建 / `-tags duckdb` / `GOOS=windows` 跨平台类型检查——#33 起零告警）+ web + **duckdb（linux CGO `-tags duckdb` 跑引擎 e2e，测试后构建部署二进制上传为 artifact，#34）** + windows（默认构建，验证 CGO 不泄漏）。另有两条夜间 workflow（#33）：**fuzz**（`internal/fees` 九个 fuzz 目标各 5 分钟引擎时间；主干 24h 无新提交即整 job 跳过——fuzz昨天的字节无意义，配额纪律）与 **bench**（基准套件隔夜同机族对比，`scripts/bench-gate.py` 超阅 20% 告警不阻断）。

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

### 5.2 从 CI 产物直接获得已验证的 Linux 二进制（#34）

上述 ①② 之外的第三条官方路径：**每次 main 推送，CI 的 duckdb job 在测试全绿后把部署二进制上传为 artifact**（`actions/upload-artifact@v4`，名 `netopsfees-linux-amd64-duckdb`，保留 30 天）。构建命令与 `scripts/release.sh` 的发布资产**逐字相同**（`CGO_ENABLED=1 go build -tags duckdb`），测试步已编译过全量依赖树——构建秒级、不增 CI 时长；CI 验证过的二进制与 Release 资产同源同树。任意平台下载：

```powershell
# Windows / macOS / Linux 均可：
gh run download <run-id> -n netopsfees-linux-amd64-duckdb -D out
./out/netopsfees fees          # 裸调用即列出全部 17 个子命令（CLI 契约：usage 应答也是单 JSON，exit 2）
./out/netopsfees fees --db ./data/netops.db --data ./data query monthly --type electricity --year 2026
```

拿到二进制后，本地 40+ 分钟的 CGO 构建场景坍塌为 5 秒的 COPY（`go:embed` 已内嵌前端，单文件即全部）：

```dockerfile
FROM ubuntu:24.04
COPY netopsfees /app/netopsfees
ENTRYPOINT ["/app/netopsfees"]
# docker build -t fees:ci . && docker run --rm -p 8080:8080 -v fees-data:/data fees:ci serve --addr :8080 --db /data/netops.db --data-dir /data
```

**基线要求（#35 实测教训）**：该二进制由 CGO 动态链接 CI 构建环境（Ubuntu 24.04）的 glibc，需要 **GLIBC ≥ 2.38**。基础镜像选错了「构建成功、运行即崩」（`libm.so.6: version 'GLIBC_2.38' not found`，容器一秒退出）——`debian:bookworm-slim`（GLIBC 2.36）与 `ubuntu:22.04`（2.35）都不够；`ubuntu:24.04`（2.39）实测可跑、DuckDB 引擎在位。**本节的 Dockerfile 示例不是普通文档**：CI 的 duckdb job 末步「verify artifact runs in the documented base image」用 `scripts/extract-doc-dockerfile.py` 把这段示例原样提取出来、用刚产出的二进制实际构建并启动断言（文档即测试，#37）——示例改坏（换错基础镜像、删掉 GLIBC 基线注）CI 立刻红，不再等用户踩坑。

诚实边界：artifact 是 **linux-amd64** 单平台产物（解决的是 CGO 跨平台不可得）；按版本取产物用 GitHub Release 的三资产（source / linux-amd64 / linux-amd64-duckdb——v0.20.0 起每版都在），按最新主干取产物用 CI artifact；artifact 过期 30 天后走 Release。

## 6. CLI 面

```
netopsfees fees --db <path> --data <dir> import           --file <f> --type <t> --year <Y> --month <M> [--map <json>]
netopsfees fees --db <path> --data <dir> import-sheets    --file <f> --year <Y> --month <M>   （多 sheet 工作簿）
netopsfees fees --db <path> --data <dir> period-probe     --file <f>      （#15：表级账期/年份探测，导入面板的数据面）
netopsfees fees --db <path> --data <dir> list             [--status completed|failed|rolled_back]
netopsfees fees --db <path> --data <dir> rollback         --batch <id>
netopsfees fees --db <path> --data <dir> evidence          [--batch <id>]   （快照证据链）
netopsfees fees --db <path> --data <dir> query             totals|monthly|duplicates|rows --type <t> --year <Y> [--month <M>] [--limit <n>] [rows: --site <s> --record-key <k>]
netopsfees fees --db <path> --data <dir> link-scan                          （分级关联扫描：exact → rule → fuzzy 提案）
netopsfees fees --db <path> --data <dir> links             <list|…>         （confirmed 关联管理）
netopsfees fees --db <path> --data <dir> risks             [--scenario <s>] （风险十场景）
netopsfees fees --db <path> --data <dir> rules              <add|list|remove>（#2：自定义规则）
netopsfees fees --db <path> --data <dir> clue-decisions   <list|decide>    （线索人工裁定：verified/pending/dismissed）
netopsfees fees --db <path> --data <dir> details           <add|list|remove>（明细层增删改查，三强制生效）
netopsfees fees --db <path> --data <dir> aliases           add|list|remove|scan （站址别名）
netopsfees fees --db <path> --data <dir> cross-reconcile                     （#12：跨数据集对账）
netopsfees fees --db <path> --data <dir> verify-sources                      （#6：源文件审计/可写建议）
netopsfees fees --db <path> --data <dir> verify-batches                      （#32：两档体检——账期问题 + 数据质量 dataProblems；#33 起每条携带 check（DQ1-DQ6）与 severity 标注，见 api.md）
```

- 全部单 JSON stdout 契约；usage 错误 exit 2；幂等重复导入 **informational（exit 0 + `skippedDuplicate`）**——与资源导入器重复跳过同严重级。
- `--map` 缺省时自动识别（表头同义词：站址编码/电费(元)/账期…；类型标记：电量/塔租/工单/综维…）；识别不到 siteCode 或 amount → 拒绝（人工映射是回退，不是猜测）。**表头归一化（#29 + #33）**：换行/回车/制表符与空格/下划线/连字符一并折叠，#33 起整面 unicode 空白族（NBSP、Zs 分隔符、\v\f）也折叠且**全角括号归一为半角**——Excel Alt+Enter 多行表头（塔类结算详单「产品服务费合计\n（出账费用）（不含税）」）折叠为单行 key 再匹配，「费用（元）」与「费用(元)」同键。**类型口径专表（#29）**：tower_lease 的金额列按口径优先级 **exact 匹配**（产品服务费合计(出账费用)(不含税)→ … → 产品服务费合计，各口径只列半角一形——#33 起括号宽度由归一化层携带，全角表头同键命中；曾靠手工双形列表，漏一种即 #29 的失效形态）——通用 contains 兜底在 157 列多金额列变体表上曾命中全零变体列（454 行导入、totalCents 全 0、零警告的实测缺陷）；专表 miss 逐字节回退通用识别；显式 `--map` 永远胜出（口径是运营决策）；摘要 `amountColumn` 写明实际取数表头，同族变体 >1 时 warnings 说明口径。**全零止血（#29）**：新导入全部行金额为 0 → completed 应答携带具名警告（绝不静默导好）；`fees verify-batches` 数据面（#32/#33）对存量批次同形态检出（`zero-amount`，DQ1）。
- `query` 四个预设：`totals`（站址汇总，分区裁剪验证面）+ `monthly`（#3：全年逐月趋势序列，不接受 `--month`）+ `rows`（#5：明细行原始面，带 --site/--record-key 过滤）+ `duplicates`（跨批次重复结算候选——**风险线索，非结论**：账单存在 ≠ 重复付款认定，输出待人工核实的差异行）。子命令闭集由 `usage_closset_test.go` 以 go/ast 双向钉死，清单以代码为准。

## 7. HTTP API 面（与 CLI 同语义）

| 端点 | 语义 |
| --- | --- |
| `POST /api/fees/imports` | multipart（file + feeType + year + month [+ map]）；201 新导入 / 200 幂等重复（`skippedDuplicate`）/ 422 行全失败或不可映射 |
| `GET /api/fees/batches?status=` | 批次列表（最新在前，中文类型标签 + 账期渲染） |
| `POST /api/fees/batches/{id}/rollback` | 回滚（删分析行保快照；409/404 守卫） |
| `GET /api/fees/batches/{id}/evidence?row=` | 证据回读：快照第 N 行原文（#86；row = 源文件自身编号，表头 = 1；`source_row` 锚点的第三级证据，回滚批次仍可读） |
| `GET /api/fees/years` | 数据年份发现（v0.20.3：有分析行的年份升序；路径级分区在场清单——回滚后的空分区目录不算在场，两种构建都作答无 501；年份输入默认跟随数据而非日历、总览年份诚实空态的判据面，详见 api.md） |
| `GET /api/fees/query/totals?feeType=&year=&month=&limit=` | 站址汇总预设 |
| `GET /api/fees/query/duplicates?feeType=&year=&month=` | 跨批次重复结算候选预设 |
| `POST /api/fees/links/scan` | 分级关联扫描（exact/rule 自动确认，fuzzy 提案） |
| `GET /api/fees/links?status=&grade=&code=&resource_id=` | 关联列表（资源侧 join） |
| `POST /api/fees/links/{id}/confirm \| dismiss` | 人工确认/驳回模糊提案（人类优先于扫描） |
| `GET /api/fees/risks/{scenario}?feeType=&year=&month=&status=&diffCents=&ratio=&z=&minSites=&limit=&maxEvidence=&includeDecided=` | 风险十场景（#64/阶段 4 + #114 场景 7 + #118 场景 8 + #2 场景 10 custom-rules；判定口径与阈值默认值见 §8；参数校验先于引擎检查，默认构建 501；#119 线索携带人工裁定标记、dismissed 默认隐藏计数、`includeDecided` 审计视图） |
| `GET /api/fees/rules?enabled=` | 可配置规则列表（#2：读开放，`enabled=1` 仅已启用） |
| `POST /api/fees/rules` | 创建可配置规则（#2：申报级令牌——AI/运营通道；新规则默认停用观察态；封闭枚举违例 422 `rule_refused`；审计行 created + actor=令牌层级） |
| `POST /api/fees/rules/{id}/enable` | 启用规则（#2：人类通道，管理级令牌；审计行 enabled） |
| `POST /api/fees/rules/{id}/disable` | 停用规则（#2：人类通道，管理级令牌；改阈值=停用旧+建新+启用新，全程审计） |
| `GET /api/fees/rules/{id}/audit` | 规则动作台账（#2：created/enabled/disabled + actor + 变更 JSON，最新优先；未知 id 404） |
| `POST /api/fees/risk-clue-decisions` | 风险线索人工裁定（#119：verify 属实留档 / dismiss 排除永不再现；UPSERT 可复议；人类通道，MCP 无此写面；不依赖引擎——默认构建可裁定） |
| `GET /api/fees/risk-clue-decisions?scenario=&feeType=&status=&siteCode=&year=` | 裁定台账（最新优先，过滤器封闭集校验；审计视图） |

写路由过 v0.11-R13 令牌写门禁（POST 一律 Bearer）；查询预设默认构建答 **501 `engine_not_built`**（诚实契约）。全部端点进 OpenAPI 文档（`fees` tag 共 33 条路由，路由表生成零漂移；计数以 [facts.md](facts.md) 为机读事实源，`TestAPIRouteCountsMatchFactsFile` 比对路由表实数钉死——v0.20.3 修正了本行停更的 21 条旧数）。实测：duckdb 二进制上 `query/totals` 跨批聚合 2233.56 与 `query/duplicates` 唯一候选命中——与 CLI 旅程逐字一致。

## 8. 风险十场景（#64，#58 阶段 4；#114 加场景 7；#118 加场景 8；#2 加场景 10）

**输出契约（治理教义对齐）**：每场景 = DuckDB 引擎**固定预设 + 参数**（调用方传不了 SQL）；输出**风险线索** `RiskClue{scenario, siteCode, year, month, metricValue, metricLabel, threshold, thresholdLabel, evidence[], evidenceTotal, note}`——`evidence` 为行级证据锚点（`source_file + source_row + import_batch`，经 `GET /api/fees/batches/{id}/evidence?row=N` 可回读快照原文——#86 起该承诺有通道兑现，默认每线索 ≤10 条、上限 50；`evidenceTotal` 为该线索全量行数）。**账单存在 ≠ 认定结论**：一切输出是待核实差异 + 金额 + 证据。信封 `RiskResult{scenario, scenarioCn, feeType, year, month, clues, total, excluded, dismissed, note}`（`dismissed` = 本次被人工驳回而隐藏的线索数，#119）。

| 场景 id | 名称 | 判定口径 | 参数（默认值） |
| --- | --- | --- | --- |
| `retired-billing` | 退租后仍在计费 | 已确认关联的资源状态 ∈ 退用集合且该站址有费用记录（跨层：Go 侧组合 SQLite 状态 + 引擎费用面） | `status`（默认 `停用,退网`） |
| `duplicate-settlement` | 跨批次重复结算 | 同 (站址, 账期) 计费批次 >1（metric=batches） | 无阈值 |
| `ledger-mismatch` | 合同账单金额差异 | 同 (站址, 账期) 各批次合计之差超阈值（先按批次聚合再比——批次内拆分账单不误报） | `diffCents`（默认 0 = 任何差异） |
| `spike` | 电费环比异常上涨 | 单批次口径月度合计环比涨幅达阈值（上期 = 上一个有账单的月份）；**#11 补报标注**：账期内含购电周期 > 70 天（正常×2）的行时，线索 note 标注「疑似长期不报后集中补报」并携带最长周期天数——环比涨幅可能是攒报的计量后果而非趋势异常；标注而非排除，缺购电起止日列的旧批次退化为原行为 | `ratio`（默认 0.30） |
| `outlier` | 同类站月度金额离群 | 同期同费用类型单批次站址集合的**月度总额** z-score（双侧，符号在 metric）——**非单位成本口径**（#117：规模归一化需基数字段，当前数据面不存在，见下） | `z`（默认 3.0）、`minSites`（默认 8） |
| `unlinked` | 零资源计费 | 费用编码既无 confirmed 关联也无 pending 提案（dismissed 视为无匹配；跨层组合） | 无阈值 |
| `idle-billing` | 有费无量 | 已确认关联的站址在该账期窗口内**指标点数为零**且费用仍在流（跨层组合 #114：引擎费用面 × SQLite 指标面；窗口 = 账期自然月 [1日, 末日] 的 RFC3339 字典序；原始需求「停电/无业务量在账」的交叉验证落点） | 无阈值（零点是确定性信号） |
| `missing-billing` | 有量无费 | 已确认关联的**在网/在建**资源在该账期窗口内**有指标采集点**，而费用侧同账期无该类型计费记录（跨层组合 #118，场景 7 的镜像：SQLite 关联与指标面 × 引擎费用面反查；退休状态资源排除——不给他们计费是正确形态，场景 1 管反向；原始需求「漏收资损」的交叉验证落点） | 无阈值（点数>0 是确定性信号） |
| `ledger-consistency` | 台账内部一致性 | 六条确定性行级规则（#5+#8+#11）：reading-gap 读数差≠填报用量（一表多户排除）/ ratio-sum 四方分摊和≠1 / chain-break 跨账期读数断链（换表已标记不计）/ zero-quantity-billing 零用量计费 / purchase-period 购电周期>35天（长期不报+集中补报候选）/ receivable-unreceived 应收>实收（资金回收风险）；缺相应列的文件对应规则自动跳过（列探测守卫——纯旧代文件不报错） | 无阈值（规则内固定容差） |
| `custom-rules` | 可配置规则（#2 第一期：逐条执行该费用类型已启用的 L1 六元组规则 {度量 amount/rows, 聚合 sum/avg/max/count, 维度 site_month, 算子 gt/gte/lt/lte/mom_ratio, 阈值, 窗口 1-12}；SQL 构建前逐条重校验封闭枚举——规则文本永不进 SQL；线索携带规则名/严重度/阈值口径；规则经 MCP `fee_rules_create` 或 HTTP/CLI 创建（默认停用观察态）、人类经管理级路由或 CLI 启用；limit 为**每规则**上限——union 截断会静默丢弃后续规则的线索，违反 #83 教义） | 阈值即规则表内容（每规则六元组） |

**确定性裁决（#59 落地）**：`spike`/`outlier` 只在**单批次** (站址, 账期) 口径上计算——重复行令窗口函数与分布统计非确定；跨批次对被**排除并在信封 `excluded` 计数**（它们是 duplicate-settlement/ledger-mismatch 的线索域），绝不静默丢弃。`spike` 的单月查询收窄**线索窗口**不收窄环比基期（上期恒取全年序列的上一个有账单月）。z-score 用 `stddev_samp` 且同期同类站数 < `minSites` 时该期不出线索（小样本不产统计结论；数学上 n≤11 时单点 |z| 无法达 3，`minSites` 默认 8 + 场景内其余站址会抬高 n，仍以参数为门槛诚实声明）；**指标输出的跨运行稳定性**：DuckDB 向量化多项聚合按块序求和、原始浮点在末位 ULP 摆动——线索集恒确定，z 在 SQL 内取整到 10 位小数使输出字节稳定（比价类单次除法无此问题，不取整）（小样本不产统计结论；数学上 n≤11 时单点 |z| 无法达 3，`minSites` 默认 8 + 场景内其余站址会抬高 n，仍以参数为门槛诚实声明）。

**场景 7 的诚实注意义务（#114）**：`idle-billing` 交叉的是**两条独立台账**——费用面（Parquet：该账期有计费记录）与指标面（SQLite：关联资源在该账期窗口内零采集点）。零点数为确定性信号、无阈值可调；但线索 note 恒携带告诫：**无采集也可能是采集覆盖缺失或采集断链，与真正空置在该证据形态下不可区分**——线索仅供人工核实，不作空置/闲置认定。无 confirmed 关联的编码不在本场景测量（那是 `unlinked` 的业务）。

**场景 8 的诚实注意义务（#118）**：`missing-billing` 交叉的是同两条台账的**反向矛盾**——指标面说资源活着（账期窗口内有采集点），费用面却无该类型计费记录。点数>0 为确定性信号、无阈值可调；metric = 窗口内指标点数（`metricPoints`），`evidenceTotal` 恒 0 且锚点列表为空——**费用侧零行即异常本体**，回读通道指向快照层而非不存在的行。线索 note 恒携带告诫：**账期无记录 ≠ 漏收认定**——季度/年度结算、该费用类型尚未导入、结算主体挂账在其他编码，在该证据形态下全部同相；线索仅供人工核实。退休状态（默认 `停用,退网`）资源不在本场景测量（不给他们计费是正确形态；`retired-billing` 管反向），pending 关联的编码同样不在（人类尚未裁决）。

**跨层场景的空面前置检查（#27）**：「无法分析」与「分析后有发现」必须在输出上可区分（#21 原则的反方向——#21 治「全量排除伪装成干净」，#27 治「全量命中伪装成发现」）。四个跨层场景在**交叉面整体为空**时不产出线索，以 note + warnings 双通道说明原因与下一步动作（前端第四态空状态 `not-analyzed`，告警色渲染）：

| 场景 | 前置门 | 拦下时的理由 |
| --- | --- | --- |
| `unlinked` | 可关联资源（站址/机房/基站）计数 = 0 | 空资源库下全部编码必然「无关联」——203 条噪音会淹没真实发现；**部分导入照常分析**（导入多少是运营决策，系统不代判） |
| `retired-billing` | 无任何已确认关联 | 宇宙（confirmed 对）为空是空集而非「干净」，0 条线索必须说明为什么 |
| `idle-billing` | 全库无任何指标点 | 全空指标面下已关联站址必然零点——数据覆盖问题，非逐站发现 |
| `missing-billing` | 无已确认关联 / 全库无指标点 / 费用侧该类型账期零记录（三重门，最根本者先报） | 空费用面下全部有量资源齐发命中——查询未导入的账期与「全体漏收」在证据上同相，诚实答案是命名数据范围问题 |

**人工核实闭环（#119，migration v33）**：场景输出的是待核实线索，核实成果落库为**裁定行**（`risk_clue_decisions`，身份 = (scenario, fee_type, 站址, 年, 月)，无 FK——站址编码是代码空间事实，批次回滚不触碰，W005/W006 教义）。消费语义：**verified 线索保持可见并标记**（运营的已知问题台账）；**dismissed 线索默认隐藏、信封 `dismissed` 计数**——绝不静默丢弃（#83），重跑不再重复出现（#69 PairDecided 教义：已裁定保持已裁定）；`includeDecided` 参数返回全量标记集。裁定按 (scenario, feeType) 隔离，UPSERT 可复议翻转。写通道仅人类（HTTP POST + CLI `fees clue-decisions verify|dismiss`），**MCP 无写面**——AI 不得替人核实（attribute-changes apply / links confirm 同一治理红线）；`fee_risks` 只读面自动携带标记。

**默认构建诚实契约**：十场景与查询预设同走引擎面——未编译（默认构建）一律 501 `engine_not_built`（CLI exit 1 + 文案携带 Docker/WSL2 指引）；参数校验先于引擎检查（坏场景/坏类型/坏阈值 → 400/exit 2）。

### CLI
```
netopslab fees --db <path> --data <dir> risks <scenario> --type <t> --year <Y> [--month <M>]
    [--status 停用,退网] [--diff-cents 0] [--ratio 0.30] [--z 3.0] [--min-sites 8] [--limit 100] [--max-evidence 10]
```

### HTTP
`GET /api/fees/risks/{scenario}?feeType=&year=&month=&status=&diffCents=&ratio=&z=&minSites=&limit=&maxEvidence=`（契约见 api.md；过 v0.11-R13 令牌写门禁只拦写方法，本端点为 GET 只读）。

### 消费面（阶段 5，v0.15.0——三通道同契约）

- **Web 工作台 `/fees`**（嵌入式 UI 导航「费用分析」）：导入（multipart，201/200 双形态 toast）/ 批次列表 / 回滚（ConfirmDialog）；**查询预设面板（#121 浏览器通道补全）**——站址账期汇总与跨批次重复候选两表（整数分自动 /100；501 引导横幅；上限走 HTTP 500 诚实界）；风险十场景钻取（线索表 + 行展开证据锚点 + `excluded`/`dismissed` 计数 + 人工裁定按钮与决策 chip + 审计开关，整数分自动 /100 展示；501 `engine_not_built` 渲染为 Docker/WSL2 引导横幅；custom-rules 场景经规则管理面启用后生效）；**关联人工确认通道**（重新扫描六计数回执 + 状态/级别筛选 pending 优先 + 确认/驳回——驳回走 ConfirmDialog，配对不再自动复提 + 审计保留）。
- **MCP `fee_risks`**（tool #11，只读）：跑同一固定预设，但阈值恒用治理默认值（ratio=0.30 / z=3.0 / minSites=8 / status=停用,退网）——**AI 不可调统计阈值**（调参是人类通道 HTTP/CLI）；场景/费用类型/账期/条数（≤50）四参数；wire shape = HTTP shape（`fees.RiskResult` 原样透传）。工具清单见 [teleagent-integration.md](teleagent-integration.md)。
- **MCP `fee_totals` / `fee_duplicates`**（tool #12/#13，只读，#120）：聚合面对 AI 开放——各站址账期合计与跨批次重复候选，wire shape = HTTP shape（同信封原样透传）；条数治理边界与 `fee_risks` 同款（缺省 20、上限 50，比 HTTP 的 500 更紧）；`fee_totals` 是「这个站今年电费多少」类基础问句的入口，`fee_risks` 的线索下钻配对面。工具清单见 [teleagent-integration.md](teleagent-integration.md)。
- **CLI**：见 §6/§8 CLI 段。

## 9. 路线（#58 阶段表 —— 终局，跟踪 issue 已关闭）

| 阶段 | 状态 |
| --- | --- |
| 0 技术验证（#59） | ✅ v0.13.0 前（双引擎基准 + CGO + Parquet 实测） |
| 1 分层设计（#60） | ✅ v0.13.0（快照层 + 分区大表 + 血缘 + 批次共存） |
| 2 导入管道（#61） | ✅ v0.13.0（CSV/Excel → 快照 → Parquet，幂等/重试/回滚/GC） |
| 3 实体分级关联（#63） | ✅ v0.13.0 + v0.13.1（migration v30：exact/rule 自动确认 + fuzzy 人工提案流 + dropout 清扫 + 人类优先） |
| 4 六大风险场景规则 + 证据追溯（#64） | ✅ v0.14.0（六场景预设 + 行级证据锚点 + excluded 确定性裁决） |
| 5 MCP 工具面扩展 + 统一工作台入口 | ✅ v0.15.0（MCP `fee_risks` + Web `/fees` 工作台含关联人工确认，见 §8 消费面） |

## 10. 相关文档

- [data-model.md](data-model.md) —— v29（fee_batches）/ v30（fee_site_links）DDL；
- [api.md](api.md) —— 费用域 HTTP 契约全量（导入/批次/回滚/查询预设/关联/风险六场景端点）与 CLI 等价物；
- [teleagent-integration.md](teleagent-integration.md) —— MCP 工具清单（含 fee_risks）；
- #59 关闭评论 —— 引擎基准数字与设计纠偏（重复行共存裁决的来源）；#58 关闭评论 —— 阶段 0-5 全量收口记录。


## §12 原始台账目录的只读保护（issue #6，v0.18）

系统的证据链起点是**源文件未被改动**这一假设。本系统自身对源文件只读
（`os.Open` 读取 → 内容寻址快照副本），但无法阻止外部程序改动源文件。

**v35 迁移**给 `fee_batches` 增加 `source_path`（导入时实读文件的绝对路径；
空 = v35 前批次或无路径通道）。**`fees verify-sources`** 对每个批次把当前
源文件 SHA256 与导入时哈希对比，输出四类判定：

| 判定 | 含义 |
| --- | --- |
| `match` | 源文件内容与导入时一致 |
| `changed` | 源文件已被改动——结论以快照为准，需人工核实改动来源（退出码非零） |
| `missing` | 源文件已不存在（快照仍在，可恢复） |
| `unknown` | 批次无源路径记录（v35 前）——跳过而非猜测 |

每条结果同时核对**快照完整性**（`snapshotIntact`：快照文件仍存在且哈希与
导入时一致）。导入时源文件可写则结果 JSON 的 `warnings` 给出只读建议
（Windows 因无 POSIX 写位语义跳过该检查）。

**部署建议（原始台账目录设只读）：**

| 平台 | 操作 |
| --- | --- |
| Linux | `chmod -R a-w /path/to/ledgers`（目录需继续收新文件则只对历史月份子目录执行）；更强：`chattr +i`（root，防误删） |
| macOS | `chmod -R a-w /path/to/ledgers`；防删：`chflags uchg` |
| Windows | 资源管理器属性勾「只读」或 `attrib +R <文件>`；整目录用 `icacls <目录> /deny Everyone:(WD,AD)`（拒写拒删） |
