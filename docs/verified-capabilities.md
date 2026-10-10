# NetOps Lab — 已验证 / 未验证能力清单

> 诚实为本：每一条"已验证"都可追溯到具体的验证方式与日期；未验证事项显式列出，不粉饰。
> 验证环境：linux/amd64（2 核沙箱），Go 1.27.2，Node 24.21.0 / npm 11.19.0。验证日期：2026-10-09（v0.1.0/v0.2.0）· 2026-10-10（v0.2.1 打磨版 · v0.2.2 画布多模式版）。

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
