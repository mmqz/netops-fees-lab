# NetOps Lab — 已验证 / 未验证能力清单

> 诚实为本：每一条"已验证"都可追溯到具体的验证方式与日期；未验证事项显式列出，不粉饰。
> 验证环境：linux/amd64（2 核沙箱），Go 1.27.2，Node 24.21.0 / npm 11.19.0。验证日期：**2026-10-09**。

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
