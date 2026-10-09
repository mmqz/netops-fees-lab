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

> CI（`.github/workflows/ci.yml`）首跑将在下次 push 触发，状态待定——本文不声称它已通过。
