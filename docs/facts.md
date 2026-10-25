# NetOps Lab — 机读事实源（facts）

> **本文件是「当前代码实数」的单一事实源**（issue #110 叙事漂移第三批的裁决产物；随费用仓拆分随行重定基线——拆分评审 E 项，fork 自 netops-lab v0.16.1 / 2223101）。
> 教义：一个冻结在散文里的数字是一句**每次演进都在撒谎**的句子——#66 清扫 14 处「恒 5 个工具」、#110 再清 4 处「恰好 5/十个」证明了纯文字清扫追不上代码。本文件把会漂移的数字集中到一处，并配 Go 漂移门禁测试把「可静态断言的数字」钉死在注册表/路由表/迁移表的**实数**上（先例：`TestAppVersionMatchesChangelogHead` 的版本号双锚定——常量与 CHANGELOG 头互锁，改一处不改另一处测试即红）。
> 实测基准：2026-10-20（v0.15.2 收口后、#106–#111 修复轮）。测试类数字随测试增删天然变化，无静态门禁，口径与更新纪律见 §3。

## 1. 静态可断言数字（漂移门禁）

下表每行的「当前值」单元格是**裸整数**（机器按表格行格式解析：首单元格为反引号包裹的 key，次单元格为裸整数——见 `internal/testsupport.FactsFileInt`）。门禁测试从代码侧实数断言 == 本表声明数：实数变了而本表未同步（或反之），测试红——漂移不再靠人眼。

| key | 当前值 | 断言方式（代码实数 → 本表声明数） |
| --- | --- | --- |
| `mcp-tools` | 4 | `internal/mcpserver.TestMCPToolCountMatchesFactsFile`：真实 MCP 握手 `tools/list` 实数；名字闭集另由 `TestMCPToolRegistry` 钉死 |
| `mcp-tools-readonly` | 4 | `internal/mcpserver.TestMCPReadOnlyToolCountMatchesFactsFile`：`--read-only` 变体握手 `tools/list` 实数（issue #10；不变式=full 面中 `readOnlyHint=true` 的子集，另由 `TestMCPReadOnlyRegistryIsReadOnlyHintSet` 钉死） |
| `api-routes` | 40 | `internal/api.TestAPIRouteCountsMatchFactsFile`：`apiRoutes()` 路由表总实数 |
| `api-fees-routes` | 14 | 同上（`fees` tag 过滤计数）——fees-layer.md §7 的口径 |
| `repo-migrations` | 34 | `internal/repo.TestMigrationCountMatchesFactsFile`：迁移列表长度 == 本值，且版本号连续 1..N（无跳号） |
| `resource-types` | 33 | `internal/domain.TestResourceTypeCountMatchesFactsFile`：`domain.AllResourceTypes()` 实数 |

**MCP 工具清单（4 个，费用仓拆分后实况）**：`resource_search`（资源宇宙检索，AI 发现面）+ 费用域 3 个 `fee_risks` / `fee_totals` / `fee_duplicates`。全部只读：无 SQL/Shell/SSH/文件工具；风险线索裁定与关联确认留在人类/HTTP/CLI 侧。全部 4 工具带 MCP 2025-06 标准注解（`readOnlyHint=true` / `destructiveHint=false` / `idempotentHint=true` / `openWorldHint=false`，显式无默认值，issue #10）；`mcp --read-only` 变体只注册注解 `readOnlyHint=true` 的工具（当前即全部 4 个）。

**费用域路由清单（`fees` tag 14 条）**：`POST /api/fees/imports`、`POST /api/fees/imports/sheets`（#125 多 sheet 台账整本导入）、`GET /api/fees/batches`、`POST /api/fees/batches/{id}/rollback`、`GET /api/fees/batches/{id}/evidence`（#86）、`GET /api/fees/query/totals`、`GET /api/fees/query/duplicates`、`GET /api/fees/risks/{scenario}`、`POST /api/fees/risk-clue-decisions`（#119）、`GET /api/fees/risk-clue-decisions`（#119）、`POST /api/fees/links/scan`、`GET /api/fees/links`、`POST /api/fees/links/{id}/confirm`、`POST /api/fees/links/{id}/dismiss`。

**迁移版本（v1–v34）**：v1–v3 核心表（migrations.go 字面量）起，逐版本单事务追加；当前头 v34（#125 fee_batches 加 sheet 坐标并重建幂等索引——`schema_migrations` 头由 repo 层迁移测试钉死）。

## 2. 派生闭集（不在此断言——已有各自的闭集派生机制）

以下数字**刻意不进 §1**：它们已由「注册表/切片派生 + 闭集测试」结构性防漂移，写死数字反而添乱（#91 MCP 类型清单、#108 HTTP/CLI usage 派生的先例教义）：

- 资源类型**清单**（`domain.AllResourceTypes()` ↔ SQL CHECK ↔ `/api/schema` ↔ web 镜像四点一致性）——§1 只钉**数量**，清单本身派生；
- CLI 顶层子命令清单（usage 从 dispatch 切片 join，go/ast 闭集测试）；
- 校验规则目录（E/W/I 13 条）、风险八场景目录（#114 加 idle-billing、#118 加 missing-billing）、检测规则目录——各自有闭集断言；
- OpenAPI 文档与路由表同源生成（`TestOpenAPISpecMatchesLiveServer` 反向行走）。

## 3. 测试计数（无静态断言——口径 + 当前值 + 更新纪律）

这三个数字每轮修复/新增测试都会变，**不是被测代码的属性**，无法配静态门禁（断言「测试数 == N」会让删一个坏测试变成构建失败）。口径 + 当前实测值如下；任何读者以复算命令为准，本表数字退化为「最近一次实测快照」。

| key | 当前值 | 口径（复算命令，仓库根目录执行） |
| --- | --- | --- |
| `go-top-level-tests` | 432 | `go test -list '.*' ./... 2>/dev/null \| grep -c '^Test'`（顶层测试函数口径，默认构建无 `-tags`） |
| `go-test-packages` | 16 | `go test -count=1 -timeout 15m ./... 2>&1 \| grep -c '^ok'`（全绿的含测试包） |
| `vitest-tests` | 129 | `cd web && npx vitest run` 的 `Tests` 计数（7 文件） |

**更新纪律**：改动测试面的提交（新增/删除/迁移任何测试）按门禁流程顺手更新本表三行与 README 的测试计数行；漏更不会让任何本地门禁变红——所以散文里的测试数字永远只作快照读，裁决权在复算命令。CHANGELOG 各版本段的测试数是**历史记录**（当轮实测），不随本表回改。

## 4. 事实源的维护契约

1. §1 表只允许**裸整数**值单元格——`FactsFileInt` 按此解析，说明文字写第三列；
2. 实数变化（新工具/新路由/新迁移/新类型）时：改代码 + 改本表 + 门禁全绿，一次提交完成——这正是门禁存在的意义（漏改本表 = 测试红，漏改别处散文 = 本文件与 grep 对照可查）；
3. 本文件不记录派生闭集的清单内容（§2 的教义），只记录「会被人手抄进散文的数字」；
4. 历史文档（CHANGELOG、dated test report、audit 报告、已关闭 issue 文档、ADR 原文决策段、verified-capabilities 各版本小节）中的旧数字是**当时记录**，一律不改写——本文件是「现在」的唯一权威。
