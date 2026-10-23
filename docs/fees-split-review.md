# 费用仓拆分决策评审——基于 v0.16.0 代码实况的逐条校验

> 评审日期：2026-10-23（v0.16.0，#125/#123 关闭、#124 修复待 Windows 复验之后）
> 评审对象：[fees-repo-split-plan.md](fees-repo-split-plan.md)（拆分决策与裁剪清单）、
> [fees-code-removal-plan.md](fees-code-removal-plan.md)（主仓移除方案与三条红线）
> 评审方法：全部结论以当日代码为证据（grep 依赖面 / 迁移归属 / 行数实测），
> 非意见性评审。

---

## 一、总评：三条主决策全部成立

| 决策 | 复核结论 | 证据 |
| --- | --- | --- |
| fork 而非分支 | **成立** | 费用与 IPRAN 是两个业务域在代码里已是事实：`internal/fees` 的 27 个文件只被 8 个装配点引用（api/fees.go、api/server.go、app/app.go、app/fees_cli.go、services/services.go、mcpserver/feetools.go + 两个测试），无一散进 sim/topology/detect 的业务逻辑——切面干净，fork 的物理成本低 |
| 不抽公共库（接受底座分裂） | **成立** | 「导入+校验+审计」底座的共享面就是 repo/workbook/mapping/validation 四包，各自独立无环；抽库的接口工程（2-4 周）远贵于两边各持一份。#124 三类修复今日全部落在共用代码上，正是「底座 bug 两边各修一次，几分钟的事」的实证——storeUpload 修复 fork 后两边 cherry-pick 即可 |
| 新仓砍 IPRAN 部分 | **方向成立，清单需修订** | 见下文五条修订——砍是砍得动的，但清单有遗漏和自相矛盾处 |

三条红线（不 revert / 数据不删 / 迁移链不断）**全部复核通过**，且今日有新实例：
红线 1 的保护面新增 #124 修复（storeUpload/WriteSnapshot/readBatchSiteCodes 全在共用代码，删除时必须原样保留）；红线 3 的链上最新费用迁移是 v34（#125 sheet 坐标）。

---

## 二、必须修订的五个点（按影响排序）

### A. 迁移链 C 类清单过时：v29–v32 → **v29–v34**

移除方案写「`internal/repo/migrations_v29..v32.go` 保留（迁移链完整性）」，
且拆分方案写「32 版迁移」。**两份文档都落后于现实**：

| 版本 | 归属（实测） | 处置 |
| --- | --- | --- |
| v29–v31 | 费用域（fee_batches/快照路径归一 #75） | 保留（方案已覆盖） |
| v32 | **非费用**——checkpoints 路径归一（#80，sim 层） | 保留（本来就留在链上，与费用无关） |
| v33 | **费用域**——risk_clue_decisions 裁定表（#119） | **方案未列，必须补** |
| v34 | **费用域**——fee_batches sheet 坐标（#125，今日合入） | **方案未列，必须补** |

若按现文执行，v33/v34 的迁移文件可能被当成「费用业务代码」删除——老库（含
v34）启动即版本校验失败，红线 3 被自己的清单打破。**C 类必须改为
「v29–v34 全部保留」**，且 facts.md 的 `repo-migrations=34` 门禁随行。

### B. `internal/importer` 缺席保留清单——费用仓的资源宇宙没有入库通道

拆分方案的保留清单有 mapping/workbook/validation（「导入必需」），却**没有
internal/importer**。而费用线的五个关联型场景全部以资源宇宙为前提：

- `link-scan`（费用-资源分级关联）读 `db.Resources.ListByType`；
- `retired-billing` / `idle-billing` / `missing-billing` / `unlinked` 四场景
  经 confirmed links 反查资源状态与指标点数。

费用服务本身不 import importer（只读 repo），但**没有 importer 就没有资源可
关联**——两份方案对「费用仓的资源从哪来」均未作答。实测 importer 的内部依赖
= domain + mapping + **renames** + repo + workbook + 测试辅助，其中 renames
在删除清单里。两个选项：

1. **importer 进费用仓 + 裁掉改名提案通道**（`importer/rename.go` 与调用点）——
   推荐：资源导入在费用仓是「建宇宙」用途，身份治理（W006 改名提案）是 IPRAN
   治理面，可断；
2. 或明确资源经 `backup` 通道从主仓恢复——需要新写恢复流程，比选项 1 贵。

无论选哪个，**「需要改造后才能删」清单必须补一行：importer × renames 的解耦**。

### C. 验证标准 #3（零 IPRAN 残留）与保留 `internal/metrics` 自相矛盾

拆分方案要求删完后「grep `device|topology|sim|alert|ring|ptp|igp` 在业务代码
里零命中」，同时又把 internal/metrics 列为必须保留（双账本交叉的另一半）。
**两者不可兼得**：

- 指标目录 14 键全是 IPRAN 端口性能概念（接收光功率/丢包率/带宽利用率…）；
- 摄入解析的 resolveBy 是 `device_name|port_name`——device 概念深植摄入面；
- 交叉场景（idle-billing/missing-billing）经 `db.Metrics.CountPointsOfResource`
  读指标点数——**指标数据是这些场景的氧气**，没有摄入管道（internal/metrics）
  就没有指标数据，两场景永远空转。

**建议**：验证标准 #3 改为「**IPRAN 治理面**（sim/topology/detect/notify/
renames/changes/governance/exporter）零残留」；**指标摄入面按数据源依赖豁免**
——正如资源表/资源类型本身也是 IPRAN 域概念但交叉场景必需，豁免的是「被
分析的数据的入口」，不是治理逻辑。

### D. mcpserver「可砍」低估了代价——费用线的主题就是 AI 循环

拆分方案：`internal/mcpserver`（1,415 行）可砍，「后期按需重建费用版」。
但 #125 自己的话：**「用户的核心诉求是看看 AI 能不能从费用数据里发现异常和
风险」**——这正是 fee_risks / fee_totals / fee_duplicates（#58 阶段 5、#120）
+ resource_search（资源发现）四个 MCP 工具存在的意义。砍掉 mcpserver，费用仓
出生即失去 AI 消费面，等于自断主题。

实测构成（非测试代码 1,546 行）：mcpserver.go 脚手架 238 + graphtools 536 +
labtools 516 + **feetools 256**。「按需重建」要重写的恰恰是脚手架 + 门禁
（facts_gate 的 13 工具闭集、真握手测试 harness），约 300+ 行与费用无关的
基建。

**建议**：mcpserver 进费用仓，裁掉 labtools（仿真）与 graphtools 的图三件套
（graph_traverse/impact/context 是拓扑概念），保留 resource_search + 三个
费用工具 + 脚手架——工具面 13 → 4，闭集门禁重定基线即可。

### E. 两仓门禁随行重定基线（执行清单缺一章）

两份方案都未提及 facts.md 门禁体系在拆分/删除后的重定：`api-routes 86 →
费用仓 ~20 / 主仓 ~70`、`api-fees-routes 14 → 费用仓 14 / 主仓 0`、
`repo-migrations 34`（两仓同链）、`mcp-tools 13 → 4 / 11`、
`go-top-level-tests 655 → 各自重测`。**不重定基线，两仓的门禁测试在删除
落地的第一个 commit 就全红**，反而会诱导「改测试凑绿」的破窗。建议拆分
执行清单加第七步：门禁基线重定（facts.md 分仓两份，各随各仓）。

---

## 三、执行顺序的确认与补充

移除方案的六步（冻结标签 → 删 A 类 → 改 B 类 → 全绿 → 真实库验证 → 提交
不 revert）**顺序正确**，补三点：

1. **fork 时点 = 现在**：v0.16.0 是干净点（25 包全绿、#125/#123 已关、
   #124 修复已进），fork 会把今日全部费用线工作（多 sheet 台账 + Windows
   修复）无损带给费用仓——再晚只会攒更多要两边同步的共用修复；
2. **主仓删除前先跑费用仓 e2e**：验证标准第 2 条（费用功能不变）的最诚实
   测法是在**费用仓**里跑 `go build ./... && go test ./...` + 三层 #125 验收
   （三批次/塔租 5000.00/幂等），而不是只在主仓删完看剩余测试绿；
3. **#124 关闭以 Windows 复跑为准**：主仓修复已进（cc7b80b/6c5b597/b3f05d7），
   但验收第 1 条写明「Windows 与 Linux 都通过」——Linux 绿不能替 Windows
   作证，issue 留开等实测是对的，fork 时该 issue 双仓各挂一份。

---

## 四、评审结论

**方案批准执行，清单先修订再动手。** 修订项按本文件第二节 A–E 落回两份
方案文档（或作为其勘误附录），其中 A（迁移链 v33/v34）是红线级必须项，
B（importer）决定费用仓五场景能否工作，C/D 是验收标准与主题自洽性，
E 是落地卫生。三条红线原文不动，全部成立。

---

## 五、增补评审（2026-10-23 下午，v0.16.1 之后）

> 评审基线从 v0.16.0 前进到 v0.16.1（质量与负载免疫加固版，十个提交）。
> 结论不变：**方案批准执行，fork 时点从「现在 = v0.16.0」上修为「v0.16.1」**。

### 1. fork 时点上修：v0.16.1 是严格更优的干净点

v0.16.1 在 v0.16.0 的 25 包全绿之上新增九个加固提交，其中四笔直接降低拆分成本：

| 提交 | 对拆分的意义 |
| --- | --- |
| e8c69d4（回滚隔离 rename Windows 竞争容忍） | 落在 `internal/fees/parquet.go`——**费用域文件**，fork 无损携带；若从 v0.16.0 fork 则费用仓出生即缺这笔 #124 同类修复 |
| 0efe8ae（性能门禁 CPU 化） | fees/sim 两仓共用的 testsupport.CPUTime + 门禁策略，fork 即得 |
| 069a8ad（repo 死代码清扫 7 函数） | **少带 7 个死函数过河**（原清单口径下的费用仓会原样继承） |
| 9538523/9470ab3/4de4a63（零覆盖两批 + #95 尾查） | 费用域补测（XLSX 证据通道/std 引擎 501 合约）随 fork 归费用仓 |

### 2. E 项门禁重定的基数更新

评审时 `go-top-level-tests = 655`；v0.16.1 已为 **681**（本轮 +26）。其余门禁基数
不变（复核于 v0.16.1）：`api-routes 86` / `api-fees-routes 14` / `repo-migrations 34` /
`mcp-tools 13` / `resource-types 33`。拆分执行清单第七步（门禁基线重定）按此表落。

### 3. #124 状态对齐

本轮曾以「Linux 全绿 + 三处修复代码核验」关闭 #124——与本文第三节第 3 点
（**关闭以 Windows 复跑为准**）不一致，已重开并留评论。fork 时双仓各挂一份不变。

### 4. 切面复核（v0.16.1 实测）

`grep -rln "internal/fees"`：真实 import 仍为评审所列 6 个装配点
（api/fees.go、api/server.go、app/app.go、app/fees_cli.go、services/services.go、
mcpserver/feetools.go）+ 测试；新增命中的 gc.go/uploads.go/domain/fees.go 三处均为
**注释引用**（无 import）。切面干净结论不变。

### 5. 拆分后费用仓的验证底座增强

本轮首次在 `-tags duckdb` 下全量跑通 `internal/api`（25 包全绿，修复一处
tag 无感知断言）。拆分评审原文要求「主仓删除前先跑费用仓 e2e」——现在费用仓
的 e2e 底座可以包含 api 包的 duckdb-tag 全量（不再只有 internal/fees），
三层 #125 验收（三批次/塔租 5000.00/幂等）+ duckdb 全量一起跑。

### 6. 执行清单仍然有效的修订汇总

A（v29–v34 全保留，红线级）· B（importer 进费用仓 + 断改名提案通道）·
C（验证标准 #3 改「治理面零残留」+ 指标摄入豁免）· D（mcpserver 进费用仓，
工具面 13→4）· E（门禁分仓重定，基数见上表）——全部维持，无新增修订项。
