# NetOps Lab — 已验证 / 未验证能力清单

> 诚实为本：每一条"已验证"都可追溯到具体的验证方式与日期；未验证事项显式列出，不粉饰。
> 验证环境：linux/amd64（2 核沙箱），Go 1.27.2，Node 24.21.0 / npm 11.19.0。验证日期：2026-10-09（v0.1.0/v0.2.0）· 2026-10-10（v0.2.1 打磨版 · v0.2.2 画布多模式版 · v0.2.3 画布全屏工作台版 · v0.3.0 地理视图版 · v0.3.2 工程审查整改版 · v0.4.0 真实电信资源类型与身份治理版）· 2026-10-11（v0.5.0 身份治理闭环与来源谱系版）。

## ✅ 已验证（2026-10-09，linux/amd64）

| 能力 | 验证方式 | 备注 |
| --- | --- | --- |
| 全量构建（单二进制，含嵌入式前端） | `make web`（npm ci + vite build，tsc strict 零错误）→ `make build` → `bin/netopslab`（~27MB） | 构建产物 `internal/webui/dist` 不入库 |
| 96 个 Go 测试全绿 | `go test -count=1 -timeout 15m ./...`：13 包、96 通过 / 0 失败 | 含 API（httptest 真实装配）、CLI 子进程 E2E、MCP stdio 回环 |
| gofmt / go vet 干净 | `gofmt -l .` 输出为空；`go vet ./...` 无告警 | CI 将持续复检 |
| 活服务器全流程冒烟 | 真实 `netopslab serve` + curl：导入 27/0/3、16/0/4、21/0/4；`POST /api/validate` E13/W3/I1 评分 32；`GET /api/stats` 64 资源（27/16/21）；`GET /api/topology/check?fromDevice=DEV-001&toDevice=DEV-003` 路径长 2；HTML 报告含免责声明 | 编排者验证 + T9 复测（真实 curl 输出留档） |
| SPA 提供真实前端包 | `curl /` 返回标题 "NetOps Lab · 云网资源治理"，且引用哈希资源 `/assets/index-*.js`（非占位 stub） | 2026-10-09 T9 |
| CLI 端到端（E2E 验收） | `TestCLIEndToEnd`（os/exec 子进程）：analyze（离线）→ import×3 → validate → report --format json → query stats/resources/relations --depth 2；错误路径（not_xlsx → 退出码 1；未知子命令 → 退出码 2） | `internal/app/e2e_test.go` |
| CLI 退出码 0/1/2 | 同上，逐一断言 | §10 契约 |
| MCP 工具面 = 恰好 1 个只读工具 | `TestMCPToolRegistry`：`tools/list` == `["resource_search"]`；未知工具名 → 协议级错误 | 安全验收（SEC） |
| MCP stdio 回环 | `TestMCPServerResourceSearch`：go-sdk 客户端 × `mcpserver.Run`（双 os.Pipe），initialize 握手 + tools/call 检索（q、type、非法 type → IsError） | S0-3；**注意这是进程内测试**，非部署态（见下） |
| 确定性（ACC-4） | `TestValidationDeterminism`：两个全新库同流程，规范化问题 JSON 逐字节相等（id/时间戳除外） | 同输入 + 同 rule_version |
| 导入原子性（S0-6） | `TestImportAtomicRollback`：注入第二 Sheet 钩子错误 → 该批次资源/关系/引用/行结果/问题全为零，前置批次不受影响 | 零半写 |
| 幂等重导（ACC-2） | `TestReimportIdempotent`：第二遍 0 新增、27 skipped、集合级问题不变 | `scripts/demo.sh` 每次全新库跑出同样计数（连续两次输出一致，已留档） |
| 批次回滚与 409 in-use | 单测 + API 测试 + 真实 curl：回滚 circuits → 计数返回；回滚 devices → 409 `batch_in_use` | 2026-10-09 |
| 行级记账（ACC-1） | `TestImportRowAccounting`：导入/跳过/失败 与 row_results 逐条对账，失败行号与规则码精确匹配 seed 位置 | 27/0/3 等计数固化于 `testdata/expected-summary.json` |
| 问题证据（ACC-3） | `TestIssueSourceEvidence`：每条问题有 source_ref 或 evidence{sheet,row_number} 或 applicationGenerated | 前端"来源"列直接消费 |
| HTML 报告 XSS 转义 | `TestReportHTML`：注入 `<script>alert(1)</script>` 设备名 → 输出为转义文本，无原始 payload | html/template 全量转义 |
| 优雅停机 | `TestAppRunGracefulShutdown`：ctx 取消后 5 秒内退出；SIGINT/SIGTERM 同路径 | |
| 合成数据再生成确定 | `cmd/testdatagen` 两次输出 sha256 一致（T4b 验证） | 字节级确定 |
| 一键演示脚本 | `./scripts/demo.sh` 连续两次运行：全部步骤成功、输出计数一致（~0.3s/次，远低于 60s 预算） | 2026-10-09 T9 |
| **真实浏览器 UI 全流程（终审）** | Chrome 155 / agent-browser 隔离会话：空库空状态 → UI 三步向导依次导入 3 份合成工作簿（27/0/3、16/0/4、21/0/4）→ 资源搜索/详情抽屉/二跳关系展开 → 问题清单（证据+来源）→ 质量报告（免责声明 + iframe 预览）→ 概览与本会话真实数据一致（64 资源 / E13W3I1 / 评分 32） | 2026-10-09 终审；截图证据见交付包 `ui-screenshots/`；过程中发现并修复 API 缓存污染缺陷（ADR 0006，含回归测试） |

## ⚠️ 未验证（诚实声明）

| 事项 | 现状 | 计划 |
| --- | --- | --- |
| **部署态 TeleAgent 的 MCP 调用** | 本开发环境没有 TeleAgent 实例；`netopslab mcp` 只经过**进程内** stdio 回环测试（go-sdk 客户端 × os.Pipe） | 接入部署态时按 `docs/teleagent-integration.md` 操作并反馈；CLI fallback 可先行 |
| **Windows / macOS 构建** | 仅在 linux/amd64 构建与测试；纯 Go + 无 CGO 理论上可交叉编译（`GOOS=windows go build`），但**未实际产出或运行过** | CI 后续可加跨平台矩阵 |
| 长时运行 / 并发负载 | 无 7×24 运行或压测数据；SQLite 单写者 + WAL + busy_timeout 在 Phase 1 量级（每批几十行）下无压力证据，但也无高并发证据 | Phase 2 若引入服务化再做 |
| 部分覆盖统计 | 未在 T9 重新测量覆盖率（门禁 = fmt/vet/全测试绿）；历史峰值：domain 91.2% / importer 83.5% / mapping 89.3% / report 84.5% / validation 87.4%（T6 时点） | 下一阶段补 `go test -cover` 报告 |

> CI 已**实际运行并通过**（GitHub Actions，2026-10-09）：run #1 失败——go job 未构建前端导致 SPA 嵌入测试 404（已修复：go job 先 `npm ci && vite build`）；run #2 go ✅ / web ✗——setup-node npm 缓存未指定 lockfile 路径（已修复：`cache-dependency-path: web/package-lock.json`）；run #3 **全绿**（go: gofmt/vet/96 测试 + web: tsc/vite，commit 215b897）。两次失败均由 CI 真实暴露并当日修复——这正是 CI 的价值。

---

# Phase 2（仿真工作台，v0.2.0）— 2026-10-09

## ✅ 已验证（2026-10-09，linux/amd64 · 2 核沙箱）

| 能力 | 验证方式 | 备注 |
| --- | --- | --- |
| 169 个 Go 测试全绿 | `go test -count=1 -timeout 15m ./...`：17 包（14 个含测试全部 ok）、169 通过 / 0 失败（P1 96 + 22 domain/repo v5 + 34 sim 引擎 + 11 undo/检查点/CLI lab E2E + 2 API §7 + 4 MCP lab 工具） | docs 任务前后各跑一轮，无回归 |
| 物化确定性（ACC-P2-1） | `TestWorkspaceMaterialize`：3 批次 → 27/40/20（21 电路 − 1 自环），eth0/eth1… 端口分配推导注释化；两个全新库 → 拓扑与哈希逐字节一致 | 基线哈希 `aea27501fa59…` 跨 CLI/API/演示三路一致 |
| 操作原子性（ACC-P2-2） | `TestApplyOpsAtomicity`：[ok, ok, bad-id] ⇒ 422/ErrOpFailed、哈希不变、失败变更集记录（ops_applied=0 / failing `failed` / 其余 `not_attempted` / inverse NULL） | 真实服务器同路径复测（422 op_failed 消息格式逐一比对） |
| 检查目录 CHK-001..005 | `checks_test.go`：CHK-001 双接口同址（detail 排序）/ 接口或设备 down ⇒ 无发现；CHK-002 identical/contains/partial 三类 + 同址对排除；CHK-003 孤岛；CHK-004 分量（并列取字典序最小）；CHK-005 未配置 | rule_version 1.0.0；评分 100−5E−1W 下限 0 |
| 连通性与故障注入（ACC-P2-5） | `TestConnectivityFaultInjection` + 真实 CLI/API：ln-1 down ⇒ DEV-001→DEV-003 不可达 + 孤岛发现；undo ⇒ 路径逐跳与基线一致 | up-only BFS、邻居编码升序（路径确定） |
| 前后对比 diff（ACC-P2-6） | `TestRunDiff` + 真实输出：95→89、delta −6、added [CHK-001, CHK-003]、removed 2×CHK-005、连通性丢失 20 对；同 run ⇒ 400；跨工作区 ⇒ 404 | 匹配键 id 漂移免疫；空数组渲染 [] |
| LIFO undo（ACC-P2-8） | `TestUndoLIFO` + 真实 409 not_latest：旧变更集被新已应用阻塞；撤销新的之后旧的可行；failed/reverted/undo 不阻塞；undo-of-undo 可行 | 撤销后哈希精确回到物化基线 |
| 检查点 Online Backup 往返（ACC-P2-7） | `TestCheckpointRestore` + `TestBackupSnapshotRoundTrip` + 真实 CLI：cp 创建（Step(-1)+Finish，非裸拷贝）→ 删除设备 → 恢复 = 3 个前向操作变更集、restoredToHash == 当前哈希；重复恢复 no-op；快照文件删除 ⇒ 409 checkpoint_missing_file | ADR-0008 |
| 确定性（DET-P2） | `TestSimulationDeterminism`：两个全新库同物化 + 同操作脚本（含一次失败变更集）⇒ 运行/变更集规范 JSON 相等（id/时间戳除外） | 演示脚本双跑输出一致（仅 mktemp 路径不同） |
| CLI lab E2E（E2E-P2） | `TestCLILabFlow`（os/exec 子进程）：建台 → apply（含故意重复地址 + 链路 down）→ checks --run → diff → undo → checkpoint create/restore；每步 JSON 断言 | `./scripts/demo-lab.sh` ×2 全绿（双跑输出一致，~2s/次） |
| API §7 全路由真实服务器测试（API-P2） | `TestAPILabFullFlow`（httptest 真实装配）+ **真实 serve + curl 全旅程**（37+ 次请求逐一留档）：create 201/dup 409/empty 400、topology/path、ops 成功 + 422 + 400×2、checks run/list/detail、diff、undo/not_latest 409、checkpoint create/list/restore/no-op/404、lab report json+html、delete、未知 ws 404 | docs/api.md §7 的示例即该旅程的真实捕获 |
| MCP 5 工具（SEC-P2） | `TestMCPToolRegistry`（go-sdk 客户端 × 双 os.Pipe + initialize 握手：tools/list == 恰好 5 个）+ `TestMCPLabApplyOps/RunChecks/DiffUndo/Checkpoint`（restore_* 双重拒绝、runChecks=false ⇒ checks null、mode/action 联动校验）+ **真子进程 stdio 冒烟**（`netopslab mcp --db` 裸 JSON-RPC → v0.2.0 + 5 工具） | 部署态（TeleAgent 宿主）仍未验证——见下表 |
| 性能实测（PERF-P2） | `TestSimulationPerformance`（2 核）：物化 3–4 ms；10 操作变更集 0–1 ms；1 操作+自动校验 1 ms；RunChecks 1 ms；Path 0 ms；Topology+哈希 0 ms | 全部低于 2 s 预算三个数量级；数字入 docs/simulation.md §7 |
| 前端构建（含 Cytoscape） | `cd web && npm run build`：tsc strict 零错误；js 717.63 kB（gzip 226.73）/ css 22.22 kB | ADR-0009（单 chunk 决策） |
| **真浏览器 /lab 全流程冒烟（终审）** | Chrome 155 / agent-browser 隔离会话对真实 serve：空状态→新建工作台（批次多选）→拓扑页（27/40/20 + 哈希）→画布渲染+布局切换→节点点击→设备表行高亮联动→运行校验（95 分、CHK-003 孤岛发现）→执行操作（表单+JSON 双模式）→配置地址 10.0.0.1/24→撤销（哈希精确回基线 + toast）→故障注入（重复地址+断链）→前后对比（评分 −6、新增 CHK-001、连通性丢失 20 对）→检查点创建/no-op 恢复/真实恢复（哈希精确还原 + 变更集 cs-5）→导出报告新页签 | 2026-10-09 终审；截图证据见交付包 `ui-screenshots-p2/`；过程中发现并修复 2 个 UI 缺陷（空状态新建死锁、IPv4 未配置判定，commit 86b903c） |
| CI（Phase 2 首推） | push 86b903c → GitHub Actions run 37967168941 **首推即绿**（go: gofmt/vet/169 tests；web: tsc strict + vite） | Phase 1 的两个 CI 数训（go job 先建前端、npm cache 路径）直接生效 |

## ⚠️ 未验证（Phase 2 诚实声明）

| 事项 | 现状 | 计划 |
| --- | --- | --- |
| **部署态 TeleAgent 的 MCP 调用** | 与 P1 相同：环境无 TeleAgent 实例；已验证进程内 go-sdk 回环 + 真子进程 stdio 冒烟（含 5 工具握手） | 接入部署态时按 `docs/teleagent-integration.md` §2 操作并反馈；CLI fallback（§4.2 lab 子命令树）可先行 |
| **真浏览器 /lab UI 冒烟** | ~~未验证~~ → **已验证**（上表「真浏览器 /lab 全流程冒烟（终审）」行；唯一遗留：Cytoscape cose 布局的美学观感属主观项，未做人工美学评审） | 完成（2026-10-09） |
| Windows / macOS 构建 | 仅 linux/amd64；纯 Go + 无 CGO 理论可交叉编译，未实际产出运行 | CI 后续加跨平台矩阵 |
| 长时运行 / 并发负载 | 无 7×24 或压测数据；仿真为单用户交互式变更，SQLite 单写者 + WAL 足够，但无高并发证据；多工作区并发写显式 out-of-scope | 服务化场景再评估 |

## v0.5.0（2026-10-11）身份治理闭环与来源谱系版

四轮迭代（W6-W8 三轮功能开发 + W9 重构收敛轮）+ W10-D 发布轮，主代理逐轮派发开发子代理并派 W6-Q/W7-Q/W8-Q/W9-Q 只读质检（W6/W7 PASS-with-notes 零 P0/P1/P2 唯 P3；W8 PASS-with-notes 含 P2×1【样例披露失实，已在该轮提交 95727da 内修正】+ P3×6；W9 PASS 零 P2 唯 P3×2）；里程碑提交 d5e940f（W6）/ 10b140b（W7）/ 95727da（W8）/ 15800f9（W9）+ 本轮发布提交。以下为本轮（W10-D）实测；W6-W9 各轮的独立实测记录见 worklog 各 Task ID 小节。

### ✅ 已验证（2026-10-11，linux/amd64 · 2 核沙箱）

| 能力 | 验证方式 | 备注 |
| --- | --- | --- |
| **296 个顶层测试全绿** | `gofmt -l .` 空；`go vet ./...` 0 告警；`go test -count=1 -timeout 15m ./...` 18 个含测试的包全部 ok；`-race -count=1` importer/repo/api/services/mcpserver 五包 0 race（24.9s/26.5s/11.5s/1.8s/7.5s）；`npx tsc -b` 0 错误；`npm run build` ✓ 4.61s | 计数口径：`go test -list '.*' ./... \| grep -c '^Test'` = **296** = v0.4.0 基线 244 + W6 +17 / W7 +6 / W8 +21 / W9 +8 = +52（W9 分项 services 2 / repo 2 / query 1 / renames 1 / changes 1 / api 1；既有断言零放宽，唯一例外 = TestAppVersionP2 版本断言随发布适配，测试内注释声明） |
| 版本事实源 | `domain.AppVersion = v0.5.0`；`TestAppVersionP2` + `TestAppVersionMatchesChangelogHead` 双锚定实跑 PASS；live serve `/api/health` 实报 v0.5.0；api.md 契约基线、README、i18n.ts APP_VERSION、teleagent-integration 两处同步；MCP 真进程 stdio 握手 serverInfo v0.5.0 | 全仓 `git grep v0.4.0` 残留逐处判定：版本事实点已改 12 处、7 文件（domain 常量+注释 2 / sim_test 断言 2 / i18n 1 / api.md 2 / README 3 / teleagent 2），其余 29 行（15 文件）逐处判定为合法历史段 / 时点事实 / 里程碑引用：CHANGELOG 3（0.4.0 历史段 + 0.5.0 段内「基线/先例」引用）、README 英文摘要 1（v0.4 real-telecom 类型时点 + browser-smoke 历史时点）、api.md 2 + import-and-validation 1（「v0.4.0/起」引入时点）、verified 历史 5 + v0.5.0 段内引用 6、W5-D 代码注释 7（schema/server/cli/gc/gc_test/projection/batches）、samples 样例开发期 2、web schema.ts「起」1、vite.config 1 |
| GET /api/schema | live serve 真实输出：8 类型（Specs 注册序，ptp_port 第 8）/ 89 字段 / 13 规则；ip_pool.vlan_id intMin=1/intMax=4094（W9 intMax 投影）；ptp_port sheetHint="PTP"、23 字段、复合键 device_model+port_name、Label「PTP端口」 | 投影由 BuildSchemaDocument() 机械生成；MCP 工具数 grep 实测仍恰 5 |
| W6-W9 旅程（汇总自各轮独立验证） | W6：改名副本 1/1/2 + W006 → 提案 pending（matchedFields 11）→ apply（旧 res id 保留持新键；name 同步为 W9 增量语义）→ 再导 0/2/2 零 warning 幂等闭环；W7：devices 三轮导入 sourceRefs 27→54→81（E001 不追加、DEV-012 恰 2）、回滚 deleted.sourceRefs=27、回滚后幸存序稳定；W8：PTP 脚本 4/0/0、W006 ptp 提案 apply 键迁移 4 资源终态、refs 2→4→6、回滚 CASCADE 归零；W9：组合根 7 组指针恒等、N+1 1+N→恒 2 查（countingDBTX 卫兵）、intMax 1/4094、Name 随 apply 双修 e2e（含 PTP 键类回落探针） | 详见 worklog W6-D…W9-Q 小节与 CHANGELOG 0.5.0；本轮发布验收复验了其中的 W6 路由 / W7 版本化 / W8 脚本通道 / intMax 四项（下两行） |
| 发布验收（W10-D live serve，HTTP 层） | 23 项断言脚本全 PASS：`/` 200 + index.html 引用 4 chunk 逐个 200 + SPA 路由（含深链 /lab/:id）全 200；devices.xlsx 完整导入 30 行 27/0/3 + issues 4E/1W/27I（评分 79）与 v0.4.0 基线一致；再导 0/27/3 纯 skip + 资源详情 sourceRefs=2 版本序；PTP 脚本通道 4/0/0（键 6150\|gei-1/1/0/7 等）+ 非 PTP 文本 422 unsupported_script；GET /api/renames 200；stats 27 device + 4 ptp_port | make build ✓（bin 31,926,223 B）；脚本全程单进程持库，验收脚本落 /tmp（仓库零污染） |
| 冒烟与演示脚本（全部复跑） | w3/w4/w6/w7/w8 五冒烟全 SMOKE-OK；demo.sh 64 资源/57 关系/E13/W3/I1/评分 32；demo-lab.sh 九步全过，物化/撤销/恢复哈希 aea27501fa59… 与历史基线一致 | 均为发布二进制（bin/netopslab）驱动 |
| MCP 真进程 stdio | 握手 serverInfo netops-lab/**v0.5.0**；tools/list 恰 5（resource_search + 4 lab 工具）；resource_search 描述含「PTP端口」（W9 补触点）；tools/call DEV-001 → total 1 + source{devices.xlsx/设备清单/row 2} + appGen=false；未知工具 → JSON-RPC -32602 | 与 W9-Q live 探针同口径；MCP 工具面冻结红线未破 |

### ⏳ CI 运行态（推送后补记）

- 本轮发布提交的 GitHub Actions 运行态：**待推送后补记**（v0.4.0 先例：发布提交推送后由编排者补 run 号，不预填）。

## ⚠️ 未验证（v0.5.0 诚实声明，沿 v0.4.0 清单）

- 部署态 TeleAgent 的 MCP 调用（环境无 TeleAgent 实例；进程内回环 + 真子进程 stdio 冒烟已验证，含 5 工具握手）。
- Windows / macOS 构建（仅 linux/amd64 实际产出）。
- 长时运行 / 并发负载（无 7×24 或压测数据；GC 语义按单进程 serve 设计）。
- 浏览器级对 /api/schema 的消费 UI（前端镜像未切换——按发布决策「稳定性优先」，schema.ts 注释已注明可经端点校验）。
- **本轮（W10-D）发布验收为 HTTP 层**（SPA 路由/资产 200 + API 契约）：未重做 v0.4.0 轮的浏览器级交互回归（agent-browser）；v0.5.0 前端增量（W6 改名面板 / W8 ptp 类型枚举六件）经 tsc/build 门禁与 W6-Q/W8-Q 逻辑审查，交互层未在本轮重验。

## v0.4.0（2026-10-10）真实电信资源类型与身份治理版

五轮迭代（W1-W4 功能开发 + W5-D 发布轮），主代理逐轮派发开发子代理并派 W1-Q/W2-Q/W3-Q/W4-Q 只读质检（四轮均 PASS-with-notes，零 P0/P1）；四个功能提交 f32fa6c（W1 port 换键）/ e5f82b0（W2 基站）/ 97cf00e（W3 机房局站）/ 92195c4（W4 属性变更）+ 本轮发布提交。以下为本轮（W5-D）实测；W1-W4 各轮的独立实测记录见 worklog 各 Task ID 小节。

### ✅ 已验证（2026-10-10，linux/amd64 · 2 核沙箱）

| 能力 | 验证方式 | 备注 |
| --- | --- | --- |
| **244 个顶层测试全绿** | `gofmt -l .` 空；`go vet ./...` 0 告警；`go test -count=1 -timeout 15m ./...` 15 个含测试的包全部 ok；`-race -count=1` importer/repo/api 三包 0 race（13.9s/19.8s/6.9s）；`npx tsc -b` 0 错误；`npm run build` ✓ | 计数口径：`go test -list '.*' \| grep -c '^Test'` = **244** = v0.3.2 基线 207 + W1-W4 各轮 30 + W5-D 7（schema 投影 3 + schema 端点 1 + GC 3） |
| GET /api/schema（W5-D） | live serve 真实输出：7 类型（Specs 注册序）/ 66 字段 / 12 规则（E001-E006·W001-W005·I001 各带 severity+description）；port 复合键 + nameFieldOverride、base_station keyFallbackFields、machine_room.site_id refType=site 逐项核对；`TestAPISchemaEndpoint`（HTTP 契约）+ `TestSchemaProjectionMirrorsSpecs/Rules/SpotPins`（逐字段对照 mapping.Specs） | 投影由 `BuildSchemaDocument()` 机械生成，无手抄面；MCP 工具数 grep 实测仍恰 5 |
| uploads GC（W5-D） | `TestGCOrphanUploads`（孤儿删 / completed+failed+rolled_back 引用保留 / 相对路径形态 / 幂等二跑 / 目录缺失 no-op）+ `TestGCOrphanUploadsRemovalFailureNotFatal`（只读目录 → errors=1 不致命）+ `TestCLIGCSubcommand`（真实 CLI dispatch，单 JSON 契约 + usage 错误 exit 2）+ live serve 启动观察 + 手动 `gc` 子命令端到端 | 引用集含任何状态批次；MCP 进程零触发（调用点仅 cmdServe 与 gc 子命令）；repo 新增 `ListWorkbookPaths` |
| web manualChunks 分包（W5-D） | `npm run build` 实测：主 chunk 795.03 kB（gzip 249.86）→ **187.19 kB（gzip 54.51）**；vendor-react 166.24 kB / vendor-cytoscape 438.71 kB / vendor-maplibre 1,085.01 kB（懒加载异步 chunk，首屏 index.html 零引用——modulepreload 与 CSS 预载均已消除，maplibre 库/样式/worker URL 三点改为同点动态 import）；首屏 CSS 123.61 → 40.46 kB；`make build` 后 live serve：/ 返回 200，index.html 引用的全部 chunk/worker 资产逐个 200 | 唯一 >500 kB 警告 = vendor-maplibre（第三方单库本体不可再分，且为按需异步 chunk，如实保留警告）；无 three 依赖（3D 由 maplibre fill-extrusion 实现，任务书「three 家族」系误设） |
| W1-W4 旅程（汇总自各轮独立验证） | port 样例 2/1/2 键 `B2\|bvi2.1008`；基站样例 4/0/4 + 3×W004；机房局站 site 2/0/4 → machine_room 4/0/2 + belongs_to×3（前导零保留）；属性变更 基站IP diff → W005×1 → apply 生效 → 同值三导零 warning → dismiss 终态；迁移 v8 三守卫 / v9 / v10 双表重建 / v11 纯建表各被专门测试钉死（含 withoutVx 真 v(n-1) 库升级断言） | 详见 worklog W1-D/W1-Q/W2-D/W2-Q/W3-D/W3-Q/W4-D/W4-Q 小节与 CHANGELOG 0.4.0 |
| 版本事实源 | `domain.AppVersion = v0.4.0`；`TestAppVersionP2` + `TestAppVersionMatchesChangelogHead` 双锚定；live serve `/api/health` 实报 v0.4.0；api.md 契约基线、README、i18n.ts APP_VERSION、teleagent-integration 同步 | 全仓 `rg v0.3.2` 残留清零（历史段/时点事实除外） |

### ⏳ CI 运行态（推送后补记）

- 本轮发布提交的 GitHub Actions 运行态：release d71874d（v0.4.0，tag v0.4.0）→ **run 38072980042 全绿**；里程碑五提交 CI 全绿（f32fa6c→38059762847 / e5f82b0→38062171141 / 97cf00e→38067078341 / 92195c4→38070035274 / d71874d→38072980042）。历史：v0.3.2 提交 84294e3 → run 38056776554 全绿。

## ⚠️ 未验证（v0.4.0 诚实声明，沿 v0.3.2 清单）

- 部署态 TeleAgent 的 MCP 调用（环境无 TeleAgent 实例；进程内回环 + 真子进程 stdio 冒烟已验证，含 5 工具握手）。
- Windows / macOS 构建（仅 linux/amd64 实际产出）。
- 长时运行 / 并发负载（无 7×24 或压测数据；GC 语义按单进程 serve 设计）。
- 浏览器级对 /api/schema 的消费 UI（前端镜像未切换——按发布决策「稳定性优先」，schema.ts 注释已注明可经端点校验）。

## v0.3.2（2026-10-10）工程审查整改版

四维只读审查（R1 架构 A- / R2 代码质量 / R3 计划偏移 / R4 主键身份模型）结论为「代码能力层无降级，偏差集中在文档/承诺层」，本版为整改落地（三个提交：404161b 版本事实源 / 96b8c74 后端健壮性 / 9a9e2d0 前端修复）。

### ✅ 已验证（2026-10-10，linux/amd64 · 2 核沙箱）

| 能力 | 验证方式 | 备注 |
| --- | --- | --- |
| **207 个顶层测试全绿** | `gofmt -l .` 输出为空；`go vet ./...` 0 告警；`go test -count=1 ./...` 14 个含测试的包全部 ok；`-race` 0 失败（importer/workbook/api/repo 四包）；`tsc -b` 0 错误；`npm run build` ✓（chunk >500KB 警告为既有 P3-5 遗留，非本轮引入） | 计数口径：`go test -list '.*' ./... | grep -c '^Test'` **顶层函数计数 = 207**。v0.3.1 曾宣称 200、实跑 198（口径未对齐），本版已校正为实测值：198 基线 + **8 项修复回归**（rollback 事务化 / 解压上限 / 崩溃自愈 / 动态统计等）+ **1 版本一致性**（`TestAppVersionMatchesChangelogHead`，钉死 domain 常量 = CHANGELOG 头版本） |
| 版本单一事实源（R1-D1 / R3-C1） | `domain.AppVersion = v0.3.2`；`/api/health` 与 MCP 握手实报 v0.3.2；前端侧栏改读 `/api/health`（不再手抄）；TestAppVersionMatchesChangelogHead 红/绿锚定 | v0.3.1 的版本虚报链（domain 停 v0.3.0 / 文档 v0.3.1）已闭环 |
| 后端健壮性六项（96b8c74） | 各自新增回归测试：rollback in-use 检查入事务（TOCTOU 归零）、workbook 解压上限（100k 行/sheet、5M 单元格 → 422 workbook_too_large）、importing/analyzing 崩溃自愈（serve/CLI import 入口复位 failed(interrupted)）、批次删除 COUNT 聚合、MCP 文案补 port、dataDir 空 fail-fast | 详见 CHANGELOG 0.3.2 与 import-and-validation.md §11 |
| 前端四项（9a9e2d0） | tsc/build 门禁 + 运行时冒烟（Overview 动态渲染 4 类型含 port；Report 竞态注入验证；侧栏 mock health 版本回落；ImportWizard IP 键告警显示） | 留档 F2 子代理工作记录 |
| 真实样例合规文档化（R3-C5） | README 合规横幅加 samples/ 例外条款（中英双语）；新增 samples/README.md（来源/授权/披露）与 docs/natural-keys.md（R4 裁决落地） | 红线「no real network data」的已文档化偏差（项目所有者授权） |

### ✅ CI 运行态（推送后补记，2026-10-10）

- 84294e3（本轮全部四个提交的最终态）→ GitHub Actions **run 38056776554 全绿**（go: gofmt/vet/207 tests；web: tsc strict + vite）；fd648df / 145fdb8 / 237bb28 历史三提交 CI 亦均 success（runs 38045557197 / 38036448565 / 38029092684）——v0.3.1 提交的 CI 运行态缺口（R3-C6）一并闭环。
- 其余未验证事项（部署态 TeleAgent MCP 调用 / Windows/macOS 构建 / 长时并发负载）与 v0.3.0 段一致，无变化。

## ✅ 已验证（2026-10-10，v0.3.0 地理视图版 · linux/amd64 · 2 核沙箱）

| 能力 | 验证方式 | 备注 |
| --- | --- | --- |
| 194 个 Go 测试全绿（版本断言同步 v0.3.0） | `gofmt -l .` 空；`go vet ./...` 干净；`go test -count=1 ./...` 14 包全部 ok、194 通过 / 0 失败（+13 为 GEO-1 geo 管道测试） | 后端新增：迁移 v6、geo Kind 校验、拆分启发式、哈希红线、undo 保坐标 |
| geo 坐标管道端到端 | 真实二进制 + curl：导入 geodevices/geocircuits → 物化 → 拓扑 API 10/10 节点携带 geoLat/geoLng（哈尔滨枢纽 45.773/126.636、漠河 down）；哈希含/不含坐标逐字节一致（`TestWorkspaceHashIgnoresGeo` 双层）；delete→undo 坐标保留 | 留档 `/home/z/my-project/tmp/geo1/` |
| 既有数据集哈希不动 | `demo-lab.sh` 回归基线哈希 `aea27501…` 与 v0.2.x 历史一致 | geo 改动未移动任何既有数据集哈希 |
| 三视图切换与持久化 | agent-browser 1920×1080：拓扑图/2D/3D 分段控件切换 + 15 次快速往返；重载后视图自动恢复（画布 v2 存档 view 字段，旧档兼容） | 控制台全程 0 error 0 warning |
| 2D 地图（ESRI 底图） | 截图像素取证：地图区 425 色（真实纹理）+ VLM 确认地理内容（满洲里/海拉尔/根河地名）；pitch=0 | CARTO 水印砖问题取证与替换见 CHANGELOG（浏览器 fetch 三层级字节级同尺寸错误砖） |
| 3D 地图（塔体） | `getPitch()`=58.0 / `getBearing()`=-30 / zoom=3.2；`queryRenderedFeatures({layers:['towers']})`=10/10；VLM 确认「明显 3D 立体柱状物、高度不一、数字孪生感较强」 | 修复两处根因：zoom 表达式规范 + 亚像素 footprint（见 CHANGELOG） |
| 地图↔表格选择联动 | 点击 HLB-MH-BS1 标记 → `.geo-marker--selected` + 表格行选中（HLB-MH-BS1 漠河基站1 已停机）；键盘 Enter/Space 同语义 | 与拓扑图 tap 同一选中回调 |
| 链路状态可视 | down 链路红虚线 + marching-ants（rAF，prefers-reduced-motion 关闭）；故障演示 CKT-QQH-JGDC-10G（demo 脚本注入） | up 蓝实线 / 选中加亮 |
| 快捷键（地理视图） | F → fitBounds（黑龙江 bbox 实测）；L → 标签往返（10 codes ↔ 10 nolabel ↔ 0 恢复）；输入框聚焦不抢键 | 与拓扑视图 F/L 同语义 |
| zen 全屏（地理） | 3D 视图进 zen：地图 1888×999 满幅（ResizeObserver → map.resize）；pitch/塔体保持；Esc 退出 | 原生 Fullscreen + CSS 双级 |
| 空态与降级 | 无坐标工作台 → 引导空态 +「返回拓扑图」可用；瓦片错误源判定覆盖 arcgisonline（离线降级路径存在，未断网实测） | 部分缺坐标 chip 计数 |
| 前端构建与懒加载 | tsc strict 0 错误；vite build 主包 784.55 kB（gzip 247.16）+ maplibre 独立异步 chunk 1,084.89 kB（gzip 294.26）+ worker 资产；`make build` 嵌入 31,525,940 B；health version v0.3.0 | 新增运行时依赖仅 maplibre-gl 6.13.0（精确锁定） |
| 拓扑视图回归 | 布局切换（力导）/路径模式横幅/节点渲染/minimap 抽查通过；v0.2.3 zen/分栏/右键菜单能力未受影响 | GEO-2 仅新增视图维度，cy 实例门控复用 |
| VLM 视觉终审 | 2D 7.5/10、3D 6.5/10、综合 7.0（「2D 已非常接近 Grafana/商业级地理大屏标准；3D 偏示意级」） | 截图 22 张归档 `download/ui-screenshots-v0.3.0-geo2/` |

## ⚠️ 未验证（v0.3.0 诚实声明）

- **离线底图降级未断网实测**：瓦片错误→无栅格样式重建的路径代码存在且错误源判定已覆盖新主机名，但未在真实断网环境跑通（演示环境有公网）。
- **3D 渲染为示意级**：塔高 = 设备连接数（18+10×degree 像素），无材质/光影/流量驱动；对标物理级数字孪生需后续版本（VLM 6.5/10 如实记录）。
- **真实生产坐标精度**：演示数据坐标为近似值（哈尔滨/大庆/齐齐哈尔/加格达奇/呼中/漠河城市级），非站点级实测经纬度；真实数据经导入列映射自动落位。
- **ESRI 瓦片服务可用性**：免密钥匿名可用（2026-10-10 实测），但第三方服务无 SLA；断网/限流时降级路径同上。
- **映射界面新字段渲染**：导入向导映射 UI 对 geo_lat/geo_lng/geo 三个新列的建议与选择仅经后端 suggest 测试，未逐像素走导入向导全流程（列映射机制与既有字段同路径）。

## ✅ 已验证（2026-10-10，v0.2.3 画布全屏工作台版 · linux/amd64 · 2 核沙箱）

| 能力 | 验证方式 | 备注 |
| --- | --- | --- |
| 181 个 Go 测试全绿（版本断言同步 v0.2.3） | `gofmt -l .` 空；`go vet ./...` 干净；`go test -count=1 ./...` 14 包全部 ok、181 通过 / 0 失败 | 本版**后端零改动**（git diff 仅 web 5 文件 +1754/-41），红线保持 |
| **zen 全屏工作模式** | agent-browser 1920×1080：点击「全屏」→ 画布卡片 `getBoundingClientRect()` = (0,0,1920,1080)，非全屏 (261,277,948,760) = **2.88 倍面积**；`classList` 含 `lab-canvas-card--zen`；原生 `requestFullscreen` 成功态 + reload 后 CSS zen 降级保留均验证 | 目标元素 `document.documentElement`（Toast z-100 仍浮于其上） |
| Esc 逐级链路 | 菜单开 → Esc 只关菜单（zen 保持）；路径完成 → Esc 只清路径回引导态；引导态 → Esc 直通退 zen；原生全屏 Esc（keydown 被浏览器消费）→ `fullscreenchange` 同步退 zen + 顺带收菜单 | 三级判据：菜单 state / traceRef+.focus-dim / zen |
| 全屏左上详情卡 | zen 中点击链路 → 卡片显示「CKT-010 运行中 两端 DEV-010·eth1 ↔ DEV-011·eth0」；设备版（编码/名称/状态/接口 up-down/IPv4 折叠）由 FX-5 子代理验证（DEV-003 含 `10.60.3.1/24`） | 数据取自 SimTopology，无新请求 |
| 右键上下文菜单 | 真鼠标右键：空白 → 2 项（适配视图 F / 重置视图）；FX-6 子代理：设备 4 项（邻域聚焦 alpha 量化 519 暗/125 亮 / 路径起点 / 表格定位 CKT-012 行 selected / 复制 ID 剪贴板打桩）+ 越界翻转（菜单右缘 928 < 光标 934）+ a11y（Esc 还焦点 / ↑↓ 导航） | `cy.on('cxttap')`，无 cxtmenu 插件 |
| PNG 快照导出 | FX-6：`netops-topology-<wsid>.png` 落盘 1924×1164（2×）、四角 alpha=255 全不透明、bg `#0e1626` | `cy.png({full,scale:2,bg})` |
| 缩放百分比指示 | 主代理：fit 86%→55% 实时变化（rAF 节流）；FX-6：按钮缩放 61→77% | `cy.on('zoom')` |
| 手动排布位置持久化 | FX-6：拖节点到左上角 → localStorage `{v:1,p:{…}}` → reload 位置保持（212 亮像素）；切网格布局 → 存档清除重排；卸载时冲刷 160ms 合并窗口 | localStorage 按工作台隔离，隐私模式 try/catch |
| 键盘快捷键 | F：58%→90%（精确恢复 fit 缩放）；L：标签开关三态往返；输入框聚焦按 f 只进字符不抢键；Ctrl/Cmd/Alt/repeat 守卫 | window keydown |
| 分栏拖拽 + 持久化 | 主代理：真实鼠标拖把手 1231→1427 → 画布 948→1144（grid 列 1177.91/6/458.09）→ reload 保持 1144；键盘 ←→：aria-valuenow 72→76；clamp 30–85 | pointer capture，拖完一次 `cy.resize()` |
| 右栏折叠 / 展开 | 折叠：画布 1618px（单列占满工作区）→ reload 保持 → 展开恢复 1209px | localStorage |
| 页头紧凑化 + 画布高度 | 页头 180→97px；1080p 非全屏画布渲染高度 **760px**（`calc(100vh - 237px)`，卡片 843 无滚动条） | min-height 520 保持 |
| 路径模式引导横幅（v0.2.3 修复） | reload（mode=trace 持久化恢复）→ 横幅「路径模式：点击起点设备」；点起点 →「已选起点 DEV-002」；点终点 →「DEV-002 → DEV-007 · 5 跳 · 经 DEV-003、DEV-004、DEV-005、DEV-006」；Esc → 回引导态；引导态 × → 退出路径模式回选择模式（横幅消失） | pick-a 渲染层派生（时序缺陷修复见 CHANGELOG） |
| 控制台零警告零错误 | 主代理终验全程 + FX-5/FX-6 子代理全程 | errors 空 |
| 前端构建 | tsc strict 0 错误；vite build 768.28 kB；`make build` 29,809,718 B 嵌入；health version v0.2.3 | 无新运行时依赖 |

## ⚠️ 未验证（v0.2.3 诚实声明）

| 事项 | 现状 | 计划 |
| --- | --- | --- |
| 原生全屏在真实多显示器环境的窗口行为 | 无头单视口验证（成功态 + 降级态都走查过；多显示器 DPI 缩放未测） | 真机复检 |
| 剪贴板复制在真实浏览器权限提示 | 无头环境剪贴板读取无权限，写入路径用打桩验证（`navigator.clipboard.writeText` 调用参数精确断言；execCommand 降级代码在位） | 真机复检 |
| 触屏长按呼出右键菜单 | 桌面 contextmenu 路径验证完整；触屏未测 | 触屏设备复检 |
| 常驻高频拖拽的分栏性能 | 单次拖拽 + 一次 resize 补偿已验证；未做连续快速拖拽压力测试 | 需要时补测 |

## ✅ 已验证（2026-10-10，v0.2.2 画布多模式版 · linux/amd64 · 2 核沙箱）

| 能力 | 验证方式 | 备注 |
| --- | --- | --- |
| 181 个 Go 测试全绿（版本断言同步 v0.2.2） | `gofmt -l .` 空；`go vet ./...` 干净；`go test -count=1 ./...` 14 包含测试全部 ok、181 通过 / 0 失败 | 后端零改动，仅 AppVersion + 断言 |
| **布局切换真实生效**（v0.2.1 回归修复） | agent-browser 依次点击 层级/同心圆/网格/环形 → VLM 独立分类 4 张截图 = hier/concentric/grid/circle 全部正确；v0.2.1 的「04-layout-grid.png」VLM 复核 = force（证实旧版从未生效） | setState 闭包陷阱，`startLayout(cy, kind)` 显式传参修复 |
| 6 布局 + 锁定 | 层级（连通分量最高度根）/同心圆（度数环）/网格/环形 VLM 分类通过；锁定模式 eval 移动节点 (500,500) 保持不动，切回力导后重排 (68,464) | Cytoscape 内置布局，无新依赖 |
| 路径追踪模式 | 真浏览器点击 DEV-001→DEV-021：横幅「20 跳 · 经 DEV-002…DEV-020」、青绿路径 + 0.14 压暗；DEV-001→DEV-022（不连通）：红横幅「不可达」；Esc/空白/× 三路径清除全部验证；途经 >6 折叠「等 N 台」 | aStar 为 Cytoscape 核心内置 |
| 邻域聚焦模式 | 点击 DEV-005：47 元素中 42 压暗 / 5 保留（自身 + 2 邻居 + 2 边）；Esc 清零 | closedNeighborhood 一跳闭邻域 |
| 链路标签开关 | 点击切换后 `cy.edges()` 20/20 持有 nolabel 类；reload 后保持（v2 持久化） | 类规则源顺序覆盖属性选择器 |
| 迷你导航图 | 视口 extent 点击前 (−230,71) → 点击角点 (−1059,−415)；VLM 确认缩略点 + 视口矩形正常渲染；DPR 自适应代码路径 | rAF 合并 pan/zoom/position/layoutstop |
| 去「AI 味」 | `document.querySelector('.sidebar-logo')` = null；`.card` computed box-shadow = none；VLM 审计确认「没有彩色/渐变 logo 方块」「无明显 AI 模板感」 | VLM 初始审计将渐变 logo 列为头号 AI 特征 |
| 控制台零警告 | v0.2.2 前 2 条 `node:hover` invalid selector 警告 → 事件驱动 `.hovered` 类后清零；errors 空 | |
| 响应式无回归 | 1280 折叠侧栏（纯字符标记）；工具带窄屏收纳 hint；1080 堆叠布局不变 | |
| 前端构建 | tsc strict 0 错误；vite build；`make build` 嵌入新前端（29,789,110 B）；health 返回 version v0.2.2 | 无新运行时依赖 |

## ⚠️ 未验证（v0.2.2 诚实声明）

| 事项 | 现状 | 计划 |
| --- | --- | --- |
| 悬停 `.hovered` 微交互的视觉过渡 | 事件 + 120ms transition 代码就位并触发（类已验证），无头截图无法体现动画帧 | 真机复检 |
| 路径追踪与邻域聚焦的触屏路径 | 点按等价单点 tap 已覆盖核心（横幅 + 类应用）；hover 信息条同 v0.2.1 限制 | 触屏设备复检 |
| 大拓扑（>50 节点）下缩略图性能 | 27 节点实测无感知延迟；rAF 合并已做，未做 200+ 节点压力测试 | 引入全图渲染时补测 |

## ✅ 已验证（2026-10-10，v0.2.1 打磨版 · linux/amd64 · 2 核沙箱）

| 能力 | 验证方式 | 备注 |
| --- | --- | --- |
| 181 个 Go 测试全绿 | `go test -count=1 -timeout 15m ./...`：14 包含测试全部 ok、181 通过 / 0 失败（169 基线 + 12 新增：空操作集三边界、同对链路拒绝 + 检查点并行链路恢复、完整谓词 diff ×2、快照完整性、回滚错误保留、DSN 特殊字符、MarkReverted 状态守卫、数值排序、并发压力 ×2、MCP 空列表/空 ops） | gofmt / go vet 干净 |
| **零实现降级（全局审计）** | AUD-3 独立审计：两份 binding brief 逐条对照（P1 ~54 行 + P2 ~37 行）→ **0 缺失 / 0 静默降级 / 0 stub / 0 文档虚报**；红线（无生产写、无 SQL/Shell 逃逸、5 工具封闭面）全部复核 | 审计报告留档 worklog AUD-3 |
| 引擎 P1 修复 wire 级验证 | 真实 serve + curl：空 ops → 400 非空 message；坏 op → 422 op_failed（含失败变更集后缀）；halted 状态 → 400 非空（v0.2.0 为空 message）；检查点空列表 `items:[]`+`total:0`；不可达 `hops:[]` | 审计 AUD1-1/2/3/4 全部闭环 |
| 完整谓词 diff | 单测 + 浏览器：set_device_status down / set_interface_status down 故障在 diff 中产生 connectivityLost；旧格式快照回退"视为 up"（历史 diff 字节一致）；状态哈希不受影响 | AUD1-2 修复 |
| 前端 P0/P1 修复浏览器验证 | 真实浏览器：拓扑标签默认缩放可见（节点内 DEV 编码徽章）、create_interface IPv4 校验（中文行内错误）、前导零拒绝、画布↔表格双向联动（lab-row-selected）、三布局 fit、滚动 600px 后表头吸顶、8×Tab 焦点陷阱、ESC 焦点还原、双击防抖 | AUD-2 全部 P1 闭环 |
| 响应式与全出血 | agent-browser 4 档视口实测：1920 → 画布 982px（v0.2.0 为 648px）、无横向滚动；1280 → 分栏保留（canvas 598px）；1024 → 堆叠、无横向滚动；概览页仍 1200px 容器 | AUD-4 W1 |
| **UI 双对标审计 + 修复** | AUD-4 实测评分（工业控制台 38/50 → 功能项逐项修复；美学 27/40 → 全部 P1 修复：AA 对比度、暗色画布、favicon、tabular-nums、图例、缩放控件、列排序、中文检查目录、自动 diff、折叠对芯片、演示 Toast） | TOP-10 全部落地（FX-3 + FX-4） |
| **VLM 视觉评审** | glm-5v-turbo 独立评审暗色画布工作台：7.2/10（砖块节点、细连线、标签噪音）→ 修复后 8.5/10 → 最终二进制 9/10（"符合工业级仿真软件的审美标准"：节点内编码、无标签堆叠、控件图例齐全、平滑连线） | 截图留档 `download/ui-screenshots-p2-polish/` |
| 终验全旅程（真实二进制） | v0.2.1 二进制 + HTTP 种子（UI 同路径）+ 真浏览器：物化 27/40/20 基线哈希 aea27501（确定性跨版本）→ 运行校验 95 → JSON 故障注入 cs-5（2 操作）+ 自动检查 → diff 自动生成（7 运行历史）→ 撤销 cs-6 哈希精确回基线 → 检查点创建/破坏/恢复（cs-4 哈希还原）→ 报告导出（免责声明）→ 画布交互（节点点击/排序/筛选/悬停信息栏）→ 3 档视口 | 23 张截图留档 |
| 前端构建 | `npx tsc -p tsconfig.app.json --noEmit` 0 错误；`npm run build`：js 737.34 kB（gzip 232.73）/ css 28.63 kB；dist favicon.svg 引用 + 200 | 无新运行时依赖 |

## ⚠️ 未验证（v0.2.1 诚实声明）

| 事项 | 现状 | 计划 |
| --- | --- | --- |
| 部署态 TeleAgent 的 MCP 调用 | 与 v0.2.0 相同（环境无 TeleAgent 宿主）；进程内 go-sdk 回环 + 真子进程 stdio 冒烟均绿 | 接入部署态时按 `docs/teleagent-integration.md` 操作 |
| `prefers-reduced-motion` 实际降级 | 媒体查询 + matchMedia 代码就位，无头浏览器无法模拟该偏好 | 真机开启系统降级动效后复检 |
| 悬停信息栏的触屏路径 | 无头环境无 touch hover；点按 → 侧面板详情已覆盖同等信息 | 触屏设备复检 |
| 2× DPR 下节点编码渲染 | 逻辑 1× 验证；高分屏未实测 | 现场演示前抽查 |
| 剪贴板复制（headless 被拒） | Clipboard API + execCommand 双回退代码就位；headless Chromium 拒绝授权属预期 | 真实浏览器（localhost 安全上下文）复检 |
