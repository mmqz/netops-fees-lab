# NetOps Lab — 已验证 / 未验证能力清单

> 诚实为本：每一条"已验证"都可追溯到具体的验证方式与日期；未验证事项显式列出，不粉饰。
> 验证环境：linux/amd64（2 核沙箱），Go 1.27.2，Node 24.21.0 / npm 11.19.0。验证日期：2026-10-09（v0.1.0/v0.2.0）· 2026-10-10（v0.2.1 打磨版 · v0.2.2 画布多模式版 · v0.2.3 画布全屏工作台版 · v0.3.0 地理视图版 · v0.3.2 工程审查整改版 · v0.4.0 真实电信资源类型与身份治理版）· 2026-10-11（v0.5.0 身份治理闭环与来源谱系版 · v0.6.0 治理三部曲终章与数据双向闭环版）· 2026-10-12（v0.6.1 地图修复版 · v0.6.2 运维加固版 · v0.6.3 用户反馈地图轮）。

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

## v0.8.0（2026-10-13）真实公开数据里程碑

> 验证环境同前（linux/amd64，Go 1.27.2，Node 24.21.0）。轮次 W26-W28；
> 数据许可与逐类口径见 [data-sources.md](data-sources.md)，裁决见
> [adr/0015-real-public-data.md](adr/0015-real-public-data.md)。
> （诚实补记：v0.7.0 的逐节验证段在先前会话基础设施切断中未落盘——其
> 验收证据以 14 个已关闭 issue 回帖 + 门禁测试为准；v0.8.0 起恢复本清单纪律。）

| 能力 | 验证方式 | 结果 |
| --- | --- | --- |
| 提取管线确定性 | `scripts/realdata/extract.py` 连续两次运行 md5 三文件一致（cables/basestations/core） | 字节级一致（gzip mtime=0 + 坐标 5 位量化 + 全序） |
| 31 工作簿导入链 | `demo-real.sh` + `internal/app/e2e_realdata_test.go`（门禁） | 1,965 行 imported / **0 failed**；计数逐簿钉死（防数据集漂移） |
| 真实基站数据 | OpenCelliD 快照过滤 → 1,198 站/1,851 小区（含 844 NR）；站编码=主小区 CGI；MNC→运营商映射入 manifest | 哈尔滨系 672/大庆 520/黑河 6（众源覆盖偏差如实披露） |
| 干线走廊 | 7 条走廊真实几何（滨洲线 231 段/绥满高速 285 段等）；锚点链里程（哈大齐 273.6km 等真实测地值） | 地图图层 + 光缆台账双口径（并集长度另记、不混用） |
| 劣化→告警旅程 | 7 日性能摄入 → `detect` → 4 条 firing 告警（packet_loss_rx_pct 连续越限） | run_state 联动真实触发 |
| 跨层影响分析 | OC-HDQ 置 down → impact up = 80 节点 | circuit/customer/service/leased_line 全可达（e2e 钉死） |
| 新地图图层（浏览器级） | `scripts/w28_browser_regression.sh`（agent-browser 1600×900） | **14/14**：图层存在/源加载/渲染要素 14 条/面板新分组/开关初始态/切换 visibility=none/3D 保持/console 零错误；截图 tmp/w28-2d/3d-realdata.png |
| mapdata 资产契约 | `internal/webui` TestHandlerRealDistRealNetworkLayers（门禁） | 200 + application/geo+json + max-age + FeatureCollection 内容级断言 |
| 全量门禁 | gofmt 空 / vet 0 / `go test -count=1 ./...` 23 包全绿 / tsc 0 错误 / `npm run build` 5.2s / `make build` | 429 顶层测试（427 基线 + e2e_realdata + webui 资产，`go test -list` 口径） |
| 演示三脚本回归 | demo.sh（27/0/3、16/0/4、21/0/4、E13W3I1、64 资源）/ demo-lab.sh（哈希 aea27501fa59 一致）/ demo-real.sh（本文档上行） | 三脚本零修改、零回归 |
| CI（GitHub Actions） | run 38373827971（2603805）**failure**——沿用 v0.6.0/v0.7.0 同况：账户 Actions-minutes 配额耗尽（job 3 秒结束、steps=0，基础设施问题非代码）；本地门禁集与 CI 同集（gofmt/vet/test/tsc/build）全绿，如实记录不伪造 | 配额恢复后 CI 自证 |

## ⚠️ 未验证（v0.8.0 诚实声明）

| 事项 | 现状 | 计划 |
| --- | --- | --- |
| 1,198 基站点在大屏的视觉密度 | 1600×900 无头断言渲染要素与开关切换；未在 4K/投影实测观感 | 现场演示前抽查（默认关，风险可控） |
| 光缆走廊与导入资源的视觉对齐（逐段） | 走廊=公开几何，光缆台账=锚点链里程，两者口径已在文档分列；逐米对齐不存在 | 不适用（口径已声明） |
| perf-day 摄入的 macOS/Windows 路径 | 与 v0.7.0 相同：仅 linux/amd64 验证 | 跨平台矩阵后补 |

## v0.6.4（2026-10-12）地表覆盖轮

用户现场反馈「2D/3D 全是陆地、没有水、没有森林」（issue #15，v0.6.3 验收后）：离线数据此前只提取道路/铁路/行政边界，`natural`/`landuse`/`place` 整体缺席。本轮重提全量自然要素 + 离线简洁底图设默认（用户要「干净 2D、无商铺标注；3D 有真实高度与河流」）。

### ✅ 已验证（2026-10-12，linux/amd64 · 2 核沙箱）

| 能力 | 验证方式 | 备注 |
| --- | --- | --- |
| **自然要素数据管线** | `scripts/gen_natural.py` 重跑产出 29,449 条（森林 12,955 / 水域面 4,961 / 河流 3,591 / 溪流 2,309 / 渠 1,113 / 草地灌木农田湿地 4,557）+ 17,615 地名，gzip 8.7 MB 双坐标变体内嵌；GCJ 首坐标偏移 +0.0050°E/+0.0023°N 实测 | osmconf `closed_ways_are_polygons` 教训入 issue-15 文档（缺失时森林面静默掉进 lines 层） |
| **2D 干净底图（#15）** | N1–N11 PASS——natural 源加载 29,449 条、森林/水面/河流/草地默认可见、地名 17,615 点四级 minzoom、公路默认开、默认无栅格底图源（离线简洁）、资产 200×4 | 浅色主题（暖白底 + Amap 系配色）；偏好键 v2 升级（旧 amap 默认不沿用，升级即见新观感） |
| **GCJ 变体切换** | N10/N11 PASS——切高德街道 → natural 源 URL 变 `hlj-natural-gcj`，切回离线 → WGS84 恢复 | 与等高线同策略：预投影文件，运行时零转换开销 |
| **3D 自然呈现** | C1–C4 复跑 PASS（terrain=dem + 分档夸张不变）；自然面/水系线贴 DEM 渲染 | 森林给山脉上色、河流随山谷走势（VLM 复核 tmp 截图，不进交付物） |
| **图层面板** | E0 PASS（面板含「自然要素」「交通与区划」分区）；E4/E5 PASS（森林/水系开关隐藏对应图层） | 自然要素任意底图可用；交通/区划仅 WGS84 底图 |
| **高德可达性（用户问询）** | REST district API key 有效（status=1 "OK"，哈尔滨市返回）；JS loader HTTP 200；栅格 style=8 / 卫星 style=6 瓦片 200 | 沙箱实测；高德保留为可选在线底图（栅格 POI 无法去除，不设默认） |
| **门禁** | `gofmt -l .` 空；`go vet ./...` 0 告警；`go test -count=1 ./...` 全绿（版本钉 TestAppVersionP2 → v0.6.4）；`npx tsc -b` 0 错；`npm run build` ✓（dist ≈22.9MB，+8.7MB）；浏览器回归 **35/35 PASS** | 截屏仅存 repo tmp/（不进交付物，用户明确要求） |
| 版本事实源 | domain.AppVersion = v0.6.4；CHANGELOG 头 0.6.4；i18n / README MCP / api.md 基线同步 | 四触点齐 |

### ⚠️ 未验证（v0.6.4 诚实声明）

- **Windows 实机**：零 Go 行为变更（版本常量与注释除外），沿用 v0.6.2 声明。
- **高德底图上自然要素视觉对齐**：沙箱封禁 autonavi，GCJ 变体验证到 HTTP 200 + 生成期偏移抽查；真实高德底图对齐待用户环境复验。
- **z≥13 极近观轮廓精度**：自然要素为分级 RDP 简化派生（森林 ~90m 容差），街区级精度需矢量瓦片化（未纳入本轮，ADR-0013 后果节）。
- 村落地名 z≥11 才显示（防标注洪水）；高德在线模式其栅格自带 POI 仍显示（不可编辑）。

## v0.6.3（2026-10-12）用户反馈地图轮

用户现场反馈三条缺陷（#12 相机锁联+比例 / #13 高程不可见 / #14 发布断档）根因收口 + 发布流水线修复。零子代理（全部主代理实现）。

### ✅ 已验证（2026-10-12，linux/amd64 · 2 核沙箱）

| 能力 | 验证方式 | 备注 |
| --- | --- | --- |
| **347 个顶层测试全绿**（无 Go 行为变更，纯前端轮） | `gofmt -l .` 空（含 6 个历史空格缩进文件规范化）；`go vet ./...` 0 告警；`go test -count=1 ./...` 21 包 ok；`npx tsc -b` 0 错误；`npm run build` ✓（dist 14MB） | 版本事实源 `TestAppVersionMatchesChangelogHead` 实跑 PASS |
| **2D/3D 独立相机（#12）** | 浏览器级 G1/G2 PASS——2D 设哈尔滨 z9.4/bearing 20 → 切 3D 设塔河 z7.5 → 切回 2D 相机恢复 `126.630,45.750,9.40,20`（含自定义 bearing，旧版会强制归零） | 每视图快照存/恢复；±1.3 补偿 hack 移除；经拓扑图中转=重新适配（ADR-0012 D1） |
| **初始比例（#12）** | B1 PASS——哈尔滨聚簇一次点击 zoom 4.84→11.49 拆簇（旧版 +2.2 需两次）；2D maxZoom 12.5 / 留白短边 8% 自适应 / 3D 上限 8.6 | 演示数据全省跨度初始视野不变（z4.84），城市尺度数据上限放宽 |
| **等高线（#13）** | A3/A4 PASS——`contours-major` 默认 visible、源加载完成（18,604 条折线，VLM 复核大兴安岭放大视野等高线清晰密集）；E3 PASS——开关隐藏；D3 PASS——WGS84/GCJ 双变体 200 | 生成：`scripts/gen_contours.py`（matplotlib marching squares + RDP 1.5e-3°）；GCJ 逐顶点预投影 → 任意底图可用 |
| **自适应地形夸张（#13）** | C3 PASS（远观 2.5 ≥2.5）/ C4 PASS（setZoom 9.2 后 1.3 ≤1.5）；VLM 复核 3D 全省截图确认「山脉立体轮廓、山谷、凹凸感」可见（v0.6.2 同视角为平面） | 分档 <6.5→4 / 6.5–8→2.5 / ≥8→1.3，zoomend 重挂 |
| **光标海拔读数（#13）** | H1 PASS（大兴安岭 124.1E,51.5N 光标 →「海拔 772 m」）/ H2 PASS（跳 118.5E,40N 覆盖外 →「海拔 —（超出高程覆盖）」） | geoElevation.ts 客户端 terrarium 解码 + 双线性采样 + GCJ 单步逆偏移 |
| **发布断档修复（#14）** | tag v0.6.3 + GitHub Release（linux-amd64 二进制 + source tar.gz）+ 下载产物 `serve --api/health` 实报 v0.6.3 核对 | v0.4.0–v0.6.2 只有 tag 无 Release——用户此前一直跑 v0.3.0；截屏仅存 repo tmp/，不进交付物 |
| 浏览器级回归 | w17 脚本扩展至 **21/21 PASS**（11→21：+等高线 ×4、夸张分档 ×2、相机独立性 ×2、海拔读数 ×2） | 断言升级为「视觉有效」导向（ADR-0012 D3，#13 教训） |
| 版本事实源 | `domain.AppVersion = v0.6.3`；CHANGELOG 头 0.6.3；i18n APP_VERSION / README MCP version / api.md 基线三触点同步 v0.6.3 | 版本矩阵四处齐 |

### ⚠️ 未验证（v0.6.3 诚实声明）

- **Windows 实机**：本轮零 Go 行为变更（gofmt 空白规范化除外），沿用 v0.6.2 声明。
- **高德在线瓦片 + GCJ 等高线视觉对齐**：沙箱封禁 autonavi，GCJ 变体仅验证 HTTP 200 与生成期偏移量抽查（0.0048°E/0.0020°N，与标准 GCJ 偏移一致）；真实高德底图上的对齐效果待用户环境复验。
- **等高线与更细间距**：z8 DEM ~610m/px 原生分辨率下 200m 间距已近噪声下限；更细间距需更高分辨率 DEM（未纳入本轮）。
- 远观夸张 4×/2.5× 非真实比例（近观回 1.3）；地形标题不谎称比例，取舍记录于 ADR-0012 D2。

## v0.6.2（2026-10-12）运维加固版

单轮 W17：剩余 6 个 open issue（#3–#8）全部修复 + 用户反馈两条地图体验（2D 聚簇 / 离线 DEM 地形）+ issue #10 以 ADR-0011 设计记录处置。零子代理（全部主代理实现）。

### ✅ 已验证（2026-10-12，linux/amd64 · 2 核沙箱）

| 能力 | 验证方式 | 备注 |
| --- | --- | --- |
| **347 个顶层测试全绿**（= v0.6.1 基线 335 + 12 新增） | `gofmt -l .` 空；`go vet ./...` 0 告警；`go test -count=1 ./...` 21 包全部 ok；`npx tsc -b` 0 错误；`npm run build` ✓ | 新增分项：mapping suggest_headers ×6（issue #4）/ api pagination+health+404 ×4（#7/#8）/ app e2e report-summary + lab-create-db ×2（#6/#8）/ webui DEM ×1；importer 1 个钉旧行为测试改钉新契约（#4 行为变更，唯一断言适配点） |
| Windows 平台修复静态验证（#3/#5） | 代码路径审查 + `runtime.GOOS` 分支编译验证（linux 全绿）；`.exe` 后缀 / tempDirT 宽容清理 / 非法文件名平台分支 | Windows 实机测试不可得（沙箱 linux-only）——三项修复均按 issue 复现的最小根因（exec 后缀 / 句柄释放滞后 / 文件名非法字符）逐一对症，待 Windows 用户复验 |
| report --out stdout 契约（#6） | `TestCLIReportOutStdoutSummary`：json/html 双格式，stdout 解析为单 JSON 对象（file/format/scope/totalResources/qualityScore/issuesBySeverity），产物文件非空 | 真实子进程 e2e（TestMain 构建的二进制） |
| 分页双接口 + 负值校验（#7） | `TestPaginationLimitOffsetValidation` 8 例（负/零/非数值/混用 → 400 带范围说明）+ `TestPaginationLimitOffsetSemantics` 3 例（limit+offset=page3 同 page/pageSize 逐行 id 相等、99999 钳 100） | 全部列表端点共享 parsePagination 一体获得 |
| 跨库错配可见性（#8） | `TestHealthCarriesDBPath`（dbPath 绝对路径）+ `TestWorkspace404HintsCrossDB`（404 信封含 cross-database 提示与绑定库路径）+ `TestCLILabCreateCarriesDB`（create 输出 db = 绝对路径）+ serve 启动日志绝对路径 | 「连错库 vs 数据丢失」一步可辨 |
| **2D 聚簇（用户反馈）** | 浏览器级：`scripts/w17_browser_regression.sh` A1/A2/B1/B2 PASS——初始全省视野 2 簇（哈尔滨 3 + 大兴安岭 2）+ 4 单例；点击簇 zoom 4.60→6.80、簇 2→1 拆分；VLM 复核截图确认标签黑块消失 | 贪心 leader 聚簇纯函数（geoCluster.ts）+ move/zoom rAF 节流重算 |
| **离线 DEM 地形（用户反馈）** | 同脚本 C/D/E PASS——3D `getTerrain().source='dem'`、hillshade 可见、页面实际拉取 76+ 张 `/mapdata/dem/*.png`（同源不受外网封禁）、图层面板开关 → terrain null / hillshade none；VLM 复核 3D 截图确认地形起伏与山体阴影可见；webui 瓦片 MIME/缓存/PNG 磁头测试 | 数据：AWS Terrain Tiles（Mapzen，terrarium）黑龙江 z4–z8 共 97 瓦片 6.4MB go:embed 内嵌；z8 ≈300m/px，更高 zoom 父级上采样 |
| 浏览器级回归 | w17 脚本 11/11 PASS、页面错误 0、console 干净 | 截图存 repo `tmp/`（用户要求：截屏不作为交付物发送） |
| 版本事实源 | `domain.AppVersion = v0.6.2`；CHANGELOG 头 0.6.2；`TestAppVersionMatchesChangelogHead` 实跑 PASS；i18n APP_VERSION / README MCP version / api.md 基线三触点同步 v0.6.2 | 版本矩阵四处齐（W16-Q P2 教训延续） |
| issue 处置 | #3 #4 #5 #6 #7 #8 修复关闭（评论附验收证据）；#10 以 ADR-0011 设计记录关闭 | 全部 11 个 open issue 清零 |

### ⚠️ 未验证（v0.6.2 诚实声明）

- **Windows 实机**：#3/#5 的修复在 linux 沙箱只能做编译与路径静态验证，Windows 实机 `go test ./...` 待用户环境复验（issue 评论已注明）。
- **高德在线瓦片**：沿用 v0.6.1 已知限制——沙箱页面世界封禁 autonavi，真实浏览器不受影响（W16 已多路取证）。
- **DEM 地形与 fill-extrusion 叠加的极端场景**（俯仰 80°+ 山区近观）：MapLibre 6.13 组合渲染本轮以 pitch 58 + 省级/城市级视野验证；更深俯仰未纳入断言。
- issue #10 的四个能力（时序/检测/告警/预测）**未实现**——ADR-0011 仅设计记录，验收标准（明确优先级与数据模型）以此满足。

## v0.6.1（2026-10-12）地图修复版

单轮 W16-D + W16-Q 独立核验（PASS-with-findings，P2×1 已修复）：issue #1/#2/#9/#11 四条地图缺陷。335 个顶层测试全绿（= 331 + webui 4）；tsc/build 过；浏览器回归 15/15；画布 756×544 实测；版本矩阵四处齐。详见 CHANGELOG 0.6.1 段与 worklog W16-D/W16-Q 小节。

## v0.6.0（2026-10-11）治理三部曲终章与数据双向闭环版

五轮迭代（W11 治理债务收口轮 + W12-W14 三轮功能开发 + W15-D 发布轮），主代理逐轮派发开发子代理并派 W11-Q/W12-Q/W13-Q/W14-Q 只读质检（四轮均 **PASS**：0 P0/P1/P2，P3 分别 4/3/3/3——均为记录精度与策略延续项；「api.md 契约基线 v0.5.0 与 v0.6 标注并存」P3 已由本轮 W15-D 收口）；里程碑提交 14bf12e（W11）/ d34a68b（W12）/ 6fa5de9（W13）/ 967b5d4（W14）+ 本轮发布提交。以下为本轮（W15-D）实测；W11-W14 各轮的独立实测记录见 worklog 各 Task ID 小节。

### ✅ 已验证（2026-10-11，linux/amd64 · 2 核沙箱）

| 能力 | 验证方式 | 备注 |
| --- | --- | --- |
| **331 个顶层测试全绿** | `gofmt -l .` 空；`go vet ./...` 0 告警；`go test -count=1 -timeout 15m ./...` 20 个含测试的包全部 ok（日志显式 grep FAIL=0）；`-race -count=1` changes/repo/api/governance/exporter/app 六包 0 race（7.7s/29.0s/16.3s/2.5s/1.4s/4.9s）；`npx tsc -b` 0 错误；`npm run build` ✓ 5.08s | 计数口径：`go test -list '.*' ./... \| grep -c '^Test'` = **331** = v0.5.0 基线 296 + W11 +0 / W12 +13 / W13 +9 / W14 +13 = +35（W12 分项 exporter 9 / api 2 / app 2；W13 分项 changes 6 / repo 1 / api 1 / app 1 / mapping +1 与 importer −1（TestRelationTypeFor 迁移净零）；W14 分项 governance 4 / repo 4 / api 2 / app 3；W15 发布轮零新增测试函数——版本断言随发布适配 + W13-Q P3-2 电路双端 List 预览断言补入既有函数；既有断言零放宽，唯一例外 = TestAppVersionP2 版本断言随发布适配，测试内注释声明） |
| 版本事实源 | `domain.AppVersion = v0.6.0`；`TestAppVersionP2` + `TestAppVersionMatchesChangelogHead` 双锚定实跑 PASS；live serve `/api/health` 实报 v0.6.0；api.md 契约基线、README、i18n.ts APP_VERSION、teleagent-integration 两处同步；MCP 真进程 stdio 握手 serverInfo v0.6.0 | 版本事实点本轮改动 13 处（7 文件：domain 常量+注释 2 / sim_test 断言 3 行 / i18n 1 / api.md 基线行+health 示例 2 / README 版本行+MCP version+英文摘要 3 / teleagent 2；另 CHANGELOG 头新增 0.6.0 段与 verified 日期行为本轮 A/B 交付件）；api.md 及全仓 v0.6 W12-D/W13-D/W14-D 能力标注统一为 v0.6.0 口径（25 处、14 文件：api.md 5 / README 3 / data-model 3 / import-and-validation 3 / natural-keys 1 / Go 注释 6 / web 注释 4——收口 W12-Q/W13-Q/W14-Q 基线并存 P3）；全仓 `v0.5.0` 残留 13 处（CHANGELOG 6 / verified 7）逐处判定为合法历史段/基线引用/发布轮行动描述，零违规 |
| 发布验收（W15-D live serve，HTTP 层） | 28 项断言全 PASS：`/api/health` v0.6.0；`GET /api/schema` 8 类 / 89 字段 / 13 规则（ptp_port sheetHint="PTP" + 复合键 device_model+port_name + 23 字段；ip_pool.vlan_id intMin/Max 1/4094；base_station 兜底键 city+base_station_name；machine_room.site_id refType=site）；`/` 与 SPA 路由（/resources /import /issues /report /lab + 深链 /resources/DEV-001、/lab/:id）全 200；index.html 引用 4 资产 + maplibre 异步 chunk/CSS/worker + favicon 逐个 200；devices.xlsx 完整导入 30 行 27/0/3 + issues 4E/1W/27I 与 v0.5.0 基线一致，再导 0/27/3 纯 skip；PTP 脚本通道 4/0/0（响应携带的 E006×1/I001×27 为全局集合级重算结果，非 PTP 行级问题）；`GET /api/exports/resources` 200 + xlsx 双头 + device sheet 27 行 + ptp sheet 4 行 + 间隔 2s 两次导出字节一致；`GET /api/governance/summary` 200 + no-store + 真数据（31 资源=27 device+4 ptp / 3 completed 批次 / bySeverity E7-W1-I27 / byRule 首位 I001=27） | make build ✓（bin 32,142,704 B）；验收脚本 /tmp（仓库零污染）； governance E7 = devices 行级 4 + 再导行级 3（E001/E002/E004 各 +1） |
| W11-W14 旅程（汇总自各轮独立验证） | W11：target_missing 诚实口径——serve 双负对照（回滚 → deleted.renameProposals=1 → apply **404**；repo 直删新资源 → **409 新文案**）；resource_id 任一侧——四层测试 + 真浏览器双侧抽屉实测（W11-Q）；w7/w8 冒烟重建 SMOKE-OK。W12：回源幂等四层证实（CLI e2e 八类型含 W005/W006 apply 后状态双库 sqlite 级等价、API 同库回导纯 skip 零行级 issue、冒烟 80 资源/60 关系等价、W12-Q 自建脏库独立复现）；字节确定性（同库两次 + CLI/HTTP 跨通道 + 50 次乱序洗牌探针）；键回填三规则负对照（朴素 split → 6 断言红）；E003 部分导出实测（W12-Q）。W13：三分支语义 + 409 `ref_target_missing` 四层自然旅程（回滚目标 → 409 零改动三件套 → 重导 → 同一提案 200）+ 同事务注入回滚（UpdateTarget 故障 → 属性/提案/边全回滚）+ 绝不建边反转负对照 + RelationTypeFor 迁移逐字节零漂移（W13-Q）；w13 冒烟 SMOKE-OK。W14：治理总览 SQL COUNT 六条全下推（注入排序翻转 → 四层红）、空库全零双通道、CLI/HTTP/sqlite 三方逐字段相等（W14-Q 自建脏库）、真浏览器面板渲染+拦截降级+重试恢复；w14 冒烟 SQL oracle 对账 SMOKE-OK | 详见 worklog W11-D…W14-Q 小节与 CHANGELOG 0.6.0；本轮发布验收复验了 schema 契约 / 导入基线 / PTP 通道 / 治理端点 / 导出端点（上两行） |
| 冒烟与演示脚本（全部复跑） | w6/w7/w8/w12/w13/w14 六冒烟全 SMOKE-OK（发布二进制驱动）；demo.sh 64 资源/57 关系/E13/W3/I1/评分 32；demo-lab.sh 九步全过，物化/撤销/恢复哈希 aea27501fa59… 与历史基线一致 | 均为本轮 make build 产物 |
| MCP 真进程 stdio | 握手 serverInfo netops-lab/**v0.6.0**；tools/list 恰 5（resource_search + 4 lab 工具）；tools/call resource_search DEV-001 → total 1 + source{devices.xlsx/设备清单/row 2}；未知工具 → JSON-RPC -32602 | MCP 工具面冻结红线未破（v0.4.0 起恒 5） |

### ⚠️ CI 运行态（已补记——三绿两受阻，账号 Actions 分钟配额耗尽）

- **W11-W13 三提交 CI 全绿**：14bf12e → run **38151970576** ✅（go+web 双 job）；d34a68b → run **38154941722** ✅；6fa5de9 → run **38158064643** ✅——与 v0.5.0 同一工作流同一 runner 镜像，证明管线本身健康。
- **W14/W15 两提交的 run 在 runner 启动层即失败**：967b5d4 → run 38160958085、9b989ee（release/tag v0.6.0）→ run 38163481738——两 job 均 **0 steps、无日志（BlobNotFound）**、4-5 秒内 conclusion=failure，rerun（attempt 2）同样失败。**非代码原因**：账号为 free 计划（私有仓库 2,000 分钟/月），账号级 Actions 用量 10 月已达 **5,876 分钟**（`/users/mmqz/settings/billing/usage` 实查），配额耗尽后私有仓库工作流无法启动 runner——GitHub 状态页 Actions 服务 operational、工作流文件与绿提交逐字节无差异、本地六门禁全绿（每轮 worklog）共同佐证。
- **恢复路径**：配额重置或升级计划后对两 run 重跑（`actions/runs/{id}/rerun`）即可；W11-W13 同管线全绿 + 本地全门禁绿是代码侧的先行证据。历史基线：v0.5.0 release 0e0ebe8 → run 38126225593 全绿；v0.4.0 release d71874d → run 38072980042 全绿。

## ⚠️ 未验证（v0.6.0 诚实声明，沿 v0.5.0 清单）

- 部署态 TeleAgent 的 MCP 调用（环境无 TeleAgent 实例；进程内回环 + 真子进程 stdio 冒烟已验证，含 5 工具握手）。
- Windows / macOS 构建（仅 linux/amd64 实际产出）。
- 长时运行 / 并发负载（无 7×24 或压测数据；GC 语义按单进程 serve 设计）。
- 浏览器级对 /api/schema 的消费 UI（前端镜像未切换——按发布决策「稳定性优先」，schema.ts 注释已注明可经端点校验）。
- **本轮（W15-D）发布验收为 HTTP 层**（SPA 路由/资产 200 + API 契约 + 导出字节确定性）：未重做 v0.4.0 轮的浏览器级交互回归；v0.6.0 前端增量（W12 导出按钮 / W13 重挂提示与 toast / W14 治理面板）经 tsc/build 门禁与逻辑审查，其中导出按钮（W12-Q 真浏览器点击）与治理面板（W14-Q 真浏览器渲染/降级/重试恢复）曾在各轮 Q 实测，本轮未重复。

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

### ✅ CI 运行态（已补记）

- 本轮发布提交的 GitHub Actions 运行态：release 0e0ebe8（v0.5.0，tag v0.5.0）→ **run 38126225593 全绿**；里程碑四提交 CI 全绿（d5e940f→38115053488 / 10b140b→38116649543 / 95727da→38120135549 / 15800f9→38122612024）。历史：v0.4.0 release d71874d → run 38072980042 全绿；v0.3.2 提交 84294e3 → run 38056776554 全绿。

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
