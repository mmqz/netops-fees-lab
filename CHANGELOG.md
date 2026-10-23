# Changelog

本项目所有显著变更变更记录于此。格式参考 Keep a Changelog；版本遵循语义化版本（SemVer）。

## [0.17.0] — 2026-10-23

**费用仓拆分**：本仓库自此从 [mmqz/netops-lab](https://github.com/mmqz/netops-lab) fork 而来（fork 时点 v0.16.1 / 2223101），定位为**纯费用域**仓库——IPRAN 网络域（仿真工作台/拓扑图查询/告警检测/维护窗口/webhook 通知/身份治理/治理总览/资源导出/PTP 配置脚本）整面移出，留在主仓演进。决策与裁剪清单：[docs/fees-repo-split-plan.md](docs/fees-repo-split-plan.md) + [docs/fees-split-review.md](docs/fees-split-review.md)（评审 A–E 五项修订全部落实）；主仓侧移除红线见 [docs/fees-code-removal-plan.md](docs/fees-code-removal-plan.md)。

### 移除（IPRAN 域整面）
- 整包删除：internal/sim（5,330 行）、internal/topology（1,005 行）、internal/detect（1,272 行）、internal/notify、internal/renames、internal/changes、internal/governance、internal/exporter、internal/ptpconfig。
- MCP 工具面 13 → 4（砍 lab 四件套 + 图三件套 + alerts/metrics 查询；保留 resource_search + 费用三件）。
- HTTP 路由 86 → 40（砍 graph 四条 / run-state 两条 / detection 六条 / maintenance 三条 / alerts 四条 / exports / governance 总览与拓扑检查 / attribute-changes 三条 / renames 三条 / lab 二十条）；api.md 契约基线同轮重写（46 个死路由段落清除，双向门禁钉死）。
- CLI 子命令 20 → 13（砍 synthdata/detect/export/query/changes/renames/lab）；serve 移除 --webhook-url/--detect-interval。
- importer 断开治理通道：改名提案检测（rename.go）与属性变更提案记录（recordAttributeDiff）不再生成（评审 B 项）；批次回滚不再计数治理提案。
- 前端：13 页面 → 7（砍 alerts/rules/maintenance/lab×2）；资源抽屉收敛为 属性/来源/性能指标 三页签；概览移除治理面板与告警统计卡；MCP 描述与 i18n 文案随行重定基线；离线地图资产（DEM 瓦片/真实网络图层）移除。
- 仓库卫生：.github/workflows 与 CI 相关脚本移除（本仓测试全本地）；IPRAN 浏览器回归/维护窗口/webhook 回归脚本移除。

### 保留（费用线全量依赖，评审修订 A–E）
- internal/repo 存储层 + **迁移链 v1–v34 完整保留**（评审 A 项红线级：迁移文件与其测试原样保留，历史库可无冲突升级）；v11/v12/v17/v18/v22-v28 的表与访问器作为迁移链验证面共存（业务层零引用）。
- internal/metrics 指标摄入面按数据源依赖豁免（评审 C 项）——双账本交叉场景的氧气。
- internal/importer 保留为资源宇宙建设通道（评审 B 项）。
- 门禁分仓重定（评审 E 项）：api-routes 40 / api-fees-routes 14 / mcp-tools 4 / repo-migrations 34 / resource-types 33；go 顶层测试 438、包 16、vitest 129（复算命令见 docs/facts.md §3）。

### 验证（拆分标准五条全过）
- `go build ./...` + `go test ./...` 16 包全绿；`-tags duckdb` 全量 16 包全绿。
- 费用导入 + 8 场景 + 双账本交叉功能不变（e2e 全绿 + 实机 spot：spike 环比识别/合计/totals/link-scan）。
- IPRAN 治理面业务代码零残留（grep 验证；迁移链与指标摄入按评审豁免）。
- 空库迁移 v0 → v34 实机通过。
- 前端 tsc+vite 构建通过、vitest 129 全绿、/fees 工作台可访问。

## [0.16.1] — 2026-10-23

**质量与负载免疫加固版**（#124 关闭收口 + 性能门禁 CPU 化 + Windows 回滚竞争容忍 + 零覆盖生产路径两批清零；纯加固无功能面变化，patch 发布）。

### 修复 — 性能门禁负载免疫化（#82 教训第二版）
- **实测证据**：同一份 v0.16.0 代码，模糊扫描性能门禁在本机空闲 1.38s、`go test -cover` 并行竞争下 15.2s——墙钟摆幅 11×，零代码变化的假红。4 个抢核进程复测：墙钟 1.38→4.19s（3×）而 **CPU 时间 2.067→2.086s（0.9%）**。
- `testsupport.CPUTime()`：Unix `Getrusage(RUSAGE_SELF)` / Windows `GetProcessTimes` 双平台（纯 stdlib syscall，零新依赖，交叉编译验证）；fees 模糊门禁 8 CPU-s（优化路径 2.07 / 朴素底线 15.5+，两向余量 3.9×/1.9×）；sim PERF-P2 四门禁 4 CPU-s（实测 0.4–8.9ms）。墙钟改记日志、永不门禁。

### 修复 — 回滚隔离 rename 的 Windows 竞争容忍（#124 同类）
- `quarantineFile`（live→隔离区）暴露于并发 DuckDB 查询持有 live 分区文件的毫秒级窗口（Windows `os.Rename` 对被持有文件答 ACCESS_DENIED 而非 POSIX 原子覆盖）→ `renameWithRetry` 有界重试（25ms/100ms 退避，最坏增时 125ms）；ENOENT 快速失败（幂等语义答案），持续失败原样透传给既有 abort/undo（#107 语义不变）。
- 暴露面精确分析留档：commit rename 目标含唯一 batch-id 无预存、restore 目标槽位不存在——仅 quarantine 一处需要容忍。seam 注入测试在 Linux 钉死三条重试策略 + 穿窗落位 + ENOENT 幂等不回归。

### 加固 — 零覆盖生产路径两批清零（全局跨包覆盖 78.0%）
- **第一批**：`importer.AnalyzeOffline`（CLI analyze 全部工作——真工作簿无副作用/缺文件/垃圾字节）；`topology.deepestNode`（§10.3 决胜梯：深度→层→类型→自然键五子测试）；`NoMatchingTemplateError.Error` 双拒答码文案；metrics `Basename`/`joinKeys`/`ValidScenario`。
- **第二批**：fees std 引擎 9 方法 501 合约全钉死（引擎级直调永不假装空结果）；`readSnapshotRowXLSX`（真实台账即 .xlsx——导入→EvidenceRow 表头+数据行逐字校验+越界 not-found）；domain 闭集四组（RunSource/WebhookDeliveryStatus/ClueDecisionStatus/metrics 三枚举的 IsValid+Parse 全对）；`statusRecorder.Flush/Unwrap`（流式端点降级面）；exporter `assignSplit` 四案例；fees 小助手三件。
- **repo 共用底座死代码清扫**：7 个零调用函数移除（feeBatchScanTargets/deliveryJoinOf/FeeSiteLinkRepo.DeleteByID/Layer×2.DeleteByWorkspace/MetricBatchRepo.SetMapping+Count），跨包覆盖 80.4%→81.1%；`LatestPointOfSeries` 补测（#95 尾查优化 0%→80%，生产路径 services/graph.go）。
- **测量方法论修正**：缓存包不重跑导致覆盖块缺失 → 函数假 0%；强制 `-count=1` 后的真实清单驱动第二批选题。CLI cmd* 函数为子进程 E2E 盲区（已由 E2E 覆盖），不计缺口。

### 修复 — api 包 duckdb-tag 全量首跑
- 验证矩阵真发现：`-tags duckdb` 下 api 包全量首次运行即红——`TestAPIFeesClueDecisionEndpoints` 的 includeDecided=1→501 断言 tag 无感知（#59 验证当年只跑 fees 包）。按同文件既有 `engine.Available()` 房式分支修复：默认构建断 501、duckdb 构建断 200。

### 验证矩阵（2026-10-23，linux/amd64 · 2 核沙箱）
- `go test -race -count=1`：**25 包全绿、零数据竞争**（含全部并发上传/费用并发/引擎并发压力测试）；
- `CGO_ENABLED=1 go test -tags duckdb`：25 包全绿（api 包首次）；
- 交叉编译：windows/amd64 + darwin/amd64 + linux/arm64 全过；
- vitest：188/188（13 文件）；gofmt/vet 全仓清零。

### 工程卫生
- #124 关闭（三类修复逐条核验 + Linux 全量绿留档，Windows 复测欢迎 reopen）；
- 本轮新增文件 gofmt tab 归一（提交门禁复盘补课）；
- 测试计数 655→681（顶层函数口径，facts.md §3 与 README 同步）。

## [0.16.0] — 2026-10-23

**真实台账形态落地 + 趋势预测还愿 + Windows 三类修复版**（#125 P0 多 sheet 台账全链 + #123 ADR-0011 D4 预测 + #124 三根因修复；v0.15.2 后 main 十二个提交的 minor 发布）。

### 功能 — 多 sheet 台账导入（#125，P0，migration v34）
- 真实月度台账是一个工作簿多张 sheet（电费/铁塔租费/修理费各一张）——v34 前的通道只读第一张且静默丢其余（P0 实测三分之二数据消失零警告）。**v34**：`fee_batches` 加 `sheet` 列（默认 ''）并重建幂等唯一索引为 `(sha256, sheet, fee_type, 账期, 映射)`——历史单 sheet 批次键逐字节保留。
- `ImportSheets` 通道（#125 方案 A+C 全量）：每 sheet 按自身证据判定类型（表名 +2/表头 +1，唯一最高分）、全部 sheet 先分类后落地（不可判定全拒、落地数=0）、装饰性 sheet 按名列出、证据锚点带 `#<sheet>` 后缀；`crossCheckDeclaredType` 拒声明与表头矛盾（换 feeType 污染账本不可能）；`refuseModeConflict` 双向互斥（双通道幂等键形态不同，都落地必重复入账）。修掉 WIP 的 mode 参数自拒 bug。
- 消费面三件：HTTP `POST /api/fees/imports/sheets`（api-routes 84→86、fees 13→14）、CLI `fees import-sheets`、web 费用页「导入模式」开关（.xls 陈旧文案一并修正）。验收钉死：塔租分区金额=铁塔租费 sheet 的 5000.00，绝无电费 sheet 的 12345.67。

### 功能 — 指标趋势预测（#123，ADR-0011 D4 最后未还承诺）
- `internal/metrics/predict.go`：日均值最小二乘线性趋势（斜率/截距/残差 SE/斜率 SE）+ N 天到阈值（Direction→advisory bound：higher_is_better→min/lower_is_better→max；neutral/无界/远离/零斜率→null 诚实留白；已越限→0 天）+ 80% 预测带（1.282）；**<14 天拒算**（D4 原话「数据不足时上预测是自欺」）；note 恒为「外推参考，非承诺」。Ceil 带 1e-9 epsilon 卫兵（浮点噪声不许多诺一天）。
- 消费面：HTTP `GET /api/metrics/forecast`、CLI `metrics forecast`、MCP `metrics_query` 可选 forecast 模式（**不加第 14 个工具**，D5 冻结红线不破）。「劣化→保养」故事线（§10）有终点：检测报今天，预测报还剩几天。

### 修复 — Windows 全量测试三类根因（#124，实测 5/5 必现的 500 + 自相矛盾门禁 + 句柄泄漏）
- **产品缺陷**：`storeUpload`（三通道共用）`os.Rename` 在 Windows 目标被读时答 ACCESS_DENIED 而非原子覆盖——并发同内容上传 100% 500。方案 A+：rename 失败校验目标（大小+全量重哈希逐字节一致才认赢者的文件；撕裂残骸绝不放行）；`WriteSnapshot` 同族竞争面同修。
- **测试缺陷**：api.md 文档门禁正则不排 CR——Windows 检出的 CRLF 让 `GET /api/health` 同被判「缺失」+「多余」。双保险（TrimRight + 字符集排除 
）+ CR 自检测试；`.gitattributes`（`* text=auto eol=lf`）从仓库层封死 #118/#124 两代 CRLF 伤。
- **真句柄泄漏**：`readBatchSiteCodes` 的 os.Open 在 parquet-go 构造器对损坏文件 panic 时永不关闭（defer 注册在构造器后）——实测 "batch-*.parquet 正被另一进程使用" 的真凶。defer 提前；全仓四处调用点审计。TempDir 清理切 `testsupport.TempDir` 重试容忍封装。

### 归档 — DuckDB 技术验证证据（#59 spike 合入）
- `spikes/duckdb-bench`：237 万行六场景双引擎基准（DuckDB ×2.5-×53）、Parquet 分区裁剪 ×2.4、go-duckdb/v2 CGO 17s 构建、重复结算行使窗口函数非确定（支持 #60 显式处理决策）。独立目录+嵌套模块，主模块零触碰。

## [0.15.2] — 2026-10-20

**发布渠道修复 + 跨平台路径契约版**（#74 发布断档第二轮修复 + #75 P1；v0.15.1 后 main 上八个提交的 patch 发布——发布渠道自 v0.8.0 起停摆九个版本，本版把渠道修到可下载、可复用、带引擎）。

### 修复 — Windows 持久化路径破坏跨平台契约（#75，P1，migration v31）
- `fee_batches.snapshot_path` 与 Parquet 分区清单是**逻辑路径契约**（持久化 + API `snapshotPath`/`files[]` + CLI JSON 逐字回显，"相对 data 目录"），却用 `filepath.Join` 生成——Windows 上产反斜杠，`TestAPIFeesImportJourney` 稳定失败而 linux CI 永远看不见。写侧改 `path.Join`（恒 `/`），磁盘拼接在唯一边界 `filepath.FromSlash`；**migration v31** 纯 UPDATE 归一存量（`replace('\','/')`，幂等；Windows 旧数据的每个反斜杠都是分隔符——OS 禁止文件名含反斜杠）；测试侧同步修掉**镜像缺陷**（parquet 布局断言的期望值原用 `filepath.Join` 构造——Windows 上期望值自己变反斜杠）。新增 `TestSnapshotPathIsLogicalContract` 与 `TestMigrationV31NormalizesFeeSnapshotPaths`；22 处 max-version 断言随 v31 前进（既有惯例）。

### 修复 — 空分析目录答 500 而非诚实空结果（#72，P2）
- `feeTableGlob` 在空目录时让 DuckDB 抛 IO 错（API 500），fresh install 的 MCP/API engine-state 测试对空库答非所问；修复后 `analysisEmpty` 短路为诚实空列表。**全包 `-tags duckdb` 构建盲区**一并补门禁（此前只有单包 duckdb 构建，多包编译错误漏网）。

### 测试 — 两条测试底座（#70 / #73）
- xlsx 读取通道首个 reader 契约测试（first-sheet、证据行号、全切片导入——9ecb3bd）；r7 回执六格化断言收口（计数 + 六标签集双断言——ddd9c3e）。

### 文档 — 三处漂移修复（#71）
- §19.2 幂等键集合停在 v0.15.1 前、README 快速开始计数停在 v0.8.1、onboarding 缺费用域——26521ee。

### 修复 — CI 静默断档（#76，工程发现）
- 发布前核对发现 **runs 30–134 全部 failure**：两段叠加——①2026-10-12 起 Actions 分钟配额耗尽（v0.4–v0.6 迭代烧光当月额度，run 有 job 壳但 runner_id=0 三秒死，v0.6.x–v0.14.0 全部"CI 验证"实为空转）；②389fc10 起 duckdb job 的 step name 写了 `#72:`——YAML 把 plain scalar 中的 ` #` 当注释起点，**整个 workflow 文件解析失败**（之后每次 push 连 job 壳都没有）。②已修（step name 无 `#` 无 `: `，文件内 NOTE 防回归；修复后 run 恢复 4 job）；①待 owner 充值/等配额刷新（本版以本地门禁为准：25 包 ok / 0 FAIL、gofmt 空、vet 0、tsc 0、vitest 40/40）。诊断全程证据链见 issue #76。

### 修复 — 发布断档第二轮（#74，发布工程）
- **现象**：Release 下载渠道停在 v0.8.0——v0.9.0 Release 空壳（有正文零资产）、v0.10.0–v0.14.0 八个 tag 无 Release、v0.15.0/v0.15.1 连 tag 都没打。费用风险六场景整个里程碑对"只下载产物"的运营者不可见。
- **修复**（七项）：①本 v0.15.2 patch 收口 main 全部未发布修复；②`scripts/release.sh` 参数化发布（TAG/名称/正文文件/资产清单，取代 TAG 硬编码 v0.8.0 的一次性 w29 脚本——不可复用正是断档根因之一）；③v0.15.2 Release 三资产：source + linux-amd64 默认构建 + **linux-amd64 duckdb 构建**（费用引擎首次随渠道分发——此前 fee_risks 在所有下载产物上 501 engine_not_built）；④下载产物核对（`serve` + `/api/health` 实报 v0.15.2，duckdb 产物另核 fee_risks 应答）；⑤v0.9.0 空壳补齐双资产；⑥v0.15.0/v0.15.1 补 tag（b5c96cb / 231e0a1）+ v0.10.0–v0.15.1 十个 tag 各建 source-only Release，另顺带补齐 #14 时代遗留的 v0.4.0–v0.6.2 五个空洞与缺失的 v0.6.1/v0.6.2 tag——**渠道自 v0.1.0 起 27 个版本零空洞**（历史版二进制不回补——#14 先例）；⑦verified-capabilities 增"发布断档修复（#74）"行 + README 快速开始增 Release 下载入口。

## [0.15.1] — 2026-10-19

**分级关联复提修复版**（#69 修复 + #67/#68 文档与测试收口；v0.15.0 后三个提交的 patch 发布）。

### 修复 — 人工确认过的配对重扫不再复提（#69，P1 行为缺陷）
- **现象**（r19 费用域回归脚本首轮 F6b 即抓到）：人工 confirm 过的 (站址编码, 资源) 配对，每次重扫都**再冒一条新 pending 提案**——dismiss 过的配对正确地永不复提，confirm 过的没有。`ConfirmLink` 对重复提案 409（一码一站守卫），重复提案就此永久滞留 pending 队列：每扫一次多一条噪音，躺在人工刚决策过的关联旁边。单元测试只钉了「扫描不覆写人工行」，从未钉「同对不再提案」——v0.13.0 起每轮测试全绿但缺陷在场。
- **根因**：`PairDecided`（fuzzy 提案过滤）的状态闭集 `IN ('pending','dismissed')` 漏了 `confirmed`。
- **修复**：闭集 → `('pending','dismissed','confirmed')`——「每个已决策的配对保持已决策：确认不是邀请重新提问」。安全性：PairDecided 只拦 fuzzy 提案路径，exact 命中短路不经过它；auto 行经 dropout 清扫后行已不在表，不产生假阴性。
- `TestLinkHumanConfirmation` 显式钉死 confirmed-pair 不复提不变式。

### 新增 — 费用域端到端回归脚本 r19（#69 本体）
- `scripts/r19_fees_regression.sh`：29 断言（F1-F12）——发布二进制驱动：sites 资源导入（真实键 ST1603990225）→ 费用 CSV 导入（行级账期）→ 幂等重导（existingBatch 指向）→ 批次共存 → link-scan 计数（exact=3/fuzzy=2/unlinked=1）→ confirm/dismiss 旅程 → 重扫（humanKept=1 且 pending=0）→ 默认构建诚实拒绝（exit 1 + 真实错误文案）→ duckdb 段（跨批聚合 173456 分/duplicate-settlement 证据 2 行跨 2 批/unlinked 线索；无 CGO 环境显式 SKIP）→ 回滚（Parquet 删 + 快照留 + 键释放重导成功）。**本脚本首轮即抓到上述 P1**——冒烟层与单元测试的分工边界实证（#65 三处 P1 同为真旅程发现）。

### 测试与文档
- #68：/fees 金额展示纯逻辑抽出 `fees-format.ts` + 34 表测（vitest 6 → 40——同值不同标签的语义分叉钉死：`formatRiskMetric(123456,'totalCents')=¥1,234.56` 而 `(123456,'batches')=123456`）。
- #67：fees-layer.md 阶段表滞后两个版本收口（§7 补 risks 端点行/§9 阶段 4/5 终局/§8 消费面小节/§10 指针修正/README links 段补 Web 通道）。
- 门禁：gofmt 空 · vet 0（默认 + duckdb 双构建）· 全包 ok · **r19 PASS=29 FAIL=0**（含 duckdb 段）· vitest 40/40 · tsc 0。
- 版本事实点 5 处对齐 v0.15.1，`TestAppVersionMatchesChangelogHead` 钉死。

## [0.15.0] — 2026-10-19

**费用工作台收口版**（#58 阶段 5「MCP 工具面扩展 + 统一工作台入口」全量落地，跟踪 issue 关闭——全阶段路线 0-5 收口）。v0.14.0 把六场景能力建在后端，本版把它交到运营者手里：**AI 通道**（fee_risks 工具）、**浏览器通道**（/fees 工作台：导入/批次/回滚/风险钻取/关联人工确认——模糊提案此前只能 CLI 确认，综维人员实际所在的工作面无入口）、**文档通道**（工具面事实口径统一，#66）。

### 新增 — MCP `fee_risks` 只读工具（#58 阶段 5，tool #11）
- 固定预设 + **治理默认阈值**（ratio=0.30 / z=3.0 / minSites=8 / status=停用,退网）——AI 只可选场景/费用类型/账期/条数上限（缺省 20、上限 50，工具面比 HTTP 更紧），**不可调统计阈值**（调参是人类通道 HTTP/CLI——「AI 不可自行放宽离群线」是治理红线，非技术限制）。
- 线索携带行级证据锚点（sourceFile + sourceRow + importBatch 可回读快照原文）；MCP wire shape = HTTP shape（`fees.RiskResult` 原样透传，一契约两传输）。
- `TestMCPToolRegistry` 断言 **11 工具封闭集**（5 资源/仿真域 + 5 图分析域 + 1 费用风险域）；默认构建报 `ErrEngineNotBuilt`（诚实契约，导入/快照/Parquet 不受影响）。

### 新增 — Web 费用工作台 `/fees`（阶段 5 的统一入口承诺）
- **导入/批次/回滚**：multipart 上传（201 新导入 / 200 幂等重复双形态 toast）；批次列表（类型/账期/行数/状态 chip）+ 回滚 ConfirmDialog（删分析行、保快照证据文案同 CLI 教义）。
- **风险六场景钻取**：场景/费用类型/账期/上限 → 线索表（站址/账期/触发指标——整数分金额自动 /100 展示/显式阈值）+ 行展开证据锚点清单（sourceFile:sourceRow + 金额 + 批次，超量「另有 N 行」）；`excluded` 跨批次排除计数明示；501 `engine_not_built` 渲染为 Docker/WSL2 引导横幅而非裸错误（运营者可自救，不需要读后端日志）。
- **费用-资源关联人类确认通道**（阶段 3 能力的工作台补全，f5974d5）：重新扫描（六计数回执 codes/exact/rule/fuzzy/unlinked/humanKept）+ 状态/级别筛选（pending 优先缺省——打开页面即待办队列）+ 确认（直接执行 + toast）/ 驳回（ConfirmDialog——配对不再自动复提 + 审计保留，值得二次确认）；相似度百分比 chip、规则名标注、来源（自动/人工）列。

### 修复 — 文档漂移（#66）
- **14 处「MCP 工具面冻结恒 5 个」过时表述清零**（api.md ×6 / import-and-validation.md ×4 / data-model.md ×3 / verified-capabilities.md v0.6.0 历史行保留原文 + 演进注记）：v0.9 扩容 +5、v0.14 +fee_risks 后实际 11 个。数量冻结声明改为**「11 个全部只读」属性声明**——冻结红线本义是「无 SQL/Shell/SSH/文件等越权工具」而非「数量恒定」，数量冻结句子是个自我贬值的句子（每加一个工具就多一处撒谎的文档）。

### 测试与门禁
- f51a7c2：`TestMCPFeeRisks`（场景/类型校验错误词汇表、year 必填、默认构建 engine-not-built refusal）；工具面封闭集断言 10 → 11。
- 门禁：gofmt 空 · vet 0（默认 + duckdb 双构建）· 25 包全 ok · 顶层测试默认构建 566 + duckdb 构建 fees 33 · vitest 6/6 · tsc 0。
- 版本事实点 5 处对齐 v0.15.0（domain.AppVersion / web APP_VERSION / README MCP 版本行 / api.md 契约基线行 + health 示例），`TestAppVersionMatchesChangelogHead` 钉死。

## [0.14.0] — 2026-10-19

**费用风险六场景版**（#64 = #58 阶段 4 全量落地；需求驱动为架构讨论中确定的六大风险场景原始清单）。六场景全部升为**风险规则 + 证据追溯**的正式输出——每条线索携带触发数值、显式阈值与**行级证据锚点**（source_file + source_row + import_batch，可回读快照原文），输出恒为待核实线索、绝不作认定结论（账单存在 ≠ 重复付款认定）。

### 新增 — 六大风险场景（#64，CLI `fees risks` + HTTP `GET /api/fees/risks/{scenario}`）
- **场景与判定口径**（全为固定预设 + 显式参数，调用方传不了 SQL）：`retired-billing` 退租后仍在计费（已确认关联的资源状态 ∈ 退用集合且费用仍流入——**跨层 Go 组合**：SQLite 状态 × 引擎费用面，无跨引擎 ATTACH，默认状态集 `停用,退网` 参数化）；`duplicate-settlement` 跨批次重复结算（同 (站,期) 批次 >1，metric=batches）；`ledger-mismatch` 合同账单金额差异（各批次**合计**之差超 `diffCents`——先按批次聚合再比，批次内拆分账单不误报；合同/账单两类台账作为不同批次导入即为此形态，口径文档诚实声明）；`spike` 电费环比异常上涨（单批次口径月合计环比涨幅 ≥ `ratio`，上期 = 上一个有账单的月份）；`outlier` 同类站单位成本离群（同期同费用类型单批次集合 z-score 双侧离群 ≥ `z`，样本数 ≥ `minSites` 才出线索）；`unlinked` 零资源计费（费用编码 confirmed+pending 关联面为空；dismissed 视为无匹配）。
- **确定性裁决（#59 落地）**：`spike`/`outlier` 只在单批次 (站,期) 口径上计算——重复行令窗口函数与分布统计非确定；跨批次对**排除并在信封 `excluded` 计数**（它们是场景 2/3 的线索域），绝不静默丢弃；`spike` 单月查询收窄线索窗口不收窄环比基期。z 用 `stddev_samp`（确定性聚合）。复跑字节一致由 duckdb 引擎测试钉死。
- **输出契约**：`RiskClue{siteCode, year, month, metricValue, metricLabel, threshold, thresholdLabel, evidence[], evidenceTotal, note}`（金额整数分——0.13.1 契约延续）；证据锚点默认每线索 ≤10（上限 50），`evidenceTotal` 全量行数；信封含 `scenario/scenarioCn/note` 口径声明与 `excluded`。
- **诚实契约**：六场景与查询预设同走引擎面——默认构建一律 501 `engine_not_built`（CLI exit 1 + 指引文案）；参数校验先于引擎检查（400/exit 2）。阈值默认值全文档化（fees-layer.md §8），零魔数。
- **修复过程中发现的实现缺陷**（均在本轮内修）：hive 分区列 DuckDB 按字符串读出——算术/过滤需显式转型（`year*12+month` 排序与单月过滤加 `::BIGINT`）；`spike` 首版把单月过滤误作用于环比基期（per-CTE 恒取全年，月份只收窄线索窗）；`outlier` JOIN 双侧同名列需限定。

### 测试
- Go 顶层测试 563 → **576**（+13：场景枚举/默认值钉死、默认构建六场景诚实契约、跨层组合（状态过滤/关联语义/证据管道）fake 引擎测试、duckdb 引擎六场景端到端（线索集/整数分指标/显式阈值/排除计数/证据锚点/复跑确定性）、HTTP 契约（8 组 400 + 六场景 501）、CLI e2e（usage ×5 + 六场景 exit 1 + 指引文案））。门禁：gofmt 空 · vet 0（默认 + duckdb 双构建）· 全包 ok · vitest 6/6 · tsc 0。

## [0.13.1] — 2026-10-19

**费用分析层实测修复版**（issue #65 独立实测三处 P1 + 一处 P3 的闭环）。金额契约换轨整数分、模糊关联扫描换轨有损零算法路径并并行化、Windows 查询路径补官方替代——三处均为对账级正确性 / HTTP 可用性 / 平台可达性的实缺陷，非打磨。

### 修复 — 金额全链路 float64 → 整数分 int64（#65 P1-a）
- **根因**：金额走 `strconv.ParseFloat` + Parquet `amount` float + DuckDB `sum(amount)` + JSON `total` 浮点链——实测 100 万次 0.01 累加漂移 0.00000017 元、不同求和顺序差异 7.5e-9 元。单笔误差极小，但**对账取整到分差 1 分钱即异常**、重复结算比对可能产出假线索、#59 双引擎 0.07% 计数差同根。与同文件站址编码「前导零保留、刻意不数值化」的精度纪律形成反差。
- **修复**：`NormalizeAmount` 定点逐位解析（不经 float64；至多两位小数，第三位 = 行失败**绝不静默取整**；科学计数法拒收；逐位溢出守卫，上界 = int64 分精确天花板）；Parquet 列 `amount` → **`amount_cents` int64**；DuckDB `sum(amount_cents)`；JSON `total` → **`totalCents` 整数分契约**（展示层 /100）。**升级注意**：v0.13.0 批次的 Parquet 文件列名为 float `amount`——回滚旧批次重导（快照层保有原件）；文档两处声明。
- 实测钉死：100 万行 0.01 累加 == 10000.00 **精确**；`TestCentsAccumulationIsExact`；边界（.5 / 5. / 0 / 负值 / 全角混合 / MaxInt64 天花板 / 溢出 / 三位小数拒收）表测。

### 修复 — 模糊关联扫描 O(codes×candidates×len²) 超时风险（#65 P1-b）
- **根因**：`topFuzzy` 对每个费用编码遍历全部候选算全量 Levenshtein（外推 1 万编码 × 1,364 候选 = 15.5 秒；资源库到 1 万对象 = 1 亿次编辑距离 = 数分钟至十分钟，`POST /api/fees/links/scan` 必然超时）；`sweepAutoDropouts` 全表加载内存过滤；每次写一个 `db.Do` 事务（万级 = 万级单句事务）。
- **修复（全部无损——接受/拒绝判定与朴素路径逐位一致，等价性测试钉死）**：①**长度预筛**（dist ≥ |lenDiff|，`1−|lenDiff|/maxLen` 用朴素表达式本身求值，阈值不可达即跳过）；②**字符多重集切**（单次编辑改变计数向量 ≤2 → Σ|Δcount| > 2·cap 必拒，ASCII 计数器）；③**带限 Levenshtein**（|i−j| > cap 带外格钳到 cap+1，带宽 2cap+1）+ **行最小值早夭**（行最小非降，超 cap 即终）；④**decide 阶段并行化**（GOMAXPROCS 工人池按索引回收——决定论零破坏，写阶段按序重放）；⑤规则级查找索引化（`linkRules` 改纯键变换 + 首胜确定性索引，替代每码线性扫）；⑥**清扫 SQL 下推**（`DeleteAutoNotIn`：keep 集进连接内 TEMP 表 + 单条 `NOT IN (SELECT…)`——分块 NOT IN 是**错误**而非仅低效：第二块只保护自己那片、会重删前块保留的码；人类决策行幸存语义不变）；⑦写事务分块 500/批。
- 实测：10,000 编码 × 1,000 **同长度类对抗语料**（长度切全失效，只考验 DP 侧）并行扫描 **1.61 秒**（#65 验收线 2 秒内；朴素外推 15.5 秒，≈10×）；等价性语料（中英文/等长/阈值边缘/空串）逐位一致；race 0（性能断言在 race 插桩下跳过——墙钟无意义）。

### 修复 — Windows 查询路径与指引（#65 P1-c）
- **根因**：go-duckdb 静态绑定需 Windows Restart Manager（`-lrstrtmgr`），mingw-w64 不提供——Windows 原生 `go build -tags duckdb` 链接失败，run.ps1/Makefile 均无 duckdb 路径，用户无官方途径获得可查询费用构建（实测撞 501）。
- **修复**：①**Docker 镜像默认含 duckdb 引擎**（golang:bookworm CGO + `-tags duckdb`，运行时补 libstdc++6；`--build-arg BUILD_TAGS=""` 可退化纯 Go）；②`make build-duckdb`（linux/WSL2/macOS）；③fees-layer.md §5.1 Windows 查询路径专节 + `run.ps1` 头部指引；④`ErrEngineNotBuilt` 错误文案直接携带 Docker/WSL2 一行命令（CLI 与 501 响应体即指引）。Windows 原生导入/快照/Parquet 写入/关联扫描本就全功能（纯 Go）。

### 修复 — 站址编码英文表头识别（#65 P3-d）
- 识别器折叠匹配键（小写 + 去空格/下划线/连字符/全角空格）：`site_code` / `Site Code` / `SITE_CODE` / `siteCode` / `station_code` / `cell_code` / `amount` / `period` / `billing_date` 等英文表头全自动识别；中文同义词保持原序原判定（英文尾项永不夺走已命中的中文建议——混合表头实测正确）。费用类型标记同享折叠匹配。

### 测试
- Go 顶层测试 549 → **563**（+14：整数分表测/累加精确/溢出边界、等价性语料、性能基准（race 跳过标志文件一对）、清扫语义（503 行为修正为 502 的正确期望 + chunk 边界 500/400 + 空清空集 + 人类决策幸存）、英文表头识别）；门禁：gofmt 空 · vet 0（默认 + duckdb 双构建）· 全包 ok（webui 包 mapdata 两测为**预存环境失败**——dist 需 `make web` 重建后才含 mapdata，与本次改动无关，干净 origin/main 复现一致）· `-race` fees/repo 0 · vitest 6/6。
- 既有断言零放宽；JSON 契约变更（total→totalCents）与 Parquet schema 变更（amount→amount_cents）在 api.md / fees-layer.md / 错误文案三处同步声明。

## [0.13.0] — 2026-10-19

**费用分析层版**（#58/#60/#61 阶段 1/2 垂直切片 + #62 letterbox 护栏修复 + 仓库首个前端单测门禁。两层存储裁决落地：状态层 SQLite 不动、费用数据本体永不进 SQLite；快照层文件系统内容寻址、分析层 Parquet hive 分区大表 + DuckDB 查询引擎）。

### 新增 — 费用分析层（#58 跟踪 issue 的阶段 1/2，migration v29）
- **三落地**：①**状态层** `fee_batches`（migration v29）——批次状态机 completed/failed/rolled_back + **幂等键 = (文件哈希 × 费用类型 × 账期 × 列映射) 的部分唯一索引**（`WHERE status='completed'`）——failed/rolled_back 不占键，#61「转换失败重试不产生半写状态」由构造保证，`MarkCompleted` 即提交点；②**快照层** `fee-imports/<type>/<year>/<month>/<sha256>_<原名>` 内容寻址不可变（临时文件+原子 rename 落盘；回滚删分析行但**快照保留**——证据寿命 > 批次寿命，与资源导入器保留工作簿同语义）；③**分析层** `analysis/fees/fee_type=*/year=*/month=*/batch-<id>.parquet` hive 分区大表——分区列只在路径；文件列 = site_code / amount / **source_file + source_row（行号级证据对位）+ import_batch（批次血缘）**；**同键多文件批次共存不合并**（#59 验证的意外发现写入设计：重复行令窗口函数非确定——重复行必须在标准化阶段显式共存，重复结算检测查它们而不是丢它们）。
- **行级账期**：每行自己的 period/date 列优先于批次声明账期（跨月文件按行拆分区）；垃圾账期 = 行失败（计数非致命），绝不静默回退——分区粒度是整个分析层的前提。金额规范化（¥/￥/千分位/全角数字）；站址编码前导零保留（身份字符串绝不数值化）。
- **查询引擎 DuckDB（CGO 隔离）**：写入纯 Go（parquet-go，无 CGO，全平台——Windows CI 照常全绿）；查询引擎 go-duckdb v2.4.3（#59 验证：预编译平台绑定 17s 构建静态 68MB）以 `//go:build duckdb` 标签隔离——默认构建查询答 `ErrEngineNotBuilt`（501 `engine_not_built`，诚实契约）而导入/快照/Parquet 写入全功能；发布构建（linux/docker）`-tags duckdb` 全功能。**查询面 = 固定预设 + 参数**（totals 站址汇总 / duplicates 跨批次重复结算候选——**风险线索非结论**：账单存在 ≠ 重复付款认定），调用方传不了 SQL（MCP 红线延续到引擎面）。CI 新增 duckdb job（linux CGO 跑引擎 e2e）。
- **CLI + HTTP 双面**：`netopslab fees --db --data import|list|rollback|query`（单 JSON stdout；幂等重复导入 informational exit 0）；HTTP 五端点 `POST /api/fees/imports`（201 新导入 / 200 幂等重复 `skippedDuplicate` / 422 行全失败或不可映射——表头同义词自动识别，识别不到必需列即拒绝，人工映射是回退不是猜测）、`GET /api/fees/batches`、`POST /api/fees/batches/{id}/rollback`（键释放→同文件可再导入）、`GET /api/fees/query/totals|duplicates`——过 v0.11-R13 令牌写门禁，进 OpenAPI 文档（`fees` tag 路由表生成零漂移）；`GCAnalysis` 清孤儿（无 completed 行背书的 Parquet 文件与过期暂存目录）。
- **实测**（duckdb 二进制真实旅程，CLI 与 HTTP 数字逐字一致）：导入 2 行 → 同文件重导幂等跳过 → 第二文件同站同期批次共存 → `totals` 跨批聚合 2233.56（= 1234.56 + 999.00）→ `duplicates` 精确返回唯一跨批候选（2 batches / 2 rows / 全血缘）→ 回滚后重导成功。
- 组合根接入：`fees.Service` 进 `internal/services`（`WithFees` dataDir 选项），App/CLI/HTTP 共享单实例（指针级装配不变）。

### 修复 — letterbox 护栏显式分支（#62，P2）
- **根因**：内容比 `(vh/vw)/0.72` 更高瘦时，72% 横向利用率数学上不可达成（所需 zoom 令内容高度超画布），但旧代码 `Math.max(FIT_PAD_V_MIN, padNeeded)` **静默钳掉负数**——护栏「看似生效」（padY 40→12）实际退化为普通 fit，无任何信号表明目标已被放弃。
- **修复**：可达性判定抽为纯函数 `fitGuard.ts` 三态显式分支（plain / utilization / **vertical-bound**——不可达时纵向铺满画布、验收主指标切到纵向利用率）；`data-fit-mode` 埋点挂 cy 容器（浏览器级验证与人工排查直读分支——真实数据集实测 vertical-bound + 零控制台错误）。
- **数学口径校准**（实现中发现）：旧公式 `(vh − zoomNeeded·bb.h)/2` 是屏幕像素模型，而留白实际按模型单位进 `cy.fit`——可达分支**欠冲**（收窄留白后横向利用率仍达不到 72% 还自以为达标）。改为精确模型单位上界 `padMax = (vh/zoomTarget − bb.h)/2`，可达分支在真实 fit 语义下**精确**达成目标（单测断言 0.72 到 4 位小数）。

### 新增 — 前端单测门禁（vitest，仓库首个）
- `web` 包新增 `npm test`（vitest run）+ CI web job 加测试步：`fitGuard` 纯函数 6 用例（#62 精确数字钉死不可达分支 + 可达中段/封顶段/边界滑入/非法输入 + 埋点 DOM 断言）。前端从此与 Go 同受「测试钉死」纪律约束。

### 测试
- Go **549 个顶层测试**全绿（v0.12.1 基线 524 + 本里程碑：费用域 +20【v29 迁移守卫/幂等部分索引/规范化表测/识别器/Parquet 布局回滚/服务幂等共存行失败回滚 GC/CLI e2e/duckdb 引擎 e2e】+ HTTP 面 +5【导入旅程/回滚/引擎契约/OpenAPI 标签/写门禁】）。门禁：gofmt 空 · vet 0（默认 + duckdb 标签双构建）· 25 包 ok · vitest 6/6 · duckdb 二进制 HTTP 冒烟。CI 四 job（go/web/duckdb/windows）因账户 Actions 分钟配额耗尽全部 0-step 失败（v0.6.0 起已知基础设施模式，非代码问题；本地门禁集与 CI 同集全绿——run 39189729436 及此前同模式）。
- 浏览器级验证：真实数据集（3,468 资源）工作台 fit 埋点 vertical-bound 读取 + 零控制台错误 + 截图存档。

## [0.12.1] — 2026-10-17

**缺陷修复版**（四个 open issue 闭环：#54 拓扑布局尺度 / #55 维护窗口时基 / #56 概览渲染循环 / #57 三项 UI·文档一致性。全部为缺陷修复与可观测性补强，零契约破坏——`suppressionNote`/`windowLabel`/`suppressionBasis` 为新增可选字段，现有消费者不受影响）。

### 修复 — 维护窗口抑制双时基（issue #55，P1）
- **根因**：窗口由运维按**墙钟**创建（「今晚 22:00」），而引擎按**数据时间**评估（`f.LastTS`）；导入历史数据回放时两个时基相隔数天，抑制永不触发且零提示——issue 实测的三组对照实验里 B 组（墙钟窗口 + 历史数据）静默失效。
- **修复**：抑制判定升级为**双时基**——证据时刻（数据时间线，可重放）或运行时刻（`detect.Options.WallClock`，由 recorder 从注入时钟盖章、HTTP/CLI/scheduler 全触发路径自动获得）任一落入窗口即抑制。引擎保持无时钟纯函数（墙钟是显式输入参数，零值 = 纯数据时间模式——既有确定性测试逐字节不变，契约测试新增 5 项钉死 B/C/零值/note/注入路径）。
- **静默失败消除器**：`RunResult.SuppressionNote`——存在墙钟激活窗口但本轮零抑制且新建了告警时，回执（HTTP 响应 + webhook 载荷）给出一行中文说明（窗口时段、运行时刻、数据时刻、双时基提示），「窗口在保护我」的错觉可诊断。抑制行携带 `windowLabel`（命中窗口事由）与 `suppressionBasis`（`data_time|wall_clock`）。
- 语义文档化三处一致：`domain/maintenance.go` 契约注释、`routes.go` OpenAPI Summary（/dev 页自动继承）、前端 /maintenance 页双时基提示（含「当前数据时刻」实时显示）。

### 修复 — 概览页无限渲染循环（issue #56，P0）
- `Overview.tsx` 把 `getAlertStats()` 裸写在组件函数体内（不在任何 useEffect）——响应回来 `setAlertStats` 触发重渲染 → 组件体再执行 → 再发请求，实测 52 QPS 永不停止。修复后纳入与 `loadGov` 相同的 useSeq 守卫纪律（useCallback + useEffect 挂载时取一次，过期响应丢弃）。浏览器级验收：打开 `/` 停留 30 秒 `/api/alerts/stats` 请求恰 1 次（修复前 1050 次/20 秒）。

### 修复 — 告警列表资源列丢设备名（issue #57①，P1）
- 端口/板卡类资源的 `name`（`xgei-1/1/1`）自身无区分度——多台设备同名端口在列表里无法分辨归属。资源列改为双行展示：主行 `name`、副行 `naturalKey`（含设备前缀，11px muted），与资源浏览页的展示风格一致；同名端口的行文本不再相同。

### 修复 — 质量报告硬编码「合成数据」声明（issue #57②，P1）
- 报告页/HTML 报告/Lab 报告三处的固定文案「本报告基于合成测试数据生成」与数据现状矛盾（库内可能是用户真实台账或真实公开数据，报告无法验证来源，断言哪个都是越权）。改为来源中立文案「本报告基于当前已导入的数据生成，请以实际网络情况为准。」（中英双语，报告模板 + i18n + Lab 常量 + 测试断言四处同步）。

### 修复 — 拓扑 L0 锚点撑大画布尺度（issue #54，P1）
- **根因**：cose 力导向把弱连通的长链（人井/杆路走廊锚链）甩到外围——实测 L0 21 节点铺开 1902 宽而 133 台设备仅 447，L0 单层撑大全图包围盒 → fit zoom 0.46 → 真实内容压在左侧 1/4。#53 修复轮的「开 L0 撑宽包围盒 = 利用率 74%」推理链被推翻（包围盒变宽 ≠ 内容铺开）。
- **双防线**：①布局 stop 后把 L0 作为**刚体**收拢进结构包围盒（统一缩放系数 + 居中平移——走廊链相对形状保真，绝对尺度对齐结构层）；②`fullGraphBoundingBox` 排除 L0（地理装饰层不定义画布尺度；纯锚点图回退锚点自身范围）。像素网格验收：修复前右侧 2/3 基本空白 → 修复后右 6 列内容占比 80.5%，内容遍布画布。

### 文档
- `docs/api.md` 补齐 v0.12 新端点与字段（issue #57③）：维护窗口三端点全节（GET/POST/DELETE + 双时基语义 + 示例）、`GET /api/alerts` 的告警关联三字段（`correlationRole`/`correlatedAlertId`/`correlatedResourceId`——下游闭包/严格层差的分诊语义补档）、`POST /api/detection/run` 响应补 `suppressionNote`/`windowLabel`/`suppressionBasis`；lab report 示例 disclaimer 同步新文案。

### 测试
- 新增 5 个双时基契约测试（`suppression_wallclock_test.go`：B 组墙钟命中、C 组数据时间命中带 basis 标注、零墙钟向后兼容、note 触发条件与内容、recorder 注入路径）；4 处免责声明断言同步新文案；`go test ./...` 24 包全绿 / gofmt 0 / vet 0 / tsc 0 / web build 4.8s。

## [0.12.0] — 2026-10-17

**告警质量治理版**（四轮迭代 R15-R18；需求驱动：v0.11 的自主运营把「跑得起来、看得见、写得进、备得了」补齐后，距完整 NOC 产品的剩余差距集中在告警噪声本身——计划内变更的告警是噪声（R15）、症状风暴淹没根因（R16）、阈值徘徊序列的周期性churn（R17）、通知送不出去等于没告（R18）。四轮共享一条设计教义：**抑制与分诊都是「机器之前/之外」的门，三边状态机（firing→acked→cleared，ADR-0011 D3）一毫米不动**；全部裁决走数据时间线（无时钟、可重放）；「好消息永不抑制」——恢复清除在任何门下都照常发生）。

### 新增 — 维护窗口：计划内抑制（R15，迁移 v24）
- 割接/升级/换板窗口内的新告警燃点被抑制（`maintenance_windows` 表：label + RFC3339 半开区间 + 资源范围 JSON 稳定编码；API CRUD，scope id 必须真实存在——拼错的 409 而非静默无效窗口；无 PATCH：窗口是计划，编辑 = 删除重建）。
- 抑制判定纯函数（数据 ts 落入区间 + 资源在范围内）；仅拦新燃（在案告警证据照常刷新）；恢复清除在窗口内照常发生；被抑制的 finding 不落告警行、不写运行状态，计数 `suppressed` 并在信封行携带**确定性应燃 id**（窗口结束后持续越限的序列以同一 id 落燃——两条线可关联）。
- 运行回执/信封/webhook 载荷/runs 列表全部携带 `suppressed`；/maintenance 页面（列表/状态 chip/资源搜索选择器/创建/删除确认）+ 回执对话框第五统计格 + 巡检条与运行历史计数。

### 新增 — 告警关联：根因/次生分诊（R16，迁移 v25）
- 引擎在检测事务末尾把图层阶梯（影响面引擎的依赖方向）投影到活动告警行：**疑似次生**（本资源的 DOWN 闭包内存在更底层资源的告警——设备告警解释电路告警；BFS 深度→层→id 确定性最近根因，按告警 id + 资源 id 链接）/ **根因嫌疑**（无下层告警但上层依赖有告警——候选解释）/ 未标记（孤立、同层兄弟、info 永不参与）。
- **严格层差反对称守卫**：同层邻居永不互标（layer(A)<layer(B) 与反向不能同时成立，裁决关系构造性无环——真实信道 lateral 守卫钉死）。观察态而非生命周期：每轮幂等重算活动 error/warning 告警的裁定，已清除告警保留最后裁定（存活期间的审计真相）。
- 读面全链路：`GET /api/alerts` 三字段永不缺省（严格客户端契约）+ `?role=primary|secondary` 过滤（bogus → 400）+ CSV 三列（20→23）+ MCP `alerts_query` 同字段与过滤（AI 分诊直接读根因/次生，无需自行推导）；/alerts 关联列 chip（根因嫌疑 warning「优先处置这条」/ 疑似次生 muted「先处置根因」）+ 筛选下拉 + 行展开因果行（次生行具名根因资源与告警 id 跳转链接）+ 回执行裁定 chip。

### 新增 — 抖动治理：重燃抑制门（R17，迁移 v26）
- (rule, series) 对在**尾随 14 天证据时间内**已有 ≥3 个完整 fire→clear 周期（仅计引擎恢复清除的数据 ts——人工清除是钟表 ts，是操作者的决定不是序列的性质，永不计数）→ 第 4 次重燃被拦：不落告警行、不写运行状态，计数 `flapHeld`，信封行 `action=flap_held` 携带确定性应燃 id。
- 抑制自然老化：被拦的重燃不产生新行 → 周期计数只减不增；旧清除行随数据推进出窗 → 下一轮持续越限以**应燃 id 落燃**（与 R15 同一可关联教义——持续越限穿过抑制期后拿到被承诺的同一身份）。抑制有界（≤14 天数据时间），永不永久。
- 门序钉死：活动维护窗口优先于抖动判定（操作者显式计划胜出——窗口内是 suppressed 永非 flap_held）；恢复清除无抖动门（好消息教义）。
- 读面：运行信封/webhook/回执列表携带 `flapHeld`（字段永不缺省）；回执对话框第六统计格「抖动抑制」+ `flap_held` 行 info chip（与维护「抑制」warning chip 区分成因）；巡检条四计数（新增/清除/抑制/抖动）+ 运行历史计数列五段式。`docs/api.md` 检测运行契约补 `suppressed`/`flapHeld`（R15 时点漏记，顺带补上）。

### 新增 — webhook 投递硬化：有界重试 + 投递审计（R18，迁移 v27）
- 单发 best-effort → **有界重试序列**（1 + 2 次，默认退避 1s/2s 可注入；整序列受调用方 ctx 界定——预先取消的 ctx 恰好一次尝试并如实记账；API/调度器异步预算 10s→25s 容纳三次最坏 5s 尝试）：接收方抖一秒不再丢通知（两次 503 后 200 = 第 3 次尝试送达，端到端钉死）。
- **投递审计表** `webhook_deliveries`：每次通知的重试终局一行（CHECK 闭集 delivered|failed；无 pending 态——无行 = 该轮通道未配置，诚实三值读）；`run_id` 链接回执（刻意非 FK：侧路径在回执提交后落行）；**URL 脱敏后入库**（查询串与 userinfo 剥离——webhook URL 常带 token，审计表永不成为凭据堆）；审计写入脱离调用方取消（WithoutCancel + 自有超时，recorder 教义）。
- 读面：`GET /api/detection/runs` 行 LEFT JOIN 裁决（`webhookStatus` ''|delivered|failed + `webhookAttempts` + `webhookError`，永不缺省）；`docs/api.md` 补 GET /api/detection/runs 节（此前从未记载，顺带补上）；/alerts 运行历史对话框「通知」列（已送达/投递失败/— 通道关）。

### 工程卫生
- 迁移 v24-v27 全部纯增量（新表或 ALTER ADD DEFAULT 0），v21 前回执回填 0 不说谎；11 个既有迁移测试的注册表头断言随注册推进必要适配。
- 抖动参数为引擎级代码常量（FlapThreshold=3 / FlapSpan=14d，测试钉死防静默漂移；按规则列化的升级路径已在 domain 注释声明为后续项）。
- 测试：24 包 **505** 顶层全绿（v0.11.1 基线 489 + r15-r18 增量；gofmt 空 / vet 0 / -race detect+repo+api+notify 0 race / tsc 0 / vite build）；四份浏览器级回归脚本在发布树全绿（r15 19/19 · r16 11/11 · r17 17/17 · r18 7/7）。CI 不运行（账户计费），[skip ci] 标记，本地门禁集与既往一致。

## [0.11.1] — 2026-10-16

**issue #53 图层覆盖层修复轮**（对 v0.11.0 `73e7fa9` 端到端实测暴露的三缺陷 + 附带口径问题；先确认 #51 主体修复成功——interfaces 46→400、layerNodes 0→1286、layerLinks 0→1073，接口名从合成 `eth<n>` 变真实端口）。全部为前端行为修复 + Go 契约测试，无 schema/API 变更。

### 修复 — L3 纯边图层开关永久禁用（#53 缺陷 A，P1）
- `TopologyCanvas` 图层开关的 hasData 判据从「nodes>0」改为「nodes>0 或 edges>0」——L3 传输是纯边图层（nodes=0、edges=221，两端都是画布上的设备），旧判据把 L3 整体禁用，207 条电路在拓扑上不可见，连 `DEFAULT_PREFS` 的 L3:true 默认态都因 `active = prefs && hasData` 被压成 false。
- 契约由 `TestLayerOverlayPureEdgeContract`（sim 层）钉死：L3 纯边形态（nodes=0/edges>0）+ 每个 summary 列出的图层必须 nodes>0 或 edges>0 + 每条图层边端点可解析到画布实体（device/interface/node 三类，无 device 自环）+ 覆盖层节点锚设备存在（派生接入上联边可渲染）——「以边计数判可用」的前端判据在数据侧永不再被无声破坏。

### 修复 — 默认视图压成竖条 + 切层缩放跳动（#53 缺陷 B，P1）
- fit 基准从「当前可见元素包围盒」改为**全图包围盒**（手工聚合全部节点含隐藏图层节点——Cytoscape 的 `boundingBox()`/`cy.fit()` 在样式启用环境排除 display:none 元素，默认组合下可见包围盒塌成 813×1113 细长条，fit 后内容挤在中间约 436px 竖条）。全图包围盒与图层开关无关 → 任意图层组合下 fit 尺度一致，切层（本身不 fit）与按 F 回到的都是同一尺度。
- letterbox 护栏：内容高瘦（宽高比低于视口）时纵向留白收窄（40px → 下限 12px）换取横向铺开，护栏目标横向利用率 0.72；实测（清偏好默认态，1600×900 视口）zoom 0.574、横向利用率 **72.1%**（修复前 ~33%）、纵向 95.6%（内容完整可见）。
- 默认图层组合改为 L0 默认开（L0/L1/L2/L3/L6）：默认可见子集（设备+业务节点）是高瘦簇（宽高比 ~0.8 vs 视口 ~2.0），任何正确 fit 都是竖条；L0 空间锚点（人井/杆路，14px 无标签暗点，视觉成本近零）铺满真实地理宽度——开启后可见内容横向利用率 ~74%。L4（1,198 基站聚簇）/L5（16 分组 ×674 边）仍按需。已存偏好不受影响（仅新会话/清偏好生效）。

### 修复 — 聚簇成员数徽标随缩放隐藏（#53 缺陷 C，P2）
- 聚簇胶囊的「接入 ×N」就是成员数徽标，但沿用 overlay 基类 5px 的 min-zoomed-font-size 在默认 fit 缩放（~0.57 × 10px = 5.7px 边缘值）下整体隐藏——用户只看到 11 个孤立小点。现 cluster 专项置 `min-zoomed-font-size: 0`（计数是语义内容不随缩放隐藏），字号 10→11、盒宽 74→88（×1198 全量计数单行放得下）。实测：11 个胶囊显示 ×440/×137/×103/×48/×46…；点击下钻（LayerInfoTab 成员表分页）本已可用，实测 440 成员 20 行/页翻页正常。

### 修复 — 链路口径拆分（#53 附带，P2）
- 头部计数 chips：「链路」→「物理链路」（virtual_links 物化表，23），原「图层 1286/1073」合并 chip 拆为「图层节点 1286」「图层边 1073」两个独立 chip（悬停 title 说明各自口径与只读投影属性）。
- 工作台列表：物理链路列更名 + 新增「图层（节点/边）」列（1286/1073；v0.11 前旧工作台显示 —）——列表即可区分新旧工作台（用户反馈「卡片与旧工作台无差别」）。新建工作台 toast 增补图层边计数（MaterializeResult 本就返回，前端此前未消费）。前端 `WorkspaceSummary`/`MaterializeResult` 类型补齐后端已有的 layerNodeCount/layerLinkCount 字段。

### 工程卫生
- `internal/domain/sim.go`、`internal/app/e2e_realdata_test.go` 补 gofmt（#51 提交 `73e7fa9` 引入的缩进漂移；当时 CI 因账户计费问题未运行未拦截——run 38857685886 三 job 均「job was not started because recent account payments have failed」，非代码原因，billing 恢复后预期绿）。
- 测试：24 包全绿 + 新增 `TestLayerOverlayPureEdgeContract`；前端 tsc/vite build 通过；浏览器级回归（agent-browser，复刻 issue 的 CDP 方法论）六项验收全过。

## [0.11.0] — 2026-10-15

**自主运营版**（五轮迭代 R11-R14；需求驱动：距完整产品的差距清单——「检测只能显式触发」「运行回执不落库不可审计」「概览硬拉 firing 总数」「资源抽屉无告警视角」「零认证」「无整库备份」）。核心：把 v0.10 的「触发得了」推进到「自动巡检、历史可查、分布可见、双向定位、写有门禁、库有快照」。

### 新增 — 定时巡检调度器（R11）
- `serve --detect-interval 15m`（最小 1s，空 = v0.10 显式触发行为）：每个间隔执行一次全库检测扫描并落库回执、触发 webhook 通知——运营闭环的最后一块，从「手动触发」到「自动运行」。调度器单飞（上一轮未完成则跳过该轮并记 warn，绝不排队重叠）；优雅停机（一个 SIGINT 同时停监听器与巡检）；nil 安全（`Status()` 未启用时返回禁用快照，调用点零分支）。
- 通知走回调注入（`NotifyFunc`），依赖方向保持单向（notify → detect），无第二个通知器/引擎实例。

### 新增 — 检测运行历史（R11）
- 迁移 v21 `detection_runs`：每次扫描（api/cli/scheduler）落一条持久回执——来源（CHECK 闭集，MCP 只读红线在存储层物理不可持久化）、时钟区间、耗时、评估窗口回显、数据截止、四计数、失败原因（失败轮本身就是可审计的运营事实）。
- `detect.RunAndRecord` 包装引擎（引擎保持纯函数，时钟注入可测）：成功与失败都落库；失败路径的回执写入用 `context.WithoutCancel` 脱离取消（被取消/超时的扫描正是值得留痕的失败）。`POST /api/detection/run` 与 `netopslab detect` 输出同形信封（runId/source/bracket/from/to，加法式扩展，v0.10 消费者不受影响）。
- `GET /api/detection/runs`（最新在前分页）+ 路由表自动进 OpenAPI。

### 新增 — 告警分布统计 + 巡检状态条（R11）
- `GET /api/alerts/stats`：闭集零填充——`byState` 三态计数 + `activeBySeverity`（firing+acked 的 error/warning/info 矩阵，运维大屏的「待处置工作量」视图）。Overview 告警卡改由该端点驱动（取代 v0.9 的 `pageSize=1` 探针，更少请求更全信息，副行展示待处置矩阵 + 已确认数）。
- /alerts 顶部巡检状态条：上次回执（来源徽章 · 相对时间 · 耗时 · 新增/清除，失败态红字悬停看原因）+ 下次自动巡检时刻（health 调度块驱动）+「运行历史」对话框（来源徽章/开始/耗时/三计数/成功失败/窗口/数据截止，分页）。

### 新增 — 资源视角告警 + 指标 CSV 导出（R12）
- 资源抽屉第六个页签「告警」：本资源告警史（全部生命周期状态，最近 50 条，行 = 状态徽章 + 级别徽章 + 规则标签 + 值域 vs 阈值 + 相对时间）；确认/清除操作面仍在告警中心（单一写面），页脚深链直达——告警页链出到资源、资源抽屉链回告警，故障定位动线双向闭环。
- 指标页签对称导出：`seriesId,metricKey,ts,value,quality` 平面化 CSV（BOM + RFC4180 转义 + 30s 延迟撤销——R7 下载竞态教训前置应用）；工具条显示点数/序列数。

### 新增 — 写操作令牌网关（R13）
- `serve --auth-token T`：所有变更类 /api 请求（POST/PATCH/DELETE）要求 `Authorization: Bearer <token>`（常数时间比较）；读面（GET/HEAD）、`/api/health`、`/api/openapi.json` 与 SPA 刻意开放（演示/大屏/AI 分析是读侧，审计身份属于写侧）；401 走标准错误信封。空 = 不启用（v0.10 行为零差异）。
- 前端配套：请求封装自动附着存储的 Bearer（含 multipart 上传与裸 fetch 的删除路径）；侧栏页脚「访问令牌」入口 → 保存/清除对话框（localStorage + 已设置徽章）——401 文案点名修法，一键可达。

### 新增 — 整库备份 CLI（R14）
- `netopslab backup --db path --out snapshot.db`：仓库层 SQLite 在线备份 API（实验室检查点同机制）首次暴露为运维命令——字节一致快照，绝不裸拷贝活库；源库读写不中断；自动建目录；stdout 单 JSON 对象 `{out, bytes}`；缺 --out 为用法错误（exit 2）。

### 优化 — 读路径索引（R14 审计驱动）
- 迁移 v22 `idx_alerts_state_fired (state, fired_at)`：告警队列默认筛选 + `fired_at` 排序与 v0.11 stats 的 GROUP BY 此前无法使用 v18 索引（state 是第三列）——EXPLAIN QUERY PLAN 测试钉死新索引的使用。
- 审计诚实记录：metrics `(series_id, ts)` 候选索引经查证被**否决**——v14 表即 `WITHOUT ROWID` + 同形主键，序列窗口读已走主键；测试反向钉死（防止后人再加冗余索引白交写入税）。

### 工程与测试
- 回归脚本：`r11_scheduler_regression.sh` 30/30（真实 `--detect-interval 3s` + webhook 接收端 + 浏览器巡检条/运行历史/Overview）、`r12_resource_alerts_regression.sh` 12/12（?focus 深链 → 告警页签 → 下载劫持验证 CSV）、`r13_auth_regression.sh` 10/10（curl 401 矩阵 + 浏览器无令牌 401 → 存令牌 → 同对话框重试成功的完整旅程）；r6/r7/r8/r9 复跑全绿（26/12/13/13）。
- 新测试：v21/v22 迁移（含失败回执、CHECK 闭集、EXPLAIN 计划）、recorder（成功/失败/拒绝来源）、scheduler（真实时钟三轮/单飞/nil/通知失败不致命）、stats 契约（与列表 total 互证）、health 调度块、backup 往返（快照可开可读）。
- 本地门禁（CI Actions 配额仍为 0 步模式，按 v0.6-v0.10 惯例以本地为准）：gofmt 空 · vet 0 · 24 包全 ok · tsc 0 错误 · 前端构建通过。

## [0.10.0] — 2026-10-15

**运营闭环补全与 API 可发现性版**（四轮迭代 R6-R9；需求驱动：距完整产品的差距清单——「规则 API 有而 UI 无」「检测运行只有 CLI/MCP 通道」「无出站通知」「无机器可读 API 文档」）。核心：把 v0.9 的「看得见」推进到「配置得出、触发得了、通知得到、可被集成」。

### 新增 — 检测规则管理页 /rules（R6）
- v0.7 起规则 CRUD API 完整而 UI 零覆盖（只能 CLI/curl）——本轮补上配置侧入口：全量表格（启停徽章/级别徽章/**规则标签列**/指标/比较器+阈值条件/窗口/更新时间/行内启停/编辑/删除，title=ruleId 精确定位）+ 行展开完整条件审计视图。
- 新建/编辑共用 RuleDialog：指标**封闭目录下拉**（`GET /api/metrics/keys`，含中文标签与合法区间提示）、比较器/阈值/窗口/级别/标签/启停；编辑态指标只读（身份字段，后端 400 契约）；删除走确认对话框，被 4 条告警引用的规则后端 409（审计保护）→ 前端 toast 透传理由并引导改用停用。
- 筛选：仅看启用中开关 + 指标下拉；`filter-push`/`field-row`/`modal--form` 三个新 CSS 原语。`r6_rules_regression.sh` 26/26（真实旅程：31 工作簿 → 检测 → 默认规则表 → 创建（目录下拉+必填校验+toast+行数）→ 编辑（只读身份）→ 改阈值 → 启停往返 → 409 审计保护（API+浏览器 toast）→ 干净删除 → 筛选 → console 零错误）。

### 新增 — 立即检测 UI 入口 + 告警 CSV 导出（R7）
- `POST /api/detection/run` 自 v0.7 只有 CLI/MCP 通道：RunDetectionDialog 三态一体（确认说明 → 运行中 → 回执）共用组件，/alerts 与 /rules 双入口（待处置队列的「现在跑一次」+ 改规则后的立即验证）；回执四格统计（发现/新建/清除/写入）+ 数据截止 + 告警变更明细表（created/cleared chip）；完成自动刷新筛选队列。
- 告警导出：当前 state+severity 筛选分页拉全（pageSize 200、100 页守卫）→ 20 列 CSV（BOM 保证 Excel 识别 UTF-8、RFC4180 转义、日期文件名）；空结果 info 提示不下载。
- **修复真实产品缺陷**（回归现场抓获）：`URL.revokeObjectURL` 在 `a.click()` 后立即执行与浏览器抓取下载句柄存在竞态——改为 30s 延迟撤销。`r7_run_export_regression.sh` 12/12（确认文案 → 回执统计+数据截止 → 重跑零变更确定性 → 自动刷新 → /rules 入口 → 下载劫持验证 BOM/表头/行数==API total → 空导出 → console 零错误）。

### 新增 — Webhook 通知通道（R8）
- 检测环出站通道：`internal/notify` 尽力而为投递器——POST JSON（event=detection.run + deliveredAt + 四计数 + evaluatedAt + **封顶 50 行**告警变更明细 + alertsTruncated 标志）；5s 投递界；失败仅 warn 日志、**绝不影响检测结果**；nil 接收器 = 通道关闭（调用点零分支）。
- 接线：`app.Config.WebhookURL` 单一装配点，HTTP 通道在 200 计算后异步投递（背景 ctx），CLI 通道在进程退出前同步投递；`serve --webhook-url` / `detect --webhook-url`；MCP 面永不通知（只读红线）。
- 测试：notify 单测（nil no-op/POST+JSON 契约/明细行/50 行封顶（计数不封）/503 与不可达的错误不致命/300ms 超时界）+ api 集成（劣化场景一次投递镜像运行回执；失败接收端保持 200）。`r8_webhook_e2e.sh` 13/13（真 serve --webhook-url + python 接收端 + **浏览器触发**（R7 对话框）→ 契约断言 + findings 与 API 回执一致 + CLI 通道投递）。

### 新增 — OpenAPI 3 文档 + /dev 参考页（R9）
- **路由表单源化**：65 个端点集中于 `internal/api/routes.go`（method/pattern/tag/中文摘要/handler），`Routes()` 循环注册 + OpenAPI 文档同表生成——**规格与实现结构性零漂移**（无摘要的路由无法存在，删路由则双处消失）。
- `GET /api/openapi.json`：OpenAPI 3.0.3，info.version 用 domain 常量（与 /api/health 同源），按表序 tags，每操作带中文摘要；sync.Once 记忆化。范围诚实：发现性文档而非全 schema 契约（信封级描述；深形状在 docs/design 与 TS 客户端）。
- 防漂移测试：spec paths == 表面；每个文档 (path, method) 用占位参数对活服务器探测断言**永不落入未注册 fallback 404**；两次读取字节一致；version == health。
- /dev API 参考页：实时渲染文档（方法徽章 GET/POST/PATCH/DELETE 四色 + 分组目录 + 端点计数）——JSON 端点的人类孪生。`r9_openapi_regression.sh` 13/13（文档契约 7 + /dev 渲染 5 + /alerts /rules 无退化抽检）。
- 回归脚本工程教训（痛苦换来的）：**`GROUPS` 是 bash 内置特殊变量**（进程组 ID 数组）——对它的赋值被吞、读取返回 gid（1001）；脚本改用 `API_GROUPS` 并注释钉死该陷阱。另：`VAR=$(fn)` 在 `set -e` 下函数返回码杀脚本——`local x=$(...)` 恒 0 + 显式 `return 0` 的惯例。

### 工程内务
- 告警中心/规则页共用的 CSS 原语与 i18n 文案节（RUL_*/RUN_*/ALR_EXP_*/API_*）。
- README §2 能力清单与快速开始更新（--webhook-url 用法与载荷契约、/dev、/rules）。
- 门禁：`gofmt -l .` 空 · `go vet` 0 告警 · `go test -count=1` **24 包全 ok**（新增 internal/notify；443 → 461 测试）· `tsc -b` 0 错误 · `npm run build` ✓ · 四份浏览器级回归脚本全绿（26/12/13/13）。

## [0.9.0] — 2026-10-14

**运营可见性与 AI 分析闭环版**（五轮迭代 R1-R5 + issues #46/#47 修复轮；需求驱动：用户「先列还有哪些能开发的，距离一个完整的产品，然后继续开发至少持续5轮」）。核心：v0.7 数据模型弧（检测告警/指标时序/图查询）与 v0.8 真实数据里程碑从「API/CLI 可用」走到「人看得见、AI 用得上」——运营侧四个 UI 入口 + 智能体侧五个只读分析工具。

### 修复 — issues #46/#47（用户/审计现场反馈，先于五轮开发落地）
- **#46（P1）**：`TestCLIGraphQueryChannels` 恒失败——`assertJSONEqual` 把唯一时钟字段 `stats.elapsedMs` 纳入深比较（两次活查询必然不同）。按 issue 方案 B 精确剔除 `stats.elapsedMs`（`stripClockFields`），`stats.visitedEdges/truncated` 等确定性键保留在契约内（真实分叉仍会红）。commit 6ed2a5c。
- **#47（P2）**：工作台 UI 四诉求一次重构闭环：工具带 actions 插槽从首组移到**末组**（画布固有控件在前）；七个动作按钮**图标化**（32px 方形 + title/aria-label，`lab-canvas-actions` nowrap）——1600/1280 视口工具带均单行（47px 实测）；页头卡片移除（无边框单行 `.lab-pagebar`，哈希/描述走 title）；/lab 默认**画布概览**（真实拓扑 SVG 缩略图卡片网格——不挂 Cytoscape/MapLibre 实例、IntersectionObserver 懒加载、确定性渲染；表格保留为「列表模式」）。`w47_browser_regression.sh` 14/14。commit a436357。
- 顺带修复 `w28_browser_regression.sh` 的过时导入顺序（v0.8.0 时代 ports 在 boards 前——90 行 port 静默 E003 失败；与 demo-real/e2e 对齐为 boards+base_stations 先于 ports、protection_groups 入序）。

### 新增 — 告警中心页 /alerts（R1）
- v0.7 检测告警（规则→告警→run_state 联动）的运营第一入口：状态筛选（触发中（默认——待处置队列）/已确认/已清除）+ 级别筛选 + `?state=` 深链 + useSeq 陈旧响应守卫 + 分页。
- 行渲染：AlertStateChip（触发中红/已确认橙/已清除灰）+ SeverityBadge + 规则 + 资源链接（→ `/resources?focus=` 抽屉）+ 相对时间 + 最新值 vs 阈值（比较器中文短语）+ 处置按钮（可见性随行状态，后端仍是最终权威）。
- 确认旅程：专用可达性对话框收集必填「确认人」（空值禁用提交、Enter 提交、busy spinner）→ firing→acked + toast 回执；清除旅程：危险确认 → 归档态。行展开详情：规则 ID/指标/序列/窗口/首末时间 + 确认与清除审计行。`r1_alerts_regression.sh` 21/21（真实旅程：31 工作簿 → 7 日摄入 → 检测 4 firing → ack/clear 全链路浏览器断言）。

### 新增 — 资源抽屉指标时序可视化（R2）
- 抽屉第四页签「性能指标」（懒加载 + useSeq 守卫 + 内联重试）：MetricChart **零依赖确定性 SVG 折线图**——y 轴 min/mid/max 网格 + x 轴首末日期、polyline + 圆点（≤60 点，title 悬浮「日期 值 单位」）、suspect 点橙色（建议界外语义）、missing 空心、**劣化序列（末值<首值）告警红渲染**、恒值序列 ±1 兜底。
- 每序列卡片：中文标签（`GET /api/metrics/keys` 封闭目录映射）+ 单位 chip + 点数/最新值；无序列资源空态文案。7 日光功率 -8→-24 dBm 劣化故事首次在 UI 可见（红线下探 + 「最新 -24」）。`r2_metrics_regression.sh` 11/11。

### 新增 — 资源抽屉影响分析页签（R3）
- 抽屉第五页签「影响分析」四子模式：**根因分析**（impact direction=down，默认——§10.5③ 根因候选含引擎中文判定理由 + BFS 距离 + 受影响面）/ **影响面**（direction=up，§10.6② 后端默认）/ 向下穿透 / 向上汇总（层序 BFS 逐层 depth 分组 + 节点/关系/跨层计数 + 截断提示）。
- 节点行：层族类型 chip（按节点 layer 配色，未知类型灰 chip 防御回落）+ entity-link 深链 + 运行态 chip（异常=down/degraded 红 chip，与引擎判定一致）；**只读**（分析视图无任何写通道）；空集合显式空态文案（audit §2.6 UI 面）。旅程实测：光缆置 down → 电路抽屉根因分析 → 候选=异常光缆（中文理由）+ 61 受影响。`r3_graph_regression.sh` 9/9。

### 新增 — MCP 十工具面：五个只读分析工具（R4，智能体核心卖点）
- **工具面 5 → 10**，严格只读侧扩宽：`graph_traverse`（纵向穿透全参数：direction/layers/relations/maxDepth/maxNodes/pathOnly）/ `graph_impact`（down 根因追查（含中文判定理由）| up 影响面收集）/ `graph_context`（§7.3 一站式：详情+邻接+最新指标+近期异常——AI 分析单资源首选）/ `alerts_query`（firing/acked/cleared × severity，§18 资源投影）/ `metrics_query`（劣化曲线证据，封闭键校验）。
- 红线不变且结构性强化：**告警 ack/clear 与检测运行不设工具**（人类/HTTP/CLI 专属——注册表测试钉死 alerts_ack/alerts_clear/detection_run/metrics_ingest 等禁止名）；每个工具绑定 HTTP 同一组合根实例（topology 引擎 × `svc.GraphStore`、`svc.GraphContext` 单实现、`svc.DB().Alerts`、`svc.Metrics`）——无 SQL、无第二引擎。
- 测试：注册表钉 10 工具；`TestMCPGraphToolsRoundTrip`（traverse/impact/context 线形 + §2.6 数组契约 + 错误词汇表）；`TestMCPAlertsMetricsRoundTrip`——**AI 根因分析全旅程走真实 MCP 线**：导入真实电信端口样例 → 摄入 5 日 -8→-24 dBm 劣化 → 检测引擎 → alerts_query 命中 firing 告警（含资源投影）→ metrics_query 取 5 点曲线佐证。真实子进程 stdio 冒烟：tools/list 恰好 10。
- 文档：README（十工具）、teleagent-integration.md（工具表 + schema + 推荐分析旅程 alerts_query → graph_context → graph_impact → metrics_query）。

### 新增 — 运行状态视图与概览告警卡（R5）
- 资源抽屉 attrs 页签新增**运行状态行**（W22 双态模型的 UI 面）：RunStateChip（运行绿/劣化橙/停机红/维护蓝/未知灰；空=「无观测」——台账状态是登记事实，运行状态是观测事实）+ 最近 8 条状态历史小时间线（`GET /api/resources/{id}/run-state/history`，抽屉加载并行拉取、失败静默）。
- 概览页新增**触发中告警卡**（第 5 张统计卡：firing 计数、>0 时红色高亮、独立容错）与快捷入口「告警中心」。

### 工程化
- 版本三处单一事实源升 v0.9.0（domain/web/CHANGELOG，一致性测试钉死）。
- 浏览器回归脚本三件套（r1/r2/r3，共 41 断言）+ w47 + w28 修正，全部真实数据旅程；测试计数 435 → **443**（+2 MCP 新测试 +注册表/禁止名扩展；`internal/mcpserver` 全绿）。
- 修正：回归脚本动态端口（固定端口曾与残留 serve 跨库串台）；`tr -d '"'` 剥 JSON 引号导致的断言解析缺陷改独立求值；React 受控输入的原生 setter 触发法。

## [0.8.1] — 2026-10-14

**issues #38-#45 修复轮**（audit-v0.8.0 的 8 项发现全部闭环 + 用户现场反馈 3 项；四提交：测试门禁与并发预算 / 演示数据三轮合一 / 前端交互成本与布局 / CLI 图查询通道）。

### 修复 — Windows 测试门禁（#38，P1，audit §2.1）
- `internal/testsupport.TempDir`：唯一共享的 Windows 容错临时目录（RemoveAll ~1s 重试；POSIX 首试即成）。59 个测试文件（18 个 SQLite 包）从裸 `t.TempDir()` 迁移；sim/repo 两份局部 tempDirT 副本收敛——新测试不再每轮重新踩 v0.6.2 的坑。
- `TestHealthCarriesDBPath` 断言 `strings.HasPrefix("/")` → `filepath.IsAbs`（产品行为本正确，是测试不可移植）；GC 失败注入测试 Windows skip（目录只读属性不阻断 Windows 删除，POSIX 分支覆盖该路径）。
- CI 新增 `windows-latest` job（用户实际平台；linux-only 门禁正是 15 项平台失败溜进 v0.7/v0.8 的原因）。

### 修复 — SQLITE_BUSY 重试预算（#39，P2，audit §2.2）
- `beginBusyAttempts` 3→6 + 指数退避 50-800ms（末次尝试后不空等）：最坏 begin 预算 ~33s，吸收整套件并行负载（Windows 审计中写锁持有被推过旧预算 5.3s 的逃逸场景）；多进程共享库文件的支持边界文档化（architecture.md §4/§7：超预算 = SQLITE_BUSY 错误，非数据损坏）。

### 演示数据三轮合一（#40/#42/#45，P2，用户现场反馈「数据的层级不够啊数据太少」「每次点击画布地图的数据都要重新渲染，卡顿」的数据侧根因）
- **字段厚度（#45）**：基站表 4→17 列（环/链、归属A IP/名称/端口、电联共享、网关/基站IP、状态、经纬度——补齐设计 §3.2 记录的真实表结构）；22 个 spec 经共享 `statusField()` 补 lifecycle 状态字段（port/fiber/customer/vpn/ring/igp 有据跳过）——无状态资源 **98% → 8.3%**；`resources` 新增 `geo_lat/geo_lng` REAL 列 + 索引（**migration v20**，json_extract 存量回填——1,288 行坐标列化）；`ResourceRepo.Nearest`（bbox 预筛 + haversine 精确排序）+ CLI `query nearest` 出口。
- **层级/关系（#42）**：基站 1,198 条 `uplink_to`（**站坐标 haversine 最近邻派生**——坐标派生非名称匹配，不违反「禁止名称推断」红线）；电路 10→32 条三族（相邻环段 15 + 跨域长距 14 + 接入层 3）——工作台 10 链路/20 接口 → **32 链路/64 接口**；孤立资源 **75.6% → 10.2%**。
- **数据质量（#40）**：`serves` 直连边 1,228 条（设备 30 + 基站 1,198，地市→客户映射披露为模拟）；`protection_group` + `protects` 补齐（4 组三类型）；`ptp_port` 经 samples 脚本通道纳入 demo-real.sh（2 类无数据 → 1 类）；端口 `mounted_on` 90 条；ip_pool「使用中」/service「开通」非法枚举值修正（13 条 W002 → 0）。**问题清单全清零，概览页质量评分 51 → 100**；关系 3,103 条、28/29 类型有数据（in_vpn 主动不做，#40 优先级低项）；manifest + data-sources.md 双处逐项披露。

### 修复 — 地理视图交互成本（#43，P2，用户现场反馈卡顿根因）
- 选中态与几何彻底解耦：`buildGeoData` 去掉 selected 参数（几何只随 topology/坐标系变化），链路选中高亮走独立单要素源 `links-selected`（点击 = 对 0/1 要素源的一次 O(1) setData，主源不再全量重下）。
- 聚簇增量化：视口裁剪（±阈值带，数学上不改变可见结果）+ 以成员 id 集合为 key 的差分维护（复用未变 chip、只增删差集、派生态就地刷新）——替代每次 move/zoom 帧的全拆全建；「≤几百设备」的过时规模假设（被 1,198 基站打破）连同其代码注释一起清除。
- 塔重建只在 3D 视图逐帧跑（2D 塔层隐藏时逐帧 setData 是纯浪费）；zoomend 恒补一次防陈旧几何。

### 修复 — 工作台布局（#44，P3，用户现场反馈四诉求）
- 页头三行 → 一行（计数 chip 并入标题行，长名截断）；七个动作按钮并入画布工具带首组（组间 wrap，窄画布不溢出）；面板**默认折叠**（画布全宽），点击设备/链路等指向面板的交互自动展开（仅会话态不落盘），显式切换的 localStorage 偏好不受影响。

### 新增 — CLI 图查询通道（#41，P3，audit §2.5；附 audit §2.6 契约修复）
- `query traverse / impact / context / nearest` 四子命令——与 HTTP 端点**同一组合根装配**（`services.GraphStore` 唯一 store 绑定 + `services.GraphContext` 唯一上下文装配，api 处理器改为委托），CLI 与 HTTP 输出**逐字节一致**（跨通道 e2e 以 JSON 相等钉死 + 10 项用法错误退出码）；脚本化巡检（导入→检测→图查询）无需先 `serve`。
- 空集合契约（audit §2.6）：traverse `nodes`/`edges`、impact `impacted`/`candidates`、context 各集合无结果时恒为 `[]` 非 `null`（严格客户端的 len()/迭代不再需要判空分支）。

### 深入开发收口（W31，发布后追加）
- `in_vpn` 演示数据补齐（端口 所属VPN实例 列，90 条）——**29/29 关系类型全覆盖**（#40 的「优先级低」项收口；仍零 W001）。
- **地图↔台账联动**：基站图层要素补站编码（`patch_station_ids.py` 确定性补丁 + extract.py 源级修正，坐标匹配 1,198/1,198 验证）——2D/3D 地图点击任一真实基站 → `GET /api/resources` 解析 → 跳转 `/resources?focus=<id>` 详情抽屉（17 列台账属性 + 关系 + 穿透分析）；hover 信息条覆盖解析失败/未导入台账两种降级提示。
- audit §2.4 稳健性钉死：`TestIngestNumericCells`——真实数值单元格（非文本）的 perf 表全链路摄入实测（双格式均可，点值精确 round-trip）。

### 测试与文档
- 测试计数：429 → **435** 顶层（+3 migration v20：列/回填/最近邻；+2 图查询 e2e：跨通道一致 + 最近邻实测——期间 4 个旧 max-version 钉 19→20 为必要适配）；全量套件 3×全绿（23 包，含负载证据）；`GOOS=windows` 交叉编译 vet 通过。
- e2e_realdata 钉死更新（1,991 行 + 4 PTP = 1,995 资源、关系计数、工作台 32/64/30、geoLat/geoLng 与状态覆盖 API 断言）；manifest/data-sources.md/architecture.md/api.md/README 同步。
- 验证脚本：`scripts/w30_verify.sh`（数据验收度量：孤立率/状态覆盖/问题清单/关系分布）。

## [0.8.0] — 2026-10-13

**真实公开数据里程碑**（用户指令：「数据用互联网公开数据填充，完全找不到的用模拟数据」；实施轮 W26-W28）。核心：演示数据集以互联网公开数据为骨架——OSM（局站/干线走廊几何）+ OpenCelliD（1,198 个真实黑龙江基站）——模拟数据仅填充公开渠道不存在的台账细节并双处披露（`docs/data-sources.md` + `testdata/realdata/manifest.json`）。裁决与边界见 [ADR-0015](docs/adr/0015-real-public-data.md)。

### 新增 — 真实公开数据集（W26/W27）
- `scripts/realdata/` 三件套（extract / workbooks / metrics，全确定性——同输入字节级同输出，md5 双跑验证）：OSM PBF + OpenCelliD 快照 → 7 条干线走廊（哈大齐/哈牡/哈佳/佳牡/齐加/加漠/哈伊，按线路名选取绥满高速 G10/滨洲线/嫩林线/京漠线 G111 等真实公路/铁路几何，RDP ~150m）+ 83 个真实局站（OSM node id 派生身份）+ 1,198 真实基站（**主小区 CGI 为站编码**，~110m 网格聚类，844 个 NR 5G 小区）。
- `testdata/realdata/`：31 本工作簿共 1,965 行（全 33 类型覆盖 L0-L6），**导入零失败**；真实列（局站/坐标/地市/光缆 A-Z 端与锚点链测地里程/基站属性/接头盒人井真实坐标）与模拟列（机房/纤芯占用/波道/电路/IP/客户/业务等，含虚构标记）逐字段分列披露。
- 端口性能 7 日（90 序列，确定性形态）：正常日稳定 + 哈大齐两站光功率 7 日 -8→-24 dBm 线性劣化 + 丢包率 0→4.8% → 种子检测规则触发 → firing 告警 → run_state 联动（真实告警旅程）。
- 许可合规：OpenCelliD 派生物按 CC BY-SA 4.0 披露（UI attribution「基站: OpenCelliD (CC BY-SA 4.0)」+ 文档级声明）；高德 POI 因再分发限制**不内嵌**（在线底图角色不变，ADR-0015 裁决）。

### 新增 — 地理视图真实网络图层（W28）
- 两个离线叠加图层（双坐标变体，任意底图可用）：**光缆干线**（琥珀色走廊 + 线路名/锚点链里程标注，铁路走廊细虚线分层渲染，默认开）与**无线基站**（1,198 真实站点，运营商配色移动蓝/联通红/电信绿/广电紫，点径随小区数，默认关一键开）；图层面板新增「真实网络数据」分组。
- 实测修复两处样式陷阱：maplibre `line-dasharray` 不支持数据驱动表达式（改为 filter 分层 + 静态 dasharray）；`match` 表达式标签/值必须平铺（嵌套数组使**整个样式**校验失败——连锁导致旧图层也全不渲染，浏览器级回归抓出）。

### 新增 — 演示与测试（W28）
- `scripts/demo-real.sh`：一键全链路（31 工作簿导入 → 7 日指标摄入 → 检测运行 → 工作台物化 → serve → 哈大齐光缆置 down → 影响面 80 节点跨层波及（circuit/customer/service/leased_line）+ 根因候选 + firing 告警 + 统计汇总）。
- `scripts/w28_browser_regression.sh`：浏览器级图层回归 14/14（图层存在/源加载/渲染要素 14 条/面板分组与开关初始态/开关真切换 visibility=none/3D 图层保持/console 零错误）。
- `internal/app/e2e_realdata_test.go`：31 工作簿导入链 + 计数钉死（1,965 行/491 关系）+ 跨层影响链（OC-HDQ down → customer/leased_line/service/circuit 全可达）——数据集漂移即红。
- `internal/webui`：新图层资产契约测试（geo+json + max-age + FeatureCollection 内容级断言）。

### 版本与文档
- 版本四处事实源同步 v0.8.0（domain/i18n/api.md 基线 + CHANGELOG 头，双 pin 测试把关）。
- 新文档：`docs/data-sources.md`（逐数据类的来源/真实性口径/许可/重现管线，含众源覆盖偏差如实说明——OpenCelliD 黑龙江集中于哈大两市，不代表市场份额）、`docs/adr/0015-real-public-data.md`（嵌入边界裁决）；README 能力条目与合规声明补「公开许可真实数据」一类。
- 测试计数：427 → **429** 顶层（+1 `internal/app` e2e_realdata 导入链钉死 +1 `internal/webui` 新图层资产契约）；版本钉死测试 TestAppVersionP2 随 v0.8.0 同步。

## [0.7.0] — 2026-10-13

**数据模型 v1.0 里程碑**（issues #16-#29，实施轮 W19-W25——七轮全部完成、14 个 issue 全部关闭、零降级）。核心：资源图从 8 类型/4 关系扩容到 **33 类型/29 关系**（七层模型 L0 空间…L6 业务全层），并围绕这张图补齐时序观测、运行状态、层序遍历、影响分析、检测告警与**无人值守导入**——「每天导入表格即可完成日常巡检」的完整底座。

### 新增 — 数据模型扩容（W19/W21，#16 #25 #26 #29）
- 25 个新资源类型：物理层（光缆/光缆段/纤芯/ODF/ODF端子/接头盒/机架/管道/人井/杆路/板卡）、传输层（OTN波道/SDH时隙）、逻辑层（环网/保护组/IGP域/VPN实例）、业务层（客户/业务实例/专线）、接入层（BBU/RRU/OLT/ONU/PON口）；迁移 v15+v16（ForeignKeysOff 保守重建，既有数据零改动）。
- 25 个新关系（`carried_by`/`routed_over`/`uses_fiber`/`fiber_of`/`subscribed_by`/`provides_by`/`member_of_ring`/`uplink_to` 等 29 总数），**关系方向注册表**（FromTypes/ToTypes/CrossLayer）成为可执行代码，配一致性门禁（派生边必须落在注册表契约内；同层判定 from×to 单调）。
- **四点一致性测试**：domain 枚举 ↔ SQL CHECK ↔ `/api/schema` ↔ web 镜像（直读 web/src/schema.ts）防漂移；层次/专业域元数据入 `/api/schema` 投影。
- B8 自然键规范化（trim+全角折叠+大写，前导零保留——01 与 1 是两个身份，`" cable-01 "` 与 `CABLE-01` 是一个）；SuggestType 表头/表名双阶梯建议。

### 新增 — 时序观测（W20，#18 #21 #27）
- 三表族（指标批次/序列/点）+ 摄入通道（CLI `ingest` + HTTP 4 端点，**同文件幂等重导**）+ `synthdata` 确定性生成器（同 seed 字节级相同）+ 异常扫描端点；指标目录 14 键+同义词+方向+建议界。
- 量级实测（#27）：30 万行/240 万点导入 P95 9.7s、异常扫描 ~1s、306.5 B/点；全量外推触发阈值 → 阶段 2（日聚合）裁决已文档化。

### 新增 — 运行状态与图查询（W22，#22 #19）
- `resource_status` 历史表 + 资源快照双写（台账与运行分立——B1 方案 C）；manual 先行，alert 源保留给检测引擎。
- **层序 BFS traverse/impact/context 三端点**（§10.3-10.5）：跨层穿透、根因候选 vs 受影响面分离、层/关系过滤、预算与防环四规则；§10.9 六项验收全绿。

### 新增 — 检测与告警（W23，#23）
- `detection_rules`/`alerts`（迁移 v18）+ 纯函数判定引擎（末尾连续 window 点越限去抖、确定性告警 ID）+ 告警状态机三边（firing→acked/cleared）+ run_state 联动（source=alert）；规则不改代码即可新增（HTTP CRUD + 5 条种子）。

### 新增 — 无人值守导入（W24，#28）
- `mapping_templates`（迁移 v19）：表头指纹（sheet 名+规范化表头序列 sha256，**列序敏感**）命中即自动执行——`POST /api/imports?auto=1` / `import --auto`；表头任何变更（改名/增删列/换序）→ 指纹变化 → **拒绝并点名 sheet**（批次回落 awaiting_mapping 人工兜底，从不静默错映）。
- 模板一键捕获（`templates save` / `POST /api/imports/{id}/template`）、幂等 upsert、回滚不对称（模板是人工确认产物，批次回滚不删模板）。**七日无人值守测试**：7 本同表头工作簿连续全自动、零人工介入。

### 修复与收口 — W25（#16 #20）
- **`leased_line.customer_code` 规格补齐**：设计依据真实专线表 23 列，但实现漏了「客户名称」列——按客户聚合专线不可答。补 ref 字段派生 `subscribed_by` 边（专线直连客户）。
- **嵌入前端陈旧构建修复**：dist 是 W21 前构建（各轮只跑 tsc 未重建），真实浏览器缺 25 类；`make web` 重建后浏览器级验收钉死。
- 终局验收 `scripts/w25_acceptance.sh`：14 sheet 全层工作簿（真实表头）→ 33 类型 UI 浏览器断言 12 项 + 四层模型 HTTP 断言（层次穿透 L6→L2 / 业务影响含客户专线 / 保护结构 / 客户聚合）5 项 + 导入 7 项，24/24 全过。

### 门禁
- gofmt/vet 0；**427 顶层测试全绿**（+13）；-race（mapping/importer/repo/api/app/detect/topology）0 race；tsc 0 错；demo.sh E13/W3/I1 与 demo-lab.sh 哈希 aea27501 基线不变；MCP 工具面冻结恰 5；版本三处事实源对齐 v0.7.0（`TestAppVersionP2`/`TestAppVersionMatchesChangelogHead` 钉死）。

## [0.6.4] — 2026-10-12

**地表覆盖轮**（issue #15：用户反馈「2D/3D 全是陆地、没有水、没有森林」）。核心：离线地图数据此前只提取了道路/铁路/行政边界三类线划——`natural`（自然）与 `landuse`（土地利用）两大类整体缺席，2D/3D 因此呈现为一片空白陆地。本轮从既有 PBF 重提**全量自然要素**并把离线底图升级为浅色「简洁底图」设为默认（用户要求：2D 干净、类高德标准地图但无商铺标注；3D 能看见实际高度、有真实河流）。

### 新增 — 自然地表覆盖 + 地名（29,449 条要素 + 17,615 个地名点）
- `scripts/gen_natural.py`（可重跑）：GDAL OSM 驱动自定义 `osmconf.ini` 导出（关键指令 `[general].closed_ways_are_polygons`——首版缺失导致闭合 `natural=wood` 面全部掉进 lines 层被丢弃，12,931 块森林只剩 571）；导出端值白名单预过滤（residential/industrial 等不进文件）→ shapely 分类白名单 → **分级 RDP 简化**（森林 ~90m / 湖岸 ~55m / 河流 ~30m / 溪流 ~24m——保住大兴安岭树枝状水系形态）→ 最小面积/长度过滤（≈0.5–1.7 ha 阈，省级视野不可见的最小斑）→ 坐标量化 5 位小数 → 紧凑 GeoJSON gzip。
- 产出（`web/public/mapdata/`，go:embed 随二进制分发）：`hlj-natural.geojson.gz`（4.09 MB，WGS84）+ `hlj-natural-gcj.geojson.gz`（4.10 MB，GCJ-02 预投影）——面 + 线混合 FC，properties `{c}`/`{c,n}`（类别/名称）；`hlj-places.geojson.gz` + `-gcj`（各 0.26 MB，地名点）。类别构成：森林 12,955 / 水域面 4,961 / 河流 3,591 / 溪流 2,309 / 渠 1,113 / 草地灌木农田湿地 4,557。GCJ 变体 numpy 向量化（摊平坐标一次变换再按遍历序回填），首坐标偏移 +0.0050°E/+0.0023°N 实测正确。
- 前端 `geoBasemap.ts`：`natural`/`places` 双源按底图坐标系选变体（高德 = GCJ 预投影，**任意底图可用不偏移**——与等高线同策略）；新增图层组：森林面（z≥4.5）/ 草地农田湿地面（z≥6，分类配色）/ 水面（z≥4）/ 河流线（z≥5，宽随 zoom 插值 0.7→2.6）/ 溪流渠线（z≥7）/ 湖库名（z≥8）/ 河名沿线（z≥9.5）/ 地名四级（市 12.5px z≥4.2、县 11px z≥6.5、乡镇 10.2px z≥8.8、村 9.4px z≥11）。浅色主题 Amap 系配色，深色主题（ESRI/卫星）配暗色变体。

### 新增 — 离线简洁底图（默认）+ 主题化配色
- **默认底图换为 offline（浅色简洁）**：暖白底 `#f5f3f0` + 森林绿 `#c6e2be` + 水面蓝 `#a9cff0` + 白路灰壳 + 等高线棕——类高德标准地图观感，**无任何商铺/POI 标注**（自有矢量数据天然无 POI），内网/离线环境开箱即用；高德街道/卫星保留为可选在线底图（实测瓦片与 REST key 均可达，但其栅格自带 POI 无法去除，故不再默认）。
- `BasemapDef` 新增 `theme: light|dark`：叠加图层/自然要素/等高线/地名/山体阴影按主题取色（浅色 = 暖灰 hillshade + 白壳道路 + 棕等高线；深色 = 原夜蓝系不变）；浅色主题公路加 casing（白路在暖白底上需描边才可见），线宽 = 原等级宽 + 1.6/1.2；加载期底色 `basemapBg(effective)` 动态防闪白。
- **偏好存储键升级 `netops-lab.geo.layers.v2`**：新增 forest/water/green/places 四开关（默认全开），公路默认改为开（用户要「河流交通这些」），默认底图换 offline——旧 v1 偏好（basemap=amap、road=关）不沿用，升级即见新观感。

### 修复 — 3D 自然呈现（地表覆盖贴 DEM）
- 3D 视图自然面/水系线直接贴在既有离线 DEM 地形上渲染（MapLibre 图层随 terrain 拉伸）：森林给山脉上绿色、河流随山谷走势、湖面嵌在海拔正确的高度——呼应「3D 能看见实际高度的比较自然的、真实地理数据要有河流」；2D/3D 相机独立性（#12）与地形夸张分档（#13）行为不变。

### 门禁与事实源
- 浏览器回归扩展至 **35/35 PASS**（新增 N1–N11：natural 源加载 ≥2 万条、森林/水面/河流/草地默认可见、地名图层存在、公路默认可见、默认无栅格底图源、四份资产 200、高德切换 GCJ 变体、切回 WGS84 恢复；E0 面板分区、E4 森林开关、E5 水系开关）；`tsc -b` 零错、`vite build` 过（dist ≈22.9MB，+8.7MB 自然要素）；Go 全测全绿。版本矩阵四处对齐 v0.6.4，`TestAppVersionP2`/`TestAppVersionMatchesChangelogHead` 钉死。截屏仅存 repo `tmp/`（不进交付物）。

## [0.6.3] — 2026-10-12

**用户反馈地图轮**（issue #12/#13/#14 一次收口 + 发布断档修复）。核心：针对用户对 v0.6.2 现场反馈的三条缺陷——2D/3D 相机「锁在一起」、初始视野比例不合适、地理高程支撑不可见——给出根因级修复；并修复发布流水线断档（v0.4.0–v0.6.2 只有 tag 没有 Release，用户实际在跑 v0.3.0，之前多轮地图修复从未送达）。附带全仓 `gofmt` 空白规范化（6 个历史空格缩进文件）。

### 修复 — issue #12：2D/3D 相机锁联 + 初始视野比例不合适
- **相机锁联**：GeoCanvas 单实例承载 2D/3D，center/zoom 全程共享（仅 ±1.3 zoom 补偿 hack），在 2D 平移后切 3D 即继承，反之亦然——「两个图锁在一起」。修复：**每视图独立相机快照**——离开视图时存（center/zoom/bearing/pitch），进入时恢复；首次进入按该视图 fitBounds；经拓扑图中转 = 组件卸载重挂、快照丢弃 → 重新适配（行为可预期）。±1.3 补偿 hack 移除；用户在 2D 的自定义 bearing 不再被切回时强制归零（回归 G2 断言钉住）。
- **默认比例**：2D fitBounds `maxZoom` 9 → **12.5**（城市尺度数据不再被压远、同城设备初始视野即可分开）；留白 80px 定值 → **容器短边 8% 自适应（夹 [24,80]px）**（小画布上固定 80px 吞掉 1/3 视野）；3D 上限 7.2 → 8.6（城市尺度 3D 近观塔体与地形）；聚簇展开 `maxZoom` 从「当前 +2.2」升级为 **max(当前 +2.2, 成员 bbox 的 cameraForBounds 解)**——哈尔滨 3 台一次点击即从 z4.8 拆簇到 z11.5（旧版需点两次）。

### 新增 — issue #13：地理高程支撑可见化（等高线 + 自适应夸张 + 海拔读数）
- **根因**：v0.6.2 的 `setTerrain` 已绑定 dem 源，但整省远观（z≈5.5，视域 ~400km）1500m 起伏 ×1.2 夸张占屏 <1%——**视觉全平**（VLM 复核截图确认）；2D 只有 hillshade，无等高线与海拔读数——「地理高度地图」缺位。回归只断言 terrain 绑定，未断言视觉有效性。
- **等高线图层**：`scripts/gen_contours.py` 从同一批 97 块 z8 terrarium 瓦片 marching squares 生成——**200m 首曲线 / 500m 计曲线 + 计曲线高程标注**（18,604 条折线、29 万点、RDP 1.5e-3° 简化），gzip 1.4MB 随二进制内嵌；**GCJ-02 预投影变体**（逐顶点 wgs84ToGcj02，高德底图上不偏移）+ WGS84 变体双分发——等高线**任意底图可用**（不随 GCJ 底图禁用，区别于行政区划/铁路/公路叠加）。图层面板独立「等高线」开关（默认开）。
- **自适应地形夸张**：`terrainExaggerationFor(zoom)`——z<6.5 → 4（整省远观起伏可见）、6.5–8 → 2.5、≥8 → 1.3（近观回真实比例）；zoomend 后按当前档位重挂 setTerrain。浏览器回归 C3/C4 断言远观 ≥2.5 / 近观 ≤1.5，VLM 复核 3D 截图确认「山脉立体轮廓、山谷、凹凸感」可见。
- **光标海拔读数**：`geoElevation.ts` 客户端解码离线 terrarium 瓦片（fetch → ImageBitmap → OffscreenCanvas 像素 → `R·256+G+B/256−32768`）双线性采样，左下角实时 chip 显示「海拔 N m」；**GCJ 底图下单步近似逆偏移采样**（误差 <2m）；覆盖外显示「—（超出高程覆盖）」；瓦片 LRU 缓存 80 块、卸载清空。2D/3D、任意底图可用。回归 H1/H2：大兴安岭光标 772m、覆盖外降级「—」。

### 修复 — issue #14：发布断档——v0.4.0–v0.6.2 从未送达用户
- 事实核查：远端 tag 已至 v0.6.0、main 已至 v0.6.2，但 GitHub Releases 最新只有 **v0.3.0**——用户拿到的一直是旧二进制，其对「聚簇/地形/比例」的抱怨在 v0.3.0 上**全部字面成立**（v0.3.0 无聚簇、无 DEM、fit 更粗）。修复：v0.6.3 一站式发布（linux-amd64 二进制 + source tar.gz，含 DEM/等高线全量内嵌），发布后核对 `/api/health` 实报 v0.6.3 与资产清单；流程约定固化为**截屏仅存 repo `tmp/` 作回归证据，永不进入 download/ 与 release 资产**（用户明确要求）。
- **连带挖出 Makefile 配方回归**：v0.6.2 给 `build`/`build-testdatagen` 加 `BIN_EXT`（issue #3）时配方被写成 8 空格而非 TAB——`make build` 自 v0.6.2 起 `missing separator` 静默失败（门禁都直跑 go test/tsc 未经过 make，bin/ 里躺着 v0.6.1 旧二进制，打出的包自然是旧货）。修复：配方行统一转 TAB；发布产物验证改为「解包→serve→`/api/health` 版本 + 资产字节数」实测（本次就拦下了旧二进制假包）。

### 门禁与事实源
- Go 21 包全绿（347 顶层测试）；`gofmt`（含 6 文件空白规范化）/`go vet` 干净；`tsc -b` 零错；`vite build` 过（dist 14MB，含 97 DEM 瓦片 + 等高线双变体）；浏览器回归扩展至 **21/21 PASS**（新增：等高线默认可见/源加载、远观/近观夸张分档、等高线开关、相机独立性 G1/G2、海拔读数 H1/H2）。版本矩阵四处对齐 v0.6.3（domain.AppVersion / i18n.APP_VERSION / README MCP version / api.md 基线），`TestAppVersionMatchesChangelogHead` 钉死 CHANGELOG 头。

## [0.6.2] — 2026-10-12

**运维加固轮**（W17 单轮：剩余 6 个 open issue 全部处置 + 用户反馈的两条地图体验问题）。核心：把 v0.6.1 之后剩余的跨平台/契约类缺陷（#3–#8）一次收口——Windows 测试可跑、无名 sheet 不再静默零行导入、report --out 不再静默无输出、负数分页不再静默忽略、跨库错配一步可辨；地图侧响应现场反馈完成两件事——2D 设备**聚簇**（修「锁在一起/默认比例不合适」）与 3D 视图**真实离线 DEM 地形**（修「2.5D 无地理高度支撑」）。issue #10（能力差距）以 ADR-0011 设计决策记录处置。

### 修复 — issue #3：Windows e2e 测试构建的临时二进制缺 `.exe` 后缀（11 个测试失败）
- `internal/app/e2e_test.go` 增加 `exeSuffix()`（`runtime.GOOS=="windows"` → `.exe`），TestMain 构建产物在 Windows 可被 `exec.Command` 执行；`Makefile` 的 `build`/`build-testdatagen` 同步追加 `BIN_EXT`（`go env GOOS` 探测），`make build` 在 Windows 产出可运行的 `bin/netopslab.exe`。Linux 行为零变化。

### 修复 — issue #4：类型识别仅依赖 sheet 名 → 标准导出（Sheet1）整表静默跳过
- 新增 `mapping.SuggestTypeFromHeaders`：sheet 名无 SheetHint 时按**表头行**推断类型——逐 spec 统计字段命中数（exact/normalized/synonym 同 bestMatch 阶梯），**完整自然键在场 + 必填字段过半**才合格（无身份键不导入，宁跳勿错），分最高者胜、同分取 Specs 序（确定论）。`Suggest` 与 `SuggestMapping` 均接入：issue 复现的 `Sheet1` + 设备名称/端口名称/设备IP/带宽表头 → 建议 port import（此前 action=skip →「导入成功但 0 行」的静默失败）。新增 `suggest_headers_test.go` 六测（含无自然键守卫、port 优先于 device 的判别、Sheet1 端到端不再跳过、列建议限定候选集）；`importer` 钉住旧行为的测试改钉新契约。

### 修复 — issue #5：Windows TempDir 清理失败（3 测试）与非法文件名（1 测试）
- modernc.org/sqlite 在 Windows 上释放文件句柄滞后于 `Close()` 返回，t.TempDir 的 RemoveAll 报「directory is not empty」→ 测试被记失败。修复：repo/sim 两包各置 `tempDirT`（MkdirTemp + 清理重试 ~1s、残余失败记日志不判败；POSIX 首试即成、永不 sleep）。`TestOpenPathWithSpecialCharacters` 的 `?` 路径在 Windows 非法 → 平台分支改用合法但同样考验 DSN 编码的字符（空格/`#`/`%`）。

### 修复 — issue #6：`report --format json --out` 退出码 0 但 stdout 空
- §10 契约（每命令 stdout 恰好一个 JSON 对象）此前对 `--out` 路径失守：文件写完、stdout 沉默，脚本无法区分成功与失败。修复：`--out` 时 stdout 输出 `reportResponse` 摘要（file/format/scope/totalResources/qualityScore/issuesBySeverity——对齐 export 命令的模式），json/html 两格式一致；无 `--out` 行为不变。新增 `TestCLIReportOutStdoutSummary` 双格式断言。

### 修复 — issue #7：API 分页负数参数静默忽略
- `limit`/`offset` 此前是**未知名**，任何值都被忽略（负值与「已生效」不可区分）。修复：`parsePagination` 支持 REST 风格双接口——`limit`（1–100，>100 钳制 100）+ `offset`（0 起，页号 = offset/limit+1）；非数值/负数/零 → 400 `bad_request`（消息说明取值范围，与枚举参数的严格性对齐）；两接口混用（page+offset、pageSize+limit）→ 400（显式冲突优于隐式优先级）。全部列表端点（resources/issues/batches/workspaces 三处）经共享 helper 一体获得。新增 `pagination_issue7_test.go`（校验 8 例 + 语义 3 例 + 既有 page/pageSize 行为回归）。

### 修复 — issue #8：CLI 与 serve 使用不同 DB 时无提示（易误判数据丢失）
- 三处可见性：① `GET /api/health` 新增 `dbPath`（服务端绑定库的**绝对**路径）；② 工作区类 404 信封追加「this server is bound to database <abs>——cross-database mismatch (serve --db vs CLI --db) is the usual cause」；③ `lab workspace create` 输出嵌入 `MaterializeResult` 并新增顶层 `db` 字段（绝对路径），与 serve 启动日志的绝对路径一行比对即分辨「连错库」与「数据丢失」。serve 启动日志同步升级为绝对路径（相对路径按工作目录解析会误导）。新增 health/404/create 三组回归测试。

### 新增 — 2D 设备聚簇（用户反馈：「2D 图锁在一起，默认没有按合适的比例缩放」）
- 根因：初始 fitBounds 是全省视野（数据跨 7° 纬度），同城设备屏幕间距仅 1–2px，编码牌堆叠成黑块。修复：`geoCluster.ts` 贪心 leader 聚簇（纯函数）——任意两个可见项（簇心/单例）屏幕距离 ≥56px，成员吸附最近 leader；`GeoCanvas` 在 move/zoom（rAF 节流）与数据/选中变化时重算，簇渲染为计数气泡（32px 圆徽 + 故障红边 + 故障角标 + 选中蓝圈，键盘可聚焦），被吞并设备的独立地标隐藏；**点击簇 fitBounds 成员 bbox 放大展开**（maxZoom +2.2），簇随缩放拆分。演示数据下：10 设备 → 2 簇（哈尔滨 3 + 大兴安岭 2）+ 4 个可读单例。GCJ 底图下投影与 Marker 同源，坐标系无关。

### 新增 — 3D 真实离线 DEM 地形（用户反馈：「2.4D 地图没有地理高度地图支撑」）
- **数据**：AWS Terrain Tiles（Mapzen 数据管线，SRTM/GMTED 融合，terrarium 编码，公开免密钥）——黑龙江 bbox（121.6–128.2E / 45–53.6N）z4–z8 共 **97 瓦片 ≈6.4MB** 随二进制内嵌（`web/public/mapdata/dem/`，`scripts/download_dem_tiles.py` 可重跑扩展）；z8 原生 ~300m/px，更高 zoom 由 MapLibre 父级上采样。
- **渲染**：`geoBasemap` 注册 `dem` raster-dem 源（encoding=terrarium，minzoom 4/maxzoom 8）+ hillshade 图层（底图之上、业务图层之下，深色主题柔和参数）；`GeoCanvas` 在 3D 视图按 `map.setTerrain({source:'dem', exaggeration:1.2})` 挂起伏、回 2D/关偏好显式卸载（2D 无视觉贡献但占渲染开销）；**图层面板新增「地形高程」开关**（默认开，localStorage 持久化；任何底图可用——DEM 是本地栅格，不受 GCJ/WGS84 底图限制）。setStyle 重建后 terrain 自动重挂（reapply 路径）。webui 对 `/mapdata/dem/**` 统一 1h 缓存（新增 PNG MIME + 磁头测试）。
- 浏览器级回归 `scripts/w17_browser_regression.sh`：**11/11 PASS**（初始聚簇 ≥2、点击展开 zoom 4.60→6.80、3D terrain=dem、hillshade 可见、页面实际拉取 76+ 张 DEM 瓦片、开关卸载/隐藏、零页面错误）。

### 设计 — issue #10：能力差距 ADR（数据异常检测/设备告警/趋势预测）
- `docs/adr/0011-ops-capabilities.md`：四个能力的优先级与数据模型设计（时序指标表、检测规则引擎与 E/W/I 规则目录的关系、告警状态机、预测算法选型），以及 MCP 工具面的红线决策（保持「恰好 5 个只读工具」冻结，分析类能力走 HTTP + Web 面板）。按 issue 建议以设计记录形式处置（不实现）。

### 门禁与事实源
- Go 21 包全绿（含新增：suggest_headers ×6、pagination/health/404 ×4、e2e report-summary + lab-create-db ×2、webui DEM ×1）；`gofmt`/`go vet` 干净；`tsc -b` 零错；`vite build` 过（dist 含 97 张 DEM 瓦片）；浏览器回归 11/11。版本矩阵四处对齐 v0.6.2（domain.AppVersion / i18n.APP_VERSION / README MCP version / api.md 基线），`TestAppVersionMatchesChangelogHead` 钉死 CHANGELOG 头。

## [0.6.1] — 2026-10-12

**地图修复轮**（W16-D 单轮开发 + W16-Q 独立核验；需求驱动为 v0.6.0 发布后的四条地图缺陷 issue——#1 画布高度塌陷、#2 Windows `.mjs` MIME、#9 3D 巨型光柱、#11 底图单一不可切换）。核心：把「能看地图」修成「能用于运维的地图」——画布塌陷与 Windows Worker 加载两处功能级失效归零；底图从硬编码 ESRI 极简变为四种可切换模式（高德街道/高德卫星/ESRI 极简/离线极简），黑龙江本地 OSM 行政区划/铁路/公路叠加数据随二进制内嵌分发（内网可用）；3D 柱体高度获得物理钳制与正确像素换算；新增图层面板让画布自己决定显示什么（铁路默认关闭，不干扰主观看）。

### 修复 — issue #1：2D/3D 地图画布高度塌陷（所有平台）
- `.geo-map` 与 MapLibre 追加的 `.maplibregl-map`（`position: relative`）同为 (0,1,0) 特异性、vendor CSS 后加载胜出 → `absolute/inset:0` 失效 → 容器高度塌 0、canvas 回退 300px、地图黑屏。修复：`global.css` 提升为 `.geo-canvas .geo-map`（(0,2,0)，结构上恒为父子、稳定）。浏览器级回归实测画布 756×544（修复前 756×0）。

### 修复 — issue #2：Windows 下 `.mjs` 静态资源 MIME 错误（MapLibre Worker 加载失败）
- Windows 注册表无 `.mjs` 条目 → `mime.TypeByExtension` 回退 `text/plain` → 浏览器拒绝将其加载为 ES Module Worker。修复：`internal/webui/webui.go` 显式钉死现代资源 MIME 表（.mjs/.js/.geojson/.wasm/.webp/.avif），不依赖平台注册表；Linux 行为不变（同值）。附带修复：目录路径（/assets/、/mapdata/）此前会走 FileServerFS 目录列表，现统一 SPA 回落 index.html。新增 `webui_test.go`（MapFS 隔离测试：MIME 钉死 / geojson.gz 头 / SPA 回落 + 真实 dist 冒烟）。

### 修复 — issue #9：3D 节点巨型光柱（宽度随缩放漂移 11 倍 / 高度 62km / 无地形说明）
- **metersPerPixel 基准修正**：v0.6.0 用 256px 瓦片基准（156543·cos/2^z），MapLibre 世界坐标实为 512px（worldSize=512·2^z）→ 系统性放大约 2×，叠加重建时机漂移实测底面宽 19→217px。修正为 78271.517·cos(lat)/2^z。
- **footprint 语义修正**：`TOWER_FOOTPRINT_PX=7` 为直径语义，构建取半宽（此前当半径用 → 实际 14px）。
- **高度物理钳制**：塔高（米）= 像素高 × mpp，数据驱动 `heightM` 钳制 [60, 4000]——不再出现数十公里光柱、柱体不再冲出画布遮挡底图。zoom 动画期间 rAF 节流同帧重建 footprint 与高度（恒定 7px 屏宽跟手，不再「动画结束才跳变」）。
- **3D 定位诚实声明**：UI tooltip 与 README 明示「3D = 倾斜相机示意（柱高示意连接数），非地形高程」（无 DEM 数据源，issue 建议的两个方向中取文档化方向）。

### 新增 — issue #11：底图四模式 + 离线叠加 + 图层面板
- **底图注册表**（`web/src/components/lab/geoBasemap.ts`）：高德街道（webrd style=8 栅格，中文标注/行政区划/交通）、高德卫星（webst style=6 + style=8 路网标注透明叠加）、ESRI 极简（原 v0.6.0 底图保留）、离线极简（无外网瓦片：画布底色 + 本地叠加）。高德 key 按维护者要求嵌入代码（栅格端点免鉴权，key 预留 Web 服务 REST 扩展）。attribution 随源切换（高德 / Esri / © OpenStreetMap contributors）。
- **GCJ-02 坐标契约**：业务数据按 WGS84 存储，高德（火星坐标）底图渲染前逐点 WGS84→GCJ-02（国界外原样返回）；设备/链路/塔/fitBounds 全走同一投影入口。离线叠加仅在 WGS84 底图（极简/离线）下可用——高德底图上叠加会偏移 ~500m 且信息重复，面板中禁用并提示。
- **离线地图数据**（Release `mapdata-hlj-2026-10-12` 资产，© OpenStreetMap contributors）：黑龙江全省 147 行政区划（省/市/县三级边界+中文名）、17257 铁路线（滨洲线等，含名称）、27170 干线公路（含国道编号 ref）——RDP 简化 + 属性精简后共 1.71MB（.geojson.gz），放 `web/public/mapdata/` 随 vite 构建进 `internal/webui/dist`、`go:embed` 进二进制（内网单二进制即可用）。服务端以 `Content-Encoding: gzip` 透明分发（浏览器自动解压，MapLibre 直接 fetch JSON）。
- **图层面板**（「画布决定显示什么」）：右上角常驻折叠钮，展开三分区——底图（radio×4）/ 叠加图层（行政区划、铁路、公路）/ 业务图层（设备、链路）。偏好 localStorage 持久化（隐私模式静默降级）。默认：底图高德街道、行政区划开（运维需知道设备属于哪个区县）、**铁路关**（用户要求不得干扰主观看，可一键开）、公路关、设备链路开。
- **瓦片失败降级看门狗**：连续 ≥3 次底图瓦片错误 且 距最近成功 ≥4s → 自动切离线底图（叠加图层保留、chip 提示）；看门狗周期复查覆盖「错误波停在宽限期内不再重发」的停滞态。双保险防假阳性：相机飞动取消的在途瓦片，MapLibre 同样发 `AJAXError: Failed to fetch (0)`（与真实断网无法从消息区分）——成功数据事件即清零计数。降级后面板 radio 反映 effective 状态（离线选中），点回原底图是真实变更（重置降级重试），绝不卡死降级态。

### 已知限制（诚实声明）
- **3D 非地形高程**：倾斜相机示意，无 DEM 地形数据（见上方声明与 ADR-0010）。
- **离线标注字形**：叠加图层中文名依赖 OpenFreeMap 字形 CDN（免密钥，CJK 覆盖）；完全离线时线/面几何照常渲染、文字标注静默降级为无（后续可自建本地字形库）。
- **塔高钳制的取舍**：省级缩放下柱体屏高 ~5px（物理高度上限优先于「恒定可见」）——设备可见性由 HTML 地标承担，柱体为辅助示意；放大到城市级恢复 40-58px 屏高与连接数差异。
- 开发沙箱的 Playwright 浏览器对页面世界的 autonavi 请求存在封禁（curl/worker/隔离世界可达、ESRI 同路径正常），瓦片加载在用户真实浏览器不受影响——回归脚本已改为证据型断言（详见 W16 worklog 调试记录）。

## [0.6.0] — 2026-10-11

**治理三部曲终章与数据双向闭环版**（五轮迭代：W11 治理债务收口轮 + W12-W14 三轮功能开发（回源导出 / 关系重挂 / 治理总览）+ W15 发布轮收口；需求驱动为 v0.5.0 各轮质量审查与审后处置留下的治理债务清单（target_missing 失实口径、W006 深链任一侧可达性、冒烟验证资产丢失、api.md 契约基线并存 P3）与「导入数据经治理后可回源」的双向闭环、治理工作量一屏入口的演进需求）。核心：治理三部曲终章（W005 属性 / W006 身份 / W13 关系——ref 字段确认时同事务改挂派生关系），数据从单向导入走向双向闭环（确定性 xlsx 导出、回源幂等），治理工作获得一屏入口视角（SQL COUNT 聚合总览），并清偿 v0.5.0 的口径与验证资产债务。

### 修复 — 治理债务收口（W11-D，零迁移）
- **target_missing 诚实口径**：`importer.Rollback` 删除链先删本批 pending 提案再删资源——标准回滚后改名提案已消失，apply 实得 **404**（W6 时代「回滚后 apply 得 409 target_missing」的文档/注释口径失实）。409 `target_missing` 是防御性守卫，唯一典型可达触发 = 新资源被其它路径直接删除而提案幸存；错误文案、api.md、import-and-validation、web 行内注释等全仓触点统一为诚实口径，`TestAPIRenamesRejectAndRollback` 更名并钉住「回滚 → deleted.renameProposals=1 → apply 404」旅程。
- **renames `resource_id` 任一侧过滤**：W006 issue 的 EntityID 落在新资源侧，深链打开新资源抽屉时 `GET /api/renames?resource_id=<新id>` 此前查不到（待确认面板不渲染）——List 条件改 `(resource_id = ? OR new_resource_id = ?)`（COUNT/SELECT 共享同一 where 与参数序），repo/服务/HTTP/CLI 四层测试钉死，双侧抽屉均渲染（W11-Q 真浏览器补测关闭缺口）。
- **w7/w8 冒烟脚本重建**：环境重置丢失的两份验证资产按原规格重建并实测 SMOKE-OK（SourceRef 版本化三轮 + 回滚随批消失 deleted 记数；PTP 脚本 4/0/0 + W006 协同 apply 键迁移 + 零警重导）。

### 新增 — 资源导出通道与回源幂等（W12-D，零迁移）
- **确定性导出**：`internal/exporter` 纯函数核心（Build/BuildBytes/WriteFile）——每个有资源的类型一张 sheet（8 张固定名字表，`SuggestType` 回投不变量钉死）、表头 = spec 全部字段 Label、行 = 当前属性快照（含 W005/W006 apply 后状态）、行序 = natural_key 字典序。excelize v2.11.0 输出全链无 wall-clock（zip 条目时间戳恒 1980-01、文档属性时间为库内常量），钉**字节级确定性**：同库同参数两次导出逐字节一致、输入顺序无关、CLI 与 HTTP 跨通道字节相同；文档属性时间为常量（真实导出时刻不落盘）的代价在 api.md 与 §19 两处诚实声明。
- **键分量三规则回填**（朴素 `split('|')` 取首段必破坏幂等——base_station 退化键「哈尔滨|基站名称」会写错编码列，负对照测试红）：①单键类型键列 = 键整体不拆（键含 `|` 亦无损）；②复合键（port/ptp_port）末分量 = Resource.Name，`TrimSuffix(key, "|"+Name)` 精确还原前缀（分量自身含 `|` 也无损）；③条件键（base_station）退化行按 KeyFallbackFields 非空属性值拼接恰等于 natural_key 判定——主键列置空、兜底列走属性值（回源重新派生同一退化键并复记 W004，身份语义保真而非把站名洗进编码列）。W005 致兜底键漂移的行（city 事后补）落主键路径——键仍精确还原，编码列内容非真实系统编码（已知精度限制，文档诚实声明，测试钉死该例）。
- **回源幂等（验收核心）**：导出 xlsx 直接走既有 analyze → suggest → import 管线导入全新库——建议映射全字段完整回填（实测置信 0.75×65 + 0.9×1——Label 走同义词档而非 exact 档，非 1.0，docs 两处如实钉）、八类型资源集合与关系集合（ref 重新派生）双库等价（CLI e2e 含 W005/W006 apply 后状态 + 退化键三形态；w12 冒烟 80 资源/60 关系 sqlite 级等价）；同库回导纯 skip 且零行级 issue。
- **三通道**：HTTP `GET /api/exports/resources?types=`（200 + xlsx 双头；types 逗号分隔、归一 Specs 序、去重、空段跳过，坏值 400 点名合法值清单）、CLI `netopslab export --db --out [--types]`（stdout 单 JSON `{file,types,totalRows}`；缺 --out/坏 types exit 2）、资源页「导出资源」按钮（filter bar 末尾常态可见，当前类型筛选进 types 参数、搜索关键词与状态筛选不参与——三处同口径）。空库导出成功但仅含中性占位空 sheet「资源导出」（重导入 422 `empty_workbook` 诚实拒绝）。
- **E003 部分导出诚实声明**：`?types=ip_pool` 部分导出的文件独立回源会因引用缺失 E003 失败（行照实 failed + 证据链），api.md 与 §19 两处声明「部分导出文件不适合独立回源」（W12-Q 实测钉死）。

### 新增 — ref 属性变更的关系重挂（W13-D，零迁移，治理三部曲终章）
- **三分支语义**：W005 确认流对 ref 类字段（机房 `site_id`、地址池 `assigned_device`、电路 a_end/z_end）的 apply——①非 ref 字段只改属性（关系零触碰）；②ref 字段且资源已有派生边 → 新值按被引用类型自然键解析成功后在**同一事务内**把边改挂到新目标（`relationRetargeted: true`；谱系 `first_batch_id`/`source_refs`/`attributes` 原样不动——relations 表无 updated_at 列，改挂的审计线 = 变更记录本身）；③ref 字段无边（空旧值 W001 孤儿路径）→ 只改属性**绝不新建无谱系关系**（`relationRetargeted: false`，建边仍需重导——每条关系行锚定其工作簿证据，人工 apply 没有工作簿行可作谱系）。目标解析守卫对**所有** ref 字段 apply 生效（含无边分支——否则 apply 可把导入路径必拒的悬空引用写进 attributes 且永不自愈）。
- **409 `ref_target_missing`**：新值解析不到 → 409 拒绝（message 含字段/目标键/被引用类型），提案保持 pending、资源与关系**零改动**（目标解析在任何写入之前）。可达触发 = 提案记录后目标资源被其它路径删除（回滚/直删）——E003 解析先于 diff 检测，「新值悬空的提案」不可能由导入通道产生；四层（服务/HTTP/CLI/冒烟）测试用自然旅程构造：独立批次导入目标 → 提案 → 回滚 → 409 → 重导目标 → 同一提案 apply 200。
- **同事务原子性**：属性写、NameField 同步、改挂、提案置 applied 全在一个 `DB.Do` 里——注入测试钉死（改挂撞 UNIQUE（改名合并残留形态）→ 整个 apply 回滚，属性/提案/双边零残留）；UNIQUE(from,to,type) 安全性论证：from 与 type 不动仅 to 移动，撞边需要同 (from,type) 已指向新目标的第二条边——导入派生世界每个单值 ref 字段至多一条边且此时 W005 旧值守卫在上游否决提案，唯一理论来源改名合并在改挂时原子失败而非脏边集。a_end/z_end 多 ref 字段独立改挂互不干扰（双向不干扰测试钉死）。
- **`relationRetargeted` 全链透传**：列表项上为实时谓词重算（引用字段且当前有派生边 → true——pending 项作确认预览，web 渲染「确认后将同步改挂关系」提示；applied 与 dismiss 后同为重算值，仅语义预览）；apply/dismiss 响应上为操作事实（apply 重挂成功 true / dismiss 恒 false）。HTTP/CLI/web 三通道同字段名。
- **`mapping.RelationTypeFor` 单一映射**：ref 字段 → 派生关系类型的映射从 importer 私有函数迁至 `internal/mapping`（纯数据包、仅依赖 domain+stdlib）——导入建边与确认改挂共用**同一张表**，绝不第二映射（语义逐字节零漂移，表测试随迁）。

### 新增 — 治理总览（W14-D，零迁移）
- **只读聚合端点** `GET /api/governance/summary`（无参数恒 200 + no-store）：待确认双队列（属性变更/疑似改名 pending 计数）、决策审计（applied/dismissed/rejected）、问题分布（按严重级三固定键 + 按规则 Top 10——`ORDER BY COUNT(*) DESC, rule_code ASC` 钉死在 repo SQL，TopRuleLimit=10 平局按码序裁决）、资源/关系/批次形态小表（byType/byRelationType 动态键）。**全部 6 条 SQL COUNT GROUP BY 下推，零全表拉取零 len()**（包注释声明该纪律）。
- **确定性**：除 generatedAt（time.Now.UTC()，契约即墙钟、测试显式豁免）外全字段是库状态的纯函数——同库两次调用逐字段相等 + 六数据块 JSON 字节相等；空库恒全零（固定键 0 / 动态 map {} / byRule []）。
- **CLI 等价物与组合根**：`netopslab query --db <path> governance`（单 JSON 输出）与 HTTP 端点逐字段相等（generatedAt 豁免，冒烟钉跨通道相等）；组合根第 8 组装配（`internal/services` Governance 字段，wiring_test 指针恒等 8 组在表）；新增 4 个最小 COUNT repo 方法（AttributeChange/RenameProposal/Batch 各 CountByStatus + Issue.CountByRule）。
- **web 概览页「治理总览」区**：待确认双卡（N>0 时 `stat-value--error` 高亮 + 「去确认」跳 /resources）、问题分布（severity 三行 + 规则 Top 5 中文规则名）、形态小表、批次状态行；独立取数点 + 面板内降级（ErrorBlock compact 不阻塞页面其余区，真浏览器拦截实证；重试可恢复）。

### 发布轮（W15-D）
- 版本四处事实源统一 **v0.6.0**（`domain.AppVersion` + CHANGELOG 头 + api.md 契约基线 + web i18n；`TestAppVersionP2` 断言随发布适配并在注释声明）；CHANGELOG 0.6.0 段、docs/verified-capabilities.md v0.6.0 段（CI 运行态按 v0.4.0/v0.5.0 先例推送后补记）、README 版本行/能力清单/英文摘要/MCP version 对齐；api.md 文内 v0.6 W12-D/W13-D/W14-D 能力标注统一为 v0.6.0 口径（收口 W12-Q/W13-Q/W14-Q 的「契约基线 v0.5.0 与 v0.6 标注并存」P3）；W13-Q P3-2 补电路双端 List 预览断言（既有测试函数内，计数不变）；全仓 `v0.5.0` 残留逐处判定（版本事实点已升，历史段/时点事实/里程碑引用保留）。
- 发布验收全量实测：六门禁全绿 + `make build` + live serve（`/api/health` 实报 v0.6.0、`GET /api/schema` 8 类 / 89 字段 / 13 规则、`/` 与 SPA 路由及全部 chunk/worker/favicon 200）+ `testdata/devices.xlsx` 完整导入基线（30 行 27/0/3 + issues 4E/1W/27I）+ `samples/ptp-config-script.txt` 脚本通道（4/0/0）+ 六个冒烟脚本（w6/w7/w8/w12/w13/w14）全 SMOKE-OK + demo.sh + demo-lab.sh 全过 + MCP 真进程 stdio 握手（serverInfo v0.6.0、恰 5 工具）+ 治理总览与导出端点 live 实测 + 全仓凭据扫描零命中。

### 测试
- Go **331 个顶层测试**全绿（v0.5.0 基线 296 + 本里程碑四轮新增：W11 +0 / W12 +13 / W13 +9 / W14 +13 = +35，`go test -list '.*'` 顶层函数实测口径——W12 分项 exporter 9 / api 2 / app 2，W13 分项 changes 6 / repo 1 / api 1 / app 1 / mapping +1 与 importer −1（`TestRelationTypeFor` 迁移净零），W14 分项 governance 4 / repo 4 / api 2 / app 3；W15 发布轮零新增测试函数（版本断言随发布适配 + W13-Q P3-2 断言补入既有函数）；键回填三规则 / 字节确定性 / 回源双库等价 / 同事务改挂 / 409 零改动 / 绝不建边 / COUNT 下推 / byRule 平局裁决等；既有断言零放宽，唯一例外为 `TestAppVersionP2` 版本断言随发布适配【注释声明】）。门禁六项全绿：gofmt 空 · vet 0 告警 · 20 含测试包全 ok · `-race` 0（changes/repo/api/governance 等重点包）· tsc 0 错误 · build ✓（最终实测计数与 CI 运行态——**已补记（三绿两受阻）**——W11-W13 三提交 CI 全绿（38151970576 / 38154941722 / 38158064643），W14/W15 两 run 因账号 Actions 分钟配额耗尽在 runner 启动层失败（0 steps 非代码原因，本地六门禁全绿佐证；恢复路径与证据链见 docs/verified-capabilities.md v0.6.0 段））。
- 端到端冒烟：W12/W13/W14 各轮独立冒烟脚本（w12/w13/w14）+ W11 重建的 w7/w8 + 既有 w6 + demo.sh / demo-lab.sh 全脚本可重复。

## [0.5.0] — 2026-10-11

**身份治理闭环与来源谱系版**（四轮迭代：W6-W8 三轮功能开发 + W9 重构收敛轮 + W10 发布轮收口；需求驱动为 R0-W5 发布时留下的下轮候选清单：名称改名的人工身份变更流、SourceRef 版本化、PTP 配置脚本导入通道、D2/D3 组合根收敛与四个语义尾巴）。核心：身份确认流补上最后一块拼图（改名合并人工流），来源证据从「首源」进化为「全史版本」，真实材料从三类工作簿扩展到第四类（配置脚本），服务装配收敛到唯一组合根。

### 新增 — 疑似改名检测与身份合并人工流（W6-D，migration v12，W006）
- 名称类键类型（ip_pool / base_station / port）的新键导入行，在「旧键已从源消失」且属性全等时**照常按新资源导入**并发 **W006** + pending 改名提案（`rename_proposals` 表，纯 CREATE TABLE）。检测五条件保守判定（类型开启 RenameDetection / 旧键消失 / 属性全等 / 新旧行双射 / 匹配集非空洞），**绝不自动合并**。
- **人工确认通道**（资源域写仅限人类）：HTTP `GET /api/renames` + `POST …/{id}/apply|reject` 与 CLI `netopslab renames list|apply|reject`。apply 单事务合并身份——关系改挂旧资源、**键分量属性同步**（新侧空值保留库内值）、删除新资源、旧资源换自然键；代理键 `res_x` 与 SourceRef 不动（审计痕迹保留）；apply 时新资源已消失 → 409 `target_missing`（驳回建议文案）。reject 零改动。回滚不对称：删本批 pending、留终态审计线。
- 增量导入（部分快照）会误报假提案——保守方向，人工 reject 即可，四处文档诚实声明。
- 端到端：基站改名副本 1/1/2 + W006 → 提案 pending（matchedFields 11 字段）→ apply（旧 res id 保留持新键、终态 2 资源）→ 再导 0/2/2 零 warning（键分量同步后无假 W005，幂等闭环）→ 重复 apply 409 conflict。

### 新增 — 来源引用版本化（W7-D，零迁移）
- **skip 即谱系版本**：同键再导入（纯 skip 与 W005 diff-skip）对每批每实体追加一条 `source_refs`（行语义/计数/issues 零变化；E001 批内重复不追加）——`UNIQUE(entity,entity_id,batch_id)` 每批一行的 v1 既有约束即版本化，**零迁移**（无 v13 涉入，source_refs DDL 逐字节未动）。
- 读侧版本序：`ListByEntity` 改 `ORDER BY rowid,row_number`（rowid 单调分配=时间版本序，删除不重编号、新版本恒殿后）；资源详情 `sourceRefs[]` 展示全史（创建批次在前，`raw` 记录该批工作簿原话）；回滚随批消失（`batch_id ON DELETE CASCADE`，DELETE 批次响应的 `deleted.sourceRefs` 诚实计数）；MCP `resource_search` 仍匹配首源（创建批次引用）。
- 三轮导入 → `sourceRefs=3` 版本序（创建 / diff-skip / 纯 skip 各留 raw）；回滚后版本行随批消失、幸存序稳定。

### 新增 — PTP 配置脚本导入通道（W8-D，migration v13，ptp_port 第 8 类资源）
- 第 4 类真实电信材料：中兴 VRP 风格 PTP 配置脚本（`.txt`/`.cfg`）经确定性解析器（`internal/ptpconfig`：节上下文栈、行号错误、`IsScriptText` 嗅探）产出「PTP配置」虚拟 sheet，**完整复用既有 analyze → 映射确认 → 导入执行管线**（全规则 / 幂等 / 回滚 / 来源版本化 / 改名检测天然适用，无旁路）；解析失败 422 `script_parse_error` 带行号，非 PTP 文本 422 `unsupported_script`（xlsx 永远走 excelize）。
- 新资源类型 `ptp_port`（复合自然键 `设备型号|接口名称`，23 字段，RenameDetection=true 与 W6 协同）；migration v13：resources CHECK 八类型重建（v9 ForeignKeysOff 模板复刻，存量行逐字节保持、relations 零触碰）。**模型级模板身份**诚实声明（脚本只携带型号不携带设备实例身份 → 无 ref 无关系）。
- 样例 `samples/ptp-config-script.txt`（真实材料逐字节提取，无凭据）；端到端 4/0/0 零 issue、再导 0/4/0 纯 skip、改名协同 apply 键迁移。

### 重构 — 组合根收敛与读路径（W9-D，零行为变化）
- 新包 `internal/services` 成为服务装配唯一事实源：`Services{db, Importer, Validation, Query, Changes, Renames, Lab, Report, LabReport}` + `New(db, opts…Option)`（`WithClock` 统一注入确认服务时钟）；app / HTTP API / MCP 三消费方共享同一实例（指针级测试 `TestAppSharesOneServiceComposition` 钉死 7 组恒等 + API/MCP 贯穿）——「App 死装配 + MCP 自建引擎」两处 R1-P2 病灶消除。
- MCP `resource_search` 收敛 `query.Service.SearchResourcesWithSource` + repo `ListByEntityIDs` 单查批量化：**N+1（1+N 查）→ 恒 2 查**（与 N 无关），repo 层 query-count 回归卫兵防倒退；MCP 契约（5 工具 / 输出形状 / 校验文案）零漂移，工具描述补 PTP端口（W8 漏的触点）；batches/issues 读路径裁量留原地（薄 handler 已锚定组合根库）。
- 四个语义尾巴收口：`intMax` 投影进 GET /api/schema（vlan 1..4094）；HTTP 409 `target_missing` 直删新资源补测（W6-Q P3-5 盲区闭）；apply 后展示名随键迁移（renames + changes 双修，键类名称字段经 `newRes.Name` 回落——W8-Q P3-5 闭）；`sheetHint` 断言钉真实值 PTP（同步修正 W8 文档漂移）。

### 发布轮（W10-D）
- 版本四处事实源统一 **v0.5.0**（`domain.AppVersion` + CHANGELOG 头 + api.md 契约基线 + web i18n；`TestAppVersionP2` 断言随发布适配并在注释声明）；CHANGELOG 0.5.0 段、docs/verified-capabilities.md v0.5.0 段（CI 运行态按 v0.4.0 先例推送后补记）、README 版本行 / 英文摘要 / 中文定位句对齐；全仓 `v0.4.0` 残留逐处判定（版本事实点改 12 处、7 文件：domain 常量+注释 2 / sim_test 断言 2 / i18n 1 / api.md 2 / README 3 / teleagent 2；其余历史段 / 时点事实 / 里程碑引用保留）。
- 发布验收全量实测：六门禁全绿 + `make build` + live serve（`/api/health` 实报 v0.5.0、`GET /api/schema` 8 类 / 89 字段 / 13 规则、`/` 与 SPA 路由及全部 chunk 200）+ `testdata/devices.xlsx` 完整导入基线（30 行 27/0/3 + issues 4E/1W/27I）+ `samples/ptp-config-script.txt` 脚本通道（4/0/0）+ 五个冒烟脚本（w3-w8）全 SMOKE-OK + demo.sh + demo-lab.sh 全过 + MCP 真进程 stdio 握手（serverInfo v0.5.0、恰 5 工具）+ 全仓凭据扫描零命中。

### 测试
- Go **296 个顶层测试**全绿（v0.4.0 基线 244 + 本里程碑四轮新增：W6 +17 / W7 +6 / W8 +21 / W9 +8 = +52，`go test -list '.*'` 顶层函数实测口径——W6 分项 importer 5 / repo 3 / renames 5 / api 2 / app 2，W7 分项 importer 4 / repo 1 / api 1，W8 分项 ptpconfig 9 / workbook 4 / mapping 4 / importer 3 / repo 1，W9 分项 **services 2 / repo 2 / query 1 / renames 1 / changes 1 / api 1**；检测五条件 / 单事务合并 / 谱系版本序 / 解析器 golden / v13 迁移 / 指针级装配 / query-count 卫兵等；既有断言零放宽，唯一例外为 `TestAppVersionP2` 版本断言随发布适配【注释声明】）。门禁六项全绿：gofmt 空 · vet 0 告警 · 18 含测试包全 ok · `-race` 0（importer/repo/api/services 等重点包）· tsc 0 错误 · build ✓（最终实测计数与 CI 运行态——**待推送后补记**——见 docs/verified-capabilities.md v0.5.0 段）。
- 真实材料端到端冒烟：W6-W8 各轮独立冒烟脚本（w6/w7/w8）+ 既有 w3/w4 + demo.sh / demo-lab.sh 全脚本可重复。

## [0.4.0] — 2026-10-10

**真实电信资源类型与身份治理版**（五轮迭代：W1-W4 四轮功能开发 + W5 发布轮收口；需求驱动为 v0.3.2 后提供的真实电信样例工作簿与 R4 身份模型审查裁决）。核心：从「三类合成台账」走到「七类资源、真实表格直导、身份键受治理、属性变更有审计」。

### 新增 — port 稳定自然键（W1-D，migration v8，闭环 R4 审查 P1）
- **根因**：v0.3.1 的 port 复合键 = `device_ip|port_name`——IP 是可变属性，设备重划 IP 即端口资源分裂。v8 换稳定键 **`device_name|port_name`**（spec 字段序拼接）；`device_ip` 降级为必填值列（进 attrs，不再入键）。
- migration v8：纯 SQL 快照三守卫（冻结快照 + 唯一认领 + 目标未被占用）就地 UPDATE；冲突行**保守保留原键**；存量 fallback 行（不可能携带 device_name 属性）逐字节保留；幂等由构造保证。`NameFieldOverride` 钉定 port 展示名 = port_name（防止 device_name 劫持「first *_name」启发式）。
- 真实样例回归：`samples/telecom-port-multi-header.xlsx` 2 imported / 1 skipped / 2 failed 不变，键 = `B2|bvi2.1008` 等；前端 `schema.ts` 镜像同步 + 向导正向提示。

### 新增 — base_station 资源类型与条件自然键（W2-D，migration v9，W004）
- 真实无线基站工作簿 15 字段直导（真实 14 列 + 预留 city）；**条件自然键机制**（spec 声明式 `KeyFallbackFields`，零类型硬编码）：主键 base_station_code 非空 → 编码即键；为空 → 键退化为 `(地市, 无线基站名称)` 兜底拼接（跳过空值，真实表无地市列 → 纯名称），行照常导入并记 **W004 身份键退化**（仅 imported 行、幂等重导不重记）；E001 判重在解析后的最终键上。
- migration v9：resources CHECK 封闭集扩 `'base_station'`（SQLite CHECK 不可 ALTER → v7 同构 ForeignKeysOff 12 步安全重建）；基站IP 为 string 值列（真实值带 /30 掩码，IP 属可变属性不作身份）；MCP 文案/前端类型枚举/双 chip/文档六处封闭集触点全同步。样例 `samples/telecom-base-station.xlsx`（4 imported / 4 failed / 3×W004）。

### 新增 — machine_room / site 资源类型与 belongs_to 跨表关联（W3-D，migration v10）
- 真实机房局站表（8 真实业务列）**同表双读**：先按局站类型导入（键 = 局站ID，12 位数字编码），再按机房类型导入同一 sheet（键 = 机房ID，17 位数字编码；机房 spec 的局站ID 列映射为可选 ref）。两类 ID 均 KindString——**前导零即身份，绝不数值化**；机房名称/局站名称/设备名称为昵称属性，IP 地址为可变属性。
- 局站先入库即自动派生 `machine_room --belongs_to--> site` 关系（E003 语义钉死导入顺序）；同表双命中建议 = 局站（注册序容器优先），映射确认页可切换。migration v10：resources + relations 双表 CHECK 扩容重建（ForeignKeysOff 安全形 + foreign_key_check 门禁）。样例 `samples/telecom-site-machine-room.xlsx`（site 2/0/4 → machine_room 4/0/2，belongs_to×3，幂等重导 0/4/2）。

### 新增 — 属性变更历史与人工确认（W4-D，migration v11，W005）
- 同键再导不再静默丢弃属性差异：diff 逐字段记为**待确认变更**（`resource_attribute_changes` 表 + **W005** issue，行仍 skip 幂等）；口径与 attributes 归集同源（非键非枚举非空、新值空不记、pending 同值去重），与 row_result/counts 同事务。
- **人工确认通道**（资源域写仅限人类）：HTTP `GET /api/attribute-changes` + `POST …/{id}/apply|dismiss` 与 CLI `netopslab changes list|apply|dismiss`；apply 携带旧值校验（漂移即 409 `attribute_conflict`）+ 同事务写入 + `updated_at` 刷新；**MCP 工具面 5 个冻结不变**（AI 不得 apply 属性变更）。
- 回滚不对称语义：回滚删本批 pending、**保留 applied/dismissed 审计线**（被审计资源若被回滚删除则记录随之级联消失，四处注释/文档一致声明）；前端 Resources 详情新增待确认变更面板。端到端：改基站IP 再导 → 0/4/4 + W005×1 → CLI apply 生效 → 同值三导零 warning（幂等闭环）。

### 新增 — 发布轮工程化（W5-D）
- **GET /api/schema（动态 schema 发现端点）**：`{"types":[…],"rules":[…]}`——types 为 mapping.Specs 的**忠实投影**（type/label/sheetHint/nameField(+override)/naturalKeyFields/keyFallbackFields/fields[name,label,required,naturalKey,kind,enum?,intMin?,refType?,synonyms[]]，由 `BuildSchemaDocument()` 机械生成，投影测试逐字段对照 Specs，禁止手抄漂移）；rules 为 domain 规则目录（12 码 + SeverityForCode + 一行英文释义）。**仅 HTTP 端点，不进 MCP**（工具面 5 冻结）；前端 `schema.ts` 镜像保留（注释注明可经 /api/schema 校验，稳定性优先不强制切换）。
- **uploads 垃圾回收**：删除 `dataDir/uploads/` 下未被任何 import_batches.workbook_path 引用的孤儿文件（**任何状态的批次都算引用**——含 failed/rolled_back：工作簿是 issue 证据的回读源）。落点 = `serve` 启动时（监听启动前串行执行，无并发上传窗口；RecoverInterrupted 同款 cmd 层先例，**MCP 进程零触发**）+ CLI `netopslab gc --db --data` 手动子命令（stdout 单 JSON 计数契约，n>0 时 slog Info 走 stderr）；删除失败记日志不致命；目录不存在容错为 no-op。
- **前端构建分包**（vite manualChunks 按依赖家族）：主 chunk **795.03 kB → 187.19 kB**（gzip 249.86 → 54.51 kB）；react 家族 166.24 kB / cytoscape 438.71 kB 独立 vendor chunk；maplibre-gl 维持 ADR-0009 懒加载异步 chunk（1,085.01 kB，本体不可再分的第三方单库——vite 唯一 >500 kB 警告即此，如实保留）；maplibre CSS 83.15 kB 随异步 chunk 懒加载（首屏 CSS 123.61 → 40.46 kB）。

### 测试
- Go **244 个顶层测试**全绿（v0.3.2 基线 207 + 本里程碑五轮新增：W1 +2 / W2 +7 / W3 +9 / W4 +12 / W5 +7 = +37，`go test -list '.*'` 顶层函数实测口径——迁移守卫、条件键、W004/W005 语义、belongs_to 语义链、schema 投影、GC 四态与 CLI 契约等；既有断言零放宽，仅 max-version 断言随迁移前进）。门禁六项全绿：gofmt 空 · vet 0 告警 · 15 含测试包全 ok · `-race` 0（importer/repo/api 等重点包）· tsc 0 错误 · build ✓（最终实测计数与 CI 运行态——**待推送后补记**——见 docs/verified-capabilities.md v0.4.0 段）。
- 真实样例端到端冒烟（W1-W4 各轮均验）：port / 基站 / 机房局站样例的导入-幂等-回滚旅程、属性变更 apply/dismiss 旅程、demo.sh 与 demo-lab.sh 全脚本可重复。

## [0.3.2] — 2026-10-10

**工程审查整改版**（R1 架构 A- / R2 代码质量 / R3 计划偏移 / R4 主键身份模型——四维只读审查的整改落地；用户需求驱动：「继续做修复然后做分析告诉我下一步的开发」）。

### 修复 — 版本事实源（R1-D1 / R3-C1，全项目唯一 P1）
- **根因**：AppVersion 三处手抄（domain / web i18n / CHANGELOG），v0.3.1 已真实漂移——domain 停在 v0.3.0，`/api/health` 与 MCP 握手错报，README 承诺未兑现。
- **修复**：三处统一升版 v0.3.2；新增 `TestAppVersionMatchesChangelogHead`——domain 常量必须等于 CHANGELOG 头版本，再漂移即测试红；侧栏版本号改读 `/api/health`（前端不再手抄）。

### 修复 — 后端健壮性（R2-P2×2 + R1-D6 + R2-P3×3）
- `importer.Rollback` 的 in-use 检查与状态前置校验**移入 `db.Do` 事务内**（BEGIN IMMEDIATE 先取写锁，与其他写事务严格串行，TOCTOU 窗口归零）。
- workbook 层新增**解压上限**（单 sheet 物化行数 / 全簿单元格总量封顶，超限返回新增 422 错误码 `workbook_too_large`）——zip 炸弹暴露面收敛；解析改为逐 sheet 流式。
- serve 启动**自愈**：进程崩溃遗留的 `analyzing`/`importing` 瞬态批次复位为 `failed(interrupted)`（R1-D6 崩溃窗口关闭）。调用点在 cmd 层——`serve` 启动与 CLI `import` 入口（确定本进程无在途导入的入口，非 api.New）；MCP 进程不做以免误伤并发导入。
- `handleBatchDelete` 删除响应计数改 **COUNT 聚合**（不再全量加载行结果与 issue 仅为取 len）。
- MCP `resource_search` 的 type 描述与错误文案**补 port**（文案与封闭集对齐）。
- dataDir 为空时上传请求直接 500（消除半初始化相对路径拼接）。

### 修复 — 前端（R2-P2-3 / R1-D4 / R4 短期）
- **Overview 类型统计动态化**：此前硬编码 `device|ip_pool|circuit` 三类型，v0.3.1 新增的 port 在总览统计完全缺席——改为按后端 stats.byType 动态渲染全部类型。
- **Report 页批次切换加 useSeq 陈旧响应守卫**（快速切换下拉时慢响应后到覆盖新批次预览的串台修复；顺带为 Overview 挂载/重试加载加同款守卫——全站异步加载点全部收口）。
- **导入映射确认页主键稳定性告警**（R4 短期落地）：IP 类列被选入自然键时告警「IP 属可变属性，重划后同键资源将分裂」；名称类自然键提示 (地市, 名称) 作用域。

### 文档 — 诚实对齐（R3-P0×2 + P1×2 + P3）
- README 合规横幅**加 samples/ 真实样例例外条款**；新增 `samples/README.md`（来源/授权/范围说明——红线 8「no real network data」的已文档化偏差）。
- 新增 `docs/natural-keys.md`：**各资源类型自然键规范**（R4 裁决落地——UUID 代理键 PK 不变；编号类自然键优先；名称键须 (地市,名称) 作用域；IP 一律降级可变属性；port 键含 IP 定性为 P1 隐患并排入 v0.4 迁移计划）。
- `docs/api.md` 补 v0.3.x 契约（拓扑节点 geoLat/geoLng、`type=port`、port 复合键导入示例、422 `workbook_too_large`，版本戳 v0.3.2）；`docs/import-and-validation.md` 补 port / 重复表头 / 解压上限 / 崩溃自愈行为。
- `docs/verified-capabilities.md` 新增 v0.3.2 段（测试计数口径注明）；ADR-0002 / ADR-0009 补遗（DSN `_txlock=immediate`；maplibre-gl 懒加载独立 chunk）；`architecture.md` §4 undo 标记原子化更正。
- backup-local-impl 本地分支处置决策已执行：本地 tag `archive/backup-local-impl` 归档（价值已迁移主线的旧基线实验；该分支与 tag 均永不推送远程），决策记入 worklog。

### 测试
- Go **207 个顶层测试**全绿（+8 修复回归：rollback 事务化 / 解压上限 / 崩溃自愈 / 动态统计等修复项各自的新增测试，+1 版本一致性 `TestAppVersionMatchesChangelogHead`；基线为 v0.3.1 实测 198——彼时宣称 200 属口径未对齐，本版已校正为 `go test -list '.*'` 顶层函数实测计数）。门禁六项全绿：gofmt 空 · vet 0 告警 · 14 包全 ok · `-race` 0（importer/workbook/api/repo）· tsc 0 错误 · build ✓（最终实测计数与 CI 运行态——待推送后记录——见 verified-capabilities.md v0.3.2 段）。

## [0.3.1] — 2026-10-10

**真实表格导入 + 3D 比例修复版**（用户需求驱动：「这个是可导入的多个表格示例，还有这个 3D 地图太过于大了，应该根据所有的点调整默认展示比例」）。

### 修复 — 3D 地图默认视野（用户点名的核心 bug）
- **根因**（三层，逐层排查收敛）：① 首批数据 fitBounds 与视图切换 easeTo 并发互相打断——相机停在 DEFAULT_CENTER 而非数据区；② MapLibre 的 pitch 感知 fitBounds「恰好容纳」解在大倾角下过于保守（黑龙江数据实测 zoom 2.84 ≈ 全球视野，塔群挤成一撮占屏宽 ~3%）；③ `fitGeoBounds` 读瞬时 pitch 在「挂载即 3D」时误判为 2D。
- **修复**：`fitGeoBounds(map, data, view)` 按**目标视图**适配；3D 分支弃用黑盒解，改用**墨卡托几何直解** `zoomForBBox3D`（bbox 弧度宽/高 × 容器像素 × 斜视压缩系数 → 塔群占屏宽 ~55%、等效高 ~30%，clamp [3, 7.2]），center 取质心 + 18% 纬度北移（pitch 58 的地面投影中心偏下补偿）；挂载初始 pitch/bearing 按目标视图设置（消除切换动画与首批 fit 的竞态）；2D↔3D 切换保留 zoom ±1.3 补偿（等效覆盖守恒）。
- **验证**：相机实测 (zoom 7.2, center 124.59/50.65, pitch 58) 与手算公式解**逐项吻合**；VLM 终评四项全过（塔群居中长条分布、塔/标签/链路可辨、构图专业无渲染错误）；F 键「适配视图」同路径生效。

### 新增 — port 资源类型（真实电信端口工作簿导入）
- 真实样例入库 `samples/telecom-port-multi-header.xlsx`（一个 sheet 内两种表头形态 + 逐字重复表头行的多段导出拼接）；导入端到端：2 imported（含带宽 0）/ 1 skipped（重复表头）/ 2 failed（表头 B 行走正常 E004 证据链）。
- migration **v7**（`port_resource_type`，首个 `ForeignKeysOff` 迁移）：resources 的 CHECK 重建扩展 `'port'`（专用连接事务外关 FK → 事务内全列复制重建 → `foreign_key_check` 空结果门禁 → 恢复 FK）；virtual_devices 不动（v6 已含 geo 列）。
- `portSpec` 16 字段：**复合自然键** `device_ip + port_name`（`|` 连接落库，如 `12.1.53.24|bvi2.1008`；单键类型 join 恒等原值，零行为扰动）；`bandwidth_mbps` 无 IntMin（真实表带宽可为 0）；`device_ip` 保持 ipv4 值列而非 device ref（端口工作簿自含导出，设备行可未入库，避免 E003 拒整表）；含 geo_lat/geo_lng/geo 同设备可选坐标列。
- 导入器**复合自然键**改造（E002/E001/幂等重导/`lookupExisting` 全走拼接键）；**重复表头行**检测跳过（≥3 非空格且逐格等于列头 → skipped 专属消息、零 issue）。
- 前端：`ResourceType + 'port'`；`TYPE_SPECS` port 镜像 + `naturalKeyFields()` 数组化（keyColumns 双键全量）；device 补 geo 三列前端镜像（v0.3.0 的 UI 缺口——此前界面上无法映射「装机经纬度」列）；i18n 全量标签 + `chip--port` 徽章。

### 测试
- Go 194 → **200**（+6：port 建议/复合键校验 ×4、port 导入端到端（含表头跳过/复合 E001/E002/幂等/单键回归）、迁移 v7 升级含 port 落库断言）；既有断言零删除零放宽，仅 `TestMigrationV6UpgradesV5Database` 的版本期望 6 → 7（v7 自动级联，契约演进）。
- `gofmt -l .` 空 · `go vet` 干净 · 14 包全 ok · `tsc -b` 0 错误 · `npm run build` ✓。

## [0.3.0] — 2026-10-10

**地理视图版**（用户需求驱动：「能把现实地理 2D 地图和 3D 地图也纳入设计吗？可以视角切换」）：设备地理坐标全链路管道（导入列 → 资源属性 → 虚拟设备 → 拓扑 API）+ 画布三视图切换（**拓扑图 | 2D 地图 | 3D 地图**，MapLibre GL 懒加载）——真实深色地理底图上按经纬度落设备、链路状态分色、3D 倾斜视角塔体、视角切换与表格双向联动。**红线：geo 绝不进入规范状态哈希**——坐标是展示元数据，含/不含坐标的同业务状态哈希逐字节一致（回归测试双层证明）。194 个 Go 测试全绿；tsc 0 错误；浏览器控制台零警告零错误。

### 新增 — 映射与导入（internal/mapping · internal/importer）
- 设备 schema 三个**可选**字段（仅设备）：`geo_lat`（纬度，float [-90,90]）、`geo_lng`（经度，float [-180,180]）、`geo`（经纬度组合列，两个数字、顺序不限）；同义词梯子覆盖 经度/纬度/lat/lng/lon/GPS纬度/GPS经度/北纬/东经/经纬度/装机经纬度/坐标/GPS坐标/A端装机经纬度/位置
- 三个新 FieldKind（`lat`/`lng`/`geo`）的 E004 校验，中文原因：`不是合法的纬度（-90 到 90 的数字）`、`不是合法的经度（-180 到 180 的数字）`、`经纬度应为两个数字（如 126.63,45.75）`；浮点范围检查在 Kind 分支内实现（整数区间机制不受影响）
- **组合列拆分（中国边界启发式）**：`f1∈[73,136] ∧ f2∈[3,54]` → (经度,纬度) 高德惯例；`f1∈[3,54] ∧ f2∈[73,136]` → (纬度,经度)；境外/歧义坐标不硬失败（取合法解读，默认 `lng=f1, lat=f2`）；分隔符支持半角/全角逗号、分号、空白；拆分值先落属性再走与分列相同的校验；**分列优先**（纬度+经度两列同时提供时组合列不拆分，原始值保留溯源）
- 拆分结果以十进制原码串（`FormatFloat 'f' -1`）写入资源属性 `geo_lat`/`geo_lng`，同时保留原始 `geo` 属性

### 新增 — 存储与仿真（internal/domain · internal/repo · internal/sim）
- `domain.VirtualDevice.GeoLat/GeoLng *float64`（nil = 无位置）；迁移 **v6**（`device_geo_columns`）：`ALTER TABLE virtual_devices ADD COLUMN geo_lat REAL / geo_lng REAL`，可空无默认，v5 库打开自动升级、旧行 NULL；仓储层 `sql.NullFloat64 ↔ *float64`、Insert 对 nil 写 NULL
- 物化解析资源属性 geo_lat/geo_lng（合法且在界内才采纳，非法忽略属防御）；拓扑节点 `GeoLat/GeoLng`（JSON `geoLat`/`geoLng`，`omitempty`——无位置节点不带键）
- **撤销路径保位置**：`delete_device` 的逆操作快照 `DeviceSnap` 携带 `geoLat`/`geoLng`（camelCase；旧 inverse_json 缺键解码为 nil，兼容）
- **哈希红线**：`hashDevice` 不含 geo（`internal/sim/state.go` 未改动）；`TestWorkspaceHashIgnoresGeo` 双层证明——两个全新库分别导入含/不含 经纬度 列的同数据工作簿，物化哈希逐字节相同；单元级直接清空已加载状态的 GeoLat/GeoLng，hashValue 不变

### 新增 — 测试数据与演示
- `cmd/testdatagen -geo`：新增 `testdata/geodevices.xlsx`（设备 sheet + 经度/纬度 分列，10 行）与 `testdata/geocircuits.xlsx`（电路 sheet，10 行）——黑龙江真实网络孪生（设备编码/名称/型号/IP/电路编号取自运营商真实导出，**坐标为演示近似值**）；哈尔滨→漠河北南干线 + 地震局 10M 长专线 + 基站支路；确定性生成（固定文档属性，重跑 sha256 一致）；**既有 4 个合成工件零改动**（sha256 前后一致）
- `scripts/demo-geo-lab.sh`：导入两本地理工作簿 → 物化「黑龙江-地理孪生演示」（描述注明坐标为演示近似值）→ GD-202366 两端配置 10.61.0.1/30 ↔ 10.61.0.2/30 → 关断 CKT-QQH-JGDC-10G（干线中段红色故障）→ 起 serve 随机端口 curl 拓扑 API 展示 geoLat/geoLng
- CLI `lab workspace create` 新增可选 `--desc`（透传 workspaces.description，此前仅 HTTP 可设）

### 已知边界（诚实清单）
- 检查点恢复重建被删设备走 `create_device`（封闭操作面无 geo 字段）→ 重建设备无坐标（状态哈希不受影响；撤销路径不受影响）
- 3D 塔为示意级数字孪生（塔高 = 设备连接数），非物理仿真渲染（无材质光影/流量驱动高度）；VLM 终评 2D 7.5 / 3D 6.5 / 综合 7.0
- 地理底图依赖公网瓦片服务（ESRI anonymous），离线时自动降级为无栅格深色网格模式（设备/链路本地 GeoJSON 恒可渲染）

### 新增 — 前端地理画布（web/src/components/lab/GeoCanvas.tsx，GEO-2）
- **三视图切换**：画布工具带新增分段控件「拓扑图 / 2D 地图（俯视 · 设备按真实经纬度落位）/ 3D 地图（斜视 58° · 塔高示意设备连接数 · 右键拖拽旋转）」，按工作台持久化（画布 v2 存档新增 view 字段，兼容旧档）；全部设备无坐标时显示引导空态 +「返回拓扑图」按钮；部分缺坐标时右下角 chip 提示「N 台设备未显示（无坐标）」
- **maplibre-gl 6.13.0（精确锁定）懒加载**：`await import('maplibre-gl')` 独立异步 chunk（主包不增重；worker 经 vite `?url` 哈希资源发射）
- **设备 HTML Marker**：状态色圆点 + 编码牌（`HLB-A4`），键盘可聚焦（tabindex + Enter/Space 触发选择），点击/选中与拓扑图 tap **同一套选中语义**（右侧表格同步定位、选中态强调环）；悬停复用画布左上信息条；L 键标签开关（隐藏编码文字保留圆点）
- **链路 GeoJSON 图层**：up #5b8fd9 2px 实线 / down #ff5252 3px 红虚线（rAF marching-ants 动画，prefers-reduced-motion 关闭）/ 选中 #2f9dff 4px；端点缺坐标的链路跳过并计数
- **3D 塔（fill-extrusion）**：仅 3D 视图可见，像素高 18 + 连接数 × 10；2D↔3D easeTo pitch 0|58（3D 同时 bearing -30 斜视角，700ms，reduced-motion 时 0）；拖拽旋转/俯仰为 MapLibre 原生右键/Ctrl 拖拽
- **地理控制**：NavigationControl（visualizePitch 指北针 + 缩放）+ ScaleControl + 适配视图（fitBounds，F 键）/ 指北复位；首批数据自动 fitBounds（maxZoom 9）
- **zen 全屏兼容**：ResizeObserver → map.resize()，1920×1080 下地图 1888×999 满幅、pitch/塔体保持；Esc 链与拓扑视图一致；图例（地理变体）含塔高说明

### 修复 — 底图与 3D 塔两处 P0/P1（编排者终验发现并动手）
- **P0 底图水印砖（取证与替换）**：CARTO `basemaps.cartocdn.com/dark_all` 对匿名 IP 统一返回 2513B 的「API KEY REQUIRED」水印砖（浏览器内 fetch z3/z4/z10 字节级相同的错误砖；截图左区亮像素 18.3% = 文字墙；HTTP 200 故瓦片 error 降级不触发）→ 替换为 **ESRI Canvas/World_Dark_Gray_Base + Reference 双层栅格底图**（免密钥、深色画布风格匹配、Reference 层提供地名/边界注记，注意 z/y/x 路径顺序）；修复后 VLM 确认真实地理内容（满洲里/海拉尔/根河等地名可见）
- **P1 3D 塔不渲染（两处叠加根因）**：① 塔高表达式对 `['zoom']` 直接做除法运算违反 MapLibre 规范（paint 属性里 zoom 只能作顶层 interpolate 输入）→ 样式校验失败、`getStyle()` 返回 undefined；修复：顶层 `interpolate exponential-2` 双停止点（z0→9e4 × towerPx、z16→9e4/2^16 × towerPx，几何插值恒等于 9e4×2^zoom）② 塔 footprint 固定 0.0016°（≈120 米）在省级缩放下仅 0.04px，亚像素多边形光栅化不出面；修复：footprint 改为恒定 7px 像素宽（`buildTowerCollection(topology, zoom)` 纯函数按当前 zoom 逐设备换算，zoomend 防抖 150ms 重建，`applyDataToMap` 不再触碰塔源防清空）；修复后 `queryRenderedFeatures(towers)`=10/10、VLM 确认「明显 3D 立体柱状物、高度不一、数字孪生感较强」
- **离线降级匹配**：瓦片错误源判定覆盖 arcgisonline 主机名；降级样式重建时塔数据与可见性一并回填

### 验证 — GEO-2 前端（agent-browser 1920×1080 真浏览器）
- 2D：ESRI 底图真实渲染（纹理 425 色 + VLM 确认地理内容与地名）；pitch=0；标记点击 → 表格行选中（HLB-MH-BS1 已停机同步）；红虚线故障链路可见
- 3D：pitch=58.0 / bearing=-30 / zoom=3.2；塔 10/10 渲染；zen 3D 全屏 1888×999 满幅
- 快捷键：F fitBounds（黑龙江 bbox）/ L 标签往返（10 codes ↔ 10 nolabel ↔ 0 恢复）
- 空态与持久化：无坐标工作台引导 + 返回拓扑图可用；重载后持久化视图自动恢复
- 稳定性：15 次快速视图切换后控制台 0 error 0 warning；拓扑视图回归（布局/路径模式横幅/节点渲染/minimap）抽查通过；截图归档 `download/ui-screenshots-v0.3.0-geo2/`（22 张）

### 验证 — GEO-1 后端
- 门禁：gofmt 空 / `go vet` 干净 / 全部测试通过（**+13 个新测试**，覆盖：三个 geo Kind 的 E004、拆分启发式（双序/中文逗号/空格/分号/境外/非法）、导入器端到端（组合列/分列/分列优先/非法拒绝）、物化→拓扑 geo 流转、哈希红线回归、delete→undo 位置保留、v6 迁移升级（v5 库活数据 + 幂等重开）、geo fixtures 不变量）
- 冒烟（真实二进制 + curl）：导入 geodevices/geocircuits → 物化 10 设备/10 链路 → 拓扑 API 10/10 节点携带 geoLat/geoLng（哈尔滨枢纽 45.773/126.636、漠河站 down）→ 故障注入后 CKT-QQH-JGDC-10G down；ops/undo/checkpoint 往返不受影响（留档 `/home/z/my-project/tmp/geo1/`）

## [0.2.3] — 2026-10-10

**画布全屏工作台版**（用户反馈驱动：「画布的组件太简陋了，画布的展示区域太小了，不能全屏工作」）：画布从「被四层壳挤压的 948×760 面板」升级为**可全屏的一等工作区**——zen 全屏模式（1080p 下 1920×1080，约 2.9 倍面积）+ 工业级画布交互（右键菜单 / PNG 导出 / 缩放指示 / 手动排布持久化 / 快捷键）+ 分栏拖拽与右栏折叠。全部改动仅在 web 前端 5 个文件（后端零改动，红线保持）。181 个 Go 测试全绿；tsc 0 错误；控制台零警告零错误。

### 新增 — zen 全屏工作模式（TopologyCanvas + LabWorkspace + global.css）
- **一键全屏**：工具带「全屏」按钮 → 画布卡片 `position:fixed inset:0 z-60` 接管整个视口（盖住侧栏/顶栏/页头/右面板）；优先 `requestFullscreen()` 原生全屏（目标 `document.documentElement`，Toast z-100 仍浮于其上），无权限/不支持时静默降级为应用内 CSS 全屏；`fullscreenchange` 监听同步（浏览器 Esc 退原生全屏时 CSS 全屏一并退，并顺带收起右键菜单——避免「盖着但非全屏」与菜单残留）
- **Esc 逐级链路**（不一键全清）：右键菜单 → 路径追踪/邻域聚焦痕迹 → 退出全屏；对话框（焦点陷阱）打开时让路
- **左上浮动选中详情卡**（全屏时右侧表格不可见）：设备显示编码/名称/状态/接口 up-down/IPv4 列表（超 5 个折叠「等 N 个」）、链路显示两端与端口（`DEV-010 · eth1 ↔ DEV-011 · eth0`）、接口显示所属与地址；无选中不显示
- **全屏工具带深色化**（与画布底色一致）+ 退出按钮；zen 状态 sessionStorage 持久化（刷新保持）
- 容器尺寸变化 `cy.resize()` 补偿（rAF 一次补齐，视口 zoom/pan 保持）

### 新增 — 画布工业级交互（FX-6）
- **右键上下文菜单**（无 cxtmenu 插件，`cy.on('cxttap')` + 自渲染菜单）：设备四项（邻域聚焦 / 设为路径追踪起点 / 在表格中定位 / 复制设备 ID）、链路两项（表格定位 / 复制链路 ID）、空白两项（适配视图 / 重置视图）；a11y `role=menu/menuitem` + 打开后焦点入首项 + Esc 还原打开前焦点 + ↑↓/Home/End 导航；落位鼠标右下、越界向对侧翻转；菜单外 pointerdown 捕获阶段关闭
- **PNG 快照导出**：`cy.png({full:true, scale:2, bg:'#0e1626'})` 保证不透明深色底，文件名 `netops-topology-<workspaceId>.png`
- **缩放百分比实时指示**：`cy.on('zoom')` rAF 节流，缩放控件旁深色 chip（如「62%」）
- **手动排布位置持久化**：拖动结束（160ms 合并窗口）全量 {id,x,y} 存 localStorage 按工作台隔离；重载恢复位置（等价锁定语义）；切布局/重置视图自动清除并重排；隐私模式 try/catch 降级
- **键盘快捷键**：F 适配视图 / L 链路标签开关 / Esc 链级；输入框聚焦、对话框打开、修饰键（Ctrl/Cmd/Alt）、长按重复均不响应

### 新增 — 画布工作区布局（FX-5）
- **页头紧凑化**：页头卡片 180px → 97px（不删信息不删按钮，仅压缩 padding/间距），1080p 画布渲染高度 717 → **760px**（`calc(100vh - 237px)`）
- **分栏拖拽**：6px 把手（`role=separator` + `aria-valuenow` + 左右方向键 ±2% + pointer capture 拖动 30–85%），拖动中 `col-resize` 光标 + `user-select:none` + Cytoscape resize 挂起（拖完一次补齐），比例 localStorage 按工作台持久化
- **右栏折叠**：一键单列（画布占满工作区宽 1618px@1920），localStorage 持久化

### 修复 — 体验缺陷（编排者终验发现）
- **路径模式引导横幅从未出现（时序缺陷）**：切换到路径模式后横幅不显示「点击起点设备」——挂载时序上 mode effect 先设 pick-a、topology 异步到达后数据同步 effect 又 `clearModeArtifacts()` 覆盖，而 mode 值不变时 effect 不会重跑。修复：pick-a 态**不入状态机**，改为渲染层从 `mode==='trace' && trace===null` 直接派生（数据刷新/清除路径后自然回到引导态）；引导态 × 按钮语义改为「退出路径模式」（否则清完又立刻派生回来，横幅关不掉）
- **原生全屏退出菜单残留**：浏览器 Esc 退原生全屏时 keydown 被浏览器消费，页面链路收不到 → 右键菜单残留悬浮。修复：`fullscreenchange` 同步退 zen 时顺带 `closeMenu`

### 验证
- 门禁：gofmt 空 / vet 干净 / 181 测试 PASS（版本断言动态化后无硬编码）/ tsc 0 错误 / vite build / `make build` 29.8 MB 嵌入
- 真浏览器（agent-browser 1920×1080）：
  - zen：非全屏 948×760 → 全屏 **1920×1080（2.88 倍面积）**；原生全屏 + reload 后 CSS zen 降级保留；Toast z-100 浮于全屏之上
  - Esc 链级：菜单开 Esc 只关菜单 / 路径完成 Esc 只清路径回引导态 / 引导态 Esc 直通退全屏；原生全屏 Esc（浏览器消费）→ 同步退 zen + 收菜单
  - 分栏：拖 60→70（画布 948→1144，reload 持久化）/ 键盘 72→76（aria-valuenow）/ 折叠 1618px（reload 保持）/ 展开恢复
  - 画布交互：右键空白菜单（适配/重置）+ 越界翻转；缩放 chip 86%→55% 实时；拖动节点 reload 位置保持；PNG 导出落盘 1924×1164 全不透明
  - 引导横幅：reload（mode 持久化恢复）→ 显示「路径模式：点击起点设备」；点起点 →「已选起点 DEV-002」；点终点 →「DEV-002 → DEV-007 · 5 跳 · 经 DEV-003、DEV-004、DEV-005、DEV-006」；Esc 清除回引导
  - zen 详情卡：链路版「CKT-010 运行中 两端 DEV-010·eth1 ↔ DEV-011·eth0」
  - 控制台全程零 error 零 warning
- 截图留档：`download/ui-screenshots-v0.2.3-fx6/`（16 张）+ `download/ui-screenshots-v0.2.3-fx5/`（25 张）+ `download/ui-screenshots-v0.2.3-final/`（编排者终验 8 张）

## [0.2.2] — 2026-10-10

**画布多模式 + 去 AI 味打磨版**（用户反馈驱动）：拓扑画布从「3 布局 + 单一交互」升级为 **6 布局 × 3 交互模式 + 链路标签开关 + 迷你导航图**；删除侧栏渐变 logo 方块（「AI 味」首要来源）、卡片扁平化。**修复 v0.2.1 遗留回归：布局切换按钮实际跑的是旧布局**（setState 闭包陷阱，VLM 复核截图证实「网格」按钮截图实为力导排布）。181 个 Go 测试全绿；tsc 0 错误。

### 修复 — 画布（TopologyCanvas）
- **布局切换闭包陷阱（P1）**：`runLayout(kind)` 先 `setLayout(kind)` 再调用 `useCallback([layout])` 闭包里的 `startLayout`——实际执行的是**旧布局**（点击「网格」时闭包里仍是 force）。v0.2.1 的「布局切换」截图复核（VLM）证实从未生效。修复：`startLayout(cy, kind, eles?)` 显式传布局类型，不再依赖状态闭包
- Cytoscape 3.32 无 `:hover` 样式伪类（控制台警告「selector invalid」）：hover 微交互改事件驱动 `.hovered` 类（节点描边提亮 2.5px / 边线宽 3px，120ms 过渡）

### 新增 — 画布多模式（对标 Kiali/Grafana 拓扑视图）
- **6 布局**：力导 cose / **层级 breadthfirst**（每个连通分量取最高度节点为根，自上而下树形——核心-汇聚-接入分层视角）/ **同心圆 concentric**（按连接度分环，枢纽居中）/ 网格 / 环形 / **锁定 freeze**（停用自动重排，新节点围绕质心螺旋落位，保留手动拖拽）
- **3 交互模式**：选择（原有表格联动）/ **路径追踪**（依次点击两台设备 → 核心 `aStar` 最短路径青绿高亮 + 其余压暗 0.14，顶部横幅显示跳数与途经——超 6 台折叠「等 N 台」；不可达红色横幅；再点起点取消；Esc/点击空白/× 清除）/ **邻域聚焦**（点击设备，一跳闭邻域之外压暗；Esc/空白清除）
- **链路标签开关**：聚簇降噪（`edge.nolabel` 类规则置于属性选择器之后，同特异度源顺序取胜）
- **迷你导航图**（右下角 160×104，DPR 自适应）：节点状态缩略点 + 视口矩形（`cy.extent()` 映射），**点击/拖动直接平移主视图**（模型坐标 → panBy 推导，防大图迷航）；pan/zoom/position/layoutstop 事件 rAF 合并重绘
- **工具带重设计**：Grafana 式图标分段控件（布局 6 + 模式 3 + 标签开关），完整中文 tooltip 说明、`aria-pressed`、窄屏收纳提示文案
- **持久化 v2**：布局 + 交互模式 + 标签开关按工作台存 sessionStorage（JSON），兼容读取 v1 纯布局字符串

### 升级 — 去「AI 模板味」（VLM 视觉审计驱动）
- **删除侧栏渐变 logo 方块**（左上角「N」135° 渐变圆角块——AI 生成控制台的头号特征，真实 B2B 工业软件用文字字标）；折叠态改纯字符标记（无底色块）
- **卡片扁平化**：去除全部卡片投影，1px 边框承载边界（AntD 5/Linear 式平面；浮层对话框保留 `--shadow-lg`）
- favicon 去渐变（扁平深蓝底 + 白色拓扑 glyph，与侧栏色系一致）

### 验证
- 真浏览器（agent-browser）全模式走查 19 张截图：层级/同心圆/网格/环形 VLM 分类全部正确（布局切换修复生效）；路径追踪（20 跳可达 + 不可达红横幅）、邻域聚焦（47 元素中 5 保留）、标签开关（20/20 边生效）、锁定（手动位置保持，切回力导重排）、缩略图（角点点击视口 -230→-1059 平移）、v2 持久化（reload 后标签开关保持）、1280 折叠侧栏（logo 无回归）
- 控制台零警告零错误（v0.2.2 前：2 条 invalid selector 警告）；gofmt 空 / vet 干净 / 181 测试 PASS / tsc 0 错误 / vite build / `make build` 嵌入

## [0.2.1] — 2026-10-10

**全局打磨版**：四个独立审计（后端 bug / 前端 bug / 降级核查 / 浏览器 UI 双对标）驱动的修复与升级。引擎修复 4 项 P1 + 10 项 P2；前端修复 4 项 P1 + 15 项 P2 + 1 项 P0；UI 功能对标工业控制台、视觉对标顶尖大厂（VLM 评审 7.2 → 8.5 → 9.0）。**181 个 Go 测试全绿**（+12）；零实现降级（审计确认）。演示：`./scripts/demo-lab.sh`（不变）。

### 修复 — 仿真引擎（internal/sim · repo · api · mcp）
- **空操作集拒绝**：`{"ops":[]}` / `{"ops":null}` 在引擎边界即被拒绝（400 `invalid_request`）——此前会落一条 0 操作的已应用变更集，使变更集级 undo 被 409 `not_latest` 永久卡死（审计 AUD1-3）
- **并行链路闭环**：`create_link` 对同接口对的第二条链路返回 `op_failed`（「link already exists between interfaces…」）；检查点恢复的链路匹配键加入 label 并按多重集对比——旧数据中的重复对链路也能哈希精确恢复（审计 AUD1-1）
- **前后对比 = 完整谓词**：连通性差异计算从「仅链路状态」升级为 §5 完整谓词（设备 up × 接口 up × 链路 up）——`set_device_status down` / `set_interface_status down` 注入的故障现在会产生正确的 `connectivityLost` 对（运行快照扩展 4 个可选状态字段，旧格式视为 up，状态哈希不变；审计 AUD1-2）
- **并发安全**：DSN 追加 `_txlock=immediate` + 事务开启 3 次有界重试，消除 SQLITE_BUSY（读→写升级竞争导致的 500 与运行丢失，审计 AUD1-4）；DSN 路径百分号编码（含空格/中文路径可用）
- **撤销原子性**：逆向应用与 `MarkReverted` 同事务；状态守卫拒绝撤销非已应用变更集
- **检查点完整性**：快照内嵌状态哈希（marker）+ 恢复前校验（被篡改/漂移 ⇒ 409 硬失败）+ 恢复后哈希复核
- **错误消息补全**：`simErrorMessages` 补 `invalid_request` / `op_failed` 条目——操作校验 400 不再返回空 message（此前前端只能显示「请求失败」）
- 其余：物化关系读失败传播（不再静默丢电路）、回滚失败保留原始错误、MCP `lab_checkpoint list` 空列表渲染 `items:[]`+`total:0`（§8 对齐）、`Path` 不可达 `hops:[]`（非 null）、链路仓储数值排序、`statusRecorder.Unwrap` 落实

### 修复 — 前端（web/src）
- **P0 拓扑标签不可见**：`min-zoomed-font-size:6` + 默认 fit 缩放 <0.6 导致全部标签消失（演示核心画面变成「匿名方块」）——标签始终渲染 + 节点内编码徽章化 + `minZoom:0.55` 钳制（审计 AUD4-P0）
- **新建接口 IPv4 校验缺失**：`create_interface` 分支静默丢弃地址 / `prefix:"abc"` 产生 NaN→400——补齐与配置分支一致的校验与中文行内错误（审计 AUD2-1）
- **`isValidIpv4` 前导零**：`010.0.0.1` 通过前端校验后被 Go 拒绝——正则收紧（审计 AUD2-2）
- **实验室取流竞态**：全部 /lab 取流挂载 `useSeq` 顺序守卫（工作台路由切换陈旧覆盖、重叠刷新留旧评分、运行详情下拉抖动）（审计 AUD2-8）
- 15 项 P2：表头吸顶修复、`fit` 等 `layoutstop`、增量布局不再全图重排、全局最新变更集判定（跨页撤销不再 409）、删除后页码钳制、`lastWorkspace` 失效清理、模态滚动锁 + 焦点陷阱（`useModalA11y` 统一五处）、错误块二级详情 + 重试、下拉截断提示、检查点标签确认后清空、错误 Toast 8s + 关闭按钮、`types.ts` 线格式对齐、`§4` 文档引用清出用户文案

### 升级 — UI 功能性（对标 Grafana / Datadog 工业控制台）
- **全出血工作台**：`/lab/:id` 突破 1200px 容器（1920 下画布 648→948+px）；分栏断点 1280→1080（笔记本保留双栏）；1024 堆叠无横向滚动
- **画布工具带**：放大 / 缩小 / 适配 / 重置 1:1 浮动控件 + 完整图例（运行/停机/接口故障设备 · 正常/中断链路）
- **列排序**：工作台列表、实体三表（设备/接口/链路）、检查结果、变更集——三态表头（升→降→还原）+ `aria-sort` + 数值后缀排序（vd-9 < vd-10）
- **中文检查目录**：CHK-001..005 中文标题/释义为第一行、英文引擎消息降为次行；实体 id 等宽码片化
- **对比对话框**：默认双运行自动生成（免手动点击）；连通性对列表折叠为「共 N 对，点击展开」芯片
- **演示级 Toast**：成功携带「查看变更集」动作链接（跳转变更集页并闪烁定位行）+ 变体图标；错误 8s
- 实体表筛选框、相对时间（「刚刚/5 分钟前」+ title 全时）、布局与标签页 sessionStorage 持久化、批量 uuid 短码 + 点击复制、`prefers-reduced-motion`

### 升级 — 视觉（对标 Ant Design Pro / Linear / Stripe）
- **暗色工程画布**：分层深蓝底（非纯黑）、节点=浅底编码徽章（内部 `DEV-001` 等宽 12px，9:1 对比度）、悬停信息栏（编码·名称·状态）、贝塞尔连线（up 加亮 `#7387ab`、down 3px 红虚线、选中加粗）
- **AA 对比度**：全部状态芯片文字 ≥4.5:1（success 3.05→4.71、error 3.73→5.40、warning 2.90→5.20、info 3.64→5.30、pool/circuit/banner 同步达标）；暗底图形元素 ≥3:1；警告色 token 统一 `#C77D0A`
- Favicon（SVG 网络节点标记）、`tabular-nums` 数字对齐、原生 select/滚动条 token 化、图例留白

### 文档
- `docs/simulation.md`：对比谓词升级说明（示例 38→24 对）、create_link 同对唯一性、检查点完整性校验
- `docs/api.md`：§7 错误消息示例更新（非空 invalid_request/op_failed）、空操作集 400、`hops:[]`
- `docs/verified-capabilities.md`：v0.2.1 验证矩阵（181 测试 + 审计 + 双对标 + VLM 评审记录）

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
